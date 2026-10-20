# Task: Provider 超时与请求级超时的错误分类修复（provider-timeout-classification-v1）

## Goal

让「Provider 自身请求超时（请求级 ctx 未到期）」与「HTTP 请求级 ctx 超时」在 HTTP 状态码、协议错误码、Run 终态、`run_finished.error_kind` 四个观测点上互相可区分且与既有映射一致：Provider 超时 → `502` + `provider_failure` + `FAILED`；请求级超时 → `504` + `deadline_exceeded` + `CANCELLED`。同时不改变真正的取消（`cancelled` / `408`）、客户端断开、Provider 4xx/5xx、工具失败与双预算耗尽的既有语义。

## Scope

- 修复 `httpapi` 对 Run 结论的错误分类与协议码映射：权威错误种类（`core.KindOf`）不得被错误 `Cause` 链中的 ctx 终态覆盖；`Runner.Run` 直接返回的 ctx 终态（真正未形成 Run 结论的路径）语义保持不变。
- 在同一判定位置一并核对 `errors.Is(err, context.Canceled)` 的同类误判（`Cause` 链含取消的 provider 失败不得映射为 408），但不得改变真正的客户端断开 / 取消语义（Run 为 `CANCELLED` + `cancelled`）。
- 逐条保持既有映射与优先级：直接 `context.DeadlineExceeded` → 504；直接 `context.Canceled` → 408；会话哨兵（`not_found` / `expired` / `exists`）优先级不变；`RunOutcome.checkedCode` 对自相矛盾结论仍按 `internal`；`provider_failure` → 502；`llm_call_limit` / `tool_call_limit` → 429；`tool_failure` → 500；`invalid_pairing` → 500。
- 在 `httpapi` 包内补齐能区分两种超时的回归测试（单元判定 + 经 handler 的端到端用例），并对失败场景断言 Session 无半轮提交。
- 回归测试落在既有测试文件内（`httpapi/errors_test.go` / `httpapi/handler_test.go`），以维持 `Design Impact = NONE`。

（本任务不启用 Deliverer，故不设 `Milestone` 节：分支内可观察证据为 `httpapi` 回归测试；运行期验收由 `agent-runtime-e2e-tests-v1` 在 `runtime-offline-acceptance-v1` 里程碑下、修复合并进 develop 后重跑承载。）

## Out of Scope

- 不修改 `docs/design/http-api.md`：Owner 已冻结其错误码映射表；`http-api.md:126` 中「`deadline_exceeded` … / 派生 ctx 超时」的措辞歧义只作为风险记录，如需澄清另开任务并由 Owner 授权。
- 不修改 `agent` / `llm/openai` / `service` 的错误分类策略或 `Cause` 保留方式（若 Analyst 判定根因必须落在这些层，回 Owner 决定是否扩大 Scope，不在实现期自行扩张）。
- 不新增错误码、协议字段、公共导出符号；不重构 `errorCodeOf` 之外的结构，不引入无关抽象。
- 不修改也不放宽 `agent-runtime-e2e-tests-v1` 的验收测试；工作区中未跟踪的 `e2e/` 目录不属于本任务，不得暂存、提交、修改、stash 或删除。
- 不把 `-race` 纳入 CI，不修改 `Makefile` / `.cnb.yml` / `.agent/**` / `.agent/engine`；不新增第三方依赖。
- 不处理 `runtime-architecture.md` §7 的语义补写（该 E2E Contract 记录的「Provider 侧超时经 agent 重分类为 502」组合如需写入长期设计，应独立处理）。

## Design Impact

Design Impact: NONE

判定依据：修复把实现拉回 `docs/design/http-api.md` §3 已定映射（`provider_failure` → 502、`deadline_exceeded` → 504、`cancelled` → 408），不新增或修改错误码、公开协议、数据模型或状态机；Run 终态语义本身未变（Provider 超时原本就是 `FAILED` + `provider_failure`，请求级超时原本就是 `CANCELLED` + `deadline_exceeded`），变化只发生在 HTTP 层对既有结论的映射。回归测试限定在既有测试文件内，不触发 Design §12 文件所有权变更。若 Analyst 判定需要把「Provider 超时 → 502 / FAILED」写入长期设计或澄清 `http-api.md:126` 措辞，则升为 `UPDATE` 并回 TaskBuilder 补 Scope / AC。

## Acceptance Criteria

