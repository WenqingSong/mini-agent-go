package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/service"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// sessionPayload 是创建会话的响应形态。
type sessionPayload struct {
	SessionID string `json:"session_id"`
}

// chatPayload 是 chat 的响应形态。
type chatPayload struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Reply     string `json:"reply"`
	LLMCalls  int    `json:"llm_calls"`
	ToolCalls int    `json:"tool_calls"`
}

// TestCreateSessionAndChatContract 核对两个端点的成功响应字段与 Runner 入参。
func TestCreateSessionAndChatContract(t *testing.T) {
	manager := newManager()
	runner := &fakeRunner{outcome: httpapi.RunOutcome{
		Status:    core.RunStatusCompleted,
		Reply:     "最终回答",
		LLMCalls:  2,
		ToolCalls: 1,
	}}
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

	created := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "", "")
	if created.status != http.StatusCreated {
		t.Fatalf("创建会话状态码 = %d，期望 201（body=%s）", created.status, created.body)
	}
	var createdPayload sessionPayload
	if err := json.Unmarshal(created.body, &createdPayload); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}
	if !isHex32(createdPayload.SessionID) {
		t.Fatalf("服务端生成的 session_id = %q，期望 32 位小写 hex", createdPayload.SessionID)
	}
	if _, err := manager.Get(context.Background(), createdPayload.SessionID); err != nil {
		t.Fatalf("创建响应返回的会话在会话层不可见：%v", err)
	}

	body := fmt.Sprintf(`{"session_id":%q,"message":"  保留前后空格  "}`, createdPayload.SessionID)
	chat := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", body, "")
	if chat.status != http.StatusOK {
		t.Fatalf("chat 状态码 = %d，期望 200（body=%s）", chat.status, chat.body)
	}
	var payload chatPayload
	if err := json.Unmarshal(chat.body, &payload); err != nil {
		t.Fatalf("chat 响应不是合法 JSON：%v", err)
	}
	if payload.SessionID != createdPayload.SessionID {
		t.Fatalf("chat 返回 session_id = %q，期望 %q", payload.SessionID, createdPayload.SessionID)
	}
	if !isHex32(payload.RunID) {
		t.Fatalf("run_id = %q，期望 32 位小写 hex", payload.RunID)
	}
	if payload.Status != "COMPLETED" {
		t.Fatalf("status = %q，期望 COMPLETED", payload.Status)
	}
	if payload.Reply != "最终回答" {
		t.Fatalf("reply = %q，期望最终助手文本", payload.Reply)
	}
	if payload.LLMCalls != 2 || payload.ToolCalls != 1 {
		t.Fatalf("调用计数 = (%d,%d)，期望 (2,1)", payload.LLMCalls, payload.ToolCalls)
	}

	request := runner.requestAt(t, 1)
	if request.SessionID != createdPayload.SessionID {
		t.Fatalf("Runner 收到 SessionID = %q，期望 %q", request.SessionID, createdPayload.SessionID)
	}
	if request.RunID != payload.RunID {
		t.Fatalf("Runner 收到 RunID = %q，期望响应中的 %q", request.RunID, payload.RunID)
	}
	if request.Message != "  保留前后空格  " {
		t.Fatalf("用户消息被改写：%q", request.Message)
	}

	// 响应中没有消息序列、Provider 载荷或 usage。
	for _, forbidden := range []string{"messages", "tool_calls_raw", "usage", "raw", "system_prompt"} {
		if strings.Contains(string(chat.body), forbidden) {
			t.Errorf("成功响应不应包含字段 %q：%s", forbidden, chat.body)
		}
	}
}

// TestCreateSessionExplicitIDAndDuplicate 核对显式 ID 与重复创建的 409。
func TestCreateSessionExplicitIDAndDuplicate(t *testing.T) {
	manager := newManager()
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: &fakeRunner{}})

	first := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", `{"session_id":"explicit-id"}`, "")
	if first.status != http.StatusCreated {
		t.Fatalf("首次创建状态码 = %d，期望 201（body=%s）", first.status, first.body)
	}
	var payload sessionPayload
	if err := json.Unmarshal(first.body, &payload); err != nil {
		t.Fatalf("响应不是合法 JSON：%v", err)
	}
	if payload.SessionID != "explicit-id" {
		t.Fatalf("响应 session_id = %q，期望回显 explicit-id", payload.SessionID)
	}

	duplicate := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", `{"session_id":"explicit-id"}`, "")
	decodeError(t, duplicate, http.StatusConflict, httpapi.CodeSessionExists)
}

