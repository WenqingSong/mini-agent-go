# Task: Turn 事务内历史前缀删除（session-turn-drop-leading-v1）

## Goal

为 `session.Turn` 增加 `DropLeading(n int) error`，使调用方能在**同一次 `RunTurn`** 内，把「已提交历史的前缀删除」「摘要更新」「本 Turn 新消息追加」当作一个事务提交：成功则三者一起生效，失败、超时或取消则 Session 的已提交状态逐字段不变。交付后，后续 Agent Runtime（T07）可以在一次 Turn 中完成「压缩历史 + 写入摘要 + 追加新消息」的原子落库，而不需要先删除再写入这种可能产生中间态的路径。

## Scope

- Analyst 起草 `contract.md`（`DropLeading` 的计数语义与合法域、错误语义、与 `Append` / `SetSummary` 的组合语义、提交链路核实结论），并请求 Owner Contract Decision（`APPROVED` 后方可进入实现）。
- Analyst **必须核实提交链路**：以当前 `Manager.RunTurn → commitTurn → Turn.staged() → publish` 的实际实现为准，证明"在 `Turn` 内删除已提交历史前缀"会真正进入 Session 的已提交快照（而不是只改变内存工作视图）。若只需要改 `Turn`，在 Contract 中明确记录该结论与证据；若不足，按最小必要范围提出变更并交 Owner 批准，不得制造只在内存工作副本生效的假实现。
- Analyst 按 `Design Impact: UPDATE` 更新 `docs/design/runtime-architecture.md`：§8.1 中 Turn 写入能力的长期陈述（当前只有消息追加 / 摘要 / Todo）、§11.1 会话管理不变量的覆盖范围，以及 §12 文件所有权表（若涉及新文件或职责变化）。
- Coder 实现公共接口 `(*session.Turn).DropLeading(n int) error`（签名由 Owner 指定，不得改名或改形），语义要求：
  - 只删除**进入本次 Turn 时已提交的历史**（`Turn.base`）的前 `n` 条，单位是 `core.Message` 条数（与 `contextmgr.Result.DroppedMessages` 的计数口径一致）；
  - 不得删除或移动本 Turn 通过 `Append` 新增的消息；
  - 校验 `n` 的合法域（含 `0`、负数、超过已提交历史长度），非法输入原子拒绝；
  - 拒绝会产生非法结果的删除边界，尤其是切断 Tool Call 与 Tool Result 合法配对的前缀边界；
  - Turn 关闭后（回调已返回）拒绝继续修改，行为与其它写方法一致；
  - 不改动 `core.ValidateMessages` / `core.ValidateToolPairing` 的既有语义，不新增 `core.ErrorKind`。
- 长期保留的测试：确定性单元测试（合法删除、非法 `n`、配对边界拒绝、`n=0`、`n=len(base)`、`DropLeading` + `SetSummary` + `Append` 组合提交、回调失败 / 取消 / 超时后状态不变、Turn 关闭后拒绝）+ 并发测试（Turn 内多 goroutine 并发调用不 data race、不重入死锁），并保持现有 `session` 测试全绿。
- 保持根模块 `go build ./...`、`go vet ./...`、`go test ./...`、`make test` 通过，`go test -race -count=5 ./...` 无 race 报告；`.agent/engine` 独立 module 测试仍通过；不新增第三方依赖。

## Out of Scope

- T07（`agent-runtime-loop-v1`）的 Agent Loop 实现与装配：本任务只提供能力，不写 Loop、不接线上下文压缩。
- 修改 `contextmgr`：不改 `Request` / `Result` / `DroppedMessages` / 压缩算法，也不把压缩接入 Turn。
- 修改 `core` 既有语义：不新增 `core.ErrorKind`，不改 `Message` / `ValidateMessages` / `ValidateToolPairing` / `RunStatus` 的行为。
- 新增存储系统、持久化或 Store 接口变更；`Manager` / `Store` 的任何变更都必须先由 Analyst 报告最小必要范围并经 Owner 批准，未经批准一律不做。
- 中间删除、任意区间删除、按消息 ID 删除、按 CallID 删除——本任务只做**前缀删除**。
- 摘要生成算法、token 估算 / 预算、窗口截断策略（归 `contextmgr`）。
- CLI / HTTP API / Docker / 可观测性后端 / 性能基准与压测目标。
- 全局资源占用：本任务不新增 `migration_version`、`error_code_domain` 等 Shared Resource（当前 `.agent/workflow.yaml` 为 `resources: {}`），`state.resources.reservations` 保持空。
- 修改 shared `develop`、合并分支、绕过 Gate；修改 `AGENTS.md`、`.cnb.yml`、`.agent/**`（本任务自身 task artifacts 除外）。

