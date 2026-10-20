package core

import "fmt"

// ExecutionRef 标识一次运行所属的会话与 Run。
//
// 会话身份一律显式传参，不使用 context.Value 承载业务身份；
// context.Context 只承担取消与超时。
type ExecutionRef struct {
	SessionID string
	RunID     string
}

// RunStatus 是一次运行的结果状态：1 个非终态 + 4 个互斥终态。
type RunStatus string

const (
	// RunStatusRunning 表示运行中，是唯一不带错误的非终态。
	RunStatusRunning RunStatus = "RUNNING"
	// RunStatusCompleted 表示正常完成（不表示任何持久化已发生）。
	RunStatusCompleted RunStatus = "COMPLETED"
	// RunStatusFailed 表示失败（Provider / 不可恢复工具错误 / 内部错误 / 契约错误）。
	RunStatusFailed RunStatus = "FAILED"
	// RunStatusLimitExceeded 表示预算耗尽。
	RunStatusLimitExceeded RunStatus = "LIMIT_EXCEEDED"
	// RunStatusCancelled 表示用户取消或整体超时。
	RunStatusCancelled RunStatus = "CANCELLED"
)

// IsTerminal 判断状态是否为终态；RUNNING 与未知取值都不是终态。
func IsTerminal(s RunStatus) bool {
	switch s {
	case RunStatusCompleted, RunStatusFailed, RunStatusLimitExceeded, RunStatusCancelled:
		return true
	default:
		return false
	}
}

// RunPolicy 是一次运行的次数预算。
//
// LLM 调用次数与工具调用次数分别计数、分别限制；两者都是必填正数，
// 非正值属于调用方契约错误（invalid_argument），不存在"0 表示不限制"的隐式约定。
type RunPolicy struct {
	MaxLLMCalls  int
	MaxToolCalls int
}

// Validate 校验双预算配置。
func (p RunPolicy) Validate() error {
	if p.MaxLLMCalls <= 0 {
		return NewError(KindInvalidArgument, "core.RunPolicy.Validate", fmt.Sprintf("MaxLLMCalls 必须是大于 0 的整数，当前为 %d", p.MaxLLMCalls))
	}
	if p.MaxToolCalls <= 0 {
		return NewError(KindInvalidArgument, "core.RunPolicy.Validate", fmt.Sprintf("MaxToolCalls 必须是大于 0 的整数，当前为 %d", p.MaxToolCalls))
	}
	return nil
}

// RunResult 是一次运行的结论：状态、实际计数、消息序列与失败原因。
type RunResult struct {
	ExecutionRef
	Status    RunStatus
	LLMCalls  int
	ToolCalls int
	Messages  []Message
	Error     *RunError
}

// Validate 逐状态校验运行结论的一致性（INV-003 / INV-004）。
//
//   - RUNNING 与 COMPLETED 必须不带错误；
//   - FAILED 必须带 Kind ∈ {provider_failure, tool_failure, internal, invalid_argument, invalid_pairing}，
//     其中 tool_failure 必须带 CallID 定位到具体调用；
//   - LIMIT_EXCEEDED 必须带 Kind ∈ {llm_call_limit, tool_call_limit}；
//   - CANCELLED 必须带 Kind ∈ {cancelled, deadline_exceeded}；
//   - 计数值不得为负。
//
// 不采用"所有终态都必须有错误"的笼统规则：COMPLETED 恰恰要求无错误。
func (r RunResult) Validate() error {
	if r.Status != RunStatusRunning && !IsTerminal(r.Status) {
		return NewError(KindInvalidArgument, "core.RunResult.Validate", fmt.Sprintf("未知运行状态 %q", r.Status))
	}
	if r.LLMCalls < 0 {
		return NewError(KindInvalidArgument, "core.RunResult.Validate", fmt.Sprintf("LLMCalls 不能为负数，当前为 %d", r.LLMCalls))
	}
	if r.ToolCalls < 0 {
		return NewError(KindInvalidArgument, "core.RunResult.Validate", fmt.Sprintf("ToolCalls 不能为负数，当前为 %d", r.ToolCalls))
	}

	switch r.Status {
	case RunStatusRunning, RunStatusCompleted:
		if r.Error != nil {
			return NewError(KindInvalidArgument, "core.RunResult.Validate", fmt.Sprintf("%s 状态不允许携带错误（实际错误种类 %s）", r.Status, KindOf(r.Error)))
		}
		return nil
	}

	if r.Error == nil {
		return NewError(KindInvalidArgument, "core.RunResult.Validate", fmt.Sprintf("%s 状态必须携带错误", r.Status))
	}

	kind := KindOf(r.Error)
	switch r.Status {
	case RunStatusFailed:
		if !isFailureKind(kind) {
			return &RunError{
				Kind:    KindInvalidArgument,
				Op:      "core.RunResult.Validate",
				Message: fmt.Sprintf("FAILED 状态不接受错误种类 %s", kind),
				CallID:  r.Error.CallID,
			}
		}
		if kind == KindToolFailure && r.Error.CallID == "" {
			return NewError(KindInvalidArgument, "core.RunResult.Validate", "tool_failure 必须携带 CallID 以定位具体工具调用")
		}
	case RunStatusLimitExceeded:
		if kind != KindLLMCallLimit && kind != KindToolCallLimit {
			return &RunError{
				Kind:    KindInvalidArgument,
				Op:      "core.RunResult.Validate",
				Message: fmt.Sprintf("LIMIT_EXCEEDED 状态不接受错误种类 %s", kind),
			}
		}
	case RunStatusCancelled:
		if kind != KindCancelled && kind != KindDeadlineExceeded {
			return &RunError{
				Kind:    KindInvalidArgument,
				Op:      "core.RunResult.Validate",
				Message: fmt.Sprintf("CANCELLED 状态不接受错误种类 %s", kind),
			}
		}
	}

	if got := StatusOf(r.Error); got != r.Status {
		return &RunError{
			Kind:    KindInvalidArgument,
			Op:      "core.RunResult.Validate",
			Message: fmt.Sprintf("状态 %s 与错误种类 %s 推导出的状态 %s 不一致", r.Status, kind, got),
			CallID:  r.Error.CallID,
		}
	}

	return nil
}

// isFailureKind 判断错误种类是否可用于 FAILED 终态。
func isFailureKind(kind ErrorKind) bool {
	switch kind {
	case KindProviderFailure, KindToolFailure, KindInternal, KindInvalidArgument, KindInvalidPairing:
		return true
	default:
		return false
	}
}
