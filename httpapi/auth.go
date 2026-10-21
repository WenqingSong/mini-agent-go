package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// bearerPrefix 是 Authorization 头中唯一的认证方案前缀。
const bearerPrefix = "Bearer "

// authorized 判断请求是否通过认证。
//
// 未配置凭证时（只允许回环监听，由 Config.Validate 保证）不要求认证。
// 认证必须在读取请求体之前完成：未认证请求不解析 body、不调用 Runner、不写会话。
func (s *Server) authorized(r *http.Request) bool {
	expected := s.cfg.AuthToken
	if expected == "" {
		return true
	}
	provided, ok := bearerCredential(r.Header.Get("Authorization"))
	if !ok {
		return false
	}
	return equalCredential(provided, expected)
}

// bearerCredential 从 Authorization 头解析 Bearer 凭证。
//
// 方案名大小写不敏感（RFC 7235），凭证本身保持原样：不 trim、不改写。
func bearerCredential(header string) (string, bool) {
	if len(header) <= len(bearerPrefix) {
		return "", false
	}
	if !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := header[len(bearerPrefix):]
	if token == "" {
		return "", false
	}
	return token, true
}

// equalCredential 以常量时间比较凭证。
//
// 先对两侧取 SHA-256 摘要再比较定长摘要，使比较耗时与凭证长度、公共前缀无关，
// 不因长度差异或提前失配产生可测量的时间差。
func equalCredential(provided, expected string) bool {
	providedHash := sha256.Sum256([]byte(provided))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(providedHash[:], expectedHash[:]) == 1
}
