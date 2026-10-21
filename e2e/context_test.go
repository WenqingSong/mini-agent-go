package e2e_test

// 本文件是「正式 HTTP 服务可触发上下文压缩」的端到端验收：真实 httpapi.Server →
// recordingRunner → service.Runner → agent.Loop → contextmgr.Manager（默认
// DigestSummarizer）→ 真实 session.Manager → loopback scripted OpenAI 兼容服务。
// 覆盖 AC-001（压缩可达）、AC-002（历史真正缩减且摘要召回）、AC-003（工具配对不破）、
// AC-004（压缩后失败完整回滚）、AC-005（预算不足显式失败）、AC-006（多轮追问）。

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
)

const (
	// contextTestBudget 是本文件统一使用的上下文估算预算。
	contextTestBudget = 800
	// contextTestKeepRecent 是压缩后保留的近期消息条数下限。
	contextTestKeepRecent = 2
)

// longQuestion 生成一条约 300 个 CJK 字符的提问；轮次标记放在末尾，
// 因此被摘要截断的早期消息不会因末尾标记而让「旧历史是否仍被发送」的断言失效。
func longQuestion(round int) string {
	return strings.Repeat("长问题内容", 60) + fmt.Sprintf("第%d轮", round)
}

// newBudgetedHarness 构造启用上下文预算的正式链路。
func newBudgetedHarness(t *testing.T, script []llmStep) *harness {
	t.Helper()
	return newHarness(t, harnessOptions{
		script:      script,
		tokenBudget: contextTestBudget,
		keepRecent:  contextTestKeepRecent,
	})
}

// wireTokens 用生产默认估算器累加线路消息的估算值（工具参数不在本函数覆盖范围内，
// 因此它给出的是下界，用于证明视图没有明显突破预算）。
func wireTokens(messages []wireMessage) int {
	var estimator contextmgr.HeuristicEstimator
	total := 0
	for _, message := range messages {
		total += estimator.EstimateMessage(core.Message{
			Role:    core.Role(message.Role),
			Content: []core.ContentPart{{Type: core.ContentType("text"), Text: message.Content}},
		})
	}
	return total
}

// wireText 拼接线路消息的文本，用于断言摘要召回与旧历史不再发送。
func wireText(messages []wireMessage) string {
	var builder strings.Builder
	for _, message := range messages {
		builder.WriteString(message.Role)
		builder.WriteString(":")
		builder.WriteString(message.Content)
		builder.WriteString("\n")
	}
	return builder.String()
}

// assertWirePairing 核对线路消息的 ToolCall / ToolResult 配对：
// 每个 tool 消息必须引用此前 assistant 声明且尚未应答的调用，结束时不允许未应答调用。
func assertWirePairing(t *testing.T, label string, messages []wireMessage) {
	t.Helper()
	pending := make(map[string]struct{})
	for index, message := range messages {
		switch message.Role {
		case string(e2eRoleAssistant):
			for _, call := range message.ToolCalls {
				if call.ID == "" {
					t.Fatalf("%s 消息[%d] 工具调用缺少 ID", label, index)
				}
				if _, ok := pending[call.ID]; ok {
					t.Fatalf("%s 消息[%d] 工具调用 %q 重复或未应答", label, index, call.ID)
				}
				pending[call.ID] = struct{}{}
			}
		case string(e2eRoleTool):
			if message.ToolCallID == "" {
				t.Fatalf("%s 消息[%d] 是孤立工具结果（缺少 tool_call_id）", label, index)
			}
			if _, ok := pending[message.ToolCallID]; !ok {
				t.Fatalf("%s 消息[%d] 引用了不存在的工具调用 %q", label, index, message.ToolCallID)
			}
			delete(pending, message.ToolCallID)
		}
	}
	if len(pending) != 0 {
		t.Fatalf("%s 存在未应答的工具调用：%v", label, pending)
	}
}

