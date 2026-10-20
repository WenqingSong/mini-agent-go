package session

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// errInjected 是并发用例里主动注入的回调失败。
var errInjected = errors.New("注入的回调失败")

// runTurnWithin 在限定时间内执行一次 Turn，用于证明执行权未被泄漏；
// 超时只作卡死保护，不作为性能判定。
func runTurnWithin(t *testing.T, m *Manager, sessionID string, timeout time.Duration, fn func(*Turn) error) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- m.RunTurn(context.Background(), sessionID, fn) }()
	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		return fmt.Errorf("在 %s 内未能取得执行权：执行权疑似泄漏", timeout)
	}
}

// turnMessageID 生成带 Turn 组前缀的唯一消息 ID。
func turnMessageID(turn, seq int) string {
	return fmt.Sprintf("g%d-m%d", turn, seq)
}

// turnGroupOf 从消息 ID 解析出 Turn 组号。
func turnGroupOf(t *testing.T, id string) int {
	t.Helper()
	prefix, _, ok := strings.Cut(id, "-")
	if !ok {
		t.Fatalf("消息 ID %q 缺少组前缀", id)
	}
	group, err := strconv.Atoi(strings.TrimPrefix(prefix, "g"))
	if err != nil {
		t.Fatalf("消息 ID %q 的组前缀无法解析：%v", id, err)
	}
	return group
}

// TestConcurrentTurnsOnSameSessionDoNotInterleave 覆盖 AC-003 / INV-002：
// M 个 goroutine 并发写同一会话，每个 Turn 写 K 条唯一消息；全部返回后消息无重复、
// 总条数等于 M×K、同一 Turn 的消息在历史中连续排列。
func TestConcurrentTurnsOnSameSessionDoNotInterleave(t *testing.T) {
	const (
		writers = 8
		perTurn = 3
	)
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				for i := 0; i < perTurn; i++ {
					if err := turn.Append(userMessage(turnMessageID(w, i), "并发内容")); err != nil {
						return err
					}
				}
				return nil
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(w)
	}
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("并发 Turn 不应失败，实际失败 %d 个：%v", len(errs), errs)
	}

	history := mustGet(t, m, "s-1").Messages
	if len(history) != writers*perTurn {
		t.Fatalf("历史总条数应为 %d，实际 %d", writers*perTurn, len(history))
	}

	seen := make(map[string]struct{}, writers*perTurn)
	groups := make(map[int]struct{}, writers)
	finished := make(map[int]struct{}, writers)
	lastGroup := -1
	for _, msg := range history {
		if _, ok := seen[msg.ID]; ok {
			t.Fatalf("消息 %q 在历史中重复出现", msg.ID)
		}
		seen[msg.ID] = struct{}{}

		group := turnGroupOf(t, msg.ID)
		groups[group] = struct{}{}
		if group == lastGroup {
			continue
		}
		if _, ok := finished[group]; ok {
			t.Fatalf("第 %d 组被其它 Turn 打断（消息 %q）：同一次写入的消息必须连续", group, msg.ID)
		}
		if lastGroup >= 0 {
			finished[lastGroup] = struct{}{}
		}
		lastGroup = group
	}
	if len(groups) != writers {
		t.Fatalf("应有 %d 个 Turn 的消息，实际 %d 个", writers, len(groups))
	}
}

// TestConcurrentFailedTurnsLeaveNoPartialHistory 覆盖 AC-003 的失败分支 / INV-001：
// 失败的 Turn 不留下任何消息，成功 Turn 的消息仍连续且完整。
func TestConcurrentFailedTurnsLeaveNoPartialHistory(t *testing.T) {
	const (
		total   = 8
		perTurn = 3
	)
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	var wg sync.WaitGroup
	for w := 0; w < total; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			shouldFail := w%2 == 1
			err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
				for i := 0; i < perTurn; i++ {
					if err := turn.Append(userMessage(turnMessageID(w, i), "内容")); err != nil {
						return err
					}
				}
				if shouldFail {
					return errInjected
				}
				return nil
			})
			if shouldFail {
				if core.KindOf(err) != core.KindInternal {
					t.Errorf("第 %d 组应返回内部错误，实际 %v", w, err)
				}
				return
			}
			if err != nil {
				t.Errorf("第 %d 组应成功提交，实际 %v", w, err)
			}
		}(w)
	}
	wg.Wait()

	history := mustGet(t, m, "s-1").Messages
	if len(history) != (total/2)*perTurn {
		t.Fatalf("只有成功 Turn 的消息可以入库：期望 %d 条，实际 %d 条", (total/2)*perTurn, len(history))
	}
	for _, msg := range history {
		if turnGroupOf(t, msg.ID)%2 == 1 {
			t.Fatalf("失败 Turn 的消息进入了历史：%q", msg.ID)
		}
	}
}

// TestTurnsOnDifferentSessionsRunConcurrently 覆盖 AC-004 / INV-011：
// 两个会话的 Turn 用显式 channel 互相声明「已进入执行」并在同步点相遇，
// 证明二者同时处于执行中；实现若引入跨会话全局锁，两个 Turn 无法相遇。
// 超时只作死锁保护，不作为性能判定。
func TestTurnsOnDifferentSessionsRunConcurrently(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-a")
	mustCreate(t, m, "s-b")

	enteredA := make(chan struct{})
	enteredB := make(chan struct{})

	run := func(sessionID string, self, other chan struct{}) error {
		return m.RunTurn(ctx, sessionID, func(turn *Turn) error {
			if err := turn.Append(userMessage(sessionID+"-m1", "进入执行")); err != nil {
				return err
			}
			close(self)
			select {
			case <-other:
				return nil
			case <-time.After(5 * time.Second):
				return errors.New("等待对方进入执行超时：疑似存在跨会话全局锁")
			}
		})
	}

	errs := make(chan error, 2)
	go func() { errs <- run("s-a", enteredA, enteredB) }()
	go func() { errs <- run("s-b", enteredB, enteredA) }()
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("跨会话 Turn 未能同时执行：%v", err)
		}
	}

	a := mustGet(t, m, "s-a")
	b := mustGet(t, m, "s-b")
	if len(a.Messages) != 1 || a.Messages[0].ID != "s-a-m1" {
		t.Fatalf("会话 A 的历史不符合预期：%+v", a.Messages)
	}
	if len(b.Messages) != 1 || b.Messages[0].ID != "s-b-m1" {
		t.Fatalf("会话 B 的历史不符合预期：%+v", b.Messages)
	}
}

