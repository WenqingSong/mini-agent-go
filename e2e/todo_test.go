package e2e_test

// 本文件覆盖 Todo 工具的端到端验收（AC-003 / AC-004 / AC-005 / AC-006 的 Todo 部分）：
// 全部请求经真实 HTTP 入口进入 service.Runner → agent.Loop，工具调用只来自脚本 Provider
// 返回的 tool_calls，Todo 的存储、ID 生成与提交边界全部由真实 session.Manager 裁决。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// todoView 是 todo 工具结果的模型可见投影。
type todoView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// todoContent 是 todo_create / todo_complete 的结果结构。
type todoContent struct {
	Todo todoView `json:"todo"`
}

// todoListContent 是 todo_list 的结果结构。
type todoListContent struct {
	Todos []todoView `json:"todos"`
}

// todoCreateStep 构造一次模型请求创建待办的脚本步。
func todoCreateStep(callID, title string) llmStep {
	return toolCallsStep(scriptCall{
		ID:        callID,
		Name:      "todo_create",
		Arguments: mustJSON(map[string]string{"title": title}),
	})
}

// todoListStep 构造一次模型请求列出待办的脚本步。
func todoListStep(callID string) llmStep {
	return toolCallsStep(scriptCall{ID: callID, Name: "todo_list", Arguments: `{}`})
}

// todoCompleteStep 从本请求已携带的待办结果里取出 ID 再完成它：
// 模拟模型「先看工具结果、再决定参数」的真实链路，不依赖测试硬编码 ID。
func todoCompleteStep(callID string) llmStep {
	return dynamicStep(func(request wireRequest) (int, string) {
		id, ok := todoIDFromWire(request)
		if !ok {
			return http.StatusInternalServerError, `{"error":{"message":"模型请求中缺少待办结果"}}`
		}
		return http.StatusOK, toolCallsBody("resp-todo-complete", scriptCall{
			ID:        callID,
			Name:      "todo_complete",
			Arguments: mustJSON(map[string]string{"id": id}),
		})
	})
}

// todoIDFromWire 从本请求最近的待办工具结果中解析待办 ID。
func todoIDFromWire(request wireRequest) (string, bool) {
	messages := request.toolMessages()
	for index := len(messages) - 1; index >= 0; index-- {
		var single todoContent
		if err := json.Unmarshal([]byte(messages[index].Content), &single); err == nil && single.Todo.ID != "" {
			return single.Todo.ID, true
		}
		var list todoListContent
		if err := json.Unmarshal([]byte(messages[index].Content), &list); err == nil {
			for _, item := range list.Todos {
				if item.ID != "" {
					return item.ID, true
				}
			}
		}
	}
	return "", false
}

