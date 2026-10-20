# Cleaner Findings

## Review Target

- Task：`agent-context-manager-v1`（首次审查 `initial_review`）
- Base：`6902d2445665ec356a2db22b33b7749d39d8018e`（`develop` = `origin/develop`）
- 被审查变更：
  - `82b7475701e9cba1f97564762361bcdd73cf2f06`（Design Artifact：`docs/design/runtime-architecture.md` §3/§9/§12/§14）
  - `d92ada3ed5219ae3a349aa248dde65267bfdc4f9`（**C1 实现**：`contextmgr/**`）
- 排除：`ead4a7d`（C2 metadata commit，只改 `state.yaml` 的 `review.status/target`，无业务代码）
- 审查对象即 **C1 = `d92ada3`**；`git status --short` 为空，`HEAD` = `origin/feat/agent-context-manager-v1` = `ead4a7d`
- 环境：Linux，`go version go1.24.1 linux/amd64`，零第三方依赖，无网络/模型凭据需求
- 相关文件（`git ls-files contextmgr`）：`contextmgr.go`、`estimator.go`、`estimator_test.go`、`manager.go`、`manager_test.go`、`summarizer.go`
- 与基线区分：`6902d24..HEAD` 另含 `.agent/tasks/agent-context-manager-v1/**`（Workflow 记录，非业务代码），无 `core/**`、`llm/**`、`tool/**`、`session/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml` 改动
- Shared Resource：`.agent/workflow.yaml` 的 `resources: {}`，`state.resources.reservations = {}`，实现未占用错误码/迁移版本，三边一致性检查无对象

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestBuildWithinBudgetKeepsFullView`（预算 5/6/100 逐条相等、`Compacted=false`、`DroppedMessages=0`、重复调用 `DeepEqual`）、`TestBuildNoBudgetLimitWhenTokenBudgetNonPositive`、`TestBuildEmptyHistoryAndExactBudget`；独立探针复算 `viewTokens <= TokenBudget` |
| AC-002 | FAIL | 常规压缩路径（`TestBuildCompactsOldHistory`：`DroppedMessages=25`、摘要来自 Fake、尾部逐条相等）成立，但「保留至少 `KeepRecent` 条最近消息」在预算不足时被静默突破 → CLEAN-001 |
| AC-003 | PASS | `TestBuildToolUnitsAreAtomic`（0/1/2 调用 + `keepRecent` 0~9 × `budget` 1~12 矩阵：`core.ValidateMessages` 无错、保留区间为输入后缀且不以 tool 开头、`DroppedMessages` 与实差一致）；独立探针用「`assistant(c1,c2)` → `tool(c1)` → `user` → `tool(c2)`」交错结构复核，未出现孤儿/缺失/重复 Result |
| AC-004 | PASS | `TestBuildSummarizerFailures`（error / 空串 / 全空白 三例）：`KindOf=internal`、`errors.As(*core.RunError)`、`errors.Is(sentinel)`、`Result` 为零值，无占位摘要 |
| AC-005 | FAIL | 配置类错误（负 `KeepRecent`、`Messages` 含 system、`KeepRecent >= 总条数`、预算小于 System Prompt、无更早历史可压缩、估算器负值、nil 依赖、ctx 取消/超时）均按契约失败；但「预算不足以容纳最小可保留原子单元」时返回成功视图而非显式错误 → CLEAN-001；`ctx` 在 `Summarizer` 内部取消被归为 `internal` → CLEAN-002 |
| AC-006 | PASS | `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./contextmgr/ -count=1` 全部 exit 0；`go list -deps ./contextmgr` 仅标准库 + `cnb.cool/framework-station/mini-agent-go/core`；全部测试使用确定性估算器与包内 Fake，无网络/模型调用 |
| AC-007 | FAIL | §9 公开契约与失败语义、§14 `TokenBudget` 已改判为已验证，与 Contract/实现一致；但 §12 文件所有权表只登记 `estimator.go`/`summarizer.go`/`manager.go`，未登记实际存在的 `estimator_test.go`、`manager_test.go`，违反 §12 自身规则（表内路径须与 `git ls-files` 一致）与 AC-007 的「及对应 `_test.go`」→ CLEAN-003 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test ./... -count=1` | PASS | `contextmgr`/`core`/`llm`/`runlog` ok，其余无测试文件 |
| `go test -race ./contextmgr/ -count=1` | PASS | 无数据竞争（实现无共享状态，`Build` 不写调用方切片：`TestBuildDoesNotMutateInput`） |
| `go list -deps ./contextmgr` | PASS | 仅标准库 + `core`（INV-007） |
| 独立行为探针（隔离目录 `/tmp/probe`，`replace` 引用本地模块） | FAIL | 复现 KeepRecent 保留被突破（`roles=[system system]`，无任何近期消息）；复现 `Summarizer` 内部 `context.Canceled`/`DeadlineExceeded` 被归为 `internal` |
| 变异测试（隔离副本 `/tmp/mut`，非 Review Target 工作区） | FAIL | 把 `tailUnitIndex` 从「按消息条数」改为「按 Unit 个数」后 `go test ./contextmgr/` 仍全部通过 → 关键保留语义无测试保护（CLEAN-004） |
| Registry ↔ Contract ↔ 实现三边检查 | N/A | 本任务无 Shared Resource Kind，Contract 无资源清单，实现未使用错误码/迁移版本 |
| Scope 检查 | PASS | C1 只改 `contextmgr/**`；Design 更新只落在 §3/§9/§12/§14 |

