package e2e_test

// 本文件覆盖 AC-001（多工具连续执行与整轮提交）与 AC-002（同会话记忆、跨会话隔离）。

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// calculatorContent 是 calculator 成功结果的结构。
type calculatorContent struct {
	Operation string  `json:"operation"`
	Result    float64 `json:"result"`
}

// searchContent 是 mock_search 成功结果的结构。
type searchContent struct {
	Mock    bool   `json:"mock"`
	Query   string `json:"query"`
	Results []struct {
		ID      string `json:"id"`
		Title   string `json:"title"`
		Summary string `json:"summary"`
	} `json:"results"`
}

// TestMultiToolChainCommitsFullTurn 证明 AC-001 的主链路：同一请求内模型连续两轮返回
// tool_calls，真实 calculator 与 mock_search 各执行一次，工具结果按 CallID 回填后模型
// 继续推理并给出 stop 终稿；HTTP 响应、Session 提交与 wire 请求三处一致。
func TestMultiToolChainCommitsFullTurn(t *testing.T) {
	const (
		calcCall   = "call-calc-1"
		searchCall = "call-search-1"
		finalReply = "6×7=42，上下文相关文档是 doc-3"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  6,
		maxToolCalls: 6,
		script: []llmStep{
			toolCallsStep(scriptCall{ID: calcCall, Name: "calculator", Arguments: `{"op":"mul","a":6,"b":7}`}),
			toolCallsStep(scriptCall{ID: searchCall, Name: "mock_search", Arguments: `{"query":"上下文"}`}),
			textStep(finalReply),
		},
	})
	sessionID := h.createSession()

	response := h.chatOK(sessionID, "请先算 6×7，再查“上下文”")
	if response.Reply != finalReply {
		t.Fatalf("reply = %q，期望终稿 %q", response.Reply, finalReply)
	}
	if response.LLMCalls != 3 || response.ToolCalls != 2 {
		t.Fatalf("调用计数 = (llm=%d, tool=%d)，期望 (3, 2)", response.LLMCalls, response.ToolCalls)
	}

	// 观测通道 2：Run 终态与计数。
	run := h.runAt(1)
	h.assertTerminal(run, core.RunStatusCompleted, "")
	if run.Outcome.LLMCalls != 3 || run.Outcome.ToolCalls != 2 {
		t.Fatalf("RunOutcome 计数 = (llm=%d, tool=%d)，期望 (3, 2)", run.Outcome.LLMCalls, run.Outcome.ToolCalls)
	}

	// 观测通道 1 + Session：整轮恰好提交一次，消息序列合法。
	h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleAssistant, e2eRoleTool, e2eRoleAssistant)
	committed := h.snapshot(sessionID)
	if len(committed.Messages) != 6 {
		t.Fatalf("提交后消息数 = %d，期望 6", len(committed.Messages))
	}
	if text := messageText(committed.Messages[0]); !strings.Contains(text, "6×7") {
		t.Fatalf("首条消息不是本轮 user 原文：%q", text)
	}
	if text := messageText(committed.Messages[5]); text != finalReply {
		t.Fatalf("末条 assistant 文本 = %q，期望 %q", text, finalReply)
	}

	// 真实 builtin 工具语义：calculator 结果与 mock_search 数据集命中可按实现核对。
	calculate := toolMessageOf(t, committed, calcCall)
	if calculate.ToolResult.IsError {
		t.Fatalf("calculator 结果被标记为错误：%+v", calculate.ToolResult)
	}
	var calcPayload calculatorContent
	if err := json.Unmarshal([]byte(calculate.ToolResult.Content), &calcPayload); err != nil {
		t.Fatalf("calculator 结果不是预期 JSON：%v（%s）", err, calculate.ToolResult.Content)
	}
	if calcPayload.Operation != "mul" || calcPayload.Result != 42 {
		t.Fatalf("calculator 结果 = %+v，期望 {mul 42}", calcPayload)
	}

	search := toolMessageOf(t, committed, searchCall)
	if search.ToolResult.IsError {
		t.Fatalf("mock_search 结果被标记为错误：%+v", search.ToolResult)
	}
	var searchPayload searchContent
	if err := json.Unmarshal([]byte(search.ToolResult.Content), &searchPayload); err != nil {
		t.Fatalf("mock_search 结果不是预期 JSON：%v（%s）", err, search.ToolResult.Content)
	}
	if !searchPayload.Mock || searchPayload.Query != "上下文" || len(searchPayload.Results) != 1 || searchPayload.Results[0].ID != "doc-3" {
		t.Fatalf("mock_search 结果 = %+v，期望 mock=true、query=上下文、唯一命中 doc-3", searchPayload)
	}

	// 观测通道 4：wire 请求逐轮携带上一轮工具结果，CallID 一一对应。
	firstRecord, firstRequest := h.llm.requestAt(t, 1)
	if firstRecord.AuthorizationHeader != "" {
		t.Fatalf("模型请求携带了 Authorization 头：%q", firstRecord.AuthorizationHeader)
	}
	if firstRequest.Model != e2eModel {
		t.Fatalf("wire model = %q，期望 %q", firstRequest.Model, e2eModel)
	}
	wantTools := []string{"calculator", "mock_search", "todo_complete", "todo_create", "todo_list"}
	if got := firstRequest.toolNames(); !reflect.DeepEqual(got, wantTools) {
		t.Fatalf("tools[] = %v，期望 %v（正式装配的完整工具清单）", got, wantTools)
	}
	for _, item := range firstRequest.Tools {
		if len(item.Function.Parameters) == 0 || !json.Valid(item.Function.Parameters) {
			t.Fatalf("工具 %q 的 parameters 不是合法 JSON Schema：%s", item.Function.Name, item.Function.Parameters)
		}
		if strings.TrimSpace(item.Function.Description) == "" {
			t.Fatalf("工具 %q 缺少 description，模型无法判断何时调用", item.Function.Name)
		}
	}
	if len(firstRequest.Messages) != 1 || firstRequest.Messages[0].Role != string(e2eRoleUser) {
		t.Fatalf("第一次模型请求消息 = %+v，期望仅一条 user 消息", firstRequest.Messages)
	}

	_, secondRequest := h.llm.requestAt(t, 2)
	if len(secondRequest.Messages) != 3 {
		t.Fatalf("第二次模型请求消息数 = %d，期望 3（user, assistant, tool）", len(secondRequest.Messages))
	}
	if got := secondRequest.Messages[2]; got.Role != string(e2eRoleTool) || got.ToolCallID != calcCall {
		t.Fatalf("第二次请求回填 = %+v，期望 tool 消息 ToolCallID=%q", got, calcCall)
	}
	if !strings.Contains(secondRequest.Messages[2].Content, `"result":42`) {
		t.Fatalf("第二次请求未携带 calculator 结果：%s", secondRequest.Messages[2].Content)
	}

	_, thirdRequest := h.llm.requestAt(t, 3)
	if len(thirdRequest.Messages) != 5 {
		t.Fatalf("第三次模型请求消息数 = %d，期望 5（含两次工具结果）", len(thirdRequest.Messages))
	}
	tools := thirdRequest.toolMessages()
	if len(tools) != 2 {
		t.Fatalf("第三次请求 tool 消息数 = %d，期望 2", len(tools))
	}
	if tools[0].ToolCallID != calcCall || tools[1].ToolCallID != searchCall {
		t.Fatalf("tool_call_id 顺序 = [%q %q]，期望 [%q %q]", tools[0].ToolCallID, tools[1].ToolCallID, calcCall, searchCall)
	}
	if !strings.Contains(tools[1].Content, `"id":"doc-3"`) {
		t.Fatalf("第三次请求未携带 mock_search 命中内容：%s", tools[1].Content)
	}
	if thirdRequest.Messages[1].ToolCalls[0].Function.Arguments != `{"op":"mul","a":6,"b":7}` {
		t.Fatalf("回传的工具参数被改写：%s", thirdRequest.Messages[1].ToolCalls[0].Function.Arguments)
	}

	h.assertRunCount(1)
	h.llm.assertScriptConsumed(t)
}

