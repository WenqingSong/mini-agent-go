# Technical Contract

## Decision Status

APPROVED（Owner Contract Decision 2026-10-20，决定原文见 `Owner Decision Record`；Evidence Commit A1 由 `state.contract.target` 引用）。

任务：`agent-session-manager-v1`（Design Impact `UPDATE` → `docs/design/runtime-architecture.md`）。

## Problem

在 `session` 包内交付**单进程内存 Session Manager**，使后续 Agent Loop（T01）装配层可以按会话保存/读取消息历史、摘要与 Todo，并同时固定四类此前未定义的语义：

1. **执行权**：同一会话的 Turn 互斥且不丢更新，不同会话互不阻塞（AC-003 / AC-004）；
2. **提交原子性**：一次 Turn 的写入要么整体生效、要么整体不生效，非法消息序列被原子拒绝（AC-003 / AC-007）；
3. **取消/超时**：排队等待执行权与执行中两条路径的取消语义、执行权释放（AC-006）；
4. **生命周期**：TTL 过期清理与在途 Turn 并发时的确定行为（AC-005）。

难点不在 CRUD，而在这四者互相耦合：过期语义决定执行权形态，执行权形态决定取消语义能否实现（`sync.Mutex` 不可被 `context` 取消）。同时本任务必须与三个相邻契约保持一致：T00 冻结的公共模型、T03 受限 Todo 能力、T05 摘要生成。

原子性范围：**只覆盖 Session Manager 管理的内存消息、摘要与 Todo 状态**，不承诺撤销任何外部工具副作用（工具已发生的副作用由工具与 Loop 负责）。

## Verified Current Behavior

- VERIFIED：根模块 `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，`go.mod` 无 `require`（零第三方依赖）；`Makefile` 的 `test` 目标是 `go test ./...`；`.cnb.yml` 的 `pr-check` 同为 `make test`，`-race` 未进入 CI。
- VERIFIED：`session/session.go` 只有 `TodoStatus`、`Todo{ID,Title,Status}`、`Session{ID,Messages,Todos}`、`Store{Load,Save}`；无摘要字段、无实现、无测试；包注释明确「不提供任何持久化或内存实现」。
- VERIFIED：`core.ValidateMessages` / `core.ValidateToolPairing` 已能拒绝孤儿 Result、缺失 Result、重复/非法 Call 标识，错误为 `*core.RunError{Kind: invalid_pairing}` 且带 `CallID`；`Message.Validate` 对角色/字段组合错误返回 `invalid_argument`（`core/pairing.go`、`core/message.go`）。
- VERIFIED：`core.ErrorKind` 只有 9 个取值，没有「会话不存在 / 已过期 / 重复创建」；`core.KindOf` 对非 `*RunError` 返回 `internal`；`RunError.Unwrap` 返回 `Cause`，因此 `errors.Is` 可用（`core/error.go`）。
- VERIFIED：`core.RunResult.Validate` 的 `FAILED` 仅接受 `provider_failure`/`tool_failure`/`internal`/`invalid_argument`/`invalid_pairing`（`isFailureKind`）；新增 `ErrorKind` 会连带影响 `StatusOf` 与 `RunResult.Validate`。
- VERIFIED：`docs/design/runtime-architecture.md` §2 / §3 / §8 / §11 / §12 / §14 当前都按「`session` 只有类型与 `Store` 抽象、不实现任何存储」表述，且把「`session.Store` 的内存与持久化实现、Todo 生命周期」整体归给后续任务。
- VERIFIED：`docs/design/runtime-architecture.md` §8 允许「Tool 若需要会话状态，必须由装配层显式注入能力或传入值」，因此「注入受限能力接口」不违反 T00 已批准决定 INV-005，无需修改 T00 决定本身。
- VERIFIED：T00 `contract.md` 规定的公共契约变更流程为「走 Contract Revision 并由 Owner 重新 `APPROVED`，随后同步 Design」（Design §15）；本次 `Session.Summary`、`TodoStatus` 导出常量、`Todo.Validate` 均按此流程由 Owner 在本 Contract 中授权。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`，无已声明 Shared Resource Kind；本任务无全局资源（`state.resources.reservations` 为空）。
- VERIFIED：工作区 clean，分支 `feat/agent-session-manager-v1`，HEAD `c2e88a8`（父提交 `6902d24` 为 Task 声明的 Base）；本机 `go1.24.1 linux/amd64`（race detector 可用）。
- VERIFIED：**T03 的 `TodoOperations` 接口在本仓库任何分支都不存在**（对全部 `refs/heads` + `refs/remotes` 执行 `git grep -i "todooperations"` 无结果；`origin/feat/builtin-agent-tools-v1`、`origin/tool-registry-executor-v1` 均只有 `runtime-core-contracts-v1` 任务目录）。因此**无法做符号级兼容性核对**，只能由本 Contract 定义 T04 的权威接口并要求 T03 采用（见 `Session/Todo 协作机制`）。
- UNKNOWN：T03 受限 Todo 能力的具体签名与并发模型（是否并行执行同一 step 的多个 Tool）。未知项不改变本方案：T04 的暂存区内部同步，使并发调用安全，T03 采用 `session.TodoOperations` 即可，无需反向依赖。
- UNKNOWN：T05 Context Manager 的接口形态（`contextmgr.Manager` 为既有抽象）。本 Contract 只固定「摘要由 Session 持有、由 Context Manager 通过 Turn 能力更新」这一条边界。
- UNKNOWN：会话历史规模上界与性能目标（Task 明确不做基准）。「每批全量校验」的 O(n) 成本按当前阶段可接受处理。

