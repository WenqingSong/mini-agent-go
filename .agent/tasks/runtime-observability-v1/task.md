# Task: Runtime 执行日志可观测性（runtime-observability-v1）

## Goal

在既有 `runlog` 事件契约之上交付可真实落地的结构化运行日志：同一次 Run 的模型调用与工具调用能在日志中被关联定位，失败与超时能从日志判断出「哪一次调用、哪一阶段、什么原因」，输出目标可注入以便测试，凭据与用户原始正文不进入日志；并且日志只是观察者，不改变任何业务执行结果。

## Scope

- 在 `runlog` 包内基于标准库 `log/slog` 实现结构化日志能力（零第三方依赖），把 `runlog.Event` 渲染为可机器解析的结构化记录。
- 为记录提供执行标识字段：`core.ExecutionRef{SessionID, RunID}`、`Step`、`CallID`，使同一次 Run 的模型调用与工具调用可被筛选并串联成一条调用序列。
- 记录执行耗时、结果状态与错误类别：耗时的单位与来源、结果状态（终态/非终态）与 `core.ErrorKind` 必须在输出中可读、可断言，而不是只能靠人读文本推断。
- 提供便于测试的输出接口：允许注入输出目标（`io.Writer` 或 `slog.Handler`，最终形态由 Contract 定稿），测试无需真实文件、网络或外部服务。
- 敏感信息防护：凭据类字段（API Key / Secret / Token / Authorization / Password 等，含嵌套位置与常见命名变体）与用户消息原始内容不得原样落盘；脱敏规则可测试。
- 日志旁路与健壮性：写入失败、`slog` Handler 异常、`ctx` 已取消、事件载荷异常时，不得 panic、不得长时间阻塞、不得改写传入的 `Event`，也不得改变 Run 状态 / 计数 / 消息序列（Design §10 旁路原则、INV-008）。
- 并发安全：多个 goroutine 同时记录事件时，每条记录必须是完整、可独立解析的结构化单元（不交织、不丢字段），`-race` 无告警（为后续并行工具调用预留）。
- 更新 Design Artifact `docs/design/runtime-architecture.md`：把 §10 中「具体落盘格式属后续任务」的占位落实为与 APPROVED Contract 一致的字段语义与脱敏边界；在 §12 文件所有权表登记本任务新增/变更的源文件；在 §14 收窄已由本任务承担的归属项。改动范围只限这三节。
- 测试：结构化字段与关联性、失败与超时定位、敏感信息防泄漏、写入失败 / 取消 / 异常隔离、并发记录，以及零第三方依赖与依赖方向（INV-001）的机械核对。

## Out of Scope

