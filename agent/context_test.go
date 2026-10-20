package agent

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
)

// summaryMessage 构造上下文管理器输出的摘要 system 消息。
func summaryMessage(text string) core.Message {
	return core.Message{
		Role:    core.Role("system"),
		Content: []core.ContentPart{{Type: testTextType, Text: text}},
	}
}

// newStaticContexts 用固定 perMessage 估算值装配真实上下文管理器，便于固定预算边界。
func newStaticContexts(t *testing.T, perMessage int, summarizer contextmgr.Summarizer) contextmgr.Manager {
	t.Helper()
	manager, err := contextmgr.NewManager(staticEstimator{perMessage: perMessage}, summarizer)
	if err != nil {
		t.Fatalf("构造上下文管理器失败：%v", err)
	}
	return manager
}

// TestRunCompactsOverBudgetHistory 覆盖 AC-007 的压缩路径：发给 Provider 的是压缩视图，
// 摘要随提交写入 Session，被覆盖消息从 Session 移除。
func TestRunCompactsOverBudgetHistory(t *testing.T) {
	provider := newScriptedProvider(
		callsResponse("a2", call("c1", "echo", `{}`)),
		textResponse("a3", "完成"),
	)
	echo := newRecordingTool("echo")
	summarizer := &fakeSummarizer{summary: "压缩摘要"}
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "第一轮"), assistantMsg("a1", "第一答"))

	cfg := baseConfig(t, provider, newRegistry(t, echo), manager)
	cfg.Contexts = newStaticContexts(t, 10, summarizer)
	cfg.TokenBudget = 40
	cfg.KeepRecent = 1
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 2},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if result.LLMCalls != 2 || result.ToolCalls != 1 {
		t.Fatalf("计数不符：LLMCalls=%d ToolCalls=%d", result.LLMCalls, result.ToolCalls)
	}
	if summarizer.callCount() != 1 {
		t.Fatalf("超预算时应压缩一次，实际 %d 次", summarizer.callCount())
	}

	compactedRequest := provider.requestAt(t, 2)
	if len(compactedRequest.Messages) != 3 {
		t.Fatalf("压缩视图应保留 1 条摘要 + 1 个原子单元（2 条），实际 %d 条：%+v",
			len(compactedRequest.Messages), compactedRequest.Messages)
	}
	if compactedRequest.Messages[0].Role != core.Role("system") ||
		messageText(compactedRequest.Messages[0]) != "压缩摘要" {
		t.Fatalf("压缩视图首条必须是新摘要 system 消息：%+v", compactedRequest.Messages[0])
	}
	if ids := messageIDs(compactedRequest.Messages[1:]); len(ids) != 2 || ids[0] != "a2" || ids[1] != "" {
		t.Fatalf("压缩视图应保留最近的原子单元 [a2, tool(c1)]：%v", ids)
	}

	after := sessionSnapshot(t, manager, "s-1")
	if after.Summary != "压缩摘要" {
		t.Fatalf("压缩摘要必须随提交写入 Session，实际 %q", after.Summary)
	}
	if ids := messageIDs(after.Messages); len(ids) != 3 || ids[0] != "a2" || ids[2] != "a3" {
		t.Fatalf("被覆盖消息必须从 Session 移除：%v", ids)
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("压缩后的提交历史必须通过配对校验：%v", err)
	}
}

