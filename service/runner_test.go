package service

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// 契约层枚举字面量：core 未导出 Role / ContentType 常量，测试只能用字面量转换。
const (
	testRoleUser      = core.Role("user")
	testRoleAssistant = core.Role("assistant")
	testRoleTool      = core.Role("tool")
	testTextType      = core.ContentType("text")
)

// countingSessions 包装真实 session.Manager，统计真实 RunTurn 次数。
//
// 「一次 chat 恰好一次真实 RunTurn」是本任务最关键的装配不变量（INV-H05 / INV-H06）：
// 计数直接对应向会话层索取执行权的次数。
type countingSessions struct {
	manager *session.Manager

	mu    sync.Mutex
	calls int
}

// RunTurn 计数后转交真实 Manager。
func (c *countingSessions) RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error {
	c.mu.Lock()
	c.calls++
	c.mu.Unlock()
	return c.manager.RunTurn(ctx, sessionID, fn)
}

// count 返回真实 RunTurn 次数。
func (c *countingSessions) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// commitRejectingSessions 在真实 Turn 内执行回调后再拒绝提交，模拟「Run 已完成但会话提交失败」。
type commitRejectingSessions struct {
	manager *session.Manager
}

// RunTurn 执行回调后固定返回 internal 错误，使整轮被丢弃。
func (c commitRejectingSessions) RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error {
	return c.manager.RunTurn(ctx, sessionID, func(turn *session.Turn) error {
		if err := fn(turn); err != nil {
			return err
		}
		return core.NewError(core.KindInternal, "service.test", "提交被拒绝")
	})
}

// scriptedProvider 是脚本化 Provider：按调用序返回预设响应，支持阻塞与自定义行为。
type scriptedProvider struct {
	mu       sync.Mutex
	script   []llm.Response
	calls    int
	requests []llm.Request
	onCall   func(ctx context.Context, call int) (llm.Response, error)
}

// Complete 实现 llm.Provider。
func (p *scriptedProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, req)
	onCall := p.onCall
	var response llm.Response
	found := call <= len(p.script)
	if found {
		response = p.script[call-1]
	}
	p.mu.Unlock()

	if onCall != nil {
		return onCall(ctx, call)
	}
	if !found {
		return llm.Response{}, errors.New("scripted provider: 脚本已耗尽")
	}
	return response, nil
}

// callCount 返回 Provider 调用次数。
func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// requestAt 返回第 n 次（1 起）模型请求。
func (p *scriptedProvider) requestAt(t *testing.T, n int) llm.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.requests) {
		t.Fatalf("模型请求序号 %d 越界：共 %d 次", n, len(p.requests))
	}
	return p.requests[n-1]
}

// recordingTool 是记录型工具：证明工具真实执行并保留参数。
type recordingTool struct {
	name string

	mu    sync.Mutex
	calls []core.ToolCall
}

// Spec 实现 tool.Tool。
func (r *recordingTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        r.name,
		Description: r.name + " 测试工具",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

// Execute 实现 tool.Tool。
func (r *recordingTool) Execute(_ context.Context, req tool.Request) (core.ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req.Call)
	r.mu.Unlock()
	return core.ToolResult{CallID: req.Call.ID, Name: req.Call.Name, Content: "ok:" + req.Call.ID}, nil
}

// executed 返回真实执行过的调用序列。
func (r *recordingTool) executed() []core.ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.ToolCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// countingSummarizer 统计被调用次数；v1 禁用压缩，正常路径必须为 0。
type countingSummarizer struct {
	mu    sync.Mutex
	calls int
}

// Summarize 实现 contextmgr.Summarizer，并记录调用。
func (s *countingSummarizer) Summarize(context.Context, contextmgr.SummarizeRequest) (string, error) {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	return "", core.NewError(core.KindInternal, "service.test", "不得调用 Summarizer")
}

// callCount 返回调用次数。
func (s *countingSummarizer) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// userMessageFor 构造测试用 user 消息。
func userMessageFor(text string) core.Message {
	return core.Message{Role: testRoleUser, Content: []core.ContentPart{{Type: testTextType, Text: text}}}
}

