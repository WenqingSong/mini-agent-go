# Cleaner Findings

## Review Target

- 任务：`session-turn-drop-leading-v1`（`feat/session-turn-drop-leading-v1`）
- 任务基线 Base commit：`dc734d20a732dcc7fc53dc2313c15dd003e919f9`（`origin/develop` 同 SHA）
- APPROVED Contract：A1 = `bcc8e96805d8f0754c2b0b75cfc40a7270f3b293`（`contract.md` 自 A1 起未变：`git diff bcc8e96 HEAD -- contract.md` 为空）
- 被审查的 immutable Implementation Evidence Commit **C1 = `2ed50f1c225323dcf392aaea403758576513dcaf`**（`feat(session): 支持 Turn 内原子删除已提交历史前缀`）
- 当前 Git 状态：HEAD `f9a5e050912b1ac960fec47487e1bdb50e52c2e7`（C2，仅 `state.yaml`；`git diff --stat 2ed50f1 HEAD` 只有 `state.yaml`，`git diff 2ed50f1 HEAD -- session` 为空）；working tree clean；`origin/feat/session-turn-drop-leading-v1` == HEAD
- 审查范围（C1 全部变更，3 个文件）：`session/turn.go`（+53/-4）、`session/turn_test.go`（+344）、`session/concurrency_test.go`（+120）
- 相关长期设计：`docs/design/runtime-architecture.md` §8.1 / §11.1 / §12（Analyst commit `bcc8e96`，早于 C1，未在 C1 中变动）
- 任务前已有修改：无（`task.md` 记录 clean；`dc734d2..C1` 全部为本人任务提交）
- 关键资源/迁移版本：无。`.agent/workflow.yaml` 为 `resources: {}`，`.agent/registry/` 不存在，`state.resources.reservations` 为空 → 无 Registry 三边检查对象
- Mutation 验证环境：临时 worktree `/tmp/cleaner-mut`（detached C1），验证后已 `git worktree remove`，主工作区未被改动

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `session/turn_test.go:343 TestTurnDropLeadingCommitsTruncatedHistory` 走 `RunTurn` + `Manager.Get`，覆盖 `n=0` / `n=2` / `n=len(base)` / `n=len(base)` 且不追加；逐条 JSON 指纹比对 ID、角色、内容与顺序，总条数 `m-n+k`；同时断言摘要不被删除操作改变。独立复跑 `go test -count=1 -race -run TestTurnDropLeading ./session` 全绿 |
| AC-002 | PASS | `session/turn_test.go:397 TestTurnDropLeadingRejectsInvalidCountAtomically`：`n=-1`、`n=len(base)+1`、已 `Append` 2 条后 `n=len(base)+1` 与 `n=len(base)+2`；断言 `invalid_argument`、`Op=session.Turn.DropLeading`、错误文本含 `n` 与 `[0, len(base)]`，调用前后 `Session()` 指纹相同，随后同 Turn 内 `DropLeading(1)` 仍成功提交并按 `base[1:]+stage` 对账 |
| AC-003 | PASS | `session/turn_test.go:455 TestTurnDropLeadingRejectsInvalidPairingBoundary`：边界切断 `call_1` 与其结果（孤儿 Tool Result）、删除后残留 `call_2` 孤儿结果、带暂存消息三条用例；断言 `invalid_pairing` 且 `CallID` 精确匹配，调用前后视图指纹相同，换 `DropLeading(3)` 后同 Turn 成功提交。另 `TestTurnDropLeadingFailureDiscardsWholeTurn` 证明被判非法的写入导致整轮不提交 |
| AC-004 | PASS | 成功路径 `TestTurnDropLeadingCombinesWithSummaryAndAppend`（同一 Turn 内 `SetSummary` + `DropLeading(2)` + `Append`，一次 `Get` 同时断言摘要与 `base[2:]+新消息`）；失败路径 `TestTurnDropLeadingFailureDiscardsWholeTurn`（前缀删除 + 摘要 + 追加已暂存后，后续非法边界使整轮 `reflect.DeepEqual` 不变）。合法 Todo 与 P5 非法 Todo 的整轮回滚由既有 `TestTurnCommitRejectsInvalidTodoFromSetTodos` 覆盖，机制与本任务新增字段无关 |
| AC-005 | PASS | `TestTurnDropLeadingDiscardedOnFailureCancelAndTimeout`：回调返回错误（`internal`）、`cancel()`（`cancelled`）、`ctx` 超时（`deadline_exceeded`）三条路径均 `reflect.DeepEqual` 比较整份 `Session`（历史 / 摘要 / Todo）；"回调返回后拒绝写入"断言 `invalid_argument` + `Op=session.Turn.DropLeading` + 文本 `Turn 已结束，不再接受写入`，且会话不变 |
| AC-006 | PASS | `session/concurrency_test.go:274 TestTurnDropLeadingConcurrentWithAppendAndSummary`：6 个 `DropLeading(1)` + 6 个 `Append`（各 2 条）+ `Session()` / `SetSummary` 并发，`sync.WaitGroup` 同步、无 `sleep`；按成功删除数结算（4 次成功、2 次合法 `invalid_argument` 拒绝），断言最终条数、剩余历史为基线的连续后缀、追加消息完整且不重复。`go test -race -count=5 ./...` 全绿无 race 报告；既有同会话串行 / 跨会话并发测试未回归 |
| AC-007 | PASS | 四方核对结果：①`Turn.DropLeading(n int) error` 签名、`n` 计数基准（相对当前剩余 `t.base`，累计单调）、`n=0` / `n=len(base)` / 负数 / 越界语义、三种错误（closed / invalid n / 原样 `invalid_pairing`）与 APPROVED Contract `Selected Design` / `Interfaces and Data` 逐条一致；②`docs/design/runtime-architecture.md:128-129` 已把 Turn 写入能力改写为「消息追加 `Append` + 已提交历史前缀删除 `DropLeading` + 摘要 + Todo」并新增前缀删除语义条目；③§11.1 新增 `INV-017`（`:215`，附来源说明 `:217`），与实现一致；④§12（`:250`/`:253`/`:254`）登记了 `session/turn.go` 的新职责，无新增文件，`git ls-files session` 的 9 个文件与 §12 的 9 行单归属一致。全仓 `docs/design` 已无「Turn 只支持追加消息」类冲突陈述 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `GOPROXY=off go build ./...` | PASS | Go 1.24.1 linux/amd64，退出码 0 |
| `GOPROXY=off go vet ./...` | PASS | 退出码 0 |
| `GOPROXY=off go test -count=1 ./...` | PASS | 8 个包全绿（非缓存） |
| `GOPROXY=off go test -race -count=5 ./...` | PASS | 8 个包全绿，无 race 报告；`session` 2.05s |
| `GOPROXY=off make test` | PASS | 退出码 0 |
| `GOPROXY=off go test -count=1 -race -run TestTurnDropLeading -v ./session` | PASS | 6 个新增测试、13 个子用例全部 PASS |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | Engine 独立 module 两个包全绿 |
| `go list -m all` | PASS | 仅 `cnb.cool/framework-station/mini-agent-go`，无新增第三方依赖 |
| Mutation M1：`DropLeading` 截断尾部（`t.base[:len(t.base)-n]`）而非前缀 | PASS（测试正确失败） | `TestTurnDropLeadingCommitsTruncatedHistory` / `RejectsInvalidCountAtomically` / `RejectsInvalidPairingBoundary` / `CombinesWithSummaryAndAppend` / `FailureDiscardsWholeTurn` 5 个测试 FAIL |
| Mutation M2：边界判定改为 `n >= len(t.base)`（禁掉清空） | PASS（测试正确失败） | `CommitsTruncatedHistory`（含 `n=len(base)` 子用例）与并发测试 FAIL |
| Mutation M3：删除 Turn 关闭检查 | PASS（测试正确失败） | `DiscardedOnFailureCancelAndTimeout/回调返回后拒绝写入` FAIL |
| Mutation M4：删除调用点 `core.ValidateMessages` 校验 | PASS（测试正确失败） | `RejectsInvalidPairingBoundary` 三个子用例全 FAIL |
| Mutation M5：调用点校验只校验 `t.base[n:]`，不含 `t.messages` | 等价变异（测试仍通过） | 在既有不变量（`Manager.Save` 校验历史 + `Append` 校验 `base+messages`）下不可区分；详见 CLEAN-001（P3） |
| Scope 检查：C1 仅改 `session/turn.go`、`session/turn_test.go`、`session/concurrency_test.go` | PASS | `git diff --stat 05ec9e2 2ed50f1`；`core/**`、`contextmgr/**`、`session/manager.go`、`go.mod`、`Makefile` 零变更 |
| 全局资源三边一致性（Registry ↔ Contract ↔ 实现） | NOT_APPLICABLE | `resources: {}`，本任务无 Shared Resource |
| 并发/Context/锁风险 | PASS | 每次写方法在同一 `t.mu` 内完成，无调用 Manager 执行权的二次加锁路径；`-race -count=5` 无报告；`DropLeading` 使用 `defer t.mu.Unlock()`，panic 不遗留锁 |
| 错误处理 / 信息泄漏 | PASS | 参数错误只暴露 `n` 与 `[0, len(base)]`，不含会话内容；配对错误原样返回 `core` 错误并保留 `CallID` |

