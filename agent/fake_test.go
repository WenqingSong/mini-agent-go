package agent

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// 消息与调用的构造辅助：测试只用字面量角色与内容类型，避免依赖未导出的契约枚举。
const (
	testTextType      = core.ContentType("text")
	testRoleUser      = core.Role("user")
	testRoleAssistant = core.Role("assistant")
)

// userMsg 构造一条 user 文本消息。
func userMsg(id, text string) core.Message {
	return core.Message{
		ID:      id,
		Role:    testRoleUser,
		Content: []core.ContentPart{{Type: testTextType, Text: text}},
	}
}

// assistantMsg 构造一条不含 ToolCall 的 assistant 文本消息。
func assistantMsg(id, text string) core.Message {
	return core.Message{
		ID:      id,
		Role:    testRoleAssistant,
		Content: []core.ContentPart{{Type: testTextType, Text: text}},
	}
}

// call 构造一次工具调用，参数保持原始 JSON。
func call(id, name, args string) core.ToolCall {
	var arguments json.RawMessage
	if args != "" {
		arguments = json.RawMessage(args)
	}
	return core.ToolCall{ID: id, Name: name, Arguments: arguments}
}

// textResponse 构造「纯文本收尾」的模型响应。
func textResponse(id, text string) llm.Response {
	return llm.Response{
		Message:      assistantMsg(id, text),
		FinishReason: llm.FinishReason("stop"),
		Usage:        llm.Usage{InputTokens: 11, OutputTokens: 7},
	}
}

// callsResponse 构造「请求工具」的模型响应。
func callsResponse(id string, calls ...core.ToolCall) llm.Response {
	return llm.Response{
		Message: core.Message{
			ID:        id,
			Role:      testRoleAssistant,
			ToolCalls: calls,
		},
		FinishReason: llm.FinishReason("tool_calls"),
		Usage:        llm.Usage{InputTokens: 5, OutputTokens: 3},
	}
}

// scriptedProvider 是脚本化 Provider：按调用序或按 RunID 返回预设响应，并记录每次请求。
//
// 不发起任何网络或真实模型调用；阻塞、错误注入与按 Run 分脚本都由测试显式配置。
type scriptedProvider struct {
	mu sync.Mutex
	// script 是全局调用序脚本（第 n 次调用取 script[n-1]）。
	script []llm.Response
	// byRun 按 RunID 分脚本，用于同会话或跨会话并发用例；配置后优先于 script。
	byRun map[string][]llm.Response
	// errAt 按全局调用序注入错误（键为第 n 次调用）。
	errAt map[int]error
	// onCall 覆盖默认返回，用于阻塞、取消等场景。
	onCall func(call int, req llm.Request) (llm.Response, error)

	calls    int
	counters map[string]int
	requests []llm.Request
}

// newScriptedProvider 返回按全局调用序返回响应的 Provider。
func newScriptedProvider(script ...llm.Response) *scriptedProvider {
	return &scriptedProvider{script: script, counters: make(map[string]int)}
}

// withPerRunScript 让每个 Run 使用自己的脚本。
func (p *scriptedProvider) withPerRunScript(byRun map[string][]llm.Response) *scriptedProvider {
	p.byRun = byRun
	return p
}

// withErrorAt 在第 n 次调用（1 起）注入错误。
func (p *scriptedProvider) withErrorAt(n int, err error) *scriptedProvider {
	if p.errAt == nil {
		p.errAt = make(map[int]error)
	}
	p.errAt[n] = err
	return p
}

// withOnCall 覆盖单次调用的返回行为。
func (p *scriptedProvider) withOnCall(fn func(call int, req llm.Request) (llm.Response, error)) *scriptedProvider {
	p.onCall = fn
	return p
}

func (p *scriptedProvider) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.counters[req.RunID]++
	runCall := p.counters[req.RunID]
	p.requests = append(p.requests, req)
	onCall := p.onCall
	injected := p.errAt[call]
	var resp llm.Response
	found := false
	if p.byRun != nil {
		if script, ok := p.byRun[req.RunID]; ok && runCall <= len(script) {
			resp, found = script[runCall-1], true
		}
	} else if call <= len(p.script) {
		resp, found = p.script[call-1], true
	}
	p.mu.Unlock()

	if onCall != nil {
		return onCall(call, req)
	}
	if injected != nil {
		return llm.Response{}, injected
	}
	if !found {
		return llm.Response{}, errors.New("scripted provider: 脚本已耗尽")
	}
	return resp, nil
}

