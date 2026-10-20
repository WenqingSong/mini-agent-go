# Task: Agent 执行循环（agent-runtime-loop-v1）

## Goal

交付可真实运行的 Agent 执行引擎：一次 Run 能把 Session 中的对话、模型调用、工具执行、上下文视图与运行日志组合成 LLM → Tool → LLM 循环，在次数预算、整体超时与取消约束下自主完成多步工具调用，并给出准确终态（`COMPLETED` / `FAILED` / `LIMIT_EXCEEDED` / `CANCELLED`）与可校验的消息历史；异常运行不留下非法或半截的消息历史，已发生的工具副作用不被重复执行、也不被伪装成已撤销。

## Deliverables

- 新增 Agent Loop 执行包（包名 / 路径 / 公开入口由 Contract 定稿，并回写 Design §12）。
- 确定性可测的循环实现：多轮工具循环、预算执行、超时与取消、终态映射、消息提交。
- 测试替身（可脚本化 Provider、记录型 / 可注入失败的 Fake Tool 等）与对应测试（含 `-race`）。
- `.agent/tasks/agent-runtime-loop-v1/contract.md`（Analyst）。
- `docs/design/runtime-architecture.md` 更新（`UPDATE`，章节范围见 Design Impact）。

## Scope

- 提供一次 Run 的完整执行入口：`RUNNING` 生命周期、终态判定、`core.RunResult`（状态、实际计数、消息序列、错误）构造与 `Validate` 通过。
- 实现 LLM → Tool → LLM 循环：以 `llm.Provider` 响应驱动；无 ToolCall 即完成；有 ToolCall 则执行、生成 `RoleTool` 结果消息并回填给模型，直至模型不再请求工具或命中终止条件。
- 支持跨 step 连续工具调用，以及单个模型响应中包含多个 ToolCall 的处理（顺序或并发由 Contract 定，验收只要求结果完整、可定位、不丢不并）。
- 工具执行装配：`tool.Executor` + `tool.Registry`，按 `core.ExecutionRef` + `core.ToolCall` 显式传身份；执行器实例生命周期须保证同一 Run 内的副作用去重成立。
- 预算执行者：`RunPolicy.MaxLLMCalls` / `MaxToolCalls` 的计数与上限判定，实际计数写入 `RunResult`，触限映射为 `LIMIT_EXCEEDED`（区分 `llm_call_limit` / `tool_call_limit`）。
- Run 级中断：整体超时与父 `ctx` 取消，映射为 `CANCELLED`（`deadline_exceeded` / `cancelled`），并明确中断点。
- 失败分类与终态映射：Provider 失败、不可恢复工具失败（panic / error）、契约错误（配对、参数）各自的终态与 `ErrorKind`，不新增 `ErrorKind`。
- 消息提交：与 `session.Manager.RunTurn` 组合，确定并实现「整轮原子提交 + 异常路径不留非法历史」的提交策略，满足 `Turn.Append` 的全序列配对校验约束。
- 上下文装配：用 `contextmgr.Manager.Build` 生成每次模型调用的消息视图（System Prompt / 历史摘要 / `TokenBudget` / `KeepRecent` 由装配参数提供），压缩摘要经 `Turn.SetSummary` 随提交写入 Session。
- 可观测性装配：按 Design §10 的既有字段语义发出 `run_started` / `llm_request` / `llm_response` / `tool_started` / `tool_finished` / `run_finished`，Sink 错误不影响 Run 结果。
- 测试：Scripted LLM 驱动的多轮确定性用例、失败注入、预算上限、超时与取消、同会话并发、`-race`；以及依赖方向与工程入口检查。
- Contract 与 Design：Analyst 起草 `contract.md` 并请 Owner 决策（`APPROVED` 后才实现）；`Design Impact = UPDATE` 的章节按 Contract 落库。

## Out of Scope

