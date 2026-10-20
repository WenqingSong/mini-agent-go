# Cleaner Findings

## Review Target

- Task：`agent-context-manager-v1`（复审 `re-review`，针对初审查结论 CHANGES_REQUIRED 的修复）
- Base：`6902d2445665ec356a2db22b33b7749d39d8018e`（`develop` = `origin/develop`）
- 复审对象（相对初审查的新变更）：
  - `364c766c7ce0e7abafb6b3c4fccf560d5823112b`（Analyst：`docs/design/runtime-architecture.md` §12 补登记，修复 CLEAN-003）
  - `bb991c1a6a2e0eb9c8c0cb8daf00c02b79f31ab8`（**C1' 实现修复**：`contextmgr/**`，修复 CLEAN-001/002/004）
  - 排除：`2735db9`（Cleaner 初查 findings，review-neutral）、`c1af2ad`（C2 metadata，只改 `state.yaml`）
- 被审查对象即 **C1' = `bb991c1`**，与 `state.review.target` 一致；`git status --short` 为空，`HEAD` = `origin/feat/agent-context-manager-v1` = `c1af2ad`
- 初审查对象 C1 = `d92ada3`（结论见 Git 历史 `2735db9` 的 findings 版本）
- 环境：Linux，`go version go1.24.1 linux/amd64`，零第三方依赖，无网络/模型凭据需求
- 复审时相关文件：`contextmgr/{contextmgr,estimator,manager,summarizer}.go` + `{estimator,manager}_test.go`；`docs/design/runtime-architecture.md`；`.agent/tasks/agent-context-manager-v1/contract.md`（未变，仍绑定 A1 `624a094`）
- Scope：C1' 只改 `contextmgr/**`；`core/**`、`llm/**`、`tool/**`、`session/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml` 未动
- Shared Resource：`resources: {}`，无 Registry 检查项

## Result

CHANGES_REQUIRED

行为与测试缺陷（CLEAN-001/002/004）与设计文档缺失（CLEAN-003）均已修复并经独立验证；但 CLEAN-001 修复边界中要求收敛的 Contract / Design 压缩算法措辞未落地，四者一致性仍不成立（CLEAN-005）。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestBuildWithinBudgetKeepsFullView`、`TestBuildNoBudgetLimitWhenTokenBudgetNonPositive`、`TestBuildEmptyHistoryAndExactBudget` 通过；探针复算未超预算时逐条相等、`Compacted=false`、`DroppedMessages=0` |
| AC-002 | PASS | 修复后保留区间为 `concatUnits(units[tailStart:])`，条数 ≥ `KeepRecent`；`TestBuildKeepsMinimumRecentMessages`（`KeepRecent=4` 且含 3 消息工具单元：保留恰 4 条而非 7 条）、`TestBuildFailsWhenKeepRecentTargetExceedsBudget`、`TestBuildToolUnitsAreAtomic` 矩阵（`minRetained` 逐档断言保留条数与 `Compacted`）；探针交错结构 51 个成功用例 `keep-violations=0` |
| AC-003 | PASS | 同上矩阵：`core.ValidateMessages` 全过、保留区间为输入后缀且不以 tool 开头、`DroppedMessages` 与实差一致；探针 `assistant(c1,c2)→tool(c1)→user→tool(c2)` 交错结构 `invalid=0`、无尾部 tool 孤儿 |
| AC-004 | PASS | `TestBuildSummarizerFailures`（error / 空串 / 全空白）仍通过：`internal`、`errors.As(*core.RunError)`、`errors.Is(sentinel)`、零值 `Result`，无占位摘要 |
| AC-005 | PASS | 预算不可满足（含「装不下 System Prompt + 摘要 + `KeepRecent` 条」与「无更早历史可压缩」）现一律 `invalid_argument` 且零值 `Result`（探针复现原失败场景全部转为显式错误）；`ctx` 在 `Summarizer` 内取消/超时映射为 `cancelled` / `deadline_exceeded`（`TestBuildMapsSummarizerContextErrors`） |
| AC-006 | PASS | `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./contextmgr/ -count=1` 均 exit 0；`go list -deps ./contextmgr` 仅标准库 + `core`；测试无网络/模型调用 |
| AC-007 | FAIL | §12 已补登记两个 `_test.go`（CLEAN-003 修复）；§9 公开契约、失败语义、§14 `TokenBudget` 已验证均与 Contract/实现一致；但 §9 压缩语义与 Contract 压缩算法第 5 步仍描述「逐单元吞并直至满足预算 / 多次调用 Summarizer」，与修复后实现（单次压缩 + 预算不满足即失败）不一致 → CLEAN-005 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` / `go vet ./...` | PASS | exit 0 |
| `go test ./... -count=1` | PASS | `contextmgr`/`core`/`llm`/`runlog` ok |
| `go test -race ./contextmgr/ -count=1` | PASS | 无数据竞争 |
| `go list -deps ./contextmgr` | PASS | 仅标准库 + `core` |
| 复跑初审查探针（`/tmp/probe`，`replace` 引用本地模块） | PASS | 原 CLEAN-001 场景（`KeepRecent=1` + 超长最新消息，budget 5/20/200）现返回 `invalid_argument: ...KeepRecent=1 条近期消息（保留 1 条）合计超出预算`，零值 `Result`，`Summarizer` 调用 1 次；原 CLEAN-002 场景（`context.Canceled` / `DeadlineExceeded` / 包装错误）分别映射 `cancelled` / `deadline_exceeded` 且 `Cause` 保留 |
| 交错工具结果结构探针 | PASS | 6 条消息（2 调用被 user 消息分隔）× budget 1~12 × keepRecent 0~6：成功 51 例全部合法、保留下限 0 违例，失败例全部 `invalid_argument` 且零值 `Result` |
| 变异测试（隔离副本 `/tmp/mut2`，未改动 Review Target 工作区） | PASS | M1 `tailUnitIndex` 改按 Unit 计数 → 测试失败（`TestBuildToolUnitsAreAtomic`、`TestBuildKeepsMinimumRecentMessages`）；M2 移除组装后预算校验 → 失败（`TestBuildFailsWhenPrefixAndRetainedTailExceedBudget`、矩阵）；M3 取消/超时降级为 `internal` → 失败（`TestBuildMapsSummarizerContextErrors`）；M4 去掉摘要后 ctx 复核 → 失败（同上，「成功但期间 ctx 已被取消」用例） |
| Scope / 依赖 / Shared Resource | PASS | C1' 只改 `contextmgr/**`；无全局资源占用 |

