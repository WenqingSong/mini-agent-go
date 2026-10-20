package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// fakeTodoPort 是测试专用 TodoOperations 实现：按 sessionID 分桶保存，记录每次调用参数。
//
// 它只用于证明工具层行为（会话作用域传递、失败映射、并发调用无竞态），
// 不能作为「真实 Session 隔离 / 持久化并发已成立」的证据；生产代码不包含任何实现。
type fakeTodoPort struct {
	mu     sync.Mutex
	todos  map[string][]session.Todo
	calls  []recordedCall
	nextID int
	err    error
}

type recordedCall struct {
	method    string
	sessionID string
	title     string
	todoID    string
}

func newFakeTodoPort() *fakeTodoPort {
	return &fakeTodoPort{todos: make(map[string][]session.Todo)}
}

func (f *fakeTodoPort) Create(_ context.Context, sessionID, title string) (session.Todo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, recordedCall{method: "Create", sessionID: sessionID, title: title})
	if f.err != nil {
		return session.Todo{}, f.err
	}

	f.nextID++
	todo := session.Todo{ID: fmt.Sprintf("todo-%d", f.nextID), Title: title, Status: todoStatusPending}
	f.todos[sessionID] = append(f.todos[sessionID], todo)
	return todo, nil
}

func (f *fakeTodoPort) List(_ context.Context, sessionID string) ([]session.Todo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, recordedCall{method: "List", sessionID: sessionID})
	if f.err != nil {
		return nil, f.err
	}

	todos := f.todos[sessionID]
	result := make([]session.Todo, len(todos))
	copy(result, todos)
	return result, nil
}

func (f *fakeTodoPort) Complete(_ context.Context, sessionID, todoID string) (session.Todo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, recordedCall{method: "Complete", sessionID: sessionID, todoID: todoID})
	if f.err != nil {
		return session.Todo{}, f.err
	}

	list := f.todos[sessionID]
	for i := range list {
		if list[i].ID != todoID {
			continue
		}
		list[i].Status = todoStatusCompleted
		f.todos[sessionID] = list
		return list[i], nil
	}
	return session.Todo{}, ErrTodoNotFound
}

func (f *fakeTodoPort) setError(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.err = err
}

func (f *fakeTodoPort) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeTodoPort) recordedCalls() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]recordedCall, len(f.calls))
	copy(result, f.calls)
	return result
}

func (f *fakeTodoPort) snapshot(sessionID string) []session.Todo {
	f.mu.Lock()
	defer f.mu.Unlock()
	result := make([]session.Todo, len(f.todos[sessionID]))
	copy(result, f.todos[sessionID])
	return result
}

// errorPayload 是领域失败内容的结构化投影。
type errorPayload struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type todoPayload struct {
	Todo todoView `json:"todo"`
}

type todoListPayload struct {
	Todos []todoView `json:"todos"`
}

// runTool 执行一次工具调用，并要求领域失败语义：返回的 error 必须为 nil。
func runTool(t *testing.T, tl tool.Tool, sessionID, args string) core.ToolResult {
	t.Helper()

	result, err := tl.Execute(context.Background(), tool.Request{
		ExecutionRef: core.ExecutionRef{SessionID: sessionID, RunID: "run-1"},
		Call: core.ToolCall{
			ID:        "call-1",
			Name:      tl.Spec().Name,
			Arguments: json.RawMessage(args),
		},
	})
	if err != nil {
		t.Fatalf("%s: 领域失败不应返回 error，实际为 %v", tl.Spec().Name, err)
	}
	if result.CallID != "call-1" || result.Name != tl.Spec().Name {
		t.Fatalf("%s: 身份回填错误，CallID=%q Name=%q", tl.Spec().Name, result.CallID, result.Name)
	}
	return result
}

// decodeErrorPayload 断言 IsError=true 并解析错误码。
func decodeErrorPayload(t *testing.T, result core.ToolResult) errorPayload {
	t.Helper()

	if !result.IsError {
		t.Fatalf("%s: 期望领域失败，实际 IsError=false，Content=%s", result.Name, result.Content)
	}

	var payload errorPayload
	if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
		t.Fatalf("%s: 失败内容不是合法 JSON：%v，Content=%s", result.Name, err, result.Content)
	}
	return payload
}

