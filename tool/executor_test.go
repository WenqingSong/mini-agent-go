package tool

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TestNewExecutorRejectsNilRegistry 覆盖装配期契约错误。
func TestNewExecutorRejectsNilRegistry(t *testing.T) {
	executor, err := NewExecutor(ExecutorOptions{})
	if executor != nil {
		t.Fatal("Registry 为空时不应返回执行器")
	}
	expectErrorKind(t, err, core.KindInvalidArgument)
}

// TestExecutorSuccess 覆盖 AC-001 / INV-103：结果保留请求 CallID 与 Name，且可参与配对。
func TestExecutorSuccess(t *testing.T) {
	// 工具自填的 CallID / Name 不可信（含未填 CallID），执行器必须归一化。
	cases := []struct {
		name   string
		filled core.ToolResult
	}{
		{
			name:   "工具自填错误 CallID 与 Name",
			filled: core.ToolResult{CallID: "工具自填 ID", Name: "工具自填名称", Content: "你好"},
		},
		{
			name:   "工具未填 CallID",
			filled: core.ToolResult{Content: "你好"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			echo := newFakeTool("echo").withFunc(func(context.Context, Request) (core.ToolResult, error) {
				return tc.filled, nil
			})
			executor := newTestExecutor(t, time.Second, echo)

			result, err := executor.Execute(context.Background(), toolRequest("run-1", "call-1", "echo", `{}`))
			if err != nil {
				t.Fatalf("执行不应返回错误：%v", err)
			}
			if result.CallID != "call-1" {
				t.Fatalf("CallID 必须归一化为请求值，实际 %q", result.CallID)
			}
			if result.Name != "echo" {
				t.Fatalf("Name 必须归一化为请求值，实际 %q", result.Name)
			}
			if result.IsError {
				t.Fatal("成功结果不应标记 IsError")
			}
			if result.Content != "你好" {
				t.Fatalf("Content 不应被改写，实际 %q", result.Content)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("成功结果应通过 ToolResult.Validate：%v", err)
			}
			if got := echo.callCount(); got != 1 {
				t.Fatalf("工具应被调用 1 次，实际 %d", got)
			}
		})
	}
}

// TestExecutorRejectsInvalidRequest 覆盖执行前拒绝（error 分支）：不调用工具、不占位。
func TestExecutorRejectsInvalidRequest(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	expiredCtx, expire := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer expire()

	cases := []struct {
		name       string
		ctx        context.Context
		req        Request
		wantKind   core.ErrorKind
		wantCallID string
	}{
		{
			name:       "CallID 为空",
			ctx:        context.Background(),
			req:        toolRequest("run-1", "", "demo", `{}`),
			wantKind:   core.KindInvalidPairing,
			wantCallID: "",
		},
		{
			name:       "RunID 为空",
			ctx:        context.Background(),
			req:        toolRequest("", "call-1", "demo", `{}`),
			wantKind:   core.KindInvalidArgument,
			wantCallID: "call-1",
		},
		{
			name:       "工具名称为空",
			ctx:        context.Background(),
			req:        toolRequest("run-1", "call-1", "", `{}`),
			wantKind:   core.KindInvalidArgument,
			wantCallID: "call-1",
		},
		{
			name:       "工具名称全空白",
			ctx:        context.Background(),
			req:        toolRequest("run-1", "call-1", "   ", `{}`),
			wantKind:   core.KindInvalidArgument,
			wantCallID: "call-1",
		},
		{
			name:       "父 Context 已取消",
			ctx:        cancelledCtx,
			req:        toolRequest("run-1", "call-1", "demo", `{}`),
			wantKind:   core.KindCancelled,
			wantCallID: "call-1",
		},
		{
			name:       "父 Context 已超时",
			ctx:        expiredCtx,
			req:        toolRequest("run-1", "call-1", "demo", `{}`),
			wantKind:   core.KindDeadlineExceeded,
			wantCallID: "call-1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			demo := newFakeTool("demo")
			executor := newTestExecutor(t, 0, demo)

			result, err := executor.Execute(tc.ctx, tc.req)
			expectErrorKind(t, err, tc.wantKind)
			if result != (core.ToolResult{}) {
				t.Fatalf("失败时不应返回结果，实际 %+v", result)
			}

			var runErr *core.RunError
			if !errors.As(err, &runErr) {
				t.Fatalf("错误必须是 *core.RunError，实际 %T", err)
			}
			if runErr.CallID != tc.wantCallID {
				t.Fatalf("RunError.CallID 应为 %q，实际 %q（%v）", tc.wantCallID, runErr.CallID, err)
			}
			if got := demo.callCount(); got != 0 {
				t.Fatalf("执行前拒绝不应调用工具实现，实际调用 %d 次", got)
			}
		})
	}
}