// TestChatSessionPrecheck 核对未知与会话过期在前置检查阶段就被拒绝，且不执行 Run。
func TestChatSessionPrecheck(t *testing.T) {
	now := time.Unix(0, 0)
	manager := session.NewManager(session.ManagerConfig{TTL: time.Minute, Now: func() time.Time { return now }})
	seedSession(t, manager, "expiring", userTexture("旧消息"), assistantTexture("旧回答"))
	now = now.Add(2 * time.Minute)

	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted}}
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

	unknown := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", `{"session_id":"missing","message":"hi"}`, "")
	decodeError(t, unknown, http.StatusNotFound, httpapi.CodeSessionNotFound)

	expired := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", `{"session_id":"expiring","message":"hi"}`, "")
	decodeError(t, expired, http.StatusNotFound, httpapi.CodeSessionExpired)

	if runner.callCount() != 0 {
		t.Fatalf("预检失败时不得执行 Run，实际调用 %d 次", runner.callCount())
	}
	if _, err := manager.Get(context.Background(), "missing"); !errors.Is(err, session.ErrSessionNotFound) {
		t.Fatalf("未知会话不应被创建：%v", err)
	}
}

// TestChatRunnerSessionSentinel 核对 Runner 返回的会话哨兵与预检使用同一套错误码（TOCTOU 防御）。
func TestChatRunnerSessionSentinel(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userTexture("hi"), assistantTexture("hello"))
	_, sentinel := manager.Get(context.Background(), "gone")

	runner := &fakeRunner{err: sentinel}
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

	result := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", `{"session_id":"s-1","message":"hi"}`, "")
	decodeError(t, result, http.StatusNotFound, httpapi.CodeSessionNotFound)
}

// TestChatErrorMapping 表驱动核对「终态/错误 → 错误码 → 状态码」与 200 边界。
func TestChatErrorMapping(t *testing.T) {
	providerFailure := &core.RunError{
		Kind:    core.KindProviderFailure,
		Op:      "test",
		Message: "上游原始载荷 SECRET-PAYLOAD 不应出现在响应体",
	}
	toolFailure := &core.RunError{Kind: core.KindToolFailure, Op: "test", Message: "工具失败", CallID: "call-1"}

	cases := []struct {
		name       string
		outcome    httpapi.RunOutcome
		err        error
		wantStatus int
		wantCode   httpapi.ErrorCode
	}{
		{
			name:       "provider 失败",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed, Err: providerFailure, LLMCalls: 1},
			wantStatus: http.StatusBadGateway,
			wantCode:   httpapi.CodeProviderFailure,
		},
		{
			name:       "工具失败",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed, Err: toolFailure, ToolCalls: 1},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeToolFailure,
		},
		{
			name:       "模型调用预算耗尽",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusLimitExceeded, Err: core.NewError(core.KindLLMCallLimit, "test", "预算")},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   httpapi.CodeLLMCallLimit,
		},
		{
			name:       "工具调用预算耗尽",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusLimitExceeded, Err: core.NewError(core.KindToolCallLimit, "test", "预算")},
			wantStatus: http.StatusTooManyRequests,
			wantCode:   httpapi.CodeToolCallLimit,
		},
		{
			name:       "配对非法",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed, Err: core.NewError(core.KindInvalidPairing, "test", "配对")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInvalidPairing,
		},
		{
			name:       "内部错误",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed, Err: core.NewError(core.KindInternal, "test", "内部")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
		{
			name:       "超时终态",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusCancelled, Err: core.NewError(core.KindDeadlineExceeded, "test", "超时")},
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   httpapi.CodeDeadlineExceeded,
		},
		{
			name:       "取消终态",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusCancelled, Err: core.NewError(core.KindCancelled, "test", "取消")},
			wantStatus: http.StatusRequestTimeout,
			wantCode:   httpapi.CodeCancelled,
		},
		{
			name:       "Runner 返回 ctx 取消",
			err:        context.Canceled,
			wantStatus: http.StatusRequestTimeout,
			wantCode:   httpapi.CodeCancelled,
		},
		{
			name:       "Runner 返回 ctx 超时",
			err:        context.DeadlineExceeded,
			wantStatus: http.StatusGatewayTimeout,
			wantCode:   httpapi.CodeDeadlineExceeded,
		},
		{
			name:       "COMPLETED 却带错误",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusCompleted, Err: core.NewError(core.KindInternal, "test", "矛盾")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
		{
			name:       "非终态",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusRunning},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
		{
			name:       "终态缺少错误",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
		{
			name:       "状态与错误种类矛盾",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusFailed, Err: core.NewError(core.KindCancelled, "test", "取消")},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
		{
			name:       "负数计数",
			outcome:    httpapi.RunOutcome{Status: core.RunStatusCompleted, LLMCalls: -1},
			wantStatus: http.StatusInternalServerError,
			wantCode:   httpapi.CodeInternal,
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			manager := newManager()
			seedSession(t, manager, "s-1", userTexture("hi"), assistantTexture("hello"))
			runner := &fakeRunner{outcome: item.outcome, err: item.err}
			server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

			result := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", `{"session_id":"s-1","message":"hi"}`, "")
			decodeError(t, result, item.wantStatus, item.wantCode)

			body := string(result.body)
			for _, forbidden := range []string{"SECRET-PAYLOAD", "agent.Loop", "/root/", "goroutine", "stack"} {
				if strings.Contains(body, forbidden) {
					t.Fatalf("错误响应泄漏内部细节 %q：%s", forbidden, body)
				}
			}
			if strings.Contains(body, "call-1") {
				t.Fatalf("错误响应不应回显 CallID：%s", body)
			}
		})
	}
}

