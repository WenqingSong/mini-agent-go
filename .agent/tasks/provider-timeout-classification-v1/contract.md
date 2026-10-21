# Technical Contract — Provider 超时与请求级超时的错误分类一致性（provider-timeout-classification-v1）

## Decision Status

APPROVED

- Owner 于 2026-10-21 对 D1–D4 全部作出决定（见 `Owner Decision Record`）。本文件内容即 A1（Contract Evidence Commit）；A2 只写 `state.contract.status=APPROVED`、`state.contract.target=A1`。
- 前置已由 TaskBuilder 完成（commit `53248a4`）：`task.md` 的 `Design Impact` 由 `NONE` 升级为 `UPDATE`，目标 Design Artifact = `docs/design/http-api.md` §4，并纳入 Scope / AC-007 / Review Baseline。本 Contract 与当前 Task 相容。
- `Design Impact = UPDATE`：Analyst 在 A1 之后、Coder 实现之前只更新 `docs/design/http-api.md` §4 的三条判定优先级事实，不改状态码映射表、错误码枚举、对外协议与状态机。
- 历史输入（非当前已批准事实）：A0 草案 commit `f107fa8` 与 `WAITING_FOR_OWNER_DECISION` 状态只保留在 Git 历史中。

## Problem

`/v1/chat` 对「Provider 自身超时」与「请求级超时」的分类不一致：Run 结论已是 `FAILED + provider_failure`（`run_finished` 同源记录），HTTP 却回 `504 + deadline_exceeded`。任务要在不改错误码枚举、状态码映射表、外部协议字段的前提下，让 HTTP `error.code` / 状态码与已确定的 Run 结论同源，并保证请求级超时、客户端取消与 Session 原子性不回归。

## Verified Current Behavior

- VERIFIED：`httpapi/errors.go:159-198` 的 `errorCodeOf` 判定顺序为「会话哨兵（`errors.Is`）→ `errors.Is(err, context.DeadlineExceeded)` → `errors.Is(err, context.Canceled)` → `core.KindOf(err)` → `internal`」，注释（`:156`）也按该顺序写明。ctx 哨兵位于 `core.KindOf` **之前**，且对整条 `Cause` 链生效。
- VERIFIED：`httpapi/run.go:47-64` 的 `checkedCode` 只校验「计数为负 / `COMPLETED` 带错误 / 非终态 / 终态缺错误 / `core.StatusOf(Err) != Status`」，通过后直接 `errorCodeOf(o.Err)`（`:63`）；它不校验 code 与 `ErrorKind` 是否同源，因此 `FAILED + provider_failure` 目前可以被合法写成 `504`。
- VERIFIED：`httpapi/handler.go:118-134` 有两条语义不同的失败路径：Runner 返回 error → `errorCodeOf(err)`（`:126-130`，"未形成结论"）；已形成 `RunOutcome` → `checkedCode()`（`:131-134`）。两条共用同一映射函数。
- VERIFIED：`agent/run.go:230-240` 的 `classifyProviderError` 在请求 ctx 未终止时一律返回 `KindProviderFailure` 并保留 `Cause: err`；ctx 已终止时先返回 ctx 终态。`agent/run.go:115-131` 证明 Provider 失败的归因发生在 Agent Loop，`llm/openai` 的原始 Kind 只作为 `Cause` 保留。
- VERIFIED：`core/error.go:84-89` 的 `RunError.Unwrap` 暴露 `Cause`；`:100-114` 的 `KindOf` 用 `errors.As` 取链中**最外层** `*RunError`（非 RunError 统一 `internal`）；`:120-133` 的 `StatusOf` 由 Kind 推导终态。
- VERIFIED：`llm/openai/client.go:150-172` 对 `http.Client.Do` 失败的处理是「请求 ctx 终态优先 → `net.Error.Timeout()` → `KindDeadlineExceeded`（保留 `Cause: *url.Error`）→ 其余 `provider_failure`」。即「Provider 客户端超时」在 LLM 层是 `deadline_exceeded`，到 Agent 层被重新归因为 `provider_failure`。
- VERIFIED（Go 1.24.1 标准库源码）：`net/http` 的 `Client.Timeout`、响应体读超时都被包装为 `*http.timeoutError`（`client.go:732`、`:979`），该类型实现 `Is(err error) bool { return err == context.DeadlineExceeded }`（`transport.go:2706-2713`）。因此这类 Provider 超时的错误链**确实**满足 `errors.Is(err, context.DeadlineExceeded) == true`，而请求级 ctx 仍然有效。
- VERIFIED（最小复现，仓库外临时探针 `/tmp/probe`，同一份 `session.Manager` + `service.Runner` + `agent.Loop` + `openai.Client` + `httptest` 慢上游装配）：
  - Provider 自身超时（`openai.Config.Timeout=40ms`，上游 150ms 才响应）：HTTP 返回 `504 {"error":{"code":"deadline_exceeded"}}`；同一 Run 的 `run_finished` 为 `status=FAILED error_kind=provider_failure`；`RunOutcome` 为 `FAILED` + `KindOf(Err)=provider_failure`。错误链为 `RunError{agent.Loop.loop: provider_failure}` → `RunError{openai.Client.Complete: deadline_exceeded}` → `*url.Error` → `*http.timeoutError`，且 `errors.Is(Err, context.DeadlineExceeded)=true`。三观测点矛盾，即 AC-001 缺陷。
  - Provider 返回 HTTP 500：`502 provider_failure`，`run_finished` 一致（链中无 ctx 哨兵）。
  - 请求级超时（`RequestTimeout=40ms`，上游 300ms 才响应）：`504 deadline_exceeded`，`run_finished` 为 `CANCELLED / deadline_exceeded`（当前已正确）。
  - Provider 错误链仅含 `context.Canceled`（请求 ctx 有效）：HTTP 返回 `408 cancelled`，而 Run 结论是 `FAILED / provider_failure`（AC-003 禁止的同类缺陷）。
  - Provider 错误链仅含 `context.DeadlineExceeded`（请求 ctx 有效）：HTTP 返回 `504`，Run 结论 `FAILED / provider_failure`（同上）。
