package session

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// fakeClock 是确定性时钟：过期证据一律来自时钟推进，不使用 sleep。
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// textMessage 构造纯文本消息。core 的角色与内容类型常量未导出，测试按契约字面值构造。
func textMessage(id string, role core.Role, text string) core.Message {
	return core.Message{
		ID:      id,
		Role:    role,
		Content: []core.ContentPart{{Type: core.ContentType("text"), Text: text}},
	}
}

// userMessage 构造 user 纯文本消息。
func userMessage(id, text string) core.Message {
	return textMessage(id, core.Role("user"), text)
}

// assistantMessage 构造带工具调用的 assistant 消息。
func assistantMessage(id string, calls ...core.ToolCall) core.Message {
	return core.Message{ID: id, Role: core.Role("assistant"), ToolCalls: calls}
}

// toolMessage 构造携带工具结果的 tool 消息。
func toolMessage(id, callID string) core.Message {
	return core.Message{
		ID:         id,
		Role:       core.Role("tool"),
		ToolResult: &core.ToolResult{CallID: callID, Name: "echo", Content: "ok"},
	}
}

// newCall 构造一次工具调用。
func newCall(id, name, args string) core.ToolCall {
	return core.ToolCall{ID: id, Name: name, Arguments: json.RawMessage(args)}
}

// expectKind 断言错误种类并返回 *core.RunError；err 为 nil 或种类不符都算失败。
func expectKind(t *testing.T, err error, want core.ErrorKind) *core.RunError {
	t.Helper()
	if err == nil {
		t.Fatalf("期望错误种类 %s，实际没有返回错误", want)
	}
	if got := core.KindOf(err); got != want {
		t.Fatalf("期望错误种类 %s，实际为 %s（错误：%v）", want, got, err)
	}
	var runErr *core.RunError
	if !errors.As(err, &runErr) {
		t.Fatalf("期望错误为 *core.RunError，实际为 %T", err)
	}
	return runErr
}

// mustCreate 创建会话，失败即终止用例。
func mustCreate(t *testing.T, m *Manager, id string) {
	t.Helper()
	if _, err := m.Create(context.Background(), id); err != nil {
		t.Fatalf("创建会话 %q 失败：%v", id, err)
	}
}

// mustGet 读取会话快照，失败即终止用例。
func mustGet(t *testing.T, m *Manager, id string) Session {
	t.Helper()
	s, err := m.Get(context.Background(), id)
	if err != nil {
		t.Fatalf("读取会话 %q 失败：%v", id, err)
	}
	return s
}

// fingerprint 用 JSON 编码比对消息序列，能发现顺序、字段或内容的任何变化（不含指针地址）。
func fingerprint(t *testing.T, msgs []core.Message) string {
	t.Helper()
	data, err := json.Marshal(msgs)
	if err != nil {
		t.Fatalf("消息序列无法编码用于比对：%v", err)
	}
	return string(data)
}

