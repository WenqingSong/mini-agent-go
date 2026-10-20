package tool

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

const executeOp = "tool.Executor.Execute"

// ExecutorOptions 是执行器的装配参数。
type ExecutorOptions struct {
	// Registry 提供按名称查找工具，必填。
	Registry Registry
	// Timeout 是单次工具执行的局部超时；<=0 表示不设工具级超时，只受调用方 Context 约束。
	Timeout time.Duration
}

// dedupKey 是去重身份：同一次 Run 内的同一个 Tool Call ID。
type dedupKey struct {
	runID  string
	callID string
}

// Executor 在超时与取消约束下执行单次工具调用。
//
// 它只回答「这一次工具调用得到了什么结果」：不产生 RunStatus、不计数预算、
// 不做批量调度、不写日志后端。占位表是过程防护，不是会话事实来源。
type Executor struct {
	registry Registry
	timeout  time.Duration

	mu   sync.Mutex
	seen map[dedupKey]struct{}
}

// NewExecutor 构造执行器；Registry 为空属调用方契约错误。
func NewExecutor(opts ExecutorOptions) (*Executor, error) {
	if opts.Registry == nil {
		return nil, core.NewError(core.KindInvalidArgument, "tool.NewExecutor", "Registry 不能为空")
	}
	return &Executor{
		registry: opts.Registry,
		timeout:  opts.Timeout,
		seen:     make(map[dedupKey]struct{}),
	}, nil
}

// Execute 执行一次工具调用。
//
// 判定顺序固定：CallID → RunID → 工具名称 → 父 Context → 去重占位 → 查表 → 参数校验 → 执行。
// 返回 ToolResult 时 CallID/Name 一律归一化为请求值；返回 error 时保留请求 CallID。
func (e *Executor) Execute(ctx context.Context, req Request) (core.ToolResult, error) {
	callID := req.Call.ID
	name := req.Call.Name

	if callID == "" {
		return core.ToolResult{}, &core.RunError{
			Kind:    core.KindInvalidPairing,
			Op:      executeOp,
			Message: "工具调用缺少 CallID，无法与结果配对",
		}
	}
	if req.RunID == "" {
		return core.ToolResult{}, &core.RunError{
			Kind:    core.KindInvalidArgument,
			Op:      executeOp,
			Message: "RunID 为空，无法确定去重范围",
			CallID:  callID,
		}
	}
	if strings.TrimSpace(name) == "" {
		return core.ToolResult{}, &core.RunError{
			Kind:    core.KindInvalidArgument,
			Op:      executeOp,
			Message: "工具名称不能为空",
			CallID:  callID,
		}
	}
	if err := ctx.Err(); err != nil {
		return core.ToolResult{}, contextRunError(err, callID)
	}
	if !e.claim(req.RunID, callID) {
		return core.ToolResult{}, &core.RunError{
			Kind:    core.KindInvalidPairing,
			Op:      executeOp,
			Message: "工具调用 ID 在本次 Run 内已提交过（重复调用）",
			CallID:  callID,
		}
	}

	tool, ok := e.registry.Lookup(name)
	if !ok {
		return core.ToolResult{
			CallID:  callID,
			Name:    name,
			Content: fmt.Sprintf("未知工具: %s", name),
			IsError: true,
		}, nil
	}

	if err := validateArguments(name, tool.Spec().Parameters, req.Call.Arguments); err != nil {
		return core.ToolResult{
			CallID:  callID,
			Name:    name,
			Content: err.Error(),
			IsError: true,
		}, nil
	}

	return e.run(ctx, tool, req)
}

// claim 以原子方式占位；返回 false 表示同 Run 同 CallID 已提交过。
func (e *Executor) claim(runID, callID string) bool {
	key := dedupKey{runID: runID, callID: callID}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, ok := e.seen[key]; ok {
		return false
	}
	e.seen[key] = struct{}{}
	return true
}

// run 在独立 goroutine 中执行工具，并通过 select 等待「工具返回 / 工具级超时 / 父 Context 结束」。
//
// 工具忽略 Context 时，本方法只保证调用方及时返回：不终止工具 goroutine，
// 也不撤销其副作用；被放弃 goroutine 的结果被丢弃。
func (e *Executor) run(ctx context.Context, t Tool, req Request) (core.ToolResult, error) {
	type outcome struct {
		result   core.ToolResult
		err      error
		panicked bool
		panicVal any
	}

	done := make(chan outcome, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				done <- outcome{panicked: true, panicVal: r}
			}
		}()
		result, err := t.Execute(ctx, req)
		done <- outcome{result: result, err: err}
	}()

	var timeoutC <-chan time.Time
	if e.timeout > 0 {
		timer := time.NewTimer(e.timeout)
		defer timer.Stop()
		timeoutC = timer.C
	}

	// 父 Context 结束信号经 context.AfterFunc 转投到独立通道：close(ctx.Done()) 会直接
	// 抢占正在 select 的调用方，使「工具结果已产出、但父 Context 已结束」的分支永远走不到。
	// 经独立 goroutine 转投后，工具 goroutine（已在运行）通常先投递结果，该分支因此可被
	// 确定性地执行与测试（CLEAN-002）。
	abort := make(chan struct{})
	stopWatch := context.AfterFunc(ctx, func() { close(abort) })
	defer stopWatch()

	select {
	case out := <-done:
		// 取消优先于工具结果：父 Context 已结束时丢弃工具产出。
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, contextRunError(err, req.Call.ID)
		}
		if out.panicked {
			return core.ToolResult{}, panicRunError(req.Call.Name, req.Call.ID, out.panicVal)
		}
		if out.err != nil {
			return core.ToolResult{
				CallID:  req.Call.ID,
				Name:    req.Call.Name,
				Content: fmt.Sprintf("工具 %s 执行失败: %v", req.Call.Name, out.err),
				IsError: true,
			}, nil
		}
		out.result.CallID = req.Call.ID
		out.result.Name = req.Call.Name
		return out.result, nil
	case <-timeoutC:
		// 工具级超时以父 Context 仍存活为前提；两者同时结束时归因到取消/整体超时。
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, contextRunError(err, req.Call.ID)
		}
		return core.ToolResult{
			CallID:  req.Call.ID,
			Name:    req.Call.Name,
			Content: fmt.Sprintf("工具 %s 执行超时（%s）", req.Call.Name, e.timeout),
			IsError: true,
		}, nil
	case <-abort:
		return core.ToolResult{}, contextRunError(ctx.Err(), req.Call.ID)
	}
}

// contextRunError 把父 Context 的结束映射为 cancelled / deadline_exceeded。
func contextRunError(err error, callID string) *core.RunError {
	kind := core.KindCancelled
	message := "工具执行被父 Context 取消"
	if errors.Is(err, context.DeadlineExceeded) {
		kind = core.KindDeadlineExceeded
		message = "工具执行时父 Context 已超时"
	}
	return &core.RunError{
		Kind:    kind,
		Op:      executeOp,
		Message: message,
		CallID:  callID,
		Cause:   err,
	}
}

// panicRunError 把工具 panic 隔离为不可恢复的 tool_failure，不记录、不拼接堆栈。
func panicRunError(name, callID string, panicVal any) *core.RunError {
	return &core.RunError{
		Kind:    core.KindToolFailure,
		Op:      executeOp,
		Message: fmt.Sprintf("工具 %s 执行发生 panic", name),
		CallID:  callID,
		Cause:   fmt.Errorf("panic: %v", panicVal),
	}
}