// TestSingleRoundMultipleToolCalls 证明单轮多调用：一条 assistant 消息携带两个工具调用，
// 结果为两条独立 tool 消息（一条消息一个结果），顺序与调用数组一致。
func TestSingleRoundMultipleToolCalls(t *testing.T) {
	const (
		calcCall   = "call-calc-multi"
		searchCall = "call-search-multi"
		finalReply = "两个结果都拿到了"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  4,
		maxToolCalls: 4,
		script: []llmStep{
			toolCallsStep(
				scriptCall{ID: calcCall, Name: "calculator", Arguments: `{"op":"add","a":40,"b":2}`},
				scriptCall{ID: searchCall, Name: "mock_search", Arguments: `{"query":"配对"}`},
			),
			textStep(finalReply),
		},
	})
	sessionID := h.createSession()

	response := h.chatOK(sessionID, "一次调用两个工具")
	if response.Reply != finalReply || response.LLMCalls != 2 || response.ToolCalls != 2 {
		t.Fatalf("结论 = (%q, llm=%d, tool=%d)，期望 (%q, 2, 2)",
			response.Reply, response.LLMCalls, response.ToolCalls, finalReply)
	}
	h.assertTerminal(h.runAt(1), core.RunStatusCompleted, "")
	h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleTool, e2eRoleAssistant)

	committed := h.snapshot(sessionID)
	if len(committed.Messages) != 5 {
		t.Fatalf("提交后消息数 = %d，期望 5", len(committed.Messages))
	}
	if committed.Messages[2].ToolResult.CallID != calcCall || committed.Messages[3].ToolResult.CallID != searchCall {
		t.Fatalf("tool 消息顺序 = [%q %q]，期望 [%q %q]",
			committed.Messages[2].ToolResult.CallID, committed.Messages[3].ToolResult.CallID, calcCall, searchCall)
	}

	_, secondRequest := h.llm.requestAt(t, 2)
	if len(secondRequest.Messages) != 4 {
		t.Fatalf("第二次模型请求消息数 = %d，期望 4（user, assistant, tool, tool）", len(secondRequest.Messages))
	}
	if count := len(secondRequest.Messages[1].ToolCalls); count != 2 {
		t.Fatalf("回传的 assistant 工具调用数 = %d，期望 2", count)
	}
	tools := secondRequest.toolMessages()
	if len(tools) != 2 || tools[0].ToolCallID != calcCall || tools[1].ToolCallID != searchCall {
		t.Fatalf("wire tool 消息 = %+v，期望两条与调用一一对应的结果", tools)
	}
	if !strings.Contains(tools[0].Content, `"result":42`) || !strings.Contains(tools[1].Content, `"id":"doc-6"`) {
		t.Fatalf("wire 工具结果内容不符合 builtin 语义：calc=%s search=%s", tools[0].Content, tools[1].Content)
	}

	h.llm.assertScriptConsumed(t)
}