// TestTodoLifecycleOverHTTP 证明 AC-003：同一会话内创建 → 查询 → 完成，
// 工具结果、会话状态与模型下一轮看到的上下文三者一致。
func TestTodoLifecycleOverHTTP(t *testing.T) {
	const (
		createCall   = "call-todo-create"
		listCall     = "call-todo-list"
		completeCall = "call-todo-complete"
		title        = "整理面试笔记"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  4,
		maxToolCalls: 4,
		script: []llmStep{
			todoCreateStep(createCall, title),
			textStep("待办已创建"),
			todoListStep(listCall),
			textStep("待办已查询"),
			todoCompleteStep(completeCall),
			textStep("待办已完成"),
		},
	})
	sessionID := h.createSession()

	// 第一轮：模型自主调用 todo_create。
	created := h.chatOK(sessionID, "帮我记一条待办")
	if created.ToolCalls != 1 || created.LLMCalls != 2 {
		t.Fatalf("创建轮计数 = (llm=%d, tool=%d)，期望 (2, 1)", created.LLMCalls, created.ToolCalls)
	}
	committed := h.snapshot(sessionID)
	if len(committed.Todos) != 1 {
		t.Fatalf("创建后 Todo 数 = %d，期望 1（%s）", len(committed.Todos), describeSession(committed))
	}
	if committed.Todos[0].Title != title || committed.Todos[0].Status != session.TodoStatusPending {
		t.Fatalf("创建后的 Todo = %+v，期望 pending 且标题为 %q", committed.Todos[0], title)
	}
	todoID := committed.Todos[0].ID
	if todoID == "" {
		t.Fatal("创建后的 Todo 缺少 ID")
	}
	createResult := toolMessageOf(t, committed, createCall)
	var createdPayload todoContent
	if err := json.Unmarshal([]byte(createResult.ToolResult.Content), &createdPayload); err != nil {
		t.Fatalf("todo_create 结果不是预期 JSON：%v（%s）", err, createResult.ToolResult.Content)
	}
	if createResult.ToolResult.IsError || createdPayload.Todo.ID != todoID {
		t.Fatalf("todo_create 结果 = %+v（IsError=%v），期望成功且 ID=%q",
			createdPayload, createResult.ToolResult.IsError, todoID)
	}

	// 第二轮：后续请求（新的 Run）能查询到上一轮已提交的待办。
	listed := h.chatOK(sessionID, "现在有哪些待办")
	if listed.ToolCalls != 1 {
		t.Fatalf("查询轮工具调用数 = %d，期望 1", listed.ToolCalls)
	}
	listResult := toolMessageOf(t, h.snapshot(sessionID), listCall)
	var listPayload todoListContent
	if err := json.Unmarshal([]byte(listResult.ToolResult.Content), &listPayload); err != nil {
		t.Fatalf("todo_list 结果不是预期 JSON：%v（%s）", err, listResult.ToolResult.Content)
	}
	if len(listPayload.Todos) != 1 || listPayload.Todos[0].ID != todoID ||
		listPayload.Todos[0].Status != string(session.TodoStatusPending) {
		t.Fatalf("todo_list 结果 = %+v，期望唯一 pending 待办 %q", listPayload.Todos, todoID)
	}

	// 第三轮：模型用上一轮工具结果里的 ID 完成该待办。
	completed := h.chatOK(sessionID, "把刚才那条标记为完成")
	if completed.ToolCalls != 1 {
		t.Fatalf("完成轮工具调用数 = %d，期望 1", completed.ToolCalls)
	}
	finalSnapshot := h.snapshot(sessionID)
	if len(finalSnapshot.Todos) != 1 || finalSnapshot.Todos[0].ID != todoID {
		t.Fatalf("完成后的 Todo 集合 = %+v，期望仍只有 %q", finalSnapshot.Todos, todoID)
	}
	if finalSnapshot.Todos[0].Status != session.TodoStatusCompleted {
		t.Fatalf("完成后状态 = %q，期望 completed", finalSnapshot.Todos[0].Status)
	}
	completeResult := toolMessageOf(t, finalSnapshot, completeCall)
	var completedPayload todoContent
	if err := json.Unmarshal([]byte(completeResult.ToolResult.Content), &completedPayload); err != nil {
		t.Fatalf("todo_complete 结果不是预期 JSON：%v（%s）", err, completeResult.ToolResult.Content)
	}
	if completeResult.ToolResult.IsError || completedPayload.Todo.Status != string(session.TodoStatusCompleted) {
		t.Fatalf("todo_complete 结果 = %+v（IsError=%v），期望 completed",
			completedPayload, completeResult.ToolResult.IsError)
	}

	// 三轮各自整轮提交：每轮 user + assistant(tool_calls) + tool + assistant。
	if len(finalSnapshot.Messages) != 12 {
		t.Fatalf("三轮后消息数 = %d，期望 12", len(finalSnapshot.Messages))
	}
	if err := core.ValidateMessages(finalSnapshot.Messages); err != nil {
		t.Fatalf("三轮后历史不合法：%v", err)
	}

	// 每个工具调用都来自脚本 Provider 的 tool_calls：三轮共 3 次工具执行、6 次模型请求。
	if got := h.llm.requestCount(); got != 6 {
		t.Fatalf("模型请求次数 = %d，期望 6", got)
	}
	h.assertRunCount(3)
	h.llm.assertScriptConsumed(t)
}

