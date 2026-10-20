// Package session 定义 Session / Todo 的数据形状与存取抽象。
//
// Session 是消息历史与 Todo 的唯一所有者；本包只固定 Store 抽象（Load / Save），
// 不提供任何持久化或内存实现。本包只依赖标准库与 core。
package session

import (
	"context"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// TodoStatus 是 Todo 的生命周期状态：pending / in_progress / completed / cancelled。
type TodoStatus string

// Todo 是会话内的一个待办项。
type Todo struct {
	ID     string
	Title  string
	Status TodoStatus
}

// Session 是一次会话的事实来源：消息历史与 Todo 都归属 Session，Run 只通过
// core.ExecutionRef 引用会话，不持有会话状态副本。
type Session struct {
	ID       string
	Messages []core.Message
	Todos    []Todo
}

// Store 是会话存取抽象。ctx 承担取消与超时，不承载业务身份。
type Store interface {
	Load(ctx context.Context, id string) (Session, error)
	Save(ctx context.Context, s Session) error
}