- VERIFIED：`runlog/slog.go:96` 的 `status` 与 `:100` 的 `error_kind`（`core.KindOf(ev.Error)`）都取自 `agent/agent.go:132-139` 发出的 `run_finished`（`Status: result.Status`、`Error: result.Error`）。因此 Run 结论与日志天然同源，只有 HTTP 映射是独立的第三方观测点。
- VERIFIED：`session/manager.go:181-221` 的 `RunTurn` 只在「ctx 未取消 + 回调返回 nil + Turn 仍打开 + entry 仍是当前 + 提交前校验通过」时整体发布；失败、取消、超时、panic 整轮丢弃（`runlog` 等旁路不参与裁决）。因此「超时无半轮历史」的机制已存在，本任务只需证明它在这两类超时下成立。
- VERIFIED：`cmd/mini-agent/main.go:269-273` 的生产装配 `openai.Config` 只设置 `BaseURL` 与 `APIKey`，**未设置** `Timeout`，也未注入 `HTTPClient`；即默认装配下 `http.Client.Timeout` 不生效，`http.Transport` 默认 `ResponseHeaderTimeout=0`。
- VERIFIED：`docs/design/http-api.md:126` 把 `deadline_exceeded/504` 的依据写成「`core.KindDeadlineExceeded` / 派生 ctx 超时」，`:128` 写「`provider_failure` ← `core.KindProviderFailure`」，`:135` 写判定优先级为「会话哨兵 → `core.KindOf` → `internal`」，没有 ctx 哨兵层。实现中多出的一层 ctx 哨兵优先正是本次不一致来源；但文档也没有写明「无 `*core.RunError` 的裸 ctx 错误如何兜底」。
- VERIFIED：测试资产现状——`httpapi/fake_test.go:153` 的 `scriptedProvider` 只回放预设响应、不能注入错误；`httpapi/handler_test.go:418` 的 `TestRealRunnerEndToEnd` 已是「真实 `session.Manager` + 真实 `service.Runner` + 真实 `agent.Loop` + `httptest`」的全链路装配；`httpapi/errors_test.go:112-173` 的 `TestErrorCodeOf` 已覆盖裸 `context.Canceled`/`context.DeadlineExceeded` → 408/504；`httpapi/lifecycle_test.go:293-322` 已覆盖请求级超时 → 504。
- INFERENCE：默认生产装配下「Provider 自身超时」主要来自传输层（连接/握手/上游错误）与上游返回的非 2xx；这类链通常**不含** ctx 哨兵，因此当前已映射为 502。能产生「ctx 哨兵 + 请求 ctx 有效」的现实途径是：注入带 `Timeout`/`ResponseHeaderTimeout` 的 `HTTPClient`、Provider 实现自行派生 deadline、或错误链被上游库包入 `context.DeadlineExceeded`。默认装配下的 TLS 握手超时（`tlsHandshakeTimeoutError`）虽有 `Timeout()==true`，但没有 `Is`，落到 `provider_failure`。
- UNKNOWN：Owner 现场触发链的具体成因（是否使用了带 `Timeout` 的 `HTTPClient`、或 Provider 侧派生 deadline）。不影响修复方案，只影响该缺陷在默认装配下的触发频率。

