package builtin

import (
	"encoding/json"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// allowedSchemaKeys 是 T02 Schema 子集白名单中本任务使用的五个关键字：
// 更宽的关键字会让 Parameters 超出已确认的模型可见协议。
var allowedSchemaKeys = map[string]bool{
	"type":        true,
	"properties":  true,
	"description": true,
	"enum":        true,
	"required":    true,
}

func TestAllToolSpecs(t *testing.T) {
	tools := All(newFakeTodoPort())
	want := []string{
		ToolNameCalculator,
		ToolNameMockSearch,
		ToolNameTodoCreate,
		ToolNameTodoList,
		ToolNameTodoComplete,
	}

	if len(tools) != len(want) {
		t.Fatalf("All 应返回 %d 个工具，实际 %d", len(want), len(tools))
	}

	seen := make(map[string]bool, len(tools))
	for i, tl := range tools {
		spec := tl.Spec()
		if spec.Name != want[i] {
			t.Fatalf("第 %d 个工具名应为 %s，实际 %s", i, want[i], spec.Name)
		}
		if seen[spec.Name] {
			t.Fatalf("工具名重复：%s", spec.Name)
		}
		seen[spec.Name] = true

		if spec.Description == "" {
			t.Fatalf("%s: 描述不能为空", spec.Name)
		}

		var schema map[string]any
		if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
			t.Fatalf("%s: Parameters 不是合法 JSON：%v", spec.Name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s: 根 Schema 必须是 object，实际 %v", spec.Name, schema["type"])
		}

		required, _ := schema["required"].([]any)
		properties, _ := schema["properties"].(map[string]any)
		for _, item := range required {
			name, _ := item.(string)
			if _, ok := properties[name]; !ok {
				t.Fatalf("%s: required 中的 %q 未在 properties 声明", spec.Name, name)
			}
		}
	}
}

func TestToolSpecsUseAllowedSchemaSubset(t *testing.T) {
	for _, tl := range All(newFakeTodoPort()) {
		spec := tl.Spec()

		var schema map[string]any
		if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
			t.Fatalf("%s: Parameters 不是合法 JSON：%v", spec.Name, err)
		}
		walkSchema(t, spec.Name, schema)
	}
}

// walkSchema 递归核对 Schema 键集合，确保只使用已确认的关键字。
func walkSchema(t *testing.T, toolName string, node map[string]any) {
	t.Helper()

	for key, value := range node {
		if !allowedSchemaKeys[key] {
			t.Errorf("%s: Schema 使用了未确认的关键字 %q", toolName, key)
			continue
		}

		switch key {
		case "properties":
			properties, ok := value.(map[string]any)
			if !ok {
				t.Errorf("%s: properties 必须是对象", toolName)
				continue
			}
			for _, raw := range properties {
				child, ok := raw.(map[string]any)
				if !ok {
					t.Errorf("%s: properties 的子节点必须是对象", toolName)
					continue
				}
				walkSchema(t, toolName, child)
			}
		case "enum":
			values, ok := value.([]any)
			if !ok || len(values) == 0 {
				t.Errorf("%s: enum 必须是非空数组", toolName)
			}
			for _, item := range values {
				if _, ok := item.(string); !ok {
					t.Errorf("%s: enum 只支持字符串值", toolName)
				}
			}
		case "required":
			values, ok := value.([]any)
			if !ok {
				t.Errorf("%s: required 必须是数组", toolName)
			}
			for _, item := range values {
				if _, ok := item.(string); !ok {
					t.Errorf("%s: required 只支持字符串", toolName)
				}
			}
		}
	}
}

func TestToolIdentityBackfill(t *testing.T) {
	type invocation struct {
		name string
		tool tool.Tool
		args string
	}

	port := newFakeTodoPort()
	invocations := []invocation{
		{name: "calculator 成功", tool: NewCalculator(), args: `{"op":"add","a":1,"b":2}`},
		{name: "calculator 失败", tool: NewCalculator(), args: `{"op":"div","a":1,"b":0}`},
		{name: "mock_search 成功", tool: NewMockSearch(), args: `{"query":"go"}`},
		{name: "mock_search 失败", tool: NewMockSearch(), args: `{"query":""}`},
		{name: "todo_create 成功", tool: NewTodoCreate(port), args: `{"title":"标题"}`},
		{name: "todo_list 成功", tool: NewTodoList(port), args: `{}`},
		{name: "todo_complete 失败", tool: NewTodoComplete(port), args: `{"id":"todo-unknown"}`},
	}

	for _, item := range invocations {
		t.Run(item.name, func(t *testing.T) {
			const callID = "call-identity"

			result, err := item.tool.Execute(t.Context(), tool.Request{
				ExecutionRef: core.ExecutionRef{SessionID: "session-a", RunID: "run-1"},
				Call: core.ToolCall{
					ID:        callID,
					Name:      item.tool.Spec().Name,
					Arguments: json.RawMessage(item.args),
				},
			})
			if err != nil {
				t.Fatalf("领域调用不应返回 error：%v", err)
			}
			if result.CallID != callID {
				t.Fatalf("CallID 应回填 %q，实际 %q", callID, result.CallID)
			}
			if result.Name != item.tool.Spec().Name {
				t.Fatalf("Name 应回填 %q，实际 %q", item.tool.Spec().Name, result.Name)
			}
			if err := result.Validate(); err != nil {
				t.Fatalf("结果必须满足 core.ToolResult.Validate：%v", err)
			}
		})
	}
}

func TestToolsDoNotPanicOnMalformedArguments(t *testing.T) {
	arguments := []string{"", "not-json", `[]`, `null`, `{"a":{}}`, `{"a":[]}`}

	port := newFakeTodoPort()
	for _, tl := range All(port) {
		for _, args := range arguments {
			t.Run(tl.Spec().Name+"/"+args, func(t *testing.T) {
				// executeNoPanic 同时断言领域失败不返回 error。
				result := executeNoPanic(t, tl, "session-a", args)
				if err := result.Validate(); err != nil {
					t.Fatalf("结果必须满足 Validate：%v", err)
				}
			})
		}
	}
}
