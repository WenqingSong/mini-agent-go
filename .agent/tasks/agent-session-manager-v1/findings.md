# Cleaner Findings

## Review Target

- 任务基线（Base commit）：`6902d2445665ec356a2db22b33b7749d39d8018e`（Task Review Baseline 声明）。
- Evidence Commit C1（本次审查对象，immutable）：`f23082ad0c25a65317cffe27ca91982dd437ed3f`（`feat(session): 落地内存 Session Manager 与 Turn 原子提交`），与 `state.review.target` 一致。
- 审查时 HEAD：`2bf8375d1599ea6618a45679517e9aff08b821f0`（C2 metadata commit，仅把 `state.yaml` 的 `review.*` 置为 `PENDING` 并绑定 C1，diff 只含 `review.status` / `review.target` 两行，review-neutral）；本地 HEAD == `origin/feat/agent-session-manager-v1`，working tree clean（`git status --short` 为空）。
- 基线区分：任务前无已有修改（Task Baseline 记录 + Cleaner 复核 `git status`）；C1 之前的提交为任务 artifact（task.md / contract.md / owner 状态文件），Contract A1 = `5263d2fc87d739ab524f2b9db73b74dd0ef30e5d`（`state.contract.target`，APPROVED）。
- 相关已跟踪文件（6902d24..f23082a）：生产 `session/manager.go`、`session/turn.go`、`session/errors.go`、`session/session.go`（增量扩展）；测试 `session/manager_test.go`、`session/turn_test.go`、`session/concurrency_test.go`、`session/expiry_test.go`、`session/cancel_test.go`；Design `docs/design/runtime-architecture.md`（Analyst 于 `0d9797b` 更新）。
- Scope 边界核对：`core/**`、`llm/**`、`tool/**`、`contextmgr/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml` 在基线与 C1 之间零改动；未新增 Contract 未列出的源文件；未新增第三方依赖。
- 全局资源：无。`.agent/workflow.yaml` 为 `resources: {}`，`state.resources.reservations` 为空，Contract 明确「全局资源：无」；实现中无 migration version / 错误码域类资源。Registry ↔ Contract ↔ 实现三边一致（无可核对项）。
- 关键配置：`ManagerConfig{TTL, Now}`；TTL 默认关闭（零值），测试经 `fakeClock` 注入可控时钟，未用 sleep 制造过期证据。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestManagerTurnCommitsHistorySummaryAndTodos`：写入 3 条消息（user + assistant 带 ToolCall + tool Result）、摘要、Todo 后逐字段断言；深拷贝隔离（改写返回值再读不受污染）；`TestManagerGetReportsMissingSession`：不存在会话返回 `invalid_argument` + `errors.Is(ErrSessionNotFound)` 且不含其它哨兵；空 ID 不当成不存在。 |
| AC-002 | PASS | `TestManagerDeleteKeepsOtherSessionIntact`：双会话交叉写入、逐条 ID 前缀断言互不相交；`Delete("s-a")` 后 `reflect.DeepEqual` 断言 s-b 消息/摘要/Todo 逐字段不变；重复删除返回 `ErrSessionNotFound`。清理隔离维度的旁证：`TestPurgeExpiredSkipsInFlightTurnAndRefreshesOnCommit` 中 s-1 被清理、s-3 在途、s-2 未过期不受影响。 |
| AC-003 | PASS | `TestConcurrentTurnsOnSameSessionDoNotInterleave`：M=8 goroutine、各 K=3 条唯一消息；断言总数 = 24、无重复 ID、按组前缀扫描连续性（被其它组打断即失败）。`TestConcurrentFailedTurnsLeaveNoPartialHistory`：8 个 Turn 中 4 个注入失败→仅 12 条入库、失败组消息零出现。二者经 `-race -count=5` 复跑。 |
| AC-004 | PASS | `TestTurnsOnDifferentSessionsRunConcurrently`：两会话 Turn 经 channel 互声明「已进入执行」并 barrier 相遇，双方都观察到对方；超时仅作卡死保护、不作性能判定。Cleaner 隔离副本变异 M1（per-session 信号量→包级单一全局信号量）后该测试 FAIL（5.00s，报「疑似存在跨会话全局锁」），证明能识别跨会话全局锁。 |
| AC-005 | PASS | `TestPurgeExpiredSkipsInFlightTurnAndRefreshesOnCommit`：注入 `fakeClock` 推进时钟（无 sleep），在途 Turn 期间 `PurgeExpired` 断言只清理过期空闲会话（purged=1）、在途会话不删且 `Get` 不阻塞、成功后提交生效并刷新 TTL（+50s 可读、+11s 后过期）。`TestFailedTurnDoesNotRefreshTTL` / `TestCancelledTurnDoesNotRefreshTTL`：失败/取消不刷新。Cleaner 复跑 `go test -race -count=5 ./...` 无 race 报告。变异 M3（PurgeExpired 不检查在途直接删）使该测试 FAIL（purged=2），证明测试有效。 |
| AC-006 | PASS | `TestQueuedTurnCancelledBeforeExecution`：执行权被占用时排队请求 cancel → `cancelled`、`atomic.Bool` 证明 fn 未执行、历史零写入、释放后新请求可取得执行权；`TestQueuedTurnTimesOut`：排队 100ms 超时 → `deadline_exceeded`、fn 未执行；`TestCancelDuringExecutionDiscardsStagedWrites`：fn 返回 nil 但 ctx 取消 → `cancelled` 且 `errors.Is(context.Canceled)`、基线 `DeepEqual` 不变、后续 Turn 可正常提交；`TestTurnPanicDoesNotCommitAndReleasesAuthority`：panic 不提交、执行权释放。变异 M2（删除 RunTurn 与 commitTurn 两处 P1 context 复核）使前两个取消用例 FAIL，证明测试有效。 |
| AC-007 | PASS | `TestTurnAppendRejectsInvalidPairingWithoutChangingHistory`：表驱动孤儿 Result / 缺失 Result / 重复 Result，断言 `invalid_pairing` 与 `CallID`，写入前后 `fingerprint`（JSON 序列化）一致、被拒批次不进入暂存；`TestTurnRejectsInvalidTodosAtomically`：空 ID / 未知 Status / 空 ID 删除被拒且列表不变；`TestManagerStoreAdapterReplacesAndValidates`：Save 非法序列带 CallID 拒绝且历史不变。变异 M4（Append 跳过 `core.ValidateMessages`）使 AC-007 主用例 FAIL，证明测试有效。 |
| AC-008 | PASS | 四方核对：Task（AC-001—008、Scope/Out of Scope）↔ APPROVED Contract（`state.contract.target=5263d2f`）↔ Design（`0d9797b` 更新）↔ 实现（C1）。Design §3 session 职责改为「类型 + Store 抽象 + 单进程内存 Session Manager」并列出「明确不负责」；§8.1 固化执行权/暂存原子提交/读隔离/生命周期/重入约束/哨兵错误；§9 补摘要归属；§11.1 新增 INV-009—INV-016 并标注与 Contract INV-001—008 的映射；§12 所有权表含 9 个 `session/` 文件且全部单归属 `session`；§14 移出「Store 内存实现/Todo 生命周期」仅保留持久化。`grep` 确认 Design 无「不实现任何存储」残留；`git ls-files session/` 与 §12 逐条一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | Cleaner 实跑，exit 0（go1.24.1 linux/amd64） |
| `go vet ./...` | PASS | Cleaner 实跑，exit 0 |
| `go test ./...` | PASS | core / llm / runlog / session 全 ok |
| `make test` | PASS | 与 CI `pr-check` 一致的入口，全 ok |
| `go test -race -count=5 ./...` | PASS | 全包 ok，无 data race 报告 |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | Engine 独立 module 不受影响 |
| `go list -m all` | PASS | 仅根 module `cnb.cool/framework-station/mini-agent-go`，零第三方依赖 |
| `go list -f ... ./session` import 核对 | PASS | 仅标准库 + `<module>/core`（INV-009 分层） |
| `git ls-files session/` ↔ Design §12 | PASS | 9 个文件逐一对应、单归属 |
| 变异 M1：全局信号量（模拟跨会话全局锁） | 测试 FAIL（预期） | 隔离副本 `/tmp/cleaner-mut1`（已删除）：`TestTurnsOnDifferentSessionsRunConcurrently` 5.00s 失败，报「疑似存在跨会话全局锁」 |
| 变异 M2：删除两处 P1 context 复核 | 测试 FAIL（预期） | 隔离副本 `/tmp/cleaner-mut2`（已删除）：`TestCancelDuringExecutionDiscardsStagedWrites`、`TestCancelledTurnDoesNotRefreshTTL` 失败（取消的 Turn 返回 nil 并企图提交） |
| 变异 M3：`PurgeExpired` 不检查在途 Turn | 测试 FAIL（预期） | 隔离副本 `/tmp/cleaner-mut3`（已删除）：`TestPurgeExpiredSkipsInFlightTurnAndRefreshesOnCommit` 失败（purged=2，在途会话被误删） |
| 变异 M4：`Turn.Append` 跳过配对校验 | 测试 FAIL（预期） | 隔离副本 `/tmp/cleaner-mut4`（已删除）：`TestTurnAppendRejectsInvalidPairingWithoutChangingHistory` 三个子用例失败 |
| 变异隔离性与清理 | PASS | 全部变异只在 `/tmp` disposable 副本执行，Review Target working tree 未做任何 Mutation；副本已删除，无遗留 |

## Findings

No actionable findings。

补充说明（不构成 Finding，记录备查）：

- `Turn.SetSummary` / `Turn.SetTodos` 在 Turn 关闭后是静默 no-op：二者按 APPROVED Contract 的接口签名没有错误返回（`session/turn.go:102-122`），与 Contract「接口以此为准」一致；契约失败语义表中「Turn 关闭后写方法返回 invalid_argument」实际覆盖的是 `Append` / `PutTodo` / `DeleteTodo`（有测试 `TestTurnWritesAfterCallbackReturnedAreRejected` 证明）。此为 Contract 接口签名的必然结果，不是实现漂移。
- `RunTurn` / `Delete` 直接命中「已过期」分支无独立用例，但该分支与 `Get` 共用 `acquire` 的过期判定路径（`manager.go:385-389`），`Get` 命中路径已由 `TestExpiredSessionIsPurgedOnAccess` 等覆盖；AC 未单独要求，不补 Finding。
- 测试以 `-count=5` 与显式 channel/WaitGroup 同步为证据；`runTurnWithin` 的 5s、barrier 的 5s 仅作卡死保护，未被用作并发/过期判定（符合 Contract 禁止用时间阈值作证据的要求）。
