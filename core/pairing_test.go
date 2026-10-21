package core

import (
	"strings"
	"testing"
)

// assistantWithCalls 构造带工具调用的 assistant 消息。
func assistantWithCalls(id string, calls ...ToolCall) Message {
	return Message{ID: id, Role: roleAssistant, ToolCalls: calls}
}

// toolMessage 构造携带工具结果的 tool 消息。
func toolMessage(id, callID string) Message {
	return Message{ID: id, Role: roleTool, ToolResult: &ToolResult{CallID: callID, Name: "echo", Content: "ok"}}
}

// TestValidateToolPairingValid 覆盖合法配对：无调用、单调用、多调用且顺序稳定。
func TestValidateToolPairingValid(t *testing.T) {
	cases := []struct {
		name string
		msgs []Message
	}{
		{
			name: "没有工具调用的纯文本对话",
			msgs: []Message{
				textMessage("m1", roleSystem, "你是助手"),
				textMessage("m2", roleUser, "你好"),
				textMessage("m3", roleAssistant, "你好，有什么可以帮你？"),
			},
		},
		{
			name: "单个工具调用与结果",
			msgs: []Message{
				textMessage("m1", roleUser, "读一下 go.mod"),
				assistantWithCalls("m2", newCall("call_1", "read_file", `{"path":"go.mod"}`)),
				toolMessage("m3", "call_1"),
				textMessage("m4", roleAssistant, "内容是 module 声明"),
			},
		},
		{
			name: "多个工具调用各自产生结果，顺序与调用一致",
			msgs: []Message{
				textMessage("m1", roleUser, "同时读两个文件"),
				assistantWithCalls("m2",
					newCall("call_1", "read_file", `{"path":"a"}`),
					newCall("call_2", "read_file", `{"path":"b"}`),
				),
				toolMessage("m3", "call_1"),
				toolMessage("m4", "call_2"),
				textMessage("m5", roleAssistant, "两个文件都读完了"),
			},
		},
		{
			name: "失败的工具结果同样参与配对",
			msgs: []Message{
				assistantWithCalls("m1", newCall("call_1", "read_file", `{"path":"missing"}`)),
				{
					ID:         "m2",
					Role:       roleTool,
					ToolResult: &ToolResult{CallID: "call_1", Name: "read_file", Content: "文件不存在", IsError: true},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateToolPairing(tc.msgs); err != nil {
				t.Fatalf("合法配对不应失败：%v", err)
			}
			if err := ValidateMessages(tc.msgs); err != nil {
				t.Fatalf("合法消息序列不应失败：%v", err)
			}
		})
	}
}

// TestValidateToolPairingInvalid 覆盖 AC-002 要求的三类非法配对及相邻边界：
// 缺失 Result、重复 Result 标识、孤儿 Result，都必须返回 invalid_pairing 并定位到 CallID。
func TestValidateToolPairingInvalid(t *testing.T) {
	cases := []struct {
		name     string
		msgs     []Message
		wantCall string
	}{
		{
			name: "缺失 Result",
			msgs: []Message{
				textMessage("m1", roleUser, "读一下 go.mod"),
				assistantWithCalls("m2", newCall("call_missing", "read_file", `{"path":"go.mod"}`)),
			},
			wantCall: "call_missing",
		},
		{
			name: "重复 Result 标识",
			msgs: []Message{
				assistantWithCalls("m1", newCall("call_dup", "read_file", `{}`)),
				toolMessage("m2", "call_dup"),
				toolMessage("m3", "call_dup"),
			},
			wantCall: "call_dup",
		},
		{
			name: "孤儿 Result 引用不存在的调用",
			msgs: []Message{
				textMessage("m1", roleUser, "你好"),
				toolMessage("m2", "call_orphan"),
			},
			wantCall: "call_orphan",
		},
		{
			name: "Result 出现在调用之前",
			msgs: []Message{
				toolMessage("m1", "call_order"),
				assistantWithCalls("m2", newCall("call_order", "read_file", `{}`)),
			},
			wantCall: "call_order",
		},
		{
			name: "同一 assistant 消息内调用标识重复",
			msgs: []Message{
				assistantWithCalls("m1",
					newCall("call_dup", "read_file", `{}`),
					newCall("call_dup", "read_file", `{}`),
				),
			},
			wantCall: "call_dup",
		},
		{
			name: "调用标识为空",
			msgs: []Message{
				assistantWithCalls("m1", newCall("", "read_file", `{}`)),
			},
			wantCall: "",
		},
		{
			name: "tool 消息缺少 ToolResult",
			msgs: []Message{
				{ID: "m1", Role: roleTool},
			},
			wantCall: "",
		},
		{
			name: "调用标识跨消息重复使用",
			msgs: []Message{
				assistantWithCalls("m1", newCall("call_reuse", "read_file", `{}`)),
				toolMessage("m2", "call_reuse"),
				assistantWithCalls("m3", newCall("call_reuse", "read_file", `{}`)),
				toolMessage("m4", "call_reuse"),
			},
			wantCall: "call_reuse",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateToolPairing(tc.msgs)
			runErr := expectKind(t, err, KindInvalidPairing)
			if runErr.CallID != tc.wantCall {
				t.Fatalf("错误应定位到 CallID %q，实际为 %q（错误：%v）", tc.wantCall, runErr.CallID, runErr)
			}
			if tc.wantCall != "" && !strings.Contains(err.Error(), tc.wantCall) {
				t.Fatalf("错误文本应包含 CallID %q，实际为：%v", tc.wantCall, err)
			}
		})
	}
}

// TestValidateMessagesAddsIndexAndKeepsKind 确认整段校验保留单条消息的错误种类并附加消息序号。
func TestValidateMessagesAddsIndexAndKeepsKind(t *testing.T) {
	msgs := []Message{
		textMessage("m1", roleUser, "你好"),
		{ID: "m2", Role: Role("developer")},
	}
	runErr := expectKind(t, ValidateMessages(msgs), KindInvalidArgument)
	if !strings.Contains(runErr.Message, "消息[1]") {
		t.Fatalf("错误应附带消息序号，实际为：%q", runErr.Message)
	}

	pairingErr := expectKind(t, ValidateMessages([]Message{
		assistantWithCalls("m1", newCall("call_x", "echo", `{}`)),
	}), KindInvalidPairing)
	if pairingErr.CallID != "call_x" {
		t.Fatalf("整段校验应保留 CallID，实际为 %q", pairingErr.CallID)
	}
}

// TestValidateMessagesRejectsNilResultToolMessage 确认 tool 消息缺少结果时不会被静默接受。
func TestValidateMessagesRejectsNilResultToolMessage(t *testing.T) {
	runErr := expectKind(t, ValidateMessages([]Message{{ID: "m1", Role: roleTool}}), KindInvalidArgument)
	if runErr.Kind != KindInvalidArgument {
		t.Fatalf("期望 invalid_argument，实际 %s", runErr.Kind)
	}
}