- `session.TodoOperations` → `builtin.TodoOperations` 适配、Todo ID 生成与状态迁移、`todo_*` 工具接入真实会话（Design §8.1 标注为「装配层 T07」，但不在 Owner 本轮需求与 AC 内，见 OPEN QUESTION OQ-1）。
- 生产 LLM `Summarizer` 适配器（用真实模型生成摘要、其调用计入 `RunPolicy`）、真实 tokenizer 与估算校准（Design §9 / §14 标注 T07；本任务只接受注入的 `Summarizer` 实现）。
- Provider 重试 / 退避 / 限流 / streaming / 鉴权；不修改 `llm/` 与 `llm/openai/` 的公共契约。
- 修改 `core` / `tool` / `session` / `contextmgr` / `runlog` 的公共符号（含新增 `ErrorKind`、导出 `Role` / `FinishReason` 常量）；确需扩展时先走 Contract Revision。
- Session 持久化（DB / Redis）、重启恢复、跨进程多副本一致性。
- CLI / HTTP Server / 可运行二进制 / Docker / 部署流水线；进程级 `slog` 默认 Logger 初始化。
- 并行工具执行的性能优化、worker pool、投机执行、把取消传播到工具 goroutine 内部。
- 性能基准、真实模型联调、多模态内容、日志后端接入与检索。
- 修改 `go.mod` / `Makefile` / `.cnb.yml` / `AGENTS.md` / `.agent/**`（本任务目录除外）/ `docs/design/agent-workflow.md`；在共享 `develop` 上提交。

## Milestone

Milestone: M2-agent-runtime-loop