// TestExecutorRejectsBeforeToolExecution 覆盖未知工具与参数非法（IsError 分支，INV-102）。
func TestExecutorRejectsBeforeToolExecution(t *testing.T) {
	cases := []struct {
		name       string
		callName   string
		args       string
		wantDetail string
	}{
		{name: "未知工具", callName: "missing", args: `{}`, wantDetail: "未知工具"},
		{name: "参数非法 JSON", callName: "demo", args: `{"name":`, wantDetail: "参数不是合法 JSON"},
		{name: "参数非 object", callName: "demo", args: `[]`, wantDetail: "参数必须是 JSON object"},
		{name: "缺少必填字段", callName: "demo", args: `{"age":1}`, wantDetail: "$.name: 缺少必填字段"},
		{name: "字段类型不符", callName: "demo", args: `{"name":1,"age":1}`, wantDetail: "$.name: 期望 string，实际 number"},
		{name: "枚举取值不符", callName: "demo", args: `{"name":"a","age":1,"level":"mid"}`, wantDetail: "$.level: 不在枚举取值内"},
		{name: "未声明字段", callName: "demo", args: `{"name":"a","age":1,"extra":1}`, wantDetail: "$.extra: 未声明的字段"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			demo := newFakeTool("demo").withParameters(personSchema)
			executor := newTestExecutor(t, 0, demo)

			result, err := executor.Execute(context.Background(), toolRequest("run-1", "call-1", tc.callName, tc.args))
			if err != nil {
				t.Fatalf("拒绝路径应以 ToolResult 承载，不应返回 error：%v", err)
			}
			if !result.IsError {
				t.Fatal("拒绝路径必须标记 IsError=true")
			}
			if result.CallID != "call-1" {
				t.Fatalf("拒绝结果必须保留请求 CallID，实际 %q", result.CallID)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("拒绝结果应可通过 ToolResult.Validate：%v", err)
			}
			if !strings.Contains(result.Content, tc.wantDetail) {
				t.Fatalf("Content 应包含 %q，实际 %q", tc.wantDetail, result.Content)
			}
			if got := demo.callCount(); got != 0 {
				t.Fatalf("参数/查表失败时工具实现必须一次都不被调用，实际 %d 次", got)
			}
		})
	}
}

// TestExecutorToolError 覆盖工具返回 error：可恢复失败，保留 CallID。
func TestExecutorToolError(t *testing.T) {
	failing := newFakeTool("flaky").withFunc(func(context.Context, Request) (core.ToolResult, error) {
		return core.ToolResult{}, errors.New("上游不可用")
	})
	executor := newTestExecutor(t, time.Second, failing)

	result, err := executor.Execute(context.Background(), toolRequest("run-1", "call-1", "flaky", `{}`))
	if err != nil {
		t.Fatalf("工具 error 应转为可恢复结果，不应上抛：%v", err)
	}
	if !result.IsError {
		t.Fatal("工具 error 必须标记 IsError=true")
	}
	if result.CallID != "call-1" {
		t.Fatalf("结果必须保留 CallID，实际 %q", result.CallID)
	}
	if !strings.Contains(result.Content, "上游不可用") {
		t.Fatalf("Content 应包含原始错误文本，实际 %q", result.Content)
	}
	if got := failing.callCount(); got != 1 {
		t.Fatalf("工具应被调用 1 次，实际 %d", got)
	}
}