- Agent Loop 本身：不实现多轮编排、不改事件发出点接线，不改 `core` / `llm` / `tool` / `session` / `contextmgr` 的公共契约。真实运行期的事件完整性与旁路成立归 Agent Loop 任务（Design §14）。
- 真实 Provider、真实工具、Session 持久化的实现及其日志接入。
- 日志后端/采集链路接入、采样、轮转、异步批量上报、metrics、分布式追踪（trace / OpenTelemetry）、日志检索界面。
- 日志文件管理与滚动策略；进程级 `slog` 默认 Logger 初始化与 CLI 装配（本任务不做装配决策，只提供可注入的实现）。
- 在实现阶段私自新增或改变 `runlog` 公共符号（含 `Event` 字段、`Sink` 方法）；需要扩展时先走 Contract Revision 并由 Owner 确认（Design §15）。
- 引入第三方日志库（zap / logrus / zerolog）、测试框架或断言库；根模块维持零 `require`。
- 修改 `go.mod` 的 module 与 go 版本、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）、`docs/design/agent-workflow.md`；在共享 `develop` 上提交。
- 重写 `runtime-architecture.md` 中已被 T00 固化的其他章节（分层、消息模型、预算、Session 归属、失败语义表等）。
- 全项目日志规范推广（分级策略、字段命名统一到所有模块、敏感字段外置配置）；本任务只覆盖 Runtime Run 执行日志。
- 里程碑交付验收：本轮 Owner 未指定 Deliverer 里程碑，Milestone 节暂缺；如需启用 Deliverer，由 Owner 给出里程碑标识后由 TaskBuilder 补充（本任务全部 AC 可在进程内证明，不依赖外部服务）。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`（§10 运行日志、§12 文件所有权表、§14 归属收窄）

判定依据：Design §10 明确把「具体落盘格式、采样、上报」留给后续任务，本任务正是落实该长期事实（日志字段语义与脱敏边界）；§12 要求新增或改名文件必须在同一交付内回写所有权表；§15 要求公共符号变更先经 Contract Revision 并同步本文件。Owner 需求标注为 `Design Impact: NONE`，与此判定不一致处已记录在 Relevant Context，供 Owner 在 Contract Decision 前校正——若 Owner 判定应保持 `NONE`，则字段语义降级为 `runlog` 模块内部实现细节、Design §10/§12 不再作为 AC，本任务对应的 §12 登记由后续统一维护任务承担。

## Acceptance Criteria

- [ ] AC-001：给定同一次 Run 的 `core.ExecutionRef{SessionID, RunID}`，当依次记录 `run_started`、`llm_request`、`llm_response`、`tool_started`、`tool_finished`（携带 `CallID`）、`run_finished` 时，每条输出记录都带同一 `session_id` / `run_id`，工具调用记录带与 `core.ToolCall.ID` 相同的 `call_id`，模型调用可按 `run_id` + `step` + 事件类型定位；当同时存在第二个 Run 的事件时，按 `run_id` 过滤只能取到该 Run 自己的记录，不混入另一 Run 的任何行。
- [ ] AC-002：当事件携带 `*core.RunError`（至少 `provider_failure`、`tool_failure`、`cancelled`、`deadline_exceeded`，以及 `llm_call_limit` / `tool_call_limit` 之一）时，输出中包含可机读的错误类别、定位字段（`op`，工具失败时含 `call_id`）与该阶段耗时；单条记录即可判定失败种类与失败发生位置，且 `cancelled` 与 `deadline_exceeded` 在输出中可区分（不得被合并成同一类别或同一文本）。
- [ ] AC-003：当事件载荷包含凭据类键值（如 `api_key` / `apiKey` / `authorization: Bearer …` / `token` / `secret` / `password`，含嵌套对象）时，在完整输出字节中检索不到原始敏感值（以固定占位符替代），而同一记录中的非敏感业务字段仍然保留；当载荷携带用户消息正文（`core.Message.Content[].Text`）时，输出中不出现该正文原文（只允许长度或显式摘要形式）。
- [ ] AC-004：给定写入必然失败的目标（如返回 `error` 的 `io.Writer` 或 `Handle` 失败的自定义 `slog.Handler`）、已取消的 `ctx`、以及含异常值/不可序列化内容的事件载荷，记录过程不 panic、在测试给定的超时内返回，传入的 `Event` 值不被改写；调用方在接入与不接入该日志实现两种情况下拿到的 `core.RunResult`（状态、计数、消息序列）逐字段相同。
- [ ] AC-005：给定注入到内存缓冲的输出目标，测试无需真实文件、网络或外部服务即可捕获并逐条解析结构化记录；当 N 个 goroutine 并发写入 M 条事件时，每条记录都能被完整解析（无交织、无字段丢失），`go test -race ./runlog/...` 无 data race 告警。
- [ ] AC-006：给定 APPROVED Contract，当逐项核对 `docs/design/runtime-architecture.md` §10 / §12 / §14 与实现时，日志字段语义、耗时与结果状态/错误类别的表达、脱敏边界三者一致无未记录偏差；`§12` 中与 `runlog/` 相关的路径与 `git ls-files runlog/` 一一对应（差集为空）；`§14` 中已由本任务承担的条目不再同时归属 Agent Loop 任务；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./runlog` 显示只 import 标准库与 `<module>/core`，且 `git diff go.mod` 为空（零第三方依赖）。

## Relevant Context

