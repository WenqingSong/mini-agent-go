# AI 辅助开发记录

本项目使用 AI Agent 辅助任务拆分、方案整理、编码、测试与审查。开发者负责需求、关键方案决策和最终接受；仓库通过任务、决策、审查记录和 Git 提交保存过程证据。

本文选取实际发生的问题，说明如何约束和验证 AI 产出。通用角色提示词与项目内 Agent Runtime 分属开发过程和运行产品两部分；业务模块不依赖 `.agent/engine`。

## 提示词与任务输入

仓库保留了六类角色提示词：

| 输入 | 来源 |
| --- | --- |
| 任务定义 | [TaskBuilderPrompt.md](../.agent/roles/TaskBuilderPrompt.md) |
| 方案与约束分析 | [AnalystAgent.md](../.agent/roles/AnalystAgent.md) |
| 编码与修复 | [CoderAgent.md](../.agent/roles/CoderAgent.md) |
| 实现及测试审查 | [CleanerAgent.md](../.agent/roles/CleanerAgent.md) |
| 核心机制与开发者决策 | [OwnerGateAgent.md](../.agent/roles/OwnerGateAgent.md) |
| 运行验收 | [DelivererAgent.md](../.agent/roles/DelivererAgent.md) |

例如，Coder 提示词中的实际约束是：

> 关键业务不变量至少有一个能区分正确与错误实现的测试；

> 禁止删除或 Skip 失败测试、放宽关键断言、忽略错误，或 Mock 掉本次真正需要验证的行为来制造 `PASS`。

具体任务通过 `.agent/tasks/<task-id>/task.md` 提供目标、范围和验收条件；有设计取舍时用 `contract.md` 保存决定，审查发现保存在 `findings.md`。这些是仓库内已留档的指令与结论，完整聊天逐字记录和当时使用的开发模型版本没有归档。

任务编号在计划与实际记录中存在差异，以下使用任务名称和文件路径定位，不用编号推断交付顺序或完成状态。

## 案例一：Provider 超时被 HTTP 层重新分类

**问题。** 模型客户端自身超时后，Agent 已把运行结果归为 `FAILED / provider_failure`。HTTP 层却先沿错误链查找 `context.DeadlineExceeded`，把它改成 `504 deadline_exceeded`，导致响应、Run 终态和日志表达不同结论。

**任务输入。** [任务说明](../.agent/tasks/provider-timeout-classification-v1/task.md)要求同时保护两条路径：已有 Run 结论时按它的错误种类映射；尚未形成结论的请求级取消/超时保留原有响应。修复范围限定在 HTTP 错误分类，不重构 Provider 或 Loop。

**修复与验证。** [httpapi/errors.go](../httpapi/errors.go)优先识别已有 `RunError`，然后才用裸 Context 错误兜底。[集成测试](../httpapi/handler_test.go)同时断言 HTTP 错误、运行终态、日志和会话快照。历史审查还临时恢复错误判定顺序，确认回归测试会失败。

证据：[技术契约](../.agent/tasks/provider-timeout-classification-v1/contract.md) · [审查与修复记录](../.agent/tasks/provider-timeout-classification-v1/findings.md) · [核心验证说明](../.agent/tasks/provider-timeout-classification-v1/core-logic.md)；修复提交 `43ce197`。

**经验。** 多层错误包装需要明确谁负责最后分类；保留 `Cause` 供诊断，并不意味着下游可以覆盖已经确定的运行结论。

## 案例二：上下文预算挤掉必须保留的近期消息

**问题。** 压缩后的上下文仍超过预算时，早期实现继续缩减保留内容，可能违反 `KeepRecent` 的下限要求。只验证最终长度是否低于预算，无法发现近期记忆被静默丢弃。

**约束修订。** [Context Manager 契约](../.agent/tasks/agent-context-manager-v1/contract.md)明确 `KeepRecent` 按消息条数计算，并且是硬下限。工具调用与全部对应结果组成原子单元，因此实际保留条数可以更多。单次构建只生成一次摘要，仍装不下则明确失败。

**修复与验证。** [contextmgr/manager.go](../contextmgr/manager.go)按约束选择保留尾部，再压缩前缀。[测试](../contextmgr/manager_test.go)覆盖恰好装下、少一个 token、超长近期消息和工具配对。审查记录包含临时改变计数方式、放宽预算校验等变异验证，确认断言能识别错误实现。

证据：[CLEAN-001 至 CLEAN-005](../.agent/tasks/agent-context-manager-v1/findings.md)；契约修订 `8e84932`，行为与测试修复 `bb991c1`、`61fa094`。

**经验。** “不超预算”和“保留必要记忆”是两个需要同时验证的约束。当前该能力已在模块中实现，但默认 HTTP 服务仍关闭压缩，不能把模块验收写成服务已启用。

## 案例三：空模型消息被当成成功回答

**问题。** Go 将 JSON `null` 解码到结构体时会留下零值。早期实现因此把 `choices[0].message=null` 当成空 assistant 消息，并接受 `finish_reason=stop`。

**修复与验证。** [响应解析](../llm/openai/wire.go)改用指针解码，拒绝缺失、null 或非对象 message；同时保留合法的 `content:null + tool_calls`。`TestCompleteErrorSemantics` 增加反例，`TestCompleteResponseParsing` 保护工具调用兼容性。历史审查回退解析代码后，新测试失败。

证据：[审查记录 CLEAN-002](../.agent/tasks/llm-openai-client-v1/findings.md) · [测试代码](../llm/openai/client_test.go)；修复提交 `b3d2146`。

**经验。** 协议字段的“缺失”“null”“空值”和“合法工具调用响应”需要分别测试，仅凭解码不报错不能认定响应有效。

## 记录范围与复核

上述问题与修复来自仓库记录，未补写不存在的对话或 Prompt。文中的提示词节选来自实际角色文件；案例描述是对任务和审查记录的整理，不是当时聊天原文。

真实模型验收后来已经完成，来源见[测试与验收](verification.md)。验收使用的模型名称不等于开发时使用的 AI 模型；后者未留档。真实密钥不属于应提交的开发记录。

需要追溯某次修复时，可在仓库中执行：

```bash
git show --stat 43ce197
git show --stat bb991c1
git show --stat b3d2146
```

完整任务材料位于 [.agent/tasks](../.agent/tasks/)；协作规则入口为 [AGENTS.md](../AGENTS.md)，详细 [Workflow 设计](design/agent-workflow.md)保留供需要了解开发流程的读者查阅。
