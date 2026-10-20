package core

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

// textMessage 构造只含文本正文的消息，供测试复用。
func textMessage(id string, role Role, text string) Message {
	return Message{
		ID:      id,
		Role:    role,
		Content: []ContentPart{{Type: contentTypeText, Text: text}},
	}
}

// newCall 构造一次工具调用。
func newCall(id, name, args string) ToolCall {
	return ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

// expectKind 断言错误种类；err 为 nil 或种类不符都算失败。
func expectKind(t *testing.T, err error, want ErrorKind) *RunError {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误种类 %s，实际没有返回错误", want)
	}
	if got := KindOf(err); got != want {
		t.Fatalf("期望错误种类 %s，实际为 %s（错误：%v）", want, got, err)
	}
	var runErr *RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("期望错误为 *RunError，实际为 %T", err)
	}
	return runErr
}

// TestMessageRoundTripToolCalls 覆盖 assistant 消息带 0 / 1 / N(≥2) 个 ToolCall 的往返：
// 调用顺序、调用标识与原始参数必须无损，且每个结果都能按 CallID 唯一定位到对应调用。
func TestMessageRoundTripToolCalls(t *testing.T) {
	cases := []struct {
		name  string
		calls []ToolCall
	}{
		{name: "零个工具调用", calls: nil},
		{
			name:  "一个工具调用",
			calls: []ToolCall{newCall("call_1", "read_file", `{"path":"docs/design/runtime-architecture.md"}`)},
		},
		{
			name: "多个工具调用",
			calls: []ToolCall{
				newCall("call_1", "read_file", `{"path":"go.mod"}`),
				newCall("call_2", "list_dir", `{"path":"core","depth":2}`),
				newCall("call_3", "echo", `{"text":"第三"}`),
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			original := Message{
				ID:   "msg_assistant",
				Role: roleAssistant,
				// Raw 使用真实片段：nil 的 json.RawMessage 会被标准库往返成字面量 null，
				// 属于 encoding/json 的既有行为，不是契约承诺的"无损"范围。
				Content:   []ContentPart{{Type: contentTypeText, Text: "准备调用工具", Raw: json.RawMessage(`{"type":"text"}`)}},
				ToolCalls: tc.calls,
				Raw:       json.RawMessage(`{"provider":"raw"}`),
			}
			if err := original.Validate(); err != nil {
				t.Fatalf("合法 assistant 消息不应校验失败：%v", err)
			}

			data, err := json.Marshal(original)
			if err != nil {
				t.Fatalf("序列化失败：%v", err)
			}
			var decoded Message
			if err := json.Unmarshal(data, &decoded); err != nil {
				t.Fatalf("反序列化失败：%v", err)
			}
			if !reflect.DeepEqual(original, decoded) {
				t.Fatalf("往返后消息不一致：\n原始：%+v\n解码：%+v", original, decoded)
			}

			if len(decoded.ToolCalls) != len(tc.calls) {
				t.Fatalf("工具调用数量变化：期望 %d，实际 %d", len(tc.calls), len(decoded.ToolCalls))
			}
			for i, want := range tc.calls {
				got := decoded.ToolCalls[i]
				if got.ID != want.ID || got.Name != want.Name {
					t.Fatalf("第 %d 个工具调用的顺序或标识变化：期望 %+v，实际 %+v", i, want, got)
				}
				if string(got.Arguments) != string(want.Arguments) {
					t.Fatalf("第 %d 个工具调用参数被改写：期望 %s，实际 %s", i, want.Arguments, got.Arguments)
				}
			}

			msgs := []Message{textMessage("msg_user", roleUser, "请读取文件")}
			msgs = append(msgs, decoded)
			for _, call := range decoded.ToolCalls {
				msgs = append(msgs, Message{
					ID:         "msg_result_" + call.ID,
					Role:       roleTool,
					ToolResult: &ToolResult{CallID: call.ID, Name: call.Name, Content: "ok"},
				})
			}
			if err := ValidateMessages(msgs); err != nil {
				t.Fatalf("往返后的合法消息序列不应校验失败：%v", err)
			}
		})
	}
}

