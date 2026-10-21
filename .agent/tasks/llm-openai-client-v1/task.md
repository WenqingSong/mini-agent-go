# Task: OpenAI-compatible LLM Client（v1）

## Goal

交付一个可替换的 OpenAI-compatible Chat Completions Provider 实现：Agent Loop 只需替换实现即可调用兼容服务，请求构造、响应解析与失败语义符合已批准契约，且全部测试在本地 `httptest` 环境下完成，不产生任何收费 API 调用。

## Scope

- 在根模块 `llm` 模块内新增 Provider 实现（建议新包 `llm/openai/`，实际文件名由 Coder 决定），实现 `llm.Provider`：`Complete(ctx, llm.Request) (llm.Response, error)`。
- 使用标准库 `net/http` 发起 Chat Completions 请求：`POST <BaseURL>/chat/completions`，`Content-Type: application/json`，仅在配置了 APIKey 时发送 `Authorization: Bearer <key>`。
- 请求体映射：`Model`、`Messages`（system / user / assistant / tool 四类消息）、`Tools`、`MaxTokens`、`Temperature`。
  - `Request.System` 映射为 system 消息（纯文本）。
  - `core.Message.Content`（`[]ContentPart`，当前仅 text）拼装为文本内容。
  - assistant 消息的 `ToolCalls` 映射为 `tool_calls[]`（`id`、`type=function`、`function.name`、`function.arguments` 字符串）；arguments 以原始 JSON 文本透传，不重新编排语义。
  - `RoleTool` 消息（`ToolResult`）映射为 `tool` 消息（`tool_call_id`、`content`，可恢复失败以 `IsError` 对应的文本表达，具体措辞由实现决定但必须可区分）。
  - `[]core.ToolSpec` 映射为 `tools[]`（`type=function` + `function.name` / `description` / `parameters`，parameters 为 JSON Schema 原文）。
- 响应解析：取模型回答消息 → `llm.Response.Message`（Role 必须为 assistant）；`choices[].finish_reason` → `llm.FinishReason`；`usage` → `llm.Usage`；完整原始响应体 → `Response.Raw`；0 / 1 / 多个（≥2）`tool_calls` → `[]core.ToolCall`（ID / Name / Arguments 原始 JSON，顺序保持）。
- 失败语义统一用 `core.RunError` 表达：HTTP 非 2xx（含 401 / 429 / 5xx）与协议错误（非法 JSON、缺失 `choices`、`arguments` 非法 JSON、未知 `finish_reason`）→ `provider_failure`；`context` 取消 → `cancelled`；`context` 超时 → `deadline_exceeded`；传输层失败 → `provider_failure` 且保留 `Cause`。
- 默认关闭自动重试：一次 `Complete` 默认只发出一次 HTTP 请求（429 / 5xx 也不自动重发）。
- 可配置项：BaseURL、APIKey、HTTPClient 或超时（具体命名与默认值由实现决定）；配置非法或缺失必需项时返回 `invalid_argument`，不发出请求。
- 测试：使用 `httptest.Server` 覆盖请求构造、普通文本回答、单个与多个 Tool Call、错误注入（400 / 401 / 429 / 500、非法 JSON、空 `choices`）、超时与取消；不访问外网、不读取真实凭据。
- 回写 `docs/design/runtime-architecture.md` §12 文件所有权表，补充本任务新增的实际文件行（仅机械一致性维护，不改该文档其他章节）。
- 单个交付可包含实现文件、实现测试文件，以及上述文档表的一行或多行补充。

## Out of Scope