## Design Impact

Design Impact: UPDATE

Design Artifact: `docs/design/runtime-architecture.md`

本任务修改既有项目级长期事实：

- 长期公开协议：新增一个公共方法 `Turn.DropLeading`，是 T07 及后续装配层要消费的接口事实。
- 既有不变量与数据模型边界：`session` 的「Turn 暂存写入」能力集合从「追加消息 / 摘要 / Todo」扩展为「含已提交历史前缀删除」，需要同步 §8.1 的 Turn 提交语义描述与 §11.1 的不变量覆盖（是否存在需要新增不变量条目或扩展既有条目，由 Analyst 判定）。
- 文件所有权：若新增源码 / 测试文件，§12 所有权表必须同步登记，保持单归属。

因此 Design 更新与实现同批交付，并由 Cleaner 做 Task ↔ APPROVED Contract ↔ Design ↔ 实现四方核对。

## Acceptance Criteria

- [ ] AC-001：给定一个会话已有 `m` 条已提交历史，且本次 Turn 内先按合法边界删除前缀、再追加 `k` 条新消息并成功结束回调，则提交后从 `Manager.Get` 读到的历史恰为「原历史第 `n` 条之后的部分 + 本次新增的 `k` 条」，逐条 ID、顺序与内容一致，总条数为 `m-n+k`；`n=0` 为空操作、历史不变；`n=len(base)` 时已提交历史被清空，提交结果仅含本次新增消息。
- [ ] AC-002：给定任意 Turn，当调用 `DropLeading` 传入负数、或传入超过"进入 Turn 时已提交历史长度"的值（特别是本 Turn 已 `Append` 若干消息后，用超过已提交历史长度的 `n` 试图删到本 Turn 新增消息），则返回 `invalid_argument` 语义错误，且已提交历史与本次 Turn 暂存消息逐条不变；已提交的 Session 状态不被破坏。
- [ ] AC-003：给定已提交历史中存在合法的 Tool Call / Tool Result 配对，当删除边界会使「剩余历史 + 本 Turn 暂存消息」不再满足 `core.ValidateToolPairing`（例如删除后残留引用已删除 Tool Call 的 Tool Result，或保留下来的 Tool Call 失去了本应位于前缀中的结果），该次 `DropLeading` 被拒绝，返回 `invalid_pairing` 语义错误并定位到具体 CallID；拒绝是本次调用的原子拒绝，已提交历史与暂存区均保持调用前状态。
- [ ] AC-004：给定同一次 Turn 内依次调用 `DropLeading`、`SetSummary` 与 `Append`，当回调成功且 `context` 有效时，三者一起生效：读取到的历史、摘要与新增消息与本次 Turn 的期望值一致，不出现"历史已删但摘要未更新"或"摘要已更新但历史未删"的中间态；当同一次 Turn 中任一写入被拒绝（如非法配对的前缀边界或非法消息批次）导致回调返回错误时，历史、摘要、消息三者全部保持提交前状态。
- [ ] AC-005：给定 Turn 内已暂存前缀删除与摘要更新，当回调返回错误、或 `context` 被取消 / 超时，则 `RunTurn` 返回对应错误，Session 的已提交历史、摘要与 Todo 逐字段与调用前相等（不产生部分提交、不回滚他人已提交内容）；给定回调已返回（Turn 关闭），当再调用 `DropLeading` 则被拒绝并返回 `invalid_argument` 语义错误，不改变任何状态。
- [ ] AC-006：给定同一 Turn 内多个 goroutine 并发调用 `DropLeading` / `Append` / `SetSummary` / `Session()`，当全部返回后不出现 data race、不出现重入死锁或永久阻塞，且每次调用的效果符合 Contract 定义的原子粒度；`go test -race -count=5 ./...` 在根模块全绿，既有同会话串行 / 跨会话并发测试不回归。
- [ ] AC-007：给定 `Design Impact: UPDATE`，当核对任务、APPROVED `contract.md`、`docs/design/runtime-architecture.md` 与最终实现时：`DropLeading` 的公共签名与语义、Turn 写入能力集合、前缀删除与 `Append` / `SetSummary` 的组合语义、错误语义四者一致；Design 中不再残留"Turn 只支持追加消息"这类与实现冲突的陈述；新增源文件与测试文件全部出现在 §12 所有权表且单归属。

