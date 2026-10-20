# Technical Contract

## Decision Status

`OWNER_DIRECTION_APPROVED`（2026-10-20，D1—D6）；**正式 `APPROVED` 尚未成立**。

阻塞条件（Owner D6 明确）：Task `## Design Impact` 与 `Relevant Context` Assumption 的冲突必须**先由 TaskBuilder 规范化 `task.md`**，之后 Analyst 才能把本 Contract 的 `Selected Design` / `Owner Decision Record` 定稿、更新 `docs/design/runtime-architecture.md`，并按 V3 完成 `APPROVED` + Evidence Binding（A1 `contract.md` → A2 仅 `state.yaml`）。在此之前不得启动 Coder。

`state.contract.status` 保持 `PENDING`（schema 无「方向已批准」枚举，不提前写入 `APPROVED`）。

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
- VERIFIED：T02（`llm-openai-client-v1` task.md）把 `core.ToolSpec.Parameters` **原样透传** 为 `tools[].function.parameters`，不做 schema 改写，也未声明额外限制或转换；因此「与 T02 支持范围兼容」= 只使用 OpenAI-compatible function parameters 的通用子集，且不依赖 T02 做任何 schema 处理。
- VERIFIED：T04（`feat/agent-session-manager-v1` task.md）Scope 含「保存与读取消息历史、摘要和 Todo 状态」，但**未声明**实现 `builtin.TodoOperations`，其 §3/§8/§12 的 Design Impact 与本任务重叠（见 `Coordination` 章节）。
- VERIFIED：`tool-registry-executor-v1` 与 `develop` 同 commit（无实现提交）、T02 分支只有任务定义提交 → 当前无 `tool/**` 或 `session/**` 实现代码可冲突。
- VERIFIED：`.agent/workflow.yaml` 为 `resources: {}`；`state.yaml` 为 `schema_version: 3`；本任务不占用任何全局资源。
- UNKNOWN：T04 的 Contract 尚未存在，其 Todo 能力 API 与 `TodoOperations` 的对齐方式未知；T07 装配层任务也未创建。

## Recommendation（已被 Owner 调整）

RECOMMENDATION 已由 Owner 有条件批准并调整，采纳版本见 `Selected Design`。相对原推荐的调整：

| 项 | 原推荐 | Owner 决定 |
| --- | --- | --- |
| Todo 端口命名 | `TodoStore` | `TodoOperations`（Create / List / Complete） |
| 生产内存实现 | 包内提供 `MemoryTodoStore` | **不提供**；真实 Todo 数据与生命周期归 T04，T03 只用测试专用 Fake |
| 已取消状态 | 额外 `todo_cancelled` 领域错误 | 不引入；状态机归 T04，T03 只映射「不存在」 |
| 并发证据 | 由 `MemoryTodoStore` 证明会话级并发 | 工具层证据（无共享可变状态 + `-race`），真实会话并发与隔离归 T04/T07 |

关键取舍（保持）：Tool 持有受限可写端口是对 Design §8「不向 Tool 传可变 Store」的最小字面豁免；换取 Todo 工具可用，同时用「无生产实现」确保 Tool 不成为 Todo 的第二事实来源。

## Selected Design

依据 Owner D1—D6（2026-10-20）。

### 1. 模块边界

新增 L2 包 `builtin/`（不进入 `tool/` 目录）。依赖只允许：标准库 + `<module>/core` + `<module>/tool` + `<module>/session`；不得 import `llm` / `contextmgr` / `runlog` 及任何上层包。

### 2. Todo 写入路径（D1）

```go
// package builtin
type TodoOperations interface {
	Create(ctx context.Context, sessionID, title string) (session.Todo, error)
	List(ctx context.Context, sessionID string) ([]session.Todo, error)
	Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error)
}

var ErrTodoNotFound = errors.New("builtin: 该会话下不存在该待办")
```

- **T03 负责**：五个工具的实现与 `TodoOperations` 接口定义（含 `ErrTodoNotFound` 哨兵）。
- **T03 不提供**任何生产存储实现（无 `MemoryTodoStore`、无 `session.Store` 实现、无文件/DB/Redis）。真实 Todo 数据与生命周期由 T04 Session Manager 拥有与实现。
- 端口由装配层（T07）注入到 `todo_*` 工具构造函数；工具每次调用都显式传 `req.SessionID`。
- **会话身份唯一来源**：`tool.Request.SessionID`（可信、由运行时注入）。工具从不由 `Arguments` 读取会话；参数 schema 不含任何 session 字段，且 `Arguments` 中出现 `session_id` / `sessionId` / `session` 键一律 `invalid_arguments`（显式拒绝而非静默忽略）。
- T04 可用具体存储直接实现 `TodoOperations`，或由 T07 写适配器；二者均**不得修改 T00 已冻结接口**（`core/**`、`tool/tool.go`、`session/session.go`）。
- 若 T04 的 Task / Contract 与本安排冲突，先报告并协调，不得单方面修改（当前发现的协调项见 `Coordination`）。

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
  todo.go                    TodoOperations 端口、ErrTodoNotFound、todo_create / todo_list / todo_complete
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

