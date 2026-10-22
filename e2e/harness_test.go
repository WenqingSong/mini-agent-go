// Package e2e_test 是 Runtime 的进程内离线验收套件。
//
// 本文件是唯一装配点：真实 httpapi.Server → service.Runner → agent.Loop →
// llm/openai.Client → loopback scripted OpenAI 兼容服务，配合真实 session.Manager、
// 真实 builtin 工具与 runlog JSON Sink。全部模型交互只发生在 httptest loopback
// 服务上，不读取、不设置任何真实 Provider 凭据。
//
// 观测通道（互不替代，出现分歧即失败）：
//  1. HTTP 响应（状态码 + 错误封套 code）；
//  2. recordingRunner 记录的 RunOutcome（Status / Err.Kind / 计数）；
//  3. runlog run_finished 事件（status / error_kind）；
//  4. scripted 服务收到的原始 wire 请求（messages / tools / tool_call_id）。
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm/openai"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/service"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// 契约层枚举字面量：core 未导出 Role / ContentType 常量，包外只能做字面量转换。
const (
	e2eRoleUser      = core.Role("user")
	e2eRoleAssistant = core.Role("assistant")
	e2eRoleTool      = core.Role("tool")
	e2eTextType      = core.ContentType("text")
)

// blockLimit 是阻塞脚本等待上游取消的上限：只用于避免脚本 goroutine 永久挂住，
// 判定始终依赖上游取消是否真的发生。
const blockLimit = 2 * time.Second

// e2eModel 是装配层透传的模型名，不对应任何真实 Provider。
const e2eModel = "e2e-scripted-model"

// scriptCall 是脚本响应中的一次工具调用。
type scriptCall struct {
	ID        string
	Name      string
	Arguments string
}

// llmStep 是 scripted 服务的一步行为：固定响应、HTTP 错误、阻塞至上游取消，
// 或按收到的 wire 请求动态生成响应（并发用例用它把响应绑定到请求内容）。
type llmStep struct {
	status int
	body   string
	block  bool
	build  func(wireRequest) (int, string)
}

// textStep 构造一次 stop 终稿响应。
func textStep(text string) llmStep {
	return llmStep{body: replyBody("resp-text", text)}
}

// toolCallsStep 构造一次 finish_reason=tool_calls 的响应（可携带多个调用）。
func toolCallsStep(calls ...scriptCall) llmStep {
	return llmStep{body: toolCallsBody("resp-tool-calls", calls...)}
}

// statusStep 构造一次 HTTP 错误响应。
func statusStep(status int, body string) llmStep {
	return llmStep{status: status, body: body}
}

// blockStep 构造一次「不响应、等待上游取消」的脚本步。
func blockStep() llmStep {
	return llmStep{block: true}
}

// dynamicStep 构造按请求内容生成响应的脚本步。
func dynamicStep(build func(wireRequest) (int, string)) llmStep {
	return llmStep{build: build}
}

// wireRequest 是 Chat Completions 请求体的解析形态（只包含本套件断言的字段）。
type wireRequest struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []wireTool    `json:"tools"`
}

// wireMessage 是一条 wire 消息。
type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content"`
	ToolCalls  []wireToolCall `json:"tool_calls"`
	ToolCallID string         `json:"tool_call_id"`
}

// wireToolCall 是 assistant 消息回传的工具调用。
type wireToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// wireTool 是 tools[] 中的一项。
type wireTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// parseWireRequest 解析 scripted 服务收到的请求体。
func parseWireRequest(body []byte) (wireRequest, error) {
	var request wireRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return wireRequest{}, err
	}
	return request, nil
}

// lastUserText 返回请求中最后一条 user 消息的文本。
func (r wireRequest) lastUserText() string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i].Role == string(e2eRoleUser) {
			return r.Messages[i].Content
		}
	}
	return ""
}

