package httpapi_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/httpapi"
)

// fakeSecret 是测试注入的假凭证：任何输出与响应中都不得出现。
const fakeSecret = "fake-secret-token-value"

// TestConfigDefaults 核对默认监听面与默认取值。
func TestConfigDefaults(t *testing.T) {
	cfg := httpapi.DefaultConfig()
	if cfg.ListenAddr != "127.0.0.1:8080" {
		t.Fatalf("默认监听地址 = %q，期望 127.0.0.1:8080", cfg.ListenAddr)
	}
	if cfg.RequestTimeout != 60*time.Second {
		t.Fatalf("默认请求超时 = %s，期望 60s", cfg.RequestTimeout)
	}
	if cfg.ShutdownGrace != 10*time.Second {
		t.Fatalf("默认宽限期 = %s，期望 10s", cfg.ShutdownGrace)
	}
	if cfg.MaxRequestBytes != 1<<20 {
		t.Fatalf("默认请求体上限 = %d，期望 1MiB", cfg.MaxRequestBytes)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("默认配置应合法，实际错误：%v", err)
	}

	// 零值配置按默认值填充后同样合法。
	if err := (httpapi.Config{}).Validate(); err != nil {
		t.Fatalf("零值配置应按默认值合法，实际错误：%v", err)
	}
}

// TestConfigLoopbackBoundary 核对默认回环监听与非回环必须认证的边界。
func TestConfigLoopbackBoundary(t *testing.T) {
	loopback := []string{"127.0.0.1:0", "[::1]:0", "localhost:0", "127.0.0.5:8081"}
	for _, addr := range loopback {
		cfg := httpapi.Config{ListenAddr: addr}
		if err := cfg.Validate(); err != nil {
			t.Errorf("回环地址 %q 不应要求凭证，实际错误：%v", addr, err)
		}
	}

	public := []string{"0.0.0.0:0", ":0", "192.168.1.10:8080", "example.com:443", "mini-agent.internal:8080"}
	for _, addr := range public {
		cfg := httpapi.Config{ListenAddr: addr}
		if err := cfg.Validate(); err == nil {
			t.Errorf("非回环地址 %q 缺少凭证时应 fail-closed", addr)
		}
		withToken := httpapi.Config{ListenAddr: addr, AuthToken: fakeSecret}
		if err := withToken.Validate(); err != nil {
			t.Errorf("非回环地址 %q 配置凭证后应合法，实际错误：%v", addr, err)
		}
	}
}

// TestConfigRejectsInvalidValues 核对非法值 fail-closed。
func TestConfigRejectsInvalidValues(t *testing.T) {
	cases := []struct {
		name string
		cfg  httpapi.Config
	}{
		{name: "地址缺少端口", cfg: httpapi.Config{ListenAddr: "127.0.0.1"}},
		{name: "端口越界", cfg: httpapi.Config{ListenAddr: "127.0.0.1:70000"}},
		{name: "端口非数字", cfg: httpapi.Config{ListenAddr: "127.0.0.1:abc"}},
		{name: "端口为负", cfg: httpapi.Config{ListenAddr: "127.0.0.1:-1"}},
		{name: "IPv6 缺少方括号", cfg: httpapi.Config{ListenAddr: "::1:8080"}},
		{name: "请求超时为负", cfg: httpapi.Config{ListenAddr: "127.0.0.1:0", RequestTimeout: -time.Second}},
		{name: "宽限期为负", cfg: httpapi.Config{ListenAddr: "127.0.0.1:0", ShutdownGrace: -time.Second}},
		{name: "请求体上限为负", cfg: httpapi.Config{ListenAddr: "127.0.0.1:0", MaxRequestBytes: -1}},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if err := item.cfg.Validate(); err == nil {
				t.Fatal("期望配置校验失败，实际通过")
			}
		})
	}
}

// TestNewFailClosedWithoutListening 核对构造失败时未进入监听。
func TestNewFailClosedWithoutListening(t *testing.T) {
	deps := httpapi.Deps{Sessions: newManager(), Runner: &fakeRunner{}}

	if _, err := httpapi.New(httpapi.Config{ListenAddr: "0.0.0.0:0"}, deps); err == nil {
		t.Fatal("非回环监听缺少凭证时构造应失败")
	}
	if _, err := httpapi.New(httpapi.Config{ListenAddr: "127.0.0.1"}, deps); err == nil {
		t.Fatal("非法监听地址时构造应失败")
	}
	if _, err := httpapi.New(testConfig(""), httpapi.Deps{Runner: &fakeRunner{}}); err == nil {
		t.Fatal("缺少 Sessions 时构造应失败")
	}
	if _, err := httpapi.New(testConfig(""), httpapi.Deps{Sessions: newManager()}); err == nil {
		t.Fatal("缺少 Runner 时构造应失败")
	}

	server, err := httpapi.New(testConfig(""), deps)
	if err != nil {
		t.Fatalf("合法配置构造失败：%v", err)
	}
	if addr := server.Addr(); addr != "" {
		t.Fatalf("构造阶段不得进入监听，实际地址 = %q", addr)
	}
}

// TestConfigStringRedaction 核对 %v / %+v / %#v 与 String()/GoString() 的凭证脱敏。
func TestConfigStringRedaction(t *testing.T) {
	cfg := httpapi.Config{ListenAddr: "0.0.0.0:8080", AuthToken: fakeSecret}

	representations := map[string]string{
		"%v":       fmt.Sprintf("%v", cfg),
		"%+v":      fmt.Sprintf("%+v", cfg),
		"%#v":      fmt.Sprintf("%#v", cfg),
		"String":   cfg.String(),
		"GoString": cfg.GoString(),
		"格式化 #v":   fmt.Sprintf("%#v", &cfg),
	}
	for name, text := range representations {
		if strings.Contains(text, fakeSecret) {
			t.Errorf("%s 输出泄漏明文凭证：%s", name, text)
		}
		if !strings.Contains(text, "<redacted>") {
			t.Errorf("%s 输出应为已配置凭证显示 <redacted>：%s", name, text)
		}
	}

	unset := httpapi.Config{ListenAddr: "127.0.0.1:8080"}
	for _, text := range []string{fmt.Sprintf("%v", unset), fmt.Sprintf("%#v", unset)} {
		if !strings.Contains(text, "<unset>") {
			t.Errorf("未配置凭证应显示 <unset>：%s", text)
		}
	}
}
