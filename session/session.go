// Package session 定义 Session / Todo / 摘要的数据形状、存取抽象，并交付单进程内存 Session Manager。
//
// Session 是消息历史、摘要与 Todo 的唯一所有者；Store 是存取抽象，*Manager 以内存实现
// Store，并额外提供会话生命周期（创建 / 读取 / 删除 / 过期清理）与 Turn 执行权
// （同会话串行、跨会话并发、整轮原子提交）。本包只依赖标准库与 core，不含持久化：
// 进程退出即丢失数据，重启恢复属后续任务。
package session

import (
	"context"
	"fmt"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TodoStatus 是 Todo 的生命周期状态，取值固定为 pending / in_progress / completed / cancelled。
type TodoStatus string

const (
	// TodoStatusPending 表示待办尚未开始。
	TodoStatusPending TodoStatus = "pending"
	// TodoStatusInProgress 表示待办正在处理。
	TodoStatusInProgress TodoStatus = "in_progress"
	// TodoStatusCompleted 表示待办已完成。
	TodoStatusCompleted TodoStatus = "completed"
	// TodoStatusCancelled 表示待办已取消。
	TodoStatusCancelled TodoStatus = "cancelled"
)

// Todo 是会话内的一个待办项。
type Todo struct {
	ID     string
	Title  string
	Status TodoStatus
}

// Validate 校验 Todo 的可提交性：ID 非空，Status 属于四个契约取值。
//
// Title 不做校验（允许空字符串）：标题是展示信息，不参与会话状态机。
func (t Todo) Validate() error {
	if t.ID == "" {
		return core.NewError(core.KindInvalidArgument, "session.Todo.Validate", "Todo ID 不能为空")
	}
	switch t.Status {
	case TodoStatusPending, TodoStatusInProgress, TodoStatusCompleted, TodoStatusCancelled:
		return nil
	default:
		return core.NewError(core.KindInvalidArgument, "session.Todo.Validate", fmt.Sprintf("Todo %q 的状态 %q 不在契约取值内", t.ID, t.Status))
	}
}

// Session 是一次会话的事实来源：消息历史、摘要与 Todo 都归属 Session，Run 只通过
// core.ExecutionRef 引用会话，不持有会话状态副本。
//
// Summary 是会话级摘要的最后一个成功提交值，"" 表示无摘要；摘要由后续 Context Manager
// 生成并经 Turn.SetSummary 写入，本包不实现摘要算法。
type Session struct {
	ID       string
	Messages []core.Message
	Todos    []Todo
	Summary  string
}

// Store 是会话存取抽象。ctx 承担取消与超时，不承载业务身份。
//
// *Manager 是内存实现：Load 返回深拷贝快照，Save 在执行权内全量替换。持久化实现属后续任务。
type Store interface {
	Load(ctx context.Context, id string) (Session, error)
	Save(ctx context.Context, s Session) error
}
