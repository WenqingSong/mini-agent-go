// Package runlog 定义运行事件模型与日志 Sink 接口。
//
// 日志是观察者：Sink.Record 返回错误不得改变 Run 状态、计数或消息序列（旁路原则）。
// 本包只定义事件形状，落盘格式、采样与上报后端属于后续任务。本包只依赖标准库与 core。
package runlog

import (
	"context"
	"encoding/json"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// EventKind 是运行事件类型：run_started / llm_request / llm_response /
// tool_started / tool_finished / run_finished。
type EventKind string

// Event 是一条运行事件。Data 必须是已脱敏的结构化载荷，不得写入凭据、Token 或私钥。
// Error 用 core.RunError 表达失败，便于与 RunResult 的错误语义保持一致。
type Event struct {
	Kind    EventKind
	Ref     core.ExecutionRef
	Step    int
	CallID  string
	Summary string
	Data    json.RawMessage
	Error   *core.RunError
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
