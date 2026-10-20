# Technical Contract

## Decision Status

`WAITING_FOR_OWNER_COORDINATION`（D7）；`OWNER_DIRECTION_APPROVED`（2026-10-20，D1—D6）仍有效，但**正式 `APPROVED` 尚未成立**。

- D6 前置已满足：TaskBuilder 已在 `a2c92d6` 规范化 `task.md`（Design Impact 冲突消除、5 工具、AC-006 证据口径收窄、新增 AC-009）。
- **新阻塞（D7）**：T04 现已有 APPROVED Contract + Design 更新（`origin/feat/agent-session-manager-v1` `0d9797b`），其中 `session.TodoOperations` 的方法集为 `Todos()` / `PutTodo(Todo)` / `DeleteTodo(id)`，与 Owner D1 明确的 `Create` / `List` / `Complete` **不一致**，且不提供 Todo ID 生成与状态迁移。按 Owner D1「T04 的既有 Task / Contract 与该安排冲突必须先报告并协调，不得单方面修改」，本 Contract 停在 D7，等 Owner 裁决接口归属。
- 因此不执行 A1/A2、不更新 `docs/design/*`、不启动 Coder。

`state.contract.status` 保持 `PENDING`（schema 无「方向已批准 / 待协调」枚举，不提前写入 `APPROVED`）。

任务：`builtin-agent-tools-v1`（Design Impact `UPDATE` → `docs/design/runtime-architecture.md`，见 D6）。

## Problem

仓库已冻结契约层（T00）：`tool.Tool`、`core.ToolResult{CallID,Name,Content,IsError}`、`session.Todo{ID,Title,Status}`，但没有任何具体工具实现。本任务需一次固定三件长期事实：

1. 承载具体工具的模块边界与依赖方向（不得写入 `tool/tool.go`，不得占用 `tool/**` 与并行任务 `tool-registry-executor-v1` 冲突）；
2. Todo 的写入路径——Tool 不持可变 `Session` / `session.Store`，真实 Todo 数据归 Session Manager（T04），工具只经受限端口读写；
3. 模型可见协议：5 个工具名、`Parameters` JSON Schema、`Content` 结构与错误表达。

## Verified Current Behavior

- VERIFIED：`tool.Tool` / `tool.Request`（内嵌 `core.ExecutionRef` + `core.ToolCall`）已冻结（`tool/tool.go:17-29`）；`tool.Registry` 无实现。
- VERIFIED：`core.ToolResult.Validate()` 要求 `CallID` 非空（`core/toolcall.go:34-40`）；`ToolSpec.Parameters` 为 JSON Schema 原文。
- VERIFIED：`session/session.go` 仅有 `TodoStatus` / `Todo{ID,Title,Status}` / `Session{ID,Messages,Todos}` / `Store{Load,Save}`；无实现、无测试、`TodoStatus` 无导出常量（T00 `CLEAN-001`）。
- VERIFIED：仓库无任何具体工具实现；根 `go.mod` 为 `go 1.24`、零 `require`；`Makefile` 的 `test` = `go test ./...`；本机 `go1.24.1 linux/amd64`。
- VERIFIED：Design §8 禁止向 Tool 传可变 `Session` / `Todo` / `Store`；§11 INV-005「Session 独占 Todo，Tool 只拿 ID」仅有签名证据；§12 所有权表无 `builtin/` 路径。
- VERIFIED：Owner 所称 **T02 = `tool-registry-executor-v1`**（已 APPROVED + 已实现，HEAD `551d6c3`）。其 `tool/validate.go` 的 fail-closed Schema 子集白名单为 `type` / `properties` / `required` / `items` / `enum` / `additionalProperties` / `description` / `title`，且根 Schema 必须 `type: object`；空 `Parameters` 规范化为 `{"type":"object","properties":{}}`。本 Contract 的 5 个 `Parameters`（仅 `type` / `properties` / `description` / `enum` / `required`）**是它的严格子集**，注册期校验可通过；漏写 `additionalProperties` 时该实现默认 `true`，与本 Contract「忽略未知字段」一致。
- VERIFIED：`llm-openai-client-v1`（HEAD `8602515`）把 `Parameters` 原样透传给 `tools[].function.parameters`，不改变 schema 语义，故 OpenAI-compatible 侧无额外约束。
- VERIFIED：**T04 现已存在 APPROVED Contract + Design 更新**（`origin/feat/agent-session-manager-v1` `0d9797b`；设计已写入 §3 / §8 / §11 / §12 / §14），其 `session.TodoOperations` 为 `Todos() []Todo` / `PutTodo(Todo) error` / `DeleteTodo(id) error`，由 `*Turn` 实现，并声明「T03 采用 `session.TodoOperations`」；该 Contract 同时导出 `TodoStatus` 四个常量、新增 `session.Session.Summary`、`Todo.Validate`。**本 Contract 判定的 D7 冲突即来自此条**。
- VERIFIED：T04 与 T02 各自在 Design §11 新增了 `INV-009`…（T02 到 `INV-013`、T04 到 `INV-016`），两条分支编号互相重叠；本任务不得再占用同一号段（见 `Coordination`）。
- VERIFIED：`tool-registry-executor-v1` 已提交 `tool/registry.go` / `validate.go` / `executor.go` 及测试；`session/**` 在 T04 分支尚未提交实现（只有 Contract + Design）。`builtin/` 不被任何分支占用。
- VERIFIED：`.agent/workflow.yaml` 为 `resources: {}`；`state.yaml` 为 `schema_version: 3`；本任务不占用任何全局资源。
- UNKNOWN：D7 裁决前，`todo_*` 工具应接收 `builtin.TodoOperations` 还是 `session.TodoOperations`；T07 装配层任务尚未创建。

