// Package agent 实现 L3 Agent Loop：把一次 Run 组合成 LLM → Tool → LLM 循环。
//
// 职责边界见 docs/design/runtime-architecture.md §3 / §7 / §9 / §10：
//   - 一次 Run 的全部执行（含所有模型调用与工具执行）在同一次 session.RunTurn 内完成，
//     只有 COMPLETED 才整轮原子提交；失败、超限与取消不留下半截历史；
//   - 每次 Run 创建独立 tool.Executor，去重范围为 RunID + CallID；
//   - 双预算在调用发出前预检，实际发起的次数写入 RunResult；
//   - 六类运行事件按 §10 的发出点接线，Sink 错误不影响结论。
//
// 本包只依赖标准库与 core / llm / tool / session / contextmgr / runlog，
// 不依赖 builtin 与具体 Provider（由服务装配任务接线）。
package agent

import (
	"context"
	"errors"
	"time"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// opRun 是本包对外的统一错误定位前缀。
const opRun = "agent.Loop.Run"

// SessionRunner 是 Loop 需要的唯一会话能力：*session.Manager 结构匹配满足。
//
// 用最小接口而不是具体类型，避免 Loop 直接依赖会话内部实现；
// RunTurn 提供同会话串行执行权与整轮原子提交（Design INV-009 / INV-010）。
type SessionRunner interface {
	RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error
}

// Config 是 Loop 的装配参数。Loop 只持有不可变配置，因此可被多个 Run 并发使用。
type Config struct {
	// Provider 是一次模型调用的实现，必填。
	Provider llm.Provider
	// Sessions 提供会话执行权与原子提交，必填。
	Sessions SessionRunner
	// Registry 提供工具查找与 Request.Tools 元数据，必填。
	Registry tool.Registry
	// Contexts 每次模型调用前装配消息视图，必填。
	Contexts contextmgr.Manager
	// Sink 接收运行事件；nil 时使用 runlog.Discard()，日志失败永不改变 Run 结论。
	Sink runlog.Sink
	// SystemPrompt 是纯文本 system prompt；为空时视图中不含 system prompt 消息。
	SystemPrompt string
	// Model 透传 llm.Request.Model，不做非空校验。
	Model string
	// TokenBudget 透传 contextmgr：<= 0 表示不限制。
	TokenBudget int
	// KeepRecent 透传 contextmgr：保留条数的硬下限，必须 >= 0。
	KeepRecent int
	// ToolTimeout 透传 tool.ExecutorOptions.Timeout：<= 0 表示不设工具级超时。
	ToolTimeout time.Duration
}

// RunRequest 是一次 Run 的身份与预算。
type RunRequest struct {
	core.ExecutionRef
	Policy core.RunPolicy
}

// Loop 是一次装配、多次执行的 Agent 运行器：无可变字段，可被并发调用。
type Loop struct {
	cfg Config
}

// New 构造 Loop。Provider / Sessions / Registry / Contexts 缺失属于调用方契约错误。
func New(cfg Config) (*Loop, error) {
	if cfg.Provider == nil {
		return nil, core.NewError(core.KindInvalidArgument, "agent.New", "Provider 不能为 nil")
	}
	if cfg.Sessions == nil {
		return nil, core.NewError(core.KindInvalidArgument, "agent.New", "Sessions 不能为 nil")
	}
	if cfg.Registry == nil {
		return nil, core.NewError(core.KindInvalidArgument, "agent.New", "Registry 不能为 nil")
	}
	if cfg.Contexts == nil {
		return nil, core.NewError(core.KindInvalidArgument, "agent.New", "Contexts 不能为 nil")
	}
	if cfg.Sink == nil {
		cfg.Sink = runlog.Discard()
	}
	return &Loop{cfg: cfg}, nil
}

// Run 执行一次完整的 Agent Run 并返回运行结论。
//
// 返回值里的 Error 是唯一权威失败语义（不额外返回 error，避免两者互相矛盾），
// 任何路径返回的结论都满足 core.RunResult.Validate() == nil。
//
// 前置契约错误（ctx 为空、SessionID / RunID 为空、预算非法）在取得会话执行权之前
// 直接返回 FAILED + invalid_argument，且不发出运行事件：ExecutionRef 不可信时不构成一次 Run。
func (l *Loop) Run(ctx context.Context, req RunRequest) core.RunResult {
	if err := validateRunRequest(ctx, req); err != nil {
		return core.RunResult{ExecutionRef: req.ExecutionRef, Status: core.RunStatusFailed, Error: err}
	}

	started := time.Now()
	l.emit(ctx, runlog.Event{
		Kind: runlog.KindRunStarted,
		Ref:  req.ExecutionRef,
		Data: eventData(map[string]any{"model": l.cfg.Model}),
	})

	state := &runState{}
	// 整个 Run（含全部分步模型调用与工具执行）在同一次 RunTurn 内完成：
	// 同会话串行、跨会话并发，且提交与回滚由会话层统一裁决。
	err := l.cfg.Sessions.RunTurn(ctx, req.SessionID, func(turn *session.Turn) error {
		return l.loop(ctx, turn, req, state)
	})

	result := core.RunResult{
		ExecutionRef: req.ExecutionRef,
		LLMCalls:     state.llmCalls,
		ToolCalls:    state.toolCalls,
		Messages:     state.messages,
	}
	if err != nil {
		result.Error = asRunError(err)
		result.Status = core.StatusOf(result.Error)
	} else {
		result.Status = core.RunStatusCompleted
	}

	l.emit(ctx, runlog.Event{
		Kind:     runlog.KindRunFinished,
		Ref:      req.ExecutionRef,
		Step:     state.step,
		Status:   result.Status,
		Error:    result.Error,
		Duration: time.Since(started),
	})
	return result
}

// validateRunRequest 校验进入循环前必须成立的调用方契约。
func validateRunRequest(ctx context.Context, req RunRequest) *core.RunError {
	if ctx == nil {
		return core.NewError(core.KindInvalidArgument, opRun, "context 不能为空")
	}
	if req.SessionID == "" {
		return core.NewError(core.KindInvalidArgument, opRun, "SessionID 不能为空")
	}
	if req.RunID == "" {
		return core.NewError(core.KindInvalidArgument, opRun, "RunID 不能为空")
	}
	if err := req.Policy.Validate(); err != nil {
		return asRunError(err)
	}
	return nil
}

// asRunError 把任意错误归一为 *core.RunError：保留错误链中已有的 Kind / Op / CallID，
// 非契约错误包一层 internal 并保留 Cause，避免把无法归类的失败伪装成业务错误。
func asRunError(err error) *core.RunError {
	var runErr *core.RunError
	if errors.As(err, &runErr) && runErr != nil {
		return runErr
	}
	return &core.RunError{
		Kind:    core.KindInternal,
		Op:      opRun,
		Message: "运行失败但不是契约错误",
		Cause:   err,
	}
}

// contextError 把 ctx 终态映射为 cancelled / deadline_exceeded（INV-006：ctx 优先于其它错误来源）。
func contextError(ctx context.Context, op string) *core.RunError {
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &core.RunError{
			Kind:    core.KindDeadlineExceeded,
			Op:      op,
			Message: "运行超出整体期限",
			Cause:   err,
		}
	}
	return &core.RunError{
		Kind:    core.KindCancelled,
		Op:      op,
		Message: "运行被取消",
		Cause:   err,
	}
}