// callCount 返回 Provider 被调用的次数。
func (p *scriptedProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// requestAt 返回第 n 次（1 起）模型调用请求。
func (p *scriptedProvider) requestAt(t *testing.T, n int) llm.Request {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if n < 1 || n > len(p.requests) {
		t.Fatalf("请求序号 %d 越界：共 %d 次调用", n, len(p.requests))
	}
	return p.requests[n-1]
}

// recordingTool 是记录型工具：记录每次真实执行的调用参数，行为可注入。
type recordingTool struct {
	name       string
	parameters json.RawMessage
	fn         func(ctx context.Context, req tool.Request) (core.ToolResult, error)

	mu    sync.Mutex
	calls []core.ToolCall
}

// newRecordingTool 返回零参数 Schema 的测试工具。
func newRecordingTool(name string) *recordingTool {
	return &recordingTool{
		name:       name,
		parameters: json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

// withParameters 设置 Parameters（用于参数校验失败的用例）。
func (r *recordingTool) withParameters(raw string) *recordingTool {
	r.parameters = json.RawMessage(raw)
	return r
}

// withFunc 注入执行行为（panic、错误、阻塞、取消等）。
func (r *recordingTool) withFunc(fn func(ctx context.Context, req tool.Request) (core.ToolResult, error)) *recordingTool {
	r.fn = fn
	return r
}

func (r *recordingTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        r.name,
		Description: r.name + " 测试工具",
		Parameters:  append([]byte(nil), r.parameters...),
	}
}

func (r *recordingTool) Execute(ctx context.Context, req tool.Request) (core.ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req.Call)
	fn := r.fn
	r.mu.Unlock()

	if fn != nil {
		return fn(ctx, req)
	}
	return core.ToolResult{CallID: req.Call.ID, Name: req.Call.Name, Content: "ok:" + req.Call.ID}, nil
}

// executed 返回真实执行过的调用序列（证明执行次数与顺序）。
func (r *recordingTool) executed() []core.ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.ToolCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// execCount 返回真实执行次数。
func (r *recordingTool) execCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// staticEstimator 对每条消息返回固定 token 数，便于用最少算术固定预算边界。
type staticEstimator struct {
	perMessage int
}

func (e staticEstimator) EstimateMessage(core.Message) int { return e.perMessage }

// fakeSummarizer 是脚本化摘要器：记录调用次数，不发起任何真实模型调用。
type fakeSummarizer struct {
	summary string
	err     error

	mu       sync.Mutex
	calls    int
	requests []contextmgr.SummarizeRequest
}

func (f *fakeSummarizer) Summarize(_ context.Context, req contextmgr.SummarizeRequest) (string, error) {
	f.mu.Lock()
	f.calls++
	f.requests = append(f.requests, req)
	f.mu.Unlock()
	return f.summary, f.err
}

// callCount 返回摘要器的调用次数。
func (f *fakeSummarizer) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// recordingSink 记录全部运行事件，供事件顺序与定位断言使用。
type recordingSink struct {
	mu     sync.Mutex
	events []runlog.Event
}

func (s *recordingSink) Record(_ context.Context, ev runlog.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
	return nil
}

// failingSink 必定返回错误，用于证明日志旁路（INV-007）。
type failingSink struct{}

func (failingSink) Record(context.Context, runlog.Event) error {
	return errors.New("日志写入失败")
}

// newRegistry 注册工具并返回内存注册表。
func newRegistry(t *testing.T, tools ...tool.Tool) *tool.MapRegistry {
	t.Helper()
	registry := tool.NewMapRegistry()
	for _, item := range tools {
		if err := registry.Register(item); err != nil {
			t.Fatalf("注册工具 %q 失败：%v", item.Spec().Name, err)
		}
	}
	return registry
}

// newSessionManager 返回不启用过期的内存会话管理器。
func newSessionManager(t *testing.T) *session.Manager {
	t.Helper()
	return session.NewManager(session.ManagerConfig{})
}

// createSession 创建会话并写入基线历史与摘要。
func createSession(t *testing.T, manager *session.Manager, id string, summary string, messages ...core.Message) {
	t.Helper()
	ctx := context.Background()
	if _, err := manager.Create(ctx, id); err != nil {
		t.Fatalf("创建会话 %q 失败：%v", id, err)
	}
	if err := manager.Save(ctx, session.Session{ID: id, Messages: messages, Summary: summary}); err != nil {
		t.Fatalf("写入会话 %q 基线失败：%v", id, err)
	}
}

// sessionSnapshot 返回会话深拷贝快照。
func sessionSnapshot(t *testing.T, manager *session.Manager, id string) session.Session {
	t.Helper()
	snapshot, err := manager.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("读取会话 %q 失败：%v", id, err)
	}
	return snapshot
}