// assistantMessageFor 构造测试用 assistant 消息。
func assistantMessageFor(text string) core.Message {
	return core.Message{Role: testRoleAssistant, Content: []core.ContentPart{{Type: testTextType, Text: text}}}
}

// textResponse 构造纯文本收尾的模型响应。
func textResponse(id, text string) llm.Response {
	return llm.Response{
		Message:      assistantMessageFor(text),
		FinishReason: llm.FinishReason("stop"),
		Usage:        llm.Usage{InputTokens: 3, OutputTokens: 2},
	}
}

// callsResponse 构造请求工具调用的模型响应。
func callsResponse(id string, calls ...core.ToolCall) llm.Response {
	message := core.Message{ID: id, Role: testRoleAssistant, ToolCalls: calls}
	return llm.Response{
		Message:      message,
		FinishReason: llm.FinishReason("tool_calls"),
		Usage:        llm.Usage{InputTokens: 3, OutputTokens: 2},
	}
}

// toolCall 构造一次工具调用。
func toolCall(id, name, arguments string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
}

// newManager 返回不启用过期的内存会话管理器。
func newManager() *session.Manager {
	return session.NewManager(session.ManagerConfig{})
}

// seedSession 创建会话并写入基线历史。
func seedSession(t *testing.T, manager *session.Manager, id string, messages ...core.Message) {
	t.Helper()
	ctx := context.Background()
	if _, err := manager.Create(ctx, id); err != nil {
		t.Fatalf("创建会话失败：%v", err)
	}
	if err := manager.Save(ctx, session.Session{ID: id, Messages: messages}); err != nil {
		t.Fatalf("写入会话基线失败：%v", err)
	}
}

// snapshot 读取会话快照。
func snapshot(t *testing.T, manager *session.Manager, id string) session.Session {
	t.Helper()
	value, err := manager.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("读取会话快照失败：%v", err)
	}
	return value
}

// newRegistry 构造注册了记录型工具并已封闭的注册表。
func newRegistry(t *testing.T, item tool.Tool) *tool.MapRegistry {
	t.Helper()
	registry := tool.NewMapRegistry()
	if err := registry.Register(item); err != nil {
		t.Fatalf("注册工具失败：%v", err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatalf("封闭注册表失败：%v", err)
	}
	return registry
}

// newTestRunner 构造装配层 Runner。
func newTestRunner(t *testing.T, deps Deps, cfg Config) *Runner {
	t.Helper()
	if cfg.Model == "" {
		cfg.Model = "test-model"
	}
	runner, err := NewRunner(deps, cfg)
	if err != nil {
		t.Fatalf("构造 Runner 失败：%v", err)
	}
	return runner
}

// TestRunUsesExactlyOneRealRunTurn 核对工具链路、同 Turn 提交与「恰好一次真实 RunTurn」。
func TestRunUsesExactlyOneRealRunTurn(t *testing.T) {
	calculator := &recordingTool{name: "calculator"}
	provider := &scriptedProvider{script: []llm.Response{
		callsResponse("resp-1", toolCall("call-1", "calculator", `{"op":"add","a":1,"b":2}`)),
		textResponse("resp-2", "3"),
	}}
	manager := newManager()
	seedSession(t, manager, "s-1")
	sessions := &countingSessions{manager: manager}

	runner := newTestRunner(t, Deps{Provider: provider, Sessions: sessions, Registry: newRegistry(t, calculator)}, Config{})

	outcome, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "1+2 等于多少"})
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if outcome.Status != core.RunStatusCompleted || outcome.Err != nil {
		t.Fatalf("终态 = (%s,%v)，期望 COMPLETED 无错误", outcome.Status, outcome.Err)
	}
	if outcome.Reply != "3" {
		t.Fatalf("reply = %q，期望本轮最终助手文本 3", outcome.Reply)
	}
	if outcome.LLMCalls != 2 || outcome.ToolCalls != 1 {
		t.Fatalf("调用计数 = (%d,%d)，期望 (2,1)", outcome.LLMCalls, outcome.ToolCalls)
	}
	if sessions.count() != 1 {
		t.Fatalf("真实 RunTurn 次数 = %d，期望恰好 1 次", sessions.count())
	}

	executed := calculator.executed()
	if len(executed) != 1 || executed[0].ID != "call-1" {
		t.Fatalf("工具执行记录 = %v，期望 1 次 call-1", executed)
	}
	if string(executed[0].Arguments) != `{"op":"add","a":1,"b":2}` {
		t.Fatalf("工具参数被改写：%s", executed[0].Arguments)
	}

	committed := snapshot(t, manager, "s-1")
	roles := make([]core.Role, 0, len(committed.Messages))
	for _, message := range committed.Messages {
		roles = append(roles, message.Role)
	}
	wantRoles := []core.Role{testRoleUser, testRoleAssistant, testRoleTool, testRoleAssistant}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Fatalf("提交后角色序列 = %v，期望 %v（user, assistant(tool_calls), tool, assistant）", roles, wantRoles)
	}
	if err := core.ValidateMessages(committed.Messages); err != nil {
		t.Fatalf("提交后历史不合法：%v", err)
	}
}

