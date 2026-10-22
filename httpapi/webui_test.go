package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
)

// testIndexHTML 是静态产物的最小入口，含一个可被断言的前端脚本引用。
const testIndexHTML = `<!doctype html><html><body><div id="root"></div><script type="module" src="/assets/app-abc123.js"></script></body></html>`

// newWebDir 构造一个最小前端产物目录：index.html + 内容哈希资源 + 图标。
func newWebDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte(testIndexHTML), 0o644); err != nil {
		t.Fatalf("写入 index.html 失败：%v", err)
	}
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o755); err != nil {
		t.Fatalf("创建 assets 目录失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "app-abc123.js"), []byte("console.log('ok')"), 0o644); err != nil {
		t.Fatalf("写入 JS 产物失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(assets, "app-abc123.css"), []byte("body{color:#000}"), 0o644); err != nil {
		t.Fatalf("写入 CSS 产物失败：%v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "favicon.svg"), []byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), 0o644); err != nil {
		t.Fatalf("写入 favicon 失败：%v", err)
	}
	return dir
}

// webTestServer 启动一个同时提供页面与 API 的进程内服务。
func webTestServer(t *testing.T, token string, runner httpapi.Runner) *httptest.Server {
	t.Helper()
	cfg := testConfig(token)
	cfg.WebDir = newWebDir(t)
	return newTestServer(t, cfg, httpapi.Deps{Sessions: newManager(), Runner: runner})
}

// TestStaticAndSPARouting 核对页面、静态资源、SPA fallback 与 API 路由互不干扰。
func TestStaticAndSPARouting(t *testing.T) {
	server := webTestServer(t, "web-test-token", &fakeRunner{})

	index := doRequest(t, server.Client(), http.MethodGet, server.URL+"/", "", "", "")
	if index.status != http.StatusOK {
		t.Fatalf("GET / 状态码 = %d，期望 200（body=%s）", index.status, index.body)
	}
	if got := index.header.Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("GET / Content-Type = %q，期望 text/html", got)
	}
	if strings.TrimSpace(string(index.body)) != strings.TrimSpace(testIndexHTML) {
		t.Fatalf("GET / 返回内容不是构建产物：%s", index.body)
	}
	if cache := index.header.Get("Cache-Control"); !strings.Contains(cache, "no-cache") {
		t.Fatalf("index.html 的 Cache-Control = %q，期望需要重新校验", cache)
	}
	if csp := index.header.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("缺少安全响应头 CSP：%q", csp)
	}
	if got := index.header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q，期望 nosniff", got)
	}

	script := doRequest(t, server.Client(), http.MethodGet, server.URL+"/assets/app-abc123.js", "", "", "")
	if script.status != http.StatusOK {
		t.Fatalf("GET 静态 JS 状态码 = %d，期望 200", script.status)
	}
	if got := script.header.Get("Content-Type"); !strings.HasPrefix(got, "text/javascript") {
		t.Fatalf("JS Content-Type = %q，期望 text/javascript", got)
	}
	if cache := script.header.Get("Cache-Control"); !strings.Contains(cache, "immutable") {
		t.Fatalf("哈希资源 Cache-Control = %q，期望 immutable", cache)
	}

	style := doRequest(t, server.Client(), http.MethodGet, server.URL+"/assets/app-abc123.css", "", "", "")
	if got := style.header.Get("Content-Type"); !strings.HasPrefix(got, "text/css") {
		t.Fatalf("CSS Content-Type = %q，期望 text/css", got)
	}

	icon := doRequest(t, server.Client(), http.MethodGet, server.URL+"/favicon.svg", "", "", "")
	if got := icon.header.Get("Content-Type"); got != "image/svg+xml" {
		t.Fatalf("SVG Content-Type = %q，期望 image/svg+xml", got)
	}

	// 页面路由刷新：无扩展名的未知路径回落 index.html。
	spa := doRequest(t, server.Client(), http.MethodGet, server.URL+"/chats/abc123", "", "", "")
	if spa.status != http.StatusOK || !strings.HasPrefix(spa.header.Get("Content-Type"), "text/html") {
		t.Fatalf("SPA 路由状态码 = %d，Content-Type = %q，期望 200 text/html", spa.status, spa.header.Get("Content-Type"))
	}

	// 未知 API 路径必须是 JSON 404，不能被 SPA fallback 变成 HTML。
	for _, target := range []string{"/v1", "/v1/unknown", "/api", "/api/unknown", "/api/v1/unknown"} {
		result := doRequest(t, server.Client(), http.MethodGet, server.URL+target, "", "", "")
		if strings.HasPrefix(result.header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s 返回了 HTML，API 路径被 SPA fallback 截获", target)
		}
		decodeError(t, result, http.StatusNotFound, httpapi.CodeNotFound)
	}

	// 缺失的哈希资源保持 404，不返回首页内容。
	missing := doRequest(t, server.Client(), http.MethodGet, server.URL+"/assets/old-zzz999.js", "", "", "")
	decodeError(t, missing, http.StatusNotFound, httpapi.CodeNotFound)

	// 路径穿越与隐藏文件不可读。
	for _, target := range []string{"/.env", "/%2e%2e%2f.env", "/../go.mod", "/assets/../../go.mod"} {
		result := doRequest(t, server.Client(), http.MethodGet, server.URL+target, "", "", "")
		if result.status != http.StatusNotFound {
			t.Fatalf("%s 状态码 = %d，期望 404（body=%s）", target, result.status, result.body)
		}
	}

	// 静态路径只接受 GET/HEAD。
	created := doRequest(t, server.Client(), http.MethodPost, server.URL+"/", "application/json", "{}", "")
	decodeError(t, created, http.StatusMethodNotAllowed, httpapi.CodeMethodNotAllowed)
}