// TestExecutorToolPanic 覆盖 panic 隔离：转为 tool_failure、保留 CallID、不泄漏堆栈。
func TestExecutorToolPanic(t *testing.T) {
	boom := newFakeTool("boom").withFunc(func(context.Context, Request) (core.ToolResult, error) {
		panic("boom")
	})
	executor := newTestExecutor(t, time.Second, boom)

	result, err := executor.Execute(context.Background(), toolRequest("run-1", "call-1", "boom", `{}`))
	expectErrorKind(t, err, core.KindToolFailure)
	if result != (core.ToolResult{}) {
		t.Fatalf("panic 不应返回结果，实际 %+v", result)
	}

	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("错误必须是 *core.RunError，实际 %T", err)
	}
	if runErr.CallID != "call-1" {
		t.Fatalf("panic 错误必须保留 CallID，实际 %q", runErr.CallID)
	}
	if !strings.Contains(err.Error(), "panic") {
		t.Fatalf("错误文本应说明 panic，实际 %v", err)
	}
	for _, forbidden := range []string{"goroutine", "stack", "runtime/debug"} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("错误文本不得包含堆栈痕迹 %q：%v", forbidden, err)
		}
	}
	if got := boom.callCount(); got != 1 {
		t.Fatalf("工具应被调用 1 次，实际 %d", got)
	}
}

// TestExecutorToolTimeout 覆盖工具级局部超时：有限时间内返回 IsError，不映射为 CANCELLED。
func TestExecutorToolTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	slow := newBlockingTool("slow", release)
	executor := newTestExecutor(t, 20*time.Millisecond, slow)

	result, err := executeWithWatchdog(t, executor, context.Background(), toolRequest("run-1", "call-1", "slow", `{}`))
	if err != nil {
		t.Fatalf("工具级超时应以 IsError 结果承载，不应返回 error：%v", err)
	}
	if !result.IsError {
		t.Fatal("工具级超时不得当成成功结果")
	}
	if result.CallID != "call-1" {
		t.Fatalf("超时结果必须保留 CallID，实际 %q", result.CallID)
	}
	if !strings.Contains(result.Content, "超时") {
		t.Fatalf("Content 应说明超时，实际 %q", result.Content)
	}
	if got := slow.callCount(); got != 1 {
		t.Fatalf("工具应被调用 1 次，实际 %d", got)
	}
}

// TestExecutorParentDeadlineDuringExecution 覆盖整体超时：返回 deadline_exceeded，绝不伪装成功。
func TestExecutorParentDeadlineDuringExecution(t *testing.T) {
	release := make(chan struct{})
	defer close(release)

	slow := newBlockingTool("slow", release)
	executor := newTestExecutor(t, 0, slow)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	result, err := executeWithWatchdog(t, executor, ctx, toolRequest("run-1", "call-1", "slow", `{}`))
	expectErrorKind(t, err, core.KindDeadlineExceeded)
	if result != (core.ToolResult{}) {
		t.Fatalf("父 Context 超时不应返回结果，实际 %+v", result)
	}
}

// TestExecutorCancelDuringExecution 覆盖「取消优先于工具结果」：父 ctx 结束后丢弃工具产出。
func TestExecutorCancelDuringExecution(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})

	var once sync.Once
	slow := newFakeTool("slow").withFunc(func(context.Context, Request) (core.ToolResult, error) {
		once.Do(func() { close(started) })
		<-release
		return core.ToolResult{Content: "工具已经跑完"}, nil
	})
	executor := newTestExecutor(t, 0, slow)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	type outcome struct {
		result core.ToolResult
		err    error
	}
	done := make(chan outcome, 1)
	go func() {
		result, err := executor.Execute(ctx, toolRequest("run-1", "call-1", "slow", `{}`))
		done <- outcome{result: result, err: err}
	}()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("工具未在 watchdog 内开始执行")
	}

	cancel()
	close(release)

	select {
	case out := <-done:
		expectErrorKind(t, out.err, core.KindCancelled)
		if out.result != (core.ToolResult{}) {
			t.Fatalf("取消后必须丢弃工具结果，实际 %+v", out.result)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("取消后执行器未在 watchdog 内返回")
	}
}