// TestTodoSessionIsolationOverHTTP 证明 AC-004：A 创建的待办不出现在 B，
// 模型即使拿到 A 的待办 ID 也无法在 B 中操作它。
func TestTodoSessionIsolationOverHTTP(t *testing.T) {
	const (
		titleA       = "会话 A 的待办"
		createCallA  = "call-a-create"
		listCallB    = "call-b-list"
		listCallA    = "call-a-list"
		completeCall = "call-b-complete"
	)
	// crossSessionID 在 A 的首轮提交后由测试赋值，随后用于 B 的「越权完成」请求。
	var crossSessionID string

	h := newHarness(t, harnessOptions{
		maxLLMCalls:  12,
		maxToolCalls: 8,
		script: []llmStep{
			todoCreateStep(createCallA, titleA),
			textStep("A 已记录"),
			todoListStep(listCallB),
			textStep("B 查看自己的待办"),
			todoListStep(listCallA),
			textStep("A 查看自己的待办"),
			dynamicStep(func(wireRequest) (int, string) {
				return http.StatusOK, toolCallsBody("resp-b-complete", scriptCall{
					ID:        completeCall,
					Name:      "todo_complete",
					Arguments: mustJSON(map[string]string{"id": crossSessionID}),
				})
			}),
			textStep("B 尝试完成越权待办"),
		},
	})
	sessionA := h.createSession()
	sessionB := h.createSession()

	// A 创建待办；B 的快照逐字段不变。
	beforeB := h.snapshot(sessionB)
	h.chatOK(sessionA, "A 记一条待办")
	h.assertSessionUnchanged(beforeB)

	todosA := h.snapshot(sessionA).Todos
	if len(todosA) != 1 {
		t.Fatalf("A 的 Todo 数 = %d，期望 1", len(todosA))
	}
	crossSessionID = todosA[0].ID

	// B 查询：只看到空列表（第 3、4 次模型请求属于 B）。
	h.chatOK(sessionB, "B 查看自己的待办")
	listB := toolMessageOf(t, h.snapshot(sessionB), listCallB)
	var listBPayload todoListContent
	if err := json.Unmarshal([]byte(listB.ToolResult.Content), &listBPayload); err != nil {
		t.Fatalf("B 的 todo_list 结果不是预期 JSON：%v（%s）", err, listB.ToolResult.Content)
	}
	if len(listBPayload.Todos) != 0 {
		t.Fatalf("B 看到了不属于自己的待办：%+v", listBPayload.Todos)
	}

	// A 查询：只看到自己的那条。
	h.chatOK(sessionA, "A 查看自己的待办")
	listA := toolMessageOf(t, h.snapshot(sessionA), listCallA)
	var listAPayload todoListContent
	if err := json.Unmarshal([]byte(listA.ToolResult.Content), &listAPayload); err != nil {
		t.Fatalf("A 的 todo_list 结果不是预期 JSON：%v（%s）", err, listA.ToolResult.Content)
	}
	if len(listAPayload.Todos) != 1 || listAPayload.Todos[0].Title != titleA {
		t.Fatalf("A 的待办列表 = %+v，期望唯一标题 %q", listAPayload.Todos, titleA)
	}

	// B 用 A 的 ID 尝试完成：领域失败 todo_not_found，A 与 B 的状态都不变。
	beforeA := h.snapshot(sessionA)
	h.chatOK(sessionB, "B 尝试完成 A 的待办")
	h.assertSessionUnchanged(beforeA)
	crossResult := toolMessageOf(t, h.snapshot(sessionB), completeCall)
	if !crossResult.ToolResult.IsError || !strings.Contains(crossResult.ToolResult.Content, `"code":"todo_not_found"`) {
		t.Fatalf("越权完成结果 = %+v，期望 todo_not_found", crossResult.ToolResult)
	}
	if len(h.snapshot(sessionB).Todos) != 0 {
		t.Fatalf("B 的越权写入落库：%+v", h.snapshot(sessionB).Todos)
	}

	// 隔离也体现在 wire：B 的前两次模型请求（查询自己的待办）不含 A 的标题与 ID。
	for _, index := range []int{3, 4} {
		_, parsed := h.llm.requestAt(t, index)
		wire := wireTextOf(parsed)
		for _, forbidden := range []string{titleA, crossSessionID} {
			if strings.Contains(wire, forbidden) {
				t.Fatalf("B 的第 %d 次模型请求泄漏了会话 A 的内容 %q：%s", index, forbidden, wire)
			}
		}
	}

	h.assertRunCount(4)
	h.llm.assertScriptConsumed(t)
}