// toolMessages 返回全部 tool 消息。
func (r wireRequest) toolMessages() []wireMessage {
	out := make([]wireMessage, 0, len(r.Messages))
	for _, message := range r.Messages {
		if message.Role == string(e2eRoleTool) {
			out = append(out, message)
		}
	}
	return out
}

// toolNames 返回 tools[] 中声明的工具名（保持 wire 顺序）。
func (r wireRequest) toolNames() []string {
	out := make([]string, 0, len(r.Tools))
	for _, item := range r.Tools {
		out = append(out, item.Function.Name)
	}
	return out
}

// llmRequestRecord 是一次模型请求的原始记录。
type llmRequestRecord struct {
	Index               int
	Method              string
	Path                string
	Host                string
	AuthorizationHeader string
	Body                []byte
	Blocked             bool
	BlockedCanceled     bool
}

// scriptedLLM 是进程内 OpenAI 兼容脚本服务：按请求序消费脚本，记录原始请求。
type scriptedLLM struct {
	t *testing.T

	server *httptest.Server

	mu              sync.Mutex
	script          []llmStep
	requests        []llmRequestRecord
	exhausted       int
	blocked         int
	blockedCanceled int
}

// newScriptedLLM 启动脚本服务；脚本耗尽即记录测试失败，避免静默放纵额外请求。
func newScriptedLLM(t *testing.T, script []llmStep) *scriptedLLM {
	t.Helper()
	scripts := &scriptedLLM{t: t, script: append([]llmStep(nil), script...)}
	scripts.server = httptest.NewServer(http.HandlerFunc(scripts.handle))
	t.Cleanup(scripts.server.Close)
	return scripts
}

// handle 消费一步脚本并记录请求。
func (s *scriptedLLM) handle(w http.ResponseWriter, r *http.Request) {
	body, readErr := io.ReadAll(r.Body)

	s.mu.Lock()
	index := len(s.requests)
	record := llmRequestRecord{
		Index:               index + 1,
		Method:              r.Method,
		Path:                r.URL.Path,
		Host:                r.Host,
		AuthorizationHeader: r.Header.Get("Authorization"),
		Body:                body,
	}
	var step llmStep
	found := index < len(s.script)
	if found {
		step = s.script[index]
	}
	s.requests = append(s.requests, record)
	s.mu.Unlock()

	if readErr != nil {
		s.fail("读取第 %d 次模型请求体失败：%v", index+1, readErr)
		writeScriptedJSON(w, http.StatusInternalServerError, `{"error":{"message":"read failure"}}`)
		return
	}
	if r.URL.Path != "/v1/chat/completions" {
		s.fail("第 %d 次模型请求路径 = %q，期望 /v1/chat/completions", index+1, r.URL.Path)
		writeScriptedJSON(w, http.StatusNotFound, `{"error":{"message":"unexpected path"}}`)
		return
	}
	if !found {
		s.mu.Lock()
		s.exhausted++
		s.mu.Unlock()
		s.fail("脚本已耗尽：收到第 %d 次模型请求，脚本只有 %d 步", index+1, len(s.script))
		writeScriptedJSON(w, http.StatusInternalServerError, `{"error":{"message":"script exhausted"}}`)
		return
	}

	if step.block {
		s.mu.Lock()
		s.blocked++
		s.requests[index].Blocked = true
		s.mu.Unlock()

		select {
		case <-r.Context().Done():
			s.mu.Lock()
			s.blockedCanceled++
			s.requests[index].BlockedCanceled = true
			s.mu.Unlock()
		case <-time.After(blockLimit):
			s.fail("阻塞脚本在 %s 内没有观察到上游取消", blockLimit)
		}
		// 不写响应：上游请求已被取消或超时。
		return
	}

	status, payload := step.status, step.body
	if step.build != nil {
		parsed, err := parseWireRequest(body)
		if err != nil {
			s.fail("第 %d 次模型请求体不是合法 JSON：%v（%s）", index+1, err, body)
			writeScriptedJSON(w, http.StatusInternalServerError, `{"error":{"message":"invalid request"}}`)
			return
		}
		status, payload = step.build(parsed)
	}
	if status == 0 {
		status = http.StatusOK
	}
	writeScriptedJSON(w, status, payload)
}

