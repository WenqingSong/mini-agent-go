# Task: 全系统离线集成测试与可靠性验收（agent-runtime-e2e-tests-v1）

## Goal

在进程内、完全离线的条件下，为已装配的 Agent Runtime 全链路（HTTP → `service` → `agent` → `llm/openai` 客户端 → 真实内置工具 → `session`）提供可重复运行的自动化验收证据：多工具连续执行、跨请求对话记忆、Session 隔离、失败/超时/取消/次数限制的真实错误语义、并发与 `-race` 干净；同时如实记录本次验收暴露的真实失败与现存缺口，整个验收过程消耗的真实 LLM API Token 为零。

## Scope

- 建立进程内离线验收装配：真实 `httpapi.New` + `service.NewRunner` + `agent.New` + 真实 `session.Manager` + 真实 `tool.MapRegistry` + 真实 `builtin` 工具（`NewCalculator` / `NewMockSearch`）+ 真实 `llm/openai.NewClient` 指向本地 scripted OpenAI 兼容服务；`runlog.Sink` 使用 `runlog.NewJSONSink` 捕获事件用于断言（不新增生产导出符号）。
- Scripted LLM：以确定性脚本驱动 `finish_reason`、`tool_calls`（单轮多调用、多轮连续调用）、`stop` 终稿、错误响应（4xx/5xx）、无响应/慢响应；脚本可断言每次收到的请求消息序列（含历史与工具结果回填）。
- 全链路验收用例：
  - 单请求内多个真实工具连续执行，工具结果回填后模型继续推理并给出终稿；
  - 同一 Session 跨请求对话记忆；不同 Session 的历史隔离（双向快照对比）；
  - 可恢复工具失败（领域错误、未知工具、参数非法）继续运行；不可恢复失败（模型失败、整体超时、取消）终止且不提交半轮；
  - `MaxLLMCalls` / `MaxToolCalls` 耗尽、HTTP 请求超时、客户端断开取消的真实错误码与 Run 终态；
  - HTTP 层并发（同 Session 并发请求、跨 Session 并发、并发工具调用）在 `-race` 下无告警。
- 全量与静态检查：`go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...`；时间敏感用例的重复执行稳定性。
- 验收记录：逐条 AC 的测试入口与可复核命令、真实失败、被跳过的检查、现存缺口与最终验收结论；未通过项不得写成 PASS。
- 更新 Design Artifact `docs/design/runtime-architecture.md` 的 §12 文件所有权表（本任务新增/变更的路径必须登记）；如 Contract 判定需要，按最小范围收窄 §14 归属条目。

## Out of Scope

- 为让验收通过而修改生产行为或放宽测试期望：不得放开 `service.Config.TokenBudget != 0` 的拒绝、不得在 v1 生产装配中注册 Todo 工具、不得改动 `docs/design/http-api.md` 已定错误码映射。发现真实实现缺陷时按 V3 流程交由 Coder 修复并复审，新能力需求回 Owner 决定。
- 服务装配任务 T08 的内容：`session.TodoOperations` → `builtin.TodoOperations` 适配、Todo ID 生成与状态迁移、生产 LLM `Summarizer` 适配器、真实 tokenizer 校准。本任务不实现这些生产代码（见 Relevant Context 的阻塞项）。
- 真实 LLM Provider 联调、任何真实 Token 消耗、流式输出、Provider 重试/限流、真实网络异常注入。
- 修改 `Makefile` / `.cnb.yml` 把 `-race` 纳入 CI（共享工程文件，需 Owner 授权）；本任务只运行 `-race` 并如实记录结果。
- Session 持久化（DB / Redis）、进程重启恢复、多进程部署、Docker、压测与容量评估。
- 扩大 HTTP 协议（新端点/字段/错误码）、变更 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `agent` 的公共符号。
- 引入第三方依赖或测试框架（根模块维持零 `require`）；修改 `.agent/engine`（独立 Module）、`.agent/workflow.yaml`、`.agent/registry/*`。
- 并行工具执行、多 step 重试策略等尚未由 Design 定义的运行时能力。

## Milestone

Milestone: `runtime-offline-acceptance-v1`

