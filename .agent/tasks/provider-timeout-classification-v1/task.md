# Task: Provider 超时与请求级超时的错误分类一致性（provider-timeout-classification-v1）

## Goal

修复 HTTP API 对「Provider 自身超时」与「请求级超时」的错误分类不一致：Provider 自身超时时，HTTP 响应、Agent Run 终态与运行日志三处必须统一表达 `provider_failure` / `FAILED`（HTTP `502 + provider_failure`），不得退化为 `504 + deadline_exceeded`；请求级超时时必须统一表达 `deadline_exceeded` / `CANCELLED`（`504 + deadline_exceeded`），不得被归类为 Provider 故障。同时保证 `409/408/429/500` 等既有映射与 Session 整轮原子性不回归。

## Scope

- Analyst 起草 `contract.md`（错误分类的权威依据与优先级、Provider 超时的最小可复现错误链、`httpapi` 直接错误路径与 RunOutcome 路径的语义分工、Session 原子性结论、允许的修复落点边界），并请求 Owner Contract Decision（`APPROVED` 后方可进入实现）。
- 调查（只为确定修复边界，不做无关扫描）：
  - `httpapi/errors.go` 的 `errorCodeOf` 分类逻辑与判定顺序；
  - `httpapi/run.go` 的 `RunOutcome.checkedCode` → HTTP 映射路径；
  - `httpapi/handler.go` 的 `handleChat` 两条失败路径（Runner 返回 error 的「未形成结论」路径 vs 已形成 `RunOutcome` 的路径）；
  - `agent/run.go` 中 `classifyProviderError` 如何产生 `provider_failure` 并保留 `Cause`；
  - `core.RunError.Unwrap` / `core.KindOf` / `core.StatusOf` 与 `context.Canceled` / `context.DeadlineExceeded` 在错误链上的关系；
  - Provider（`llm/openai/client.go`）自身超时被归为 `deadline_exceeded` 的事实，以及它与 Agent Loop 重新归因为 `provider_failure` 之间的层间差异。
- 明确「已确定的 Run 终态 + `ErrorKind`」与「底层 `Cause` 中的 ctx 哨兵」谁是权威依据，并覆盖直接错误路径与 RunOutcome 路径的不同语义。
- 最小范围生产代码修复：Owner D2 已决定生产改动只落在 `httpapi/errors.go` 的 `errorCodeOf`，不改 `llm/openai`、`agent`、`core`、`session`、`service`、`cmd`。若实现中发现必须突破该边界，停止并请求 Owner 决策，不得自行扩大。
- Design 交付物（Task 声明，不是 Coder 的实现范围）：更新 `docs/design/http-api.md` §4 的错误码判定优先级事实（仅该节；按 `## Design Impact` 的三条事实口径），由 Analyst 在 Contract 经 Owner 批准并登记 `APPROVED` 之后、Coder 实现之前落库；Coder 不得修改 `docs/design/*`。
- 关键回归测试：Provider 自身超时、请求级超时、请求 ctx 仍有效但错误链含 `context.DeadlineExceeded` / `context.Canceled`、既有映射不回归、Session 不产生半轮历史。
- 验证：构建、静态检查、单元测试、Race Test 通过（命令见 Verification）。

## Out of Scope

- Agent Loop 架构重构（`agent` 的循环结构、提交边界、分类职责划分的重新设计）。
- LLM Client 协议重构（`llm` 接口、请求/响应结构、`llm.Request` / `llm.Response` 语义）。
- 新增 `core.ErrorKind`、新增 HTTP `ErrorCode`、修改既有错误码 → HTTP 状态码映射表。
- 修改 Session / Context Manager 的语义、存储或并发模型（含 `session.Manager`、`session.Turn`、`contextmgr`）。
- Todo 与 Summarizer 相关开发；v1 的 `TokenBudget = 0` 与大上下文边界。
- 新增真实 Provider 调用、真实付费模型、网络依赖或以网络成功作为验收条件。
- 修改 Workflow Engine（`.agent/engine/`）或 `.agent/workflow.yaml`。
- 承担 `agent-runtime-e2e-tests-v1` 的最终系统验收：本任务只修生产缺陷，不改写原 E2E 的业务断言、不清理或覆盖其工作区的未跟踪 `e2e/` 文件。
- 顺带修复与本次错误分类无关的问题（日志字段、文案、重试策略、限流等）。
- 全局资源占用：本任务不新增 Shared Resource，`state.resources.reservations` 保持空。