// TestTodoRollbackOnProviderFailure 证明 AC-005：同一次 Run 内先执行 todo_create，
// 随后 Provider 失败，整轮不提交——消息、摘要与 Todo 全部保持运行前状态。
func TestTodoRollbackOnProviderFailure(t *testing.T) {
	const (
		createCall = "call-rollback-create"
		title      = "失败轮次的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  4,
		maxToolCalls: 4,
		script: []llmStep{
			todoCreateStep(createCall, title),
			statusStep(http.StatusInternalServerError, `{"error":{"message":"upstream down"}}`),
		},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, "记一条待办然后失败")
	h.assertErrorResponse(result, http.StatusBadGateway, "provider_failure")
	h.assertSessionUnchanged(before)

	run := h.runAt(1)
	h.assertTerminal(run, core.RunStatusFailed, core.KindProviderFailure)
	if run.Outcome.LLMCalls != 2 || run.Outcome.ToolCalls != 1 {
		t.Fatalf("Run 计数 = (llm=%d, tool=%d)，期望 (2, 1)：工具确实执行过才谈得上回滚",
			run.Outcome.LLMCalls, run.Outcome.ToolCalls)
	}

	// 第 2 次模型请求确实收到了 todo_create 的结果：工具真的执行过，回滚不是「没执行」。
	_, secondRequest := h.llm.requestAt(t, 2)
	tools := secondRequest.toolMessages()
	if len(tools) != 1 || tools[0].ToolCallID != createCall || !strings.Contains(tools[0].Content, title) {
		t.Fatalf("第 2 次模型请求的 tool 消息 = %+v，期望含 %q 的创建结果", tools, title)
	}

	h.assertRunCount(1)
	h.llm.assertScriptConsumed(t)
}

// TestTodoRollbackOnToolBudget 证明 AC-005：Todo 已在本轮创建、随后触发工具预算上限，
// 整轮丢弃，不留下「Todo 已创建但消息未提交」的不一致状态。
func TestTodoRollbackOnToolBudget(t *testing.T) {
	const (
		createCall = "call-limit-create"
		title      = "预算受限轮次的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  4,
		maxToolCalls: 1,
		script: []llmStep{
			todoCreateStep(createCall, title),
			todoListStep("call-limit-list"),
		},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, "创建待办后继续请求工具")
	h.assertErrorResponse(result, http.StatusTooManyRequests, "tool_call_limit")
	h.assertSessionUnchanged(before)

	run := h.runAt(1)
	h.assertTerminal(run, core.RunStatusLimitExceeded, core.KindToolCallLimit)
	if run.Outcome.ToolCalls != 1 {
		t.Fatalf("Run 工具调用数 = %d，期望 1（第一批已执行，第二批整批拒绝）", run.Outcome.ToolCalls)
	}

	_, secondRequest := h.llm.requestAt(t, 2)
	if tools := secondRequest.toolMessages(); len(tools) != 1 || !strings.Contains(tools[0].Content, title) {
		t.Fatalf("第 2 次模型请求的 tool 消息 = %+v，期望含本轮已创建的待办", tools)
	}
	h.llm.assertScriptConsumed(t)
}