- [ ] AC-001：给定真实 `llm/openai` 客户端指向阻塞不响应的 loopback 服务且 Provider 侧超时先于请求级超时到期，当一次 chat 请求完成时，HTTP 状态码为 `502`、响应体 `error.code` 为 `provider_failure`、Runner 记录的 Run 终态为 `FAILED` + `provider_failure`、`run_finished` 事件的 `status=FAILED` / `error_kind=provider_failure`，四个观测点一致。
- [ ] AC-002：给定请求级 ctx 先到期（Provider 仍阻塞），当该请求结束时，HTTP 状态码为 `504`、`error.code` 为 `deadline_exceeded`、Run 终态为 `CANCELLED` + `deadline_exceeded`、`run_finished` 的 `status=CANCELLED` / `error_kind=deadline_exceeded`，四个观测点一致。
- [ ] AC-003：同一套用例中，AC-001 与 AC-002 两个场景在四个观测点上逐字段不同（不得出现「协议码相同」或「终态互顶」），且两者都不得落到 `internal`；请求级 ctx 仍存活时，`Cause` 链含 `context.Canceled` 的 provider 失败映射为 `502` + `provider_failure`，不落 `408`。
- [ ] AC-004：既有语义逐条不回归：`Runner.Run` 直接返回 `context.DeadlineExceeded` → `504`；直接返回 `context.Canceled` → `408`；会话哨兵优先级不变；`checkedCode` 对状态与错误种类矛盾、负计数、终态缺错误仍返回 `internal`；provider 4xx/5xx → `502`；`llm_call_limit` / `tool_call_limit` → `429`；`tool_failure` → `500`；`invalid_pairing` → `500`；`httpapi` 既有 `TestErrorCodeOf` 与 `TestErrorCodeStatusMapping` 全绿。
- [ ] AC-005：无半轮提交：上述 Provider 失败、请求级超时、取消三类路径返回后，会话 `Messages` / `Summary` / `Todos` 与请求前逐字段相同，且历史仍通过 `core.ValidateMessages`（用 `httpapi` 既有 fake Store / Runner 能力做快照比较）。
- [ ] AC-006：回归测试位于 `httpapi` 包内既有测试文件，且具备判别力：把 `errorCodeOf` 的 ctx 终态判定还原为「先于 `core.KindOf` 检查整条 `Cause` 链」时，新增用例必须失败（提供至少一处可逆 mutation 的 diff 与失败输出）；无 `t.Skip`、无 build tag、无外网访问、不依赖未跟踪的 `e2e/`。
- [ ] AC-007：仓库根 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 退出码 0；本修复未引入新的失败用例；工作区若存在 E2E 任务的未跟踪 `e2e/`，其结果只作观察、不计入本任务证据，且该目录未被提交或修改。

## Relevant Context

