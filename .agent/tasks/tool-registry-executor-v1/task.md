# Task: Tool 注册、参数校验与受控执行（tool-registry-executor-v1）

## Goal

在既有 `tool` 契约（`Tool` / `Registry` / `Request`）之上交付可用的工具装配与执行层：装配方能把合法工具注册进 Registry、按名称查找、并取出 LLM 可直接使用的 `core.ToolSpec` 列表；模型发起的一次 Tool Call 能被执行器在超时/取消/异常下安全执行，并返回带 Call ID 的结构化结果；非法工具、非法参数、重复注册与重复 Call ID 都有明确、稳定、可测试的拒绝语义，而不是静默失败或让 Runtime 失控。

## Scope

- 实现 `tool` 包内的 Registry 具体实现：注册、按名称查找、导出全部工具元数据；空名称与重复注册的拒绝语义。
- 实现「工具定义 → LLM 所需 Schema」的输出路径：以 `[]core.ToolSpec`（`Name` / `Description` / `Parameters` JSON Schema）形式提供给 `llm.Request.Tools`；**不得** import `llm`（INV-001 L1 互不依赖），也不得另外定义一套与 `core.ToolSpec` 并行的元数据类型。
- 实现工具参数校验：工具名称合法性、`Arguments` 的 JSON 语法、必填字段、字段类型；校验失败必须在调用工具实现**之前**返回明确结果，不允许把非法参数透传给 Fake Tool。
- 实现 Tool Executor：接收 `tool.Request`（含 `core.ExecutionRef` 与 `core.ToolCall`）、按名称取工具、校验参数、在超时与可取消 `context.Context` 下执行，并返回保留 `CallID` 的结构化 `core.ToolResult`。
- 实现执行期异常处理：工具返回 `error`、工具 panic、工具超时、整体取消四类情形均不得使执行器 panic 或永久阻塞；结果必须可被调用方与日志定位到具体 `CallID`。
- 明确并实现「单次 Run 内重复 Tool Call ID」的处理规则（去重范围与命中后的返回值由 Contract 定稿）。
- 更新 Design Artifact `docs/design/runtime-architecture.md`，使四处长期事实与 APPROVED Contract 一致：
  - §7 失败与一致性语义：新增/明确工具级局部超时、参数校验失败、未知工具、重复 Tool Call ID 的终态与 `ErrorKind` 归属（若某类明确划归 Agent Loop 任务，必须在该行直接写明归属）；
  - §11 不变量表：更新或新增覆盖「工具执行结果保留 `CallID`」「去重范围」的可验证条目，并标注对应证据类型；
  - §14 归属：把本任务已承担的分类从「Agent Loop 任务」名下收窄，不得出现同一语义被两处同时声称拥有；
  - §12 文件所有权表：登记 `tool/` 下新增源文件，与实际 `git ls-files tool/` 一一对应。
  - 设计内容以 Contract 为准，由 Analyst 在 Contract `APPROVED` 后落实；Coder 只做一致落地与 §12 路径登记，不在实现阶段发明架构决定（§15）。
- 测试：Fake Tool（成功 / 返回 error / panic / 阻塞至超时）、参数校验表驱动测试、取消、重复 Call ID 去重测试，以及 `-race` 下的并发安全测试。

## Out of Scope

