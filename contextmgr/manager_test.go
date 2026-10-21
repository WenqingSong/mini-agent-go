package contextmgr

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// staticEstimator 对每条消息返回固定 token 数，便于用最少算术固定预算边界。
type staticEstimator struct {
	perMessage int
}

func (e staticEstimator) EstimateMessage(core.Message) int { return e.perMessage }

// textEstimator 按 Content 的 rune 数计 token，用于证明预算判断确实随注入估算器变化。
type textEstimator struct{}

func (textEstimator) EstimateMessage(msg core.Message) int {
	total := 0
	for _, part := range msg.Content {
		total += utf8.RuneCountInString(part.Text)
	}
	return total
}

// negativeEstimator 模拟违反契约、返回负值的估算器。
type negativeEstimator struct{}

func (negativeEstimator) EstimateMessage(core.Message) int { return -1 }

// fakeSummarizer 是测试用脚本化摘要器：记录每次调用，不发起任何真实模型或网络调用。
type fakeSummarizer struct {
	summary  string
	err      error
	onCall   func(call int, req SummarizeRequest) (string, error)
	calls    int
	requests []SummarizeRequest
}

func (f *fakeSummarizer) Summarize(ctx context.Context, req SummarizeRequest) (string, error) {
	f.calls++
	f.requests = append(f.requests, req)
	if f.onCall != nil {
		return f.onCall(f.calls, req)
	}
	return f.summary, f.err
}

// newTestManager 构造测试用 Manager，构造失败即视为测试环境错误。
func newTestManager(t *testing.T, est Estimator, sum Summarizer) Manager {
	t.Helper()
	mgr, err := NewManager(est, sum)
	if err != nil {
		t.Fatalf("NewManager() 返回错误：%v", err)
	}
	return mgr
}

func textMsg(id string, role core.Role, text string) core.Message {
	return core.Message{
		ID:      id,
		Role:    role,
		Content: []core.ContentPart{{Type: core.ContentType("text"), Text: text}},
	}
}

func userText(id, text string) core.Message { return textMsg(id, core.Role("user"), text) }

func assistantText(id, text string) core.Message { return textMsg(id, core.Role("assistant"), text) }

func call(id, name, args string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Arguments: []byte(args)}
}

func assistantCalls(id, text string, calls ...core.ToolCall) core.Message {
	msg := textMsg(id, core.Role("assistant"), text)
	msg.ToolCalls = calls
	return msg
}

func toolMsg(id, callID, content string) core.Message {
	return core.Message{
		ID:         id,
		Role:       core.Role("tool"),
		Content:    []core.ContentPart{{Type: core.ContentType("text"), Text: content}},
		ToolResult: &core.ToolResult{CallID: callID, Name: "echo", Content: content},
	}
}

// viewTokens 独立于实现重新累加视图估算值，用于核对 INV-001。
func viewTokens(est Estimator, msgs []core.Message) int {
	total := 0
	for _, msg := range msgs {
		total += est.EstimateMessage(msg)
	}
	return total
}

// messageText 返回消息首个内容片段的文本。
func messageText(t *testing.T, msg core.Message) string {
	t.Helper()
	if len(msg.Content) == 0 {
		return ""
	}
	return msg.Content[0].Text
}

// cloneMessages 深拷贝消息序列，用于断言 Build 不改写调用方输入。
func cloneMessages(msgs []core.Message) []core.Message {
	out := make([]core.Message, len(msgs))
	for i, msg := range msgs {
		clone := msg
		clone.Content = append([]core.ContentPart(nil), msg.Content...)
		if msg.ToolCalls != nil {
			clone.ToolCalls = make([]core.ToolCall, len(msg.ToolCalls))
			for j, c := range msg.ToolCalls {
				clone.ToolCalls[j] = c
				clone.ToolCalls[j].Arguments = append([]byte(nil), c.Arguments...)
			}
		}
		if msg.ToolResult != nil {
			result := *msg.ToolResult
			clone.ToolResult = &result
		}
		clone.Raw = append([]byte(nil), msg.Raw...)
		out[i] = clone
	}
	return out
}

func assertZeroResult(t *testing.T, result Result) {
	t.Helper()
	if !reflect.DeepEqual(result, Result{}) {
		t.Fatalf("错误路径必须返回零值 Result，实际为 %+v", result)
	}
}

// TestBuildWithinBudgetKeepsFullView 覆盖 AC-001：未超预算（含预算恰好等于所需）时保持
// 原始顺序与内容，不压缩、不丢弃，且相同输入重复构建结果一致。
func TestBuildWithinBudgetKeepsFullView(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "不应被调用的摘要"}
	mgr := newTestManager(t, est, sum)

	messages := []core.Message{
		userText("m1", "第一问"),
		assistantText("m2", "第一答"),
		userText("m3", "第二问"),
	}
	base := Request{
		ExecutionRef:    core.ExecutionRef{SessionID: "session-1", RunID: "run-1"},
		SystemPrompt:    "系统提示",
		PreviousSummary: "旧摘要",
		Messages:        messages,
		KeepRecent:      2,
	}

	want := []core.Message{
		textMsg("", core.Role("system"), "系统提示"),
		textMsg("", core.Role("system"), "旧摘要"),
		messages[0], messages[1], messages[2],
	}

	// 完整视图估算 = 1(system) + 1(旧摘要) + 3 = 5，预算 5 恰好等于所需。
	for _, budget := range []int{5, 6, 100} {
		req := base
		req.TokenBudget = budget
		result, err := mgr.Build(context.Background(), req)
		if err != nil {
			t.Fatalf("TokenBudget=%d 时 Build() 返回错误：%v", budget, err)
		}
		if result.Compacted || result.DroppedMessages != 0 || result.Summary != "" {
			t.Fatalf("TokenBudget=%d 未超预算不应压缩：%+v", budget, result)
		}
		if !reflect.DeepEqual(result.Messages, want) {
			t.Fatalf("TokenBudget=%d 视图与输入不一致：\n got=%+v\nwant=%+v", budget, result.Messages, want)
		}
		if got := viewTokens(est, result.Messages); got > budget {
			t.Fatalf("TokenBudget=%d 估算值 %d 超出预算", budget, got)
		}
		if err := core.ValidateMessages(result.Messages); err != nil {
			t.Fatalf("TokenBudget=%d 视图未通过 core.ValidateMessages：%v", budget, err)
		}
	}

	// INV-006：相同 Request + 相同注入实现 → 逐条相同结果。
	first, err := mgr.Build(context.Background(), base)
	if err != nil {
		t.Fatalf("首次 Build() 返回错误：%v", err)
	}
	second, err := mgr.Build(context.Background(), base)
	if err != nil {
		t.Fatalf("第二次 Build() 返回错误：%v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("相同输入两次构建结果不一致：\n first=%+v\nsecond=%+v", first, second)
	}
	if sum.calls != 0 {
		t.Fatalf("未超预算不应调用 Summarizer，实际调用 %d 次", sum.calls)
	}
}

