package builtin

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// executeNoPanic 在 recover 保护下执行工具，用于证明畸形输入不会让进程 panic。
func executeNoPanic(t *testing.T, tl tool.Tool, sessionID, args string) (result core.ToolResult) {
	t.Helper()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("%s: Execute 不得 panic，实际 %v", tl.Spec().Name, r)
		}
	}()

	return runTool(t, tl, sessionID, args)
}

// calculatorArguments 生成 calculator 的参数 JSON；浮点数用 'g' 格式保证是合法 JSON number。
func calculatorArguments(op string, a, b float64) string {
	return fmt.Sprintf(`{"op":%q,"a":%s,"b":%s}`,
		op,
		strconv.FormatFloat(a, 'g', -1, 64),
		strconv.FormatFloat(b, 'g', -1, 64),
	)
}

func TestCalculatorSuccess(t *testing.T) {
	cases := []struct {
		name string
		op   string
		a    float64
		b    float64
		want float64
	}{
		{name: "整数加法", op: "add", a: 2, b: 3, want: 5},
		{name: "负数减法", op: "sub", a: -5, b: -3, want: -2},
		{name: "负数乘法", op: "mul", a: -2.5, b: 4, want: -10},
		{name: "精确小数加法", op: "add", a: 0.5, b: 2.5, want: 3},
		{name: "除法整数结果", op: "div", a: 7.5, b: 2.5, want: 3},
		{name: "除不尽", op: "div", a: 1, b: 3, want: 1.0 / 3.0},
		{name: "浮点精度边界", op: "add", a: 0.1, b: 0.2, want: 0.30000000000000004},
		{name: "大数乘法", op: "mul", a: 1e154, b: 1e154, want: 1e308},
		{name: "下溢为零", op: "div", a: 1e-308, b: 1e308, want: 0},
	}

	calculator := NewCalculator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := executeNoPanic(t, calculator, "", calculatorArguments(tc.op, tc.a, tc.b))
			if result.IsError {
				t.Fatalf("不应失败：%s", result.Content)
			}

			var payload calculatorContent
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Fatalf("成功内容不是合法 JSON：%v，Content=%s", err, result.Content)
			}
			if payload.Operation != tc.op {
				t.Fatalf("operation 应回显 %q，实际 %q", tc.op, payload.Operation)
			}
			if math.IsNaN(payload.Result) || math.IsInf(payload.Result, 0) {
				t.Fatalf("成功结果必须是有限值，实际 %v", payload.Result)
			}

			// 除不尽与浮点边界用相对误差判定，不用字符串相等。
			tolerance := 1e-12 * math.Max(1, math.Abs(tc.want))
			if diff := math.Abs(payload.Result - tc.want); diff > tolerance {
				t.Fatalf("结果不符：want=%v got=%v diff=%v", tc.want, payload.Result, diff)
			}
		})
	}
}

func TestCalculatorFailure(t *testing.T) {
	cases := []struct {
		name string
		args string
		code string
	}{
		{name: "除数为 0", args: `{"op":"div","a":1,"b":0}`, code: errCodeDivisionByZero},
		{name: "除数为 -0", args: `{"op":"div","a":1,"b":-0.0}`, code: errCodeDivisionByZero},
		{name: "未知运算符", args: `{"op":"pow","a":1,"b":2}`, code: errCodeUnknownOperator},
		{name: "空运算符", args: `{"op":"","a":1,"b":2}`, code: errCodeUnknownOperator},
		{name: "缺 op", args: `{"a":1,"b":2}`, code: errCodeMissingOperator},
		{name: "op 为 null", args: `{"op":null,"a":1,"b":2}`, code: errCodeInvalidArguments},
		{name: "op 非字符串", args: `{"op":1,"a":1,"b":2}`, code: errCodeInvalidArguments},
		{name: "缺 a", args: `{"op":"add","b":2}`, code: errCodeMissingOperand},
		{name: "缺 b", args: `{"op":"add","a":2}`, code: errCodeMissingOperand},
		{name: "a 是字符串", args: `{"op":"add","a":"2","b":2}`, code: errCodeInvalidArguments},
		{name: "b 是 null", args: `{"op":"add","a":2,"b":null}`, code: errCodeInvalidArguments},
		{name: "a 是对象", args: `{"op":"add","a":{},"b":2}`, code: errCodeInvalidArguments},
		{name: "a 超出 float64 范围", args: `{"op":"add","a":1e309,"b":2}`, code: errCodeInvalidArguments},
		{name: "NaN 字面量不是合法 JSON", args: `{"op":"add","a":NaN,"b":2}`, code: errCodeInvalidArguments},
		{name: "Infinity 字面量不是合法 JSON", args: `{"op":"add","a":Infinity,"b":2}`, code: errCodeInvalidArguments},
		{name: "乘法溢出", args: `{"op":"mul","a":1e308,"b":10}`, code: errCodeResultNotFinite},
		{name: "除法溢出", args: `{"op":"div","a":1e308,"b":1e-308}`, code: errCodeResultNotFinite},
		{name: "空参数", args: "", code: errCodeInvalidArguments},
		{name: "非法 JSON", args: "not-json", code: errCodeInvalidArguments},
		{name: "数组参数", args: `[]`, code: errCodeInvalidArguments},
		{name: "null 参数", args: `null`, code: errCodeInvalidArguments},
	}

	calculator := NewCalculator()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := executeNoPanic(t, calculator, "", tc.args)
			payload := decodeErrorPayload(t, result)

			if payload.Error.Code != tc.code {
				t.Fatalf("期望错误码 %s，实际 %s（Content=%s）", tc.code, payload.Error.Code, result.Content)
			}
			if payload.Error.Message == "" {
				t.Fatal("领域失败必须带稳定的中文说明")
			}

			// 失败结果不得带任何可能被误判为有效数值的字段。
			var body map[string]json.RawMessage
			if err := json.Unmarshal([]byte(result.Content), &body); err != nil {
				t.Fatalf("失败内容不是合法 JSON：%v", err)
			}
			if _, ok := body["result"]; ok {
				t.Fatalf("失败结果不应包含 result 字段：%s", result.Content)
			}
			if _, ok := body["operation"]; ok {
				t.Fatalf("失败结果不应包含 operation 字段：%s", result.Content)
			}
		})
	}
}

func TestCalculatorIgnoresUnknownArguments(t *testing.T) {
	result := executeNoPanic(t, NewCalculator(), "", `{"op":"add","a":1,"b":2,"note":"额外字段"}`)
	if result.IsError {
		t.Fatalf("未知字段应被忽略：%s", result.Content)
	}
}