## Recommendation（已被 Owner 调整）

RECOMMENDATION 已由 Owner 有条件批准并调整（D6 前置：`task.md` 已由 TaskBuilder 于 `a2c92d6` 规范化），采纳版本见 `Selected Design`。相对原推荐的调整：

| 项 | 原推荐 | Owner 决定 |
| --- | --- | --- |
| Todo 端口命名 | `TodoStore` | `TodoOperations`（Create / List / Complete）；**归属待 D7** |
| 生产内存实现 | 包内提供 `MemoryTodoStore` | **不提供**；真实 Todo 数据与生命周期归 T04，T03 只用测试专用 Fake |
| 已取消状态 | 额外 `todo_cancelled` 领域错误 | 不引入；状态机归 T04，T03 只映射「不存在」 |
| 并发证据 | 由 `MemoryTodoStore` 证明会话级并发 | 工具层证据（无共享可变状态 + `-race`），真实会话并发与隔离归 T04/T07 |

关键取舍（保持）：Tool 持有受限可写端口是对 Design §8「不向 Tool 传可变 Store」的最小字面豁免；换取 Todo 工具可用，同时用「无生产实现」确保 Tool 不成为 Todo 的第二事实来源。

## Selected Design

依据 Owner D1—D6（2026-10-20）。

### 1. 模块边界

新增 L2 包 `builtin/`（不进入 `tool/` 目录）。依赖只允许：标准库 + `<module>/core` + `<module>/tool` + `<module>/session`；不得 import `llm` / `contextmgr` / `runlog` 及任何上层包。

### 2. Todo 写入路径（D1；接口归属待 D7 裁决）

**以下为 Owner D1 的方法集，若 D7 采纳 C1 推荐方案，则本段整体迁到 `session` 包（`type TodoOperations` 同方法集，哨兵改 `session.ErrTodoNotFound`），`builtin` 只引用不定义。**

```go
// package builtin（D7 裁决后可能改为 package session）
type TodoOperations interface {
	Create(ctx context.Context, sessionID, title string) (session.Todo, error)
	List(ctx context.Context, sessionID string) ([]session.Todo, error)
	Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error)
}

var ErrTodoNotFound = errors.New("builtin: 该会话下不存在该待办")
```

- **T03 负责**：五个工具的实现；接口定义按 D1 归 T03，但 T04 已 APPROVED 的 Contract 声明 `session.TodoOperations` 为权威并要求 T03 采用 → 存在冲突，等待 D7 裁决（见 `Coordination` C1）。
- **T03 不提供**任何生产存储实现（无 `MemoryTodoStore`、无 `session.Store` 实现、无文件/DB/Redis）。真实 Todo 数据与生命周期由 T04 Session Manager 拥有与实现。
- 端口由装配层（T07）在 Turn 内注入到 `todo_*` 工具构造函数（不得注入 `*Manager`，否则重入死锁）；工具每次调用都显式传 `req.SessionID`。
- **会话身份唯一来源**：`tool.Request.SessionID`（可信、由运行时注入）。工具从不由 `Arguments` 读取会话；参数 schema 不含任何 session 字段，且 `Arguments` 中出现 `session_id` / `sessionId` / `session` 键一律 `invalid_arguments`（显式拒绝而非静默忽略）。
- 二者均**不得修改 T00 已冻结接口**（`core/**`、`tool/tool.go`、`session/session.go`；T04 已按 Revision 流程经 Owner 授权修改 `session/session.go`，本任务不改）。
- T04 的 Task / Contract 与本安排的冲突**已发生**，按 Owner D1 要求先报告（`Coordination` C1），未单方面修改。

