# Technical Contract

## Decision Status
APPROVED

## Problem

`contextmgr` 目前只有 T00 留下的形状契约（`Request{Messages, TokenBudget, KeepRecent}` / `Result{Messages, Compacted, DroppedMessages}` / `Manager.Build`），没有任何构建逻辑：没有 token 估算，没有窗口选择，没有压缩，没有 System Prompt 与历史摘要进入视图的方式，也没有失败语义。

本 Contract 要解决的是：在给定预算下如何确定地产生一个「可送 Provider 的合法消息视图」；压缩产生的新摘要与覆盖范围如何通过明确输出契约交还调用方（供 Session Manager 跨轮次持久化）；压缩失败、预算不可满足时系统如何显式失败且不造成历史丢失。任务边界固定为 `contextmgr/**` 与（`Design Impact=UPDATE` 经 Owner 确认后）`docs/design/runtime-architecture.md`；不实现真实摘要服务、不引入 tokenizer、不改 `core` 公共契约。

## Verified Current Behavior

- VERIFIED：`contextmgr/contextmgr.go` 只有 `Request`（内嵌 `core.ExecutionRef`，含 `Messages/TokenBudget/KeepRecent`）、`Result`、`Manager` 三个符号，无构造器、无实现；注释已约定 `TokenBudget <= 0` 不限制、`KeepRecent` 为「至少保留最近 N 条」、输出必须是合法配对序列。
- VERIFIED：`core.ValidateMessages` 先逐条 `Message.Validate` 再 `ValidateToolPairing`；配对违规固定 `invalid_pairing` 并定位 `CallID`；孤儿 Result、缺 Result、重复 Result 都被拒绝（`core/pairing.go:26-78`）。
- VERIFIED：现有 `Result{Messages, Compacted, DroppedMessages}` **不能**表达「新摘要文本」；`DroppedMessages` 只能表达条数、不能直接表达被覆盖消息的身份集合。因此本 Contract 为 `Result` 增加 `Summary` 字段（Owner D1 批准，不要求机械保持 `Result` 不变）。
- VERIFIED：`core` 的 `contentTypeText = "text"` 是**未导出**常量，包外只能写 `core.ContentType("text")`；`Message.Validate` 会拒绝其他类型，因此字面量写错会在校验期暴露（`core/message.go:21,94-98`）。
- VERIFIED：`core.ErrorKind` 现有 9 种，其中可用于「参数/预算不可满足」的是 `invalid_argument`，可用于「内部/摘要失败」的是 `internal`；Out of Scope 禁止新增 `ErrorKind`（`core/error.go:15-34`）。
- VERIFIED：`docs/design/runtime-architecture.md` §9 明确「契约只暴露 `Request{Messages, TokenBudget, KeepRecent}` 与 `Result{...}`」，§12 文件所有权表把 `contextmgr` 限定为 `contextmgr/contextmgr.go`，§14 把 `TokenBudget` 语义列为未验证项。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`，本任务不需要任何 Shared Resource Kind；`state.yaml` 的 `contract.status=PENDING`，`resources.reservations={}`。
- VERIFIED：当前分支 `feat/agent-context-manager-v1`（HEAD `66d500c`，task 定义 commit）与 `origin/feat/agent-context-manager-v1` 同指同 SHA；`origin/develop=6902d24`；工作区 clean；Go 1.24.1，根模块零第三方依赖，`Makefile` 提供 `test/build/vet`。
- VERIFIED：仓库内不存在 `Summarizer`、压缩相关符号或测试（全仓 `*.go` 检索 `Summarizer|compaction|摘要` 无命中）。
- UNKNOWN：默认启发式估算对中文/CJK 的偏差幅度（无真实 tokenizer，无法在本任务内量化）；本任务以「可注入估算器 + 文档化启发式」承担，不算设计阻塞。

## Recommendation

RECOMMENDATION：保持 `Manager.Build` 签名与 `Result` 既有字段语义不变，只做**最小必要扩展**，把「System Prompt / 已有摘要 / 估算器 / 摘要器」显式化，压缩仅由 `TokenBudget` 触发，失败一律显式返回 `*core.RunError` 且不返回部分视图。

```go
type Request struct {
    core.ExecutionRef
    SystemPrompt    string         // 可为空；渲染为视图首条 system 消息，永不压缩
    PreviousSummary string         // 可为空；上一次压缩产生的摘要，永不压缩，参与合并摘要
    Messages        []core.Message  // 仅 user / assistant / tool；出现 RoleSystem → invalid_argument
    TokenBudget     int            // 估算预算：<= 0 不限制；启发式估算不宣称等于真实模型 token 数
    KeepRecent      int            // 保留近期消息的目标条数（按消息条数）
}

