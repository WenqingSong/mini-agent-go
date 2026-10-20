package tool

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
)

// emptyObjectSchema 是空 Parameters 的规范化形式：合法的空对象 Schema。
const emptyObjectSchema = `{"type":"object","properties":{}}`

// supportedTypes 是 Schema 子集允许的类型；根 Schema 额外要求必须是 object。
var supportedTypes = map[string]struct{}{
	"object":  {},
	"string":  {},
	"number":  {},
	"integer": {},
	"boolean": {},
	"array":   {},
	"null":    {},
}

// supportedKeywords 是 fail-closed 子集：未列出的关键字一律拒绝注册。
var supportedKeywords = map[string]struct{}{
	"type":                 {},
	"properties":           {},
	"required":             {},
	"items":                {},
	"enum":                 {},
	"additionalProperties": {},
	"description":          {},
	"title":                {},
}

// schemaNode 是注册期编译出的 Schema 节点，供调用期参数校验使用。
type schemaNode struct {
	typ                  string
	properties           map[string]*schemaNode
	required             []string
	items                *schemaNode
	enum                 []any
	hasEnum              bool
	additionalProperties bool
}

// argumentError 是调用期参数校验失败：带字段路径与固定原因文本。
type argumentError struct {
	path   string
	reason string
}

func (e *argumentError) Error() string {
	return e.path + ": " + e.reason
}

// normalizeParameters 校验工具定义并返回注册表保存的 Parameters。
//
// 空 Parameters 规范化为空对象 Schema；非空 Parameters 校验通过后逐字节保留。
func normalizeParameters(raw json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(emptyObjectSchema), nil
	}

	node, err := compileSchema(raw, "$")
	if err != nil {
		return nil, err
	}
	if node.typ != "object" {
		return nil, errors.New("$.type: 根 Schema 的 type 必须是 object")
	}
	return append(json.RawMessage(nil), raw...), nil
}

// compileSchema 解析并校验一段 Schema 原文。
func compileSchema(raw json.RawMessage, path string) (*schemaNode, error) {
	value, err := decodeJSONValue(raw)
	if err != nil {
		return nil, fmt.Errorf("%s: Schema 不是合法 JSON", path)
	}
	return compileSchemaValue(value, path)
}

// compileSchemaValue 递归校验 JSON Schema 子集，未支持关键字或结构非法一律返回错误。
func compileSchemaValue(value any, path string) (*schemaNode, error) {
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: Schema 必须是 JSON object", path)
	}

	for _, key := range sortedKeys(obj) {
		if _, ok := supportedKeywords[key]; !ok {
			return nil, fmt.Errorf("%s: 不支持的关键字 %q", joinPath(path, key), key)
		}
	}

	node := &schemaNode{additionalProperties: true}

	if rawType, ok := obj["type"]; ok {
		typeName, ok := rawType.(string)
		if !ok {
			return nil, fmt.Errorf("%s: 必须是字符串", joinPath(path, "type"))
		}
		if _, ok := supportedTypes[typeName]; !ok {
			return nil, fmt.Errorf("%s: 不支持的类型 %q", joinPath(path, "type"), typeName)
		}
		node.typ = typeName
	}

	if rawProps, ok := obj["properties"]; ok {
		props, ok := rawProps.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%s: 必须是 JSON object", joinPath(path, "properties"))
		}
		node.properties = make(map[string]*schemaNode, len(props))
		for _, key := range sortedKeys(props) {
			child, err := compileSchemaValue(props[key], joinPath(joinPath(path, "properties"), key))
			if err != nil {
				return nil, err
			}
			node.properties[key] = child
		}
	}

	if rawRequired, ok := obj["required"]; ok {
		items, ok := rawRequired.([]any)
		if !ok || len(items) == 0 {
			return nil, fmt.Errorf("%s: 必须是非空字符串数组", joinPath(path, "required"))
		}
		required := make([]string, 0, len(items))
		for _, item := range items {
			name, ok := item.(string)
			if !ok || name == "" {
				return nil, fmt.Errorf("%s: 必须是非空字符串数组", joinPath(path, "required"))
			}
			required = append(required, name)
		}
		node.required = required
	}

	if rawItems, ok := obj["items"]; ok {
		if node.typ != "array" {
			return nil, fmt.Errorf("%s: 只能用于 type=array 的 Schema", joinPath(path, "items"))
		}
		child, err := compileSchemaValue(rawItems, joinPath(path, "items"))
		if err != nil {
			return nil, err
		}
		node.items = child
	}

	if rawEnum, ok := obj["enum"]; ok {
		values, ok := rawEnum.([]any)
		if !ok || len(values) == 0 {
			return nil, fmt.Errorf("%s: 必须是非空数组", joinPath(path, "enum"))
		}
		for _, v := range values {
			switch v.(type) {
			case string, json.Number, bool, nil:
			default:
				return nil, fmt.Errorf("%s: 元素只能是 string/number/bool/null", joinPath(path, "enum"))
			}
		}
		node.enum = values
		node.hasEnum = true
	}

	if rawAdditional, ok := obj["additionalProperties"]; ok {
		allowed, ok := rawAdditional.(bool)
		if !ok {
			return nil, fmt.Errorf("%s: 只能是布尔值", joinPath(path, "additionalProperties"))
		}
		node.additionalProperties = allowed
	}

	return node, nil
}

