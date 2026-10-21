package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
)

// startServer 在回环随机端口上启动服务，返回服务器、基础 URL 与 Serve 结果通道。
func startServer(t *testing.T, cfg httpapi.Config, deps httpapi.Deps) (*httpapi.Server, string, <-chan error) {
	t.Helper()
	server, err := httpapi.New(cfg, deps)
	if err != nil {
		t.Fatalf("构造服务失败：%v", err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve() }()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if addr := server.Addr(); addr != "" {
			return server, "http://" + addr, served
		}
		time.Sleep(time.Millisecond)
	}
	_ = server.Close()
	t.Fatal("服务未在限定时间内进入监听")
	return nil, "", nil
}

// TestServeReadyHealthzAndGracefulShutdown 核对就绪后可服务、关闭后拒绝新连接、重复关闭幂等。
func TestServeReadyHealthzAndGracefulShutdown(t *testing.T) {
	manager := newManager()
	server, baseURL, served := startServer(t, testConfig(""), httpapi.Deps{
		Sessions: manager,
		Runner:   &fakeRunner{outcome: httpapi.RunOutcome{Status: core.RunStatusCompleted}},
	})
	client := &http.Client{Timeout: 3 * time.Second}

	health := doRequest(t, client, http.MethodGet, baseURL+"/healthz", "", "", "")
	if health.status != http.StatusOK {
		t.Fatalf("就绪后 /healthz 应返回 200，实际 %d（body=%s）", health.status, health.body)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("优雅关闭返回错误：%v", err)
	}
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("关闭后 Serve 应返回 nil，实际：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后 Serve 未返回")
	}

	if _, err := client.Get(baseURL + "/healthz"); err == nil {
		t.Fatal("关闭后新连接应被拒绝")
	}

	// 重复关闭幂等、不 panic。
	if err := server.Shutdown(context.Background()); err != nil {
		t.Fatalf("重复 Shutdown 应幂等返回 nil，实际：%v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("关闭后 Close 应幂等返回 nil，实际：%v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("重复 Close 应幂等返回 nil，实际：%v", err)
	}
}

