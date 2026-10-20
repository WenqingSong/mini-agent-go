package session

import (
	"context"
	"errors"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// newExpiryManager 构造启用 TTL（1 分钟）并注入可控时钟的 Manager。
func newExpiryManager(t *testing.T) (*Manager, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	return NewManager(ManagerConfig{TTL: time.Minute, Now: clock.Now}), clock
}

// TestExpiredSessionIsPurgedOnAccess 覆盖 INV-012 / INV-015 的惰性过期：
// TTL 到期后访问返回 ErrSessionExpired 并物理删除，与「不存在」可区分。
func TestExpiredSessionIsPurgedOnAccess(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.Append(userMessage("m1", "会随过期删除"))
	}); err != nil {
		t.Fatalf("Turn 应成功：%v", err)
	}

	clock.Advance(time.Minute) // 到达过期时刻即视为过期

	_, err := m.Get(ctx, "s-1")
	expectKind(t, err, core.KindInvalidArgument)
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("TTL 到期后访问应返回 ErrSessionExpired：%v", err)
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("过期与不存在必须可区分：%v", err)
	}

	// 过期即物理删除：再次访问是「不存在」，同 ID 可重新创建且不带旧历史。
	_, err = m.Get(ctx, "s-1")
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("过期会话应已被物理删除：%v", err)
	}
	if _, err := m.Create(ctx, "s-1"); err != nil {
		t.Fatalf("过期后应可重建同 ID 会话：%v", err)
	}
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 {
		t.Fatalf("重建的会话不应携带旧历史：%+v", got.Messages)
	}
}

// TestCreateReplacesExpiredSession 覆盖 D5 的 Create 分支：过期会话不再是有效会话，
// 允许同 ID 重建；重建后的会话状态为空。
func TestCreateReplacesExpiredSession(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		turn.SetSummary("旧摘要")
		return turn.PutTodo(Todo{ID: "todo-old", Status: TodoStatusPending})
	}); err != nil {
		t.Fatalf("Turn 应成功：%v", err)
	}

	clock.Advance(2 * time.Minute)

	recreated, err := m.Create(ctx, "s-1")
	if err != nil {
		t.Fatalf("过期会话应允许同 ID 重建：%v", err)
	}
	if len(recreated.Messages) != 0 || len(recreated.Todos) != 0 || recreated.Summary != "" {
		t.Fatalf("重建的会话必须是空的：%+v", recreated)
	}
	if _, err := m.Create(ctx, "s-1"); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("重建后同 ID 会话有效，再次 Create 应返回 ErrSessionExists：%v", err)
	}
}

// TestCreateRejectsSessionWithInFlightTurn 覆盖 INV-015：TTL 已过但存在在途 Turn 时，
// 会话被视为有效，不允许被 Create 替换。
func TestCreateRejectsSessionWithInFlightTurn(t *testing.T) {
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")

	release, done := startBlockingTurn(t, m, "s-1")
	clock.Advance(2 * time.Minute)

	if _, err := m.Create(context.Background(), "s-1"); !errors.Is(err, ErrSessionExists) {
		t.Fatalf("在途 Turn 期间会话仍有效，Create 应返回 ErrSessionExists：%v", err)
	}

	release()
	if err := <-done; err != nil {
		t.Fatalf("在途 Turn 应正常完成：%v", err)
	}
}