// TestChatFailureHasNoSideEffects 核对任何非 COMPLETED 路径都不产生会话写入。
func TestChatFailureHasNoSideEffects(t *testing.T) {
	outcomes := []httpapi.RunOutcome{
		{Status: core.RunStatusFailed, Err: core.NewError(core.KindProviderFailure, "test", "上游")},
		{Status: core.RunStatusCancelled, Err: core.NewError(core.KindCancelled, "test", "取消")},
		{Status: core.RunStatusCancelled, Err: core.NewError(core.KindDeadlineExceeded, "test", "超时")},
		{Status: core.RunStatusLimitExceeded, Err: core.NewError(core.KindLLMCallLimit, "test", "预算")},
	}

	for index, outcome := range outcomes {
		t.Run(fmt.Sprintf("case-%d", index), func(t *testing.T) {
			manager := newManager()
			seedSession(t, manager, "s-1", userTexture("已提交用户"), assistantTexture("已提交回答"))
			before := snapshot(t, manager, "s-1")

			runner := &fakeRunner{outcome: outcome}
			server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

			result := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", `{"session_id":"s-1","message":"这一轮应被丢弃"}`, "")
			if result.status == http.StatusOK {
				t.Fatalf("非 COMPLETED 路径不得返回 200：%s", result.body)
			}

			after := snapshot(t, manager, "s-1")
			if !reflect.DeepEqual(before.Messages, after.Messages) {
				t.Fatalf("失败路径改变了会话历史：before=%d after=%d", len(before.Messages), len(after.Messages))
			}
			if !reflect.DeepEqual(before.Todos, after.Todos) {
				t.Fatal("失败路径改变了 Todo 列表")
			}
			if err := core.ValidateMessages(after.Messages); err != nil {
				t.Fatalf("失败路径后历史不再合法：%v", err)
			}
		})
	}
}

