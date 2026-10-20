# Task: Runtime 核心契约与基础工程结构（v1）

## Goal

交付一个可长期演进的 Runtime 架构基线：项目级长期设计文档已由 Owner 批准，仓库根 Go Module 可构建、可测试，且后续并行模块（LLM / Tool / Session / Context / 运行日志）可以只依赖这一层公共接口、数据模型与错误语义开始各自实现，而不再回头修改公共契约。

## Scope

- Analyst 完成整体架构与关键执行语义设计，起草 `contract.md` 并请求 Owner Contract Decision（`APPROVED` 后才进入实现）。
- Owner 批准后，在 `docs/design/runtime-architecture.md` 落地长期架构设计（模块职责、依赖方向、不变量、失败语义、文件所有权）。
- 初始化仓库根 Go Module（`go.mod`）与必要的工程目录；不使用 `go.work` / `replace` 与 `.agent/engine` 关联。
- 定义统一数据模型：`Message`、`ToolCall`、`ToolResult`、`RunStatus` 及统一错误类型。
- 定义 `LLM`、`Tool`、`Session`、`Context`、运行日志（run log）之间的最小必要接口。
- 明确并写入契约：Session 与 Todo 的状态归属；工具执行的会话身份传递方式；Agent Loop 次数预算；取消语义；工具失败语义；消息与 Tool Call/Result 配对语义。
- 明确各模块的文件所有权，保证后续并行任务不会修改同一公共文件。
- 建立真实可执行的 `Makefile` 测试入口（`make test`），解决 `.cnb.yml` 的 `pr-check` 已调用 `make test` 但仓库无 `Makefile` 的既有缺口。
- 契约层测试：编译性验证 + 关键语义（配对、状态枚举映射、取消/预算/工具失败的可表达性）的表驱动测试。

## Deliverables

- `docs/design/runtime-architecture.md`（长期设计，Owner 批准后的权威架构事实）
- `.agent/tasks/runtime-core-contracts-v1/contract.md`（Analyst，复杂任务必需）
- 仓库根 `go.mod` 与契约/接口 Go 源码（目录结构由 Contract 固化）
- 仓库根 `Makefile`（至少 `test`，可含 `build` / `vet`）
- 契约层测试文件（`_test.go`）

## Out of Scope