// fail 在 handler goroutine 内记录失败（t.Fatalf 只能在测试 goroutine 调用）。
func (s *scriptedLLM) fail(format string, args ...any) {
	s.t.Errorf(format, args...)
}

// writeScriptedJSON 写出脚本响应。
func writeScriptedJSON(w http.ResponseWriter, status int, payload string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, payload)
}

// requestRecords 返回已记录的请求副本。
func (s *scriptedLLM) requestRecords() []llmRequestRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]llmRequestRecord, len(s.requests))
	copy(out, s.requests)
	return out
}

// requestAt 返回第 n 次（1 起）模型请求的原始记录与解析结果。
func (s *scriptedLLM) requestAt(t *testing.T, n int) (llmRequestRecord, wireRequest) {
	t.Helper()
	records := s.requestRecords()
	if n < 1 || n > len(records) {
		t.Fatalf("模型请求序号 %d 越界：共 %d 次", n, len(records))
	}
	parsed, err := parseWireRequest(records[n-1].Body)
	if err != nil {
		t.Fatalf("第 %d 次模型请求体不是合法 JSON：%v", n, err)
	}
	return records[n-1], parsed
}

// requestCount 返回已收到的模型请求数。
func (s *scriptedLLM) requestCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// stats 返回脚本耗尽次数与阻塞观测计数。
func (s *scriptedLLM) stats() (exhausted, blocked, blockedCanceled int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exhausted, s.blocked, s.blockedCanceled
}

// assertScriptConsumed 断言脚本全部被消费且没有额外请求。
func (s *scriptedLLM) assertScriptConsumed(t *testing.T) {
	t.Helper()
	exhausted, _, _ := s.stats()
	if exhausted != 0 {
		t.Fatalf("脚本被额外请求耗尽 %d 次", exhausted)
	}
	if got := s.requestCount(); got != len(s.script) {
		t.Fatalf("模型请求次数 = %d，脚本步数 = %d", got, len(s.script))
	}
}

// replyBody 构造一次 stop 终稿响应体。
func replyBody(id, text string) string {
	return mustJSON(map[string]any{
		"id": id,
		"choices": []map[string]any{{
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
}

// toolCallsBody 构造一次 tool_calls 响应体。
func toolCallsBody(id string, calls ...scriptCall) string {
	encoded := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		encoded = append(encoded, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": call.Arguments,
			},
		})
	}
	return mustJSON(map[string]any{
		"id": id,
		"choices": []map[string]any{{
			"message": map[string]any{
				"role":       "assistant",
				"content":    "",
				"tool_calls": encoded,
			},
			"finish_reason": "tool_calls",
		}},
		"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 7},
	})
}

// mustJSON 序列化测试脚本数据；失败即测试代码缺陷。
func mustJSON(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		panic(fmt.Sprintf("e2e 脚本序列化失败：%v", err))
	}
	return string(encoded)
}

// syncBuffer 是并发安全的日志捕获缓冲。
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

// Write 实现 io.Writer。
func (b *syncBuffer) Write(payload []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(payload)
}

// String 返回当前捕获内容。
func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// recordedRun 是一次 Run 的只读观测记录。
type recordedRun struct {
	Request httpapi.RunRequest
	Outcome httpapi.RunOutcome
	Err     error
}

// recordingRunner 是只读透传的 httpapi.Runner 装饰器：不改 ctx、不改请求、不改返回值，
// 只记录 Run 结论，供失败路径断言精确终态（这是 HTTP 状态码之外的独立观测通道）。
type recordingRunner struct {
	inner httpapi.Runner

	mu   sync.Mutex
	runs []recordedRun
}