// TestRunSecondTurnSeesPreviousHistory 核对多轮历史由 Session 提供。
func TestRunSecondTurnSeesPreviousHistory(t *testing.T) {
	provider := &scriptedProvider{script: []llm.Response{
		textResponse("resp-1", "第一轮回答"),
		textResponse("resp-2", "第二轮回答"),
	}}
	manager := newManager()
	seedSession(t, manager, "s-1")
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: manager, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	if _, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "第一轮问题"}); err != nil {
		t.Fatalf("第一轮失败：%v", err)
	}
	if _, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-2", Message: "第二轮问题"}); err != nil {
		t.Fatalf("第二轮失败：%v", err)
	}

	second := provider.requestAt(t, 2)
	foundUser, foundAssistant, foundSecondUser := false, false, false
	for _, message := range second.Messages {
		switch message.Role {
		case testRoleUser:
			switch textOf(message) {
			case "第一轮问题":
				foundUser = true
			case "第二轮问题":
				foundSecondUser = true
			}
		case testRoleAssistant:
			if textOf(message) == "第一轮回答" {
				foundAssistant = true
			}
		}
	}
	if !foundUser || !foundAssistant {
		t.Fatalf("第二轮模型请求缺少上一轮历史：user=%v assistant=%v", foundUser, foundAssistant)
	}
	if !foundSecondUser {
		t.Fatal("第二轮模型请求缺少本轮用户消息")
	}

	messages := snapshot(t, manager, "s-1").Messages
	if len(messages) != 4 {
		t.Fatalf("两轮后消息数 = %d，期望 4", len(messages))
	}
	for index, message := range messages {
		if index%2 == 0 && message.Role != testRoleUser {
			t.Fatalf("消息[%d] 角色 = %q，期望 user", index, message.Role)
		}
		if index%2 == 1 && message.Role != testRoleAssistant {
			t.Fatalf("消息[%d] 角色 = %q，期望 assistant", index, message.Role)
		}
	}
}

// TestRunUnknownSessionDoesNotInvokeRun 核对未知会话不执行 Run、不产生写入。
func TestRunUnknownSessionDoesNotInvokeRun(t *testing.T) {
	provider := &scriptedProvider{script: []llm.Response{textResponse("resp-1", "不应出现")}}
	manager := newManager()
	sessions := &countingSessions{manager: manager}
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: sessions, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	_, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "missing", RunID: "run-1", Message: "hi"})
	if !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("错误 = %v，期望会话不存在哨兵", err)
	}
	if provider.callCount() != 0 {
		t.Fatalf("未知会话不得调用 Provider，实际 %d 次", provider.callCount())
	}
	// 外层仍会索取一次执行权，但会话层在回调执行前返回哨兵：Run 不成立、无写入。
	if sessions.count() != 1 {
		t.Fatalf("未知会话的 RunTurn 尝试次数 = %d，期望 1（会话层拒绝回调执行）", sessions.count())
	}
	if _, getErr := manager.Get(context.Background(), "missing"); !errors.Is(getErr, session.ErrSessionNotFound) {
		t.Fatalf("未知会话不应被创建：%v", getErr)
	}
}

