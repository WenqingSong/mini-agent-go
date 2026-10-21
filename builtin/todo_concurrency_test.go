package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// 本文件的证据边界：只用并发安全的测试专用 Fake 证明「工具实例无共享可变状态、
// 并发调用无数据竞争、按会话标识透传」。它不证明真实会话隔离、跨会话不可写或
// 持久化并发（这些属于 Session Manager 与装配层的运行期证据）。

func executeAsync(t *testing.T, tl tool.Tool, sessionID, callID, args string) (core.ToolResult, error) {
	t.Helper()

	return tl.Execute(context.Background(), tool.Request{
		ExecutionRef: core.ExecutionRef{SessionID: sessionID, RunID: "run-1"},
		Call: core.ToolCall{
			ID:        callID,
			Name:      tl.Spec().Name,
			Arguments: json.RawMessage(args),
		},
	})
}

func TestTodoConcurrentCreate(t *testing.T) {
	const n = 50

	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	list := NewTodoList(port)

	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			result, err := executeAsync(t, create, "session-a", fmt.Sprintf("call-%d", i), fmt.Sprintf(`{"title":"并发-%d"}`, i))
			if err != nil {
				t.Errorf("并发创建不应返回 error：%v", err)
				return
			}
			if result.IsError {
				t.Errorf("并发创建不应失败：%s", result.Content)
				return
			}

			var payload todoPayload
			if err := json.Unmarshal([]byte(result.Content), &payload); err != nil {
				t.Errorf("结果不是合法 JSON：%v", err)
				return
			}
			ids <- payload.Todo.ID
		}(i)
	}
	wg.Wait()
	close(ids)

	unique := make(map[string]bool, n)
	for id := range ids {
		unique[id] = true
	}
	if len(unique) != n {
		t.Fatalf("并发创建后标识应互不重复，期望 %d 个，实际 %d 个", n, len(unique))
	}

	listed := runTool(t, list, "session-a", `{}`)
	var payload todoListPayload
	if err := json.Unmarshal([]byte(listed.Content), &payload); err != nil {
		t.Fatalf("列表结果不是合法 JSON：%v", err)
	}
	if len(payload.Todos) != n {
		t.Fatalf("并发创建后列表应恰为 %d 条，实际 %d 条", n, len(payload.Todos))
	}
}

func TestTodoConcurrentCompleteSameID(t *testing.T) {
	const n = 10

	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	complete := NewTodoComplete(port)

	var created todoPayload
	result := runTool(t, create, "session-a", `{"title":"并发完成"}`)
	if err := json.Unmarshal([]byte(result.Content), &created); err != nil {
		t.Fatalf("创建结果不是合法 JSON：%v", err)
	}

	args := fmt.Sprintf(`{"id":%q}`, created.Todo.ID)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()

			res, err := executeAsync(t, complete, "session-a", fmt.Sprintf("call-%d", i), args)
			if err != nil {
				t.Errorf("并发完成不应返回 error：%v", err)
				return
			}
			if res.IsError {
				t.Errorf("并发完成不应失败：%s", res.Content)
			}
		}(i)
	}
	wg.Wait()

	snapshot := port.snapshot("session-a")
	if len(snapshot) != 1 {
		t.Fatalf("并发完成不得产生部分写入，期望 1 条，实际 %d 条", len(snapshot))
	}
	if snapshot[0].Status != todoStatusCompleted {
		t.Fatalf("并发完成后终态应为 completed，实际 %q", snapshot[0].Status)
	}
}

func TestTodoConcurrentTwoSessions(t *testing.T) {
	const perSession = 25

	port := newFakeTodoPort()
	create := NewTodoCreate(port)
	list := NewTodoList(port)

	var wg sync.WaitGroup
	for _, sessionID := range []string{"session-a", "session-b"} {
		for i := 0; i < perSession; i++ {
			wg.Add(1)
			go func(sessionID string, i int) {
				defer wg.Done()

				_, err := executeAsync(t, create, sessionID, fmt.Sprintf("%s-%d", sessionID, i), fmt.Sprintf(`{"title":"%s-%d"}`, sessionID, i))
				if err != nil {
					t.Errorf("%s 并发创建不应返回 error：%v", sessionID, err)
				}
			}(sessionID, i)
		}
	}
	wg.Wait()

	idsOf := func(sessionID string) map[string]bool {
		listed := runTool(t, list, sessionID, `{}`)
		var payload todoListPayload
		if err := json.Unmarshal([]byte(listed.Content), &payload); err != nil {
			t.Fatalf("%s 列表结果不是合法 JSON：%v", sessionID, err)
		}
		result := make(map[string]bool, len(payload.Todos))
		for _, todo := range payload.Todos {
			result[todo.ID] = true
		}
		return result
	}

	a := idsOf("session-a")
	b := idsOf("session-b")

	if len(a) != perSession || len(b) != perSession {
		t.Fatalf("每个会话应各有 %d 条，实际 a=%d b=%d", perSession, len(a), len(b))
	}
	for id := range a {
		if b[id] {
			t.Fatalf("会话列表不得包含对方标识：%s", id)
		}
	}
}
