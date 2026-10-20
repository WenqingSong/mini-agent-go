package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// opLoop 是循环内部错误的定位前缀：除执行器自身的错误外，循环判定的失败都由它标记。
const opLoop = "agent.Loop.loop"

// 契约层枚举字面量：core / llm 未导出 Role / FinishReason 常量（Contract 禁止新增公共符号），
// 包外只能做字面量转换；取值错误会被 core.ValidateMessages / llm.Response.Validate 拒绝。
const (
	roleSystem = core.Role("system")
	roleTool   = core.Role("tool")

	finishReasonStop      = llm.FinishReason("stop")
	finishReasonToolCalls = llm.FinishReason("tool_calls")
	finishReasonLength    = llm.FinishReason("length")
	finishReasonUnknown   = llm.FinishReason("unknown")
)

// runState 是一次 Run 的执行期状态：实际计数、最后使用的 step 与本 Run 产出的消息。
//
// messages 是本 Run 的完整事实记录（含被压缩覆盖、含未提交的消息），与 Session 提交解耦；
// 失败时允许以「携带 ToolCalls 但缺少结果」的 assistant 消息结尾（Design §7 提交边界）。
type runState struct {
	llmCalls  int
	toolCalls int
	step      int
	messages  []core.Message
}

// loop 是循环主体：整个 Run 期间持有会话执行权，成功返回即表示本 Turn 可以提交。
//
// 顺序即语义：ctx 终态 → LLM 预算 → 上下文构建 → 模型调用 → D5 结束判定 →
// 工具预算整批预检 → 按数组序串行执行工具 → 回到下一 step；退出后一次性提交。
func (l *Loop) loop(ctx context.Context, turn *session.Turn, req RunRequest, state *runState) error {
	// 每次 Run 独立执行器：去重占位按 (RunID, CallID) 隔离，无跨 Run 命中与内存增长。
	executor, err := tool.NewExecutor(tool.ExecutorOptions{
		Registry: l.cfg.Registry,
		Timeout:  l.cfg.ToolTimeout,
	})
	if err != nil {
		return asRunError(err)
	}

	snapshot := turn.Session()
	history := snapshot.Messages
	baseLen := len(history)
	// head 是「已被摘要覆盖的前缀长度」，只增不减；Build 的输入始终从 history[head:] 开始，
	// 因此同一段历史不会被重复摘要。
	head := 0
	summary := snapshot.Summary
	compacted := false
	// seen 只用于 D3 的执行尝试计数口径与前置重复拒绝；执行器去重仍是最后防线。
	seen := make(map[string]struct{})

	for step := 1; ; step++ {
		state.step = step
		if err := contextError(ctx, opLoop); err != nil {
			return err
		}
		// LLM 预算：先检查再发出，第 MaxLLMCalls+1 次调用不会被发出、也不计入。
		if state.llmCalls >= req.Policy.MaxLLMCalls {
			return core.NewError(core.KindLLMCallLimit, opLoop,
				fmt.Sprintf("模型调用预算耗尽：已发起 %d 次，上限 %d 次", state.llmCalls, req.Policy.MaxLLMCalls))
		}

		view, err := l.cfg.Contexts.Build(ctx, contextmgr.Request{
			ExecutionRef:    req.ExecutionRef,
			SystemPrompt:    l.cfg.SystemPrompt,
			PreviousSummary: summary,
			Messages:        history[head:],
			TokenBudget:     l.cfg.TokenBudget,
			KeepRecent:      l.cfg.KeepRecent,
		})
		if err != nil {
			// ctx 终态优先：Build 内部的取消/超时也必须归因为 CANCELLED。
			if ctxErr := contextError(ctx, opLoop); ctxErr != nil {
				return ctxErr
			}
			return asRunError(err)
		}
		if view.Compacted {
			if view.DroppedMessages < 0 || head+view.DroppedMessages > len(history) {
				return core.NewError(core.KindInternal, opLoop,
					fmt.Sprintf("上下文管理器返回的压缩条数越界：已覆盖 %d 条，本次 %d 条，历史共 %d 条",
						head, view.DroppedMessages, len(history)))
			}
			head += view.DroppedMessages
			summary = view.Summary
			compacted = true
		}

		// System Prompt 通过 llm.Request.System 传递；视图中的摘要 system 消息仍保留在 Messages。
		messages, err := stripSystemPrompt(view.Messages, l.cfg.SystemPrompt)
		if err != nil {
			return err
		}

		// 计数口径（INV-003）：通过预算检查后、调用前 +1；Provider 失败的那次也计入。
		state.llmCalls++
		l.emitLLMRequest(ctx, req.ExecutionRef, step, l.cfg.Model, len(messages), view.Compacted, view.DroppedMessages)

		started := time.Now()
		resp, callErr := l.cfg.Provider.Complete(ctx, llm.Request{
			ExecutionRef: req.ExecutionRef,
			Model:        l.cfg.Model,
			System:       l.cfg.SystemPrompt,
			Messages:     messages,
			Tools:        l.cfg.Registry.Specs(),
		})
		duration := time.Since(started)

		var callFailure *core.RunError
		if callErr != nil {
			callFailure = l.classifyProviderError(ctx, callErr)
		}
		l.emitLLMResponse(ctx, req.ExecutionRef, step, resp, callFailure, duration)
		if callFailure != nil {
			return callFailure
		}

		// D5-1：结构校验失败按其返回的 Kind 终止（不执行该响应携带的任何工具）。
		if err := resp.Validate(); err != nil {
			return asRunError(err)
		}

		history = append(history, resp.Message)
		state.messages = append(state.messages, resp.Message)

		calls := resp.Message.ToolCalls
		finish := resp.FinishReason
		// D5-2 / D5-3 / D5-4：三种结束语义都不得 COMPLETED，且只归入 provider_failure。
		switch {
		case finish == finishReasonLength:
			return providerFailure("模型输出被截断（finish_reason=length），不构成完整回答")
		case finish == finishReasonToolCalls && len(calls) == 0:
			return providerFailure("模型声明 finish_reason=tool_calls 但未返回任何工具调用")
		case finish == finishReasonUnknown:
			return providerFailure("模型结束原因未知（finish_reason=unknown）")
		}

		// D5-6：只有「无 ToolCall 且 finish_reason=stop」才是合法最终回答（空文本也合法）。
		if len(calls) == 0 {
			if finish != finishReasonStop {
				// 防御未来新增的结束取值：无法判定为完成时不得 COMPLETED。
				return providerFailure(fmt.Sprintf("模型以未知语义结束且未请求工具（finish_reason=%s）", finish))
			}
			break
		}

		// D3：工具预算整批预检。额度不足时一个都不执行、不伪造结果。
		if state.toolCalls+len(calls) > req.Policy.MaxToolCalls {
			return core.NewError(core.KindToolCallLimit, opLoop,
				fmt.Sprintf("工具调用预算不足：已发起 %d 次，本响应需要 %d 次，上限 %d 次",
					state.toolCalls, len(calls), req.Policy.MaxToolCalls))
		}

		for index, call := range calls {
			if err := contextError(ctx, opLoop); err != nil {
				return err
			}
			// 同一 Run 内重复 CallID：不执行、不计数，并定位到该调用。
			if _, ok := seen[call.ID]; ok {
				return &core.RunError{
					Kind:    core.KindInvalidPairing,
					Op:      opLoop,
					Message: "工具调用 ID 在本次 Run 内重复，拒绝再次执行",
					CallID:  call.ID,
				}
			}
			seen[call.ID] = struct{}{}

			// 计数口径：通过预算与重复检查后、交给执行器前 +1；工具失败/panic 的那次也计入。
			state.toolCalls++
			l.emitToolStarted(ctx, req.ExecutionRef, step, index, call)

			started := time.Now()
			executed, execErr := executor.Execute(ctx, tool.Request{
				ExecutionRef: req.ExecutionRef,
				Call:         call,
			})
			duration := time.Since(started)

			var execFailure *core.RunError
			if execErr != nil {
				execFailure = l.classifyToolError(ctx, execErr)
			}
			l.emitToolFinished(ctx, req.ExecutionRef, step, index, call, executed, execFailure, duration)
			if execFailure != nil {
				// 已执行的 Call 保持已计数、已产生的 RoleTool 结果保留在 RunResult.Messages 中；
				// 不回滚、不补偿、不自动重试。
				return execFailure
			}

			result := toolResultMessage(executed)
			history = append(history, result)
			state.messages = append(state.messages, result)
		}
	}

	// COMPLETED 提交（仍在执行权内）：摘要、前缀删除与新增消息一次提交或整轮回滚。
	if compacted {
		turn.SetSummary(summary)
	}
	if drop := min(head, baseLen); drop > 0 {
		if err := turn.DropLeading(drop); err != nil {
			return asRunError(err)
		}
	}
	// 只追加本 Run 未被压缩覆盖的消息：history[:max(head, baseLen)] 要么已在 Session 中，
	// 要么已被摘要覆盖。
	if err := turn.Append(history[max(head, baseLen):]...); err != nil {
		return asRunError(err)
	}
	return nil
}

