package tool

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// personSchema 是覆盖对象/必填/嵌套/数组/枚举/严格字段的合法 Schema。
const personSchema = `{
  "type": "object",
  "properties": {
    "name": {"type": "string"},
    "age": {"type": "integer"},
    "score": {"type": "number"},
    "level": {"type": "string", "enum": ["low", "high"]},
    "note": {"type": "null"},
    "address": {
      "type": "object",
      "properties": {"zip": {"type": "string"}},
      "required": ["zip"],
      "additionalProperties": false
    },
    "tags": {"type": "array", "items": {"type": "string"}}
  },
  "required": ["name", "age"],
  "additionalProperties": false
}`

// TestValidateToolSpecRejectsUnsupportedSchema 覆盖 fail-closed 子集：未支持关键字或结构非法一律拒绝注册。
func TestValidateToolSpecRejectsUnsupportedSchema(t *testing.T) {
	cases := []struct {
		name     string
		params   string
		wantPath string
	}{
		{
			name:     "pattern 关键字",
			params:   `{"type":"object","properties":{"city":{"type":"string","pattern":"^a$"}}}`,
			wantPath: "$.properties.city.pattern",
		},
		{
			name:     "format 关键字",
			params:   `{"type":"object","properties":{"city":{"type":"string","format":"uuid"}}}`,
			wantPath: "$.properties.city.format",
		},
		{
			name:     "oneOf 关键字",
			params:   `{"type":"object","properties":{"v":{"oneOf":[{"type":"string"}]}}}`,
			wantPath: "$.properties.v.oneOf",
		},
		{
			name:     "$ref 关键字",
			params:   `{"type":"object","properties":{"v":{"$ref":"#/$defs/x"}}}`,
			wantPath: "$.properties.v.$ref",
		},
		{
			name:     "minLength 关键字",
			params:   `{"type":"object","properties":{"v":{"minLength":1}}}`,
			wantPath: "$.properties.v.minLength",
		},
		{
			name:     "联合类型数组",
			params:   `{"type":["object","null"]}`,
			wantPath: "$.type",
		},
		{
			name:     "tuple items",
			params:   `{"type":"object","properties":{"tags":{"type":"array","items":[{"type":"string"}]}}}`,
			wantPath: "$.properties.tags.items",
		},
		{
			name:     "items 用于非数组",
			params:   `{"type":"object","properties":{"v":{"type":"string","items":{"type":"string"}}}}`,
			wantPath: "$.properties.v.items",
		},
		{
			name:     "additionalProperties 为对象",
			params:   `{"type":"object","additionalProperties":{"type":"string"}}`,
			wantPath: "$.additionalProperties",
		},
		{
			name:     "根 Schema 不是 object",
			params:   `{"type":"string"}`,
			wantPath: "$.type",
		},
		{
			name:     "根参数不是 JSON object",
			params:   `[]`,
			wantPath: "$",
		},
		{
			name:     "Parameters 非法 JSON",
			params:   `{"type":`,
			wantPath: "$",
		},
		{
			name:     "type 非字符串",
			params:   `{"type":1}`,
			wantPath: "$.type",
		},
		{
			name:     "properties 非 object",
			params:   `{"type":"object","properties":[]}`,
			wantPath: "$.properties",
		},
		{
			name:     "required 非字符串数组",
			params:   `{"type":"object","required":[1]}`,
			wantPath: "$.required",
		},
		{
			name:     "enum 空数组",
			params:   `{"type":"object","properties":{"v":{"enum":[]}}}`,
			wantPath: "$.properties.v.enum",
		},
		{
			name:     "enum 元素为对象",
			params:   `{"type":"object","properties":{"v":{"enum":[{"a":1}]}}}`,
			wantPath: "$.properties.v.enum",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewMapRegistry()
			err := registry.Register(newFakeTool("demo").withParameters(tc.params))
			if err == nil {
				t.Fatal("不支持的 Schema 应拒绝注册")
			}
			if !errors.Is(err, ErrInvalidToolSpec) {
				t.Fatalf("错误应可用 errors.Is 匹配 ErrInvalidToolSpec，实际 %v", err)
			}
			expectErrorKind(t, err, core.KindInvalidArgument)
			if !strings.Contains(err.Error(), tc.wantPath) {
				t.Fatalf("错误文本应包含关键字路径 %q，实际 %v", tc.wantPath, err)
			}
			if len(registry.Specs()) != 0 {
				t.Fatal("注册失败不应向注册表写入任何工具")
			}
		})
	}
}

