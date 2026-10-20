package httpapi

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

// 默认值：默认只监听回环地址，避免无认证接口暴露到公网。
const (
	// DefaultListenAddr 是默认监听地址（仅本机可访问）。
	DefaultListenAddr = "127.0.0.1:8080"
	// DefaultRequestTimeout 是单个请求的服务端超时。
	DefaultRequestTimeout = 60 * time.Second
	// DefaultShutdownGrace 是优雅关闭的宽限期。
	DefaultShutdownGrace = 10 * time.Second
	// DefaultMaxRequestBytes 是请求体读取上限（1 MiB）。
	DefaultMaxRequestBytes int64 = 1 << 20
)

// Config 是 HTTP 对外协议层的配置。
//
// 零值字段在 New / Validate 时按默认值填充；负面或不可解析的取值 fail-closed。
// AuthToken 是敏感字段：任何打印形式（%v / %+v / %#v）都必须脱敏。
type Config struct {
	// ListenAddr 是监听地址，默认 DefaultListenAddr；非回环监听必须配置 AuthToken。
	ListenAddr string
	// AuthToken 是静态 Bearer 凭证；为空表示关闭认证（只允许回环监听）。
	AuthToken string
	// RequestTimeout 是每个请求的服务端超时，默认 DefaultRequestTimeout。
	RequestTimeout time.Duration
	// ShutdownGrace 是优雅关闭宽限期，默认 DefaultShutdownGrace。
	ShutdownGrace time.Duration
	// MaxRequestBytes 是请求体读取上限，默认 DefaultMaxRequestBytes。
	MaxRequestBytes int64
}

// DefaultConfig 返回带默认值的配置。
func DefaultConfig() Config {
	return Config{
		ListenAddr:      DefaultListenAddr,
		RequestTimeout:  DefaultRequestTimeout,
		ShutdownGrace:   DefaultShutdownGrace,
		MaxRequestBytes: DefaultMaxRequestBytes,
	}
}

// withDefaults 用默认值填充零值字段，返回新配置（不修改接收者）。
func (c Config) withDefaults() Config {
	out := c
	if out.ListenAddr == "" {
		out.ListenAddr = DefaultListenAddr
	}
	if out.RequestTimeout == 0 {
		out.RequestTimeout = DefaultRequestTimeout
	}
	if out.ShutdownGrace == 0 {
		out.ShutdownGrace = DefaultShutdownGrace
	}
	if out.MaxRequestBytes == 0 {
		out.MaxRequestBytes = DefaultMaxRequestBytes
	}
	return out
}

// Validate 校验配置是否可用：地址可解析、时长与上限为正、非回环监听必须携带凭证。
//
// 校验不进入监听，也不访问任何外部系统；失败时调用方必须放弃启动（fail-closed）。
func (c Config) Validate() error {
	cfg := c.withDefaults()

	host, err := listenHost(cfg.ListenAddr)
	if err != nil {
		return err
	}
	if cfg.RequestTimeout <= 0 {
		return fmt.Errorf("httpapi: RequestTimeout 必须为正数，当前为 %s", cfg.RequestTimeout)
	}
	if cfg.ShutdownGrace <= 0 {
		return fmt.Errorf("httpapi: ShutdownGrace 必须为正数，当前为 %s", cfg.ShutdownGrace)
	}
	if cfg.MaxRequestBytes <= 0 {
		return fmt.Errorf("httpapi: MaxRequestBytes 必须为正数，当前为 %d", cfg.MaxRequestBytes)
	}
	if !isLoopbackHost(host) && cfg.AuthToken == "" {
		return fmt.Errorf("httpapi: 监听 %q 非回环地址时必须配置认证凭证", cfg.ListenAddr)
	}
	return nil
}

// String 实现脱敏输出：凭证只显示 <redacted> / <unset>。
func (c Config) String() string {
	return c.describe()
}

// GoString 覆盖 %#v，避免 Go 语法形式绕过 String() 泄漏凭证。
func (c Config) GoString() string {
	return c.describe()
}

// describe 生成唯一的脱敏描述。
func (c Config) describe() string {
	cfg := c.withDefaults()
	return fmt.Sprintf(
		"httpapi.Config{listen_addr:%q auth_token:%s request_timeout:%s shutdown_grace:%s max_request_bytes:%d}",
		cfg.ListenAddr, redacted(cfg.AuthToken), cfg.RequestTimeout, cfg.ShutdownGrace, cfg.MaxRequestBytes,
	)
}

// redacted 返回凭证的脱敏表示：已配置时只说明存在，未配置时明确 <unset>。
func redacted(secret string) string {
	if secret == "" {
		return "<unset>"
	}
	return "<redacted>"
}

// listenHost 解析并校验监听地址，返回主机部分：主机可空（表示全部接口），端口必须是 0..65535 的数字。
func listenHost(addr string) (string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("httpapi: 监听地址 %q 不可解析: %w", addr, err)
	}
	number, convErr := strconv.Atoi(port)
	if convErr != nil || number < 0 || number > 65535 {
		return "", fmt.Errorf("httpapi: 监听端口 %q 不是 0..65535 的数字", port)
	}
	return host, nil
}

// isLoopbackHost 判断主机是否回环：127.0.0.0/8、::1 与 localhost。
//
// 空主机表示监听全部接口，不是回环；无法证明是回环的主机名一律按非回环处理（fail-closed）。
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