## Recommendation

Owner 已确认 D1 / D2 / D4 / D5（见 `Owner Decision Record`），Analyst 推荐的 D3 / D6 与 Todo 协作机制随同一批准生效。最终选择见 `Selected Design`。

## Selected Design

### D1 摘要承载（Owner 决定）

`session.Session` 新增 `Summary string`。摘要属于会话的长期状态，由 Session Manager 存储，由后续 Context Manager（T05）生成/更新；本任务不实现摘要算法。

- 单一事实源：Session 同时持有 `Messages`、`Summary`、`Todos`，`Store.Load`/`Save` 可完整往返。
- 写入路径唯一：只能在 Turn 内经 `Turn.SetSummary` 修改（随 Turn 原子提交）；无 Manager 级直接写摘要的公开方法。
- 读取：`Get` / `Load` / `Turn.Session()` 返回深拷贝中的值；`""` 表示无摘要。

### D2 Turn 提交语义（Owner 决定）

`RunTurn(ctx, sessionID, fn)` 在会话执行权内执行 `fn`；`fn` 通过 `*Turn` 写入进入**隔离暂存区**；满足全部提交条件才整体提交，否则整轮丢弃。

- 同一 Session 串行、不同 Session 并行；等待执行权支持 `context` 取消/超时。
- 失败、取消、超时**不得部分提交**。
- 提交条件不是「`fn` 返回 `nil`」这一条，而是下面的完整条件集。
- Turn 内连续 Todo 操作必须读到自己尚未提交的修改（read-your-writes），由「基线快照 + 暂存」视图保证。
- 读取一律返回安全快照（深拷贝），调用方修改返回值不影响会话。
- 执行权在正常、失败、取消、`panic` 路径下均由 `defer` 释放，不泄漏。

**提交条件（全部成立才提交）**：

| # | 条件 | 不成立时 |
| --- | --- | --- |
| P1 | `ctx.Err() == nil`（Turn 结束时复核，不只看 `fn` 返回值） | `KindCancelled` / `KindDeadlineExceeded`，不提交 |
| P2 | `fn` 返回 `nil` | 返回 `fn` 的错误（非 `*RunError` 时包 `KindInternal` + `Cause`），不提交 |
| P3 | Turn 仍处于打开状态（未被并发关闭；`fn` 返回后到达的写入被拒绝，不参与提交） | 该写入返回 `invalid_argument`，不回滚已接受的暂存 |
| P4 | 提交前复核 entry 仍是 map 中的当前 entry（未被 `Delete`/`PurgeExpired` 移除） | `ErrSessionNotFound`，不提交 |
| P5 | 提交前对「已提交历史 + 暂存消息」再跑 `core.ValidateMessages`，且暂存 Todo 全部通过 `Todo.Validate` | 返回该校验错误，不提交 |

`ctx` 取消/超时优先于 `fn` 的返回值：即使 `fn` 返回 `nil`，只要 P1 不成立就不提交——避免「已取消的 Turn 借提交篡改会话状态」。

`Append` / `PutTodo` / `SetTodos` 的校验失败是**该批次原子拒绝**，不 poison 整个 Turn：`fn` 若忽略该错误并返回 `nil`，此前已被接受的暂存写入照常提交，被拒批次不落库。

### D3 执行权与并发（推荐，随批准生效）