具体工具类型不导出，只经构造函数暴露 `tool.Tool`。端口与哨兵定义在本包；实现方（T04 或 T07 适配器）单向 import 本包，本包绝不 import 实现方——这是 Design 中唯一允许的「实现方 → 端口」额外依赖边，不构成循环。

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

对 T04（`feat/agent-session-manager-v1`，当前仅有 task.md）的核对结果——**无硬冲突，但存在两处待协调缺口**：

1. T04 的 Scope 与 AC 未包含「实现 `builtin.TodoOperations`」。Owner 允许「可以通过具体存储实现满足接口」，因此这是可选路径而非 T04 现有义务；若 T04 不实现，缺口由 T07 适配器承担。→ 需 Owner / TaskBuilder 决定是否在 T04 task.md 或 T07 任务中显式写入该衔接。
2. T04 的 `Design Impact: UPDATE` 同样要改 §3 / §8 / §11 / §12，与本任务的 Design 更新重叠（同章节并发修改有冲突与语义漂移风险）。→ 建议次序：T03 先落「L2 `builtin` 模块 + 端口语义 + INV-005 细化」；T04 在其后扩展 `session` 实现职责。最终 merge 顺序由 Owner 决定。
3. T04 的 AC-003 / AC-005（并发写入、过期与在途 Turn、`-race`）正好承接本任务从 AC-006 移出的运行期证据，无冲突。

本任务 `task.md` 需 TaskBuilder 规范化的条目（Analyst 建议，非决定）：

- **N1**：消除 `## Design Impact`（UPDATE）与 `Relevant Context` Assumption 第 3 条（原 `NONE`）的冲突。
- **N2**：Scope 的「Todo 写入能力注入方式」改为 `TodoOperations` 端口 + 「不提供生产内存实现」+ 测试专用 Fake。
- **N3**：AC-006 改为工具层证据（并发调用无共享可变状态、`-race` 干净、Fake 下 N 条不丢不重），并把真实会话并发/隔离证据指向 T04 / T07。
- **N4**：AC-005 措辞限定为「工具按 `req.SessionID` 作用域传递且拒绝跨会话标识」，不表述为「真实 Session 隔离已验证」。
- **N5**：`Relevant Context` 补「无生产 Todo 存储」「T04 拥有 Todo 生命周期」「T07 装配」事实，并标注 `TodoOperations` 名称。
- **N6**：Out of Scope 增补「任何生产 Todo 存储实现（不止 `session.Store`）」。

## Design Sync Plan（`APPROVED` 后由 Analyst 执行，先于 Coder）

1. §2：新增 `L2 builtin`（依赖 `core` / `tool` / `session`），原「L2（未实现）Agent Loop / 装配层」改为 `L3`；补「L1 不得反向依赖 L2」与「实现方 → 端口」单向边例外。
2. §3：新增 `builtin` 模块行（三个内置工具 + Mock 数据集 + `TodoOperations` 端口；不负责 Registry / 调度 / 持久化 / Todo 生命周期）。
3. §8：新增「Todo 写入路径」——端口语义、为何不是 `session.Store`、会话身份只来自 `tool.Request.SessionID`、空 `SessionID` 与跨会话拒绝点、幂等完成、单一注入实现；把 INV-005 的可执行定义细化为「Tool 不得持有 `Session` / `session.Store`，只能持有按 `SessionID` 显式作用域的最小 Todo 端口」。真实 Todo 数据归 Session Manager。
4. §11：INV-005 文案按上条细化并注明证据归属（签名 + 工具层测试；运行期证据归 T04/T07）；新增 INV-009「内置工具协议稳定（工具名 / schema 子集 / `Content` 结构变更需 Contract Revision）」。
5. §12：补入 `builtin/` 下 10 个文件（单归属）。
6. §14：更新 Todo 生命周期 / Todo 写入实现的归属（T04 + T07），记录 T03 证据边界。

## Verification Requirements