// TestBuildNoBudgetLimitWhenTokenBudgetNonPositive 覆盖 AC-001：TokenBudget <= 0 表示不限制。
func TestBuildNoBudgetLimitWhenTokenBudgetNonPositive(t *testing.T) {
	est := staticEstimator{perMessage: 100}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	messages := make([]core.Message, 0, 20)
	for i := 0; i < 20; i++ {
		messages = append(messages, userText("m"+string(rune('a'+i)), "内容"))
	}

	for _, budget := range []int{0, -1, -1000} {
		result, err := mgr.Build(context.Background(), Request{
			SystemPrompt: "系统提示",
			Messages:     messages,
			TokenBudget:  budget,
			KeepRecent:   5,
		})
		if err != nil {
			t.Fatalf("TokenBudget=%d 时不应限制预算，实际返回错误：%v", budget, err)
		}
		if result.Compacted || result.DroppedMessages != 0 || result.Summary != "" {
			t.Fatalf("TokenBudget=%d 不应压缩：%+v", budget, result)
		}
		if len(result.Messages) != len(messages)+1 {
			t.Fatalf("TokenBudget=%d 视图长度 = %d，期望 %d", budget, len(result.Messages), len(messages)+1)
		}
	}
	if sum.calls != 0 {
		t.Fatalf("不限制预算时不应调用 Summarizer，实际调用 %d 次", sum.calls)
	}
}

// TestBuildCompactsOldHistory 覆盖 AC-002 / INV-002 / INV-005：长历史超预算时把较早历史
// 压缩为注入 Summarizer 给出的摘要，保留最近 KeepRecent 条，DroppedMessages 与实际覆盖条数一致。
func TestBuildCompactsOldHistory(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "早期对话摘要"}
	mgr := newTestManager(t, est, sum)

	messages := make([]core.Message, 0, 30)
	for i := 0; i < 15; i++ {
		messages = append(messages, userText("u"+string(rune('a'+i)), "问题"))
		messages = append(messages, assistantText("a"+string(rune('a'+i)), "回答"))
	}
	req := Request{
		ExecutionRef:    core.ExecutionRef{SessionID: "session-2", RunID: "run-2"},
		SystemPrompt:    "系统提示",
		PreviousSummary: "上一次的摘要",
		Messages:        messages,
		TokenBudget:     9,
		KeepRecent:      5,
	}

	result, err := mgr.Build(context.Background(), req)
	if err != nil {
		t.Fatalf("Build() 返回错误：%v", err)
	}

	// 完整视图 = 2 + 30 = 32；保留尾部 5 条后视图 = 1(system) + 1(摘要) + 5 = 7 <= 9。
	const wantDropped = 25
	if !result.Compacted {
		t.Fatalf("超预算时应发生压缩：%+v", result)
	}
	if result.DroppedMessages != wantDropped {
		t.Fatalf("DroppedMessages = %d，期望 %d", result.DroppedMessages, wantDropped)
	}
	if result.Summary != "早期对话摘要" {
		t.Fatalf("Summary = %q，期望来自注入 Summarizer 的摘要", result.Summary)
	}
	if len(result.Messages) != 2+len(messages)-wantDropped {
		t.Fatalf("视图长度 = %d，期望 %d", len(result.Messages), 2+len(messages)-wantDropped)
	}
	if got := messageText(t, result.Messages[0]); got != "系统提示" {
		t.Fatalf("首条消息应为 System Prompt，实际为 %q", got)
	}
	if got := messageText(t, result.Messages[1]); got != result.Summary {
		t.Fatalf("第二条消息应为新摘要且与 Result.Summary 逐字节一致，实际为 %q", got)
	}
	assertSameMessages(t, "保留尾部", result.Messages[2:], messages[wantDropped:])
	if result.Messages[1].Role != core.Role("system") {
		t.Fatalf("摘要必须是 system 消息，实际角色为 %q", result.Messages[1].Role)
	}
	if err := core.ValidateMessages(result.Messages); err != nil {
		t.Fatalf("压缩后的视图未通过 core.ValidateMessages：%v", err)
	}
	if got := viewTokens(est, result.Messages); got > req.TokenBudget {
		t.Fatalf("视图估算值 %d 超出预算 %d", got, req.TokenBudget)
	}

	// 摘要输入恰为被覆盖的输入前缀，且旧摘要并入新摘要。
	if sum.calls != 1 {
		t.Fatalf("Summarizer 调用次数 = %d，期望 1", sum.calls)
	}
	got := sum.requests[0]
	if !reflect.DeepEqual(got.Messages, messages[:wantDropped]) {
		t.Fatalf("Summarizer 收到的待压缩消息必须恰为输入前 %d 条", wantDropped)
	}
	if got.PreviousSummary != req.PreviousSummary {
		t.Fatalf("Summarizer 必须收到旧摘要以合并，实际为 %q", got.PreviousSummary)
	}
	if got.ExecutionRef != req.ExecutionRef {
		t.Fatalf("Summarizer 必须收到执行身份，实际为 %+v", got.ExecutionRef)
	}
}