## Findings

### CLEAN-001：`暂存消息与剩余历史一起参与校验` 用例在当前不变量下不可区分

- Severity：P3
- Status：OPEN
- Location：`session/turn_test.go:473`（`TestTurnDropLeadingRejectsInvalidPairingBoundary` 的第三个子用例）；对应生产代码 `session/turn.go:102-108`
- AC / Invariant：INV-003（调用点配对安全，「剩余已提交历史 + 暂存消息」必须通过 `core.ValidateMessages`）
- Trigger：把 `DropLeading` 的调用点校验改成只校验 `t.base[n:]`（Mutation M5），保留暂存消息不参与校验
- Actual：`go test -count=1 -run TestTurnDropLeading ./session` 仍然全绿；该子用例的 `invalid_pairing` 实际由「剩余历史以孤儿 Tool Result 开头」触发，与暂存消息无关
- Expected：用例名与注释声称能证明暂存消息参与校验，实际无法区分；要么改名为「剩余历史校验优先级」之类，要么明确记录该分支在当前不变量下不可达
- Impact：仅影响测试可读性与后续维护判断，不产生错误业务行为。理由：`Manager.Save` 已校验已提交历史、`Append` 已校验 `base+messages`，因此暂存消息不可能引用被删前缀中的调用（引用即重复结果，会在 `Append` 被拒），故「`base[n:]` 合法 ⇒ `base[n:]+messages` 合法」成立，M5 为等价变异
- Evidence：M5 变异后 `session` 包 `ok`；`session/manager.go:244` 的 `core.ValidateMessages` 与 `session/turn.go:138` 的 `Append` 校验
- Required Fix Boundary：不要求改生产代码，也不要求为此新增断言；由 Owner 决定是否调整该子用例的名称/注释，或补一条真正可区分的场景。不得为此放宽 `DropLeading` 的现有校验
