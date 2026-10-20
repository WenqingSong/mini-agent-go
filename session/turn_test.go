package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TestTurnTodoOperationsReadYourWrites 覆盖 INV-008：Turn 内连续 Todo 操作看到自己尚未提交的修改，
// 视图是深拷贝，删除不存在的 Todo 幂等成功。
func TestTurnTodoOperationsReadYourWrites(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.PutTodo(Todo{ID: "todo-base", Title: "基线", Status: TodoStatusPending})
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}

	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if got := turn.Todos(); len(got) != 1 || got[0].ID != "todo-base" {
			return fmt.Errorf("应看到已提交基线，实际 %+v", got)
		}
		if err := turn.PutTodo(Todo{ID: "todo-base", Title: "基线", Status: TodoStatusCompleted}); err != nil {
			return err
		}
		if got := turn.Todos(); len(got) != 1 || got[0].Status != TodoStatusCompleted {
			return fmt.Errorf("应读到本 Turn 尚未提交的修改，实际 %+v", got)
		}
		if err := turn.PutTodo(Todo{ID: "todo-new", Title: "新增", Status: TodoStatusInProgress}); err != nil {
			return err
		}
		if got := turn.Todos(); len(got) != 2 || got[1].ID != "todo-new" {
			return fmt.Errorf("新 Todo 应追加到末尾，实际 %+v", got)
		}
		if err := turn.DeleteTodo("todo-new"); err != nil {
			return err
		}
		if err := turn.DeleteTodo("todo-missing"); err != nil {
			return fmt.Errorf("删除不存在的 Todo 应幂等成功：%v", err)
		}
		// 未提交的 Todo 不得出现在会话快照视角之外的数据里（视图是深拷贝）。
		if got := turn.Todos(); len(got) != 1 {
			return fmt.Errorf("删除后应只剩基线 Todo，实际 %+v", got)
		}
		view := turn.Todos()
		view[0].Status = TodoStatusCancelled
		if got := turn.Todos(); got[0].Status != TodoStatusCompleted {
			return fmt.Errorf("Todos 必须返回深拷贝，实际 %+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Todo 读写 Turn 应成功：%v", err)
	}

	final := mustGet(t, m, "s-1").Todos
	if len(final) != 1 || final[0].ID != "todo-base" || final[0].Status != TodoStatusCompleted {
		t.Fatalf("提交后的 Todo 不符合预期：%+v", final)
	}
}

// TestTurnAppendRejectsInvalidPairingWithoutChangingHistory 覆盖 AC-007 / INV-005：
// 非法消息序列被该批次原子拒绝，错误为 invalid_pairing 并定位 CallID，历史逐条不变。
func TestTurnAppendRejectsInvalidPairingWithoutChangingHistory(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	base := []core.Message{
		userMessage("m1", "读一下 go.mod"),
		assistantMessage("m2", newCall("call_1", "read_file", `{"path":"go.mod"}`)),
		toolMessage("m3", "call_1"),
	}
	if err := m.Save(ctx, Session{ID: "s-1", Messages: base, Summary: "基线摘要"}); err != nil {
		t.Fatalf("基线 Save 应成功：%v", err)
	}
	before := fingerprint(t, mustGet(t, m, "s-1").Messages)

	cases := []struct {
		name     string
		batch    []core.Message
		wantCall string
	}{
		{
			name:     "孤儿 Result 引用不存在的调用",
			batch:    []core.Message{toolMessage("m4", "call_orphan")},
			wantCall: "call_orphan",
		},
		{
			name:     "assistant 调用缺少对应的 Result",
			batch:    []core.Message{assistantMessage("m4", newCall("call_2", "echo", `{}`))},
			wantCall: "call_2",
		},
		{
			name:     "同一调用产生重复 Result",
			batch:    []core.Message{toolMessage("m4", "call_1")},
			wantCall: "call_1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var appendErr error
			err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				appendErr = turn.Append(tc.batch...)
				if appendErr == nil {
					return errors.New("非法批次必须被拒绝")
				}
				// 被拒批次不进入暂存区。
				if got := len(turn.Session().Messages); got != len(base) {
					return fmt.Errorf("被拒批次进入了暂存区，消息数 %d", got)
				}
				return nil
			})
			if err != nil {
				t.Fatalf("回调返回 nil 时应提交（暂存为空）：%v", err)
			}
			runErr := expectKind(t, appendErr, core.KindInvalidPairing)
			if runErr.CallID != tc.wantCall {
				t.Fatalf("错误应定位 CallID %q，实际 %q（错误：%v）", tc.wantCall, runErr.CallID, runErr)
			}
			if after := fingerprint(t, mustGet(t, m, "s-1").Messages); after != before {
				t.Fatalf("历史必须逐条不变：\n之前：%s\n之后：%s", before, after)
			}
		})
	}

	if got := mustGet(t, m, "s-1").Summary; got != "基线摘要" {
		t.Fatalf("被拒批次不得影响摘要，实际 %q", got)
	}
}

// TestTurnKeepsAcceptedStagingAfterRejectedBatch 覆盖 D2：单批校验失败只拒绝该批次，
// 不 poison 整个 Turn——回调仍返回 nil 时，此前已接受的暂存照常提交。
func TestTurnKeepsAcceptedStagingAfterRejectedBatch(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m-ok", "合法批次")); err != nil {
			return err
		}
		if err := turn.Append(toolMessage("m-bad", "call_orphan")); err == nil {
			return errors.New("非法批次必须被拒绝")
		} else if core.KindOf(err) != core.KindInvalidPairing {
			return fmt.Errorf("拒绝原因应为 invalid_pairing，实际 %v", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("Turn 应提交合法批次：%v", err)
	}

	history := mustGet(t, m, "s-1").Messages
	if len(history) != 1 || history[0].ID != "m-ok" {
		t.Fatalf("只应提交被接受的消息，实际 %+v", history)
	}
}

