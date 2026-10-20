# 核心逻辑

> 本文件承载 Owner Core Logic 验证卡。Owner 决策状态权威在 `state.owner`（OwnerGate 负责），本文件不记录、也不推断 Owner 是否接受。

对应 Review Target C1 = `92068976d73d29384c9227fc90d648fb3ddb3261`。以下 Mutation 均须在**临时隔离 worktree / disposable checkout**执行，不得在 review-target 工作区直接改动后恢复，不得 commit Mutation。

## CL-001：失败分级与内部信息防泄漏（INV-B02 / AC-009）

- Owner 需要理解：可恢复的领域失败必须表现为 `IsError=true` 且 `Execute` 的 `error` 为 nil（让模型据此继续决策）；不可恢复的基础设施失败（端口返回非领域错误、`ctx` 已结束、端口未注入）必须返回非 nil 的 `*core.RunError`，且模型可见的 `Content` 不得写入路径、连接串、凭据等内部细节。二者混淆的后果：要么把真实故障伪装成"成功结果"误导模型，要么把内部敏感信息泄漏给模型。
- 生产代码：`builtin/todo.go` —— `beginTodo`（`258-277`：端口未注入→`KindInternal`）、`contextError`（`292-314`：ctx 取消/超时→`KindCancelled`/`KindDeadlineExceeded` 且不触达端口）、`portError`（`320-336`：原始错误只进 `RunError.Cause`，`Content` 留空，`Message` 固定中文）。
- 关键测试：`builtin/todo_test.go` —— `TestTodoPortInfrastructureFailure`（`500`）、`TestTodoCanceledContext`（`550`）、`TestTodoWithoutOperations`（`586`）。
- 基线验证：`go test -run 'TestTodoPortInfrastructureFailure|TestTodoCanceledContext|TestTodoWithoutOperations' -race ./builtin/` → PASS。
- 可选 Mutation：在 `portError` 返回值中给 `core.ToolResult{...}` 增加 `Content: cause.Error()`（把端口错误原文写进模型可见 Content）。
- 预期失败：`TestTodoPortInfrastructureFailure/普通错误` 必失败——Content 出现 `password` / `/var/lib`，命中 `todo_test.go:542` 的泄漏断言。
- 恢复确认：还原该行后重跑基线命令应恢复 PASS（Cleaner 已在隔离 worktree 实证：该 Mutation 确实导致 FAIL）。

## CL-002：会话作用域来源唯一（INV-B05 / AC-004 / AC-005）

- Owner 需要理解：Todo 工具的会话身份只能取自可信的 `tool.Request.SessionID`；空 `SessionID` 必须拒绝且**不触达端口**，`Arguments` 中出现 `session_id`/`sessionId`/`session` 必须拒绝。否则模型可经参数越权选择或读写其他会话的待办。
- 生产代码：`builtin/todo.go` —— `beginTodo` 的空 `SessionID` 拒绝（`271-274`，先于端口调用）；`builtin/builtin.go` —— `sessionKeys`（`50`）与 `hasSessionField`（`114-121`）。
- 关键测试：`builtin/todo_test.go` —— `TestTodoEmptySessionIDRejected`（`336`）、`TestTodoSessionFieldsRejected`（`363`）、`TestTodoCrossSessionScope`（`292`）。
- 基线验证：`go test -run 'TestTodoEmptySessionIDRejected|TestTodoSessionFieldsRejected|TestTodoCrossSessionScope' -race ./builtin/` → PASS。
- 可选 Mutation：把 `beginTodo` 的 `if req.SessionID == "" {` 改为 `if false && req.SessionID == "" {`（取消空会话拒绝）。
- 预期失败：`TestTodoEmptySessionIDRejected` 三个子用例全部失败——空 `SessionID` 不再被拒，端口被调用且返回非 `missing_session_id`。
- 恢复确认：还原该行后重跑基线命令应恢复 PASS（Cleaner 已在隔离 worktree 实证：该 Mutation 确实导致 FAIL）。

## 证据边界（务必向 Owner 说明）

- 上述两条只证明**工具层**行为：把 `req.SessionID` 原样透传、按码映射失败、不 panic、并发无竞态。
- **真实**会话隔离、跨会话不可写、Todo 生命周期与持久化并发不由本任务证明，归 Session Manager（T04）与装配层（T07）；`builtin.TodoOperations` 与 `session.TodoOperations` 是两套端口（Owner D7 方案②），适配、Todo ID 生成与状态迁移由 T07 显式承担（见 Contract C4 / Design §14）。