- 每会话执行权 = **容量 1 的 channel 信号量**（`select` 同时等待获取与 `ctx.Done()`），因此排队获取可被取消/超时。
- `map` 互斥锁只用于查找/插入/删除 entry，**绝不跨 `fn` 持有**（AC-004 的机械核对点）。
- 会话数据以不可变提交快照发布（`atomic.Pointer`），写入仅在执行权内 copy-on-commit；读取不加锁、不阻塞、不返回未提交状态。
- 获取执行权后必须复核 entry 仍是当前 entry；等待期间被 `Delete`/`PurgeExpired` 移除则释放并返回 `ErrSessionNotFound`。
- 不承诺严格 FIFO 公平；只保证互斥、不泄漏、不饥饿（Go channel 语义）。
- 被排除的方案：`map[id]*sync.Mutex`（不可取消，AC-006 不可实现）；会话 actor 单 goroutine（需 `Close` 与生命周期管理，收益仅公平性）。

### D4 过期语义（Owner 决定）

- TTL 由 `ManagerConfig.TTL` 提供（`> 0` 启用，`<= 0` 关闭，零值即关闭）；时钟由 `ManagerConfig.Now` 注入（`nil` → `time.Now`），支持确定性过期测试，禁止用 `sleep` 制造过期证据。
- TTL 锚点：创建 + **每次成功提交**（写续期）；普通读取不续期。
- **只有成功提交才刷新 TTL**：失败、取消、超时、被跳过的提交都不得借刷新 TTL 隐式提交或延长状态。
- 无后台 Janitor goroutine、无 `Close()`；过期判定 = 惰性访问检查 + 显式 `PurgeExpired(ctx)`。
- **过期判定以「能取得执行权」为前提**：能取得执行权且 `expiresAt` 已过 ⇒ 确认为过期 ⇒ 物理删除；不能取得（有在途 Turn）⇒ 视为有活动，按有效会话处理，不删除、不续期。因此正在执行的 Turn 永远不被过期清理破坏。
- 在途 Turn 正常完成 → 其提交生效并刷新 TTL（在途活动视为续期）；失败/取消 → 不提交、不续期。
- 过期即**物理删除**（不可恢复；本任务无持久化，进程退出同样丢失）。

### D5 错误语义（Owner 决定）

- **不新增 `core.ErrorKind`**。`session` 包导出哨兵：`ErrSessionNotFound`、`ErrSessionExpired`、`ErrSessionExists`，统一以 `*core.RunError{Kind: core.KindInvalidArgument, Cause: 哨兵}` 返回。
- 判定双通道：`core.KindOf(err)` 得到 `invalid_argument`；`errors.Is(err, ErrSessionNotFound/Expired/Exists)` 区分具体情形。哨兵身份必须能沿 `RunError.Unwrap` 命中，**不得只编码在错误字符串里**；`Message` 附会话 ID 便于定位。
- 取消/超时复用既有种类：`context.Canceled` → `KindCancelled`；`context.DeadlineExceeded` → `KindDeadlineExceeded`。
- 后续 HTTP API 依赖上述三哨兵区分「不存在 / 已过期 / 重复创建」，不得退化为统一通用错误。
- 被排除的方案：新增 `core.KindNotFound`（语义更清晰，但修改 T00 已批准的错误集合与 `StatusOf`/`isFailureKind` 映射）。

### D6 Store 关系与写入校验（推荐，随批准生效）

- `*Manager` 实现 `session.Store`：`Load` = `Get`（深拷贝）；`Save` = 执行权内**全量替换** Messages/Summary/Todos（不合并、不隐式创建，不存在则 `ErrSessionNotFound`），同样受 `core.ValidateMessages` 与 `Todo.Validate` 约束。
- 分层保持：Store 是存取适配，Turn 是调度与提交。
- 每次 `Append` 对「已提交历史 + 本 Turn 已暂存 + 新批次」调用 `core.ValidateMessages`，失败时暂存区不变、错误带 `CallID`。
- 取舍：全量校验简单且能保证 AC-007 的原子拒绝；长历史下每 Turn 接近 O(n²)，本任务无性能目标。

## Session/Todo 协作机制（跨任务约束）

**单一事实源**：正式 Todo 数据只有一份——`session.Session.Todos`，由 Manager 持有、只在 Turn 暂存提交时变更。T03 不得自建 Todo 存储或影子状态。

**受限能力接口**（`session` 包定义，`*Turn` 实现，T03 采用）：