// TestTodoRollbackOnRequestTimeout 证明 AC-005 的超时边界：Todo 已创建，
// 随后请求级超时，Run 终态 CANCELLED/deadline_exceeded，会话状态不变。
func TestTodoRollbackOnRequestTimeout(t *testing.T) {
	const (
		createCall = "call-timeout-create"
		title      = "超时轮次的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:    4,
		maxToolCalls:   4,
		requestTimeout: 200 * time.Millisecond,
		script: []llmStep{
			todoCreateStep(createCall, title),
			blockStep(),
		},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, "创建待办后模型无响应")
	h.assertErrorResponse(result, http.StatusGatewayTimeout, "deadline_exceeded")
	h.assertSessionUnchanged(before)

	run := h.runAt(1)
	h.assertTerminal(run, core.RunStatusCancelled, core.KindDeadlineExceeded)
	if run.Outcome.ToolCalls != 1 {
		t.Fatalf("Run 工具调用数 = %d，期望 1", run.Outcome.ToolCalls)
	}

	// 阻塞中的第 2 次请求已经带上本轮创建结果，说明 Todo 在本轮内确实写入过暂存区。
	_, secondRequest := h.llm.requestAt(t, 2)
	if tools := secondRequest.toolMessages(); len(tools) != 1 || !strings.Contains(tools[0].Content, title) {
		t.Fatalf("第 2 次模型请求的 tool 消息 = %+v，期望含本轮已创建的待办", tools)
	}
	h.llm.assertScriptConsumed(t)
}

// TestTodoRollbackOnClientCancel 证明 AC-005 的取消边界：Todo 已创建、模型调用进行中，
// 客户端断开后 Run 记为 CANCELLED，本轮暂存的 Todo 与消息都不提交。
func TestTodoRollbackOnClientCancel(t *testing.T) {
	const (
		createCall = "call-cancel-create"
		title      = "取消轮次的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:    4,
		maxToolCalls:   4,
		requestTimeout: 5 * time.Second,
		script: []llmStep{
			todoCreateStep(createCall, title),
			blockStep(),
		},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.requestChat(ctx, sessionID, "创建待办后中途取消")
		done <- err
	}()

	// 等第 2 次模型请求进入阻塞（此刻 Todo 已写入本轮暂存区），再取消客户端请求。
	waitFor(t, 2*time.Second, func() bool {
		_, blocked, _ := h.llm.stats()
		return blocked == 1
	}, "脚本服务进入阻塞响应")
	cancel()

	waitFor(t, 2*time.Second, func() bool {
		return h.recorder.runCount() == 1
	}, "Run 形成终态")
	h.assertTerminal(h.runAt(1), core.RunStatusCancelled, core.KindCancelled)
	h.assertSessionUnchanged(before)

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("客户端取消后仍收到可用响应")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("客户端取消后请求未及时返回")
	}
}

// TestUserTextDoesNotSelectTool 证明工具执行只来自模型返回的 tool_calls：
// 用户消息里出现工具关键词时，Provider 未请求工具就不得执行任何工具。
func TestUserTextDoesNotSelectTool(t *testing.T) {
	const finalReply = "我只回答问题，不调用工具"
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  2,
		maxToolCalls: 2,
		script:       []llmStep{textStep(finalReply)},
	})
	sessionID := h.createSession()

	response := h.chatOK(sessionID, "请帮我创建待办、搜索资料并计算 1+1")
	if response.ToolCalls != 0 || response.LLMCalls != 1 {
		t.Fatalf("计数 = (llm=%d, tool=%d)，期望 (1, 0)：不得按关键词分发工具", response.LLMCalls, response.ToolCalls)
	}
	h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant)

	// 工具定义仍然随请求下发，只是模型这次没有选择调用。
	_, firstRequest := h.llm.requestAt(t, 1)
	if len(firstRequest.Tools) != 5 {
		t.Fatalf("tools[] 数量 = %d，期望 5", len(firstRequest.Tools))
	}
	h.llm.assertScriptConsumed(t)
}

