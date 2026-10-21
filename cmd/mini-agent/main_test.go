package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/contextmgr"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/service"
)

// 测试注入的假凭证：不得出现在任何响应、日志或配置输出中。
const (
	fakeAuthToken = "fake-auth-token-9b7c"
	fakeLLMAPIKey = "fake-llm-api-key-4f2a"
)

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

// envFunc 把 map 适配成 getenv。
func envFunc(values map[string]string) func(string) string {
	return func(name string) string { return values[name] }
}

// fakeOpenAI 启动一个 OpenAI 兼容的进程内假服务，记录收到的请求体。
func fakeOpenAI(t *testing.T, reply string) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.Copy(io.Discard, r.Body)
		mu.Lock()
		calls++
		mu.Unlock()

		payload := map[string]any{
			"id": "chatcmpl-test",
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}))
	t.Cleanup(server.Close)
	return server, func() int {
		mu.Lock()
		defer mu.Unlock()
		return calls
	}
}

// startRun 在后台运行服务，返回就绪地址、信号通道、运行结果与完成信号。
//
// 测试结束（含失败）时取消 ctx，保证后台服务退出，不遗留监听。
func startRun(t *testing.T, env map[string]string, logs *syncBuffer) (string, chan os.Signal, <-chan error, <-chan struct{}) {
	t.Helper()
	ready := make(chan string, 1)
	signals := make(chan os.Signal, 4)
	results := make(chan error, 1)
	done := make(chan struct{})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("服务未在取消后退出")
		}
	})

	go func() {
		defer close(done)
		results <- run(ctx, options{
			getenv:  envFunc(env),
			stderr:  logs,
			onReady: func(addr string) { ready <- addr },
			signals: signals,
		})
	}()

	select {
	case addr := <-ready:
		return "http://" + addr, signals, results, done
	case err := <-results:
		t.Fatalf("服务未能进入监听：%v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("服务未在限定时间内进入监听")
	}
	return "", nil, nil, nil
}

// httpJSON 发送一次带凭证的 JSON 请求。
func httpJSON(t *testing.T, method, url, body, token string) (int, []byte, http.Header) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	request, err := http.NewRequest(method, url, reader)
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("发送请求失败：%v", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("读取响应失败：%v", err)
	}
	return response.StatusCode, payload, response.Header
}

// TestRunFailClosedOnInvalidConfig 核对非法或不完整配置下启动返回错误且不进入监听。
func TestRunFailClosedOnInvalidConfig(t *testing.T) {
	base := map[string]string{
		"MINI_AGENT_LISTEN_ADDR":   "127.0.0.1:0",
		"MINI_AGENT_LLM_BASE_URL":  "http://127.0.0.1:1/v1",
		"MINI_AGENT_MODEL":         "test-model",
		"MINI_AGENT_AUTH_TOKEN":    fakeAuthToken,
		"MINI_AGENT_LLM_API_KEY":   fakeLLMAPIKey,
		"MINI_AGENT_MAX_LLM_CALLS": "8",
	}

	cases := []struct {
		name   string
		mutate func(map[string]string)
	}{
		{name: "缺少 LLM BaseURL", mutate: func(values map[string]string) { delete(values, "MINI_AGENT_LLM_BASE_URL") }},
		{name: "缺少 Model", mutate: func(values map[string]string) { delete(values, "MINI_AGENT_MODEL") }},
		{name: "监听地址非法", mutate: func(values map[string]string) { values["MINI_AGENT_LISTEN_ADDR"] = "127.0.0.1" }},
		{name: "监听端口非法", mutate: func(values map[string]string) { values["MINI_AGENT_LISTEN_ADDR"] = "127.0.0.1:99999" }},
		{name: "非回环监听缺少凭证", mutate: func(values map[string]string) {
			values["MINI_AGENT_LISTEN_ADDR"] = "0.0.0.0:0"
			delete(values, "MINI_AGENT_AUTH_TOKEN")
		}},
		{name: "时长非法", mutate: func(values map[string]string) { values["MINI_AGENT_REQUEST_TIMEOUT"] = "60" }},
		{name: "时长为零", mutate: func(values map[string]string) { values["MINI_AGENT_REQUEST_TIMEOUT"] = "0s" }},
		{name: "上限为负", mutate: func(values map[string]string) { values["MINI_AGENT_MAX_REQUEST_BYTES"] = "-1" }},
		{name: "预算为零", mutate: func(values map[string]string) { values["MINI_AGENT_MAX_LLM_CALLS"] = "0" }},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			values := make(map[string]string, len(base))
			for key, value := range base {
				values[key] = value
			}
			item.mutate(values)

			logs := &syncBuffer{}
			ready := false
			done := make(chan error, 1)
			go func() {
				done <- run(context.Background(), options{
					getenv:  envFunc(values),
					stderr:  logs,
					onReady: func(string) { ready = true },
				})
			}()

			select {
			case err := <-done:
				if err == nil {
					t.Fatal("非法配置应返回错误")
				}
				if strings.Contains(err.Error(), fakeAuthToken) || strings.Contains(err.Error(), fakeLLMAPIKey) {
					t.Fatalf("启动错误泄漏凭证：%v", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("非法配置下启动未及时返回")
			}
			if ready {
				t.Fatal("配置非法时不得进入监听")
			}
		})
	}
}

