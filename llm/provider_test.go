package llm

import (
	"encoding/json"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// assistantMessage 构造合法的 assistant 响应消息。
func assistantMessage(calls ...core.ToolCall) core.Message {
	return core.Message{
		ID:        "msg_assistant",
		Role:      roleAssistant,
		Content:   []core.ContentPart{{Type: core.ContentType("text"), Text: "好的"}},
		ToolCalls: calls,
	}
}

// TestResponseValidate 覆盖 Provider 响应的合法与拒绝路径。
func TestResponseValidate(t *testing.T) {
	cases := []struct {
		name     string
		response Response
		want     core.ErrorKind
	}{
		{
			name: "正常文本响应",
			response: Response{
				Message:      assistantMessage(),
				FinishReason: finishReasonStop,
				Usage:        Usage{InputTokens: 10, OutputTokens: 5},
			},
		},
		{
			name: "工具调用响应",
			response: Response{
				Message:      assistantMessage(core.ToolCall{ID: "call_1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}),
				FinishReason: finishReasonToolCalls,
				Usage:        Usage{InputTokens: 20, OutputTokens: 8},
				Raw:          json.RawMessage(`{"id":"resp_1"}`),
			},
		},
		{
			name: "unknown 结束原因",
			response: Response{
				Message:      assistantMessage(),
				FinishReason: finishReasonUnknown,
			},
		},
		{
			name: "响应角色不是 assistant",
			response: Response{
				Message:      core.Message{ID: "m1", Role: core.Role("user")},
				FinishReason: finishReasonStop,
			},
			want: core.KindInvalidArgument,
		},
		{
			name: "结束原因缺失",
			response: Response{
				Message: assistantMessage(),
			},
			want: core.KindInvalidArgument,
		},
		{
			name: "结束原因不在契约取值内",
			response: Response{
				Message:      assistantMessage(),
				FinishReason: FinishReason("stop_sequence"),
			},
			want: core.KindInvalidArgument,
		},
		{
			name: "token 用量为负",
			response: Response{
				Message:      assistantMessage(),
				FinishReason: finishReasonStop,
				Usage:        Usage{InputTokens: -1},
			},
			want: core.KindInvalidArgument,
		},
		{
			name: "内层消息带 ToolResult",
			response: Response{
				Message: core.Message{
					ID:         "m1",
					Role:       roleAssistant,
					ToolResult: &core.ToolResult{CallID: "call_1", Content: "ok"},
				},
				FinishReason: finishReasonStop,
			},
			want: core.KindInvalidArgument,
		},
		{
			name: "内层工具调用标识重复",
			response: Response{
				Message: assistantMessage(
					core.ToolCall{ID: "call_dup", Name: "echo"},
					core.ToolCall{ID: "call_dup", Name: "echo"},
				),
				FinishReason: finishReasonToolCalls,
			},
			want: core.KindInvalidPairing,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.response.Validate()
			if tc.want == "" {
				if err != nil {
					t.Fatalf("不应返回错误：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("期望错误种类 %s，实际没有返回错误", tc.want)
			}
			if got := core.KindOf(err); got != tc.want {
				t.Fatalf("期望错误种类 %s，实际 %s（错误：%v）", tc.want, got, err)
			}
		})
	}
}