### 3. 工具协议（D2 / D3 / D4）

| 工具名 | 职责 | 失败码 |
| --- | --- | --- |
| `calculator` | `op` ∈ `add`/`sub`/`mul`/`div`，两个 `float64` 操作数 | `invalid_arguments` / `missing_operator` / `unknown_operator` / `missing_operand` / `division_by_zero` / `result_not_finite` |
| `mock_search` | 固定有序数据集、大小写不敏感子串匹配 | `invalid_arguments` / `empty_query` |
| `todo_create` | 创建待办（标题非空） | `invalid_arguments` / `missing_session_id` / `empty_title` |
| `todo_list` | 列出本会话待办 | `missing_session_id` |
| `todo_complete` | 完成待办（重复完成幂等） | `invalid_arguments` / `missing_session_id` / `todo_not_found` |

- 领域失败统一 `{"error":{"code":"<snake_case>","message":"<稳定中文说明>"}}` + `IsError=true`，且 `Execute` 的 `error` 为 nil。
- 基础设施失败（注入端口返回非领域错误、`ctx` 已结束、端口未注入）按现有 `*core.RunError` 契约返回非 nil `error`；不得降级成 `IsError=true` 伪装成功，也不得把路径、堆栈、连接串、凭据等内部细节写入 `Content`（原始错误只留在 `RunError.Cause`）。
- Calculator：`float64` + JSON number；除零、溢出、NaN、Inf 与浮点精度边界必须有测试（见 `Verification Requirements`）。
- Mock Search：源码内置有序数据集；`query` 去首尾空白并小写后匹配 `title` / `summary` 子串；结果按数据集顺序、不排序不打分不截断；空查询 → `empty_query`；无命中 → `IsError=false` + `results: []` + `"mock":true`。
- `todo_complete`：已完成 → 幂等成功（`IsError=false`，仍为 `completed`）；不存在 → `todo_not_found` 领域错误且无副作用。

### 4. JSON Schema 兼容性（D4）

`Parameters` 只使用 `type` / `properties` / `description` / `enum` / `required` 五个关键字；不得使用 `$ref` / `anyOf` / `oneOf` / `allOf` / `not` / `additionalProperties` / `default` / `format` / `pattern`。该限制由测试机械核对（遍历 schema JSON 的键集合）。`enum` 仅用于 `calculator.op`；若 T02 实现表明其支持范围更窄，走 Contract Revision，不在实现中自行降级。

## Interfaces and Data

### 文件清单（新增 10 个源文件）

```text
builtin/                     新 L2 包：内置工具实现（不进入 tool/）
  builtin.go                 包文档、工具名常量、错误码常量、成功/失败 Content 编码、参数解码与会话字段拒绝
  calculator.go              Calculator 工具
  search.go                  Mock Search 工具
  search_data.go             固定预置数据集（有序、纯常量）
  todo.go                    TodoOperations 端口（归属待 D7）、ErrTodoNotFound、todo_create / todo_list / todo_complete
  spec_test.go               5 个 Spec schema 子集合法性、名称唯一、All 聚合、CallID/Name 回填、畸形参数不 panic
  calculator_test.go         AC-001 / AC-002 与 D4 数值边界
  search_test.go             AC-003 确定性、Mock 标识、无命中、空查询、大小写
  todo_test.go               AC-004 / AC-005、空 SessionID、幂等完成、会话字段拒绝、端口错误分级
  todo_concurrency_test.go   工具层并发调用 + 测试专用 Fake 的原子性、-race
```

测试专用 Fake（实现 `TodoOperations`，仅存在于 `_test.go`）不得进入生产代码，也不得作为「真实 Session 隔离已验证」的证据。

### 公共符号（Coder 必须保持）

```go
// package builtin

const (
	ToolNameCalculator   = "calculator"
	ToolNameMockSearch   = "mock_search"
	ToolNameTodoCreate   = "todo_create"
	ToolNameTodoList     = "todo_list"
	ToolNameTodoComplete = "todo_complete"
)

func NewCalculator() tool.Tool
func NewMockSearch() tool.Tool
func NewTodoCreate(ops TodoOperations) tool.Tool
func NewTodoList(ops TodoOperations) tool.Tool
func NewTodoComplete(ops TodoOperations) tool.Tool
// All 顺序固定：calculator, mock_search, todo_create, todo_list, todo_complete。
func All(ops TodoOperations) []tool.Tool

type TodoOperations interface { /* Create / List / Complete，见 Selected Design §2 */ }
var ErrTodoNotFound = errors.New("builtin: 该会话下不存在该待办")
```

