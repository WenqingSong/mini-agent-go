package session

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// baseHistory 构造前缀删除用例的已提交历史：system / user / assistant(ToolCall) / tool。
func baseHistory() []core.Message {
	return []core.Message{
		textMessage("m1", core.Role("system"), "系统提示"),
		userMessage("m2", "读一下 go.mod"),
		assistantMessage("m3", newCall("call_1", "read_file", `{"path":"go.mod"}`)),
		toolMessage("m4", "call_1"),
	}
}

// mustSaveHistory 把消息写为会话基线，摘要固定为「基线摘要」。
func mustSaveHistory(t *testing.T, m *Manager, id string, msgs []core.Message) {
	t.Helper()
	if err := m.Save(context.Background(), Session{ID: id, Messages: msgs, Summary: "基线摘要"}); err != nil {
		t.Fatalf("写入基线历史失败：%v", err)
	}
}

// messageIDs 提取消息 ID，用于断言条数与顺序。
func messageIDs(msgs []core.Message) []string {
	ids := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		ids = append(ids, msg.ID)
	}
	return ids
}

// assertMessages 逐条比对消息序列：JSON 指纹覆盖 ID、角色、内容、调用与结果。
func assertMessages(t *testing.T, label string, got, want []core.Message) {
	t.Helper()
	if gotPrint, wantPrint := fingerprint(t, got), fingerprint(t, want); gotPrint != wantPrint {
		t.Fatalf("%s 消息序列不符合预期：\n实际：%s\n期望：%s", label, gotPrint, wantPrint)
	}
}

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

// TestTurnDropLeadingCommitsTruncatedHistory 覆盖 AC-001 / INV-001 / INV-002：
// 前缀删除必须经 RunTurn 提交进入已提交快照，提交后从 Manager.Get 读到的历史恰为
// 「原历史第 n 条之后的部分 + 本 Turn 新增消息」，条数为 m-n+k。
func TestTurnDropLeadingCommitsTruncatedHistory(t *testing.T) {
	ctx := context.Background()
	base := baseHistory()
	cases := []struct {
		name  string
		drop  int
		extra []core.Message
	}{
		{name: "n=0 为空操作", drop: 0, extra: []core.Message{userMessage("m5", "新增问题")}},
		{name: "删除中间前缀", drop: 2, extra: []core.Message{userMessage("m5", "新增问题")}},
		{name: "删除全部已提交历史", drop: len(base), extra: []core.Message{userMessage("m5", "新增问题")}},
		{name: "清空且不追加", drop: len(base)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(ManagerConfig{})
			mustCreate(t, m, "s-1")
			mustSaveHistory(t, m, "s-1", base)

			want := append(cloneMessages(base[tc.drop:]), cloneMessages(tc.extra)...)
			var view []core.Message
			if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				if err := turn.DropLeading(tc.drop); err != nil {
					return err
				}
				if len(tc.extra) > 0 {
					if err := turn.Append(tc.extra...); err != nil {
						return err
					}
				}
				view = turn.Session().Messages
				return nil
			}); err != nil {
				t.Fatalf("Turn 应成功提交：%v", err)
			}

			if got, wantIDs := len(view), len(want); got != wantIDs {
				t.Fatalf("Turn 读视图条数应为 %d，实际 %d（%v）", wantIDs, got, messageIDs(view))
			}
			assertMessages(t, "Turn 读视图", view, want)

			committed := mustGet(t, m, "s-1")
			assertMessages(t, "提交后的已提交历史", committed.Messages, want)
			if committed.Summary != "基线摘要" {
				t.Fatalf("前缀删除不得改变摘要，实际 %q", committed.Summary)
			}
		})
	}
}