// TestChatRejectsInvalidInputWithoutSideEffects 端到端核对参数类错误的状态码、错误码与无副作用。
func TestChatRejectsInvalidInputWithoutSideEffects(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userTexture("已提交用户"), assistantTexture("已提交回答"))
	before := snapshot(t, manager, "s-1")

	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted}}
	cfg := testConfig("")
	cfg.MaxRequestBytes = 128
	server := newTestServer(t, cfg, httpapi.Deps{Sessions: manager, Runner: runner})

	cases := []struct {
		name        string
		contentType string
		body        string
		wantStatus  int
		wantCode    httpapi.ErrorCode
	}{
		{name: "错误媒体类型", contentType: "text/plain", body: `{"session_id":"s-1","message":"hi"}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: httpapi.CodeUnsupportedMediaType},
		{name: "缺少 Content-Type", contentType: "", body: `{"session_id":"s-1","message":"hi"}`, wantStatus: http.StatusUnsupportedMediaType, wantCode: httpapi.CodeUnsupportedMediaType},
		{name: "数组 body", contentType: "application/json", body: `[]`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidJSON},
		{name: "null body", contentType: "application/json", body: `null`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidJSON},
		{name: "尾部数据", contentType: "application/json", body: `{"session_id":"s-1","message":"hi"}{"x":1}`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidJSON},
		{name: "未知字段", contentType: "application/json", body: `{"session_id":"s-1","message":"hi","extra":true}`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeUnknownField},
		{name: "缺少 message", contentType: "application/json", body: `{"session_id":"s-1"}`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeMissingField},
		{name: "空 body", contentType: "application/json", body: "", wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeMissingField},
		{name: "message 类型错误", contentType: "application/json", body: `{"session_id":"s-1","message":123}`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidField},
		{name: "session_id 非法字符", contentType: "application/json", body: `{"session_id":"bad id","message":"hi"}`, wantStatus: http.StatusBadRequest, wantCode: httpapi.CodeInvalidField},
		{
			name:        "请求体超限",
			contentType: "application/json",
			body:        fmt.Sprintf(`{"session_id":"s-1","message":%q}`, strings.Repeat("a", 256)),
			wantStatus:  http.StatusRequestEntityTooLarge,
			wantCode:    httpapi.CodePayloadTooLarge,
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", item.contentType, item.body, "")
			decodeError(t, result, item.wantStatus, item.wantCode)
		})
	}

	if runner.callCount() != 0 {
		t.Fatalf("参数类错误不得执行 Run，实际调用 %d 次", runner.callCount())
	}
	after := snapshot(t, manager, "s-1")
	if !reflect.DeepEqual(before.Messages, after.Messages) || !reflect.DeepEqual(before.Todos, after.Todos) {
		t.Fatal("参数类错误产生了会话副作用")
	}
}

// TestRoutingContract 核对路由级 404/405 与 Allow 头。
func TestRoutingContract(t *testing.T) {
	manager := newManager()
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: &fakeRunner{}})

	cases := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		wantCode   httpapi.ErrorCode
		wantAllow  string
	}{
		{name: "未知路径", method: http.MethodGet, path: "/v1/unknown", wantStatus: http.StatusNotFound, wantCode: httpapi.CodeNotFound},
		{name: "路径尾斜杠", method: http.MethodPost, path: "/v1/chat/", wantStatus: http.StatusNotFound, wantCode: httpapi.CodeNotFound},
		{name: "根路径", method: http.MethodGet, path: "/", wantStatus: http.StatusNotFound, wantCode: httpapi.CodeNotFound},
		{name: "chat 方法不符", method: http.MethodGet, path: "/v1/chat", wantStatus: http.StatusMethodNotAllowed, wantCode: httpapi.CodeMethodNotAllowed, wantAllow: "POST"},
		{name: "sessions 方法不符", method: http.MethodDelete, path: "/v1/sessions", wantStatus: http.StatusMethodNotAllowed, wantCode: httpapi.CodeMethodNotAllowed, wantAllow: "POST"},
		{name: "healthz 方法不符", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed, wantCode: httpapi.CodeMethodNotAllowed, wantAllow: "GET"},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result := doRequest(t, server.Client(), item.method, server.URL+item.path, "application/json", "", "")
			decodeError(t, result, item.wantStatus, item.wantCode)
			if item.wantAllow != "" && result.header.Get("Allow") != item.wantAllow {
				t.Fatalf("Allow = %q，期望 %q", result.header.Get("Allow"), item.wantAllow)
			}
		})
	}
}

// TestRealRunnerEndToEnd 以真实 session.Manager + 真实 agent.Loop（经 service 装配）+ Fake Provider
// 证明 AC-001 多轮历史与 AC-002 工具链路：HTTP 只返回最终文本与调用计数。
func TestRealRunnerEndToEnd(t *testing.T) {
	calculator := newRecordingTool("calculator")
	registry := tool.NewMapRegistry()
	if err := registry.Register(calculator); err != nil {
		t.Fatalf("注册工具失败：%v", err)
	}
	if err := registry.Seal(); err != nil {
		t.Fatalf("封闭注册表失败：%v", err)
	}

	provider := &scriptedProvider{script: []llm.Response{
		callsResponse("resp-1", toolCall("call-1", "calculator", `{"op":"add","a":1,"b":2}`)),
		textResponse("resp-2", "3"),
		textResponse("resp-3", "已完成"),
	}}
	manager := newManager()
	runner, err := service.NewRunner(service.Deps{
		Provider: provider,
		Sessions: manager,
		Registry: registry,
	}, service.Config{Model: "test-model"})
	if err != nil {
		t.Fatalf("构造 Runner 失败：%v", err)
	}
	server := newTestServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})

	created := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "", "")
	if created.status != http.StatusCreated {
		t.Fatalf("创建会话失败：%d %s", created.status, created.body)
	}
	var sessionPayloadValue sessionPayload
	if err := json.Unmarshal(created.body, &sessionPayloadValue); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}
	sessionID := sessionPayloadValue.SessionID

	first := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json",
		fmt.Sprintf(`{"session_id":%q,"message":"1+2 等于多少"}`, sessionID), "")
	if first.status != http.StatusOK {
		t.Fatalf("第一轮 chat 失败：%d %s", first.status, first.body)
	}
	var firstPayload chatPayload
	if err := json.Unmarshal(first.body, &firstPayload); err != nil {
		t.Fatalf("chat 响应不是合法 JSON：%v", err)
	}
	if firstPayload.Reply != "3" {
		t.Fatalf("reply = %q，期望最终文本 3（不是中间 tool_call 载荷）", firstPayload.Reply)
	}
	if firstPayload.Status != "COMPLETED" || firstPayload.LLMCalls != 2 || firstPayload.ToolCalls != 1 {
		t.Fatalf("第一轮结论 = (%s,%d,%d)，期望 (COMPLETED,2,1)",
			firstPayload.Status, firstPayload.LLMCalls, firstPayload.ToolCalls)
	}

	executed := calculator.executed()
	if len(executed) != 1 {
		t.Fatalf("工具执行次数 = %d，期望 1", len(executed))
	}
	if executed[0].ID != "call-1" || executed[0].Name != "calculator" {
		t.Fatalf("工具调用身份 = (%q,%q)，期望 (call-1,calculator)", executed[0].ID, executed[0].Name)
	}
	if string(executed[0].Arguments) != `{"op":"add","a":1,"b":2}` {
		t.Fatalf("工具参数 = %s，期望原始 JSON", executed[0].Arguments)
	}

	committed := snapshot(t, manager, sessionID)
	if len(committed.Messages) != 4 {
		t.Fatalf("提交后消息数 = %d，期望 4（user, assistant(tool_calls), tool, assistant）", len(committed.Messages))
	}
	roles := []core.Role{}
	for _, message := range committed.Messages {
		roles = append(roles, message.Role)
	}
	wantRoles := []core.Role{testRoleUser, testRoleAssistant, core.Role("tool"), testRoleAssistant}
	if !reflect.DeepEqual(roles, wantRoles) {
		t.Fatalf("提交后角色序列 = %v，期望 %v", roles, wantRoles)
	}
	if err := core.ValidateMessages(committed.Messages); err != nil {
		t.Fatalf("提交后历史不合法：%v", err)
	}

	// 第二轮：多轮历史由 Session 提供，Fake Provider 必须看到上一轮的用户消息与助手回复。
	second := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json",
		fmt.Sprintf(`{"session_id":%q,"message":"再总结一次"}`, sessionID), "")
	if second.status != http.StatusOK {
		t.Fatalf("第二轮 chat 失败：%d %s", second.status, second.body)
	}

	thirdRequest := provider.requestAt(t, 3)
	seenUser, seenAssistant := false, false
	for _, message := range thirdRequest.Messages {
		if message.Role == testRoleUser && messageText(message) == "1+2 等于多少" {
			seenUser = true
		}
		if message.Role == testRoleAssistant && messageText(message) == "3" {
			seenAssistant = true
		}
	}
	if !seenUser || !seenAssistant {
		t.Fatalf("第二轮模型请求缺少上一轮历史：user=%v assistant=%v", seenUser, seenAssistant)
	}

	// 一次 chat 只能出现一条本轮用户消息：会话中不得有重复或半轮历史。
	countUser := 0
	for _, message := range snapshot(t, manager, sessionID).Messages {
		if message.Role == testRoleUser {
			countUser++
		}
	}
	if countUser != 2 {
		t.Fatalf("两轮对话的用户消息数 = %d，期望 2（无重复 user 消息）", countUser)
	}
}

// isHex32 判断是否为 32 位小写 hex。
func isHex32(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, char := range value {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}

// messageText 拼接消息的文本片段。
func messageText(message core.Message) string {
	var builder strings.Builder
	for _, part := range message.Content {
		builder.WriteString(part.Text)
	}
	return builder.String()
}