```go
type TodoOperations interface {
	Todos() []Todo            // 深拷贝：基线 + 本 Turn 尚未提交的修改
	PutTodo(todo Todo) error  // 按 ID upsert：已存在则原地替换，否则追加到末尾
	DeleteTodo(id string) error // 不存在视为幂等成功
}
```

**访问路径（唯一合法路径）**：

- Turn 内的组件（Tool、Context Manager）只能通过装配层注入的 `session.TodoOperations` / `*Turn` 访问 Todo 与摘要，因此看到的是「基线 + 暂存」视图，满足 read-your-writes。
- **禁止把 `*Manager` 注入任何在 Turn 内运行的组件**：`Manager.Save` / `Manager.Delete` / `Manager.RunTurn` 会再次获取同一会话的执行权（Turn 内重入），造成自死锁；`Manager.Get` 虽不阻塞，但只返回已提交状态，会读到本 Turn 的旧值，违反 read-your-writes。
- 能力注入符合 Design §8 的「由装配层显式注入能力」，不违反 T00 INV-005（注入的是受限接口，不是可变 `Session` / `Todo` / `Store`）。

**并发与生命周期**：

- `*Turn` 的暂存区内部加互斥，同一 Turn 内多 goroutine 调用安全（每次调用原子，顺序不保证）；因此 T03 若并行执行同一 step 的多个 Tool，无需自行串行化。
- `*Turn` 与 `fn` 同生命周期：`fn` 返回后 Turn 关闭，写入方法返回 `invalid_argument`，只读方法仍返回安全快照。禁止长期持有 `*Turn` 供异步回调使用；异步工具必须把结果回传给 Loop，由 Loop 在 Turn 内写入。
- T03 若已有与上表不同的受限接口签名，必须先对齐本 Contract；无法对齐 → 报告冲突并走 Contract Revision，不得并行维护两套 Todo 数据。

**与 T05（Context Manager）的边界**：摘要由 Session 持有；T05 只在 Turn 内经 `Turn.SetSummary` 写入，经 `Turn.Session()` 读取消息与摘要视图；T05 不持有 `*Manager`，不实现存储。

## Interfaces and Data

`Op` 固定为 `session.<Type>.<Method>`；公共符号以此为准（超出即 Contract Revision）。

```go
// package session — session/session.go（既有文件，扩展模型）
type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
	TodoStatusCancelled  TodoStatus = "cancelled"
)

type Todo struct {
	ID     string
	Title  string
	Status TodoStatus
}

func (t Todo) Validate() error // ID 非空；Status ∈ 四个取值；Title 不校验

type Session struct {
	ID       string
	Messages []core.Message
	Todos    []Todo
	Summary  string // 新增：会话级摘要，最后一次成功提交的值；"" 表示无摘要
}

type Store interface {
	Load(ctx context.Context, id string) (Session, error)
	Save(ctx context.Context, s Session) error
}
```

```go
// package session — session/manager.go
type ManagerConfig struct {
	TTL time.Duration    // > 0 启用过期；<= 0 关闭（零值即关闭）
	Now func() time.Time // 可选；nil → time.Now（测试注入可控时钟）
}

type Manager struct{ /* 未导出字段；必须经 NewManager 构造，零值不可用 */ }

func NewManager(cfg ManagerConfig) *Manager

func (m *Manager) Create(ctx context.Context, id string) (Session, error) // 已存在 → ErrSessionExists
func (m *Manager) Get(ctx context.Context, id string) (Session, error)    // 深拷贝；不存在/已过期 → 明确错误
func (m *Manager) Delete(ctx context.Context, id string) error            // 获取执行权后删除
func (m *Manager) PurgeExpired(ctx context.Context) int                   // 返回物理删除数
func (m *Manager) RunTurn(ctx context.Context, sessionID string, fn func(*Turn) error) error

func (m *Manager) Load(ctx context.Context, id string) (Session, error) // = Get
func (m *Manager) Save(ctx context.Context, s Session) error            // 执行权内全量替换

var _ Store = (*Manager)(nil)
```

```go
// package session — session/turn.go
type Turn struct{ /* 未导出字段；与 fn 同生命周期 */ }

var _ TodoOperations = (*Turn)(nil)

func (t *Turn) Session() Session      // 基线快照 + 已暂存写入（深拷贝）
func (t *Turn) Append(msgs ...core.Message) error
func (t *Turn) SetSummary(summary string)
func (t *Turn) SetTodos(todos []Todo) // 全量替换，复制切片
func (t *Turn) Todos() []Todo         // TodoOperations
func (t *Turn) PutTodo(todo Todo) error
func (t *Turn) DeleteTodo(id string) error
```

