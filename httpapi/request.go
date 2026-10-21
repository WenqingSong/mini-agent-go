package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"regexp"
	"strings"
	"unicode/utf8"
)

// 字段级限制：与长期 HTTP 契约保持一致，取值变更属于公开协议变更。
const (
	// maxSessionIDLength 是 session_id 的最大字符数。
	maxSessionIDLength = 128
	// maxMessageLength 是 message 的最大 UTF-8 字符数。
	maxMessageLength = 8192
)

// sessionIDPattern 是 session_id 的字符集与长度约束。
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// requestError 是请求解析阶段的稳定错误码载体。
//
// 解析阶段只产生参数类错误码（invalid_json / unknown_field / missing_field /
// invalid_field / unsupported_media_type / payload_too_large），错误文本不携带动态内容。
type requestError struct {
	code ErrorCode
}

// Error 实现 error：文本即错误码，便于日志定位且不含用户输入。
func (e *requestError) Error() string { return string(e.code) }

// fail 构造解析阶段错误。
func fail(code ErrorCode) error { return &requestError{code: code} }

// mediaTypeAllowed 判断 Content-Type 是否为 application/json（允许 ; charset=utf-8）。
func mediaTypeAllowed(header string) bool {
	if header == "" {
		return false
	}
	mediaType, params, err := mime.ParseMediaType(header)
	if err != nil || !strings.EqualFold(mediaType, "application/json") {
		return false
	}
	if charset, ok := params["charset"]; ok && !strings.EqualFold(charset, "utf-8") {
		return false
	}
	return true
}

// readJSONObject 有界读取请求体并严格解析为单个 JSON 对象。
//
// 语义：空 body 等价于 {}；数组、null、标量、尾部数据与非法 JSON 都是 invalid_json；
// 超过上限是 payload_too_large。读取使用 http.MaxBytesReader，超限即拒绝，不无界缓存。
func readJSONObject(w http.ResponseWriter, r *http.Request, limit int64) (map[string]json.RawMessage, error) {
	var body []byte
	if r.Body != nil {
		reader := http.MaxBytesReader(w, r.Body, limit)
		read, err := io.ReadAll(reader)
		if err != nil {
			var maxErr *http.MaxBytesError
			if errors.As(err, &maxErr) {
				return nil, fail(CodePayloadTooLarge)
			}
			// 其余读取失败同样意味着请求体不构成可解析的 JSON 对象。
			return nil, fail(CodeInvalidJSON)
		}
		body = read
	}

	// 非法 UTF-8 按字段错误拒绝：标准库解码会把它替换为 U+FFFD 而静默改变用户输入。
	if !utf8.Valid(body) {
		return nil, fail(CodeInvalidField)
	}

	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return map[string]json.RawMessage{}, nil
	}
	if trimmed[0] != '{' {
		// 数组、字符串、数值与 null 都不是契约要求的 JSON 对象。
		return nil, fail(CodeInvalidJSON)
	}

	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	var fields map[string]json.RawMessage
	if err := decoder.Decode(&fields); err != nil || fields == nil {
		return nil, fail(CodeInvalidJSON)
	}
	// 只允许单个 JSON 值：尾部任何 token（包括第二个对象）都是 invalid_json。
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return nil, fail(CodeInvalidJSON)
	}
	return fields, nil
}

// rejectUnknownFields 拒绝任何契约外键。
func rejectUnknownFields(fields map[string]json.RawMessage, allowed ...string) error {
	for name := range fields {
		known := false
		for _, candidate := range allowed {
			if name == candidate {
				known = true
				break
			}
		}
		if !known {
			return fail(CodeUnknownField)
		}
	}
	return nil
}

// stringField 读取字符串字段。
//
// 返回 present 表示字段是否出现；出现但不是字符串（含 JSON null）一律 invalid_field，
// 不做隐式类型转换或 trim。
func stringField(fields map[string]json.RawMessage, name string) (value string, present bool, err error) {
	raw, ok := fields[name]
	if !ok {
		return "", false, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", true, fail(CodeInvalidField)
	}
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", true, fail(CodeInvalidField)
	}
	return value, true, nil
}

// validSessionID 校验会话 ID 的字符集与长度。
func validSessionID(id string) bool {
	return sessionIDPattern.MatchString(id) && len(id) <= maxSessionIDLength
}

// validMessage 校验用户消息：合法 UTF-8 且长度在 1..8192 个字符之间。
func validMessage(message string) bool {
	if !utf8.ValidString(message) {
		return false
	}
	length := utf8.RuneCountInString(message)
	return length >= 1 && length <= maxMessageLength
}

// sessionCreateRequest 是 POST /v1/sessions 的解析结果。
type sessionCreateRequest struct {
	// SessionID 为空表示由服务端生成。
	SessionID string
}

// parseSessionCreate 严格解析创建会话请求。
func parseSessionCreate(fields map[string]json.RawMessage) (sessionCreateRequest, error) {
	if err := rejectUnknownFields(fields, "session_id"); err != nil {
		return sessionCreateRequest{}, err
	}
	id, present, err := stringField(fields, "session_id")
	if err != nil {
		return sessionCreateRequest{}, err
	}
	if present && !validSessionID(id) {
		return sessionCreateRequest{}, fail(CodeInvalidField)
	}
	return sessionCreateRequest{SessionID: id}, nil
}

// chatRequest 是 POST /v1/chat 的解析结果。
type chatRequest struct {
	SessionID string
	Message   string
}

// parseChat 严格解析 chat 请求：session_id 与 message 都必填，不 trim、不归一化。
func parseChat(fields map[string]json.RawMessage) (chatRequest, error) {
	if err := rejectUnknownFields(fields, "session_id", "message"); err != nil {
		return chatRequest{}, err
	}
	sessionID, present, err := stringField(fields, "session_id")
	if err != nil {
		return chatRequest{}, err
	}
	if !present {
		return chatRequest{}, fail(CodeMissingField)
	}
	message, present, err := stringField(fields, "message")
	if err != nil {
		return chatRequest{}, err
	}
	if !present {
		return chatRequest{}, fail(CodeMissingField)
	}
	if !validSessionID(sessionID) {
		return chatRequest{}, fail(CodeInvalidField)
	}
	if !validMessage(message) {
		return chatRequest{}, fail(CodeInvalidField)
	}
	return chatRequest{SessionID: sessionID, Message: message}, nil
}

// writeRequestError 把解析阶段错误写成对应封套；非解析错误按 internal 兜底。
func writeRequestError(w http.ResponseWriter, err error) {
	var parseErr *requestError
	if errors.As(err, &parseErr) {
		writeError(w, parseErr.code)
		return
	}
	writeError(w, CodeInternal)
}
