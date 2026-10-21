package e2e_test

// 本文件覆盖 AC-003 / AC-004 的时间敏感用例：provider 无响应、HTTP 请求超时、
// 客户端断开取消。三类语义必须可区分，且都不得留下半轮提交。

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TestProviderUnresponsive 证明 AC-004「provider 无响应」行：Provider 侧超时后由 agent
// 重分类为 provider_failure（502 + FAILED），而不是 504 + CANCELLED；请求级 ctx 未到期。
func TestProviderUnresponsive(t *testing.T) {
	h := newHarness(t, harnessOptions{
		maxLLMCalls:     4,
		maxToolCalls:    4,
		requestTimeout:  5 * time.Second,
		providerTimeout: 50 * time.Millisecond,
		script:          []llmStep{blockStep()},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, "provider 不响应")
	run := h.runAt(1)

	// Contract 失败矩阵要求「502 + provider_failure + FAILED/provider_failure」。
	// 该三元组是 Owner D3① 的显式决定，不得为让本用例通过而放宽。
	if result.Status != http.StatusBadGateway || !strings.Contains(string(result.Body), `"code":"provider_failure"`) {
		t.Fatalf("Provider 无响应：HTTP = %d %s，Contract 要求 502 + provider_failure；"+
			"实际 Run 终态 = %s/%q（状态码与 Run 终态不自洽）",
			result.Status, result.Body, run.Outcome.Status, core.KindOf(run.Outcome.Err))
	}
	h.assertSessionUnchanged(before)
	h.assertTerminal(run, core.RunStatusFailed, core.KindProviderFailure)
	if run.Outcome.LLMCalls != 1 || run.Outcome.ToolCalls != 0 {
		t.Fatalf("Run 计数 = (llm=%d, tool=%d)，期望 (1, 0)", run.Outcome.LLMCalls, run.Outcome.ToolCalls)
	}

	// 服务端观测是异步的：客户端先拿到结论时，脚本 goroutine 可能尚未记录上游取消。
	// 按 Contract R4 用有界轮询等待观测成立，再断言完整三元组（观察项本身不删除、不弱化）。
	waitFor(t, 2*time.Second, func() bool {
		_, blocked, canceled := h.llm.stats()
		return blocked == 1 && canceled == 1
	}, "脚本服务观察到请求阻塞与上游取消")
	if exhausted, blocked, canceled := h.llm.stats(); blocked != 1 || canceled != 1 || exhausted != 0 {
		t.Fatalf("脚本观测 = (blocked=%d, canceled=%d, exhausted=%d)，期望 (1, 1, 0)", blocked, canceled, exhausted)
	}
	if got := h.llm.requestCount(); got != 1 {
		t.Fatalf("模型请求次数 = %d，期望 1（不重试）", got)
	}
	h.assertRunCount(1)
}

// TestRequestTimeout 证明 AC-004「HTTP 请求超时」行：请求级 ctx 到期后终止，得到
// 504 + deadline_exceeded + CANCELLED，且不会在超时后继续发起新的模型调用。
func TestRequestTimeout(t *testing.T) {
	h := newHarness(t, harnessOptions{
		maxLLMCalls:    4,
		maxToolCalls:   4,
		requestTimeout: 200 * time.Millisecond,
		script:         []llmStep{blockStep()},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	result := h.chat(sessionID, "整体超时")
	h.assertErrorResponse(result, http.StatusGatewayTimeout, "deadline_exceeded")
	h.assertSessionUnchanged(before)

	run := h.runAt(1)
	h.assertTerminal(run, core.RunStatusCancelled, core.KindDeadlineExceeded)
	if run.Outcome.LLMCalls != 1 {
		t.Fatalf("Run LLMCalls = %d，期望 1（超时后不再发起调用）", run.Outcome.LLMCalls)
	}

	// 同上：请求级超时先由客户端侧 ctx 到期产生结论，脚本侧取消观测可能稍后到达。
	waitFor(t, 2*time.Second, func() bool {
		_, blocked, canceled := h.llm.stats()
		return blocked == 1 && canceled == 1
	}, "脚本服务观察到请求阻塞与上游取消")
	if exhausted, blocked, canceled := h.llm.stats(); blocked != 1 || canceled != 1 || exhausted != 0 {
		t.Fatalf("脚本观测 = (blocked=%d, canceled=%d, exhausted=%d)，期望 (1, 1, 0)", blocked, canceled, exhausted)
	}
	if got := h.llm.requestCount(); got != 1 {
		t.Fatalf("模型请求次数 = %d，期望 1", got)
	}
	h.assertRunCount(1)
}

// TestClientDisconnect 证明 AC-004「客户端断开」行：客户端取消请求 ctx 后 Run 记为
// CANCELLED + cancelled，脚本服务观察到上游中止，Session 不留半轮；HTTP 状态码按设计
// 不承诺，因此只断言客户端确实没有得到可用响应。
func TestClientDisconnect(t *testing.T) {
	h := newHarness(t, harnessOptions{
		maxLLMCalls:    4,
		maxToolCalls:   4,
		requestTimeout: 5 * time.Second,
		script:         []llmStep{blockStep()},
	})
	sessionID := h.createSession()
	h.seed(sessionID, "既有摘要", failureSeedTodos())
	before := h.snapshot(sessionID)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type clientOutcome struct {
		result httpResult
		err    error
	}
	done := make(chan clientOutcome, 1)
	go func() {
		result, err := h.requestChat(ctx, sessionID, "客户端中途断开")
		done <- clientOutcome{result: result, err: err}
	}()

	waitFor(t, 2*time.Second, func() bool {
		return h.llm.requestCount() == 1
	}, "脚本服务收到首个模型请求")
	waitFor(t, 2*time.Second, func() bool {
		_, blocked, _ := h.llm.stats()
		return blocked == 1
	}, "脚本服务进入阻塞响应")

	cancel()
	waitFor(t, 2*time.Second, func() bool {
		_, _, canceled := h.llm.stats()
		return canceled == 1
	}, "脚本服务观察到上游请求被中止")

	select {
	case outcome := <-done:
		if outcome.err == nil {
			t.Fatalf("客户端取消后仍收到可用响应：%d %s", outcome.result.Status, outcome.result.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("客户端取消后请求未及时返回")
	}

	waitFor(t, 2*time.Second, func() bool {
		return h.recorder.runCount() == 1
	}, "Run 形成终态")
	h.assertTerminal(h.runAt(1), core.RunStatusCancelled, core.KindCancelled)
	h.assertSessionUnchanged(before)

	if got := h.llm.requestCount(); got != 1 {
		t.Fatalf("模型请求次数 = %d，期望 1（取消后不再调用）", got)
	}
	if got := h.recorder.runCount(); got != 1 {
		t.Fatalf("service.Runner 调用次数 = %d，期望 1", got)
	}
}