// TestRunEndToEndAndGracefulShutdown 端到端核对启动、健康检查、认证、chat、关闭与零泄漏。
func TestRunEndToEndAndGracefulShutdown(t *testing.T) {
	llmServer, llmCalls := fakeOpenAI(t, "你好，我是测试助手")
	logs := &syncBuffer{}

	baseURL, signals, results, _ := startRun(t, map[string]string{
		"MINI_AGENT_LISTEN_ADDR":     "127.0.0.1:0",
		"MINI_AGENT_AUTH_TOKEN":      fakeAuthToken,
		"MINI_AGENT_LLM_BASE_URL":    llmServer.URL,
		"MINI_AGENT_LLM_API_KEY":     fakeLLMAPIKey,
		"MINI_AGENT_MODEL":           "test-model",
		"MINI_AGENT_SYSTEM_PROMPT":   "你是测试助手",
		"MINI_AGENT_REQUEST_TIMEOUT": "30s",
	}, logs)

	// 就绪后 /healthz 免认证可用。
	status, body, header := httpJSON(t, http.MethodGet, baseURL+"/healthz", "", "")
	if status != http.StatusOK || strings.TrimSpace(string(body)) != `{"status":"ok"}` {
		t.Fatalf("/healthz = (%d,%s)，期望 (200,{\"status\":\"ok\"})", status, body)
	}

	// 未认证请求被拒绝且不执行 Run。
	status, body, _ = httpJSON(t, http.MethodPost, baseURL+"/v1/sessions", "", "")
	if status != http.StatusUnauthorized || !strings.Contains(string(body), "unauthorized") {
		t.Fatalf("未认证创建会话 = (%d,%s)，期望 401 unauthorized", status, body)
	}

	status, body, _ = httpJSON(t, http.MethodPost, baseURL+"/v1/sessions", "", fakeAuthToken)
	if status != http.StatusCreated {
		t.Fatalf("创建会话 = (%d,%s)，期望 201", status, body)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}

	chatBody := fmt.Sprintf(`{"session_id":%q,"message":"你好"}`, created.SessionID)
	status, body, chatHeader := httpJSON(t, http.MethodPost, baseURL+"/v1/chat", chatBody, fakeAuthToken)
	if status != http.StatusOK {
		t.Fatalf("chat = (%d,%s)，期望 200", status, body)
	}
	var chat struct {
		Status string `json:"status"`
		Reply  string `json:"reply"`
	}
	if err := json.Unmarshal(body, &chat); err != nil {
		t.Fatalf("chat 响应不是合法 JSON：%v", err)
	}
	if chat.Status != "COMPLETED" || chat.Reply != "你好，我是测试助手" {
		t.Fatalf("chat 结论 = (%s,%s)，期望 (COMPLETED,最终文本)", chat.Status, chat.Reply)
	}
	if llmCalls() != 1 {
		t.Fatalf("Provider 调用次数 = %d，期望 1", llmCalls())
	}

	// 响应体与响应头零泄漏。
	scanForSecrets(t, "chat 响应体", string(body))
	scanForSecrets(t, "chat 响应头", headerText(chatHeader))
	scanForSecrets(t, "healthz 响应头", headerText(header))

	// 终止信号触发优雅关闭，进程返回。
	signals <- os.Interrupt
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("优雅关闭返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("收到终止信号后未在限定时间内退出")
	}

	// 关闭后新连接被拒绝。
	client := &http.Client{Timeout: time.Second}
	if _, err := client.Get(baseURL + "/healthz"); err == nil {
		t.Fatal("关闭后新连接应被拒绝")
	}

	// 日志字节零泄漏：假 Bearer 凭证与假 LLM API Key 都不得出现。
	captured := logs.String()
	scanForSecrets(t, "运行日志", captured)
	if captured == "" {
		t.Fatal("运行日志为空：至少应有 Run 事件用于观察")
	}
}