func TestTodoLifecycle(t *testing.T) {
	// 证据边界：这里只证明工具按 req.SessionID 传递作用域并正确映射端口结果，
	// 不证明真实 Session 隔离（那属于 Session Manager 与装配层的运行期证据）。
	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	list := NewTodoList(port)
	complete := NewTodoComplete(port)

	created := runTool(t, create, "session-a", `{"title":"写测试"}`)
	if created.IsError {
		t.Fatalf("创建待办不应失败：%s", created.Content)
	}

	var createdPayload todoPayload
	if err := json.Unmarshal([]byte(created.Content), &createdPayload); err != nil {
		t.Fatalf("创建结果不是合法 JSON：%v", err)
	}
	if createdPayload.Todo.ID == "" {
		t.Fatal("创建结果必须带端口返回的标识")
	}
	if createdPayload.Todo.Title != "写测试" {
		t.Fatalf("标题不符：%q", createdPayload.Todo.Title)
	}
	if createdPayload.Todo.Status != string(todoStatusPending) {
		t.Fatalf("新待办状态应为 pending，实际 %q", createdPayload.Todo.Status)
	}

	listed := runTool(t, list, "session-a", `{}`)
	if listed.IsError {
		t.Fatalf("列表不应失败：%s", listed.Content)
	}
	var listPayload todoListPayload
	if err := json.Unmarshal([]byte(listed.Content), &listPayload); err != nil {
		t.Fatalf("列表结果不是合法 JSON：%v", err)
	}
	if len(listPayload.Todos) != 1 || listPayload.Todos[0].ID != createdPayload.Todo.ID {
		t.Fatalf("列表应恰好包含刚创建的待办，实际 %+v", listPayload.Todos)
	}

	completed := runTool(t, complete, "session-a", fmt.Sprintf(`{"id":%q}`, createdPayload.Todo.ID))
	if completed.IsError {
		t.Fatalf("完成不应失败：%s", completed.Content)
	}
	var completedPayload todoPayload
	if err := json.Unmarshal([]byte(completed.Content), &completedPayload); err != nil {
		t.Fatalf("完成结果不是合法 JSON：%v", err)
	}
	if completedPayload.Todo.Status != string(todoStatusCompleted) {
		t.Fatalf("完成后状态应为 completed，实际 %q", completedPayload.Todo.Status)
	}
	if completedPayload.Todo.Title != "写测试" {
		t.Fatalf("完成后标题不应改变：%q", completedPayload.Todo.Title)
	}

	final := runTool(t, list, "session-a", `{}`)
	if err := json.Unmarshal([]byte(final.Content), &listPayload); err != nil {
		t.Fatalf("列表结果不是合法 JSON：%v", err)
	}
	if len(listPayload.Todos) != 1 || listPayload.Todos[0].Status != string(todoStatusCompleted) {
		t.Fatalf("列表应显示 completed，实际 %+v", listPayload.Todos)
	}

	for _, call := range port.recordedCalls() {
		if call.sessionID != "session-a" {
			t.Fatalf("%s 收到的会话标识应为 req.SessionID，实际 %q", call.method, call.sessionID)
		}
	}
}

func TestTodoCompleteIsIdempotent(t *testing.T) {
	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	complete := NewTodoComplete(port)

	var created todoPayload
	result := runTool(t, create, "session-a", `{"title":"幂等完成"}`)
	if err := json.Unmarshal([]byte(result.Content), &created); err != nil {
		t.Fatalf("创建结果不是合法 JSON：%v", err)
	}

	args := fmt.Sprintf(`{"id":%q}`, created.Todo.ID)
	first := runTool(t, complete, "session-a", args)
	second := runTool(t, complete, "session-a", args)

	if first.IsError || second.IsError {
		t.Fatalf("重复完成应幂等成功：first=%s second=%s", first.Content, second.Content)
	}
	if first.Content != second.Content {
		t.Fatalf("重复完成的内容应一致：first=%s second=%s", first.Content, second.Content)
	}
	if !strings.Contains(second.Content, `"status":"completed"`) {
		t.Fatalf("重复完成后状态应为 completed：%s", second.Content)
	}
}

