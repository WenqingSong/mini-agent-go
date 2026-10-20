// Package contextmgr 定义上下文预算、窗口选择与压缩的契约。
//
// 本包只描述"决定送进 LLM 的消息视图"的形状：token 估算、截断与压缩算法属于后续任务。
// 使用 contextmgr 而不是 context，避免遮蔽标准库 context。本包只依赖标准库与 core。
package contextmgr

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Request 是一次上下文构建的输入。
//
// TokenBudget > 0 表示上下文 token 上限，<= 0 表示不限制；KeepRecent 表示至少保留最近 N 条消息。
type Request struct {
	core.ExecutionRef
	Messages    []core.Message
	TokenBudget int
	KeepRecent  int
}

// Result 是构建出的消息视图。
//
// Compacted 表示是否发生压缩；DroppedMessages 表示窗口截断丢弃的消息条数。
// 输出视图仍必须是合法的配对序列，不得因截断产生孤儿 Result。
type Result struct {
	Messages        []core.Message
	Compacted       bool
	DroppedMessages int
}

// Manager 构建一次 LLM 调用所需的消息视图。
type Manager interface {
	Build(ctx context.Context, req Request) (Result, error)
}