具体工具类型不导出，只经构造函数暴露 `tool.Tool`。**端口与哨兵的最终归属待 D7 裁决**：若保留在本包，`session` 实现方无法反向 import（L1→L2 违规），只能由 T07 适配器实现，而 ID 生成与状态迁移将落到装配层；若按 C1 推荐迁到 `session`，则 `builtin` → `session` 单向依赖，无违规。D7 裁决后本节随之定稿。

### 模型可见协议

```json
{"type":"object","properties":{"op":{"type":"string","enum":["add","sub","mul","div"],"description":"运算符"},"a":{"type":"number","description":"第一个操作数"},"b":{"type":"number","description":"第二个操作数"}},"required":["op","a","b"]}
{"type":"object","properties":{"query":{"type":"string","description":"查询关键词，大小写不敏感的子串匹配"}},"required":["query"]}
{"type":"object","properties":{"title":{"type":"string","description":"待办标题，非空"}},"required":["title"]}
{"type":"object","properties":{}}
{"type":"object","properties":{"id":{"type":"string","description":"本会话内的待办标识"}},"required":["id"]}
```

结果形状：

- `calculator`：`{"operation":"div","result":2.5}`
- `mock_search`：`{"mock":true,"query":"go","results":[{"id":"doc-1","title":"…","summary":"…"}]}`；无命中 `"results":[]`
- `todo_create` / `todo_complete`：`{"todo":{"id":"todo-1","title":"写测试","status":"pending"}}`
- `todo_list`：`{"todos":[…]}`，空会话 `{"todos":[]}`
- 任何失败：`{"error":{"code":"…","message":"…"}}`，不含 `result` 类字段

工具名与字段名是模型可见长期协议，变更需 Contract Revision。

### 数据约束

- 无数据库、无 Redis、无 MQ、无 migration、无网络、无环境变量依赖；根模块保持零 `require`。
- 本任务不持有任何 Todo 状态：Todo 的 ID 生成、状态迁移、会话隔离与持久化语义全部由 `TodoOperations` 实现方（T04 / T07 装配）决定；工具只按值消费 `session.Todo`。
- `TodoStatus` 在 `builtin` 内以私有常量表示 `pending` / `completed`；不修改 `session/session.go` 补导出常量。
- 全局资源：无。`resources.reservations` 保持为空，不新增 Registry 文件。

## Business Invariants

- INV-B01（工具身份回填）：`Execute` 返回的 `core.ToolResult` 恒满足 `CallID == req.Call.ID`、`Name == Spec().Name`（成功与失败一致）；`All()` 内工具名唯一。
- INV-B02（失败分级）：除零、未知运算符、缺操作数、非法 JSON、空查询、空 `SessionID`、空标题、待办不存在、参数中出现会话字段，只能表现为 `IsError=true` + `err==nil`；只有端口基础设施错误、`ctx` 已结束、端口未注入才返回非 nil `*core.RunError`。
- INV-B03（计算有限性）：Calculator 任何 `IsError=false` 结果的 `result` 都是有限 `float64`；除数为 0（含 -0）必然失败；成功结果永不出现 NaN/±Inf（含乘法/除法溢出路径）。
- INV-B04（搜索确定性）：同一查询同一数据集下 `Content` 逐字节一致、顺序等于数据集顺序；无命中 `IsError=false` + 空数组；所有成功结果含 `"mock":true`。
- INV-B05（会话作用域来源唯一）：Todo 工具的全部读写作用域只由 `req.SessionID` 决定；`SessionID` 为空 → 拒绝且**不调用**端口；`Arguments` 中出现 `session_id` / `sessionId` / `session` → 拒绝；用其他会话的待办标识 → `todo_not_found` 且无副作用。
- INV-B06（工具层并发）：工具实例不含共享可变状态，可被并发 `Execute`；在并发安全端口下，并发创建 N 条后列表恰为 N 条且标识集合大小 N，重复完成终态唯一；`go test -race` 无 data race。**本条不声称真实 Session 隔离或持久化并发已成立。**
- INV-B07（无生产存储）：`builtin` 生产代码不包含任何 Todo 状态存储或 ID 生成实现；`TodoOperations` 实现只出现在测试与后续装配任务中。
- INV-B08（依赖方向）：`builtin` 只 import 标准库 + `core` / `tool` / `session`；`tool/tool.go`、`session/session.go`、`core/**` 逐字节未变。
- INV-B09（Schema 子集）：5 个 `Parameters` 只用 `type` / `properties` / `description` / `enum` / `required`，并被测试机械核对。