// TestRunKeepsHistoryWhenWithinBudget 覆盖 AC-007 的非压缩路径：不压缩、摘要不变。
func TestRunKeepsHistoryWhenWithinBudget(t *testing.T) {
	provider := newScriptedProvider(textResponse("a2", "完成"))
	summarizer := &fakeSummarizer{summary: "不该使用"}
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "基线摘要", userMsg("u1", "第一轮"), assistantMsg("a1", "第一答"))

	cfg := baseConfig(t, provider, newRegistry(t), manager)
	cfg.Contexts = newStaticContexts(t, 10, summarizer)
	cfg.TokenBudget = 1000
	cfg.KeepRecent = 1
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	if summarizer.callCount() != 0 {
		t.Fatalf("未超预算时不得压缩，实际摘要调用 %d 次", summarizer.callCount())
	}
	after := sessionSnapshot(t, manager, "s-1")
	if after.Summary != "基线摘要" {
		t.Fatalf("未压缩时摘要必须保持不变，实际 %q", after.Summary)
	}
	if ids := messageIDs(after.Messages); len(ids) != 3 || ids[0] != "u1" || ids[1] != "a1" || ids[2] != "a2" {
		t.Fatalf("未压缩时应原样追加消息：%v", ids)
	}
	firstRequest := provider.requestAt(t, 1)
	if len(firstRequest.Messages) != 3 {
		t.Fatalf("未压缩视图应为 旧摘要 + 2 条历史，实际 %+v", firstRequest.Messages)
	}
	if firstRequest.Messages[0].Role != core.Role("system") ||
		messageText(firstRequest.Messages[0]) != "基线摘要" {
		t.Fatalf("旧摘要必须以 system 消息保留在 Messages 中：%+v", firstRequest.Messages[0])
	}
	if ids := messageIDs(firstRequest.Messages[1:]); len(ids) != 2 || ids[0] != "u1" || ids[1] != "a1" {
		t.Fatalf("System Prompt 之外的会话历史应原样进入视图：%v", ids)
	}
}

// TestRunFailsWhenTokenBudgetUnsatisfiable 覆盖 AC-007 的 Build 失败路径：
// Run 以 invalid_argument 结束，Session 不被部分修改。
func TestRunFailsWhenTokenBudgetUnsatisfiable(t *testing.T) {
	provider := newScriptedProvider(textResponse("a2", "不该发出"))
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "基线摘要", userMsg("u1", "第一轮"), assistantMsg("a1", "第一答"))
	before := sessionSnapshot(t, manager, "s-1")

	cfg := baseConfig(t, provider, newRegistry(t), manager)
	cfg.Contexts = newStaticContexts(t, 10, &fakeSummarizer{summary: "压缩摘要"})
	cfg.TokenBudget = 20
	// KeepRecent 大到没有可压缩的更早历史：预算不可满足。
	cfg.KeepRecent = 100
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 2, MaxToolCalls: 2},
	})

	assertTerminal(t, result, core.RunStatusFailed, core.KindInvalidArgument, "")
	if result.LLMCalls != 0 || provider.callCount() != 0 {
		t.Fatalf("Build 失败时不得发出模型调用：LLMCalls=%d Provider=%d", result.LLMCalls, provider.callCount())
	}
	assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
}

// TestRunFailsWhenSummarizerFails 覆盖 AC-007 的摘要失败路径：internal 且不提交。
func TestRunFailsWhenSummarizerFails(t *testing.T) {
	provider := newScriptedProvider(textResponse("a2", "不该发出"))
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "第一轮"), assistantMsg("a1", "第一答"))
	before := sessionSnapshot(t, manager, "s-1")

	cfg := baseConfig(t, provider, newRegistry(t), manager)
	cfg.Contexts = newStaticContexts(t, 10, &fakeSummarizer{err: errors.New("摘要服务不可用")})
	cfg.TokenBudget = 10
	cfg.KeepRecent = 1
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 2, MaxToolCalls: 2},
	})

	assertTerminal(t, result, core.RunStatusFailed, core.KindInternal, "")
	assertSessionUnchanged(t, before, sessionSnapshot(t, manager, "s-1"))
}

// stubContexts 是受控的上下文管理器：按调用序返回预设视图，用于验证提交阶段的偏移算术。
type stubContexts struct {
	build func(call int, req contextmgr.Request) (contextmgr.Result, error)

	calls int
}

func (s *stubContexts) Build(_ context.Context, req contextmgr.Request) (contextmgr.Result, error) {
	s.calls++
	return s.build(s.calls, req)
}