- 完整 Agent Loop 调度实现、多轮对话运行、并发工具执行调度。
- 具体 LLM Provider 客户端（OpenAI / Anthropic / HTTP、鉴权、streaming、重试、限流）。
- 具体 Tool 实现（文件、Shell、HTTP、MCP 等）及其沙箱与权限控制。
- Session / Todo 的持久化（DB、Redis）、Migration、错误码域等全局资源占用。
- CLI、HTTP Server、可运行二进制、Docker / 部署流水线扩展。
- 后续阶段模块（Tool 实现、Provider、持久化）的实现代码。
- 修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`。
- 引入 `go.work` 或跨 module `replace`（`AGENTS.md` 明确 Engine 与业务 module 不关联）。
- 性能优化、基准测试、可观测性后端接入。

## Milestone

Milestone: M1-runtime-core-contracts

## Design Impact

Design Impact: NEW
Design Artifact: `docs/design/runtime-architecture.md`

本任务新增项目级长期事实：根模块与包边界、核心数据模型（消息 / 工具调用 / 运行状态）、跨模块不变量（Session 与 Todo 归属、工具执行身份传递）、执行语义（预算、取消、工具失败、消息配对）与模块文件所有权。这些事实将被后续所有 Runtime 任务依赖，因此必须先在 Contract 中确认、再沉淀为 Design、最后与实现一致。

## Acceptance Criteria

- [ ] AC-001：给定仓库根 Go Module，当执行 `go build ./...`、`go vet ./...`、`go test ./...` 时全部包编译通过且不存在 import cycle；同时按 Contract 固化的分层方向，底层契约包不 import 上层实现包（以 `go list` 依赖输出机械核对，出现反向依赖即失败）。
- [ ] AC-002：给定统一 `Message` / `ToolCall` / `ToolResult` 类型，当构造「assistant 消息带 0 个、1 个、多个（≥2）Tool Call」三种场景时，调用顺序、调用标识与参数可被无损表达；当用 Tool Call 标识关联 Tool Results 时，每个 Result 能被唯一定位到对应 Call；当构造缺失 Result、重复 Result 标识、Result 引用不存在 Call 的配对时，校验返回明确错误而不是静默接受。
- [ ] AC-003：给定 Contract 与长期 Design，当逐项核对 Session 与 Todo 的状态归属、工具执行的会话身份传递、Agent Loop 次数预算、取消、工具失败、消息配对六项语义时，每一项都有一处可执行定义（不是描述性口号），且 `RunStatus` 与错误类型能区分「正常完成 / 被取消 / 工具失败 / 预算耗尽」四种结果，并有表驱动测试固化该映射。
- [ ] AC-004：给定 Owner 对 `contract.md` 的 `APPROVED` 决定，当 Cleaner 四方核对 Task ↔ `contract.md` ↔ `docs/design/runtime-architecture.md` ↔ 实现时，公开接口、数据模型、枚举与错误语义三者一致、无未记录偏差；Design 文档包含模块职责、依赖方向、不变量、失败语义与文件所有权表。
- [ ] AC-005：给定干净检出，当在仓库根执行 `make test` 时退出码为 0，且该目标真实运行根模块测试（不是空目标或仅 `echo`）；`make test` 与 `.cnb.yml` 中 `pr-check` 调用的入口一致；根模块 `go build ./...`、`go vet ./...` 通过；`.agent/engine` 作为独立 module 的 `GOWORK=off go -C .agent/engine test ./...` 仍通过，未被根模块影响。
- [ ] AC-006：给定 Design 中的文件所有权表，当核对实际仓库结构与 Contract 时，除明确标注为工程公共文件的 `go.mod` / `Makefile` 外，每个源文件只归属一个模块；表中路径与实际文件一一对应（新增或改名已回写），使后续并行任务不存在需要共同修改的公共源文件。

## Relevant Context

- 已核实事实：仓库根当前没有 `go.mod`、没有 `Makefile`，也没有任何 Go 业务源码；已跟踪内容只有 `docs/design/agent-workflow.md`、`.agent/**`、`AGENTS.md`、`.cnb.yml`。
- 已核实事实：`.cnb.yml` 在 `(main|develop)` 的 `pull_request` 触发 `pr-check`，镜像 `golang:1.24`，执行 `make test`；当前该命令无对应目标（既有缺口）。
- 已核实事实：`.agent/engine` 是独立 Go Module（`module workflow-v2-engine`，`go 1.23.0`），`AGENTS.md` 要求它与宿主业务 Module 之间不使用 `go.work` / `replace`。
- 已核实事实：本机 `go1.24.1`、`GNU Make 4.3` 可用；`.agent/bin/workflow-check` 为本地构建产物（Git 忽略）。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空）。
- 已核实事实：`docs/design/` 目前只有 Workflow 自身的 `agent-workflow.md`，尚无 Runtime 架构设计。
- 已核实事实：`state.yaml` 采用 `schema_version: 3` 严格解码（未知字段直接 ERROR）。
- Assumption：`docs/design/runtime-architecture.md` 为本任务 Design Artifact 的落点路径；若 Analyst 认为与既有命名约定冲突需改名，须回 Owner / TaskBuilder 更新本任务，不在实现阶段自行改路径。
- OPEN QUESTION：根 Module 的 `module` 路径未由 Owner 指定（影响全部 import 路径）。不阻塞 Analyst 分析，但必须在 Contract 中由 Owner 确认后才能交 Coder。
- OPEN QUESTION：本轮接口深度（仅接口 + 契约测试，还是附带最小 reference 实现）未由 Owner 指定，交 Analyst 在 Contract 中给唯一推荐并由 Owner 确认。

## Verification

- AC-001 → 仓库根执行 `go build ./...`、`go vet ./...`、`go test ./...`，退出码 0；用 `go list -deps ./...`（或 `go list -f '{{.ImportPath}} {{.Imports}}'`）对照 Contract 的分层表核对依赖方向，发现底层包 import 上层实现包即判失败。
- AC-002 → 表驱动单元测试：0 / 1 / N（≥2）个 Tool Call 的构造与往返（round-trip）用例；非法配对三类用例（缺失 Result、重复 Result 标识、孤儿 Result）各断言返回的错误类型/标识，而非 nil。
- AC-003 → 单元测试断言 `RunStatus` 与错误类型的四种结果映射；Cleaner 按 Contract 的语义清单逐条定位 Design 章节与对应测试，任一条无对应实现或测试 → 不算满足。
- AC-004 → Owner 在 Contract Decision 中 `APPROVED`（`state.contract.status=APPROVED` 且绑定证据 commit）；Cleaner 做四方一致性核对，Design 与实现不一致处必须回写或在 Contract 中记录偏差理由。
- AC-005 → `make -n test` 确认目标存在且非空；`make test` 退出码 0 并输出真实测试结果；对照 `.cnb.yml` 确认命令一致；`GOWORK=off go -C .agent/engine test ./...` 单独通过。验证环境：Linux + Go 1.24.x + GNU Make，无需外部服务（MySQL / Redis / MQ 均不涉及）。
- AC-006 → 用 `git ls-files` 输出与 Design 所有权表逐条比对；发现同一源文件被两个模块声明拥有即判失败。

## Complexity

COMPLEX

原因：涉及重要架构与公开协议选择（统一消息模型、接口边界、错误语义）、跨模块不变量（Session/Todo 归属、工具执行身份传递）、执行语义（预算、取消、工具失败、配对）以及后续并行任务的文件所有权边界；多个现实方案会产生不同的后续实现成本与扩展性结果，需 Analyst 分析并由 Owner 在 Contract 中确认。

## Analyst Questions

1. 根 Module 路径与包分层：`module` 路径需 Owner 确认；分层（契约层 / 运行时层 / 适配层）与目录结构由 Analyst 提出唯一推荐并在 Contract 固化，需说明如何用 `go list` 机械验证 AC-001 的依赖方向。
2. 统一消息模型形态：单一 struct + 可选字段，还是 content-part 多态；是否为 Provider 原始载荷保留扩展位；Tool Call/Result 配对在构造期强制还是提供独立校验入口。不同选择直接决定 AC-002 的错误语义与后续 Provider 适配成本。
3. Session 与 Todo 的状态归属：由 Runtime 内存持有、还是抽象 Store 接口（本轮只定接口不实现持久化）；工具执行的会话身份传递用 `context.Context` 携带还是显式参数。该选择会锁定后续所有 Tool 与 Provider 的方法签名。
4. Agent Loop 执行语义：预算计数单位（LLM 调用 / 工具调用 / step）；预算耗尽属错误还是正常终止状态；工具失败是回填给模型继续、还是终止 Run；多个 Tool Call 的顺序与部分失败处理。AC-003 要求这些语义可区分，需明确到可测试粒度。
5. 本轮接口深度：只定义接口 + 契约测试，还是提供最小 reference 实现（如内存 Session / echo LLM）以让 `make test` 有真实覆盖。影响 Coder 工作量与 AC-005 的可信度。
6. 错误类型体系：统一 error 类型 + 种类枚举，还是按模块分型；是否需要跨模块错误码。注意本项目 `resources: {}`，若判定需要 `error_code_domain`，须先由 Owner 在 machine config 声明 kind，属 Scope 变更，不在本任务内自行新增。
7. 工程入口细节：`Makefile` 的 `test` 是否只跑根模块（排除 `.agent/engine`），以及如何在 Makefile 中保持 Engine 独立 module 的测试命令不被根模块吞掉；是否顺带提供 `build` / `vet` 目标（供本地与后续 CI 复用）。

## Review Baseline

- Base commit：本地 `develop` = `367c655`（`chore(agent): 从 Workflow v4.3.0 回退部署 v3.0.0 稳定版`）；`origin/develop` = `ebf8778`。本地 `develop` 领先 `origin/develop` 1 个 commit（`367c655` 尚未推送）。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean。
- 任务分支：尚未准备。当前位于 `develop` 分支，本任务文件已落盘但未提交（Agent 不得在 shared `develop` 上提交，也不得自行创建分支）。Owner 创建任务分支后，本任务基线以该分支首个任务 commit 的父提交为准，并由 TaskBuilder 在该分支提交 `task.md` / `state.yaml` / 初始文件。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/runtime-core-contracts-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；其余文件由对应角色在各自 commit 中引入。若发现他人遗留 dirty 文件，STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX）