- 已核实事实：`runlog/runlog.go` 目前只有 `EventKind`（仅类型、无常量）、`Event{Kind, Ref, Step, CallID, Summary, Data, Error}`、`Sink` 接口与 `Discard()`；全仓库无任何 `slog` 使用，也没有日志实现。
- 已核实事实：`runlog.Event` 不含耗时与时间戳字段，`EventKind` 也没有常量定义——因此「记录执行耗时」「结果状态」在现有契约下没有承载点，这是本任务实现前必须先定的契约问题（见 Analyst Questions 1–3）。
- 已核实事实：`core` 已提供 `ExecutionRef{SessionID, RunID}`、`RunStatus`（`RUNNING` / `COMPLETED` / `FAILED` / `LIMIT_EXCEEDED` / `CANCELLED`）、`ErrorKind`（9 值）、`*RunError{Kind, Op, Message, CallID, Cause}`、`StatusOf(err)` 与 `KindOf(err)`；`Event.Error` 即 `*core.RunError`，日志无需自造错误分类。
- 已核实事实：Design §10 规定日志是观察者、事件以 run 为单位组织（`ExecutionRef` + step + `CallID` + 已脱敏 `Data`），并明确「具体落盘格式、采样、上报属后续任务」；§12 要求新增文件回写所有权表，表中当前仅有 `runlog/runlog.go`、`runlog/runlog_test.go`；§14 把「runlog 旁路在真实运行时成立、事件完整性」归 Agent Loop 任务。
- 已核实事实：`go.mod` 为 `module cnb.cool/framework-station/mini-agent-go` / `go 1.24` / 零 `require`；本机 `go1.24.1`；当前 `go build ./...`、`go vet ./...`、`go test ./...` 全部通过（`runlog` 仅 `Discard` 测试）。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空），无 migration、无错误码域。
- 已核实事实：本任务分支 `feat/runtime-observability-v1` 指向 `6902d24`，与本地 `develop`、`origin/develop`、`origin/feat/runtime-observability-v1` 一致；`git status --short` 为空。并行任务分支 `feat/agent-context-manager-v1`、`tool-registry-executor-v1` 已存在。
- Assumption：`Event.Summary` 语义为「已脱敏的人读摘要」，其内容由发出端负责；本任务只保证不额外输出消息正文。若 Contract 判定 `Summary` 也需过滤/截断，属范围变更，需回 TaskBuilder。
- Assumption：本任务全部 AC 可在进程内证明（单元测试 + `-race`），不需要真实 MySQL / Redis / MQ / HTTP / Docker 环境。
- Assumption：Design 文档由 Analyst 在 Contract `APPROVED` 后先行落库，Coder 只做一致落地与 §12 路径登记（与 `tool-registry-executor-v1` 的既有做法一致）。
- OPEN QUESTION（不阻塞 Analyst）：并行 T01—T05 若各自形成「往 `runlog` 发事件」的局部约定，可能与本任务定稿的字段 schema 冲突；需由 Analyst 在 Contract 中固定发出端必须遵守的最小字段契约，本任务不替 Loop 决定编排。
- 待 Owner 确认：`Design Impact` 由本任务判为 `UPDATE`（理由见 Design Impact 节），与 Owner 需求中的 `NONE` 不一致；若 Owner 坚持 `NONE`，需在 Contract Decision 前明确，届时本任务 AC-006 与 Design 相关 Scope 需回退。

## Verification

