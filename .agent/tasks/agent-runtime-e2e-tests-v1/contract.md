# Technical Contract

Task: `agent-runtime-e2e-tests-v1`（无任务编号；`T09` = `agent-http-api-v1`，`T08` = 服务装配任务，尚未建任务）
Design Impact: UPDATE（`docs/design/runtime-architecture.md` §12 路径登记 + §14 缺口记录，见 §Design Update）

## Decision Status

WAITING_FOR_OWNER_DECISION

## Problem

本任务要交付的不是「更多测试」，而是**一条此前不存在的证据链**：进程内完全离线地把 `httpapi.New` → `service.NewRunner` → `agent.New` → 真实 `llm/openai.Client` → 真实 `builtin` 工具（`calculator` / `mock_search`）→ 真实 `session.Manager` 串起来，并由四类断言同时成立：HTTP 响应字段、Session 存储状态、`runlog` 事件内容、scripted 服务收到的 wire 请求。

阻止直接实现的是三个设计选择，不是编码量：

1. **验收装配落点与文件所有权**：仓库内 4 份 scripted provider 都是各包私有替身（跨包不可引用），验证套件放哪里直接决定 Design §12 变更集与「不新增生产符号」能否成立；
2. **「Scripted LLM」的形态**：直接用 `llm.Provider` 替身只能证明 Loop 语义（已有证据），用真实 `openai.Client` + loopback 脚本服务才能同时证明 wire 映射与 Loop 语义；
3. **失败/超时/取消的三元组（状态码 + 错误码 + Run 终态）在无 Clock 注入、依赖真实计时的条件下如何构造得既确定又有判别力**，以及 v1 装配下不可达能力（Todo over HTTP、上下文压缩）怎样如实记录为缺口而不是伪 PASS。

任务边界：只新增测试文件与 Design 登记，**不修改任何生产文件**。

## Verified Current Behavior

装配与接线：

- VERIFIED：`httpapi` 通过端口依赖 Run 与会话（`httpapi/run.go:14-25`），`handleChat` 的处理顺序为认证 → Content-Type → 解析 → `Sessions.Get` 预检 → `context.WithTimeout(r.Context(), RequestTimeout)` → `Runner.Run`（`httpapi/handler.go:86-134`）。因此**请求级超时 ctx 是 HTTP 层唯一能产出 504 的来源**。
- VERIFIED：`service.Runner.Run` 外层恰好一次 `session.Manager.RunTurn`，回调内 `turn.Append(user)` → `agent.New`（per-Run Loop）→ `loop.Run`；非 `COMPLETED` 由回调返回错误使整轮丢弃（`service/runner.go:175-208`）。非 `COMPLETED` 时以 `RunOutcome`（含 `Status` / `Err` / 计数）返回而非 error（`service/runner.go:199-206`），因此测试侧可零侵入地读到 Run 终态。
- VERIFIED：`service.Config.validate` 拒绝 `TokenBudget != 0`（`service/runner.go:90-92`），`Deps.Summarizer` 注释为「仅供测试注入，生产装配必须留空」（`service/runner.go:117-118`）。**结论：v1 装配下上下文压缩经 HTTP 不可达，即使注入 Summarizer 也不可达。**
- VERIFIED：`cmd/mini-agent` 的 `newRegistry` 只注册 `calculator`，Todo 端口适配归 T08（`cmd/mini-agent/main.go:170-182`）。**结论：v1 下 HTTP 无法触发 `todo_*`，跨会话 Todo 隔离无法经 HTTP 证明。**
- VERIFIED：压缩与「压缩过程中的工具消息配对」已在 `agent` 层有运行期证据（`agent/context_test.go:33` `TestRunCompactsOverBudgetHistory`、`:192` `TestRunDropsRunMessagesWhenCompactionCrossesBaseline` 断言摘要覆盖跨基线后 `RunResult.Messages` 仍完整且提交后历史合法），`contextmgr/manager_test.go` 覆盖预算与原子单元。**本任务无需为压缩新增证据。**

失败语义（HTTP 层映射来源）：