// TestRunDropsRunMessagesWhenCompactionCrossesBaseline 覆盖契约的偏移规则：
// head 允许越过运行前的基线长度，被摘要覆盖的本 Run 消息不再追加到 Session。
func TestRunDropsRunMessagesWhenCompactionCrossesBaseline(t *testing.T) {
	provider := newScriptedProvider(
		callsResponse("a1", call("c1", "echo", `{}`)),
		textResponse("a2", "完成"),
	)
	echo := newRecordingTool("echo")
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "旧摘要", userMsg("u1", "基线"))
	before := sessionSnapshot(t, manager, "s-1")

	contexts := &stubContexts{}
	contexts.build = func(call int, req contextmgr.Request) (contextmgr.Result, error) {
		if call == 1 {
			return contextmgr.Result{Messages: append([]core.Message{summaryMessage("系统提示")}, req.Messages...)}, nil
		}
		// 第 2 次调用把「当前全部未覆盖历史」都纳入摘要，覆盖范围因此越过基线长度。
		return contextmgr.Result{
			Messages:        []core.Message{summaryMessage("系统提示"), summaryMessage("新摘要")},
			Compacted:       true,
			DroppedMessages: len(req.Messages),
			Summary:         "新摘要",
		}, nil
	}
	cfg := baseConfig(t, provider, newRegistry(t, echo), manager)
	cfg.Contexts = contexts
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 3, MaxToolCalls: 2},
	})

	assertTerminal(t, result, core.RunStatusCompleted, "", "")
	// 第 2 次 Build 时未覆盖历史是 [u1, a1, tool(c1)]，head 因此累计到 3（> baseLen=1）。
	if contexts.calls != 2 {
		t.Fatalf("期望 2 次上下文构建，实际 %d 次", contexts.calls)
	}
	after := sessionSnapshot(t, manager, "s-1")
	if after.Summary != "新摘要" {
		t.Fatalf("压缩摘要必须随提交写入 Session，实际 %q", after.Summary)
	}
	if ids := messageIDs(after.Messages); len(ids) != 1 || ids[0] != "a2" {
		t.Fatalf("被摘要覆盖的基线消息与本 Run 早期消息都不应保留，实际 %v", ids)
	}
	if before.Summary == after.Summary {
		t.Fatalf("压缩后摘要必须更新")
	}
	// RunResult 仍记录本次 Run 全部产出消息（含被摘要覆盖的）。
	if ids := messageIDs(result.Messages); len(ids) != 3 || ids[0] != "a1" || ids[2] != "a2" {
		t.Fatalf("RunResult.Messages 必须保留完整事实记录：%v", ids)
	}
}

// TestStripSystemPrompt 覆盖 System Prompt 与摘要 system 消息的区分：
// 只剥离首条 System Prompt，摘要消息留在 Messages 中。
func TestStripSystemPrompt(t *testing.T) {
	prompt := summaryMessage("系统提示")
	summary := summaryMessage("摘要")
	user := userMsg("u1", "你好")

	cases := []struct {
		name    string
		prompt  string
		input   []core.Message
		want    []core.Message
		wantErr core.ErrorKind
	}{
		{
			name:   "无 System Prompt 时原样返回",
			prompt: "",
			input:  []core.Message{summary, user},
			want:   []core.Message{summary, user},
		},
		{
			name:   "剥离首条 System Prompt 并保留摘要",
			prompt: "系统提示",
			input:  []core.Message{prompt, summary, user},
			want:   []core.Message{summary, user},
		},
		{
			name:    "首条与 SystemPrompt 不一致",
			prompt:  "另一个提示",
			input:   []core.Message{prompt, user},
			wantErr: core.KindInternal,
		},
		{
			name:    "视图为空",
			prompt:  "系统提示",
			input:   nil,
			wantErr: core.KindInternal,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := stripSystemPrompt(tc.input, tc.prompt)
			if tc.wantErr != "" {
				if err == nil || core.KindOf(err) != tc.wantErr {
					t.Fatalf("期望 %s，实际 %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误：%v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("剥离结果不符：\n实际：%v\n期望：%v", messageIDs(got), messageIDs(tc.want))
			}
		})
	}
}
