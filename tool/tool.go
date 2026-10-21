// Package tool 定义工具执行契约与工具查找抽象。
//
// 本包只定义"运行一次工具"的形状：不含具体工具实现、沙箱、权限与并发调度，
// 这些属于后续任务。本包只依赖标准库与 core。
package tool

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Request 是一次工具执行的输入。
//
// 会话身份通过内嵌 core.ExecutionRef 显式传参：禁止用 context.Value 承载业务身份，
// 也不向 Tool 传递可变的 session.Session / Todo / Store，避免工具绕过运行时改写会话状态。
type Request struct {
	core.ExecutionRef
	Call core.ToolCall
}

// Tool 是单个工具的抽象。
//
// 可恢复的执行失败返回 core.ToolResult{IsError: true} 让模型继续决策；
// 只有不可恢复且应终止 Run 的失败才返回 error（由 Loop 映射为 FAILED + tool_failure）。
type Tool interface {
	Spec() core.ToolSpec
	Execute(ctx context.Context, req Request) (core.ToolResult, error)
}

// Registry 是工具查找抽象：按名称取工具，并暴露全部工具元数据供 LLM 请求使用。
type Registry interface {
	Lookup(name string) (Tool, bool)
	Specs() []core.ToolSpec
}