// TestMessageValidate 覆盖消息结构与工具身份的组合边界。
func TestMessageValidate(t *testing.T) {
	result := &ToolResult{CallID: "call_1", Name: "echo", Content: "ok"}

	cases := []struct {
		name     string
		msg      Message
		want     ErrorKind
		wantCall string
	}{
		{name: "system 纯文本", msg: textMessage("m1", roleSystem, "你是助手"), want: ""},
		{name: "user 纯文本", msg: textMessage("m2", roleUser, "你好"), want: ""},
		{
			name: "assistant 带多个工具调用",
			msg: Message{
				ID:        "m3",
				Role:      roleAssistant,
				ToolCalls: []ToolCall{newCall("call_1", "echo", `{}`), newCall("call_2", "echo", `{}`)},
			},
			want: "",
		},
		{
			name: "tool 消息携带结果",
			msg:  Message{ID: "m4", Role: roleTool, ToolResult: result},
			want: "",
		},
		{
			name: "assistant 不允许携带 ToolResult",
			msg:  Message{ID: "m5", Role: roleAssistant, ToolResult: result},
			want: KindInvalidArgument,
		},
		{
			name: "user 不允许携带 ToolCalls",
			msg:  Message{ID: "m6", Role: roleUser, ToolCalls: []ToolCall{newCall("call_1", "echo", `{}`)}},
			want: KindInvalidArgument,
		},
		{
			name: "tool 消息必须携带 ToolResult",
			msg:  Message{ID: "m7", Role: roleTool},
			want: KindInvalidArgument,
		},
		{
			name: "tool 消息不允许携带 ToolCalls",
			msg: Message{
				ID:         "m8",
				Role:       roleTool,
				ToolCalls:  []ToolCall{newCall("call_1", "echo", `{}`)},
				ToolResult: result,
			},
			want: KindInvalidArgument,
		},
		{name: "未知角色", msg: Message{ID: "m9", Role: Role("developer")}, want: KindInvalidArgument},
		{
			name:     "工具调用 ID 为空",
			msg:      Message{ID: "m10", Role: roleAssistant, ToolCalls: []ToolCall{newCall("", "echo", `{}`)}},
			want:     KindInvalidPairing,
			wantCall: "",
		},
		{
			name: "同一消息内工具调用 ID 重复",
			msg: Message{
				ID:        "m11",
				Role:      roleAssistant,
				ToolCalls: []ToolCall{newCall("call_dup", "echo", `{}`), newCall("call_dup", "echo", `{}`)},
			},
			want:     KindInvalidPairing,
			wantCall: "call_dup",
		},
		{
			name: "工具结果 CallID 为空",
			msg: Message{
				ID:         "m12",
				Role:       roleTool,
				ToolResult: &ToolResult{Name: "echo", Content: "ok"},
			},
			want: KindInvalidPairing,
		},
		{
			name: "未知内容类型",
			msg: Message{
				ID:      "m13",
				Role:    roleUser,
				Content: []ContentPart{{Type: ContentType("image"), Text: "x"}},
			},
			want: KindInvalidArgument,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.msg.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("不应返回错误：%v", err)
				}
				return
			}
			runErr := expectKind(t, err, tc.want)
			if runErr.CallID != tc.wantCall {
				t.Fatalf("错误应定位到 CallID %q，实际为 %q", tc.wantCall, runErr.CallID)
			}
		})
	}
}

// TestMessageValidateReportsCallID 确认重复调用标识的报错文本可用于定位。
func TestMessageValidateReportsCallID(t *testing.T) {
	msg := Message{
		ID:        "m1",
		Role:      roleAssistant,
		ToolCalls: []ToolCall{newCall("call_dup", "echo", `{}`), newCall("call_dup", "echo", `{}`)},
	}
	err := msg.Validate()
	if err == nil {
		t.Fatal("重复工具调用 ID 不应通过校验")
	}
	if !strings.Contains(err.Error(), "call_dup") {
		t.Fatalf("错误文本应包含 CallID，实际为：%v", err)
	}
}
