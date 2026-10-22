package contextmgr

import (
	"unicode"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Estimator 估算单条消息的 token 数。
//
// 预算判断全部基于注入的估算器：测试可注入确定性实现固定预算边界，生产装配可注入
// 校准后的实现；HeuristicEstimator 只是不依赖第三方 tokenizer 的默认启发式。
type Estimator interface {
	EstimateMessage(core.Message) int
}

const (
	// messageOverheadTokens 是 HeuristicEstimator 对每条消息计入的固定开销。
	messageOverheadTokens = 4
	// latinRunesPerToken 是非 CJK 文本的字符/token 折算比（4 字符 ≈ 1 token）。
	latinRunesPerToken = 4
	// maxEstimateTokens 是单个估算值的饱和上限：防止病态输入把 int 累加溢出成负数，
	// 从而让预算判断失效。触发饱和只可能是「明显超预算」，因此饱和是安全方向。
	maxEstimateTokens = 1 << 30
)

// HeuristicEstimator 是默认的启发式估算器，零字段、可直接值使用。
//
// 规则：每条消息固定开销 4 token，加上各 ContentPart.Text、ToolCall.Name、
// ToolCall.Arguments、ToolResult.Name、ToolResult.Content 的折算值：
//
//   - CJK 字符（汉字、假名、谚文）每个计 1 token；
//   - 其余字符每 4 个计 1 token（向上取整）。
//
// 这样处理的原因：原「统一 4 字符/token」对中文低估约 2–4 倍，会让中文长会话在真实
// Provider 上先超限而本地预算仍显示充裕。改为 CJK 每字符 1 token 后，中文侧偏保守
// （多数 tokenizer 对常用汉字约 0.6–1 token/字），英文侧保持 4 字符/token 的常见近似。
// 它仍然不是精确 tokenizer：英文代码/JSON 可能被低估，不同模型的 CJK 折算是 1–1.5
// token/字，因此生产配置应为达到真实上限预留余量，不能把本估算当作计费依据。
// Message.ID 与 Message.Raw 不参与估算。
type HeuristicEstimator struct{}

// EstimateMessage 实现 Estimator：按上述启发式规则返回非负估算值。
func (HeuristicEstimator) EstimateMessage(msg core.Message) int {
	tokens := messageOverheadTokens
	for _, part := range msg.Content {
		tokens = saturatingAdd(tokens, textTokens(part.Text))
	}
	for _, call := range msg.ToolCalls {
		tokens = saturatingAdd(tokens, textTokens(call.Name))
		tokens = saturatingAdd(tokens, textTokens(string(call.Arguments)))
	}
	if msg.ToolResult != nil {
		tokens = saturatingAdd(tokens, textTokens(msg.ToolResult.Name))
		tokens = saturatingAdd(tokens, textTokens(msg.ToolResult.Content))
	}
	return tokens
}

// textTokens 把一个文本片段折算为 token 数：空片段计 0；CJK 字符每字 1 token；
// 其余字符按 ceil(runes/4) 折算。结果不超过 maxEstimateTokens。
func textTokens(s string) int {
	if s == "" {
		return 0
	}
	wide := 0
	latin := 0
	for _, r := range s {
		if isWideRune(r) {
			wide++
			continue
		}
		latin++
	}
	tokens := wide + (latin+latinRunesPerToken-1)/latinRunesPerToken
	if tokens > maxEstimateTokens || tokens < 0 {
		return maxEstimateTokens
	}
	return tokens
}

// isWideRune 判断是否按「1 token/字符」保守计费的东亚字符：汉字、平假名、片假名、
// 谚文及其兼容区。判断只看 Unicode 区段，不依赖 locale 或外部词表。
func isWideRune(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

// saturatingAdd 做饱和加法：任一侧到达 maxEstimateTokens 后保持上限，不产生负值。
func saturatingAdd(total, add int) int {
	if add <= 0 {
		return total
	}
	if add >= maxEstimateTokens || total >= maxEstimateTokens-add {
		return maxEstimateTokens
	}
	return total + add
}
