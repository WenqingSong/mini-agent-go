// Package contextmgr 定义上下文预算、窗口选择与压缩的契约与实现。
//
// 本包决定"送进 LLM 的消息视图"：在给定 token 估算预算下，把 System Prompt、历史摘要与
// 近期消息组合成可直接交给 Provider 的合法消息视图。完整视图的估算值超出 TokenBudget 时，
// 按原子单元压缩最早的历史为摘要并保留必要的近期消息；压缩失败或预算不可满足时显式返回
// *core.RunError，不返回超预算或配对非法的部分视图。
// 使用 contextmgr 而不是 context，避免遮蔽标准库 context。本包只依赖标准库与 core。
package contextmgr

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// Request 是一次上下文构建的输入。
//
// SystemPrompt 与 PreviousSummary 显式提供，永不压缩：前者渲染为视图首条 system 消息，
// 后者是上一次压缩产生的摘要，参与合并摘要；Messages 只允许 user / assistant / tool，
// 出现 system 角色即 invalid_argument（System Prompt 不得重复携带）。
// TokenBudget > 0 表示上下文 token 估算上限，<= 0 表示不限制；KeepRecent 表示至少保留
// 最近 N 条消息（预算不足时可被进一步压缩，见 Manager.Build）。
type Request struct {
	core.ExecutionRef
	SystemPrompt    string
	PreviousSummary string
	Messages        []core.Message
	TokenBudget     int
	KeepRecent      int
}

// Result 是构建出的消息视图。
//
// Compacted 表示是否发生压缩；DroppedMessages 表示被压缩覆盖的消息条数，恰为
// req.Messages 的前 DroppedMessages 条（不含 PreviousSummary）；Summary 是本次压缩
// 产生的新摘要的权威文本，与视图中的摘要 system 消息内容逐字节一致，Compacted=false
// 时为空串。跨轮次持久化配方见 docs/design/runtime-architecture.md §9。
type Result struct {
	Messages        []core.Message
	Compacted       bool
	DroppedMessages int
	Summary         string
}

// Manager 构建一次 LLM 调用所需的消息视图。
type Manager interface {
	Build(ctx context.Context, req Request) (Result, error)
}
