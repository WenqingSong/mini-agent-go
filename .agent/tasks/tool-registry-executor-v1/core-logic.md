# Owner Core Logic 验证卡

任务：`tool-registry-executor-v1`
Review Target（CLEAN 绑定的实现快照）：`03dced5177ba7db864fe6c553a9ca4ce2e7c6136`

以下两张卡对应本任务真正决定业务正确性的机制。每张都给出「代码位置 → 业务后果 → 测试失败」的因果关系，供 Owner 用少量阅读 + 可逆 Mutation 亲自确认。

## CL-001：执行前把关与同 Run 去重占位

- Owner 需要理解：任何非法调用（未知工具、参数不合法、同 Run 内重复 Tool Call ID）都**不得**进入工具实现；同一次 Run 里同一个 Call ID 至多真正执行一次。若这条失效，模型可以重复触发有副作用的工具（重复下单 / 重复写文件），或用缺字段、错类型的参数把脏数据塞进工具。
- 生产代码：`tool/executor.go:58-131`（`Execute` 固定判定顺序：CallID → RunID → 名称 → 父 Context → 去重占位 → 查表 → 参数校验 → 执行）、`tool/executor.go:120-131`（`claim` 原子占位）；参数校验在 `tool/validate.go:182-276`（`validateArguments` / `validateValue` / `validateObject` / `validateArray`，含 CLEAN-001 修复：约束按参数值形态生效，不因子 Schema 未声明 `type` 而失效）
- 关键测试：
  - `tool/executor_test.go:497` `TestExecutorRejectsNestedConstraintsWithoutType`（含「合法参数放行、调用计数 1」对照）
  - `tool/validate_test.go:285` `TestValidateArgumentsEnforceConstraintsWithoutType`
  - `tool/executor_test.go:160` `TestExecutorRejectsBeforeToolExecution`、`tool/executor_test.go:353` `TestExecutorDedupWithinRun`、`tool/executor_test.go:386` `TestExecutorDedupScopeIsPerRun`、`tool/executor_test.go:407` `TestExecutorDedupConcurrent`
- 基线验证：`go test -race ./tool/ -run 'TestExecutorRejects|TestValidateArguments|TestExecutorDedup' -count=1` → PASS（约 0.05s）
- 可选 Mutation（任一，均在隔离副本中做，做完恢复）：
  1. `tool/executor.go:121` 把 `dedupKey{runID: runID, callID: callID}` 改成 `dedupKey{callID: callID}`（去重键丢 RunID）
  2. `tool/validate.go:229-238` 把 `validateValue` 改回 `switch node.typ` 分支实现（未声明 `type` 的子 Schema 约束重新被忽略）
- 预期失败：
  1. `TestExecutorDedupScopeIsPerRun` FAIL（不同 Run 的同名 CallID 被误判为重复）
  2. `TestExecutorRejectsNestedConstraintsWithoutType` / `TestValidateArgumentsEnforceConstraintsWithoutType` FAIL（缺必填、错类型、未声明字段不再被拒绝）
- 恢复确认：恢复原代码后重跑上面的基线验证命令，回到全 PASS

## CL-002：Context 边界——取消优先于工具结果、超时绝不伪装成功

- Owner 需要理解：工具级局部超时必须回到「可恢复失败」（`IsError=true`），父 Context 取消/整体超时必须向上返回 `cancelled` / `deadline_exceeded`，且**父 Context 已结束时即使工具刚跑完也必须丢弃其结果**。若这条失效，已取消的 Run 会拿到看似成功的结果并继续往下走，调用方还会被阻塞到工具自己返回为止。
- 生产代码：`tool/executor.go:137-204`（`run`：工具在独立 goroutine 执行、`context.AfterFunc` 把父 Context 结束信号转投到 `abort`、`select` 等待「结果 / 工具级超时 / 取消」）、`tool/executor.go:171-176`（`done` 分支的取消优先守卫）、`tool/executor.go:198-213`（`contextRunError`）、`tool/executor.go:215-224`（`panicRunError`，不泄漏堆栈）
- 关键测试：
  - `tool/executor_test.go:563` `TestExecutorCancelBeatsToolResult`（200 次循环，工具返回前自行取消父 Context）
  - `tool/executor_test.go:263` `TestExecutorToolTimeout`、`tool/executor_test.go:289` `TestExecutorParentDeadlineDuringExecution`、`tool/executor_test.go:230` `TestExecutorToolPanic`、`tool/executor_test.go:471` `TestExecutorTimeoutDoesNotGrowGoroutines`
- 基线验证：`go test -race ./tool/ -run 'TestExecutorCancel|TestExecutorToolTimeout|TestExecutorParentDeadline|TestExecutorToolPanic|TestExecutorTimeoutDoesNotGrowGoroutines' -count=1` → PASS（约 0.1s）
- 可选 Mutation：删除 `tool/executor.go:171-176` 中 `case out := <-done:` 下的 `if err := ctx.Err(); err != nil { return core.ToolResult{}, contextRunError(err, req.Call.ID) }` 三行守卫
- 预期失败：`TestExecutorCancelBeatsToolResult` 首轮即 FAIL（`期望错误种类 cancelled，实际没有错误`）；注意 `TestExecutorCancelDuringExecution` 在这种情况下仍会 PASS——这正是 CLEAN-002 修复前该不变量无有效保护的原因
- 恢复确认：恢复三行守卫后重跑基线验证命令，回到全 PASS

## 已知风险与边界（不阻塞，供 Owner 一并掌握）

- CLEAN-003（P3，未修复）：执行期参数校验读工具实时 `Spec()` 而非 Registry 注册快照（`tool/executor.go:107`），需工具自身注册后改 Spec 才可绕过；每次调用重复编译 Schema。是否处理由 Owner 决定。
- Contract `Open Risks` 仍然有效：占位表只增不删、拒绝空 `RunID` 提高集成前置要求、fail-closed 子集未来扩充须走 Contract Revision、忽略 Context 的工具其 goroutine 与副作用不可撤销、`Seal()` 依赖装配层主动调用、时间相关测试在慢速 CI 上需留余量。