如果发现必须突破上述范围才能满足 AC，停止并请求 Owner 决策。

## Design Impact

Design Impact: UPDATE

Design Artifact: `docs/design/http-api.md` §4（错误封套与错误码域：错误码判定优先级与 Context 兜底）

本次只更新该节的判定优先级事实，且只允许记录三条事实：

- 已有的 `core.RunError.Kind` 是 HTTP 错误分类的权威依据；
- 底层 `Cause` 中的 `context.Canceled` / `context.DeadlineExceeded` 不得覆盖已形成的分类；
- 错误链中不存在 `*core.RunError` 时，Context 哨兵作为兜底判定。

明确不改变：既有 HTTP 错误码 → 状态码映射表、错误码枚举（`ErrorCode`）、对外协议与状态机。

责任与时序：该 Design 更新是 Task 声明的 Design 交付物，由 Analyst 在 Contract 经 Owner 批准并登记 `APPROVED` 之后、Coder 实现之前落库；Coder 不得修改 `docs/design/*`。Design 更新与实现同批交付，由 Cleaner 做 Task ↔ APPROVED Contract ↔ `docs/design/http-api.md` §4 ↔ 最终 Implementation 四方核对。

## Acceptance Criteria

- [ ] AC-001：给定某次 chat 请求的请求级 Context 仍未到期，而 Provider 自身的 HTTP 请求发生超时（其返回错误链可含 `context.DeadlineExceeded`），当该请求结束时，HTTP 响应必须是 `502` 且 `error.code = provider_failure`；同一 Run 的 `RunStatus` 必须是 `FAILED`、`ErrorKind` 必须是 `provider_failure`，运行日志 `run_finished` 事件的 `error_kind` 必须是 `provider_failure`（`status` 为 `FAILED`）。不得返回 `504 + deadline_exceeded`，也不得出现三个观测点彼此矛盾。
- [ ] AC-002：给定请求级 Context 因服务端请求超时到期（Provider 可能已观察到该 ctx 取消，也可能尚未发起模型调用），当该请求结束时，HTTP 响应必须是 `504` 且 `error.code = deadline_exceeded`；同一 Run 的 `RunStatus` 必须是 `CANCELLED`、`ErrorKind` 必须是 `deadline_exceeded`，`run_finished.error_kind` 必须是 `deadline_exceeded`。不得归类为 `provider_failure`，也不得归类为 `cancelled`。
- [ ] AC-003：错误分类优先级必须一致且可判定：
  - Provider 自身超时与请求级超时被明确区分（AC-001 / AC-002 互不串味）。
  - 请求级 Context 仍有效时，即使 Provider 原始 `Cause` 中包含 `context.DeadlineExceeded`，也不得覆盖已经得出的 `provider_failure` 结论。
  - 请求级 Context 仍有效时，即使 Provider `Cause` 中包含 `context.Canceled`，也不得映射为客户端取消（`408 cancelled`）。
  - 当一次 Run 已经给出无自相矛盾的终态与 `ErrorKind` 时，HTTP `error.code` / 状态码必须以该结论为唯一权威，底层 `Cause` 不得改变映射结果；不得出现 `504 + provider_failure`、`502 + deadline_exceeded` 这类混搭。
  - 会话哨兵（`session_not_found` / `session_expired` / `session_exists`）的既有最高优先级不得被削弱。
  - 不构成有效结论的组合（非终态、终态缺少错误、`COMPLETED` 却带错误、`Status` 与 `StatusOf(Err)` 不匹配、调用计数为负）仍必须映射为 `internal` / 非 2xx，不得映射为成功。
- [ ] AC-004：原有行为不回归，以下情形映射结果与本次修复前一致（除 AC-001/AC-002/AC-003 涉及的错误分类外）：
  - 直接传入请求 ctx 超时 / 直接传入请求 ctx 取消（不经过 Run 结论的路径）→ `504 deadline_exceeded` / `408 cancelled`。
  - 客户端断开连接 → Run 终态 `CANCELLED` + `ErrorKind cancelled`，本 Turn 不提交；连接已不可写时不要求客户端实际收到状态码。
  - Provider 返回 HTTP 4xx / 429 / 5xx（由 Provider 以 `provider_failure` 表达）→ `502 provider_failure`，不得映射为 `429 llm_call_limit` 或 `400`。
  - Session 不存在 / 已过期 / 同 ID 仍有效 → `404 session_not_found` / `404 session_expired` / `409 session_exists`；chat 的会话预检失败不得执行 Run、不得产生会话写入。
  - `checkedCode` 对自相矛盾结论的拒绝仍为 `internal`（5xx，非 200）。
  - 工具失败（不可恢复、带 `CallID`）→ `500 tool_failure`；同一 Run 内重复 `CallID` → `500 invalid_pairing`。
  - LLM 预算耗尽 → `429 llm_call_limit`；工具预算不足（整批不执行）→ `429 tool_call_limit`。
  - 模型结束语义异常（`finish_reason=length`、无 Call 的 `tool_calls`、`unknown`）→ `502 provider_failure`，不得 `COMPLETED`。