// TestTodoInvalidCallsKeepSessionConsistent 证明 AC-006：非法 Todo ID 与非法参数
// 都以可恢复的工具结果回填，Run 仍然 COMPLETED，会话照常提交且没有待办副作用。
func TestTodoInvalidCallsKeepSessionConsistent(t *testing.T) {
	const (
		missingCall = "call-todo-missing"
		argsCall    = "call-todo-bad-args"
		finalReply  = "两次调用都失败了"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  4,
		maxToolCalls: 4,
		script: []llmStep{
			toolCallsStep(
				scriptCall{ID: missingCall, Name: "todo_complete", Arguments: `{"id":"todo-404"}`},
				scriptCall{ID: argsCall, Name: "todo_create", Arguments: `{}`},
			),
			textStep(finalReply),
		},
	})
	sessionID := h.createSession()

	response := h.chatOK(sessionID, "调用两个非法待办操作")
	if response.ToolCalls != 2 || response.LLMCalls != 2 {
		t.Fatalf("计数 = (llm=%d, tool=%d)，期望 (2, 2)", response.LLMCalls, response.ToolCalls)
	}
	h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleTool, e2eRoleAssistant)

	committed := h.snapshot(sessionID)
	missing := toolMessageOf(t, committed, missingCall)
	if !missing.ToolResult.IsError || !strings.Contains(missing.ToolResult.Content, `"code":"todo_not_found"`) {
		t.Fatalf("非法 ID 结果 = %+v，期望 todo_not_found", missing.ToolResult)
	}
	badArgs := toolMessageOf(t, committed, argsCall)
	if !badArgs.ToolResult.IsError || !strings.Contains(badArgs.ToolResult.Content, "参数校验失败") {
		t.Fatalf("非法参数结果 = %+v，期望参数校验失败", badArgs.ToolResult)
	}
	if len(committed.Todos) != 0 {
		t.Fatalf("非法调用产生了待办：%+v", committed.Todos)
	}
	if committed.Summary != "" {
		t.Fatalf("非法调用改写了摘要：%q", committed.Summary)
	}
	h.llm.assertScriptConsumed(t)
}

// concurrentTodoStep 按请求内容生成行为：本轮第一次调用（最后一条消息是 user）创建
// 以本会话用户消息为标题的待办，拿到本轮工具结果后给出终稿。并发下不依赖请求到达顺序，
// 也不受同会话历史中上一轮 tool 消息的影响。
//
// CallID 由本轮用户消息派生：core 的配对校验要求同一会话历史内 CallID 不重复，
// 同会话的第二次 Run 不能复用上一轮的 ID。
func concurrentTodoStep() llmStep {
	return dynamicStep(func(request wireRequest) (int, string) {
		text := request.lastUserText()
		if lastRoleOf(request) == string(e2eRoleUser) {
			return http.StatusOK, toolCallsBody("resp-todo-create", scriptCall{
				ID:        "call-create:" + text,
				Name:      "todo_create",
				Arguments: mustJSON(map[string]string{"title": text}),
			})
		}
		return http.StatusOK, replyBody("resp-todo-final", "已记录:"+text)
	})
}

// lastRoleOf 返回请求消息序列的最后一条角色；空请求返回空串。
func lastRoleOf(request wireRequest) string {
	if len(request.Messages) == 0 {
		return ""
	}
	return request.Messages[len(request.Messages)-1].Role
}