// TestSessionMemoryAndCrossSessionIsolation 证明 AC-002：同会话第二次请求携带首轮原文与
// 工具结果；另一会话的请求不含本会话任何内容；双向运行前后对方会话快照逐字段不变。
func TestSessionMemoryAndCrossSessionIsolation(t *testing.T) {
	const (
		sessionAFirst  = "会话 A 的第一个问题"
		sessionASecond = "会话 A 的第二个问题"
		sessionBFirst  = "会话 B 的问题"
		replyA1        = "第一轮结果 42"
		replyB1        = "会话 B 的独立回答"
		replyA2        = "第二轮：我记得 42"
		aSummary       = "会话 A 的既有摘要（不得被子会话覆盖）"
		bSummary       = "会话 B 的既有摘要"
		calcCall       = "call-a-1"
	)
	todosA := []session.Todo{
		{ID: "a-1", Title: "整理指标", Status: session.TodoStatusPending},
		{ID: "a-2", Title: "复述结论", Status: session.TodoStatusInProgress},
	}
	todosB := []session.Todo{
		{ID: "b-1", Title: "独立任务", Status: session.TodoStatusCompleted},
	}

	h := newHarness(t, harnessOptions{
		maxLLMCalls:  8,
		maxToolCalls: 8,
		script: []llmStep{
			toolCallsStep(scriptCall{ID: calcCall, Name: "calculator", Arguments: `{"op":"add","a":20,"b":22}`}),
			textStep(replyA1),
			textStep(replyB1),
			textStep(replyA2),
		},
	})

	sessionA := h.createSession()
	sessionB := h.createSession()
	h.seed(sessionA, aSummary, todosA)
	h.seed(sessionB, bSummary, todosB)

	// A → B 方向：A 完成一轮后，B 的快照逐字段不变。
	beforeB := h.snapshot(sessionB)
	if response := h.chatOK(sessionA, sessionAFirst); response.Reply != replyA1 {
		t.Fatalf("会话 A 第一轮 reply = %q，期望 %q", response.Reply, replyA1)
	}
	h.assertMessageRoles(sessionA, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleAssistant)
	afterB := h.snapshot(sessionB)
	if !reflect.DeepEqual(beforeB, afterB) {
		t.Fatalf("会话 A 运行后会话 B 发生变化：\nbefore=%s\nafter=%s", describeSession(beforeB), describeSession(afterB))
	}

	// B → A 方向：B 完成一轮后，A 的快照逐字段不变。
	beforeA := h.snapshot(sessionA)
	if response := h.chatOK(sessionB, sessionBFirst); response.Reply != replyB1 {
		t.Fatalf("会话 B reply = %q，期望 %q", response.Reply, replyB1)
	}
	afterA := h.snapshot(sessionA)
	if !reflect.DeepEqual(beforeA, afterA) {
		t.Fatalf("会话 B 运行后会话 A 发生变化：\nbefore=%s\nafter=%s", describeSession(beforeA), describeSession(afterA))
	}

	// 同会话记忆：A 的第二轮请求包含首轮 user、assistant、tool 原文。
	second := h.chatOK(sessionA, sessionASecond)
	if second.Reply != replyA2 {
		t.Fatalf("会话 A 第二轮 reply = %q，期望 %q", second.Reply, replyA2)
	}
	_, aSecondRequest := h.llm.requestAt(t, 4)
	if len(aSecondRequest.Messages) != 6 {
		t.Fatalf("会话 A 第二轮模型请求消息数 = %d，期望 6（summary + 首轮 4 条 + 本轮 user）", len(aSecondRequest.Messages))
	}
	wireText := wireTextOf(aSecondRequest)
	for _, want := range []string{aSummary, sessionAFirst, replyA1, sessionASecond, `"result":42`} {
		if !strings.Contains(wireText, want) {
			t.Fatalf("会话 A 第二轮请求缺少 %q：%s", want, wireText)
		}
	}
	callIDs := make([]string, 0, 1)
	for _, message := range aSecondRequest.toolMessages() {
		callIDs = append(callIDs, message.ToolCallID)
	}
	if !reflect.DeepEqual(callIDs, []string{calcCall}) {
		t.Fatalf("会话 A 第二轮请求 tool_call_id = %v，期望 [%q]", callIDs, calcCall)
	}

	// 跨会话隔离：B 的请求只含 B 的摘要与自己的一条 user 消息。
	_, bRequest := h.llm.requestAt(t, 3)
	if len(bRequest.Messages) != 2 {
		t.Fatalf("会话 B 模型请求消息数 = %d，期望 2（B 的摘要 + B 的 user）", len(bRequest.Messages))
	}
	if bRequest.Messages[0].Role != "system" || bRequest.Messages[0].Content != bSummary {
		t.Fatalf("会话 B 请求首条 = %+v，期望 B 的摘要 system 消息", bRequest.Messages[0])
	}
	if bRequest.Messages[1].Role != string(e2eRoleUser) || bRequest.Messages[1].Content != sessionBFirst {
		t.Fatalf("会话 B 请求 user 消息 = %+v，期望 %q", bRequest.Messages[1], sessionBFirst)
	}
	bWire := wireTextOf(bRequest)
	for _, forbidden := range []string{sessionAFirst, replyA1, calcCall, aSummary, "doc-3"} {
		if strings.Contains(bWire, forbidden) {
			t.Fatalf("会话 B 请求泄漏了会话 A 的内容 %q：%s", forbidden, bWire)
		}
	}

	// 预置状态不被成功运行覆盖，且消息增量符合两轮提交。
	finalA := h.snapshot(sessionA)
	if finalA.Summary != aSummary || !reflect.DeepEqual(finalA.Todos, todosA) {
		t.Fatalf("会话 A 预置状态被覆盖：summary=%q todos=%v", finalA.Summary, finalA.Todos)
	}
	if len(finalA.Messages) != 6 {
		t.Fatalf("会话 A 最终消息数 = %d，期望 6", len(finalA.Messages))
	}
	finalB := h.snapshot(sessionB)
	if finalB.Summary != bSummary || !reflect.DeepEqual(finalB.Todos, todosB) {
		t.Fatalf("会话 B 预置状态被覆盖：summary=%q todos=%v", finalB.Summary, finalB.Todos)
	}
	if len(finalB.Messages) != 2 {
		t.Fatalf("会话 B 最终消息数 = %d，期望 2", len(finalB.Messages))
	}

	if got := len(h.finishedEvents(sessionA)); got != 2 {
		t.Fatalf("会话 A run_finished 事件数 = %d，期望 2", got)
	}
	if got := len(h.finishedEvents(sessionB)); got != 1 {
		t.Fatalf("会话 B run_finished 事件数 = %d，期望 1", got)
	}
	h.assertRunCount(3)
	h.llm.assertScriptConsumed(t)
}

// wireTextOf 把一次 wire 请求的全部可读内容拼接为单一字符串，用于包含/泄漏断言。
func wireTextOf(request wireRequest) string {
	var builder strings.Builder
	for _, message := range request.Messages {
		builder.WriteString(message.Role)
		builder.WriteString(":")
		builder.WriteString(message.Content)
		builder.WriteString("|")
		builder.WriteString(message.ToolCallID)
		for _, call := range message.ToolCalls {
			builder.WriteString("|")
			builder.WriteString(call.ID)
			builder.WriteString(":")
			builder.WriteString(call.Function.Name)
			builder.WriteString(":")
			builder.WriteString(call.Function.Arguments)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}
