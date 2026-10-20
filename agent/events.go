package agent

import (
	"context"
	"encoding/json"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
)

// emit 发出一个运行事件。
//
// 日志是观察者（Design §10 旁路原则）：Sink.Record 的返回值一律忽略——不重试、不降级、
// 不改变 RunResult、计数、消息序列与提交行为。Data 只使用 §10 允许清单内的键，不含正文。
func (l *Loop) emit(ctx context.Context, ev runlog.Event) {
	_ = l.cfg.Sink.Record(ctx, ev)
}

// eventData 把固定字段序列化为事件载荷；字段都是 JSON 基础类型，编码不会失败。
func eventData(fields map[string]any) json.RawMessage {
	raw, err := json.Marshal(fields)
	if err != nil {
		// 理论上不可达：构造失败时不输出载荷，避免写出半截 JSON 影响采集侧解析。
		return nil
	}
	return raw
}

// emitLLMRequest 在发起模型调用之前发出 llm_request。
//
// messageCount 是本次实际交给 Provider 的 Messages 条数（不含通过 Request.System 传递的
// System Prompt）；compacted / droppedMessages 描述本次视图的压缩结果。
func (l *Loop) emitLLMRequest(ctx context.Context, ref core.ExecutionRef, step int, model string, messageCount int, compacted bool, droppedMessages int) {
	l.emit(ctx, runlog.Event{
		Kind: runlog.KindLLMRequest,
		Ref:  ref,
		Step: step,
		Data: eventData(map[string]any{
			"model":            model,
			"message_count":    messageCount,
			"compacted":        compacted,
			"dropped_messages": droppedMessages,
		}),
	})
}

// emitLLMResponse 在模型调用返回后发出 llm_response：失败时携带已归因的错误。
func (l *Loop) emitLLMResponse(ctx context.Context, ref core.ExecutionRef, step int, resp llm.Response, failure *core.RunError, duration time.Duration) {
	l.emit(ctx, runlog.Event{
		Kind:     runlog.KindLLMResponse,
		Ref:      ref,
		Step:     step,
		Error:    failure,
		Duration: duration,
		Data: eventData(map[string]any{
			"finish_reason":   string(resp.FinishReason),
			"tool_call_count": len(resp.Message.ToolCalls),
			"input_tokens":    resp.Usage.InputTokens,
			"output_tokens":   resp.Usage.OutputTokens,
		}),
	})
}

// emitToolStarted 在每次工具执行之前发出 tool_started，CallID 即 ToolCall.ID。
func (l *Loop) emitToolStarted(ctx context.Context, ref core.ExecutionRef, step, index int, call core.ToolCall) {
	l.emit(ctx, runlog.Event{
		Kind:   runlog.KindToolStarted,
		Ref:    ref,
		Step:   step,
		CallID: call.ID,
		Data: eventData(map[string]any{
			"tool_name": call.Name,
			"index":     index,
		}),
	})
}

// emitToolFinished 在每次工具执行返回后（含失败）发出 tool_finished。
//
// is_error 取执行结果的 ToolResult.IsError：可恢复失败同样记为 is_error=true 且不终止 Run；
// 不可恢复失败（panic / 取消）由 failure 携带并保留在错误字段中。
func (l *Loop) emitToolFinished(ctx context.Context, ref core.ExecutionRef, step, index int, call core.ToolCall, result core.ToolResult, failure *core.RunError, duration time.Duration) {
	l.emit(ctx, runlog.Event{
		Kind:     runlog.KindToolFinished,
		Ref:      ref,
		Step:     step,
		CallID:   call.ID,
		Error:    failure,
		Duration: duration,
		Data: eventData(map[string]any{
			"tool_name": call.Name,
			"is_error":  result.IsError,
			"index":     index,
		}),
	})
}