## Recommendation

RECOMMENDATION（Owner 2026-10-21 已全部批准，见 `Owner Decision Record`）：把「已形成的 Run 结论」提升为唯一映射权威，修复只落在 `httpapi/errors.go` 的单一映射入口 `errorCodeOf`，判定顺序改为：

1. 会话哨兵（`errors.Is`，保持最高优先级，覆盖 404/409）；
2. 若错误链中存在 `*core.RunError`（`errors.As` 命中），则只按其**最外层** `Kind` 映射（`KindOf`），`Cause` 中的 `context.DeadlineExceeded` / `context.Canceled` **不得**改变结果；
3. 仅当链中不存在 `*core.RunError` 时，才用 `errors.Is(ctx…)` 兜底映射 `504` / `408`（保留 AC-004 的「直接 ctx 超时/取消」语义）；
4. 其余 → `internal`。

不改 `checkedCode` 的既有校验集合，不改 `llm/openai`、`agent`、`core`、`session`、`service` 的任何生产代码，不改错误码枚举与状态码映射表。

关键取舍：

- 选「唯一映射入口收敛」而不是「在 `checkedCode` 增加 code 与 `ErrorKind` 同源校验」：后者会让同一权威在两处重复实现，且直接错误路径（`handler.go:126-130`）仍需另一套规则，漂移风险更高；前者一处改动同时覆盖两条路径，`checkedCode` 继续只负责「结论是否自相矛盾」。代价是必须显式区分「链中有 RunError」与「链中没有」，不能只依赖 `KindOf`（它对非 RunError 也返回 `internal`）。
- 不选「把 `llm/openai` 的客户端超时改判为 `provider_failure`」：`agent.classifyProviderError` 已经把它重新归因为 `provider_failure`，LLM 层语义（ctx/客户端超时 → `deadline_exceeded`）与其既有测试（`llm/openai/client_test.go:705`）无关本缺陷；改它会扩大爆炸半径且无法覆盖「Provider 错误链含 ctx 哨兵」这一类防御性场景（AC-003）。
- 不选「删掉 ctx 兜底，全部按 `KindOf`」：会破坏 AC-004 的裸 `context.Canceled` / `context.DeadlineExceeded` 直传映射（`httpapi/handler_test.go:230-239`、`errors_test.go:159-160`）。

失败时系统表现：映射层不再有「结论与 code 混搭」的可能；非终态、终态缺错误、`COMPLETED` 带错误、`Status != StatusOf(Err)`、负计数仍由 `checkedCode` 拒绝为 `internal`；请求级超时、客户端取消、会话哨兵、Provider 4xx/429/5xx、工具失败、预算耗尽、结束语义异常的映射全部不变。

需要什么证据：分类表驱动测试（映射入口）+ 全链路测试（真实装配下的两类超时、三观测点一致、Session 逐字段不变）+ 可逆 Mutation 证明新测试能失败。