func TestTodoCompleteUnknownIDHasNoSideEffect(t *testing.T) {
	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	list := NewTodoList(port)
	complete := NewTodoComplete(port)

	runTool(t, create, "session-a", `{"title":"保留"}`)
	before := port.snapshot("session-a")

	result := runTool(t, complete, "session-a", `{"id":"todo-not-exist"}`)
	payload := decodeErrorPayload(t, result)
	if payload.Error.Code != errCodeTodoNotFound {
		t.Fatalf("期望错误码 %s，实际 %s", errCodeTodoNotFound, payload.Error.Code)
	}

	after := port.snapshot("session-a")
	if len(after) != len(before) || after[0] != before[0] {
		t.Fatalf("失败调用不得产生副作用：before=%+v after=%+v", before, after)
	}

	listed := runTool(t, list, "session-a", `{}`)
	if !strings.Contains(listed.Content, `"status":"pending"`) {
		t.Fatalf("列表应仍为 pending：%s", listed.Content)
	}
}

func TestTodoCrossSessionScope(t *testing.T) {
	// 证据边界：B 看不到 A 的待办由 fake 的分桶语义造成；本用例只证明工具把
	// req.SessionID 原样传给端口，并把 ErrTodoNotFound 映射为 todo_not_found，
	// 不证明真实 Session 隔离或跨会话不可写。
	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	list := NewTodoList(port)
	complete := NewTodoComplete(port)

	var created todoPayload
	result := runTool(t, create, "session-a", `{"title":"A 的待办"}`)
	if err := json.Unmarshal([]byte(result.Content), &created); err != nil {
		t.Fatalf("创建结果不是合法 JSON：%v", err)
	}
	runTool(t, create, "session-a", `{"title":"A 的第二条"}`)

	before := port.snapshot("session-a")

	bList := runTool(t, list, "session-b", `{}`)
	var bPayload todoListPayload
	if err := json.Unmarshal([]byte(bList.Content), &bPayload); err != nil {
		t.Fatalf("列表结果不是合法 JSON：%v", err)
	}
	if len(bPayload.Todos) != 0 {
		t.Fatalf("session-b 不应看到 session-a 的待办：%+v", bPayload.Todos)
	}

	cross := runTool(t, complete, "session-b", fmt.Sprintf(`{"id":%q}`, created.Todo.ID))
	payload := decodeErrorPayload(t, cross)
	if payload.Error.Code != errCodeTodoNotFound {
		t.Fatalf("跨会话完成应返回 %s，实际 %s", errCodeTodoNotFound, payload.Error.Code)
	}

	after := port.snapshot("session-a")
	if len(after) != len(before) {
		t.Fatalf("跨会话完成不得改变 A 的数据：before=%+v after=%+v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("跨会话完成不得改变 A 的数据：before=%+v after=%+v", before, after)
		}
	}
}

func TestTodoEmptySessionIDRejected(t *testing.T) {
	cases := []struct {
		name    string
		args    string
		newTool func(TodoOperations) tool.Tool
	}{
		{name: "create", args: `{"title":"标题"}`, newTool: NewTodoCreate},
		{name: "list", args: `{}`, newTool: NewTodoList},
		{name: "complete", args: `{"id":"todo-1"}`, newTool: NewTodoComplete},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := newFakeTodoPort()

			result := runTool(t, tc.newTool(port), "", tc.args)
			payload := decodeErrorPayload(t, result)
			if payload.Error.Code != errCodeMissingSessionID {
				t.Fatalf("期望 %s，实际 %s", errCodeMissingSessionID, payload.Error.Code)
			}
			if port.callCount() != 0 {
				t.Fatalf("空 SessionID 不得调用端口，实际调用 %d 次", port.callCount())
			}
		})
	}
}

func TestTodoSessionFieldsRejected(t *testing.T) {
	keys := []string{"session_id", "sessionId", "session"}

	for _, key := range keys {
		for _, name := range []string{"create", "list", "complete"} {
			t.Run(name+"/"+key, func(t *testing.T) {
				port := newFakeTodoPort()

				var tl tool.Tool
				args := fmt.Sprintf(`{"title":"标题",%q:"other-session"}`, key)
				switch name {
				case "create":
					tl = NewTodoCreate(port)
				case "list":
					tl = NewTodoList(port)
					args = fmt.Sprintf(`{%q:"other-session"}`, key)
				case "complete":
					tl = NewTodoComplete(port)
					args = fmt.Sprintf(`{"id":"todo-1",%q:"other-session"}`, key)
				}

				result := runTool(t, tl, "session-a", args)
				payload := decodeErrorPayload(t, result)
				if payload.Error.Code != errCodeInvalidArguments {
					t.Fatalf("期望 %s，实际 %s", errCodeInvalidArguments, payload.Error.Code)
				}
				if port.callCount() != 0 {
					t.Fatalf("含会话字段的调用不得触达端口，实际 %d 次", port.callCount())
				}
			})
		}
	}
}