- VERIFIED：`code → status` 与 `errorCodeOf` 是单点映射（`httpapi/errors.go:87-108`、`:159-198`）：`deadline_exceeded`→504、`cancelled`→408（best-effort）、`provider_failure`→502、`llm_call_limit`/`tool_call_limit`→429、`tool_failure`→500、`invalid_pairing`→500。
- VERIFIED：设计明确「客户端已断开时不承诺任何 HTTP 状态码」（`docs/design/http-api.md:142`），且基线设计 §14 已记录该限制（`runtime-architecture.md:389`）。
- VERIFIED：`openai.Client` 把 4xx/5xx 与非 2xx 映射为 `provider_failure`（`llm/openai/client.go:189-195`），把 `HTTPClient.Timeout` 映射为 `deadline_exceeded`（`:158-166`）。
- VERIFIED（关键重分类）：`agent` 在 Provider 返回错误后先看 `ctx`：`ctx` 未终结时一律重分类为 `provider_failure`（`agent/run.go:126`、`:230-239`）。**因此 Provider 侧超时（`openai.Config.Timeout`）经 Run 边界表现为 `FAILED + provider_failure` + 502，而不是 504**；504 只属于请求级 ctx 超时。此组合在设计 §7 未被显式覆盖（见 Open Risks）。
- VERIFIED：`RunOutcome.checkedCode` 对自相矛盾结论（状态与错误种类不符、计数为负、终态缺错误）一律 `internal`，不会 200（`httpapi/run.go:47-64`）。

观测与工具语义：

- VERIFIED：`runlog.NewJSONSink(w)` 用单个 `slog.JSONHandler` 串行写出（`runlog/slog.go:40-45`），`run_finished` 事件携带 `Status` 与由 `core.KindOf` 推出的 `error_kind`（`docs/design/runtime-architecture.md:190`、`:204`）。因此 JSON 事件是独立于 HTTP 的第二个终态观测通道。
- VERIFIED：wire 层工具结果的消息形状为 `{"role":"tool","content":...,"tool_call_id":<CallID>}`，可恢复失败内容带固定前缀 `工具执行失败: `（`llm/openai/wire.go:34`、`:201-216`）；`tools[]` 来自 `Registry.Specs()`（`agent/run.go:120`）。断言工具回填必须按该前缀，不能期望原始 Content。
- VERIFIED：`calculator` 成功内容为 `{"operation":"<op>","result":<float64>}`（`builtin/calculator.go:14-18`），除零领域失败码 `division_by_zero`（`:88-92`）；`mock_search` 成功内容含 `"mock":true`、`query` 归一化小写、`results` 按数据集顺序（`builtin/search.go:68-80`），`query="上下文"` 唯一命中 `doc-3`（`builtin/search_data.go:19`）。
- VERIFIED：真实 `openai.Client` 指向 `httptest` 假服务已被证明可行（`cmd/mini-agent/main_test.go:50-81` 的 `fakeOpenAI`、`llm/openai/client_test.go`）。

可复用证据（决定「新增证据」的判别力，避免重复）：

- VERIFIED：Loop 层已有同会话 N=4 串行（`agent/concurrency_test.go:18`）、跨会话并发（`:93`）、排队取消（`:148`）；service 层已有同会话 2 并发（`service/runner_test.go:524`）；session 层有 `session/concurrency_test.go`；执行器去重并发有 `tool/executor_test.go:407`；JSON Sink 并发不交织有 `runlog/slog_test.go:711`。
- VERIFIED：已有一次跨层装配式测试 `httpapi/handler_test.go:420` `TestRealRunnerEndToEnd`（真实 `httpapi` + `service` + `agent` + `session.Manager`），但 Provider 与 Tool 都是该包私有替身，未覆盖真实 `builtin`、真实 `llm/openai.Client`、`runlog` 断言与失败三元组。

工程事实（本任务方案可行性）：

- VERIFIED（本地 `go1.24.1` 探针）：仅含 `*_test.go`（`package e2e_test`）的目录 —— `go test ./...` 正常；`go vet ./...` 退出 0；`go build ./...` 退出 0（该目录被跳过）；但 `go build ./e2e` 单包会报 `no non-test Go files`（预期，非缺陷）。
- VERIFIED（同探针）：该目录 `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 的 `.Imports` 为空，模块内依赖落在 `.XTestImports`。**因此新增仅测试包不会破坏 Design §2 的分层机械核对。**
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`，本任务不占用任何全局资源（无 migration、无错误码域）。

