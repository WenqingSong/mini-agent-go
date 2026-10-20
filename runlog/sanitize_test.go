package runlog

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// sanitizedObject 返回脱敏后载荷的 JSON 文本与解码结果，便于同时做结构断言与字节级扫描。
func sanitizedObject(t *testing.T, payload string) (string, map[string]any) {
	t.Helper()

	data, invalid, truncated := sanitizeData(json.RawMessage(payload))
	if invalid {
		t.Fatalf("载荷不应被判为非法：%s", payload)
	}
	if truncated {
		t.Fatalf("载荷不应被判为超限：%s", payload)
	}

	raw, err := json.Marshal(data)
	if err != nil {
		t.Fatalf("脱敏结果无法序列化：%v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("脱敏结果不是合法 JSON：%v", err)
	}
	return string(raw), decoded
}

// TestSanitizeDataKeepsAllowedFields 证明允许清单内的业务字段原样保留（INV-LOG-003 的正向部分）。
func TestSanitizeDataKeepsAllowedFields(t *testing.T) {
	payload := `{
		"model": "gpt-test",
		"provider": "openai",
		"name": "search",
		"tool_name": "search",
		"is_error": false,
		"ok": true,
		"status": "done",
		"finish_reason": "stop",
		"attempt": 2,
		"max_attempts": 3,
		"retry_count": 1,
		"compacted": true,
		"dropped_messages": 2,
		"message_count": 7,
		"tool_call_count": 1,
		"id": "call_1",
		"index": 3,
		"truncated": false,
		"usage": {"input_tokens": 11, "output_tokens": 22, "total_tokens": 33, "cached_tokens": 5}
	}`

	raw, got := sanitizedObject(t, payload)

	var want map[string]any
	if err := json.Unmarshal([]byte(payload), &want); err != nil {
		t.Fatalf("期望值构造失败：%v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("允许清单字段被改写：\n得到：%s\n期望：%s", raw, payload)
	}
}

// TestSanitizeDataRedactsSensitiveKeys 覆盖顶层与嵌套的凭据命名变体：原始值必须消失（INV-LOG-003）。
func TestSanitizeDataRedactsSensitiveKeys(t *testing.T) {
	tests := []struct {
		name    string
		payload string
		secrets []string
	}{
		{
			name:    "下划线 api_key",
			payload: `{"api_key":"sk-live-1"}`,
			secrets: []string{"sk-live-1"},
		},
		{
			name:    "驼峰 apiKey",
			payload: `{"apiKey":"sk-live-2"}`,
			secrets: []string{"sk-live-2"},
		},
		{
			name:    "短横线 X-Api-Key",
			payload: `{"X-Api-Key":"sk-live-3"}`,
			secrets: []string{"sk-live-3"},
		},
		{
			name:    "Authorization Bearer",
			payload: `{"authorization":"Bearer sk-live-4"}`,
			secrets: []string{"sk-live-4", "Bearer sk-live-4"},
		},
		{
			name:    "token/secret/password",
			payload: `{"token":"tok-5","secret":"sec-6","password":"pw-7","passwd":"pw-8"}`,
			secrets: []string{"tok-5", "sec-6", "pw-7", "pw-8"},
		},
		{
			name:    "私钥与访问密钥",
			payload: `{"private_key":"-----BEGIN PRIVATE KEY-----abc","access_key":"ak-9"}`,
			secrets: []string{"-----BEGIN PRIVATE KEY-----abc", "ak-9"},
		},
		{
			name:    "cookie 与 proxy-authorization",
			payload: `{"cookie":"sid=10","proxy-authorization":"Basic xyz11"}`,
			secrets: []string{"sid=10", "xyz11"},
		},
		{
			// 允许清单内的容器值会递归套用同一规则，因此嵌套凭据同样必须被替换。
			name:    "允许容器内嵌套对象中的凭据",
			payload: `{"usage":{"apiKey":"deep-12","auth":{"token":"deeper-13"},"input_tokens":5}}`,
			secrets: []string{"deep-12", "deeper-13"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, _ := sanitizedObject(t, tt.payload)
			if !strings.Contains(raw, redactedValue) {
				t.Fatalf("未出现脱敏占位符：%s", raw)
			}
			for _, secret := range tt.secrets {
				if strings.Contains(raw, secret) {
					t.Fatalf("敏感值 %q 泄漏到输出：%s", secret, raw)
				}
			}
		})
	}

	// 允许容器内：嵌套凭据被替换，同级非敏感业务字段仍需保留。
	_, got := sanitizedObject(t, `{"usage":{"apiKey":"deep-secret","input_tokens":5}}`)
	nested, ok := got["usage"].(map[string]any)
	if !ok {
		t.Fatalf("允许清单容器丢失：%v", got)
	}
	if nested["input_tokens"] != float64(5) {
		t.Fatalf("嵌套非敏感字段被改写：%v", nested)
	}
	if nested["apiKey"] != redactedValue {
		t.Fatalf("嵌套敏感字段未被替换为占位符：%v", nested)
	}
}

// TestSanitizeDataRedactsUserContent 证明正文只保留长度摘要，不输出原文（INV-LOG-003）。
func TestSanitizeDataRedactsUserContent(t *testing.T) {
	bodies := map[string]string{
		"content": "用户正文A",
		"text":    "用户正文B",
		"prompt":  "用户正文D",
		"input":   "用户正文E",
		"raw":     "用户正文F",
		"body":    "用户正文G",
	}

	var b strings.Builder
	b.WriteString("{")
	first := true
	for key, body := range bodies {
		if !first {
			b.WriteString(",")
		}
		first = false
		b.WriteString(strconv.Quote(key))
		b.WriteString(":")
		b.WriteString(strconv.Quote(body))
	}
	b.WriteString(`,"messages":[{"role":"user","content":"用户正文C"}]}`)

	raw, got := sanitizedObject(t, b.String())

	for key, body := range bodies {
		if strings.Contains(raw, body) {
			t.Fatalf("正文键 %s 的原文泄漏：%s", key, raw)
		}
		want := "[REDACTED:len=" + strconv.Itoa(len(body)) + "]"
		if got[key] != want {
			t.Fatalf("正文键 %s 的长度摘要不符：得到 %v，期望 %s", key, got[key], want)
		}
	}

	if strings.Contains(raw, "用户正文C") {
		t.Fatalf("messages 中的正文泄漏：%s", raw)
	}
	if v, ok := got["messages"].(string); !ok || !strings.HasPrefix(v, "[REDACTED:len=") {
		t.Fatalf("messages 未按正文处理：%v", got["messages"])
	}
}

// TestSanitizeDataOmitsUnknownKeys 证明未知键整键省略：键名与值都不出现（Owner Q2 决定）。
func TestSanitizeDataOmitsUnknownKeys(t *testing.T) {
	raw, got := sanitizedObject(t, `{"foo":"bar-value","blob":{"x":"y"},"nested":{"apiKey":"deep-secret"},"model":"m","usage":{"input_tokens":1,"weird":"z"}}`)

	for _, leaked := range []string{"foo", "bar-value", "blob", `"x"`, "nested", "deep-secret", "weird", `"z"`} {
		if strings.Contains(raw, leaked) {
			t.Fatalf("未知键内容 %q 出现在输出中：%s", leaked, raw)
		}
	}
	if got["model"] != "m" {
		t.Fatalf("允许清单字段丢失：%v", got)
	}
	usage, ok := got["usage"].(map[string]any)
	if !ok || usage["input_tokens"] == nil {
		t.Fatalf("允许清单嵌套字段丢失：%v", got["usage"])
	}
}

// TestSanitizeDataArrayElementRules 证明数组元素遵循独立规则：字符串按正文处理，数字/布尔保留。
func TestSanitizeDataArrayElementRules(t *testing.T) {
	_, got := sanitizedObject(t, `{"usage":[{"input_tokens":1},"raw-array-string",5,true,null]}`)

	items, ok := got["usage"].([]any)
	if !ok || len(items) != 5 {
		t.Fatalf("数组结构被改写：%v", got["usage"])
	}
	if obj, ok := items[0].(map[string]any); !ok || obj["input_tokens"] != float64(1) {
		t.Fatalf("数组内对象未按成员规则处理：%v", items[0])
	}
	if items[1] != "[REDACTED:len=16]" {
		t.Fatalf("数组内字符串未按正文处理：%v", items[1])
	}
	if items[2] != float64(5) || items[3] != true || items[4] != nil {
		t.Fatalf("数组内标量被改写：%v", items)
	}
}

// TestSanitizeDataEnforcesLimits 证明深度、节点、字符串与载荷上限都以标记表达，且结果可复现（INV-LOG-007）。
func TestSanitizeDataEnforcesLimits(t *testing.T) {
	t.Run("深度超过 8 层替换为 TRUNCATED", func(t *testing.T) {
		payload := strings.Repeat(`{"usage":`, 12) + `"leaf-value"` + strings.Repeat(`}`, 12)
		raw, _ := sanitizedObject(t, payload)
		if !strings.Contains(raw, truncatedValue) {
			t.Fatalf("深嵌套未出现截断标记：%s", raw)
		}
		if strings.Contains(raw, "leaf-value") {
			t.Fatalf("超出深度的值泄漏：%s", raw)
		}
	})

	t.Run("节点预算耗尽后替换为 TRUNCATED", func(t *testing.T) {
		payload := `{"usage":[` + strings.Repeat(`{"input_tokens":1},`, maxNodes) + `{"input_tokens":1}]}`
		raw, _ := sanitizedObject(t, payload)
		if !strings.Contains(raw, truncatedValue) {
			t.Fatalf("节点预算耗尽未出现截断标记")
		}
		if strings.Count(raw, "[TRUNCATED]") < 1 {
			t.Fatalf("截断标记数量异常：%d", strings.Count(raw, truncatedValue))
		}
	})

	t.Run("超长字符串截断到 UTF-8 边界", func(t *testing.T) {
		long := strings.Repeat("长", 200) // 600 字节
		_, got := sanitizedObject(t, `{"model":`+strconv.Quote(long)+`}`)

		truncated, ok := got["model"].(string)
		if !ok {
			t.Fatalf("长字符串丢失：%v", got["model"])
		}
		if !strings.HasSuffix(truncated, "…") {
			t.Fatalf("截断结果缺少省略号：%q", truncated)
		}
		if len(truncated) > maxStringBytes+len("…") {
			t.Fatalf("截断结果超长：%d 字节", len(truncated))
		}
		if !utf8.ValidString(truncated) {
			t.Fatalf("截断结果不是合法 UTF-8：%q", truncated)
		}
	})

	t.Run("同一输入两次脱敏结果相同", func(t *testing.T) {
		payload := `{"usage":[` + strings.Repeat(`{"input_tokens":1},`, maxNodes) + `{"input_tokens":1}],` +
			`"model":"m","nested":{"api_key":"s","ok":true}}`

		first, _ := sanitizedObject(t, payload)
		second, _ := sanitizedObject(t, payload)
		if first != second {
			t.Fatalf("脱敏结果不稳定：\n第一次：%s\n第二次：%s", first, second)
		}
	})
}

// TestSanitizeDataRejectsBadPayload 证明非法/超限载荷只产出标记，不输出任何原始字节。
func TestSanitizeDataRejectsBadPayload(t *testing.T) {
	oversized := `{"model":"` + strings.Repeat("a", maxDataBytes) + `"}`

	tests := []struct {
		name          string
		payload       string
		wantInvalid   bool
		wantTruncated bool
	}{
		{name: "非法 JSON", payload: `{"model":`, wantInvalid: true},
		{name: "尾随内容", payload: `{"model":"m"} {"model":"n"}`, wantInvalid: true},
		{name: "顶层数组", payload: `["a","b"]`, wantInvalid: true},
		{name: "顶层字符串", payload: `"plain"`, wantInvalid: true},
		{name: "空载荷", payload: ``, wantInvalid: false},
		{name: "超过 64 KiB", payload: oversized, wantTruncated: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, invalid, truncated := sanitizeData(json.RawMessage(tt.payload))
			if invalid != tt.wantInvalid || truncated != tt.wantTruncated {
				t.Fatalf("标记不符：invalid=%v truncated=%v，期望 invalid=%v truncated=%v",
					invalid, truncated, tt.wantInvalid, tt.wantTruncated)
			}
			if invalid || truncated {
				if data != nil {
					t.Fatalf("不可用载荷不应产出数据：%v", data)
				}
			}
		})
	}
}

// TestSanitizeStringRules 覆盖值级规则：凭据模式整体替换、普通长文本按 UTF-8 边界截断。
func TestSanitizeStringRules(t *testing.T) {
	for _, secret := range []string{"Bearer sk-abc", "basic dXNlcjpwYXNz", "bearer    token-value"} {
		if got := sanitizeString(secret); got != redactedValue {
			t.Fatalf("凭据模式未脱敏：%q → %q", secret, got)
		}
	}

	if got := sanitizeString("普通短文本"); got != "普通短文本" {
		t.Fatalf("普通文本被改写：%q", got)
	}

	truncated := sanitizeString(strings.Repeat("a", maxStringBytes+50))
	if len(truncated) != maxStringBytes+len("…") {
		t.Fatalf("截断长度不符：%d", len(truncated))
	}

	// 边界：不含凭据模式时保持原样。
	if got := sanitizeString("basic"); got != "basic" {
		t.Fatalf("单词 basic 被误判为凭据：%q", got)
	}
}