func TestTodoTitleValidation(t *testing.T) {
	cases := []struct {
		name string
		args string
		code string
	}{
		{name: "缺少 title", args: `{}`, code: errCodeInvalidArguments},
		{name: "title 非字符串", args: `{"title":1}`, code: errCodeInvalidArguments},
		{name: "空 title", args: `{"title":""}`, code: errCodeEmptyTitle},
		{name: "纯空白 title", args: `{"title":"   "}`, code: errCodeEmptyTitle},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := newFakeTodoPort()
			result := runTool(t, NewTodoCreate(port), "session-a", tc.args)
			payload := decodeErrorPayload(t, result)
			if payload.Error.Code != tc.code {
				t.Fatalf("期望 %s，实际 %s", tc.code, payload.Error.Code)
			}
			if port.callCount() != 0 {
				t.Fatalf("标题非法时不得调用端口，实际 %d 次", port.callCount())
			}
		})
	}
}

func TestTodoArgumentsValidation(t *testing.T) {
	tools := []struct {
		name    string
		newTool func(TodoOperations) tool.Tool
		invalid []string
	}{
		{name: "create", newTool: NewTodoCreate, invalid: []string{"", "not-json", `[]`, `null`, `"str"`}},
		{name: "list", newTool: NewTodoList, invalid: []string{"not-json", `[]`, `"str"`, `123`}},
		{name: "complete", newTool: NewTodoComplete, invalid: []string{"", "not-json", `[]`, `null`, `"str"`}},
	}

	for _, tl := range tools {
		for _, args := range tl.invalid {
			t.Run(tl.name+"/"+args, func(t *testing.T) {
				port := newFakeTodoPort()

				result := runTool(t, tl.newTool(port), "session-a", args)
				payload := decodeErrorPayload(t, result)
				if payload.Error.Code != errCodeInvalidArguments {
					t.Fatalf("期望 %s，实际 %s", errCodeInvalidArguments, payload.Error.Code)
				}
				if port.callCount() != 0 {
					t.Fatalf("非法参数不得调用端口，实际 %d 次", port.callCount())
				}
			})
		}
	}
}

func TestTodoListToleratesOmittedArguments(t *testing.T) {
	// todo_list 没有业务参数：Provider 省略 Arguments 时不能被误判为参数错误。
	for _, args := range []string{"", "null"} {
		port := newFakeTodoPort()
		result := runTool(t, NewTodoList(port), "session-a", args)
		if result.IsError {
			t.Fatalf("args=%q: 省略参数不应失败：%s", args, result.Content)
		}
		if port.callCount() != 1 {
			t.Fatalf("args=%q: 应调用端口一次，实际 %d 次", args, port.callCount())
		}
	}
}