- UNKNOWN：`go test -race ./...` 在 CI（`golang:1.24`）上的耗时与内存是否可接受；本任务只在本地记录实测值，不把 `-race` 纳入 CI。
- UNKNOWN：`-count=3` 在极慢机器上的稳定性；只能通过本地重复执行与保守时间参数降低风险，无法先验证明。

## Recommendation

RECOMMENDATION（四项打包，均为本任务内部选择，不改变 Scope 与 AC）：

**R1 前置缺口处置选 B（按现有 v1 装配执行，缺口如实标注）。**
- 依据：AC-001—AC-008 均未要求「HTTP 触发 Todo」或「HTTP 触发压缩」；压缩已有 agent 层证据（见上）；Todo 端到端确属 T08。
- 满足 Goal / AC：全部 8 条 AC 在 v1 装配下可实现；`delivery.md` 明确写「HTTP 层 Todo 与压缩不可达，归 T08」，不冒充生产装配已成立。
- 保持行为：不改 AC、不改 Scope、不等前置任务。
- 代价与风险：验收对象是「Runtime 能力」而非「当前生产注册集合」；Todo 隔离降级为快照级证据。
- 不推荐 A（等待前置任务，阻塞里程碑且压缩并不需要新证据）与 C（把装配并入测试任务，改变 Complexity/Design Impact 与 Deliverer 验收对象）。

**R2 套件落点：新增根级仅测试包 `e2e/`（`package e2e_test`，6 个 `*_test.go`）。**

```text
e2e/harness_test.go      装配 + scripted OpenAI 服务 + 三条观测通道 + 断言辅助
e2e/chat_test.go         AC-001 多工具连续执行；AC-002 记忆与跨会话隔离
e2e/failure_test.go      AC-003 可恢复工具失败；AC-004 provider 4xx/5xx、双限额
e2e/timeout_test.go      AC-003/AC-004 时间敏感：provider 无响应、HTTP 超时、客户端断开
e2e/concurrency_test.go  AC-006 并发
e2e/offline_test.go      AC-005 零真实 Token 机械证据
```

- 依据：与 `cmd/mini-agent` 同为 L3 装配视角，可 import 任意 L0–L3 包（Design §2 已允许 `cmd` 如此），而 `httpapi` 生产代码不得 import `service`/`llm`/`tool`；把整套验收塞进 `httpapi` 测试包会让「端点契约测试包」事实上承担全系统验收，并继续扩散 `httpapi_test` 的跨层 import。
- 关键取舍：新增一个顶层目录（Design §12 必须登记，且 `go build ./e2e` 单包失败）换「验收套件与被测包的测试替身彻底解耦」。
- 备选（仅记录）：并入 `httpapi/handler_test.go` 所在包，复用既有 `newTestServer`/`doRequest`/`snapshot` 辅助，Design 变更集更小，但套件归属与可维护性变差。

**R3 Scripted LLM 单形态：真实 `llm/openai.Client` + loopback `httptest` 脚本服务，不再新增第 5 份 `llm.Provider` 替身。**
- 依据：Provider 级语义已有 `agent/fake_test.go` + `agent/agent_test.go`（`finish_reason` 表驱动、预算边界、失败注入）与 `service/runner_test.go` 证据；端到端唯一缺的是 wire 层（`messages`/`tool_calls`/`tool_call_id`/`tools[]`/`finish_reason`）。
- 满足 AC：一次链路同时覆盖 wire 映射 + Loop 语义，且天然满足 AC-005（只有 loopback endpoint，无凭据）。

**R4 终态观测用「零逻辑装饰器 + 事件交叉校验」，不引入可注入 Clock、不改生产代码。**
- 观测通道三条：① HTTP 响应（状态码 + 封套 code）；② 测试侧 `recordingRunner{inner: 真实 service.Runner}` 记录 `httpapi.RunOutcome`（`Status`、`Err.Kind`、计数）——这是失败请求下唯一精确的 Run 终态来源；③ `runlog.NewJSONSink` 的 `run_finished` 事件（`status` / `error_kind`），与 ② 交叉校验。
- 确定性做法：时间敏感用例不靠 `Sleep` 竞争 —— 脚本服务读请求后阻塞在**自身** `r.Context().Done()`（或 5s 上限），由注入的失败模式唯一决定结果；断言「服务端收到 N 次请求」用有界轮询（≤2s）而不是瞬时读取；请求级超时用 `httpapi.Config.RequestTimeout`（建议 200ms），Provider 侧超时用 `openai.Config.Timeout`（建议 50ms），单例总耗时可控。
- 拒绝的方案：给 `httpapi` / `agent` 注入 Clock 或可注入超时源。理由：属生产 API 变更（Out of Scope），而本任务所有时间语义都由 ctx deadline 与本进程可控配置决定，不需要伪造时间。

