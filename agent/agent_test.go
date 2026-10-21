package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// TestRunCompletesOnPlainTextResponse 覆盖 AC-001：一次不含 ToolCall 的纯文本响应即完成，
// 消息整轮提交、计数为 1/0，且不发生任何工具执行。
func TestRunCompletesOnPlainTextResponse(t *testing.T) {
	provider := newScriptedProvider(textResponse("a1", "最终回答"))
	echo := newRecordingTool("echo")
	registry := newRegistry(t, echo)
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "你好"))
	before := sessionSnapshot(t, manager, "s-1")

	loop := mustNewLoop(t, baseConfig(t, provider, registry, manager))
	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if result.LLMCalls != 1 || result.ToolCalls != 0 {
		t.Fatalf("计数不符：期望 LLMCalls=1 ToolCalls=0，实际 %d/%d", result.LLMCalls, result.ToolCalls)
	}
	if echo.execCount() != 0 {
		t.Fatalf("不得发生工具执行，实际执行 %d 次", echo.execCount())
	}

	after := sessionSnapshot(t, manager, "s-1")
	if ids := messageIDs(after.Messages); len(ids) != 2 || ids[0] != "u1" || ids[1] != "a1" {
		t.Fatalf("会话历史不符：%v", ids)
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("提交后的会话历史必须通过配对校验：%v", err)
	}
	if after.Summary != before.Summary {
		t.Fatalf("未压缩时摘要不得改变：%q -> %q", before.Summary, after.Summary)
	}
	if ids := messageIDs(result.Messages); len(ids) != 1 || ids[0] != "a1" {
		t.Fatalf("RunResult.Messages 只应包含本次 Run 产出的消息：%v", ids)
	}

	request := provider.requestAt(t, 1)
	if request.System != "系统提示" {
		t.Fatalf("System Prompt 必须经 Request.System 传递，实际 %q", request.System)
	}
	for _, msg := range request.Messages {
		if msg.Role == core.Role("system") {
			t.Fatalf("System Prompt 不得以 system 消息混入 Messages：%+v", request.Messages)
		}
	}
	if len(request.Tools) != 1 || request.Tools[0].Name != "echo" {
		t.Fatalf("Tools 应来自 Registry.Specs()，实际 %+v", request.Tools)
	}
}

// TestRunLoopsThroughToolsAcrossSteps 覆盖 AC-002：三步脚本驱动两次工具调用，
// 每次请求都已携带上一轮的 assistant ToolCalls 与配对的 RoleTool 结果。
func TestRunLoopsThroughToolsAcrossSteps(t *testing.T) {
	provider := newScriptedProvider(
		callsResponse("a1", call("c1", "echo", `{"v":1}`)),
		callsResponse("a2", call("c2", "echo", `{"v":2}`)),
		textResponse("a3", "完成"),
	)
	echo := newRecordingTool("echo")
	registry := newRegistry(t, echo)
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "开始"))
	loop := mustNewLoop(t, baseConfig(t, provider, registry, manager))

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 5, MaxToolCalls: 5},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if result.LLMCalls != 3 || result.ToolCalls != 2 {
		t.Fatalf("计数不符：期望 LLMCalls=3 ToolCalls=2，实际 %d/%d", result.LLMCalls, result.ToolCalls)
	}

	executed := echo.executed()
	if len(executed) != 2 {
		t.Fatalf("工具应各执行一次，实际 %d 次", len(executed))
	}
	if executed[0].ID != "c1" || string(executed[0].Arguments) != `{"v":1}` {
		t.Fatalf("第 1 次工具调用参数不符：%+v", executed[0])
	}
	if executed[1].ID != "c2" || string(executed[1].Arguments) != `{"v":2}` {
		t.Fatalf("第 2 次工具调用参数不符：%+v", executed[1])
	}

	second := provider.requestAt(t, 2)
	if ids := messageIDs(second.Messages); len(ids) != 3 || ids[0] != "u1" || ids[1] != "a1" || ids[2] != "" {
		t.Fatalf("第 2 次请求应包含 assistant 的 ToolCalls 与其结果：%v", ids)
	}
	results := toolResults(second.Messages)
	if len(results) != 1 || results[0].CallID != "c1" {
		t.Fatalf("第 2 次请求的结果不符：%+v", results)
	}

	third := provider.requestAt(t, 3)
	if results := toolResults(third.Messages); len(results) != 2 ||
		results[0].CallID != "c1" || results[1].CallID != "c2" {
		t.Fatalf("第 3 次请求应包含两轮结果：%+v", results)
	}

	after := sessionSnapshot(t, manager, "s-1")
	if ids := messageIDs(after.Messages); len(ids) != 6 || ids[0] != "u1" || ids[5] != "a3" {
		t.Fatalf("会话历史不符：%v", ids)
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("提交后的会话历史必须通过配对校验：%v", err)
	}
}

