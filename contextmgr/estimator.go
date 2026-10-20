package contextmgr

import (
	"unicode/utf8"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Estimator 估算单条消息的 token 数。
//
// 预算判断全部基于注入的估算器：测试可注入确定性实现固定预算边界，生产装配可注入
// 校准后的实现；HeuristicEstimator 只是不依赖第三方 tokenizer 的默认启发式。
type Estimator interface {
	EstimateMessage(core.Message) int
}

// messageOverheadTokens 是 HeuristicEstimator 对每条消息计入的固定开销。
const messageOverheadTokens = 4

// runesPerToken 是 HeuristicEstimator 的字符/token 折算比（4 字符 ≈ 1 token）。
const runesPerToken = 4

// HeuristicEstimator 是默认的启发式估算器，零字段、可直接值使用。
//
// 规则：每条消息固定开销 4 token，加上各 ContentPart.Text、ToolCall.Name、
// ToolCall.Arguments、ToolResult.Name、ToolResult.Content 的 rune 数按
// ceil(runes/4) 求和；空文本片段计 0。Message.ID、Raw 等内容不参与估算。
// 它不宣称等于真实模型 token 数（对 CJK 偏低），生产装配应注入校准后的估算器。
type HeuristicEstimator struct{}

// EstimateMessage 实现 Estimator：按上述启发式规则返回非负估算值。
func (HeuristicEstimator) EstimateMessage(msg core.Message) int {
	tokens := messageOverheadTokens
	for _, part := range msg.Content {
		tokens += textTokens(part.Text)
	}
	for _, call := range msg.ToolCalls {
		tokens += textTokens(call.Name) + textTokens(string(call.Arguments))
	}
	if msg.ToolResult != nil {
		tokens += textTokens(msg.ToolResult.Name) + textTokens(msg.ToolResult.Content)
	}
	return tokens
}

// textTokens 把一个文本片段折算为 token 数：空片段计 0，否则按 ceil(runes/4) 计算。
func textTokens(s string) int {
	if s == "" {
		return 0
	}
	return (utf8.RuneCountInString(s) + runesPerToken - 1) / runesPerToken
}