说明：本任务是各模块首次组合成可运行系统（T00 为 `M1-runtime-core-contracts`）。Deliverer 需在干净快照上独立验证一次端到端 Run（可通过临时 consumer module 使用公开 API 完成，无需 CLI / 二进制）。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`（§3 模块责任新增 L3 Agent Loop 行、§7 中「由 Agent Loop 判定」的中断点与消息保留策略、§10 事件发出点接线、§12 文件所有权登记、§14 归属收窄与后续装配任务边界）

判定依据：T00 已把该层声明为「L3（未实现）Agent Loop / 装配层」，并把 §14 中的「取消/超时在 Loop 中的实际中断点与消息保留策略」「跨 step 的 `RunStatus` 终态映射、同 step 多调用顺序与并发」「运行期事件完整性、日志旁路成立」明确归 Agent Loop 任务；§12 要求新增文件必须在同一交付内回写。本任务不改变数据模型、`ErrorKind` 集合与合法状态迁移，属落实/修改既有长期事实，而非新增。Owner 需求标注 `Design Impact: NONE`，与此判定不一致，见 OPEN QUESTION OQ-2。

## Acceptance Criteria

- [ ] AC-001：给定一个已有 user 消息的 Session，当 Scripted LLM 的首次响应是不含 ToolCall 的纯文本 assistant 消息（`finish_reason` 表示不再需要工具）且以合法 `RunPolicy` 执行一次 Run 时，则 `RunResult.Status = COMPLETED`、`Error = nil`、`LLMCalls = 1`、`ToolCalls = 0`、`RunResult.Validate()` 返回 nil；Session 历史新增该 assistant 回答且 `core.ValidateMessages` 通过；不发生任何工具执行。
- [ ] AC-002：给定 Scripted LLM 脚本为「调用工具 A → 依据结果再次调用工具 B → 纯文本收尾」的三步序列，当执行一次 Run 时，则两次工具调用各被执行一次（记录型工具观测到调用参数与先后顺序），第二次及之后的 LLM 请求中已包含「assistant 的 ToolCalls + 对应 `RoleTool` 结果」且配对合法，最终 `Status = COMPLETED`、`LLMCalls = 3`、`ToolCalls = 2`，Session 历史完整成对。
- [ ] AC-003：给定一次模型响应同时包含 3 个及以上 ToolCall（其中至少 1 个成功、至少 1 个可恢复失败，例如未知工具或参数非法），当执行该 step 时，则每个 Call 都恰好产生一条 `RoleTool` 结果消息并以 `CallID` 回填（不丢弃成功结果、不跳过失败调用、不合并成一条），下一次 LLM 请求包含全部结果，Run 不因可恢复工具失败而终止，最终以模型不再请求工具而 `COMPLETED`；`ToolCalls` 计数等于实际执行的调用数。
- [ ] AC-004：给定失败注入，当分别构造下列情形时，终态、`ErrorKind`、计数与消息历史必须准确：① Provider 返回错误 → `FAILED` + `provider_failure`，失败后不再执行任何工具；② 工具 panic 或不可恢复工具错误 → `FAILED` + `tool_failure`，`Error.CallID` 指向具体调用；③ 模型调用达到 `MaxLLMCalls` → `LIMIT_EXCEEDED` + `llm_call_limit`，且第 `MaxLLMCalls+1` 次模型调用没有被发出；④ 工具调用达到 `MaxToolCalls` → `LIMIT_EXCEEDED` + `tool_call_limit`，超限 Call 不执行、不伪造结果；⑤ 父 `ctx` 取消 → `CANCELLED` + `cancelled`；⑥ 整体超时 → `CANCELLED` + `deadline_exceeded`。此外，每种情形的 `RunResult.Validate()` 返回 nil，结束后 Session 历史（若发生提交）通过 `core.ValidateMessages`，不存在「assistant 带 ToolCalls 却缺少结果」或孤儿 Result 的半截历史，且失败/取消不产生部分提交（历史要么整轮生效、要么与运行前完全一致）。
- [ ] AC-005：给定同一 Session 上并发发起 N（≥3）个不同 RunID 的 Run，当全部结束时，则这些 Run 在该会话上串行推进（任一时刻至多一个 Run 在该会话内执行，历史中每个 Run 的 assistant / tool 消息组连续不交错），总消息数等于各成功 Run 的消息数之和；同时给定两个不同 Session 的 Run 与一个阻塞中的假 Provider，则两者可同时推进（证明没有跨会话全局锁）；`go test -race` 无告警。
- [ ] AC-006：给定带调用计数的 Fake Tool，当出现「同一 Run 内重复 CallID」与「工具已执行后 Provider 失败或整体取消」两类情形时：① 重复 CallID 不会导致工具被第二次执行（实际执行次数为 1），Run 以非 `COMPLETED` 的终态结束且 `Error.CallID` 可定位到该调用；② 已执行的工具副作用不被「假装撤销」——`RunResult.ToolCalls` 反映真实执行次数，已产生的 `RoleTool` 结果不被删除或改写为未执行，结果与日志中不出现声称已回滚 / 已补偿的语义；③ 失败后不自动重试同一工具调用。
- [ ] AC-007：给定超预算历史与注入的 `Estimator` / `Summarizer`，当一次 Run 的上下文估算超出 `TokenBudget` 时，发给 Provider 的消息视图是 `contextmgr` 构建的压缩视图（保留最近不少于 `KeepRecent` 条，System Prompt 不以 `RoleSystem` 混入 `Messages`），压缩摘要随提交写入 `Session.Summary`，被覆盖消息按 Design §9 语义从 Session 移除；未超预算时不压缩、摘要不变；`Build` 失败时 Run 以 Contract 明确规定的失败终态结束，且 Session 历史不被部分修改。
- [ ] AC-008：给定注入的 `runlog.Sink`，当执行一次多步 Run 时，产生覆盖 `run_started` → 每次模型调用的 `llm_request` / `llm_response` → 每个工具调用的 `tool_started` / `tool_finished`（带与 `ToolCall.ID` 相同的 `call_id`）→ `run_finished` 的事件序列，每条带同一 `session_id` / `run_id` 与可用于定位的 `step`；当把 Sink 换成必定返回错误的实现时，`RunResult`（状态、计数、消息序列）与 Session 历史与正常 Sink 时逐字段相同（INV-008 的运行期证据），Run 不因此失败。
- [ ] AC-009：给定 APPROVED Contract，当 Cleaner 做四方核对（Task ↔ `contract.md` ↔ `docs/design/runtime-architecture.md` ↔ 实现）时，Loop 的入口签名、终态映射、预算计数、消息提交与中断点语义在四处一致且无未记录偏差；Design §12 新增路径与 `git ls-files` 差集为空；§14 中原归「Agent Loop 任务」的条目已收窄且无双重归属；新包依赖方向符合 INV-001（只依赖 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog`，不 import `llm/openai` 与 `builtin`）；`make test`、`go vet ./...`、`go test -race ./...` 退出码 0。

