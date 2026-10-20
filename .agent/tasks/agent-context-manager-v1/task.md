# Task: Agent 上下文预算、窗口管理与摘要压缩（v1）

## Goal

交付可装配的上下文构建能力：在给定 token 预算下，把 System Prompt、历史摘要与近期消息组合成可直接送给 Provider 的合法消息视图；历史超预算时按确定边界压缩为摘要并保留必要近期消息，压缩失败时显式失败，不产生虚假的完整记忆。

## Scope

- Analyst 调查既有 `contextmgr` 契约与 `core` 消息/配对模型，起草 `contract.md` 并请求 Owner Contract Decision（`APPROVED` 后才进入实现）。
- 按 Contract 实现 `contextmgr.Manager` 的具体构建逻辑：窗口选择、预算控制与历史压缩。
- 实现可配置的 Token 估算（注入式估算器 + 默认启发式估算，不引入真实 tokenizer 依赖），所有预算判断基于可替换的估算器。
- 达到配置阈值时压缩较早历史：通过独立 `Summarizer` 接口产出摘要，摘要与近期消息共同组成输出视图。
- 定义独立 `Summarizer` 接口；仓库内只提供测试用 Fake/脚本化实现，全部自动化测试不调用真实模型。
- 消息原子性：assistant 消息的 ToolCalls 与其后续全部 ToolResults 作为一个整体保留或整体压缩，输出视图必须通过 `core.ValidateMessages`。
- 处理压缩失败、超长输入、预算无法满足和非法配置，给出调用方可区分的显式错误，不返回越界或配对非法的部分视图。
- 单元测试覆盖预算边界、长历史模拟、Fake Summarizer、工具消息配对与失败语义；`go build ./...`、`go vet ./...`、`go test ./...` / `make test` 通过。

## Out of Scope

- 任何真实模型调用、真实摘要服务、第三方 tokenizer（tiktoken 等）或网络依赖。
- Agent Loop 装配与调度（T01）、Provider 客户端（T02）、Tool 实现与执行（T03）、Session 持久化（T04）、运行日志落地（T06）。
- 修改 `core` 公共契约（含新增 `ErrorKind`）、`llm`、`tool`、`session`、`runlog`：如实现确需，走 Contract Revision 并由 Owner 决定，不在本任务内自行扩张。
- 更新 `docs/design/runtime-architecture.md` 等长期 Design（除非 Analyst 在 Contract 中把 Design Impact 校正为 `UPDATE` 并经 Owner 确认，见 Relevant Context）。
- 摘要的跨 Run 持久化、增量重写、缓存、并发构建、性能基准与可观测性上报。
- CLI / HTTP 入口、可运行二进制、Docker 与部署流水线。
- 多模态内容与 content-part 扩展。

## Acceptance Criteria

- [ ] AC-001：给定包含 System Prompt、历史消息与明确 `TokenBudget` 的请求，当估算总量未超预算时，则构建结果保持原始顺序与内容完整，`Compacted=false`、`DroppedMessages=0`，且按注入的估算器计算不超过预算；相同输入重复构建结果一致。
- [ ] AC-002：给定长历史且估算总量超预算，当达到配置的压缩阈值时，则较早历史被压缩为摘要并保留至少 `KeepRecent` 条最近消息，输出视图重新满足预算，`Compacted=true`、`DroppedMessages` 与实际被压缩/丢弃的条数一致（不得虚报）；摘要来自注入的 `Summarizer`，不经过真实模型。
- [ ] AC-003：给定历史中 assistant 消息携带 ToolCalls 且其 ToolResults 紧随其后，当压缩或窗口边界落在该组合内部时，则该「调用 + 全部结果」被整体保留或整体压缩，输出视图通过 `core.ValidateMessages`；不产生孤儿 Result、缺失 Result 或重复 Result。
- [ ] AC-004：给定 `Summarizer` 返回错误或不可用摘要，当构建需要压缩时，则 `Build` 返回非 nil 的 `*core.RunError` 且不返回被标记为已压缩的视图；不得用空串/占位摘要冒充完整记忆，也不得静默丢弃历史后报告成功。
- [ ] AC-005：给定预算无法满足（例如预算不足以容纳 System Prompt 与最小可保留原子单元）或配置非法（例如负 `KeepRecent`），当调用 `Build` 时，则返回可区分原因的显式错误，且不返回超预算或配对非法的部分视图；空历史、预算恰好等于所需、`KeepRecent` 大于消息总数、超长单条消息等边界均有确定且被测试固定的行为。
- [ ] AC-006：给定全部自动化测试，当执行 `go test ./...`（或 `make test`）时，则上下文构建相关测试全部通过且不发起真实模型/网络调用（使用 Fake Summarizer 与确定性估算器）；`contextmgr` 仍只依赖标准库与 `core`（`go list -deps` 机械核对），`go build ./...` 与 `go vet ./...` 通过。

