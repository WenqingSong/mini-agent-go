package core

import "fmt"

// ValidateMessages 校验整段消息序列：先逐条做结构校验，再做 ToolCall / ToolResult 配对校验。
//
// 装配层在把消息交给 Provider 之前必须调用本函数，配对非法时返回 invalid_pairing，
// 不得把非法序列静默发给模型。
func ValidateMessages(msgs []Message) error {
	for i, m := range msgs {
		if err := m.Validate(); err != nil {
			return wrapMessageIndex(err, i)
		}
	}
	return ValidateToolPairing(msgs)
}

// ValidateToolPairing 只校验配对完整性（INV-002），返回的错误种类固定为 invalid_pairing：
//
//  1. assistant 消息内 ToolCall.ID 非空且唯一（跨消息也不重复）；
//  2. 每个 ToolCall 恰好被一条后续 RoleTool 消息的结果引用；
//  3. 不存在引用未出现 ToolCall 的孤儿 Result，也不存在重复 Result；
//  4. 任一违规都定位到具体 CallID，而不是静默接受。
//
// 报错顺序：先按消息顺序报结构性问题，最后按 ToolCall 出现顺序报缺失结果，保证结果可复现。
func ValidateToolPairing(msgs []Message) error {
	pending := make(map[string]struct{})
	answered := make(map[string]struct{})

	for i, m := range msgs {
		switch m.Role {
		case roleAssistant:
			inMessage := make(map[string]struct{}, len(m.ToolCalls))
			for _, call := range m.ToolCalls {
				if call.ID == "" {
					return pairingError(i, "", "assistant 消息中的工具调用缺少 ID")
				}
				if _, ok := inMessage[call.ID]; ok {
					return pairingError(i, call.ID, "同一 assistant 消息内工具调用 ID 重复")
				}
				if _, ok := pending[call.ID]; ok {
					return pairingError(i, call.ID, "工具调用 ID 在前序消息中已出现却仍未应答")
				}
				if _, ok := answered[call.ID]; ok {
					return pairingError(i, call.ID, "工具调用 ID 在前序消息中已使用")
				}
				inMessage[call.ID] = struct{}{}
				pending[call.ID] = struct{}{}
			}
		case roleTool:
			if m.ToolResult == nil {
				return pairingError(i, "", "tool 消息缺少 ToolResult")
			}
			callID := m.ToolResult.CallID
			if callID == "" {
				return pairingError(i, "", "工具结果缺少 CallID")
			}
			if _, ok := answered[callID]; ok {
				return pairingError(i, callID, "工具调用已有结果，出现重复结果")
			}
			if _, ok := pending[callID]; !ok {
				return pairingError(i, callID, "工具结果引用了不存在的工具调用")
			}
			delete(pending, callID)
			answered[callID] = struct{}{}
		}
	}

	for _, m := range msgs {
		for _, call := range m.ToolCalls {
			if _, ok := pending[call.ID]; ok {
				return pairingError(-1, call.ID, "工具调用缺少对应结果")
			}
		}
	}

	return nil
}

// pairingError 构造带消息序号与 CallID 的 invalid_pairing 错误；index < 0 表示不指向具体消息。
func pairingError(index int, callID, msg string) error {
	if index >= 0 {
		msg = fmt.Sprintf("消息[%d]: %s", index, msg)
	}
	return &RunError{
		Kind:    KindInvalidPairing,
		Op:      "core.ValidateToolPairing",
		Message: msg,
		CallID:  callID,
	}
}