- Agent Loop 本身：多轮编排、LLM 调用与工具调用的衔接、工具调用计数与预算（`RunPolicy` / `RunResult.ToolCalls`）、跨工具的 `RunStatus` 终态映射与 Loop 级重试决策。本任务只定义**单次工具执行**的结果语义，跨工具/跨步骤的聚合与终态归 Agent Loop 任务，边界在 Contract 中一次划清并同步到 Design §7 / §14。
- 一次响应内多工具的并发批量调度与顺序编排（`tool` 模块职责表明确排除「并发调度」）。
- 具体真实工具实现（文件、Shell、HTTP、MCP）及其沙箱、权限、鉴权。
- 工具结果的持久化、Session / Todo 存储、runlog 事件落盘（执行器不写日志后端，最多产出可被 runlog 消费的结构化数据由 Contract 决定；不新增 `runlog` 公共符号）。
- 修改 `core` 的既有公共符号与错误语义；如需新增 `ErrorKind` 或修改 `tool.Registry` 接口，必须走 Contract Revision 并由 Owner 确认，不在实现阶段自行扩展。
- 引入第三方依赖（JSON Schema 校验库、测试框架等）；根模块维持零 `require`。
- 修改 `.agent/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`.agent/engine/**`；在共享 `develop` 上提交。
- 在实现阶段自行发明 Contract 未确认的架构决定，或用设计文档更新反过来扩大实现 Scope（`runtime-architecture.md` §15：公共契约变更须先走 Contract Revision 并由 Owner 重新 `APPROVED`）。
- 重写 `runtime-architecture.md` 的其他章节（T00 已固化的分层、消息模型、预算、Session 归属等长期事实不在本任务变更范围内）。
- Milestone 交付验收（Deliverer）本次未由 Owner 指定，若后续需要启用，里程碑标识由 Owner 补充。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`（§7 失败与一致性语义、§11 不变量、§12 文件所有权、§14 归属）

本任务在 `tool` 包内新增的公共执行符号（Registry 实现、参数校验器、Executor）会长期约束后续任务：单工具级超时与失败的结果归属、参数校验失败与未知工具的承载形式、重复 Tool Call ID 的去重范围，都会与 `core` 的 `RunStatus` / `ErrorKind` 映射（§7、INV-006 / INV-007）以及 §14 中已划给 Agent Loop 的分类直接交叉。若不写回设计，T01、T03—T06 会各自解读同一语义。因此本任务必须更新该文档。

变更记录（2026-10-20，Owner 明确指令）：Owner 原输入为 `Design Impact: NONE`；经 TaskBuilder 指出「新增公共符号 + 与 §14 归属交叉」的边界问题后，Owner 确认校正为 `UPDATE` 并指定上述四个设计面纳入 Scope / AC。除上述四处外，不改动其他长期事实。

## Acceptance Criteria

- [ ] AC-001：给定一个名称非空、`Parameters` 为合法 JSON Schema 的工具，当注册后查找并执行时，`Lookup` 命中同一工具实例、`Specs()` 输出其名称/描述/原始 `Parameters` 且可被 `llm.Request.Tools` 直接使用；执行返回 `core.ToolResult`，其 `CallID` 等于请求 `Call.ID`、`IsError=false`、`ToolResult.Validate()` 通过，且 `tool` 包未 import `llm`。
- [ ] AC-002：给定下列非法情形——名称为空或全空白、重复注册同名工具、执行未知工具名称、`Arguments` 非法 JSON、缺少必填字段、字段类型不符——系统返回明确且可区分的错误语义（Kind / 承载形式由 Contract 指定），不 panic、不静默成功，且 Fake Tool 的实现体一次都未被调用（调用计数为 0）；`Lookup` 未命中时返回 `ok=false` 而不是 panic。
- [ ] AC-003：给定四个 Fake Tool（正常、返回 `error`、panic、阻塞超过工具级超时），当执行器在工具级超时与可取消上下文中执行时，调用方在有限时间内必定收到返回；超时与取消不会被当成成功结果，panic 被隔离为结构化结果/错误，所有结果都保留 `CallID`；执行器不返回、不推断、不写入 `RunStatus`；连续 N 次超时用例后未出现 goroutine 持续增长。
- [ ] AC-004：给定 Contract 定义的「同一去重范围」，当同一个 Tool Call ID 在该范围内第二次提交执行时，Fake Tool 实际执行次数保持 1，且返回结果与首次一致或返回显式的「重复调用」错误（不得再次执行、不得静默当成新调用成功）；范围之外（不同范围实例、不同 Call ID）不得被误判为重复。
- [ ] AC-005：给定干净检出，当在仓库根执行 `make test`、`go vet ./...`、`go test -race ./tool/...` 时全部通过；`go.mod` 未新增 `require`；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` 只含标准库与 `<module>/core`。
- [ ] AC-006：给定 APPROVED Contract，当核对 `docs/design/runtime-architecture.md` 时：§7 含本任务新增的单工具级语义行（工具级超时、参数校验失败、未知工具、重复 Tool Call ID）且每行都有明确的终态/`ErrorKind`归属或显式划归 Agent Loop；§11 含覆盖「结果保留 `CallID`」与「去重范围」的可验证条目；§14 中已由本任务承担的分类不再同时署名 Agent Loop；§12 文件所有权表与 `git ls-files tool/` 一一对应；四处内容与 Contract 无未记录偏差（不得出现文档承诺而实现未做，或实现已做而文档未记）。

## Relevant Context

- 已核实事实：`tool/tool.go` 目前只有契约——`Request{core.ExecutionRef, Call core.ToolCall}`、`Tool{Spec() core.ToolSpec; Execute(ctx, Request) (core.ToolResult, error)}`、`Registry{Lookup(name) (Tool, bool); Specs() []core.ToolSpec}`；无具体实现、无测试。
- 已核实事实：`core.ToolSpec{Name, Description, Parameters json.RawMessage}` 已被注释为 `llm.Request.Tools` 与 `tool.Tool.Spec` 共用，`Parameters` 为 JSON Schema 原文；`core.ToolResult{CallID, Name, Content, IsError}`，`CallID` 缺省时 `Validate()` 报 `KindInvalidPairing`；`core.ToolCall.Arguments` 是未经解析的原始 JSON。
- 已核实事实：`core.ErrorKind` 现有 9 值，其中与工具相关的是 `tool_failure`（不可恢复、终止 Run）与 `invalid_argument` / `invalid_pairing`；**没有**工具级超时或「工具名非法」的专用 Kind。
- 已核实事实：`docs/design/runtime-architecture.md` §3 规定 `tool` 模块负责「`Tool` 执行契约、工具元数据、`Registry` 查找」，明确不负责「具体工具实现、沙箱、权限、并发调度」；§14 把「工具失败可恢复性分类、同 step 多调用顺序与并发」归属 Agent Loop 任务；§15 规定后续任务不得私自扩展公共符号；§12 规定实现新增源文件时必须在同一交付内回写文件所有权表。
- 已核实事实：`docs/design/runtime-architecture.md` §7 / INV-006 / INV-007 只定义了「整体超时 → `CANCELLED + deadline_exceeded`」「可恢复工具失败 → `ToolResult{IsError:true}` 回填」「不可恢复 → `FAILED + tool_failure`」，未定义**单个工具的局部超时**如何归类。
- 已核实事实：`go.mod` 为 `module cnb.cool/framework-station/mini-agent-go` / `go 1.24`，零 `require`；`Makefile` 提供 `test` / `build` / `vet` / `test-engine`；`.cnb.yml` 的 `pr-check` 执行 `make test`。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空），`core.ErrorKind` 是代码内枚举而非 Registry 资源。
- Assumption：Registry 与 Executor 应作为 `tool` 包的公共实现（而非 `internal/`），以便 T01 / T03—T06 及装配层直接复用；若 Analyst 判定应为 `internal/`，需在 Contract 中说明对后续任务的影响。
- Assumption：「工具定义 → LLM Schema」在本仓只能落在 `[]core.ToolSpec`（`tool` 不得 import `llm`），因此该 AC 的实现形态是 `Specs()` 输出 + 必要的最小规范化（如空 Schema 补默认），不新增跨包转换类型。
- OPEN QUESTION（不阻塞，交 Analyst / Owner）：AC-004 的「已定义的去重范围」尚未定义——同一 Run、同一 Executor 实例、还是同一 assistant 消息；不同范围会得出不同去重结果与后续 Loop 集成方式。
- 已核实事实：Owner 已于 2026-10-20 将 `Design Impact` 由 `NONE` 校正为 `UPDATE`，Design Artifact 为 `docs/design/runtime-architecture.md` 的 §7 / §11 / §12 / §14 四处；除这四处外不得改动该文档其他长期事实。
- OPEN QUESTION（不阻塞，交 Analyst）：§11 新增条目是否沿用 `INV-0xx` 编号并从 `INV-009` 续编，还是以「单工具执行」小节承载；两种写法都会影响后续任务的引用方式，需在 Contract 中给出唯一写法。

## Verification

- AC-001 → `tool` 包单元测试：注册一个 Fake Tool → 断言 `Lookup` 命中同一实例、`Specs()` 的 `Name` / `Description` / `Parameters` 与注册值逐字节一致；执行断言 `CallID` / `Name` / `IsError=false` 且 `ToolResult.Validate()==nil`。依赖方向用 `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` 机械核对，只允许标准库与 `<module>/core`。环境：Linux + Go 1.24.x，无外部服务。
- AC-002 → 表驱动测试逐条覆盖六类拒绝路径，每条断言 (a) 返回的错误/结果语义与 Contract 表一致、(b) Fake Tool 调用计数为 0、(c) 无 panic；`Lookup` 未命中用例断言 `(nil,false)`。
- AC-003 → 四个 Fake Tool 分别测试：阻塞型工具在工具级超时后返回，用例本身设 `testing` 级 watchdog（如 `select` + `time.After`）证明不会永久阻塞；panic 用例断言不发生 panic 逃逸且结果带 `CallID`；取消用例传入已取消的 context 并断言及时返回；goroutine 泄漏用「重复 N 次超时后 `runtime.NumGoroutine()` 不随迭代单调增长」的宽松断言 + `go test -race ./tool/...`。
- AC-004 → 同一 Call ID 连续两次执行，断言 Fake Tool 调用计数为 1；对照用例（不同 Call ID / 不同去重范围实例）断言计数为 2，证明去重范围既生效又不越界。
- AC-005 → 仓库根执行 `make test`、`go vet ./...`、`go test -race ./tool/...`，均要求退出码 0 且有真实测试输出；`git diff go.mod` 为空。
- AC-006 → Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对，逐条产出可核查证据：(a) 打开 §7 表格确认四条新增语义行存在，且其终态/`ErrorKind` 与 Contract 的失败语义表逐字对应（含「归 Agent Loop」的显式标注）；(b) §11 中对应条目的「证据」列指向本任务真实存在的测试名或文件；(c) §14 归属表无一条语义同时属于本任务与 Agent Loop；(d) `git ls-files tool/` 与 §12 表一一对应（差集为空）；(e) `git diff` 中设计文档的改动范围不超出上述四节。任一项不成立 → 不算满足。

## Complexity

COMPLEX

原因：AC-004 的去重范围与命中语义、AC-003 中单个工具局部超时/取消/panic 的归类（现有长期设计只定义了整体超时与可恢复/不可恢复工具失败，未覆盖工具级超时）、以及 Registry 的并发注册语义与 JSON Schema 支持子集（零第三方依赖约束下需自实现），都是会产生不同业务结果与后续 Loop 集成成本的规则选择；同时 §15 禁止实现期私自扩展公共符号，新增 `Executor` 等公共 API 必须先经 Contract 由 Owner 确认；`Design Impact = UPDATE` 还要求这些语义先有一致的设计表述（§7 / §11 / §14），并与 Agent Loop 的归属边界一次划清。

## Analyst Questions

1. 重复 Tool Call ID 的处理规则：去重范围是什么（同一 Run / 同一 Executor 实例 / 同一 assistant 消息）？命中重复时返回什么（复用首次 `ToolResult` / 返回显式错误并携带首末 CallID / 交给配对校验拒绝）？该规则如何与 INV-002（assistant 消息内 ID 唯一、重复 Result 被配对校验拒绝）保持不自相矛盾，并写入 Contract 让后续 Agent Loop 直接遵循。
2. 工具级局部超时与取消的语义归属：单个工具超时映射为可恢复的 `ToolResult{IsError:true}`（Run 保持 `RUNNING`）、还是以 Go `error` 上抛由 Loop 决定、还是新增 `ErrorKind`；工具超时与整体 `deadline_exceeded` / `cancelled` 如何区分，是否需要在错误中携带 `CallID`。若需新增 `ErrorKind`，说明是否构成 `core` 公共契约变更及处理路径。
3. 参数校验失败与未知工具的可恢复性：应返回 `ToolResult{IsError:true}`（模型可自行修正重试）还是返回 Go `error`（由 Loop 映射为 `FAILED`）？两类失败是否共用同一种承载形式，`Content` 文本需要包含哪些定位信息（工具名 / 字段路径）。
4. Registry 语义：静态构建（注册后只读）还是支持运行期注册；是否要求并发安全（多 goroutine 同时 `Lookup` / `Specs` / 注册）；重复注册检测的判定粒度（同名即拒绝 / 同名同 Spec 幂等 / 大小写与空白归一化）；`Specs()` 的输出顺序是否必须稳定（影响 LLM 请求可复现性）。
5. JSON Schema 支持子集：在零第三方依赖前提下，支持到哪一档（`type` / `properties` / `required` / `additionalProperties` / 嵌套对象 / 数组 `items` / 枚举）；不支持的 Schema 关键字是拒绝注册还是忽略；类型不匹配与 `null` 的处理规则。该决定必须可测试且写清边界。
6. Design 更新的最小一致集（`Design Impact = UPDATE` 已由 Owner 确认）：Contract 中每条失败语义必须有唯一归属方（本任务单工具级 / Agent Loop 跨步骤级）；据此给出 §7 新增或改写的行、§11 条目的编号与证据指向、§14 需要从 Agent Loop 名下收回的条目、§12 需要登记的文件路径清单。设计应在 Contract `APPROVED` 后由 Analyst 先行落库，Coder 按其实现并只做 §12 路径登记。

## Review Baseline

- Base commit：`6902d24`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论`）。当前分支 `tool-registry-executor-v1` 指向 `6902d24`，与本地 `develop`、`origin/develop`、`origin/tool-registry-executor-v1` 全部一致。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无未跟踪文件。
- 重叠修改的区分方式：TaskBuilder 只创建并拥有 `.agent/tasks/tool-registry-executor-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`（当前未提交）；业务源码由 Coder 在独立 commit 中引入，Contract 由 Analyst 维护。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX，`contract.status = PENDING`）