// TestTurnDropLeadingRejectsInvalidCountAtomically 覆盖 AC-002 / INV-004：
// 负数与超过「当前剩余已提交历史」条数的 n 返回 invalid_argument；被拒调用不改变
// 已提交历史与暂存消息，Turn 不关闭，换合法参数仍能成功提交。
func TestTurnDropLeadingRejectsInvalidCountAtomically(t *testing.T) {
	ctx := context.Background()
	base := baseHistory()
	staged := []core.Message{userMessage("m5", "本 Turn 新增 1"), userMessage("m6", "本 Turn 新增 2")}
	cases := []struct {
		name  string
		stage []core.Message
		drop  int
	}{
		{name: "负数", drop: -1},
		{name: "超过已提交历史长度", drop: len(base) + 1},
		{name: "已追加消息后仍不能删到本 Turn 新增消息", stage: staged, drop: len(base) + 1},
		{name: "已追加消息后删到暂存区更深处", stage: staged, drop: len(base) + len(staged)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(ManagerConfig{})
			mustCreate(t, m, "s-1")
			mustSaveHistory(t, m, "s-1", base)

			err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				if len(tc.stage) > 0 {
					if err := turn.Append(tc.stage...); err != nil {
						return err
					}
				}
				before := fingerprint(t, turn.Session().Messages)
				runErr := expectKind(t, turn.DropLeading(tc.drop), core.KindInvalidArgument)
				if runErr.Op != "session.Turn.DropLeading" {
					return fmt.Errorf("错误应定位到 DropLeading，实际 Op=%q", runErr.Op)
				}
				for _, want := range []string{fmt.Sprintf("%d", tc.drop), fmt.Sprintf("[0, %d]", len(base))} {
					if !strings.Contains(runErr.Message, want) {
						return fmt.Errorf("错误文本应包含 %q，实际 %q", want, runErr.Message)
					}
				}
				if after := fingerprint(t, turn.Session().Messages); after != before {
					return fmt.Errorf("被拒删除改变了暂存视图：\n之前：%s\n之后：%s", before, after)
				}
				// 单次调用原子拒绝：Turn 未失效，换合法参数仍可成功。
				return turn.DropLeading(1)
			})
			if err != nil {
				t.Fatalf("被拒删除后 Turn 应仍可提交：%v", err)
			}

			want := append(cloneMessages(base[1:]), cloneMessages(tc.stage)...)
			assertMessages(t, "提交后的已提交历史", mustGet(t, m, "s-1").Messages, want)
		})
	}
}

// TestTurnDropLeadingRejectsInvalidPairingBoundary 覆盖 AC-003 / INV-003：
// 删除边界使「剩余历史 + 暂存消息」违反配对约束时，调用点原子拒绝并定位 CallID，
// 视图逐条不变；换合法边界后同一 Turn 仍可成功提交。
// 注意：前缀删除不可能"保留调用但删掉其结果"——结果总在调用之后，因此越界边界
// 在可观察上表现为"剩余历史以孤儿 Tool Result 开头"。
func TestTurnDropLeadingRejectsInvalidPairingBoundary(t *testing.T) {
	ctx := context.Background()
	base := []core.Message{
		userMessage("m1", "第一次调用"),
		assistantMessage("m2", newCall("call_1", "read_file", `{"path":"go.mod"}`)),
		toolMessage("m3", "call_1"),
		userMessage("m4", "第二次调用"),
		assistantMessage("m5", newCall("call_2", "echo", `{}`)),
		toolMessage("m6", "call_2"),
	}
	cases := []struct {
		name     string
		stage    []core.Message
		drop     int
		wantCall string
	}{
		{name: "边界切断调用与其结果", drop: 2, wantCall: "call_1"},
		{name: "删除后残留更后面调用的孤儿结果", drop: 5, wantCall: "call_2"},
		{name: "暂存消息与剩余历史一起参与校验", stage: []core.Message{userMessage("m7", "暂存消息")}, drop: 2, wantCall: "call_1"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := NewManager(ManagerConfig{})
			mustCreate(t, m, "s-1")
			mustSaveHistory(t, m, "s-1", base)

			err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				if len(tc.stage) > 0 {
					if err := turn.Append(tc.stage...); err != nil {
						return err
					}
				}
				before := fingerprint(t, turn.Session().Messages)
				runErr := expectKind(t, turn.DropLeading(tc.drop), core.KindInvalidPairing)
				if runErr.CallID != tc.wantCall {
					return fmt.Errorf("错误应定位 CallID %q，实际 %q（错误：%v）", tc.wantCall, runErr.CallID, runErr)
				}
				if after := fingerprint(t, turn.Session().Messages); after != before {
					return fmt.Errorf("被拒删除改变了暂存视图：\n之前：%s\n之后：%s", before, after)
				}
				// 换合法边界（保留完整调用链）后同一 Turn 仍可成功。
				return turn.DropLeading(3)
			})
			if err != nil {
				t.Fatalf("被拒删除后 Turn 应仍可提交：%v", err)
			}

			want := append(cloneMessages(base[3:]), cloneMessages(tc.stage)...)
			assertMessages(t, "提交后的已提交历史", mustGet(t, m, "s-1").Messages, want)
		})
	}
}