// baseConfig 返回可用的最小装配；调用方按需覆盖字段后再 New。
func baseConfig(t *testing.T, provider llm.Provider, registry tool.Registry, sessions SessionRunner) Config {
	t.Helper()
	contexts, err := contextmgr.NewManager(contextmgr.HeuristicEstimator{}, &fakeSummarizer{summary: "压缩摘要"})
	if err != nil {
		t.Fatalf("构造上下文管理器失败：%v", err)
	}
	return Config{
		Provider:     provider,
		Sessions:     sessions,
		Registry:     registry,
		Contexts:     contexts,
		SystemPrompt: "系统提示",
		Model:        "fake-model",
		KeepRecent:   4,
	}
}

// mustNewLoop 构造 Loop，构造失败即视为测试环境错误。
func mustNewLoop(t *testing.T, cfg Config) *Loop {
	t.Helper()
	loop, err := New(cfg)
	if err != nil {
		t.Fatalf("New() 返回错误：%v", err)
	}
	return loop
}

// assertResultValid 断言 RunResult 自身一致（INV-002）。
func assertResultValid(t *testing.T, result core.RunResult) {
	t.Helper()
	if err := result.Validate(); err != nil {
		t.Fatalf("RunResult.Validate() 必须为 nil，实际：%v（状态 %s，LLMCalls=%d，ToolCalls=%d）",
			err, result.Status, result.LLMCalls, result.ToolCalls)
	}
}

// assertTerminal 断言终态、错误种类与错误携带的 CallID。
func assertTerminal(t *testing.T, result core.RunResult, status core.RunStatus, kind core.ErrorKind, callID string) {
	t.Helper()
	assertResultValid(t, result)
	if result.Status != status {
		t.Fatalf("终态不符：期望 %s，实际 %s（错误 %v）", status, result.Status, result.Error)
	}
	if status == core.RunStatusCompleted {
		if result.Error != nil {
			t.Fatalf("COMPLETED 不允许携带错误，实际：%v", result.Error)
		}
		// COMPLETED 时 RunResult.Messages 必须是合法的配对序列（Contract D1）。
		if err := core.ValidateMessages(result.Messages); err != nil {
			t.Fatalf("COMPLETED 的 RunResult.Messages 必须通过配对校验：%v", err)
		}
		return
	}
	if result.Error == nil {
		t.Fatalf("%s 必须携带错误", status)
	}
	if got := core.KindOf(result.Error); got != kind {
		t.Fatalf("错误种类不符：期望 %s，实际 %s（%v）", kind, got, result.Error)
	}
	if result.Error.CallID != callID {
		t.Fatalf("错误 CallID 不符：期望 %q，实际 %q（%v）", callID, result.Error.CallID, result.Error)
	}
}

// assertSessionUnchanged 断言会话的消息、摘要与 Todo 与运行前逐字段一致（INV-001）。
func assertSessionUnchanged(t *testing.T, before, after session.Session) {
	t.Helper()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("失败路径不得改动会话：\n运行前：%+v\n运行后：%+v", before, after)
	}
}

// toolResults 提取消息序列中全部 RoleTool 结果。
func toolResults(messages []core.Message) []core.ToolResult {
	out := make([]core.ToolResult, 0, len(messages))
	for _, msg := range messages {
		if msg.ToolResult != nil {
			out = append(out, *msg.ToolResult)
		}
	}
	return out
}

// messageIDs 提取消息 ID，便于断言顺序。
func messageIDs(messages []core.Message) []string {
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.ID)
	}
	return out
}