## Selected Design

Owner 2026-10-21 已确认 D1–D4（逐条记录见 `Owner Decision Record`）。Coder 只能在下列约束内实现，不得自行决定公开协议或扩大范围。

- **D1（权威依据与优先级，APPROVED）**：`errorCodeOf` 的判定顺序固定为
  1. 会话哨兵（`errors.Is`，最高优先级，覆盖 `404` / `409`）；
  2. 错误链中存在 `*core.RunError` 时，只按其**最外层** `Kind` 映射；
  3. 链中不存在 `*core.RunError` 时，才用裸 Context 哨兵兜底（`504` / `408`）；
  4. 其余 → `internal`。
  已形成的 `RunError` 分类不得被 `Cause` 中的 `context.Canceled` / `context.DeadlineExceeded` 覆盖。实现必须显式区分「链中有 RunError」与「链中没有」，不能只依赖 `core.KindOf`（它对非 RunError 也返回 `internal`）。
- **D2（修复落点，APPROVED）**：生产代码原则上只改 `httpapi/errors.go` 的 `errorCodeOf`（含函数注释）；`checkedCode` 的校验项不增不减（不新增 code/Kind 副本校验），`agent` / `llm`（含 `llm/openai`）/ `core` / `session` / `service` / `cmd` 零改动。必须用测试证明：直接错误路径、`RunOutcome` 路径、会话哨兵优先级、既有矛盾状态检测均无回归。若单点修复无法满足已批准 AC，停止并提交证据，不得自行扩大范围。
- **D3（Design Impact，APPROVED = UPDATE）**：`task.md` 已声明 `Design Impact: UPDATE`（commit `53248a4`）。Analyst 在本 Contract 登记 `APPROVED` 后、Coder 实现前只更新 `docs/design/http-api.md` §4，且只记录三条事实：已有 `RunError.Kind` 是 HTTP 错误分类的权威依据；底层 `Cause` 中的 Context 哨兵不能覆盖已形成的分类；没有 `RunError` 时 Context 哨兵作为兜底。不修改既有 HTTP 状态码映射表、协议与状态机。
- **D4（Provider 超时配置边界，APPROVED）**：不修改 `cmd/mini-agent`，不新增 Provider `Timeout` 配置，不调整生产默认超时策略。
- 本次批准不授权提前修改生产代码，也不授权合并到 `develop`。

## Interfaces and Data

- 不新增/不修改任何导出符号：`ErrorCode` 封闭枚举、`errorStatuses`、`writeError`、`RunOutcome`、`checkedCode`、`SessionStore`、`Runner`、`RunRequest` 全部保持现状。
- `errorCodeOf(err error) ErrorCode` 签名不变，只在函数体内调整判定顺序并同步注释（`:154-158`）。
- 事实来源与依赖方向不变：`httpapi` 生产代码仍只依赖标准库 + `core` + `session`。
- 测试辅助：允许在 `httpapi` 外部测试包（`package httpapi_test`）新增「可注入错误的 Provider 替身」，并允许该测试文件 import `llm/openai`（与现有 `httpapi/fake_test.go` 已 import `llm` / `tool` 的既有做法一致），用于构造**真实** Provider 超时错误链（`*url.Error` + `*http.timeoutError`）。该 import 仅存在于测试，不改变生产依赖方向。
- 不新增 Shared Resource：`error_code_domain` 等 Registry 均不涉及（错误码是协议封闭枚举，见 `docs/design/http-api.md:110`）；`.agent/workflow.yaml` 的 `resources` 保持 `{}`，`state.resources.reservations` 保持空。

## Business Invariants