// TestPurgeExpiredSkipsInFlightTurnAndRefreshesOnCommit 覆盖 AC-005 / INV-015：
// 在途 Turn 的会话不被过期清理破坏，Turn 成功后提交生效并刷新 TTL；
// 同时验证未过期会话不被清理、已过期且空闲的会话被物理删除。
func TestPurgeExpiredSkipsInFlightTurnAndRefreshesOnCommit(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)

	mustCreate(t, m, "s-1")
	mustCreate(t, m, "s-3") // 二者的过期时刻都是 t0+60s
	clock.Advance(30 * time.Second)
	mustCreate(t, m, "s-2") // 过期时刻 t0+90s

	started := make(chan struct{})
	release := make(chan struct{})
	turnErr := make(chan error, 1)
	go func() {
		turnErr <- m.RunTurn(ctx, "s-3", func(turn *Turn) error {
			if err := turn.Append(userMessage("m-inflight", "在途写入")); err != nil {
				return err
			}
			close(started)
			<-release
			return nil
		})
	}()
	<-started

	clock.Advance(40 * time.Second) // t0+70s：s-1 与 s-3 已过 TTL，s-2 未过

	if purged := m.PurgeExpired(ctx); purged != 1 {
		t.Fatalf("只应清理已过期且空闲的会话，实际清理 %d 个", purged)
	}
	if _, err := m.Get(ctx, "s-1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("过期空闲会话应被清理：%v", err)
	}
	// 在途会话未被删除，且 Get 不因等待执行权而阻塞（返回已提交快照）。
	inflight := mustGet(t, m, "s-3")
	if len(inflight.Messages) != 0 {
		t.Fatalf("在途 Turn 的暂存不得可见：%+v", inflight.Messages)
	}
	if _, err := m.Get(ctx, "s-2"); err != nil {
		t.Fatalf("未过期会话不得被清理：%v", err)
	}

	close(release)
	if err := <-turnErr; err != nil {
		t.Fatalf("在途 Turn 应正常完成：%v", err)
	}

	committed := mustGet(t, m, "s-3")
	if len(committed.Messages) != 1 || committed.Messages[0].ID != "m-inflight" {
		t.Fatalf("在途 Turn 的成功提交应生效：%+v", committed.Messages)
	}

	// 成功提交刷新 TTL：t0+70s 提交 → 过期时刻 t0+130s。
	clock.Advance(50 * time.Second) // t0+120s
	if _, err := m.Get(ctx, "s-3"); err != nil {
		t.Fatalf("成功提交应刷新 TTL：%v", err)
	}
	clock.Advance(11 * time.Second) // t0+131s
	_, err := m.Get(ctx, "s-3")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("刷新后的 TTL 到期应返回 ErrSessionExpired：%v", err)
	}
}

// TestFailedTurnDoesNotRefreshTTL 覆盖 D4：失败 Turn 不得借刷新 TTL 延长会话状态。
func TestFailedTurnDoesNotRefreshTTL(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")

	clock.Advance(10 * time.Second)
	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m1", "不会落库")); err != nil {
			return err
		}
		return errInjected
	})
	expectKind(t, err, core.KindInternal)
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 {
		t.Fatalf("失败 Turn 不得落库：%+v", got.Messages)
	}

	clock.Advance(51 * time.Second) // t0+61s，已超过创建时的 TTL
	_, err = m.Get(ctx, "s-1")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("失败 Turn 不得刷新 TTL，应已过期：%v", err)
	}
}

// TestCancelledTurnDoesNotRefreshTTL 覆盖 D4：取消的 Turn 不提交、不刷新 TTL。
func TestCancelledTurnDoesNotRefreshTTL(t *testing.T) {
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")

	clock.Advance(10 * time.Second)
	turnCtx, cancel := context.WithCancel(context.Background())
	err := m.RunTurn(turnCtx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m1", "不会落库")); err != nil {
			return err
		}
		cancel()
		return nil
	})
	expectKind(t, err, core.KindCancelled)

	clock.Advance(51 * time.Second)
	_, err = m.Get(context.Background(), "s-1")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("取消的 Turn 不得刷新 TTL，应已过期：%v", err)
	}
}

// TestReadDoesNotRefreshTTL 覆盖 D4：普通读取不续期（写续期、读不续期）。
func TestReadDoesNotRefreshTTL(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")

	clock.Advance(30 * time.Second)
	if _, err := m.Get(ctx, "s-1"); err != nil {
		t.Fatalf("未过期会话应可读取：%v", err)
	}

	clock.Advance(31 * time.Second) // t0+61s
	_, err := m.Get(ctx, "s-1")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("读取不应刷新 TTL，应已过期：%v", err)
	}
}

// TestPurgeExpiredDisabledWithoutTTL 确认 TTL <= 0 时不存在过期语义。
func TestPurgeExpiredDisabledWithoutTTL(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	if purged := m.PurgeExpired(ctx); purged != 0 {
		t.Fatalf("未启用 TTL 时不应清理，实际 %d", purged)
	}
	if _, err := m.Get(ctx, "s-1"); err != nil {
		t.Fatalf("未启用 TTL 的会话不应过期：%v", err)
	}
}

// TestPurgeExpiredIgnoresCancelledContext 确认清理在 ctx 已取消时不产生删除动作。
func TestPurgeExpiredIgnoresCancelledContext(t *testing.T) {
	ctx := context.Background()
	m, clock := newExpiryManager(t)
	mustCreate(t, m, "s-1")
	clock.Advance(2 * time.Minute)

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if purged := m.PurgeExpired(cancelled); purged != 0 {
		t.Fatalf("ctx 已取消时不应删除会话，实际删除 %d 个", purged)
	}

	// 会话仍在：下一次正常访问才判定过期并删除。
	_, err := m.Get(ctx, "s-1")
	if !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("会话应仍存在且被判定为过期：%v", err)
	}
}
