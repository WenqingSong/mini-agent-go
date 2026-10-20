package core

import (
	"encoding/json"
	"fmt"
)

// Role 是消息的发言方，取值 system / user / assistant / tool。
type Role string

// ContentType 是内容片段类型，v1 只支持 text；多模态通过新增类型扩展，不改变 Message 形态。
type ContentType string

// 契约层内部使用的枚举字面量；对外只暴露类型本身，避免 Contract 之外的新增公共符号。
const (
	roleSystem    Role = "system"
	roleUser      Role = "user"
	roleAssistant Role = "assistant"
	roleTool      Role = "tool"

	contentTypeText ContentType = "text"
)

// ContentPart 是消息正文的一个片段。Raw 保存 Provider 原始片段，供日志与回放，契约层不解析。
type ContentPart struct {
	Type ContentType
	Text string
	Raw  json.RawMessage
}

// Message 是统一的对话消息，采用单一 struct + 类型化可选字段，而不是 content-part 接口多态。
//
// 结构约束（见 Validate）：
//   - ToolCalls 只允许出现在 assistant 消息上；
//   - ToolResult 只允许出现在 tool 消息上，且 RoleTool 必须携带；
//   - 一条 tool 消息只承载一个结果，多个结果即多条消息；
//   - Raw 保存 Provider 原始载荷，契约层不解析。
type Message struct {
	ID         string
	Role       Role
	Content    []ContentPart
	ToolCalls  []ToolCall
	ToolResult *ToolResult
	Raw        json.RawMessage
}

// Validate 校验单条消息的结构合法性，不校验跨消息配对（见 ValidateToolPairing / ValidateMessages）。
//
// 角色与可选字段的组合错误返回 invalid_argument；
// 工具调用/结果的身份（ID、CallID）缺失等配对身份问题返回 invalid_pairing，
// 与 INV-002「assistant 消息内 ToolCall.ID 非空且唯一」保持一致。
func (m Message) Validate() error {
	switch m.Role {
	case roleSystem, roleUser:
		if m.ToolResult != nil {
			return NewError(KindInvalidArgument, "core.Message.Validate", fmt.Sprintf("%s 消息不允许携带 ToolResult", m.Role))
		}
		if len(m.ToolCalls) > 0 {
			return NewError(KindInvalidArgument, "core.Message.Validate", fmt.Sprintf("%s 消息不允许携带 ToolCalls", m.Role))
		}
	case roleAssistant:
		if m.ToolResult != nil {
			return NewError(KindInvalidArgument, "core.Message.Validate", "assistant 消息不允许携带 ToolResult，工具结果必须作为独立的 tool 消息")
		}
		seen := make(map[string]struct{}, len(m.ToolCalls))
		for i, call := range m.ToolCalls {
			if err := validateToolCall(call); err != nil {
				return err
			}
			if _, ok := seen[call.ID]; ok {
				return &RunError{
					Kind:    KindInvalidPairing,
					Op:      "core.Message.Validate",
					Message: fmt.Sprintf("assistant 消息内工具调用 ID 重复（第 %d 个）", i),
					CallID:  call.ID,
				}
			}
			seen[call.ID] = struct{}{}
		}
	case roleTool:
		if len(m.ToolCalls) > 0 {
			return NewError(KindInvalidArgument, "core.Message.Validate", "tool 消息不允许携带 ToolCalls")
		}
		if m.ToolResult == nil {
			return NewError(KindInvalidArgument, "core.Message.Validate", "tool 消息必须携带 ToolResult")
		}
		if err := m.ToolResult.Validate(); err != nil {
			return err
		}
	default:
		return NewError(KindInvalidArgument, "core.Message.Validate", fmt.Sprintf("未知消息角色 %q", m.Role))
	}

	for i, part := range m.Content {
		if part.Type != contentTypeText {
			return NewError(KindInvalidArgument, "core.Message.Validate", fmt.Sprintf("content[%d] 的内容类型 %q 不受支持", i, part.Type))
		}
	}

	return nil
}
