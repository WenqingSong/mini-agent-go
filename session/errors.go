package session

import (
	"context"
	"errors"
	"fmt"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// 会话级哨兵错误。会话不存在 / 已过期 / 重复创建都归入 core.KindInvalidArgument，
// 具体情形由调用方用 errors.Is 细分：哨兵挂在 *core.RunError.Cause 上，
// 因此既保留统一错误种类，又能沿 RunError.Unwrap 命中，而不只编码在错误文本里。
var (
	// ErrSessionNotFound 表示会话不存在：从未创建，或已被 Delete / PurgeExpired 物理删除。
	ErrSessionNotFound = errors.New("会话不存在或已被清理")
	// ErrSessionExpired 表示会话曾存在但已超过 TTL，本次访问同时完成了物理删除。
	ErrSessionExpired = errors.New("会话已过期")
	// ErrSessionExists 表示 Create 时同 ID 会话仍有效。
	ErrSessionExists = errors.New("同 ID 会话仍有效")
)

// sessionError 构造带会话 ID 的会话级错误，哨兵作为 Cause 保留错误链。
func sessionError(op, id string, cause error) error {
	return &core.RunError{
		Kind:    core.KindInvalidArgument,
		Op:      op,
		Message: fmt.Sprintf("会话 %q: %s", id, cause.Error()),
		Cause:   cause,
	}
}

// sessionNotFoundError 表示会话不存在（含已被清理）。
func sessionNotFoundError(op, id string) error {
	return sessionError(op, id, ErrSessionNotFound)
}

// sessionExpiredError 表示会话已过期且已被物理删除。
func sessionExpiredError(op, id string) error {
	return sessionError(op, id, ErrSessionExpired)
}

// sessionExistsError 表示 Create 时同 ID 会话仍有效。
func sessionExistsError(op, id string) error {
	return sessionError(op, id, ErrSessionExists)
}

// invalidArgumentError 构造不带哨兵的参数错误。
func invalidArgumentError(op, msg string) error {
	return core.NewError(core.KindInvalidArgument, op, msg)
}

// contextError 把已取消 / 已超时的 context 映射为契约错误种类。
//
// ctx 为 nil 属于调用方契约错误；context.DeadlineExceeded 映射为 deadline_exceeded，
// 其余（含 context.Canceled 与自定义取消原因）映射为 cancelled。
func contextError(op string, ctx context.Context) error {
	if ctx == nil {
		return invalidArgumentError(op, "context 不能为空")
	}
	err := ctx.Err()
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return &core.RunError{
			Kind:    core.KindDeadlineExceeded,
			Op:      op,
			Message: "会话操作超出 context 期限",
			Cause:   err,
		}
	}
	return &core.RunError{
		Kind:    core.KindCancelled,
		Op:      op,
		Message: "会话操作被 context 取消",
		Cause:   err,
	}
}

// callbackError 保留回调自身返回的错误；非 *core.RunError 时包一层 internal 并保留 Cause，
// 避免把无法归类的失败伪装成业务错误种类。
func callbackError(op string, err error) error {
	var runErr *core.RunError
	if errors.As(err, &runErr) {
		return err
	}
	return &core.RunError{
		Kind:    core.KindInternal,
		Op:      op,
		Message: "Turn 回调返回非契约错误",
		Cause:   err,
	}
}

// turnClosedError 表示 Turn 已随回调返回而关闭，不再接受写入。
func turnClosedError(op string) error {
	return invalidArgumentError(op, "Turn 已结束，不再接受写入")
}