// TestExecutorDedupWithinRun 覆盖 INV-104：同 Run 同 CallID 至多执行一次，重复提交显式拒绝。
func TestExecutorDedupWithinRun(t *testing.T) {
	echo := newFakeTool("echo")
	executor := newTestExecutor(t, 0, echo)
	ctx := context.Background()
	req := toolRequest("run-1", "call-1", "echo", `{}`)

	first, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("首次调用不应失败：%v", err)
	}
	if err := first.Validate(); err != nil {
		t.Fatalf("首次结果应可通过 ToolResult.Validate：%v", err)
	}

	second, err := executor.Execute(ctx, req)
	expectErrorKind(t, err, core.KindInvalidPairing)
	if second != (core.ToolResult{}) {
		t.Fatalf("重复调用不得复用首次结果，实际 %+v", second)
	}

	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("错误必须是 *core.RunError，实际 %T", err)
	}
	if runErr.CallID != "call-1" {
		t.Fatalf("重复调用错误必须携带请求 CallID，实际 %q", runErr.CallID)
	}
	if got := echo.callCount(); got != 1 {
		t.Fatalf("整个过程工具只应执行 1 次，实际 %d", got)
	}
}

// TestExecutorDedupScopeIsPerRun 覆盖去重范围：不同 RunID / 不同 CallID 互不干扰。
func TestExecutorDedupScopeIsPerRun(t *testing.T) {
	echo := newFakeTool("echo")
	executor := newTestExecutor(t, 0, echo)
	ctx := context.Background()

	requests := []Request{
		toolRequest("run-1", "call-1", "echo", `{}`),
		toolRequest("run-2", "call-1", "echo", `{}`), // 不同 Run，同 CallID
		toolRequest("run-1", "call-2", "echo", `{}`), // 同 Run，不同 CallID
	}
	for i, req := range requests {
		if _, err := executor.Execute(ctx, req); err != nil {
			t.Fatalf("第 %d 次调用不应被判为重复：%v", i+1, err)
		}
	}
	if got := echo.callCount(); got != int64(len(requests)) {
		t.Fatalf("不同 Run 或不同 CallID 应各执行一次，实际 %d 次", got)
	}
}

// TestExecutorDedupConcurrent 覆盖并发同键：只有一个 goroutine 执行，其余显式拒绝。
func TestExecutorDedupConcurrent(t *testing.T) {
	const goroutines = 16

	echo := newFakeTool("echo")
	executor := newTestExecutor(t, time.Second, echo)
	req := toolRequest("run-1", "call-1", "echo", `{}`)

	start := make(chan struct{})
	results := make(chan error, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := executor.Execute(context.Background(), req)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	success, duplicates := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case core.KindOf(err) == core.KindInvalidPairing:
			duplicates++
		default:
			t.Fatalf("并发同键出现非预期错误：%v", err)
		}
	}
	if success != 1 {
		t.Fatalf("并发同键应只有 1 次成功，实际 %d", success)
	}
	if duplicates != goroutines-1 {
		t.Fatalf("其余 %d 次应返回 invalid_pairing，实际 %d", goroutines-1, duplicates)
	}
	if got := echo.callCount(); got != 1 {
		t.Fatalf("并发同键不得产生重复副作用，实际执行 %d 次", got)
	}
}

// TestExecutorDedupAppliesToFailedFirstCall 覆盖「首次失败同样占位」：后续提交仍按重复调用拒绝。
func TestExecutorDedupAppliesToFailedFirstCall(t *testing.T) {
	executor := newTestExecutor(t, 0) // 空注册表，首次调用是未知工具
	ctx := context.Background()
	req := toolRequest("run-1", "call-1", "missing", `{}`)

	first, err := executor.Execute(ctx, req)
	if err != nil {
		t.Fatalf("未知工具应以 IsError 结果承载：%v", err)
	}
	if !first.IsError {
		t.Fatal("未知工具必须标记 IsError=true")
	}

	_, err = executor.Execute(ctx, req)
	expectErrorKind(t, err, core.KindInvalidPairing)
}

// TestExecutorTimeoutDoesNotGrowGoroutines 覆盖工具级超时后 goroutine 收敛（INV-105）。
func TestExecutorTimeoutDoesNotGrowGoroutines(t *testing.T) {
	const iterations = 30

	slow := newFakeTool("slow").withFunc(func(context.Context, Request) (core.ToolResult, error) {
		time.Sleep(5 * time.Millisecond)
		return core.ToolResult{Content: "迟到的结果"}, nil
	})
	executor := newTestExecutor(t, time.Millisecond, slow)

	baseline := runtime.NumGoroutine()
	for i := 0; i < iterations; i++ {
		result, err := executeWithWatchdog(t, executor, context.Background(),
			toolRequest("run-1", fmt.Sprintf("call-%02d", i), "slow", `{}`))
		if err != nil {
			t.Fatalf("第 %d 次超时应以 IsError 结果承载：%v", i+1, err)
		}
		if !result.IsError {
			t.Fatalf("第 %d 次超时不得当成成功", i+1)
		}
	}

	waitUntil(t, 3*time.Second, func() bool {
		return runtime.NumGoroutine() <= baseline+10
	}, "连续工具级超时后 goroutine 未在收敛窗口内回落")
}