// validateArguments 在调用工具实现之前校验参数，返回 nil 表示通过。
//
// 错误文本形如「工具 <name> 参数校验失败: <路径>: <原因>」，可直接作为 ToolResult.Content。
func validateArguments(toolName string, schema, args json.RawMessage) error {
	fail := func(detail string) error {
		return fmt.Errorf("工具 %s 参数校验失败: %s", toolName, detail)
	}

	node := &schemaNode{typ: "object", additionalProperties: true}
	if len(bytes.TrimSpace(schema)) > 0 {
		compiled, err := compileSchema(schema, "$")
		if err != nil || compiled.typ != "object" {
			return fail("工具 Schema 非法")
		}
		node = compiled
	}

	var value any = map[string]any{}
	if trimmed := bytes.TrimSpace(args); len(trimmed) > 0 {
		decoded, err := decodeJSONValue(trimmed)
		if err != nil {
			return fail("参数不是合法 JSON")
		}
		value = decoded
	}
	if _, ok := value.(map[string]any); !ok {
		return fail("参数必须是 JSON object")
	}

	if err := validateValue(node, value, "$"); err != nil {
		return fail(err.Error())
	}
	return nil
}

// validateValue 按固定顺序校验：类型 → enum → required 声明顺序 → properties 键字典序 → 数组下标升序。
func validateValue(node *schemaNode, value any, path string) error {
	if node.typ != "" && !matchesType(node.typ, value) {
		return &argumentError{path: path, reason: fmt.Sprintf("期望 %s，实际 %s", node.typ, jsonTypeName(value))}
	}
	if node.hasEnum && !enumContains(node.enum, value) {
		return &argumentError{path: path, reason: "不在枚举取值内"}
	}

	switch node.typ {
	case "object":
		obj := value.(map[string]any)
		for _, name := range node.required {
			if _, ok := obj[name]; !ok {
				return &argumentError{path: joinPath(path, name), reason: "缺少必填字段"}
			}
		}
		for _, key := range sortedKeys(obj) {
			if child, ok := node.properties[key]; ok {
				if err := validateValue(child, obj[key], joinPath(path, key)); err != nil {
					return err
				}
				continue
			}
			if !node.additionalProperties {
				return &argumentError{path: joinPath(path, key), reason: "未声明的字段"}
			}
		}
	case "array":
		items := value.([]any)
		if node.items == nil {
			return nil
		}
		for i, item := range items {
			if err := validateValue(node.items, item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}

	return nil
}

// matchesType 判断 JSON 值是否满足声明的类型；integer 接受数值为整数的 JSON 数字。
func matchesType(typeName string, value any) bool {
	switch typeName {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := value.(json.Number)
		return ok
	case "integer":
		number, ok := value.(json.Number)
		return ok && isIntegerNumber(number)
	default:
		return false
	}
}

// isIntegerNumber 判断 JSON 数字是否为整数：1.0 视为整数，1.5 不是。
func isIntegerNumber(number json.Number) bool {
	if _, err := number.Int64(); err == nil {
		return true
	}
	value, err := number.Float64()
	if err != nil {
		return false
	}
	return !math.IsInf(value, 0) && value == math.Trunc(value)
}

// jsonTypeName 返回 JSON 值对应的类型名，用于错误文本中的「实际」类型。
func jsonTypeName(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// enumContains 按 JSON 值语义判断取值是否落在枚举内。
func enumContains(enum []any, value any) bool {
	for _, candidate := range enum {
		if jsonValuesEqual(candidate, value) {
			return true
		}
	}
	return false
}

// jsonValuesEqual 比较两个 JSON 基础值：数字按数值、字符串按字面、bool 与 null 精确。
func jsonValuesEqual(a, b any) bool {
	left, leftIsNumber := a.(json.Number)
	right, rightIsNumber := b.(json.Number)
	if leftIsNumber || rightIsNumber {
		return leftIsNumber && rightIsNumber && numbersEqual(left, right)
	}

	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		return ok && av == bv
	case bool:
		bv, ok := b.(bool)
		return ok && av == bv
	case nil:
		return b == nil
	default:
		return false
	}
}

// numbersEqual 按数值比较两个 JSON 数字，避免 1 与 1.0 被判为不同。
func numbersEqual(a, b json.Number) bool {
	if ai, err := a.Int64(); err == nil {
		if bi, err := b.Int64(); err == nil {
			return ai == bi
		}
	}
	af, aErr := a.Float64()
	bf, bErr := b.Float64()
	return aErr == nil && bErr == nil && af == bf
}

// decodeJSONValue 解码一段 JSON，保留数字原始文本（json.Number）并拒绝多余内容。
func decodeJSONValue(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()

	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("JSON 之后存在多余内容")
	}
	return value, nil
}

// sortedKeys 返回 JSON object 的键（字典序），保证校验与报错顺序稳定。
func sortedKeys(obj map[string]any) []string {
	keys := make([]string, 0, len(obj))
	for key := range obj {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// joinPath 生成字段路径，形如 $.address.zip。
func joinPath(path, key string) string {
	if path == "" || path == "$" {
		return "$." + key
	}
	return path + "." + key
}
