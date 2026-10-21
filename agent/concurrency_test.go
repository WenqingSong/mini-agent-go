package agent

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// TestRunSerializesSameSession 覆盖 AC-005 的同会话部分：N 个并发 Run 在会话内串行推进，
// 每个 Run 的 assistant / tool 消息组连续不交错，总数等于各 Run 之和。
func TestRunSerializesSameSession(t *testing.T) {
	const runs = 4

	byRun := make(map[string][]llm.Response, runs)
	for i := 0; i < runs; i++ {
		runID := fmt.Sprintf("r-%d", i)
		byRun[runID] = []llm.Response{
			callsResponse(fmt.Sprintf("a-%d-1", i), call(fmt.Sprintf("c-%d", i), "echo", `{}`)),
			textResponse(fmt.Sprintf("a-%d-2", i), fmt.Sprintf("answer-%d", i)),
		}
	}
	provider := newScriptedProvider().withPerRunScript(byRun)
	echo := newRecordingTool("echo")
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "并发"))
	loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t, echo), manager))

	results := make([]core.RunResult, runs)
	var wg sync.WaitGroup
	wg.Add(runs)
	for i := 0; i < runs; i++ {
		go func(i int) {
			defer wg.Done()
			results[i] = loop.Run(context.Background(), RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: fmt.Sprintf("r-%d", i)},
				Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 2},
			})
		}(i)
	}
	wg.Wait()

	for i, result := range results {
		assertTerminal(t, result, core.RunStatusCompleted, "", "")
		if result.LLMCalls != 2 || result.ToolCalls != 1 {
			t.Fatalf("Run r-%d 计数不符：LLMCalls=%d ToolCalls=%d", i, result.LLMCalls, result.ToolCalls)
		}
	}

	after := sessionSnapshot(t, manager, "s-1")
	// 每个 Run 产出 3 条消息：assistant(ToolCalls) → RoleTool → assistant 文本。
	if want := 1 + runs*3; len(after.Messages) != want {
		t.Fatalf("会话消息总数不符：期望 %d，实际 %d（%v）", want, len(after.Messages), messageIDs(after.Messages))
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("并发提交后历史必须通过配对校验：%v", err)
	}
	// 从第 1 条（user 之后）开始，每 3 条必须属于同一 Run 且内部配对正确。
	for group := 0; group < runs; group++ {
		base := 1 + group*3
		first := after.Messages[base]
		if len(first.ToolCalls) != 1 {
			t.Fatalf("第 %d 组首条必须是携带 1 个 ToolCall 的 assistant：%+v", group, first)
		}
		callID := first.ToolCalls[0].ID
		wantIndex := -1
		for i := 0; i < runs; i++ {
			if callID == fmt.Sprintf("c-%d", i) {
				wantIndex = i
			}
		}
		if wantIndex < 0 {
			t.Fatalf("第 %d 组出现未知 CallID %q", group, callID)
		}
		result := after.Messages[base+1].ToolResult
		if result == nil || result.CallID != callID {
			t.Fatalf("第 %d 组第 2 条必须是 %q 的结果消息：%+v", group, callID, after.Messages[base+1])
		}
		if got := messageText(after.Messages[base+2]); got != fmt.Sprintf("answer-%d", wantIndex) {
			t.Fatalf("第 %d 组的收尾回答不属于同一 Run：%q", group, got)
		}
	}
}

// TestRunAllowsConcurrentDifferentSessions 覆盖 AC-005 的跨会话部分：两个不同 Session 的
// Run 能同时推进到 Provider，证明没有跨会话全局锁。
func TestRunAllowsConcurrentDifferentSessions(t *testing.T) {
	entered := make(chan string, 2)
	release := make(chan struct{})
	provider := newScriptedProvider().withOnCall(func(_ int, req llm.Request) (llm.Response, error) {
		entered <- req.SessionID
		<-release
		return textResponse("a-"+req.SessionID, "ok"), nil
	})
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "会话一"))
	createSession(t, manager, "s-2", "", userMsg("u1", "会话二"))
	loop := mustNewLoop(t, baseConfig(t, provider, newRegistry(t), manager))

	results := make(chan core.RunResult, 2)
	for _, sessionID := range []string{"s-1", "s-2"} {
		go func(sessionID string) {
			results <- loop.Run(context.Background(), RunRequest{
				ExecutionRef: core.ExecutionRef{SessionID: sessionID, RunID: "r-1"},
				Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
			})
		}(sessionID)
	}

	// 两个 Run 都进入 Provider 才能证明并行；只收到一个说明存在跨会话全局锁。
	seen := make(map[string]bool, 2)
	for len(seen) < 2 {
		select {
		case sessionID := <-entered:
			seen[sessionID] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("两个会话未能同时推进，已进入 Provider 的会话：%v", seen)
		}
	}
	close(release)

	for i := 0; i < 2; i++ {
		assertTerminal(t, <-results, core.RunStatusCompleted, "", "")
	}
}

// signalingSink 把事件种类投递到通道，用于观察 Run 是否已开始。
type signalingSink struct {
	kinds chan runlog.EventKind
}

func (s *signalingSink) Record(_ context.Context, ev runlog.Event) error {
	select {
	case s.kinds <- ev.Kind:
	default:
	}
	return nil
}

// TestRunCancelledWhileWaitingForSessionAuthority 覆盖契约的中断点 1：
// 排队等待执行权时取消，回调不执行、无计数、无提交。
func TestRunCancelledWhileWaitingForSessionAuthority(t *testing.T) {
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "占用执行权"))
	before := sessionSnapshot(t, manager, "s-1")

	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	holderDone := make(chan error, 1)
	go func() {
		holderDone <- manager.RunTurn(context.Background(), "s-1", func(*session.Turn) error {
			close(holderEntered)
			<-releaseHolder
			return nil
		})
	}()
	<-holderEntered

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sink := &signalingSink{kinds: make(chan runlog.EventKind, 4)}
	provider := newScriptedProvider(textResponse("a1", "不该发出"))
	cfg := baseConfig(t, provider, newRegistry(t), manager)
	cfg.Sink = sink
	loop := mustNewLoop(t, cfg)

	done := make(chan core.RunResult, 1)
	go func() {
		done <- loop.Run(ctx, RunRequest{
			ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
			Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
		})
	}()

	// 等到 run_started 后取消：此时 Run 正在等待会话执行权。
	select {
	case kind := <-sink.kinds:
		if kind != runlog.KindRunStarted {
			t.Fatalf("首个事件必须是 run_started，实际 %s", kind)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run 未在 watchdog 内开始")
	}
	cancel()

	select {
	case result := <-done:
		assertTerminal(t, result, core.RunStatusCancelled, core.KindCancelled, "")
		if result.LLMCalls != 0 || len(result.Messages) != 0 {
			t.Fatalf("排队取消时不得有任何调用或消息：LLMCalls=%d Messages=%d", result.LLMCalls, len(result.Messages))
		}
		assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
	case <-time.After(2 * time.Second):
		t.Fatalf("排队取消后 Run 未在 watchdog 内返回")
	}

	close(releaseHolder)
	if err := <-holderDone; err != nil {
		t.Fatalf("持有执行权的基线 Turn 应成功：%v", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("排队取消不得发出模型调用，实际 %d 次", provider.callCount())
	}
}