// TestBrowserAPIInjectsServerCredential 核对同源 /api 复用 API 处理器并在服务端注入凭证。
func TestBrowserAPIInjectsServerCredential(t *testing.T) {
	manager := newManager()
	runner := &fakeRunner{outcome: httpapi.RunOutcome{
		Status:    core.RunStatusCompleted,
		Reply:     "同源代理回答",
		LLMCalls:  1,
		ToolCalls: 0,
	}}
	cfg := testConfig("browser-test-token")
	cfg.WebDir = newWebDir(t)
	server := newTestServer(t, cfg, httpapi.Deps{Sessions: manager, Runner: runner})

	// 浏览器不持有凭证：/api 入口由服务端注入。
	created := doRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/sessions", "application/json", "{}", "")
	if created.status != http.StatusCreated {
		t.Fatalf("POST /api/v1/sessions 状态码 = %d，期望 201（body=%s）", created.status, created.body)
	}
	var session sessionPayload
	if err := json.Unmarshal(created.body, &session); err != nil {
		t.Fatalf("创建会话响应不是合法 JSON：%v", err)
	}

	chat := doRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/chat", "application/json",
		`{"session_id":"`+session.SessionID+`","message":"你好"}`, "")
	if chat.status != http.StatusOK {
		t.Fatalf("POST /api/v1/chat 状态码 = %d，期望 200（body=%s）", chat.status, chat.body)
	}
	var payload chatPayload
	if err := json.Unmarshal(chat.body, &payload); err != nil {
		t.Fatalf("chat 响应不是合法 JSON：%v", err)
	}
	if payload.Reply != "同源代理回答" || payload.SessionID != session.SessionID {
		t.Fatalf("chat 响应不符合契约：%+v", payload)
	}

	health := doRequest(t, server.Client(), http.MethodGet, server.URL+"/api/healthz", "", "", "")
	if health.status != http.StatusOK || !strings.Contains(string(health.body), `"ok"`) {
		t.Fatalf("GET /api/healthz 状态码 = %d，body = %s", health.status, health.body)
	}

	// 浏览器自带的 Authorization 被覆盖：伪造凭证不影响同源调用，也不会泄漏服务端凭证。
	spoofed := doRequest(t, server.Client(), http.MethodPost, server.URL+"/api/v1/sessions", "application/json", "{}", "forged-browser-token")
	if spoofed.status != http.StatusCreated {
		t.Fatalf("伪造凭证的同源调用状态码 = %d，期望 201（body=%s）", spoofed.status, spoofed.body)
	}
	for name, values := range spoofed.header {
		for _, value := range values {
			if strings.Contains(value, "browser-test-token") {
				t.Fatalf("响应头 %s 中出现了服务端凭证", name)
			}
		}
	}
	if strings.Contains(string(spoofed.body), "browser-test-token") {
		t.Fatal("响应体出现了服务端凭证")
	}

	// /v1 仍然要求 Bearer 凭证：浏览器入口没有放宽原有认证。
	unauthorized := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "{}", "")
	decodeError(t, unauthorized, http.StatusUnauthorized, httpapi.CodeUnauthorized)
	authorized := doRequest(t, server.Client(), http.MethodPost, server.URL+"/v1/sessions", "application/json", "{}", "browser-test-token")
	if authorized.status != http.StatusCreated {
		t.Fatalf("带凭证的 /v1 调用状态码 = %d，期望 201（body=%s）", authorized.status, authorized.body)
	}
}

