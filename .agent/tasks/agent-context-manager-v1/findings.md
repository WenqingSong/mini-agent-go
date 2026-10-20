# Cleaner Findings

## Review Target

- Task：`agent-context-manager-v1`（第三轮复审，针对 CLEAN-005 的 Contract R1 修订）
- Base：`6902d2445665ec356a2db22b33b7749d39d8018e`（`develop` = `origin/develop`）
- 本轮被审查变更：
  - `8e84932a1152889e35a5be6997c9721184f796d9`（Contract R1 修订，A1'）
  - `a8250fda8073c223fdb0400bc2ce8f3c0e704144`（A2'：`state.contract.target` → `8e84932`）
  - `e6a55bc`（Design §9 同步 R1 语义）
  - `61fa094ad44a14ea608ffcc773c20689e057e00b`（**C1'' 证据提交**：新增边界回归测试）
  - 排除：`9dba268`（C2 metadata，只改 `state.review.target`）
- 审查对象即 **C1'' = `61fa094`**，与 `state.review.target` 一致；`git status --short` 为空，`HEAD` = `origin/feat/agent-context-manager-v1` = `9dba268`
- 历史审查对象：C1 = `d92ada3`（初查 CHANGES_REQUIRED）、C1' = `bb991c1`（复审 CHANGES_REQUIRED，仅 CLEAN-005 未闭合）
- 生产代码自 `bb991c1` 未再变化（`git diff --stat bb991c1..HEAD` 只有 `manager_test.go` 与文档/状态文件），本轮实质新增为测试与文档收敛
- 环境：Linux，`go version go1.24.1 linux/amd64`，零第三方依赖
- 相关文件（`git ls-files contextmgr`，6 个）：`contextmgr.go`、`estimator.go`、`estimator_test.go`、`manager.go`、`manager_test.go`、`summarizer.go`，与 Design §12 一一对应
- Scope：本轮仅改 `contextmgr/manager_test.go`、`contract.md`、`docs/design/runtime-architecture.md`、`state.yaml`；`core/**`、`llm/**`、`tool/**`、`session/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml` 未动；公开契约（`Request`/`Result`/`Estimator`/`Summarizer`/`NewManager`）在 R1 中未变
- Shared Resource：`resources: {}`，无 Registry 检查项

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestBuildWithinBudgetKeepsFullView`（budget 5/6/100，逐条相等、`Compacted=false`、`DroppedMessages=0`、重复 `DeepEqual`）、`TestBuildNoBudgetLimitWhenTokenBudgetNonPositive`、`TestBuildEmptyHistoryAndExactBudget` |
| AC-002 | PASS | 保留下限为硬约束：`TestBuildKeepsMinimumRecentMessages`（含 3 消息工具单元时保留恰 4 条而非 7 条）、`TestBuildCompactionBudgetBoundaryExactFit`（预算恰好=压缩后估算则成功且保留条数恰为 `KeepRecent` 最小尾部；少 1 token 即失败）、`TestBuildToolUnitsAreAtomic`（`minRetained` 逐档断言）；独立探针 51 个成功用例保留下限 0 违例 |
| AC-003 | PASS | `TestBuildToolUnitsAreAtomic` 矩阵（`core.ValidateMessages` 全过、保留区间为输入后缀且不以 tool 开头、`DroppedMessages` 与实差一致）；探针交错结构（`assistant(c1,c2)→tool(c1)→user→tool(c2)`）`invalid=0`，无孤儿/缺失/重复 Result |
| AC-004 | PASS | `TestBuildSummarizerFailures`（error / 空串 / 全空白）：`internal`、`errors.As(*core.RunError)`、`errors.Is(sentinel)`、零值 `Result`，无占位摘要 |
| AC-005 | PASS | 预算不可满足（含 System Prompt、`KeepRecent` 下限、摘要本身）→ `invalid_argument` + 零值 `Result`（`TestBuildFailsWhenKeepRecentTargetExceedsBudget`、`TestBuildFailsWhenPrefixAndRetainedTailExceedBudget`、`TestBuildRejectsInvalidConfiguration`、`TestBuildCompactionBudgetBoundaryExactFit`）；`ctx` 取消/超时（含摘要期间）→ `cancelled` / `deadline_exceeded`（`TestBuildContextCancellation`、`TestBuildMapsSummarizerContextErrors`） |
| AC-006 | PASS | `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./contextmgr/ -count=1` 均 exit 0；`go list -deps ./contextmgr` 仅标准库 + `core`；全部测试用确定性估算器与包内 Fake，无网络/模型调用 |
| AC-007 | PASS | 四方一致：Task（AC-002/AC-005）↔ Contract R1（关键取舍 4/5、压缩算法 3–6 步、INV-008、失败表、Open Risks，`contract.md:70,88-95,130,138-146,168-172`）↔ Design §9（`runtime-architecture.md:116,122,124`，含 R1 A1' 引用）↔ 实现（`manager.go:89-172`）；§12 登记 6 个文件与实际 `git ls-files` 一致；§14 `TokenBudget` 已改为已验证（精度校准留 T07） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` / `go vet ./...` | PASS | exit 0 |
| `go test ./... -count=1` | PASS | `contextmgr`/`core`/`llm`/`runlog` ok |
| `go test -race ./contextmgr/ -count=1` | PASS | 无数据竞争 |
| `go test ./contextmgr/ -run TestBuildCompactionBudgetBoundaryExactFit -v` | PASS | 2 组 `KeepRecent` × （预算恰好 / 少 1 token）四例全通过 |
| `go list -deps ./contextmgr` | PASS | 仅标准库 + `core`（INV-007） |
| 复跑原 Finding 触发场景（`/tmp/probe`） | PASS | CLEAN-001 场景（`KeepRecent=1` + 超长最新消息，budget 5/20/200）全部 `invalid_argument`、`dropped=0`、`viewLen=0`、`Summarizer` 1 次；CLEAN-002 场景（`Canceled` / `DeadlineExceeded` / 包装错误）映射 `cancelled` / `deadline_exceeded` |
| 交错工具结果探针 | PASS | budget 1~12 × keepRecent 0~6：成功 51 例全部合法、保留下限 0 违例、失败例全为 `invalid_argument` 且零值 `Result` |
| 变异测试（隔离副本 `/tmp/mut3`，未改动 Review Target 工作区） | PASS | M1 `KeepRecent` 改按 Unit 计数 → `TestBuildKeepsMinimumRecentMessages`/`TestBuildToolUnitsAreAtomic`/`TestBuildCompactionBudgetBoundaryExactFit` FAIL；M2 放宽组装后预算校验 → 5 个用例 FAIL；M3 `DroppedMessages` 按单元数虚报 → 3 个用例 FAIL；M4 去掉摘要后 `ctx` 复核 → `TestBuildMapsSummarizerContextErrors` FAIL；M5 Unit 划分退化为逐条消息 → `TestBuildToolUnitsAreAtomic` FAIL；恢复后全绿 |
| Scope / 依赖 / Shared Resource | PASS | 本轮只改测试与文档；无全局资源占用 |

