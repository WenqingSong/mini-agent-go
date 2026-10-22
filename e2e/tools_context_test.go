package e2e_test

// 本文件是「工具装配 × 上下文压缩」的联合验收：同一 Run 内 Todo 写入与压缩必须一起提交、
// 一起回滚，不同 Session 的 Todo 与 Summary 必须互不串话。
//
// 装配路径与 harness_test.go 完全一致：真实 httpapi.Server → recordingRunner →
// service.Runner（正式 service.NewToolset）→ agent.Loop → contextmgr（默认
// DigestSummarizer）→ 真实 session.Manager / builtin 工具；模型端只有 loopback
// scripted 服务，不读取、不设置任何真实 Provider 凭据。

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/session"
)

const (
	// jointSummaryMarker 是 DigestSummarizer 的摘要首行标记（与 contextmgr 默认实现一致）。
	jointSummaryMarker = "早期对话摘要"
	// jointQuestionBody 是联合提问的固定长文本：约 300 个 CJK 字符，
	// 使两轮历史加上第三轮输入后必定越过 contextTestBudget。
	jointQuestionBody = "长问题内容"
)

// jointQuestion 生成带会话标记的长提问。标记放在最前面：摘要按消息前缀截断摘录，
// 只有前置标记才能同时出现在摘要与保留消息中，从而让「归属」断言有判别力。
func jointQuestion(marker string, round int) string {
	return marker + strings.Repeat(jointQuestionBody, 60) + fmt.Sprintf("第%d轮", round)
}

// TestJointTodoWriteAndCompactionCommitInSameRun 覆盖联合验收 1：同一个 Run 内既发生
// 上下文压缩、又成功写入 Todo，两者必须在同一次 Turn 提交后同时可见。
func TestJointTodoWriteAndCompactionCommitInSameRun(t *testing.T) {
	const (
		createCall = "call-joint-create"
		title      = "整理联合验收证据"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  8,
		maxToolCalls: 4,
		tokenBudget:  contextTestBudget,
		keepRecent:   contextTestKeepRecent,
		script: []llmStep{
			textStep("第一轮回答"),
			textStep("第二轮回答"),
			todoCreateStep(createCall, title),
			textStep("待办已创建"),
		},
	})
	sessionID := h.createSession()
	h.chatOK(sessionID, jointQuestion("联合", 1))
	h.chatOK(sessionID, jointQuestion("联合", 2))

	// 前提断言：前两轮不得提前压缩，否则「同一 Run」不成立。
	before := h.snapshot(sessionID)
	if before.Summary != "" {
		t.Fatalf("前置轮次已提前压缩（summary=%q），用例前提不成立", before.Summary)
	}
	if len(before.Todos) != 0 {
		t.Fatalf("前置轮次已存在 Todo=%v，期望 0 条", before.Todos)
	}

	created := h.chatOK(sessionID, jointQuestion("联合", 3))
	if created.ToolCalls != 1 || created.LLMCalls != 2 {
		t.Fatalf("联合轮计数 = (llm=%d, tool=%d)，期望 (2, 1)", created.LLMCalls, created.ToolCalls)
	}
	h.llm.assertScriptConsumed(t)
	h.assertRunCount(3)

	// Provider 侧证据：该 Run 的第一次请求已带摘要，证明压缩发生在这个 Run 内，
	// 而不是前两轮遗留的结果。
	_, firstRequest := h.llm.requestAt(t, 3)
	if !strings.Contains(wireText(firstRequest.Messages), jointSummaryMarker) {
		t.Fatalf("本次 Run 的首次请求没有摘要，压缩未发生在同一 Run 内")
	}
	assertWirePairing(t, "联合轮首次请求", firstRequest.Messages)
	// 第二次请求必须同时看到摘要与 Todo 工具结果：压缩与工具链路在同一 Run 内共存。
	_, secondRequest := h.llm.requestAt(t, 4)
	assertWirePairing(t, "联合轮工具调用后的请求", secondRequest.Messages)
	if !strings.Contains(wireText(secondRequest.Messages), title) {
		t.Fatalf("压缩后的视图没有带上 Todo 工具结果：%s", wireText(secondRequest.Messages))
	}

	// 提交侧证据：摘要与 Todo 同时出现在同一次 Run 后的快照里。
	committed := h.snapshot(sessionID)
	if !strings.Contains(committed.Summary, jointSummaryMarker) {
		t.Fatalf("压缩摘要未随同一次提交落库：%q", committed.Summary)
	}
	if len(committed.Todos) != 1 {
		t.Fatalf("Todo 未随同一次提交落库：%v（%s）", committed.Todos, describeSession(committed))
	}
	if committed.Todos[0].Title != title || committed.Todos[0].Status != session.TodoStatusPending {
		t.Fatalf("提交后的 Todo = %+v，期望 pending 且标题为 %q", committed.Todos[0], title)
	}
	if err := core.ValidateMessages(committed.Messages); err != nil {
		t.Fatalf("联合提交后的会话历史非法：%v（%s）", err, describeSession(committed))
	}
	run := h.runAt(3)
	if run.Outcome.Status != core.RunStatusCompleted || run.Outcome.ToolCalls != 1 {
		t.Fatalf("联合轮终态 = (%s, tool=%d)，期望 (COMPLETED, 1)",
			run.Outcome.Status, run.Outcome.ToolCalls)
	}
}