- 已核实事实（触发与实测，来自 E2E 任务 Coder 的验收记录）：`httpapi.Config.RequestTimeout=5s`、`openai.Config.Timeout=50ms`、脚本服务阻塞不响应时，HTTP 返回 `504` + `deadline_exceeded`，而 `RunOutcome` 为 `FAILED` + `provider_failure`、`run_finished` 为 `FAILED` / `provider_failure` —— 协议码与 Run 终态自相矛盾。
- 已核实事实（根因链，代码）：`httpapi/run.go:63` 把 `RunOutcome.Err` 交给 `errorCodeOf`；`httpapi/errors.go:173-178` 在 `core.KindOf(err)` 之前对整个 `Cause` 链执行 `errors.Is(err, context.DeadlineExceeded)` / `errors.Is(err, context.Canceled)`；`agent/run.go:230-239` 在请求级 ctx 仍存活时把 Provider 失败重分类为 `provider_failure` 且保留原始 `Cause`；`llm/openai/client.go:156-166` 把 `http.Client.Timeout` 映射为 `KindDeadlineExceeded`，其 `Cause` 为 `*url.Error`（满足 `errors.Is(..., context.DeadlineExceeded)`）。
- 已核实事实（同源路径）：`httpapi/handler.go:118-128` 用 `context.WithTimeout(r.Context(), RequestTimeout)` 派生请求 ctx，且 `Runner.Run` 直接返回的 error 与 `RunOutcome.checkedCode()`（`httpapi/run.go:47-64`）最终都走同一个 `errorCodeOf` 映射。
- 已核实事实（设计依据）：`docs/design/http-api.md:126-131` 的单点映射为 `deadline_exceeded`→504、`cancelled`→408（best-effort）、`provider_failure`→502；§5:141-143 规定请求级超时 = `CANCELLED` + `deadline_exceeded`，客户端断开 = `CANCELLED` + `cancelled` 且不承诺状态码，并要求「取消与超时靠终态错误种类区分」。同一映射表第 126 行把 `deadline_exceeded` 触发条件写作「`core.KindDeadlineExceeded` / 派生 ctx 超时」，与本次行为存在措辞歧义（见 OPEN QUESTION 2）。
- 已核实事实（既有回归入口）：`httpapi/errors_test.go:19` `TestErrorCodeStatusMapping`、`:113` `TestErrorCodeOf`；`httpapi/lifecycle_test.go:234` `TestClientDisconnectCancelsRun`、`:172` `TestShutdownCancelsInflightRunAfterGrace`；`httpapi/handler_test.go` 提供 fake SessionStore / Runner 与请求辅助。
- 已核实事实（工作区与分支）：当前 checkout 为 `feat/agent-runtime-e2e-tests-v1` @ `3a7b857`（= `origin/feat/agent-runtime-e2e-tests-v1`），`origin/develop` = `d54c912`；工作区存在未跟踪目录 `e2e/`（6 个 `*_test.go`，属 `agent-runtime-e2e-tests-v1` 未完成工作），`e2e/` 不在 `develop` 上，不能作为本任务的分支内验收入口。本地与远端均不存在本任务的 feature branch。
- 已核实事实（资源）：`.agent/workflow.yaml` 的 `resources: {}`；本任务只复用既有 `provider_failure` / `deadline_exceeded`，无 migration、无错误码域、无 Shared Resource 需求。
- Assumption：`http.Client.Timeout` 触发的错误在 Go 1.24 下满足 `errors.Is(err, context.DeadlineExceeded)`（E2E Coder 已用独立探针实测确认，修复方案不得依赖该假设之外的未验证行为）。
- OPEN QUESTION（阻塞）：本任务的目标 feature branch 未提供。TaskBuilder 禁止创建 / 重命名分支；在 Owner 准备好分支（从 `develop` 建）之前无法建立独立基线，也无法安全提交任务事实——当前分支属 E2E 任务，且工作区含该任务的未跟踪文件。
- OPEN QUESTION（交 Analyst，不阻塞开工后分析）：`docs/design/http-api.md:126` 的「/ 派生 ctx 超时」是否需要澄清；Owner 已冻结映射表，因此默认只记录风险、不改文档。

## Verification

- AC-001 → `httpapi` 端到端用例：真实 `openai` 客户端 + 阻塞 loopback 服务，Provider 超时先到、请求级超时留足；断言 `status=502`、`error.code=provider_failure`、Runner 记录的终态、`run_finished` 的 `status`/`error_kind` 四者一致。
- AC-002 → 同一装配下把请求级超时压到 Provider 超时之前；断言 `504` + `deadline_exceeded` + `CANCELLED` + `error_kind=deadline_exceeded`。
- AC-003 → 两个用例的结果结构逐字段对比（集合两两不相等）；另加 `core.RunError{Kind: provider_failure, Cause: 包裹 context.Canceled 的错误}` 用例断言 502；断言不出现 `internal`。
- AC-004 → `httpapi/errors_test.go` 表驱动补齐「直接 ctx 错误」「Cause 包裹 ctx 错误的 provider 失败」「六类既有 Kind」行；运行 `go test ./httpapi -run 'TestErrorCodeOf|TestErrorCodeStatusMapping' -v` 全绿；`checkedCode` 矛盾结论用例保持 `internal`；客户端断开 / 关闭宽限用例 `go test ./httpapi -run 'TestClientDisconnectCancelsRun|TestShutdownCancelsInflightRunAfterGrace' -v` 保持通过。
- AC-005 → 失败路径用例在请求前后对 fake Store 中的 `Session{Messages, Summary, Todos}` 做逐字段深比较，并断言 `core.ValidateMessages` 通过。
- AC-006 → 在本地临时还原 `errorCodeOf` 的 ctx-优先顺序（可逆 mutation，不提交），确认新增用例失败并记录输出；恢复后全绿；`grep` 确认无 `t.Skip` / build tag。
- AC-007 → 仓库根依次执行 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 并记录退出码与输出；`git status --short` 确认 `e2e/` 仍为未跟踪且未被修改。
- 环境：Linux + Go 1.24.x，无需外部服务与真实凭据；`GOWORK=off go -C .agent/engine test ./...` 应保持通过（未改动 Engine）。

## Complexity

COMPLEX