## Failure and Consistency Semantics

- 成功含义：Calculator / Mock Search 的成功只表示「本次返回确定结果」，无状态；Todo 的成功表示「`TodoOperations` 实现已接受该操作」，**不表示**任何持久化已发生，也不表示 Session Manager 之外的状态一致。
- 事实来源：Todo 的唯一事实来源是 Session Manager（T04 实现，T07 装配）；T03 不保留副本、不缓存、不生成 ID，因此不存在双写问题。装配层只允许注入一个 `TodoOperations` 实现。
- 原子性：工具自身不做「读-改-写」，`Complete` 语义（定位 + 置为 completed）由端口实现原子完成；工具的失败路径在调用端口前返回，不产生部分写入。
- 幂等：`todo_complete` 对已完成待办重复调用返回成功且内容一致；`todo_create` 不幂等（每次产生新待办，去重不在 Scope）。
- 取消与超时：Calculator / Mock Search 不检查 `ctx`；Todo 工具在调用端口前检查 `ctx.Err()`，非 nil 时返回 `*core.RunError`（`Kind=cancelled` / `deadline_exceeded`，`Op="builtin.<tool>.Execute"`）且不写入。端口返回 `context.Canceled` / `DeadlineExceeded` 同样映射为 `KindCancelled` / `KindDeadlineExceeded`；其他端口错误包装为 `*core.RunError{Kind: core.KindOf(err), Cause: err}`（非 RunError → `internal`），`Message` 使用固定中文文本，不回显内部细节。
- 证据边界（Owner D1）：T03 的测试只能证明「工具把 `req.SessionID` 正确传递、按码映射失败、不 panic、并发调用无竞态」；**真实会话隔离、跨会话不可写、生命周期与并发一致性由 T04 Session Manager 与 T07 装配证明**，T03 不得在交付中声明其已验证。
- 无跨系统一致性：无数据库、无 MQ、无异步复制。

## Allowed / Forbidden Changes

- 允许（Coder）：新增 `builtin/` 下上述 10 个文件。
- 允许（Analyst，仅在 TaskBuilder 规范化完成且 Contract `APPROVED` 后）：更新 `docs/design/runtime-architecture.md`。
- 禁止：提供任何生产 Todo 存储实现（`MemoryTodoStore`、`session.Store` 实现、文件/DB/Redis 存储）、在 `builtin` 内生成或持有 Todo ID。
- 禁止：修改 `core/**`、`tool/tool.go`、`session/session.go`、`llm/**`、`contextmgr/**`、`runlog/**` 的既有类型、字段、方法与错误种类；为 `session` 补导出 `TodoStatus` 常量。
- 禁止：实现 `tool.Registry`、工具执行器、Agent Loop、LLM Provider 调用、Session Manager、CLI / HTTP / 二进制。
- 禁止：引入第三方依赖、访问网络、调用真实 LLM API、读取真实凭据或环境变量。
- 禁止：修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`AGENTS.md`、`Makefile`、`go.mod`。
- 禁止：在 `tool/` 目录内新增文件；`builtin` import 上层包或实现方包。

## Coordination（Owner 要求报告，不单方面修改）

### C1（D7，阻塞）：T04 已 APPROVED 的 `session.TodoOperations` 与 Owner D1 的方法集冲突

证据（`origin/feat/agent-session-manager-v1`，Contract `APPROVED`，Design 已更新）：

```go
// session 包（T04 D1/Design §8 原文）
type TodoOperations interface {
	Todos() []Todo            // 深拷贝：基线 + 本 Turn 未提交修改
	PutTodo(todo Todo) error  // 按 ID upsert
	DeleteTodo(id string) error
}
```

冲突点：

1. **方法集**：Owner D1 要求 `Create` / `List` / `Complete`；T04 是 `Todos` / `PutTodo` / `DeleteTodo`。按 T04 的接口，`todo_create` 必须由工具自己生成 Todo ID、`todo_complete` 必须由工具自己实现状态迁移，直接违反本任务 `task.md` 的「工具不生成 ID」与 Out of Scope「Todo 的 ID 生成规则、状态机扩展归 T04」。
2. **接口归属**：Owner D1 写「T03 负责…及接口定义」；T04 Contract 写「只能由本 Contract 定义 T04 的权威接口并要求 T03 采用」。两处不能同时成立。
3. **分层**：若保留 `builtin.TodoOperations` 并由 `session` 实现，`session`（L1）必须 import `builtin`（L2），违反 Design §2 分层；唯一避免方式是把实现放在装配层（T07），但 ID 生成与状态迁移属 Todo 生命周期，不应落在装配层。

