# Technical Contract

## Decision Status

APPROVED

## Problem

`session.Turn` 目前只能在一次 `RunTurn` 内追加消息（`Append`）、写摘要（`SetSummary`）与 Todo，**没有** 删除「已提交历史前缀」的能力。因此 T07 落地上下文压缩时只能走「先删一批并提交、再追加一批并提交」的两步路径；中途失败会留下「历史已删、摘要未更新」这类调用方无法修复的中间态。

本任务交付公共方法 `(*session.Turn).DropLeading(n int) error`，使「删除已提交历史前缀 + 更新摘要 + 追加本 Turn 新消息」在同一次 `RunTurn` 内整体提交或整体丢弃，`Manager.Get` 读到的历史为最终一致结果。

边界：只做**前缀**删除；不改 `contextmgr`（`Request` / `Result` / `DroppedMessages` / 压缩算法）、不改 `core` 既有校验语义、不改 `Manager` / `Store` 接口、不接线 Agent Loop、不引入存储或新依赖。

## Verified Current Behavior

VERIFIED：

1. **提交链路足以持久化前缀删除（核心结论）**：`Manager.RunTurn` 取得执行权后 `turn := newTurn(*e.snapshot.Load())`（`session/manager.go:204`），回调返回即 `turn.close()`（`session/manager.go:208`）；提交时 `commitTurn` 用 `t.staged()` 取整份内容（`session/manager.go:401`）→ `core.ValidateMessages`（`session/manager.go:404`）→ `m.publish(...)` 整体替换 `entry.snapshot`（`session/manager.go:419`，`snapshot.Store` 在 `session/manager.go:333`）。提交是**全量替换**，不是增量合并。因此只要 `DropLeading` 在 Turn 内改变 `Turn.base`，前缀删除必然进入已提交快照；**不需要** `Manager` / `Store` 新方法，也不需要新增 Turn 字段或提交指令。
2. `Turn.base` 是进入 Turn 时已提交历史的**私有深拷贝**（`session/turn.go:48`）；Turn 内只有两处读取它，且拼装规则相同（`base + messages`）——`Session()`（`session/turn.go:59-61`）与 `staged()`（`session/turn.go:191-193`）。因此改 `base` 一份即可同时改变读视图与提交内容，不存在"两套视图"。
3. 当前 `session` 包**没有**任何前缀删除能力（`DropLeading` / `dropped` 在 `session/**` 无匹配）；`contextmgr.Result.DroppedMessages` 的语义是"被压缩覆盖的消息条数，恰为输入 `Messages` 的前 `DroppedMessages` 条"（`contextmgr/contextmgr.go:35-38`），未被 Turn 消费。
4. **既有拒绝模式**：`Append` 在持 `t.mu` 内对「base + 暂存 + 新批次」跑 `core.ValidateMessages`，失败原样返回错误、暂存区不变、Turn 不关闭、仍可提交其它合法写入（`session/turn.go:76-99`、`session/turn_test.go:140-165`）；Turn 关闭后写方法统一返回 `turnClosedError`（`invalid_argument`，`session/errors.go:96-99`），且 `Append` **先判关闭再判空批次**（`session/turn.go:81-86`）。
5. 配对违规固定返回 `KindInvalidPairing`，带消息序号与 `CallID`（`core/pairing.go:26-91`）；`ValidateMessages` = 逐条 `Message.Validate` + `ValidateToolPairing`（`core/pairing.go:9-16`）。`ErrorKind` 共 9 种（`core/error.go:15-34`），`invalid_argument` / `invalid_pairing` 足以表达本任务全部拒绝，无需新增 Kind。
6. `core.ValidateMessages` 对空切片返回 nil（无空序列约束），因此"清空全部已提交历史"本身是合法序列。注意形态差异：`Manager.Create` 的历史为 nil，而 Turn 提交空历史时 `Session()` / `staged()` 用 `make(..., 0, 0)` 得到长度 0 的**非 nil** 切片（`session/turn.go:59-61`、`:191-195`）；本 Contract 不对 nil / 空切片形态作约束，测试按长度与逐条内容比较。
7. **并发**：`Turn.mu` 保护每次调用（`session/turn.go:35`、`:56-57`）；Turn 内不存在对 `Manager` 执行权的二次加锁路径（执行权只在 `RunTurn` 入口取得一次，`session/manager.go:197-201`），同粒度加锁不会引入重入死锁。`RunTurn` 保证同会话串行、跨会话并发。
8. **基线**：HEAD `5e965e0`（TaskBuilder 任务定义提交），在 `origin/develop` `dc734d2` 之上 1 个提交；`origin/feat/session-turn-drop-leading-v1` **尚不存在**（`git ls-remote --heads origin` 核对），与 `task.md` Review Baseline 一致。该 remote 缺失会让 `gate coder-start` 返回 ERROR（exit 2），故 HANDOFF 前必须由 Analyst 推送当前 feature。