// TestManagerTurnCommitsHistorySummaryAndTodos 覆盖 AC-001 / INV-001 / INV-004：
// 写入多条消息（含带 ToolCall 的 assistant 消息与对应 tool 结果）、摘要与 Todo 后按 ID 读取，
// 条数、顺序、ID 与内容一致，摘要与 Todo 为最后一次写入的值；返回值是深拷贝快照。
func TestManagerTurnCommitsHistorySummaryAndTodos(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	written := []core.Message{
		userMessage("m1", "帮我读 go.mod"),
		assistantMessage("m2", newCall("call_1", "read_file", `{"path":"go.mod"}`)),
		toolMessage("m3", "call_1"),
	}
	baseTodos := []Todo{
		{ID: "todo-1", Title: "写实现", Status: TodoStatusInProgress},
		{ID: "todo-2", Title: "跑测试", Status: TodoStatusPending},
	}

	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.Append(written...); err != nil {
			return err
		}
		turn.SetSummary("第一版摘要")
		turn.SetTodos(baseTodos)
		// 同一 Turn 内的后续写入必须覆盖前面的值（最后一次写入生效）。
		return turn.PutTodo(Todo{ID: "todo-2", Title: "跑测试", Status: TodoStatusCompleted})
	})
	if err != nil {
		t.Fatalf("Turn 应成功提交：%v", err)
	}

	got := mustGet(t, m, "s-1")
	if got.ID != "s-1" {
		t.Fatalf("会话 ID 不一致：%q", got.ID)
	}
	if len(got.Messages) != len(written) {
		t.Fatalf("消息条数不一致：期望 %d，实际 %d", len(written), len(got.Messages))
	}
	for i, want := range written {
		if got.Messages[i].ID != want.ID || got.Messages[i].Role != want.Role {
			t.Fatalf("第 %d 条消息的 ID 或角色不一致：期望 %s/%s，实际 %s/%s", i, want.ID, want.Role, got.Messages[i].ID, got.Messages[i].Role)
		}
	}
	if text := got.Messages[0].Content[0].Text; text != "帮我读 go.mod" {
		t.Fatalf("消息内容不一致：%q", text)
	}
	if len(got.Messages[1].ToolCalls) != 1 || got.Messages[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("assistant 消息的 ToolCall 不一致：%+v", got.Messages[1].ToolCalls)
	}
	if string(got.Messages[1].ToolCalls[0].Arguments) != `{"path":"go.mod"}` {
		t.Fatalf("工具参数被改写：%s", got.Messages[1].ToolCalls[0].Arguments)
	}
	if got.Messages[2].ToolResult == nil || got.Messages[2].ToolResult.CallID != "call_1" {
		t.Fatalf("tool 消息的 ToolResult 不一致：%+v", got.Messages[2].ToolResult)
	}
	if got.Summary != "第一版摘要" {
		t.Fatalf("摘要应为最后一次写入值，实际 %q", got.Summary)
	}
	if len(got.Todos) != 2 || got.Todos[0].Status != TodoStatusInProgress || got.Todos[1].Status != TodoStatusCompleted {
		t.Fatalf("Todo 状态应为最后一次写入值，实际 %+v", got.Todos)
	}

	// 深拷贝隔离：改写返回值不得影响会话。
	got.Messages[0].Content[0].Text = "被改写"
	got.Messages[1].ToolCalls[0].ID = "被改写"
	got.Messages[2].ToolResult.CallID = "被改写"
	got.Messages[2].ToolResult.Content = "被改写"
	got.Todos[0].Status = TodoStatusCancelled
	got.Summary = "被改写"

	again := mustGet(t, m, "s-1")
	if again.Messages[0].Content[0].Text != "帮我读 go.mod" {
		t.Fatalf("消息正文被外部改写污染：%q", again.Messages[0].Content[0].Text)
	}
	if again.Messages[1].ToolCalls[0].ID != "call_1" {
		t.Fatalf("ToolCall 被外部改写污染：%+v", again.Messages[1].ToolCalls)
	}
	if again.Messages[2].ToolResult.CallID != "call_1" || again.Messages[2].ToolResult.Content != "ok" {
		t.Fatalf("ToolResult 被外部改写污染：%+v", again.Messages[2].ToolResult)
	}
	if again.Todos[0].Status != TodoStatusInProgress || again.Summary != "第一版摘要" {
		t.Fatalf("Todo 或摘要被外部改写污染：%+v / %q", again.Todos, again.Summary)
	}
}