// TestRunHandlesMultipleCallsInSingleResponse 覆盖 AC-003：单响应 3 个 Call（成功 / 未知工具 /
// 参数非法）各产生一条 RoleTool，全部回填给模型且不终止 Run。
func TestRunHandlesMultipleCallsInSingleResponse(t *testing.T) {
	provider := newScriptedProvider(
		callsResponse("a1",
			call("c1", "write", `{"path":"a.txt"}`),
			call("c2", "filesystem_delete", `{}`),
			call("c3", "write", `{}`),
		),
		textResponse("a2", "已处理"),
	)
	write := newRecordingTool("write").withParameters(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)
	registry := newRegistry(t, write)
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "并发调用"))
	loop := mustNewLoop(t, baseConfig(t, provider, registry, manager))

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 5, MaxToolCalls: 5},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if result.ToolCalls != 3 {
		t.Fatalf("ToolCalls 必须等于实际执行的调用数 3，实际 %d", result.ToolCalls)
	}
	results := toolResults(result.Messages)
	if len(results) != 3 {
		t.Fatalf("每个 Call 恰一条 RoleTool，期望 3 条，实际 %d 条", len(results))
	}
	if results[0].CallID != "c1" || results[0].IsError {
		t.Fatalf("c1 应成功：%+v", results[0])
	}
	if results[1].CallID != "c2" || !results[1].IsError {
		t.Fatalf("c2 未知工具应作为可恢复失败回填：%+v", results[1])
	}
	if results[2].CallID != "c3" || !results[2].IsError {
		t.Fatalf("c3 参数非法应作为可恢复失败回填：%+v", results[2])
	}
	if write.execCount() != 1 {
		t.Fatalf("参数非法的调用不得进入工具实现，实际执行 %d 次", write.execCount())
	}

	second := provider.requestAt(t, 2)
	secondResults := toolResults(second.Messages)
	if len(secondResults) != 3 {
		t.Fatalf("下一次请求必须包含全部 3 条结果，实际 %d 条", len(secondResults))
	}
	for i, callID := range []string{"c1", "c2", "c3"} {
		if secondResults[i].CallID != callID {
			t.Fatalf("结果顺序应与 ToolCalls 数组序一致：位置 %d 期望 %s，实际 %s", i, callID, secondResults[i].CallID)
		}
	}

	if err := core.ValidateMessages(sessionSnapshot(t, manager, "s-1").Messages); err != nil {
		t.Fatalf("会话历史必须通过配对校验：%v", err)
	}
}

