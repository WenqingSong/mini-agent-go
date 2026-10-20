package contextmgr

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// SummarizeRequest 是请求 Summarizer 压缩一段历史的输入。
//
// Messages 是待压缩的完整原子单元序列（保持原顺序，不重排、不改写）；
// PreviousSummary 是上一次压缩产生的摘要（可为空），与新历史一起重新摘要。
type SummarizeRequest struct {
	core.ExecutionRef
	PreviousSummary string
	Messages        []core.Message
}

// Summarizer 把一段历史压缩为纯文本摘要。
//
// 生产 LLM 适配器归 T07，且其调用必须计入 RunPolicy；仓库内只提供测试用 Fake，
// 不提供可被误接入生产装配路径的默认实现，也不调用真实模型。
type Summarizer interface {
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}
