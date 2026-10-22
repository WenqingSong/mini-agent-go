package contextmgr

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// digestMessages 构造一段包含中英文与工具调用的早期历史。
func digestMessages() []core.Message {
	messages := make([]core.Message, 0, 12)
	for index := 0; index < 4; index++ {
		label := strconv.Itoa(index)
		messages = append(messages,
			userText("u"+label, "第"+label+"轮问题：请计算 1+2 的结果 value"),
			assistantText("a"+label, "第"+label+"轮回答：结果是 3，符合预期"),
		)
	}
	messages = append(messages,
		assistantCalls("a-tool", "我来计算", call("c1", "calculator", `{"op":"add","a":1,"b":2}`)),
		toolMsg("t1", "c1", "3"),
	)
	messages[len(messages)-1].ToolResult.IsError = true
	return messages
}

// TestDigestSummarizerDeterministicAndBounded 覆盖：同输入逐字节相同、输出有界、
// 保留与近期消息相邻的上下文、明确标注未展开条数、输出是合法 UTF-8。
func TestDigestSummarizerDeterministicAndBounded(t *testing.T) {
	messages := digestMessages()
	summarizer := DigestSummarizer{MaxRunes: 220}
	req := SummarizeRequest{
		ExecutionRef:    core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		PreviousSummary: "",
		Messages:        messages,
	}

	first, err := summarizer.Summarize(context.Background(), req)
	if err != nil {
		t.Fatalf("Summarize() 返回错误：%v", err)
	}
	second, err := summarizer.Summarize(context.Background(), req)
	if err != nil {
		t.Fatalf("第二次 Summarize() 返回错误：%v", err)
	}
	if first != second {
		t.Fatalf("相同输入应产生相同摘要：\nfirst=%q\nsecond=%q", first, second)
	}
	if !utf8.ValidString(first) {
		t.Fatal("摘要必须是合法 UTF-8")
	}
	if got := utf8.RuneCountInString(first); got > 220 {
		t.Fatalf("摘要长度 = %d，超过 MaxRunes=220", got)
	}
	if !strings.Contains(first, summaryHeader) {
		t.Fatalf("摘要必须声明自己是规则化摘录：%q", first)
	}
	if !strings.Contains(first, "calculator") {
		t.Fatalf("摘要必须保留工具调用信息：%q", first)
	}
	if !strings.Contains(first, "未展开") {
		t.Fatalf("超出上限时必须显式说明未展开的消息条数：%q", first)
	}
	// 保留与「保留区间」相邻的最新被压缩消息，而不是最老的几条。
	latest := messageText(t, messages[len(messages)-2])
	if !strings.Contains(first, excerpt(latest, maxMessageExcerptRunes)) {
		t.Fatalf("摘要应包含最新的被压缩消息：%q", first)
	}
}

// TestDigestSummarizerQuotesOriginalTextOnly 覆盖「不虚构」：摘要中每段引用都必须
// 能在原消息文本中找到，不得出现原文没有的事实描述。
func TestDigestSummarizerQuotesOriginalTextOnly(t *testing.T) {
	messages := digestMessages()
	summary, err := DigestSummarizer{MaxRunes: 400}.Summarize(context.Background(), SummarizeRequest{Messages: messages})
	if err != nil {
		t.Fatalf("Summarize() 返回错误：%v", err)
	}
	sources := make([]string, 0, len(messages))
	for _, message := range messages {
		sources = append(sources, collapseWhitespace(textOfMessage(message)))
	}
	for _, line := range strings.Split(summary, "\n") {
		quoted := quotedDigestText(line)
		if quoted == "" {
			continue
		}
		matched := false
		for _, source := range sources {
			if strings.Contains(source, quoted) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("摘要行 %q 的引用 %q 在原消息中不存在（疑似虚构）", line, quoted)
		}
	}
}

// quotedDigestText 从一行摘录中取出被引用的文本（去掉角色前缀与省略号）。
func quotedDigestText(line string) string {
	for _, prefix := range []string{"- 用户: ", "- 助手: ", "- 工具 "} {
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		rest := strings.TrimPrefix(line, prefix)
		if prefix == "- 工具 " {
			// 工具行格式：「- 工具 <name> <结果|失败>: <content>」
			index := strings.Index(rest, ": ")
			if index < 0 {
				return ""
			}
			rest = rest[index+2:]
		}
		if index := strings.Index(rest, " [调用: "); index >= 0 {
			rest = rest[:index]
		}
		rest = strings.TrimSuffix(rest, "…")
		if rest == "（空消息）" || rest == "（无文本）" || rest == "（无内容）" {
			return ""
		}
		return rest
	}
	return ""
}