// TestValidateToolSpecAcceptsSubset 覆盖合法子集：嵌套对象、数组 items、enum、严格字段与注解关键字。
func TestValidateToolSpecAcceptsSubset(t *testing.T) {
	cases := []struct {
		name   string
		params string
	}{
		{
			name:   "嵌套对象与 required",
			params: `{"type":"object","properties":{"address":{"type":"object","properties":{"zip":{"type":"string"}},"required":["zip"]}}}`,
		},
		{
			name:   "数组 items",
			params: `{"type":"object","properties":{"tags":{"type":"array","items":{"type":"string"}}}}`,
		},
		{
			name:   "枚举",
			params: `{"type":"object","properties":{"level":{"type":"string","enum":["low","high"]}}}`,
		},
		{
			name:   "additionalProperties false",
			params: `{"type":"object","properties":{"a":{"type":"string"}},"additionalProperties":false}`,
		},
		{
			name:   "注解关键字",
			params: `{"title":"参数","description":"说明","type":"object","properties":{"a":{"type":"integer","description":"整数"}}}`,
		},
		{
			name:   "null 类型",
			params: `{"type":"object","properties":{"note":{"type":"null"}}}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewMapRegistry()
			if err := registry.Register(newFakeTool("demo").withParameters(tc.params)); err != nil {
				t.Fatalf("合法 Schema 注册不应失败：%v", err)
			}
			specs := registry.Specs()
			if len(specs) != 1 || string(specs[0].Parameters) != tc.params {
				t.Fatalf("合法 Parameters 应逐字节保留，实际 %+v", specs)
			}
		})
	}
}

// TestValidateArguments 覆盖调用期参数校验：路径与固定原因文本。
func TestValidateArguments(t *testing.T) {
	cases := []struct {
		name       string
		schema     string
		args       string
		wantDetail string
	}{
		{name: "合法参数", schema: personSchema, args: `{"name":"amy","age":3}`},
		{name: "integer 接受 1.0", schema: personSchema, args: `{"name":"amy","age":1.0}`},
		{name: "number 接受小数", schema: personSchema, args: `{"name":"amy","age":1,"score":1.5}`},
		{name: "合法嵌套与数组", schema: personSchema, args: `{"name":"amy","age":3,"address":{"zip":"100"},"tags":["a"],"level":"high","note":null}`},
		{name: "参数非法 JSON", schema: personSchema, args: `{"name":`, wantDetail: "参数不是合法 JSON"},
		{name: "顶层为数组", schema: personSchema, args: `[]`, wantDetail: "参数必须是 JSON object"},
		{name: "缺少必填字段", schema: personSchema, args: `{"age":3}`, wantDetail: "$.name: 缺少必填字段"},
		{name: "必填字段为 null 仍需类型校验", schema: personSchema, args: `{"name":null,"age":3}`, wantDetail: "$.name: 期望 string，实际 null"},
		{name: "integer 拒绝小数", schema: personSchema, args: `{"name":"amy","age":1.5}`, wantDetail: "$.age: 期望 integer，实际 number"},
		{name: "字符串字段类型不符", schema: personSchema, args: `{"name":1,"age":3}`, wantDetail: "$.name: 期望 string，实际 number"},
		{name: "枚举取值不符", schema: personSchema, args: `{"name":"amy","age":3,"level":"mid"}`, wantDetail: "$.level: 不在枚举取值内"},
		{name: "未声明字段", schema: personSchema, args: `{"name":"amy","age":3,"extra":1}`, wantDetail: "$.extra: 未声明的字段"},
		{name: "嵌套字段路径", schema: personSchema, args: `{"name":"amy","age":3,"address":{"zip":1}}`, wantDetail: "$.address.zip: 期望 string，实际 number"},
		{name: "嵌套缺少必填", schema: personSchema, args: `{"name":"amy","age":3,"address":{}}`, wantDetail: "$.address.zip: 缺少必填字段"},
		{name: "嵌套未声明字段", schema: personSchema, args: `{"name":"amy","age":3,"address":{"zip":"1","city":"x"}}`, wantDetail: "$.address.city: 未声明的字段"},
		{name: "数组下标路径", schema: personSchema, args: `{"name":"amy","age":3,"tags":["a",1]}`, wantDetail: "$.tags[1]: 期望 string，实际 number"},
		{name: "null 类型字段不符", schema: personSchema, args: `{"name":"amy","age":3,"note":"x"}`, wantDetail: "$.note: 期望 null，实际 string"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArguments("demo", json.RawMessage(tc.schema), json.RawMessage(tc.args))
			if tc.wantDetail == "" {
				if err != nil {
					t.Fatalf("合法参数不应报错：%v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("非法参数应报错，期望包含 %q", tc.wantDetail)
			}
			if !strings.HasPrefix(err.Error(), "工具 demo 参数校验失败: ") {
				t.Fatalf("错误文本前缀不符：%v", err)
			}
			if !strings.Contains(err.Error(), tc.wantDetail) {
				t.Fatalf("错误文本应包含 %q，实际 %v", tc.wantDetail, err)
			}
		})
	}
}

// TestValidateArgumentsEmptyArgumentsAsObject 覆盖空/纯空白 Arguments 视为 {}。
func TestValidateArgumentsEmptyArgumentsAsObject(t *testing.T) {
	const schema = `{"type":"object","properties":{"a":{"type":"string"}}}`

	for _, args := range []string{"", "   \n\t"} {
		if err := validateArguments("demo", json.RawMessage(schema), json.RawMessage(args)); err != nil {
			t.Fatalf("空 Arguments 应视为 {} 并通过校验，实际 %v", err)
		}
	}
}

// TestValidateArgumentsErrorOrder 覆盖首个错误的固定顺序：required 声明顺序 → properties 键字典序。
func TestValidateArgumentsErrorOrder(t *testing.T) {
	const schema = `{
	  "type":"object",
	  "properties":{"a":{"type":"string"},"b":{"type":"string"}},
	  "required":["b","a"],
	  "additionalProperties":false
	}`

	err := validateArguments("demo", json.RawMessage(schema), json.RawMessage(`{"z":1,"y":2}`))
	if err == nil {
		t.Fatal("缺少必填字段时应报错")
	}
	if !strings.Contains(err.Error(), "$.b: 缺少必填字段") {
		t.Fatalf("required 应按声明顺序先报 $.b，实际 %v", err)
	}

	const noRequiredSchema = `{
	  "type":"object",
	  "properties":{"a":{"type":"string"}},
	  "additionalProperties":false
	}`
	err = validateArguments("demo", json.RawMessage(noRequiredSchema), json.RawMessage(`{"z":1,"y":2}`))
	if err == nil {
		t.Fatal("未声明字段应报错")
	}
	if !strings.Contains(err.Error(), "$.y: 未声明的字段") {
		t.Fatalf("properties 键应按字典序先报 $.y，实际 %v", err)
	}
}
