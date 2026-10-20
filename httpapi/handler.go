package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

// identifierBytes 是服务端生成标识的随机字节数（16 字节 → 32 位小写 hex）。
const identifierBytes = 16

// healthResponse 是 GET /healthz 的固定响应：不含配置、密钥、版本或内部状态。
type healthResponse struct {
	Status string `json:"status"`
}

// sessionResponse 是 POST /v1/sessions 的成功响应。
type sessionResponse struct {
	SessionID string `json:"session_id"`
}

// chatResponse 是 POST /v1/chat 的成功响应。
//
// 只包含本轮最终文本与调用计数：不返回消息序列、Provider 原始载荷、token 用量或系统提示。
type chatResponse struct {
	SessionID string `json:"session_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Reply     string `json:"reply"`
	LLMCalls  int    `json:"llm_calls"`
	ToolCalls int    `json:"tool_calls"`
}

// handleHealthz 处理免认证健康检查：不探测 Session / Provider，不读取请求体。
func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}

// handleCreateSession 处理 POST /v1/sessions。
//
// 处理顺序：认证 → Content-Type → 有界读取与严格解析 → 创建会话。
// 会话 ID 缺失时由服务端生成；同 ID 仍有效时按会话哨兵返回 409。
func (s *Server) handleCreateSession(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, CodeUnauthorized)
		return
	}
	if !mediaTypeAllowed(r.Header.Get("Content-Type")) {
		writeError(w, CodeUnsupportedMediaType)
		return
	}
	fields, err := readJSONObject(w, r, s.cfg.MaxRequestBytes)
	if err != nil {
		writeRequestError(w, err)
		return
	}
	request, err := parseSessionCreate(fields)
	if err != nil {
		writeRequestError(w, err)
		return
	}

	sessionID := request.SessionID
	if sessionID == "" {
		sessionID, err = newIdentifier()
		if err != nil {
			writeError(w, CodeInternal)
			return
		}
	}
	if _, err := s.deps.Sessions.Create(r.Context(), sessionID); err != nil {
		writeError(w, errorCodeOf(err))
		return
	}
	writeJSON(w, http.StatusCreated, sessionResponse{SessionID: sessionID})
}

// handleChat 处理 POST /v1/chat。
//
// 处理顺序固定：认证 → Content-Type → 有界读取与严格解析 → 会话预检 →
// 派生请求超时 context → 调用 Runner → 结果映射。
//
// 会话预检不调用 Runner（未知会话不执行 Run）；Runner 返回的会话哨兵按同一套
// 哨兵分类映射，用于防御预检与执行之间的 TOCTOU 变化。
func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	if !s.authorized(r) {
		writeError(w, CodeUnauthorized)
		return
	}
	if !mediaTypeAllowed(r.Header.Get("Content-Type")) {
		writeError(w, CodeUnsupportedMediaType)
		return
	}
	fields, err := readJSONObject(w, r, s.cfg.MaxRequestBytes)
	if err != nil {
		writeRequestError(w, err)
		return
	}
	request, err := parseChat(fields)
	if err != nil {
		writeRequestError(w, err)
		return
	}

	// 预检失败即为最终结论：不执行 Run，也不产生任何会话写入。
	if _, err := s.deps.Sessions.Get(r.Context(), request.SessionID); err != nil {
		writeError(w, errorCodeOf(err))
		return
	}

	runID, err := newIdentifier()
	if err != nil {
		writeError(w, CodeInternal)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.RequestTimeout)
	defer cancel()

	outcome, err := s.deps.Runner.Run(ctx, RunRequest{
		SessionID: request.SessionID,
		RunID:     runID,
		Message:   request.Message,
	})
	if err != nil {
		// 未形成 Run 结论：会话哨兵、ctx 终态或基础设施失败。
		writeError(w, errorCodeOf(err))
		return
	}
	if code, success := outcome.checkedCode(); !success {
		writeError(w, code)
		return
	}

	writeJSON(w, http.StatusOK, chatResponse{
		SessionID: request.SessionID,
		RunID:     runID,
		Status:    string(outcome.Status),
		Reply:     outcome.Reply,
		LLMCalls:  outcome.LLMCalls,
		ToolCalls: outcome.ToolCalls,
	})
}

// newIdentifier 生成 32 位小写 hex 的随机标识，用于会话 ID 与 run_id。
func newIdentifier() (string, error) {
	buffer := make([]byte, identifierBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
