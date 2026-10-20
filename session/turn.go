package session

import (
	"encoding/json"
	"sync"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TodoOperations 是 Turn 内的受限 Todo 能力接口，供 Tool / Context Manager 由装配层注入。
//
// 它暴露的是「基线 + 本 Turn 暂存」视图，因此同一 Turn 内连续操作满足 read-your-writes；
// 注入受限能力而不注入 *Manager / 可变 Session，避免组件绕过运行时改写会话状态
// （Turn 内回调 Manager.RunTurn / Delete / Save 会重入同一会话执行权而自死锁）。
type TodoOperations interface {
	// Todos 返回 Todo 的深拷贝：已提交基线 + 本 Turn 尚未提交的修改。
	Todos() []Todo
	// PutTodo 按 ID upsert：已存在则原地替换，否则追加到末尾。
	PutTodo(todo Todo) error
	// DeleteTodo 删除指定 ID 的 Todo，不存在视为幂等成功。
	DeleteTodo(id string) error
}

// Turn 是一次 RunTurn 的暂存写入句柄，生命周期与回调一致。
//
// 回调内的写入先进入隔离暂存区，只有提交条件全部成立才整体提交；失败、取消、超时、
// panic 都整轮丢弃。暂存区内部加互斥，因此同一 Turn 内多 goroutine 调用安全（每次调用
// 原子，调用顺序不保证）。回调返回后 Turn 关闭：写方法返回 invalid_argument，
// 只读方法仍返回安全快照；禁止长期持有 *Turn 供异步回调使用。
type Turn struct {
	sessionID string
	// base 是进入 Turn 时的已提交历史，私有深拷贝，只用于拼装视图与提交前校验。
	base []core.Message

	mu       sync.Mutex
	closed   bool
	messages []core.Message
	summary  string
	todos    []Todo
}

var _ TodoOperations = (*Turn)(nil)

// newTurn 以已提交快照为基线构造 Turn；summary / todos 直接继承基线，保证未写入时提交为幂等。
func newTurn(base Session) *Turn {
	return &Turn{
		sessionID: base.ID,
		base:      cloneMessages(base.Messages),
		summary:   base.Summary,
		todos:     cloneTodos(base.Todos),
	}
}

// Session 返回「已提交基线 + 本 Turn 已暂存写入」的深拷贝视图，供回调内读取。
func (t *Turn) Session() Session {
	t.mu.Lock()
	defer t.mu.Unlock()

	messages := make([]core.Message, 0, len(t.base)+len(t.messages))
	messages = append(messages, t.base...)
	messages = append(messages, t.messages...)

	return Session{
		ID:       t.sessionID,
		Messages: cloneMessages(messages),
		Todos:    cloneTodos(t.todos),
		Summary:  t.summary,
	}
}

// Append 把一批消息追加到暂存区。
//
// 每次追加都对「已提交历史 + 本 Turn 已暂存 + 新批次」调用 core.ValidateMessages：
// 非法序列是这一批次的原子拒绝，暂存区保持调用前状态，错误带 CallID 定位。
// 空批次是合法的空操作。
func (t *Turn) Append(msgs ...core.Message) error {
	const op = "session.Turn.Append"

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return turnClosedError(op)
	}
	if len(msgs) == 0 {
		return nil
	}

	batch := cloneMessages(msgs)
	combined := make([]core.Message, 0, len(t.base)+len(t.messages)+len(batch))
	combined = append(combined, t.base...)
	combined = append(combined, t.messages...)
	combined = append(combined, batch...)
	if err := core.ValidateMessages(combined); err != nil {
		return err
	}

	t.messages = append(t.messages, batch...)
	return nil
}

// SetSummary 暂存摘要，随 Turn 原子提交；Turn 关闭后忽略。
func (t *Turn) SetSummary(summary string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.summary = summary
}

// SetTodos 全量替换暂存的 Todo 列表（复制切片），随 Turn 原子提交；Turn 关闭后忽略。
//
// 本方法没有错误返回：非法 Todo（空 ID / 未知状态）不在此处拒绝，而是在提交前校验
// （P5）失败，整轮不提交。需要逐条拒绝时使用 PutTodo / DeleteTodo。
func (t *Turn) SetTodos(todos []Todo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.todos = cloneTodos(todos)
}