INFERENCE：

- T07 的主路径是"一次 Turn 调一次 `DropLeading(DroppedMessages)`"（对齐 §9 的压缩配方），多次调用是少数路径；因此多次调用语义必须在本 Contract 内定死，不能留给 T07 决定。

UNKNOWN：

- 无阻塞性未知项。T07 的具体调用形态尚未实现（`origin/feat/agent-runtime-loop-v1` 未合并），但不影响本方法的语义选择。

## Recommendation

RECOMMENDATION：**只改 `session/turn.go`，用"删除当前剩余已提交历史的前 `n` 条"表达 `DropLeading`，并在调用点用 `core.ValidateMessages` 对「剩余已提交历史 + 本 Turn 暂存消息」做一次校验，全部通过才截断 `t.base`。**

语义（已按 Owner 决定的 D1–D3 固化，见 Selected Design 与 Owner Decision Record）：

- **计数基准**：`n` 是"本次调用新删除的条数"，作用对象是**当前剩余**的已提交历史（`t.base`）。同一 Turn 内累计删除量单调不减，每次要求 `0 <= n <= len(t.base)`。等价说法：连续 `DropLeading(2)` 再 `DropLeading(3)` 共删 5 条（2+3），而不是"删到第 3 条"。
- **合法域**：`n = 0` 为空操作（但仍先判 Turn 已关闭）；`n = len(base)` 合法，清空已提交历史（首次调用时即 AC-001 的 `n=len(base)` 场景）；负数、`n > len(base)` 一律 `invalid_argument`。因为 `base` 只含进入 Turn 时的已提交历史，`n` 在结构上不可能删到本 Turn 新增消息。
- **校验落点**：在 `DropLeading` 内（持 `t.mu`）对 `t.base[n:] + t.messages` 调 `core.ValidateMessages`；失败原样返回 `core` 错误（`invalid_pairing` + `CallID`），不截断 `base`。这样 AC-003 的拒绝在**调用点**可观察，且 commitTurn 的 P5 不会因本方法失败。
- **拒绝粒度**：沿用 `Append` 的"单次调用原子拒绝、Turn 不关闭、可换合法参数继续"模式。
- **组合语义**：`DropLeading` 只作用于 `base`，`Append` 只作用于 `messages`，最终序列恒为 `base[n:] + messages`；`Append` 的全量校验自动基于截断后的 `base`。

关键取舍：`n` 取"相对当前剩余的前缀条数"，而不是"相对进入 Turn 时快照的绝对位置"。绝对位置方案在 `DropLeading(0)` 时会**复活已删除的前缀**（非单调），必须额外拒绝 `n < 已删量` 才安全，规则更复杂且更容易被误用；前缀条数方案与 `contextmgr.DroppedMessages` 的口径一致，天然单调。

被否定的备选（仅记录，不建议）：把配对校验只留到 `commitTurn` 的 P5。这样 `DropLeading` 会接受一个"当时非法"的删除，失败被推迟成整轮失败，直接违反 AC-003「调用点可观察的拒绝」，并让 T07 无法在同一 Turn 内纠正参数。

## Selected Design

Owner 已批准 D1–D3，实现按以下设计执行：