## Selected Design

等待 Owner 确认。

## Interfaces and Data

不新增任何生产导出符号；不改动 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `agent` / `httpapi` / `service` / `builtin` / `cmd` 的生产文件。

装配图（全部进程内，无外网）：

```text
httptest.Server(httpapi.New)                      ← real HTTP over loopback
   Deps.Sessions = *session.Manager (real, NewManager(ManagerConfig{}))
   Deps.Runner   = recordingRunner{ inner: service.NewRunner(real deps) }
service.Deps
   Provider = *openai.Client{BaseURL: scriptedServer.URL, APIKey: ""}   ← real client
   Registry = tool.NewMapRegistry + builtin.NewCalculator() + builtin.NewMockSearch()
   Sessions = 同一个 *session.Manager
   Sink     = runlog.NewJSONSink(captureWriter)     ← 断言事件内容
scriptedServer = httptest.NewServer(script handler) ← 唯一模型交互点
```

固定数据与接口约束：

- `recordingRunner` 只读透传：签名与 `httpapi.Runner` 完全一致，不改 `ctx`、不改 `RunRequest`、不改返回值，仅记录 `(RunRequest, RunOutcome, error)`；不得加超时、重试、缓存或短路。
- scripted 服务按脚本消费请求：每行脚本 ∈ {成功响应（含 `finish_reason` 与 `tool_calls`/`content`）、HTTP 错误码（4xx/5xx）、阻塞直至自身 ctx 结束}；记录每次收到的**原始 body**（用于 `messages`/`tools`/`tool_call_id` 断言）、请求 Host、`Authorization` 头是否存在。请求序号与脚本严格一一对应，脚本耗尽即 `t.Fatal`（避免静默放纵）。
- Session 快照比较口径固定为 `Session{ID, Messages, Summary, Todos}` 全字段深比较（`manager.Get` 返回深拷贝，已是既有事实）。
- 跨会话隔离用例必须让被观察会话的 `Todos` 与 `Summary` **非空**（用公开的 `manager.Save` 预置合法数据），否则「逐字段不变」在 v1 是空断言。
- e2e 的 registry 为 `calculator` + `mock_search`（AC-001 要求），**与 `cmd/mini-agent` 的 v1 生产 registry（仅 `calculator`）不同**；交付记录不得宣称与生产注册集合等价。
- 事件解析：按行 `json.Unmarshal` 为本地结构（字段名以下划线形式 `session_id`/`run_id`/`event`/`status`/`error_kind`），禁止依赖 `runlog` 的私有类型。

## Business Invariants

- INV-E2E-01（观测不改变被测行为）：`recordingRunner` 与捕获型 Sink 只读透传；同一用例中「HTTP 收到 1 次 chat」⇔「`service.Runner` 被调用 1 次」⇔「scripted 服务按脚本收到 N 次请求」。任一不等即测试失败。
- INV-E2E-02（无半轮提交）：任一非 `COMPLETED` 的 chat 请求（含超时、取消、断开、限额、provider 失败）返回后，会话 `Messages` / `Summary` / `Todos` 与请求前逐字段相同，且历史仍通过 `core.ValidateMessages`。
- INV-E2E-03（成功即整轮提交一次）：200 响应后会话末尾恰好是本轮 `user` + 各轮 `assistant(tool_calls)` + 每条 Call 一条 `tool`（`CallID` 一一对应）+ 终稿 `assistant`；不含未配对消息、无重复 user 消息。
- INV-E2E-04（三元组唯一且互斥）：每个失败用例的「HTTP 状态码 + `error.code` + Run 终态/`error_kind`」等于下表唯一行；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 不得互相顶替；`internal` 不得出现在任何可预期失败用例中。
- INV-E2E-05（零真实 Token）:所有模型交互只发生在本进程 `httptest` loopback 服务上；e2e 源码不含真实 Provider 域名/密钥字面量；每次模型请求的 Host 等于脚本服务地址；`Authorization` 头不存在（`APIKey` 为空）；整轮验收不设置 `MINI_AGENT_LLM_BASE_URL` / `MINI_AGENT_LLM_API_KEY`。
- INV-E2E-06（并发不串话）：并发用例结束后，每个会话的消息总数等于其成功轮次之和、`CallID` 唯一且配对合法；每条 `runlog` 记录都是可独立解析的完整单行 JSON（不交织）。
- INV-E2E-07（判别力）：AC-001 / AC-003 / AC-004 各至少一条用例在对应错误实现下必然失败（mutation 清单见 Verification Requirements）；e2e 目录禁止 `t.Skip` / `testing.Short()` / build tag / 外网访问。