// Run 实现 httpapi.Runner。
func (r *recordingRunner) Run(ctx context.Context, req httpapi.RunRequest) (httpapi.RunOutcome, error) {
	outcome, err := r.inner.Run(ctx, req)
	r.mu.Lock()
	r.runs = append(r.runs, recordedRun{Request: req, Outcome: outcome, Err: err})
	r.mu.Unlock()
	return outcome, err
}

// runsCopy 返回已记录的 Run 结论副本。
func (r *recordingRunner) runsCopy() []recordedRun {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]recordedRun, len(r.runs))
	copy(out, r.runs)
	return out
}

// runCount 返回 Run 被调用次数。
func (r *recordingRunner) runCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.runs)
}

// harnessOptions 是验收装配的参数；零值字段使用固定默认值。
type harnessOptions struct {
	script          []llmStep
	maxLLMCalls     int
	maxToolCalls    int
	requestTimeout  time.Duration
	providerTimeout time.Duration
	toolTimeout     time.Duration
	// tokenBudget / keepRecent 透传 service.Config：> 0 时启用上下文压缩。
	tokenBudget int
	keepRecent  int
}

// harness 是一次完整验收装配。
type harness struct {
	t *testing.T

	sessions *session.Manager
	service  *service.Runner
	recorder *recordingRunner
	provider *openai.Client
	llm      *scriptedLLM
	logs     *syncBuffer
	api      *httpapi.Server
	server   *httptest.Server
	client   *http.Client
}

// newHarness 组装真实全链路：httptest(httpapi.New) → recordingRunner → service.Runner →
// agent.Loop → 真实 openai.Client → loopback 脚本服务；工具为真实 builtin 实现。
func newHarness(t *testing.T, opts harnessOptions) *harness {
	t.Helper()

	scripts := newScriptedLLM(t, opts.script)
	provider, err := openai.NewClient(openai.Config{
		BaseURL: scripts.server.URL + "/v1",
		Timeout: opts.providerTimeout,
	})
	if err != nil {
		t.Fatalf("构造真实 openai.Client 失败：%v", err)
	}

	sessions := session.NewManager(session.ManagerConfig{})
	logs := &syncBuffer{}
	runner, err := service.NewRunner(service.Deps{
		Provider: provider,
		Sessions: sessions,
		// 与正式入口同一个构造器：工具清单与 Todo 的 Turn 绑定方式都来自生产代码。
		Tools: service.NewToolset,
		Sink:  runlog.NewJSONSink(logs),
	}, service.Config{
		Model:        e2eModel,
		MaxLLMCalls:  opts.maxLLMCalls,
		MaxToolCalls: opts.maxToolCalls,
		ToolTimeout:  opts.toolTimeout,
		TokenBudget:  opts.tokenBudget,
		KeepRecent:   opts.keepRecent,
	})
	if err != nil {
		t.Fatalf("构造 service.Runner 失败：%v", err)
	}

	recorder := &recordingRunner{inner: runner}
	requestTimeout := opts.requestTimeout
	if requestTimeout <= 0 {
		requestTimeout = 10 * time.Second
	}
	apiServer, err := httpapi.New(httpapi.Config{
		ListenAddr:     "127.0.0.1:0",
		RequestTimeout: requestTimeout,
	}, httpapi.Deps{Sessions: sessions, Runner: recorder})
	if err != nil {
		t.Fatalf("构造 httpapi.Server 失败：%v", err)
	}
	server := httptest.NewServer(apiServer)
	t.Cleanup(server.Close)

	return &harness{
		t:        t,
		sessions: sessions,
		service:  runner,
		recorder: recorder,
		provider: provider,
		llm:      scripts,
		logs:     logs,
		api:      apiServer,
		server:   server,
		client:   &http.Client{Timeout: 15 * time.Second},
	}
}