// TestBuildFailsWhenKeepRecentTargetExceedsBudget 是 CLEAN-001 的回归测试：KeepRecent 是保留
// 条数的硬下限，预算装不下「摘要 + KeepRecent 条近期消息」时必须显式失败，不得通过继续吞并
// 尾部把保留条数降到 KeepRecent 以下后报告成功。
func TestBuildFailsWhenKeepRecentTargetExceedsBudget(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	messages := make([]core.Message, 0, 10)
	for i := 0; i < 10; i++ {
		messages = append(messages, userText("m"+string(rune('a'+i)), "内容"))
	}

	// 保留 3 条时视图 = 1(摘要) + 3 = 4 > 3：不允许退化成保留 2 条或 0 条。
	result, err := mgr.Build(context.Background(), Request{
		Messages:    messages,
		TokenBudget: 3,
		KeepRecent:  3,
	})
	if err == nil {
		t.Fatalf("预算装不下 KeepRecent=3 条近期消息时应失败，实际返回 %+v", result)
	}
	if kind := core.KindOf(err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInvalidArgument, err)
	}
	if !strings.Contains(err.Error(), "KeepRecent") {
		t.Fatalf("错误文本 %q 未指出 KeepRecent 保留下限", err.Error())
	}
	assertZeroResult(t, result)
	if sum.calls != 1 {
		t.Fatalf("Summarizer 调用次数 = %d，期望 1（保留条数不得靠重复摘要继续收缩）", sum.calls)
	}

	// 预算刚好容得下保留下限时成功，且保留条数恰为 3（不多压一条）。
	sum = &fakeSummarizer{summary: "摘要"}
	mgr = newTestManager(t, est, sum)
	result, err = mgr.Build(context.Background(), Request{
		Messages:    messages,
		TokenBudget: 4,
		KeepRecent:  3,
	})
	if err != nil {
		t.Fatalf("预算恰好容得下保留下限时 Build() 返回错误：%v", err)
	}
	if len(result.Messages) != 1+3 {
		t.Fatalf("视图长度 = %d，期望 4（摘要 + KeepRecent 条）", len(result.Messages))
	}
	if result.DroppedMessages != 7 {
		t.Fatalf("DroppedMessages = %d，期望 7", result.DroppedMessages)
	}
	assertSameMessages(t, "视图尾部", result.Messages[1:], messages[7:])
	if got := viewTokens(est, result.Messages); got > 4 {
		t.Fatalf("视图估算值 %d 超出预算 4", got)
	}

	// 场景二：最新一条 user 消息本身就撑破预算（Cleaner 复现用例）。此时即使只保留 1 条
	// 近期消息也无法满足预算，必须失败，不得把保留区间吞并到空后返回 [system summary]。
	probeSum := &fakeSummarizer{summary: "摘要"}
	probeMgr := newTestManager(t, textEstimator{}, probeSum)
	probeResult, err := probeMgr.Build(context.Background(), Request{
		SystemPrompt: "sys",
		Messages: []core.Message{
			userText("m1", "旧问"),
			assistantText("m2", "旧答"),
			userText("m3", strings.Repeat("长", 200)),
		},
		TokenBudget: 5,
		KeepRecent:  1,
	})
	if err == nil {
		t.Fatalf("最新消息单独撑破预算时应失败，实际返回 %+v", probeResult)
	}
	if kind := core.KindOf(err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInvalidArgument, err)
	}
	assertZeroResult(t, probeResult)
}

// TestBuildKeepsMinimumRecentMessages 是 CLEAN-004 的回归测试：KeepRecent 按「消息条数」而非
// 「原子单元个数」计，多消息单元（工具组合）下保留区间条数必须 >= KeepRecent。
// 若实现把 KeepRecent 误解为单元数，或把保留尾部吞并到低于下限，本测试失败。
func TestBuildKeepsMinimumRecentMessages(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	// 原子单元：m1 | m2+m3+m4 | m5 | m6+m7 | m8，共 5 个单元、8 条消息。
	messages := []core.Message{
		userText("m1", "开始"),
		assistantCalls("m2", "", call("c1", "read_file", `{"path":"a"}`), call("c2", "read_file", `{"path":"b"}`)),
		toolMsg("m3", "c1", "A"),
		toolMsg("m4", "c2", "B"),
		userText("m5", "继续"),
		assistantCalls("m6", "", call("c3", "echo", `{"text":"hi"}`)),
		toolMsg("m7", "c3", "ok"),
		userText("m8", "结束"),
	}

	// KeepRecent=4：最小尾部 = m5,m6,m7,m8（4 条、3 个单元）；若按单元数取 4 个单元
	// 就会保留 7 条（m2..m8），因此本用例能区分两种实现。视图 = 1(摘要) + 4 = 5。
	result, err := mgr.Build(context.Background(), Request{
		Messages:    messages,
		TokenBudget: 5,
		KeepRecent:  4,
	})
	if err != nil {
		t.Fatalf("Build() 返回错误：%v", err)
	}
	retained := result.Messages[1:]
	if len(retained) < 4 {
		t.Fatalf("保留条数 = %d，少于 KeepRecent=4", len(retained))
	}
	if len(retained) != 4 {
		t.Fatalf("保留条数 = %d，期望 4（最小满足保留下限的尾部）", len(retained))
	}
	if result.DroppedMessages != 4 {
		t.Fatalf("DroppedMessages = %d，期望 4", result.DroppedMessages)
	}
	assertSameMessages(t, "视图尾部", retained, messages[4:])
	if err := core.ValidateMessages(result.Messages); err != nil {
		t.Fatalf("视图未通过 core.ValidateMessages：%v", err)
	}
	if got := viewTokens(est, result.Messages); got > 5 {
		t.Fatalf("视图估算值 %d 超出预算 5", got)
	}

	// 预算装不下 4 条近期消息（1 + 4 = 5 > 4）时必须失败，不得退回保留 3 条或更少。
	sum = &fakeSummarizer{summary: "摘要"}
	mgr = newTestManager(t, est, sum)
	result, err = mgr.Build(context.Background(), Request{
		Messages:    messages,
		TokenBudget: 4,
		KeepRecent:  4,
	})
	if err == nil {
		t.Fatalf("预算装不下 KeepRecent=4 条近期消息时应失败，实际返回 %+v", result)
	}
	if kind := core.KindOf(err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInvalidArgument, err)
	}
	assertZeroResult(t, result)
}

