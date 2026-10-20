package httpapi

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// SessionStore 是 HTTP 层需要的会话生命周期能力，*session.Manager 结构匹配。
//
// Get 只读、不续期 TTL，因此「chat 前预检」不会延长会话寿命；
// HTML 层不持有会话状态副本，也不回读消息生成响应。
type SessionStore interface {
	Create(ctx context.Context, id string) (session.Session, error)
	Get(ctx context.Context, id string) (session.Session, error)
}

// Runner 执行一次 Agent Run，由装配层（service）实现。
//
// 返回的 error 只表达「未形成 Run 结论」的会话或基础设施失败（会话哨兵、ctx 终态）；
// Run 已形成结论时必须通过 RunOutcome 返回，成功与失败都不使用 error。
type Runner interface {
	Run(ctx context.Context, req RunRequest) (RunOutcome, error)
}

// RunRequest 是一次 chat 请求对应的 Run 身份与用户消息。
type RunRequest struct {
	SessionID string
	RunID     string
	Message   string
}

// RunOutcome 是一次 Run 的结论：终态、本轮最终助手文本、调用计数与失败原因。
type RunOutcome struct {
	Status    core.RunStatus
	Reply     string
	LLMCalls  int
	ToolCalls int
	Err       *core.RunError
}

// checkedCode 判断该结论是否可以返回 200；success=false 时给出应写出的协议错误码。
//
// 只有「COMPLETED 且无错误」才算成功（INV-H01）：非终态、状态与错误种类矛盾、
// 计数为负、终态缺少错误都属于自相矛盾结论，一律按 internal 处理，不得 200。
func (o RunOutcome) checkedCode() (code ErrorCode, success bool) {
	if o.LLMCalls < 0 || o.ToolCalls < 0 {
		return CodeInternal, false
	}
	if o.Status == core.RunStatusCompleted {
		if o.Err != nil {
			return CodeInternal, false
		}
		return "", true
	}
	if !core.IsTerminal(o.Status) || o.Err == nil {
		return CodeInternal, false
	}
	if core.StatusOf(o.Err) != o.Status {
		return CodeInternal, false
	}
	return errorCodeOf(o.Err), false
}