- 计数基准（D1）：`n` 是"本次调用新删除的条数"，作用于**当前剩余**的已提交历史（`t.base`）；累计删除量单调不减，每次 `0 <= n <= len(t.base)`；连续 `DropLeading(2)` + `DropLeading(3)` 共删 5 条；不允许通过后续调用恢复已删除消息。
- 合法域（D1 / D2）：`n=0` 为空操作（仍先判 Turn 已关闭）；`n=len(t.base)` 合法，包含最终历史为空；`n<0` 或 `n>len(t.base)` 返回 `invalid_argument`；不引入"至少保留一条消息"的约束。
- 校验落点：在 `DropLeading` 内（持 `t.mu`）对 `t.base[n:] + t.messages` 调 `core.ValidateMessages`；失败原样返回 `core` 错误（`invalid_pairing` + `CallID`），不截断 `base`；因此每次成功调用后暂存序列必合法。
- 原子性：单次调用原子；失败不改变 `base` / `messages` / `summary` / `todos`，也不关闭 Turn；可换合法参数在同一 Turn 内继续。
- 并发（D3）：`Turn.mu` 串行化每次调用，不规定并发操作的业务先后顺序；只保证无 data race、无死锁、无部分修改，且每次成功后序列合法。
- 实现落点：仅 `session/turn.go`；不新增文件，不改 `Manager` / `Store` / `core` / `contextmgr`。若既有 Turn 提交机制无法满足本 Contract，Coder 必须停止并报告，不得扩大 Scope。

## Interfaces and Data

公共接口（签名固定，不得改名、改形、增加参数）：

```go
func (t *Turn) DropLeading(n int) error
```

- 不新增公共类型、公共常量、`Turn` 公共字段；不新增 `core.ErrorKind`；不改 `Store` / `Manager` 方法集。
- 私有实现允许的形态：直接截断 `t.base`（推荐，无新增状态）；若引入私有计数状态，必须与"截断 `base`"语义等价，且不改变上述可观察行为。
- 错误契约：

| 情形 | 返回 | 可观察字段 |
| --- | --- | --- |
| Turn 已关闭（回调已返回） | `invalid_argument`，复用 `turnClosedError` | `Op = session.Turn.DropLeading`，文本 `Turn 已结束，不再接受写入` |
| `n < 0` 或 `n > len(t.base)` | `invalid_argument` | `Op = session.Turn.DropLeading`，文本含请求的 `n` 与本次可删上限 `[0, len(base)]` |
| 删除后配对/结构非法 | 原样返回 `core.ValidateMessages` 的错误 | `Kind = invalid_pairing`、`Op = core.ValidateToolPairing`、`CallID` 指向具体调用 |

- 错误 `Op` 不重写为 `session.Turn.DropLeading` 的两种情形必须保留 `CallID`（不得包装成丢失 `CallID` 的新错误）；这与 `Append` 原样返回 `core` 错误一致。
- 数据形态不变：`Session.Messages` 仍是历史的唯一事实源；`Session()` 视图与提交视图均为 `base[n:] + messages`，`Summary` / `Todos` 不受影响。
- 组合顺序：先 `Append` 后 `DropLeading` 与相反顺序得到同一**最终序列**（两者都基于当前状态校验，同一拼接序列的合法性判定与顺序无关）；但"哪一次调用被拒"随顺序变化，调用方不得假设某顺序必然成功。
- Coder 必须同步更新 `Turn` 的文档注释与包级写入能力描述（当前 `session/turn.go:24-29` 只提"暂存写入"），并在方法注释中固化上述计数基准与错误语义。

## Business Invariants

本任务局部编号（Design 落地后在 §11.1 合并为一条全局不变量 INV-017）：

- INV-001（范围隔离）：`DropLeading` 只改变"进入本 Turn 时已提交历史"的前缀；本 Turn 经 `Append` 新增的消息永不被删除、移动或重排；同一 Turn 内累计删除量单调不减且不超过进入时的已提交历史长度。
- INV-002（视图一致）：`Turn.Session()` 与提交内容使用同一拼装规则（剩余已提交历史 + 暂存消息），不存在"读视图已删、提交内容未删"或反向的分叉。
- INV-003（调用点配对安全）：任何一次 `DropLeading` 成功返回后，「剩余已提交历史 + 暂存消息」必满足 `core.ValidateMessages`；因此本方法不会让 Turn 进入只能靠 P5 拒绝的状态。
- INV-004（拒绝原子性）：被拒绝的调用不改变 `base` / `messages` / `summary` / `todos` 任何一项，也不关闭 Turn；同一 Turn 内换合法参数可继续操作并成功提交。
- INV-005（整轮原子提交）：历史、摘要、Todo 仍由 `RunTurn` 整轮提交；回调返回错误、`ctx` 取消/超时、panic 时三者逐字段保持提交前状态，不产生部分删除。
- INV-006（关闭后不可写）：回调返回后再调 `DropLeading` 返回 `invalid_argument` 且不改变任何状态。