推荐（需 Owner 裁决，不自作决定）：**端口归 `session`，方法集按 Owner D1 扩展为 `Create` / `List` / `Complete`**，即 T04 走一次 Contract Revision：`*Turn` 上新增 `Create(ctx, title) (Todo, error)`（由 T04 生成 ID）、`List() []Todo`、`Complete(id) (Todo, error)`（幂等；不存在返回 `session.ErrTodoNotFound`）。T03 随后把工具构造函数参数类型改为 `session.TodoOperations`，删除 `builtin.TodoOperations` 与 `builtin.ErrTodoNotFound`。这样同时满足 D1 的方法集、D1 的「T04 拥有生命周期」、Design §2 分层（`builtin` → `session` 单向），且不产生两套 Todo 端口。

备选（不推荐）：T03 保留 `builtin.TodoOperations`，T07 写适配器包装 `*Turn` 并自行生成 ID 与状态迁移。可满足 D1 字面，但把 Todo 生命周期移出 T04，与 D1「生命周期由 T04 管理」矛盾，且增加一层无收益适配。

若 Owner 裁决采纳推荐方案，本 Contract 的 `Interfaces and Data` 需同步替换：`TodoOperations` 归属 `session`、删除 `ErrTodoNotFound`（改用 `session.ErrTodoNotFound`）、`TodoStatus` 改用 T04 已导出的 `session.TodoStatusPending` / `session.TodoStatusCompleted`（当前分支基线尚无这些符号，需 T04 合并后才能编译，属集成顺序问题）。

### C2（需 Owner 决定）：Design 不变量编号已被 T02 / T04 占用且互相重叠

T02 在 §11 新增 `INV-009`—`INV-013`，T04 新增 `INV-009`—`INV-016`（两条分支独立编号，已互相重叠）。本任务不能再用 `INV-009` 起的新号。建议：T03 在 §11 下新增独立小节「11.x 内置工具不变量（`builtin-agent-tools-v1`）」，用任务内编号 `INV-B01`—`INV-B09` 引用 Contract，全局重编号留待 Owner 集成时统一处理。

### C3（T04 / T07 装配衔接）

T04 Contract 的「访问路径」要求 Turn 内组件只能通过注入的 `session.TodoOperations` / `*Turn` 访问，且禁止注入 `*Manager`（重入死锁）。这与本任务「构造函数注入端口、工具不做读改写」一致；无论 D7 如何裁决，T07 都必须在 Turn 内注入能力接口，不得注入 `*Manager`。

## Design Sync Plan（D7 裁决 + `APPROVED` 后由 Analyst 执行，先于 Coder）

以 `origin/develop` 当前内容为基线增量写入（不覆盖 T02 / T04 已落条文）：

1. §2：新增 `L2 builtin`（依赖 `core` / `tool` / `session`），原「L2（未实现）Agent Loop / 装配层」改为 `L3`；补「L1 不得反向依赖 L2」。
2. §3：新增 `builtin` 模块行（五个内置工具 + Mock 数据集；不负责 Registry / 调度 / 持久化 / Todo 生命周期）。此表与 T04 已改的 `session` 行相邻，仅追加一行。
3. §8：在 T04 已写的「注入会话能力时使用受限接口」段落后追加工具侧条文——会话身份只来自 `tool.Request.SessionID`、空 `SessionID` 与 `Arguments` 会话字段的拒绝点、工具不做读改写与 ID 生成、幂等完成由端口保证；不重写 T04 关于 `session.TodoOperations` / `*Turn` / `Manager` 的条文（D7 裁决后如需改端口方法集，由 T04 的 Revision 落该段）。
4. §11：**不新增全局 `INV-0xx`**（T02 的 `INV-009`—`013` 与 T04 的 `INV-009`—`016` 已重叠，见 C2）。改为新增小节「内置工具不变量（`builtin-agent-tools-v1` Contract `INV-B01`—`INV-B09`）」，并在 INV-005 行按 D7 结果与 T04 现有文案对齐（T04 已改为「Tool 只拿 ID 或受限能力接口」）。
5. §12：补入 `builtin/` 下 10 个文件（单归属），位置紧邻 T02 已补的 `tool/*` 行。
6. §14：在「归属后续任务」表补 T03 证据边界（工具层证据 vs T04/T07 运行期证据），并调整「Todo 生命周期」行（T04 已改为只留持久化）。

## Verification Requirements