## Relevant Context

- 已核实事实：`contextmgr/contextmgr.go` 已存在（T00 交付），当前只定义 `Request{ExecutionRef, Messages, TokenBudget, KeepRecent}`、`Result{Messages, Compacted, DroppedMessages}` 与 `Manager.Build`；注释已约定 `TokenBudget <= 0` 表示不限制、`KeepRecent` 表示至少保留最近 N 条、输出必须是合法配对序列。
- 已核实事实：`core` 已有 `Message` / `ToolCall` / `ToolResult`、`ValidateMessages`、`ValidateToolPairing` 与统一 `*core.RunError` + `ErrorKind`；配对违规固定返回 `invalid_pairing` 并定位 `CallID`，配对校验是独立入口而非构造期强制。
- 已核实事实：`docs/design/runtime-architecture.md` §2 规定 `contextmgr` 属 L1，只依赖标准库与 `core`（INV-001 用 `go list` 核对）；§9 记录「契约只暴露 Request{...} 与 Result{...}，token 估算与压缩算法属后续任务」；§14 把「token 预算、窗口截断、压缩算法」归属 Context 任务，并把 `TokenBudget` 的实际语义列为未验证项。
- 已核实事实：根模块 `go.mod` 为 `go 1.24`、零第三方依赖；`Makefile` 提供 `test`（`go test ./...`）/ `build` / `vet`；`.cnb.yml` 的 `pr-check` 调用 `make test`。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空）。
- 已核实事实：当前分支 `feat/agent-context-manager-v1` 与 `origin/feat/agent-context-manager-v1` 均指向 `6902d24`（= `develop` 头，T00 delivery commit）；工作区 clean，尚无本任务目录。
- 已核实事实：`.agent/tasks/` 当前只有已完成交付（`ACCEPTED` / `PASS`）的 `runtime-core-contracts-v1`（T00）。
- Assumption：Owner 声明的 `Design Impact = NONE` 被沿用。但新增/修改 `contextmgr` 对外接口（如 `Summarizer`、System Prompt / 摘要字段）可能与 `docs/design/runtime-architecture.md` §9 的表述产生偏差，需 Analyst 在 Contract 中校正；若校正结论为 `UPDATE`，则增加设计文档交付物属 Scope 变化，须由 Owner 确认。
- Assumption：`Summarizer` 的生产实现不在本任务范围，测试 Fake 只用于测试，不提供可被误接入生产装配路径的默认假实现。
- OPEN QUESTION：Owner 需求要求上下文包含 System Prompt，但 T00 的 `Request` 无该字段。System Prompt 如何进入构建视图（新增字段 / 由调用方预置 system 消息 / 其他）交 Analyst 提出唯一推荐并由 Owner 在 Contract 中确认。
- OPEN QUESTION：本任务未由 Owner 指定 Deliverer 验收的里程碑标识，因此 `task.md` 未声明 Milestone；若后续需要 Deliverer，须先由 Owner 补充，Deliverer 不得自行推断。

## Verification

- AC-001 → 表驱动单元测试，注入确定性估算器（按固定 token 计数），对「未超预算」用例断言消息序列逐条相等、顺序不变、`Compacted=false`、`DroppedMessages=0`、估算值 ≤ `TokenBudget`；重复调用结果一致。
- AC-002 → 长历史模拟（构造远超预算的消息序列，含 summary 已存在场景），Fake Summarizer 记录被压缩区间与返回摘要；断言触发压缩、`Compacted=true`、最近消息保留、`DroppedMessages` 与输入/输出条数差一致、输出估算值 ≤ 预算。
- AC-003 → 构造带 0 / 1 / N（≥2）个 ToolCall 的 assistant 消息与对应 ToolResults，并把预算边界刻意落在组合中间；断言输出经 `core.ValidateMessages` 无错误，且该组消息要么整体存在、要么整体被摘要替换（不允许只剩 Call 或只剩 Result）。
- AC-004 → Fake Summarizer 返回 error、空摘要、超长摘要三类用例；断言 `Build` 返回非 nil 错误且 `KindOf(err)` 可识别，返回的 `Result` 不被标记为已压缩，不产生占位摘要文本。
- AC-005 → 边界用例表：空历史、`TokenBudget` 恰好等于所需、预算小于 System Prompt、预算小于最小可保留原子单元、`KeepRecent` 大于消息总数、负 `KeepRecent`、超长单条消息；每项断言确定的返回值或错误种类，且失败时无部分视图。
- AC-006 → 仓库根执行 `go build ./...`、`go vet ./...`、`go test ./...`（等同 `make test`），退出码 0；用 `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./contextmgr` 核对只含标准库与 `cnb.cool/framework-station/mini-agent-go/core`；测试在无网络、无模型凭据环境下运行。
- 环境：Linux + Go 1.24.x；本任务不涉及 MySQL / Redis / MQ / 外部服务。