```go
// package session — session/errors.go
var (
	ErrSessionNotFound error // 会话不存在、已删除或已被清理
	ErrSessionExpired  error // 会话仍存在但已超过 TTL
	ErrSessionExists   error // Create 时同 ID 会话仍有效
)
```

接口约束：

- 所有导出方法在入口检查 `ctx`；已取消/超时立即返回对应 `Kind`，不写入。
- `Get` / `Load` / `Create` / `Turn.Session` / `Turn.Todos` 返回**深层复制**（每条 `core.Message` 的 `Content`、`ToolCalls`、`ToolResult`、`Raw` 独立复制；`Todos` 独立切片），调用方修改返回值不影响会话。
- 空 `id` / 空 `sessionID` → `KindInvalidArgument`，不以空 ID 建会话。
- Manager 不校验 `core.Message.ID` 唯一性（core 无此规则，非本任务不变量）；`CallID` 配对由 `core.ValidateMessages` 负责。
- `Delete` / `Save` / `RunTurn` 对同一会话互斥；`Get` / `Load` 不参与执行权。
- 全局资源：**无**。`ErrorKind` 是代码内枚举，不是 Registry 资源。

## Business Invariants

- INV-001（提交原子性）：一次 `RunTurn` 的消息、摘要与 Todo 变更要么整体生效、要么整体不生效；P1—P5 任一不成立都不产生任何持久于会话的变更。
- INV-002（会话串行）：同一会话同一时刻至多一个 Turn 执行 `fn`；提交后每个成功 Turn 的消息连续、不与其它 Turn 交错，总条数等于各成功 Turn 写入条数之和。
- INV-003（跨会话独立）：不存在跨会话全局锁；不同会话的 Turn 可同时执行；某会话的写入、删除、过期不改变其它会话的任何字段。
- INV-004（读隔离与明确失败）：读取返回深拷贝快照；不存在/已过期返回带哨兵的明确错误，不以「空会话 + nil error」表示；三种情形可被 `errors.Is` 区分。
- INV-005（校验原子）：非法消息序列（`invalid_pairing` / `invalid_argument`）与非法 Todo 被拒绝时，会话历史与 Todo 列表的长度、顺序、内容完全不变。
- INV-006（执行权不泄漏）：正常、`fn` 错误、`ctx` 取消、超时、过期、删除、`panic` 等全部路径都释放执行权；排队中取消/超时后 `fn` 不执行、不写入。
- INV-007（过期不破坏在途 Turn）：过期清理不删除正在执行的 Turn 所属会话；在途 Turn 成功后提交并刷新 TTL；失败/取消不得刷新 TTL；过期由「可取得执行权且已过 TTL」唯一判定。
- INV-008（Todo 单一事实源与暂存可见性）：正式 Todo 只有 `session.Session.Todos` 一份；Turn 内连续 Todo 操作能看到自己未提交的修改；Turn 关闭后的写入被拒绝。
- INV-009（分层与模型稳定）：`session` 只 import 标准库与 `core`，无第三方依赖；`core` 既有类型与语义不变；本任务唯一的公共模型变更是 `Session.Summary`、`TodoStatus` 导出常量与 `Todo.Validate`（均已在本 Contract 授权）。

## Failure and Consistency Semantics

| 情形 | 返回 | 会话数据 |
| --- | --- | --- |
| Turn 成功提交（P1—P5 全成立） | `nil` | 追加消息、写入 Summary/Todos、刷新 TTL |
| `fn` 返回错误 | `fn` 的错误（非 `*RunError` 时包 `KindInternal` + `Cause`） | 不变（丢弃暂存） |
| 执行中 `ctx` 取消（即使 `fn` 返回 `nil`） | `KindCancelled` | 不变，**不刷新 TTL** |
| 执行中超时 | `KindDeadlineExceeded` | 不变，不刷新 TTL |
| `fn` panic | 不 recover；`defer` 释放执行权后再向上传播 | 不变（不提交） |
| 排队等待时取消/超时 | `cancelled` / `deadline_exceeded`，`fn` 未执行 | 不变 |
| `Append` 非法序列 | `invalid_pairing`（带 `CallID`）或 `invalid_argument` | 该批次不进入暂存 |
| `PutTodo` / `SetTodos` 非法 Todo | `invalid_argument` | 该批次不进入暂存 |
| 提交前复核发现 entry 已被移除 | `ErrSessionNotFound` | 不提交 |
| 会话不存在 / 已删除 | `KindInvalidArgument` + `ErrSessionNotFound` | — |
| 会话已过期（可取得执行权 + 超过 TTL） | `KindInvalidArgument` + `ErrSessionExpired`，并物理删除 | 删除 |
| 会话 TTL 已过但存在在途 Turn | 按有效会话返回快照（不删除、不续期） | 不变 |
| `PurgeExpired` 命中可清理的过期会话 | 计数 +1 | 物理删除 |
| `PurgeExpired` 命中在途会话 | 跳过 | 不变 |
| `Save` 非法序列 / 不存在 | `invalid_pairing` / `ErrSessionNotFound` | 不变 |
| `Create` 同 ID 仍有效 | `KindInvalidArgument` + `ErrSessionExists` | 不变 |
| Turn 关闭后调用写方法 | `KindInvalidArgument`（Turn 已结束） | 不变 |