// TestRunFailureSemantics 覆盖 AC-004：失败注入下的终态、错误种类、计数与「不提交」。
func TestRunFailureSemantics(t *testing.T) {
	t.Run("Provider 失败后不执行任何工具", func(t *testing.T) {
		provider := newScriptedProvider(callsResponse("a1", call("c1", "echo", `{}`))).
			withErrorAt(1, errors.New("upstream 500"))
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "触发失败"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
		})

		assertTerminal(t, result, core.RunStatusFailed, core.KindProviderFailure, "")
		if result.LLMCalls != 1 {
			t.Fatalf("失败的那次模型调用必须计入，实际 LLMCalls=%d", result.LLMCalls)
		}
		if result.ToolCalls != 0 || echo.execCount() != 0 {
			t.Fatalf("Provider 失败后不得执行工具：ToolCalls=%d 执行次数=%d", result.ToolCalls, echo.execCount())
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("工具 panic 映射 tool_failure 并定位 CallID", func(t *testing.T) {
		provider := newScriptedProvider(callsResponse("a1",
			call("c1", "echo", `{}`),
			call("c2", "boom", `{}`),
			call("c3", "echo", `{}`),
		))
		echo := newRecordingTool("echo")
		boom := newRecordingTool("boom").withFunc(func(context.Context, tool.Request) (core.ToolResult, error) {
			panic("工具内部崩溃")
		})
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "触发 panic"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo, boom), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 5},
		})

		assertTerminal(t, result, core.RunStatusFailed, core.KindToolFailure, "c2")
		if result.ToolCalls != 2 {
			t.Fatalf("panic 的那次尝试必须计入 ToolCalls，实际 %d", result.ToolCalls)
		}
		if obj := toolResults(result.Messages); len(obj) != 1 || obj[0].CallID != "c1" {
			t.Fatalf("已产生的工具结果必须保留，且不为失败 Call 伪造结果：%+v", obj)
		}
		if echo.execCount() != 1 {
			t.Fatalf("panic 之后的调用不得执行，echo 实际执行 %d 次", echo.execCount())
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("模型调用预算耗尽时不再发出下一次调用", func(t *testing.T) {
		provider := newScriptedProvider(
			callsResponse("a1", call("c1", "echo", `{}`)),
			callsResponse("a2", call("c2", "echo", `{}`)),
			textResponse("a3", "不该到这里"),
		)
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "预算"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 2, MaxToolCalls: 5},
		})

		assertTerminal(t, result, core.RunStatusLimitExceeded, core.KindLLMCallLimit, "")
		if result.LLMCalls != 2 || provider.callCount() != 2 {
			t.Fatalf("第 MaxLLMCalls+1 次调用不得发出：LLMCalls=%d Provider 调用=%d", result.LLMCalls, provider.callCount())
		}
		if result.ToolCalls != 2 {
			t.Fatalf("触限前已发起的工具调用应保留计数，实际 %d", result.ToolCalls)
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("工具预算整批预检：额度不足一个都不执行", func(t *testing.T) {
		provider := newScriptedProvider(callsResponse("a1",
			call("c1", "echo", `{}`),
			call("c2", "echo", `{}`),
			call("c3", "echo", `{}`),
		))
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "工具预算"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 2},
		})

		assertTerminal(t, result, core.RunStatusLimitExceeded, core.KindToolCallLimit, "")
		if result.ToolCalls != 0 || echo.execCount() != 0 {
			t.Fatalf("整批预检失败时不得执行任何调用：ToolCalls=%d 执行次数=%d", result.ToolCalls, echo.execCount())
		}
		if results := toolResults(result.Messages); len(results) != 0 {
			t.Fatalf("不得为未执行的 Call 伪造结果：%+v", results)
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("模型调用期间父 ctx 取消", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		provider := newScriptedProvider().withOnCall(func(int, llm.Request) (llm.Response, error) {
			cancel()
			return llm.Response{}, ctx.Err()
		})
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "取消"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(ctx, RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
		})

		assertTerminal(t, result, core.RunStatusCancelled, core.KindCancelled, "")
		if result.LLMCalls != 1 {
			t.Fatalf("已发出的模型调用必须计入，实际 %d", result.LLMCalls)
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("整体超时映射 deadline_exceeded", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		provider := newScriptedProvider().withOnCall(func(int, llm.Request) (llm.Response, error) {
			// 不 sleep：等待投递到 Provider 的 ctx 到期，超时归因由 Loop 判定。
			<-ctx.Done()
			return llm.Response{}, ctx.Err()
		})
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "超时"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(ctx, RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
		})

		assertTerminal(t, result, core.RunStatusCancelled, core.KindDeadlineExceeded, "")
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("ctx 已超时时不发出模型调用", func(t *testing.T) {
		provider := newScriptedProvider(textResponse("a1", "不该发出"))
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "立即超时"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t), manager))

		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		result := loop.Run(ctx, RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
		})

		assertTerminal(t, result, core.RunStatusCancelled, core.KindDeadlineExceeded, "")
		if result.LLMCalls != 0 || provider.callCount() != 0 {
			t.Fatalf("ctx 已结束时不得发出模型调用：LLMCalls=%d 调用次数=%d", result.LLMCalls, provider.callCount())
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})
}

