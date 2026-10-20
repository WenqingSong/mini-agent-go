package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// ErrorCode 是 HTTP 对外协议的错误码，属于本协议内的封闭枚举。
//
// 协议码与内部 core.ErrorKind 相互独立：公开协议不随内部错误种类演进漂移，
// 运行期 core.KindInvalidArgument 也不会被误映射成 400（见 errorCodeOf）。
type ErrorCode string

const (
	// CodeInvalidJSON 表示请求体不是单个合法 JSON 对象。
	CodeInvalidJSON ErrorCode = "invalid_json"
	// CodeUnknownField 表示出现契约外字段。
	CodeUnknownField ErrorCode = "unknown_field"
	// CodeMissingField 表示必填字段缺失。
	CodeMissingField ErrorCode = "missing_field"
	// CodeInvalidField 表示字段类型、长度、字符集或 UTF-8 非法。
	CodeInvalidField ErrorCode = "invalid_field"
	// CodeUnsupportedMediaType 表示 Content-Type 不是 application/json。
	CodeUnsupportedMediaType ErrorCode = "unsupported_media_type"
	// CodePayloadTooLarge 表示请求体超过大小上限。
	CodePayloadTooLarge ErrorCode = "payload_too_large"
	// CodeUnauthorized 表示凭证缺失或错误（两者必须同码）。
	CodeUnauthorized ErrorCode = "unauthorized"
	// CodeNotFound 表示未知路径。
	CodeNotFound ErrorCode = "not_found"
	// CodeMethodNotAllowed 表示已知路径的方法不符。
	CodeMethodNotAllowed ErrorCode = "method_not_allowed"
	// CodeSessionNotFound 表示会话不存在。
	CodeSessionNotFound ErrorCode = "session_not_found"
	// CodeSessionExpired 表示会话已过期。
	CodeSessionExpired ErrorCode = "session_expired"
	// CodeSessionExists 表示同 ID 会话仍有效。
	CodeSessionExists ErrorCode = "session_exists"
	// CodeDeadlineExceeded 表示请求级超时。
	CodeDeadlineExceeded ErrorCode = "deadline_exceeded"
	// CodeCancelled 表示请求 context 被取消（连接仍可写时的 best-effort 响应）。
	CodeCancelled ErrorCode = "cancelled"
	// CodeProviderFailure 表示模型服务调用失败。
	CodeProviderFailure ErrorCode = "provider_failure"
	// CodeToolFailure 表示不可恢复的工具失败。
	CodeToolFailure ErrorCode = "tool_failure"
	// CodeLLMCallLimit 表示模型调用预算耗尽。
	CodeLLMCallLimit ErrorCode = "llm_call_limit"
	// CodeToolCallLimit 表示工具调用预算耗尽。
	CodeToolCallLimit ErrorCode = "tool_call_limit"
	// CodeInvalidPairing 表示工具调用与结果配对非法。
	CodeInvalidPairing ErrorCode = "invalid_pairing"
	// CodeInternal 表示其余内部错误。
	CodeInternal ErrorCode = "internal"
)

// errorMessages 是 code → 固定 message 的一对一常量表：消息不含任何动态内容。
var errorMessages = map[ErrorCode]string{
	CodeInvalidJSON:          "请求体必须是单个合法 JSON 对象",
	CodeUnknownField:         "请求包含契约外字段",
	CodeMissingField:         "缺少必填字段",
	CodeInvalidField:         "字段取值非法",
	CodeUnsupportedMediaType: "Content-Type 必须是 application/json",
	CodePayloadTooLarge:      "请求体超过大小上限",
	CodeUnauthorized:         "未通过认证",
	CodeNotFound:             "资源不存在",
	CodeMethodNotAllowed:     "请求方法不被允许",
	CodeSessionNotFound:      "会话不存在或已被清理",
	CodeSessionExpired:       "会话已过期",
	CodeSessionExists:        "同 ID 会话仍有效",
	CodeDeadlineExceeded:     "请求处理超时",
	CodeCancelled:            "请求已取消",
	CodeProviderFailure:      "模型服务调用失败",
	CodeToolFailure:          "工具执行失败",
	CodeLLMCallLimit:         "模型调用次数超出上限",
	CodeToolCallLimit:        "工具调用次数超出上限",
	CodeInvalidPairing:       "工具调用与结果配对非法",
	CodeInternal:             "服务器内部错误",
}

