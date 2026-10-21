package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/service"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// 契约层枚举字面量：core 未导出 Role / ContentType 常量，测试只能用字面量转换。
const (
	testRoleUser      = core.Role("user")
	testRoleAssistant = core.Role("assistant")
	testTextType      = core.ContentType("text")
)

// fakeRunner 是可注入的 Run 执行器：记录调用与入参，行为可由测试覆盖。
type fakeRunner struct {
	mu       sync.Mutex
	calls    int
	requests []httpapi.RunRequest
	outcome  httpapi.RunOutcome
	err      error
	onRun    func(ctx context.Context, req httpapi.RunRequest) (httpapi.RunOutcome, error)
}

// Run 实现 httpapi.Runner。
func (f *fakeRunner) Run(ctx context.Context, req httpapi.RunRequest) (httpapi.RunOutcome, error) {
	f.mu.Lock()
	f.calls++
	f.requests = append(f.requests, req)
	outcome, err, onRun := f.outcome, f.err, f.onRun
	f.mu.Unlock()

	if onRun != nil {
		return onRun(ctx, req)
	}
	return outcome, err
}

// callCount 返回 Runner 被调用次数。
func (f *fakeRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// requestAt 返回第 n 次（1 起）Run 入参。
func (f *fakeRunner) requestAt(t *testing.T, n int) httpapi.RunRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if n < 1 || n > len(f.requests) {
		t.Fatalf("Run 调用序号 %d 越界：共 %d 次", n, len(f.requests))
	}
	return f.requests[n-1]
}

// testConfig 返回回环随机端口的测试配置。
func testConfig(token string) httpapi.Config {
	cfg := httpapi.DefaultConfig()
	cfg.ListenAddr = "127.0.0.1:0"
	cfg.AuthToken = token
	return cfg
}

// newTestServer 以 httptest 启动一个进程内服务。
func newTestServer(t *testing.T, cfg httpapi.Config, deps httpapi.Deps) *httptest.Server {
	t.Helper()
	server, err := httpapi.New(cfg, deps)
	if err != nil {
		t.Fatalf("构造服务失败：%v", err)
	}
	testServer := httptest.NewServer(server)
	t.Cleanup(testServer.Close)
	return testServer
}

// httpResult 是一次 HTTP 调用的完整结果。
type httpResult struct {
	status int
	body   []byte
	header http.Header
}

// doRequest 发送一次 JSON 请求；token / contentType 为空时对应头不设置。
func doRequest(t *testing.T, client *http.Client, method, url, contentType, body, token string) httpResult {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, url, reader)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	if contentType != "" {
		request.Header.Set("Content-Type", contentType)
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("发送请求失败：%v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取响应失败：%v", err)
	}
	return httpResult{status: response.StatusCode, body: payload, header: response.Header}
}

// errorPayload 是错误封套的解析形态。
type errorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// decodeError 解析错误封套并断言状态码。
func decodeError(t *testing.T, result httpResult, wantStatus int, wantCode httpapi.ErrorCode) errorPayload {
	t.Helper()
	if result.status != wantStatus {
		t.Fatalf("状态码 = %d，期望 %d（body=%s）", result.status, wantStatus, result.body)
	}
	var payload errorPayload
	if err := json.Unmarshal(result.body, &payload); err != nil {
		t.Fatalf("错误响应不是合法 JSON：%v（body=%s）", err, result.body)
	}
	if payload.Error.Code != string(wantCode) {
		t.Fatalf("错误码 = %q，期望 %q（body=%s）", payload.Error.Code, wantCode, result.body)
	}
	if payload.Error.Message == "" {
		t.Fatalf("错误 message 为空：%s", result.body)
	}
	return payload
}

// scriptedProvider 是脚本化 LLM Provider：按调用序返回预设响应并记录请求。
type scriptedProvider struct {
	mu       sync.Mutex
	script   []llm.Response
	calls    int
	requests []llm.Request
}

// Complete 实现 llm.Provider。
func (p *scriptedProvider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.mu.Lock()
	p.calls++
	call := p.calls
	p.requests = append(p.requests, req)
	var response llm.Response
	found := call <= len(p.script)
	if found {
		response = p.script[call-1]
	}
	p.mu.Unlock()

	if !found {
		return llm.Response{}, errors.New("scripted provider: 脚本已耗尽")
	}
	return response, nil
}

// callCount 返回 Provider 被调用次数。
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

// recordingTool 是记录型工具：证明工具确实被执行并保留参数。
type recordingTool struct {
	name string

	mu    sync.Mutex
	calls []core.ToolCall
}

// newRecordingTool 返回零参数 Schema 的记录型工具。
func newRecordingTool(name string) *recordingTool {
	return &recordingTool{name: name}
}

// Spec 实现 tool.Tool。
func (r *recordingTool) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        r.name,
		Description: r.name + " 测试工具",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
	}
}

// Execute 实现 tool.Tool：记录调用并返回固定成功结果。
func (r *recordingTool) Execute(_ context.Context, req tool.Request) (core.ToolResult, error) {
	r.mu.Lock()
	r.calls = append(r.calls, req.Call)
	r.mu.Unlock()
	return core.ToolResult{
		CallID:  req.Call.ID,
		Name:    req.Call.Name,
		Content: "ok:" + req.Call.ID,
	}, nil
}

// executed 返回真实执行过的调用序列。
func (r *recordingTool) executed() []core.ToolCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]core.ToolCall, len(r.calls))
	copy(out, r.calls)
	return out
}