## Findings

### CLEAN-001：预算装不下 `KeepRecent` 条近期消息时静默降级

- Severity：P1
- Status：**CLOSED**（复审验证）
- Location：`contextmgr/manager.go:102-155`（修复后）
- 复审结论：吞并循环已移除，改为「一次压缩 `units[:tailStart]` + 保留尾部；仍超预算即 `invalid_argument`」；探针复现原触发条件全部转为显式错误且 `Result` 零值、`Summarizer` 仅调用 1 次；回归测试 `TestBuildFailsWhenKeepRecentTargetExceedsBudget`、`TestBuildFailsWhenPrefixAndRetainedTailExceedBudget`、`TestBuildKeepsMinimumRecentMessages` 覆盖原失败语义，变异 M1/M2 均被测试捕获。AC-002 / AC-005 相应通过。

### CLEAN-002：`Summarizer` 因 `ctx` 取消/超时返回的错误被归为 `internal`

- Severity：P2
- Status：**CLOSED**（复审验证）
- Location：`contextmgr/manager.go:110-122`（调用与摘要后复核）、`contextmgr/manager.go:212-228`（`summarizeError`）
- 复审结论：`summarizeError` 先按 `ctx.Err()` 判定，再按 `errors.Is` 识别 `context.Canceled` / `DeadlineExceeded`，`Cause` 始终保留；摘要成功后仍复核 `ctx` 终态，取消时不返回成功视图。`TestBuildMapsSummarizerContextErrors` 覆盖 5 种形态（含包装错误、内部取消后返回普通错误、成功但期间取消、普通错误仍为 `internal`）；变异 M3/M4 均被捕获。探针复核三种错误形态种类正确。

### CLEAN-003：§12 文件所有权表未登记 `contextmgr` 两个 `_test.go`

- Severity：P3
- Status：**CLOSED**（复审验证）
- Location：`docs/design/runtime-architecture.md:170-171`
- 复审结论：Analyst 在 `364c766` 补登记 `contextmgr/estimator_test.go`、`contextmgr/manager_test.go`；现与 `git ls-files contextmgr` 一一对应（6 个文件）。

