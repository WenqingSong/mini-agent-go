package contextmgr

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// opSummarize 是 DigestSummarizer 错误的统一 Op 定位。
const opSummarize = "contextmgr.DigestSummarizer.Summarize"

// 摘要格式与长度常量。
const (
	// DefaultSummaryMaxRunes 是默认摘要上限（rune）。400 个中文 rune 约 400 估算 token
	// （HeuristicEstimator），明显小于建议的生产 TokenBudget，使「摘要本身撑破预算」
	// 只可能出现在极小预算或调用方显式配置过大的 MaxRunes 时。
	DefaultSummaryMaxRunes = 400
	// summaryHeader 是摘要首行标记：用于区分「压缩摘要」与普通 system prompt。
	summaryHeader = "[早期对话摘要]"
	// minMessageExcerptRunes / maxMessageExcerptRunes 限定单条消息摘录长度：
	// 摘要上限很小的时候仍可读，极大时候也不会被一条超长消息占满。
	minMessageExcerptRunes = 16
	maxMessageExcerptRunes = 80
	// maxToolNamesRunes 限定一行摘录里工具名称列表的长度（防御异常长的工具名）。
	maxToolNamesRunes = 60
	// roleUser 是本包视图里 user 角色的字面量（core 未导出 Role 常量）。
	roleUser = core.Role("user")
)

// DigestSummarizer 是确定性的规则化摘要器，不发起任何模型或网络调用。
//
// 它把「将被压缩的早期消息」逐条摘录为一行：角色 + 原文截断 + 工具调用/结果标识。
// 产生的摘要具备以下性质：
//
//   - 有界且扁平：输出不超过 MaxRunes（默认 DefaultSummaryMaxRunes）。上一次摘要被重新
//     解析为摘录行参与同一预算，空间不足时从最老的摘录行开始丢弃并累计省略条数，
//     因此反复压缩既不会无限累积，也不会形成「摘要里嵌套摘要」；
//   - 保真：只引用原文前缀并显式标注省略号，不推断、不补写、不概括语义；
//   - 可解释：首行声明这是规则化摘录而非语义总结，并给出保留/未展开的条数与角色计数；
//   - 确定性：同一输入逐字节相同，完全离线可测。
//
// 信息损失是明确的：被截断的后半段文本、工具参数的完整内容、消息 ID 与时间顺序细节
// 都不会保留；它适合「基础压缩」，不能替代真正的语义 Memory 或 RAG。
// 若后续需要高质量摘要，可注入实现同一 Summarizer 接口的模型适配器（其调用必须计入
// RunPolicy 并可取消），本类型不读取 Session，也不会递归调用 Manager.Build。
type DigestSummarizer struct {
	// MaxRunes 是输出摘要的最大 rune 数；<= 0 时使用 DefaultSummaryMaxRunes。
	MaxRunes int
}

var _ Summarizer = DigestSummarizer{}

// Summarize 生成规则化摘要。ctx 已取消/超时返回对应 Kind，不产出部分摘要。
func (s DigestSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	if len(req.Messages) == 0 && strings.TrimSpace(req.PreviousSummary) == "" {
		return "", core.NewError(core.KindInvalidArgument, opSummarize,
			"没有可摘要的历史消息与旧摘要，拒绝生成空摘要")
	}
	limit := s.MaxRunes
	if limit <= 0 {
		limit = DefaultSummaryMaxRunes
	}
	perMessage := limit / 6
	if perMessage < minMessageExcerptRunes {
		perMessage = minMessageExcerptRunes
	}
	if perMessage > maxMessageExcerptRunes {
		perMessage = maxMessageExcerptRunes
	}

	// 上一份摘要被解析为「扁平摘录行 + 累计省略条数」，新摘要重新生成而不嵌套旧摘要，
	// 因此反复压缩后仍然只有一行首行标记，不会出现摘要里的摘要。
	bullets, omitted := parseDigest(req.PreviousSummary)
	unreadable := 0
	for _, msg := range req.Messages {
		line := messageDigestLine(msg, perMessage)
		if line == "" {
			unreadable++
			continue
		}
		bullets = append(bullets, line)
	}
	total := omitted + len(bullets) + unreadable

	// 从最老的摘录行开始让位，直到「首行 + 全部摘录行」装进 limit；不做行内截断，
	// 使每一行都保持「角色 + 原文片段」的可核对形态。
	header := digestHeader(len(bullets), countBulletRoles(bullets), total-len(bullets))
	for len(bullets) > 0 && digestRunes(header, bullets) > limit {
		bullets = bullets[1:]
		header = digestHeader(len(bullets), countBulletRoles(bullets), total-len(bullets))
	}
	lines := make([]string, 0, len(bullets)+1)
	lines = append(lines, header)
	lines = append(lines, bullets...)

	// 兜底：只有 limit 小到装不下首行时才可能触发。
	return clampRunes(strings.Join(lines, "\n"), limit), nil
}

// parseDigest 解析上一份摘要：返回其中的摘录行与累计未展开条数。
//
// 只认本格式的行（"- 用户: " / "- 助手: " / "- 工具 ..." / 省略标记），这样重新摘要时
// 旧摘要的标题与计数不会层层嵌套。遇到非本格式的摘要（例如注入的模型摘要），
// 整段折叠为一条摘录行保留，避免静默丢失调用方写入的记忆。
func parseDigest(previous string) ([]string, int) {
	trimmed := strings.TrimSpace(previous)
	if trimmed == "" {
		return nil, 0
	}
	bullets := make([]string, 0, 8)
	omitted := 0
	for _, raw := range strings.Split(trimmed, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if count, ok := parseOmittedMarker(line); ok {
			omitted += count
			continue
		}
		if strings.HasPrefix(line, "- ") {
			bullets = append(bullets, line)
		}
	}
	if len(bullets) == 0 && omitted == 0 {
		return []string{"- 此前摘要: " + excerpt(trimmed, maxMessageExcerptRunes)}, 0
	}
	return bullets, omitted
}