// TestRunShutdownCancelsInflightProvider 核对宽限期满后取消在途 Run（Fake Provider 观察到取消）。
func TestRunShutdownCancelsInflightProvider(t *testing.T) {
	entered := make(chan struct{})
	observedCancel := make(chan struct{})
	var once sync.Once
	llmServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		once.Do(func() { close(entered) })
		// 阻塞到请求 context 结束，模拟慢 Provider。
		<-r.Context().Done()
		close(observedCancel)
	}))
	t.Cleanup(llmServer.Close)

	logs := &syncBuffer{}
	baseURL, signals, results, _ := startRun(t, map[string]string{
		"MINI_AGENT_LISTEN_ADDR":     "127.0.0.1:0",
		"MINI_AGENT_AUTH_TOKEN":      fakeAuthToken,
		"MINI_AGENT_LLM_BASE_URL":    llmServer.URL,
		"MINI_AGENT_LLM_API_KEY":     fakeLLMAPIKey,
		"MINI_AGENT_MODEL":           "test-model",
		"MINI_AGENT_REQUEST_TIMEOUT": "10s",
		"MINI_AGENT_SHUTDOWN_GRACE":  "50ms",
	}, logs)

	status, body, _ := httpJSON(t, http.MethodPost, baseURL+"/v1/sessions", "", fakeAuthToken)
	if status != http.StatusCreated {
		t.Fatalf("创建会话 = (%d,%s)，期望 201", status, body)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}

	go func() {
		chatBody := fmt.Sprintf(`{"session_id":%q,"message":"慢请求"}`, created.SessionID)
		request, err := http.NewRequest(http.MethodPost, baseURL+"/v1/chat", strings.NewReader(chatBody))
		if err != nil {
			return
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Authorization", "Bearer "+fakeAuthToken)
		client := &http.Client{Timeout: 10 * time.Second}
		response, err := client.Do(request)
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Provider 未收到请求")
	}

	signals <- os.Interrupt

	select {
	case <-observedCancel:
	case <-time.After(5 * time.Second):
		t.Fatal("宽限期满后在途 Run 未被取消：Provider 未观察到 context 结束")
	}
	select {
	case <-results:
		// 宽限期满的强制关闭返回非 nil 错误是预期行为；这里只要求进程返回且不 panic。
	case <-time.After(5 * time.Second):
		t.Fatal("在途 Run 被取消后进程未退出")
	}
}

// TestRunRepeatedTerminationSignals 核对关闭期间重复信号不 panic 且进程仍能退出。
func TestRunRepeatedTerminationSignals(t *testing.T) {
	llmServer, _ := fakeOpenAI(t, "ok")
	logs := &syncBuffer{}
	_, signals, results, _ := startRun(t, map[string]string{
		"MINI_AGENT_LISTEN_ADDR":  "127.0.0.1:0",
		"MINI_AGENT_AUTH_TOKEN":   fakeAuthToken,
		"MINI_AGENT_LLM_BASE_URL": llmServer.URL,
		"MINI_AGENT_MODEL":        "test-model",
	}, logs)

	signals <- os.Interrupt
	signals <- os.Interrupt

	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("重复终止信号下关闭返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("重复终止信号下进程未退出")
	}
}

