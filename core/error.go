package core

import (
	"errors"
	"fmt"
	"strings"
)

// ErrorKind 是 Runtime 统一错误种类。
//
// 跨模块只用 *RunError + ErrorKind 表达失败语义：不引入跨模块错误码域，
// 也不把数据库、缓存、路径、堆栈等内部细节放进对外可见的错误文本。
type ErrorKind string

const (
	// KindInvalidArgument 表示调用方传入的参数或构造的数据不满足契约。
	KindInvalidArgument ErrorKind = "invalid_argument"
	// KindInvalidPairing 表示 ToolCall 与 ToolResult 的配对不完整或非法。
	KindInvalidPairing ErrorKind = "invalid_pairing"
	// KindCancelled 表示用户主动取消。
	KindCancelled ErrorKind = "cancelled"
	// KindDeadlineExceeded 表示整体超时（与用户取消终态相同，靠 Kind 区分）。
	KindDeadlineExceeded ErrorKind = "deadline_exceeded"
	// KindLLMCallLimit 表示 LLM 调用次数预算耗尽。
	KindLLMCallLimit ErrorKind = "llm_call_limit"
	// KindToolCallLimit 表示工具调用次数预算耗尽。
	KindToolCallLimit ErrorKind = "tool_call_limit"
	// KindToolFailure 表示不可恢复、并最终终止 Run 的工具失败。
	KindToolFailure ErrorKind = "tool_failure"
	// KindProviderFailure 表示 LLM Provider 调用失败。
	KindProviderFailure ErrorKind = "provider_failure"
	// KindInternal 表示 Runtime 内部错误，以及无法归类的未知错误。
	KindInternal ErrorKind = "internal"
)

// RunError 是 Runtime 的统一错误类型。
//
// Op 使用 "<pkg>.<func>" 形式定位出错位置；CallID 在工具失败时定位到具体工具调用；
// Cause 保留原始错误链，供调用方用 errors.Is / errors.As 判断，不做错误吞并。
type RunError struct {
	Kind    ErrorKind
	Op      string
	Message string
	CallID  string
	Cause   error
}

// Error 输出稳定、可读、可检索的错误文本：op: kind: message (call_id=...) : cause。
func (e *RunError) Error() string {
	if e == nil {
		// 避免 nil *RunError 被当作 error 传入时 panic。
		return "<nil>"
	}

	kind := e.Kind
	if kind == "" {
		kind = KindInternal
	}

	var b strings.Builder
	if e.Op != "" {
		b.WriteString(e.Op)
		b.WriteString(": ")
	}
	b.WriteString(string(kind))
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.CallID != "" {
		b.WriteString(" (call_id=")
		b.WriteString(e.CallID)
		b.WriteString(")")
	}
	if e.Cause != nil {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}

	return b.String()
}

// Unwrap 返回原始错误，保持错误链完整。
func (e *RunError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// NewError 构造不带 Cause 的 RunError。
func NewError(kind ErrorKind, op, msg string) *RunError {
	return &RunError{Kind: kind, Op: op, Message: msg}
}

// KindOf 返回错误的种类。
//
// 错误链中任意位置是 *RunError 时取其 Kind；Kind 为空视为 internal；
// 非 RunError（含 nil）统一返回 KindInternal，调用方需要区分"无错误"时必须先判断 err != nil。
func KindOf(err error) ErrorKind {
	if err == nil {
		return KindInternal
	}

	var runErr *RunError
	if errors.As(err, &runErr) && runErr != nil {
		if runErr.Kind == "" {
			return KindInternal
		}
		return runErr.Kind
	}

	return KindInternal
}

// StatusOf 把错误映射为 Run 的终态。
//
// nil 表示没有失败 → COMPLETED；取消/超时 → CANCELLED；预算耗尽 → LIMIT_EXCEEDED；
// 其余（含非 RunError）→ FAILED。
func StatusOf(err error) RunStatus {
	if err == nil {
		return RunStatusCompleted
	}

	switch KindOf(err) {
	case KindCancelled, KindDeadlineExceeded:
		return RunStatusCancelled
	case KindLLMCallLimit, KindToolCallLimit:
		return RunStatusLimitExceeded
	default:
		return RunStatusFailed
	}
}

// wrapMessageIndex 为逐条消息校验的结果附加消息序号，保留 Kind / CallID / Cause。
func wrapMessageIndex(err error, index int) error {
	var runErr *RunError
	if errors.As(err, &runErr) && runErr != nil {
		return &RunError{
			Kind:    runErr.Kind,
			Op:      runErr.Op,
			Message: fmt.Sprintf("消息[%d]: %s", index, runErr.Message),
			CallID:  runErr.CallID,
			Cause:   runErr.Cause,
		}
	}

	return &RunError{
		Kind:    KindInternal,
		Op:      "core.ValidateMessages",
		Message: fmt.Sprintf("消息[%d]: 非 RunError 的校验错误", index),
		Cause:   err,
	}
}
