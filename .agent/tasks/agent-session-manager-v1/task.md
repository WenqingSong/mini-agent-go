# Task: 内存 Session Manager（agent-session-manager-v1）

## Goal

交付一个单进程内存 Session Manager，使调用方（后续 Agent Loop 装配层）可以按会话保存与读取消息历史、摘要和 Todo 状态，并保证：同一 Session 的 Turn 严格串行、不同 Session 的 Turn 真正并发、超时/取消与过期清理都不会破坏会话数据或产生数据竞争。交付后，会话级并发正确性由长期保留的 `-race` 测试证明，而不是靠约定。

## Scope

- Analyst 起草 `contract.md`（会话数据模型、Turn 提交语义、锁作用域、过期语义、错误语义），并请求 Owner Contract Decision（`APPROVED` 后方可进入实现）。
- Analyst 按 `Design Impact: UPDATE` 更新 `docs/design/runtime-architecture.md`：session 模块职责从「仅抽象」扩展为「含内存实现与 Session Manager」、Manager 的角色与边界、会话生命周期与过期不变量、并发/串行不变量、错误语义，以及 §12 文件所有权表。
- 实现内存 Session Manager（具体 API 形态、包布局与命名由 Contract 固化）：
  - 创建、获取、删除、过期清理会话；
  - 保存与读取消息历史、摘要、Todo 状态；
  - 同一会话的 Turn 串行执行（互斥且不丢更新）；
  - 不同会话的 Turn 并行执行，不使用跨会话全局锁；
  - 处理调用方超时/取消，以及在途 Turn 期间发生的会话状态变化（如过期、删除）；
  - 过期清理不产生数据竞争，不静默破坏在途 Turn 的数据。
- 长期保留的测试：并发（同会话串行、跨会话并发）、取消/超时、过期清理、非法写入拒绝，以及 `go test -race ./...` 全绿。
- 保持根模块 `go build ./...`、`go vet ./...`、`make test` 与 `.agent/engine` 独立 module 测试仍通过；不新增第三方依赖。

## Out of Scope

- 摘要生成算法、上下文压缩、token 估算与窗口截断（Context 任务）。
- Agent Loop 调度、工具执行、LLM 调用与 Provider 适配（并行 T01—T03）。
- DB / Redis 持久化、Migration、重启恢复与数据迁移；本任务只做进程内存实现。
- 跨进程 / 分布式会话锁与多副本一致性。
- 全局资源占用：`migration_version`、`error_code_domain` 等 Shared Resource 一律不预留、不新增（当前 `.agent/workflow.yaml` 的 `resources: {}`）。
- CLI / HTTP API、可观测性后端、性能基准与压测目标。
- 顺手修改 `core` 既有类型的既有语义（`Message` / `RunStatus` / `RunResult` / `ValidateToolPairing` 的行为不得改变）；若确需新增 `core.ErrorKind`，必须先写入 Contract 并由 Owner 批准。
- 修改 `.agent/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`AGENTS.md`。
- Deliverer 运行环境验收（本任务未声明 Milestone，见 Relevant Context 的 Assumption）。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`

本任务修改既有项目级长期事实：

- 模块职责：Design §3 目前声明 `session` 只定义「Session / Todo 数据类型与 `Store` 存取抽象」、明确「不实现任何存储」；本任务在该模块内落地内存实现与会话级调度，改变了该模块的长期边界陈述。
- 数据模型：若 Contract 确认「摘要」属于 Session 数据（`session.Session` 当前只有 `ID` / `Messages` / `Todos`），则修改已被 T00 APPROVED 的公共数据模型。
- 不变量与错误语义：新增「同一会话 Turn 串行 / 跨会话并发 / 过期与在途 Turn 的关系」这类跨模块不变量，并需要为「会话不存在 / 已过期 / Turn 被取消」确定错误表达（可能触及 `core.ErrorKind` 集合）。
- 文件所有权：§12 所有权表需随新增源文件更新。

因此 Design 更新与实现必须同批交付，并由 Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对。

## Acceptance Criteria

