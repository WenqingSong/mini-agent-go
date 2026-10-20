package builtin

// searchDoc 是内置 Mock 数据集中的一条记录，与 Content 的 results 元素一一对应。
type searchDoc struct {
	ID      string
	Title   string
	Summary string
}

// searchDataset 是 mock_search 的固定有序数据集：顺序即返回顺序，不排序、不打分、不截断。
//
// 数据集是只读常量数据，任何查询都不修改它，因此同一查询多次执行的结果逐字节一致；
// 匹配同时覆盖 Title 与 Summary，大小写不敏感。
var searchDataset = []searchDoc{
	{ID: "doc-1", Title: "Go 并发模型", Summary: "goroutine 与 channel 是 Go 并发的基石"},
	{ID: "doc-2", Title: "Go 接口与类型系统", Summary: "接口是隐式实现，类型断言在运行时检查"},
	{ID: "doc-3", Title: "上下文与取消传播", Summary: "context 的取消与超时沿调用链向下传递"},
	{ID: "doc-4", Title: "错误处理约定", Summary: "错误链与 RunError 让失败原因保持可追溯"},
	{ID: "doc-5", Title: "Session 与 Todo 归属", Summary: "会话独占消息历史与 Todo，工具只拿标识"},
	{ID: "doc-6", Title: "工具调用配对", Summary: "ToolCall 与 ToolResult 通过 ID 一一配对"},
}
