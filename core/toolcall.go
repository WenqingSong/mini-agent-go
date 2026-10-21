package core

import "encoding/json"

// ToolCall 是模型请求执行一次工具调用的记录。
//
// ID 是配对主键，在所属 assistant 消息内唯一（契约层保证同一 Run 内不重复）；
// Arguments 保持 Provider 返回的原始 JSON，契约层不解析、不改写。
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// ToolResult 是一次工具调用的结果，作为独立 RoleTool 消息的载荷。
//
// CallID 是配对主键；Name 只用于日志与模型阅读，不参与配对；
// IsError=true 表示可恢复的工具失败（回填模型继续决策），Content 即错误描述。
type ToolResult struct {
	CallID  string
	Name    string
	Content string
	IsError bool
}

// ToolSpec 是工具元数据，被 llm.Request.Tools 与 tool.Tool.Spec 共用，
// 放在 core 以避免两个 L1 包互相依赖。Parameters 为 JSON Schema 原文。
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Validate 校验 ToolResult 是否可参与配对：缺少 CallID 时无法唯一定位到 ToolCall。
func (r ToolResult) Validate() error {
	if r.CallID == "" {
		return NewError(KindInvalidPairing, "core.ToolResult.Validate", "工具结果缺少 CallID，无法定位到对应工具调用")
	}
	return nil
}

// validateToolCall 校验 ToolCall 的配对身份，供 Message.Validate 与配对校验复用。
func validateToolCall(c ToolCall) error {
	if c.ID == "" {
		return NewError(KindInvalidPairing, "core.Message.Validate", "工具调用缺少 ID，无法与结果配对")
	}
	return nil
}
