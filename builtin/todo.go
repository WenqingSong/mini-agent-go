package builtin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// TodoOperations 是 Todo 工具依赖的受限写入端口。
//
// 端口由装配层在构造工具时注入（只允许注入 Turn 内的能力实现，不得注入会话管理器本身，
// 否则会重入死锁）。本包只定义端口、不提供任何生产实现：Todo 的存储、ID 生成、状态迁移
// 与会话隔离全部由实现方负责，工具不缓存、不保留副本、不生成 ID。
//
// 端口必须以 ErrTodoNotFound 表示「本会话不存在该待办」，包括该标识属于其他会话的情况。
type TodoOperations interface {
	Create(ctx context.Context, sessionID, title string) (session.Todo, error)
	List(ctx context.Context, sessionID string) ([]session.Todo, error)
	Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error)
}

// ErrTodoNotFound 是端口表达「本会话不存在该待办」的哨兵错误。
var ErrTodoNotFound = errors.New("builtin: 该会话下不存在该待办")

// 本包只按值消费 session.Todo；状态字面量在此以私有常量表示，不修改 session 包。
const (
	todoStatusPending   session.TodoStatus = "pending"
	todoStatusCompleted session.TodoStatus = "completed"
)

// todoCreateParameters / todoCompleteParameters 是 todo 工具的模型可见参数协议；
// 两者都不含任何会话字段，会话身份只来自 tool.Request.SessionID。
const (
	todoCreateParameters   = `{"type":"object","properties":{"title":{"type":"string","description":"待办标题，非空"}},"required":["title"]}`
	todoListParameters     = `{"type":"object","properties":{}}`
	todoCompleteParameters = `{"type":"object","properties":{"id":{"type":"string","description":"本会话内的待办标识"}},"required":["id"]}`
)

// todoView 是 session.Todo 的模型可见投影。
type todoView struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

type todoContent struct {
	Todo todoView `json:"todo"`
}

type todoListContent struct {
	Todos []todoView `json:"todos"`
}

// viewOf 把端口返回值投影为模型可见结构；工具不改写状态，只按值透传。
func viewOf(todo session.Todo) todoView {
	return todoView{ID: todo.ID, Title: todo.Title, Status: string(todo.Status)}
}

// todoCreate 是创建待办的工具。
type todoCreate struct {
	ops TodoOperations
}

// NewTodoCreate 构造 todo_create 工具；ops 为 nil 时工具仍可构造，但执行会返回内部错误。
func NewTodoCreate(ops TodoOperations) tool.Tool {
	return todoCreate{ops: ops}
}

// Spec 返回 todo_create 的模型可见元数据。
func (todoCreate) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        ToolNameTodoCreate,
		Description: "在当前会话中创建一条待办，标题必须非空；会话身份由运行时注入，不接受参数指定。",
		Parameters:  json.RawMessage(todoCreateParameters),
	}
}

// Execute 创建一条待办。
//
// 工具只做参数校验与会话作用域传递：待办 ID、初始状态与存储语义都由端口决定。
func (t todoCreate) Execute(ctx context.Context, req tool.Request) (core.ToolResult, error) {
	name := ToolNameTodoCreate
	op := "builtin.todo_create.Execute"
	callID := req.Call.ID

	domainResult, infraErr := beginTodo(ctx, name, op, req, t.ops)
	if domainResult != nil {
		return *domainResult, nil
	}
	if infraErr != nil {
		return core.ToolResult{CallID: callID, Name: name}, infraErr
	}

	fields, ok := decodeTodoArguments(req.Call.Arguments, false)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 必须是 JSON 对象"), nil
	}
	if hasSessionField(fields) {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 不允许包含会话字段，会话身份由运行时注入"), nil
	}

	title, ok := stringField(fields, "title")
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "参数 title 必须是字符串"), nil
	}
	title = strings.TrimSpace(title)
	if title == "" {
		return failure(name, callID, errCodeEmptyTitle, "待办标题不能为空"), nil
	}

	todo, err := t.ops.Create(ctx, req.SessionID, title)
	if err != nil {
		return portError(name, op, callID, err)
	}

	return success(name, callID, todoContent{Todo: viewOf(todo)}), nil
}