// TestBuildToolUnitsAreAtomic 覆盖 AC-003 / INV-003：预算与保留边界落在「assistant 携带的
// ToolCalls + 其全部 ToolResults」内部时，整组要么保留要么整体压缩，输出始终通过校验。
func TestBuildToolUnitsAreAtomic(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	messages := []core.Message{
		userText("m1", "开始"),
		assistantCalls("m2", "并发两个工具", call("c1", "read_file", `{"path":"a"}`), call("c2", "read_file", `{"path":"b"}`)),
		toolMsg("m3", "c1", "A"),
		toolMsg("m4", "c2", "B"),
		userText("m5", "继续"),
		assistantCalls("m6", "", call("c3", "echo", `{"text":"hi"}`)),
		toolMsg("m7", "c3", "ok"),
		assistantText("m8", "完成"),
		userText("m9", "谢谢"),
	}

	// 视图前缀固定为 System Prompt + 摘要（staticEstimator 各计 1 token）。
	const prefixTokens = 2
	fullTokens := prefixTokens + len(messages)

	// 每个 KeepRecent 下，满足保留下限的「最小尾部消息条数」（单元划分：m1 | m2..m4 | m5 | m6,m7 | m8 | m9）。
	// 按消息条数取尾部：0、1、2、4、4、9；必须恰为这些值，才能同时证明「按条数计数」与「不吞并到下限以下」。
	minRetained := map[int]int{0: 0, 1: 1, 2: 2, 3: 4, 4: 4, 9: 9}

	for _, keepRecent := range []int{0, 1, 2, 3, 4, 9} {
		need := prefixTokens + minRetained[keepRecent]
		for budget := 1; budget <= 12; budget++ {
			// 预算容得下「前缀 + 保留下限」时构建必须成功；否则必须显式失败。
			wantSuccess := budget >= need
			label := fmt.Sprintf("keepRecent=%d budget=%d", keepRecent, budget)

			sum := &fakeSummarizer{summary: "摘要"}
			mgr := newTestManager(t, est, sum)
			result, err := mgr.Build(context.Background(), Request{
				SystemPrompt:    "系统提示",
				PreviousSummary: "旧摘要",
				Messages:        cloneMessages(messages),
				TokenBudget:     budget,
				KeepRecent:      keepRecent,
			})
			if err != nil {
				if wantSuccess {
					t.Fatalf("%s 该预算下必须成功，实际返回错误：%v", label, err)
				}
				if kind := core.KindOf(err); kind != core.KindInvalidArgument {
					t.Fatalf("%s 错误种类 = %q，期望 invalid_argument（预算不可满足不降级）", label, kind)
				}
				assertZeroResult(t, result)
				continue
			}
			if !wantSuccess {
				t.Fatalf("%s 预算不可满足时应显式失败，实际返回 %+v", label, result)
			}

			if err := core.ValidateMessages(result.Messages); err != nil {
				t.Fatalf("%s 视图非法：%v", label, err)
			}
			if got := viewTokens(est, result.Messages); got > budget {
				t.Fatalf("%s 视图估算值 %d 超预算", label, got)
			}
			if len(result.Messages) < prefixTokens {
				t.Fatalf("%s 视图必须至少包含 system 与摘要消息", label)
			}

			// 视图尾部必须是输入的后缀：不重排、不改写、边界只落在单元之间。
			retained := result.Messages[prefixTokens:]
			assertSameMessages(t, label+" 保留区间", retained, messages[len(messages)-len(retained):])
			if len(retained) > 0 && retained[0].Role == core.Role("tool") {
				t.Fatalf("%s 保留区间不得以 tool 结果开头（单元被截断）", label)
			}
			if len(retained) < keepRecent {
				t.Fatalf("%s 保留条数 = %d，低于 KeepRecent 下限", label, len(retained))
			}
			if budget < fullTokens {
				// 必须压缩，且保留条数恰为满足保留下限的最小尾部。
				if !result.Compacted {
					t.Fatalf("%s 超预算必须压缩，实际 %+v", label, result)
				}
				if len(retained) != minRetained[keepRecent] {
					t.Fatalf("%s 保留条数 = %d，期望 %d", label, len(retained), minRetained[keepRecent])
				}
				if result.DroppedMessages != len(messages)-len(retained) {
					t.Fatalf("%s DroppedMessages = %d，期望 %d", label, result.DroppedMessages, len(messages)-len(retained))
				}
				if result.Summary == "" || messageText(t, result.Messages[1]) != result.Summary {
					t.Fatalf("%s 摘要文本与 Summary 不一致", label)
				}
			} else if result.Compacted || result.DroppedMessages != 0 || result.Summary != "" {
				t.Fatalf("%s 未超预算不应压缩，实际 %+v", label, result)
			}
		}
	}
}

// assertSameMessages 逐条比较消息序列，避免 nil / 空切片差异干扰「顺序与内容不变」断言。
func assertSameMessages(t *testing.T, label string, got, want []core.Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s：消息条数 %d，期望 %d", label, len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("%s：第 %d 条消息与输入不一致：\n got=%+v\nwant=%+v", label, i, got[i], want[i])
		}
	}
}