- [ ] AC-001：给定一个新建 Session，当依次写入多条消息（其中至少包含一条带 ToolCall 的 assistant 消息及其对应 tool 结果消息）、更新摘要并变更 Todo 状态后，再按会话 ID 读取，返回的消息条数、顺序、ID 与内容与写入一致，摘要与 Todo 状态为最后一次写入的值；读取不存在的会话返回明确错误，而不是返回空会话且无错误。
- [ ] AC-002：给定同一 Manager 中的两个 Session，当分别写入不同的消息、摘要与 Todo 后交叉读取，每个 Session 只看到自己写入的数据；删除或过期其中一个 Session 不会改变另一个 Session 的任何数据。
- [ ] AC-003：给定同一 Session 上并发发起多次写入（每次写入一组带唯一标识的连续消息，等价于一次 Turn 的完整消息序列），当全部调用返回后：每次写入的消息全部存在且各出现一次，同一次写入的消息在历史中连续排列、不与其它写入交错，历史总条数等于各次写入条数之和；任一次写入失败时其消息不产生部分落库。
- [ ] AC-004：给定两个不同 Session 上的 Turn（各自阻塞在对方已进入执行的同步点），当两者在同步点相遇时证明二者同时处于执行中；且一个 Session 持有执行权期间，另一个 Session 的写入/执行不被阻塞。判定使用显式同步原语（channel / WaitGroup / barrier），不以时间阈值或 sleep 作为证据。
- [ ] AC-005：给定一个正在执行 Turn 的 Session，当 TTL 到期或显式过期清理与该 Turn 并发发生时：清理与在途 Turn 的行为符合 Contract 规定的单一定义（在途 Turn 正常完成、或明确被取消），不出现部分写入；`go test -race -count=5 ./...` 无 data race 报告，且清理后该 Session 的后续访问行为与 Contract 一致。
- [ ] AC-006：给定同一 Session 上已有请求持有执行权，当排队等待的请求其 `context` 被取消或超时，该请求返回 `cancelled` / `deadline_exceeded` 语义的错误且不执行、不写入；给定执行中的 Turn 被取消，其返回结果标记为取消、已有历史不被回滚破坏，且执行权被释放——后续请求能正常获得执行权，不出现永久阻塞或锁泄漏。
- [ ] AC-007：给定 Session 中已有合法历史，当提交非法消息序列（例如 tool 消息的 Result 引用不存在的 Call、assistant 消息的 Call 缺少 Result）时，写入被拒绝并返回 `invalid_pairing` 语义错误，Session 历史逐条保持不变（拒绝是原子的，不产生半条写入）。
- [ ] AC-008：给定 `Design Impact: UPDATE`，当核对本任务 Task、APPROVED `contract.md`、`docs/design/runtime-architecture.md` 与实现时：session 模块职责（含内存实现与 Session Manager）、Manager 的公开 API 与生命周期、串行/并发与过期不变量、错误语义、§12 文件所有权表四者一致；Design 中不再残留「session 不实现任何存储」这类与实现冲突的陈述，新增源文件全部出现在所有权表且单归属。

## Relevant Context