## Relevant Context

- 已核实事实：`session.Turn`（`session/turn.go`）内部持有 `sessionID`、`base []core.Message`（进入 Turn 时的已提交历史**私有深拷贝**）、`messages []core.Message`（暂存追加）、`summary`、`todos`；`Turn.Session()` 与 `Turn.staged()` 都以 `base + messages` 拼装视图与提交内容。当前没有任何前缀删除能力。
- 已核实事实：提交是**全量替换**而不是增量合并——`Manager.RunTurn`（`session/manager.go:188-221`）在取得执行权后 `newTurn(*e.snapshot.Load())`，回调返回后经 `commitTurn`（`session/manager.go:400-428`）取 `staged()` → `core.ValidateMessages` → `m.publish(...)` 发布整份 `Session`。因此"只改 `Turn`（截断 `base`）"在当前实现下**能够**持久化前缀删除，不需要 `Store` / `Manager` 新方法；该结论仍须由 Analyst 在 Contract 中记录证据，并由 Coder 用「提交后 `Get` 读到截断结果」的测试证明，不能只证明内存视图变化。
- 已核实事实：`core.ValidateMessages` = 逐条 `Message.Validate` + `core.ValidateToolPairing`；配对违规固定返回 `invalid_pairing` 并携带消息序号与 `CallID`（`core/pairing.go`）。`ErrorKind` 现有 9 种，`invalid_argument` 与 `invalid_pairing` 已足以表达本任务的拒绝语义。
- 已核实事实：`Turn` 的既有拒绝模式是"**单次调用级原子拒绝**"——`Append` 对 `base + 暂存 + 新批次` 全量校验，失败时暂存区保持调用前状态，Turn 不因此关闭、仍可继续提交合法操作；Turn 关闭后写方法统一返回 `turnClosedError`（`invalid_argument`，"Turn 已结束，不再接受写入"）。
- 已核实事实：`Turn.mu` 已保护每次调用；`Manager.RunTurn` 保证同一会话串行、跨会话并发。Turn 内不存在与 Manager 执行权的二次加锁路径，因此新增方法只要保持同样的加锁粒度就不会引入重入死锁。
- 已核实事实：`contextmgr.Result.DroppedMessages` 的语义是"被压缩覆盖的消息条数，恰为输入 `Messages` 的前端前缀条数"（`contextmgr/contextmgr.go:35-42`），与本任务"已提交历史前缀条数"口径一致；这是 T07 消费 `DropLeading` 的直接语义来源，但本任务不修改 `contextmgr`。
- 已核实事实：`docs/design/runtime-architecture.md` §8.1 目前把 Turn 的写入描述为"隔离暂存区（消息、摘要、Todo）"，§11.1 的 INV-016 覆盖"Todo 单一事实源与暂存可见性"，§12 按文件登记所有权；本任务落地后这些陈述需要更新，故为 `Design Impact: UPDATE`。
- 已核实事实：根模块 `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零第三方依赖），`Makefile` 的 `test` 目标为 `go test ./...`，`-race` 未进入 CI 需本地显式执行；`.agent/engine` 是独立 module。
- Assumption（有既有先例支撑，Analyst 若选择不同语义须在 Contract 说明并经 Owner 批准）：`DropLeading` 的拒绝粒度与 `Append` 一致——单次调用原子拒绝，Turn 不因此关闭，调用方可继续用合法参数操作并在同一 Turn 内提交。
- Assumption：`n` 只作用于"进入 Turn 时已提交历史"，因此同一 Turn 内多次 `DropLeading` 的语义需要 Contract 明确（相对原始已提交历史累积，还是相对当前剩余历史继续删）；两种选择都会影响 AC-001 的期望值，属 Analyst 必答项。
- Assumption：本任务未由 Owner 声明 `Milestone`，因此 `task.md` 不含 Milestone 小节；若 Owner 要求 Deliverer 做里程碑运行验收，需由 Owner 补充里程碑标识后启用。
- OPEN QUESTION（不阻塞，交 Analyst 提唯一推荐并由 Owner 在 Contract 中决定）：`n == len(base)`（清空全部已提交历史）与 `n == 0`（空操作）的合法性已按 Owner 表述"超过历史长度才非法"处理为合法，但由于清空后剩余序列可能仅剩本 Turn 新消息，是否还要附加"提交后历史不得为空"这类额外约束，需要 Contract 明确。

## Verification

- AC-001 → 确定性单元测试：构造包含 system / user / assistant(ToolCall) / tool(ToolResult) 的已提交历史，在 `RunTurn` 回调内 `DropLeading(n)` 后 `Append` 新消息，回调成功返回后从 `Manager.Get` 读取并逐条比对 ID、顺序、内容与总条数（覆盖 `n=0`、`1<n<len(base)`、`n=len(base)`）。必须走 `Manager.RunTurn` + `Get` 链路，不能只断言 `Turn.Session()`。
- AC-002 → 表驱动测试：`n=-1`、`n=len(base)+1`、以及"本 Turn 已 `Append` k 条后传 `n=len(base)+1 .. len(base)+k`"三种输入；断言 `core.KindOf(err) == invalid_argument`，并逐字段比较调用前后 `Turn.Session()` 与提交后 `Manager.Get` 的历史。
- AC-003 → 表驱动测试：构造 (a) 删除后残留孤儿 Tool Result、(b) 删除后前置 Tool Call 保留但其结果位于被删前缀、(c) 删除边界落在 assistant ToolCall 与其结果之间；断言返回 `invalid_pairing` 且错误文本/CallID 指向具体调用，调用前后历史与暂存逐条相等；再补一个"换用合法 `n` 后同一 Turn 仍可成功提交"的用例，证明拒绝未使 Turn 失效（若 Contract 采用其它语义，以 Contract 为准并在测试中固化）。
- AC-004 → 组合测试：同一 Turn 内 `DropLeading` + `SetSummary` + `Append` 成功后，一次读取同时断言三部分值；失败路径（非法配对边界 / 非法消息批次）断言历史、摘要、消息三者与调用前逐字段相等，不出现单项生效。
- AC-005 → 失败与取消测试：回调返回 error、回调内 `cancel()`、`ctx` 超时三种路径分别断言 `RunTurn` 的错误种类符合既有语义（`cancelled` / `deadline_exceeded` / 回调错误），且 `Manager.Get` 的历史 / 摘要 / Todo 与调用前相等；另加"回调返回后调用 `DropLeading` 返回 `invalid_argument` 且状态不变"用例。
- AC-006 → 并发测试：同一 Turn 内启动多个 goroutine 并发调用 `DropLeading` / `Append` / `SetSummary` / `Session()`（使用 `sync.WaitGroup` 与完成信号，不用 `sleep` 充当证据），断言无 panic、无死锁（超时仅作卡死保护）、提交后状态满足 Contract 的原子粒度；`go test -race -count=5 ./...` 全绿且无 race 报告。
- AC-007 → Cleaner 四方核对：逐项比对 `task.md` AC、APPROVED `contract.md`、`docs/design/runtime-architecture.md`（§8.1 / §11.1 / §12）与源码；用 `git ls-files session` 与 §12 登记行比对单归属；残留"Turn 只支持追加消息"等冲突陈述即判不满足。
- 通用检查 → 仓库根 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race -count=5 ./...`、`make test` 退出码 0；`GOWORK=off go -C .agent/engine test ./...` 仍通过；`go list -m all` 仍只有根 module（无新增第三方依赖）。
- 环境要求：Linux + Go 1.24.x（race detector 可用）+ GNU Make；不需要 MySQL / Redis / MQ / Docker；**全程本地测试，不调用真实付费 LLM API，不以网络依赖作为通过条件**（建议以 `GOPROXY=off` 执行以机械证明无网络与真实模型依赖）。

