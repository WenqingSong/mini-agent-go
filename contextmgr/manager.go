package contextmgr

import (
	"context"
	"fmt"
	"strings"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// opBuild 是本包构建错误的统一 Op 定位。
const opBuild = "contextmgr.Manager.Build"

// 视图中 system / assistant / tool 角色与 text 内容类型的字面量：
// core 未导出对应常量，包外只能使用字面量；取值错误会被 core.ValidateMessages 拒绝。
const (
	roleSystem    = core.Role("system")
	roleAssistant = core.Role("assistant")
	roleTool      = core.Role("tool")
	textType      = core.ContentType("text")
)

// manager 是 Manager 的确定性实现：估算、窗口选择与压缩全部基于注入的
// Estimator 与 Summarizer，不引入真实 tokenizer 或真实模型调用。
type manager struct {
	estimator  Estimator
	summarizer Summarizer
}

// NewManager 构造 Manager。estimator 与 summarizer 必须非 nil，否则调用方契约不成立。
func NewManager(estimator Estimator, summarizer Summarizer) (Manager, error) {
	if estimator == nil {
		return nil, core.NewError(core.KindInvalidArgument, "contextmgr.NewManager", "estimator 不能为 nil")
	}
	if summarizer == nil {
		return nil, core.NewError(core.KindInvalidArgument, "contextmgr.NewManager", "summarizer 不能为 nil")
	}
	return &manager{estimator: estimator, summarizer: summarizer}, nil
}

// Build 构建一次 LLM 调用所需的消息视图。
//
// 成功时 Result.Messages 必过 core.ValidateMessages，且按注入估算器求和不超 TokenBudget
// （TokenBudget <= 0 表示不限制）；压缩只覆盖 req.Messages 的严格前缀。任何错误路径返回
// 零值 Result，不返回超预算或配对非法的部分视图，调用方不得据此修改持久化状态。
func (m *manager) Build(ctx context.Context, req Request) (Result, error) {
	if err := validateRequest(req); err != nil {
		return Result{}, err
	}
	if err := contextError(ctx); err != nil {
		return Result{}, err
	}

	// 完整输入视图：System Prompt 消息 + 历史摘要消息 + 全部历史消息。
	systemMsg, hasSystem := systemTextMessage(req.SystemPrompt)
	previousSummaryMsg, hasPreviousSummary := systemTextMessage(req.PreviousSummary)
	view := make([]core.Message, 0, len(req.Messages)+2)
	if hasSystem {
		view = append(view, systemMsg)
	}
	if hasPreviousSummary {
		view = append(view, previousSummaryMsg)
	}
	view = append(view, req.Messages...)

	total, err := m.estimateView(view)
	if err != nil {
		return Result{}, err
	}
	if req.TokenBudget <= 0 || total <= req.TokenBudget {
		// 未超预算：原样返回，不做任何压缩。
		return Result{Messages: view}, nil
	}

	// 已超预算：System Prompt 永不压缩，其估算值单独超预算时预算不可满足。
	if hasSystem {
		systemTokens, err := m.estimateMessage(systemMsg)
		if err != nil {
			return Result{}, err
		}
		if systemTokens > req.TokenBudget {
			return Result{}, core.NewError(core.KindInvalidArgument, opBuild,
				"TokenBudget 无法满足：System Prompt 的估算值单独超出预算")
		}
	}

	// 划分为原子单元，按 KeepRecent 取最小保留尾部；尾部之外没有更早单元时明确失败。
	units := splitUnits(req.Messages)
	tailStart := tailUnitIndex(units, req.KeepRecent)
	if tailStart <= 0 {
		if len(units) == 0 {
			// 只有 System Prompt 与历史摘要，两者都不可压缩。
			return Result{}, core.NewError(core.KindInvalidArgument, opBuild,
				"TokenBudget 无法满足：没有可压缩的历史消息，System Prompt 与历史摘要合计超出预算")
		}
		return Result{}, core.NewError(core.KindInvalidArgument, opBuild,
			fmt.Sprintf("TokenBudget 无法满足：KeepRecent=%d 的保留目标下没有可压缩的更早历史", req.KeepRecent))
	}

	// 逐单元吞并：每轮把保留尾部中最老的一个 Unit 并入压缩集合，直到视图满足预算。
	// Summarizer 调用上限为 unitCount + 1；每轮至少并入一个 Unit，保留条数严格减少，
	// 不可能无限循环。
	maxSummarizeCalls := len(units) + 1
	summarizeCalls := 0
	for compactEnd := tailStart; ; compactEnd++ {
		if summarizeCalls >= maxSummarizeCalls {
			return Result{}, core.NewError(core.KindInternal, opBuild,
				"压缩无法取得有效进展：Summarizer 调用次数超过单次 Build 上限")
		}
		if err := contextError(ctx); err != nil {
			return Result{}, err
		}

		summary, err := m.summarizer.Summarize(ctx, SummarizeRequest{
			ExecutionRef:    req.ExecutionRef,
			PreviousSummary: req.PreviousSummary,
			Messages:        concatUnits(units[:compactEnd]),
		})
		summarizeCalls++
		if err != nil {
			return Result{}, &core.RunError{
				Kind:    core.KindInternal,
				Op:      opBuild,
				Message: "Summarizer 压缩历史失败",
				Cause:   err,
			}
		}
		if strings.TrimSpace(summary) == "" {
			return Result{}, core.NewError(core.KindInternal, opBuild,
				"Summarizer 返回空摘要或全空白摘要，不得以占位文本冒充完整记忆")
		}

		newSummaryMsg, _ := systemTextMessage(summary)
		summaryTokens, err := m.estimateMessage(newSummaryMsg)
		if err != nil {
			return Result{}, err
		}
		if summaryTokens > req.TokenBudget {
			// 摘要单独即超预算：任何包含它的视图都不可能满足预算。
			return Result{}, core.NewError(core.KindInvalidArgument, opBuild,
				"TokenBudget 无法满足：摘要本身的估算值超出预算")
		}

		// 组装候选视图：System Prompt + 新摘要 + 保留尾部，旧摘要由新摘要整体替换。
		candidate := make([]core.Message, 0, len(units)-compactEnd+2)
		if hasSystem {
			candidate = append(candidate, systemMsg)
		}
		candidate = append(candidate, newSummaryMsg)
		candidate = append(candidate, concatUnits(units[compactEnd:])...)

		estimate, err := m.estimateView(candidate)
		if err != nil {
			return Result{}, err
		}
		if estimate <= req.TokenBudget {
			// 输出前自检：压缩结果必须仍是合法配对序列。
			if err := core.ValidateMessages(candidate); err != nil {
				return Result{}, &core.RunError{
					Kind:    core.KindInternal,
					Op:      opBuild,
					Message: "压缩结果未通过 core.ValidateMessages 自检",
					Cause:   err,
				}
			}
			return Result{
				Messages:        candidate,
				Compacted:       true,
				DroppedMessages: countUnitMessages(units[:compactEnd]),
				Summary:         summary,
			}, nil
		}

		if compactEnd >= len(units) {
			// 保留历史已全部压缩，视图仍超预算：无法取得进一步进展。
			return Result{}, core.NewError(core.KindInternal, opBuild,
				"压缩无法取得有效进展：保留历史已全部压缩，视图仍超预算")
		}
	}
}

// validateRequest 校验调用方契约：KeepRecent 非负、Messages 不含 system 角色、
// 消息结构与配对合法。配对/结构错误原样返回 core 错误，保持 Kind 与 CallID 不变。
func validateRequest(req Request) error {
	if req.KeepRecent < 0 {
		return core.NewError(core.KindInvalidArgument, opBuild,
			fmt.Sprintf("KeepRecent 不能为负数，当前为 %d", req.KeepRecent))
	}
	for i, msg := range req.Messages {
		if msg.Role == roleSystem {
			return core.NewError(core.KindInvalidArgument, opBuild,
				fmt.Sprintf("消息[%d] 不允许为 system 角色：System Prompt 必须通过 Request.SystemPrompt 提供", i))
		}
	}
	return core.ValidateMessages(req.Messages)
}

// contextError 把 ctx 的取消/超时映射为对应 Kind，不透传为 internal。
func contextError(ctx context.Context) error {
	switch ctx.Err() {
	case context.Canceled:
		return core.NewError(core.KindCancelled, opBuild, "上下文已取消")
	case context.DeadlineExceeded:
		return core.NewError(core.KindDeadlineExceeded, opBuild, "上下文已超时")
	default:
		return nil
	}
}

// systemTextMessage 把一段纯文本渲染为 system 消息（ID 与 Raw 为空）。
// System Prompt 与摘要都通过它进入视图：前者固定为首条，后者紧随其后，均永不压缩。
// 文本为空时不产生消息。
func systemTextMessage(text string) (core.Message, bool) {
	if text == "" {
		return core.Message{}, false
	}
	return core.Message{
		Role:    roleSystem,
		Content: []core.ContentPart{{Type: textType, Text: text}},
	}, true
}

// unit 是一个原子单元：要么是单条独立消息，要么是「携带 ToolCalls 的 assistant 消息
// + 应答其全部调用的 tool 消息（含其间被吞并的消息）」。压缩只能整单元取舍，
// 因此不会产生孤儿 Result、缺失 Result 或重复 Result。
type unit struct {
	msgs []core.Message
}

// splitUnits 把消息序列划分为原子单元。
//
// 输入必须先通过 core.ValidateMessages：配对校验保证每个调用恰好有一个后续结果，
// 因此从携带调用的 assistant 消息起向前吞并，直到其全部调用（含吞并的 assistant
// 消息新产生的调用）都已应答；得到的单元内部自洽，跨单元不会截断配对。
func splitUnits(msgs []core.Message) []unit {
	units := make([]unit, 0, len(msgs))
	for i := 0; i < len(msgs); {
		end := i
		if isAssistantWithCalls(msgs[i]) {
			pending := make(map[string]struct{}, len(msgs[i].ToolCalls))
			for _, call := range msgs[i].ToolCalls {
				pending[call.ID] = struct{}{}
			}
			for end+1 < len(msgs) && len(pending) > 0 {
				end++
				switch {
				case msgs[end].Role == roleTool:
					if msgs[end].ToolResult != nil {
						delete(pending, msgs[end].ToolResult.CallID)
					}
				case isAssistantWithCalls(msgs[end]):
					// 吞并的 assistant 消息若携带新调用，同样必须在本单元内应答。
					for _, call := range msgs[end].ToolCalls {
						pending[call.ID] = struct{}{}
					}
				}
			}
		}
		units = append(units, unit{msgs: msgs[i : end+1]})
		i = end + 1
	}
	return units
}

// isAssistantWithCalls 判断消息是否为携带 ToolCalls 的 assistant 消息。
func isAssistantWithCalls(msg core.Message) bool {
	return msg.Role == roleAssistant && len(msg.ToolCalls) > 0
}

// tailUnitIndex 返回保留尾部首个单元的下标：从尾部按单元累加消息条数，取满足
// 「条数 >= keepRecent」的最小尾部；keepRecent <= 0 时尾部为空（可全部压缩）。
// 返回 0 表示全部单元都只能作为尾部（没有可压缩的更早历史）。
func tailUnitIndex(units []unit, keepRecent int) int {
	if keepRecent <= 0 {
		return len(units)
	}
	count := 0
	for i := len(units) - 1; i >= 0; i-- {
		count += len(units[i].msgs)
		if count >= keepRecent {
			return i
		}
	}
	return 0
}

// concatUnits 把若干单元的消息按原顺序拼接为独立切片（不共享输入底层数组）。
func concatUnits(units []unit) []core.Message {
	out := make([]core.Message, 0, countUnitMessages(units))
	for _, u := range units {
		out = append(out, u.msgs...)
	}
	return out
}

// countUnitMessages 统计若干单元包含的消息条数。
func countUnitMessages(units []unit) int {
	count := 0
	for _, u := range units {
		count += len(u.msgs)
	}
	return count
}

// estimateMessage 估算单条消息；估算器返回负值视为调用方契约错误。
func (m *manager) estimateMessage(msg core.Message) (int, error) {
	tokens := m.estimator.EstimateMessage(msg)
	if tokens < 0 {
		return 0, core.NewError(core.KindInvalidArgument, opBuild,
			fmt.Sprintf("估算器对消息返回负值 %d，无法进行预算判断", tokens))
	}
	return tokens, nil
}

// estimateView 估算整个视图的 token 总量。
func (m *manager) estimateView(msgs []core.Message) (int, error) {
	total := 0
	for _, msg := range msgs {
		tokens, err := m.estimateMessage(msg)
		if err != nil {
			return 0, err
		}
		total += tokens
	}
	return total, nil
}