- 已核实事实：根模块为 `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零第三方依赖），`Makefile` 的 `test` 目标是 `go test ./...`，`.cnb.yml` 的 `pr-check` 同为 `make test`；`go test -race` 未进入 CI，需在本地显式执行。
- 已核实事实：`session` 包目前只有 `session/session.go`，内容为 `TodoStatus`、`Todo{ID,Title,Status}`、`Session{ID,Messages,Todos}`、`Store{Load,Save}`，**没有任何实现与测试**；`Session` 无摘要字段。
- 已核实事实：`core` 已提供 `Message` / `ContentPart` / `ToolCall` / `ToolResult`、`ValidateMessages` / `ValidateToolPairing`（非法配对策 `invalid_pairing`）、`ExecutionRef{SessionID,RunID}`、`RunStatus`（1 非终态 + 4 终态）、`RunResult.Validate`、`RunPolicy`，以及 `*RunError` + `ErrorKind`（`invalid_argument` / `invalid_pairing` / `cancelled` / `deadline_exceeded` / `llm_call_limit` / `tool_call_limit` / `tool_failure` / `provider_failure` / `internal`）。**没有会话不存在 / 已过期这类错误种类。**
- 已核实事实：`docs/design/runtime-architecture.md` §3 声明 `session` 负责「Session / Todo 数据类型与 `Store` 存取抽象」，§8 声明「会话持久化是后续任务，本层只固定 `Store` 抽象……不实现任何存储」，§14 将「`session.Store` 的内存与持久化实现、Todo 生命周期」归属「Session 持久化任务」，并把 INV-005（会话归属）的运行期证据也归属该任务。
- 已核实事实：T00 的 APPROVED Contract 明确 Owner 决定「T00 **不**实现完整内存 Session Store」，T04 正是被归属的后续任务，因此不与既有决定冲突；但落地实现会改变 Design §3 / §8 / §12 的陈述，故按 `UPDATE` 处理。
- 已核实事实：`.agent/workflow.yaml` 为 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不预留任何全局资源（`state.resources.reservations` 为空）。若 Analyst 判定需要新增 kind，属 Scope / Owner 变更，不得自行新增。
- 已核实事实：Contract 边界相关的既有内容不会被本任务自动继承——T00 的 `contract.md` 只约束 T00，本任务需要独立 Contract。
- Assumption：「摘要」被理解为会话级的持久文本摘要（供后续上下文压缩使用），本任务只要求可保存、读取、隔离与过期，不要求实现摘要生成算法。
- Assumption：本任务不启用 Deliverer——Owner 未声明 Milestone，验收依赖 `go test -race` 与 Cleaner 独立复跑；若 Owner 要求纳入里程碑验收，需补充 `Milestone` 字段。
- Assumption：内存实现的「过期即数据不可再读」可接受，进程重启后数据丢失可接受（持久化属后续任务）。
- OPEN QUESTION（不阻塞，交 Analyst 提唯一推荐并由 Owner 在 Contract 中决定）：过期后会话数据是物理删除还是仅标记不可读——两种选择在「历史可恢复性」与「内存占用」上产生不同业务结果。
- OPEN QUESTION（不阻塞，交 Analyst 提唯一推荐并由 Owner 在 Contract 中决定）：是否允许为会话语义新增 `core.ErrorKind`（会修改 T00 已 APPROVED 的公共错误语义），还是复用现有 kind。
- OPEN QUESTION（不阻塞，交 Analyst 给出唯一推荐）：摘要的承载位置——扩展 `session.Session`、还是 Manager 侧独立状态、还是留给 `contextmgr` 压缩产物；该选择决定是否修改公共数据模型。

## Verification

- AC-001 → `session` 相关单元测试：写入 N 条消息 + 摘要 + Todo 后读取并逐字段断言（顺序、ID、内容、摘要、Todo 状态）；读取不存在 ID 的会话断言返回非 nil 错误且 Resolved 错误种类符合 Contract。环境：Linux + Go 1.24.x，无外部服务。
- AC-002 → 双会话交叉写入与读取测试：断言各自历史集合互不相交（逐条 ID 比对），删除/过期 A 后 B 的消息条数、摘要、Todo 与操作前逐字段相等。
- AC-003 → 并发写入测试：`sync.WaitGroup` 启动 M（≥8）个 goroutine，各自写入 K（≥3）条唯一消息；结束后断言总数 = M×K、无重复 ID、每组的消息在历史中连续（按组 ID 前缀扫描）。用 `-race` 运行；重复 `-count=5` 排除偶然通过。
- AC-004 → 交叉同步测试：Turn A 在写入第一条消息后等待「B 已进入执行」的 channel，Turn B 同理等待 A；若实现存在跨会话全局锁，两个 Turn 无法同时进入，测试将以超时/断言失败暴露（超时只作为卡死保护，不用作性能判定）。断言两个 Turn 都观察到对方已进入执行。
- AC-005 → 过期与在途 Turn 并发测试：在 Turn 执行中置位内部可控时钟或显式触发清理，重复运行 `-race -count=5`；断言行为与 Contract 定义一致（完成或取消，二选一）、无部分写入、清理后访问返回 Contract 规定的错误；`go test -race ./...` 全绿且无 race 报告。
- AC-006 → 取消/超时测试三组：(a) 等待执行权时 `ctx` 取消 → 返回 `cancelled`，被调用函数未执行、历史未变；(b) 等待时 `ctx` 超时 → `deadline_exceeded`；(c) 执行中取消 → 结果标记取消，随后新请求能在限定时间内获得执行权（用 channel 断言，不用长 sleep）。
- AC-007 → 非法写入表驱动测试：构造孤儿 Result、缺失 Result 两组非法序列，断言返回 `invalid_pairing` 与 `CallID` 定位，且写入前后历史长度与内容哈希一致。
- AC-008 → Cleaner 四方核对：逐项比对 `docs/design/runtime-architecture.md`（职责、API、不变量、错误语义、§12 所有权表）与 APPROVED Contract 及实际源码；用 `git ls-files` 与实际文件比对所有权表单归属；残留冲突陈述即判不满足。
- 通用检查 → 仓库根 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race -count=5 ./...`、`make test` 退出码 0；`GOWORK=off go -C .agent/engine test ./...` 仍通过（Engine 独立 module 不受影响）；`go list -m all` 仍只有根 module。
- 环境要求：Linux + Go 1.24.x（race detector 可用）+ GNU Make；不需要 MySQL / Redis / MQ / Docker / 网络。