// TestBrowserAPIRejectsCrossSiteWrite 核对跨站写入在到达 Runner 之前被拒绝。
func TestBrowserAPIRejectsCrossSiteWrite(t *testing.T) {
	runner := &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted, Reply: "ok"}}
	server := webTestServer(t, "browser-test-token", runner)

	crossOrigin := func(origin, fetchSite string) int {
		request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/chat", strings.NewReader(`{"session_id":"x","message":"hi"}`))
		if err != nil {
			t.Fatalf("构造请求失败：%v", err)
		}
		request.Header.Set("Content-Type", "application/json")
		if origin != "" {
			request.Header.Set("Origin", origin)
		}
		if fetchSite != "" {
			request.Header.Set("Sec-Fetch-Site", fetchSite)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatalf("发送请求失败：%v", err)
		}
		defer response.Body.Close()
		return response.StatusCode
	}

	// 跨站 Origin（无 Fetch Metadata 的老浏览器）与显式 cross-site 都被拒绝。
	if status := crossOrigin("https://unrelated.example", ""); status != http.StatusForbidden {
		t.Fatalf("跨站 Origin 状态码 = %d，期望 403", status)
	}
	if status := crossOrigin("", "cross-site"); status != http.StatusForbidden {
		t.Fatalf("cross-site 状态码 = %d，期望 403", status)
	}
	if runner.callCount() != 0 {
		t.Fatalf("跨站请求触达了 Runner：%d 次", runner.callCount())
	}

	// 同源（含云开发端口代理改写 Host 的场景）放行，由 API 自身判定会话不存在。
	if status := crossOrigin(server.URL, "same-origin"); status != http.StatusNotFound {
		t.Fatalf("同源请求状态码 = %d，期望 404（会话不存在）", status)
	}

	// 错误方法在代理入口即被拒绝。
	get := doRequest(t, server.Client(), http.MethodGet, server.URL+"/api/v1/chat", "", "", "")
	decodeError(t, get, http.StatusMethodNotAllowed, httpapi.CodeMethodNotAllowed)
}

// TestWebDirValidation 核对静态目录配置错误在启动时明确失败。
func TestWebDirValidation(t *testing.T) {
	deps := httpapi.Deps{Sessions: newManager(), Runner: &fakeRunner{}}

	missing := testConfig("")
	missing.WebDir = filepath.Join(t.TempDir(), "absent")
	if _, err := httpapi.New(missing, deps); err == nil {
		t.Fatal("静态目录不存在时构造服务应当失败")
	} else if !strings.Contains(err.Error(), "前端静态目录") {
		t.Fatalf("错误信息未指明静态目录：%v", err)
	}

	incomplete := testConfig("")
	incomplete.WebDir = t.TempDir()
	if _, err := httpapi.New(incomplete, deps); err == nil {
		t.Fatal("缺少 index.html 时构造服务应当失败")
	}

	// 未配置静态目录：只提供 API，页面路径返回 JSON 404。
	server := newTestServer(t, testConfig(""), deps)
	page := doRequest(t, server.Client(), http.MethodGet, server.URL+"/", "", "", "")
	decodeError(t, page, http.StatusNotFound, httpapi.CodeNotFound)
	browser := doRequest(t, server.Client(), http.MethodGet, server.URL+"/api/healthz", "", "", "")
	decodeError(t, browser, http.StatusNotFound, httpapi.CodeNotFound)
}