// TestDigestSummarizerMergesPreviousSummaryWithoutGrowth 覆盖：旧摘要只并入一小段，
// 反复压缩后摘要长度稳定有界，不会随轮次无界累积。
func TestDigestSummarizerMergesPreviousSummaryWithoutGrowth(t *testing.T) {
	const limit = 300
	summarizer := DigestSummarizer{MaxRunes: limit}
	previous := strings.Repeat("很早以前的对话摘要内容。", 500)

	for round := 0; round < 6; round++ {
		req := SummarizeRequest{
			PreviousSummary: previous,
			Messages: []core.Message{
				userText("u", "新一轮问题：继续追问细节"),
				assistantText("a", "新一轮回答：细节如下"),
			},
		}
		summary, err := summarizer.Summarize(context.Background(), req)
		if err != nil {
			t.Fatalf("第 %d 轮 Summarize() 返回错误：%v", round+1, err)
		}
		if got := utf8.RuneCountInString(summary); got > limit {
			t.Fatalf("第 %d 轮摘要长度 = %d，超过上限 %d", round+1, got, limit)
		}
		// 摘要必须保持扁平：每轮只有一行首行标记，不出现「摘要里的摘要」。
		if got := strings.Count(summary, summaryHeader); got != 1 {
			t.Fatalf("第 %d 轮摘要标记出现 %d 次，期望 1 次：%q", round+1, got, summary)
		}
		if round == 0 && !strings.Contains(summary, "此前摘要") {
			t.Fatalf("首轮必须合并传入的旧摘要：%q", summary)
		}
		previous = summary
	}
	// 最后一轮仍必须是非空摘录（信息被裁剪但摘要本身不被清空）。
	if previous == "" || !strings.Contains(previous, summaryHeader) {
		t.Fatalf("多轮压缩后摘要不应为空：%q", previous)
	}
}

// TestDigestSummarizerReportsToolFailures 覆盖：工具结果区分成功/失败，不把失败写成成功。
func TestDigestSummarizerReportsToolFailures(t *testing.T) {
	messages := []core.Message{
		userText("u1", "算一下"),
		assistantCalls("a1", "", call("c1", "calculator", `{"op":"div","a":1,"b":0}`)),
		toolMsg("t1", "c1", "除数不能为 0"),
	}
	messages[2].ToolResult.IsError = true

	summary, err := DigestSummarizer{}.Summarize(context.Background(), SummarizeRequest{Messages: messages})
	if err != nil {
		t.Fatalf("Summarize() 返回错误：%v", err)
	}
	if !strings.Contains(summary, "失败") {
		t.Fatalf("工具失败必须在摘要中标注：%q", summary)
	}
	if strings.Contains(summary, "工具 calculator 结果") {
		t.Fatalf("失败结果不得写成成功：%q", summary)
	}
}

// TestDigestSummarizerRejectsEmptyInput 覆盖：没有消息也没有旧摘要时拒绝生成空摘要。
func TestDigestSummarizerRejectsEmptyInput(t *testing.T) {
	summary, err := DigestSummarizer{}.Summarize(context.Background(), SummarizeRequest{})
	if err == nil {
		t.Fatalf("空输入必须返回错误，实际摘要 %q", summary)
	}
	if kind := core.KindOf(err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 invalid_argument", kind)
	}
	if summary != "" {
		t.Fatalf("失败时不得返回部分摘要：%q", summary)
	}
}

// TestDigestSummarizerHonorsContext 覆盖：ctx 取消/超时映射为 cancelled / deadline_exceeded。
func TestDigestSummarizerHonorsContext(t *testing.T) {
	req := SummarizeRequest{Messages: digestMessages()}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (DigestSummarizer{}).Summarize(cancelled, req); core.KindOf(err) != core.KindCancelled {
		t.Fatalf("取消 ctx 的错误种类 = %q，期望 cancelled", core.KindOf(err))
	}
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	if _, err := (DigestSummarizer{}).Summarize(expired, req); core.KindOf(err) != core.KindDeadlineExceeded {
		t.Fatalf("超时 ctx 的错误种类 = %q，期望 deadline_exceeded", core.KindOf(err))
	}
}

// TestDigestSummarizerDefaultLimit 覆盖：零值使用默认上限，且自定义上限同样生效。
func TestDigestSummarizerDefaultLimit(t *testing.T) {
	long := make([]core.Message, 0, 60)
	for index := 0; index < 30; index++ {
		long = append(long,
			userText("u", strings.Repeat("问题细节", 10)),
			assistantText("a", strings.Repeat("回答细节", 10)),
		)
	}
	defaultSummary, err := DigestSummarizer{}.Summarize(context.Background(), SummarizeRequest{Messages: long})
	if err != nil {
		t.Fatalf("默认上限 Summarize() 返回错误：%v", err)
	}
	if got := utf8.RuneCountInString(defaultSummary); got > DefaultSummaryMaxRunes {
		t.Fatalf("默认摘要长度 = %d，超过默认上限 %d", got, DefaultSummaryMaxRunes)
	}
	customSummary, err := DigestSummarizer{MaxRunes: 90}.Summarize(context.Background(), SummarizeRequest{Messages: long})
	if err != nil {
		t.Fatalf("自定义上限 Summarize() 返回错误：%v", err)
	}
	if got := utf8.RuneCountInString(customSummary); got > 90 {
		t.Fatalf("自定义摘要长度 = %d，超过上限 90", got)
	}
}