- INV-001（结论优先）：当 Run 已形成终态且 `Status == StatusOf(Err)` 时，HTTP `error.code` / 状态码只由 `Err.Kind` 决定；`Err.Cause` 中的 `context.DeadlineExceeded` / `context.Canceled` 不得改变映射结果。→ 验证：映射入口表驱动测试 + 全链路测试。
- INV-002（Provider 自身失败）：请求级 ctx 未到期时，任何 Provider 失败（含链中带 ctx 哨兵的客户端/传输超时）形成 `FAILED + provider_failure`，HTTP 必须为 `502 provider_failure`，`run_finished` 必须为 `status=FAILED`、`error_kind=provider_failure`。→ 验证：全链路测试（至少「链含 `context.DeadlineExceeded`」与「链不含 ctx 哨兵」两种构造）。
- INV-003（请求级超时）：请求级 ctx 到期时结论为 `CANCELLED + deadline_exceeded`，HTTP 必须为 `504 deadline_exceeded`，不得为 `provider_failure` / `cancelled` / `internal`。→ 验证：全链路测试（Provider 已发出调用、以及尚未发起调用两种）。
- INV-004（原子性）：Provider 自身超时与请求级超时后，`session.Manager.Get` 读到的 `Messages` / `Summary` / `Todos` 与请求前逐字段相等，且历史仍通过 `core.ValidateMessages`。→ 验证：预置基线后比对快照。
- INV-005（优先级不被削弱）：会话哨兵仍最高（`404 session_not_found` / `404 session_expired` / `409 session_exists`）；自相矛盾结论（非终态、终态缺错误、`COMPLETED` 带错误、`Status != StatusOf(Err)`、负计数）仍为 `internal` 且非 2xx。→ 验证：既有 `TestErrorCodeOf` / outcome 表用例保持通过，并补充交叉用例。

## Failure and Consistency Semantics

- 唯一存储是进程内 `session.Manager`，不涉及 Redis/MQ/外部持久化，无最终一致性问题。
- `200` 只在 `COMPLETED` 且无错误、且会话提交已发生时返回；`502` / `504` / `408` / `500` / `429` / `404` / `409` 都表示该轮未提交，Session 与请求前逐字段一致。
- `Runner.Run` 返回 error 表示「未形成 Run 结论」（会话哨兵、ctx 终态、基础设施失败）；此时不存在 `run_finished` 事件，AC-002 的日志断言只适用于 Run 已形成的情形（例如 ctx 在取得执行权后、模型调用前后到期）。Coder 的断言不得要求「未形成 Run」的场景也有 `run_finished`。
- 请求级超时与 Provider 自身超时若同时发生（ctx 已到期），以 ctx 终态为准（`agent/run.go:231-233`），即 `deadline_exceeded`；本修复不改变该歧义边界。
- 客户端断开：内部结论为 `CANCELLED + cancelled`，未提交；`408` 仅是连接仍可写时的 best-effort，不承诺可达。
- 无重试、无补偿、无自动回滚；日志 Sink 失败不影响响应、Run 状态与计数。
- 重复请求：无幂等键，每次 chat 各自成轮；同会话并发由执行权串行。

## Allowed / Forbidden Changes

允许：

- `httpapi/errors.go`：调整 `errorCodeOf` 判定顺序（含函数注释），语义即 INV-001。
- `httpapi/errors_test.go`、`httpapi/handler_test.go`、`httpapi/lifecycle_test.go`、`httpapi/fake_test.go`：新增/补充分类表驱动用例、全链路用例、可注入错误的 Provider 替身、Session 快照与 `run_finished` 断言。
- `docs/design/http-api.md` §4：D3 已批准为 `UPDATE`，由 Analyst 在 Contract `APPROVED`（A1）之后、Coder 实现前补充三条优先级事实；该变更属 Analyst 交付物，Coder 的 `review.target` 不得包含 Design 改动，也不得再修改 Design。

禁止：

- 修改 `httpapi/run.go` 的 `checkedCode` 校验集合（可改注释，不得削弱或新增校验分支）。
- 修改 `ErrorCode` 枚举、`errorStatuses` 映射、错误 `message` 文案、`RunOutcome` / `Runner` / `SessionStore` 端口。
- 修改 `agent/**`、`llm/**`（含 `llm/openai`）、`core/**`、`session/**`、`contextmgr/**`、`service/**`、`runlog/**`、`builtin/**` 的生产代码。
- 修改 `cmd/mini-agent`、新增环境变量或 Provider `Timeout` 配置（D4）。
- 修改 `.agent/engine/**`、`.agent/workflow.yaml`、`.agent/registry/**`。
- 触碰其他任务的 `e2e/` 未跟踪文件；禁止 `git add -A` / `git add .`；不清理、不 stash 他人修改。
- 以修改既有测试期望值的方式「修复」回归（如把 `TestErrorCodeOf` 的裸 ctx 用例改成 502）。