// TestShutdownWaitsForInflightRequest 核对宽限期内在途请求完成并返回正确结果。
func TestShutdownWaitsForInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	runner := &fakeRunner{onRun: func(ctx context.Context, _ httpapi.RunRequest) (httpapi.RunOutcome, error) {
		close(started)
		select {
		case <-release:
			return httpapi.RunOutcome{Status: core.RunStatusCompleted, Reply: "完成"}, nil
		case <-ctx.Done():
			return httpapi.RunOutcome{Status: core.RunStatusCancelled, Err: core.NewError(core.KindCancelled, "test", "取消")}, nil
		}
	}}
	server, baseURL, served := startServer(t, testConfig(""), httpapi.Deps{Sessions: newManager(), Runner: runner})
	client := &http.Client{Timeout: 5 * time.Second}

	created := doRequest(t, client, http.MethodPost, baseURL+"/v1/sessions", "application/json", `{"session_id":"s-1"}`, "")
	if created.status != http.StatusCreated {
		t.Fatalf("创建会话失败：%d %s", created.status, created.body)
	}

	type callResult struct {
		status int
		body   []byte
		err    error
	}
	requestDone := make(chan callResult, 1)
	go func() {
		response, err := client.Post(baseURL+"/v1/chat", "application/json",
			strings.NewReader(`{"session_id":"s-1","message":"慢请求"}`))
		if err != nil {
			requestDone <- callResult{err: err}
			return
		}
		defer response.Body.Close()
		payload, _ := io.ReadAll(response.Body)
		requestDone <- callResult{status: response.StatusCode, body: payload}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未进入 Runner")
	}

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		shutdownDone <- server.Shutdown(shutdownCtx)
	}()

	// 在宽限期内释放在途请求，响应必须正常返回。
	time.Sleep(20 * time.Millisecond)
	close(release)

	select {
	case result := <-requestDone:
		if result.err != nil {
			t.Fatalf("宽限期内在途请求应正常完成，实际错误：%v", result.err)
		}
		if result.status != http.StatusOK {
			t.Fatalf("在途请求状态码 = %d，期望 200（body=%s）", result.status, result.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("在途请求未在宽限期内完成")
	}

	select {
	case err := <-shutdownDone:
		if err != nil {
			t.Fatalf("宽限期内完成全部请求的关闭应返回 nil，实际：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭未返回")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("关闭后 Serve 应返回 nil，实际：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后 Serve 未返回")
	}
}

// TestShutdownCancelsInflightRunAfterGrace 核对宽限期满后在途 Run 被取消且关闭不遗留执行。
func TestShutdownCancelsInflightRunAfterGrace(t *testing.T) {
	started := make(chan struct{})
	observed := make(chan error, 1)
	runner := &fakeRunner{onRun: func(ctx context.Context, _ httpapi.RunRequest) (httpapi.RunOutcome, error) {
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return httpapi.RunOutcome{Status: core.RunStatusCancelled, Err: core.NewError(core.KindCancelled, "test", "关闭取消")}, nil
	}}
	manager := newManager()
	seedSession(t, manager, "s-1")
	cfg := testConfig("")
	cfg.ShutdownGrace = 50 * time.Millisecond
	server, baseURL, served := startServer(t, cfg, httpapi.Deps{Sessions: manager, Runner: runner})
	client := &http.Client{Timeout: 5 * time.Second}

	go func() {
		_, _ = client.Post(baseURL+"/v1/chat", "application/json", strings.NewReader(`{"session_id":"s-1","message":"阻塞"}`))
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未进入 Runner")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	begin := time.Now()
	shutdownErr := server.Shutdown(shutdownCtx)
	elapsed := time.Since(begin)

	if shutdownErr == nil {
		t.Fatal("宽限期满的关闭应返回超时错误")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("关闭耗时 %s，超出限定时间", elapsed)
	}

	select {
	case ctxErr := <-observed:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Fatalf("在途 Run 的 ctx 应被取消，实际：%v", ctxErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("宽限期满后在途 Run 未被取消")
	}

	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("关闭后 Serve 应返回 nil，实际：%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("关闭后 Serve 未返回")
	}

	if err := server.Close(); err != nil {
		t.Fatalf("关闭后的 Close 应幂等，实际：%v", err)
	}
}

// TestClientDisconnectCancelsRun 核对客户端断开真实穿透到 Run（AC-007）：不承诺响应码、不留部分提交。
func TestClientDisconnectCancelsRun(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1", userTexture("已提交用户"), assistantTexture("已提交回答"))
	before := snapshot(t, manager, "s-1")

	started := make(chan struct{})
	observed := make(chan error, 1)
	runner := &fakeRunner{onRun: func(ctx context.Context, _ httpapi.RunRequest) (httpapi.RunOutcome, error) {
		close(started)
		<-ctx.Done()
		observed <- ctx.Err()
		return httpapi.RunOutcome{Status: core.RunStatusCancelled, Err: core.NewError(core.KindCancelled, "test", "客户端取消")}, nil
	}}
	_, baseURL, _ := startServer(t, testConfig(""), httpapi.Deps{Sessions: manager, Runner: runner})
	client := &http.Client{Timeout: 5 * time.Second}

	requestCtx, cancelRequest := context.WithCancel(context.Background())
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, baseURL+"/v1/chat",
		strings.NewReader(`{"session_id":"s-1","message":"断开连接"}`))
	if err != nil {
		t.Fatalf("构造请求失败：%v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	done := make(chan struct{})
	go func() {
		defer close(done)
		response, err := client.Do(request)
		if err == nil {
			_ = response.Body.Close()
		}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("请求未进入 Runner")
	}
	cancelRequest()

	select {
	case ctxErr := <-observed:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Fatalf("Run 的 ctx 应被取消，实际：%v", ctxErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("客户端断开未穿透到 Run")
	}
	<-done

	after := snapshot(t, manager, "s-1")
	if len(after.Messages) != len(before.Messages) {
		t.Fatalf("断开路径产生了会话写入：before=%d after=%d", len(before.Messages), len(after.Messages))
	}
	if err := core.ValidateMessages(after.Messages); err != nil {
		t.Fatalf("断开路径后历史不合法：%v", err)
	}
}

// TestRequestTimeoutReturns504 核对请求级超时 → 504 deadline_exceeded，且与取消可区分。
func TestRequestTimeoutReturns504(t *testing.T) {
	observed := make(chan error, 1)
	runner := &fakeRunner{onRun: func(ctx context.Context, _ httpapi.RunRequest) (httpapi.RunOutcome, error) {
		<-ctx.Done()
		observed <- ctx.Err()
		return httpapi.RunOutcome{
			Status: core.RunStatusCancelled,
			Err:    core.NewError(core.KindDeadlineExceeded, "test", "请求超时"),
		}, nil
	}}
	cfg := testConfig("")
	cfg.RequestTimeout = 30 * time.Millisecond
	_, baseURL, _ := startServer(t, cfg, httpapi.Deps{Sessions: newManager(), Runner: runner})
	client := &http.Client{Timeout: 3 * time.Second}

	// 预检需要一个存在的会话。
	created := doRequest(t, client, http.MethodPost, baseURL+"/v1/sessions", "application/json", `{"session_id":"s-1"}`, "")
	if created.status != http.StatusCreated {
		t.Fatalf("创建会话失败：%d %s", created.status, created.body)
	}

	result := doRequest(t, client, http.MethodPost, baseURL+"/v1/chat", "application/json", `{"session_id":"s-1","message":"超时"}`, "")
	var payload errorPayload
	if err := json.Unmarshal(result.body, &payload); err != nil {
		t.Fatalf("响应不是合法 JSON：%v（body=%s）", err, result.body)
	}
	if result.status != http.StatusGatewayTimeout || payload.Error.Code != string(httpapi.CodeDeadlineExceeded) {
		t.Fatalf("超时响应 = (%d,%q)，期望 (504,deadline_exceeded)", result.status, payload.Error.Code)
	}

	select {
	case ctxErr := <-observed:
		if !errors.Is(ctxErr, context.DeadlineExceeded) {
			t.Fatalf("超时路径的 ctx 错误应为 DeadlineExceeded，实际：%v", ctxErr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("超时未穿透到 Run")
	}
}

// TestServeFailsWhenAddressInUse 核对监听失败时返回错误且不进入服务状态。
func TestServeFailsWhenAddressInUse(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("占用端口失败：%v", err)
	}
	defer listener.Close()

	cfg := testConfig("")
	cfg.ListenAddr = listener.Addr().String()
	server, err := httpapi.New(cfg, httpapi.Deps{Sessions: newManager(), Runner: &fakeRunner{}})
	if err != nil {
		t.Fatalf("构造服务失败：%v", err)
	}
	if err := server.Serve(); err == nil {
		t.Fatal("端口被占用时 Serve 应返回错误")
	}
	if addr := server.Addr(); addr != "" {
		t.Fatalf("监听失败后不应报告监听地址，实际 %q", addr)
	}
}