本任务启用 Deliverer；Deliverer 在此里程碑下做离线运行验收，`delivery.md` 记录环境、逐 AC 结果、未验证项与剩余风险。

## Design Impact

Design Impact: UPDATE

Design Artifact: `docs/design/runtime-architecture.md`（§12 文件所有权表登记本任务新增/变更文件；必要时按 Contract 最小收窄 §14 归属）

判定依据：Design §12 规则明确「实现确需新增或重命名文件时，必须在同一交付内回写本表；表内路径与实际 `git ls-files` 不一致视为 AC-006 失败」，而 §12 表内同时逐条列出各包测试文件。本任务预计新增验收测试文件（落点由 Analyst 在 Contract 中定稿），因此会改变项目级文件所有权事实。Owner 需求标注 `Design Impact: NONE`，与此判定不一致；若 Owner 在 Contract Decision 前坚持 `NONE`，则需同时约束「不得新增文件、只在既有测试文件内扩展」，届时 AC-008 与 Design 相关 Scope 回退。

## Acceptance Criteria

- [ ] AC-001：给定进程内离线装配（真实 `httpapi.New` / `service.NewRunner` / `agent.New` / 真实 `session.Manager` / 真实 `builtin` 工具 / 真实 `llm/openai.Client` 指向本地 scripted OpenAI 兼容服务），当一次 chat 请求中模型连续两轮返回 `tool_calls`（至少覆盖 `calculator` 与 `mock_search`）并最终返回不含 tool_calls 的 `stop` 文本时，则响应成功且 reply 等于终稿文本；两个工具真实执行（计算结果与 mock 数据集命中可按 `builtin` 语义核对）；Session 中提交的消息为 user / assistant(tool_calls) / tool×N / assistant 终稿且满足 `core.ValidateMessages`；scripted 服务第 N 次请求包含前一轮全部工具结果，`CallID` 一一对应。
- [ ] AC-002：给定同一 Session 的第二次请求，当 scripted 服务收到请求时，其消息序列包含第一轮 user 与 assistant 原文；给定另一个 Session 的请求，其消息序列不含第一个 Session 的任何内容；当一个 Session 完成运行后，另一个 Session 的 `Messages` / `Summary` / `Todos` 快照逐字段不变（A→B 与 B→A 两个方向均验证）。
- [ ] AC-003：当工具返回可恢复失败（`calculator` 除零、未知工具名、参数非法）时，错误结果作为 tool 消息回填、下一次模型请求可见该错误内容，Run 可在后续正常结束并成功提交；当 context 被取消或整体超时时，Run 以 `cancelled` / `deadline_exceeded` 终止且不再发起新的模型调用，HTTP 状态码与错误码与 `docs/design/http-api.md` 已定义映射一致，且 Session 不留下半轮（提交前后逐字段相同）。
- [ ] AC-004：对 provider 4xx、provider 5xx、provider 无响应、HTTP 请求超时、客户端断开取消、`MaxLLMCalls` 耗尽、`MaxToolCalls` 耗尽分别构造用例，每次运行都得到与设计一致的「HTTP 状态码 + 错误码 + Run 终态」组合；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 按语义区分，不得互相混同；所有失败场景都验证 Session 无半轮提交。
- [ ] AC-005：整个验收在无外网、无真实凭据条件下完成（不设置指向真实服务的 `LLM_BASE_URL` / `LLM_API_KEY`），全部模型交互来自 `httptest` loopback 服务；交付记录给出机械证据（测试源码中无真实 Provider 域名常量、scripted 服务实际收到的请求次数与脚本预期一致），整轮验收不产生任何真实 LLM API Token 消耗。
- [ ] AC-006：仓库根 `go test ./...`、`go test -race ./...`、`go vet ./...`、`go build ./...` 退出码均为 0；新增并发用例（同 Session 并发请求、跨 Session 并发、并发工具调用）在 `-race` 下无告警；时间敏感用例重复执行（如 `-count=3`）结果稳定；不存在 `t.Skip`、build tag 排除或依赖外网的用例。
- [ ] AC-007：任务交付记录逐条列出每个 AC 对应的测试入口与可复核命令；核心结论由业务字段、Session 存储状态或运行事件内容断言，不存在只断言 HTTP 200 或仅断言替身调用次数的核心用例；本次运行中未通过项、被跳过的检查与现存缺口（如 v1 不注册 Todo 工具、`TokenBudget=0` 不压缩、`-race` 未入 CI）逐项写明且不得标为 PASS。
- [ ] AC-008：`docs/design/runtime-architecture.md` §12 中与本任务相关的路径集合与 `git ls-files` 双向差集为空；若 Contract 判定需要收窄 §14 归属，则被收窄条目与本任务结论一致、无悬空或双重归属；除 §12（及经 Contract 确认的 §14）外未改动项目级长期事实。

