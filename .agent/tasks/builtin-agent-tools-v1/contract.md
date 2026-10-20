# Technical Contract

## Decision Status

WAITING_FOR_OWNER_DECISION（2026-10-20）。Owner 确认后本节改为 `APPROVED` 并写入 `Owner Decision Record`，随后按 A1（最终 `contract.md`）/ A2（仅 `state.yaml`）两段式提交。

任务：`builtin-agent-tools-v1`（Design Impact `UPDATE` → `docs/design/runtime-architecture.md`）。

待决问题编号 D1—D6 见 `Owner Decision Record`。

## Problem

仓库已冻结契约层（T00）：`tool.Tool`（`Spec()` + `Execute(ctx, tool.Request)`）、`core.ToolResult{CallID,Name,Content,IsError}`、`session.Todo{Todo.Title,Todo.Status}`，但**没有任何具体工具实现**。本任务首次交付可被模型调用的内置工具，因此必须一次固定三件长期事实：

1. 承载具体工具的模块边界与依赖方向（不得写入 `tool/tool.go`，也不得与并行任务 `tool-registry-executor-v1` 争用 `tool/**`）；
2. Todo 的写入路径——在不把可变 `Session` / `Todo` / `session.Store` 交给 Tool 的前提下，仍让 Todo 工具读写会话待办，且不破坏「Session 是 Todo 唯一所有者」（Design INV-005）；
3. 模型可见协议：5 个工具的名称、`Parameters` JSON Schema、`Content` 文本结构与错误表达。

这三者一旦被 Agent Loop / Registry / Session Manager 消费即难以更改，故需 Contract 固化。

## Verified Current Behavior