## Failure and Consistency Semantics

- 事实源：单一进程内 `Manager` 内存快照，无多存储、无异步复制、无 MQ，因此不存在跨系统最终一致性问题。
- `DropLeading` 返回 `nil` 只表示"本次前缀删除已进入本 Turn 的暂存区"，**不表示已提交**；是否落库由 `RunTurn` 的提交条件决定。
- 失败路径：
  - 非法 `n` / Turn 已关闭 → 当次调用失败，暂存与已提交状态均不变，Turn 可继续使用（关闭情形除外）。
  - 配对非法 → 当次调用原子拒绝，返回带 `CallID` 的 `invalid_pairing`，Turn 不关闭。
  - 回调返回错误 / `ctx` 取消 / 超时 / panic → 整轮丢弃，历史、摘要、Todo 逐字段不变（既有 P1/P2/P5 语义，`session/manager.go:212-220`、`:400-428`）。
  - 提交前 P5 失败 → 整轮不提交。`DropLeading` 自身不会触发 P5（INV-003），P5 失败只可能来自其它写入（如 `SetTodos` 非法 Todo）。
- 重复与顺序：不存在"去重"需求——每次调用都是独立的前缀删除请求；顺序只影响"哪一次被拒"，不影响同一最终序列的合法性判定。
- 并发：每次调用在 `Turn.mu` 内原子完成；不保证多 goroutine 的调用顺序，也不保证"删除与追加"的组合形成一个业务上更优的序列；只保证任何一次成功调用结束时暂存序列合法、且不产生 data race / 重入死锁。

## Allowed / Forbidden Changes

- 允许（Coder）：`session/turn.go`（新增 `DropLeading` + 相关文档注释）、`session/turn_test.go`（语义与失败测试）、`session/concurrency_test.go`（Turn 内并发测试）。
- 允许（Analyst）：`.agent/tasks/session-turn-drop-leading-v1/contract.md`、`state.yaml`（仅按两段式提交写 `contract.status` / `contract.target`）、`docs/design/runtime-architecture.md`（§8.1 / §11.1 / §12 描述）。
- 禁止：新增生产或测试**文件**（保持 §12 所有权表行不变；新增文件须先走 Contract Revision）；修改 `core/**`、`contextmgr/**`、`session/manager.go`、`session/session.go`；新增 `core.ErrorKind`；修改 `Store` / `Manager` 方法集或 `Session` 字段；改动 `go.mod` / `Makefile` / `.cnb.yml` / `AGENTS.md` / `.agent/**`（Analyst 自身 artifacts 除外）；把上下文压缩接线进 Turn；`git add -A` / `git add .`。
- 测试辅助函数必须复用既有 `session` 测试 helper（`mustCreate` / `mustGet` / `userMessage` / `assistantMessage` / `toolMessage` / `newCall` / `fingerprint` / `expectKind` / `runTurnWithin`，见 `session/manager_test.go:48-113`、`session/concurrency_test.go:21-50`），不复制一份等价实现。

## Verification Requirements

