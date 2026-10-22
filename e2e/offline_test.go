package e2e_test

// 本文件覆盖 AC-005：以机械证据证明整轮验收零外网、零真实凭据、零真实 LLM Token：
//  1. 源码扫描：e2e 目录只有 *_test.go、无真实 Provider 域名、无凭据字面量、
//     无真实凭据环境变量引用、无跳过标记或构建 tag、无任何非 loopback URL 字面量；
//  2. 运行期：唯一模型交互点是 loopback 脚本服务，Host 一致、无 Authorization 头、
//     实际请求数等于脚本步数。

import (
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
)

// expectedE2EFiles 是本套件的全部文件（全部仅测试）：原 6 个验收文件 +
// Todo 端到端验收文件（正式装配接入 Todo 后新增）。
var expectedE2EFiles = []string{
	"harness_test.go",
	"chat_test.go",
	"failure_test.go",
	"timeout_test.go",
	"concurrency_test.go",
	"offline_test.go",
	"todo_test.go",
}

// TestNoRealProviderOrCredential 是 AC-005 的机械证据入口。
func TestNoRealProviderOrCredential(t *testing.T) {
	t.Run("源码无真实Provider域名与凭据", func(t *testing.T) {
		scanOfflineSources(t)
	})
	t.Run("运行期只访问loopback且无Authorization", func(t *testing.T) {
		h := newHarness(t, harnessOptions{script: []llmStep{echoStep()}})
		sessionID := h.createSession()
		response := h.chatOK(sessionID, "离线验收请求")
		if response.Reply != "reply:离线验收请求" {
			t.Fatalf("reply = %q，期望由 loopback 脚本服务生成的回复", response.Reply)
		}

		// 请求次数与脚本预期严格一致：脚本服务是唯一模型交互点。
		h.llm.assertScriptConsumed(t)
		records := h.llm.requestRecords()
		if len(records) != 1 {
			t.Fatalf("模型请求次数 = %d，期望 1", len(records))
		}

		scriptedURL, err := url.Parse(h.llm.server.URL)
		if err != nil {
			t.Fatalf("脚本服务地址不可解析：%v", err)
		}
		if !isLoopbackHost(scriptedURL.Hostname()) {
			t.Fatalf("脚本服务不是 loopback：%s", h.llm.server.URL)
		}
		record := records[0]
		if record.Host != scriptedURL.Host {
			t.Fatalf("模型请求 Host = %q，期望脚本服务 Host %q（存在真实 Provider 访问）", record.Host, scriptedURL.Host)
		}
		if record.Path != "/v1/chat/completions" {
			t.Fatalf("模型请求路径 = %q，期望 /v1/chat/completions", record.Path)
		}
		if record.AuthorizationHeader != "" {
			t.Fatalf("模型请求携带了 Authorization 头：%q", record.AuthorizationHeader)
		}

		apiURL, err := url.Parse(h.server.URL)
		if err != nil {
			t.Fatalf("HTTP 服务地址不可解析：%v", err)
		}
		if !isLoopbackHost(apiURL.Hostname()) {
			t.Fatalf("HTTP 服务不是 loopback：%s", h.server.URL)
		}

		h.assertRunCount(1)
	})
}

// scanOfflineSources 扫描 e2e 目录全部测试源码。
func scanOfflineSources(t *testing.T) {
	t.Helper()

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取 e2e 目录失败：%v", err)
	}
	files := make(map[string]bool, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("e2e 目录不允许子目录：%s", entry.Name())
		}
		if !strings.HasSuffix(entry.Name(), "_test.go") {
			t.Fatalf("e2e 目录只允许 *_test.go（仅测试包）：%s", entry.Name())
		}
		files[entry.Name()] = true
	}
	for _, want := range expectedE2EFiles {
		if !files[want] {
			t.Fatalf("缺少 Design §12 登记的验收文件：%s", want)
		}
	}

	// 真实 Provider 域名按片段拼接，避免扫描器把本文件自身的模式当作命中。
	realHosts := []string{
		"api." + "openai" + ".com",
		"api." + "anthropic" + ".com",
		"generativelanguage." + "googleapis" + ".com",
		"openai." + "azure" + ".com",
		"api." + "deepseek" + ".com",
		"dashscope." + "aliyuncs" + ".com",
	}
	// 真实凭据环境变量与「弱化测试」标记同样按片段拼接。
	envPrefix := "MINI_AGENT_" + "LLM_"
	credentialPrefix := "s" + "k-"
	skipMarker := "t." + "Skip"
	shortMarker := "testing." + "Short("
	buildTag := "//go:" + "build"
	urlPattern := regexp.MustCompile("htt" + "ps?://[^\\s\"'`)]+")

	for name := range files {
		payload, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读取 %s 失败：%v", name, err)
		}
		source := string(payload)
		lower := strings.ToLower(source)

		if !strings.Contains(source, "package e2e_test") {
			t.Fatalf("%s 不是 package e2e_test", name)
		}
		for _, host := range realHosts {
			if strings.Contains(lower, host) {
				t.Fatalf("%s 含真实 Provider 域名 %q", name, host)
			}
		}
		for _, marker := range []string{envPrefix, credentialPrefix, skipMarker, shortMarker, buildTag} {
			if strings.Contains(source, marker) {
				t.Fatalf("%s 含禁止标记 %q", name, marker)
			}
		}
		for _, raw := range urlPattern.FindAllString(source, -1) {
			parsed, err := url.Parse(raw)
			if err != nil {
				t.Fatalf("%s 含不可解析的 URL 字面量 %q：%v", name, raw, err)
			}
			if !isLoopbackHost(parsed.Hostname()) {
				t.Fatalf("%s 含非 loopback URL 字面量 %q（可能指向真实服务）", name, raw)
			}
		}
	}
}

// isLoopbackHost 判断主机是否为回环地址；无法证明是回环时一律按非回环处理。
func isLoopbackHost(host string) bool {
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