// httpResult 是一次 HTTP 调用的完整结果。
type httpResult struct {
	Status int
	Body   []byte
}

// chatResponse 是成功响应体的解析形态。
type chatResponse struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Reply     string `json:"reply"`
	LLMCalls  int    `json:"llm_calls"`
	ToolCalls int    `json:"tool_calls"`
}

// errorEnvelope 是错误响应体的解析形态。
type errorEnvelope struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// createSession 通过真实 HTTP 端点创建会话。
func (h *harness) createSession() string {
	h.t.Helper()
	result := h.do(http.MethodPost, "/v1/sessions", "{}")
	if result.Status != http.StatusCreated {
		h.t.Fatalf("创建会话失败：%d %s", result.Status, result.Body)
	}
	var payload struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		h.t.Fatalf("创建会话响应不是合法 JSON：%v（%s）", err, result.Body)
	}
	if payload.SessionID == "" {
		h.t.Fatalf("创建会话未返回 session_id：%s", result.Body)
	}
	return payload.SessionID
}

// do 发送一次 JSON 请求并读取完整响应。
func (h *harness) do(method, path, body string) httpResult {
	h.t.Helper()
	request, err := http.NewRequestWithContext(context.Background(), method, h.server.URL+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatalf("构造 HTTP 请求失败：%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		h.t.Fatalf("发送 %s %s 失败：%v", method, path, err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		h.t.Fatalf("读取 %s %s 响应失败：%v", method, path, err)
	}
	return httpResult{Status: response.StatusCode, Body: payload}
}

// requestChat 发送一次 chat 请求并把传输错误以 error 返回：并发用例在非测试 goroutine
// 中调用它，因此不得在这里使用 t.Fatalf。
func (h *harness) requestChat(ctx context.Context, sessionID, message string) (httpResult, error) {
	body, err := json.Marshal(map[string]string{"session_id": sessionID, "message": message})
	if err != nil {
		return httpResult{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/v1/chat", bytes.NewReader(body))
	if err != nil {
		return httpResult{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := h.client.Do(request)
	if err != nil {
		return httpResult{}, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		return httpResult{}, err
	}
	return httpResult{Status: response.StatusCode, Body: payload}, nil
}

// chat 发送一次 chat 请求。
func (h *harness) chat(sessionID, message string) httpResult {
	h.t.Helper()
	result, err := h.requestChat(context.Background(), sessionID, message)
	if err != nil {
		h.t.Fatalf("发送 chat 请求失败：%v", err)
	}
	return result
}

// chatOK 发送一次 chat 请求并要求 200 + COMPLETED，返回解析后的响应。
func (h *harness) chatOK(sessionID, message string) chatResponse {
	h.t.Helper()
	result := h.chat(sessionID, message)
	if result.Status != http.StatusOK {
		h.t.Fatalf("chat 状态码 = %d，期望 200（body=%s）", result.Status, result.Body)
	}
	var payload chatResponse
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		h.t.Fatalf("chat 响应不是合法 JSON：%v（%s）", err, result.Body)
	}
	if payload.Status != string(core.RunStatusCompleted) {
		h.t.Fatalf("chat status = %q，期望 COMPLETED（body=%s）", payload.Status, result.Body)
	}
	if payload.SessionID != sessionID || payload.RunID == "" {
		h.t.Fatalf("chat 响应身份异常：session_id=%q run_id=%q", payload.SessionID, payload.RunID)
	}
	return payload
}

// seed 通过公开 Save 预置非空 Summary / Todos：空会话的隔离与回滚断言没有判别力。
func (h *harness) seed(sessionID, summary string, todos []session.Todo) {
	h.t.Helper()
	if err := h.sessions.Save(context.Background(), session.Session{
		ID:      sessionID,
		Summary: summary,
		Todos:   todos,
	}); err != nil {
		h.t.Fatalf("预置会话 %q 状态失败：%v", sessionID, err)
	}
}

// snapshot 返回会话深拷贝快照。
func (h *harness) snapshot(sessionID string) session.Session {
	h.t.Helper()
	snapshot, err := h.sessions.Get(context.Background(), sessionID)
	if err != nil {
		h.t.Fatalf("读取会话 %q 快照失败：%v", sessionID, err)
	}
	return snapshot
}

// assertSessionUnchanged 断言会话在失败/取消路径上逐字段未变（INV-E2E-02）。
func (h *harness) assertSessionUnchanged(before session.Session) {
	h.t.Helper()
	after := h.snapshot(before.ID)
	if !reflect.DeepEqual(before, after) {
		h.t.Fatalf("会话在未完成的 Run 后发生变化：\nbefore=%s\nafter=%s",
			describeSession(before), describeSession(after))
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		h.t.Fatalf("失败路径后会话历史不合法：%v", err)
	}
}

// describeSession 生成会话的紧凑描述，便于失败时定位。
func describeSession(s session.Session) string {
	roles := make([]string, 0, len(s.Messages))
	for _, message := range s.Messages {
		roles = append(roles, string(message.Role))
	}
	return fmt.Sprintf("{id:%s messages:%d roles:%v summary:%q todos:%v}",
		s.ID, len(s.Messages), roles, s.Summary, s.Todos)
}

// assertMessageRoles 断言会话消息角色序列。
func (h *harness) assertMessageRoles(sessionID string, want ...core.Role) {
	h.t.Helper()
	snapshot := h.snapshot(sessionID)
	got := make([]core.Role, 0, len(snapshot.Messages))
	for _, message := range snapshot.Messages {
		got = append(got, message.Role)
	}
	if !reflect.DeepEqual(got, want) {
		h.t.Fatalf("会话 %q 角色序列 = %v，期望 %v", sessionID, got, want)
	}
	if err := core.ValidateMessages(snapshot.Messages); err != nil {
		h.t.Fatalf("会话 %q 历史不合法：%v", sessionID, err)
	}
}

// assertErrorResponse 断言失败响应的状态码与错误码。
func (h *harness) assertErrorResponse(result httpResult, wantStatus int, wantCode httpapi.ErrorCode) errorEnvelope {
	h.t.Helper()
	if result.Status != wantStatus {
		h.t.Fatalf("HTTP 状态码 = %d，期望 %d（body=%s）", result.Status, wantStatus, result.Body)
	}
	var payload errorEnvelope
	if err := json.Unmarshal(result.Body, &payload); err != nil {
		h.t.Fatalf("错误响应不是合法 JSON：%v（body=%s）", err, result.Body)
	}
	if payload.Error.Code != string(wantCode) {
		h.t.Fatalf("error.code = %q，期望 %q（body=%s）", payload.Error.Code, wantCode, result.Body)
	}
	if payload.Error.Message == "" {
		h.t.Fatalf("error.message 为空（body=%s）", result.Body)
	}
	return payload
}

// runAt 返回第 n 次（1 起）Run 的观测记录。
func (h *harness) runAt(n int) recordedRun {
	h.t.Helper()
	runs := h.recorder.runsCopy()
	if n < 1 || n > len(runs) {
		h.t.Fatalf("Run 调用序号 %d 越界：共 %d 次", n, len(runs))
	}
	return runs[n-1]
}

// assertRunCount 断言 Run 被调用的次数（INV-E2E-01）。
func (h *harness) assertRunCount(want int) {
	h.t.Helper()
	if got := h.recorder.runCount(); got != want {
		h.t.Fatalf("service.Runner 被调用 %d 次，期望 %d 次", got, want)
	}
}

// eventRecord 是 runlog JSON 行的解析形态（不依赖 runlog 私有类型）。
type eventRecord struct {
	Msg        string `json:"msg"`
	SessionID  string `json:"session_id"`
	RunID      string `json:"run_id"`
	Event      string `json:"event"`
	Step       int    `json:"step"`
	CallID     string `json:"call_id"`
	Status     string `json:"status"`
	ErrorKind  string `json:"error_kind"`
	DurationMS int64  `json:"duration_ms"`
}

// events 解析捕获到的全部日志行：每行必须是独立的完整 JSON 记录（并发不交织）。
func (h *harness) events() []eventRecord {
	h.t.Helper()
	raw := h.logs.String()
	lines := strings.Split(raw, "\n")
	records := make([]eventRecord, 0, len(lines))
	for index, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record eventRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			h.t.Fatalf("运行日志第 %d 行不是独立合法 JSON：%v（%q）", index+1, err, line)
		}
		if record.Msg != "run_event" {
			h.t.Fatalf("运行日志第 %d 行 msg = %q，期望 run_event", index+1, record.Msg)
		}
		records = append(records, record)
	}
	return records
}

// finishedEvents 返回指定会话的 run_finished 事件。
func (h *harness) finishedEvents(sessionID string) []eventRecord {
	out := make([]eventRecord, 0, 2)
	for _, record := range h.events() {
		if record.Event == "run_finished" && record.SessionID == sessionID {
			out = append(out, record)
		}
	}
	return out
}

// assertTerminal 交叉校验 Run 终态：recordingRunner 的 RunOutcome 与 run_finished 事件
// 必须同时等于期望的「状态 + 错误种类」组合，任一不一致即失败。
func (h *harness) assertTerminal(run recordedRun, wantStatus core.RunStatus, wantKind core.ErrorKind) {
	h.t.Helper()
	if run.Err != nil {
		h.t.Fatalf("Run 返回了未形成结论的 error：%v", run.Err)
	}
	if run.Outcome.Status != wantStatus {
		h.t.Fatalf("Run 终态 = %q，期望 %q（error=%v）", run.Outcome.Status, wantStatus, run.Outcome.Err)
	}
	gotKind := core.ErrorKind("")
	if run.Outcome.Err != nil {
		gotKind = core.KindOf(run.Outcome.Err)
	}
	if gotKind != wantKind {
		h.t.Fatalf("Run 错误种类 = %q，期望 %q（error=%v）", gotKind, wantKind, run.Outcome.Err)
	}

	events := h.finishedEvents(run.Request.SessionID)
	if len(events) != 1 {
		h.t.Fatalf("会话 %q 的 run_finished 事件数 = %d，期望 1", run.Request.SessionID, len(events))
	}
	if events[0].RunID != run.Request.RunID {
		h.t.Fatalf("事件 run_id = %q，recordingRunner run_id = %q", events[0].RunID, run.Request.RunID)
	}
	if events[0].Status != string(wantStatus) {
		h.t.Fatalf("事件 status = %q，期望 %q", events[0].Status, wantStatus)
	}
	if events[0].ErrorKind != string(wantKind) {
		h.t.Fatalf("事件 error_kind = %q，期望 %q", events[0].ErrorKind, wantKind)
	}
}

// waitFor 以有界轮询等待条件成立，避免用 Sleep 竞争时序。
func waitFor(t *testing.T, timeout time.Duration, condition func() bool, description string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("%s 未在 %s 内成立", description, timeout)
}

// toolMessageOf 返回会话中指定 CallID 的 tool 消息。
func toolMessageOf(t *testing.T, snapshot session.Session, callID string) core.Message {
	t.Helper()
	for _, message := range snapshot.Messages {
		if message.Role != e2eRoleTool || message.ToolResult == nil {
			continue
		}
		if message.ToolResult.CallID == callID {
			return message
		}
	}
	t.Fatalf("会话中不存在 CallID=%q 的 tool 消息", callID)
	return core.Message{}
}

// messageText 拼接一条消息的全部文本片段。
func messageText(message core.Message) string {
	var builder strings.Builder
	for _, part := range message.Content {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