- INV-001 / AC-001 → `RunTurn` + `Manager.Get` 表驱动：base 含 system / user / assistant(ToolCall) / tool，覆盖 `n=0`、`1<n<len(base)`、`n=len(base)`；删除后 `Append` 若干条并成功提交；逐条比对 ID、顺序、内容与总条数 `m-n+k`；`n=0` 历史不变；`n=len(base)` 时提交结果仅含本次新增消息（无新增时长度为 0）。禁止只断言 `Turn.Session()`。
- INV-004 / AC-002 → 表驱动：`n=-1`、`n=len(base)+1`、以及"已 `Append k` 条后传 `n=len(base)+1 .. len(base)+k`"；断言 `core.KindOf(err)==invalid_argument`，并逐字段比较调用前后 `Turn.Session()` 与提交后 `Manager.Get`；再补"同一 Turn 换合法 `n` 后仍成功提交"。
- INV-003 / AC-003 → 表驱动：(a) 删除后残留孤儿 Tool Result；(b) 保留 Tool Call 但其结果位于被删前缀；(c) 边界落在 assistant ToolCall 与其结果之间；断言 `invalid_pairing` 且 `CallID` 指向具体调用，调用前后历史与暂存逐条相等；再用合法 `n` 证明 Turn 未失效。
- INV-005 / AC-004 → 组合测试：同一 Turn 内 `DropLeading` + `SetSummary` + `Append` 成功后一次读取同时断言历史 / 摘要 / 新增消息；失败路径（非法配对边界、非法 Todo 导致 P5 失败）断言三者与提交前逐字段相等。
- INV-005 / INV-006 / AC-005 → 回调返回 error、回调内 `cancel()`、`ctx` 超时三条路径断言 `RunTurn` 错误种类符合既有语义（回调错误 → `internal` 或原 `*RunError`；取消 → `cancelled`；超时 → `deadline_exceeded`），且 `Get` 的历史 / 摘要 / Todo 与调用前相等；另加"回调返回后 `DropLeading` 返回 `invalid_argument` 且状态不变"。
- 并发 / AC-006 → 同一 Turn 内多个 goroutine 并发 `DropLeading(1)` / `Append` / `SetSummary` / `Session()`（`sync.WaitGroup` + 显式完成信号，超时只作卡死保护）；断言无 panic、无死锁、无 race，提交后条数 = 进入时历史长度 − 成功删除数 + 成功追加数，且逐条可对账。期望值按**成功次数**计算，不能按请求次数（并发下剩余不足的删除会被合法拒绝）。
- 通用 → 仓库根 `go build ./...`、`go vet ./...`、`go test ./...`、`make test`、`go test -race -count=5 ./...` 全绿且无 race 报告；`GOWORK=off go -C .agent/engine test ./...` 仍通过；`go list -m all` 无新增依赖；既有 `session` 测试不回归。环境：Linux + Go 1.24.x，全程本地，不调用真实付费 LLM、不以网络为通过条件（建议 `GOPROXY=off`）。

## Open Risks

- 并发用例的期望值必须按"成功删除数"结算：若按请求数断言，剩余不足时的合法拒绝会被误判为实现缺陷。
- 顺序等价性只在"同一最终序列的合法性判定"层面成立；失败发生在哪一次调用随顺序变化，T07 不得依赖特定顺序必成。
- `n = len(base)` 且本 Turn 未追加消息时，提交后的 `Session.Messages` 为长度 0 的非 nil 切片；长度 0 是合法终态，调用方（T07）必须自行处理空历史视图，不能假设至少一条消息。
- 本方法不返回"实际删除条数"；若 T07 后续需要该信息，须另起 Contract Revision（当前不扩展返回签名）。
- `-race` 未进入 CI（`.cnb.yml` 只执行 `make test`），并发证据仅来自本地显式执行，Cleaner / Deliverer 需自行复跑。

## Owner Decision Record

2026-10-20，Owner 明确批准 D1–D3。Analyst 只机械持久化以下选择，未替 Owner 决策：

- **D1 → 批准增量删除语义**：每次 `DropLeading(n)` 删除当前剩余已提交历史的前 `n` 条；连续调用累计（初始 10 条，先删 2 再删 3 → 累计 5 条、剩余 5 条）；`n=0` 合法空操作；`n<0` 或 `n>len(base)` 返回 `invalid_argument`；每次调用独立、原子，失败不得改变 Turn 状态；不允许通过后续调用恢复已删除消息。
- **D2 → 批准删除全部剩余历史**：允许 `n=len(base)`，包括最终历史为空；不额外引入"至少保留一条消息"的限制；调用成功仍必须满足完整消息序列结构与 Tool Call / Tool Result 配对约束。
- **D3 → 批准最小并发保证**：`DropLeading` / `Append` / `SetSummary` 每次调用原子；每次成功后「`base` + 本 Turn 暂存消息」满足 `core.ValidateMessages`；并发不得产生 data race、死锁或部分修改；不规定并发操作的业务先后顺序（同一 Turn 的并发操作由互斥锁串行化，顺序取决于实际抢锁先后）；任一失败不得部分删除消息、改变摘要或影响 Todo。
- **范围确认**：实现保持最小范围——不修改 `Manager` / `Store`、不新增 `core.ErrorKind`、不修改 T07 生产代码、不调用真实付费 LLM API；若实现发现既有 `Turn` 提交机制无法满足本 Contract，必须停止并报告，不得擅自扩大 Scope。
- **适用版本**：本 Decision Record 绑定 A1（`state.contract.target`）中的 `contract.md`；A1 之后 `contract.md` 内容不得再变，否则审批失效。
