// Package builtin 实现可被 Agent Loop 直接调用的内置工具。
//
// 本包只承载具体工具实现与模型可见协议（工具名、Parameters JSON Schema、Content 结构），
// 不负责工具查找、调度与失败回填（属于 tool.Registry / Agent Loop），也不提供任何生产
// Todo 存储：Todo 状态由装配层注入的 TodoOperations 实现方独占。
//
// 依赖方向：标准库 + core / tool / session，不 import 上层运行时包。
//
// 失败分级：
//   - 领域失败（参数非法、除零、未知运算符、空查询、空会话、空标题、待办不存在等）
//     返回 core.ToolResult{IsError: true} 且函数返回的 error 为 nil，Content 为
//     {"error":{"code":"<snake_case>","message":"<稳定中文说明>"}}；
//   - 基础设施失败（端口未注入、端口返回非领域错误、ctx 已结束）返回 *core.RunError，
//     原始错误只保留在 Cause，不写入 Content。
package builtin

import (
	"bytes"
	"encoding/json"
	"math"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// 工具名是模型可见的长期协议，变更需 Contract Revision。
const (
	ToolNameCalculator   = "calculator"
	ToolNameMockSearch   = "mock_search"
	ToolNameTodoCreate   = "todo_create"
	ToolNameTodoList     = "todo_list"
	ToolNameTodoComplete = "todo_complete"
)

// 领域错误码：只出现在 Content 的 error.code 中，不进入 RunError。
const (
	errCodeInvalidArguments = "invalid_arguments"
	errCodeMissingOperator  = "missing_operator"
	errCodeUnknownOperator  = "unknown_operator"
	errCodeMissingOperand   = "missing_operand"
	errCodeDivisionByZero   = "division_by_zero"
	errCodeResultNotFinite  = "result_not_finite"
	errCodeEmptyQuery       = "empty_query"
	errCodeMissingSessionID = "missing_session_id"
	errCodeEmptyTitle       = "empty_title"
	errCodeTodoNotFound     = "todo_not_found"
)

// sessionKeys 是禁止出现在 Arguments 中的会话字段：会话身份只来自 tool.Request.SessionID，
// 不允许模型通过参数选择会话，也不静默忽略这类键。
var sessionKeys = []string{"session_id", "sessionId", "session"}

// errorContent 是领域失败的内容结构。
type errorContent struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// failure 构造领域失败结果：IsError=true，调用方的 error 保持 nil。
func failure(name, callID, code, message string) core.ToolResult {
	return core.ToolResult{
		CallID:  callID,
		Name:    name,
		Content: encode(errorContent{Error: errorDetail{Code: code, Message: message}}),
		IsError: true,
	}
}

// success 构造成功结果。
func success(name, callID string, body any) core.ToolResult {
	return core.ToolResult{
		CallID:  callID,
		Name:    name,
		Content: encode(body),
	}
}

// encode 序列化固定的结果结构。
//
// 这些结构的字段只有 string / bool / float64 / 切片，编码不会失败；为避免编码失败
// 退化成 panic 或伪成功，仍给出恒定兜底文本（该分支不可达）。
func encode(body any) string {
	raw, err := json.Marshal(body)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

// decodeObject 把 Arguments 解析为字段表。
//
// 只接受 JSON 对象：空参数、非法 JSON、数组、数字、字符串与 null 一律返回 false，
// 由调用方映射为 invalid_arguments。
func decodeObject(args json.RawMessage) (map[string]json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, false
	}
	if fields == nil {
		return nil, false
	}
	return fields, true
}

// hasSessionField 判断 Arguments 是否试图指定会话身份。
func hasSessionField(fields map[string]json.RawMessage) bool {
	for _, key := range sessionKeys {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

// stringField 读取字符串字段；字段缺失或类型不是字符串时返回 false。
func stringField(fields map[string]json.RawMessage, key string) (string, bool) {
	raw, ok := fields[key]
	if !ok {
		return "", false
	}
	return stringValue(raw)
}

// stringValue 解析 JSON 字符串；null、缺失与其它类型都返回 false。
func stringValue(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return "", false
	}

	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", false
	}
	return value, true
}

// numberValue 解析有限 float64：null、字符串、布尔与超出 float64 范围的数字字面量
// 都返回 false；NaN / ±Inf 也不被接受。
func numberValue(raw json.RawMessage) (float64, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return 0, false
	}

	var value float64
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return 0, false
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	return value, true
}