// TestManagerGetReportsMissingSession 覆盖 INV-004：读取不存在的会话必须返回明确错误，
// 且「不存在」「已过期」「重复创建」三种情形可被 errors.Is 区分。
func TestManagerGetReportsMissingSession(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})

	_, err := m.Get(ctx, "ghost")
	expectKind(t, err, core.KindInvalidArgument)
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("读取不存在的会话应命中 ErrSessionNotFound：%v", err)
	}
	if errors.Is(err, ErrSessionExpired) || errors.Is(err, ErrSessionExists) {
		t.Fatalf("不存在的会话不得与其它情形混淆：%v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("错误应携带会话 ID 便于定位：%v", err)
	}

	// 空 ID 是调用方参数错误，不是「会话不存在」。
	_, err = m.Get(ctx, "")
	expectKind(t, err, core.KindInvalidArgument)
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("空 ID 不得被当作会话不存在：%v", err)
	}
}

// TestManagerCreateRejectsLiveSession 覆盖 D5：同 ID 会话仍有效时 Create 失败且不重置数据。
func TestManagerCreateRejectsLiveSession(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.Append(userMessage("m1", "既有数据"))
	}); err != nil {
		t.Fatalf("Turn 应成功提交：%v", err)
	}

	_, err := m.Create(ctx, "s-1")
	expectKind(t, err, core.KindInvalidArgument)
	if !errors.Is(err, ErrSessionExists) {
		t.Fatalf("重复创建应命中 ErrSessionExists：%v", err)
	}
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 1 {
		t.Fatalf("重复创建不得重置既有会话，实际消息数 %d", len(got.Messages))
	}

	if _, err := m.Create(ctx, ""); core.KindOf(err) != core.KindInvalidArgument {
		t.Fatalf("空 ID 创建应返回 invalid_argument，实际：%v", err)
	}
}

// TestManagerDeleteKeepsOtherSessionIntact 覆盖 AC-002：两个会话交叉读写互不可见，
// 删除其中一个不改变另一个的任何字段。
func TestManagerDeleteKeepsOtherSessionIntact(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-a")
	mustCreate(t, m, "s-b")

	fill := func(id string, text, summary, todoID string) {
		t.Helper()
		err := m.RunTurn(ctx, id, func(turn *Turn) error {
			if err := turn.Append(userMessage(id+"-m1", text), userMessage(id+"-m2", text+"续")); err != nil {
				return err
			}
			turn.SetSummary(summary)
			return turn.PutTodo(Todo{ID: todoID, Title: text, Status: TodoStatusPending})
		})
		if err != nil {
			t.Fatalf("填充会话 %q 失败：%v", id, err)
		}
	}
	fill("s-a", "A 的内容", "A 摘要", "todo-a")
	fill("s-b", "B 的内容", "B 摘要", "todo-b")

	a := mustGet(t, m, "s-a")
	b := mustGet(t, m, "s-b")
	if len(a.Messages) != 2 || len(b.Messages) != 2 {
		t.Fatalf("两个会话的消息条数应各自独立：A=%d B=%d", len(a.Messages), len(b.Messages))
	}
	for _, msg := range a.Messages {
		if !strings.HasPrefix(msg.ID, "s-a-") {
			t.Fatalf("会话 A 读到了其它会话的消息：%q", msg.ID)
		}
	}
	for _, msg := range b.Messages {
		if !strings.HasPrefix(msg.ID, "s-b-") {
			t.Fatalf("会话 B 读到了其它会话的消息：%q", msg.ID)
		}
	}
	if a.Summary == b.Summary || a.Todos[0].ID == b.Todos[0].ID {
		t.Fatalf("两个会话的摘要或 Todo 发生了串扰：%+v / %+v", a, b)
	}

	if err := m.Delete(ctx, "s-a"); err != nil {
		t.Fatalf("删除会话 A 失败：%v", err)
	}
	if _, err := m.Get(ctx, "s-a"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("删除后访问应返回 ErrSessionNotFound：%v", err)
	}
	if err := m.Delete(ctx, "s-a"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("重复删除应返回 ErrSessionNotFound：%v", err)
	}

	if after := mustGet(t, m, "s-b"); !reflect.DeepEqual(b, after) {
		t.Fatalf("删除会话 A 改变了会话 B 的数据：\n删除前：%+v\n删除后：%+v", b, after)
	}
}

