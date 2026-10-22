package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/builtin"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/httpapi"
	"cnb.cool/framework-station/mini-agent-go/llm"
	"cnb.cool/framework-station/mini-agent-go/session"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// productionToolNames 是正式装配必须暴露的工具清单（MapRegistry 按名称字典序返回）。
var productionToolNames = []string{"calculator", "mock_search", "todo_complete", "todo_create", "todo_list"}

// TestNewToolsetRegistersProductionInventory 核对正式注册表：五类工具齐全、Schema 完整、已封闭。
func TestNewToolsetRegistersProductionInventory(t *testing.T) {
	registry, err := NewToolset(nil)
	if err != nil {
		t.Fatalf("构造正式工具注册表失败：%v", err)
	}

	specs := registry.Specs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
		if spec.Description == "" {
			t.Fatalf("%s: 描述为空，模型无法判断何时调用", spec.Name)
		}
		var schema map[string]any
		if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
			t.Fatalf("%s: Parameters 不是合法 JSON：%v", spec.Name, err)
		}
		if schema["type"] != "object" {
			t.Fatalf("%s: 根 Schema 必须是 object，实际 %v", spec.Name, schema["type"])
		}
		if _, ok := registry.Lookup(spec.Name); !ok {
			t.Fatalf("%s: 已在 Specs 中声明但无法 Lookup", spec.Name)
		}
	}
	if !reflect.DeepEqual(names, productionToolNames) {
		t.Fatalf("工具清单 = %v，期望 %v", names, productionToolNames)
	}

	// 已封闭：装配期之后的注册被拒绝，运行期不能再改变模型可见清单。
	mapRegistry, ok := registry.(*tool.MapRegistry)
	if !ok {
		t.Fatalf("NewToolset 返回的注册表类型 = %T，期望 *tool.MapRegistry", registry)
	}
	if err := mapRegistry.Register(builtin.NewCalculator()); !errors.Is(err, tool.ErrRegistrySealed) {
		t.Fatalf("封闭后注册错误 = %v，期望 ErrRegistrySealed", err)
	}
}

// TestTurnTodoOperationsLifecycle 核对 Turn 绑定端口：创建 → 列表 → 完成，且随 Turn 提交。
func TestTurnTodoOperationsLifecycle(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	ctx := context.Background()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		ops := newTurnTodoOperations(turn, "s-1")

		first, createErr := ops.Create(ctx, "s-1", "  写周报  ")
		if createErr != nil {
			t.Fatalf("创建待办失败：%v", createErr)
		}
		if first.ID != "todo-1" || first.Title != "  写周报  " || first.Status != session.TodoStatusPending {
			t.Fatalf("创建结果 = %+v，期望 {todo-1, 原文标题, pending}", first)
		}

		second, createErr := ops.Create(ctx, "s-1", "复盘")
		if createErr != nil {
			t.Fatalf("创建第二条待办失败：%v", createErr)
		}
		if second.ID != "todo-2" {
			t.Fatalf("第二条待办 ID = %q，期望会话内唯一递增的 todo-2", second.ID)
		}

		// read-your-writes：同一 Turn 内立即可见两条。
		list, listErr := ops.List(ctx, "s-1")
		if listErr != nil {
			t.Fatalf("列出待办失败：%v", listErr)
		}
		if len(list) != 2 || list[0].ID != "todo-1" || list[1].ID != "todo-2" {
			t.Fatalf("同 Turn 列表 = %+v，期望 [todo-1 todo-2]", list)
		}

		completed, completeErr := ops.Complete(ctx, "s-1", "todo-1")
		if completeErr != nil {
			t.Fatalf("完成待办失败：%v", completeErr)
		}
		if completed.Status != session.TodoStatusCompleted {
			t.Fatalf("完成结果状态 = %q，期望 completed", completed.Status)
		}

		// 幂等：已完成的待办再次完成不报错、不改写顺序。
		again, completeErr := ops.Complete(ctx, "s-1", "todo-1")
		if completeErr != nil {
			t.Fatalf("重复完成失败：%v", completeErr)
		}
		if again != completed {
			t.Fatalf("重复完成结果 = %+v，期望与首次一致 %+v", again, completed)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}

	committed := snapshot(t, manager, "s-1").Todos
	want := []session.Todo{
		{ID: "todo-1", Title: "  写周报  ", Status: session.TodoStatusCompleted},
		{ID: "todo-2", Title: "复盘", Status: session.TodoStatusPending},
	}
	if !reflect.DeepEqual(committed, want) {
		t.Fatalf("提交后 Todo = %+v，期望 %+v", committed, want)
	}
}