type Result struct {
    Messages        []core.Message
    Compacted       bool
    DroppedMessages int
    Summary         string // 新增：本次压缩产生的新摘要；Compacted=false 时为空
}

type Estimator interface {
    EstimateMessage(core.Message) int
}

type HeuristicEstimator struct{} // 默认启发式，可直接值使用

type SummarizeRequest struct {
    core.ExecutionRef
    PreviousSummary string
    Messages        []core.Message // 待压缩的完整原子单元序列（原顺序）
}

type Summarizer interface {
    Summarize(ctx context.Context, req SummarizeRequest) (string, error)
}

func NewManager(estimator Estimator, summarizer Summarizer) (Manager, error)
```

关键取舍：

1. **System Prompt 与摘要用显式字段，而不是塞进 `Messages` 的 system 消息**。理由：AC-001 明确要求「请求包含 System Prompt」；显式字段让「永不压缩」可判定，避免「哪些 system 消息算 pinned」的歧义。代价：`Request` 新增字段、`Messages` 中出现 system 消息被拒绝（比 `core` 更窄的输入契约）。
2. **只保留一个触发点：完整输入视图估算 > `TokenBudget` 时压缩**，不引入 ratio/水位配置。理由：AC-001 要求「未超预算必须保持完整」，任何 <100% 的阈值都会与 AC-001 冲突；多一个比例配置只会增加未验证语义。AC-002 的「配置的阈值」由 `TokenBudget` 本身承担。
3. **摘要以一条独立 `system` 消息插入在 System Prompt 之后**，纯文本，`ID`/`Raw` 为空；旧 `PreviousSummary` 通过 `SummarizeRequest.PreviousSummary` 与新历史一起重新摘要，由新摘要整体替换，不同时保留两条摘要（D1）。
4. **压缩算法：先按 `KeepRecent` 取尾部，再逐步吞并最老保留单元直到满足预算**，并设 `Summarizer` 调用上限与进展检查（D3）。确定性优先，代价是可能多次调用 `Summarizer`。
5. **`KeepRecent` 目标无法在预算内满足时明确失败**，不做放宽保留的降级（D3 批准的取舍：宁可显式失败，不破坏配对、不返回部分视图）。

## Selected Design

已按 Owner D1—D5 确认，最终设计如下。

**输入约束**：`Messages` 只允许 `user` / `assistant` / `tool`；出现 `RoleSystem` 视为调用方重复携带 System Prompt → `invalid_argument`。System Prompt 通过 `Request.SystemPrompt` 提供，历史摘要通过 `Request.PreviousSummary` 提供。

**输出契约（跨轮次持久化）**：

- `Result.Summary` 是本次压缩产出的新摘要的权威文本，与 `Result.Messages` 中的摘要 system 消息内容逐字节一致；`Compacted=false` 时为空串。
- 压缩只压缩输入 `Messages` 的**严格前缀**（最老的一组 Unit），因此被摘要覆盖的消息恰为 `req.Messages` 的前 `DroppedMessages` 条（按输入顺序），`DroppedMessages` 即覆盖条数。
- Session Manager 的持久化配方：
  - `Compacted=true`：以 `Result.Summary` **全量替换**已存摘要（旧摘要已并入新摘要，不单独保留）；从已持久化历史中删除前 `DroppedMessages` 条消息；`Result.Messages` 尾部即新的保留历史。
  - `Compacted=false`：不改动已存摘要；视图中的摘要消息（若 `SystemPrompt` 非空则为第二条，否则为第一条）文本等于 `PreviousSummary`。
- T05 只输出压缩结果，**不直接读写 Session Store**；调用方按上述配方自行持久化。

**压缩算法（确定性，按此顺序）**：

1. 校验配置与输入：`KeepRecent < 0`、`Messages` 含 `RoleSystem`、构造期 estimator/summarizer 为 nil → `invalid_argument`；`core.ValidateMessages` 失败 → 原样返回该错误（保持 `Kind`/`CallID`）；`ctx` 已取消/超时 → `cancelled` / `deadline_exceeded`。
2. 计算完整输入视图的估算值（System Prompt 消息 + PreviousSummary 消息 + 全部 `Messages`）；`TokenBudget <= 0` 或未超预算 → 直接返回，`Compacted=false`、`Summary=""`、`DroppedMessages=0`。
3. 把 `Messages` 划分为原子 Unit（见 Interfaces and Data）；从尾部按 Unit 累加，取「消息条数 ≥ `KeepRecent`」的最小尾部。若已无更早 Unit 可压缩（含 `KeepRecent` ≥ 总条数、或历史只剩尾部），→ `invalid_argument`（保留约束下无法满足预算，D3：明确失败，不降级）。
4. 调用 `Summarizer` 压缩除尾部外的全部 Unit，得到摘要文本，组装视图并复算估算值。
5. 仍超预算 → 把保留尾部中最老的一个 Unit 并入压缩集合，重新调用 `Summarizer`，重复至满足预算。单次 `Build` 的 `Summarizer` 调用上限为 `unitCount + 1`；每一轮必须使保留尾部消息数严格减少（至少并入一个 Unit）。达到上限或无法取得进展 → `internal`（「压缩无法取得有效进展」），禁止无限循环（D3）。
6. 输出前再次以 `core.ValidateMessages` 自检（防御）；自检失败按 `internal` 处理。

**失败时不丢历史**：所有错误路径返回零值 `Result`（`Messages=nil`、`Compacted=false`、`DroppedMessages=0`、`Summary=""`），调用方拿不到任何新摘要与覆盖信息；按持久化配方，调用方对失败 Build 不得修改任何持久化状态，旧历史与旧摘要保持原样。因此旧摘要只会被「成功返回的新摘要」显式替换，不存在静默丢弃或静默截断。

## Design Impact

- `Design Impact = UPDATE`，目标 Artifact 为 `docs/design/runtime-architecture.md`，在 Contract `APPROVED` 后、Coder 实现前更新 §9（`contextmgr` 公开契约与压缩/持久化语义）、§12（`contextmgr` 文件所有权表）、§14（`TokenBudget` 语义从未验证改为已验证）。
- 前置条件（D2）：`task.md` 的 Design Impact 与设计文档交付物属 TaskBuilder 权限范围，须先由 TaskBuilder 更新 `task.md`，Analyst 才写 `docs/design/**`；未完成前不进入 Design 同步与 Coder Handoff。

## Interfaces and Data

必须保持（不得破坏）：

- `Manager.Build(ctx, Request) (Result, error)` 签名；`Result` 既有三字段语义；`ExecutionRef` 继续内嵌在 `Request`；`TokenBudget <= 0` = 不限制；输出视图必须通过 `core.ValidateMessages`。
- 输入消息的既有顺序、内容、配对在保留区间内逐条不变（不重排、不合并、不改写 ToolResult）。

本次建立：

- `Request.SystemPrompt`、`Request.PreviousSummary`、`Result.Summary`、`Estimator`、`HeuristicEstimator`、`SummarizeRequest`、`Summarizer`、`NewManager`。
- 生成的 system 消息形态：`core.Message{Role: core.Role("system"), Content: []core.ContentPart{{Type: core.ContentType("text"), Text: ...}}}`，`ID` 与 `Raw` 为空。因 `core` 未导出 `text` 常量，`contextmgr` 只能使用该字面量；错误字面量会被 `core.ValidateMessages` 拒绝（测试覆盖）。
- 原子单元（Unit）定义：assistant 消息若携带 ToolCalls，则「该消息 + 紧随其后、应答其全部 CallID 的 tool 消息」构成一个 Unit；其余消息各自为 Unit。输入先经 `core.ValidateMessages` 校验，因此不会出现以 tool 消息开头的 Unit，也不会出现被截断的 Unit。
- `TokenBudget` 是**估算预算**，只覆盖 `contextmgr` 构建的上下文视图；`HeuristicEstimator` 不宣称等于真实模型 token 数；工具 Schema、模型输出等预算由 T07 在视图之外预留。
- 默认估算器 `HeuristicEstimator`：每条消息固定开销 4 token，加上各 `ContentPart.Text`、`ToolCall.Name`、`ToolCall.Arguments`、`ToolResult.Name`、`ToolResult.Content` 的 rune 数按 `ceil(runes/4)` 求和；空文本片段计 0。仅依赖标准库。

## Business Invariants

本任务编号，与 `runtime-architecture.md` 的 INV-xxx 相互独立。

- INV-001：`Build` 成功返回时，`Result.Messages` 必过 `core.ValidateMessages`，且按注入估算器求得的总 token ≤ `TokenBudget`（`TokenBudget <= 0` 时不做此约束）。
- INV-002：`SystemPrompt` 与摘要永不被压缩、截断或丢弃；它们分别固定为视图首条消息与紧随其后的 system 消息；保留区间内的输入消息逐条保序保内容。
- INV-003：一个 Unit 要么整组保留，要么整组被摘要替换；输出中不得出现孤儿 Result、缺失 Result、重复 Result。
- INV-004：任何错误路径返回零值 `Result`（四字段全零/空），不得返回超预算或配对非法的部分视图。
- INV-005：`Compacted=true` ⇔ `Summary != ""` 且 `DroppedMessages ≥ 1`；`DroppedMessages` 恰等于最终被压缩 Unit 包含的消息条数，且被覆盖消息恰为 `req.Messages` 的前 `DroppedMessages` 条（不虚报、不含 `PreviousSummary`）。
- INV-006：相同 `Request` + 相同注入实现（确定性估算器与 Fake Summarizer）→ 逐条相同的 `Result`。
- INV-007：`contextmgr` 只依赖标准库与 `core`（`go list` 机械核对）。
- INV-008：单次 `Build` 内 `Summarizer` 调用次数 ≤ `unitCount + 1`，且每轮压缩后保留消息数严格减少；不可能无限循环。

## Failure and Consistency Semantics

「成功」代表：返回的视图可直接交 Provider，满足预算、配对与保留约束，且 `Result.Summary` / `DroppedMessages` 足以让调用方把会话推进到「新摘要 + 保留历史」的一致状态；不代表摘要已持久化，也不代表跨 Run 记忆完整。

失败映射（不新增 `ErrorKind`，不泄漏敏感错误信息，`Cause` 仅供诊断）：

| 情形 | Kind | 说明 |
| --- | --- | --- |
| `KeepRecent < 0`、`Messages` 含 system、构造期 estimator/summarizer 为 nil、估算器返回负值 | `invalid_argument` | 调用方契约错误 |
| 输入消息结构/配对非法 | 原样返回 core 错误 | 保持 `invalid_pairing`/`invalid_argument` 与 `CallID` |
| 预算无法满足（含 System Prompt、`KeepRecent` 目标、摘要本身超预算） | `invalid_argument` | 错误文本给出可识别的具体原因（哪一部分装不下） |
| `Summarizer` 返回 error | `internal` | `Cause` 保留原始错误，`errors.Is/As` 可追溯 |
| 摘要为空或全空白 | `internal` | 不得用占位文本冒充摘要 |
| 达到 `Summarizer` 调用上限或压缩无进展 | `internal` | 「压缩无法取得有效进展」 |
| `ctx` 取消 / 超时 | `cancelled` / `deadline_exceeded` | 不透传为 `internal` |

错误路径不返回任何可被误判为成功的压缩结果；调用方不据此修改持久化状态，历史与旧摘要保持原样。

## Allowed / Forbidden Changes

- 允许：`contextmgr/**`（实现与测试，可新增文件如 `estimator.go`、`summarizer.go`、`manager.go` 及对应 `_test.go`）；`.agent/tasks/agent-context-manager-v1/contract.md`；TaskBuilder 更新 `task.md` 后由 Analyst 更新 `docs/design/runtime-architecture.md`。
- 禁止：修改 `core/**`、`llm/**`、`tool/**`、`session/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml`；新增 `core.ErrorKind`；引入第三方 tokenizer、网络调用、真实模型或生产默认 `Summarizer` 实现（仓库内只允许测试包内的 Fake）；改动其他 task 目录；在 `task.md` 更新前写 `docs/design/**`。

## Verification Requirements

- INV-001 → 表驱动用例（含 `TokenBudget` 恰好等于所需、超 1 token）；断言 `core.ValidateMessages` 无错且估算和 ≤ `TokenBudget`。
- INV-002 → 用例同时带 `SystemPrompt` 与 `PreviousSummary`，强制压缩；断言前两条消息固定且内容不变、尾部输入消息逐条相等。
- INV-003 → 构造 0/1/N（≥2）个 ToolCall 的 assistant 消息，把预算边界刻意落在 Unit 内部；断言整组保留或整组消失，无孤儿/缺失/重复 Result。
- INV-004 → `Summarizer` 返回 error / 空串 / 全空白；预算小于 System Prompt；`KeepRecent` ≥ 总条数；每例断言返回 `*core.RunError`、`KindOf` 符合上表、`Result` 四字段为零值。
- INV-005 → 长历史压缩用例：断言 `Compacted=true`、`Summary` 等于视图摘要消息文本、`DroppedMessages` 等于被压缩 Unit 的消息条数、被覆盖消息恰为输入前 `DroppedMessages` 条、`len(Result.Messages) = system?+1+尾部条数`。
- INV-006 → 同输入连续调用两次，`reflect.DeepEqual` 相等；再以不同注入估算器验证预算判断确实随注入变化。
- INV-007 → `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./contextmgr` 只含标准库与 `core`；`go build ./...`、`go vet ./...`、`go test ./...`（`make test`）退出码 0，测试无网络。
- INV-008 → 构造「每轮吞并一个 Unit 仍超预算」的场景，断言 `Summarizer` 被调用次数 ≤ `unitCount+1`，最终要么成功要么返回 `internal`，不得死循环。
- 环境：Linux + Go 1.24.x，无 MySQL/Redis/MQ/外部服务，无真实模型调用。

## Open Risks

- 默认启发式按 4 字符/token 估算，对中文（CJK）可能明显低估，真实 Provider 仍可能溢出；`TokenBudget` 只是估算预算，生产装配须注入校准后的估算器。
- 逐 Unit 吞并可能多次调用 `Summarizer`（受 `unitCount+1` 上限约束），真实摘要成本未在 v1 优化；T07 接入真实 LLM Summarizer 时须把摘要调用计入 `RunPolicy` 预算。
- `core` 未导出 `text` 内容类型常量，`contextmgr` 需复制字面量 `"text"`；若 `core` 变更取值，只能靠校验期错误发现。
- `Messages` 禁止 system 消息是比 `core` 更窄的输入契约，装配层（T01）必须把 system 内容交给 `SystemPrompt`，否则会拿到 `invalid_argument`。

## Owner Decision Record

| 编号 | 决定 | 适用范围 |
| --- | --- | --- |
| D1 | 有条件批准：`Request` 显式增加 `SystemPrompt`、`PreviousSummary`；`Messages` 不得重复携带 System Prompt；`Result` 增加 `Summary` 字段以输出新摘要；`DroppedMessages` 表达覆盖条数、被覆盖消息为输入严格前缀；旧摘要与待压缩历史一起重新摘要、不静默丢弃；T05 只输出结果，不写 Session Store | 本 Contract 全篇；跨轮次持久化配方见 Selected Design |
| D2 | 批准 `Design Impact = UPDATE`；须先由 TaskBuilder 更新 `task.md`，再由 Analyst 更新 `runtime-architecture.md`，不得绕过 Task 权限边界 | `docs/design/**` 与 Design 同步时序 |
| D3 | 批准 `TokenBudget` 为唯一压缩触发阈值；未超预算不得压缩；超预算按完整 Unit 压缩；System Prompt 保留；`KeepRecent` 超预算时明确失败（不降级）；`Summarizer` 调用须有上限与进展检查；`TokenBudget` 是估算预算，T07 负责为工具 Schema、模型输出预留 | 压缩算法、失败语义、Design §9/§14 表述 |
| D4 | 批准失败语义：预算不可满足 → `invalid_argument`（含可识别具体原因）；摘要失败/空摘要/无进展 → `internal`（保留 Cause）；错误路径不返回部分压缩结果；不新增 `ErrorKind`，不泄漏敏感信息 | Failure and Consistency Semantics 全表 |
| D5 | 批准 `Summarizer` 纯文本摘要、`NewManager` 拒绝 nil；测试用 Fake Summarizer，不接真实模型；生产 LLM Summarizer 归 T07，且 T07 须将摘要调用计入 `RunPolicy` | 接口定义、测试策略、T07 边界 |
