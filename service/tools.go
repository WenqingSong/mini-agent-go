package service

import (
	"fmt"

	"cnb.cool/framework-station/mini-agent-go/builtin"
	"cnb.cool/framework-station/mini-agent-go/tool"
)

// ToolsetFactory 构造一次 Run 使用的工具注册表。
//
// 注册表按 Run 构造而不是进程级共享，原因是 Todo 工具必须绑定「当前 Run 所属 Turn」
// 的受限能力：同一进程内不同会话、不同 Run 的 Todo 能力不能互相可见。入参只暴露
// builtin.TodoOperations 端口，装配层无法借它拿到 *session.Turn 或会话管理器本身。
type ToolsetFactory func(todoOps builtin.TodoOperations) (tool.Registry, error)

// NewToolset 是生产装配的工具注册表构造器：把 builtin.All 的完整工具清单逐一注册并封闭。
//
// 清单只在这里定义一次，正式入口（cmd/mini-agent）与离线验收共用同一函数，
// 因此测试看到的工具清单就是线上会发给模型的工具清单。
func NewToolset(todoOps builtin.TodoOperations) (tool.Registry, error) {
	registry := tool.NewMapRegistry()
	for _, impl := range builtin.All(todoOps) {
		if err := registry.Register(impl); err != nil {
			return nil, fmt.Errorf("service: 注册工具 %q 失败: %w", impl.Spec().Name, err)
		}
	}
	if err := registry.Seal(); err != nil {
		return nil, fmt.Errorf("service: 封闭工具注册表失败: %w", err)
	}
	return registry, nil
}