// TestJointTodoAndCompactionRollBackOnProviderFailure 覆盖联合验收 2：压缩已经发生、
// Todo 已经写入 Turn，随后 Provider 失败时必须整轮回滚：Messages、Summary、Todos 全部
// 与运行前逐字段一致。
func TestJointTodoAndCompactionRollBackOnProviderFailure(t *testing.T) {
	const (
		createCall = "call-joint-rollback"
		title      = "会在失败中回滚的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  8,
		maxToolCalls: 4,
		tokenBudget:  contextTestBudget,
		keepRecent:   contextTestKeepRecent,
		script: []llmStep{
			textStep("第一轮回答"),
			textStep("第二轮回答"),
			todoCreateStep(createCall, title),
			statusStep(http.StatusInternalServerError, `{"error":{"message":"upstream failed"}}`),
		},
	})
	sessionID := h.createSession()
	h.chatOK(sessionID, jointQuestion("回滚", 1))
	h.chatOK(sessionID, jointQuestion("回滚", 2))
	before := h.snapshot(sessionID)
	if before.Summary != "" || len(before.Todos) != 0 {
		t.Fatalf("失败前状态不是空摘要/空 Todo：%s", describeSession(before))
	}

	result := h.chat(sessionID, jointQuestion("回滚", 3))
	h.assertErrorResponse(result, http.StatusBadGateway, httpapi.CodeProviderFailure)

	// 该 Run 确实完成了「压缩 + Todo 写入」：压缩体现在第一次请求的摘要上，
	// Todo 体现在第一次请求之后真实执行了一次工具。
	_, firstRequest := h.llm.requestAt(t, 3)
	if !strings.Contains(wireText(firstRequest.Messages), jointSummaryMarker) {
		t.Fatalf("失败轮首次请求没有摘要，压缩未发生，回滚断言没有判别力")
	}
	_, toolRequest := h.llm.requestAt(t, 4)
	if !strings.Contains(wireText(toolRequest.Messages), title) {
		t.Fatalf("失败轮未把 Todo 工具结果回填给 Provider：%s", wireText(toolRequest.Messages))
	}
	assertWirePairing(t, "失败轮工具调用后的请求", toolRequest.Messages)

	// 该会话共有三次 Run，因此这里逐条核对第三次，而不是复用单轮会话的 assertTerminal。
	run := h.runAt(3)
	if run.Err != nil {
		t.Fatalf("失败路径应已形成 Run 结论：%v", run.Err)
	}
	if run.Outcome.Status != core.RunStatusFailed || core.KindOf(run.Outcome.Err) != core.KindProviderFailure {
		t.Fatalf("终态 = (%s,%v)，期望 FAILED/provider_failure", run.Outcome.Status, run.Outcome.Err)
	}
	if run.Outcome.ToolCalls != 1 || run.Outcome.LLMCalls != 2 {
		t.Fatalf("失败轮计数 = (llm=%d, tool=%d)，期望 (2, 1)",
			run.Outcome.LLMCalls, run.Outcome.ToolCalls)
	}
	events := h.finishedEvents(sessionID)
	last := events[len(events)-1]
	if last.RunID != run.Request.RunID || last.Status != string(core.RunStatusFailed) ||
		last.ErrorKind != string(core.KindProviderFailure) {
		t.Fatalf("run_finished 事件与 Run 结论不一致：%+v", last)
	}

	// Messages、Summary、Todos 三者逐字段回滚（assertSessionUnchanged 做深比较）。
	h.assertSessionUnchanged(before)
	after := h.snapshot(sessionID)
	if after.Summary != "" {
		t.Fatalf("失败轮把摘要提交进了 Session：%q", after.Summary)
	}
	if len(after.Todos) != 0 {
		t.Fatalf("失败轮把 Todo 提交进了 Session：%v", after.Todos)
	}
}

