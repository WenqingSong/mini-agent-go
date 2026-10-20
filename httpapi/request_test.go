package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMediaTypeAllowed 核对 Content-Type 接受与拒绝集合。
func TestMediaTypeAllowed(t *testing.T) {
	accepted := []string{
		"application/json",
		"application/json; charset=utf-8",
		"Application/JSON; Charset=UTF-8",
	}
	for _, header := range accepted {
		if !mediaTypeAllowed(header) {
			t.Errorf("Content-Type %q 应被接受", header)
		}
	}

	rejected := []string{
		"",
		"text/plain",
		"application/json; charset=latin-1",
		"application/json-patch+json",
		"multipart/form-data; boundary=x",
		"not a media type",
	}
	for _, header := range rejected {
		if mediaTypeAllowed(header) {
			t.Errorf("Content-Type %q 应被拒绝", header)
		}
	}
}

// TestReadJSONObject 核对有界读取与 JSON 结构的严格性。
func TestReadJSONObject(t *testing.T) {
	const limit = 256

	cases := []struct {
		name     string
		body     string
		wantCode ErrorCode
	}{
		{name: "空 body 等价空对象", body: "", wantCode: ""},
		{name: "空白 body 等价空对象", body: "   \n\t", wantCode: ""},
		{name: "空对象", body: "{}", wantCode: ""},
		{name: "普通对象", body: `{"session_id":"abc"}`, wantCode: ""},
		{name: "尾部空白", body: `{"session_id":"abc"}   `, wantCode: ""},
		{name: "null 不是对象", body: "null", wantCode: CodeInvalidJSON},
		{name: "数组不是对象", body: `[]`, wantCode: CodeInvalidJSON},
		{name: "字符串不是对象", body: `"x"`, wantCode: CodeInvalidJSON},
		{name: "数值不是对象", body: `1`, wantCode: CodeInvalidJSON},
		{name: "截断的 JSON", body: `{"session_id":`, wantCode: CodeInvalidJSON},
		{name: "非法 UTF-8", body: "{\"session_id\":\"\xff\"}", wantCode: CodeInvalidField},
		{name: "尾部多余数据", body: `{"a":1}{"b":2}`, wantCode: CodeInvalidJSON},
		{name: "尾部垃圾字符", body: `{"a":1}x`, wantCode: CodeInvalidJSON},
		{name: "超过上限", body: `{"session_id":"` + strings.Repeat("a", limit) + `"}`, wantCode: CodePayloadTooLarge},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(item.body))
			recorder := httptest.NewRecorder()

			fields, err := readJSONObject(recorder, request, limit)
			if item.wantCode == "" {
				if err != nil {
					t.Fatalf("期望解析成功，实际错误：%v", err)
				}
				if fields == nil {
					t.Fatal("成功解析必须返回非 nil 字段表")
				}
				return
			}

			var parseErr *requestError
			if !errors.As(err, &parseErr) {
				t.Fatalf("期望解析错误 %q，实际：%v", item.wantCode, err)
			}
			if parseErr.code != item.wantCode {
				t.Fatalf("错误码 = %q，期望 %q", parseErr.code, item.wantCode)
			}
		})
	}
}