// TestTurnTodoWritesFromMultipleGoroutines 覆盖 INV-008 的并发部分：
// 同一 Turn 内多 goroutine 写 Todo 安全，每次调用原子且不丢更新。
func TestTurnTodoWritesFromMultipleGoroutines(t *testing.T) {
	const writers = 8
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	var failures []error
	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		var (
			wg    sync.WaitGroup
			mu    sync.Mutex
			local []error
		)
		for w := 0; w < writers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				todo := Todo{ID: fmt.Sprintf("todo-%d", w), Title: "并发写入", Status: TodoStatusPending}
				if err := turn.PutTodo(todo); err != nil {
					mu.Lock()
					local = append(local, err)
					mu.Unlock()
				}
			}(w)
		}
		wg.Wait()
		failures = local
		if got := len(turn.Todos()); got != writers {
			return fmt.Errorf("同一 Turn 内的并发写入应全部可见：期望 %d，实际 %d", writers, got)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Turn 应成功提交：%v", err)
	}
	if len(failures) > 0 {
		t.Fatalf("同一 Turn 内的并发写入不应失败：%v", failures)
	}
	if got := len(mustGet(t, m, "s-1").Todos); got != writers {
		t.Fatalf("提交后 Todo 数量应为 %d，实际 %d", writers, got)
	}
}

// TestTurnDropLeadingConcurrentWithAppendAndSummary 覆盖 AC-006 / D3 的并发部分：
// 同一 Turn 内多 goroutine 并发调用 DropLeading / Append / SetSummary / Session，
// 无 panic、无死锁、无 data race；提交后的条数按「成功删除数 / 成功追加数」结算，
// 剩余历史仍是原历史的后缀，追加消息完整出现且不重复。
func TestTurnDropLeadingConcurrentWithAppendAndSummary(t *testing.T) {
	const (
		droppers  = 6
		appenders = 6
		perAppend = 2
	)
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	base := []core.Message{
		userMessage("b1", "基线 1"),
		userMessage("b2", "基线 2"),
		userMessage("b3", "基线 3"),
		userMessage("b4", "基线 4"),
	}
	mustSaveHistory(t, m, "s-1", base)

	var (
		mu       sync.Mutex
		dropped  int
		appended []string
		failures []error
	)
	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		var wg sync.WaitGroup
		for i := 0; i < droppers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := turn.DropLeading(1)
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					dropped++
				case core.KindOf(err) == core.KindInvalidArgument:
					// 当前剩余已提交历史不足时的合法拒绝，不计入成功次数。
				default:
					failures = append(failures, err)
				}
			}()
		}
		for i := 0; i < appenders; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				batch := make([]core.Message, 0, perAppend)
				for j := 0; j < perAppend; j++ {
					batch = append(batch, userMessage(fmt.Sprintf("a%d-%d", i, j), "并发追加"))
				}
				err := turn.Append(batch...)
				mu.Lock()
				defer mu.Unlock()
				if err != nil {
					failures = append(failures, err)
					return
				}
				for _, msg := range batch {
					appended = append(appended, msg.ID)
				}
			}(i)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = turn.Session()
			turn.SetSummary("并发摘要")
		}()
		wg.Wait()
		return nil
	})
	if err != nil {
		t.Fatalf("并发 Turn 应成功提交：%v", err)
	}
	if len(failures) > 0 {
		t.Fatalf("并发调用不应产生契约外失败：%v", failures)
	}

	// 每次 DropLeading(1) 在剩余历史非空时必然成功，因此全部基线消息都被删除。
	if dropped != len(base) {
		t.Fatalf("成功删除数应为 %d，实际 %d", len(base), dropped)
	}
	remainingBase := len(base) - dropped

	final := mustGet(t, m, "s-1")
	if want := remainingBase + len(appended); len(final.Messages) != want {
		t.Fatalf("提交后条数应为 %d，实际 %d（%v）", want, len(final.Messages), messageIDs(final.Messages))
	}
	if final.Summary != "并发摘要" {
		t.Fatalf("并发写入的摘要应提交，实际 %q", final.Summary)
	}

	// 保留下来的已提交历史必须是原历史的后缀，顺序不变。
	wantPrefix := messageIDs(base[dropped:])
	for i, id := range wantPrefix {
		if final.Messages[i].ID != id {
			t.Fatalf("第 %d 条应为 %q，实际 %q", i, id, final.Messages[i].ID)
		}
	}

	// 追加消息必须全部出现且不重复。
	seen := make(map[string]struct{}, len(appended))
	for _, id := range appended {
		seen[id] = struct{}{}
	}
	for _, msg := range final.Messages[remainingBase:] {
		if _, ok := seen[msg.ID]; !ok {
			t.Fatalf("出现非本 Turn 追加的消息 %q", msg.ID)
		}
		delete(seen, msg.ID)
	}
	if len(seen) != 0 {
		t.Fatalf("追加消息缺失：%v", seen)
	}
}