## Relevant Context

已核实事实：

- 根 Module 为 `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，零 `require`；本机 `go1.24.1`；任务开始时 `make test`（8 个包）与 `go vet ./...` 全绿。
- Git 基线：分支 `feat/agent-runtime-loop-v1` = `dc734d2`，与本地 `develop`、`origin/develop`、`origin/feat/agent-runtime-loop-v1` 一致（`git rev-list --left-right --count HEAD...@{u}` = `0 0`）；`git status --porcelain` 为空。
- `core` 已有 `RunStatus`（`RUNNING` + 4 个终态）、`IsTerminal`、`RunPolicy{MaxLLMCalls, MaxToolCalls}` 与 `Validate`、`RunResult{ExecutionRef, Status, LLMCalls, ToolCalls, Messages, Error}` 与逐状态 `Validate`（`tool_failure` 必须带 `CallID`）、9 个 `ErrorKind`、`KindOf` / `StatusOf`、`ExecutionRef{SessionID, RunID}`、`ValidateMessages` / `ValidateToolPairing`；但**没有任何代码构造完整 `RunResult`**。
- `llm.Provider` 为 `Complete(ctx, Request) (Response, error)`；`Response{Message, FinishReason, Usage, Raw}`，`Response.Validate` 要求 assistant 角色与已知 `finish_reason`；`FinishReason`（`stop` / `tool_calls` / `length` / `unknown`）**没有导出常量**，包外只能字面量比较。
- `tool.Executor.Execute(ctx, tool.Request) (core.ToolResult, error)`：未知工具、参数非法、工具级超时返回 `ToolResult{IsError:true}, err == nil`；工具 panic → `tool_failure`；父 `ctx` 取消 / 超时 → `cancelled` / `deadline_exceeded`；去重按 `(RunID, CallID)` 占位且**不释放、仅对同一 Executor 实例有效**，重复调用返回 `invalid_pairing` 且不复用首次结果。
- `session.Manager.RunTurn(ctx, sessionID, func(*Turn) error)` 已提供会话执行权串行与整轮原子提交（失败 / 取消 / 超时 / panic 整轮丢弃）；`Turn.Append` 每次都对「已提交基线 + 本 Turn 暂存 + 新批次」执行 `core.ValidateMessages`，因此**携带 ToolCalls 的 assistant 消息必须与其全部 `RoleTool` 结果同批提交**，否则被配对校验拒绝；`Turn.SetSummary` 随轮提交；Turn 内禁止回调 `RunTurn`（同会话执行权重入会自死锁）。
- `RunTurn` 在 `ctx` 已取消时直接返回 `cancelled` 且不提交，而 Design §7 规定「用户取消：保留已有消息，不回滚」——两者存在张力，需 Analyst 在 Contract 中给出唯一策略（见 Analyst Questions 2）。
- `contextmgr.NewManager(estimator, summarizer)` 要求两者非 nil；`Request{ExecutionRef, SystemPrompt, PreviousSummary, Messages, TokenBudget, KeepRecent}`、`Result{Messages, Compacted, DroppedMessages, Summary}`；`Messages` 不允许 `RoleSystem`；压缩摘要与 `DroppedMessages` 的写回语义见 Design §9。
- `runlog` 已有 6 个 `EventKind` 常量、`Event{Kind, Ref, Step, CallID, Summary, Data, Error, Duration, Status}` 与 `Sink.Record`，但**全仓没有任何发出方**（6 类事件均未接线）；§10 的 `Data` 允许清单是封闭集合。
- `builtin` 现有 5 个工具（`calculator` / `mock_search` / `todo_create` / `todo_list` / `todo_complete`），无 shell、无文件写入等副作用工具；`todo_*` 依赖尚未接线的 `TodoOperations` 端口。
- 全仓不存在 `loop` / `agent` / `runtime` 调度包，也没有 Scripted / Fake Provider（现有测试替身均为包内私有 `_test.go` 类型，不可复用）。
- `.agent/workflow.yaml` 的 `resources: {}`，无已声明 Shared Resource Kind；本任务不占用全局资源（`resources.reservations` 为空）。
- Design §3 把 L3 标为「Agent Loop / 装配层：未实现」；§14 把「取消/超时中断点与消息保留」「跨 step 终态映射、同 step 多调用顺序与并发」「日志发出点接线与事件完整性」归 Agent Loop 任务；同节把 Todo 适配与生产 `Summarizer` 适配器归「装配层（T07）」。
- `AGENTS.md` 仍写「仓库根尚无 `go.mod` / 无 `Makefile`」，与当前仓库事实不一致（文档滞后，不影响本任务）。

Assumption：

- 新代码放在新增顶层包（建议 `agent`，最终由 Contract 定），不修改既有包公共符号；确需扩展公共契约时走 Contract Revision。
- 全部 AC 可在进程内证明：Scripted Provider + Fake Tool + 注入 Sink + Fake `Summarizer`，不需要网络、外部服务、Docker 或真实模型。
- Deliverer 可用临时 consumer module 通过公开 API 完成端到端验证，不需要 CLI 或可运行二进制。
- Design 由 Analyst 在 Contract `APPROVED` 后先行落库（与 T05 / T06 一致），Coder 负责实现一致性与 §12 路径登记。

OPEN QUESTION：

- OQ-1（待 Owner 决定）：Todo 端口适配与生产 `Summarizer` 适配器是否纳入本任务？Design §8.1 / §9 / §14 标注为「装配层（T07）」，但 Owner 本轮需求与 AC 未包含，且两者可独立验收。本任务默认**不纳入**；若 Owner 要求纳入，属 Scope 扩展，需回 TaskBuilder 或由 Owner 在 Contract Decision 中明确，届时补 AC。
- OQ-2（待 Owner 决定）：Design Impact 判定为 `UPDATE`（理由见 Design Impact 节），与 Owner 需求中的 `NONE` 不一致；若 Owner 坚持 `NONE`，Design 相关 Scope 与 AC-009 需回退。
- OQ-3（待 Owner 决定）：Deliverer 里程碑标识使用 `M2-agent-runtime-loop`（T00 为 `M1-runtime-core-contracts`）；若 Owner 另有里程碑体系，请指出。
- OQ-4（不阻塞，Analyst 定）：是否导出测试替身（Scripted Provider 等）供 Deliverer 复用，影响 Design §12 登记与包边界。

## Verification

- AC-001 → 表驱动单元测试：单个纯文本响应，断言 `RunResult` 各字段、`Validate()` 为 nil、Session 快照新增 assistant 消息、工具调用计数为 0；对 Session 快照执行 `core.ValidateMessages` 断言 nil。
- AC-002 → Scripted Provider 三步脚本 + 记录型工具：断言工具调用参数序列、每次 `llm.Request.Messages` 的尾段（含 assistant ToolCalls 与对应 RoleTool 结果）、`LLMCalls` / `ToolCalls`、最终状态与 Session 配对合法性。
- AC-003 → 单响应 3+ ToolCall（成功 + 未知工具 + 参数非法）用例：断言产生 3+ 条 `RoleTool` 消息、`CallID` 一一对应、下一次请求包含全部结果、Run 继续并在下一次响应收尾时 `COMPLETED`；断言不存在丢弃或合并。
- AC-004 → 失败注入表：假 Provider 返回 error / 工具 panic / 脚本超过 `MaxLLMCalls` / 单响应调用数超过 `MaxToolCalls` / `ctx` 取消 / `context.WithTimeout` 超时六类，逐项断言 `Status` + `KindOf(Error)` + `CallID` + 计数 + `Validate()`；断言超限后 Provider / 工具调用次数未增长（记录型替身计数）；断言 Session 历史前后对比（整轮生效或完全不变）且 `core.ValidateMessages` 通过。
- AC-005 → 同会话 N≥3 并发 Run：断言消息组连续、总数等于各 Run 之和、每个 Run 的 `RunResult` 计数正确；跨会话并行用阻塞 Provider（`chan` 同步点）证明可同时推进；`go test -race ./...` 退出码 0。
- AC-006 → 记录型 Fake Tool：脚本故意重复同一 `CallID`，断言执行次数为 1、Run 非 `COMPLETED`、`Error.CallID` 命中；工具执行后 Provider 失败 / 超时用例断言 `RunResult.ToolCalls` 等于真实执行次数、已产生的工具结果消息仍存在且内容未被改写、输出内容中无回滚 / 补偿声明。
- AC-007 → Fake `Estimator` / `Summarizer`：构造超预算历史，断言 `llm.Request.Messages` 为压缩视图、`Session.Summary` 更新、被覆盖消息移除；未超预算用例断言 `Compacted=false` 且摘要不变；`Build` 失败用例断言终态与历史不变。
- AC-008 → 注入 `runlog.NewJSONSink(bytes.Buffer)`：解析每行 JSON，断言事件种类顺序、`session_id` / `run_id` / `step` / `call_id` 可定位；再换成必定失败的 Sink，断言 `RunResult` 与 Session 历史逐字段相同、Run 未失败。
- AC-009 → Cleaner 四方核对并给出逐条证据；`git ls-files` 与 Design §12 差集为空；`go list -f '{{.ImportPath}} {{join .Imports " "}}' <新包>` 只出现标准库与 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog`；Design §14 无同一语义双重归属。
- 通用检查：仓库根 `make test`、`go vet ./...`、`go test -race ./...` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 仍通过（Engine 为独立 Module）。环境：Linux + Go 1.24.x，无需 MySQL / Redis / MQ / Docker / 网络。