// todoList 是列出本会话待办的工具。
type todoList struct {
	ops TodoOperations
}

// NewTodoList 构造 todo_list 工具；ops 为 nil 时工具仍可构造，但执行会返回内部错误。
func NewTodoList(ops TodoOperations) tool.Tool {
	return todoList{ops: ops}
}

// Spec 返回 todo_list 的模型可见元数据。
func (todoList) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        ToolNameTodoList,
		Description: "列出当前会话的全部待办及其状态；会话身份由运行时注入，不接受参数指定。",
		Parameters:  json.RawMessage(todoListParameters),
	}
}

// Execute 列出本会话待办。
//
// 工具不筛选、不补全：端口按传入的 SessionID 返回什么，就按原顺序透传什么；
// 空会话返回 IsError=false 的空数组。
func (t todoList) Execute(ctx context.Context, req tool.Request) (core.ToolResult, error) {
	name := ToolNameTodoList
	op := "builtin.todo_list.Execute"
	callID := req.Call.ID

	domainResult, infraErr := beginTodo(ctx, name, op, req, t.ops)
	if domainResult != nil {
		return *domainResult, nil
	}
	if infraErr != nil {
		return core.ToolResult{CallID: callID, Name: name}, infraErr
	}

	// todo_list 没有业务参数：空与 null 视为省略而不报错，其它非对象取值仍然拒绝。
	fields, ok := decodeTodoArguments(req.Call.Arguments, true)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 必须是 JSON 对象"), nil
	}
	if hasSessionField(fields) {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 不允许包含会话字段，会话身份由运行时注入"), nil
	}

	todos, err := t.ops.List(ctx, req.SessionID)
	if err != nil {
		return portError(name, op, callID, err)
	}

	views := make([]todoView, 0, len(todos))
	for _, todo := range todos {
		views = append(views, viewOf(todo))
	}

	return success(name, callID, todoListContent{Todos: views}), nil
}

// todoComplete 是完成待办的工具。
type todoComplete struct {
	ops TodoOperations
}

// NewTodoComplete 构造 todo_complete 工具；ops 为 nil 时工具仍可构造，但执行会返回内部错误。
func NewTodoComplete(ops TodoOperations) tool.Tool {
	return todoComplete{ops: ops}
}

// Spec 返回 todo_complete 的模型可见元数据。
func (todoComplete) Spec() core.ToolSpec {
	return core.ToolSpec{
		Name:        ToolNameTodoComplete,
		Description: "把当前会话中指定标识的待办标记为 completed；对已完成的待办重复调用幂等，不存在的标识返回 todo_not_found。",
		Parameters:  json.RawMessage(todoCompleteParameters),
	}
}

// Execute 完成一条待办。
//
// 「定位 + 置为 completed」由端口原子完成，工具不做读改写；不存在（含其他会话的标识）
// 映射为 todo_not_found 领域失败且无副作用。
func (t todoComplete) Execute(ctx context.Context, req tool.Request) (core.ToolResult, error) {
	name := ToolNameTodoComplete
	op := "builtin.todo_complete.Execute"
	callID := req.Call.ID

	domainResult, infraErr := beginTodo(ctx, name, op, req, t.ops)
	if domainResult != nil {
		return *domainResult, nil
	}
	if infraErr != nil {
		return core.ToolResult{CallID: callID, Name: name}, infraErr
	}

	fields, ok := decodeTodoArguments(req.Call.Arguments, false)
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 必须是 JSON 对象"), nil
	}
	if hasSessionField(fields) {
		return failure(name, callID, errCodeInvalidArguments, "Arguments 不允许包含会话字段，会话身份由运行时注入"), nil
	}

	todoID, ok := stringField(fields, "id")
	if !ok {
		return failure(name, callID, errCodeInvalidArguments, "参数 id 必须是字符串"), nil
	}
	todoID = strings.TrimSpace(todoID)
	if todoID == "" {
		return failure(name, callID, errCodeInvalidArguments, "参数 id 不能为空"), nil
	}

	todo, err := t.ops.Complete(ctx, req.SessionID, todoID)
	if err != nil {
		if errors.Is(err, ErrTodoNotFound) {
			return failure(name, callID, errCodeTodoNotFound, "该会话下不存在该待办"), nil
		}
		return portError(name, op, callID, err)
	}

	return success(name, callID, todoContent{Todo: viewOf(todo)}), nil
}

