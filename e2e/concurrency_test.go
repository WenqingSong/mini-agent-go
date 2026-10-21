package e2e_test

// 本文件覆盖 AC-006：HTTP 层并发（同会话、跨会话、并发工具调用）在 -race 下无告警，
// 且每个会话的消息总数、CallID 配对与回复内容都不串话。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// echoStep 按请求中的最后一条 user 消息生成回复：回复内容与请求内容绑定，
// 因此「回复是否落在正确会话」成为可判别的断言，而不是固定脚本碰运气。
func echoStep() llmStep {
	return dynamicStep(func(request wireRequest) (int, string) {
		return http.StatusOK, replyBody("resp-echo", "reply:"+request.lastUserText())
	})
}

// concurrentToolStep 按请求是否已带工具结果选择行为：首轮返回两个工具调用，
// 次轮根据本会话自己的工具结果 CallID 生成终稿。并发下不依赖请求到达顺序。
func concurrentToolStep() llmStep {
	return dynamicStep(func(request wireRequest) (int, string) {
		if len(request.toolMessages()) == 0 {
			base, ok := parseBase(request.lastUserText())
			if !ok {
				return http.StatusInternalServerError, `{"error":{"message":"缺少 base 标记"}}`
			}
			return http.StatusOK, toolCallsBody("resp-concurrent-tools",
				scriptCall{
					ID:        fmt.Sprintf("calc-%d", base),
					Name:      "calculator",
					Arguments: fmt.Sprintf(`{"op":"add","a":%d,"b":1}`, base),
				},
				scriptCall{
					ID:        fmt.Sprintf("search-%d", base),
					Name:      "mock_search",
					Arguments: `{"query":"上下文"}`,
				},
			)
		}
		base, ok := baseFromToolCalls(request)
		if !ok {
			return http.StatusInternalServerError, `{"error":{"message":"缺少本会话工具结果"}}`
		}
		return http.StatusOK, replyBody("resp-final", fmt.Sprintf("完成 base=%d", base))
	})
}

// basePrefix 是并发工具用例中会话专属基数的标记。
const basePrefix = "并发工具 base="

// parseBase 解析消息中的会话专属基数。
func parseBase(text string) (int, bool) {
	rest, ok := strings.CutPrefix(text, basePrefix)
	if !ok {
		return 0, false
	}
	value, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return value, true
}

// baseFromToolCalls 从本请求自己的 tool 消息 CallID 反解会话专属基数。
func baseFromToolCalls(request wireRequest) (int, bool) {
	for _, message := range request.toolMessages() {
		rest, ok := strings.CutPrefix(message.ToolCallID, "calc-")
		if !ok {
			continue
		}
		value, err := strconv.Atoi(rest)
		if err != nil {
			continue
		}
		return value, true
	}
	return 0, false
}

// asyncChatOutcome 是一次并发 chat 的结果。
type asyncChatOutcome struct {
	tag    string
	result httpResult
	err    error
}

// runConcurrent 并发发送一组 chat，并返回全部结果；HTTP 传输错误不在此处 Fatal。
func runConcurrent(h *harness, requests map[string]struct{ sessionID, message string }) []asyncChatOutcome {
	results := make(chan asyncChatOutcome, len(requests))
	var group sync.WaitGroup
	for tag, item := range requests {
		group.Add(1)
		go func(tag string, sessionID, message string) {
			defer group.Done()
			result, err := h.requestChat(context.Background(), sessionID, message)
			results <- asyncChatOutcome{tag: tag, result: result, err: err}
		}(tag, item.sessionID, item.message)
	}
	group.Wait()
	close(results)

	out := make([]asyncChatOutcome, 0, len(requests))
	for outcome := range results {
		out = append(out, outcome)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].tag < out[j].tag })
	return out
}

// decodeChatBody 解码成功响应体。
func decodeChatBody(t *testing.T, result httpResult) chatResponse {
	t.Helper()
	if result.Status != http.StatusOK {
		t.Fatalf("chat 状态码 = %d，期望 200（body=%s）", result.Status, result.Body)
	}
	var payload chatResponse
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		t.Fatalf("chat 响应不是合法 JSON：%v（%s）", err, result.Body)
	}
	if payload.Status != string(core.RunStatusCompleted) {
		t.Fatalf("chat status = %q，期望 COMPLETED（body=%s）", payload.Status, result.Body)
	}
	return payload
}

// textSet 返回消息文本集合，用于并发下与顺序无关的断言。
func textSet(messages []core.Message) map[string]int {
	out := make(map[string]int, len(messages))
	for _, message := range messages {
		out[messageText(message)]++
	}
	return out
}