一致性规则：

- 单进程内存、单一存储，无跨系统最终一致性、无消息投递与重试幂等键问题。
- 「成功」只表示写入内存并对后续读取原子可见，**不表示任何持久化**（进程退出即丢失）。
- 取消/失败的 Turn：会话数据不变；该 Turn 已产生的消息仍在调用方拿到的 `core.RunResult.Messages`（Run 层事实源），两层互不回滚。
- 重复：Manager 不做消息去重，同一批消息重复提交会重复出现；重复/孤儿 `Result` 由 `core.ValidateMessages` 拒绝。
- 过期清理与排队 Turn 的相对顺序不严格承诺（Go channel 语义）；被清理后排队 Turn 返回 `ErrSessionNotFound`，不得静默继续。
- 外部副作用不在回滚范围：工具已执行的动作不因 Turn 未提交而撤销。

## Allowed / Forbidden Changes

允许（Coder）：

- 修改 `session/session.go`：新增 `Summary`、`TodoStatus` 四个导出常量、`Todo.Validate`，并同步包注释（删除与新职责冲突的「不提供任何实现」表述）。
- 新增 `session/manager.go`、`session/turn.go`、`session/errors.go`。
- 新增测试 `session/manager_test.go`、`session/turn_test.go`、`session/concurrency_test.go`、`session/expiry_test.go`、`session/cancel_test.go`（`Store` 适配测试可并入 `manager_test.go`）。
- 未列出的源文件不得新增；确需新增 → Contract Revision（Design §12 由 Analyst 同步，Coder 不得自行改 Design）。

禁止：

- 修改 `core/**`（含新增 `ErrorKind`）、`llm/**`、`tool/**`、`contextmgr/**`、`runlog/**`、`go.mod`、`Makefile`、`.cnb.yml`、`AGENTS.md`。
- 修改 `.agent/**` 与 `docs/design/**`（Design 由 Analyst 在本交付内更新）。
- 新增第三方依赖、`go.work`、`replace`。
- 后台 goroutine / `Close()` / 后台定时清理；跨会话全局锁（含用单一全局互斥保护会话数据）。
- 新增本 Contract 未列出的导出符号（需 Revision）；为 T03/T05 提前实现它们的模块。
- 以 `sleep` 或时间阈值作为并发/过期证据；以降低断言强度换取测试通过。
- 调用真实付费 LLM API（本任务无外部调用）。

## Verification Requirements