## Complexity

COMPLEX

原因：本任务的实现前必须先定多处会长期约束后续任务的执行语义——(1) 消息提交事务边界：`session.Manager.RunTurn` 的整轮原子提交与「取消后保留已有消息」相互冲突，且 `Turn.Append` 要求 assistant 的 ToolCalls 与全部结果同批提交，不同策略会产生不同的历史合法性与数据丢失风险；(2) 终态判定与失败分类边界（Provider 失败、工具 panic、`finish_reason=length/unknown`、可恢复工具失败、契约错误如何映射）；(3) 双预算的计数时机与超限截断（触限时不发出调用、单响应多调用如何截断且不伪造结果）；(4) 单响应多 ToolCall 的顺序 / 并发与副作用安全（重复 `CallID`、失败后不重试）；(5) 事件发出点与 §10 封闭字段清单的兼容；(6) 上下文压缩与摘要提交时序。多个现实方案会产生不同的可靠性、并发行为与后续扩展成本，需 Analyst 分析并由 Owner 在 Contract 中确认。

## Analyst Questions

1. 循环入口与包边界：新包路径与包名、公开入口签名（形如 `Run(ctx, Request) (core.RunResult, error)` 还是仅返回 `RunResult`）、装配参数（Provider / Session Manager / Executor / Registry / Context Manager / Sink / `RunPolicy` / SystemPrompt / Model / TokenBudget / KeepRecent）应如何组织；Loop 值本身是否要求并发安全（同一实例被多个 Run 并发使用）。这些决定 Design §3 / §12 的登记内容。
2. 消息提交事务边界（最关键）：整轮 Run 是否包在一次 `session.Manager.RunTurn` 内；若包，`ctx` 取消时 `RunTurn` 不提交与 Design §7「保留已有消息」如何同时满足；若不包，改用「内存累积 + 结束时一次提交」还是「每 step 一次提交」。需给出可测试的唯一策略，说明：成功 / 失败 / 取消 / 超时 / panic 各自哪些消息被提交到 Session，如何满足 `Turn.Append` 的「assistant ToolCalls 与全部结果同批」约束，以及如何避免半截配对。
3. 终态判定与失败分类：`Provider` 返回 error、`Response.Validate` 失败、`Executor` 返回的 `*core.RunError`（`invalid_argument` / `invalid_pairing` / `cancelled` / `deadline_exceeded` / `tool_failure`）分别映射到什么终态；`finish_reason = length` / `unknown` 或「`finish_reason=tool_calls` 但 ToolCalls 为空」这类矛盾响应如何收尾；空 assistant 内容是否合法。
4. 预算的计数与判定时机：`LLMCalls` / `ToolCalls` 在调用前预留还是调用后计入；触限的那次调用是否允许发出；单响应含 N 个 Call 而剩余工具预算小于 N 时如何处置（拒绝整个响应 / 截断并回填拒绝结果 / 直接 `LIMIT_EXCEEDED`）；计数与 `RunResult.Validate` 的一致性约束。
5. 单 step 多 ToolCall 的调度：顺序执行（按 `ToolCalls` 数组序）还是并发执行；一个失败是否影响同 step 其余调用；`step` 的定义（一次模型调用一个 step？）与 `runlog.Event.Step` 的取值规则。
6. 取消与超时的中断点：模型调用中 / 工具执行中 / 两者之间 / 提交阶段；如何区分用户取消与整体超时（`errors.Is(err, context.DeadlineExceeded)` → `deadline_exceeded`），以及已执行的工具结果是否进入 `RunResult.Messages`。
7. 重复 CallID 与副作用安全：同一 Run 内重复 `CallID` 的终态（`FAILED` + `invalid_pairing`？）；`Executor` 实例生命周期（Loop 单例 vs 每 Run 新建）对去重范围的影响；是否需要 Loop 层额外去重；不得新增 `ErrorKind`。
8. 观测发出点最小契约：每类事件在何处发出、`step` 如何递增、`Data` 只用 §10 允许清单内的键（新增键必须同步 Design）、`Sink` 错误如何吞掉、`run_finished` 的 `Status` / `Error` 绑定，以及是否需要在 §10 补充「发出端必填字段」句子。
9. 上下文组合细节：`SystemPrompt` / `Model` / `TokenBudget` / `KeepRecent` 的来源与默认值语义；`Build` 返回 `invalid_argument` 时的终态；`Compacted=true` 时 `SetSummary` 与「移除前 `DroppedMessages` 条消息」的落地方式（是否需要在 Session 侧新增能力，若需要则属公共契约变更，须先 Contract Revision）。
10. Design 更新最小集（`UPDATE`）：§3 需新增的 L3 行措辞与「不负责」边界；§7 需落实的中断点与消息保留语句；§10 发出点接线措辞；§12 需登记的路径（含测试替身是否导出）；§14 需收窄的条目，以及 §8.1 / §9 / §14 中「装配层（T07）」引用应如何处置（与 OQ-1 的 Owner 结论保持一致，避免悬空或双重归属）。
11. 测试替身归属：Scripted Provider / 记录型 Tool 放在包内 `_test.go`（不导出）还是作为可复用测试支持包导出（供 Deliverer 与后续装配任务使用）；两种选择对 Design §12、包边界与 Deliverer 验证方式的影响。

## Review Baseline

- Base commit：`dc734d2`（`merge: 合并 feat/runtime-observability-v1 到 develop`）。本地 `develop`、`origin/develop`、`origin/feat/agent-runtime-loop-v1` 与该提交一致（`git rev-list --left-right --count HEAD...@{u}` = `0 0`）。
- 任务开始时已有修改：无。`git status --porcelain` 为空，无未跟踪文件；`make test`、`go vet ./...` 基线全绿。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/agent-runtime-loop-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}` 与首个基线 commit（`chore(agent): 落库 T07 任务基线与初始状态`）；`contract.md` 由 Analyst 维护；`docs/design/runtime-architecture.md` 由 Analyst 在 Contract `APPROVED` 后按 Design Impact 声明的章节更新，不得外溢到其他章节；新包与测试由 Coder 在独立 commit 引入。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX，`contract.status = PENDING`）