// omittedMarkerPrefix / omittedMarkerSuffix 是省略标记行的固定前后缀。
const (
	omittedMarkerPrefix = "- （更早 "
	omittedMarkerSuffix = " 条消息因摘要长度上限未展开）"
)

// parseOmittedMarker 解析省略标记行，返回其中的条数。
func parseOmittedMarker(line string) (int, bool) {
	if !strings.HasPrefix(line, omittedMarkerPrefix) || !strings.HasSuffix(line, omittedMarkerSuffix) {
		return 0, false
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(line, omittedMarkerPrefix), omittedMarkerSuffix)
	count, err := strconv.Atoi(raw)
	if err != nil || count <= 0 {
		return 0, false
	}
	return count, true
}

// digestHeader 生成摘要首行：只统计当前保留的摘录行，并明确说明未展开条数。
func digestHeader(bullets int, counts roleCounts, omitted int) string {
	return fmt.Sprintf("%s 规则化摘录（非模型语义总结），保留 %d 条较早历史摘录：user %d / assistant %d / tool %d，另有 %d 条更早消息未展开。",
		summaryHeader, bullets, counts.user, counts.assistant, counts.tool, omitted)
}

// countBulletRoles 按摘录行前缀统计角色，用于首行计数；非本格式行不计入。
func countBulletRoles(bullets []string) roleCounts {
	var counts roleCounts
	for _, line := range bullets {
		switch {
		case strings.HasPrefix(line, "- 用户:"):
			counts.user++
		case strings.HasPrefix(line, "- 助手:"):
			counts.assistant++
		case strings.HasPrefix(line, "- 工具 "):
			counts.tool++
		}
	}
	return counts
}

// digestRunes 返回「首行 + 摘录行」拼接后的 rune 数（含换行）。
func digestRunes(header string, bullets []string) int {
	total := lineRunes(header)
	for _, line := range bullets {
		total += lineRunes(line) + 1
	}
	return total
}

// roleCounts 是摘要首行的角色计数。
type roleCounts struct {
	user      int
	assistant int
	tool      int
}

// messageDigestLine 把一条消息摘录为单行；tool 结果只引用 ToolResult 的文本。
func messageDigestLine(msg core.Message, maxRunes int) string {
	switch msg.Role {
	case roleUser:
		return "- 用户: " + nonEmptyExcerpt(textOfMessage(msg), maxRunes, "（空消息）")
	case roleAssistant:
		text := textOfMessage(msg)
		line := "- 助手: " + nonEmptyExcerpt(text, maxRunes, "（无文本）")
		if names := toolCallNames(msg.ToolCalls); names != "" {
			// 名称不进单条摘录预算：它们通常很短，截断反而丢失「调用了什么」这一关键事实。
			line += " [调用: " + clampRunes(names, maxToolNamesRunes) + "]"
		}
		return line
	case roleTool:
		if msg.ToolResult == nil {
			return ""
		}
		label := "结果"
		if msg.ToolResult.IsError {
			label = "失败"
		}
		name := msg.ToolResult.Name
		if name == "" {
			name = "未知工具"
		}
		content := nonEmptyExcerpt(msg.ToolResult.Content, maxRunes, "（无内容）")
		return fmt.Sprintf("- 工具 %s %s: %s", name, label, content)
	default:
		return ""
	}
}

// toolCallNames 返回消息内工具调用名称的逗号连接形式（缺失名称按未知工具标注）。
func toolCallNames(calls []core.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if call.Name == "" {
			names = append(names, "未知工具")
			continue
		}
		names = append(names, call.Name)
	}
	return strings.Join(names, ", ")
}

// textOfMessage 拼接消息的全部文本片段。
func textOfMessage(msg core.Message) string {
	var builder strings.Builder
	for _, part := range msg.Content {
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// nonEmptyExcerpt 在文本为空时返回占位说明，避免摘要行只剩标签。
func nonEmptyExcerpt(text string, maxRunes int, placeholder string) string {
	trimmed := excerpt(text, maxRunes)
	if trimmed == "" {
		return placeholder
	}
	return trimmed
}

// excerpt 折叠空白后截断到 maxRunes（含省略号），截断发生在 rune 边界上。
func excerpt(text string, maxRunes int) string {
	collapsed := collapseWhitespace(text)
	if collapsed == "" || maxRunes <= 0 {
		return ""
	}
	runes := []rune(collapsed)
	if len(runes) <= maxRunes {
		return collapsed
	}
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// clampRunes 把文本硬截断到 maxRunes（含省略号），保证摘要输出有界。
func clampRunes(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	if maxRunes == 1 {
		return "…"
	}
	return string(runes[:maxRunes-1]) + "…"
}

// collapseWhitespace 把连续空白折叠为单个空格，使每条摘录只占一行。
func collapseWhitespace(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

// lineRunes 返回一行文本的 rune 数（不含换行符）。
func lineRunes(line string) int {
	return utf8.RuneCountInString(line)
}