// TestTurnRejectsInvalidTodosAtomically 覆盖 INV-005 的 Todo 部分：
// 非法 Todo 被该项操作原子拒绝，暂存列表保持不变。
func TestTurnRejectsInvalidTodosAtomically(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.PutTodo(Todo{ID: "todo-base", Title: "基线", Status: TodoStatusPending})
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}

	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.PutTodo(Todo{ID: "", Title: "空 ID", Status: TodoStatusPending}); err == nil {
			return errors.New("空 ID 的 Todo 必须被拒绝")
		} else if core.KindOf(err) != core.KindInvalidArgument {
			return fmt.Errorf("空 ID 应返回 invalid_argument，实际 %v", err)
		}
		if err := turn.PutTodo(Todo{ID: "todo-x", Title: "未知状态", Status: TodoStatus("done")}); err == nil {
			return errors.New("未知状态的 Todo 必须被拒绝")
		} else if core.KindOf(err) != core.KindInvalidArgument {
			return fmt.Errorf("未知状态应返回 invalid_argument，实际 %v", err)
		}
		if err := turn.DeleteTodo(""); err == nil {
			return errors.New("删除空 ID 必须返回参数错误")
		} else if core.KindOf(err) != core.KindInvalidArgument {
			return fmt.Errorf("删除空 ID 应返回 invalid_argument，实际 %v", err)
		}
		if got := turn.Todos(); len(got) != 1 || got[0].ID != "todo-base" {
			return fmt.Errorf("被拒操作改变了暂存列表：%+v", got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 应成功提交：%v", err)
	}
	if got := mustGet(t, m, "s-1").Todos; len(got) != 1 || got[0].ID != "todo-base" {
		t.Fatalf("被拒操作影响了会话 Todo：%+v", got)
	}
}

// TestTurnCommitRejectsInvalidTodoFromSetTodos 覆盖 P5：SetTodos 没有错误返回，
// 非法 Todo 在提交前校验失败，整轮（含消息与摘要）都不提交。
func TestTurnCommitRejectsInvalidTodoFromSetTodos(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetSummary("基线摘要")
		return turn.Append(userMessage("m1", "基线"))
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}
	before := mustGet(t, m, "s-1")

	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetTodos([]Todo{
			{ID: "todo-ok", Title: "合法", Status: TodoStatusPending},
			{ID: "", Title: "非法", Status: TodoStatusPending},
		})
		turn.SetSummary("不应提交")
		return turn.Append(userMessage("m2", "不应提交"))
	})
	expectKind(t, err, core.KindInvalidArgument)
	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
		t.Fatalf("非法 Todo 使整轮不应提交：\n之前：%+v\n之后：%+v", before, after)
	}
}

// TestTurnWritesAfterCallbackReturnedAreRejected 覆盖 P3 / INV-008：
// 回调返回后 Turn 关闭，写方法返回 invalid_argument，只读方法仍返回安全快照。
func TestTurnWritesAfterCallbackReturnedAreRejected(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	var held *Turn
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		held = turn
		return nil
	}); err != nil {
		t.Fatalf("Turn 应成功：%v", err)
	}
	if held == nil {
		t.Fatal("未捕获 Turn")
	}

	expectKind(t, held.Append(userMessage("m1", "迟到写入")), core.KindInvalidArgument)
	expectKind(t, held.PutTodo(Todo{ID: "todo-1", Status: TodoStatusPending}), core.KindInvalidArgument)
	expectKind(t, held.DeleteTodo("todo-1"), core.KindInvalidArgument)
	if got := held.Session(); got.ID != "s-1" || len(got.Messages) != 0 {
		t.Fatalf("关闭后只读方法仍应返回安全快照，实际 %+v", got)
	}
	if got := held.Todos(); len(got) != 0 {
		t.Fatalf("关闭后 Todos 应返回安全快照，实际 %+v", got)
	}
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 || len(got.Todos) != 0 {
		t.Fatalf("关闭后的写入影响了会话：%+v", got)
	}
}

// TestTurnPanicDoesNotCommitAndReleasesAuthority 覆盖 panic 路径：
// 不 recover、不提交、执行权在 defer 中释放。
func TestTurnPanicDoesNotCommitAndReleasesAuthority(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.Append(userMessage("m1", "基线"))
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}
	before := mustGet(t, m, "s-1")

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Errorf("回调 panic 必须向上传播，不得被吞掉")
			}
		}()
		_ = m.RunTurn(ctx, "s-1", func(turn *Turn) error {
			if err := turn.Append(userMessage("m2", "panic 前的暂存")); err != nil {
				return err
			}
			turn.SetSummary("不应提交")
			panic("测试注入的 panic")
		})
	}()

	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
		t.Fatalf("panic 的 Turn 不得提交：\n之前：%+v\n之后：%+v", before, after)
	}
	if err := runTurnWithin(t, m, "s-1", 5*time.Second, func(turn *Turn) error { return nil }); err != nil {
		t.Fatalf("panic 后执行权未释放：%v", err)
	}
}