// TestBuildSummarizerFailures 覆盖 AC-004：摘要失败、空摘要与全空白摘要都必须显式失败，
// 不返回被标记为已压缩的视图，也不用占位摘要冒充完整记忆。
func TestBuildSummarizerFailures(t *testing.T) {
	sentinel := errors.New("摘要模型不可用")

	cases := []struct {
		name string
		sum  *fakeSummarizer
	}{
		{name: "Summarizer 返回错误", sum: &fakeSummarizer{err: sentinel}},
		{name: "Summarizer 返回空摘要", sum: &fakeSummarizer{summary: ""}},
		{name: "Summarizer 返回全空白摘要", sum: &fakeSummarizer{summary: " \n\t "}},
	}

	messages := []core.Message{userText("m1", "旧问题"), assistantText("m2", "旧回答"), userText("m3", "新问题")}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestManager(t, staticEstimator{perMessage: 1}, tc.sum)
			result, err := mgr.Build(context.Background(), Request{
				SystemPrompt: "系统提示",
				Messages:     messages,
				TokenBudget:  3,
				KeepRecent:   1,
			})
			if err == nil {
				t.Fatalf("Build() 应返回错误，实际返回 %+v", result)
			}
			if kind := core.KindOf(err); kind != core.KindInternal {
				t.Fatalf("错误种类 = %q，期望 %q", kind, core.KindInternal)
			}
			assertZeroResult(t, result)
			var runErr *core.RunError
			if !errors.As(err, &runErr) {
				t.Fatalf("错误必须是 *core.RunError，实际为 %T", err)
			}
			if tc.sum.err != nil && !errors.Is(err, sentinel) {
				t.Fatalf("Summarizer 原始错误必须保留在 Cause 链中：%v", err)
			}
		})
	}
}

// TestBuildRejectsInvalidConfiguration 覆盖 AC-005：非法配置与预算不可满足返回可区分原因的
// 显式错误，失败时不返回任何部分视图。
func TestBuildRejectsInvalidConfiguration(t *testing.T) {
	threeMessages := []core.Message{
		userText("m1", "第一问"),
		assistantText("m2", "第一答"),
		userText("m3", "第二问"),
	}

	cases := []struct {
		name       string
		estimator  Estimator
		request    Request
		wantKind   core.ErrorKind
		wantText   string
		wantCallID string
	}{
		{
			name:      "负 KeepRecent",
			estimator: staticEstimator{perMessage: 1},
			request:   Request{Messages: threeMessages, TokenBudget: 1, KeepRecent: -1},
			wantKind:  core.KindInvalidArgument,
			wantText:  "KeepRecent",
		},
		{
			name:      "Messages 携带 system 消息",
			estimator: staticEstimator{perMessage: 1},
			request: Request{
				SystemPrompt: "系统提示",
				Messages:     []core.Message{textMsg("m1", core.Role("system"), "重复的系统提示")},
				TokenBudget:  100,
			},
			wantKind: core.KindInvalidArgument,
			wantText: "SystemPrompt",
		},
		{
			name:      "输入配对非法时原样返回 core 错误并定位 CallID",
			estimator: staticEstimator{perMessage: 1},
			request: Request{
				Messages:    []core.Message{userText("m1", "问"), toolMsg("m2", "call_missing", "结果")},
				TokenBudget: 100,
			},
			wantKind:   core.KindInvalidPairing,
			wantText:   "call_missing",
			wantCallID: "call_missing",
		},
		{
			name:      "KeepRecent 不小于消息总数时没有可压缩的更早历史",
			estimator: staticEstimator{perMessage: 1},
			request:   Request{Messages: threeMessages, TokenBudget: 1, KeepRecent: 3},
			wantKind:  core.KindInvalidArgument,
			wantText:  "KeepRecent",
		},
		{
			name:      "预算小于 System Prompt",
			estimator: textEstimator{},
			request: Request{
				SystemPrompt: "这是一个很长的系统提示",
				Messages:     []core.Message{userText("m1", "短")},
				TokenBudget:  3,
				KeepRecent:   0,
			},
			wantKind: core.KindInvalidArgument,
			wantText: "System Prompt",
		},
		{
			name:      "超长单条消息且无更早历史可压缩",
			estimator: textEstimator{},
			request: Request{
				Messages:    []core.Message{userText("m1", strings.Repeat("长", 100))},
				TokenBudget: 10,
				KeepRecent:  1,
			},
			wantKind: core.KindInvalidArgument,
			wantText: "KeepRecent",
		},
		{
			name:      "只有历史摘要超预算且没有可压缩历史",
			estimator: textEstimator{},
			request: Request{
				PreviousSummary: strings.Repeat("旧", 50),
				TokenBudget:     10,
			},
			wantKind: core.KindInvalidArgument,
			wantText: "历史摘要",
		},
		{
			name:      "估算器返回负值",
			estimator: negativeEstimator{},
			request:   Request{Messages: threeMessages, TokenBudget: 10, KeepRecent: 1},
			wantKind:  core.KindInvalidArgument,
			wantText:  "估算器",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr := newTestManager(t, tc.estimator, &fakeSummarizer{summary: "摘要"})
			result, err := mgr.Build(context.Background(), tc.request)
			if err == nil {
				t.Fatalf("Build() 应返回错误，实际返回 %+v", result)
			}
			if kind := core.KindOf(err); kind != tc.wantKind {
				t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, tc.wantKind, err)
			}
			if !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("错误文本 %q 未包含可识别原因 %q", err.Error(), tc.wantText)
			}
			var runErr *core.RunError
			if !errors.As(err, &runErr) {
				t.Fatalf("错误必须是 *core.RunError，实际为 %T", err)
			}
			if runErr.CallID != tc.wantCallID {
				t.Fatalf("CallID = %q，期望 %q", runErr.CallID, tc.wantCallID)
			}
			assertZeroResult(t, result)
		})
	}
}