// TestHTTPCompactionShrinksHistoryAndRecallsSummary 覆盖 AC-001 / AC-002 / AC-006：
// 连续多轮 HTTP 对话达到配置预算后触发压缩，Session 历史真的缩减、摘要被保存，
// 下一轮请求能召回摘要且不再重复发送被覆盖的旧消息。
func TestHTTPCompactionShrinksHistoryAndRecallsSummary(t *testing.T) {
	h := newBudgetedHarness(t, []llmStep{
		textStep("第一轮回答"),
		textStep("第二轮回答"),
		textStep("第三轮回答"),
		textStep("第四轮回答"),
	})
	sessionID := h.createSession()

	for round := 1; round <= 4; round++ {
		response := h.chatOK(sessionID, longQuestion(round))
		if response.Status != string(core.RunStatusCompleted) {
			t.Fatalf("第 %d 轮终态 = %s，期望 COMPLETED", round, response.Status)
		}
	}
	h.llm.assertScriptConsumed(t)

	committed := h.snapshot(sessionID)
	if !strings.Contains(committed.Summary, "早期对话摘要") {
		t.Fatalf("压缩摘要未写入 Session：%q", committed.Summary)
	}
	if len(committed.Messages) >= 8 {
		t.Fatalf("压缩后历史条数 = %d，期望少于未压缩时的 8 条（4 轮 × 2）", len(committed.Messages))
	}
	if err := core.ValidateMessages(committed.Messages); err != nil {
		t.Fatalf("压缩后的会话历史非法：%v", err)
	}

	// 最后一次请求：召回摘要、不再发送第 1 轮完整消息、视图不超预算。
	_, lastRequest := h.llm.requestAt(t, 4)
	lastText := wireText(lastRequest.Messages)
	if !strings.Contains(lastText, "早期对话摘要") {
		t.Fatalf("压缩后的下一轮请求未召回摘要：%s", lastText)
	}
	if strings.Contains(lastText, longQuestion(1)) {
		t.Fatalf("被摘要覆盖的第 1 轮消息仍完整发送给 Provider")
	}
	// 当前轮用户输入不得被压缩丢弃（KeepRecent >= 1 保证尾部保留）。
	if !strings.Contains(lastText, "第4轮") {
		t.Fatalf("压缩丢弃了本轮用户输入：%s", lastText)
	}
	if tokens := wireTokens(lastRequest.Messages); tokens > contextTestBudget {
		t.Fatalf("最后一次请求估算值 = %d，超出配置预算 %d", tokens, contextTestBudget)
	}
	if got := strings.Count(lastText, "早期对话摘要"); got != 1 {
		t.Fatalf("摘要 system 消息出现 %d 次，期望恰好 1 次（不得重复插入历史）", got)
	}
}

// TestHTTPCompactionKeepsToolPairing 覆盖 AC-003：含 ToolCalls / ToolResults 的历史
// 在压缩后仍然合法，发送给 Provider 的每次请求都通过配对检查。
func TestHTTPCompactionKeepsToolPairing(t *testing.T) {
	h := newBudgetedHarness(t, []llmStep{
		toolCallsStep(scriptCall{ID: "call-1", Name: "calculator", Arguments: `{"op":"add","a":1,"b":2}`}),
		textStep("计算完成"),
		textStep("第二轮回答"),
		textStep("第三轮回答"),
	})
	sessionID := h.createSession()

	h.chatOK(sessionID, "请计算 1+2")
	for round := 2; round <= 3; round++ {
		h.chatOK(sessionID, longQuestion(round))
	}
	h.llm.assertScriptConsumed(t)

	// 工具调用后的那次请求必须包含完整的 Call/Result 对。
	_, toolRequest := h.llm.requestAt(t, 2)
	assertWirePairing(t, "工具调用后的请求", toolRequest.Messages)
	if len(toolRequest.toolMessages()) != 1 {
		t.Fatalf("工具结果条数 = %d，期望 1", len(toolRequest.toolMessages()))
	}

	// 压缩发生后（第 3 轮请求）仍必须是合法视图，且不存在孤立工具结果。
	_, compactedRequest := h.llm.requestAt(t, 3)
	assertWirePairing(t, "压缩后的请求", compactedRequest.Messages)

	committed := h.snapshot(sessionID)
	if err := core.ValidateMessages(committed.Messages); err != nil {
		t.Fatalf("提交后的会话历史非法：%v", err)
	}
	for _, message := range committed.Messages {
		if message.Role == e2eRoleTool && message.ToolResult == nil {
			t.Fatal("提交历史包含缺少 ToolResult 的 tool 消息")
		}
	}
}

// TestHTTPCompactionRollsBackOnProviderFailure 覆盖 AC-004：压缩已经发生，但 Provider
// 随后失败时，Session 的消息、摘要与 Todo 与运行前完全一致。
func TestHTTPCompactionRollsBackOnProviderFailure(t *testing.T) {
	h := newBudgetedHarness(t, []llmStep{
		textStep("第一轮回答"),
		textStep("第二轮回答"),
		textStep("第三轮回答"),
		statusStep(http.StatusInternalServerError, `{"error":{"message":"upstream failed"}}`),
	})
	sessionID := h.createSession()

	for round := 1; round <= 3; round++ {
		h.chatOK(sessionID, longQuestion(round))
	}
	before := h.snapshot(sessionID)
	if !strings.Contains(before.Summary, "早期对话摘要") {
		t.Fatalf("失败前应已发生一次压缩，当前摘要 %q", before.Summary)
	}

	result := h.chat(sessionID, longQuestion(4))
	h.assertErrorResponse(result, http.StatusBadGateway, httpapi.CodeProviderFailure)

	// 该会话已有多轮 Run，逐轮对照 run_finished 事件的最后一条。
	run := h.runAt(4)
	if run.Err != nil {
		t.Fatalf("失败路径应已形成 Run 结论：%v", run.Err)
	}
	if run.Outcome.Status != core.RunStatusFailed || core.KindOf(run.Outcome.Err) != core.KindProviderFailure {
		t.Fatalf("终态 = (%s,%v)，期望 FAILED/provider_failure", run.Outcome.Status, run.Outcome.Err)
	}
	if run.Outcome.LLMCalls == 0 {
		t.Fatal("失败的那次模型调用必须计入 LLMCalls")
	}
	events := h.finishedEvents(sessionID)
	last := events[len(events)-1]
	if last.RunID != run.Request.RunID || last.Status != string(core.RunStatusFailed) ||
		last.ErrorKind != string(core.KindProviderFailure) {
		t.Fatalf("run_finished 事件与 Run 结论不一致：%+v", last)
	}
	h.assertSessionUnchanged(before)
}