- AC-001 → `runlog` 表驱动测试：两个 Run 的事件序列写入注入的 `bytes.Buffer`，逐行解析结构化记录，断言 `session_id` / `run_id` / `step` / `call_id` 取值与构造值一致，且按 `run_id` 过滤的结果只含该 Run 的记录（含事件类型顺序）。
- AC-002 → 表驱动测试分别注入 `provider_failure`、`tool_failure`（带 `CallID`）、`cancelled`、`deadline_exceeded`、`llm_call_limit` 事件，断言错误类别、`op`、`call_id`、耗时字段存在且等于构造值；断言 `cancelled` 与 `deadline_exceeded` 输出的类别字段不同。
- AC-003 → 敏感样例（顶层与嵌套 `api_key`、`Authorization: Bearer …`、`password`、`token`、私钥片段）与消息正文原文注入 `Data`，对完整输出字节做子串扫描：原始值不出现、占位符出现、非敏感字段仍在；另断言正文原文不出现。
- AC-004 → 失败 writer、`Handle` 返回错误的 Handler、已取消 `ctx`、含异常值/不可序列化内容的事件：断言不 panic、在测试超时内返回、`Event` 记录前后 `reflect.DeepEqual` 相等；同一 `core.RunResult` 在记录前后逐字段相同。
- AC-005 → 并发用例（N goroutine × M 事件）逐行解析成功率 100%；`go test -race ./runlog/...` 无告警、退出码 0。
- AC-006 → Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对并给出逐条证据：§10 字段语义与脱敏边界句子存在且与实现一致；§12 路径与 `git ls-files runlog/` 差集为空；§14 无同一语义双重归属；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./runlog` 只出现标准库与 `<module>/core`；`git diff go.mod` 为空。
- 通用检查：仓库根 `make test`、`go vet ./...`、`go test -race ./runlog/...` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 仍通过（Engine 为独立 Module）。环境：Linux + Go 1.24.x，无需外部服务。

## Complexity

COMPLEX

原因：实现前必须先定三处会长期约束并行任务的选择——(1) 耗时与结果状态在当前 `runlog.Event` 中无承载点，是扩展公共 `Event` 还是规定 `Data` 子 schema，决定 Agent Loop 等发出端的写法与兼容成本；(2) 脱敏属于安全边界（哪些键必须替换、未知键保留还是丢弃、是否允许正文摘要），不同选择带来不同的泄漏风险；(3) 写入失败 / Handler 异常 / `ctx` 取消下的旁路与错误返回语义，决定 `Sink` 实现契约与 Loop 的调用方式。同时 Design §15 禁止实现期私自扩展公共符号，新增 `Sink` 实现 API 必须先有 Owner 确认的 Contract；`Design Impact = UPDATE` 还要求字段语义先与 Design §10 一致。

## Analyst Questions

1. 耗时与事件形状：是扩展 `runlog.Event`（新增耗时/时间戳/状态字段），还是保持 T00 形状并由 Contract 规定 `Data` 的结构化子 schema 承载？两者对并行 T01 发出端、Cleaner 可核查性与后续兼容成本分别如何取舍？`EventKind` 是否补齐为常量（当前只有类型）。
2. 耗时来源与语义：耗时由发出端测量后随事件传入，还是由 Sink 依据事件到达时间推算；单位（毫秒/纳秒）、缺失时是否允许省略、时钟来源（`time.Now` 还是可注入 `Clock` 以便测试）。
3. 结果状态表达：终态用 `core.RunStatus` 原值，还是用 `ErrorKind` + `core.StatusOf` 推导；`tool_started` / `llm_request` 等非终态事件是否也带状态；`cancelled` 与 `deadline_exceeded` 的可区分粒度要求（字段级还是文本级）。
4. 脱敏边界：采用「已知敏感键替换 + 未知键保留」还是「白名单保留 + 其余丢弃或摘要」；覆盖范围（顶层与嵌套、`api_key` / `apiKey` / `Authorization` / `Bearer` 等变体）、占位符形式、是否允许输出正文的长度或哈希等派生信息、`Summary` 是否也需过滤。
5. `Sink` 的失败与健壮性语义：写入失败时 `Record` 返回 error 还是吞掉；是否要求 `panic` 恢复；是否允许丢弃；`ctx` 已取消时的行为。需给出可直接写进测试的契约句子，并与 INV-008（日志旁路）一致。
6. 落点与可测试接口：实现放在 `runlog` 包内新文件还是 `runlog/slog` 子包；公开哪些符号（构造函数、选项、Handler 适配器）；如何注入输出（`io.Writer` / `slog.Handler` / 两者）以满足「测试不需要真实文件与网络」。
7. Design 更新最小集（`Design Impact = UPDATE`）：§10 需要新增哪些字段语义与脱敏边界句子；§12 需要登记哪些路径；§14 中哪一条应从「Agent Loop 任务」收窄或保持（避免同一语义双重归属）；确认 Design 改动不超出 §10 / §12 / §14。

## Review Baseline

- Base commit：`6902d24`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）`）。当前分支 `feat/runtime-observability-v1` 指向 `6902d24`，与本地 `develop`、`origin/develop`、`origin/feat/runtime-observability-v1` 全部一致。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无未跟踪文件；`go build / vet / test ./...` 基线全绿。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/runtime-observability-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`runlog/**` 由 Coder 在独立 commit 引入；`contract.md` 由 Analyst 维护；`docs/design/runtime-architecture.md` 由 Analyst 在 Contract `APPROVED` 后按 §10 / §12 / §14 更新，且改动不得外溢到其他章节。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX，`contract.status = PENDING`）