// TestBuildFailsWhenPrefixAndRetainedTailExceedBudget 覆盖 CLEAN-001：即使把可按单元压缩的历史
// 全部压缩，只要 System Prompt + 摘要 + KeepRecent 条近期消息仍超预算，就必须返回
// invalid_argument，而不是继续吞并尾部或返回部分记忆。
func TestBuildFailsWhenPrefixAndRetainedTailExceedBudget(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	result, err := mgr.Build(context.Background(), Request{
		SystemPrompt: "系统提示",
		Messages:     []core.Message{userText("m1", "旧问"), userText("m2", "旧答")},
		TokenBudget:  2,
		KeepRecent:   1,
	})
	if err == nil {
		t.Fatalf("Build() 应返回错误，实际返回 %+v", result)
	}
	if kind := core.KindOf(err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInvalidArgument, err)
	}
	if !strings.Contains(err.Error(), "KeepRecent") {
		t.Fatalf("错误文本 %q 未指出保留下限约束", err.Error())
	}
	// 只允许压缩一次：不得靠重复摘要把保留条数继续压到下限以下。
	if sum.calls != 1 {
		t.Fatalf("Summarizer 调用次数 = %d，期望 1", sum.calls)
	}
	assertZeroResult(t, result)
}

// TestNewManagerRejectsNilDependencies 覆盖 AC-005：构造期 estimator / summarizer 为 nil 时
// 必须显式失败，避免装配层拿到会在运行期 panic 或静默降级的 Manager。
func TestNewManagerRejectsNilDependencies(t *testing.T) {
	cases := []struct {
		name       string
		estimator  Estimator
		summarizer Summarizer
	}{
		{name: "estimator 为 nil", estimator: nil, summarizer: &fakeSummarizer{summary: "摘要"}},
		{name: "summarizer 为 nil", estimator: staticEstimator{perMessage: 1}, summarizer: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mgr, err := NewManager(tc.estimator, tc.summarizer)
			if err == nil {
				t.Fatalf("NewManager() 应返回错误，实际返回 %v", mgr)
			}
			if mgr != nil {
				t.Fatalf("构造失败时不应返回 Manager")
			}
			if kind := core.KindOf(err); kind != core.KindInvalidArgument {
				t.Fatalf("错误种类 = %q，期望 %q", kind, core.KindInvalidArgument)
			}
		})
	}
}

// TestBuildContextCancellation 覆盖失败语义表：ctx 取消/超时映射为 cancelled / deadline_exceeded，
// 不透传为 internal。
func TestBuildContextCancellation(t *testing.T) {
	mgr := newTestManager(t, staticEstimator{perMessage: 1}, &fakeSummarizer{summary: "摘要"})
	req := Request{SystemPrompt: "系统提示", Messages: []core.Message{userText("m1", "问")}, TokenBudget: 100}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := mgr.Build(cancelled, req)
	if err == nil {
		t.Fatalf("已取消的 ctx 应返回错误")
	}
	if kind := core.KindOf(err); kind != core.KindCancelled {
		t.Fatalf("错误种类 = %q，期望 %q", kind, core.KindCancelled)
	}
	assertZeroResult(t, result)

	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	result, err = mgr.Build(expired, req)
	if err == nil {
		t.Fatalf("已超时的 ctx 应返回错误")
	}
	if kind := core.KindOf(err); kind != core.KindDeadlineExceeded {
		t.Fatalf("错误种类 = %q，期望 %q", kind, core.KindDeadlineExceeded)
	}
	assertZeroResult(t, result)
}

// TestBuildMapsSummarizerContextErrors 是 CLEAN-002 的回归测试：Summarizer 因 ctx 取消/超时
// 失败时必须映射为 cancelled / deadline_exceeded（保留 Cause），不得降级为 internal。
func TestBuildMapsSummarizerContextErrors(t *testing.T) {
	messages := []core.Message{userText("m1", "旧问"), assistantText("m2", "旧答"), userText("m3", "新问")}
	// 前缀（摘要）+ 1 条近期消息 = 2，预算 2：必然触发一次 Summarizer 调用。
	req := Request{Messages: messages, TokenBudget: 2, KeepRecent: 1}

	cases := []struct {
		name      string
		newSum    func(cancel context.CancelFunc) Summarizer
		wantKind  core.ErrorKind
		wantIs    error
		wantCause bool
	}{
		{
			name: "Summarizer 直接返回 context.Canceled",
			newSum: func(context.CancelFunc) Summarizer {
				return &fakeSummarizer{err: context.Canceled}
			},
			wantKind:  core.KindCancelled,
			wantIs:    context.Canceled,
			wantCause: true,
		},
		{
			name: "Summarizer 返回包装后的 context.DeadlineExceeded",
			newSum: func(context.CancelFunc) Summarizer {
				return &fakeSummarizer{err: fmt.Errorf("摘要调用超时: %w", context.DeadlineExceeded)}
			},
			wantKind:  core.KindDeadlineExceeded,
			wantIs:    context.DeadlineExceeded,
			wantCause: true,
		},
		{
			name: "Summarizer 内部取消 ctx 后返回普通错误",
			newSum: func(cancel context.CancelFunc) Summarizer {
				return &fakeSummarizer{onCall: func(int, SummarizeRequest) (string, error) {
					cancel()
					return "", errors.New("摘要调用被中断")
				}}
			},
			// 此例原始错误是普通 error（不是 ctx 错误），Kind 由 ctx 终态决定，Cause 保留原始错误。
			wantKind:  core.KindCancelled,
			wantCause: true,
		},
		{
			name: "Summarizer 成功但期间 ctx 已被取消",
			newSum: func(cancel context.CancelFunc) Summarizer {
				return &fakeSummarizer{onCall: func(int, SummarizeRequest) (string, error) {
					cancel()
					return "摘要", nil
				}}
			},
			// 取消后不返回成功视图：按 ctx 终态失败，无 Cause。
			wantKind: core.KindCancelled,
		},
		{
			name: "Summarizer 返回普通错误仍为 internal",
			newSum: func(context.CancelFunc) Summarizer {
				return &fakeSummarizer{err: errors.New("摘要服务返回 500")}
			},
			wantKind:  core.KindInternal,
			wantCause: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			mgr := newTestManager(t, staticEstimator{perMessage: 1}, tc.newSum(cancel))
			result, err := mgr.Build(ctx, req)
			if err == nil {
				t.Fatalf("Build() 应返回错误，实际返回 %+v", result)
			}
			if kind := core.KindOf(err); kind != tc.wantKind {
				t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, tc.wantKind, err)
			}
			var runErr *core.RunError
			if !errors.As(err, &runErr) {
				t.Fatalf("错误必须是 *core.RunError，实际为 %T", err)
			}
			if tc.wantCause && runErr.Cause == nil {
				t.Fatalf("原始错误必须保留在 Cause 中")
			}
			if tc.wantIs != nil && !errors.Is(err, tc.wantIs) {
				t.Fatalf("错误链必须可用 errors.Is 追溯 %v：%v", tc.wantIs, err)
			}
			assertZeroResult(t, result)
		})
	}
}

