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

// TestBuildShrinksTailUntilBudgetFits 覆盖 INV-008：一次吞并一个原子单元仍超预算时继续收紧
// 保留尾部，Summarizer 调用次数受上限约束，不会无限循环。
func TestBuildShrinksTailUntilBudgetFits(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	messages := make([]core.Message, 0, 10)
	for i := 0; i < 10; i++ {
		messages = append(messages, userText("m"+string(rune('a'+i)), "内容"))
	}

	// 保留 3 条时视图 = 1(system) + 1(摘要) + 3 = 5 > 3，必须再吞并单元。
	result, err := mgr.Build(context.Background(), Request{
		Messages:    messages,
		TokenBudget: 3,
		KeepRecent:  3,
	})
	if err != nil {
		t.Fatalf("Build() 返回错误：%v", err)
	}
	if sum.calls != 2 {
		t.Fatalf("Summarizer 调用次数 = %d，期望 2（逐单元吞并一次后满足预算）", sum.calls)
	}
	if sum.calls > len(messages)+1 {
		t.Fatalf("Summarizer 调用次数 %d 超过 unitCount+1 上限", sum.calls)
	}
	if len(result.Messages) != 3 {
		t.Fatalf("视图长度 = %d，期望 3", len(result.Messages))
	}
	if result.DroppedMessages != 8 {
		t.Fatalf("DroppedMessages = %d，期望 8", result.DroppedMessages)
	}
	assertSameMessages(t, "视图尾部", result.Messages[1:], messages[8:])
	if got := viewTokens(est, result.Messages); got > 3 {
		t.Fatalf("视图估算值 %d 超出预算 3", got)
	}
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

	for _, keepRecent := range []int{0, 1, 2, 3, 4, 9} {
		for budget := 1; budget <= 12; budget++ {
			// 只要预算容得下前缀，且确实存在可压缩的更早单元（或本就无需压缩），
			// 构建就必须成功：不允许以「边界落在单元内部」为由拒绝合法请求。
			wantSuccess := budget >= fullTokens || (keepRecent < len(messages) && budget >= prefixTokens)
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
				if kind := core.KindOf(err); kind != core.KindInvalidArgument && kind != core.KindInternal {
					t.Fatalf("%s 错误种类 = %q，期望 invalid_argument 或 internal", label, kind)
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
			if result.Compacted {
				if result.DroppedMessages != len(messages)-len(retained) {
					t.Fatalf("%s DroppedMessages = %d，期望 %d", label, result.DroppedMessages, len(messages)-len(retained))
				}
				if result.Summary == "" || messageText(t, result.Messages[1]) != result.Summary {
					t.Fatalf("%s 摘要文本与 Summary 不一致", label)
				}
			} else if result.DroppedMessages != 0 || result.Summary != "" {
				t.Fatalf("%s 未压缩时不应报告丢弃或摘要", label)
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

// TestBuildCompactionStillOverBudgetReturnsInternal 覆盖 INV-008 的另一半：历史已全部压缩
// 仍超预算时返回 internal（压缩无法取得有效进展），不返回部分视图，也不死循环。
func TestBuildCompactionStillOverBudgetReturnsInternal(t *testing.T) {
	est := staticEstimator{perMessage: 1}
	sum := &fakeSummarizer{summary: "摘要"}
	mgr := newTestManager(t, est, sum)

	result, err := mgr.Build(context.Background(), Request{
		SystemPrompt: "系统提示",
		Messages:     []core.Message{userText("m1", "旧问"), userText("m2", "旧答")},
		TokenBudget:  1,
		KeepRecent:   1,
	})
	if err == nil {
		t.Fatalf("Build() 应返回错误，实际返回 %+v", result)
	}
	if kind := core.KindOf(err); kind != core.KindInternal {
		t.Fatalf("错误种类 = %q，期望 %q（错误：%v）", kind, core.KindInternal, err)
	}
	if !strings.Contains(err.Error(), "无法取得有效进展") {
		t.Fatalf("错误文本 %q 未说明压缩无进展", err.Error())
	}
	// 2 个原子单元 → 调用上限 3，实际每轮吞并一个单元即终止。
	if sum.calls > 3 {
		t.Fatalf("Summarizer 调用次数 %d 超出 unitCount+1 上限", sum.calls)
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

	fixed := newTestManager(t, staticEstimator{perMessage: 1}, &fakeSummarizer{summary: "摘要"})
	result, err := fixed.Build(context.Background(), Request{Messages: messages, TokenBudget: 3, KeepRecent: 1})
	if err != nil {
		t.Fatalf("固定估算器下 Build() 返回错误：%v", err)
	}
	if result.Compacted {
		t.Fatalf("固定 1 token/条时 3 条消息不应超预算：%+v", result)
	}

	byText := newTestManager(t, textEstimator{}, &fakeSummarizer{summary: "摘要"})
	result, err = byText.Build(context.Background(), Request{Messages: messages, TokenBudget: 3, KeepRecent: 1})
	if err != nil {
		t.Fatalf("文本估算器下 Build() 返回错误：%v", err)
	}
	if !result.Compacted {
		t.Fatalf("按字符估算时 3 条长消息必须触发压缩：%+v", result)
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
