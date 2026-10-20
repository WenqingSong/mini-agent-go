# Cleaner Findings

## Review Target

- 任务基线：`dc734d2`（`develop`，Task 声明的 Base commit）；任务开始时 `git status --porcelain` 为空，无任务前遗留修改。
- 审查对象（implementation Evidence Commit C1）：`e9dd3da`（`feat(agent): 实现 Agent 执行循环与终态、提交与事件接线`），相对基线新增 8 个文件、2407 行，全部位于 `agent/`：`agent.go` / `run.go` / `events.go` / `fake_test.go` / `agent_test.go` / `concurrency_test.go` / `context_test.go` / `events_test.go`。
- 元数据 Commit C2（不参与审查）：`2c0c31b`，仅改 `state.yaml`（`review.status=PENDING`、`review.target=e9dd3da`）。
- 任务内其他相关提交（非 C1：任务/契约/Design 产物）：`36e2292`（基线落库）、`5e53709` / `3d9b61b`（Contract 起草与 APPROVED 内容）、`8f44dc3`（Design 落库）、`674e758` / `4918efa` / `2b91f8c`（Contract R1 与解除阻塞）、`a033165`（Design 同步 DropLeading 已交付）、`7de65a1`（merge develop，含前置任务 `635e311`）。
- 关键配置：`.agent/workflow.yaml` 的 `resources: {}`、`state.yaml` 的 `resources.reservations: {}` → 本任务不占用任何 Shared Resource（无 Registry 三边检查对象）。
- 审查时工作区状态：`git status --short` 为空；本地 HEAD = `2c0c31b`，`origin/feat/agent-runtime-loop-v1` = `2b91f8c`（C2 尚未 push，随本轮 Cleaner tail 一并发布）。
- 复现方式：`git switch --detach e9dd3da`（或等价的隔离 checkout）后运行下文 Verification 的命令；变异验证在 `git worktree add --detach /tmp/...` 的隔离副本中进行，未在审查对象工作树内做 mutation。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `agent/agent_test.go:TestRunCompletesOnPlainTextResponse`：`COMPLETED`、`Error=nil`、`LLMCalls=1`、`ToolCalls=0`、`Validate()=nil`；Session `[u1,a1]` 且 `core.ValidateMessages` 通过；记录型工具执行 0 次；`Request.System` 承载 System Prompt、`Messages` 无 system 角色（`agent_test.go:17`）。 |
| AC-002 | PASS | `TestRunLoopsThroughToolsAcrossSteps`：3 次模型调用 / 2 次工具执行，执行序与参数为 `c1{"v":1}`、`c2{"v":2}`；第 2 次请求已含 `a1` 的 ToolCalls + `c1` 结果，第 3 次请求含 `c1`/`c2` 两条结果；Session 6 条成对且 `ValidateMessages` 通过。 |
| AC-003 | PASS | `TestRunHandlesMultipleCallsInSingleResponse`：单响应 3 Call（成功 / 未知工具 / 参数非法）→ 3 条 `RoleTool`、`CallID` 一一对应、成功项 `IsError=false`、失败两项 `IsError=true`；下一请求含全部 3 条；工具实现只被调用 1 次（参数非法未进入实现）；最终 `COMPLETED`、`ToolCalls=3`。 |
| AC-004 | PASS | `TestRunFailureSemantics` 六个子用例：① Provider error → `FAILED`+`provider_failure`、`LLMCalls=1`、`ToolCalls=0`；② 工具 panic → `FAILED`+`tool_failure`+`CallID=c2`、`ToolCalls=2`、后续 `echo` 未执行、已产生的 `c1` 结果保留；③ `MaxLLMCalls=2` → `LIMIT_EXCEEDED`+`llm_call_limit` 且 Provider 仅被调用 2 次；④ 单响应 3 Call / `MaxToolCalls=2` → `tool_call_limit`、`ToolCalls=0`、无伪造结果；⑤ 模型调用中 `cancel()` → `CANCELLED`+`cancelled`；⑥ 20ms 整体超时 → `CANCELLED`+`deadline_exceeded`。全部子用例断言 `Validate()=nil` 且 Session 与运行前 `reflect.DeepEqual`（`assertSessionUnchanged`）。补充：`TestRunStopsOnCancellationDuringToolExecution`（工具执行中断点）、`ctx 已超时时不发出模型调用`。 |
| AC-005 | PASS | `agent/concurrency_test.go:TestRunSerializesSameSession`：同 Session 4 个不同 RunID 并发 → 每个 Run `COMPLETED`、`LLMCalls=2/ToolCalls=1`；Session 总数 `1+4*3`，逐 3 条一组断言「assistant(ToolCalls) → RoleTool → 文本回答」同属一个 Run 且配对正确、`ValidateMessages` 通过。`TestRunAllowsConcurrentDifferentSessions`：两个 Session 的 Run 同时进入阻塞 Provider（否则 2s 内失败）→ 证明无跨会话全局锁。`go test -race -count=1 ./agent` 通过，无 race 告警。 |
| AC-006 | PASS | `TestRunNeverRepeatsSideEffects`：① 第 1 步 `c1`、第 2 步重复 `c1` → 工具执行次数 1、`ToolCalls=1`、`FAILED`+`invalid_pairing`+`CallID=c1`、只有 1 条结果、Session 不变；② 工具执行后 Provider 失败 → `ToolCalls=1`（真实执行次数）、`c1` 结果仍在且内容为 `ok:c1`、`RunResult.Messages` 中无「回滚 / 补偿 / rollback / compensat」措辞、工具未被重试、Session 不变。 |
| AC-007 | PASS | `agent/context_test.go:TestRunCompactsOverBudgetHistory`：超预算 → 第 2 次请求为「摘要 system + 最近原子单元」压缩视图、`Session.Summary` 更新为压缩摘要、被覆盖消息（`u1`/`a1`）从 Session 移除、提交后 `ValidateMessages` 通过；`TestRunKeepsHistoryWhenWithinBudget`：未超预算 `Compacted=false`、摘要器 0 次调用、摘要不变；`TestRunFailsWhenTokenBudgetUnsatisfiable` → `FAILED`+`invalid_argument`、0 次模型调用、Session 不变；`TestRunFailsWhenSummarizerFails` → `FAILED`+`internal`、Session 不变；`TestRunDropsRunMessagesWhenCompactionCrossesBaseline` 覆盖 `head > baseLen` 偏移；`TestStripSystemPrompt` 覆盖「System Prompt 不混入 Messages、摘要 system 消息保留」。 |
| AC-008 | PASS | `agent/events_test.go:TestRunEmitsEventSequence`：解析 `runlog.NewJSONSink` 逐行 JSON，事件序为 `run_started → llm_request → llm_response → tool_started → tool_finished → llm_request → llm_response → run_finished`，`step` 为 `0/1/1/1/1/2/2/2`，每条带同一 `session_id`/`run_id`，`tool_*` 的 `call_id == "c1"`；`Data` 键取值为 `model`/`message_count`/`finish_reason`/`tool_call_count`/`input_tokens`，均在 Design §10 允许清单内；`run_finished.status=COMPLETED`。`TestRunIgnoresSinkFailures`：失败 Sink 下 `RunResult` 与 Session 快照与正常 Sink `reflect.DeepEqual`。 |
| AC-009 | PASS | 四方核对见下文「四方一致性」；`comm -3`（Design §12 的 `agent/*` 行 ↔ `git ls-files agent/`）无输出；`go list` 的 `agent` import 仅标准库 + `contextmgr/core/llm/runlog/session/tool`；`make test`、`go vet ./...`、`go test -race ./...`、`GOWORK=off go -C .agent/engine test ./...` 退出码 0。遗留文档措辞偏差记为 CLEAN-001（P3，不阻塞）。 |