// TestParseChat 核对 chat 请求的字段、类型与取值边界。
func TestParseChat(t *testing.T) {
	validID := "session-1"
	validBody := func(message string) string {
		payload, err := json.Marshal(map[string]any{"session_id": validID, "message": message})
		if err != nil {
			t.Fatalf("构造请求失败：%v", err)
		}
		return string(payload)
	}

	cases := []struct {
		name     string
		body     string
		wantCode ErrorCode
	}{
		{name: "合法", body: validBody("你好"), wantCode: ""},
		{name: "单字符消息", body: validBody("a"), wantCode: ""},
		{name: "8192 字符消息", body: validBody(strings.Repeat("字", 8192)), wantCode: ""},
		{name: "8193 字符消息", body: validBody(strings.Repeat("字", 8193)), wantCode: CodeInvalidField},
		{name: "空消息", body: validBody(""), wantCode: CodeInvalidField},
		{name: "缺少 session_id", body: `{"message":"hi"}`, wantCode: CodeMissingField},
		{name: "缺少 message", body: `{"session_id":"session-1"}`, wantCode: CodeMissingField},
		{name: "空对象", body: `{}`, wantCode: CodeMissingField},
		{name: "未知字段", body: `{"session_id":"session-1","message":"hi","extra":1}`, wantCode: CodeUnknownField},
		{name: "session_id 类型错误", body: `{"session_id":1,"message":"hi"}`, wantCode: CodeInvalidField},
		{name: "session_id 为 null", body: `{"session_id":null,"message":"hi"}`, wantCode: CodeInvalidField},
		{name: "message 类型错误", body: `{"session_id":"session-1","message":["hi"]}`, wantCode: CodeInvalidField},
		{name: "message 为 null", body: `{"session_id":"session-1","message":null}`, wantCode: CodeInvalidField},
		{name: "session_id 含空格", body: `{"session_id":"bad id","message":"hi"}`, wantCode: CodeInvalidField},
		{name: "session_id 含非 ASCII", body: `{"session_id":"会话","message":"hi"}`, wantCode: CodeInvalidField},
		{name: "session_id 超过 128", body: `{"session_id":"` + strings.Repeat("a", 129) + `","message":"hi"}`, wantCode: CodeInvalidField},
		{name: "session_id 恰好 128", body: `{"session_id":"` + strings.Repeat("a", 128) + `","message":"hi"}`, wantCode: ""},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(item.body), &fields); err != nil {
				t.Fatalf("测试用例本身不是合法 JSON：%v", err)
			}
			_, err := parseChat(fields)
			assertRequestCode(t, err, item.wantCode)
		})
	}
}

// TestParseSessionCreate 核对创建会话的字段规则：session_id 可选但一旦出现必须合法。
func TestParseSessionCreate(t *testing.T) {
	cases := []struct {
		name     string
		body     string
		wantID   string
		wantCode ErrorCode
	}{
		{name: "空对象由服务端生成", body: `{}`, wantID: ""},
		{name: "显式 ID", body: `{"session_id":"abc.DEF_1-2:3"}`, wantID: "abc.DEF_1-2:3"},
		{name: "ID 超过 128", body: `{"session_id":"` + strings.Repeat("a", 129) + `"}`, wantCode: CodeInvalidField},
		{name: "ID 为空字符串", body: `{"session_id":""}`, wantCode: CodeInvalidField},
		{name: "ID 类型错误", body: `{"session_id":true}`, wantCode: CodeInvalidField},
		{name: "未知字段", body: `{"session_id":"abc","model":"x"}`, wantCode: CodeUnknownField},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			var fields map[string]json.RawMessage
			if err := json.Unmarshal([]byte(item.body), &fields); err != nil {
				t.Fatalf("测试用例本身不是合法 JSON：%v", err)
			}
			request, err := parseSessionCreate(fields)
			assertRequestCode(t, err, item.wantCode)
			if err == nil && request.SessionID != item.wantID {
				t.Fatalf("session_id = %q，期望 %q", request.SessionID, item.wantID)
			}
		})
	}
}

// TestValidMessageRejectsInvalidUTF8 核对非法 UTF-8 被判为字段错误（不是崩溃或静默替换）。
func TestValidMessageRejectsInvalidUTF8(t *testing.T) {
	if validMessage("\xff\xfe") {
		t.Fatal("非法 UTF-8 应被判为字段错误")
	}
	if !validMessage("你好") {
		t.Fatal("合法 UTF-8 应被接受")
	}
}

// assertRequestCode 断言解析错误码（空串表示期望成功）。
func assertRequestCode(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	if want == "" {
		if err != nil {
			t.Fatalf("期望成功，实际错误：%v", err)
		}
		return
	}
	var parseErr *requestError
	if !errors.As(err, &parseErr) {
		t.Fatalf("期望解析错误 %q，实际：%v", want, err)
	}
	if parseErr.code != want {
		t.Fatalf("错误码 = %q，期望 %q", parseErr.code, want)
	}
}