// TestConcurrentChatsSameSession 证明同会话并发请求由执行权串行，成功后消息总数精确
// 等于成功轮次之和，回复与请求一一对应，日志行不交织。
func TestConcurrentChatsSameSession(t *testing.T) {
	const concurrency = 4
	steps := make([]llmStep, concurrency)
	for i := range steps {
		steps[i] = echoStep()
	}
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  12,
		maxToolCalls: 12,
		script:       steps,
	})
	sessionID := h.createSession()

	requests := make(map[string]struct{ sessionID, message string }, concurrency)
	for i := 1; i <= concurrency; i++ {
		message := fmt.Sprintf("并发消息-%d", i)
		requests[message] = struct{ sessionID, message string }{sessionID: sessionID, message: message}
	}

	outcomes := runConcurrent(h, requests)
	if len(outcomes) != concurrency {
		t.Fatalf("并发结果数 = %d，期望 %d", len(outcomes), concurrency)
	}
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("并发 chat %q 传输失败：%v", outcome.tag, outcome.err)
		}
		payload := decodeChatBody(t, outcome.result)
		if payload.Reply != "reply:"+outcome.tag {
			t.Fatalf("并发 chat 回复 = %q，期望 %q（请求与回复串话）", payload.Reply, "reply:"+outcome.tag)
		}
		if payload.LLMCalls != 1 || payload.ToolCalls != 0 {
			t.Fatalf("并发 chat 计数 = (llm=%d, tool=%d)，期望 (1, 0)", payload.LLMCalls, payload.ToolCalls)
		}
	}

	// 每个成功轮次恰好 2 条消息（user + assistant），没有丢轮或重复提交。
	h.assertMessageRoles(sessionID,
		e2eRoleUser, e2eRoleAssistant, e2eRoleUser, e2eRoleAssistant,
		e2eRoleUser, e2eRoleAssistant, e2eRoleUser, e2eRoleAssistant)
	committed := h.snapshot(sessionID)
	if len(committed.Messages) != 2*concurrency {
		t.Fatalf("会话消息数 = %d，期望 %d", len(committed.Messages), 2*concurrency)
	}
	assistantTexts := textSet(messagesWithRole(committed.Messages, e2eRoleAssistant))
	for _, outcome := range outcomes {
		if assistantTexts["reply:"+outcome.tag] != 1 {
			t.Fatalf("会话中缺少唯一的回复 %q：%v", "reply:"+outcome.tag, assistantTexts)
		}
	}
	for _, message := range messagesWithRole(committed.Messages, e2eRoleUser) {
		if messageText(message) != "" && !strings.HasPrefix(messageText(message), "并发消息-") {
			t.Fatalf("会话中出现非本会话消息：%q", messageText(message))
		}
	}

	if got := len(h.finishedEvents(sessionID)); got != concurrency {
		t.Fatalf("run_finished 事件数 = %d，期望 %d", got, concurrency)
	}
	for _, event := range h.finishedEvents(sessionID) {
		if event.Status != string(core.RunStatusCompleted) || event.RunID == "" {
			t.Fatalf("并发 run_finished 事件异常：%+v", event)
		}
	}
	if got := h.llm.requestCount(); got != concurrency {
		t.Fatalf("模型请求次数 = %d，期望 %d", got, concurrency)
	}
	h.assertRunCount(concurrency)
	h.llm.assertScriptConsumed(t)
}

// TestConcurrentChatsDifferentSessions 证明跨会话并发互不影响：每个会话只包含自己的
// 消息与回复，快照规模等于自身成功轮次之和。
func TestConcurrentChatsDifferentSessions(t *testing.T) {
	const (
		sessionsCount = 3
		perSession    = 3
	)
	total := sessionsCount * perSession
	steps := make([]llmStep, total)
	for i := range steps {
		steps[i] = echoStep()
	}
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  20,
		maxToolCalls: 20,
		script:       steps,
	})

	sessionIDs := make([]string, sessionsCount)
	requests := make(map[string]struct{ sessionID, message string }, total)
	for i := 0; i < sessionsCount; i++ {
		sessionIDs[i] = h.createSession()
		for j := 0; j < perSession; j++ {
			message := fmt.Sprintf("会话%d-消息%d", i+1, j+1)
			requests[message] = struct{ sessionID, message string }{
				sessionID: sessionIDs[i],
				message:   message,
			}
		}
	}

	outcomes := runConcurrent(h, requests)
	if len(outcomes) != total {
		t.Fatalf("并发结果数 = %d，期望 %d", len(outcomes), total)
	}
	replies := make(map[string]string, total)
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("并发 chat %q 传输失败：%v", outcome.tag, outcome.err)
		}
		payload := decodeChatBody(t, outcome.result)
		if payload.Reply != "reply:"+outcome.tag {
			t.Fatalf("并发 chat 回复 = %q，期望 %q", payload.Reply, "reply:"+outcome.tag)
		}
		replies[outcome.tag] = payload.Reply
	}

	for i, sessionID := range sessionIDs {
		h.assertMessageRoles(sessionID, appendRepeated(e2eRoleUser, e2eRoleAssistant, perSession)...)
		committed := h.snapshot(sessionID)
		if len(committed.Messages) != 2*perSession {
			t.Fatalf("会话 %d 消息数 = %d，期望 %d", i+1, len(committed.Messages), 2*perSession)
		}
		prefix := fmt.Sprintf("会话%d-", i+1)
		for _, message := range committed.Messages {
			text := messageText(message)
			if message.Role == e2eRoleUser && !strings.HasPrefix(text, prefix) {
				t.Fatalf("会话 %d 出现他人消息：%q", i+1, text)
			}
			if message.Role == e2eRoleAssistant && !strings.HasPrefix(text, "reply:"+prefix) {
				t.Fatalf("会话 %d 出现他人回复：%q", i+1, text)
			}
		}
		if got := len(h.finishedEvents(sessionID)); got != perSession {
			t.Fatalf("会话 %d run_finished 事件数 = %d，期望 %d", i+1, got, perSession)
		}
	}

	if got := h.llm.requestCount(); got != total {
		t.Fatalf("模型请求次数 = %d，期望 %d", got, total)
	}
	h.assertRunCount(total)
	h.llm.assertScriptConsumed(t)
}

