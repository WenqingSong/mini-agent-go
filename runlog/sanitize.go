package runlog

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// redactedValue 是敏感键与凭据模式字符串的固定占位符。
	redactedValue = "[REDACTED]"
	// truncatedValue 是深度或节点预算耗尽后的固定占位符。
	truncatedValue = "[TRUNCATED]"

	// maxDataBytes 是单条记录允许输出的载荷上限（64 KiB）；超过时不解析、不输出 data。
	maxDataBytes = 64 << 10
	// maxDepth 是载荷允许展开的最大深度，Data 顶层对象为 depth 0。
	maxDepth = 8
	// maxNodes 是单条记录允许访问的最大值数量。
	maxNodes = 1024
	// maxStringBytes 是单个字符串值的字节上限，超长截断后追加省略号。
	maxStringBytes = 256
)

// allowedKeys 是允许原样输出值的键（归一化后精确匹配，封闭集合）。
// 新增业务字段必须同步 docs/design/runtime-architecture.md §10 与本表，否则不会出现在日志中。
var allowedKeys = map[string]struct{}{
	"model":           {},
	"provider":        {},
	"finishreason":    {},
	"stopreason":      {},
	"name":            {},
	"tool":            {},
	"toolname":        {},
	"iserror":         {},
	"ok":              {},
	"status":          {},
	"attempt":         {},
	"maxattempts":     {},
	"retrycount":      {},
	"usage":           {},
	"inputtokens":     {},
	"outputtokens":    {},
	"totaltokens":     {},
	"cachedtokens":    {},
	"compacted":       {},
	"droppedmessages": {},
	"messagecount":    {},
	"toolcallcount":   {},
	"id":              {},
	"index":           {},
	"truncated":       {},
}

// sensitiveFragments 是敏感键的归一化片段；键名包含任一即整体脱敏。
// 允许清单优先，因此 total_tokens / input_tokens 等计数不会被 token 片段误伤。
var sensitiveFragments = []string{
	"apikey",
	"secret",
	"token",
	"password",
	"passwd",
	"credential",
	"privatekey",
	"accesskey",
}

// sensitiveKeys 是需要精确匹配的敏感键（归一化后）。
var sensitiveKeys = map[string]struct{}{
	"authorization":      {},
	"auth":               {},
	"proxyauthorization": {},
	"cookie":             {},
	"setcookie":          {},
}

// bodyKeys 是用户正文键：只输出长度摘要，不输出原文。
var bodyKeys = map[string]struct{}{
	"content":  {},
	"text":     {},
	"messages": {},
	"prompt":   {},
	"input":    {},
	"raw":      {},
	"body":     {},
}

// credentialPattern 匹配值级的凭据前缀（不区分大小写），命中即整体脱敏。
var credentialPattern = regexp.MustCompile(`(?i)(bearer|basic)\s+\S+`)

// normalizeKey 归一化键名：小写，去掉 `_`、`-`、`.` 与空格。
func normalizeKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range key {
		switch r {
		case '_', '-', '.', ' ':
			continue
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

// isAllowedKey 判断归一化键是否在允许清单内。
func isAllowedKey(normalized string) bool {
	_, ok := allowedKeys[normalized]
	return ok
}

// isSensitiveKey 判断归一化键是否为敏感键。
func isSensitiveKey(normalized string) bool {
	if _, ok := sensitiveKeys[normalized]; ok {
		return true
	}
	for _, fragment := range sensitiveFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

// isBodyKey 判断归一化键是否为用户正文键。
func isBodyKey(normalized string) bool {
	_, ok := bodyKeys[normalized]
	return ok
}

// sanitizeString 对单个字符串值套用值级规则：凭据模式整体替换，超长按 UTF-8 边界截断。
func sanitizeString(s string) string {
	if credentialPattern.MatchString(s) {
		return redactedValue
	}
	return truncateString(s)
}

// truncateString 把超过 256 字节的字符串截断到 UTF-8 边界并追加省略号。
func truncateString(s string) string {
	if len(s) <= maxStringBytes {
		return s
	}
	cut := maxStringBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// redactBody 把正文值替换为长度摘要：只暴露字节长度，不暴露内容。
func redactBody(v any) string {
	return "[REDACTED:len=" + strconv.Itoa(bodyLength(v)) + "]"
}

// bodyLength 返回值的字节长度：字符串取其字节数，其余值取 JSON 序列化长度。
func bodyLength(v any) int {
	if s, ok := v.(string); ok {
		return len(s)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}

// sanitizer 在单条记录的节点预算内完成脱敏，预算耗尽后剩余值一律 [TRUNCATED]。
type sanitizer struct {
	budget int
}

// newSanitizer 返回带完整节点预算的脱敏器。
func newSanitizer() *sanitizer {
	return &sanitizer{budget: maxNodes}
}

// sanitizeData 把事件载荷脱敏为可输出的对象。
//
// invalid=true 表示载荷不可解析、含尾随内容或顶层不是对象；truncated=true 表示原始字节超过
// 64 KiB。两种情况都不输出任何原始字节，由调用方改为输出对应标记。
func sanitizeData(raw json.RawMessage) (data map[string]any, invalid bool, truncated bool) {
	if len(raw) == 0 {
		return nil, false, false
	}
	if len(raw) > maxDataBytes {
		return nil, false, true
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()

	var top any
	if err := dec.Decode(&top); err != nil {
		return nil, true, false
	}
	// 一个载荷只允许一个 JSON 值：拒绝尾随内容，避免记录被截断后的半截字节。
	if err := dec.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, true, false
	}

	obj, ok := top.(map[string]any)
	if !ok {
		return nil, true, false
	}

	return newSanitizer().object(obj, 0), false, false
}

// object 按对象键字典序处理成员，保证截断结果可复现。
func (s *sanitizer) object(m map[string]any, depth int) map[string]any {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make(map[string]any, len(keys))
	for _, key := range keys {
		normalized := normalizeKey(key)
		switch {
		case isAllowedKey(normalized):
			out[key] = s.value(m[key], depth+1)
		case isSensitiveKey(normalized):
			// 命中即停：不再遍历其值，避免嵌套凭据泄漏。
			out[key] = redactedValue
		case isBodyKey(normalized):
			out[key] = redactBody(m[key])
		default:
			// 未知键整键省略：既不输出键名，也不输出原始值。
		}
	}
	return out
}

// value 处理允许清单键的值：对象/数组递归套用同一规则，字符串套用值级规则。
func (s *sanitizer) value(v any, depth int) any {
	if depth > maxDepth || s.budget <= 0 {
		return truncatedValue
	}
	s.budget--

	switch t := v.(type) {
	case string:
		return sanitizeString(t)
	case map[string]any:
		return s.object(t, depth)
	case []any:
		return s.array(t, depth)
	default:
		return v
	}
}

// array 处理数组元素：对象按成员规则，数字/布尔/null 保留，字符串按正文处理。
func (s *sanitizer) array(items []any, depth int) []any {
	out := make([]any, len(items))
	for i, item := range items {
		out[i] = s.element(item, depth+1)
	}
	return out
}

// element 是数组元素的脱敏规则，与对象成员不同：字符串一律只保留长度摘要。
func (s *sanitizer) element(v any, depth int) any {
	if depth > maxDepth || s.budget <= 0 {
		return truncatedValue
	}
	s.budget--

	switch t := v.(type) {
	case string:
		return redactBody(t)
	case map[string]any:
		return s.object(t, depth)
	case []any:
		return s.array(t, depth)
	default:
		return v
	}
}
