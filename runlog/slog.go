package runlog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// recordMessage 是所有运行事件记录的固定 msg，便于采集侧按 msg 过滤。
const recordMessage = "run_event"

// slogSink 把 Event 渲染为 slog 记录。
//
// 它自身无可变状态：并发安全依赖 slog.Handler 的并发契约（标准库 JSONHandler 以共享
// mutex 串行写单个 writer），因此同一 Sink 可被多个 goroutine 并发使用。
type slogSink struct {
	handler slog.Handler
	// invalid 非 nil 表示构造参数缺失：Record 固定返回该错误，不 panic。
	invalid error
}

var _ Sink = (*slogSink)(nil)

// NewSlogSink 用调用方注入的 slog.Handler 构造 Sink。
// 分级、改写与输出目标由该 Handler 决定；h 为 nil 时返回的 Sink 在 Record 时报错。
func NewSlogSink(h slog.Handler) Sink {
	if h == nil {
		return &slogSink{invalid: errors.New("runlog: nil slog.Handler")}
	}
	return &slogSink{handler: h}
}

// NewJSONSink 构造输出单行 JSON 的 Sink，writer 为注入的输出目标（文件、缓冲、网络连接均可）。
// w 为 nil 时返回的 Sink 在 Record 时报错。
func NewJSONSink(w io.Writer) Sink {
	if w == nil {
		return &slogSink{invalid: errors.New("runlog: nil io.Writer")}
	}
	return &slogSink{handler: slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo})}
}

// Record 同步写出单条结构化记录。
//
// 语义（旁路原则）：
//   - 写入失败与 Handler 返回的错误原样返回，调用方必须忽略；
//   - Handler panic 被 recover 为错误返回，不传播给调用方；
//   - ctx 只透传给 Handler，已取消不阻止记录、不返回 ctx.Err()；
//   - 只读入参：不改写 Event、Data 字节或 *core.RunError。
func (s *slogSink) Record(ctx context.Context, ev Event) (err error) {
	if s == nil {
		return errors.New("runlog: nil sink")
	}
	if s.invalid != nil {
		return s.invalid
	}
	if s.handler == nil {
		return errors.New("runlog: nil slog.Handler")
	}

	defer func() {
		if r := recover(); r != nil {
			// 只暴露 panic 类型，不把载荷或堆栈写进错误，避免二次泄漏。
			err = fmt.Errorf("runlog: slog handler panic recovered (%T)", r)
		}
	}()

	const level = slog.LevelInfo
	if !s.handler.Enabled(ctx, level) {
		return nil
	}

	record := slog.NewRecord(time.Now(), level, recordMessage, 0)
	record.AddAttrs(recordAttrs(ev)...)
	return s.handler.Handle(ctx, record)
}

// recordAttrs 按固定顺序构建记录字段；不适用的字段直接省略。
func recordAttrs(ev Event) []slog.Attr {
	attrs := make([]slog.Attr, 0, 12)
	attrs = append(attrs,
		slog.String("session_id", ev.Ref.SessionID),
		slog.String("run_id", ev.Ref.RunID),
		slog.String("event", string(ev.Kind)),
		slog.Int("step", ev.Step),
	)
	if callID := eventCallID(ev); callID != "" {
		attrs = append(attrs, slog.String("call_id", callID))
	}
	attrs = append(attrs,
		slog.Int64("duration_ms", ev.Duration.Milliseconds()),
		slog.String("status", string(eventStatus(ev))),
	)

	if ev.Error != nil {
		attrs = append(attrs, slog.String("error_kind", string(core.KindOf(ev.Error))))
		if ev.Error.Op != "" {
			attrs = append(attrs, slog.String("op", ev.Error.Op))
		}
		if ev.Error.Message != "" {
			// 错误文本同样经过值级脱敏与截断；Cause、堆栈与错误对象本身不进入日志。
			attrs = append(attrs, slog.String("error_message", sanitizeString(ev.Error.Message)))
		}
	}

	if ev.Summary != "" {
		attrs = append(attrs, slog.String("summary", ev.Summary))
	}

	if len(ev.Data) > 0 {
		data, invalid, truncated := sanitizeData(ev.Data)
		switch {
		case invalid:
			attrs = append(attrs, slog.Bool("data_invalid", true))
		case truncated:
			attrs = append(attrs, slog.Bool("data_truncated", true))
		default:
			attrs = append(attrs, slog.Any("data", data))
		}
	}

	return attrs
}

// eventCallID 取事件自身的 CallID；为空时回退到错误携带的 CallID（工具失败的定位字段）。
func eventCallID(ev Event) string {
	if ev.CallID != "" {
		return ev.CallID
	}
	if ev.Error != nil {
		return ev.Error.CallID
	}
	return ""
}

// eventStatus 计算记录中的 status：显式 Status 优先，其次由错误推导，最后是 RUNNING。
// Sink 不合成终态：工具/模型调用成功事件在没有错误时输出 RUNNING。
func eventStatus(ev Event) core.RunStatus {
	if ev.Status != "" {
		return ev.Status
	}
	if ev.Error != nil {
		return core.StatusOf(ev.Error)
	}
	return core.RunStatusRunning
}