## Findings

### CLEAN-001：预算装不下 `KeepRecent` 条近期消息时静默降级，`Build` 返回可能不含任何近期消息的"成功"视图

- Severity：P1
- Status：OPEN
- Location：`contextmgr/manager.go:100-179`（吞并循环，尤其 `105`、`144-178`）；`contextmgr/manager.go:21-22`（`Request.KeepRecent` 注释）；固化错误行为的测试 `contextmgr/manager_test.go:319-354`、`contextmgr/manager_test.go:376-380`
- AC / Invariant：AC-002「保留至少 `KeepRecent` 条最近消息」；AC-005「预算无法满足 → 可区分原因的显式错误」；Contract 关键取舍 #5 与 D3「`KeepRecent` 超预算时明确失败，不做放宽保留的降级」；`docs/design/runtime-architecture.md:122`「保留不少于 `KeepRecent` 条的近期消息」
- Trigger：历史超预算，且把保留尾部收缩到 `KeepRecent` 条后视图仍超预算（例如最新一条 user 消息本身接近或超过预算）。
- Actual：`Build` 继续把保留尾部中最老的 Unit 并入压缩集合（`compactEnd++`），直到尾部被整体吞并，然后返回 `Compacted=true` 的成功结果。独立探针（`textEstimator` 按 rune 计 token）：`Request{SystemPrompt:"sys", Messages:[user"旧问", assistant"旧答", user(200×"长")], TokenBudget:5, KeepRecent:1}` → `err=nil, Compacted=true, DroppedMessages=3, roles=[system system]`。另一组矩阵（6 条消息含工具单元）在 `budget=2, keep=1..5` 时同样返回 `[system system]` 且 `DroppedMessages=6`。
- Expected：`KeepRecent` 是保留下限，不得因预算被突破。当 `SystemPrompt` + 新摘要 + 至少 `KeepRecent` 条近期消息已超预算时，返回 `invalid_argument`（预算不可满足），`Result` 为零值，调用方据此不改动持久化状态。
- Impact：调用方按 Contract 持久化配方会「以新摘要替换旧摘要 + 删除前 `DroppedMessages` 条消息」——在本场景下会把**当前这一轮的 user 提问也从会话历史中删除**，同时构造出不含任何 user 消息的 Provider 请求（模型没有可回答的当期输入）。这属于任务 Goal 明确禁止的「不产生虚假的完整记忆 / 不静默丢弃历史后报告成功」，且与 Owner D3 的取舍相反。
- Evidence：隔离探针输出（见 Verification 行）；`contextmgr/manager.go:174-178` 只在「全部 Unit 已压缩」时才报 `internal`，此前每轮都在无提示地减少保留条数；`manager_test.go:344-350` 把「`KeepRecent=3` 却只保留 1 条」断言为期望行为，`manager_test.go:380` 的 `wantSuccess` 公式进一步把「预算只够前缀时必须成功」写成断言。
- Required Fix Boundary：恢复「保留区间消息条数 ≥ `KeepRecent`」这一下限：尾部不得因预算被压缩到低于 `KeepRecent` 条；无法同时容纳 `SystemPrompt` + 新摘要 + `KeepRecent` 条近期消息时必须以 `invalid_argument` 显式失败（不得返回 `Compacted=true` 的部分记忆）。同步修正 `manager_test.go` 中固化降级行为的两处断言，并补充「预算只够前缀、保留目标无法满足 → 报错且 `Result` 零值」的回归用例。注意：Contract「压缩算法」第 5 步（`contract.md:94`）与 Design §9「逐单元吞并直至满足预算」的字面表述与 D3/AC-002 冲突，该措辞需由 Analyst 在本轮修复中收敛（Cleaner 不改 `contract.md`／`docs/design/**`）。

### CLEAN-002：`Summarizer` 因 `ctx` 取消/超时返回的错误被统一归为 `internal`