- [ ] AC-005：Session 原子性：Provider 自身超时与请求级超时后，从 `session.Manager` 读到的历史消息、Summary 与 Todo 必须与请求前逐字段相等——既不提交本轮用户消息，也不提交半轮 assistant / tool 消息；已有历史、Summary、Todo 不得被错误修改。
- [ ] AC-006：回归测试有效：
  - 使用 `httptest` + 测试替身 Provider + 本地可控环境，不调用真实付费模型、不依赖外网。
  - 测试断言真实观测值：HTTP 状态码与 `error.code`、Run 的 `Status` 与 `ErrorKind`、`run_finished` 的 `status` / `error_kind`、Session 快照，不得只断言 mock 调用次数或内部函数返回值。
  - 至少对关键分类逻辑做一次可逆 Mutation（临时回退为错误实现），证明新增测试会失败；Mutation 需恢复原状并记录在 Coder 交接中，由 Cleaner 独立复核。
  - 既有相关测试与全量测试通过，`go test -race ./...` 通过。
- [ ] AC-007：交付边界正确：生产代码只落在 APPROVED Contract 授权范围内（Owner D2：仅 `httpapi/errors.go` 的 `errorCodeOf`；任何越界改动必须在 Contract 中列明并经 Owner 批准）；Task ↔ APPROVED Contract ↔ `docs/design/http-api.md` §4 ↔ 最终 Implementation 四者一致，Design 该节只记录 `## Design Impact` 列出的三条事实，且不改变既有状态码映射表、错误码枚举、对外协议与状态机；不修改原 E2E 任务的业务断言以求通过；不混入其他任务的未提交文件；`review.target` 指向明确的 Implementation Commit。

## Relevant Context