- INV-B01 / AC-007 → `spec_test.go`：遍历 `All(fake)`，断言 `Spec().Name` 唯一、`Parameters` 合法 JSON object、`required` 与实现校验一致；以固定 `CallID` 各执行一次成功与失败，断言回填正确且 `Validate()` 通过。
- INV-B09 → `spec_test.go` 遍历 schema JSON 全部键，断言键集合 ⊆ {`type`,`properties`,`description`,`enum`,`required`}（递归到 property 内层）。
- INV-B02 / AC-002 → `calculator_test.go` 表驱动：`{"op":"div","b":0}`、`b:-0.0`、`op:"pow"`、缺 `a`、`a:"2"`、`a:1e309`、`Arguments` 为空 / `not-json` / `[]`；每条断言 `IsError==true`、`err==nil`、`error.code` 等于期望码、`Content` 不含 `"result"` 键、不 panic（`defer recover()`）。
- INV-B03 / AC-001 / D4 → `calculator_test.go`：整数、负数、`0.5`/`2.5` 精确小数、`1/3` 除不尽（相对误差 ≤ 1e-12）、`1e308*10` 与 `1e308/1e-308`（`result_not_finite`）、`1e-308/1e308`（下溢为 0，成功且有限）、`0.1+0.2`（断言有限且与 `0.30000000000000004` 数值一致，不用字符串相等）、`NaN` / `Infinity` 字面量（非法 JSON → `invalid_arguments`）；并断言所有成功 `result` 满足 `!IsNaN && !IsInf`。
- INV-B04 / AC-003 → `search_test.go`：同查询连续 ≥ 2 次 `Content` 完全相等；含 `"mock":true`；`Go` 与 `go` 结果一致；无命中 `results` 为空数组且 `IsError==false`；空查询/纯空白 `empty_query`。
- INV-B05 / AC-004 / AC-005 → `todo_test.go`：空 `SessionID` 用 spy Fake 断言零调用 + `missing_session_id`；`Arguments` 含 `session_id` / `sessionId` / `session` → 拒绝且零调用；A 建 2 条、B 列表为空；B 用 A 的 ID 完成 → `todo_not_found` 且 A 的数据逐字段不变；不存在 ID → `todo_not_found` 且列表不变；重复完成 → 成功且 `completed`；Fake 返回普通 error → `err != nil` 且 `KindOf(err)==KindInternal`；Fake 返回 `context.Canceled` → `KindCancelled`；`ctx` 预取消 → `err != nil`、`KindCancelled`、零调用；`All(nil)` 的 Todo 工具 → `err != nil`（`KindInternal`）、不 panic。
- INV-B06 / AC-006 → `todo_concurrency_test.go`：同一会话并发创建 N=50（并发安全 Fake），断言列表 50 条、ID 集合大小 50；并发完成同一 ID 断言终态唯一 `completed`；两会话并发创建断言互不包含对方 ID；`go test -race -count=1 ./...` 通过。测试注释与 `Open Risks` 必须写明该证据不覆盖真实 Session 隔离。
- INV-B02 / AC-009 → `todo_test.go`：端口返回普通 `error` → `err != nil`、`KindOf(err)==KindInternal`、`Content` 不含该错误原文中的路径 / 凭据样例；端口返回 `context.Canceled` → `KindCancelled`；预取消 `ctx` → `err != nil`、`KindCancelled`、端口零调用；`All(nil)` → `err != nil`、`KindInternal`、不 panic。
- INV-B07 → 代码核对：`git grep -n "type .*Store struct" -- builtin/` 无命中；生产文件不含 ID 生成逻辑；Fake 仅在 `_test.go`。
- INV-B08 / AC-007 → `go build ./...`、`go vet ./...`、`make test` 退出码 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 中 `builtin` 行只含标准库 + `core` / `tool` / `session`；`git diff --stat <baseline>..HEAD -- core tool session llm contextmgr runlog` 为空。
- AC-008 → Analyst 先更新 Design，Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对；§12 与 `git ls-files` 双向差集为空。

环境：Linux + Go 1.24.x + GNU Make；无 MySQL / Redis / MQ / 外网 / API Key / 真实 LLM 调用。

## Open Risks