// beginTodo 完成三个 todo 工具共用的前置检查：端口注入、ctx 状态与可信会话身份。
//
// 返回值的语义：
//   - domainResult 非 nil：领域失败，调用方原样返回它且 error 为 nil；
//   - infraErr 非 nil：基础设施失败，调用方返回空结果与该 *core.RunError；
//   - 两者都为 nil：可以继续校验业务参数。
//
// 会话检查先于参数解析：SessionID 为空时一律 missing_session_id，且绝不触达端口。
func beginTodo(ctx context.Context, name, op string, req tool.Request, ops TodoOperations) (*core.ToolResult, error) {
	callID := req.Call.ID

	if ops == nil {
		return nil, &core.RunError{
			Kind:    core.KindInternal,
			Op:      op,
			Message: "待办能力未注入，无法执行",
			CallID:  callID,
		}
	}

	if err := contextError(ctx, op, callID); err != nil {
		return nil, err
	}

	if req.SessionID == "" {
		result := failure(name, callID, errCodeMissingSessionID, "缺少可信的会话身份，无法执行待办操作")
		return &result, nil
	}

	return nil, nil
}

// decodeTodoArguments 解析 todo 工具的参数；allowEmpty 为 true 时把空与 null 视为空对象。
func decodeTodoArguments(args json.RawMessage, allowEmpty bool) (map[string]json.RawMessage, bool) {
	if allowEmpty {
		trimmed := bytes.TrimSpace(args)
		if len(trimmed) == 0 || string(trimmed) == "null" {
			return map[string]json.RawMessage{}, true
		}
	}
	return decodeObject(args)
}

// contextError 在调用端口前检查 ctx：取消与超时都必须返回非 nil 的 *core.RunError，
// 并保证不产生任何写入。
func contextError(ctx context.Context, op, callID string) error {
	if ctx == nil {
		return nil
	}

	err := ctx.Err()
	if err == nil {
		return nil
	}

	kind := core.KindCancelled
	if errors.Is(err, context.DeadlineExceeded) {
		kind = core.KindDeadlineExceeded
	}

	return &core.RunError{
		Kind:    kind,
		Op:      op,
		Message: "工具调用已结束，未执行待办操作",
		CallID:  callID,
		Cause:   err,
	}
}

// portError 把端口返回的非领域错误映射为基础设施失败。
//
// 包装保留原始错误链（Cause），Message 固定为不含内部细节的中文文本；Content 保持空，
// 不回显路径、连接串或凭据。
func portError(name, op, callID string, cause error) (core.ToolResult, error) {
	kind := core.KindOf(cause)
	switch {
	case errors.Is(cause, context.Canceled):
		kind = core.KindCancelled
	case errors.Is(cause, context.DeadlineExceeded):
		kind = core.KindDeadlineExceeded
	}

	return core.ToolResult{CallID: callID, Name: name}, &core.RunError{
		Kind:    kind,
		Op:      op,
		Message: "待办能力调用失败，请稍后重试",
		CallID:  callID,
		Cause:   cause,
	}
}

// All 返回全部内置工具的固定顺序集合：calculator、mock_search、todo_create、
// todo_list、todo_complete。
//
// 顺序固定便于装配层稳定注册；ops 为 nil 时 todo 工具仍可构造，但执行返回内部错误。
func All(ops TodoOperations) []tool.Tool {
	return []tool.Tool{
		NewCalculator(),
		NewMockSearch(),
		NewTodoCreate(ops),
		NewTodoList(ops),
		NewTodoComplete(ops),
	}
}
