package builtin

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestMockSearchDeterministic(t *testing.T) {
	search := NewMockSearch()

	first := executeNoPanic(t, search, "", `{"query":"go"}`)
	second := executeNoPanic(t, search, "", `{"query":"go"}`)

	if first.IsError || second.IsError {
		t.Fatalf("查询不应失败：first=%s second=%s", first.Content, second.Content)
	}
	if first.Content != second.Content {
		t.Fatalf("同一查询的结果必须逐字节一致：first=%s second=%s", first.Content, second.Content)
	}
	if !strings.Contains(first.Content, `"mock":true`) {
		t.Fatalf("结果必须带 Mock 标识：%s", first.Content)
	}

	var payload searchContent
	if err := json.Unmarshal([]byte(first.Content), &payload); err != nil {
		t.Fatalf("结果不是合法 JSON：%v", err)
	}
	if !payload.Mock {
		t.Fatal("mock 字段必须为 true")
	}
	if len(payload.Results) == 0 {
		t.Fatal("go 应命中内置数据集中的记录")
	}
}

func TestMockSearchCaseInsensitive(t *testing.T) {
	search := NewMockSearch()

	upper := executeNoPanic(t, search, "", `{"query":"Go"}`)
	lower := executeNoPanic(t, search, "", `{"query":"go"}`)
	padded := executeNoPanic(t, search, "", `{"query":"  Go  "}`)

	if upper.Content != lower.Content || lower.Content != padded.Content {
		t.Fatalf("大小写与首尾空白不应改变结果：upper=%s lower=%s padded=%s",
			upper.Content, lower.Content, padded.Content)
	}
	if !strings.Contains(lower.Content, `"query":"go"`) {
		t.Fatalf("query 应回显归一化后的关键词：%s", lower.Content)
	}
}

func TestMockSearchOrderFollowsDataset(t *testing.T) {
	result := executeNoPanic(t, NewMockSearch(), "", `{"query":"工具"}`)
	if result.IsError {
		t.Fatalf("查询不应失败：%s", result.Content)
	}

	var payload searchContent
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("结果不是合法 JSON：%v", err)
	}

	want := []string{"doc-5", "doc-6"}
	if len(payload.Results) != len(want) {
		t.Fatalf("命中数量应为 %d，实际 %d：%s", len(want), len(payload.Results), result.Content)
	}
	for i, id := range want {
		if payload.Results[i].ID != id {
			t.Fatalf("第 %d 条命中应为 %s，实际 %s（顺序必须等于数据集顺序）", i, id, payload.Results[i].ID)
		}
		if payload.Results[i].Title == "" || payload.Results[i].Summary == "" {
			t.Fatalf("命中记录必须包含 title 与 summary：%+v", payload.Results[i])
		}
	}
}

func TestMockSearchNoMatch(t *testing.T) {
	result := executeNoPanic(t, NewMockSearch(), "", `{"query":"不存在的关键词zzz"}`)
	if result.IsError {
		t.Fatalf("无命中不是失败：%s", result.Content)
	}
	if !strings.Contains(result.Content, `"results":[]`) {
		t.Fatalf("无命中必须返回空数组：%s", result.Content)
	}
	if !strings.Contains(result.Content, `"mock":true`) {
		t.Fatalf("无命中结果同样必须带 Mock 标识：%s", result.Content)
	}
}

func TestMockSearchQueryValidation(t *testing.T) {
	cases := []struct {
		name string
		args string
		code string
	}{
		{name: "空查询", args: `{"query":""}`, code: errCodeEmptyQuery},
		{name: "纯空白查询", args: `{"query":"   "}`, code: errCodeEmptyQuery},
		{name: "缺 query", args: `{}`, code: errCodeInvalidArguments},
		{name: "query 非字符串", args: `{"query":1}`, code: errCodeInvalidArguments},
		{name: "query 为 null", args: `{"query":null}`, code: errCodeInvalidArguments},
		{name: "空参数", args: "", code: errCodeInvalidArguments},
		{name: "非法 JSON", args: "not-json", code: errCodeInvalidArguments},
		{name: "数组参数", args: `[]`, code: errCodeInvalidArguments},
	}

	search := NewMockSearch()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := executeNoPanic(t, search, "", tc.args)
			payload := decodeErrorPayload(t, result)
			if payload.Error.Code != tc.code {
				t.Fatalf("期望错误码 %s，实际 %s（Content=%s）", tc.code, payload.Error.Code, result.Content)
			}
			if strings.Contains(result.Content, `"results"`) {
				t.Fatalf("失败结果不应包含 results 字段：%s", result.Content)
			}
		})
	}
}