原因：修复点虽小，但改动的是公开错误映射的判定优先级，且同一个映射函数同时服务两条不同语义的入口——`Runner.Run` 直接返回的 ctx 终态（请求级超时 / 客户端断开，必须保持 504 / 408）与 `RunOutcome` 的失败结论（权威 Kind 必须胜出）。可选方案会产出不同业务结果：按 `core.KindOf` 优先、按「是否为裸 ctx 错误」分流、或在 `agent` / `llm` 层剥离 / 改写 `Cause`（影响其他消费者与诊断信息）。此外需同时收敛 `context.Canceled` 的同类误判而不破坏真实取消语义，并保持六类既有映射与会话哨兵优先级不变。这些选择需要 Analyst 收敛、Owner 确认，不能由 Coder 在实现期自选。

## Analyst Questions

1. 判定优先级：`errorCodeOf` 应改为「`core.KindOf` 权威优先，仅当无权威 Kind 时才按 ctx 终态兜底」，还是改为「只有裸 ctx 错误（`err == context.DeadlineExceeded` / `errors.Is` 且错误本身不含 `*core.RunError`）才按 ctx 终态」？两种写法对「直接 ctx 错误」与「Cause 链含 ctx 错误的 provider 失败」的区分度与后续可维护性分别如何？
2. 路径分流：`handler.go:128`（Runner 直接返回 error）与 `run.go:63`（`RunOutcome.checkedCode`）是否应共用同一判定，还是各用一套（后者已有 `Status` 可交叉校验 Kind）？如果分流，两边必须共享的不变量是哪几条？
3. 实现落点与形态：是否引入最小内部判定函数（例如把「裸 ctx 错误」识别与「Kind → Code」映射分开），放在 `httpapi/errors.go` 还是 `run.go`？是否需要改动 `checkedCode`？禁止改动的边界（`agent` / `llm` / `service` 的 Cause 保留）如何在 Contract 中固定？
4. 取消语义的边界：`Cause` 链含 `context.Canceled` 的 provider 失败应映射 502（本 AC-003 已要求）；而真实客户端断开时 Run 为 `CANCELLED` + `cancelled` 应仍为 408（best-effort）。请给出可直接写入测试的判别句子，并说明 `httpapi/lifecycle_test.go` 现有用例是否会受影响。
5. 回归测试形状：`errorCodeOf` 表驱动够不够覆盖「四观测点一致」，还是必须补经 handler 的端到端用例（真实 `openai` 客户端 + 阻塞 loopback 服务）？后者应落在 `httpapi/handler_test.go` 还是 `errors_test.go`，如何避免依赖未跟踪的 `e2e/`？
6. `-race` 与时间敏感用例的稳定性：Provider 超时 50ms / 请求超时数秒级参数在 CI 机器上是否可能抖动？可接受的时间参数与断言方式（有界轮询、事件驱动）应如何在 Contract 中固定？
7. Design 边界：`http-api.md:126` 的措辞歧义是否需要在任务内以风险形式记录即可（Owner 冻结映射表），还是必须升级为 `UPDATE` 并回 Owner？请给出与 AC-008 / Design Impact `NONE` 判定一致的结论。

## Review Baseline

- Base commit：UNKNOWN。本任务的 feature branch 未由 Owner 提供；当前 checkout `3a7b857`（`feat/agent-runtime-e2e-tests-v1`）属另一任务，不是本任务基线。已知 `origin/develop` = `d54c912`，预期基线为该分支上的最新 develop 提交（以 Owner 提供的分支创建点为准）。
- 任务开始时已有修改：工作区存在未跟踪目录 `e2e/`（6 个 `*_test.go`，属 E2E 任务未完成工作）。本任务不得 stash、丢弃、覆盖、暂存或提交这些文件；本任务自身的任务文件在当前工作区尚未提交（缺分支）。
- 重叠修改的区分方式：本任务独占 `.agent/tasks/provider-timeout-classification-v1/*`；`httpapi/**` 由 Coder 在独立 commit 修改；`e2e/**` 与 `.agent/tasks/agent-runtime-e2e-tests-v1/**` 属他人工作，不改不提交。若切换分支导致未跟踪文件一同进入新工作区，`e2e/` 必须保持未跟踪且不得被任何 commit 包含；发现他人遗留 dirty 文件 → STOP 并报告。

## Initial Route

BLOCKED（`blocked.active=true`）：缺 Owner 准备的 feature branch，无法建立独立基线。分支就绪后交 Analyst（COMPLEX，`contract.status = PENDING`）。