// TestRunStopsOnCancellationDuringToolExecution 覆盖 AC-004⑤ 的工具执行中断点：
// 工具已计入一次执行尝试，但半截结果不进入 Session。
func TestRunStopsOnCancellationDuringToolExecution(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	toolEntered := make(chan struct{})
	provider := newScriptedProvider(callsResponse("a1", call("c1", "slow", `{}`)))
	slow := newRecordingTool("slow").withFunc(func(toolCtx context.Context, _ tool.Request) (core.ToolResult, error) {
		close(toolEntered)
		<-toolCtx.Done()
		return core.ToolResult{}, toolCtx.Err()
	})
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "工具中取消"))
	before := sessionSnapshot(t, manager, "s-1")
	loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, slow), manager))

	type outcome struct {
		result core.RunResult
	}
	done := make(chan outcome, 1)
	go func() {
		done <- outcome{result: loop.Run(ctx, RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
		})}
	}()

	<-toolEntered
	cancel()

	select {
	case out := <-done:
		assertTerminal(t, out.result, core.RunStatusCancelled, core.KindCancelled, "")
		if out.result.ToolCalls != 1 {
			t.Fatalf("工具执行尝试必须计入，实际 %d", out.result.ToolCalls)
		}
		if results := toolResults(out.result.Messages); len(results) != 0 {
			t.Fatalf("取消的工具调用不得伪造结果：%+v", results)
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	case <-time.After(2 * time.Second):
		t.Fatalf("取消后 Run 未在 watchdog 内返回")
	}
}

// TestRunNeverRepeatsSideEffects 覆盖 AC-006：重复 CallID 不重复执行；
// 已执行副作用不因后续失败被撤销、也不自动重试。
func TestRunNeverRepeatsSideEffects(t *testing.T) {
	t.Run("同一 Run 内重复 CallID 不重复执行", func(t *testing.T) {
		provider := newScriptedProvider(
			callsResponse("a1", call("c1", "echo", `{}`)),
			callsResponse("a2", call("c1", "echo", `{}`)),
			textResponse("a3", "不该到这里"),
		)
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "重复调用"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 5, MaxToolCalls: 5},
		})

		assertTerminal(t, result, core.RunStatusFailed, core.KindInvalidPairing, "c1")
		if echo.execCount() != 1 {
			t.Fatalf("重复 CallID 不得再次执行，实际执行 %d 次", echo.execCount())
		}
		if result.ToolCalls != 1 {
			t.Fatalf("被拒绝的重复调用不计入 ToolCalls，实际 %d", result.ToolCalls)
		}
		if results := toolResults(result.Messages); len(results) != 1 {
			t.Fatalf("重复调用不得产生第二条结果：%+v", results)
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})

	t.Run("工具已执行后 Provider 失败：副作用不回滚、不伪造结果、不重试", func(t *testing.T) {
		provider := newScriptedProvider(callsResponse("a1", call("c1", "echo", `{}`))).
			withErrorAt(2, errors.New("第二次模型调用失败"))
		echo := newRecordingTool("echo")
		manager := newSessionManager(t)
		createSession(t, manager, "s-1", "", userMsg("u1", "工具后失败"))
		before := sessionSnapshot(t, manager, "s-1")
		loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

		result := loop.Run(context.Background(), RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 5, MaxToolCalls: 5},
		})

		assertTerminal(t, result, core.RunStatusFailed, core.KindProviderFailure, "")
		if echo.execCount() != 1 {
			t.Fatalf("失败后不得自动重试已执行的调用，实际执行 %d 次", echo.execCount())
		}
		if result.ToolCalls != 1 {
			t.Fatalf("ToolCalls 必须反映真实执行次数 1，实际 %d", result.ToolCalls)
		}
		results := toolResults(result.Messages)
		if len(results) != 1 || results[0].CallID != "c1" || results[0].Content != "ok:c1" {
			t.Fatalf("已产生的工具结果必须原样保留：%+v", results)
		}
		for _, msg := range result.Messages {
			text := messageText(msg)
			for _, forbidden := range []string{"回滚", "补偿", "rollback", "compensat"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("不得声称已回滚或已补偿工具副作用：%q", text)
				}
			}
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	})
}