// classifyProviderError 归因模型调用失败：ctx 终态优先，其余一律 provider_failure。
func (l *Loop) classifyProviderError(ctx context.Context, err error) *core.RunError {
	if ctxErr := contextError(ctx, opLoop); ctxErr != nil {
		return ctxErr
	}
	return &core.RunError{
		Kind:    core.KindProviderFailure,
		Op:      opLoop,
		Message: "模型调用失败",
		Cause:   err,
	}
}

// classifyToolError 归因工具执行失败：ctx 终态优先，其余沿用执行器给出的 Kind 与 CallID
// （tool_failure 必须带 CallID，由执行器保证）。
func (l *Loop) classifyToolError(ctx context.Context, err error) *core.RunError {
	if ctxErr := contextError(ctx, opLoop); ctxErr != nil {
		return ctxErr
	}
	return asRunError(err)
}

// providerFailure 构造 D5 模型结束语义的失败：只使用 provider_failure，不新增 ErrorKind。
func providerFailure(message string) *core.RunError {
	return core.NewError(core.KindProviderFailure, opLoop, message)
}

// stripSystemPrompt 去掉视图首条 System Prompt 消息：它通过 llm.Request.System 传递，
// 不进入 llm.Request.Messages；摘要 system 消息（紧随其后）保留在 Messages 中。
func stripSystemPrompt(messages []core.Message, systemPrompt string) ([]core.Message, error) {
	if systemPrompt == "" {
		return messages, nil
	}
	if len(messages) == 0 || messages[0].Role != roleSystem || messageText(messages[0]) != systemPrompt {
		return nil, core.NewError(core.KindInternal, opLoop,
			"上下文视图缺少与 SystemPrompt 一致的首条 system 消息")
	}
	return messages[1:], nil
}

// messageText 拼接消息全部文本片段，用于校验 System Prompt 消息身份。
func messageText(msg core.Message) string {
	var b strings.Builder
	for _, part := range msg.Content {
		b.WriteString(part.Text)
	}
	return b.String()
}

// toolResultMessage 把一次工具结果包装为独立的 RoleTool 消息：一条消息只承载一个结果，
// 未执行的 Call 不生成消息（不伪造结果）。
func toolResultMessage(result core.ToolResult) core.Message {
	payload := result
	return core.Message{Role: roleTool, ToolResult: &payload}
}