// TestRunProviderFailureLeavesNoPartialHistory 核对失败整轮丢弃（INV-H04）。
func TestRunProviderFailureLeavesNoPartialHistory(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userMessageFor("已提交用户"), assistantMessageFor("已提交回答"))
	before := snapshot(t, manager, "s-1")

	provider := &scriptedProvider{onCall: func(context.Context, int) (llm.Response, error) {
		return llm.Response{}, errors.New("上游不可用")
	}}
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: manager, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	outcome, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "这一轮应被丢弃"})
	if err != nil {
		t.Fatalf("Run 已形成结论时不应返回 error：%v", err)
	}
	if outcome.Status != core.RunStatusFailed {
		t.Fatalf("终态 = %q，期望 FAILED", outcome.Status)
	}
	if core.KindOf(outcome.Err) != core.KindProviderFailure {
		t.Fatalf("错误种类 = %q，期望 provider_failure", core.KindOf(outcome.Err))
	}

	after := snapshot(t, manager, "s-1")
	if !reflect.DeepEqual(before.Messages, after.Messages) {
		t.Fatalf("失败路径写入了会话：before=%d after=%d", len(before.Messages), len(after.Messages))
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("失败路径后历史不合法：%v", err)
	}
}

// TestRunClientCancelLeavesNoPartialHistory 核对取消真实穿透且无部分提交（AC-007）。
func TestRunClientCancelLeavesNoPartialHistory(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userMessageFor("已提交用户"), assistantMessageFor("已提交回答"))
	before := snapshot(t, manager, "s-1")

	started := make(chan struct{})
	observed := make(chan error, 1)
	provider := &scriptedProvider{onCall: func(ctx context.Context, _ int) (llm.Response, error) {
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return llm.Response{}, ctx.Err()
	}}
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: manager, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	outcome, err := runner.Run(ctx, httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "取消这一轮"})
	if err != nil {
		t.Fatalf("取消路径不应返回未形成结论的错误：%v", err)
	}
	if outcome.Status != core.RunStatusCancelled {
		t.Fatalf("终态 = %q，期望 CANCELLED", outcome.Status)
	}
	if kind := core.KindOf(outcome.Err); kind != core.KindCancelled {
		t.Fatalf("错误种类 = %q，期望 cancelled", kind)
	}

	select {
	case ctxErr := <-observed:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Fatalf("Provider 观察到的 ctx 错误 = %v，期望 Canceled", ctxErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Provider 未观察到取消")
	}

	after := snapshot(t, manager, "s-1")
	if !reflect.DeepEqual(before.Messages, after.Messages) {
		t.Fatalf("取消路径写入了会话：before=%d after=%d", len(before.Messages), len(after.Messages))
	}
}

// TestRunDeadlineIsDistinguishableFromCancel 核对超时终态为 CANCELLED + deadline_exceeded，且与取消可区分。
func TestRunDeadlineIsDistinguishableFromCancel(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")

	provider := &scriptedProvider{onCall: func(ctx context.Context, _ int) (llm.Response, error) {
		<-ctx.Done()
		return llm.Response{}, ctx.Err()
	}}
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: manager, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	outcome, err := runner.Run(ctx, httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "超时这一轮"})
	if err != nil {
		t.Fatalf("超时路径不应返回未形成结论的错误：%v", err)
	}
	if outcome.Status != core.RunStatusCancelled {
		t.Fatalf("终态 = %q，期望 CANCELLED", outcome.Status)
	}
	if kind := core.KindOf(outcome.Err); kind != core.KindDeadlineExceeded {
		t.Fatalf("错误种类 = %q，期望 deadline_exceeded（不得为 cancelled 或 internal）", kind)
	}
	if len(snapshot(t, manager, "s-1").Messages) != 0 {
		t.Fatal("超时路径产生了会话写入")
	}
}