// TestRunFinishReasonSemantics 覆盖 INV-008 / D5：finish_reason 与 ToolCall 的组合判定，
// 只有 stop + 无 Call 才能 COMPLETED，其余异常结束语义一律 provider_failure。
func TestRunFinishReasonSemantics(t *testing.T) {
	lengthResponse := func(calls ...core.ToolCall) llm.Response {
		return llm.Response{
			Message:      core.Message{ID: "a1", Role: testRoleAssistant, ToolCalls: calls},
			FinishReason: llm.FinishReason("length"),
		}
	}

	cases := []struct {
		name          string
		script        []llm.Response
		wantStatus    core.RunStatus
		wantKind      core.ErrorKind
		wantLLMCalls  int
		wantToolCalls int
		wantExec      int
	}{
		{
			name:         "stop 且无 ToolCall → COMPLETED",
			script:       []llm.Response{textResponse("a1", "收尾")},
			wantStatus:   core.RunStatusCompleted,
			wantLLMCalls: 1,
		},
		{
			name:         "length + 纯文本 → provider_failure",
			script:       []llm.Response{lengthResponse()},
			wantStatus:   core.RunStatusFailed,
			wantKind:     core.KindProviderFailure,
			wantLLMCalls: 1,
		},
		{
			name:         "length + ToolCall → provider_failure 且不执行工具",
			script:       []llm.Response{lengthResponse(call("c1", "echo", `{}`))},
			wantStatus:   core.RunStatusFailed,
			wantKind:     core.KindProviderFailure,
			wantLLMCalls: 1,
		},
		{
			name: "tool_calls 却无任何 ToolCall → provider_failure",
			script: []llm.Response{{
				Message:      assistantMsg("a1", ""),
				FinishReason: llm.FinishReason("tool_calls"),
			}},
			wantStatus:   core.RunStatusFailed,
			wantKind:     core.KindProviderFailure,
			wantLLMCalls: 1,
		},
		{
			name: "unknown → provider_failure",
			script: []llm.Response{{
				Message:      assistantMsg("a1", "?"),
				FinishReason: llm.FinishReason("unknown"),
			}},
			wantStatus:   core.RunStatusFailed,
			wantKind:     core.KindProviderFailure,
			wantLLMCalls: 1,
		},
		{
			name:          "stop + ToolCall → 执行工具并继续循环",
			script:        []llm.Response{stopWithCalls("a1", call("c1", "echo", `{}`)), textResponse("a2", "收尾")},
			wantStatus:    core.RunStatusCompleted,
			wantLLMCalls:  2,
			wantToolCalls: 1,
			wantExec:      1,
		},
		{
			name: "响应结构非法（角色不是 assistant）→ invalid_argument",
			script: []llm.Response{{
				Message:      userMsg("a1", "不是助手"),
				FinishReason: llm.FinishReason("stop"),
			}},
			wantStatus:   core.RunStatusFailed,
			wantKind:     core.KindInvalidArgument,
			wantLLMCalls: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := newScriptedProvider(tc.script...)
			echo := newRecordingTool("echo")
			manager := newSessionManager(t)
			createSession(t, manager, "s-1", "", userMsg("u1", "结束语义"))
			before := sessionSnapshot(t, manager, "s-1")
			loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

			result := loop.Run(context.Background(), RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 3},
			})

			assertTerminal(t, result, tc.wantStatus, tc.wantKind, "")
			if result.LLMCalls != tc.wantLLMCalls {
				t.Fatalf("LLMCalls 不符：期望 %d，实际 %d", tc.wantLLMCalls, result.LLMCalls)
			}
			if result.ToolCalls != tc.wantToolCalls {
				t.Fatalf("ToolCalls 不符：期望 %d，实际 %d", tc.wantToolCalls, result.ToolCalls)
			}
			if echo.execCount() != tc.wantExec {
				t.Fatalf("工具执行次数不符：期望 %d，实际 %d", tc.wantExec, echo.execCount())
			}
			if tc.wantStatus != core.RunStatusCompleted {
				assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
			}
		})
	}
}

// stopWithCalls 构造 finish_reason=stop 但携带 ToolCall 的响应（D5-5：仍进入工具流程）。
func stopWithCalls(id string, calls ...core.ToolCall) llm.Response {
	return llm.Response{
		Message:      core.Message{ID: id, Role: testRoleAssistant, ToolCalls: calls},
		FinishReason: llm.FinishReason("stop"),
	}
}