- Streaming / SSE、增量响应、`stream_options`。
- 官方 SDK 或任何第三方依赖（根模块保持零 `require`）。
- 自动重试、指数退避、熔断、客户端限流器；可配置重试策略不在本任务（"默认关闭自动重试"由"不实现重试、单次请求"满足）。
- 真实 LLM API 调用、真实 API Key、CI Secret、任何外部网络依赖。
- 修改公共契约：`core/**` 与 `llm/provider.go` 的既有类型、字段、方法、错误种类不得变更；确有必要时走 Contract Revision 回 Analyst / Owner。
- Agent Loop 装配、多轮调度、工具执行、`session` / `contextmgr` / `runlog` 的实现。
- Token 估算、上下文截断与压缩、日志落盘与上报。
- 非 OpenAI 兼容协议（Anthropic / 自研协议）与多 Provider 抽象重构。
- CLI、HTTP Server、可运行二进制、Docker / 部署流水线。
- 修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`。
- 引入全局资源（无 migration、无错误码域；本项目 `resources: {}`）。

## Acceptance Criteria

- [ ] AC-001：给定配置了 BaseURL / APIKey 的 Client 与一个包含 system prompt、system / user / assistant / tool 消息、1 个带 JSON Schema 的工具的 `llm.Request`，当调用 `Complete` 时，httptest 服务端收到的请求为 `POST <BaseURL>/chat/completions`、`Content-Type: application/json`、`Authorization: Bearer <key>`，且 JSON 请求体中的 `model`、`messages`（逐条角色、文本内容、`tool_calls` 的 id/name/arguments、`tool_call_id`）、`tools`（`type=function` 与 function 三字段）、`max_tokens`、`temperature` 与请求语义逐字段一致；未配置 APIKey 时不发送 `Authorization` 头。
- [ ] AC-002：给定普通文本回答、单个 Tool Call、多个（≥2）Tool Call 以及带 `usage` 的响应，当解析时，`Response.Message.Role` 恒为 assistant、文本内容无损、`FinishReason` 正确映射、`Usage` 正确、每个 Tool Call 的 ID / Name / Arguments（原始 JSON 字节）与响应一致且顺序保持、`Response.Raw` 保留原始响应体，且 `Response.Validate()` 返回 nil。
- [ ] AC-003：给定各类失败，当调用 `Complete` 时返回带正确 `ErrorKind` 的 `*core.RunError`，且不 panic、不返回半成品 `Response`、不把错误吞成 nil：HTTP 400 / 401 / 429 / 500 → `provider_failure` 且错误中可定位到状态码，429 场景只发出一次请求；非法 JSON 响应体 / 缺失 `choices` / `arguments` 非法 JSON / 未知 `finish_reason` → `provider_failure`；服务端阻塞导致 `context` 超时 → `deadline_exceeded`；调用前或调用中 `context` 取消 → `cancelled`；连接失败 → `provider_failure` 且保留 `Cause`；任一错误文本都不包含 APIKey 明文。
- [ ] AC-004：给定无外网凭据的本地环境，当执行 `go test ./...` 时全部通过，且新包测试只使用 `httptest.Server`、不含真实 LLM 主机名与真实 API Key、不读取外部凭据；`go vet ./...`、`go build ./...` 退出码为 0；`go.mod` 未新增 `require`。
- [ ] AC-005：给定分层约束，当执行 `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 时，新包只 import 标准库与 `<module>/core`、`<module>/llm`，不 import `tool` / `session` / `contextmgr` / `runlog`；`core/**` 与 `llm/provider.go` 的公共语义无改动（Diff 中无这些文件的语义变更）。
- [ ] AC-006：给定 `docs/design/runtime-architecture.md` §12 文件所有权表，当对照 `git ls-files llm/` 时，本任务新增的每个源文件都在表中有且仅有一行归属，实际路径与表内路径一一对应（无遗漏、无改名未回写）。

## Relevant Context

- 已核实事实：根 `go.mod` 为 `module cnb.cool/framework-station/mini-agent-go`、`go 1.24`、零 `require`；`Makefile` 的 `test` 目标即 `go test ./...`（与 `.cnb.yml` 的 `pr-check` 入口一致）。
- 已核实事实：`llm/provider.go` 已定义 `Provider` 接口、`Request`（内嵌 `core.ExecutionRef`，含 `Model` / `System` / `Messages` / `Tools` / `MaxTokens` / `Temperature`）、`Response`（`Message` / `FinishReason` / `Usage` / `Raw`）与 `Response.Validate()`；`llm` 包当前不含任何 HTTP / 鉴权 / 重试 / 限流代码。
- 已核实事实：`FinishReason` 只有导出类型，其字面量常量（`stop` / `tool_calls` / `length` / `unknown`）与 `core.Role` 取值（`system` / `user` / `assistant` / `tool`）在各自包内**均未导出**。实现包只能通过类型转换使用这些字面量；若认为必须导出新常量，属修改公共契约，须走 Contract Revision，不得在实现中自行新增导出符号。
- 已核实事实：`core.ToolCall.Arguments` 与 `core.ToolSpec.Parameters` 均为 `json.RawMessage`（保持原始 JSON，契约层不解析）；`core.ToolResult` 携带 `CallID` / `Name` / `Content` / `IsError`；一条 tool 消息只承载一个结果。
- 已核实事实：`core.ErrorKind` 已包含 `provider_failure`、`cancelled`、`deadline_exceeded`、`invalid_argument`；`docs/design/runtime-architecture.md` §7 规定 Provider 调用失败 → `FAILED + provider_failure`，取消 / 超时 → `CANCELLED + cancelled` / `deadline_exceeded`，不做重试语义。
- 已核实事实：`docs/design/runtime-architecture.md` §2 要求 L1 只依赖 `core`、L1 之间零 import；§12 规定「实现确需新增或重命名文件时，必须在同一交付内回写本表」。
- 已核实事实：`docs/design/runtime-architecture.md` §14 将 Provider 重试 / 限流 / streaming 归后续任务；本任务按 Owner 明确要求「默认关闭自动重试」，即单次请求、不实现重试策略。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，本任务不占用任何共享资源；`state.yaml.resources.reservations` 为空。
- Assumption：实现落在 `llm/openai/` 子包（Owner 未指定路径；Coder 可在 `llm` 模块内选择，但必须同步回写所有权表并保持分层与零第三方依赖）。
- Assumption：「默认关闭自动重试」以「完全不实现重试、每次 `Complete` 只发一次请求」满足；若 Owner 后续要求可配置重试开关，属 Scope 变更，需回 TaskBuilder 更新任务。
- Assumption：本任务 Design Impact 维持 Owner 判定的 `NONE`——不新增模块边界、数据模型、状态机或对外公开协议（外部协议由 OpenAI 兼容规范给定，`llm.Provider` 接口不变）；§12 所有权表回写是既有设计规则的机械一致性维护，计入 Scope 与 AC-006。若 Analyst / Cleaner 判定该回写构成 Design `UPDATE`，回 Owner 校正判定，不在实现阶段自行改判。
- Assumption：本任务不启用 Deliverer（交付物是库内实现，无独立可运行产物）；若 Owner 要求里程碑运行验收，需在 `task.md` 补充 `Milestone` 标识后重走该阶段。
- OPEN QUESTION（不阻塞）：兼容服务的端点约定是否固定为 `<BaseURL>/chat/completions`（由 Coder 在实现中固化并在文档/注释说明即可），以及是否需要支持 `BaseURL` 已含 `/v1` 的常见写法；两者不改变 AC-001 的可观察结果。