## Relevant Context

- 已核实事实：当前分支 `feat/agent-runtime-e2e-tests-v1` 位于 `d54c912`（`merge: 合并 feat/agent-http-api-v1 到 develop`），与本地 `develop`、`origin/develop`、`origin/feat/agent-runtime-e2e-tests-v1` 一致；`git status --short` 为空。基线 `go build ./...`、`go vet ./...`、`go test ./...` 全绿（12 个包，零 `require`）。
- 已核实事实：已存在跨层装配式测试 `httpapi/handler_test.go`（`TestRealRunnerEndToEnd`，真实 `httpapi` + `service.NewRunner` + `agent` + `session.Manager` + `tool`），说明全链路可在进程内组装；但其 Provider 与 Tool 均为该包 `_test.go` 内私有替身，未覆盖真实 `builtin` 工具、`llm/openai.Client`、压缩与 `runlog` 断言。
- 已核实事实：仓库内共 4 份互不复用的 scripted provider 私有替身（`agent/fake_test.go`、`service/runner_test.go`、`httpapi/fake_test.go`、`cmd/mini-agent/main_test.go` 的 `fakeOpenAI`），跨包不可引用；`llm.Provider` 只有接口定义，无可 `new` 的 fake。
- 已核实事实：`llm/openai.NewClient` 可指向 `httptest` 假 OpenAI（`cmd/mini-agent/main_test.go` 的 `fakeOpenAI` 为现成先例），因此「真实 LLM Client + scripted 服务」在本仓库已被证明可行。
- 已核实事实：`service.Config.validate` 明确拒绝 `TokenBudget != 0`（`service/runner.go:90-92`，注释「v1 禁用上下文压缩」），`service.Deps.Summarizer` 注释为「仅供测试注入，生产装配必须留空」（`service/runner.go:117-118`）；`cmd/mini-agent/main.go` 的 `newRegistry` 只注册 `calculator`，注释注明 Todo 端口适配属服务装配任务 T08。因此经 HTTP 触发上下文压缩与 Todo 端到端行为在 v1 装配下不可达。
- 已核实事实：Design §14 明确记录 `builtin` 的真实会话隔离与 Todo 生命周期归服务装配任务 T08（`runtime-architecture.md:383-384`、`:404`），并记录「v1 服务禁用上下文压缩且不注册 Todo 工具……属既定边界而非缺陷」（`:390`）；`-race` 未进入 CI 亦已记录（`:387`）。
- 已核实事实：`session.Manager` 无可注入 Clock（仅 `ManagerConfig.Now`）、无会话/消息容量上限；`httpapi` 无并发上限与时钟注入，`RequestTimeout` 依赖真实计时。超时/取消用例的确定性需要显式处理。
- 已核实事实：`Makefile` 只有 `test` / `build` / `vet` / `test-engine`，`.cnb.yml` 的 `pr-check` 只执行 `make test`；仓库无 `e2e` / `integration` 命名测试与构建 tag（`.agent/engine` 的 `git_integration_test.go` 属独立 Module，不在根模块 `./...` 内）。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，本任务不占用任何全局资源（无 migration、无错误码域）。
- Assumption：验收套件以新增测试文件交付（落点由 Analyst 定稿），因此触发 Design §12 回写；若 Owner 要求不新增文件，则 Design Impact 可回到 `NONE`，但套件可维护性与可追溯性下降。
- Assumption：取消语义可用「客户端断开 / context 取消」在进程内证明，不需要真实网络中断或故障注入中间件。
- Assumption：「零真实 Token」用「全部 endpoint 来自 loopback `httptest` + 不注入真实密钥 + 请求计数与脚本一致」三条机械证据证明，不依赖外部审计。
- OPEN QUESTION（阻塞，需 Owner 决定）：Owner 的实施内容要求验证「不同 Session 的 Todo 隔离」与「长历史压缩与工具消息配对」，但上述两项在 v1 装配下不可经 HTTP 触发（见上）。可选处置：
  - A：先交付服务装配任务（Todo 端口适配 + 生产 `Summarizer` + `TokenBudget` 接线），本任务随后按 HTTP 全链路验收；本任务 AC 保持 Owner 原意，但需等待前置任务。
  - B：本任务按现有 v1 装配执行，Todo 隔离与长历史压缩降级为 `agent` + `session` 层验收（真实 `agent.Loop` + 真实 `session.Manager` + 真实工具 + 可注入 `Summarizer`，不新增生产代码），交付记录中明确标注「HTTP 层不可达，归服务装配任务」，并补充为 AC-009 / AC-010。
  - C：把服务装配并入本任务（Scope 从「测试验收」扩大为「装配 + 验收」），需重判 Complexity / Design Impact，并重写 AC 与 Deliverer 验收对象。
