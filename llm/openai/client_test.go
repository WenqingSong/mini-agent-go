package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/llm"
)

// testAPIKey 是测试专用假凭据，用于验证鉴权头与错误文本不泄漏凭据。
const testAPIKey = "sk-test-secret-key-do-not-log"

// textPart 构造 text 内容片段。
func textPart(text string) core.ContentPart {
	return core.ContentPart{Type: core.ContentType("text"), Text: text}
}

// minimalRequest 是最小的合法请求：一个 user 消息 + Model。
func minimalRequest() llm.Request {
	return llm.Request{
		ExecutionRef: core.ExecutionRef{SessionID: "session-1", RunID: "run-1"},
		Model:        "test-model",
		Messages: []core.Message{
			{ID: "m1", Role: core.Role("user"), Content: []core.ContentPart{textPart("你好")}},
		},
	}
}

// captured 是服务端实际收到的请求事实。
type captured struct {
	method string
	path   string
	header http.Header
	body   []byte
}

// startCaptureServer 启动返回固定响应体的 httptest 服务，并通过 channel 回传收到的请求。
func startCaptureServer(t *testing.T, status int, body string) (*httptest.Server, <-chan captured) {
	t.Helper()
	requests := make(chan captured, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		select {
		case requests <- captured{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: raw}:
		default:
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// testClient 构造指向 baseURL 的 Client。
func testClient(t *testing.T, baseURL, apiKey string) *Client {
	t.Helper()
	client, err := NewClient(Config{BaseURL: baseURL, APIKey: apiKey})
	if err != nil {
		t.Fatalf("构造 Client 失败：%v", err)
	}
	return client
}

// mustReceive 取服务端收到的第一个请求。
func mustReceive(t *testing.T, requests <-chan captured) captured {
	t.Helper()
	select {
	case got := <-requests:
		return got
	default:
		t.Fatalf("服务端没有收到请求")
		return captured{}
	}
}

// assertNoRequest 断言调用方没有发出任何 HTTP 请求。
func assertNoRequest(t *testing.T, requests <-chan captured) {
	t.Helper()
	select {
	case got := <-requests:
		t.Fatalf("不期望发出请求，但服务端收到了 %s %s", got.method, got.path)
	default:
	}
}

// wireMessage / wireBody 是断言用的独立请求体结构，不复用生产代码的 wire 类型，
// 避免生产结构体字段写错时测试跟着一起错。
type wireMessage struct {
	Role      string `json:"role"`
	Content   string `json:"content"`
	ToolCalls []struct {
		ID       string `json:"id"`
		Type     string `json:"type"`
		Function struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"function"`
	} `json:"tool_calls"`
	ToolCallID string `json:"tool_call_id"`
}

type wireBody struct {
	Model    string        `json:"model"`
	Messages []wireMessage `json:"messages"`
	Tools    []struct {
		Type     string `json:"type"`
		Function struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"function"`
	} `json:"tools"`
	MaxTokens   int      `json:"max_tokens"`
	Temperature *float64 `json:"temperature"`
}

// parseOKText 是标准的文本成功响应。
const parseOKText = `{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"好的"},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`

// fullRequest 是覆盖全部映射分支的请求。
func fullRequest(temperature *float64) llm.Request {
	return llm.Request{
		ExecutionRef: core.ExecutionRef{SessionID: "session-1", RunID: "run-1"},
		Model:        "test-model",
		System:       "你是测试助手",
		Messages: []core.Message{
			{ID: "m1", Role: core.Role("system"), Content: []core.ContentPart{textPart("系统设定")}},
			{ID: "m2", Role: core.Role("user"), Content: []core.ContentPart{textPart("你好，"), textPart("世界")}},
			{
				ID:        "m3",
				Role:      core.Role("assistant"),
				Content:   []core.ContentPart{textPart("我来调用工具")},
				ToolCalls: []core.ToolCall{{ID: "call_1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)}},
			},
			{ID: "m4", Role: core.Role("tool"), ToolResult: &core.ToolResult{CallID: "call_1", Name: "echo", Content: "hi"}},
		},
		Tools: []core.ToolSpec{{
			Name:        "echo",
			Description: "回显文本",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`),
		}},
		MaxTokens:   512,
		Temperature: temperature,
	}
}

// TestCompleteRequestConstruction 覆盖 AC-001：请求方法、路径、鉴权与请求体逐字段映射。
func TestCompleteRequestConstruction(t *testing.T) {
	t.Run("完整请求逐字段映射", func(t *testing.T) {
		srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
		client := testClient(t, srv.URL, testAPIKey)
		temperature := 0.25

		resp, err := client.Complete(context.Background(), fullRequest(&temperature))
		if err != nil {
			t.Fatalf("不应返回错误：%v", err)
		}
		if resp.Message.Role != core.Role("assistant") {
			t.Fatalf("期望响应角色 assistant，实际 %q", resp.Message.Role)
		}

		got := mustReceive(t, requests)
		if got.method != http.MethodPost {
			t.Errorf("期望方法 POST，实际 %s", got.method)
		}
		if got.path != "/chat/completions" {
			t.Errorf("期望路径 /chat/completions，实际 %s", got.path)
		}
		if ct := got.header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("期望 Content-Type application/json，实际 %q", ct)
		}
		if auth := got.header.Get("Authorization"); auth != "Bearer "+testAPIKey {
			t.Errorf("期望 Authorization %q，实际 %q", "Bearer "+testAPIKey, auth)
		}

		var body wireBody
		if err := json.Unmarshal(got.body, &body); err != nil {
			t.Fatalf("请求体不是合法 JSON：%v（原文 %s）", err, got.body)
		}
		if body.Model != "test-model" {
			t.Errorf("model 期望 test-model，实际 %q", body.Model)
		}
		if body.MaxTokens != 512 {
			t.Errorf("max_tokens 期望 512，实际 %d", body.MaxTokens)
		}
		if body.Temperature == nil || *body.Temperature != 0.25 {
			t.Errorf("temperature 期望 0.25，实际 %v", body.Temperature)
		}

		// system prompt 在最前，随后按请求顺序逐条映射。
		wantRoles := []string{"system", "system", "user", "assistant", "tool"}
		if len(body.Messages) != len(wantRoles) {
			t.Fatalf("消息数期望 %d，实际 %d（原文 %s）", len(wantRoles), len(body.Messages), got.body)
		}
		for i, want := range wantRoles {
			if body.Messages[i].Role != want {
				t.Errorf("消息[%d] 角色期望 %s，实际 %s", i, want, body.Messages[i].Role)
			}
		}
		if got, want := body.Messages[0].Content, "你是测试助手"; got != want {
			t.Errorf("system prompt 期望 %q，实际 %q", want, got)
		}
		if got, want := body.Messages[1].Content, "系统设定"; got != want {
			t.Errorf("system 消息内容期望 %q，实际 %q", want, got)
		}
		// 多个 text 片段按顺序拼接。
		if got, want := body.Messages[2].Content, "你好，世界"; got != want {
			t.Errorf("user 消息内容期望 %q，实际 %q", want, got)
		}
		if got, want := body.Messages[3].Content, "我来调用工具"; got != want {
			t.Errorf("assistant 消息内容期望 %q，实际 %q", want, got)
		}
		if calls := body.Messages[3].ToolCalls; len(calls) != 1 {
			t.Fatalf("assistant tool_calls 数期望 1，实际 %d", len(calls))
		} else {
			if calls[0].ID != "call_1" {
				t.Errorf("tool_calls[0].id 期望 call_1，实际 %q", calls[0].ID)
			}
			if calls[0].Type != "function" {
				t.Errorf("tool_calls[0].type 期望 function，实际 %q", calls[0].Type)
			}
			if calls[0].Function.Name != "echo" {
				t.Errorf("tool_calls[0].function.name 期望 echo，实际 %q", calls[0].Function.Name)
			}
			if got, want := calls[0].Function.Arguments, `{"text":"hi"}`; got != want {
				t.Errorf("tool_calls[0].function.arguments 期望原样 %q，实际 %q", want, got)
			}
		}
		if got, want := body.Messages[4].Content, "hi"; got != want {
			t.Errorf("tool 消息内容期望 %q，实际 %q", want, got)
		}
		if got, want := body.Messages[4].ToolCallID, "call_1"; got != want {
			t.Errorf("tool 消息 tool_call_id 期望 %q，实际 %q", want, got)
		}

		if len(body.Tools) != 1 {
			t.Fatalf("tools 数期望 1，实际 %d", len(body.Tools))
		}
		if body.Tools[0].Type != "function" {
			t.Errorf("tools[0].type 期望 function，实际 %q", body.Tools[0].Type)
		}
		if body.Tools[0].Function.Name != "echo" {
			t.Errorf("tools[0].function.name 期望 echo，实际 %q", body.Tools[0].Function.Name)
		}
		if body.Tools[0].Function.Description != "回显文本" {
			t.Errorf("tools[0].function.description 期望 回显文本，实际 %q", body.Tools[0].Function.Description)
		}
		wantSchema := `{"type":"object","properties":{"text":{"type":"string"}},"required":["text"]}`
		if got := string(body.Tools[0].Function.Parameters); got != wantSchema {
			t.Errorf("tools[0].function.parameters 期望原样 %q，实际 %q", wantSchema, got)
		}
	})

	t.Run("未配置 APIKey 不发送 Authorization", func(t *testing.T) {
		srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
		client := testClient(t, srv.URL, "")

		if _, err := client.Complete(context.Background(), minimalRequest()); err != nil {
			t.Fatalf("不应返回错误：%v", err)
		}
		got := mustReceive(t, requests)
		if _, ok := got.header["Authorization"]; ok {
			t.Fatalf("未配置 APIKey 时不应发送 Authorization，实际 %q", got.header.Get("Authorization"))
		}
	})

	t.Run("BaseURL 自带 /v1 与结尾斜杠", func(t *testing.T) {
		srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
		client := testClient(t, srv.URL+"/v1/", testAPIKey)

		if _, err := client.Complete(context.Background(), minimalRequest()); err != nil {
			t.Fatalf("不应返回错误：%v", err)
		}
		got := mustReceive(t, requests)
		if got.path != "/v1/chat/completions" {
			t.Fatalf("期望路径 /v1/chat/completions，实际 %s", got.path)
		}
	})

	t.Run("工具失败结果以可区分文本回填", func(t *testing.T) {
		srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
		client := testClient(t, srv.URL, testAPIKey)

		req := minimalRequest()
		req.Messages = []core.Message{
			{ID: "m1", Role: core.Role("assistant"), ToolCalls: []core.ToolCall{
				{ID: "call_ok", Name: "echo", Arguments: json.RawMessage(`{}`)},
				{ID: "call_err", Name: "echo", Arguments: json.RawMessage(`{}`)},
			}},
			{ID: "m2", Role: core.Role("tool"), ToolResult: &core.ToolResult{CallID: "call_ok", Name: "echo", Content: "正常结果"}},
			{ID: "m3", Role: core.Role("tool"), ToolResult: &core.ToolResult{CallID: "call_err", Name: "echo", Content: "磁盘不可写", IsError: true}},
		}

		if _, err := client.Complete(context.Background(), req); err != nil {
			t.Fatalf("不应返回错误：%v", err)
		}
		var body wireBody
		if err := json.Unmarshal(mustReceive(t, requests).body, &body); err != nil {
			t.Fatalf("请求体不是合法 JSON：%v", err)
		}
		if len(body.Messages) != 3 {
			t.Fatalf("消息数期望 3，实际 %d", len(body.Messages))
		}
		if got := body.Messages[1].Content; got != "正常结果" {
			t.Errorf("成功结果应原样回填，实际 %q", got)
		}
		if !strings.Contains(body.Messages[2].Content, "磁盘不可写") {
			t.Errorf("失败结果应保留原始描述，实际 %q", body.Messages[2].Content)
		}
		if body.Messages[2].Content == "磁盘不可写" {
			t.Errorf("失败结果必须与正常结果可区分，实际 %q", body.Messages[2].Content)
		}
		if got, want := body.Messages[2].ToolCallID, "call_err"; got != want {
			t.Errorf("失败结果 tool_call_id 期望 %q，实际 %q", want, got)
		}
	})
}

// TestCompleteRejectsInvalidRequest 覆盖请求侧契约错误：返回 invalid_argument 且不发出请求。
func TestCompleteRejectsInvalidRequest(t *testing.T) {
	cases := []struct {
		name string
		req  llm.Request
	}{
		{
			name: "缺少 Model",
			req:  llm.Request{Messages: minimalRequest().Messages},
		},
		{
			name: "工具调用缺少 ID",
			req: llm.Request{
				Model: "test-model",
				Messages: []core.Message{
					{ID: "m1", Role: core.Role("assistant"), ToolCalls: []core.ToolCall{{Name: "echo", Arguments: json.RawMessage(`{}`)}}},
				},
			},
		},
		{
			name: "工具缺少参数 Schema",
			req: llm.Request{
				Model:    "test-model",
				Messages: minimalRequest().Messages,
				Tools:    []core.ToolSpec{{Name: "echo", Description: "回显"}},
			},
		},
		{
			name: "tool 消息缺少 ToolResult",
			req: llm.Request{
				Model:    "test-model",
				Messages: []core.Message{{ID: "m1", Role: core.Role("tool")}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
			client := testClient(t, srv.URL, testAPIKey)

			_, err := client.Complete(context.Background(), tc.req)
			if err == nil {
				t.Fatalf("期望 invalid_argument，实际成功")
			}
			if got := core.KindOf(err); got != core.KindInvalidArgument {
				t.Fatalf("期望错误种类 invalid_argument，实际 %s（错误：%v）", got, err)
			}
			assertNoRequest(t, requests)
		})
	}
}

// TestCompleteResponseParsing 覆盖 AC-002：文本、单/多 Tool Call、usage 的字段映射与顺序。
func TestCompleteResponseParsing(t *testing.T) {
	cases := []struct {
		name          string
		body          string
		wantText      string
		wantFinish    llm.FinishReason
		wantUsage     llm.Usage
		wantCalls     []core.ToolCall
		wantEmptyBody bool
	}{
		{
			name:       "普通文本回答",
			body:       parseOKText,
			wantText:   "好的",
			wantFinish: llm.FinishReason("stop"),
			wantUsage:  llm.Usage{InputTokens: 11, OutputTokens: 7},
		},
		{
			name:       "usage 缺失",
			body:       `{"id":"chatcmpl-2","choices":[{"message":{"role":"assistant","content":"简洁回答"},"finish_reason":"stop"}]}`,
			wantText:   "简洁回答",
			wantFinish: llm.FinishReason("stop"),
		},
		{
			// CLEAN-002 兼容用例：message 对象存在、content 为 null 且带有效 tool_calls 仍须解析成功。
			name:          "单个 Tool Call（content 为 null）",
			body:          `{"id":"chatcmpl-3","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{\"text\":\"hi\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":5,"completion_tokens":9}}`,
			wantFinish:    llm.FinishReason("tool_calls"),
			wantUsage:     llm.Usage{InputTokens: 5, OutputTokens: 9},
			wantEmptyBody: true,
			wantCalls: []core.ToolCall{
				{ID: "call_1", Name: "echo", Arguments: json.RawMessage(`{"text":"hi"}`)},
			},
		},
		{
			name:       "多个 Tool Call 保持顺序",
			body:       `{"id":"chatcmpl-4","choices":[{"message":{"role":"assistant","content":"并行调用","tool_calls":[{"id":"call_a","type":"function","function":{"name":"echo","arguments":"{\"text\":\"a\"}"}},{"id":"call_b","type":"function","function":{"name":"add","arguments":"{\"a\":1,\"b\":[2,3]}"}}]},"finish_reason":"tool_calls"}]}`,
			wantText:   "并行调用",
			wantFinish: llm.FinishReason("tool_calls"),
			wantCalls: []core.ToolCall{
				{ID: "call_a", Name: "echo", Arguments: json.RawMessage(`{"text":"a"}`)},
				{ID: "call_b", Name: "add", Arguments: json.RawMessage(`{"a":1,"b":[2,3]}`)},
			},
		},
		{
			name:       "length 结束原因",
			body:       `{"id":"chatcmpl-5","choices":[{"message":{"role":"assistant","content":"被截断"},"finish_reason":"length"}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`,
			wantText:   "被截断",
			wantFinish: llm.FinishReason("length"),
			wantUsage:  llm.Usage{InputTokens: 3, OutputTokens: 1},
		},
		{
			name:          "零个 Tool Call 的文本回答带 content 空串",
			body:          `{"id":"chatcmpl-6","choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
			wantText:      "",
			wantFinish:    llm.FinishReason("stop"),
			wantEmptyBody: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := startCaptureServer(t, http.StatusOK, tc.body)
			client := testClient(t, srv.URL, testAPIKey)

			resp, err := client.Complete(context.Background(), minimalRequest())
			if err != nil {
				t.Fatalf("不应返回错误：%v", err)
			}
			if resp.Message.Role != core.Role("assistant") {
				t.Fatalf("Message.Role 必须为 assistant，实际 %q", resp.Message.Role)
			}
			if resp.FinishReason != tc.wantFinish {
				t.Errorf("FinishReason 期望 %q，实际 %q", tc.wantFinish, resp.FinishReason)
			}
			if resp.Usage != tc.wantUsage {
				t.Errorf("Usage 期望 %+v，实际 %+v", tc.wantUsage, resp.Usage)
			}
			if tc.wantEmptyBody {
				if len(resp.Message.Content) != 0 {
					t.Fatalf("content 为 null 时不应产生文本片段，实际 %+v", resp.Message.Content)
				}
			} else {
				if len(resp.Message.Content) != 1 {
					t.Fatalf("文本片段数期望 1，实际 %d", len(resp.Message.Content))
				}
				if got := resp.Message.Content[0].Text; got != tc.wantText {
					t.Errorf("文本内容期望 %q，实际 %q", tc.wantText, got)
				}
				if resp.Message.Content[0].Type != core.ContentType("text") {
					t.Errorf("文本片段类型期望 text，实际 %q", resp.Message.Content[0].Type)
				}
			}

			if len(resp.Message.ToolCalls) != len(tc.wantCalls) {
				t.Fatalf("ToolCall 数期望 %d，实际 %d", len(tc.wantCalls), len(resp.Message.ToolCalls))
			}
			for i, want := range tc.wantCalls {
				got := resp.Message.ToolCalls[i]
				if got.ID != want.ID {
					t.Errorf("ToolCall[%d].ID 期望 %q，实际 %q", i, want.ID, got.ID)
				}
				if got.Name != want.Name {
					t.Errorf("ToolCall[%d].Name 期望 %q，实际 %q", i, want.Name, got.Name)
				}
				if string(got.Arguments) != string(want.Arguments) {
					t.Errorf("ToolCall[%d].Arguments 期望原样 %q，实际 %q", i, want.Arguments, got.Arguments)
				}
			}

			if string(resp.Raw) != tc.body {
				t.Errorf("Raw 必须保留完整原始响应体，期望 %q，实际 %q", tc.body, resp.Raw)
			}
			if len(resp.Message.Raw) == 0 {
				t.Errorf("Message.Raw 应保留原始 message 载荷")
			}
			if err := resp.Validate(); err != nil {
				t.Fatalf("解析成功的响应必须通过 Response.Validate：%v", err)
			}
		})
	}
}

// TestCompleteErrorSemantics 覆盖 AC-003 的失败语义：HTTP、协议错误与请求次数。
func TestCompleteErrorSemantics(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantKind     core.ErrorKind
		wantContains string
	}{
		{
			name:         "HTTP 400",
			status:       http.StatusBadRequest,
			body:         `{"error":{"message":"bad request"}}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "400",
		},
		{
			name:         "HTTP 401",
			status:       http.StatusUnauthorized,
			body:         `{"error":{"message":"invalid api key"}}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "401",
		},
		{
			name:         "HTTP 429 只请求一次",
			status:       http.StatusTooManyRequests,
			body:         `{"error":{"message":"rate limited"}}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "429",
		},
		{
			name:         "HTTP 500",
			status:       http.StatusInternalServerError,
			body:         `internal error`,
			wantKind:     core.KindProviderFailure,
			wantContains: "500",
		},
		{
			name:         "非法 JSON 响应体",
			status:       http.StatusOK,
			body:         `{"choices":`,
			wantKind:     core.KindProviderFailure,
			wantContains: "JSON",
		},
		{
			name:         "缺少 choices",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x"}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "choices",
		},
		{
			name:         "choices 为空数组",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "choices",
		},
		{
			name:         "choices 缺少 message",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"finish_reason":"stop"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "message",
		},
		{
			// 回归 CLEAN-002：message 为 null 时不得被解码成零值结构体后当作成功响应。
			name:         "choices 的 message 为 null",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":null,"finish_reason":"stop"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "message",
		},
		{
			name:         "choices 的 message 不是对象",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":"oops","finish_reason":"stop"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "message",
		},
		{
			name:         "arguments 非法 JSON",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":"{oops"}}]},"finish_reason":"tool_calls"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "arguments",
		},
		{
			name:         "arguments 为空字符串",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"id":"call_1","type":"function","function":{"name":"echo","arguments":""}}]},"finish_reason":"tool_calls"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "arguments",
		},
		{
			name:         "工具调用缺少 ID",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":null,"tool_calls":[{"type":"function","function":{"name":"echo","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "ID",
		},
		{
			name:         "未知 finish_reason",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"content_filter"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "finish_reason",
		},
		{
			name:         "finish_reason 缺失",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"x"}}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "finish_reason",
		},
		{
			name:         "响应角色不是 assistant",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"user","content":"x"},"finish_reason":"stop"}]}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "assistant",
		},
		{
			name:         "token 用量为负",
			status:       http.StatusOK,
			body:         `{"id":"chatcmpl-x","choices":[{"message":{"role":"assistant","content":"x"},"finish_reason":"stop"}],"usage":{"prompt_tokens":-1,"completion_tokens":2}}`,
			wantKind:     core.KindProviderFailure,
			wantContains: "负数",
		},
		{
			name:         "空响应体",
			status:       http.StatusOK,
			body:         ``,
			wantKind:     core.KindProviderFailure,
			wantContains: "空",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				atomic.AddInt32(&hits, 1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			t.Cleanup(srv.Close)
			client := testClient(t, srv.URL, testAPIKey)

			resp, err := client.Complete(context.Background(), minimalRequest())
			if err == nil {
				t.Fatalf("期望失败，实际成功并返回 %+v", resp)
			}
			if got := core.KindOf(err); got != tc.wantKind {
				t.Fatalf("期望错误种类 %s，实际 %s（错误：%v）", tc.wantKind, got, err)
			}
			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("错误文本应可定位 %q，实际 %q", tc.wantContains, err.Error())
			}
			if strings.Contains(err.Error(), testAPIKey) {
				t.Fatalf("错误文本不得包含 APIKey 明文：%v", err)
			}
			if resp.Message.Role != "" || len(resp.Message.Content) != 0 || resp.Raw != nil || resp.FinishReason != "" {
				t.Fatalf("失败时不得返回半成品 Response，实际 %+v", resp)
			}
			if got := atomic.LoadInt32(&hits); got != 1 {
				t.Fatalf("默认不重试：期望只发出 1 次请求，实际 %d 次", got)
			}
		})
	}
}

// TestCompleteContextFailures 覆盖取消、超时与传输失败的错误种类与边界。
func TestCompleteContextFailures(t *testing.T) {
	t.Run("服务端阻塞导致 context 超时", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(func() {
			close(release)
			srv.Close()
		})
		client := testClient(t, srv.URL, testAPIKey)

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		_, err := client.Complete(ctx, minimalRequest())
		if got := core.KindOf(err); got != core.KindDeadlineExceeded {
			t.Fatalf("期望 deadline_exceeded，实际 %s（错误：%v）", got, err)
		}
	})

	t.Run("HTTPClient 超时映射为 deadline_exceeded", func(t *testing.T) {
		release := make(chan struct{})
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(func() {
			close(release)
			srv.Close()
		})
		client, err := NewClient(Config{
			BaseURL:    srv.URL,
			APIKey:     testAPIKey,
			HTTPClient: &http.Client{Timeout: 30 * time.Millisecond},
		})
		if err != nil {
			t.Fatalf("构造 Client 失败：%v", err)
		}

		_, err = client.Complete(context.Background(), minimalRequest())
		if got := core.KindOf(err); got != core.KindDeadlineExceeded {
			t.Fatalf("期望 deadline_exceeded，实际 %s（错误：%v）", got, err)
		}
	})

	t.Run("调用前已取消", func(t *testing.T) {
		srv, requests := startCaptureServer(t, http.StatusOK, parseOKText)
		client := testClient(t, srv.URL, testAPIKey)

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := client.Complete(ctx, minimalRequest())
		if got := core.KindOf(err); got != core.KindCancelled {
			t.Fatalf("期望 cancelled，实际 %s（错误：%v）", got, err)
		}
		assertNoRequest(t, requests)
	})

	t.Run("调用中取消", func(t *testing.T) {
		started := make(chan struct{})
		release := make(chan struct{})
		var once sync.Once
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			once.Do(func() { close(started) })
			// 处理函数由 cleanup 显式放行，不依赖服务端是否及时感知连接关闭。
			select {
			case <-release:
			case <-r.Context().Done():
			}
		}))
		t.Cleanup(func() {
			close(release)
			srv.Close()
		})
		client := testClient(t, srv.URL, testAPIKey)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			<-started
			cancel()
		}()

		_, err := client.Complete(ctx, minimalRequest())
		if got := core.KindOf(err); got != core.KindCancelled {
			t.Fatalf("期望 cancelled，实际 %s（错误：%v）", got, err)
		}
	})

	t.Run("连接失败保留 Cause", func(t *testing.T) {
		srv := httptest.NewServer(http.NotFoundHandler())
		closedURL := srv.URL
		srv.Close()

		client := testClient(t, closedURL, testAPIKey)
		_, err := client.Complete(context.Background(), minimalRequest())
		if got := core.KindOf(err); got != core.KindProviderFailure {
			t.Fatalf("期望 provider_failure，实际 %s（错误：%v）", got, err)
		}
		var runErr *core.RunError
		if !errors.As(err, &runErr) {
			t.Fatalf("期望 *core.RunError，实际 %T", err)
		}
		if runErr.Cause == nil {
			t.Fatalf("传输失败必须保留原始 Cause")
		}
	})
}

// TestNewClientConfigValidation 覆盖配置缺失/非法时的 invalid_argument 与合法端点拼接。
func TestNewClientConfigValidation(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
	}{
		{name: "BaseURL 为空", cfg: Config{APIKey: testAPIKey}},
		{name: "BaseURL 不是合法 URL", cfg: Config{BaseURL: "://bad"}},
		{name: "BaseURL 缺少 scheme", cfg: Config{BaseURL: "127.0.0.1:8080"}},
		{name: "BaseURL 协议不受支持", cfg: Config{BaseURL: "ftp://example.com"}},
		{name: "BaseURL 缺少主机名", cfg: Config{BaseURL: "http:///v1"}},
		{name: "Timeout 为负", cfg: Config{BaseURL: "http://127.0.0.1:1", Timeout: -time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := NewClient(tc.cfg)
			if err == nil {
				t.Fatalf("期望 invalid_argument，实际构造成功：%+v", client)
			}
			if client != nil {
				t.Fatalf("配置非法时不应返回 Client")
			}
			if got := core.KindOf(err); got != core.KindInvalidArgument {
				t.Fatalf("期望错误种类 invalid_argument，实际 %s（错误：%v）", got, err)
			}
		})
	}

	t.Run("合法配置拼接端点", func(t *testing.T) {
		for _, baseURL := range []string{"https://api.example.com/v1", "https://api.example.com/v1/", "https://api.example.com/v1//"} {
			client, err := NewClient(Config{BaseURL: baseURL, APIKey: testAPIKey})
			if err != nil {
				t.Fatalf("BaseURL %q 不应失败：%v", baseURL, err)
			}
			if got, want := client.endpoint, "https://api.example.com/v1/chat/completions"; got != want {
				t.Fatalf("BaseURL %q 期望端点 %q，实际 %q", baseURL, want, got)
			}
		}
	})
}