func TestTodoCompleteIDValidation(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{name: "缺少 id", args: `{}`},
		{name: "id 非字符串", args: `{"id":1}`},
		{name: "id 为空", args: `{"id":""}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := newFakeTodoPort()
			result := runTool(t, NewTodoComplete(port), "session-a", tc.args)
			payload := decodeErrorPayload(t, result)
			if payload.Error.Code != errCodeInvalidArguments {
				t.Fatalf("期望 %s，实际 %s", errCodeInvalidArguments, payload.Error.Code)
			}
			if port.callCount() != 0 {
				t.Fatalf("非法标识不得调用端口，实际 %d 次", port.callCount())
			}
		})
	}
}

func TestTodoIgnoresUnknownArguments(t *testing.T) {
	port := newFakeTodoPort()
	result := runTool(t, NewTodoCreate(port), "session-a", `{"title":"带额外字段","note":"忽略"}`)
	if result.IsError {
		t.Fatalf("未知字段应被忽略：%s", result.Content)
	}
}

func TestTodoPortInfrastructureFailure(t *testing.T) {
	const leak = "/var/lib/mini-agent/db.sqlite password=secret-token"

	cases := []struct {
		name     string
		inject   error
		wantKind core.ErrorKind
	}{
		{name: "普通错误", inject: errors.New("dial " + leak), wantKind: core.KindInternal},
		{name: "端口取消", inject: context.Canceled, wantKind: core.KindCancelled},
		{name: "端口超时", inject: context.DeadlineExceeded, wantKind: core.KindDeadlineExceeded},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port := newFakeTodoPort()
			port.setError(tc.inject)

			tools := []tool.Tool{NewTodoCreate(port), NewTodoList(port), NewTodoComplete(port)}
			args := []string{`{"title":"标题"}`, `{}`, `{"id":"todo-1"}`}

			for i, tl := range tools {
				result, err := tl.Execute(context.Background(), tool.Request{
					ExecutionRef: core.ExecutionRef{SessionID: "session-a", RunID: "run-1"},
					Call: core.ToolCall{
						ID:        "call-1",
						Name:      tl.Spec().Name,
						Arguments: json.RawMessage(args[i]),
					},
				})
				if err == nil {
					t.Fatalf("%s: 端口基础设施失败必须返回 error", tl.Spec().Name)
				}
				if kind := core.KindOf(err); kind != tc.wantKind {
					t.Fatalf("%s: 期望错误种类 %s，实际 %s", tl.Spec().Name, tc.wantKind, kind)
				}
				if !errors.Is(err, tc.inject) {
					t.Fatalf("%s: 错误链应保留原始错误", tl.Spec().Name)
				}
				if result.IsError {
					t.Fatalf("%s: 基础设施失败不得降级为 IsError=true", tl.Spec().Name)
				}
				if strings.Contains(result.Content, "password") || strings.Contains(result.Content, "/var/lib") {
					t.Fatalf("%s: Content 不得泄漏内部细节：%s", tl.Spec().Name, result.Content)
				}
			}
		})
	}
}

func TestTodoCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	port := newFakeTodoPort()
	tools := []tool.Tool{NewTodoCreate(port), NewTodoList(port), NewTodoComplete(port)}
	args := []string{`{"title":"标题"}`, `{}`, `{"id":"todo-1"}`}

	for i, tl := range tools {
		result, err := tl.Execute(ctx, tool.Request{
			ExecutionRef: core.ExecutionRef{SessionID: "session-a", RunID: "run-1"},
			Call: core.ToolCall{
				ID:        "call-1",
				Name:      tl.Spec().Name,
				Arguments: json.RawMessage(args[i]),
			},
		})
		if err == nil {
			t.Fatalf("%s: 已取消的 ctx 必须返回 error", tl.Spec().Name)
		}
		if kind := core.KindOf(err); kind != core.KindCancelled {
			t.Fatalf("%s: 期望 %s，实际 %s", tl.Spec().Name, core.KindCancelled, kind)
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("%s: 错误链应保留 context.Canceled", tl.Spec().Name)
		}
		if result.IsError {
			t.Fatalf("%s: 取消不得降级为 IsError=true", tl.Spec().Name)
		}
	}

	if port.callCount() != 0 {
		t.Fatalf("已取消的 ctx 不得调用端口，实际 %d 次", port.callCount())
	}
}

func TestTodoWithoutOperations(t *testing.T) {
	tools := All(nil)
	if len(tools) != 5 {
		t.Fatalf("All(nil) 应仍返回 5 个工具，实际 %d", len(tools))
	}

	args := []string{`{"title":"标题"}`, `{}`, `{"id":"todo-1"}`}
	for i, tl := range tools[2:] {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: 端口未注入时不得 panic：%v", tl.Spec().Name, r)
				}
			}()

			result, err := tl.Execute(context.Background(), tool.Request{
				ExecutionRef: core.ExecutionRef{SessionID: "session-a", RunID: "run-1"},
				Call: core.ToolCall{
					ID:        "call-1",
					Name:      tl.Spec().Name,
					Arguments: json.RawMessage(args[i]),
				},
			})
			if err == nil {
				t.Fatalf("%s: 端口未注入必须返回 error", tl.Spec().Name)
			}
			if kind := core.KindOf(err); kind != core.KindInternal {
				t.Fatalf("%s: 期望 %s，实际 %s", tl.Spec().Name, core.KindInternal, kind)
			}
			if result.IsError {
				t.Fatalf("%s: 端口未注入不得伪装为领域失败", tl.Spec().Name)
			}
		}()
	}
}

// 保证 fakeTodoPort 满足端口契约。
var _ TodoOperations = (*fakeTodoPort)(nil)