## Complexity

COMPLEX

原因：涉及模块公开协议扩展（Token 估算器、`Summarizer`、System Prompt / 摘要进入视图的方式）、消息与工具调用配对的原子性边界、压缩边界与保留策略的多方案取舍，以及压缩失败、超长输入和预算不可满足的错误语义；这些选择会决定装配层如何调用 `contextmgr`，不同方案产生不同的正确性与可恢复性结果，需 Analyst 分析并由 Owner 在 Contract 中确认。

## Analyst Questions

1. Token 估算抽象：使用接口注入、函数类型注入还是配置 + 默认启发式（如字符数/固定系数）？默认估算器如何取值、按消息还是按整段累加、是否预留误差余量、超长输入的估算上限处理。`TokenBudget <= 0 表示不限制` 的既有约定是否保留。
2. `contextmgr` 公开契约的扩展形态：System Prompt 与摘要如何进入 `Request` / `Result`（新增字段、摘要消息放在哪个 Role、是否保留既有字段语义与向后兼容）；这会改变 Design 文档 §9 的表述，需明确 Design Impact 是否应校正为 `UPDATE`，以及是否需 Owner 确认新增设计文档交付物。
3. `Summarizer` 接口形态：输入（待压缩消息、是否携带既有摘要、`ExecutionRef`）、输出（纯文本或结构化摘要）、是否允许分段/部分压缩；错误语义、空摘要与超长摘要如何处理；测试 Fake 的归属（仅测试包内）与生产默认实现是否属于本任务。
4. 压缩边界与消息原子性：assistant ToolCalls 与其 ToolResults 的原子单元如何界定（一次响应多个 Call 是否同属一个单元）；`KeepRecent` 按消息条数还是按原子单元计数；System Prompt 是否永不压缩、摘要占用的预算如何计入与取舍；阈值（触发点）与目标水位如何定义。
5. 失败与不可满足语义：预算小于 System Prompt 或最小原子单元时返回哪种 `ErrorKind`（是否复用 `invalid_argument`，避免新增 `ErrorKind` 造成跨模块契约变化）；压缩失败是返回错误还是降级返回未压缩视图（必须与 AC-004 一致）；`Compacted` / `DroppedMessages` 在错误路径上的取值约定。
6. 与并行任务的边界：T05 只改 `contextmgr/**`；若确需修改 `core`（新错误种类、配对辅助函数）或 `docs/design/runtime-architecture.md`，如何在不与 T01—T04、T06 产生文件冲突的前提下处理（交 Owner 决策或延后）。

## Review Baseline

- Base commit：`6902d2445665ec356a2db22b33b7749d39d8018e`（`develop` 与 `origin/develop` 当前头，T00 delivery commit）；当前分支 `feat/agent-context-manager-v1` 与 `origin/feat/agent-context-manager-v1` 同指该 commit。
- 任务开始时已有修改：无暂存、无未暂存、无未跟踪业务文件；`git status --porcelain` 为空，本任务目录此前不存在。
- 重叠修改的区分方式：本任务角色只写 `.agent/tasks/agent-context-manager-v1/**`；Coder 只改 `contextmgr/**`。与 T01—T04、T06 并行，禁止改动 `core/**`、`llm/**`、`tool/**`、`session/**`、`runlog/**`、`go.mod`、`Makefile`、`docs/design/**`、`.agent/` 其他内容。发现他人遗留 dirty 文件时 STOP 并报告，不 stash、不覆盖、不使用 `git add -A` / `git add .`。

## Initial Route

交 Analyst（COMPLEX）