// TestConcurrentToolCalls 证明跨会话并发执行工具时结果不串话：每个会话的 calculator
// 结果与其自身基数一致，mock_search 命中固定数据集，CallID 与结果一一配对。
func TestConcurrentToolCalls(t *testing.T) {
	const sessionsCount = 3
	steps := make([]llmStep, sessionsCount*2)
	for i := range steps {
		steps[i] = concurrentToolStep()
	}
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  20,
		maxToolCalls: 20,
		script:       steps,
	})

	bases := []int{10, 20, 30}
	requests := make(map[string]struct{ sessionID, message string }, sessionsCount)
	for _, base := range bases {
		sessionID := h.createSession()
		message := fmt.Sprintf("%s%d", basePrefix, base)
		requests[message] = struct{ sessionID, message string }{sessionID: sessionID, message: message}
	}

	outcomes := runConcurrent(h, requests)
	if len(outcomes) != sessionsCount {
		t.Fatalf("并发结果数 = %d，期望 %d", len(outcomes), sessionsCount)
	}
	for _, outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("并发工具 chat %q 传输失败：%v", outcome.tag, outcome.err)
		}
		base, ok := parseBase(outcome.tag)
		if !ok {
			t.Fatalf("无法解析结果标记 %q", outcome.tag)
		}
		payload := decodeChatBody(t, outcome.result)
		if payload.Reply != fmt.Sprintf("完成 base=%d", base) {
			t.Fatalf("并发工具会话 base=%d reply = %q", base, payload.Reply)
		}
		if payload.LLMCalls != 2 || payload.ToolCalls != 2 {
			t.Fatalf("并发工具会话 base=%d 计数 = (llm=%d, tool=%d)，期望 (2, 2)",
				base, payload.LLMCalls, payload.ToolCalls)
		}

		sessionID := requests[outcome.tag].sessionID
		h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleTool, e2eRoleAssistant)
		committed := h.snapshot(sessionID)
		if len(committed.Messages) != 5 {
			t.Fatalf("会话 base=%d 消息数 = %d，期望 5（user + assistant + tool×2 + assistant）", base, len(committed.Messages))
		}

		calculate := toolMessageOf(t, committed, fmt.Sprintf("calc-%d", base))
		var calcPayload calculatorContent
		if err := json.Unmarshal([]byte(calculate.ToolResult.Content), &calcPayload); err != nil {
			t.Fatalf("calculator 结果不是预期 JSON：%v（%s）", err, calculate.ToolResult.Content)
		}
		if calcPayload.Result != float64(base+1) {
			t.Fatalf("会话 base=%d 的 calculator 结果 = %v，期望 %d（工具结果串话）",
				base, calcPayload.Result, base+1)
		}

		search := toolMessageOf(t, committed, fmt.Sprintf("search-%d", base))
		if !strings.Contains(search.ToolResult.Content, `"id":"doc-3"`) {
			t.Fatalf("会话 base=%d 的 mock_search 结果未命中 doc-3：%s", base, search.ToolResult.Content)
		}
	}

	if got := h.llm.requestCount(); got != sessionsCount*2 {
		t.Fatalf("模型请求次数 = %d，期望 %d", got, sessionsCount*2)
	}
	h.assertRunCount(sessionsCount)
	h.llm.assertScriptConsumed(t)
}

// messagesWithRole 过滤指定角色的消息。
func messagesWithRole(messages []core.Message, role core.Role) []core.Message {
	out := make([]core.Message, 0, len(messages))
	for _, message := range messages {
		if message.Role == role {
			out = append(out, message)
		}
	}
	return out
}

// appendRepeated 生成重复的「user, assistant」角色对。
func appendRepeated(user, assistant core.Role, rounds int) []core.Role {
	out := make([]core.Role, 0, rounds*2)
	for i := 0; i < rounds; i++ {
		out = append(out, user, assistant)
	}
	return out
}