// errorStatuses 是 code → HTTP 状态码的唯一单点映射。
var errorStatuses = map[ErrorCode]int{
	CodeInvalidJSON:          http.StatusBadRequest,
	CodeUnknownField:         http.StatusBadRequest,
	CodeMissingField:         http.StatusBadRequest,
	CodeInvalidField:         http.StatusBadRequest,
	CodeUnsupportedMediaType: http.StatusUnsupportedMediaType,
	CodePayloadTooLarge:      http.StatusRequestEntityTooLarge,
	CodeUnauthorized:         http.StatusUnauthorized,
	CodeNotFound:             http.StatusNotFound,
	CodeMethodNotAllowed:     http.StatusMethodNotAllowed,
	CodeSessionNotFound:      http.StatusNotFound,
	CodeSessionExpired:       http.StatusNotFound,
	CodeSessionExists:        http.StatusConflict,
	CodeDeadlineExceeded:     http.StatusGatewayTimeout,
	CodeCancelled:            http.StatusRequestTimeout,
	CodeProviderFailure:      http.StatusBadGateway,
	CodeToolFailure:          http.StatusInternalServerError,
	CodeLLMCallLimit:         http.StatusTooManyRequests,
	CodeToolCallLimit:        http.StatusTooManyRequests,
	CodeInvalidPairing:       http.StatusInternalServerError,
	CodeInternal:             http.StatusInternalServerError,
}

// known 判断错误码是否属于封闭枚举；未知取值一律按 internal 处理。
func (c ErrorCode) known() bool {
	_, ok := errorMessages[c]
	return ok
}

// Status 返回错误码对应的 HTTP 状态码；未知错误码按 internal 处理。
func (c ErrorCode) Status() int {
	if !c.known() {
		return http.StatusInternalServerError
	}
	return errorStatuses[c]
}

// Message 返回错误码的固定文案；未知错误码按 internal 处理。
func (c ErrorCode) Message() string {
	if !c.known() {
		return errorMessages[CodeInternal]
	}
	return errorMessages[c]
}

// errorEnvelope 是全部错误响应的固定形状。
type errorEnvelope struct {
	Error errorDetail `json:"error"`
}

// errorDetail 只承载稳定 code 与固定 message，不含路径、堆栈、Provider 载荷或凭据。
type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeError 按单点映射写出错误封套。
func writeError(w http.ResponseWriter, code ErrorCode) {
	if !code.known() {
		code = CodeInternal
	}
	writeJSON(w, code.Status(), errorEnvelope{Error: errorDetail{
		Code:    string(code),
		Message: code.Message(),
	}})
}

// errorCodeOf 把来自会话层或运行层的结果映射为协议错误码。
//
// 判定优先级固定：先会话哨兵（errors.Is）→ 再 context 终态 → 再 core.KindOf → 最后兜底 internal。
// 会话错误只能按哨兵分类，不能按错误文本；core.KindInvalidArgument 一律 internal，
// 因为它既可能来自调用参数，也可能来自会话错误，只有哨兵能区分。
func errorCodeOf(err error) ErrorCode {
	if err == nil {
		return CodeInternal
	}

	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		return CodeSessionNotFound
	case errors.Is(err, session.ErrSessionExpired):
		return CodeSessionExpired
	case errors.Is(err, session.ErrSessionExists):
		return CodeSessionExists
	}

	if errors.Is(err, context.DeadlineExceeded) {
		return CodeDeadlineExceeded
	}
	if errors.Is(err, context.Canceled) {
		return CodeCancelled
	}

	switch core.KindOf(err) {
	case core.KindDeadlineExceeded:
		return CodeDeadlineExceeded
	case core.KindCancelled:
		return CodeCancelled
	case core.KindProviderFailure:
		return CodeProviderFailure
	case core.KindToolFailure:
		return CodeToolFailure
	case core.KindLLMCallLimit:
		return CodeLLMCallLimit
	case core.KindToolCallLimit:
		return CodeToolCallLimit
	case core.KindInvalidPairing:
		return CodeInvalidPairing
	default:
		return CodeInternal
	}
}

// writeJSON 写出固定结构的 JSON 响应。
//
// 先编码再写头，避免编码失败时已经发出 2xx 状态；编码失败只可能来自本包固定结构，
// 此时退化为 internal 封套，绝不返回空响应或成功状态。
func writeJSON(w http.ResponseWriter, status int, payload any) {
	body, err := json.Marshal(payload)
	if err != nil {
		status = http.StatusInternalServerError
		body, _ = json.Marshal(errorEnvelope{Error: errorDetail{
			Code:    string(CodeInternal),
			Message: CodeInternal.Message(),
		}})
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