// TestRunCompletesAtExactBudget 覆盖 INV-003 的边界：恰好用满双预算仍然 COMPLETED。
func TestRunCompletesAtExactBudget(t *testing.T) {
	provider := newScriptedProvider(
		callsResponse("a1", call("c1", "echo", `{}`)),
		callsResponse("a2", call("c2", "echo", `{}`)),
		textResponse("a3", "收尾"),
	)
	echo := newRecordingTool("echo")
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "用满预算"))
	loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 2},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if result.LLMCalls != 3 || result.ToolCalls != 2 {
		t.Fatalf("恰好用满预算的计数不符：LLMCalls=%d ToolCalls=%d", result.LLMCalls, result.ToolCalls)
	}
	if echo.execCount() != 2 {
		t.Fatalf("工具应执行 2 次，实际 %d", echo.execCount())
	}
}

// TestRunRejectsInvalidRequestBeforeAcquiringAuthority 覆盖 INV-002 的前置契约错误：
// 直接返回 FAILED + invalid_argument，不发出事件、不改动会话。
func TestRunRejectsInvalidRequestBeforeAcquiringAuthority(t *testing.T) {
	provider := newScriptedProvider(textResponse("a1", "不该发出"))
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "前置校验"))
	before := sessionSnapshot(t, manager, "s-1")

	cases := []struct {
		name string
		ctx  context.Context
		req  RunRequest
	}{
		{
			name: "context 为空",
			ctx:  nil,
			req: RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
			},
		},
		{
			name: "SessionID 为空",
			ctx:  context.Background(),
			req: RunRequest{
				ExecutionRef: core.ExecutionRef{RunID: "r-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
			},
		},
		{
			name: "RunID 为空",
			ctx:  context.Background(),
			req: RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: "s-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
			},
		},
		{
			name: "预算非正",
			ctx:  context.Background(),
			req: RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 0, MaxToolCalls: 1},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sink := &recordingSink{}
			cfg := baseConfig(t, provider, newRegistry(t), manager)
			cfg.Sink = sink
			loop := mustNewLoop(t, cfg)

			result := loop.Run(tc.ctx, tc.req)

			assertTerminal(t, result, core.RunStatusFailed, core.KindInvalidArgument, "")
			if provider.callCount() != 0 {
				t.Fatalf("前置校验失败不得发出模型调用，实际 %d 次", provider.callCount())
			}
			if len(sink.events) != 0 {
				t.Fatalf("ExecutionRef 不可信时不构成一次 Run，不得发出事件：%+v", sink.events)
			}
			assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
		})
	}
}

// TestNewRejectsMissingDependencies 覆盖装配期契约：四个必填依赖缺失都是 invalid_argument。
func TestNewRejectsMissingDependencies(t *testing.T) {
	provider := newScriptedProvider()
	manager := newSessionManager(t)
	registry := newRegistry(t)
	cfg := baseConfig(t, provider, registry, manager)

	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "Provider 为 nil", mutate: func(c *Config) { c.Provider = nil }},
		{name: "Sessions 为 nil", mutate: func(c *Config) { c.Sessions = nil }},
		{name: "Registry 为 nil", mutate: func(c *Config) { c.Registry = nil }},
		{name: "Contexts 为 nil", mutate: func(c *Config) { c.Contexts = nil }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			current := cfg
			tc.mutate(&current)
			if _, err := New(current); err == nil || core.KindOf(err) != core.KindInvalidArgument {
				t.Fatalf("期望 invalid_argument，实际 %v", err)
			}
		})
	}
}

// TestNewDefaultsSinkToDiscard 覆盖默认装配：未注入 Sink 时使用 Discard，Run 正常完成。
func TestNewDefaultsSinkToDiscard(t *testing.T) {
	provider := newScriptedProvider(textResponse("a1", "默认 Sink"))
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "你好"))
	cfg := baseConfig(t, provider, newRegistry(t), manager)
	cfg.Sink = nil
	loop := mustNewLoop(t, cfg)
	if loop.cfg.Sink == nil {
		t.Fatalf("Sink 为 nil 时必须回退到 runlog.Discard()")
	}

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
	})
	assertTerminal(t, result, core.RunStatusCompleted, "", "")
}
