package session

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// startBlockingTurn 启动一个进入执行后阻塞的 Turn，返回释放函数与结束错误通道。
// 返回时该 Turn 已确认进入执行，即已持有会话执行权（超时只作卡死保护）。
func startBlockingTurn(t *testing.T, m *Manager, sessionID string) (func(), <-chan error) {
	t.Helper()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- m.RunTurn(context.Background(), sessionID, func(turn *Turn) error {
			close(started)
			<-release
			return nil
		})
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatalf("Turn 未能进入执行")
	}
	return func() { close(release) }, done
}

// TestQueuedTurnCancelledBeforeExecution 覆盖 AC-006(a) / INV-014：
// 执行权被占用时排队请求的 ctx 取消 → cancelled，回调不执行、历史不变、执行权不泄漏。
func TestQueuedTurnCancelledBeforeExecution(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	release, holderDone := startBlockingTurn(t, m, "s-1")

	var executed atomic.Bool
	waitCtx, cancel := context.WithCancel(ctx)
	waitDone := make(chan error, 1)
	launched := make(chan struct{})
	go func() {
		close(launched)
		waitDone <- m.RunTurn(waitCtx, "s-1", func(turn *Turn) error {
			executed.Store(true)
			return turn.Append(userMessage("m-queued", "排队请求不应执行"))
		})
	}()
	<-launched
	cancel()

	err := <-waitDone
	expectKind(t, err, core.KindCancelled)
	if executed.Load() {
		t.Fatal("排队取消后回调不得执行")
	}

	release()
	if err := <-holderDone; err != nil {
		t.Fatalf("持权 Turn 应正常完成：%v", err)
	}
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 {
		t.Fatalf("被取消的请求不得写入，实际 %+v", got.Messages)
	}

	// 执行权未泄漏：后续请求能正常取得执行权。
	if err := runTurnWithin(t, m, "s-1", 5*time.Second, func(turn *Turn) error {
		return turn.Append(userMessage("m-after", "后续请求"))
	}); err != nil {
		t.Fatalf("取消后执行权应已释放：%v", err)
	}
}

// TestQueuedTurnTimesOut 覆盖 AC-006(b)：排队等待执行权时 ctx 超时 → deadline_exceeded，
// 回调不执行、历史不变。
func TestQueuedTurnTimesOut(t *testing.T) {
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	release, holderDone := startBlockingTurn(t, m, "s-1")

	var executed atomic.Bool
	waitCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := m.RunTurn(waitCtx, "s-1", func(turn *Turn) error {
		executed.Store(true)
		return turn.Append(userMessage("m-timeout", "超时请求不应执行"))
	})
	expectKind(t, err, core.KindDeadlineExceeded)
	if executed.Load() {
		t.Fatal("排队超时后回调不得执行")
	}

	release()
	if err := <-holderDone; err != nil {
		t.Fatalf("持权 Turn 应正常完成：%v", err)
	}
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 {
		t.Fatalf("超时的请求不得写入，实际 %+v", got.Messages)
	}
	if err := runTurnWithin(t, m, "s-1", 5*time.Second, func(turn *Turn) error { return nil }); err != nil {
		t.Fatalf("超时后执行权应已释放：%v", err)
	}
}

// TestCancelDuringExecutionDiscardsStagedWrites 覆盖 AC-006(c) / P1：
// 执行中取消时结果标记为取消，已有历史不被回滚破坏，本 Turn 暂存整体丢弃，执行权被释放。
func TestCancelDuringExecutionDiscardsStagedWrites(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetSummary("已提交摘要")
		return turn.PutTodo(Todo{ID: "todo-committed", Title: "已提交", Status: TodoStatusPending})
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}
	base := mustGet(t, m, "s-1")

	turnCtx, cancel := context.WithCancel(context.Background())
	// 即便回调返回 nil，只要 ctx 已取消就不允许提交。
	err := m.RunTurn(turnCtx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m-cancelled", "取消前写入")); err != nil {
			return err
		}
		turn.SetSummary("取消的摘要")
		if err := turn.PutTodo(Todo{ID: "todo-cancelled", Status: TodoStatusPending}); err != nil {
			return err
		}
		cancel()
		return nil
	})
	expectKind(t, err, core.KindCancelled)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消错误应保留 context.Canceled 错误链：%v", err)
	}

	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(base, after) {
		t.Fatalf("取消的 Turn 不得产生任何提交：\n之前：%+v\n之后：%+v", base, after)
	}

	if err := runTurnWithin(t, m, "s-1", 5*time.Second, func(turn *Turn) error {
		turn.SetSummary("恢复后的摘要")
		return nil
	}); err != nil {
		t.Fatalf("取消后执行权应已释放：%v", err)
	}
	if got := mustGet(t, m, "s-1"); got.Summary != "恢复后的摘要" {
		t.Fatalf("后续 Turn 应能正常提交，实际摘要 %q", got.Summary)
	}
}
