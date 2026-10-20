# Core Logic

本文件只承载 Owner Core Logic 验证卡与验证证据；Owner 决策状态（`state.owner.*`）由 OwnerGate 在 Owner 明确决定后持久化，Cleaner 不写、不推断。

审查对象：C1 `f23082ad0c25a65317cffe27ca91982dd437ed3f`。

## CL-001：每会话执行权（容量 1 信号量）——同会话串行、跨会话并发

- Owner 需要理解：如果实现退化成「一把跨会话大锁」，不同会话的 Turn 会互相阻塞，后续 Agent Loop 失去真正并行能力，等待方还可能死锁或饥饿；正确形态是每个会话一把可被 `context` 取消的执行权。
- 生产代码：`session/manager.go:40-47`（`entry.authority` 为容量 1 的 channel 信号量，每会话独立）；`session/manager.go:359-392`（`acquire` 用 `select` 同时等待取得执行权与 `ctx.Done()`，`map` 锁只保护查找、绝不跨越回调）。
- 关键测试：`session/concurrency_test.go` 的 `TestTurnsOnDifferentSessionsRunConcurrently`（双会话 Turn 经 channel barrier 互证同时进入执行）。
- 基线验证：`cd /root/projects/mini-agent-go && go test -race -run TestTurnsOnDifferentSessionsRunConcurrently ./session/` → PASS。
- 可选 Mutation：把 `session/manager.go` 的 `newEntry` 中 `make(chan struct{}, 1)` 改为包级单一 `var g = make(chan struct{}, 1)` 并令 `e := &entry{authority: g}`（即所有会话共享一把执行权）。
- 预期失败：两个会话的 Turn 无法在同步点相遇，`TestTurnsOnDifferentSessionsRunConcurrently` 以「等待对方进入执行超时：疑似存在跨会话全局锁」失败（Cleaner 已在隔离副本实测 FAIL，约 5.00s）。
- 恢复确认：还原为 per-session channel 后重跑 `go test -race -run TestTurnsOnDifferentSessionsRunConcurrently ./session/` → PASS。

## CL-002：提交条件 P1——发布前最终复核 context

- Owner 需要理解：若只凭回调返回 `nil` 就提交，已取消/已超时的 Turn 仍能改写会话历史与摘要，等于「取消之后状态还被污染」；正确形态是 fn 返回后与发布前各复核一次 context，任一不过就整轮丢弃。
- 生产代码：`session/manager.go:212-215`（fn 返回后复核 `ctx.Err()`，P1 优先于回调返回值）；`session/manager.go:413-416`（`commitTurn` 发布前再次复核，暂存最终不能绕过取消生效）。
- 关键测试：`session/cancel_test.go` 的 `TestCancelDuringExecutionDiscardsStagedWrites`（回调返回 nil 但 ctx 已取消 → 返回 `cancelled`、`errors.Is(context.Canceled)`、基线历史 DeepEqual 不变、执行权释放后后续 Turn 可正常提交）；`session/expiry_test.go` 的 `TestCancelledTurnDoesNotRefreshTTL`（取消的 Turn 连 TTL 都不刷新）。
- 基线验证：`cd /root/projects/mini-agent-go && go test -race -run 'TestCancelDuringExecutionDiscardsStagedWrites|TestCancelledTurnDoesNotRefreshTTL' ./session/` → PASS。
- 可选 Mutation：删除上述两处 `contextError(op, ctx)` 复核（两处都删，只删一处仍会被另一处拦下）。
- 预期失败：已取消的 Turn 返回 nil 并提交暂存，`TestCancelDuringExecutionDiscardsStagedWrites` 的 `cancelled` 断言与历史不变断言失败（Cleaner 已在隔离副本实测 FAIL：「期望错误种类 cancelled，实际没有返回错误」）。
- 恢复确认：还原两处 P1 复核后重跑上述命令 → PASS。
