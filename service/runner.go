// Package service 是 HTTP 端口到 Agent Loop 的装配层。
//
// 职责（见 docs/design/http-api.md §2.1）：实现 httpapi.Runner，把一轮用户消息与
// Agent 产出放进同一次真实的 session.Manager.RunTurn —— 回调内先 Append 用户消息，
// 再用「直接返回当前 Turn」的 agent.SessionRunner 构造 per-Run Loop。
// 因此一次 chat 恰好一次真实 RunTurn：COMPLETED 整轮原子提交，其余终态整轮丢弃。
//
// 本包不实现 HTTP 协议、会话存储、Provider 与工具；Todo 端口适配、生产 Summarizer
// 与 tokenizer 校准不属于本层（v1 只注册 calculator，TokenBudget 固定 0）。
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cnb.cool/framework-station/mini-agent-go/agent"
	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// opRun 是装配层错误的定位前缀。
const opRun = "service.Runner.Run"

// 默认值：与 core.RunPolicy 的必填正数约束一致。
const (
	// DefaultMaxLLMCalls 是单次 Run 的默认模型调用上限。
	DefaultMaxLLMCalls = 8
	// DefaultMaxToolCalls 是单次 Run 的默认工具调用上限。
	DefaultMaxToolCalls = 8
	// DefaultToolTimeout 是单次工具执行的默认超时。
	DefaultToolTimeout = 30 * time.Second
)

// SessionRunner 是装配层需要的会话执行权能力，*session.Manager 结构匹配。
type SessionRunner interface {
	RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error
}

// Config 是一次 Run 的装配参数。
type Config struct {
	// MaxLLMCalls 是模型调用预算，默认 DefaultMaxLLMCalls，必须为正。
	MaxLLMCalls int
	// MaxToolCalls 是工具调用预算，默认 DefaultMaxToolCalls，必须为正。
	MaxToolCalls int
	// SystemPrompt 是纯文本 system prompt，可为空。
	SystemPrompt string
	// Model 透传 llm.Request.Model，必须非空。
	Model string
	// TokenBudget 是上下文预算；v1 固定为 0（不压缩），非 0 视为配置非法。
	TokenBudget int
	// KeepRecent 是保留条数的硬下限，默认 0，必须 >= 0。
	KeepRecent int
	// ToolTimeout 是工具级超时，默认 DefaultToolTimeout；负值表示不设（透传 agent.Config 语义）。
	ToolTimeout time.Duration
}

// withDefaults 用默认值填充零值字段，返回新配置（不修改接收者）。
func (c Config) withDefaults() Config {
	out := c
	if out.MaxLLMCalls == 0 {
		out.MaxLLMCalls = DefaultMaxLLMCalls
	}
	if out.MaxToolCalls == 0 {
		out.MaxToolCalls = DefaultMaxToolCalls
	}
	if out.ToolTimeout == 0 {
		out.ToolTimeout = DefaultToolTimeout
	}
	return out
}

// validate 校验装配参数，失败时调用方必须放弃启动（fail-closed）。
func (c Config) validate() error {
	if c.MaxLLMCalls <= 0 {
		return fmt.Errorf("service: MaxLLMCalls 必须为正数，当前为 %d", c.MaxLLMCalls)
	}
	if c.MaxToolCalls <= 0 {
		return fmt.Errorf("service: MaxToolCalls 必须为正数，当前为 %d", c.MaxToolCalls)
	}
	if c.Model == "" {
		return errors.New("service: Model 不能为空")
	}
	if c.TokenBudget != 0 {
		return fmt.Errorf("service: v1 禁用上下文压缩，TokenBudget 必须为 0，当前为 %d", c.TokenBudget)
	}
	if c.KeepRecent < 0 {
		return fmt.Errorf("service: KeepRecent 不能为负数，当前为 %d", c.KeepRecent)
	}
	// ToolTimeout 负值表示不设工具级超时；零值已由默认值填充，此处不做额外限制。
	return nil
}

// policy 返回本次 Run 的预算。
func (c Config) policy() core.RunPolicy {
	return core.RunPolicy{MaxLLMCalls: c.MaxLLMCalls, MaxToolCalls: c.MaxToolCalls}
}

// Deps 是装配层的可注入依赖。
type Deps struct {
	// Provider 是一次模型调用的实现，必填。
	Provider llm.Provider
	// Sessions 提供会话执行权与整轮原子提交，必填。
	Sessions SessionRunner
	// Registry 提供工具查找与元数据，必填；v1 只注册 calculator。
	Registry tool.Registry
	// Sink 接收运行事件；nil 时丢弃日志。Sink 失败不影响响应、Run 状态与计数。
	Sink runlog.Sink
	// Estimator 覆盖默认的上下文估算器；nil 时使用 HeuristicEstimator。
	Estimator contextmgr.Estimator
	// Summarizer 覆盖默认的 fail-closed 占位实现；仅供测试注入，生产装配必须留空。
	Summarizer contextmgr.Summarizer
}

// Runner 是 httpapi.Runner 的实现：每次 Run 构造独立 Loop，会话执行权只取一次。
type Runner struct {
	cfg      Config
	deps     Deps
	contexts contextmgr.Manager
}

var _ httpapi.Runner = (*Runner)(nil)