// TestBuildEmptyHistoryAndExactBudget 覆盖 AC-005 的空历史边界：没有历史消息时按预算渲染
// System Prompt 与历史摘要，不触发压缩；预算恰好等于所需时同样不压缩。
func TestBuildEmptyHistoryAndExactBudget(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	result, err := mgr.Build(context.Background(), Request{TokenBudget: 10, KeepRecent: 3})
	if err != nil {
		t.Fatalf("空历史且空 System Prompt 时 Build() 返回错误：%v", err)
	}
	if len(result.Messages) != 0 || result.Compacted || result.DroppedMessages != 0 || result.Summary != "" {
		t.Fatalf("空输入应返回空视图：%+v", result)
	}

	// System Prompt + 历史摘要 = 2，预算恰好为 2。
	result, err = mgr.Build(context.Background(), Request{
		SystemPrompt:    "系统提示",
		PreviousSummary: "旧摘要",
		TokenBudget:     2,
	})
	if err != nil {
		t.Fatalf("预算恰好等于所需时 Build() 返回错误：%v", err)
	}
	if len(result.Messages) != 2 || result.Compacted {
		t.Fatalf("预算恰好等于所需时不应压缩：%+v", result)
	}
	if sum.calls != 0 {
		t.Fatalf("不应调用 Summarizer，实际调用 %d 次", sum.calls)
	}
}

// TestBuildBudgetFollowsInjectedEstimator 覆盖 INV-006：预算判断确实基于注入估算器，
// 同一请求在不同估算器下得到不同的压缩决策。
func TestBuildBudgetFollowsInjectedEstimator(t *testing.T) {
	messages := []core.Message{
		userText("m1", strings.Repeat("细", 20)),
		assistantText("m2", strings.Repeat("节", 20)),
		userText("m3", strings.Repeat("内容", 10)),
	}

	// 同一预算 22：固定 1 token/条 → 3 <= 22 不压缩；按字符计 → 60 > 22，压缩到保留 1 条
	// （摘要 2 + 最后一条 20 = 22）刚好满足预算与保留下限。
	const budget = 22

	fixed := newTestManager(t, staticEstimator{perMessage: 1}, &fakeSummarizer{summary: "摘要"})
	result, err := fixed.Build(context.Background(), Request{Messages: messages, TokenBudget: budget, KeepRecent: 1})
	if err != nil {
		t.Fatalf("固定估算器下 Build() 返回错误：%v", err)
	}
	if result.Compacted {
		t.Fatalf("固定 1 token/条时 3 条消息不应超预算：%+v", result)
	}

	byText := newTestManager(t, textEstimator{}, &fakeSummarizer{summary: "摘要"})
	result, err = byText.Build(context.Background(), Request{Messages: messages, TokenBudget: budget, KeepRecent: 1})
	if err != nil {
		t.Fatalf("文本估算器下 Build() 返回错误：%v", err)
	}
	if !result.Compacted {
		t.Fatalf("按字符估算时 3 条长消息必须触发压缩：%+v", result)
	}
	if len(result.Messages) != 2 || result.DroppedMessages != 2 {
		t.Fatalf("保留区间应为 1 条：%+v", result)
	}
}

// TestBuildDoesNotMutateInput 覆盖 INV-002 的写入约束：Build 不重排、不改写调用方消息。
func TestBuildDoesNotMutateInput(t *testing.T) {
	messages := []core.Message{
		userText("m1", "旧问"),
		assistantCalls("m2", "", call("c1", "echo", `{"text":"hi"}`)),
		toolMsg("m3", "c1", "ok"),
		userText("m4", "新问"),
	}
	original := cloneMessages(messages)

	mgr := newTestManager(t, staticEstimator{perMessage: 1}, &fakeSummarizer{summary: "摘要"})
	if _, err := mgr.Build(context.Background(), Request{
		SystemPrompt:    "系统提示",
		PreviousSummary: "旧摘要",
		Messages:        messages,
		TokenBudget:     3,
		KeepRecent:      1,
	}); err != nil {
		t.Fatalf("Build() 返回错误：%v", err)
	}

	if !reflect.DeepEqual(messages, original) {
		t.Fatalf("Build() 不得修改调用方输入消息")
	}
}