// scanForSecrets 断言文本中不出现测试注入的假凭证。
func scanForSecrets(t *testing.T, where, text string) {
	t.Helper()
	for _, secret := range []string{fakeAuthToken, fakeLLMAPIKey} {
		if strings.Contains(text, secret) {
			t.Fatalf("%s 泄漏凭证 %q：%s", where, secret, text)
		}
	}
}

// headerText 把响应头拼接为可扫描文本。
func headerText(header http.Header) string {
	var builder strings.Builder
	for name, values := range header {
		builder.WriteString(name)
		builder.WriteString(": ")
		builder.WriteString(strings.Join(values, ","))
		builder.WriteString("\n")
	}
	return builder.String()
}

// TestLoadConfigContextBudget 核对上下文预算与保留下限的配置解析：
// 未配置时保持禁用且保留下限取默认值；显式负值/非整数属于配置错误。
func TestLoadConfigContextBudget(t *testing.T) {
	base := map[string]string{
		"MINI_AGENT_LLM_BASE_URL": "http://127.0.0.1:1/v1",
		"MINI_AGENT_MODEL":        "test-model",
	}

	_, serviceConfig, _, err := loadConfig(envFunc(base))
	if err != nil {
		t.Fatalf("未配置上下文预算时应使用默认值：%v", err)
	}
	if serviceConfig.TokenBudget != 0 {
		t.Fatalf("TokenBudget 默认值 = %d，期望 0（禁用压缩）", serviceConfig.TokenBudget)
	}
	if serviceConfig.KeepRecent != service.DefaultKeepRecent {
		t.Fatalf("KeepRecent 默认值 = %d，期望 %d", serviceConfig.KeepRecent, service.DefaultKeepRecent)
	}

	enabled := map[string]string{
		"MINI_AGENT_LLM_BASE_URL":         base["MINI_AGENT_LLM_BASE_URL"],
		"MINI_AGENT_MODEL":                base["MINI_AGENT_MODEL"],
		"MINI_AGENT_CONTEXT_TOKEN_BUDGET": "4096",
		"MINI_AGENT_CONTEXT_KEEP_RECENT":  "6",
	}
	_, serviceConfig, _, err = loadConfig(envFunc(enabled))
	if err != nil {
		t.Fatalf("合法上下文配置解析失败：%v", err)
	}
	if serviceConfig.TokenBudget != 4096 || serviceConfig.KeepRecent != 6 {
		t.Fatalf("上下文配置未生效：TokenBudget=%d KeepRecent=%d", serviceConfig.TokenBudget, serviceConfig.KeepRecent)
	}

	invalid := []struct {
		name  string
		key   string
		value string
	}{
		{name: "预算为负", key: "MINI_AGENT_CONTEXT_TOKEN_BUDGET", value: "-1"},
		{name: "保留下限为负", key: "MINI_AGENT_CONTEXT_KEEP_RECENT", value: "-1"},
		{name: "预算不是整数", key: "MINI_AGENT_CONTEXT_TOKEN_BUDGET", value: "4k"},
	}
	for _, item := range invalid {
		t.Run(item.name, func(t *testing.T) {
			values := map[string]string{
				"MINI_AGENT_LLM_BASE_URL": base["MINI_AGENT_LLM_BASE_URL"],
				"MINI_AGENT_MODEL":        base["MINI_AGENT_MODEL"],
				item.key:                  item.value,
			}
			if _, _, _, err := loadConfig(envFunc(values)); err == nil {
				t.Fatal("非法上下文配置必须被拒绝")
			} else if !strings.Contains(err.Error(), item.key) {
				t.Fatalf("错误文本未指出非法变量 %s：%v", item.key, err)
			}
		})
	}
}