- 已核实事实：`httpapi/errors.go:159` 的 `errorCodeOf` 判定顺序为「会话哨兵（`errors.Is`）→ `errors.Is(err, context.DeadlineExceeded)` → `errors.Is(err, context.Canceled)` → `core.KindOf(err)` → `internal`」。ctx 哨兵检查在 `core.KindOf` **之前**，且对整条 `Cause` 链生效，这是「已确定的 `provider_failure` 被底层 `context.DeadlineExceeded` 覆盖」的直接代码位置。
- 已核实事实：`agent/run.go:230` 的 `classifyProviderError` 在请求 ctx 未终止时一律返回 `KindProviderFailure` 并保留 `Cause: err`；`core/error.go:84` 的 `RunError.Unwrap` 暴露该 `Cause`。因此 Provider 错误链中任意 ctx 哨兵都能到达 HTTP 映射层。
- 已核实事实：`httpapi/run.go:47` 的 `checkedCode` 只校验「计数为负 / `COMPLETED` 带错误 / 非终态 / 终态缺少错误 / `core.StatusOf(Err) != Status`」，通过后直接调用 `errorCodeOf(o.Err)`；它不校验「code 与 `ErrorKind` 是否同源」。因此 `FAILED + provider_failure` 目前可以被合法写成 `504 + deadline_exceeded`，HTTP 与 Run 结论不一致不会在这里被拦截。
- 已核实事实：`httpapi/handler.go:86` 的 `handleChat` 存在两条语义不同的失败路径——(a) `Runner` 返回 error（「未形成 Run 结论」：会话哨兵、ctx 终态、基础设施失败）直接走 `errorCodeOf(err)`；(b) 已形成 `RunOutcome` 时走 `checkedCode()`。修复必须同时保证两条路径语义正确，不能只改其中一条。
- 已核实事实：`llm/openai/client.go:150` 将客户端自身 `http.Client.Timeout` 触发（`net.Error.Timeout()`）归为 `KindDeadlineExceeded` 并保留 `Cause`（`*url.Error`）；ctx 终止走 `contextRunError`（取消 → `cancelled`，否则 `deadline_exceeded`）；其它传输失败与 HTTP 非 2xx 归为 `provider_failure`。`cmd/mini-agent/main.go:269` 的生产装配只设置 `BaseURL` / `APIKey`，未设置 Provider `Timeout`。
- 已核实事实：`agent/agent.go:100` 以 `core.StatusOf(Error)` 生成 `RunResult.Status`，`run_finished` 事件同时携带 `Status` 与 `Error`；`runlog/slog.go:99` 用 `core.KindOf(ev.Error)` 输出 `error_kind`。因此「Run ErrorKind」与「`run_finished.error_kind`」同源，但都与「HTTP code」独立，三者一致必须由实现保证。
- 已核实事实：`docs/design/http-api.md` §4 的错误映射表把 `deadline_exceeded` / `provider_failure` 分别对应 `core.KindDeadlineExceeded` / `core.KindProviderFailure`，并写明判定优先级为「先会话哨兵（`errors.Is`）→ 再 `core.KindOf` → 最后兜底 `internal`」；`docs/design/runtime-architecture.md` INV-006 规定「ctx 终态优先于其它错误来源」属 Run 归因层（`CANCELLED` + `cancelled`/`deadline_exceeded`）。即：现行长期 Design 已把 Run 的 `ErrorKind` 作为映射依据，实现中多出的一层 ctx 哨兵优先是本次不一致的来源。
- 已核实事实：`session/manager.go:188` 的 `RunTurn` 在回调返回错误或 ctx 终止时整轮丢弃（不发布 `staged()`），因此「失败不产生半轮历史」的机制已存在，本任务需要的是证明它在这两类超时下仍成立，而不是新造机制。
- 已核实事实：全链路测试装配已存在——`httpapi/handler_test.go:418` 的 `TestRealRunnerEndToEnd` 用真实 `session.Manager` + 真实 `agent.Loop`（经 `service.NewRunner` 装配）+ `httptest` 服务器；`httpapi/fake_test.go:153` 的 `scriptedProvider` 只回放预设响应，**不支持注入错误**，Coder 需要补一个可注入错误的 Provider 测试替身（测试内实现，不改生产接口）。
- 已核实事实：根模块 Go 版本 1.24，零第三方依赖；`Makefile` 提供 `test`（`go test ./...`）、`build`、`vet`、`test-engine`；`-race` 未进入 CI，需显式执行。`.agent/engine` 是独立 module，与本任务无关且不应改动。
- Assumption：Provider 自身超时时其错误链中确实会出现 `context.DeadlineExceeded`（Owner 现场报告，且 `llm/openai` 在客户端超时路径保留 `*url.Error` 为 `Cause` 的行为支持该现象）；精确的链形态由 Analyst 用最小复现固化，不由 TaskBuilder 猜测。
- Assumption：本工作区 `git status --short --untracked-files=all` 为空，未出现 Owner 提到的 `e2e/` 未跟踪文件，推测其属于原 E2E 工作区而非本工作区；本任务仍按「不得清理、覆盖或混入」的约束执行。
- 已决事实（Owner D2）：不改 `llm/openai`，也不改 `cmd/mini-agent`、不新增 Provider `Timeout` 配置；Provider 自身超时的分类由 HTTP 映射入口（`httpapi/errors.go` 的 `errorCodeOf`）修复。`llm/openai` 现有的「客户端自身超时 → `KindDeadlineExceeded` 且保留 `Cause`」行为保持不变，因此 HTTP 层必须能在请求 ctx 仍有效时正确忽略 `Cause` 中的 ctx 哨兵。
- 已决事实（Owner D4）：生产装配（`cmd/mini-agent`）与默认超时配置保持现状，本任务不新增任何 Provider 超时配置项，也不改变 `RequestTimeout` 的默认值。

## Verification

