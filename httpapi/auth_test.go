package httpapi_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
)

// TestAuthMissingAndWrongCredentialAreIdentical 核对缺失与错误凭证返回同一 401 响应且不执行 Run。
func TestAuthMissingAndWrongCredentialAreIdentical(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userTexture("hi"), assistantTexture("hello"))
	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted, Reply: "ok"}}
	server := newTestServer(t, testConfig(fakeSecret), httpapi.Deps{Sessions: manager, Runner: runner})

	chatBody := `{"session_id":"s-1","message":"再回答一次"}`

	missing := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", chatBody, "")
	missingPayload := decodeError(t, missing, http.StatusUnauthorized, httpapi.CodeUnauthorized)

	wrong := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", "application/json", chatBody, "wrong-token")
	wrongPayload := decodeError(t, wrong, http.StatusUnauthorized, httpapi.CodeUnauthorized)

	if string(missing.body) != string(wrong.body) {
		t.Fatalf("缺失与错误凭证的响应体必须一致：缺失=%s 错误=%s", missing.body, wrong.body)
	}
	if missingPayload.Error.Message != wrongPayload.Error.Message {
		t.Fatal("缺失与错误凭证的 message 必须一致")
	}
	if runner.callCount() != 0 {
		t.Fatalf("未认证请求不得执行 Run，实际调用 %d 次", runner.callCount())
	}
	if strings.Contains(string(missing.body), fakeSecret) {
		t.Fatalf("错误体不得回显预期凭证：%s", missing.body)
	}

	after := snapshot(t, manager, "s-1")
	if len(after.Messages) != 2 {
		t.Fatalf("未认证请求不得写会话，实际消息数 = %d", len(after.Messages))
	}

	// 未认证创建会话同样被拒绝且不产生会话。
	create := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", `{"session_id":"unauthorized-create"}`, "")
	decodeError(t, create, http.StatusUnauthorized, httpapi.CodeUnauthorized)
	if _, err := manager.Get(context.Background(), "unauthorized-create"); err == nil {
		t.Fatal("未认证请求不得创建会话")
	}
}

// TestAuthPrecedesBodyParsing 核对认证先于 Content-Type 校验与请求体解析（INV-H02）。
func TestAuthPrecedesBodyParsing(t *testing.T) {
	manager := newManager()
	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted}}
	server := newTestServer(t, testConfig(fakeSecret), httpapi.Deps{Sessions: manager, Runner: runner})

	cases := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "错误媒体类型与非法 body", contentType: "text/plain", body: "not-json"},
		{name: "空媒体类型与数组 body", contentType: "", body: "[]"},
		{name: "正确媒体类型与超限 body", contentType: "application/json", body: strings.Repeat("x", 2<<20)},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			result := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/chat", item.contentType, item.body, "")
			decodeError(t, result, http.StatusUnauthorized, httpapi.CodeUnauthorized)
		})
	}

	if runner.callCount() != 0 {
		t.Fatalf("未认证请求不得执行 Run，实际 %d 次", runner.callCount())
	}
}

// TestAuthAcceptsConfiguredCredential 核对合法凭证与大小写不敏感的 Bearer 方案。
func TestAuthAcceptsConfiguredCredential(t *testing.T) {
	manager := newManager()
	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted, Reply: "ok"}}
	server := newTestServer(t, testConfig(fakeSecret), httpapi.Deps{Sessions: manager, Runner: runner})

	authorized := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "", fakeSecret)
	if authorized.status != http.StatusCreated {
		t.Fatalf("合法凭证应创建会话，实际状态 %d（body=%s）", authorized.status, authorized.body)
	}

	// 方案名大小写不敏感：小写 bearer 同样有效。
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/sessions", strings.NewReader(""))
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "bearer "+fakeSecret)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("发送请求失败：%v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("小写 bearer 方案应通过认证，实际状态 %d", response.StatusCode)
	}

	// 其他认证方案一律拒绝。
	basic := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "", "")
	if basic.status != http.StatusUnauthorized {
		t.Fatalf("无凭证请求应 401，实际 %d", basic.status)
	}
	request, err = http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL+"/v1/sessions", strings.NewReader(""))
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Basic "+fakeSecret)
	response, err = server.Client().Do(request)
	if err != nil {
		t.Fatalf("发送请求失败：%v", err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("非 Bearer 方案应 401，实际 %d", response.StatusCode)
	}
}

// TestHealthzExemptFromAuthAndLeakFree 核对 /healthz 免认证且不含配置或密钥。
func TestHealthzExemptFromAuthAndLeakFree(t *testing.T) {
	manager := newManager()
	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted}}
	server := newTestServer(t, testConfig(fakeSecret), httpapi.Deps{Sessions: manager, Runner: runner})

	result := doRequest(t, server.Client(), http.MethodGet, server.URL+"/healthz", "", "", "")
	if result.status != http.StatusOK {
		t.Fatalf("/healthz 应免认证返回 200，实际 %d（body=%s）", result.status, result.body)
	}
	if strings.TrimSpace(string(result.body)) != `{"status":"ok"}` {
		t.Fatalf("/healthz 响应体 = %s，期望固定 {\"status\":\"ok\"}", result.body)
	}

	// /healthz 不得泄漏凭证、配置字段或内部状态。
	healthzMarkers := []string{fakeSecret, "auth_token", "AuthToken", "listen_addr", "api_key", "API_KEY", "session_id", "model"}
	for name, text := range map[string]string{
		"healthz 响应体": string(result.body),
		"healthz 响应头": headerText(result.header),
	} {
		for _, marker := range healthzMarkers {
			if strings.Contains(text, marker) {
				t.Errorf("%s 泄漏敏感标记 %q：%s", name, marker, text)
			}
		}
	}

	// 配置打印只允许出现脱敏标记，绝不出现明文凭证。
	text := configText()
	if strings.Contains(text, fakeSecret) {
		t.Fatalf("配置打印泄漏明文凭证：%s", text)
	}
	if !strings.Contains(text, "<redacted>") {
		t.Fatalf("配置打印应显示 <redacted>：%s", text)
	}
}

// headerText 把响应头拼成可扫描文本。
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

// configText 返回配置的默认格式化输出（用于泄漏扫描）。
func configText() string {
	return httpapi.Config{ListenAddr: "127.0.0.1:8080", AuthToken: fakeSecret}.String()
}