// TestRunCompletedButCommitRejectedIsNotSuccess 核对 Run 完成但提交失败时不返回成功结论。
func TestRunCompletedButCommitRejectedIsNotSuccess(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	provider := &scriptedProvider{script: []llm.Response{textResponse("resp-1", "完成")}}
	runner := newTestRunner(t, Deps{
		Provider: provider,
		Sessions: commitRejectingSessions{manager: manager},
		Registry: newRegistry(t, &recordingTool{name: "calculator"}),
	}, Config{})

	outcome, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "hi"})
	if err == nil {
		t.Fatalf("提交失败必须返回错误，实际结论：%+v", outcome)
	}
	if outcome.Status == core.RunStatusCompleted && outcome.Err == nil {
		t.Fatal("提交失败不得返回 COMPLETED 结论")
	}
	if len(snapshot(t, manager, "s-1").Messages) != 0 {
		t.Fatal("提交被拒绝后不应有会话写入")
	}
}

// TestRunSameSessionConcurrentChatsSerialize 核对同会话并发不重入、不死锁（-race）。
func TestRunSameSessionConcurrentChatsSerialize(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	sessions := &countingSessions{manager: manager}
	provider := &scriptedProvider{onCall: func(_ context.Context, call int) (llm.Response, error) {
		// 轻微延迟，放大并发窗口；同会话执行权必须串行。
		time.Sleep(5 * time.Millisecond)
		return textResponse("resp", "回答"), nil
	}}
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: sessions, Registry: newRegistry(t, &recordingTool{name: "calculator"})}, Config{})

	var waitGroup sync.WaitGroup
	results := make([]error, 2)
	outcomes := make([]httpapi.RunOutcome, 2)
	for index := 0; index < 2; index++ {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			outcomes[index], results[index] = runner.Run(context.Background(), httpapi.RunRequest{
				SessionID: "s-1",
				RunID:     "run-" + string(rune('a'+index)),
				Message:   "并发消息",
			})
		}(index)
	}

	done := make(chan struct{})
	go func() {
		waitGroup.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("同会话并发 Run 疑似死锁")
	}

	for index := range results {
		if results[index] != nil {
			t.Fatalf("并发 Run[%d] 返回错误：%v", index, results[index])
		}
		if outcomes[index].Status != core.RunStatusCompleted {
			t.Fatalf("并发 Run[%d] 终态 = %q，期望 COMPLETED", index, outcomes[index].Status)
		}
	}
	if sessions.count() != 2 {
		t.Fatalf("两次 chat 的真实 RunTurn 次数 = %d，期望 2", sessions.count())
	}

	messages := snapshot(t, manager, "s-1").Messages
	if len(messages) != 4 {
		t.Fatalf("两轮并发提交后消息数 = %d，期望 4（无丢失、无重复）", len(messages))
	}
	if err := core.ValidateMessages(messages); err != nil {
		t.Fatalf("并发提交后历史不合法：%v", err)
	}
}

// TestRunNeverCallsSummarizer 核对 v1 禁用压缩：占位摘要器在任何路径都零调用（INV-H12）。
func TestRunNeverCallsSummarizer(t *testing.T) {
	summarizer := &countingSummarizer{}
	provider := &scriptedProvider{script: []llm.Response{
		textResponse("resp-1", "长历史回答"),
		textResponse("resp-2", "第二轮回答"),
	}}
	manager := newManager()
	// 构造超过任何合理预算的长历史与长消息，确认 TokenBudget=0 时仍不触发压缩。
	longHistory := make([]core.Message, 0, 40)
	for index := 0; index < 20; index++ {
		longHistory = append(longHistory, userMessageFor(strings.Repeat("历史用户", 50)))
		longHistory = append(longHistory, assistantMessageFor(strings.Repeat("历史回答", 50)))
	}
	seedSession(t, manager, "s-1", longHistory...)

	runner := newTestRunner(t, Deps{
		Provider:   provider,
		Sessions:   manager,
		Registry:   newRegistry(t, &recordingTool{name: "calculator"}),
		Summarizer: summarizer,
	}, Config{KeepRecent: 0})

	outcome, err := runner.Run(context.Background(), httpapi.RunRequest{
		SessionID: "s-1", RunID: "run-1", Message: strings.Repeat("长消息", 500),
	})
	if err != nil {
		t.Fatalf("Run 失败：%v", err)
	}
	if outcome.Status != core.RunStatusCompleted {
		t.Fatalf("终态 = %q，期望 COMPLETED", outcome.Status)
	}
	if summarizer.callCount() != 0 {
		t.Fatalf("v1 不得调用 Summarizer，实际调用 %d 次", summarizer.callCount())
	}
}