// TestTurnTodoOperationsRollback 核对 Turn 失败时本轮的 Todo 写入整轮丢弃（无部分提交）。
func TestTurnTodoOperationsRollback(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	seeded := []session.Todo{{ID: "seed-1", Title: "既有待办", Status: session.TodoStatusPending}}
	if err := manager.Save(context.Background(), session.Session{ID: "s-1", Todos: seeded}); err != nil {
		t.Fatalf("预置 Todo 失败：%v", err)
	}
	before := snapshot(t, manager, "s-1")
	ctx := context.Background()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		if _, createErr := newTurnTodoOperations(turn, "s-1").Create(ctx, "s-1", "本轮新增"); createErr != nil {
			t.Fatalf("创建待办失败：%v", createErr)
		}
		return core.NewError(core.KindProviderFailure, "service.test", "模拟本轮失败")
	})
	if err == nil {
		t.Fatal("回调返回错误时 RunTurn 必须失败")
	}

	after := snapshot(t, manager, "s-1")
	if !reflect.DeepEqual(before.Todos, after.Todos) {
		t.Fatalf("失败轮次提交了 Todo：before=%+v after=%+v", before.Todos, after.Todos)
	}
}

// TestTurnTodoOperationsNotFound 核对不存在的标识：返回哨兵且不产生写入。
func TestTurnTodoOperationsNotFound(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	ctx := context.Background()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		ops := newTurnTodoOperations(turn, "s-1")

		if _, completeErr := ops.Complete(ctx, "s-1", "todo-404"); !errors.Is(completeErr, builtin.ErrTodoNotFound) {
			t.Fatalf("未知标识错误 = %v，期望 ErrTodoNotFound", completeErr)
		}
		if _, createErr := ops.Create(ctx, "s-1", "唯一待办"); createErr != nil {
			t.Fatalf("创建待办失败：%v", createErr)
		}
		if _, completeErr := ops.Complete(ctx, "s-1", "todo-404"); !errors.Is(completeErr, builtin.ErrTodoNotFound) {
			t.Fatalf("未知标识错误 = %v，期望 ErrTodoNotFound", completeErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}

	todos := snapshot(t, manager, "s-1").Todos
	if len(todos) != 1 || todos[0].ID != "todo-1" || todos[0].Status != session.TodoStatusPending {
		t.Fatalf("错误标识产生了副作用：%+v", todos)
	}
}

// TestTurnTodoOperationsRejectsOtherSession 核对端口只接受构造时绑定的可信会话身份。
func TestTurnTodoOperationsRejectsOtherSession(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	seedSession(t, manager, "s-2")
	ctx := context.Background()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		ops := newTurnTodoOperations(turn, "s-1")

		if _, createErr := ops.Create(ctx, "s-2", "越权写入"); core.KindOf(createErr) != core.KindInvalidArgument {
			t.Fatalf("跨会话创建错误 = %v，期望 invalid_argument", createErr)
		}
		if _, listErr := ops.List(ctx, "s-2"); core.KindOf(listErr) != core.KindInvalidArgument {
			t.Fatalf("跨会话列出错误 = %v，期望 invalid_argument", listErr)
		}
		if _, completeErr := ops.Complete(ctx, "s-2", "todo-1"); core.KindOf(completeErr) != core.KindInvalidArgument {
			t.Fatalf("跨会话完成错误 = %v，期望 invalid_argument", completeErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}
	if len(snapshot(t, manager, "s-1").Todos) != 0 || len(snapshot(t, manager, "s-2").Todos) != 0 {
		t.Fatal("跨会话调用产生了待办写入")
	}
}

// TestTurnTodoOperationsCancelledDoesNotWrite 核对 ctx 已结束时端口先拒绝，不产生写入。
func TestTurnTodoOperationsCancelledDoesNotWrite(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	ctx := context.Background()

	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		ops := newTurnTodoOperations(turn, "s-1")

		if _, createErr := ops.Create(cancelled, "s-1", "取消后不应写入"); !errors.Is(createErr, context.Canceled) {
			t.Fatalf("已取消 ctx 的错误 = %v，期望 context.Canceled", createErr)
		}
		if list, listErr := ops.List(cancelled, "s-1"); listErr == nil || len(list) != 0 {
			t.Fatalf("已取消 ctx 的列表 = (%+v,%v)，期望错误且无结果", list, listErr)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}
	if len(snapshot(t, manager, "s-1").Todos) != 0 {
		t.Fatal("取消的调用产生了待办写入")
	}
}

// TestTurnTodoOperationsRejectsWriteAfterTurnClosed 核对 Turn 关闭后的迟到写入被拒绝：
// 工具执行超时被调用方放弃后，其 goroutine 不可能再改动已失效的 Turn。
func TestTurnTodoOperationsRejectsWriteAfterTurnClosed(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	ctx := context.Background()

	var late *turnTodoOperations
	if err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		late = newTurnTodoOperations(turn, "s-1")
		return nil
	}); err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}

	if _, err := late.Create(ctx, "s-1", "Turn 关闭后写入"); err == nil {
		t.Fatal("Turn 关闭后仍允许写入：迟到 goroutine 可能篡改会话")
	}
	if len(snapshot(t, manager, "s-1").Todos) != 0 {
		t.Fatal("Turn 关闭后的写入进入了会话状态")
	}
}