- VERIFIED：`tool.Tool` 与 `tool.Request`（内嵌 `core.ExecutionRef{SessionID,RunID}` + `core.ToolCall`）已冻结，文件为 `tool/tool.go:17-29`；`tool.Registry` 只有接口，无实现。
- VERIFIED：`core.ToolResult.Validate()` 要求 `CallID` 非空，否则 `KindInvalidPairing`（`core/toolcall.go:19-40`）；`ToolSpec.Parameters` 是 JSON Schema 原文 `json.RawMessage`。
- VERIFIED：`session/session.go` 只有 `TodoStatus`、`Todo{ID,Title,Status}`、`Session{ID,Messages,Todos}`、`Store{Load,Save}`；**无任何实现、无测试、`TodoStatus` 字面量未导出为常量**（T00 `CLEAN-001`）。
- VERIFIED：`git ls-files` 中不存在任何具体工具实现（无 `builtin` / `tools` 包），根模块 `go.mod` 为 `go 1.24` 且零 `require`；`Makefile` 的 `test` 为 `go test ./...`；本机 `go1.24.1 linux/amd64`。
- VERIFIED：`docs/design/runtime-architecture.md` §2 为 `L0 core` + 五个 `L1` 包 + 「L2（未实现）Agent Loop / 装配层」；§8 明确禁止向 Tool 传递可变 `Session` / `Todo` / `Store`；§11 INV-005 为「Session 独占消息与 Todo，Tool 只拿 ID」，且其证据仅为签名核对、运行期证据归属 Session 持久化任务；§12 所有权表无 `builtin/` 相关路径。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`（无已声明 Shared Resource Kind）；`state.yaml` 为 `schema_version: 3`；本任务不占用任何全局资源。
- VERIFIED：并行分支现状（`git ls-remote` + `git log`）——`tool-registry-executor-v1` 与 `develop` 同一 commit（无实现提交）、`llm-openai-client-v1` 只有任务定义提交、`feat/agent-session-manager-v1` 只有任务定义提交。**当前不存在可复用的 `session` 实现或 Todo 存储 API**，故 Todo 写入端口必须在本任务内定义。
- UNKNOWN：T04（`agent-session-manager-v1`）尚无 Contract，其内存 Session Manager 的公开 API 与 Todo 能力形态未知；本任务定义的 Todo 写入端口是否由它直接实现，需由 Agent Loop 装配层或 T04 后续决定。
- UNKNOWN：`tool-registry-executor-v1` 未来在 `tool/**` 新增的公共 API 形态未知；本 Contract 通过「不进入 `tool/` 目录」规避文件级冲突，接口级冲突需该任务启动时复核。

## Recommendation

RECOMMENDATION（待 Owner 确认，非 DECISION）：

### D1 Todo 写入路径：注入「按 SessionID 作用域的最小 Todo 端口」+ 包内参考内存实现

```go
// package builtin
type TodoStore interface {
	Create(ctx context.Context, sessionID, title string) (session.Todo, error)
	List(ctx context.Context, sessionID string) ([]session.Todo, error)
	Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error)
}
```

- 端口由**装配层**注入到 `todo_*` 工具的构造函数；工具每次调用都必须显式传 `req.SessionID`，端口实现按 `(sessionID, id)` 二维键定位，跨会话标识自然不存在。
- 工具持有的是「只操作 Todo、必须显式带 SessionID、按值返回 `session.Todo` 副本」的能力，**不是** `session.Session` / `session.Store`，因此仍无法读写消息历史或整体改写会话。
- 端口与默认实现同包（`builtin/todo.go` + `builtin/todo_memory.go`）；`MemoryTodoStore` 是单进程内互斥保护的会话作用域 Todo 存储，不是 `session.Store` 实现，不持有消息历史。
- 后续 Session Manager（T04）或持久化任务通过装配层适配器实现同一端口即可替换，工具代码不变。

关键取舍：这是对 Design §8「不向 Tool 传递可变 Store」**字面上的最小豁免**（工具确实持有一个可写对象），换取 Todo 工具在 AC-004—AC-006 下的真实可用与并发可验证。替代方案是「工具只产出变更意图、由执行器落库」，但 `core.ToolResult` 只有 `Content` / `IsError` 两个载荷字段，承载意图需要修改 `core` 契约，且执行器属并行任务 `tool-registry-executor-v1`，代价与冲突面都更大。

### D2 工具协议形态：5 个独立工具，Todo 不合并 action

| 工具名 | 职责 |
| --- | --- |
| `calculator` | 四则运算 |
| `mock_search` | 固定数据集检索（Mock） |
| `todo_create` | 创建待办 |
| `todo_list` | 列出本会话待办 |
| `todo_complete` | 完成待办（幂等） |

关键取舍：5 个工具各自 schema 严格（无「可选字段按 action 分支」），模型误用面小、错误可定位；代价是 3 条 Todo 工具描述占用更多 prompt 预算。合并为 1 个 `todo` + `action` 枚举可省 token，但会让「action 与参数不匹配」成为一类额外失败语义，v1 不值得。

### D3 结果与错误文本：统一 JSON

- 成功：业务 JSON 对象（`result` / `results` / `todo` / `todos`）。
- 失败（`IsError=true`）：`{"error":{"code":"<snake_case>","message":"<中文说明>"}}`，**不含**任何 `result` 类字段，避免被误读为有效结果。
- 错误码是包内私有常量构成的模型可见字符串协议，不是跨模块 `ErrorKind`，不构成全局错误码域。

关键取舍：结构化 `code` 让测试断言稳定（不依赖中文子串），也让后续 Loop 可做分类统计；代价是结果文本对模型略啰嗦，且失败/成功两种 JSON 形状不同（由 `IsError` 区分）。

### D4 Calculator 数值规则：`float64` + JSON number + 非有限结果必失败

- 参数为结构化字段 `{"op":"add|sub|mul|div","a":<number>,"b":<number>}`（不采用表达式字符串，免去解析器与歧义）。
- 结果以 JSON number 编码；除不尽样例按 IEEE-754 `float64` 结果判定（测试用相对误差 ≤ 1e-12，不做字符串相等）。
- 除数为 0（含 -0）→ `division_by_zero`；运算结果为 NaN/±Inf（如 `1e308*10`）→ `result_not_finite`，永不返回非有限数值。

### D5 Mock Search：源码内置数据集、大小写不敏感子串、数据集顺序

数据集固定为 `builtin/search_data.go` 内的有序切片（`doc-1`…`doc-8`）；`query` 去首尾空白并小写后，匹配 `title` 或 `summary` 的子串；结果按数据集顺序返回，不排序、不截断、不打分；空查询 → `empty_query`（视为调用错误，而非「无命中」）；无命中 → `IsError=false` 且 `results: []`。

### D6 Design Impact 保持 `UPDATE`

Task `## Design Impact` 已声明 `UPDATE`（新增模块边界 + Todo 写入路径与 INV-005 细化 + 模型可见工具协议）。Task `Relevant Context` 的 Assumption 第 3 条称 Owner 原文标注 `NONE`，与本 Task 自身 Design Impact 章节冲突；按事实来源优先级（Task 的 Goal/Scope/AC 高于 Relevant Context 中的假设），本 Contract 按 `UPDATE` 准备，Owner 只需确认该判定，不需要扩大 Scope。

## Selected Design

等待 Owner 确认 D1—D6。确认后本节替换为最终选择（含 Owner 调整），届时同步写入 `docs/design/runtime-architecture.md`。

## Interfaces and Data

### 包边界与文件清单（新增，11 个源文件）

```text
builtin/                      新 L2 包：内置工具实现（不进入 tool/ 目录，避免与 tool-registry-executor-v1 冲突）
  builtin.go                  包文档、工具名常量、错误码常量、成功/失败 Content 编码、参数解码辅助
  calculator.go               Calculator 工具（解析、校验、计算、格式化）
  search.go                   Mock Search 工具（查询规范化、匹配、结果编码）
  search_data.go              固定预置数据集（有序、纯常量）
  todo.go                     TodoStore 端口、哨兵错误、todo_create / todo_list / todo_complete
  todo_memory.go              MemoryTodoStore（互斥保护 + 全局单调 ID）
  spec_test.go                5 个 Spec 的 schema 合法性、名称唯一、All 聚合、CallID/Name 回填、畸形参数不 panic
  calculator_test.go          AC-001 / AC-002 表驱动用例
  search_test.go              AC-003 确定性、Mock 标识、无命中、空查询
  todo_test.go                AC-004 / AC-005、空 SessionID、幂等完成、store 错误与取消语义
  todo_concurrency_test.go    AC-006 并发创建/完成、双会话隔离、-race
```

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
func NewTodoCreate(store TodoStore) tool.Tool
func NewTodoList(store TodoStore) tool.Tool
func NewTodoComplete(store TodoStore) tool.Tool
// All 返回 5 个工具，顺序固定：calculator, mock_search, todo_create, todo_list, todo_complete。
func All(store TodoStore) []tool.Tool

type TodoStore interface {
	Create(ctx context.Context, sessionID, title string) (session.Todo, error)
	List(ctx context.Context, sessionID string) ([]session.Todo, error)
	Complete(ctx context.Context, sessionID, todoID string) (session.Todo, error)
}

// 端口域内失败哨兵错误：工具据此产生 IsError=true 的可恢复结果。
var (
	ErrSessionRequired = errors.New("builtin: 需要非空 SessionID")
	ErrTodoNotFound    = errors.New("builtin: 该会话下不存在该待办")
	ErrTodoCancelled   = errors.New("builtin: 待办已取消，不能再完成")
)

type MemoryTodoStore struct{ /* 未导出字段 */ }
func NewMemoryTodoStore() *MemoryTodoStore
```

约束：具体工具类型不导出，只经构造函数暴露 `tool.Tool`；`builtin` 不得 import `llm` / `contextmgr` / `runlog` 及任何上层包。

### 模型可见协议（长期）

`calculator` —— `Parameters`：

```json
{"type":"object","properties":{"op":{"type":"string","enum":["add","sub","mul","div"],"description":"运算符"},"a":{"type":"number","description":"第一个操作数"},"b":{"type":"number","description":"第二个操作数"}},"required":["op","a","b"]}
```

结果：`{"operation":"div","result":2.5}`；失败码：`invalid_arguments` / `missing_operator` / `unknown_operator` / `missing_operand` / `division_by_zero` / `result_not_finite`。

`mock_search` —— `Parameters`：

```json
{"type":"object","properties":{"query":{"type":"string","description":"查询关键词，大小写不敏感的子串匹配"}},"required":["query"]}
```

结果：`{"mock":true,"query":"go","results":[{"id":"doc-1","title":"...","summary":"..."}]}`；无命中时 `"results":[]`；失败码：`invalid_arguments` / `empty_query`。

`todo_create` —— `Parameters`：`{"type":"object","properties":{"title":{"type":"string","description":"待办标题，非空"}},"required":["title"]}`；结果：`{"todo":{"id":"todo-1","title":"写测试","status":"pending"}}`；失败码：`invalid_arguments` / `missing_session_id` / `empty_title`。

`todo_list` —— `Parameters`：`{"type":"object","properties":{}}`；结果：`{"todos":[{"id":"todo-1","title":"写测试","status":"pending"}]}`，空会话为 `{"todos":[]}`；失败码：`missing_session_id`。

`todo_complete` —— `Parameters`：`{"type":"object","properties":{"id":{"type":"string","description":"本会话内的待办标识"}},"required":["id"]}`；结果：`{"todo":{"id":"todo-1","title":"写测试","status":"completed"}}`；失败码：`invalid_arguments` / `missing_session_id` / `todo_not_found` / `todo_cancelled`。

通用规则：

- 失败一律 `{"error":{"code":"...","message":"..."}}` + `IsError=true`，`error` 返回 nil。
- `Parameters` 不声明 `additionalProperties`；运行时忽略未知字段（不构成失败）。
- `spec_test.go` 断言 5 个 `Parameters` 均为可解析的 JSON object，`required` 与实现校验一致。
- 工具名与上述字段名是模型可见协议，变更需 Contract Revision。

### 数据约束

- 无数据库、无 Redis、无 MQ、无 migration、无网络、无环境变量依赖；根模块保持零 `require`。
- `MemoryTodoStore` 状态仅存在于进程内，重启即丢失；不实现 `session.Store`（`Load` / `Save` 仍归 Session 任务）。
- Todo ID 规则：`todo-<十进制序号>`，序号在单进程内全局单调递增（跨会话不重复），由存储实现在互斥区内分配。
- `TodoStatus` 只在 `builtin` 内用私有常量 `pending` / `completed` 表示；**不修改 `session/session.go` 补导出常量**（Out of Scope 禁止）。

### 全局资源

无。`resources.reservations` 保持为空；不新增 Registry 文件，不修改 `.agent/workflow.yaml`。

## Business Invariants

- INV-B01（工具身份回填）：每个 `Execute` 返回的 `core.ToolResult` 满足 `CallID == req.Call.ID`、`Name == Spec().Name`（成功与失败一致）；`All()` 中工具名唯一。
- INV-B02（失败分级）：输入校验与领域失败（除零、未知运算符、缺操作数、非法 JSON、空查询、空 `SessionID`、空标题、待办不存在、待办已取消）只能表现为 `IsError=true` + `err==nil`；只有 Todo 存储基础设施错误、`ctx` 已结束、未注入 store 才返回非 nil `error`。
- INV-B03（计算有限性）：Calculator 任何 `IsError=false` 结果的 `result` 都是有限 `float64`；除数为 0 必然失败；成功结果中永不出现 NaN/±Inf。
- INV-B04（搜索确定性）：同一查询在同一数据集下多次调用，`Content` 逐字节一致且顺序等于数据集顺序；无命中时 `IsError=false`、`results` 为空数组；所有成功结果都带 `"mock":true`。
- INV-B05（Todo 会话作用域）：Todo 工具的全部读写作用域由 `req.SessionID` 决定；`SessionID` 为空 → 拒绝且**不调用** `TodoStore`（无写入）；用其他会话的待办标识 → `todo_not_found` 且不改变任何会话数据。
- INV-B06（并发一致性）：`MemoryTodoStore` 下同一会话并发创建 N 条 → 恰好 N 条且 ID 集合大小 = N；同一 ID 并发完成 → 终态唯一 `completed`；不同会话并发互不影响；`go test -race` 无 data race。
- INV-B07（ID 全局唯一）：`MemoryTodoStore` 生成的 ID 在单进程内全局唯一（跨会话不重复），格式 `todo-<正整数>`。
- INV-B08（依赖方向）：`builtin` 只 import 标准库 + `<module>/core` + `<module>/tool` + `<module>/session`；`tool/tool.go`、`session/session.go`、`core/**` 逐字节未变。

## Failure and Consistency Semantics

- 成功的含义：Calculator / Mock Search 的成功表示「本次调用返回了确定结果」，不涉及任何状态；Todo 的成功表示「`MemoryTodoStore` 内存状态已更新」，**不表示**任何持久化发生（进程退出即丢失）。
- 单事实来源：进程内 Todo 只能有一个注入实现；装配层不得同时向工具与 Session Manager 写入同一会话的 Todo。当前任务无装配层，该约束由 Design 记录并交 Agent Loop 任务执行。
- 原子性：`MemoryTodoStore` 的 `Create` / `Complete` 在单一互斥区内完成「定位 + 修改」，不存在部分写入；失败路径（`todo_not_found` / `todo_cancelled` / 空 `SessionID`）在写入前返回，不改变任何数据。
- 幂等：`todo_complete` 对已 `completed` 的待办重复调用返回成功且内容一致（`IsError=false`，状态仍 `completed`），使「模型重试」与「并发重复完成」都不产生错误噪音；`todo_create` **不**幂等（每次调用产生新待办），去重不在本任务范围。
- 并发边界：并发正确性由注入的 `TodoStore` 实现保证（本任务即 `MemoryTodoStore` 的全局互斥锁）；工具本身无共享可变状态，可被并发调用。
- 取消与超时：Calculator / Mock Search 不检查 `ctx`（纯计算、无副作用、无阻塞点）；Todo 工具在调用存储前检查 `ctx.Err()`，非 nil 时返回 `*core.RunError`（`Kind=cancelled` 或 `deadline_exceeded`，`Op="builtin.<tool>.Execute"`）且不写入。存储返回 `context.Canceled` / `context.DeadlineExceeded` 同样映射为 `KindCancelled` / `KindDeadlineExceeded`；其他存储错误包装为 `*core.RunError{Kind: core.KindOf(err), Cause: err}`（非 RunError → `internal`）。上述错误路径均不产生 `IsError=true` 结果。
- 无跨系统一致性：本任务无数据库、无 MQ、无异步复制，不存在最终一致性、重复投递、乱序问题。

## Allowed / Forbidden Changes

- 允许（Coder）：新增 `builtin/` 下上述 11 个文件；修改 `docs/design/runtime-architecture.md` **仅在 Analyst 已按 APPROVED Contract 先行更新完毕之后**（Coder 不得改 Design）。
- 允许：Analyst 在 Owner `APPROVED` 后更新 `docs/design/runtime-architecture.md`（Design Sync Plan 章节）。
- 禁止：修改 `core/**`、`tool/tool.go`、`session/session.go`、`llm/**`、`contextmgr/**`、`runlog/**` 的既有类型、字段、方法与错误种类；给 `session` 补导出 `TodoStatus` 常量。
- 禁止：实现 `tool.Registry`、工具执行器、Agent Loop、LLM Provider 调用、Todo 持久化、`session.Store` 实现、CLI / HTTP / 二进制。
- 禁止：引入第三方依赖（`go.mod` 保持零 `require`）、访问网络、读取环境变量、在测试中使用真实服务。
- 禁止：修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`AGENTS.md`、`Makefile`（`go test ./...` 自动覆盖新包）。
- 禁止：在 `tool/` 目录内新增文件（并行任务 `tool-registry-executor-v1` 的文件所有权）。

## Design Sync Plan（Contract APPROVED 后由 Analyst 执行，先于 Coder）

更新 `docs/design/runtime-architecture.md`：

1. §2 分层：新增 `L2 builtin`（内置工具实现，依赖 `core` / `tool` / `session`），原「L2（未实现）Agent Loop / 装配层」改为 `L3`；依赖规则补一条「L2 只依赖 L0/L1，L1 不得反向依赖 L2」。
2. §3 模块职责：新增 `builtin` 行（三个内置工具 + Mock 数据集 + Todo 写入端口与参考内存实现；不负责 Registry / 调度 / 持久化 / 真实搜索）。
3. §8 会话归属与工具身份：新增「Todo 写入路径」小节——端口语义、为何不是 `session.Store`、空 `SessionID` 与跨会话拒绝点、幂等完成、单一注入实现（单事实来源）约束；把 INV-005 的可执行定义从「Tool 只拿 ID」细化为「Tool 不得持有 `Session` / `session.Store`，只能持有按 SessionID 显式作用域的最小 Todo 端口」。
4. §11 不变量：INV-005 文案按上条细化并注明本任务测试证据；必要时新增 INV-009（内置工具协议稳定：工具名 / schema / Content 结构变更需 Contract Revision）。
5. §12 文件所有权：补入 `builtin/` 下 11 个文件（每个文件单归属 `builtin`）。
6. §14：把「Todo 生命周期 / Todo 写入端口实现」的归属更新为「装配层适配 Session 实现」，并记录 T04 与本任务的衔接方式。

## Verification Requirements

- INV-B01 → `builtin/spec_test.go`：表驱动遍历 `All(store)`，断言 `Spec().Name` 唯一、`Parameters` 为合法 JSON object、`required` 字段存在；用固定 `CallID` 调用一次成功与一次失败，断言 `ToolResult.CallID` / `Name` 回填正确且 `Validate()` 通过。
- INV-B02 / AC-002 → `builtin/calculator_test.go` 表驱动：`{"op":"div","a":1,"b":0}`、`{"op":"pow",...}`、缺 `a`、`a:"2"`（字符串）、`a:1e309`、`Arguments` 为空 / `not-json` / `[]`；每条断言 `IsError==true`、`err==nil`、`Content` 的 `error.code` 等于期望码、`Content` 不含 `"result"` 键；用 `defer recover()` 包裹断言不 panic。
- INV-B03 / AC-001 → 表驱动：整数、负数、`0.5`/`2.5` 精确小数、`1/3` 除不尽、`1e308*10`（`result_not_finite`）、`-0.0` 除数；成功用例从 `Content` 解析 `result` 并与期望值比较（精确值用 `==`，除不尽用相对误差 ≤ 1e-12）。
- INV-B04 / AC-003 → 同一查询连续调用 ≥ 2 次断言 `Content` 字符串完全相等；断言含 `"mock":true`；大小写不同查询（`Go` / `go`）结果相同；无命中断言 `results` 为空数组且 `IsError==false`；空查询断言 `empty_query`。
- INV-B05 / AC-004 / AC-005 → `builtin/todo_test.go`：空 `SessionID` 时用 spy `TodoStore` 断言零调用并 `IsError==true`（`missing_session_id`）；A 建 2 条、B 列表为空；B 用 A 的 ID 完成 → `todo_not_found`，随后 A 的列表逐字段与操作前相等；不存在 ID 完成 → `todo_not_found` 且列表不变；重复完成 → 成功且终态 `completed`；注入返回 `ErrTodoCancelled` 的 fake → `todo_cancelled` 且无写入；注入返回普通 error 的 fake → `err != nil` 且 `KindOf(err)==KindInternal`；`ctx` 已取消 → `err != nil` 且 `KindOf(err)==KindCancelled` 且 store 未被调用；`All(nil)` 的 Todo 工具执行 → `err != nil`（`KindInternal`），不 panic。
- INV-B06 / INV-B07 / AC-006 → `builtin/todo_concurrency_test.go`：同一会话 `sync.WaitGroup` 并发创建 N=50，断言列表 50 条且 ID 集合大小 50；并发完成同一 ID → 全部成功或仅返回终态一致，最终状态唯一 `completed`；两会话并发创建断言各自集合互不包含对方 ID；`go test -race -count=1 ./...` 通过。
- INV-B08 / AC-007 → `go build ./...`、`go vet ./...`、`make test` 退出码 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 核对 `builtin` 行只含标准库 + `core`/`tool`/`session`；`git diff --stat <baseline>..HEAD -- core tool session llm contextmgr runlog` 为空。
- AC-008 → Analyst 先更新 Design，Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对；§12 与 `git ls-files` 差集为空；`builtin` 不出现在 `tool/` 目录。

环境：Linux + Go 1.24.x + GNU Make；无 MySQL / Redis / MQ / 外网 / API Key。

## Open Risks

- **Todo 双实现并存**：`MemoryTodoStore` 与 T04 Session Manager 的 Todo 状态可能成为两份事实来源。本 Contract 以「装配层单一注入」的装配约束缓解，但该约束靠 Design + Review 保证，无法机械验证；T04 启动时需复核并可能触发 Revision。
- **`builtin` 包承载存储实现**：端口与其参考实现同包，长期看 Session 职责可能更适合承载；代价已接受（避免新增第二个模块边界与 Scope 扩张）。
- **全局单锁**：`MemoryTodoStore` 用一把互斥锁覆盖所有会话，跨会话并发无并行收益；v1 非性能目标，后续由 Session Manager 细化。
- **`float64` 精度**：`0.1+0.2 != 0.3`，模型可能对结果感到意外；协议已固定用 JSON number，若 Owner 要求十进制精确算术需 Revision。
- **模型可见协议不可逆**：工具名、字段名、错误码一旦进入 prompt 就难以更改；本 Contract 已尽量保守（不引入可选参数、不引入分页/排序参数）。
- **`empty_query` / `empty_title` 为 Analyst 判定**：Task 未要求，属主动收紧；Owner 若倾向「空查询返回空结果」需在确认 D5 时指出。
- **`ctx` 语义**：本任务定义了工具层最小响应规则，但 Loop 如何消费（是否先取消）仍归 Agent Loop 任务，运行期证据缺失。

## Owner Decision Record

等待 Owner 确认。需 Owner 明确回答：

- **D1**：Todo 写入路径是否采用「注入按 SessionID 作用域的最小 `TodoStore` 端口 + 包内 `MemoryTodoStore`」？（这构成对 Design §8「不向 Tool 传可变 Store」的最小字面豁免，需 Owner 明确认可该豁免。）
- **D2**：模型可见工具划分——5 个独立工具（`calculator` / `mock_search` / `todo_create` / `todo_list` / `todo_complete`），还是 Todo 合并为 1 个带 `action` 的工具？
- **D3**：成功与失败 `Content` 是否统一为 JSON（失败为 `{"error":{"code","message"}}`）？
- **D4**：Calculator 精度规则（`float64` + JSON number + 非有限结果必失败）与 Mock Search 规则（源码内置数据集、大小写不敏感子串、数据集顺序、空查询报错）是否可接受？
- **D5**：`todo_complete` 对已完成的待办重复调用是否按幂等成功处理（而非返回错误）？
- **D6**：确认 Design Impact = `UPDATE`（Task `## Design Impact` 已声明）；确认后 Analyst 先更新 `docs/design/runtime-architecture.md`，再交 Coder。