### CLEAN-004：测试未保护「`KeepRecent` 按消息条数」与「保留下限」语义

- Severity：P2
- Status：**CLOSED**（复审验证）
- Location：`contextmgr/manager_test.go`（`TestBuildKeepsMinimumRecentMessages`、`TestBuildFailsWhenKeepRecentTargetExceedsBudget`、`TestBuildToolUnitsAreAtomic` 的 `minRetained` 断言）
- 复审结论：新增/改写用例显式断言「保留条数恰为满足下限的最小尾部」「低于下限即失败」；复审变异 M1（按单元计数）现被测试拒绝。测试已能区分原错误实现。

### CLEAN-005：Contract 压缩算法与 Design §9 压缩语义仍描述已被替换的「逐单元吞并」行为

- Severity：P2
- Status：OPEN
- Location：`.agent/tasks/agent-context-manager-v1/contract.md:94`（压缩算法第 5 步）、`:170`（Open Risks 第二条）；`docs/design/runtime-architecture.md:122`（§9 压缩语义）
- AC / Invariant：AC-007（Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ Implementation 四者一致）；协作规范 §4「出现不一致必须回写，不允许长期漂移」；CLEAN-001 的 Required Fix Boundary（已要求本轮收敛该措辞）
- Trigger：CLEAN-001 修复后实现改为「单次压缩 `units[:tailStart]`，视图仍超预算即 `invalid_argument`，不降级」；而 `contract.md` 自 A1 `624a094` 未变，Design §9 亦未同步。
- Actual：`contract.md:94` 仍写「仍超预算 → 把保留尾部中最老的一个 Unit 并入压缩集合，重新调用 `Summarizer`，重复至满足预算。单次 `Build` 的 `Summarizer` 调用上限为 `unitCount + 1`……达到上限或无法取得进展 → `internal`」；`contract.md:170` 仍写「逐 Unit 吞并可能多次调用 `Summarizer`」；`docs/design/runtime-architecture.md:122` 同一句内并存「保留不少于 `KeepRecent` 条的近期消息，逐单元吞并直至满足预算」——自身矛盾，且与实现（`manager.go:102-155`，一次调用 + 显式失败）不符。
- Expected：Contract 压缩算法 3–5 步、Open Risks 与 Design §9 压缩语义收敛为实现级语义：一次压缩前缀单元、`Summarizer` 单次调用、预算（含 `KeepRecent` 保留下限）不满足即 `invalid_argument`，不引入降级路径；D1—D5 决定与公开契约不变。
- Impact：权威设计文本自相矛盾且与代码不符，下游 T01（装配）与 T07（真实 Summarizer + `RunPolicy` 记账）会按「多次调用 Summarizer、预算压力下降级保留」做出错误假设；构成协作规范禁止的长期漂移。
- Evidence：`git log --oneline -3 -- .agent/tasks/agent-context-manager-v1/contract.md` 仅有 `624a094`；`git --no-pager show bb991c1` 未触及 `contract.md` 与 §9；实现 `manager.go:102-155`（无循环、单次 `Summarize`、`151-155` 显式 `invalid_argument`）。
- Required Fix Boundary：由 Analyst 收敛措辞（`contract.md` 与 `docs/design/**` 均属 Analyst 维护）。`contract.md` 已 `APPROVED` 且 `state.contract.target` 绑定 A1 `624a094`，措辞修订须走 Contract Revision 并交 Owner 决定（新 A1/A2），不得由 Coder 或 Cleaner 代改；Design §9 在同一轮同步。修订只允许收敛压缩算法与风险描述，不得借机改动公开契约、D1—D5 决定或 `TokenBudget` 语义。

## 复审补充观察（非 Finding）

- 修复后 `Summarizer` 单次收到整个被压缩前缀（`concatUnits(units[:tailStart])`）。对 T07 的真实 LLM 摘要器，超长前缀的自身上下文溢出需由该任务处理（分块/截断策略），本任务 Scope 未要求 `contextmgr` 分片，不构成 Finding。
- `KeepRecent <= 0` 时允许把全部历史压缩为摘要（视图仅 System Prompt + 摘要），这与 Contract「`KeepRecent` 为保留条数」语义一致，非缺陷。
