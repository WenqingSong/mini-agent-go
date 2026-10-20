// Package llm 定义一次 LLM 调用的请求/响应契约与 Provider 接口。
//
// 本包只描述"叫一次模型"的形状：不含 HTTP、鉴权、流式、重试、限流与 Token 估算，
// 这些属于具体 Provider 实现任务。本包只依赖标准库与 core。
package llm

import (
	"context"
	"encoding/json"
	"fmt"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// FinishReason 是模型结束一次响应的原因：stop / tool_calls / length / unknown。
type FinishReason string

// 契约层内部使用的枚举字面量；对外只暴露类型本身。
const (
	finishReasonStop      FinishReason = "stop"
	finishReasonToolCalls FinishReason = "tool_calls"
	finishReasonLength    FinishReason = "length"
	finishReasonUnknown   FinishReason = "unknown"

	roleAssistant core.Role = "assistant"
)

// Usage 是一次调用的 token 用量，由 Provider 回填。
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Request 是一次 LLM 调用的输入。
//
// System 是纯文本 system prompt（当前 Provider 共同子集）；Tools 使用 core.ToolSpec，
// 避免 llm 与 tool 两个 L1 包互相依赖。
type Request struct {
	core.ExecutionRef
	Model       string
	System      string
	Messages    []core.Message
	Tools       []core.ToolSpec
	MaxTokens   int
	Temperature *float64
}

// Response 是一次 LLM 调用的输出。Message 的 Role 必须是 assistant，
// Raw 保存 Provider 原始载荷供日志与回放，契约层不解析。
type Response struct {
	Message      core.Message
	FinishReason FinishReason
	Usage        Usage
	Raw          json.RawMessage
}

// Validate 校验 Provider 返回的响应结构：
// 角色必须是 assistant、消息结构合法、FinishReason 属于已定义取值、token 用量非负。
func (r Response) Validate() error {
	if r.Message.Role != roleAssistant {
		return core.NewError(core.KindInvalidArgument, "llm.Response.Validate", fmt.Sprintf("响应消息角色必须是 assistant，实际为 %q", r.Message.Role))
	}
	if err := r.Message.Validate(); err != nil {
		return err
	}
	if !isKnownFinishReason(r.FinishReason) {
		return core.NewError(core.KindInvalidArgument, "llm.Response.Validate", fmt.Sprintf("未知 FinishReason %q", r.FinishReason))
	}
	if r.Usage.InputTokens < 0 || r.Usage.OutputTokens < 0 {
		return core.NewError(core.KindInvalidArgument, "llm.Response.Validate", fmt.Sprintf("token 用量不能为负数：input=%d output=%d", r.Usage.InputTokens, r.Usage.OutputTokens))
	}
	return nil
}

// isKnownFinishReason 判断 FinishReason 是否属于契约已定义取值。
func isKnownFinishReason(reason FinishReason) bool {
	switch reason {
	case finishReasonStop, finishReasonToolCalls, finishReasonLength, finishReasonUnknown:
		return true
	default:
		return false
	}
}

// Provider 是一次 LLM 调用的抽象。ctx 只承担取消与超时，会话身份由 Request.ExecutionRef 显式携带。
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}
