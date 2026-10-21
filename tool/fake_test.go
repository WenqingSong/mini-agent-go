package tool

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// fakeTool 是测试用工具：记录真实调用次数，执行行为由注入的 fn 决定。
type fakeTool struct {
	mu          sync.Mutex
	name        string
	description string
	parameters  json.RawMessage
	fn          func(ctx context.Context, req Request) (core.ToolResult, error)
	calls       atomic.Int64
}

// newFakeTool 返回参数 Schema 为空对象、默认返回成功结果的测试工具。
func newFakeTool(name string) *fakeTool {
	return &fakeTool{
		name:        name,
		description: name + " 测试工具",
		parameters:  json.RawMessage(emptyObjectSchema),
	}
}

// withParameters 替换工具声明的 Parameters，用于注册期 Schema 用例。
func (f *fakeTool) withParameters(raw string) *fakeTool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.parameters = json.RawMessage(raw)
	return f
}

// withFunc 替换执行行为，用于错误、panic、阻塞等场景。
func (f *fakeTool) withFunc(fn func(ctx context.Context, req Request) (core.ToolResult, error)) *fakeTool {
	f.fn = fn
	return f
}

// Spec 每次返回当前定义，便于验证注册表保存的是注册时的深拷贝。
func (f *fakeTool) Spec() core.ToolSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	return core.ToolSpec{
		Name:        f.name,
		Description: f.description,
		Parameters:  append([]byte(nil), f.parameters...),
	}
}

// Execute 记录调用次数后交给注入行为执行。
func (f *fakeTool) Execute(ctx context.Context, req Request) (core.ToolResult, error) {
	f.calls.Add(1)
	if f.fn != nil {
		return f.fn(ctx, req)
	}
	return core.ToolResult{Content: "ok"}, nil
}

// callCount 返回工具实现被真实调用的次数（INV-102 / INV-104 的计数证据）。
func (f *fakeTool) callCount() int64 {
	return f.calls.Load()
}

// newBlockingTool 返回忽略 Context、直到 release 关闭才返回的工具，用于制造工具级超时。
func newBlockingTool(name string, release <-chan struct{}) *fakeTool {
	return newFakeTool(name).withFunc(func(context.Context, Request) (core.ToolResult, error) {
		<-release
		return core.ToolResult{Content: "迟到的结果"}, nil
	})
}

// toolRequest 构造一次工具执行请求。
func toolRequest(runID, callID, name, args string) Request {
	var arguments json.RawMessage
	if args != "" {
		arguments = json.RawMessage(args)
	}
	return Request{
		ExecutionRef: core.ExecutionRef{SessionID: "session-1", RunID: runID},
		Call:         core.ToolCall{ID: callID, Name: name, Arguments: arguments},
	}
}

// newTestExecutor 注册工具并构造执行器。
func newTestExecutor(t *testing.T, timeout time.Duration, tools ...Tool) *Executor {
	t.Helper()
	registry := NewMapRegistry()
	for _, tl := range tools {
		if err := registry.Register(tl); err != nil {
			t.Fatalf("注册工具 %q 失败：%v", tl.Spec().Name, err)
		}
	}
	executor, err := NewExecutor(ExecutorOptions{Registry: registry, Timeout: timeout})
	if err != nil {
		t.Fatalf("构造执行器失败：%v", err)
	}
	return executor
}

// executeWithWatchdog 在 watchdog 内执行一次工具调用，证明调用方不会被永久阻塞。
func executeWithWatchdog(t *testing.T, executor *Executor, ctx context.Context, req Request) (core.ToolResult, error) {
	t.Helper()

	type outcome struct {
		result core.ToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executor.Execute(ctx, req)
		done <- outcome{result: result, err: err}
	}()

	select {
	case out := <-done:
		return out.result, out.err
	case <-time.After(2 * time.Second):
		t.Fatalf("工具执行未在 watchdog 内返回（CallID=%q）", req.Call.ID)
		return core.ToolResult{}, nil
	}
}

// expectErrorKind 断言错误种类。
func expectErrorKind(t *testing.T, err error, want core.ErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误种类 %s，实际没有错误", want)
	}
	if got := core.KindOf(err); got != want {
		t.Fatalf("错误种类不符：期望 %s，实际 %s（%v）", want, got, err)
	}
}

// waitUntil 在窗口内轮询条件，用于时间相关断言的收敛判断。
func waitUntil(t *testing.T, window time.Duration, condition func() bool, message string) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s（等待 %s 仍未满足）", message, window)
}