// TestTurnTodoOperationsIDIsUniquePerSession 核对 ID 生成只依赖本会话既有待办：
// 预置的、非常规格式的 ID 不会被覆盖，也不会阻塞新 ID 推导。
func TestTurnTodoOperationsIDIsUniquePerSession(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	seeded := []session.Todo{
		{ID: "todo-1", Title: "已占用 1", Status: session.TodoStatusPending},
		{ID: "custom-id", Title: "自定义标识", Status: session.TodoStatusCompleted},
	}
	if err := manager.Save(context.Background(), session.Session{ID: "s-1", Todos: seeded}); err != nil {
		t.Fatalf("预置 Todo 失败：%v", err)
	}
	ctx := context.Background()

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		created, createErr := newTurnTodoOperations(turn, "s-1").Create(ctx, "s-1", "新待办")
		if createErr != nil {
			t.Fatalf("创建待办失败：%v", createErr)
		}
		if created.ID != "todo-2" {
			t.Fatalf("新 ID = %q，期望跳过已占用的 todo-1", created.ID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}

	todos := snapshot(t, manager, "s-1").Todos
	if len(todos) != 3 {
		t.Fatalf("Todo 数量 = %d，期望 3（两条预置 + 一条新增）", len(todos))
	}
	if todos[0].ID != "todo-1" || todos[1].ID != "custom-id" || todos[2].ID != "todo-2" {
		t.Fatalf("Todo 序列 = %+v，期望预置顺序不变、新增追加到末尾", todos)
	}
}

// TestRunTodoToolsBindCurrentTurn 通过装配层证明 Todo 工具由真实生产注册表进入模型工具清单，
// 并且连续两轮的写入落在同一会话、ID 不冲突。
func TestRunTodoToolsBindCurrentTurn(t *testing.T) {
	provider := &scriptedProvider{script: []llm.Response{
		callsResponse("resp-1", toolCall("call-1", "todo_create", `{"title":"准备周报"}`)),
		textResponse("resp-2", "已记录"),
		callsResponse("resp-3", toolCall("call-2", "todo_create", `{"title":"复盘会议"}`)),
		textResponse("resp-4", "已记录"),
	}}
	manager := newManager()
	seedSession(t, manager, "s-1")
	runner := newTestRunner(t, Deps{
		Provider: provider,
		Sessions: manager,
		Tools:    NewToolset,
	}, Config{})

	for _, runID := range []string{"run-1", "run-2"} {
		outcome, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: runID, Message: "记一条待办"})
		if err != nil {
			t.Fatalf("%s 返回错误：%v", runID, err)
		}
		if outcome.Status != core.RunStatusCompleted || outcome.ToolCalls != 1 {
			t.Fatalf("%s 结论 = (%s, tool=%d)，期望 (COMPLETED, 1)", runID, outcome.Status, outcome.ToolCalls)
		}
	}

	// 模型每轮都收到了完整工具清单（Todo 未因缺少端口而缺席）。
	firstRequest := provider.requestAt(t, 1)
	names := make([]string, 0, len(firstRequest.Tools))
	for _, spec := range firstRequest.Tools {
		names = append(names, spec.Name)
	}
	if !reflect.DeepEqual(names, productionToolNames) {
		t.Fatalf("模型收到的工具 = %v，期望 %v", names, productionToolNames)
	}

	committed := snapshot(t, manager, "s-1")
	want := []session.Todo{
		{ID: "todo-1", Title: "准备周报", Status: session.TodoStatusPending},
		{ID: "todo-2", Title: "复盘会议", Status: session.TodoStatusPending},
	}
	if !reflect.DeepEqual(committed.Todos, want) {
		t.Fatalf("会话 Todo = %+v，期望 %+v", committed.Todos, want)
	}
	for _, callID := range []string{"call-1", "call-2"} {
		result := toolResultOf(t, committed.Messages, callID)
		if result.IsError || !strings.Contains(result.Content, `"status":"pending"`) {
			t.Fatalf("%s 的工具结果 = %+v，期望成功的 pending 待办", callID, result)
		}
	}
}