## Verification Requirements

- INV-001 / AC-003 → `httpapi` 映射入口表驱动测试，至少覆盖：
  - `provider_failure` + `Cause=context.DeadlineExceeded` → `502`；
  - `provider_failure` + `Cause=context.Canceled` → `502`；
  - `provider_failure` 且 `Cause` 为真实 `*url.Error` → `*http.timeoutError` 链 → `502`；
  - `deadline_exceeded` / `cancelled` 终态（`Cause` 为对应 ctx 哨兵）→ `504` / `408`；
  - 会话哨兵包装（含包在 `RunError` 内）→ `404` / `409`；
  - 裸 `context.DeadlineExceeded` / `context.Canceled`（链中无 RunError）→ `504` / `408`；
  - 裸非 RunError 普通错误 → `internal`；
  - 矛盾结论五类 → `internal` 且非 2xx。
  断言对象是协议层输出（HTTP 状态码 + `error.code`），不是 `errorCodeOf` 返回值之外的内部状态。
- INV-002 / AC-001 → 全链路测试（现有 `handler_test.go:418` 的装配方式 + 记录型 `runlog.Sink`）：Provider 自身超时且请求 ctx 有效，断言 `502` + `provider_failure`、`run_finished.status=FAILED` + `error_kind=provider_failure`、`RunOutcome.Status=FAILED` + `KindOf(Err)=provider_failure`。至少两种构造：链含 `context.DeadlineExceeded`（建议用 `openai.Client` + `Timeout` + 慢 `httptest` 上游，得到真实 `*http.timeoutError` 链），链不含 ctx 哨兵。
- INV-003 / AC-002 → 全链路测试：`RequestTimeout` 极短（或派生可取消 ctx），分别覆盖 Provider 已发出调用与尚未发起调用；断言 `504` + `deadline_exceeded`、`CANCELLED` + `deadline_exceeded`，以及在 Run 已形成时 `run_finished.error_kind=deadline_exceeded`。
- INV-004 / AC-005 → 请求前用 `Manager.Save` 预置带 `Messages` / `Summary` / `Todos` 的基线，两类超时各触发一次，请求后用 `Manager.Get` 逐字段比对（消息逐条、Summary、Todos），并断言 `core.ValidateMessages` 通过。
- INV-005 / AC-004 → 复用既有用例：裸 ctx 408/504（`errors_test.go:159-160`、`handler_test.go:230-239`）、请求级超时 504（`lifecycle_test.go:293`）、Provider 4xx/429/5xx → 502、会话三类哨兵（含预检不执行 Run、零写入）、`checkedCode` 矛盾拒绝、工具失败/重复 `CallID`、LLM/工具预算、`finish_reason` 三类异常。当前无覆盖的情形补最小用例。
- AC-006 → 可逆 Mutation：临时把 `errorCodeOf` 改回「ctx 哨兵先于 Kind」，确认 INV-001/002 的新测试失败；恢复后全绿；不得把 Mutation 痕迹带入提交。命令：`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`、`make test` 均退出码 0。
- AC-007 → Cleaner 核对 `review.target` 完整 diff 路径集合是否只落在 Allowed 清单；确认 `agent`/`llm`/`core`/`session`/`service`/`cmd`/`.agent/engine` 零变更，无 `e2e/` 或其他任务文件混入；并核对 `docs/design/http-api.md` §4（Analyst 在 A1 之后落库）只含三条优先级事实、状态码映射表与错误码枚举未被改动，实现与该节一致。

## Open Risks