// TestManagerStoreAdapterReplacesAndValidates 覆盖 D6：Manager 实现 Store，
// Load 返回深拷贝快照，Save 全量替换并受消息 / Todo 校验约束。
func TestManagerStoreAdapterReplacesAndValidates(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	replacement := Session{
		ID: "s-1",
		Messages: []core.Message{
			userMessage("m1", "第一版"),
			assistantMessage("m2", newCall("call_1", "echo", `{}`)),
			toolMessage("m3", "call_1"),
		},
		Todos:   []Todo{{ID: "todo-1", Title: "", Status: TodoStatusPending}},
		Summary: "store 摘要",
	}
	if err := m.Save(ctx, replacement); err != nil {
		t.Fatalf("Save 应成功：%v", err)
	}
	loaded, err := m.Load(ctx, "s-1")
	if err != nil {
		t.Fatalf("Load 应成功：%v", err)
	}
	if !reflect.DeepEqual(loaded, replacement) {
		t.Fatalf("Load 应返回 Save 的内容：\n期望：%+v\n实际：%+v", replacement, loaded)
	}

	// Save 是全量替换，不是合并。
	second := Session{ID: "s-1", Messages: []core.Message{userMessage("m9", "第二版")}, Summary: "第二版摘要"}
	if err := m.Save(ctx, second); err != nil {
		t.Fatalf("Save 全量替换应成功：%v", err)
	}
	loaded = mustGet(t, m, "s-1")
	if len(loaded.Messages) != 1 || loaded.Messages[0].ID != "m9" || len(loaded.Todos) != 0 || loaded.Summary != "第二版摘要" {
		t.Fatalf("Save 未做全量替换：%+v", loaded)
	}

	// Load 返回深拷贝。
	loaded.Messages[0].Content[0].Text = "被改写"
	if again := mustGet(t, m, "s-1"); again.Messages[0].Content[0].Text != "第二版" {
		t.Fatalf("Load 返回值被外部改写污染：%q", again.Messages[0].Content[0].Text)
	}

	if err := m.Save(ctx, Session{ID: "ghost"}); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("Save 不存在的会话应返回 ErrSessionNotFound：%v", err)
	}
	if err := m.Save(ctx, Session{}); core.KindOf(err) != core.KindInvalidArgument {
		t.Fatalf("Save 空 ID 应返回 invalid_argument，实际：%v", err)
	}

	before := fingerprint(t, mustGet(t, m, "s-1").Messages)
	runErr := expectKind(t, m.Save(ctx, Session{ID: "s-1", Messages: []core.Message{toolMessage("mx", "call_orphan")}}), core.KindInvalidPairing)
	if runErr.CallID != "call_orphan" {
		t.Fatalf("Save 校验错误应定位 CallID，实际 %q", runErr.CallID)
	}
	expectKind(t, m.Save(ctx, Session{ID: "s-1", Todos: []Todo{{ID: "", Status: TodoStatusPending}}}), core.KindInvalidArgument)
	if after := fingerprint(t, mustGet(t, m, "s-1").Messages); after != before {
		t.Fatalf("被拒绝的 Save 改变了会话历史：\n之前：%s\n之后：%s", before, after)
	}
}