// NewRunner 校验配置与依赖并构造 Runner。
//
// 配置非法（预算非正、Model 为空、TokenBudget != 0、ToolTimeout 非正）或依赖缺失时返回错误，
// 不构造任何可执行对象。
func NewRunner(deps Deps, cfg Config) (*Runner, error) {
	normalized := cfg.withDefaults()
	if err := normalized.validate(); err != nil {
		return nil, err
	}
	if deps.Provider == nil {
		return nil, errors.New("service: Deps.Provider 不能为 nil")
	}
	if deps.Sessions == nil {
		return nil, errors.New("service: Deps.Sessions 不能为 nil")
	}
	if deps.Registry == nil {
		return nil, errors.New("service: Deps.Registry 不能为 nil")
	}
	if deps.Sink == nil {
		deps.Sink = runlog.Discard()
	}

	estimator := deps.Estimator
	if estimator == nil {
		estimator = contextmgr.HeuristicEstimator{}
	}
	summarizer := deps.Summarizer
	if summarizer == nil {
		summarizer = failClosedSummarizer{}
	}
	contexts, err := contextmgr.NewManager(estimator, summarizer)
	if err != nil {
		return nil, err
	}

	return &Runner{cfg: normalized, deps: deps, contexts: contexts}, nil
}

// Run 执行一次 chat 对应的 Agent Run。
//
// 装配语义：外层恰好一次真实 RunTurn → 回调内 Append 用户消息 → per-Run Loop
// 复用当前 Turn → COMPLETED 提交，其余终态由回调返回非 nil 而整轮丢弃。
//
// 返回值语义：Run 已形成结论时通过 RunOutcome 返回（含失败与取消），error 只用于
// 「未形成 Run 结论」的会话/基础设施失败与「结论已完成但提交失败」。
func (r *Runner) Run(ctx context.Context, req httpapi.RunRequest) (httpapi.RunOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	var result core.RunResult
	err := r.deps.Sessions.RunTurn(ctx, req.SessionID, func(turn *session.Turn) error {
		if appendErr := turn.Append(userMessage(req.Message)); appendErr != nil {
			return appendErr
		}
		loop, buildErr := agent.New(r.loopConfig(turn, req.SessionID))
		if buildErr != nil {
			return buildErr
		}
		result = loop.Run(ctx, agent.RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: req.SessionID, RunID: req.RunID},
			Policy:       r.cfg.policy(),
		})
		if result.Status != core.RunStatusCompleted {
			return runErrorOf(result)
		}
		return nil
	})

	if err != nil {
		// 已形成的失败/取消结论比外层错误更精确（错误种类与调用计数只在 RunResult 中）。
		// COMPLETED 但提交失败时不能返回 200：Run 的产出没有落库。
		if result.Status != "" && result.Status != core.RunStatusCompleted {
			return outcomeOf(result), nil
		}
		return httpapi.RunOutcome{}, err
	}
	return outcomeOf(result), nil
}

// loopConfig 构造 per-Run Loop 配置：会话能力指向当前 Turn，其余依赖由装配层透传。
func (r *Runner) loopConfig(turn *session.Turn, sessionID string) agent.Config {
	return agent.Config{
		Provider:     r.deps.Provider,
		Sessions:     &turnSession{sessions: r.deps.Sessions, turn: turn, sessionID: sessionID},
		Registry:     r.deps.Registry,
		Contexts:     r.contexts,
		Sink:         r.deps.Sink,
		SystemPrompt: r.cfg.SystemPrompt,
		Model:        r.cfg.Model,
		TokenBudget:  r.cfg.TokenBudget,
		KeepRecent:   r.cfg.KeepRecent,
		ToolTimeout:  r.cfg.ToolTimeout,
	}
}

// outcomeOf 把 Run 结论映射为 HTTP 端口的结果类型。
func outcomeOf(result core.RunResult) httpapi.RunOutcome {
	return httpapi.RunOutcome{
		Status:    result.Status,
		Reply:     replyText(result.Messages),
		LLMCalls:  result.LLMCalls,
		ToolCalls: result.ToolCalls,
		Err:       result.Error,
	}
}

// runErrorOf 取非 COMPLETED 结论对应的错误；结论缺少错误时按 internal 兜底。
func runErrorOf(result core.RunResult) error {
	if result.Error != nil {
		return result.Error
	}
	return core.NewError(core.KindInternal, opRun, "非 COMPLETED 结论缺少错误")
}

// failClosedSummarizer 是 v1 的 fail-closed 占位实现：被调用即证明装配错误。
//
// v1 固定 TokenBudget = 0，contextmgr.Build 不会触发压缩，因此正常路径永不调用；
// 它不是生产级摘要能力（生产 LLM Summarizer 与 tokenizer 校准属 T08）。
type failClosedSummarizer struct{}

var _ contextmgr.Summarizer = failClosedSummarizer{}

// Summarize 始终返回 internal 错误，不做任何摘要。
func (failClosedSummarizer) Summarize(context.Context, contextmgr.SummarizeRequest) (string, error) {
	return "", core.NewError(core.KindInternal, "service.failClosedSummarizer.Summarize",
		"v1 禁用上下文压缩，不得调用 Summarizer")
}