## Complexity

COMPLEX

原因：本任务的核心不是写 CRUD，而是并发与生命周期语义——会话级锁作用域（如何在不引入全局锁的前提下串行化同一会话）、Turn/Run 提交语义、过期清理与在途 Turn 的交互、取消/超时的锁释放、摘要与消息历史的写入原子性，以及错误语义如何表达「会话不存在 / 已过期 / 已取消」。这些选择会被后续 Agent Loop 直接消费，且不同方案会产生不同的数据丢失风险与可观测行为，必须由 Analyst 分析并由 Owner 在 Contract 中确认。

## Analyst Questions

1. 会话数据模型与「摘要」承载：摘要扩展 `session.Session`（修改 T00 已 APPROVED 的公共数据模型）还是 Manager 侧独立状态？读取返回的是值拷贝还是共享引用（影响 AC-002 隔离与外部改写风险）？
2. Turn / Run 提交语义：Manager 对外暴露什么形态——回调式（在会话锁内执行调用方函数）、还是提交 `core.ExecutionRef` + 消息批次 + 状态？串行粒度是「整轮 Turn」还是「单次追加」？如何与 T01 Agent Loop 装配而不强迫 Loop 反向依赖会话内部结构？
3. 锁作用域与公平性：per-session 锁采用 `map[id]*sync.Mutex`、keyed lock 还是会话 actor 单 goroutine；是否需要 FIFO 公平、是否允许同会话并发读；如何用测试机械证明「不存在跨会话全局锁」（AC-004 的判定方式）。
4. 过期语义：TTL 来源与默认值、清理触发方式（惰性访问 vs 后台 goroutine）、是否物理删除数据、清理 goroutine 的生命周期与 `Close` 语义、过期与在途 Turn 发生时的确定行为（阻塞过期 / 取消在途 / 允许完成但之后不可读）。
5. 错误语义：会话不存在、已过期、正在过期、Turn 被取消分别如何表达？复用现有 `core.ErrorKind`（如 `invalid_argument` / `internal`）还是新增会话相关 kind（会修改公共错误语义，需 Owner 批准）；是否需要 `CallID`/`SessionID` 定位。
6. 内存实现与 `session.Store` 的关系：Manager 是否实现 `Store`（`Load` / `Save`）、`Load` 是否返回深拷贝、并发安全由 Store 还是 Manager 负责、是否保留「Store 只做存取、Manager 做调度」的分层。
7. 写入校验边界：追加消息时是否调用 `core.ValidateMessages`（每次全量校验为 O(n)），还是仅在 Turn 结束/读取出时校验；非法序列的原子拒绝如何界定（AC-007 的通过标准）。
8. 超时归属：Turn 超时由调用方 `context` 决定还是 Manager 策略决定（`ExecutionRef`/`context` 只承担取消与超时，不承载业务身份）；超时后已写入的消息保留还是回滚（现有 `RunStatus` 语义为不回滚）。

## Review Baseline

- Base commit：`6902d2445665ec356a2db22b33b7749d39d8018e`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）`）。
- 任务分支：`feat/agent-session-manager-v1`，已由 Owner 预先准备；当前 HEAD 与该分支一致，`origin/feat/agent-session-manager-v1` 与 `origin/develop` 均为同一 SHA（6902d24），本地未领先、未落后。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无暂存内容。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/agent-session-manager-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`session/**` 新实现由 Coder 在 C1 引入；`docs/design/runtime-architecture.md` 由 Analyst 更新；若需要改动 `core/**`，必须在 Contract 中明确列出并说明不改变既有语义。发现他人遗留 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX）