- AC-001 → 全链路测试（`httptest` + 真实 `session.Manager` + 真实 `service.Runner` + 真实 `agent.Loop` + 注入错误的测试替身 Provider，复用 `httpapi/handler_test.go` 现有装配方式）：Provider 返回一个 `Kind` 已被 Agent 归因为 `provider_failure`、且 `Cause` 链包含 `context.DeadlineExceeded` 的错误（同时覆盖「`Cause` 只含普通超时错误」与「`Cause` 含 ctx 哨兵」两种构造），断言：HTTP 状态 502、`error.code=provider_failure`；同时注入记录型 `runlog.Sink`，断言 `run_finished` 的 `status=FAILED`、`error_kind=provider_failure`；并通过 `RunOutcome`（或 `service` 返回值）确认 `Status=FAILED`、`core.KindOf(Err)=provider_failure`。若 Provider 替身无法覆盖某一层，必须改用对应层（`openai.Client` + `httptest` 上游）测试同一走查，且写出所用层与理由。
- AC-002 → 全链路测试：把 `httpapi.Config.RequestTimeout` 设为极短值，或在测试中派生可取消/可超时的请求 context，构造「请求级到期」场景（分别覆盖 Provider 观察到 ctx 取消与尚未发起模型调用两种），断言 HTTP 504 + `deadline_exceeded`、Run `CANCELLED` + `deadline_exceeded`、`run_finished.error_kind=deadline_exceeded`。
- AC-003 → 分类表驱动测试（映射入口层）：构造 `provider_failure` + `Cause=context.DeadlineExceeded`、`provider_failure` + `Cause=context.Canceled`、`cancelled/deadline_exceeded` 终态、会话哨兵包装、矛盾结论（非终态 / 缺错误 / `COMPLETED` 带错 / `Status` 与 `StatusOf` 不符 / 负计数）等输入，断言 code 与状态码符合「Run 结论优先、Cause 不得翻转」的契约，且矛盾结论一律非 2xx。断言对象是协议层输出，不是内部函数返回值。
- AC-004 → 回归用例：直接 ctx 超时/取消（非结论路径）、客户端断开（`cancel()` 后断言终态 `CANCELLED + cancelled` 且未提交）、Provider 4xx/429/5xx、Session 三类哨兵（含预检不执行 Run、不产生写入）、`checkedCode` 矛盾拒绝、工具失败与重复 `CallID`、LLM/工具预算耗尽、`finish_reason` 三类异常。优先沿用 `httpapi` / `service` 既有用例并确认仍通过；如某情形当前无覆盖，补最小用例。
- AC-005 → 原子性测试：请求前用 `session.Manager.Save` 写入带 Summary / Todo / 历史的基线，分别触发 Provider 超时与请求级超时，请求后用 `Manager.Get` 逐字段比对（消息逐条、Summary、Todo），断言完全相等。
- AC-006 → 可逆 Mutation：临时把分类逻辑回退为修复前行为（或等价地把结论优先级反转），确认 AC-001/AC-002/AC-003 的新测试失败，然后恢复并全绿；清理 Mutation 痕迹，确保不进入提交。命令：`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`、`make test` 均退出码 0；另确认 `.agent/engine` 未受影响（可不重跑，若重跑须 `GOWORK=off go -C .agent/engine test ./...`）。
- AC-007 → Cleaner 四方核对：`review.target` 完整 diff 的路径集合必须落在 APPROVED Contract 授权清单内（Task ↔ APPROVED Contract ↔ `docs/design/http-api.md` §4 ↔ 最终 Implementation 四者一致），核对 `state.yaml` 与任务文件归属；确认 `docs/design/http-api.md` §4 只记录 `## Design Impact` 列出的三条事实、未改动状态码映射表与错误码枚举；确认无 `e2e/` 或其他任务文件混入（禁止 `git add -A` / `git add .`）。
- 环境要求：Linux + Go 1.24.x（race detector 可用）+ GNU Make；无需 MySQL / Redis / MQ / Docker；全程本地测试，不调用真实付费模型，不以网络可达作为通过条件。

## Complexity

COMPLEX

原因：表象是一个映射优先级问题，但根因尚未被证据固化（Provider 自身超时为何在链上带 `context.DeadlineExceeded`、哪一层才是权威），且存在多个会产生不同可观察结果的现实方案（只收敛 HTTP 映射入口，还是同时调整 `llm/openai` 对客户端超时的归类；结论优先权落在 `checkedCode` 还是 `errorCodeOf`；是否需要在 `checkedCode` 增加 code 层一致性校验）。这些问题同时受长期 Design 已记录的判定优先级约束，属错误语义与公开协议边界，须由 Analyst 分析、Owner 在 Contract 中确认。

## Analyst Questions

以下问题中第 2、3 项已由 Owner D1（错误分类优先级）与 D2（生产改动只改 `httpapi/errors.go` 的 `errorCodeOf`）决定，原文仅作调查记录；其余问题仍待 Analyst 在 Contract 中收口。