- OPEN QUESTION（不阻塞执行，需 Owner 校正）：Owner 需求把本任务称为「Task 09」、前置「T08」、并行「T10」，但仓库既有编号为 T00 `runtime-core-contracts-v1`、T01 `agent-runtime-loop-v1`、T02 `llm-openai-client-v1`、T03 `builtin-agent-tools-v1`、T04 `agent-session-manager-v1`、T05 `agent-context-manager-v1`、T06 `runtime-observability-v1`、T09 `agent-http-api-v1`（其 `contract.md` D7 已定稿编号 T09），T08 = 服务装配（尚未建任务、无分支）。Design §14 引用需与此对齐，术语统一由 Analyst 在 Contract 中落定；`task-slug` 不含编号，不影响执行。

### 恢复核实（2026-10-21，TaskBuilder）

- 已核实事实：`develop` HEAD = `4f160699c934d49374d46b680eadc8bd42d67cef`（`merge: 合并 fix/provider-timeout-classification-v1 到 develop`）；修复实现提交 `43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`（`fix(httpapi): 让 Provider 自身超时统一映射为 provider_failure`）已为 `origin/develop` 祖先，设计落库 `aa2f6e2`（`docs/design/http-api.md` §4）同样在 `develop` 上。
- 已核实事实：前置任务 `provider-timeout-classification-v1` 的 `state.yaml` 为 `contract=APPROVED(34b1505)` / `review=CLEAN(43ce197)` / `owner=ACCEPTED(43ce197)` / `delivery=PASS`，且其交付提交 `c596001`、`580be03`、合并 `4f16069` 均在 `develop` 历史中，非仅存在于 feature 分支。
- 已核实事实：修复后生产行为与 Contract 失败矩阵一致 —— Provider 自身超时（`openai.Config.Timeout` 到期 + 请求级 ctx 未到期）→ 502 + `provider_failure` + Run `FAILED`；请求级 `httpapi.RequestTimeout` 到期 → 504 + `deadline_exceeded` + Run `CANCELLED`。判定优先级改为「先会话哨兵 → 再最外层 `*core.RunError` 的 Kind → 无 `RunError` 时才兜底裸 Context 哨兵」。
- 已核实事实：本任务分支 HEAD = `f4414138e31250c8d1b49bae5dd01b91f048c155`（`merge: 合并 origin/develop 到 feat/agent-runtime-e2e-tests-v1`），与 `origin/feat/agent-runtime-e2e-tests-v1` 一致；`43ce197` 已为分支 HEAD 祖先（修复已进入 E2E 分支）。
- 已核实事实：`e2e/` 六个测试文件已是**已跟踪、已提交**状态（`git ls-files 'e2e/*'` = 6 项），引入提交为 `e0b4021`，此后无改动；工作区 clean，无未跟踪文件，无需保护未提交成果。
- 已核实事实：在含修复的基线上 `go test ./e2e -count=1` 的 13 个用例全部 PASS（含此前失败的 `TestProviderUnresponsive`，0.05s PASS）；`go build ./...`、`go vet ./...`、`go test ./...` 均通过，Go 版本 `go1.24.1`。
- 已核实事实：`contract.md` 内容与 `contract.target = 0462b5f` 完全一致（`git diff` 无输出），Design §12 已登记 `e2e/` 六行且与 `git ls-files` 一致。
- 已知措辞偏差（不阻塞，不得据此削弱验收）：Contract 的 AC-001 Verification 行把 Session 角色序列写成 `user,assistant,tool,tool,assistant`（5 条），而 AC-001 正文要求「连续两轮 `tool_calls`」，实际用例断言为 `user,assistant,tool,assistant,tool,assistant`（6 条，`llm_calls/tool_calls = 3/2`）。以 AC 正文为准；该表行属过时简写，不得用来放宽 AC-001。修正需走 Contract Revision（会使 APPROVED 失效），本任务不自行修改 `contract.md`。
- 已知提交归属异常（不阻塞，禁止改写历史）：`e2e/` 六个文件是由 `e0b4021`（提交信息为 `provider-timeout-classification-v1` 任务定义与初始状态）一并提交的，提交内容混入了另一任务的产物；该提交只在 E2E feature 分支、不在 `develop`。已发布历史不得 rebase / amend；Cleaner 审查 `review.target` 内的 `e2e/*` 时须覆盖该提交，不得按提交信息推断归属。

