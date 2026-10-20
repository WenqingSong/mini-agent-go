# Owner 核心逻辑验证卡

本文件只承载 Owner 核心逻辑验证卡、因果说明与验证证据。Owner 决策状态（`state.owner.*`）由 OwnerGate 在 Owner 明确决定后持久化，Cleaner 不写。

审查对象：C1'' = `61fa094`（`state.review.status = CLEAN`）。以下验证卡均可由 Owner 在本地复跑。

## CL-001：`KeepRecent` 是保留条数的硬下限，预算不足时显式失败而不是静默丢弃近期消息

- Owner 需要理解：会话历史被压缩时，最近 `KeepRecent` 条消息必须原样保留；如果「System Prompt + 新摘要 + 这 `KeepRecent` 条」仍然装不进 `TokenBudget`，`Build` 必须报错（`invalid_argument`）而不是继续删减保留消息。否则会把用户当前这一轮提问也压掉，调用方按持久化配方还会把它从会话历史里删掉——模型拿到一个没有当前问题的请求，历史被静默改写。
- 生产代码：`contextmgr/manager.go:89-100`（按 `KeepRecent` 取「消息条数 ≥ N」的最小尾部单元集合，无可压缩的更早历史即失败）；`contextmgr/manager.go:102-155`（只压缩尾部之前的前缀一次，组装后仍超预算即 `invalid_argument`，不进入缩尾循环）；`contextmgr/manager.go:290-305`（`tailUnitIndex` 按消息条数累加）。
- 关键测试：`contextmgr/manager_test.go:320` `TestBuildFailsWhenKeepRecentTargetExceedsBudget`、`:398` `TestBuildKeepsMinimumRecentMessages`、`:1012` `TestBuildCompactionBudgetBoundaryExactFit`（预算恰好=压缩后估算则成功、少 1 token 即失败，且两条路径都要求 `Summarizer` 恰好调用 1 次）。
- 基线验证：`go test ./contextmgr/ -run 'TestBuildFailsWhenKeepRecentTargetExceedsBudget|TestBuildKeepsMinimumRecentMessages|TestBuildCompactionBudgetBoundaryExactFit' -count=1` → 预期全部 PASS。
- 可选 Mutation：把 `contextmgr/manager.go:299` 的 `count += len(units[i].msgs)` 改为 `count++`（把 `KeepRecent` 误解为「单元个数」而非「消息条数」）。
- 预期失败：`TestBuildKeepsMinimumRecentMessages`（含 3 消息工具单元时保留 7 条而非 4 条）、`TestBuildToolUnitsAreAtomic`、`TestBuildCompactionBudgetBoundaryExactFit` 均 FAIL（Cleaner 已在隔离副本实测）。
- 恢复确认：恢复该行后重跑上述 `go test -run` 命令 → PASS。

## CL-002：工具调用与其全部结果作为原子单元整体保留或整体压缩

- Owner 需要理解：assistant 消息里的 `ToolCalls` 和应答它们的 `ToolResults` 必须同进同出。若压缩边界落在中间，Provider 会收到「有调用没结果」或「有结果没调用」的非法序列（`invalid_pairing`），真实模型调用会直接失败或返回不可信结果。因此压缩只能以整组为单位取舍，且输出前必须过 `core.ValidateMessages`。
- 生产代码：`contextmgr/manager.go:250-283`（`splitUnits`：从携带调用的 assistant 消息起吞并后续 tool 结果，中途出现的新调用也并入同一单元）；`contextmgr/manager.go:89-100`（只按单元边界切尾部）；`contextmgr/manager.go:158-165`（输出前 `core.ValidateMessages` 自检，失败按 `internal`）。
- 关键测试：`contextmgr/manager_test.go:462` `TestBuildToolUnitsAreAtomic`（0/1/2 调用 × `KeepRecent` 0~9 × 预算 1~12：保留区间必须是输入后缀、不得以 tool 开头、`DroppedMessages` 与实差一致）；`:1012` `TestBuildCompactionBudgetBoundaryExactFit`（保留边界刻意落在工具单元内的预算临界点）。
- 基线验证：`go test ./contextmgr/ -run 'TestBuildToolUnitsAreAtomic|TestBuildCompactionBudgetBoundaryExactFit' -count=1` → 预期 PASS。
- 可选 Mutation：把 `contextmgr/manager.go:259` 的 `if isAssistantWithCalls(msgs[i])` 改为 `if false && isAssistantWithCalls(msgs[i])`（每条消息各自成单元，不再合并调用与结果）。
- 预期失败：`TestBuildToolUnitsAreAtomic` FAIL——保留边界落到 tool 结果上，视图被截断或自检返回 `internal`（Cleaner 已在隔离副本实测）。
- 恢复确认：恢复该判断后重跑上述命令 → PASS。