- INV-001 / INV-004 / AC-001 → 单会话写入 N 条消息（含带 `ToolCall` 的 assistant 消息 + 对应 `role=tool` 结果）、`SetSummary`、`SetTodos` 后读取逐字段断言（条数、顺序、`ID`、内容、摘要、Todo 状态）；不存在 ID → `KindOf == invalid_argument` 且 `errors.Is(ErrSessionNotFound)`；修改返回值不影响后续读取。
- INV-003 / AC-002 → 双会话交叉读写逐条比对互不相交；`Delete` A 与使 A 过期后，B 的消息、摘要、Todo 逐字段不变。
- INV-001 / INV-002 / AC-003 → `sync.WaitGroup` 启动 M ≥ 8 goroutine，各 `RunTurn` 写入 K ≥ 3 条带组前缀的唯一消息；断言总数 = M×K、无重复 `ID`、每组连续；再构造「`Append` 后 `fn` 返回错误」与「`fn` 返回 `nil` 但 `ctx` 已取消」两种 Turn，断言历史长度与内容哈希不变；`-race -count=5`。
- INV-003 / AC-004 → 两会话各一 Turn，`fn` 内 `Append` 后经 channel 向对方声明「已进入执行」并等待对方（barrier），断言双方都观察到对方已进入；超时仅作死锁保护。**可逆 mutation**：把每会话信号量换成单一全局信号量后该测试必须失败。
- INV-007 / AC-005 → 注入可控时钟使 TTL 到期，在 Turn 执行中并发 `PurgeExpired`：断言在途 Turn 正常完成、提交生效并刷新 TTL、无部分提交、清理未删除该会话；再断言失败/取消的 Turn 不刷新 TTL（过期后访问返回 `ErrSessionExpired`）；`go test -race -count=5 ./...` 无 race。
- INV-006 / AC-006 → (a) 执行权被占用时排队请求 `ctx` 取消 → `KindCancelled`、`fn` 未执行（`fn` 内标志位断言）、历史未变；(b) 排队超时 → `KindDeadlineExceeded`；(c) 执行中取消 → `KindCancelled`、历史不变、随后请求能在 channel 同步下取得执行权；另加 panic 用例断言后续 Turn 仍可取得执行权。
- INV-005 / AC-007 → 表驱动：孤儿 `Result`、缺失 `Result` 断言 `KindOf == invalid_pairing` 且 `CallID` 指向具体调用；写入前后历史长度与内容哈希一致。
- INV-008 / Todo 协作 → Turn 内 `PutTodo` 后立即 `Todos()` 可见未提交值、`DeleteTodo` 幂等、非法 `Todo`（空 ID / 未知 Status）被拒且列表不变；Turn 关闭后写方法返回 `invalid_argument`；`var _ TodoOperations = (*Turn)(nil)` 编译期约束。
- INV-009 → `go list -m all` 仅根 module；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./session` 只含标准库与 `<module>/core`。
- AC-008 → Cleaner 四方核对：Task ↔ 本 Contract ↔ `docs/design/runtime-architecture.md`（§3 职责、§8 会话/Todo 归属与能力注入、§11 不变量、§12 所有权表）↔ 实现；`git ls-files session/` 与 §12 逐条比对且单归属；残留「session 不实现任何存储」即判不满足。
- 通用 → 仓库根 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race -count=5 ./...`、`make test` 退出码 0；`GOWORK=off go -C .agent/engine test ./...` 仍通过。

## Open Risks

- **T03 接口未落地**：`TodoOperations` 在本仓库任何分支都不存在，无法做符号级核对。若 T03 已按不同签名规划，需在本 Contract 上做 Revision 对齐，否则会出现两套 Todo 路径。
- 每批全量 `core.ValidateMessages` 为 O(n)，长会话下单 Turn 接近 O(n²)；无性能目标，未基准化。
- 取消/失败的 Turn 不进入会话历史（D2 取舍）；若后续 Loop 需要「失败 Turn 留痕」，需 Revision。
- `not_found` / `expired` / `exists` 共用 `KindInvalidArgument`；HTTP 层必须用 `errors.Is` 细分，用 `Kind` 判断会退化。
- `-race` 未纳入 CI 与 `make` 目标，长期证据依赖人工执行。
- 「写续期、读不续期」会回收只读长会话；若需要空闲超时，需改为读续期（读路径引入写入）。
- 无后台清理：调用方忘记 `PurgeExpired` 时，过期数据仅在被访问时回收，内存占用可能短时偏高。
- `TodoStatus` 导出常量解决了 T00 `CLEAN-001` 中 `TodoStatus` 一项（其余枚举仍 OPEN），属 Owner 在本 Contract 中间接解决，需在 T00 侧知悉。
- UNKNOWN：T01 Loop 与 T05 Context Manager 的真实调用形态可能暴露 API 不足（如 Turn 内需要跨会话读取）；出现时走 Contract Revision。

## Owner Decision Record

决定时间：2026-10-20。来源：Owner 明确 Contract Decision（T04 技术方向有条件批准）。适用范围：`agent-session-manager-v1` 及消费本契约的 T01 / T03 / T05。