## Verification

- AC-001 → 新建 `_test.go`：`httptest.NewServer` 捕获请求，逐字段断言 method / path / header / JSON body（含 0 个 APIKey 时不带 `Authorization` 的用例）；断言失败信息指向具体字段。
- AC-002 → 表驱动解析测试：文本、单 Tool Call、多 Tool Call、`usage` 缺失与存在；断言 `FinishReason`、`Usage`、每个 `ToolCall.ID` / `Name` / `Arguments` 字节级一致、顺序保持、`Raw` 非空且等于响应体；对每个成功响应调用 `Response.Validate()` 断言 nil。
- AC-003 → 错误注入测试：httptest 分别返回 400 / 401 / 429 / 500、`{invalid json`、`{}`（无 choices）、`arguments` 非 JSON、未知 `finish_reason`；用 `core.KindOf(err)` 断言 Kind，用请求计数器断言 429 只发生 1 次；服务端 `time.Sleep` + `context.WithTimeout` 断言 `deadline_exceeded`；预取消与调用中取消断言 `cancelled` 且不阻塞；关闭 server 制造连接失败断言 `provider_failure` + `errors.As/RunError.Cause` 非空；断言错误字符串不含测试用 APIKey。
- AC-004 → 仓库根执行 `go build ./...`、`go vet ./...`、`go test ./...`（或 `make test`）退出码 0；检查新包测试源码无外网主机名与真实 Key；`git diff go.mod` 为空。
- AC-005 → 执行 `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 核对新包 import 列表；`git diff --stat core/ llm/provider.go` 为空（或仅新增文件，不含既有契约文件改动）。
- AC-006 → `git ls-files llm/` 与 `docs/design/runtime-architecture.md` §12 表逐行比对，缺行或路径不一致即失败。
- 环境：Linux + Go 1.24.x + GNU Make，无需外部服务（MySQL / Redis / MQ / 外网均不涉及）。

## Complexity

NORMAL

原因：`llm.Provider` 接口、消息模型、工具调用/结果模型与错误种类均已在 T00 的 APPROVED Contract 与长期 Design 中固定，本任务是在既有契约内完成一个对外协议适配实现；外部协议（OpenAI 兼容 Chat Completions）由外部规范给定，不产生新的项目级数据模型、状态机、跨模块不变量或资源分配；主要风险在协议映射细节与失败语义的可测试性，不构成需要 Analyst 选择的架构方案。若实现中确需变更公共契约，则先走 Contract Revision，任务复杂度再重新判定。

## Review Baseline

- Base commit：`6902d2445665ec356a2db22b33b7749d39d8018e`。当前分支 `llm-openai-client-v1`，本地 HEAD == `origin/llm-openai-client-v1` == `origin/develop` == 该 commit（`git rev-list --left-right --count origin/develop...HEAD` 为 `0 0`，且 `git diff origin/develop...HEAD` 为空）。
- 任务开始时已有修改：无。`git status --short` 为空（本任务文件落盘前工作区 clean）。
- 任务分支：由 Owner 预先创建并推送的 `llm-openai-client-v1`（无 `feature/` 前缀）；TaskBuilder 不创建、不切换、不重命名分支。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/llm-openai-client-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`llm/**` 下新增源文件与测试、以及 `docs/design/runtime-architecture.md` §12 表的补充由 Coder 在实现 commit 中引入。发现他人遗留 dirty 文件时 STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Coder（NORMAL）