// TestExecutorRejectsNestedConstraintsWithoutType 覆盖 CLEAN-001：子 Schema 未声明 type 时，
// 必填 / 类型 / 未声明字段约束仍必须生效，非法参数不得透传给工具实现。
func TestExecutorRejectsNestedConstraintsWithoutType(t *testing.T) {
	const schema = `{
	  "type":"object",
	  "properties":{
	    "address":{"properties":{"zip":{"type":"string"}},"required":["zip"],"additionalProperties":false}
	  }
	}`

	cases := []struct {
		name       string
		args       string
		wantDetail string
		wantCalled bool
	}{
		{name: "嵌套缺少必填字段", args: `{"address":{}}`, wantDetail: "$.address.zip: 缺少必填字段"},
		{name: "嵌套字段类型不符", args: `{"address":{"zip":1}}`, wantDetail: "$.address.zip: 期望 string，实际 number"},
		{name: "嵌套未声明字段", args: `{"address":{"zip":"1","city":"x"}}`, wantDetail: "$.address.city: 未声明的字段"},
		{name: "合法参数放行", args: `{"address":{"zip":"100"}}`, wantCalled: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			demo := newFakeTool("demo").withParameters(schema)
			executor := newTestExecutor(t, time.Second, demo)

			result, err := executor.Execute(context.Background(), toolRequest("run-1", "call-1", "demo", tc.args))
			if tc.wantCalled {
				if err != nil {
					t.Fatalf("合法参数不应被拒绝：%v", err)
				}
				if result.IsError {
					t.Fatalf("合法参数不应被标记 IsError，Content=%q", result.Content)
				}
				if got := demo.callCount(); got != 1 {
					t.Fatalf("合法参数应放行到工具实现，实际调用 %d 次", got)
				}
				return
			}

			if err != nil {
				t.Fatalf("非法参数应以 IsError 结果承载，不应上抛：%v", err)
			}
			if !result.IsError {
				t.Fatal("未声明 type 的子 Schema 约束必须生效")
			}
			if !strings.Contains(result.Content, tc.wantDetail) {
				t.Fatalf("Content 应包含 %q，实际 %q", tc.wantDetail, result.Content)
			}
			if got := demo.callCount(); got != 0 {
				t.Fatalf("参数校验失败时工具实现一次都不应被调用，实际 %d 次", got)
			}
		})
	}
}

// TestExecutorCancelBeatsToolResult 覆盖「取消优先于工具结果」（INV-106 / CLEAN-002）：
// 工具产出结果时父 Context 已结束，执行器必须丢弃结果并返回 ctx 错误。
//
// 工具在返回前自行取消父 Context，确定性地制造「结果已产出、父 Context 已结束」的竞态窗口：
// 无论 select 先取到工具结果还是父 Context 结束，调用方得到的都必须是 ctx 错误。
// 若删除该守卫，循环中会出现 IsError=false 的成功结果，本测试随之失败。
func TestExecutorCancelBeatsToolResult(t *testing.T) {
	const iterations = 200

	for i := 0; i < iterations; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		echo := newFakeTool("echo").withFunc(func(context.Context, Request) (core.ToolResult, error) {
			cancel() // 父 Context 先结束，工具随后才产出结果
			return core.ToolResult{Content: "工具结果"}, nil
		})
		executor := newTestExecutor(t, time.Second, echo)

		result, err := executeWithWatchdog(t, executor, ctx,
			toolRequest("run-1", fmt.Sprintf("call-%03d", i), "echo", `{}`))

		expectErrorKind(t, err, core.KindCancelled)
		if result != (core.ToolResult{}) {
			t.Fatalf("第 %d 次：父 Context 结束后必须丢弃工具结果，实际 %+v", i+1, result)
		}
		if got := echo.callCount(); got != 1 {
			t.Fatalf("第 %d 次：工具应被调用 1 次，实际 %d 次", i+1, got)
		}
		cancel()
	}
}