- Severity：P2
- Status：OPEN
- Location：`contextmgr/manager.go:110-127`
- AC / Invariant：AC-005 / Contract 失败语义表「`ctx` 取消 / 超时 → `cancelled` / `deadline_exceeded`，不透传为 `internal`」；`docs/design/runtime-architecture.md:124`
- Trigger：`ctx` 在 `Summarizer.Summarize` 内部被取消或超时，`Summarize` 返回 `context.Canceled` / `context.DeadlineExceeded`（T07 接入真实 LLM 摘要后最常见的中断形态）。
- Actual：`Build` 只在循环入口检查 `ctx.Err()`；一旦 `Summarize` 返回错误，一律包装为 `&core.RunError{Kind: core.KindInternal, Cause: err}`。探针：`summarizer` 返回 `context.Canceled` → `KindOf(err) = "internal"`；返回 `context.DeadlineExceeded` → 同样 `"internal"`。
- Expected：`errors.Is(err, context.Canceled)` / `DeadlineExceeded` 时分别映射为 `cancelled` / `deadline_exceeded`（`Cause` 保留原始错误），不得降级为 `internal`。
- Impact：调用方按 `Kind` 决定 Run 终态（`core.StatusOf`：`cancelled`/`deadline_exceeded` → `CANCELLED`，其余 → `FAILED`）。当前实现会把用户取消或整体超时记录为 `FAILED`，使 Loop 层无法区分「用户取消」与「内部故障」，并可能触发错误的重试/告警语义。
- Evidence：`manager.go:120-127`（无 `ctx.Err()` 二次判定）；探针输出 `[B] kind="internal"`、`[C] kind="internal"`；`manager_test.go:670-695` 只覆盖「调用前 ctx 已取消/已超时」，未覆盖「调用期间取消」。
- Required Fix Boundary：`Summarize` 返回错误时先按 `ctx.Err()` 判定取消/超时并映射为对应 `Kind`，仅真正的内部错误保留 `internal`；补一条「`Summarizer` 内部返回 `context.Canceled` → `cancelled`」的回归测试。

### CLEAN-003：`runtime-architecture.md` §12 文件所有权表未登记 `contextmgr` 的两个 `_test.go`（AC-007 漂移）

- Severity：P3
- Status：OPEN
- Location：`docs/design/runtime-architecture.md:166-169`（表格）、`:176`（§12 自设规则）
- AC / Invariant：AC-007「§12 文件所有权表登记 `contextmgr` 新增源文件（… 及对应 `_test.go`，以实际文件为准）」；§12 规则「表内路径与实际 `git ls-files` 不一致视为失败」
- Trigger：比对 `git ls-files contextmgr` 与 §12 表格。
- Actual：表格登记 `contextmgr.go`/`estimator.go`/`summarizer.go`/`manager.go`；实际还有 `estimator_test.go`、`manager_test.go` 未登记（表内已有 `core/*_test.go`、`runlog_test.go` 先例，说明该表惯例包含测试文件）。
- Expected：§12 登记这两个测试文件及其职责说明，表内路径与实际文件一一对应。
- Impact：长期设计事实与实际文件漂移，后续任务按 §12 判断文件归属时会漏判 `contextmgr` 测试文件所有权；不影响运行期行为。
- Evidence：`git ls-files contextmgr`（6 个文件）vs §12 表格（4 行 contextmgr）；§12 自身把该不一致定义为失败条件。
- Required Fix Boundary：由 Analyst（Contract Allowed Changes 限定 `docs/design/**` 由 Analyst 更新）在 §12 补登记 `contextmgr/estimator_test.go`、`contextmgr/manager_test.go`；不得顺带改动其他模块的登记。

### CLEAN-004：测试未保护「`KeepRecent` 按消息条数」与「保留下限」语义，错误实现仍可通过

- Severity：P2
- Status：OPEN
- Location：`contextmgr/manager_test.go`（`TestBuildCompactsOldHistory:246-315`、`TestBuildShrinksTailUntilBudgetFits:319-354`、`TestBuildToolUnitsAreAtomic:358-434`）
- AC / Invariant：AC-002（保留至少 `KeepRecent` 条，`KeepRecent` 按消息条数）、INV-002、INV-005
- Trigger：把 `contextmgr/manager.go` 的 `tailUnitIndex` 从「累加 `len(units[i].msgs)`」改为「累加 Unit 个数」（即把 `KeepRecent` 误解为单元数）。
- Actual：隔离副本变异后 `go test ./contextmgr/ -count=1` 仍 `ok`（全部用例通过）。原因：唯一带多消息 Unit 的 `TestBuildToolUnitsAreAtomic` 只断言「保留区间是输入后缀 + 不以 tool 开头」，从不断言保留条数 ≥ `KeepRecent`；其余用例的 Unit 均为单条消息，条数与单元数恰好相等。同理，若把保留尾部压缩到 0 条（CLEAN-001 的现状），现有断言依然成立。
- Expected：至少一条用例在多消息 Unit（含工具组合）与正 `KeepRecent` 下断言「保留消息条数 ≥ `KeepRecent`」与「`DroppedMessages` = 实差」，使「按单元计数」或「无限吞并尾部」的错误实现必然失败。
- Impact：AC-002 的核心保留下限没有任何能区分正确/错误实现的测试；CLEAN-001 这类行为偏差因此未被现有测试发现，反而被测试固定为期望。
- Evidence：`/tmp/mut`（Review Target 的隔离副本，未改动被审查工作区）应用变异后 `go test ./contextmgr/ -count=1` → `ok`；原实现同样通过。
- Required Fix Boundary：随 CLEAN-001 一并补充保留条数断言（多消息 Unit + 正 `KeepRecent`，含工具组合），使「按 Unit 计数」与「尾部吞并至空」两种错误实现分别被测试拒绝。