## Verification

- AC-001 → HTTP 层集成用例：构造 `httptest` scripted OpenAI 服务按脚本返回两轮 `tool_calls` 与终稿；断言响应字段、Session 消息序列（`core.ValidateMessages` 通过、`CallID` 对应）、scripted 服务收到的第 N 次请求消息与工具结果内容一致。环境：Linux + Go 1.24.x，无需外部服务。
- AC-002 → 同 Session 第二次请求断言 scripted 请求含首轮原文；另一 Session 请求断言不含；两 Session 的 `Messages` / `Summary` / `Todos` 在交叉运行前后做逐字段快照对比（双向）。
- AC-003 → 表驱动用例：领域失败、未知工具、参数非法各一例，断言错误 tool 消息被回填且下一次模型请求可见；取消/超时用例断言终态与错误码符合 `docs/design/http-api.md` 映射、模型调用次数不再增加、Session 快照与提交前逐字段相同。
- AC-004 → 七类失败场景各一例（provider 4xx / 5xx / 无响应 / HTTP 超时 / 客户端断开 / LLM 限额 / 工具限额），断言「状态码 + 错误码 + Run 终态」三元组与设计一致，且无半轮提交；限额用例通过 scripted 脚本持续返回 tool_calls 触发。
- AC-005 → 源码扫描（无真实 Provider 域名常量、无真实密钥字面量）+ 运行时断言（scripted 服务请求计数等于脚本预期；测试进程仅访问 loopback）+ 在未设置真实凭据的环境执行整轮验收。
- AC-006 → 仓库根依次执行 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`（可加 `-count=3` 复跑时间敏感用例），逐条记录退出码与输出；并发用例在 `-race` 下不得有告警；同时保持 `GOWORK=off go -C .agent/engine test ./...` 通过（未改动 Engine）。
- AC-007 → 交付记录逐 AC 映射测试名与命令；Cleaner 抽查核心用例是否能在错误实现下失败（至少选择 1~2 处可逆 mutation 验证），并核对无 `t.Skip` / 无弱断言。
- AC-008 → `git ls-files` 与 Design §12 路径集合双向 `comm` 差集为空；若 §14 有收窄，逐条核对措辞与任务结论一致。

## Complexity

COMPLEX

原因：任务定义前必须先定的不是「写哪些测试」，而是几个会决定证据强度与项目级事实的选择——(1) 验收装配的落点与跨包共享方式：4 份私有 scripted provider 无法复用，选择「各自重复替身 / 新增仅测试用的共享包 / 只用真实 `llm/openai.Client` + loopback 服务」，直接决定 Design §12 文件所有权变更与 AC-004（不弱化测试）能否成立；(2) 「Scripted LLM」与 Owner 要求「覆盖 LLM Client」之间的关系：是直接实现 `llm.Provider` 替身，还是真实 `openai.Client` 对 loopback 脚本服务，两者证明的层不同；(3) 超时/取消/限额在无 Clock 注入、依赖真实计时的前提下如何做到可重复且不 flake，必要时是否触碰生产代码（超 Scope）；(4) 「零真实 Token」需要可机械复核的保证方式；(5) 前置能力缺口（Todo 隔离、HTTP 层压缩在 v1 不可达）如何验收，属于 Scope 与里程碑结论的实质变化。以上均需 Analyst 收敛并由 Owner 确认，不能由 Coder 在实现期自选。

## Analyst Questions

1. 前置缺口处置（阻塞，Owner 决定后写入 Contract）：在 A / B / C 中选哪个？若选 B，降级后的验收层（`agent` + `session`）与「不新增生产代码」的边界如何精确表述，才能既保留证据又不冒称生产装配已成立？
2. 验收套件落点与共享方式：新增专用测试文件/目录，还是扩展既有 `_test.go`？跨包共享 scripted OpenAI 服务与工具替身采用哪种方式（仅在测试文件内重复实现 / 新增仅测试用包且不导出生产符号）？对 Design §12 的最小回写集是什么？
3. Scripted LLM 形态：真实 `llm/openai.Client` + `httptest` 假 OpenAI（覆盖 wire 层）与直接实现 `llm.Provider` 替身（覆盖 Loop 语义）如何分工？是否两条都要，各自证明什么、避免重复？
4. 时间相关用例的确定性：`httpapi.RequestTimeout`、provider 无响应、客户端断开、工具超时如何构造既有确定性又不依赖长等待的用例？是否需要引入可注入 Clock 或可注入超时源（如需要即为生产变更，需 Owner 决定）？
5. 并发验收边界：同 Session 并发、跨 Session 并发、并发工具调用、并发 `runlog` 四项中，哪些由本任务新增证据、哪些已由既有测试覆盖（避免重复并明确「新增」的判别力）？
6. AC-004 / AC-007 的机械化审查标准：如何定义「不存在弱化测试」（例如每条 AC 至少一个「错误实现必失败」的判别用例、是否采用可逆 mutation 作为审查证据），使 Cleaner 可机械核验？
7. Design 更新最小集：§12 需登记的路径清单；§14 是否需要收窄、哪几条，以及与 §14 既有「v1 不压缩/不注册 Todo 属既定边界」表述如何保持一致而不产生双重归属。

## Review Baseline

- Base commit：`d54c912540c090fa7c80961cb6a7cbee8e6ec9a0`（`merge: 合并 feat/agent-http-api-v1 到 develop`）。当前分支 `feat/agent-runtime-e2e-tests-v1` 指向该 commit，与本地 `develop`、`origin/develop`、`origin/feat/agent-runtime-e2e-tests-v1` 一致。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无未跟踪文件；`go build` / `go vet` / `go test ./...` 基线全绿。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/agent-runtime-e2e-tests-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；验收测试文件由 Coder 在独立 commit 引入；`contract.md` 与 `docs/design/runtime-architecture.md` 由 Analyst 维护；`delivery.md` 由 Deliverer 维护。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。
- 恢复基线（2026-10-21）：feature HEAD `f4414138e31250c8d1b49bae5dd01b91f048c155` = `origin/feat/agent-runtime-e2e-tests-v1`；`develop` / `origin/develop` = `4f160699c934d49374d46b680eadc8bd42d67cef`；工作区 clean、无未跟踪文件。Coder 的 `review.target`（C1）必须从该基线之后产生，且 C1 必须包含此前已提交的 `e2e/` 六文件与新增改动。原有 `d54c912` 基线记录保留，用于说明任务起点。

## Initial Route

交 Coder（COMPLEX，`contract.status = APPROVED`，`contract.target = 0462b5f2e783f9d9340a6ea629b76aa2869c40e8`）。

2026-10-21 恢复：前置生产修复已在 `develop` 集成并进入本分支，`blocked.active` 已由 TaskBuilder 置为 `false`；后续按 `Coder → Cleaner → OwnerGate → Deliverer` 继续原有验收，不重写已完成工作、不降低 AC。