// TestRunTodoToolNotFoundIsRecoverable 核对「待办不存在」是可恢复的领域失败：
// 工具返回错误内容、Run 仍然 COMPLETED，会话照常提交。
func TestRunTodoToolNotFoundIsRecoverable(t *testing.T) {
	provider := &scriptedProvider{script: []llm.Response{
		callsResponse("resp-1", toolCall("call-1", "todo_complete", `{"id":"todo-404"}`)),
		textResponse("resp-2", "该待办不存在"),
	}}
	manager := newManager()
	seedSession(t, manager, "s-1")
	runner := newTestRunner(t, Deps{Provider: provider, Sessions: manager, Tools: NewToolset}, Config{})

	outcome, err := runner.Run(context.Background(), httpapi.RunRequest{SessionID: "s-1", RunID: "run-1", Message: "完成不存在的待办"})
	if err != nil {
		t.Fatalf("Run 返回错误：%v", err)
	}
	if outcome.Status != core.RunStatusCompleted {
		t.Fatalf("终态 = %q，期望 COMPLETED（领域失败可恢复）", outcome.Status)
	}

	committed := snapshot(t, manager, "s-1")
	result := toolResultOf(t, committed.Messages, "call-1")
	if !result.IsError || !strings.Contains(result.Content, `"code":"todo_not_found"`) {
		t.Fatalf("工具结果 = %+v，期望 todo_not_found 领域失败", result)
	}
	if len(committed.Todos) != 0 {
		t.Fatalf("失败调用产生了 Todo：%+v", committed.Todos)
	}
}

// TestTurnTodoOperationsConcurrentCreate 核对同一 Turn 内的并发创建：ID 不冲突、无覆盖，
// 提交后条目数与并发度一致（工具串行执行时也成立，这里额外覆盖未来并行调度的风险）。
func TestTurnTodoOperationsConcurrentCreate(t *testing.T) {
	manager := newManager()
	seedSession(t, manager, "s-1")
	ctx := context.Background()
	const concurrency = 8

	err := manager.RunTurn(ctx, "s-1", func(turn *session.Turn) error {
		ops := newTurnTodoOperations(turn, "s-1")
		var group sync.WaitGroup
		failures := make(chan error, concurrency)
		for index := 0; index < concurrency; index++ {
			group.Add(1)
			go func(index int) {
				defer group.Done()
				_, createErr := ops.Create(ctx, "s-1", fmt.Sprintf("并发-%d", index))
				failures <- createErr
			}(index)
		}
		group.Wait()
		close(failures)
		for createErr := range failures {
			if createErr != nil {
				return createErr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 提交失败：%v", err)
	}

	todos := snapshot(t, manager, "s-1").Todos
	if len(todos) != concurrency {
		t.Fatalf("并发创建后 Todo 数 = %d，期望 %d（ID 冲突会互相覆盖）", len(todos), concurrency)
	}
	seen := make(map[string]bool, concurrency)
	for _, todo := range todos {
		if seen[todo.ID] {
			t.Fatalf("并发创建出现重复 ID %q：%+v", todo.ID, todos)
		}
		seen[todo.ID] = true
	}
}

// toolResultOf 返回会话中指定 CallID 的工具结果。
func toolResultOf(t *testing.T, messages []core.Message, callID string) core.ToolResult {
	t.Helper()
	for _, message := range messages {
		if message.ToolResult != nil && message.ToolResult.CallID == callID {
			return *message.ToolResult
		}
	}
	t.Fatalf("会话中不存在 CallID=%q 的工具结果", callID)
	return core.ToolResult{}
}
