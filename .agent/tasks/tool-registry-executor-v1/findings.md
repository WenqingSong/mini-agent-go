# Cleaner Findings

## Review Target

- Task：`tool-registry-executor-v1`（Complexity = COMPLEX，`Design Impact = UPDATE`）
- 基线：`6902d24`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论`），任务开始时 `git status --short` 为空、无未跟踪文件。
- Contract：`APPROVED`，Evidence Commit A1 = `95e10b2`（`state.contract.target`）。
- 被审查实现对象（immutable Evidence Commit C1）：`707cb71b14a69eaf05eecf62d1a53093df9e865e`（`feat(tool): 实现工具注册、参数校验与受控执行器`）。
- Coder metadata commit C2：`551d6c3`，只改 `state.yaml`（`review.status=PENDING`、`review.target=707cb71`），无业务内容。
- 工作区状态：`git status --short` 为空；`git rev-list --left-right --count origin/tool-registry-executor-v1...HEAD` = `0 0`；HEAD = `551d6c3`。
- 纳入审查的变更集（`6902d24..707cb71`）：`tool/{registry.go,validate.go,executor.go}`、`tool/{registry_test.go,validate_test.go,executor_test.go,fake_test.go}`、`docs/design/runtime-architecture.md`（§7 / §11 / §12 / §14）、`.agent/tasks/tool-registry-executor-v1/*`。
- 设计落库路径：Analyst 设计提交 `a06dbcc`（§7 / §11 / §14）→ A2 `c6b9e66` → C1（`§12` 路径登记）。
- 任务前已有修改：无。所有差异均属本任务，无需区分他人遗留改动。
- 全局资源：`.agent/workflow.yaml` 的 `resources: {}`；`state.resources.reservations` 为空。本任务未使用任何 Registry 资源，三边一致性检查不适用（无 Registry 文件可对）。
- 环境：Linux + go1.24.1，无网络、无外部服务。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `tool/registry_test.go:15` `TestMapRegistryRegisterLookupAndSpecs`（`Lookup` 返回同一 `Tool` 实例、`Specs()` 的 Name/Description/Parameters 逐字节一致、大小写与空白不归一化）；`tool/executor_test.go:26` `TestExecutorSuccess`（工具自填 CallID/Name 被归一化为请求值、`IsError=false`、`ToolResult.Validate()==nil`）；`go list` 输出仅标准库 + `.../core`，`tool` 未 import `llm`。 |
| AC-002 | FAIL | 六类拒绝路径本身均已覆盖并通过（`TestMapRegistryRejectsInvalidRegistration`、`TestExecutorRejectsInvalidRequest`、`TestExecutorRejectsBeforeToolExecution`、`TestMapRegistryLookupMissing`，调用计数均为 0）。但存在**可注册**的合法 Schema，其声明的 `required` / `properties` / `additionalProperties` 被静默忽略，非法参数透传给工具实现（调用计数 1、`IsError=false`）→ 见 CLEAN-001。 |
| AC-003 | PASS（测试保护不足，见 CLEAN-002） | `TestExecutorToolTimeout`（20ms 工具级超时 → `IsError=true`）、`TestExecutorParentDeadlineDuringExecution`（`deadline_exceeded`）、`TestExecutorCancelDuringExecution`（丢弃工具产出）、`TestExecutorToolPanic`（`tool_failure`、无 `goroutine`/`stack` 痕迹、保留 CallID）、`TestExecutorTimeoutDoesNotGrowGoroutines`（30 次超时后 `waitUntil` 收敛）。执行器不产生 `RunStatus`（代码核对：`tool` 包无 `RunStatus` 引用）。「取消优先」守卫的行为正确，但现有用例无法识别其被删除（见 CLEAN-002）。 |
| AC-004 | PASS | `TestExecutorDedupWithinRun`（同 Run 同 CallID 第二次 `invalid_pairing`、不复用结果、总计数 1）、`TestExecutorDedupScopeIsPerRun`（不同 Run / 不同 CallID 计数 3）、`TestExecutorDedupConcurrent`（16 goroutine 同键：1 成功 + 15 `invalid_pairing`，计数 1）、`TestExecutorDedupAppliesToFailedFirstCall`（首次失败同样占位）。 |
| AC-005 | PASS | `make test`（全部包 ok）、`go vet ./...`（exit 0）、`go test -race ./tool/...`（ok）、`go test -race -count=3 ./tool/...`（ok）；`git diff 6902d24 -- go.mod go.sum` 为空；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` = 标准库 + `cnb.cool/framework-station/mini-agent-go/core`。 |
| AC-006 | PASS（附注） | §7 新增 4 行（单工具局部超时 / 参数校验失败与未知工具 / 同 Run 重复 Tool Call ID / 工具 panic），终态与 `ErrorKind` 与 Contract 失败语义表一致且显式标注「由 Agent Loop 判定」；§11 续编 `INV-009`—`INV-013` 且证据列指向真实测试文件；§14 已从 Agent Loop 名下收回「工具失败可恢复性分类」并加「归属收窄」说明；§12 登记 7 个新文件与 `git ls-files tool/`（另含 T00 既有 `tool/tool.go`）差集为空；`git diff 6902d24..707cb71 -- docs/design/runtime-architecture.md` 只落在 §7（表格 + Context 边界段）、§11（表 + INV-007 证据列）、§12、§14（未验证项 + 归属表 + 收窄说明）。附注：INV-011「参数非法时 `Tool.Execute` 不被调用」在 CLEAN-001 场景下不成立，属实现侧缺陷，不改设计条款。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `make test`（仓库根，go1.24.1） | PASS | `contextmgr/session` 无测试；`core/llm/runlog/tool` 全 ok |
| `go vet ./...` | PASS | exit 0 |
| `go test -race ./tool/...` | PASS | ok 1.121s |
| `go test -race -count=3 ./tool/...` | PASS | ok 1.324s（时间相关用例重复 3 轮稳定） |
| `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` | PASS | 仅 std + `<module>/core`，无 `llm`、无第三方 |
| `git diff 6902d24 -- go.mod go.sum` | PASS | 空，根模块仍零 `require` |
| `git ls-files tool/` ↔ 设计 §12 表 | PASS | 8 个文件（`tool.go` + 7 新增）全部登记，差集为空 |
| 设计 diff 范围（`6902d24..707cb71`） | PASS | 只涉及 §7 / §11 / §12 / §14 |
| 隔离 worktree `git worktree add --detach /tmp/review-c1 707cb71` 内做 Mutation（已在临时副本执行，未触碰 review target 工作区，已 `worktree remove`） | — | 见下方 4 项；其中 1 项未被现有测试检出 |
| M1：去重键去掉 `runID`（`dedupKey{callID}`） | DETECTED | `TestExecutorDedupScopeIsPerRun` FAIL |
| M2：把去重占位移到 `Lookup` 之后 | DETECTED | `TestExecutorDedupAppliesToFailedFirstCall` FAIL |
| M3：`done` 改为无缓冲 channel（放弃的 goroutine 永久阻塞） | DETECTED | `TestExecutorTimeoutDoesNotGrowGoroutines` FAIL（3s 收敛窗口不满足） |
| M4：删除 `case out := <-done` 内的「取消优先」守卫 | **NOT DETECTED** | `TestExecutorCancelDuringExecution` 连跑 20 次 + 10 次单跑全部 PASS → CLEAN-002 |
| 探针：嵌套子 schema 不声明 `type` 的注册与调用期行为 | **FAIL（缺陷）** | 注册被接受；`validateArguments` 对 `{"address":{}}`、`{"address":{"zip":1}}`、`{"address":{"zip":"1","city":"x"}}` 均返回 nil；执行器层面工具被调用 1 次且 `IsError=false` → CLEAN-001 |
| 探针：注册后工具自改 `Spec()` 对执行期校验的影响 | 观察记录 | 注册为严格 Schema 的工具自改为空 Schema 后，`{"name":1}` 通过校验并执行成功 → CLEAN-003 |
| Context 边界探测（超时后放弃 goroutine、同键再提交） | PASS | 超时返回 `IsError=true`；同 Run 同 CallID 再提交返回 `invalid_pairing`，未被放弃 goroutine 影响 |
| 测试质量抽查（断言最终业务结果 / 是否绕过边界） | 见 Findings | 拒绝路径均断言 `Tool.Execute` 调用计数（INV-102 真实生效），未用 Mock 绕过被测逻辑；唯一缺口是「取消优先」守卫 |

## Findings

### CLEAN-001：嵌套子 Schema 未声明 `type` 时，`required` / `properties` / `additionalProperties` 约束被静默忽略，非法参数透传给工具实现

- Severity：P2
- Status：OPEN
- Location：`tool/validate.go:218`（`validateValue` 按 `node.typ` 分支校验）、`tool/validate.go:88-180`（`compileSchemaValue` 允许子 schema 不声明 `type`）
- AC / Invariant：AC-002（非法参数必须在调用工具实现前被拒绝、Fake Tool 调用计数为 0）；Task Scope「不允许把非法参数透传给 Fake Tool」；Contract `Owner Decision D4`「未支持关键字或非法 Schema 拒绝注册，**不允许静默忽略约束**」；Contract INV-102；Design §11 INV-011
- Trigger：注册一个合法 Schema，其嵌套子 schema 只声明 `properties` / `required` / `additionalProperties`（未声明 `type`），例如 `{"type":"object","properties":{"address":{"properties":{"zip":{"type":"string"}},"required":["zip"],"additionalProperties":false}}}`，再提交 `{"address":{"zip":1,"city":"x"}}`
- Actual：注册被接受（无错误）；执行期 `validateValue` 走 `switch node.typ`，`node.typ == ""` 无任何分支，子树的类型、必填、未声明字段全部跳过；执行器返回 `IsError=false`，且 `Tool.Execute` 被调用 1 次（非法参数已透传给工具实现）
- Expected：注册期 fail-closed 拒绝该 Schema（子 schema 声明了 `properties` / `required` / `additionalProperties` / `enum` 却未声明可用的 `type`），或调用期按已声明的约束照常校验；两者任一都必须保证「声明的约束不会被静默丢弃」
- Impact：参数校验这一层在合法注册的 Schema 下被绕过——模型可以给未来内置工具（文件 / HTTP / Shell）传入缺字段、错类型或未声明字段，而执行器认为「参数合法」；这也是 `D4` 的 fail-closed 承诺在执行期失效的唯一路径
- Evidence：隔离副本探测（`/tmp/review-c1`，已移除）——`validateArguments` 对上述三种非法参数均返回 `nil`；执行器层面 `result={CallID:call-1 Name:demo Content:ok IsError:false}`、`callCount()==1`。现有 `tool/validate_test.go` 的用例（`personSchema` 等）所有节点都声明了 `type`，因此未覆盖该形态
- Required Fix Boundary：恢复「声明的约束不被静默忽略」——对「声明了 `properties` / `required` / `additionalProperties`（含 `enum`）但未声明匹配 `type`」的子 schema 在注册期拒绝，或在调用期按约束存在性校验；两种方式都需补一条能区分修复前后的回归用例（覆盖至少 `required` 与 `additionalProperties:false`），且不得放宽既有 fail-closed 关键字白名单

### CLEAN-002：「取消优先于工具结果」守卫缺少能识别错误实现的测试

- Severity：P2
- Status：OPEN
- Location：`tool/executor.go:163-168`（`case out := <-done` 内 `ctx.Err()` 守卫）；测试 `tool/executor_test.go:307` `TestExecutorCancelDuringExecution`
- AC / Invariant：AC-003「超时与取消不会被当成成功结果」；Contract §4「**取消优先于工具结果**：执行返回时若父 ctx 已结束，丢弃工具结果并返回 ctx 错误」；Contract INV-106；Design §11 INV-010
- Trigger：删除 `case out := <-done` 内的 `if err := ctx.Err(); err != nil { return contextRunError(...) }` 守卫（即工具结果永远优先于已结束的父 Context），再运行 `go test ./tool/`
- Actual：`TestExecutorCancelDuringExecution` 仍然 PASS（`-count=20` 20/20 PASS，另 10 次单跑全 PASS）。该用例先 `cancel()` 再 `close(release)`，其 `select` 总是在 `ctx.Done` 分支返回，因此从未走到「`done` 先就绪但父 ctx 已结束」这条守卫路径
- Expected：存在一个用例，删除该守卫后必定 FAIL（确定性，不依赖 `select` 随机选择或调度时序）
- Impact：Design §11 `INV-010` 与 Contract §4 明确写入的核心不变量（取消优先、绝不把已取消的结果当成成功回填）没有可识别的回归保护；后续重构（如把 `select` 改为先收结果再判 ctx）会静默破坏它，而全量测试仍绿
- Evidence：隔离副本 Mutation M4（删除守卫）后 `go test -count=20 ./tool/ -run TestExecutorCancelDuringExecution` 全 PASS；同一守卫在可控 Context（`Done()` 恒 nil、工具返回前置 `Err()=context.Canceled`）下可被确定性检出（原始实现返回 `cancelled`，删除守卫后返回 `IsError=false` 成功结果），证明「可写出必失败用例」
- Required Fix Boundary：新增（或改造）一条能确定性区分该守卫存在与否的用例，使删除「父 ctx 已结束则丢弃工具结果」的判断后测试必失败；用例须断言返回 `cancelled` / `deadline_exceeded` 且**不返回 `ToolResult`**，不得依赖 `select` 随机性或固定 sleep

### CLEAN-003：执行期参数校验读取工具实时 `Spec()`，绕开 Registry 注册时的已校验快照（P3）

- Severity：P3
- Status：OPEN
- Location：`tool/executor.go:107`（`validateArguments(name, tool.Spec().Parameters, ...)`）；对比 `tool/registry.go:72-82`（注册时深拷贝并保存 `specs[name]`）
- AC / Invariant：Contract §1「注册时保存 Spec 的一份深拷贝……后续修改工具自身 `Spec()` 均不影响 Registry 内定义」；Design §11 INV-107；INV-102（校验必须先于执行）的语义一致性
- Trigger：工具在注册后修改自身 `Spec().Parameters`（例如放宽为 `{"type":"object","properties":{}}` 或改成子集外关键字），再提交原本不合法的参数
- Actual：`Specs()` 仍返回注册快照（符合 INV-107），但执行期校验使用工具实时 Spec —— 探测中把严格 Schema 自改为空 Schema 后，`{"name":1}` 通过校验并执行成功（`IsError=false`）；自改为非法 Schema 时则退化为 `Content="工具 ... 参数校验失败: 工具 Schema 非法"`。另外每次调用都会重新 `compileSchema`（注册期已编译一次）
- Expected：执行期校验以 Registry 注册时已校验的定义为准（或至少保证工具自改 Spec 不会使已声明的参数约束失效），使「注册表定义不可被外部篡改」覆盖到校验路径，而不只是 `Specs()` 输出
- Impact：需要工具自身违反「注册后不可变」才可触发，属信任边界内的防御深度问题；同时每次调用重复编译 Schema 是小幅但恒定的开销。不影响 INV-107 的既有测试结论
- Evidence：隔离副本探测——注册 `{"properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}` 后工具自改为空 Schema，`Execute` 返回 `{Content:ok, IsError:false}`
- Required Fix Boundary：由 Owner 决定是否处理。若处理，需在不改 `Registry` 接口签名的前提下令执行期校验使用注册时定义的 Schema，并补一条「注册后工具自改 Spec 不影响校验结果」的用例

## 复审要求

- CLEAN-001 与 CLEAN-002 为 P2，阻塞 `CLEAN`。Coder 修复后须提供：修复 Diff、能区分修复前后的回归测试、`make test` / `go vet ./...` / `go test -race ./tool/...` 结果，并重新发起 Review Request（新 C1）。
- CLEAN-003 为 P3，不阻塞；由 Owner 决定是否纳入本次修复。
- Cleaner 保留 `findings.md` 的关闭权：修复 Diff 与回归证据由 Cleaner 复审验证后再关闭 Finding。
