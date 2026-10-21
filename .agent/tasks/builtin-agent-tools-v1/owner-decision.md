# Owner Decision

## Review Target

- Review Target commit：`92068976d73d29384c9227fc90d648fb3ddb3261`（C1，`feat(builtin): 交付五个内置工具与 Todo 写入端口`）
- 任务基线：`6902d2445665ec356a2db22b33b7749d39d8018e`；任务：`builtin-agent-tools-v1`（T03 内置 Agent 工具）
- 本决策绑定上述 CLEAN snapshot，`owner.review_target` 同值。

## Core Logic

- CL-001：失败分级与内部信息防泄漏（INV-B02 / AC-009）——领域失败 `IsError=true` 且 `error=nil`，基础设施失败返回非 nil `*core.RunError`，模型可见 `Content` 不含路径 / 连接串 / 凭据等内部细节。
- CL-002：会话作用域来源唯一（INV-B05 / AC-004 / AC-005）——会话身份只取自可信 `tool.Request.SessionID`，空 SessionID 拒绝且不触达端口，`Arguments` 中的 `session_id` / `sessionId` / `session` 字段一律拒绝。

## Owner Decision

ACCEPTED

## Decision Evidence

- Owner 于 2026-10-20 明确回复 `ACCEPT`，并确认已阅读 Cleaner 审查结论与 OwnerGate 提供的核心机制、验证证据（含两条基线验证 PASS 与两项 Mutation 实证）。
- 接受范围仅限 T03 的**工具层**实现与已验证行为；Owner 接受的是 Review Target `92068976d73d29384c9227fc90d648fb3ddb3261` 这个具体 snapshot，非抽象接受整个任务。
- Owner 保留的后续集成要求（不改变本决策，移交后续任务）：
  - T04 负责真实 Session / Todo 数据管理及隔离；
  - T07 负责 `builtin.TodoOperations` 与 `session.TodoOperations` 的适配、Todo ID 生成与状态转换；
  - 后续必须验证跨会话隔离、并发及完整 Todo 调用链。
- 证据边界（同 core-logic.md）：真实会话隔离、跨会话不可写、Todo 生命周期与持久化并发不由本任务证明。