## Complexity

COMPLEX

原因：本任务表面是一个小方法，实际约束集中在事务与不变量上——前缀删除必须与既有 Turn 暂存区、`Append` 的全量校验、`commitTurn` 的全量替换提交语义正确组合，同时要保证"只删已提交历史、不碰本 Turn 新增消息"、不破坏 Tool Call / Tool Result 配对不变量、拒绝具有原子性、关闭后不可写、并发下不 reentrancy 死锁。这些语义会被 T07 直接消费，且"`n` 的计数基准与多次调用累积规则""拒绝粒度""Design 不变量如何登记"存在多个会产生不同行为的现实方案，须由 Analyst 分析、Owner 在 Contract 中确认。

## Analyst Questions

1. 提交链路核实：以 `RunTurn → commitTurn → staged() → publish` 的实际实现为准，确认"截断 `Turn.base` 即可持久化前缀删除"。若结论是不足，给出**最小必要变更范围**（例如需要 `Turn` 携带删除计数由 `commitTurn` 单独应用、或需要 `Manager` / `Store` 变更），交 Owner 批准后再实现；禁止只在内存工作视图生效的假实现。
2. `n` 的计数基准与多次调用语义：`n` 相对"进入 Turn 时的已提交历史"还是"当前剩余历史"？同一 Turn 内连续 `DropLeading(2)` 再 `DropLeading(3)` 应删除 2+3 还是 3 条？累计上限何时校验？
3. 合法域与错误映射：`0`、`len(base)`、负数、超过 `len(base)`（含跨越到本 Turn 暂存消息）分别如何表达；`n == len(base)` 是否需要附加"结果序列不得为空"这类额外约束；错误 `Op` 命名与文本约定。
4. 配对校验的落点：在 `DropLeading` 调用点立即校验"剩余历史 + 暂存消息"，还是只在提交时统一校验（`commitTurn` 的 P5）？若只在提交时校验，AC-003 的"调用点可观察的拒绝"无法满足，必须在 Contract 中明确取舍与理由。
5. 拒绝粒度：沿用 `Append` 的"单次调用原子拒绝、Turn 仍可继续"模式，还是改为整轮失败？是否需要在同一 Turn 内支持"先试删、失败后换合法参数再删"。
6. 与 `Append` / `SetSummary` 的顺序无关性：先 `Append` 后 `DropLeading` 与相反顺序是否必须等价（同一最终序列）；`Turn.Session()` 的 read-your-writes 视图在删除后应如何呈现。
7. 并发粒度：Turn 内多 goroutine 同时调用 `DropLeading` 与 `Append` 时，是否需要保证二者组合的额外不变量（例如删除与追加不得交错产生非法中间序列），还是仅保证每次调用原子、调用顺序不保证。
8. Design 更新范围：§8.1 Turn 写入能力描述如何改写；§11.1 是新增不变量条目还是扩展既有 INV-016；若新增源码 / 测试文件，§12 如何登记。

