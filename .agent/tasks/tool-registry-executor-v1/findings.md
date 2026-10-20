# Cleaner Findings

## Review Target

- Task：`tool-registry-executor-v1`（Complexity = COMPLEX，`Design Impact = UPDATE`）
- 基线：`6902d24`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论`），任务开始时 `git status --short` 为空、无未跟踪文件。
- Contract：`APPROVED`，Evidence Commit A1 = `95e10b2`（`state.contract.target`）。
- 本轮被审查实现对象（immutable Evidence Commit C1'）：`03dced5177ba7db864fe6c553a9ca4ce2e7c6136`（`fix(tool): 修复 CLEAN-001 约束静默忽略与 CLEAN-002 取消优先守卫不可测`）。
- Coder metadata commit C2'：`7ad44b7`，只改 `state.yaml`（`review.target` 指向 C1'），无业务内容。
- Cleaner review-neutral tail：本轮 `findings.md` / `core-logic.md` / `state.review` 提交不改动 `review.target`，仍绑定 C1'。
- 工作区状态：`git status --short` 为空；`git rev-list --left-right --count origin/tool-registry-executor-v1...HEAD` = `0 0`。
- 累计变更集（`6902d24..03dced5`）：`tool/{tool.go,registry.go,validate.go,executor.go}`、`tool/{registry_test.go,validate_test.go,executor_test.go,fake_test.go}`、`docs/design/runtime-architecture.md`（§7 / §11 / §12 / §14）、`.agent/tasks/tool-registry-executor-v1/*`。
- 设计落库路径：Analyst `a06dbcc`（§7 / §11 / §14）→ A2 `c6b9e66` → C1 `707cb71`（§12 路径登记 + 实现）→ C1' `03dced5`（修复，未新增文件、未改设计）。
- 任务前已有修改：无。所有差异均属本任务，无需区分他人遗留改动。
- 全局资源：`.agent/workflow.yaml` 的 `resources: {}`；`state.resources.reservations` 为空。本任务未使用任何 Registry 资源，三边一致性检查不适用（无 Registry 文件可对）。
- 环境：Linux + go1.24.1，无网络、无外部服务。

## Result

CLEAN（复审结论；保留 CLEAN-003 为 P3，交 Owner 决定）

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `tool/registry_test.go:15` `TestMapRegistryRegisterLookupAndSpecs`（`Lookup` 返回同一 `Tool` 实例、`Specs()` 的 Name/Description/Parameters 逐字节一致、大小写与空白不归一化）；`tool/executor_test.go:26` `TestExecutorSuccess`（工具自填 CallID/Name 被归一化为请求值、`IsError=false`、`ToolResult.Validate()==nil`）；`go list` 输出仅标准库 + `.../core`，`tool` 未 import `llm` |
| AC-002 | PASS（复审修复后） | 六类拒绝路径覆盖于 `TestMapRegistryRejectsInvalidRegistration` / `TestExecutorRejectsInvalidRequest` / `TestExecutorRejectsBeforeToolExecution` / `TestMapRegistryLookupMissing`（Fake Tool 调用计数均为 0）；CLEAN-001 修复后新增 `TestValidateArgumentsEnforceConstraintsWithoutType`（`tool/validate_test.go:285`）与 `TestExecutorRejectsNestedConstraintsWithoutType`（`tool/executor_test.go:497`），均含「合法参数放行、调用计数 1」对照；回归旧实现两用例均 FAIL（见 Verification） |
| AC-003 | PASS | `TestExecutorToolTimeout`（20ms 工具级超时 → `IsError=true`）、`TestExecutorParentDeadlineDuringExecution`（`deadline_exceeded`）、`TestExecutorCancelDuringExecution`、`TestExecutorCancelBeatsToolResult`（`tool/executor_test.go:563`，200 次循环，CLEAN-002 修复新增）、`TestExecutorToolPanic`（`tool_failure`、无 `goroutine`/`stack` 痕迹、保留 CallID）、`TestExecutorTimeoutDoesNotGrowGoroutines`（30 次超时后 `waitUntil` 收敛）；执行器不产生 `RunStatus`（代码核对：`tool` 包无 `RunStatus` 引用） |
| AC-004 | PASS | `TestExecutorDedupWithinRun`（同 Run 同 CallID 第二次 `invalid_pairing`、不复用结果、总计数 1）、`TestExecutorDedupScopeIsPerRun`（不同 Run / 不同 CallID 计数 3）、`TestExecutorDedupConcurrent`（16 goroutine 同键：1 成功 + 15 `invalid_pairing`，计数 1）、`TestExecutorDedupAppliesToFailedFirstCall`（首次失败同样占位） |
| AC-005 | PASS | 见 Verification：`make test` / `go vet ./...` / `go test -race ./tool/...` / `go test -race -count=2` 全部通过；`git diff 707cb71 -- go.mod go.sum` 为空；`go list` 仅标准库 + `<module>/core` |
| AC-006 | PASS | §7 四条新增语义行存在且终态/`ErrorKind` 与 Contract 失败语义表一致、显式标注「由 Agent Loop 判定」；§11 `INV-009`—`INV-013` 证据列指向真实测试文件；§14 已从 Agent Loop 名下收回「工具失败可恢复性分类」并保留「归属收窄」说明；§12 与 `git ls-files tool/`（`tool/tool.go` + 7 新增）差集为空，C1' 未新增/重命名文件；设计 diff 只落在 §7/§11/§12/§14 |

## Verification

本轮（复审）实际执行的命令、环境与结果。所有 Mutation 与探针均在临时隔离副本（`git worktree add --detach`）内完成，未触碰 review target 工作区，结束已 `worktree remove`。

| Check | Result | Evidence / Reason |
|---|---|---|
| `make test`（go1.24.1） | PASS | `core/llm/runlog/tool` 全 ok |
| `go vet ./...` | PASS | exit 0 |
| `go test -race ./tool/...` | PASS | ok |
| `go test -race -count=2 ./tool/...` | PASS | ok 1.265s（时间相关用例 2 轮稳定） |
| `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` | PASS | 仅 std + `<module>/core`，无 `llm`、无第三方 |
| `git diff 707cb71 -- go.mod go.sum` | PASS | 空 |
| `git ls-files tool/` ↔ 设计 §12 表 | PASS | 8 个文件与上轮一致，差集为空 |
| 复审修复 Diff（`707cb71..03dced5`，`tool/` + `docs/`） | PASS | 只含 `validate.go`（按值形态施加约束）、`executor.go`（`context.AfterFunc` 转投 + 注释）、两个测试文件新增用例；`docs/` 无改动 |
| M-CLEAN001-revert：`validateValue` 恢复为按 `node.typ` 分支的旧实现 | DETECTED | `TestValidateArgumentsEnforceConstraintsWithoutType` 3 个子用例 FAIL；`TestExecutorRejectsNestedConstraintsWithoutType` FAIL |
| M4：删除 `case out := <-done` 内的「取消优先」守卫 | DETECTED | `TestExecutorCancelBeatsToolResult` 首轮即 FAIL（`-count=3` 均 FAIL）；旧 `TestExecutorCancelDuringExecution` 仍 PASS（证明新增用例才是有效保护） |
| M-dedup：去重键去掉 `runID` | DETECTED | `TestExecutorDedupScopeIsPerRun` FAIL |
| M-claim-order：去重占位移到 `Lookup` 之后 | DETECTED | `TestExecutorDedupAppliesToFailedFirstCall` FAIL |
| M-unbuffered：`done` 改为无缓冲 channel | DETECTED | `TestExecutorTimeoutDoesNotGrowGoroutines` FAIL（3s 收敛窗口不满足） |
| 探针：修复后约束语义回归（9 组 Schema/参数） | PASS | 未声明 `type` 的子 Schema：对象值仍受 `required`/`type`/`additionalProperties` 约束、字符串值按 `type` 可选放行、`null` 放行；声明 `type=object` 而值是字符串仍拒绝；数组 `items`、根 `additionalProperties:false`、无 `type` 节点的 `enum` 均正常 |
| 探针：取消时延与 goroutine 收敛（AfterFunc 转投） | PASS | 取消后约 0.3ms 返回（总 20.3ms 含前置 sleep）；200 次连续成功执行后 `NumGoroutine()` 在 3s 窗口内回落到 `baseline+8` 内 |
| 探针：超时后放弃 goroutine 不影响同键后续调用 | PASS | 超时返回 `IsError=true`；同 Run 同 CallID 再提交返回 `invalid_pairing` |

### 复审对照（原 Finding → 修复 → 验证）

- CLEAN-001（P2，OPEN → CLOSED）：修复为「按参数值实际形态施加约束」——`validateValue` 拆出 `validateObject` / `validateArray`（`tool/validate.go:221-276`），`required` / `properties` / `additionalProperties` 在值为对象时生效、`items` 在值为数组时生效，不再依赖子 Schema 是否声明 `type`。复核：原触发条件（`{"address":{"zip":1,"city":"x"}}`）现返回 `IsError=true` 且 `$.address.city: 未声明的字段`、工具调用计数 0；合法参数 `{"address":{"zip":"100"}}` 放行且计数 1；把实现改回旧逻辑两个新用例均 FAIL。新风险排查：探针 9 组无回归，无「把合法参数误判为非法」的反例；契约 §2 调用期规则逐条仍成立。
- CLEAN-002（P2，OPEN → CLOSED）：修复为两处——`run` 用 `context.AfterFunc` 把父 Context 结束信号转投到独立 `abort` 通道（避免 `close(ctx.Done())` 直接抢占正在 `select` 的调用方，使「结果已产出、父 Context 已结束」分支可被执行），并新增 `TestExecutorCancelBeatsToolResult`：工具在返回前自行 `cancel()`，200 次循环断言必得 `cancelled` 且不得返回 `ToolResult`。复核：删除 `case out := <-done` 内的 `ctx.Err()` 守卫后该用例首轮即 FAIL（`-count=3` 均 FAIL），原 `TestExecutorCancelDuringExecution` 无法区分；`AfterFunc` 的 goroutine 与停止注册由 `defer stopWatch()` 回收，探针确认取消时延与 goroutine 收敛正常。
- CLEAN-003（P3，OPEN，未修复）：`tool/executor.go:107` 仍以 `tool.Spec().Parameters`（工具实时 Spec）做调用期校验，而非 Registry 注册快照；每次调用重新编译 Schema。属信任边界内的防御深度问题，需工具自身在注册后改 Spec 才触发，不影响本任务任何 AC。交 Owner 决定是否后续处理。

## Findings

### CLEAN-001：嵌套子 Schema 未声明 `type` 时，`required` / `properties` / `additionalProperties` 约束被静默忽略，非法参数透传给工具实现

- Severity：P2
- Status：CLOSED（复审验证）
- Location（修复后）：`tool/validate.go:217-276`（`validateValue` / `validateObject` / `validateArray`）
- AC / Invariant：AC-002；Contract `Owner Decision D4`「不允许静默忽略约束」；INV-102；Design §11 INV-011
- Trigger（原）：注册 `{"type":"object","properties":{"address":{"properties":{"zip":{"type":"string"}},"required":["zip"],"additionalProperties":false}}}`，提交 `{"address":{"zip":1,"city":"x"}}`
- Actual（原）：注册被接受；执行期 `switch node.typ` 对 `typ == ""` 无分支，子树约束全部跳过；`Tool.Execute` 被调用 1 次且 `IsError=false`
- Expected：声明的约束不被静默丢弃
- Fix（C1'）：按参数值形态施加约束，子 Schema 未声明 `type` 时对象/数组约束仍生效
- Evidence（修复后）：`tool/validate_test.go:285` `TestValidateArgumentsEnforceConstraintsWithoutType`、`tool/executor_test.go:497` `TestExecutorRejectsNestedConstraintsWithoutType`（含合法参数放行与调用计数对照）；M-CLEAN001-revert 两用例 FAIL；复审探针 9 组无回归
- 关闭理由：原触发条件已恢复预期行为；新增回归测试可区分修复前后；未发现新引入的误拒或漏放行

### CLEAN-002：「取消优先于工具结果」守卫缺少能识别错误实现的测试

- Severity：P2
- Status：CLOSED（复审验证）
- Location（修复后）：`tool/executor.go:163-203`（`context.AfterFunc` 转投 + `select` + `case out := <-done` 内守卫）；测试 `tool/executor_test.go:563` `TestExecutorCancelBeatsToolResult`
- AC / Invariant：AC-003「超时与取消不会被当成成功结果」；Contract §4「取消优先于工具结果」；INV-106；Design §11 INV-010
- Trigger（原）：删除 `case out := <-done` 内的 `ctx.Err()` 守卫后运行 `go test ./tool/`
- Actual（原）：`TestExecutorCancelDuringExecution` 20 轮全 PASS，该不变量无有效保护
- Expected：存在删除守卫后必失败的用例
- Fix（C1'）：`AfterFunc` 转投使「结果已产出、父 Context 已结束」分支可被确定性执行；新增 200 次循环用例
- Evidence（修复后）：M4 变异（删除守卫）→ `TestExecutorCancelBeatsToolResult` 首轮 FAIL、`-count=3` 均 FAIL；取消时延与 goroutine 收敛探针 PASS
- 关闭理由：该守卫现已被可识别错误实现的测试保护；`AfterFunc` 引入的 goroutine 与定时器回收经探针确认无泄漏

### CLEAN-003：执行期参数校验读取工具实时 `Spec()`，绕开 Registry 注册时的已校验快照

- Severity：P3
- Status：OPEN（未修复，交 Owner 决定）
- Location：`tool/executor.go:107`（`validateArguments(name, tool.Spec().Parameters, ...)`）；对比 `tool/registry.go:72-82`
- AC / Invariant：Contract §1「后续修改工具自身 `Spec()` 均不影响 Registry 内定义」；Design §11 INV-107
- Trigger：工具注册后自改 `Spec().Parameters`（如放宽为空 Schema），再提交原本不合法的参数
- Actual：`Specs()` 仍返回注册快照，但执行期校验用实时 Spec —— 严格 Schema 自改为空 Schema 后，`{"name":1}` 通过校验并执行成功；自改为非法 Schema 时退化为 `Content="工具 ... 参数校验失败: 工具 Schema 非法"`；每次调用重复编译 Schema
- Expected：执行期校验以注册时定义为准，使「注册表定义不可篡改」覆盖校验路径
- Impact：需工具自身违反「注册后不可变」才触发，属信任边界内防御深度问题；不影响本任务任何 AC
- Required Fix Boundary：由 Owner 决定是否处理；若处理，需在不改 `Registry` 接口签名的前提令校验使用注册时 Schema，并补「注册后工具自改 Spec 不影响校验结果」的用例

## 复审结论依据

- 原触发条件逐条复验通过（见「复审对照」），修复 Diff 仅限 Finding 的修复边界，未夹带无关改动。
- 既有 Mutation（去重键 / 占位顺序 / 无缓冲 channel）检出能力未被削弱。
- 未发现修复引入的新风险：约束语义探针 9 组、取消时延与 goroutine 收敛探针、全量测试与 `-race` 均通过。
- Review Target C1'（`03dced5`）与当前代码一致，`CLEAN` 对该 target 有效。