- 默认生产装配未设置 Provider `Timeout`（`cmd/mini-agent/main.go:269-273`），因此「Provider 自身超时 + 链含 ctx 哨兵」在默认配置下的可达途径有限（注入 `HTTPClient`、Provider 自派生 deadline、上游库包裹 ctx 哨兵）。本修复纠正的是**分类规则**（对上述任意来源都成立），不改变可达性；若期望生产默认具备 Provider 级超时，需另开任务（D4）。
- 直接错误路径若收到非 `*core.RunError` 的裸 `*url.Error`（当前装配不存在），其 ctx 哨兵仍会把 Provider 超时映射为 504；本任务不覆盖该形状，避免为不存在路径引入推断规则。
- `KindInvalidArgument` 携带 ctx `Cause` 时，映射将从 504 变为 `internal`。这是「结论优先」的直接推论，更符合 `docs/design/http-api.md:135`；Owner D1 已批准该优先级且未设例外，Coder 不得为该形状保留 ctx 覆盖。
- D3 已批准为 `UPDATE`：`docs/design/http-api.md` §4 将由 Analyst 在 A1 之后记录该优先级（三条事实）。Cleaner 需按 AC-007 核对 Task ↔ Contract ↔ Design §4 ↔ 实现四者一致。
- 全链路测试需要真实 `openai.Client` + `httptest` 慢上游；若 CI 环境对毫秒级超时敏感，需用足够的时间余量（例如上游 200ms / 客户端 50ms）避免 flaky，且不得依赖外网。
- 请求级超时测试依赖 `RequestTimeout` 极短值，需避免与 Session 预检、runner 装配竞争导致偶发 502/500；建议通过可取消的请求 context 控制触发点。

## Owner Decision Record

Owner 于 2026-10-21 针对 Contract 草案 `f107fa8` 作出决定：**APPROVE D1 / D2 / D3 / D4**。

- **D1 — APPROVED**：采用「会话哨兵 → 链中最外层 `*core.RunError.Kind` → 裸 Context 哨兵兜底 → `internal`」的分类优先级；已形成的 `RunError` 分类不得被 `Cause` 中的 `context.Canceled` / `context.DeadlineExceeded` 覆盖。适用范围：`httpapi` 错误分类入口的全部路径（直接错误路径与 `RunOutcome` 路径）。
- **D2 — APPROVED**：生产代码原则上只修改 `httpapi/errors.go` 的 `errorCodeOf`，不修改 `checkedCode`、Agent、LLM、Core 或 Session；但必须通过测试证明直接错误路径、`RunOutcome` 路径、会话哨兵优先级以及既有矛盾状态检测均无回归。若单点修复无法满足已批准 AC，停止并提交证据，不得自行扩大范围。
- **D3 — APPROVED**：`Design Impact` 由 `NONE` 正式升级为 `UPDATE`；先由 TaskBuilder 更新任务定义（已完成，`53248a4`），再由 Analyst 对齐 Contract 并在批准范围内更新 `docs/design/http-api.md` §4；该节只记录三条事实（`RunError.Kind` 为权威依据、`Cause` 中 Context 哨兵不得覆盖、无 `RunError` 时 Context 哨兵兜底），不修改既有 HTTP 状态码映射表、协议和状态机。
- **D4 — APPROVED**：不修改 `cmd/mini-agent`，不新增 Provider `Timeout` 配置，也不调整生产默认超时策略。范围：仅本任务；生产 Provider 级超时若需要，属后续独立任务。
- 实施要求（Owner 原文要点）：保持最小修复范围；重点验证 Provider 超时 `502 / provider_failure / FAILED`、请求级超时 `504 / deadline_exceeded / CANCELLED`、Provider `Cause` 含 Context 哨兵不被误分类、既有会话错误/直接取消/直接超时/Provider 错误/`checkedCode` 行为不回归、Session 无半轮提交、回归测试具有实际判别力。
- 流程要求：Contract 与 Design 流程完成、Owner 批准状态有效且 `coder-start` Gate `PASS` 后，才交 Coder 实现；本次批准不授权提前修改生产代码，也不授权合并到 `develop`。
