package runlog

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Discard 必须满足 Sink 契约。
var _ Sink = Discard()

// TestDiscardHasNoSideEffects 覆盖 INV-008 的可验证部分：丢弃实现不返回错误、
// 不改写事件内容，也不依赖 ctx（取消后仍然成功），因此日志旁路不会影响 Run 结果。
func TestDiscardHasNoSideEffects(t *testing.T) {
	sink := Discard()
	if sink == nil {
		t.Fatal("Discard 不能返回 nil Sink")
	}

	events := []Event{
		{},
		{
			Kind:    EventKind("run_started"),
			Ref:     core.ExecutionRef{SessionID: "session_1", RunID: "run_1"},
			Step:    1,
			Summary: "开始运行",
			Data:    json.RawMessage(`{"model":"test-model"}`),
		},
		{
			Kind:   EventKind("tool_finished"),
			Ref:    core.ExecutionRef{SessionID: "session_1", RunID: "run_1"},
			Step:   2,
			CallID: "call_1",
			Data:   json.RawMessage(`{"name":"echo","is_error":true}`),
			Error:  &core.RunError{Kind: core.KindToolFailure, Op: "tool.execute", Message: "不可恢复", CallID: "call_1"},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for i, ev := range events {
		before := ev
		if err := sink.Record(ctx, ev); err != nil {
			t.Fatalf("第 %d 个事件不应返回错误：%v", i, err)
		}
		if !reflect.DeepEqual(before, ev) {
			t.Fatalf("第 %d 个事件被改写：\n发送前：%+v\n调用后：%+v", i, before, ev)
		}
	}

	// 多次调用保持稳定：丢弃实现不累积状态。
	for i := 0; i < 3; i++ {
		if err := sink.Record(context.Background(), events[1]); err != nil {
			t.Fatalf("重复调用不应返回错误：%v", err)
		}
	}
}