// TestManagerRunTurnCallbackErrorDiscardsTurn 覆盖 P2 与失败语义：
// 回调错误整轮丢弃并保留错误链，已是 *RunError 的错误原样返回。
func TestManagerRunTurnCallbackErrorDiscardsTurn(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")
	if err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		return turn.Append(userMessage("m1", "已提交基线"))
	}); err != nil {
		t.Fatalf("基线 Turn 应成功：%v", err)
	}
	before := mustGet(t, m, "s-1")

	boom := errors.New("provider 调用失败")
	err := m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m2", "不应提交")); err != nil {
			return err
		}
		turn.SetSummary("不应提交")
		if err := turn.PutTodo(Todo{ID: "todo-bad", Status: TodoStatusPending}); err != nil {
			return err
		}
		return boom
	})
	expectKind(t, err, core.KindInternal)
	if !errors.Is(err, boom) {
		t.Fatalf("回调错误链必须保留：%v", err)
	}
	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
		t.Fatalf("失败 Turn 产生了部分提交：\n之前：%+v\n之后：%+v", before, after)
	}

	custom := core.NewError(core.KindToolFailure, "test.tool", "工具不可恢复失败")
	err = m.RunTurn(ctx, "s-1", func(turn *Turn) error {
		if err := turn.Append(userMessage("m3", "不应提交")); err != nil {
			return err
		}
		return custom
	})
	if err != custom {
		t.Fatalf("已是 *RunError 的回调错误应原样返回，实际：%v", err)
	}
	if after := mustGet(t, m, "s-1"); !reflect.DeepEqual(before, after) {
		t.Fatalf("失败 Turn 产生了部分提交：\n之前：%+v\n之后：%+v", before, after)
	}
}

// TestManagerRejectsInvalidArguments 覆盖入口参数与 context 检查：
// 非法参数与已取消 / 已超时的 context 都必须返回契约错误且不写入。
func TestManagerRejectsInvalidArguments(t *testing.T) {
	ctx := context.Background()
	m := NewManager(ManagerConfig{})
	mustCreate(t, m, "s-1")

	err := m.RunTurn(ctx, "", func(*Turn) error { return nil })
	expectKind(t, err, core.KindInvalidArgument)
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("空会话 ID 不得被当作会话不存在：%v", err)
	}
	expectKind(t, m.RunTurn(ctx, "s-1", nil), core.KindInvalidArgument)

	err = m.RunTurn(ctx, "ghost", func(*Turn) error { return nil })
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("不存在的会话应返回 ErrSessionNotFound：%v", err)
	}
	if err := m.Delete(ctx, "ghost"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("删除不存在的会话应返回 ErrSessionNotFound：%v", err)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = m.Load(cancelled, "s-1")
	expectKind(t, err, core.KindCancelled)
	_, err = m.Create(cancelled, "s-2")
	expectKind(t, err, core.KindCancelled)
	expectKind(t, m.RunTurn(cancelled, "s-1", func(*Turn) error { return nil }), core.KindCancelled)
	if _, err := m.Get(ctx, "s-2"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("被取消的 Create 不得留下会话：%v", err)
	}

	// 已取消的 context 不得写入：历史保持为空。
	if got := mustGet(t, m, "s-1"); len(got.Messages) != 0 {
		t.Fatalf("被取消的调用产生了写入：%+v", got.Messages)
	}

	overdue, cancelOverdue := context.WithDeadline(ctx, time.Now().Add(-time.Second))
	defer cancelOverdue()
	_, err = m.Get(overdue, "s-1")
	expectKind(t, err, core.KindDeadlineExceeded)
	expectKind(t, m.RunTurn(overdue, "s-1", func(*Turn) error { return nil }), core.KindDeadlineExceeded)

	// nil context 属于调用方契约错误，不得 panic。
	_, err = m.Get(nil, "s-1")
	expectKind(t, err, core.KindInvalidArgument)
}

// TestManagerZeroValueIsRejected 确认未经 NewManager 构造的 Manager 返回契约错误而不是 panic。
func TestManagerZeroValueIsRejected(t *testing.T) {
	var zero Manager
	_, err := zero.Create(context.Background(), "s-1")
	expectKind(t, err, core.KindInvalidArgument)
	if err := zero.Delete(context.Background(), "s-1"); core.KindOf(err) != core.KindInvalidArgument {
		t.Fatalf("零值 Manager 应返回 invalid_argument，实际：%v", err)
	}
}