1. **D1 摘要**：采用 `session.Session.Summary string`。摘要属会话长期状态，由 Session Manager 存储，由后续 Context Manager 负责生成与更新；涉及 T00 已批准公共模型，必须按正式流程核对 T00 Contract 与 Design 的变更要求，不得未经授权直接修改冻结接口。
2. **D2 Turn 语义**：采用 `RunTurn` 暂存 + 整轮原子提交。同 Session 串行、跨 Session 并行；等待执行权支持 `context` 取消；Turn 内写入先进入隔离暂存，失败/取消/超时不得部分提交；**提交条件不得只是 callback 返回 `nil`，必须同时复核 `context` 与最终执行结果**；同一 Turn 内连续 Todo 操作必须能看到未提交的修改；读取必须提供安全快照；执行权在正常、失败、取消、panic 路径均不得泄漏。原子性仅覆盖 Session Manager 管理的内存消息、摘要与 Todo，不承诺撤销外部工具副作用。
3. **Todo 跨任务约束**：T03 使用受限 `TodoOperations` 接口，T04 负责正式 Todo 数据与生命周期；必须核对双方接口兼容性、明确 Todo 操作如何访问 RunTurn 暂存状态、避免重入锁死锁；不得让 T03 与 T04 各自维护互不关联的正式 Todo 数据；无法在冻结接口下同时满足时先报告冲突，不得直接进入 Coder。
4. **D4 过期**：接受推荐 TTL 方案——写入成功刷新 TTL、普通读取不续期；不启动后台 Janitor；惰性过期检查 + `PurgeExpired`；正在执行的 Turn 不得被过期清理破坏；成功提交的在途 Turn 刷新 TTL，失败时不得借刷新 TTL 隐式提交状态；时间依赖可替换以支持确定性过期测试。
5. **D5 错误**：不新增 `core.ErrorKind`；采用 `ErrSessionNotFound`、`ErrSessionExpired`、`ErrSessionExists` 等 `session` 包错误并支持 `errors.Is`；后续 HTTP API 必须能区分不存在、过期与重复创建，不得混为无法定位的通用错误。
6. **执行要求**：先完善 Contract 的 Session/Todo 协作机制并核对 T03、T05 与 T00 公共契约兼容性；修改已批准公共契约或长期 Design 必须走 Workflow V3 正式流程，不得由 Coder 自行修改；方案一致后再完成正式批准记录、Design 更新与 Handoff。本任务不调用真实付费 LLM API。

AC 兼容性核对：以上决定与 AC-001—AC-008 及 Scope 无冲突。AC-004（跨会话并行）由 per-session 信号量满足；AC-003/AC-005/AC-007（原子性）由 P1—P5 提交条件与暂存提交满足；AC-006（取消/超时）由可取消的执行权获取满足。D2 的「取消/失败 Turn 不落库」不违反 AC-006 的「已有历史不被回滚破坏」——已有历史指提交过的历史。

由上述决定直接派生、Analyst 记录为批准内容的实现约束（若 Owner 有异议，实现前可退回）：

- `TodoStatus` 四个导出常量与 `Todo.Validate`（Todo 生命周期归本模块的直接后果，同时解决 T00 `CLEAN-001` 的 `TodoStatus` 一项）；
- 受限接口命名为 `session.TodoOperations` 并由 `*Turn` 实现（T03 消费，避免两套 Todo 数据）；
- `*Turn` 暂存区内部同步，允许同一 Turn 内多 goroutine 调用（避免对 T03 的 Tool 并发模型施加反向约束）；
- 提交前置条件 P1—P5 与「过期判定需可取得执行权」的细化（落实「不得仅凭 `nil` 判定提交」与「在途 Turn 不被清理破坏」）。

## Design Update（本交付内由 Analyst 执行）

`docs/design/runtime-architecture.md`：

- §2 分层图与 §3 职责表：`session` 改为「Session / Todo / 摘要数据类型与 `Store` 抽象 + 单进程内存 Session Manager（会话生命周期、Turn 执行权与原子提交、过期清理）」；「明确不负责」改为持久化实现、跨进程锁与多副本一致性、摘要生成算法、Agent Loop 调度。
- §8：删除「不实现任何存储」，写入内存实现的非持久化性质、Turn 执行权与提交语义、摘要与 Todo 归属、`session.TodoOperations` 能力注入（并说明它符合「显式注入能力」而无需修改 T00 INV-005）、禁止在 Turn 内注入 `*Manager`。
- §9：补一条边界——摘要由 Session 持有，Context Manager 只生成/更新，不存储。
- §11：新增会话级不变量（提交原子性、会话串行与跨会话并发、读隔离与明确失败、执行权不泄漏、过期与在途 Turn、Todo 单一事实源），并把 INV-005 的证据补为「签名核对 + 能力注入约束」。
- §12：所有权表新增 `session/manager.go`、`session/turn.go`、`session/errors.go` 与五个 `_test.go`；`session/session.go` 说明改为「类型 + `Summary` + `TodoStatus` 常量 + `Todo.Validate`」。
- §14：从后续任务移出「`session.Store` 的内存实现」与「Todo 生命周期」，仅保留持久化实现。
