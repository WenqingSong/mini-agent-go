package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// TestErrorCodeStatusMapping 逐一核对封闭错误码枚举的 code → status → message 单点映射。
func TestErrorCodeStatusMapping(t *testing.T) {
	cases := []struct {
		code   ErrorCode
		status int
	}{
		{CodeInvalidJSON, http.StatusBadRequest},
		{CodeUnknownField, http.StatusBadRequest},
		{CodeMissingField, http.StatusBadRequest},
		{CodeInvalidField, http.StatusBadRequest},
		{CodeUnsupportedMediaType, http.StatusUnsupportedMediaType},
		{CodePayloadTooLarge, http.StatusRequestEntityTooLarge},
		{CodeUnauthorized, http.StatusUnauthorized},
		{CodeNotFound, http.StatusNotFound},
		{CodeMethodNotAllowed, http.StatusMethodNotAllowed},
		{CodeForbiddenOrigin, http.StatusForbidden},
		{CodeSessionNotFound, http.StatusNotFound},
		{CodeSessionExpired, http.StatusNotFound},
		{CodeSessionExists, http.StatusConflict},
		{CodeDeadlineExceeded, http.StatusGatewayTimeout},
		{CodeCancelled, http.StatusRequestTimeout},
		{CodeProviderFailure, http.StatusBadGateway},
		{CodeToolFailure, http.StatusInternalServerError},
		{CodeLLMCallLimit, http.StatusTooManyRequests},
		{CodeToolCallLimit, http.StatusTooManyRequests},
		{CodeInvalidPairing, http.StatusInternalServerError},
		{CodeInternal, http.StatusInternalServerError},
	}

	if len(cases) != len(errorStatuses) {
		t.Fatalf("错误码覆盖不完整：测试列出 %d 个，实现映射 %d 个", len(cases), len(errorStatuses))
	}
	for _, item := range cases {
		if got := item.code.Status(); got != item.status {
			t.Errorf("错误码 %q 的状态码 = %d，期望 %d", item.code, got, item.status)
		}
		if message := item.code.Message(); message == "" {
			t.Errorf("错误码 %q 缺少固定 message", item.code)
		}
		if _, ok := errorStatuses[item.code]; !ok {
			t.Errorf("错误码 %q 未纳入单点映射表", item.code)
		}
	}

	unknown := ErrorCode("not_a_code")
	if got := unknown.Status(); got != http.StatusInternalServerError {
		t.Errorf("未知错误码状态码 = %d，期望 500", got)
	}
	if got := unknown.Message(); got != errorMessages[CodeInternal] {
		t.Errorf("未知错误码 message = %q，期望 internal 的固定文案", got)
	}
}

// TestWriteErrorEnvelope 核对错误封套形状：只含稳定 code 与固定 message。
func TestWriteErrorEnvelope(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeError(recorder, CodeProviderFailure)

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("状态码 = %d，期望 502", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/json") {
		t.Fatalf("Content-Type = %q，期望 application/json", got)
	}

	body := recorder.Body.String()
	var payload struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &payload); err != nil {
		t.Fatalf("错误响应不是合法 JSON：%v（body=%s）", err, body)
	}
	if payload.Error.Code != string(CodeProviderFailure) {
		t.Errorf("code = %q，期望 %q", payload.Error.Code, CodeProviderFailure)
	}
	if payload.Error.Message != CodeProviderFailure.Message() {
		t.Errorf("message = %q，期望固定文案 %q", payload.Error.Message, CodeProviderFailure.Message())
	}
	for _, forbidden := range []string{"stack", "goroutine", "provider_payload", "/root/", "Authorization"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("错误封套泄漏内部细节 %q：%s", forbidden, body)
		}
	}

	// 未知错误码不得输出空 code 或空 message。
	unknown := httptest.NewRecorder()
	writeError(unknown, ErrorCode("bogus"))
	if !strings.Contains(unknown.Body.String(), string(CodeInternal)) {
		t.Errorf("未知错误码应退化为 internal 封套，实际：%s", unknown.Body.String())
	}
}

// TestErrorCodeOf 核对错误分类优先级：会话哨兵 → 链中最外层 RunError.Kind → 裸 ctx 兜底 → internal。
func TestErrorCodeOf(t *testing.T) {
	ctx := context.Background()

	now := time.Unix(0, 0)
	clock := func() time.Time { return now }
	ttlManager := session.NewManager(session.ManagerConfig{TTL: time.Second, Now: clock})
	if _, err := ttlManager.Create(ctx, "expiring"); err != nil {
		t.Fatalf("创建会话失败：%v", err)
	}
	now = now.Add(2 * time.Second)

	_, expiredErr := ttlManager.Get(ctx, "expiring")
	if !errors.Is(expiredErr, session.ErrSessionExpired) {
		t.Fatalf("期望会话过期哨兵，实际：%v", expiredErr)
	}

	manager := session.NewManager(session.ManagerConfig{})
	notFoundErr := func() error {
		_, err := manager.Get(ctx, "missing")
		return err
	}()

	if _, err := manager.Create(ctx, "dup"); err != nil {
		t.Fatalf("创建会话失败：%v", err)
	}
	existsErr := func() error {
		_, err := manager.Create(ctx, "dup")
		return err
	}()

	cases := []struct {
		name string
		err  error
		want ErrorCode
	}{
		{"nil", nil, CodeInternal},
		{"普通错误", errors.New("boom"), CodeInternal},
		{"invalid_argument 不得映射 400", core.NewError(core.KindInvalidArgument, "op", "参数非法"), CodeInternal},
		{"provider_failure", core.NewError(core.KindProviderFailure, "op", "上游失败"), CodeProviderFailure},
		{"tool_failure", core.NewError(core.KindToolFailure, "op", "工具失败"), CodeToolFailure},
		{"llm_call_limit", core.NewError(core.KindLLMCallLimit, "op", "预算"), CodeLLMCallLimit},
		{"tool_call_limit", core.NewError(core.KindToolCallLimit, "op", "预算"), CodeToolCallLimit},
		{"invalid_pairing", core.NewError(core.KindInvalidPairing, "op", "配对"), CodeInvalidPairing},
		{"cancelled", core.NewError(core.KindCancelled, "op", "取消"), CodeCancelled},
		{"deadline_exceeded", core.NewError(core.KindDeadlineExceeded, "op", "超时"), CodeDeadlineExceeded},
		{"包装的 provider_failure", fmt.Errorf("外层: %w", core.NewError(core.KindProviderFailure, "op", "上游")), CodeProviderFailure},
		{"ctx 取消", context.Canceled, CodeCancelled},
		{"ctx 超时", context.DeadlineExceeded, CodeDeadlineExceeded},
		{"会话不存在", notFoundErr, CodeSessionNotFound},
		{"会话过期", expiredErr, CodeSessionExpired},
		{"会话重复创建", existsErr, CodeSessionExists},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			if got := errorCodeOf(item.err); got != item.want {
				t.Fatalf("errorCodeOf(%v) = %q，期望 %q", item.err, got, item.want)
			}
		})
	}
}
