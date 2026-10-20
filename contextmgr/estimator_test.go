package contextmgr

import (
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TestHeuristicEstimatorTokens 固定默认启发式的计数规则：每条消息固定开销 4 token，
// 文本/工具字段按 ceil(runes/4) 折算，空片段计 0，ID 与 Raw 不参与估算。
// 该测试同时区分「只数字数」与「按字段分别折算」的错误实现。
func TestHeuristicEstimatorTokens(t *testing.T) {
	var estimator HeuristicEstimator

	cases := []struct {
		name string
		msg  core.Message
		want int
	}{
		{
			name: "空 user 消息只有固定开销",
			msg:  core.Message{ID: "m1", Role: core.Role("user")},
			want: 4,
		},
		{
			name: "文本不足 4 个 rune 按 1 token 计",
			msg: core.Message{
				ID:      "m2",
				Role:    core.Role("user"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "abc"}},
			},
			want: 5,
		},
		{
			name: "文本恰好 4 个 rune 仍按 1 token 计",
			msg: core.Message{
				ID:      "m3",
				Role:    core.Role("user"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "abcd"}},
			},
			want: 5,
		},
		{
			name: "8 个 rune 按 2 token 计，CJK 同样按 rune 折算",
			msg: core.Message{
				ID:      "m4",
				Role:    core.Role("user"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "你好世界你好"}},
			},
			want: 6,
		},
		{
			name: "4 个 CJK rune 按 1 token 计",
			msg: core.Message{
				ID:      "m4b",
				Role:    core.Role("user"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "你好世界"}},
			},
			want: 5,
		},
		{
			name: "多个 content 片段分别折算，空片段计 0",
			msg: core.Message{
				ID:   "m5",
				Role: core.Role("user"),
				Content: []core.ContentPart{
					{Type: core.ContentType("text"), Text: "ab"},
					{Type: core.ContentType("text"), Text: ""},
					{Type: core.ContentType("text"), Text: "cdefgh"},
				},
			},
			want: 7,
		},
		{
			name: "assistant 工具调用的 Name 与 Arguments 分别折算",
			msg: core.Message{
				ID:      "m6",
				Role:    core.Role("assistant"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "我来读取"}},
				ToolCalls: []core.ToolCall{
					{ID: "call_1", Name: "read_file", Arguments: []byte(`{"path":"a"}`)},
				},
			},
			// 4 + ceil(4/4)=1 + ceil(9/4)=3 + ceil(11/4)=3
			want: 11,
		},
		{
			name: "tool 消息按 ToolResult 的 Name 与 Content 折算",
			msg: core.Message{
				ID:      "m7",
				Role:    core.Role("tool"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "ok"}},
				ToolResult: &core.ToolResult{
					CallID:  "call_1",
					Name:    "read_file",
					Content: "文件内容",
				},
			},
			// 4 + 1（content "ok"）+ 3（name）+ ceil(4/4)=1（content）
			want: 9,
		},
		{
			name: "ID 与 Raw 不参与估算",
			msg: core.Message{
				ID:      "很长的消息标识不参与计数",
				Role:    core.Role("user"),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "a"}},
				Raw:     []byte(`{"raw":"很长的原始载荷不参与计数"}`),
			},
			want: 5,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := estimator.EstimateMessage(tc.msg); got != tc.want {
				t.Fatalf("EstimateMessage() = %d，期望 %d", got, tc.want)
			}
			if tc.want < 0 {
				t.Fatalf("估算值不应为负：%d", tc.want)
			}
		})
	}
}

// TestHeuristicEstimatorImplementsEstimator 固定 Estimator 接口可由值类型满足。
func TestHeuristicEstimatorImplementsEstimator(t *testing.T) {
	var _ Estimator = HeuristicEstimator{}
}

// TestHeuristicEstimatorNonNegative 对任意消息都不返回负值（INV-001 的估算前提）。
func TestHeuristicEstimatorNonNegative(t *testing.T) {
	var estimator HeuristicEstimator
	msgs := []core.Message{
		{},
		{ID: "m1", Role: core.Role("user")},
		{Role: core.Role("assistant"), ToolCalls: []core.ToolCall{{ID: "c1"}}},
		{Role: core.Role("tool"), ToolResult: &core.ToolResult{CallID: "c1"}},
		{Role: core.Role("user"), Content: []core.ContentPart{{Type: core.ContentType("text"), Text: "任意内容"}}},
	}
	for i, msg := range msgs {
		if got := estimator.EstimateMessage(msg); got < 0 {
			t.Fatalf("消息[%d] 估算值为负：%d", i, got)
		}
	}
}