## Findings

### CLEAN-001：预算装不下 `KeepRecent` 条近期消息时静默降级

- Severity：P1
- Status：**CLOSED**（复审两轮验证：`bb991c1` 行为修复 + 本轮 R1 口径收敛）
- Location：`contextmgr/manager.go:89-172`
- 复审结论：实现改为「按 `KeepRecent` 取保留尾部 → 对尾部之前的全部 Unit 单次压缩 → 组装后仍超预算即 `invalid_argument`」；`KeepRecent` 为硬下限（`INV-008`）；CLEAN-001 场景探针与 `TestBuildFailsWhenKeepRecentTargetExceedsBudget`/`TestBuildFailsWhenPrefixAndRetainedTailExceedBudget` 一致；变异 M1/M2 均被测试拒绝。AC-002 / AC-005 通过。

### CLEAN-002：`Summarizer` 因 `ctx` 取消/超时返回的错误被归为 `internal`

- Severity：P2
- Status：**CLOSED**
- Location：`contextmgr/manager.go:110-122`、`:212-228`
- 复审结论：`summarizeError` 先按 `ctx.Err()` 判定，再按 `errors.Is` 识别取消/超时，`Cause` 保留；摘要成功后仍复核 `ctx` 终态；`TestBuildMapsSummarizerContextErrors` 覆盖 5 种形态；变异 M4 被拒绝。

### CLEAN-003：§12 文件所有权表未登记 `contextmgr` 两个 `_test.go`

- Severity：P3
- Status：**CLOSED**
- Location：`docs/design/runtime-architecture.md:170-171`
- 复审结论：已补登记，表内 6 行与 `git ls-files contextmgr` 6 个文件一一对应。

### CLEAN-004：测试未保护「`KeepRecent` 按消息条数」与「保留下限」语义

- Severity：P2
- Status：**CLOSED**
- Location：`contextmgr/manager_test.go:398,462,1012`
- 复审结论：新增/改写用例显式断言保留条数下限、最小尾部与预算临界两侧；变异 M1/M3 现被拒绝，测试可区分原错误实现。

### CLEAN-005：Contract 压缩算法与 Design §9 仍描述已被替换的「逐单元吞并」行为

- Severity：P2
- Status：**CLOSED**
- Location：`contract.md:70,88-95,130,168-172,182`；`docs/design/runtime-architecture.md:116,122,124`
- 复审结论：
  - Contract 走 Owner 批准的 R1 修订（A1' = `8e84932`，A2' = `a8250fd` 重绑 `state.contract.target`），关键取舍 4、压缩算法第 4–5 步、INV-008、失败表（移除「调用上限/无进展 → `internal`」行）、验证要求与 Open Risks 均已收敛为「单次压缩 + `KeepRecent` 硬下限 + 预算不满足即 `invalid_argument`」；公开契约与 D1—D5 未变（R1 记录明确此边界）。
  - Design §9 同步同口径，并把 A1 引用更新为 R1 `8e84932`；§9 不再出现「逐单元吞并 / 多次调用」表述，内部无自相矛盾。
  - 实现（`manager.go:89-172`）与该口径逐条一致：单次 `Summarize`、无尾部吞并循环、无「无进展」分支。
  - `Owner Decision Record` 保留 D4 原文（含「无进展」）属历史决策记录，由 R1 显式覆盖，不构成漂移。

## 残留观察（非 Finding）

- `Summarizer` 单次收到整个被压缩前缀；真实 LLM 摘要器的输入长度控制属 T07 职责（本任务 Scope 未要求分片）。
- `KeepRecent <= 0` 允许把全部历史压缩为摘要（视图仅 System Prompt + 摘要），与「`KeepRecent` 为保留条数」语义一致。
- `HeuristicEstimator` 对 CJK 偏低（4 字符/token），`TokenBudget` 为估算预算，生产装配须注入校准估算器（Design §14 已登记为未验证项）。