// TestHTTPConcurrentCompactionKeepsSessionsIsolated 覆盖 AC-007 的并发压缩：
// 两个会话并发多轮触发压缩时互不串话，摘要与历史各自归属（配合 -race 运行）。
func TestHTTPConcurrentCompactionKeepsSessionsIsolated(t *testing.T) {
	// 固定短回答，让预算增长由用户长问题决定，压缩时机可预期。
	answer := dynamicStep(func(wireRequest) (int, string) {
		return http.StatusOK, replyBody("resp-concurrent", "并发回答")
	})
	h := newBudgetedHarness(t, []llmStep{answer, answer, answer, answer, answer, answer})
	sessionA := h.createSession()
	sessionB := h.createSession()

	const rounds = 3
	var waitGroup sync.WaitGroup
	errorsBySession := make([]error, 2)
	for index, sessionID := range []string{sessionA, sessionB} {
		waitGroup.Add(1)
		go func(index int, sessionID string) {
			defer waitGroup.Done()
			marker := fmt.Sprintf("会话%c", 'A'+index)
			for round := 1; round <= rounds; round++ {
				result, err := h.requestChat(context.Background(), sessionID, strings.Repeat("长问题内容", 60)+marker+fmt.Sprintf("第%d轮", round))
				if err != nil {
					errorsBySession[index] = err
					return
				}
				if result.Status != http.StatusOK {
					errorsBySession[index] = fmt.Errorf("状态码 %d：%s", result.Status, result.Body)
					return
				}
			}
		}(index, sessionID)
	}
	waitGroup.Wait()
	for index, err := range errorsBySession {
		if err != nil {
			t.Fatalf("并发会话[%d] 失败：%v", index, err)
		}
	}

	for index, sessionID := range []string{sessionA, sessionB} {
		committed := h.snapshot(sessionID)
		if !strings.Contains(committed.Summary, "早期对话摘要") {
			t.Fatalf("会话[%d] 未发生压缩：%q", index, committed.Summary)
		}
		if err := core.ValidateMessages(committed.Messages); err != nil {
			t.Fatalf("会话[%d] 历史非法：%v", index, err)
		}
		ownMarker := fmt.Sprintf("会话%c", 'A'+index)
		otherMarker := fmt.Sprintf("会话%c", 'A'+(1-index))
		text := committed.Summary
		for _, message := range committed.Messages {
			text += messageText(message)
		}
		if !strings.Contains(text, ownMarker) {
			t.Fatalf("会话[%d] 丢失自己的历史：%s", index, text)
		}
		if strings.Contains(text, otherMarker) {
			t.Fatalf("会话[%d] 混入另一会话的历史：%s", index, text)
		}
	}
	if got := h.llm.requestCount(); got != 2*rounds {
		t.Fatalf("模型请求次数 = %d，期望 %d", got, 2*rounds)
	}
}

// TestHTTPInsufficientBudgetFailsExplicitly 覆盖 AC-005：保留目标装不进预算时返回明确
// 失败，不发出模型调用、不进入压缩循环、不修改 Session。
func TestHTTPInsufficientBudgetFailsExplicitly(t *testing.T) {
	h := newHarness(t, harnessOptions{
		tokenBudget: 60,
		keepRecent:  8,
	})
	sessionID := h.createSession()
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, longQuestion(1))
	// KindInvalidArgument 按既有 HTTP 错误分类规则映射为 internal（不新增协议码）。
	h.assertErrorResponse(result, http.StatusInternalServerError, httpapi.CodeInternal)

	run := h.runAt(1)
	if run.Outcome.Status != core.RunStatusFailed {
		t.Fatalf("终态 = %q，期望 FAILED", run.Outcome.Status)
	}
	if kind := core.KindOf(run.Outcome.Err); kind != core.KindInvalidArgument {
		t.Fatalf("错误种类 = %q，期望 invalid_argument（预算不可满足）", kind)
	}
	if got := h.llm.requestCount(); got != 0 {
		t.Fatalf("预算不足时不得发出模型调用，实际 %d 次", got)
	}
	h.assertSessionUnchanged(before)
}