// TestConcurrentTodoWrites 证明 AC-006 的并发边界：
// 不同会话并发写入互不串话；同一会话并发写入由执行权串行，不丢更新、ID 不冲突。
func TestConcurrentTodoWrites(t *testing.T) {
	t.Run("不同会话并发", func(t *testing.T) {
		const sessionsCount = 3
		steps := make([]llmStep, sessionsCount*2)
		for index := range steps {
			steps[index] = concurrentTodoStep()
		}
		h := newHarness(t, harnessOptions{maxLLMCalls: 20, maxToolCalls: 20, script: steps})

		sessionIDs := make([]string, sessionsCount)
		requests := make(map[string]struct{ sessionID, message string }, sessionsCount)
		for index := 0; index < sessionsCount; index++ {
			sessionIDs[index] = h.createSession()
			title := fmt.Sprintf("会话%d待办", index+1)
			requests[title] = struct{ sessionID, message string }{sessionID: sessionIDs[index], message: title}
		}

		outcomes := runConcurrent(h, requests)
		for _, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("并发 chat %q 传输失败：%v", outcome.tag, outcome.err)
			}
			payload := decodeChatBody(t, outcome.result)
			if payload.Reply != "已记录:"+outcome.tag {
				t.Fatalf("并发 chat 回复 = %q，期望与本会话消息一致", payload.Reply)
			}
		}

		for index, sessionID := range sessionIDs {
			want := fmt.Sprintf("会话%d待办", index+1)
			todos := h.snapshot(sessionID).Todos
			if len(todos) != 1 || todos[0].Title != want || todos[0].Status != session.TodoStatusPending {
				t.Fatalf("会话 %d 的 Todo = %+v，期望唯一 pending 待办 %q", index+1, todos, want)
			}
		}
		h.assertRunCount(sessionsCount)
		h.llm.assertScriptConsumed(t)
	})

	t.Run("同会话并发串行", func(t *testing.T) {
		const concurrency = 2
		steps := make([]llmStep, concurrency*2)
		for index := range steps {
			steps[index] = concurrentTodoStep()
		}
		h := newHarness(t, harnessOptions{maxLLMCalls: 12, maxToolCalls: 12, script: steps})
		sessionID := h.createSession()

		requests := make(map[string]struct{ sessionID, message string }, concurrency)
		for index := 1; index <= concurrency; index++ {
			title := fmt.Sprintf("同会话待办-%d", index)
			requests[title] = struct{ sessionID, message string }{sessionID: sessionID, message: title}
		}

		outcomes := runConcurrent(h, requests)
		for _, outcome := range outcomes {
			if outcome.err != nil {
				t.Fatalf("并发 chat %q 传输失败：%v", outcome.tag, outcome.err)
			}
			decodeChatBody(t, outcome.result)
		}

		committed := h.snapshot(sessionID)
		if len(committed.Todos) != concurrency {
			t.Fatalf("同会话并发后 Todo 数 = %d，期望 %d（无丢更新）：%s",
				len(committed.Todos), concurrency, describeSession(committed))
		}
		ids := make(map[string]bool, concurrency)
		titles := make(map[string]bool, concurrency)
		for _, todo := range committed.Todos {
			if ids[todo.ID] {
				t.Fatalf("并发写入产生重复 ID：%q（%+v）", todo.ID, committed.Todos)
			}
			ids[todo.ID] = true
			titles[todo.Title] = true
		}
		for title := range requests {
			if !titles[title] {
				t.Fatalf("并发写入丢失待办 %q：%+v", title, committed.Todos)
			}
		}
		// 两轮各 4 条消息（user + assistant + tool + assistant），全部提交且历史合法。
		if len(committed.Messages) != concurrency*4 {
			t.Fatalf("同会话并发后消息数 = %d，期望 %d", len(committed.Messages), concurrency*4)
		}
		if err := core.ValidateMessages(committed.Messages); err != nil {
			t.Fatalf("同会话并发后历史不合法：%v", err)
		}
		h.assertRunCount(concurrency)
		h.llm.assertScriptConsumed(t)
	})
}