// wireMessage 是 Chat Completions 请求中的一条消息（只解析本用例需要的字段）。
type wireMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// recordingOpenAI 启动固定的 OpenAI 兼容假服务并记录每次请求的消息序列。
func recordingOpenAI(t *testing.T, reply string) (*httptest.Server, func() [][]wireMessage) {
	t.Helper()
	var (
		mu      sync.Mutex
		records [][]wireMessage
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var request struct {
			Messages []wireMessage `json:"messages"`
		}
		payload, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(payload, &request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		mu.Lock()
		records = append(records, request.Messages)
		mu.Unlock()

		response := map[string]any{
			"id": "chatcmpl-test",
			"choices": []map[string]any{{
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 3, "completion_tokens": 2},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return server, func() [][]wireMessage {
		mu.Lock()
		defer mu.Unlock()
		out := make([][]wireMessage, len(records))
		copy(out, records)
		return out
	}
}

// TestRunCompactsWithConfiguredBudgetEnv 是「make up 后可通过 HTTP 触发压缩」的进程级证据：
// 只通过环境变量启用预算，正式 run() 装配的 HTTP 服务在长对话中实际发生压缩，
// 每次发给 Provider 的请求都不超过配置预算，且压缩后的历史被截断、摘要被召回。
func TestRunCompactsWithConfiguredBudgetEnv(t *testing.T) {
	const budget = 800
	llmServer, requests := recordingOpenAI(t, "已收到")
	logs := &syncBuffer{}
	baseURL, signals, results, _ := startRun(t, map[string]string{
		"MINI_AGENT_LISTEN_ADDR":          "127.0.0.1:0",
		"MINI_AGENT_AUTH_TOKEN":           fakeAuthToken,
		"MINI_AGENT_LLM_BASE_URL":         llmServer.URL,
		"MINI_AGENT_MODEL":                "test-model",
		"MINI_AGENT_CONTEXT_TOKEN_BUDGET": "800",
		"MINI_AGENT_CONTEXT_KEEP_RECENT":  "2",
		"MINI_AGENT_REQUEST_TIMEOUT":      "30s",
	}, logs)

	status, body, _ := httpJSON(t, http.MethodPost, baseURL+"/v1/sessions", "", fakeAuthToken)
	if status != http.StatusCreated {
		t.Fatalf("创建会话 = (%d,%s)，期望 201", status, body)
	}
	var created struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}

	longQuestion := strings.Repeat("长问题内容", 60) // 300 个 CJK 字符
	for round := 1; round <= 4; round++ {
		chatBody := fmt.Sprintf(`{"session_id":%q,"message":%q}`, created.SessionID, longQuestion+fmt.Sprintf("第%d轮", round))
		status, body, _ = httpJSON(t, http.MethodPost, baseURL+"/v1/chat", chatBody, fakeAuthToken)
		if status != http.StatusOK {
			t.Fatalf("第 %d 轮 chat = (%d,%s)，期望 200", round, status, body)
		}
	}

	records := requests()
	if len(records) != 4 {
		t.Fatalf("Provider 请求次数 = %d，期望 4", len(records))
	}
	var estimator contextmgr.HeuristicEstimator
	compacted := 0
	for index, messages := range records {
		total := 0
		for _, message := range messages {
			total += estimator.EstimateMessage(core.Message{
				Role:    core.Role(message.Role),
				Content: []core.ContentPart{{Type: core.ContentType("text"), Text: message.Content}},
			})
			if message.Role == "system" && strings.Contains(message.Content, "早期对话摘要") {
				compacted++
			}
		}
		if total > budget {
			t.Fatalf("第 %d 次请求估算值 = %d，超出配置预算 %d", index+1, total, budget)
		}
	}
	if compacted == 0 {
		t.Fatalf("正式服务未发生上下文压缩：所有请求都没有摘要 system 消息")
	}

	// 压缩后最早的历史不再发送，但摘要必须召回。
	last := records[len(records)-1]
	lastText := ""
	for _, message := range last {
		lastText += message.Content
	}
	if strings.Contains(lastText, "第1轮") {
		t.Fatalf("压缩后仍把最早一轮历史发送给 Provider：%s", lastText)
	}
	if !strings.Contains(lastText, "早期对话摘要") {
		t.Fatalf("压缩后未召回摘要：%s", lastText)
	}

	signals <- os.Interrupt
	select {
	case err := <-results:
		if err != nil {
			t.Fatalf("优雅关闭返回错误：%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("收到终止信号后未在限定时间内退出")
	}
	scanForSecrets(t, "运行日志", logs.String())
}
