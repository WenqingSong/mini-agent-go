// Command mini-agent 是 Agent Runtime 的可启动 HTTP 入口。
//
// 它只负责装配：环境变量 → 配置（fail-closed 校验）→ Provider / 工具注册表构造器 /
// 内存 Session Manager / 上下文管理器 / 日志 Sink → 监听 → 终止信号 → 优雅关闭。
// 业务逻辑与协议定义分别在 service 与 httpapi 中，本命令不承载任何业务规则。
//
// 工具清单本身由 service.NewToolset 决定（calculator / mock_search / todo_*），
// 这里只把构造器交给装配层，避免入口与运行时各自维护一份工具列表。
//
// 配置只从环境变量读取（不进入命令行参数）；凭证不硬编码、不打印、不写入错误信息。
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm/openai"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/service"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// 环境变量名：与 docs/design/http-api.md §8 的配置契约一致。
const (
	envListenAddr = "MINI_AGENT_LISTEN_ADDR"
	envAuthToken  = "MINI_AGENT_AUTH_TOKEN"
	// 前端静态产物目录：配置后同一个进程同时提供页面与 /api、/v1；未配置时只提供 API。
	envWebDir          = "MINI_AGENT_WEB_DIR"
	envLLMBaseURL      = "MINI_AGENT_LLM_BASE_URL"
	envLLMAPIKey       = "MINI_AGENT_LLM_API_KEY"
	envModel           = "MINI_AGENT_MODEL"
	envSystemPrompt    = "MINI_AGENT_SYSTEM_PROMPT"
	envRequestTimeout  = "MINI_AGENT_REQUEST_TIMEOUT"
	envShutdownGrace   = "MINI_AGENT_SHUTDOWN_GRACE"
	envMaxRequestBytes = "MINI_AGENT_MAX_REQUEST_BYTES"
	envMaxLLMCalls     = "MINI_AGENT_MAX_LLM_CALLS"
	envMaxToolCalls    = "MINI_AGENT_MAX_TOOL_CALLS"
	envToolTimeout     = "MINI_AGENT_TOOL_TIMEOUT"
	// 上下文压缩：预算 0 或未配置表示禁用（负值视为配置错误）；
	// KeepRecent 0 或未配置时使用 service.DefaultKeepRecent。
	envContextTokenBudget = "MINI_AGENT_CONTEXT_TOKEN_BUDGET"
	envContextKeepRecent  = "MINI_AGENT_CONTEXT_KEEP_RECENT"
)

// toolTimeoutDisabled 表示不设工具级超时：agent.Config.ToolTimeout <= 0 即不设超时。
const toolTimeoutDisabled = -1

// readyTimeout 是监听就绪的最长等待时间。
const readyTimeout = 5 * time.Second

// options 是可注入的运行参数，生产路径使用默认值，测试注入 getenv / signals / onReady。
type options struct {
	// getenv 读取环境变量；nil 时使用 os.Getenv。
	getenv func(string) string
	// stderr 接收运行日志与启动错误；nil 时丢弃。
	stderr io.Writer
	// onReady 在监听成功后收到实际监听地址。
	onReady func(addr string)
	// signals 是终止信号来源；nil 时监听 SIGINT / SIGTERM。
	signals <-chan os.Signal
}

func main() {
	opts := options{getenv: os.Getenv, stderr: os.Stderr}
	if err := run(context.Background(), opts); err != nil {
		fmt.Fprintf(os.Stderr, "mini-agent: %v\n", err)
		os.Exit(1)
	}
}