// textResponse 构造纯文本收尾的模型响应。
func textResponse(id, text string) llm.Response {
	return llm.Response{
		Message: core.Message{
			ID:      id,
			Role:    testRoleAssistant,
			Content: []core.ContentPart{{Type: testTextType, Text: text}},
		},
		FinishReason: llm.FinishReason("stop"),
		Usage:        llm.Usage{InputTokens: 3, OutputTokens: 2},
	}
}

// callsResponse 构造请求工具调用的模型响应。
func callsResponse(id string, calls ...core.ToolCall) llm.Response {
	return llm.Response{
		Message: core.Message{
			ID:        id,
			Role:      testRoleAssistant,
			ToolCalls: calls,
		},
		FinishReason: llm.FinishReason("tool_calls"),
		Usage:        llm.Usage{InputTokens: 3, OutputTokens: 2},
	}
}

// toolCall 构造一次工具调用。
func toolCall(id, name, arguments string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(arguments)}
}

// userTexture 返回测试用 user 文本。
func userTexture(text string) core.Message {
	return core.Message{
		Role:    testRoleUser,
		Content: []core.ContentPart{{Type: testTextType, Text: text}},
	}
}

// assistantTexture 返回测试用 assistant 文本。
func assistantTexture(text string) core.Message {
	return core.Message{
		Role:    testRoleAssistant,
		Content: []core.ContentPart{{Type: testTextType, Text: text}},
	}
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
		t.Fatalf("创建会话 %q 失败：%v", id, err)
	}
	if err := manager.Save(ctx, session.Session{ID: id, Messages: messages}); err != nil {
		t.Fatalf("写入会话 %q 基线失败：%v", id, err)
	}
}

// snapshot 返回会话深拷贝快照。
func snapshot(t *testing.T, manager *session.Manager, id string) session.Session {
	t.Helper()
	snapshot, err := manager.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("读取会话 %q 快照失败：%v", id, err)
	}
	return snapshot
}

// seedSessionState 创建会话并写入完整基线（消息、摘要、Todo），用于原子性比对。
func seedSessionState(t *testing.T, manager *session.Manager, base session.Session) {
	t.Helper()
	ctx := context.Background()
	if _, err := manager.Create(ctx, base.ID); err != nil {
		t.Fatalf("创建会话 %q 失败：%v", base.ID, err)
	}
	if err := manager.Save(ctx, base); err != nil {
		t.Fatalf("写入会话 %q 基线失败：%v", base.ID, err)
	}
}

// assertSessionStateEqual 断言失败路径后会话的消息、摘要与 Todo 与请求前逐字段相等。
func assertSessionStateEqual(t *testing.T, manager *session.Manager, want session.Session) {
	t.Helper()
	got := snapshot(t, manager, want.ID)
	if !reflect.DeepEqual(got.Messages, want.Messages) {
		t.Fatalf("会话消息被修改：before=%+v after=%+v", want.Messages, got.Messages)
	}
	if got.Summary != want.Summary {
		t.Fatalf("会话摘要被修改：before=%q after=%q", want.Summary, got.Summary)
	}
	if !reflect.DeepEqual(got.Todos, want.Todos) {
		t.Fatalf("会话 Todo 被修改：before=%+v after=%+v", want.Todos, got.Todos)
	}
	if err := core.ValidateMessages(got.Messages); err != nil {
		t.Fatalf("失败路径后历史不再合法：%v", err)
	}
}

// failingProvider 是可注入错误的 LLM Provider 替身：用于构造真实 Run 下的 Provider 失败。
type failingProvider struct {
	mu         sync.Mutex
	calls      int
	onComplete func(ctx context.Context) (llm.Response, error)
}

// Complete 实现 llm.Provider：记录调用并执行注入行为。
func (p *failingProvider) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	p.mu.Lock()
	p.calls++
	hook := p.onComplete
	p.mu.Unlock()

	if hook == nil {
		return llm.Response{}, errors.New("failing provider: 未注入行为")
	}
	return hook(ctx)
}

// callCount 返回 Provider 被调用次数。
func (p *failingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// newRealRunner 用真实 session.Manager 与真实 agent.Loop（经 service 装配）构造 Runner。
func newRealRunner(t *testing.T, provider llm.Provider, manager *session.Manager, sink runlog.Sink) *service.Runner {
	t.Helper()
	registry := tool.NewMapRegistry()
	if err := registry.Seal(); err != nil {
		t.Fatalf("封闭注册表失败：%v", err)
	}
	runner, err := service.NewRunner(service.Deps{
		Provider: provider,
		Sessions: manager,
		Registry: registry,
		Sink:     sink,
	}, service.Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("构造 Runner 失败：%v", err)
	}
	return runner
}

// runFinishedRecord 是 run_finished 在 JSON 日志中的观测字段。
type runFinishedRecord struct {
	Event     string `json:"event"`
	Status    string `json:"status"`
	ErrorKind string `json:"error_kind"`
}

// lastRunFinished 从 JSON Sink 输出中取出最后一条 run_finished 事件。
func lastRunFinished(t *testing.T, buffer *bytes.Buffer) runFinishedRecord {
	t.Helper()
	var found *runFinishedRecord
	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record runFinishedRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("运行日志不是合法 JSON：%v（%q）", err, line)
		}
		if record.Event == string(runlog.KindRunFinished) {
			copied := record
			found = &copied
		}
	}
	if found == nil {
		t.Fatalf("运行日志中缺少 run_finished 事件：%s", buffer.String())
	}
	return *found
}
