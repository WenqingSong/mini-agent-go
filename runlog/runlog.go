// Package runlog 定义运行事件模型与日志 Sink 接口，并提供基于标准库 log/slog 的
// 结构化实现。
//
// 日志是观察者：Sink.Record 返回错误不得改变 Run 状态、计数或消息序列（旁路原则）。
// 记录形态固定为一行 log/slog JSON（msg=run_event），字段语义、脱敏边界与上限见
// docs/design/runtime-architecture.md §10；文件落盘/滚动、采样与上报后端属于后续任务。
// 本包只依赖标准库与 core。
package runlog

import (
	"context"
	"encoding/json"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// EventKind 是运行事件类型：run_started / llm_request / llm_response /
// tool_started / tool_finished / run_finished。
type EventKind string

const (
	// KindRunStarted 表示一次 Run 开始。
	KindRunStarted EventKind = "run_started"
	// KindLLMRequest 表示即将发起一次 LLM 调用。
	KindLLMRequest EventKind = "llm_request"
	// KindLLMResponse 表示一次 LLM 调用返回。
	KindLLMResponse EventKind = "llm_response"
	// KindToolStarted 表示即将执行一次工具调用。
	KindToolStarted EventKind = "tool_started"
	// KindToolFinished 表示一次工具调用结束。
	KindToolFinished EventKind = "tool_finished"
	// KindRunFinished 表示一次 Run 结束。
	KindRunFinished EventKind = "run_finished"
)

// Event 是一条运行事件。Data 必须是已脱敏的结构化载荷，不得写入凭据、Token 或私钥；
// Sink 仍会按固定允许清单再做一次防御性脱敏。
// Error 用 core.RunError 表达失败，便于与 RunResult 的错误语义保持一致。
type Event struct {
	Kind    EventKind
	Ref     core.ExecutionRef
	Step    int
	CallID  string
	Summary string
	Data    json.RawMessage
	Error   *core.RunError

	// Duration 是发出端测量的本阶段耗时，记录为 duration_ms（向下取整到毫秒）；
	// 0 表示未测量或不足 1 毫秒，Sink 不推算耗时。
	Duration time.Duration
	// Status 只允许终态事件（如 run_finished）填写，遵守 core.RunStatus 语义；
	// 为空时由 Error 推导，仍为空则记录 RUNNING。Sink 不为工具/模型成功事件合成终态。
	Status core.RunStatus
}

// Sink 接收运行事件。返回错误只代表日志写入失败，调用方必须忽略其对 Run 结果的影响。
type Sink interface {
	Record(ctx context.Context, ev Event) error
}

// discardSink 丢弃全部事件，不产生任何副作用。
type discardSink struct{}

// Record 直接返回 nil：默认装配与测试使用的空实现。
func (discardSink) Record(context.Context, Event) error {
	return nil
}

// Discard 返回丢弃实现的 Sink，供默认装配与测试使用。
func Discard() Sink {
	return discardSink{}
}
