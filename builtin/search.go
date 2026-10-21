package builtin

import (
	"context"
	"encoding/json"
	"strings"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// mockSearchParameters 是 mock_search 的模型可见参数协议。
const mockSearchParameters = `{"type":"object","properties":{"query":{"type":"string","description":"查询关键词，大小写不敏感的子串匹配"}},"required":["query"]}`

// searchContent 是 mock_search 的结果结构：所有成功结果都带 mock 标识。
type searchContent struct {
	Mock    bool           `json:"mock"`
	Query   string         `json:"query"`
	Results []searchResult `json:"results"`
}

type searchResult struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// mockSearch 是无状态的 Mock 搜索工具：只读内置数据集，不访问网络，可并发调用。
type mockSearch struct{}

// NewMockSearch 构造 mock_search 工具。
func NewMockSearch() tool.Tool {
	return mockSearch{}
}

// Spec 返回 mock_search 的模型可见元数据。
func (mockSearch) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        ToolNameMockSearch,
		Description: "在源码内置的固定 Mock 数据集中做大小写不敏感的子串匹配；结果按数据集顺序返回，不访问网络，所有结果都标记 mock=true。",
		Parameters:  json.RawMessage(mockSearchParameters),
	}
}

// Execute 在固定数据集上执行一次子串匹配。
//
// 结果顺序等于数据集顺序；无命中返回 IsError=false 的空数组；空查询与纯空白查询
// 返回 empty_query 领域失败。
func (mockSearch) Execute(_ context.Context, req tool.Request) (core.ToolResult, error) {
	name := ToolNameMockSearch
	callID := req.Call.ID

	fields, ok := decodeObject(req.Call.Arguments)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 必须是 JSON 对象"), nil
	}

	query, ok := stringField(fields, "query")
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "参数 query 必须是字符串"), nil
	}

	normalized := strings.ToLower(strings.TrimSpace(query))
	if normalized == "" {
		return failure(name, callID, errCodeEmptyQuery, "查询关键词不能为空"), nil
	}

	results := make([]searchResult, 0, len(searchDataset))
	for _, doc := range searchDataset {
		if !strings.Contains(strings.ToLower(doc.Title), normalized) &&
			!strings.Contains(strings.ToLower(doc.Summary), normalized) {
			continue
		}
		results = append(results, searchResult{ID: doc.ID, Title: doc.Title, Summary: doc.Summary})
	}

	return success(name, callID, searchContent{Mock: true, Query: normalized, Results: results}), nil
}
