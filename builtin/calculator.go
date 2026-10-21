package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"math"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// calculatorParameters 是 calculator 的模型可见参数协议。
const calculatorParameters = `{"type":"object","properties":{"op":{"type":"string","enum":["add","sub","mul","div"],"description":"运算符"},"a":{"type":"number","description":"第一个操作数"},"b":{"type":"number","description":"第二个操作数"}},"required":["op","a","b"]}`

// calculatorContent 是 calculator 成功结果的结构。
type calculatorContent struct {
	Operation string  `json:"operation"`
	Result    float64 `json:"result"`
}

// calculator 是无状态的四则运算工具：不同 Execute 之间不共享可变状态，可并发调用。
type calculator struct{}

// NewCalculator 构造 calculator 工具。
func NewCalculator() tool.Tool {
	return calculator{}
}

// Spec 返回 calculator 的模型可见元数据。
func (calculator) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        ToolNameCalculator,
		Description: "执行一次四则运算：op 取 add / sub / mul / div，a 与 b 为有限浮点数；除零、未知运算符、缺失或非数值操作数、结果非有限都返回 error 内容。",
		Parameters:  json.RawMessage(calculatorParameters),
	}
}

// Execute 执行一次四则运算。
//
// 所有可预期的输入问题都以领域失败返回（IsError=true，error 为 nil）；
// 计算只使用 float64，成功结果的 result 必定是有限值。
func (calculator) Execute(_ context.Context, req tool.Request) (core.ToolResult, error) {
	name := ToolNameCalculator
	callID := req.Call.ID

	fields, ok := decodeObject(req.Call.Arguments)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 必须是 JSON 对象"), nil
	}

	opRaw, hasOp := fields["op"]
	if !hasOp {
		return failure(name, callID, errCodeMissingOperator, "缺少运算符 op"), nil
	}
	op, ok := stringValue(opRaw)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "运算符 op 必须是字符串"), nil
	}
	if !isOperator(op) {
		return failure(name, callID, errCodeUnknownOperator, fmt.Sprintf("未知运算符 %q，只支持 add / sub / mul / div", op)), nil
	}

	aRaw, hasA := fields["a"]
	bRaw, hasB := fields["b"]
	if !hasA || !hasB {
		return failure(name, callID, errCodeMissingOperand, "缺少操作数 a 或 b"), nil
	}

	a, ok := numberValue(aRaw)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "操作数 a 必须是 JSON 数值"), nil
	}
	b, ok := numberValue(bRaw)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "操作数 b 必须是 JSON 数值"), nil
	}

	var result float64
	switch op {
	case "add":
		result = a + b
	case "sub":
		result = a - b
	case "mul":
		result = a * b
	case "div":
		if b == 0 {
			// b == 0 同时覆盖 0、-0 与 0.0 的各种写法。
			return failure(name, callID, errCodeDivisionByZero, "除数不能为 0"), nil
		}
		result = a / b
	}

	if math.IsNaN(result) || math.IsInf(result, 0) {
		return failure(name, callID, errCodeResultNotFinite, "计算结果不是有限数值"), nil
	}

	return success(name, callID, calculatorContent{Operation: op, Result: result}), nil
}

// isOperator 判断运算符是否属于模型可见协议的枚举。
func isOperator(op string) bool {
	switch op {
	case "add", "sub", "mul", "div":
		return true
	default:
		return false
	}
}