- INV-B01 / AC-007 → `spec_test.go`：遍历 `All(fake)`，断言 `Spec().Name` 唯一、`Parameters` 合法 JSON object、`required` 与实现校验一致；以固定 `CallID` 各执行一次成功与失败，断言回填正确且 `Validate()` 通过。
- INV-B09 → `spec_test.go` 遍历 schema JSON 全部键，断言键集合 ⊆ {`type`,`properties`,`description`,`enum`,`required`}（递归到 property 内层）。
- INV-B02 / AC-002 → `calculator_test.go` 表驱动：`{"op":"div","b":0}`、`b:-0.0`、`op:"pow"`、缺 `a`、`a:"2"`、`a:1e309`、`Arguments` 为空 / `not-json` / `[]`；每条断言 `IsError==true`、`err==nil`、`error.code` 等于期望码、`Content` 不含 `"result"` 键、不 panic（`defer recover()`）。
- INV-B03 / AC-001 / D4 → `calculator_test.go`：整数、负数、`0.5`/`2.5` 精确小数、`1/3` 除不尽（相对误差 ≤ 1e-12）、`1e308*10` 与 `1e308/1e-308`（`result_not_finite`）、`1e-308/1e308`（下溢为 0，成功且有限）、`0.1+0.2`（断言有限且与 `0.30000000000000004` 数值一致，不用字符串相等）、`NaN` / `Infinity` 字面量（非法 JSON → `invalid_arguments`）；并断言所有成功 `result` 满足 `!IsNaN && !IsInf`。
- INV-B04 / AC-003 → `search_test.go`：同查询连续 ≥ 2 次 `Content` 完全相等；含 `"mock":true`；`Go` 与 `go` 结果一致；无命中 `results` 为空数组且 `IsError==false`；空查询/纯空白 `empty_query`。
- INV-B05 / AC-004 / AC-005 → `todo_test.go`：空 `SessionID` 用 spy Fake 断言零调用 + `missing_session_id`；`Arguments` 含 `session_id` / `sessionId` / `session` → 拒绝且零调用；A 建 2 条、B 列表为空；B 用 A 的 ID 完成 → `todo_not_found` 且 A 的数据逐字段不变；不存在 ID → `todo_not_found` 且列表不变；重复完成 → 成功且 `completed`；Fake 返回普通 error → `err != nil` 且 `KindOf(err)==KindInternal`；Fake 返回 `context.Canceled` → `KindCancelled`；`ctx` 预取消 → `err != nil`、`KindCancelled`、零调用；`All(nil)` 的 Todo 工具 → `err != nil`（`KindInternal`）、不 panic。
- INV-B06 / AC-006（待 TaskBuilder N3 规范化） → `todo_concurrency_test.go`：同一会话并发创建 N=50（并发安全 Fake），断言列表 50 条、ID 集合大小 50；并发完成同一 ID 断言终态唯一 `completed`；两会话并发创建断言互不包含对方 ID；`go test -race -count=1 ./...` 通过。测试注释与 `Open Risks` 必须写明该证据不覆盖真实 Session 隔离。
- INV-B07 → 代码核对：`git grep -n "type .*Store struct" -- builtin/` 无命中；生产文件不含 ID 生成逻辑；Fake 仅在 `_test.go`。
- INV-B08 / AC-007 → `go build ./...`、`go vet ./...`、`make test` 退出码 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 中 `builtin` 行只含标准库 + `core` / `tool` / `session`；`git diff --stat <baseline>..HEAD -- core tool session llm contextmgr runlog` 为空。
- AC-008 → Analyst 先更新 Design，Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对；§12 与 `git ls-files` 双向差集为空。

环境：Linux + Go 1.24.x + GNU Make；无 MySQL / Redis / MQ / 外网 / API Key / 真实 LLM 调用。

## Open Risks

- **证据边界易被夸大**：无生产存储意味着「会话隔离」在 T03 只有 Fake 证据；若 Cleaner / OwnerGate 不严格区分，会把 Fake 当运行期证据。已写入 INV-B06 / Design Sync Plan / Verification 注释要求。
- **T04 尚未承接端口**：若 T04 Contract 定义自己的 Todo API 且不实现 `TodoOperations`，T07 必须写适配器，否则 Todo 工具无法在真实装配中工作；需 Owner 在 T04 启动前协调。
- **Design 章节并发修改**：T03 与 T04 都改 §3 / §8 / §11 / §12，merge 顺序与章节归属需 Owner 决定，否则语义漂移。
- **JSON Schema 子集**：T02 目前原样透传，未见更窄限制；若 T02 实现表明 `enum` 不被支持，`calculator.op` 需退化为纯 `description` 文本（Revision）。
- **`float64` 精度**：`0.1+0.2 != 0.3`，模型可能意外；协议已固定，若要求十进制精确算术需 Revision。
- **端口依赖方向例外**：实现方 import `builtin` 形成 L2→L2 单向边，需在 Design 明示，避免后续任务误判为分层违规。
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

兼容性核对：D1—D5 与 Task AC-001 / AC-002 / AC-003 / AC-004 / AC-005 / AC-007 兼容；与 AC-006 存在**证据口径差异**（Task 现文要求「同一 Session 并发创建后 N 条、`-race` 无数据竞争」由本任务证明，Owner D1 要求真实隔离证据归 T04/T07），需按 N3 由 TaskBuilder 规范化后才能 `APPROVED`。D6 的流程要求与 Role 边界一致：Analyst 不修改 `task.md`。