// TestNewRunnerFailClosed 核对装配参数非法时 fail-closed。
func TestNewRunnerFailClosed(t *testing.T) {
	validDeps := func() Deps {
		return Deps{
			Provider: &scriptedProvider{},
			Sessions: newManager(),
			Registry: newRegistry(t, &recordingTool{name: "calculator"}),
		}
	}

	cases := []struct {
		name string
		deps Deps
		cfg  Config
	}{
		{name: "TokenBudget 非 0", deps: validDeps(), cfg: Config{Model: "m", TokenBudget: 1}},
		{name: "Model 为空", deps: validDeps(), cfg: Config{}},
		{name: "MaxLLMCalls 为负", deps: validDeps(), cfg: Config{Model: "m", MaxLLMCalls: -1}},
		{name: "MaxToolCalls 为负", deps: validDeps(), cfg: Config{Model: "m", MaxToolCalls: -1}},
		{name: "KeepRecent 为负", deps: validDeps(), cfg: Config{Model: "m", KeepRecent: -1}},
		{name: "缺少 Provider", deps: Deps{Sessions: newManager(), Registry: tool.NewMapRegistry()}, cfg: Config{Model: "m"}},
		{name: "缺少 Sessions", deps: Deps{Provider: &scriptedProvider{}, Registry: tool.NewMapRegistry()}, cfg: Config{Model: "m"}},
		{name: "缺少 Registry", deps: Deps{Provider: &scriptedProvider{}, Sessions: newManager()}, cfg: Config{Model: "m"}},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if _, err := NewRunner(item.deps, item.cfg); err == nil {
				t.Fatal("期望构造失败，实际成功")
			}
		})
	}

	if _, err := NewRunner(validDeps(), Config{Model: "m"}); err != nil {
		t.Fatalf("合法配置构造失败：%v", err)
	}
}

// TestFailClosedSummarizerAlwaysFails 核对占位摘要器不是生产级能力：被调用即返回 internal。
func TestFailClosedSummarizerAlwaysFails(t *testing.T) {
	summary, err := failClosedSummarizer{}.Summarize(context.Background(), contextmgr.SummarizeRequest{})
	if err == nil {
		t.Fatal("占位摘要器被调用时必须返回错误")
	}
	if summary != "" {
		t.Fatalf("占位摘要器不得返回摘要，实际 %q", summary)
	}
	if core.KindOf(err) != core.KindInternal {
		t.Fatalf("错误种类 = %q，期望 internal", core.KindOf(err))
	}
}

// TestReplyText 核对 reply 提取只取最后一条 assistant 消息的文本。
func TestReplyText(t *testing.T) {
	messages := []core.Message{
		userMessageFor("问题"),
		{Role: testRoleAssistant, ToolCalls: []core.ToolCall{toolCall("call-1", "calculator", `{}`)}},
		{Role: testRoleTool, ToolResult: &core.ToolResult{CallID: "call-1", Name: "calculator", Content: "ok"}},
		assistantMessageFor("最终"),
	}
	if got := replyText(messages); got != "最终" {
		t.Fatalf("replyText = %q，期望 最终", got)
	}
	if got := replyText([]core.Message{userMessageFor("只有用户")}); got != "" {
		t.Fatalf("没有 assistant 消息时应返回空串，实际 %q", got)
	}
}

// textOf 拼接消息文本片段。
func textOf(message core.Message) string {
	var builder strings.Builder
	for _, part := range message.Content {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