1. 根因确认：请给出 Provider 自身超时导致错误链中出现 `context.DeadlineExceeded`（以及可能出现的 `context.Canceled`）的最小可复现构造，并说明它来自 `llm/openai` 的客户端超时归类、传输层 `*url.Error` 包裹，还是上层派生 context；哪些构造在请求 ctx 仍有效时会真实出现，哪些只是防御性场景。
2. 权威依据与优先级：当 Run 已给出无矛盾的终态 + `ErrorKind` 时，HTTP 映射的唯一权威是什么？`Cause` 中的 ctx 哨兵在什么情况下仍可用于映射（非 `*core.RunError` 的 ctx 错误、会话哨兵、包装错误）？该结论与 `docs/design/http-api.md` §4 现行措辞是否完全兼容，还是需要 Design 文案变更（若需要，属 `Design Impact: UPDATE`，须先交 Owner 批准）。
3. 修复落点：只改 `httpapi` 的错误分类入口是否足以满足 AC-001？若必须同时调整 `llm/openai` 对「客户端自身超时」的分类（当前为 `deadline_exceeded`），请给出最小必要范围、对既有 502/504 语义与现有 `llm/openai` 测试的影响，并请求 Owner 批准。
4. 两条路径的分工：`handleChat` 的直接错误路径（未形成结论）与 `checkedCode` 路径（已形成结论）是否应各自明确「谁可以使用 ctx 哨兵」？是否需要在 `checkedCode` 增加「code 必须与 `ErrorKind` 同源」的自洽校验，还是应把自洽性收敛在唯一映射入口；两种选择对 AC-003 的「矛盾结论不得成功」观察方式有何差异。
5. 会话哨兵与 ctx 的交叉：`session.Manager` 的 ctx 终止错误是 `KindDeadlineExceeded` / `KindCancelled` 且保留 ctx `Cause`，与「请求级超时」语义重合；请确认这类错误与 Provider 超时的区分边界，避免修复后把会话层超时误归为 Provider 故障。
6. `ErrorKind` 之外的兜底：非 `*core.RunError` 的错误（例如基础设施错误、`*url.Error` 直接穿透）应如何映射，是否需要保留 `errors.Is(ctx…)` 的兜底，并说明其与 AC-004 中「直接 ctx 超时/取消」用例的一致性。
7. Session 原子性验证口径：确认用 `Manager.Get` 逐字段比对（消息、Summary、Todo、计数）即足以证明「无半轮历史」，还是需要额外核对 `Turn` 层或日志旁路。
8. 全局资源：确认本任务不涉及 `migration_version` / `error_code_domain` 等 Shared Resource（不新增错误码），`resources.reservations` 保持为空。

## Review Baseline

- Base commit：`0977f243df0348fa5e526b8d954e3f9a2720237d`（`merge: 合并 feat/agent-project-docs-v1 到 develop`）。
- 任务基线核实：任务 Base commit 为 `0977f24`（当时 `origin/develop`）。当前分支 `fix/provider-timeout-classification-v1`（Owner 已预先准备）HEAD 为 `100743a`，由 `94a9c53`（TaskBuilder 任务定义）、`f107fa8`（Analyst A0 Contract 草案）、`100743a`（Analyst 阻塞状态 commit）组成；工作区 clean，local == `origin/fix/provider-timeout-classification-v1`；remote feature 分支已存在（`workflow-check` 出口 Gate 的 `handoffReady` 前提可满足）。
- 任务开始时已有修改：无。`git status --short --untracked-files=all` 为空，无暂存内容、无未跟踪文件。
- 任务分支边界：本任务只负责在 `fix/provider-timeout-classification-v1` 上交付；不得合并/推送 `develop`、不得创建或删除分支、不得改动 `main`。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/provider-timeout-classification-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；生产变更按 Owner D2 只允许出现在 `httpapi/errors.go` 的 `errorCodeOf`，测试变更落在 `httpapi/*_test.go`（如需同链路装配，复用 `httpapi` 包内既有的 `service` 装配方式，不改 `service/**`）；`docs/design/http-api.md` §4 的本次更新由 Analyst 负责；`docs/design/**` 其余内容与 `agent/**`、`llm/**`、`core/**`、`session/**`、`service/**`、`cmd/**` 仍预期零变更。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。
- 保护约束：原 E2E 任务工作区存在未跟踪的 `e2e/` 文件（本工作区未见）；本任务不得清理、覆盖或把这些文件混入提交。

## Initial Route

交 Analyst（COMPLEX）