// TestJointTodoAndSummaryIsolatedAcrossSessions 覆盖联合验收 3：不同 Session 各自完成
// 「长历史压缩 + Todo 写入」后，Todo 与 Summary 都只属于自己，互不串话。
func TestJointTodoAndSummaryIsolatedAcrossSessions(t *testing.T) {
	const (
		createA = "call-joint-a"
		createB = "call-joint-b"
		titleA  = "会话A的待办"
		titleB  = "会话B的待办"
	)
	h := newHarness(t, harnessOptions{
		maxLLMCalls:  16,
		maxToolCalls: 8,
		tokenBudget:  contextTestBudget,
		keepRecent:   contextTestKeepRecent,
		// 脚本按请求序全局消费：先跑完 A 的三轮，再跑 B 的三轮。
		script: []llmStep{
			textStep("A第一轮回答"),
			textStep("A第二轮回答"),
			todoCreateStep(createA, titleA),
			textStep("A待办已创建"),
			textStep("B第一轮回答"),
			textStep("B第二轮回答"),
			todoCreateStep(createB, titleB),
			textStep("B待办已创建"),
		},
	})
	sessionA := h.createSession()
	sessionB := h.createSession()

	// runJointSession 依次跑三轮：前两轮只积累历史，第三轮同时触发压缩并创建 Todo。
	runJointSession := func(sessionID, marker string) {
		t.Helper()
		for round := 1; round <= 2; round++ {
			h.chatOK(sessionID, jointQuestion(marker, round))
		}
		response := h.chatOK(sessionID, jointQuestion(marker, 3))
		if response.ToolCalls != 1 {
			t.Fatalf("%s 联合轮工具调用数 = %d，期望 1", marker, response.ToolCalls)
		}
	}
	runJointSession(sessionA, "会话A")
	runJointSession(sessionB, "会话B")

	h.llm.assertScriptConsumed(t)
	h.assertRunCount(6)

	for _, item := range []struct {
		sessionID   string
		marker      string
		title       string
		otherMarker string
		otherTitle  string
	}{
		{sessionA, "会话A", titleA, "会话B", titleB},
		{sessionB, "会话B", titleB, "会话A", titleA},
	} {
		committed := h.snapshot(item.sessionID)
		if !strings.Contains(committed.Summary, jointSummaryMarker) {
			t.Fatalf("%s 未发生压缩：%q", item.marker, committed.Summary)
		}
		if !strings.Contains(committed.Summary, item.marker) {
			t.Fatalf("%s 的摘要不含自己的历史：%q", item.marker, committed.Summary)
		}
		if strings.Contains(committed.Summary, item.otherMarker) {
			t.Fatalf("%s 的摘要混入另一会话的历史：%q", item.marker, committed.Summary)
		}
		if len(committed.Todos) != 1 || committed.Todos[0].Title != item.title {
			t.Fatalf("%s 的 Todo = %v，期望只有 %q", item.marker, committed.Todos, item.title)
		}
		// Todo 归属以 Session 的 Todo 状态为准：压缩后的历史是摘要视图，
		// 工具结果可能已被摘要覆盖，不能再用消息文本证明 Todo 归属。
		text := committed.Summary
		for _, message := range committed.Messages {
			text += messageText(message)
		}
		if strings.Contains(text, item.otherTitle) {
			t.Fatalf("%s 混入另一会话的 Todo：%s", item.marker, text)
		}
		if err := core.ValidateMessages(committed.Messages); err != nil {
			t.Fatalf("%s 的会话历史非法：%v（%s）", item.marker, err, describeSession(committed))
		}
	}
}