## Review Baseline

- Base commit：`dc734d20a732dcc7fc53dc2313c15dd003e919f9`（`merge: 合并 feat/runtime-observability-v1 到 develop`）。
- 任务分支：`feat/session-turn-drop-leading-v1`，已由 Owner 预先准备，当前 HEAD 与该分支一致且等于 Base commit；本地与 `origin/develop` 同 SHA（`dc734d2`），未领先、未落后。该分支目前**尚无远端对应分支**（`origin/feat/session-turn-drop-leading-v1` 不存在），首次 push 须由 Analyst 在完成本角色职责（Contract + Design + 各自 commit）时正常建立，TaskBuilder 不创建、不推送分支。已核实：该 remote 缺失会让 `.agent/bin/workflow-check gate coder-start .agent/tasks/session-turn-drop-leading-v1` 直接返回 `ERROR`（exit 2，"remote feature ... 不存在"）而非 `FAIL`，因此 Analyst 必须先形成 commit 并 push 当前 feature，再重跑 gate。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无暂存内容，无未跟踪文件。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/session-turn-drop-leading-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`session/turn.go` 与 `session/*_test.go` 的变更由 Coder 在 C1 引入；`docs/design/runtime-architecture.md` 由 Analyst 更新（Contract APPROVED 之后、Coder 实现之前）；`core/**`、`contextmgr/**` 在本任务中预期零变更，如需改动必须在 Contract 中列出并经 Owner 批准。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX）