## Failure and Consistency Semantics

失败矩阵（v1 装配，均要求 INV-E2E-02 成立）：

| 用例 | 注入方式 | HTTP | `error.code` | Run 终态 | `error_kind` |
| --- | --- | --- | --- | --- | --- |
| provider 4xx | 脚本返回 400 | 502 | `provider_failure` | `FAILED` | `provider_failure` |
| provider 5xx | 脚本返回 500 | 502 | `provider_failure` | `FAILED` | `provider_failure` |
| provider 无响应 | 脚本阻塞 + `openai.Config.Timeout` 到期 | 502 | `provider_failure` | `FAILED` | `provider_failure`（`agent` 重分类） |
| HTTP 请求超时 | 脚本阻塞 + `httpapi.RequestTimeout` 到期 | 504 | `deadline_exceeded` | `CANCELLED` | `deadline_exceeded` |
| 客户端断开 | 客户端取消请求 ctx | 不承诺（录制到 408 仅作观察项） | `cancelled`（若可写出） | `CANCELLED` | `cancelled` |
| `MaxLLMCalls` 耗尽 | 脚本持续返回 `tool_calls`，上限取小值 | 429 | `llm_call_limit` | `LIMIT_EXCEEDED` | `llm_call_limit` |
| `MaxToolCalls` 耗尽 | 单响应多 `tool_calls` 超过工具额度 | 429 | `tool_call_limit` | `LIMIT_EXCEEDED` | `tool_call_limit` |

- 可恢复工具失败（AC-003，不改变终态）：`calculator` 除零 / 未知工具名 / 参数非法 → `ToolResult{IsError:true}` 回填，wire 层为 `role=tool` + `tool_call_id` + 内容带 `工具执行失败: ` 前缀；下一次模型请求可见该内容；Run 可继续并 `COMPLETED`（HTTP 200）。
- 成功语义：`200` 只代表该轮 `COMPLETED` 且会话提交已发生（`docs/design/http-api.md:144`）；不代表日志已落盘、不代表外部持久化。
- 重复与并发：无幂等键，同一消息重复提交各自成为一轮；同会话并发由执行权串行，排队超过 `RequestTimeout` 即 504（`docs/design/http-api.md:146`）——并发用例只断言「成功轮次数 = 会话消息增量对应关系」与无死锁，不断言排队顺序。
- 观测优先级：Run 终态以 `recordingRunner` 记录的 `RunOutcome` 为准，`run_finished` 事件为交叉校验；两者不一致时测试失败（不允许只取其一）。

## Allowed / Forbidden Changes