// TestBuildCompactionBudgetBoundaryExactFit 固定 R1「单次压缩 + KeepRecent 硬下限」在预算临界点
// 的两侧行为（Contract 压缩算法 4–5 步、INV-008）：
//
//   - 压缩后视图估算恰好等于 TokenBudget → 成功（Compacted=true、DroppedMessages 与实差一致、
//     保留条数 >= KeepRecent、Summarizer 恰好调用 1 次、估算值精确等于预算而不是略小于）；
//   - 预算再少 1 token → invalid_argument、零值 Result、Summarizer 仍恰好调用 1 次（不重试、
//     不吞并保留消息）。
//
// 夹具刻意使用多消息工具 Unit 与正 KeepRecent，使保留边界落在「assistant 携带的 ToolCalls +
// 其全部 ToolResults」之外，保证保留条数按消息条数计算。
func TestBuildCompactionBudgetBoundaryExactFit(t *testing.T) {
	est := staticEstimator{perMessage: 1}

	// 原子单元：u0=[m1] | u1=[m2,m3] | u2=[m4] | u3=[m5,m6,m7]，共 7 条消息。
	messages := []core.Message{
		userText("m1", "旧问"),
		assistantCalls("m2", "", call("c1", "read_file", `{"path":"a"}`)),
		toolMsg("m3", "c1", "A"),
		userText("m4", "继续"),
		assistantCalls("m5", "", call("c2", "read_file", `{"path":"b"}`), call("c3", "echo", `{"text":"hi"}`)),
		toolMsg("m6", "c2", "B"),
		toolMsg("m7", "c3", "ok"),
	}

	// 视图前缀 = System Prompt + 摘要（staticEstimator 各计 1 token）。
	const prefixTokens = 2
	const systemPrompt = "系统提示"
	const previousSummary = "旧摘要"
	const newSummary = "边界摘要"

	cases := []struct {
		name       string
		keepRecent int
		dropped    int // 压缩覆盖的输入前缀条数
	}{
		// KeepRecent=3：最小尾部恰为 u3（3 条），压缩 m1..m4。
		{name: "KeepRecent=3 保留尾部恰为多消息工具单元", keepRecent: 3, dropped: 4},
		// KeepRecent=4：最小尾部为 u2+u3（4 条），压缩 m1..m3。
		{name: "KeepRecent=4 保留尾部跨入单条 user 单元", keepRecent: 4, dropped: 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			need := prefixTokens + tc.keepRecent

			t.Run("预算恰好等于压缩后视图估算", func(t *testing.T) {
				sum := &fakeSummarizer{summary: newSummary}
				mgr := newTestManager(t, est, sum)

				result, err := mgr.Build(context.Background(), Request{
					SystemPrompt:    systemPrompt,
					PreviousSummary: previousSummary,
					Messages:        cloneMessages(messages),
					TokenBudget:     need,
					KeepRecent:      tc.keepRecent,
				})
				if err != nil {
					t.Fatalf("预算恰好等于压缩后视图估算时应成功，实际返回错误：%v", err)
				}
				if !result.Compacted {
					t.Fatalf("超预算触发压缩后 Compacted 必须为 true：%+v", result)
				}
				if result.DroppedMessages != tc.dropped {
					t.Fatalf("DroppedMessages = %d，期望 %d", result.DroppedMessages, tc.dropped)
				}
				if len(result.Messages) != prefixTokens+tc.keepRecent {
					t.Fatalf("视图长度 = %d，期望 %d", len(result.Messages), prefixTokens+tc.keepRecent)
				}

				retained := result.Messages[prefixTokens:]
				if len(retained) < tc.keepRecent {
					t.Fatalf("保留条数 = %d，低于 KeepRecent=%d 下限", len(retained), tc.keepRecent)
				}
				if result.DroppedMessages != len(messages)-len(retained) {
					t.Fatalf("DroppedMessages = %d 与实差 %d 不一致", result.DroppedMessages, len(messages)-len(retained))
				}
				assertSameMessages(t, "保留区间", retained, messages[tc.dropped:])

				if got := viewTokens(est, result.Messages); got != need {
					t.Fatalf("压缩后视图估算值 = %d，期望恰好等于 TokenBudget=%d（不多压也不少压）", got, need)
				}
				if err := core.ValidateMessages(result.Messages); err != nil {
					t.Fatalf("视图未通过 core.ValidateMessages：%v", err)
				}
				if result.Summary != newSummary || messageText(t, result.Messages[1]) != result.Summary {
					t.Fatalf("摘要文本与 Summary 不一致：%q vs %q", messageText(t, result.Messages[1]), result.Summary)
				}
				if sum.calls != 1 {
					t.Fatalf("Summarizer 调用次数 = %d，期望恰好 1 次", sum.calls)
				}
				assertSameMessages(t, "Summarizer 待压缩前缀", sum.requests[0].Messages, messages[:tc.dropped])
				if sum.requests[0].PreviousSummary != previousSummary {
					t.Fatalf("Summarizer 必须收到旧摘要以合并，实际为 %q", sum.requests[0].PreviousSummary)
				}
			})

			t.Run("预算少 1 token", func(t *testing.T) {
				sum := &fakeSummarizer{summary: newSummary}
				mgr := newTestManager(t, est, sum)

				result, err := mgr.Build(context.Background(), Request{
					SystemPrompt:    systemPrompt,
					PreviousSummary: previousSummary,
					Messages:        cloneMessages(messages),
					TokenBudget:     need - 1,
					KeepRecent:      tc.keepRecent,
				})
				if err == nil {
					t.Fatalf("预算少 1 token 时应显式失败，实际返回 %+v", result)
				}
				if kind := core.KindOf(err); kind != core.KindInvalidArgument {
					t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInvalidArgument, err)
				}
				if !strings.Contains(err.Error(), "KeepRecent") {
					t.Fatalf("错误文本 %q 未指出保留下限约束", err.Error())
				}
				assertZeroResult(t, result)
				if sum.calls != 1 {
					t.Fatalf("失败路径 Summarizer 调用次数 = %d，期望恰好 1 次（不重试）", sum.calls)
				}
			})
		})
	}
}
