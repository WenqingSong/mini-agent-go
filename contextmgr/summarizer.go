package contextmgr

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// SummarizeRequest 是请求 Summarizer 压缩一段历史的输入。
//
// Messages 是待压缩的完整原子单元序列（保持原顺序，不重排、不改写）；
// PreviousSummary 是上一次压缩产生的摘要（可为空），与新历史一起重新摘要。
// 本结构刻意不携带 TokenBudget：Summarizer 只负责产出摘要文本，是否装得下由
// Manager.Build 统一判定，避免摘要器与预算逻辑各算一套。
type SummarizeRequest struct {
	core.ExecutionRef
	PreviousSummary string
	Messages        []core.Message
}

// Summarizer 把一段历史压缩为纯文本摘要。
//
// 契约：返回非空、非全空白的摘要；失败返回 error，由 Manager 映射为 internal 或
// cancelled / deadline_exceeded。实现不得读写 Session、不得递归调用 Manager.Build。
//
// 生产默认实现是 DigestSummarizer（确定性规则化摘录，零模型调用、可离线测试）。
// 需要语义摘要时，可注入实现本接口的模型适配器；该适配器的调用必须计入 RunPolicy、
// 遵守 ctx 取消与超时，并在按 RunPolicy 计数（而非另立预算）后才允许使用。
type Summarizer interface {
	Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}