// run 装配并运行服务，直到收到终止信号、ctx 结束或服务自身退出。
//
// 配置非法、依赖构造失败或监听失败时返回错误且不进入服务状态（fail-closed）。
func run(ctx context.Context, opts options) error {
	getenv := opts.getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	logWriter := opts.stderr
	if logWriter == nil {
		logWriter = io.Discard
	}

	httpConfig, serviceConfig, providerConfig, err := loadConfig(getenv)
	if err != nil {
		return err
	}

	provider, err := openai.NewClient(providerConfig)
	if err != nil {
		return fmt.Errorf("mini-agent: 构造 LLM Provider 失败: %w", err)
	}
	sessions := session.NewManager(session.ManagerConfig{})
	runner, err := service.NewRunner(service.Deps{
		Provider: provider,
		Sessions: sessions,
		// 工具注册表按 Run 构造：Todo 工具必须绑定本次 Run 的 Session Turn。
		Tools: service.NewToolset,
		// 运行事件只经 runlog.Sink 记录；Sink 失败不影响响应与 Run 结论。
		Sink: runlog.NewJSONSink(logWriter),
	}, serviceConfig)
	if err != nil {
		return err
	}
	server, err := httpapi.New(httpConfig, httpapi.Deps{Sessions: sessions, Runner: runner})
	if err != nil {
		return err
	}

	served := make(chan error, 1)
	go func() { served <- server.Serve() }()

	addr, err := waitReady(ctx, server, served)
	if err != nil {
		_ = server.Close()
		return err
	}
	if opts.onReady != nil {
		opts.onReady(addr)
	}

	signals := opts.signals
	if signals == nil {
		channel := make(chan os.Signal, 2)
		signal.Notify(channel, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(channel)
		signals = channel
	}

	select {
	case <-ctx.Done():
	case <-signals:
	case err := <-served:
		if err != nil {
			return err
		}
		return nil
	}

	// 关闭期间再次收到终止信号：强制关闭（幂等、不 panic），避免进程被永久卡住。
	stopped := make(chan struct{})
	defer close(stopped)
	go func() {
		for {
			select {
			case <-signals:
				_ = server.Close()
			case <-stopped:
				return
			}
		}
	}()

	graceCtx, cancel := context.WithTimeout(context.Background(), httpConfig.ShutdownGrace)
	defer cancel()

	shutdownErr := server.Shutdown(graceCtx)
	// 等待 Serve 返回，保证进程退出前没有遗留的监听或请求处理协程。
	if serveErr := <-served; serveErr != nil && shutdownErr == nil {
		shutdownErr = serveErr
	}
	return shutdownErr
}

// waitReady 等待监听就绪并返回实际地址；服务提前退出、ctx 结束或超时都返回错误。
func waitReady(ctx context.Context, server *httpapi.Server, served <-chan error) (string, error) {
	timeout := time.NewTimer(readyTimeout)
	defer timeout.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()

	for {
		if addr := server.Addr(); addr != "" {
			return addr, nil
		}
		select {
		case err := <-served:
			if err == nil {
				return "", errors.New("mini-agent: 服务在就绪前退出")
			}
			return "", err
		case <-ctx.Done():
			return "", ctx.Err()
		case <-timeout.C:
			return "", errors.New("mini-agent: 服务未在预期时间内进入监听")
		case <-ticker.C:
		}
	}
}

// loadConfig 把环境变量转换为三份配置；缺失必填项或取值非法时返回错误。
//
// 只从环境变量读取：凭证不进入命令行参数，也不会被写进任何错误文本。
func loadConfig(getenv func(string) string) (httpapi.Config, service.Config, openai.Config, error) {
	httpConfig := httpapi.DefaultConfig()
	if value := strings.TrimSpace(getenv(envListenAddr)); value != "" {
		httpConfig.ListenAddr = value
	}
	httpConfig.AuthToken = getenv(envAuthToken)
	httpConfig.WebDir = strings.TrimSpace(getenv(envWebDir))

	var err error
	if httpConfig.RequestTimeout, err = positiveDurationEnv(getenv, envRequestTimeout, httpConfig.RequestTimeout); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	if httpConfig.ShutdownGrace, err = positiveDurationEnv(getenv, envShutdownGrace, httpConfig.ShutdownGrace); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	if httpConfig.MaxRequestBytes, err = positiveInt64Env(getenv, envMaxRequestBytes, httpConfig.MaxRequestBytes); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}

	serviceConfig := service.Config{
		MaxLLMCalls:  service.DefaultMaxLLMCalls,
		MaxToolCalls: service.DefaultMaxToolCalls,
		SystemPrompt: getenv(envSystemPrompt),
		Model:        strings.TrimSpace(getenv(envModel)),
		// 上下文压缩默认关闭：未配置预算时行为与旧版本一致（不压缩、无限积累历史）。
		TokenBudget: 0,
		KeepRecent:  service.DefaultKeepRecent,
		ToolTimeout: service.DefaultToolTimeout,
	}
	if serviceConfig.MaxLLMCalls, err = positiveIntEnv(getenv, envMaxLLMCalls, serviceConfig.MaxLLMCalls); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	if serviceConfig.MaxToolCalls, err = positiveIntEnv(getenv, envMaxToolCalls, serviceConfig.MaxToolCalls); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	// 0 表示禁用压缩；负数属于配置错误（显式拒绝，不做静默降级）。
	if serviceConfig.TokenBudget, err = nonNegativeIntEnv(getenv, envContextTokenBudget, serviceConfig.TokenBudget); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	if serviceConfig.KeepRecent, err = nonNegativeIntEnv(getenv, envContextKeepRecent, serviceConfig.KeepRecent); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	toolTimeout, err := durationEnv(getenv, envToolTimeout, serviceConfig.ToolTimeout)
	if err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}
	if toolTimeout <= 0 {
		// 契约：环境变量取值 <= 0 表示不设工具级超时。
		serviceConfig.ToolTimeout = toolTimeoutDisabled
	} else {
		serviceConfig.ToolTimeout = toolTimeout
	}

	baseURL := strings.TrimSpace(getenv(envLLMBaseURL))
	if baseURL == "" {
		return httpapi.Config{}, service.Config{}, openai.Config{}, fmt.Errorf("mini-agent: 环境变量 %s 必填", envLLMBaseURL)
	}
	if serviceConfig.Model == "" {
		return httpapi.Config{}, service.Config{}, openai.Config{}, fmt.Errorf("mini-agent: 环境变量 %s 必填", envModel)
	}
	if err := httpConfig.Validate(); err != nil {
		return httpapi.Config{}, service.Config{}, openai.Config{}, err
	}

	providerConfig := openai.Config{
		BaseURL: baseURL,
		// LLM API Key 可空：为空时 openai 不发送 Authorization；与 HTTP Bearer 凭证相互独立。
		APIKey: getenv(envLLMAPIKey),
	}
	return httpConfig, serviceConfig, providerConfig, nil
}

