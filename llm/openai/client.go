// Package openai 提供 OpenAI 兼容 Chat Completions 的 llm.Provider 实现。
//
// 设计约束（见 docs/design/runtime-architecture.md）：
//   - 只依赖标准库与 core、llm，不引入官方 SDK 或第三方依赖；
//   - 每次 Complete 只发出一次 HTTP 请求，不做自动重试（429 / 5xx 也不重发）；
//   - 失败统一用 core.RunError 表达：HTTP 非 2xx、协议错误与传输失败为 provider_failure，
//     ctx 取消为 cancelled，ctx 超时为 deadline_exceeded；
//   - 端点固定为 <BaseURL>/chat/completions，BaseURL 可自带 /v1 前缀与结尾斜杠。
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
)

const (
	// opNewClient / opComplete 是错误 Op，定位到具体调用点。
	opNewClient = "openai.NewClient"
	opComplete  = "openai.Client.Complete"

	// chatPath 是 Chat Completions 的相对路径，与 BaseURL 拼接。
	chatPath = "/chat/completions"

	// errorBodyLimit 限制错误文本中服务端响应体的字符数，避免把整段载荷写进错误。
	errorBodyLimit = 256
)

// Config 是 Client 的可配置项。
type Config struct {
	// BaseURL 是兼容服务根地址，例如 https://api.example.com/v1；必填。
	BaseURL string
	// APIKey 非空时以 Authorization: Bearer <key> 发送；为空时不发送该头。
	APIKey string
	// HTTPClient 非空时直接使用（此时 Timeout 不生效）；为空时按 Timeout 构造独立 Client。
	HTTPClient *http.Client
	// Timeout 是单次请求总超时（作为 http.Client.Timeout 生效），超时映射为 deadline_exceeded；
	// 0 表示不额外设超时，只受 ctx 控制。
	Timeout time.Duration
}

// Client 是 OpenAI 兼容 Chat Completions Provider。构造后即可并发复用。
type Client struct {
	endpoint   string
	apiKey     string
	httpClient *http.Client
}

// NewClient 校验配置并构造 Client。
//
// 配置非法（BaseURL 缺失 / 非法 / 协议不受支持 / 无主机名、Timeout 为负）时
// 返回 invalid_argument，不发出任何请求。
func NewClient(cfg Config) (*Client, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, core.NewError(core.KindInvalidArgument, opNewClient, "BaseURL 不能为空")
	}
	if cfg.Timeout < 0 {
		return nil, core.NewError(core.KindInvalidArgument, opNewClient, fmt.Sprintf("Timeout 不能为负值，当前为 %s", cfg.Timeout))
	}

	parsed, err := url.Parse(base)
	if err != nil {
		return nil, &core.RunError{
			Kind:    core.KindInvalidArgument,
			Op:      opNewClient,
			Message: "BaseURL 不是合法 URL",
			Cause:   err,
		}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, core.NewError(core.KindInvalidArgument, opNewClient, fmt.Sprintf("BaseURL 必须以 http:// 或 https:// 开头，当前为 %q", parsed.Scheme))
	}
	if parsed.Host == "" {
		return nil, core.NewError(core.KindInvalidArgument, opNewClient, "BaseURL 缺少主机名")
	}

	// 端点固定为 <BaseURL>/chat/completions：只规范化结尾斜杠，保留 BaseURL 自带的前缀（如 /v1）。
	parsed.Path = strings.TrimRight(parsed.Path, "/") + chatPath
	parsed.RawQuery = ""
	parsed.Fragment = ""

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: cfg.Timeout}
	}

	return &Client{
		endpoint:   parsed.String(),
		apiKey:     cfg.APIKey,
		httpClient: httpClient,
	}, nil
}

// Complete 发出一次 Chat Completions 请求并解析响应。
//
// 失败语义：调用前 / 调用中 ctx 取消 → cancelled；ctx 超时或 HTTPClient 超时 → deadline_exceeded；
// HTTP 非 2xx、协议错误、传输失败 → provider_failure（传输失败保留 Cause）。
// 任何失败都不返回半成品 Response。
func (c *Client) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return llm.Response{}, contextRunError(opComplete, err)
	}
	if req.Model == "" {
		return llm.Response{}, core.NewError(core.KindInvalidArgument, opComplete, "Model 不能为空")
	}

	payload, err := buildChatRequest(req)
	if err != nil {
		return llm.Response{}, err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return llm.Response{}, &core.RunError{
			Kind:    core.KindInvalidArgument,
			Op:      opComplete,
			Message: "请求体无法序列化为 JSON",
			Cause:   err,
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return llm.Response{}, &core.RunError{
			Kind:    core.KindInvalidArgument,
			Op:      opComplete,
			Message: "构造 HTTP 请求失败",
			Cause:   err,
		}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		// 先看 ctx：取消 / 超时优先归因为调用方控制，而不是传输故障。
		if ctxErr := ctx.Err(); ctxErr != nil {
			return llm.Response{}, contextRunError(opComplete, ctxErr)
		}
		// HTTPClient.Timeout 触发时错误是 *url.Error 包裹的超时，ctx 本身没有到期；
		// 两者语义都是"整体超时"，统一归为 deadline_exceeded。
		var timeoutErr net.Error
		if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
			return llm.Response{}, &core.RunError{
				Kind:    core.KindDeadlineExceeded,
				Op:      opComplete,
				Message: "调用已超时",
				Cause:   err,
			}
		}
		return llm.Response{}, &core.RunError{
			Kind:    core.KindProviderFailure,
			Op:      opComplete,
			Message: "发送 Chat Completions 请求失败",
			Cause:   err,
		}
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return llm.Response{}, contextRunError(opComplete, ctxErr)
		}
		return llm.Response{}, &core.RunError{
			Kind:    core.KindProviderFailure,
			Op:      opComplete,
			Message: fmt.Sprintf("读取响应体失败（HTTP %d）", resp.StatusCode),
			Cause:   err,
		}
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return llm.Response{}, &core.RunError{
			Kind:    core.KindProviderFailure,
			Op:      opComplete,
			Message: fmt.Sprintf("服务端返回 HTTP %d %s%s", resp.StatusCode, http.StatusText(resp.StatusCode), bodyExcerpt(raw)),
		}
	}
	if len(raw) == 0 {
		return llm.Response{}, &core.RunError{
			Kind:    core.KindProviderFailure,
			Op:      opComplete,
			Message: "响应体为空",
		}
	}

	return parseChatResponse(raw)
}

// contextRunError 把 ctx 错误映射为契约错误种类：取消 → cancelled，超时 → deadline_exceeded。
func contextRunError(op string, ctxErr error) error {
	if errors.Is(ctxErr, context.Canceled) {
		return &core.RunError{
			Kind:    core.KindCancelled,
			Op:      op,
			Message: "调用已被取消",
			Cause:   ctxErr,
		}
	}
	return &core.RunError{
		Kind:    core.KindDeadlineExceeded,
		Op:      op,
		Message: "调用已超时",
		Cause:   ctxErr,
	}
}

// bodyExcerpt 把服务端响应体压缩成错误文本里的短摘要；空体返回空串。
func bodyExcerpt(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	runes := []rune(text)
	if len(runes) > errorBodyLimit {
		text = string(runes[:errorBodyLimit]) + "…"
	}
	return ": " + text
}