// TestTurnDropLeadingCombinesWithSummaryAndAppend 覆盖 AC-004 成功路径：
// 同一次 Turn 内的前缀删除、摘要更新与消息追加一起提交，一次读取同时断言三者。
func TestTurnDropLeadingCombinesWithSummaryAndAppend(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	base := baseHistory()
	mustSaveHistory(t, m, "s-1", base)

	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetSummary("压缩后的摘要")
		if err := turn.DropLeading(2); err != nil {
			return err
		}
		return turn.Append(userMessage("m5", "压缩后的新问题"))
	}); err != nil {
		t.Fatalf("组合提交应成功：%v", err)
	}

	got := mustGet(t, m, "s-1")
	if want := "压缩后的摘要"; got.Summary != want {
		t.Fatalf("摘要应为 %q，实际 %q", want, got.Summary)
	}
	want := append(cloneMessages(base[2:]), userMessage("m5", "压缩后的新问题"))
	assertMessages(t, "组合提交后的历史", got.Messages, want)
}

// TestTurnDropLeadingFailureDiscardsWholeTurn 覆盖 AC-004 / INV-005 失败路径：
// 已暂存的前缀删除、摘要与追加消息遇到后续非法删除时，三者全部不提交。
func TestTurnDropLeadingFailureDiscardsWholeTurn(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	mustSaveHistory(t, m, "s-1", baseHistory())
	before := mustGet(t, m, "s-1")

	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetSummary("不应提交的摘要")
		if err := turn.DropLeading(2); err != nil {
			return err
		}
		if err := turn.Append(userMessage("m5", "不应提交的消息")); err != nil {
			return err
		}
		// 剩余历史以 m4 的 Tool Result 开头（其调用已被删除）→ 本次调用被拒绝。
		return turn.DropLeading(1)
	})
	expectKind(t, err, core.KindInvalidPairing)

	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
		t.Fatalf("被拒写入使整轮不得提交：\n之前：%+v\n之后：%+v", before, after)
	}
}

// TestTurnDropLeadingDiscardedOnFailureCancelAndTimeout 覆盖 AC-005 / INV-005 / INV-006：
// 回调失败、取消、超时都整轮丢弃已暂存的前缀删除；Turn 关闭后 DropLeading 返回 invalid_argument。
func TestTurnDropLeadingDiscardedOnFailureCancelAndTimeout(t *testing.T) {
	ctx := context.Background()
	base := baseHistory()

	// 每个子用例使用独立 Manager，断言失败前后会话逐字段相等。
	newManager := func(t *testing.T) (*Manager, Session) {
		t.Helper()
		m := NewManager(ManagerConfig{})
		mustCreate(t, m, "s-1")
		mustSaveHistory(t, m, "s-1", base)
		return m, mustGet(t, m, "s-1")
	}
	assertUnchanged := func(t *testing.T, m *Manager, before Session) {
		t.Helper()
		if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
			t.Fatalf("失败的 Turn 不得提交：\n之前：%+v\n之后：%+v", before, after)
		}
	}

	t.Run("回调返回错误", func(t *testing.T) {
		m, before := newManager(t)
		err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
			if err := turn.DropLeading(2); err != nil {
				return err
			}
			turn.SetSummary("不应提交的摘要")
			return errInjected
		})
		expectKind(t, err, core.KindInternal)
		assertUnchanged(t, m, before)
	})

	t.Run("回调内取消", func(t *testing.T) {
		m, before := newManager(t)
		turnCtx, cancel := context.WithCancel(ctx)
		err := m.RunTurn(turnCtx, "s-1", func(turn *Turn) error {
			if err := turn.DropLeading(2); err != nil {
				return err
			}
			turn.SetSummary("取消的摘要")
			cancel()
			return nil
		})
		expectKind(t, err, core.KindCancelled)
		assertUnchanged(t, m, before)
	})

	t.Run("回调内超时", func(t *testing.T) {
		m, before := newManager(t)
		turnCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		err := m.RunTurn(turnCtx, "s-1", func(turn *Turn) error {
			if err := turn.DropLeading(2); err != nil {
				return err
			}
			turn.SetSummary("超时的摘要")
			<-turnCtx.Done()
			return nil
		})
		expectKind(t, err, core.KindDeadlineExceeded)
		assertUnchanged(t, m, before)
	})

	t.Run("回调返回后拒绝写入", func(t *testing.T) {
		m, before := newManager(t)
		var held *Turn
		if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
			held = turn
			return nil
		}); err != nil {
			t.Fatalf("Turn 应成功：%v", err)
		}
		runErr := expectKind(t, held.DropLeading(1), core.KindInvalidArgument)
		if runErr.Op != "session.Turn.DropLeading" {
			t.Fatalf("错误应定位到 DropLeading，实际 Op=%q", runErr.Op)
		}
		if runErr.Message != "Turn 已结束，不再接受写入" {
			t.Fatalf("关闭后错误文本不符合契约，实际 %q", runErr.Message)
		}
		assertUnchanged(t, m, before)
	})
}