- **证据边界易被夸大**：无生产存储意味着「会话隔离」在 T03 只有 Fake 证据；若 Cleaner / OwnerGate 不严格区分，会把 Fake 当运行期证据。已写入 INV-B06 / Design Sync Plan / Verification 注释要求。
- **D7 未裁决前无法定稿**：`TodoOperations` 归属与方法集决定公共符号与文件清单；若 Owner 采纳 C1 推荐方案，本 Contract 的 `Interfaces and Data` 与 Fake 签名都要改，且需 T04 先走 Revision。
- **集成顺序耦合**：若改用 `session.TodoOperations`，`builtin` 将依赖 T04 合并后的 `session` 符号（`TodoStatus` 常量、`ErrTodoNotFound`），本分支在 T04 合并前无法编译——需 Owner 决定 merge 顺序或允许跨分支依赖（当前禁止）。
- **Design 章节并发修改**：T03 / T04 都改 §3 / §8 / §11 / §12/§14，且 T02 已改 §7 / §11 / §12 / §14；merge 顺序、章节归属与不变量编号（C2）需 Owner 决定。
- **`float64` 精度**：`0.1+0.2 != 0.3`，模型可能意外；协议已固定，若要求十进制精确算术需 Revision。
- **`empty_query` / `empty_title` / 会话字段拒绝** 属主动收紧（Task 未逐条要求），Owner 已批准 D4 空查询为参数错误；`empty_title` 与会话字段拒绝是 Analyst 判定，Cleaner 按 Contract 审查。
- **`ctx` 语义**：T03 只固定工具层最小响应，Loop 侧如何消费归 Agent Loop 任务，运行期证据缺失。

## Owner Decision Record

决定时间：2026-10-20。来源：Owner 明确「T03 设计方案有条件批准」。适用范围：`builtin-agent-tools-v1`（T03）及其与 T02 / T04 / T07 的衔接约束。**本次仅批准设计方向**，不构成 Contract `APPROVED`，也不授权启动 Coder。

1. **D1（Todo 写入路径）**：采用 `TodoOperations` 受限接口注入，含 `Create` / `List` / `Complete`。
   - T03 负责五个工具的实现及接口定义。
   - T03 不提供独立的正式 `MemoryTodoStore`。
   - 正式 Todo 数据及生命周期由 T04 Session Manager 管理。
   - T03 使用测试专用 Fake 验证工具行为，**不得把 Fake 测试当作真实 Session 隔离已验证的证据**。
   - 工具只能从可信的 `tool.Request.SessionID` 获取会话身份，**禁止模型通过 JSON 参数选择 Session**。
   - T04 可以通过具体存储实现满足接口；T07 负责装配；不得修改 T00 已冻结接口。
   - 如果 T04 的既有 Task / Contract 与该安排冲突，必须先报告并协调，不得单方面修改。
2. **D2（工具划分）**：批准五个独立工具 `calculator` / `mock_search` / `todo_create` / `todo_list` / `todo_complete`。
3. **D3（错误协议）**：领域错误统一 `{"error":{"code","message"}}` 且 `IsError=true`；基础设施错误按现有 `RunError` 契约处理，不得伪装成功，不得泄漏敏感内部信息。
4. **D4（数值与搜索）**：Calculator 使用 `float64`，但必须测试除零、数值溢出、NaN、Inf 与浮点精度边界；Mock Search 使用固定有序数据、大小写不敏感匹配；空查询报参数错误；无结果返回空数组且带 `mock:true`；JSON Schema 必须与 T02 支持范围兼容。
5. **D5（完成语义）**：重复完成同一个已完成 Todo 返回幂等成功；不存在的 Todo ID 返回领域错误。
6. **D6（Design 与流程）**：批准 Design Impact 调整为 `UPDATE`；但 Task 内部的 Design Impact 冲突必须**先由有权限的 TaskBuilder 规范化 `task.md`**，再由 Analyst 更新 Contract 与长期 Design，并按 V3 完成正式批准与证据绑定。在 Task / Contract / Design 一致性完成前**不得启动 Coder**。保持现有 feature 分支与已提交证据，不重建分支；不得扩大到 Agent Loop，不得调用真实付费 LLM API。

兼容性核对（对规范化后的 `task.md` @ `a2c92d6` 复核）：D2—D6 与 `task.md` 的 Goal / Scope / AC-001—AC-009 全部兼容；AC-006 已按 Owner D1 收窄为工具层证据，AC-009 已覆盖 D3 的分级要求；D1 的方法集（`Create` / `List` / `Complete`）与 `task.md` Scope 第 16 条一致。**唯一未决的是 D1 的接口归属**（T03 定义 vs T04 已 APPROVED 的 `session.TodoOperations`），即 D7。

7. **D7（待 Owner 裁决，新增于 2026-10-20）**：见 `Coordination` C1。选项：① 端口归 `session`、方法集按 D1 扩为 `Create` / `List` / `Complete`（需 T04 Contract Revision）；② 保留 `builtin.TodoOperations`，由 T07 适配器实现（ID 生成与状态迁移落到装配层）。Analyst 推荐 ①，理由：同时满足 D1 方法集、D1「T04 拥有生命周期」与 Design §2 分层；② 与「生命周期归 T04」矛盾。
