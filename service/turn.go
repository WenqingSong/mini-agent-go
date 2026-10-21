package service

import (
	"context"
	"strings"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// 契约层枚举字面量：core 未导出 Role / ContentType 常量，包外只能做字面量转换。
const (
	roleAssistant   = core.Role("assistant")
	contentTypeText = core.ContentType("text")
)

// turnSession 是 agent.SessionRunner 的同 Turn 适配器。
//
// 一次 chat 只在外层调用一次真实的 session.Manager.RunTurn；Loop 内部的
// SessionRunner.RunTurn 直接落到当前 Turn，因此用户消息、本轮产出与摘要变更
// 落在同一次提交或同一轮回滚里，且不会二次获取同一会话执行权（死锁）。
type turnSession struct {
	sessions  SessionRunner
	turn      *session.Turn
	sessionID string
}

// RunTurn 在会话 ID 匹配时直接复用当前 Turn；其他 ID 回落真实会话执行权（防御性路径，正常不触发）。
func (t *turnSession) RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error {
	if sessionID == t.sessionID {
		return fn(t.turn)
	}
	return t.sessions.RunTurn(ctx, sessionID, fn)
}

// userMessage 构造本轮用户消息：不 trim、不改写、不生成业务 ID。
func userMessage(text string) core.Message {
	return core.Message{
		Role:    core.Role("user"),
		Content: []core.ContentPart{{Type: contentTypeText, Text: text}},
	}
}

// replyText 取本次 Run 中最后一条 assistant 消息的文本片段拼接结果；没有则返回空串。
//
// 只读取 Run 产出的消息（RunResult.Messages），不回读会话，也不返回中间工具载荷。
func replyText(messages []core.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != roleAssistant {
			continue
		}
		var builder strings.Builder
		for _, part := range messages[i].Content {
			builder.WriteString(part.Text)
		}
		return builder.String()
	}
	return ""
}