// durationEnv 读取时长型环境变量；未配置时返回 fallback。
func durationEnv(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 不是合法时长（示例 60s）", name)
	}
	return value, nil
}

// intEnv 读取整型环境变量；未配置时返回 fallback。
func intEnv(getenv func(string) string, name string, fallback int) (int, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 不是合法整数", name)
	}
	return value, nil
}

// int64Env 读取 64 位整型环境变量；未配置时返回 fallback。
func int64Env(getenv func(string) string, name string, fallback int64) (int64, error) {
	raw := strings.TrimSpace(getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 不是合法整数", name)
	}
	return value, nil
}

// positiveDurationEnv 读取必须为正的时长型环境变量；显式配置非正值视为配置非法。
func positiveDurationEnv(getenv func(string) string, name string, fallback time.Duration) (time.Duration, error) {
	value, err := durationEnv(getenv, name, fallback)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(getenv(name)) != "" && value <= 0 {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 必须为正数", name)
	}
	return value, nil
}

// positiveIntEnv 读取必须为正的整型环境变量；显式配置非正值视为配置非法。
func positiveIntEnv(getenv func(string) string, name string, fallback int) (int, error) {
	value, err := intEnv(getenv, name, fallback)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(getenv(name)) != "" && value <= 0 {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 必须为正整数", name)
	}
	return value, nil
}

// nonNegativeIntEnv 读取必须非负的整型环境变量；显式配置负值视为配置非法，
// 未配置时返回 fallback。用于「0 有明确语义（禁用）」的开关型预算。
func nonNegativeIntEnv(getenv func(string) string, name string, fallback int) (int, error) {
	value, err := intEnv(getenv, name, fallback)
	if err != nil {
		return 0, err
	}
	if value < 0 {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 必须为非负整数", name)
	}
	return value, nil
}

// positiveInt64Env 读取必须为正的 64 位整型环境变量；显式配置非正值视为配置非法。
func positiveInt64Env(getenv func(string) string, name string, fallback int64) (int64, error) {
	value, err := int64Env(getenv, name, fallback)
	if err != nil {
		return 0, err
	}
	if strings.TrimSpace(getenv(name)) != "" && value <= 0 {
		return 0, fmt.Errorf("mini-agent: 环境变量 %s 必须为正整数", name)
	}
	return value, nil
}