- 允许新增：`e2e/harness_test.go`、`e2e/chat_test.go`、`e2e/failure_test.go`、`e2e/timeout_test.go`、`e2e/concurrency_test.go`、`e2e/offline_test.go`（全部 `package e2e_test`，无任何非测试文件）。
- 允许（仅 Analyst，Contract APPROVED 后）：`docs/design/runtime-architecture.md` §12 追加上述 6 行 + `e2e/` 仅测试包边界说明；§14 追加 1 条缺口记录。
- 禁止：修改任何生产文件（`core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `agent` / `httpapi` / `service` / `builtin` / `cmd`）；放宽 `service.Config` 对 `TokenBudget != 0` 的拒绝；在 harness 中注册 Todo 工具冒充生产装配；新增 `llm.Provider` 替身；新增生产导出符号、第三方依赖或测试框架；新增 `t.Skip` / build tag / 外网访问；修改 `docs/design/http-api.md` 错误码映射；修改 `Makefile` / `.cnb.yml` / `.agent/**` / `.agent/engine`。
- 若实现中发现生产缺陷：停止扩张，按 V3 流程交 Coder 修复并复审（Scope 允许修复真实缺陷，但不得为使验收通过而放宽测试期望）。

## Verification Requirements

| AC | 入口 | 关键断言 | 命令 |
| --- | --- | --- | --- |
| AC-001 | `e2e/chat_test.go`：`TestMultiToolChainCommitsFullTurn` | 两轮 `tool_calls`（`calculator` + `mock_search`）→ `stop`；200 + `reply` == 终稿；`llm_calls`/`tool_calls` == 3/2；Session 角色序列 `user,assistant,tool,tool,assistant` 且 `ValidateMessages` 通过；`calculator` 内容 `operation/result` 与 `mock_search` 命中 `doc-3`；第 2/3 次 wire 请求含上一轮全部 `tool_call_id` 且一一对应；`tools[]` 含两工具 Schema | `go test ./e2e -run TestMultiToolChainCommitsFullTurn -v` |
| AC-002 | `e2e/chat_test.go`：`TestSessionMemoryAndCrossSessionIsolation` | 同会话第 2 次请求 wire 含首轮 user 与 assistant 原文；B 会话请求不含 A 的任何内容；A→B 与 B→A 两个方向的前后快照 `Messages`/`Summary`/`Todos` 逐字段相同（种子非空） | `go test ./e2e -run TestSessionMemoryAndCrossSessionIsolation -v` |
| AC-003 | `e2e/failure_test.go`：`TestRecoverableToolFailuresContinue` / `TestCancelAndTimeoutLeaveNoHalfTurn` | 三类可恢复失败均回填且下一次请求可见；取消/超时终态与矩阵一致、模型调用次数不再增加、Session 逐字段不变 | `go test ./e2e -run 'TestRecoverableToolFailuresContinue|TestCancelAndTimeoutLeaveNoHalfTurn' -v` |
| AC-004 | `e2e/failure_test.go` + `e2e/timeout_test.go`：`TestProviderFailures`、`TestBudgetLimits`、`TestProviderUnresponsive`、`TestRequestTimeout`、`TestClientDisconnect` | 七行矩阵的三元组逐行匹配；每行 `RunOutcome.Status`/`Err.Kind` 与 `run_finished` 的 `status`/`error_kind` 一致；每行 Session 无半轮 | `go test ./e2e -run 'TestProviderFailures|TestBudgetLimits|TestProviderUnresponsive|TestRequestTimeout|TestClientDisconnect' -v` |
| AC-005 | `e2e/offline_test.go`：`TestNoRealProviderOrCredential` | 源码扫描：e2e 目录无真实 Provider 域名/密钥字面量、无非 loopback `http(s)` 字面量；运行期：每个请求 Host == 脚本服务 Host，无 `Authorization`，脚本收到次数 == 期望；用例不得读取 `MINI_AGENT_LLM_*` | `go test ./e2e -run TestNoRealProviderOrCredential -v` |
| AC-006 | `e2e/concurrency_test.go`：`TestConcurrentChatsSameSession`、`TestConcurrentChatsDifferentSessions`、`TestConcurrentToolCalls` | INV-E2E-06；同会话 N 并发后消息总数精确匹配、配对合法、无死锁；跨会话互不影响；并发工具调用结果不串话；`-race` 无告警 | `go test ./e2e -race -count=3 -v` |
| AC-007 | 交付记录（Deliverer/Coder） | 逐 AC 映射测试名与命令；缺口逐项列明（HTTP 层 Todo、HTTP 层压缩、`-race` 未入 CI、registry 与生产不同）；无 `t.Skip` | `go test ./... && go test -race ./... && go vet ./... && go build ./...` |
| AC-008 | `git ls-files` vs Design §12 | 本任务新增路径双向差集为空；§12 外未改动长期事实 | `git ls-files 'e2e/*'` + Design 人工核对 |

mutation 清单（Cleaner 至少执行 2 处可逆 mutation，记录 diff 与失败输出；与 INV-E2E-07 对应）：

- M1：把 `service.Runner.Run` 的非 `COMPLETED` 分支改为 `return nil`（让失败轮也提交）→ INV-E2E-02 用例必须失败；
- M2：把 `httpapi` 的 `errorCodeOf` 中 `KindLLMCallLimit` 改指向 `CodeProviderFailure` → AC-004 限额行必须失败；
- M3：把 `wire.go` 的 tool 消息 `ToolCallID` 置空 → AC-001 的 `tool_call_id` 一一对应用例必须失败。

## Design Update

`Design Impact = UPDATE`，Contract APPROVED 后、Coder 实现前由 Analyst 写入（仅这两处）：

1. §12 增补 6 行（`e2e/harness_test.go`、`e2e/chat_test.go`、`e2e/failure_test.go`、`e2e/timeout_test.go`、`e2e/concurrency_test.go`、`e2e/offline_test.go`），归属「集成验收（仅测试）」；并在 §12 规则处补一句边界：`e2e/` 是仅测试包（`package e2e_test`，全部 `*_test.go`），不参与 §2 生产分层（`go list .Imports` 为空、`go build ./...` 跳过；`go build ./e2e` 单包报 `no non-test Go files` 属预期），禁止放入生产代码或导出符号。
2. §14 追加一条缺口：HTTP 层触发 `todo_*` 与上下文压缩在 v1 装配下不可达（`service.Config` 拒绝 `TokenBudget != 0`、v1 registry 不注册 Todo 工具），本任务只以 seeded Session 快照证明跨会话隔离，Todo 端到端与生产 Summarizer 仍归服务装配任务（T08）。

不新增/不修改 §11 不变量（AC-008 限定除 §12 与经确认的 §14 外不改动长期事实）。

## Open Risks

- Provider 侧超时经 `agent` 重分类为 `provider_failure`（502）而非 504：设计 §7/§14 未显式覆盖该组合。本任务只按实际行为断言并在交付记录标注；**不修改 Design §7**（AC-008 限制）。若 Owner 认为需要把该语义写入长期设计，应以独立任务处理。
- e2e 装配的 registry（`calculator` + `mock_search`）与 `cmd/mini-agent` 的生产 registry（仅 `calculator`）不同，证据强度只覆盖「Runtime 能力」，不得宣称为生产配置等价。
- AC-002 若不预置非空 `Todos` / `Summary`，隔离断言在 v1 是空断言，会构成「弱测试」并被 AC-007 判为不合格。
- 时间敏感用例在极端调度延迟下仍可能抖动：缓解措施是最小化 `RequestTimeout`（200ms）、阻塞式脚本服务、有界轮询断言、避免 `Sleep` 时序竞争；残余风险靠 `-count=3` 实测记录。
- 客户端断开的 HTTP 状态码按设计不承诺，AC-004 的「状态码」维度对该行只能声明为观察项；若 Owner 要求强制断言状态码，需要 Contract Revision（并会与 `docs/design/http-api.md:142` 冲突）。
- `-race` 全量耗时与内存未测，若影响可接受性，需要 Owner 决定是否只对 `./e2e` 与既有并发包跑 `-race`（本任务默认执行全量并如实记录）。

## Owner Decision Record

等待 Owner 确认。以下四个问题需要 Owner 明确选择（均附 Analyst 推荐）：

- D1（阻塞项，决定 `blocked.active` 是否可清除）：前置缺口处置 A / B / C → 推荐 **B**（按 v1 装配执行；Todo 与压缩缺口如实标注归 T08），B 不需要修改 Task 与 AC。
- D2：验收套件落点 → 推荐 **新增根级仅测试包 `e2e/`（6 个文件）+ Design §12 登记**；备选为并入 `httpapi` 测试包（Design 变更更小，但套件归属与解耦变差）。
- D3：AC-004 失败矩阵三元组确认（上表七行，特别是「provider 无响应 = 502/FAILED」与「HTTP 超时 = 504/CANCELLED」的区分，以及客户端断开不承诺状态码）。
- D4：AC-002 是否允许用 `session.Manager.Save` 预置非空 `Todos` / `Summary`（仅测试侧、不改生产代码）以消除空断言 → 推荐 **允许**；若不允许，该维度只能记为「v1 下无可验内容」的缺口。

补充（不属于决策项，供 Owner 知悉）：`state.yaml` 当前 `blocked.active=true`，`coder-start` Gate 要求其为 `false`；Owner 选定 D1 后需由 TaskBuilder 清除阻塞并保持 `contract.status=PENDING` 直到本 Contract 经 Owner `APPROVED`。