四方一致性结论（AC-009 明细）：

- 入口签名：Contract §「Interfaces and Data」的 `SessionRunner` / `Config`（10 字段）/ `RunRequest` / `New` / `Run` 与 `agent/agent.go` 逐项一致（含 `New` 四个必填依赖的 `invalid_argument`、`Sink` 默认 `runlog.Discard()`、`Run` 只返回 `core.RunResult`）。
- 终态映射：Contract「Failure and Consistency Semantics」表与 `agent/run.go` 的 `contextError` / `classifyProviderError` / `classifyToolError` / `providerFailure` / `asRunError` + D5 `switch` 一致（`length`、`tool_calls` 空 Call、`unknown` → `provider_failure`；重复 `CallID` → `invalid_pairing`+`CallID`；ctx 终态优先）。
- 预算计数：Contract D3「通过预算检查后、调用前 +1」与 `run.go:111`（LLM）、`run.go:185`（工具，位于预算与重复检查之后）一致，恒有 `LLMCalls ≤ MaxLLMCalls`、`ToolCalls ≤ MaxToolCalls`。
- 消息提交与中断点：Contract §「单次 Run 的算法」的「一次 `RunTurn` + 仅 `COMPLETED` 提交 + `SetSummary` → `DropLeading(min(head, baseLen))` → `Append(history[max(head, baseLen):])`」与 `agent.go:115`、`run.go:212-226` 一致；五个中断点逐一对齐。
- Design：§2 L3 行、§3 `agent` 行、§7「提交边界」、§9 Loop 用法、§10 发出点接线表、§11.1 INV-001—INV-008、§12 八个 `agent/*` 路径、§14 归属收窄与「T07 不再承接 Todo / 生产 Summarizer」与 Contract 一致（仅状态措辞滞后，见 CLEAN-001）。

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go vet ./...` | PASS | exit 0，无输出 |
| `go build ./...` | PASS | exit 0 |
| `make test` | PASS | 9 个包全部 `ok`（含新增 `agent`） |
| `go test -race ./...` | PASS | 9 个包全部 `ok` |
| `go test -race -count=1 ./agent` | PASS | `ok ... 1.037s`（强制非缓存，无 race 告警） |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | Engine 独立 Module 仍绿 |
| `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./agent` | PASS | 仅 `context encoding/json errors fmt strings time` + `contextmgr core llm runlog session tool`；无 `builtin` / `llm/openai` |
| Design §12 ↔ `git ls-files agent/` 差集 | PASS | `comm -3` 无输出（8 ↔ 8，路径与文件名完全一致） |
| 依赖方向 INV-001 | PASS | 同上；L3 只依赖 L0/L1 |
| 新增文件范围 | PASS | C1 仅 8 个 `agent/` 文件；`core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`/`go.mod`/`Makefile`/`.cnb.yml` 未被 C1 修改 |
| Shared Resource 三边一致性 | N/A（PASS） | `.agent/workflow.yaml` 的 `resources: {}`，`state.yaml` 无 reservation，实现无新增全局资源值 → 无 Registry 检查对象 |
| Mutation A：工具预算整批预检失效（`> MaxToolCalls` → `> MaxToolCalls+100`） | 已捕获 | `TestRunFailureSemantics/工具预算整批预检` FAIL：终态 `FAILED` 而非 `LIMIT_EXCEEDED` |
| Mutation B：跳过压缩前缀删除（`drop > 0` → `drop < 0`） | 已捕获 | `TestRunCompactsOverBudgetHistory` FAIL（`Session` 仍为 `[u1 a1 a2  a3]`）、`TestRunDropsRunMessagesWhenCompactionCrossesBaseline` FAIL |
| Mutation C：去掉 Loop 层重复 `CallID` 前置拒绝 | 已捕获 | `TestRunNeverRepeatsSideEffects/同一 Run 内重复 CallID` FAIL：`ToolCalls=2` |
| Mutation D：提交阶段只追加部分消息（`Append` 少写最后一条） | 已捕获 | `TestRunLoopsThroughToolsAcrossSteps` FAIL：`FAILED invalid_pairing`（`call_id=c1` 缺结果） |
| Mutation E：把 `finish_reason=length` 当作可继续/完成 | 已捕获 | `TestRunFinishReasonSemantics/length + ToolCall` FAIL（`LLMCalls` 由 1 变 2） |
| Mutation 隔离方式 | 合规 | `git worktree add --detach /tmp/t07-mut*` 上一次一个变异，`git checkout -- agent/run.go` 复位或直接 `git worktree remove --force`；未在审查对象工作树内改代码，未 commit mutation，`git status` 保持为空 |
| 测试可信度（真实性边界） | PASS | 测试使用真实 `session.Manager`（内存实现）、真实 `tool.Executor`（含去重/超时/panic 恢复）、真实 `contextmgr`、真实 `runlog.NewJSONSink`；仅 Provider 与工具实现被替换为 Fake，未绕过本次要证明的会话、执行器、上下文与日志边界 |
| 断言有效性 | PASS | 断言最终业务结果（`RunResult` 字段 + `Validate`、Session 深比较、消息配对、真实执行次数），不只断言 Mock 调用次数 |
| 覆盖诚实性说明 | NOT_APPLICABLE | Design §11.1 INV-006 证据行提到「提交阶段」中断用例，`agent` 包内没有对应用例；该语义由 `session/cancel_test.go:TestCancelDuringExecutionDiscardsStagedWrites`（回调返回 nil 但 ctx 已取消 → 整轮丢弃）覆盖，`agent` 侧只是 `core.StatusOf(err)` 映射。记为 CLEAN-001 的文档措辞部分，不构成功能缺口 |

## Findings

### CLEAN-001：Design / Contract 的交付状态与字段措辞滞后于最终实现

- Severity：P3
- Status：OPEN
- Location：`docs/design/runtime-architecture.md:352`、`:353`、`:365`、`:366`、`:367`；`.agent/tasks/agent-runtime-loop-v1/contract.md:202`
- AC / Invariant：AC-009（Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 最终实现四方一致）
- Trigger：读者依据 Design §14 / §「未验证」判断 L3 是否已实现，或依据 Design §11.1 INV-006 的证据行判断中断点覆盖范围
- Actual：① §14 三条 Agent Loop 归属与「未验证」列表仍写「实现未交付」，而 C1 已交付；② §11.1 INV-006 证据行写「等执行权 / 模型调用中 / 工具执行中 / 提交阶段四类中断用例」，但 `agent` 包内没有提交阶段中断用例（该语义由 `session/cancel_test.go` 覆盖）；③ Contract §2 写「工具结果消息 `Name` 用 `ToolResult.Name`」，而 `core.Message` 没有 `Name` 字段，实现按 `Role` + `ToolResult` 构造（`agent/run.go:280`），行为正确但措辞无对应字段
- Expected：Design §14 / 未验证列表标注「T07 已交付（C1 `e9dd3da`），运行期证据在 `agent/*_test.go`」；INV-006 证据行的「提交阶段」指向 `session` 层用例；Contract 删除不存在的 `Name` 字段要求
- Impact：仅影响文档可读性与归属判断，不改变任何运行行为、不改变后续任务的行为约束；不影响本次 CLEAN 对实现的判定
- Evidence：上述行号；`agent/{agent,concurrency,context,events}_test.go` 全文无提交阶段中断用例；`core/message.go:38` 的 `Message` 字段清单无 `Name`；`go list`/测试结果证明实现与 Design 其余语义一致
- Required Fix Boundary：只同步文档措辞（`docs/design/*` 由 Analyst 主责，`contract.md` 由 Analyst 维护）；不修改生产代码、测试或已批准的 AC/不变量语义