// Todos 返回「基线 + 本 Turn 暂存」的 Todo 深拷贝。
func (t *Turn) Todos() []Todo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return cloneTodos(t.todos)
}

// PutTodo 按 ID upsert 暂存 Todo：已存在则原地替换，否则追加到末尾。
//
// 非法 Todo 是这一批次的原子拒绝：返回 invalid_argument，暂存列表保持不变。
func (t *Turn) PutTodo(todo Todo) error {
	const op = "session.Turn.PutTodo"

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return turnClosedError(op)
	}
	if err := todo.Validate(); err != nil {
		return err
	}

	for i, existing := range t.todos {
		if existing.ID == todo.ID {
			t.todos[i] = todo
			return nil
		}
	}
	t.todos = append(t.todos, todo)
	return nil
}

// DeleteTodo 删除暂存 Todo，不存在视为幂等成功；空 ID 属于调用方契约错误。
func (t *Turn) DeleteTodo(id string) error {
	const op = "session.Turn.DeleteTodo"

	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return turnClosedError(op)
	}
	if id == "" {
		return invalidArgumentError(op, "Todo ID 不能为空")
	}

	for i, existing := range t.todos {
		if existing.ID == id {
			t.todos = append(t.todos[:i], t.todos[i+1:]...)
			return nil
		}
	}
	return nil
}

// close 关闭 Turn：此后的写入被拒绝，提交前的读取仍可用。
func (t *Turn) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
}

// staged 返回待提交的完整会话内容：已提交历史 + 暂存消息、摘要与 Todo。
// 返回值为独立副本，调用方可直接发布。
func (t *Turn) staged() ([]core.Message, string, []Todo) {
	t.mu.Lock()
	defer t.mu.Unlock()

	messages := make([]core.Message, 0, len(t.base)+len(t.messages))
	messages = append(messages, t.base...)
	messages = append(messages, t.messages...)

	return cloneMessages(messages), t.summary, cloneTodos(t.todos)
}

// cloneSession 深拷贝会话，用于读取快照与保存入参。
func cloneSession(s Session) Session {
	return Session{
		ID:       s.ID,
		Messages: cloneMessages(s.Messages),
		Todos:    cloneTodos(s.Todos),
		Summary:  s.Summary,
	}
}

// cloneMessages 深拷贝消息切片：Content / ToolCalls / ToolResult / Raw 都独立复制，
// 使调用方改写返回值不影响会话状态。
func cloneMessages(msgs []core.Message) []core.Message {
	if msgs == nil {
		return nil
	}
	out := make([]core.Message, len(msgs))
	for i, m := range msgs {
		out[i] = cloneMessage(m)
	}
	return out
}

// cloneMessage 深拷贝单条消息。
func cloneMessage(m core.Message) core.Message {
	out := core.Message{ID: m.ID, Role: m.Role}
	if m.Content != nil {
		out.Content = make([]core.ContentPart, len(m.Content))
		for i, part := range m.Content {
			out.Content[i] = core.ContentPart{Type: part.Type, Text: part.Text, Raw: cloneRaw(part.Raw)}
		}
	}
	if m.ToolCalls != nil {
		out.ToolCalls = make([]core.ToolCall, len(m.ToolCalls))
		for i, call := range m.ToolCalls {
			out.ToolCalls[i] = core.ToolCall{ID: call.ID, Name: call.Name, Arguments: cloneRaw(call.Arguments)}
		}
	}
	if m.ToolResult != nil {
		result := *m.ToolResult
		out.ToolResult = &result
	}
	out.Raw = cloneRaw(m.Raw)
	return out
}

// cloneRaw 复制原始 JSON 载荷，避免快照与外部共享底层数组。
func cloneRaw(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make(json.RawMessage, len(raw))
	copy(out, raw)
	return out
}

// cloneTodos 复制 Todo 切片。
func cloneTodos(todos []Todo) []Todo {
	if todos == nil {
		return nil
	}
	out := make([]Todo, len(todos))
	copy(out, todos)
	return out
}
