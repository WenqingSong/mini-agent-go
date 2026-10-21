# Owner Decision

任务：`tool-registry-executor-v1`
决定时间：2026-10-20（Owner 在 OwnerGate Decision Checkpoint 内明确 ACCEPT）

## Review Target

`03dced5177ba7db864fe6c553a9ca4ce2e7c6136`（C1'，`fix(tool): 修复 CLEAN-001 约束静默忽略与 CLEAN-002 取消优先守卫不可测`）

Owner 接受的是 Cleaner 已 CLEAN 的**这个具体 snapshot**，不是抽象地接受本任务。

## Core Logic

- CL-001：执行前把关与同 Run 去重占位——任何非法调用（未知工具、参数不合法、同 Run 内重复 Tool Call ID）都不得进入工具实现；同一次 Run 里同一个 Call ID 至多真正执行一次。生产代码 `tool/executor.go:58-131`、`tool/validate.go:182-276`；测试 `tool/executor_test.go`（RejectsBeforeToolExecution / DedupWithinRun / DedupScopeIsPerRun / DedupConcurrent / RejectsNestedConstraintsWithoutType）与 `tool/validate_test.go`（EnforceConstraintsWithoutType）。
- CL-002：Context 边界——取消优先于工具结果、超时绝不伪装成功——工具级局部超时回到可恢复失败（`IsError=true`），父 Context 取消/整体超时向上返回 `cancelled`/`deadline_exceeded`，父 Context 已结束时即使工具刚跑完也必须丢弃其结果。生产代码 `tool/executor.go:137-233`；测试 `tool/executor_test.go`（CancelBeatsToolResult / ToolTimeout / ParentDeadlineDuringExecution / ToolPanic / TimeoutDoesNotGrowGoroutines）。

## Owner Decision

ACCEPTED

## Decision Evidence

1. **接受范围**：Owner 确认接受 CL-001、CL-002 的实现及已报告的验证证据（基线验证命令由 OwnerGate 实跑 PASS；Cleaner findings.md 的 Mutation 证据已核阅）。
2. **CLEAN-003（P3）暂缓**：执行期参数校验读取工具实时 `Spec()`（`tool/executor.go:107`）而非注册快照的问题，Owner 明确暂缓处理，不要求本轮修复。不影响本任务任何 AC。
3. **去重生命周期决定（Owner 明确）**：
   - 接受当前 Contract 规定的 Executor 实例内部 `(RunID, CallID)` 去重记录「只增不淘汰」行为，本轮不修改代码。
   - 「去重记录不得无限积累」的要求**不撤销**，明确交由后续 Agent Runtime 装配任务落实。
   - 建议采用每 Run 创建独立 Executor 的方式：同一 Run 内复用同一实例，不同 Run 不共享实例，Run 结束后释放引用，使去重状态具备被回收的条件。
   - 该方案**尚未实现和验证**，不得提前宣称内存生命周期问题已解决；后续必须验证运行期组件装配、跨 Run 隔离和资源释放。
   - 如果实际装配要求长期共享 Executor，或每 Run 实例无法保证正确性，必须在集成前提出替代设计并完成必要的 Contract Revision；不允许带着无界累积问题直接宣布最终验收通过。
4. **Contract Open Risks 仍然有效**：占位表随 Executor 生命周期累积（本决定第 3 条已明确后续落实路径）、拒绝空 `RunID` 提高集成前置要求、fail-closed Schema 子集扩充须走 Contract Revision、忽略 Context 的工具其 goroutine 与副作用不可撤销、`Seal()` 依赖装配层主动调用、时间相关测试在慢速 CI 上需留余量、panic 归 `tool_failure` 影响面为 Run 级、§12 文件所有权表须与 `git ls-files tool/` 一致。
5. **适用范围**：本决定仅绑定 Review Target `03dced5`；该 snapshot 之后若出现实质变化，本决定失效，须重新走 OwnerGate。
