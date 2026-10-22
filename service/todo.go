package service

import (
	"context"
	"fmt"
	"sync"

	"cnb.cool/framework-station/mini-agent-go/builtin"
	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// 适配层错误定位前缀。
const (
	opTodoCreate   = "service.turnTodoOperations.Create"
	opTodoList     = "service.turnTodoOperations.List"
	opTodoComplete = "service.turnTodoOperations.Complete"
)

// turnTodoOperations 把 builtin.TodoOperations 端口绑定到当前 Run 的 *session.Turn。
//
// 为什么这样绑定是安全的：
//   - 实例只在外层 RunTurn 回调内构造，只交给本次 Run 的 per-Run 注册表，Run 结束即不可达，
//     因此模型参数无法指定会话，也不存在跨 Run 复用某个 Turn 的可能；
//   - 只调用 Turn 的暂存读写方法（Todos / PutTodo）：不触碰 Manager，就不会重入同一会话的
//     执行权（RunTurn / Save / Delete 会重入而死锁），也不会绕过本 Turn 的事务边界；
//   - 不缓存 Todo 副本、不生成会话状态：Turn 是唯一事实来源，读操作按值返回。
type turnTodoOperations struct {
	// sessionID 是本 Run 的可信会话身份（来自 RunRequest，不来自模型参数），用于防御性校验。
	sessionID string
	turn      *session.Turn
	// mu 串行化「读列表 → 选 ID → 写入」：Turn 保证单次写入原子，但 ID 推导不在同一步里，
	// 因此即使将来把工具改成并行调度，也不会出现两条待办抢同一个 ID。
	mu sync.Mutex
}

var _ builtin.TodoOperations = (*turnTodoOperations)(nil)

// newTurnTodoOperations 为当前 Turn 构造 Todo 端口实现。
func newTurnTodoOperations(turn *session.Turn, sessionID string) *turnTodoOperations {
	return &turnTodoOperations{sessionID: sessionID, turn: turn}
}

// Create 在暂存区追加一条 pending 待办，ID 由当前会话已存在的待办推导，保证会话内唯一。
//
// 写入落在 Turn 暂存区：Run COMPLETED 时与消息一起提交，失败/取消/超限则整轮丢弃。
func (t *turnTodoOperations) Create(ctx context.Context, sessionID, title string) (session.Todo, error) {
	if err := t.ready(ctx, opTodoCreate, sessionID); err != nil {
		return session.Todo{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	todos := t.turn.Todos()
	todo := session.Todo{ID: nextTodoID(todos), Title: title, Status: session.TodoStatusPending}
	if err := t.turn.PutTodo(todo); err != nil {
		return session.Todo{}, err
	}
	return todo, nil
}

// List 返回「已提交基线 + 本 Turn 暂存」的待办快照，因此同一 Turn 内连续操作可读到自己刚创建的待办。
func (t *turnTodoOperations) List(ctx context.Context, sessionID string) ([]session.Todo, error) {
	if err := t.ready(ctx, opTodoList, sessionID); err != nil {
		return nil, err
	}
	return t.turn.Todos(), nil
}

// Complete 把指定待办置为 completed；已完成的重复调用幂等（不再写入），
// 不存在的标识（含属于其他会话的标识）返回 builtin.ErrTodoNotFound，且不产生任何写入。
func (t *turnTodoOperations) Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error) {
	if err := t.ready(ctx, opTodoComplete, sessionID); err != nil {
		return session.Todo{}, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	for _, todo := range t.turn.Todos() {
		if todo.ID != todoID {
			continue
		}
		if todo.Status == session.TodoStatusCompleted {
			return todo, nil
		}
		todo.Status = session.TodoStatusCompleted
		if err := t.turn.PutTodo(todo); err != nil {
			return session.Todo{}, err
		}
		return todo, nil
	}
	return session.Todo{}, builtin.ErrTodoNotFound
}

// ready 执行端口入口的公共校验：context 终态优先（不留写入），其次校验会话身份一致。
//
// 会话不一致属于装配缺陷而非模型可达路径，因此按 invalid_argument 报错而不是伪装成领域失败。
func (t *turnTodoOperations) ready(ctx context.Context, op, sessionID string) error {
	if ctx != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if sessionID != t.sessionID {
		return core.NewError(core.KindInvalidArgument, op, "待办操作与当前会话不匹配")
	}
	return nil
}

// nextTodoID 返回会话内最小的空闲 todo-N 标识。
//
// 从当前待办列表推导而不保存计数器：适配层不引入第二份可变状态，Turn 丢弃后也不会残留
// 已用号码；只要列表内 ID 唯一，新 ID 就不会与已有待办冲突。
func nextTodoID(todos []session.Todo) string {
	used := make(map[string]struct{}, len(todos))
	for _, todo := range todos {
		used[todo.ID] = struct{}{}
	}
	for index := 1; ; index++ {
		id := fmt.Sprintf("todo-%d", index)
		if _, exists := used[id]; !exists {
			return id
		}
	}
}
