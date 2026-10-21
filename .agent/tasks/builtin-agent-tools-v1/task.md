# Task: 内置 Agent 工具（Calculator / Mock Search / Todo，v1）

## Goal

交付五个可被 Agent 以统一 `tool.Tool` 契约直接调用的内置工具——`calculator`、`mock_search`、`todo_create`、`todo_list`、`todo_complete`：行为确定、错误可预期、结果可被模型直接消费。Todo 工具的会话身份只取自可信的 `tool.Request.SessionID`，工具自身不持有任何 Todo 状态，全部读写经装配层注入的 `TodoOperations` 端口按会话作用域完成；工具层并发调用无共享可变状态、`-race` 干净。真实的会话隔离、跨会话不可写、Todo 生命周期与持久化并发**不由本任务证明**，归属 Session Manager（T04）与装配层（T07）。

## Scope

- 新增承载内置工具的实现包（Owner 方向批准的包路径为 `builtin/`，包名与最终文件清单由 APPROVED Contract 固化）。不得把具体工具实现写入 `tool` 包已有的契约文件 `tool/tool.go`——T00 长期 Design §3 明确 `tool` 模块不负责具体工具实现。
- 按 Owner D2 提供 **5 个独立工具**（不采用「一个工具带 action 参数」）：
  - `calculator`：`op` ∈ `add` / `sub` / `mul` / `div`，两个 `float64` 操作数；成功返回可解析的数值结果。
  - `mock_search`：基于源码内置的有序预置数据返回结果，不访问网络、不依赖外部服务。
  - `todo_create` / `todo_list` / `todo_complete`：三个工具共用一个按会话作用域的 `TodoOperations` 端口。
- Calculator 的输入校验与失败行为：除零（含 `-0`）、未知运算符、缺少操作数、操作数非数值（含非数字字符串、`NaN`/`Inf` 字面量等非法 JSON）、结果非有限（乘法/除法溢出产生 `±Inf` 或 `NaN`）、参数不是合法 JSON，均给出可恢复的失败结果（`core.ToolResult.IsError=true` + 领域错误码），`Execute` 的 `error` 为 nil，不 panic，也不返回任何看似有效的数值结果。
- Mock Search 的确定性：固定有序数据集、`query` 去首尾空白并小写后按子串匹配，结果顺序等于数据集顺序（不排序、不打分、不截断）；所有成功结果明确标识为 Mock；无命中返回 `IsError=false` 的空结果集合；空查询 / 纯空白查询按参数错误拒绝。
- Todo 的写入能力注入方式（Owner D1）：本任务定义 `TodoOperations` 端口（`Create` / `List` / `Complete`）与 `ErrTodoNotFound` 哨兵，由装配层（T07）在构造时注入实现；端口实现方单向 import 本包，本包绝不 import 实现方。
- Todo 的会话作用域（工具层）：会话身份**只**取自 `tool.Request.SessionID`，每次调用都向端口显式传递；`SessionID` 为空时拒绝执行且**不调用端口**、不产生写入；`Arguments` 中出现 `session_id` / `sessionId` / `session` 等会话字段一律拒绝（不允许模型通过参数选择会话）；端口以 `ErrTodoNotFound` 表示「本会话不存在该待办」（含其他会话的标识）时，工具映射为 `todo_not_found` 领域错误且无副作用；工具不缓存、不保留 Todo 副本、不生成 ID。
- Todo 的并发特性（工具层）：工具实例不含共享可变状态，可被并发 `Execute`；在并发安全端口实现下，同一会话并发创建 N 条后列表恰为 N 条且标识互不重复，并发完成同一标识终态唯一；`go test -race` 无数据竞争。**本任务不声明真实会话隔离或持久化并发已成立。**
- **不提供任何生产 Todo 存储实现**：无 `MemoryTodoStore`、无 `session.Store` 实现、无文件 / DB / Redis 存储，生产代码不含 Todo ID 生成逻辑。测试专用 Fake 只存在于 `_test.go`，且不得作为「真实 Session 隔离已验证」的证据。
- 失败分级（Owner D3）：领域失败（除零、未知运算符、缺操作数、非法 JSON、空查询、空 `SessionID`、空标题、待办不存在、参数中含会话字段）只表现为 `IsError=true` + `error` 为 nil；基础设施失败（端口返回非领域错误、`ctx` 已结束、端口未注入）返回非 nil 的 `*core.RunError`，不得降级为 `IsError=true` 伪装成功，也不得把路径、堆栈、连接串、凭据等内部细节写入 `Content`。
- 五个工具均实现 `tool.Tool`（`Spec()` + `Execute(ctx, tool.Request) (core.ToolResult, error)`），可通过公共构造函数在不装配 Agent Loop、不启动 Provider 的情况下被构造与调用；工具不得 import 上层运行时包。
- 工具名、描述、`Parameters` JSON Schema 与 `Content` 结构属于模型可见的长期协议，在本任务内固化并纳入 APPROVED Contract。
- 测试：五个工具的正常、失败与边界用例；工具层并发与依赖方向用例；测试不访问网络、不读取环境变量、不依赖真实服务。
- 更新 `docs/design/runtime-architecture.md`：新增包的位置与职责、依赖方向、Todo 写入路径与既有 INV-005（会话归属）的关系、证据归属边界；§12 文件所有权表补入本任务新增文件，并在 Cleaner 审查时与 `git ls-files` 双向差集为空（每个源文件只归属一个模块）。

## Out of Scope

- `tool.Registry` 实现、工具查找 / 调度 / 执行器、同 step 多工具并发调度与失败回填顺序（属 `tool-registry-executor-v1` / Agent Loop 任务）。
- Agent Loop 多轮调度、消息历史组装、预算与取消在 Loop 中的落地。
- LLM Provider 调用或任何真实模型请求；工具结果如何进入 prompt 的措辞设计。
- 真实搜索引擎、HTTP 抓取、外部网络、第三方 SDK（根模块保持零 `require`）。
- **任何生产 Todo 存储实现**——包括 `MemoryTodoStore`、`session.Store` 实现、文件 / DB / Redis 持久化、migration、跨进程共享、重启恢复、多实例一致性。
- Todo 的 ID 生成规则、状态机扩展（如 `cancelled` 状态迁移）与生命周期管理；内存 Session Manager 本身（属 T04）。
- 真实 Session 隔离 / 跨会话不可写 / Todo 生命周期与持久化并发的运行期证明（属 T04 Session Manager 与 T07 装配）；本任务不实现、也不声称已验证。
- 修改既有公共契约：`core/**`、`tool/tool.go`、`session/session.go`、`llm/**`、`contextmgr/**`、`runlog/**` 的既有类型、字段、方法与错误种类；为 `session` 补导出 `TodoStatus` 常量；确需变更时走 Contract Revision 回 Analyst / Owner。
- 沙箱、权限模型、审计、限流、工具级超时与重试策略（本任务只固定工具层对 `ctx` 的最小响应）。
- CLI / HTTP Server / 可运行二进制 / Docker。
- 修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`AGENTS.md`、`Makefile`、`go.mod`。
- 全局资源：本任务不占用任何 Shared Resource（无 migration、无错误码域、不新增 Registry 文件）；`resources.reservations` 保持为空。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`

理由（Owner D6 已批准调整为 `UPDATE`）：新增 L2 `builtin` 包会改变 §2 分层与 §3 模块职责（新增一条模块边界，并新增「实现方 → 端口」单向依赖例外）；Todo 工具需要一条不违反 T00 §8「不向 Tool 传递可变 `Session` / `Todo` / `Store`」的写入路径，会细化 INV-005（会话归属）的可执行定义并新增长期公开接口（`TodoOperations`）；五个工具名、JSON Schema 与 `Content` 结构成为模型可见的长期协议。因此不属于局部实现。

## Acceptance Criteria

- [ ] AC-001：给定一个 `calculator` 工具实例，当以合法操作数分别调用 `add` / `sub` / `mul` / `div` 时，均返回 `IsError=false` 的结果，且结果内容可解析为与数学答案数值相等的值（覆盖整数、负数、可精确表示的小数、除不尽样例与浮点精度边界如 `0.1+0.2`；除不尽样例按 Contract 固化的相对误差判定，不用字符串相等）；所有成功结果的数值均为有限值。
- [ ] AC-002：给定 `calculator`，当输入除数为 `0` / `-0`、未知运算符、缺少操作数、操作数非数值（含 `NaN` / `Infinity` 字面量）、结果非有限（如 `1e308*10`）、参数不是合法 JSON 时，均返回 `IsError=true` 的结果并给出可定位到具体原因的错误码，`Execute` 返回的 `error` 为 nil，结果中不存在任何被误判为有效数值的字段，进程不 panic。
- [ ] AC-003：给定 `mock_search` 与固定预置数据，当对同一查询连续调用多次时，返回结果逐字节一致（含顺序与大小写不同的同一查询）；结果明确包含 Mock 标识；当查询无命中时返回 `IsError=false` 的空结果集合且同样带 Mock 标识；当查询为空或纯空白时返回 `IsError=true` 的参数错误。
- [ ] AC-004：给定一个非空 `SessionID` 与一个记录调用参数的端口实现，当通过 `todo_create` 创建待办、`todo_list` 查询列表、`todo_complete` 完成某个待办时：创建返回唯一标识，查询能列出该会话的全部待办及其状态，完成后该待办状态为 `completed`，且每次调用向端口传递的 `SessionID` 与 `req.SessionID` 完全一致；当完成一个不存在的待办标识时返回 `IsError=true` + `todo_not_found` 且不产生副作用；当 `SessionID` 为空时拒绝执行（`missing_session_id`）且端口调用次数为 0。
- [ ] AC-005：给定 Session A 与 Session B 的两次工具调用，当 A 创建若干待办后，B 的 `todo_list` 只按 B 的 `SessionID` 读取（端口对其返回空集合）；当 B 使用 A 的待办标识执行 `todo_complete`、端口以 `ErrTodoNotFound` 表示该标识不属于 B 时，调用返回 `IsError=true` + `todo_not_found`，且工具不产生任何写入；当 `Arguments` 中出现 `session_id` / `sessionId` / `session` 时，调用被拒绝且端口调用次数为 0。本条只证明工具「按 `req.SessionID` 作用域传递并正确映射端口失败」，不证明真实 Session 隔离（见 AC-006 与本文件的证据边界）。
- [ ] AC-006：给定同一 `SessionID` 与一个并发安全的端口实现，当用同一工具实例并发创建 N 条待办（`N=50`）后查询时，返回恰好 N 条且标识集合大小 = N；并发完成同一标识时最终状态唯一为 `completed` 且无部分写入；两个会话并发操作时任一列表都不含对方标识（在该端口实现语义下）；工具实例无共享可变状态，`go test -race -count=1 ./...` 通过。本条是**工具层证据**，不声称真实会话隔离、跨会话不可写或持久化并发已成立。
- [ ] AC-007：给定一份干净检出，当不启动 Agent Loop、不连接网络与外部服务、仅通过公共构造函数与 `tool.Tool` 接口调用五个工具时，行为满足 AC-001 ~ AC-006；`go build ./...`、`go vet ./...`、`make test` 退出码 0；新增包不 import 上层运行时包与端口实现方包，`go list` 核对无反向依赖，`core/**`、`tool/tool.go`、`session/session.go` 逐字节未变。
- [ ] AC-008：给定更新后的 `docs/design/runtime-architecture.md`，当与 APPROVED Contract 及最终实现逐项核对时，新增包的职责、依赖方向、Todo 写入路径与 INV-005 的解释、证据归属边界四者一致；§12 所有权表与 `git ls-files` 的双向差集为空（每个源文件只归属一个模块）。
- [ ] AC-009：给定失败分级，当向端口注入返回普通错误的实现、注入 `context.Canceled`、或使用已取消的 `ctx`、或以未注入端口（`All(nil)`）构造 Todo 工具时，`Execute` 分别返回非 nil 的 `*core.RunError`（kind 为 `internal` / `cancelled` / `cancelled` / `internal`），`Content` 不含内部错误细节、路径或凭据，且端口调用次数为 0（`ctx` 已取消与端口未注入两种情形）。

## Relevant Context

- 已核实事实：T00 `runtime-core-contracts-v1` 已 `CLEAN` / Owner `ACCEPTED` / 交付 `PASS`；`tool.Tool` 冻结为 `Spec() core.ToolSpec` 与 `Execute(ctx context.Context, req Request) (core.ToolResult, error)`；`tool.Request` 内嵌 `core.ExecutionRef{SessionID, RunID}` 与 `core.ToolCall`（`tool/tool.go`）。仓库中不存在任何具体工具实现，也不存在 `builtin/` 包。
- 已核实事实：`core.ToolResult{CallID, Name, Content, IsError}`；`core.ToolResult.Validate()` 要求 `CallID` 非空；可恢复失败以 `IsError=true` 表达且 `error` 为 nil，只有不可恢复失败才返回 `error`（T00 Contract §5、§7）。
- 已核实事实：`session` 包已有 `Todo{ID, Title, Status}`、`TodoStatus`（`pending` / `in_progress` / `completed` / `cancelled`）与 `Store{Load, Save}` 接口，但无任何实现；`TodoStatus` 字面量未导出常量（T00 `CLEAN-001`），构造状态值需字符串转换或在 `builtin` 内以私有常量表示。
- 已核实事实：T00 Design §3 明确 `tool` 模块「不负责具体工具实现」；§8 规定「不向 Tool 传递可变的 `Session`、`Todo` 或 `Store`」「会话持久化是后续任务，本层只固定 `Store` 抽象，不实现任何存储」；§11 INV-005 目前只有签名核对证据；§14 把「`session.Store` 的内存与持久化实现、Todo 生命周期」归属 Session 持久化任务（T04）；§12 所有权表无 `builtin/` 路径。
- 已核实事实（Owner 决定，2026-10-20，D1—D6）：Design Impact 调整为 `UPDATE`；采用 `TodoOperations`（`Create` / `List` / `Complete`）受限端口；T03 不提供生产内存实现、只用测试专用 Fake；真实 Todo 数据与生命周期归 T04，T07 负责装配；会话身份只来自 `tool.Request.SessionID`，禁止模型经 JSON 参数选择 Session；五个独立工具；领域错误统一 `{"error":{"code","message"}}` + `IsError=true`，基础设施错误按 `RunError` 处理且不泄漏内部信息；Calculator 用 `float64` 但必须测试除零 / 溢出 / `NaN` / `Inf` / 精度边界；Mock Search 固定有序数据、大小写不敏感、空查询为参数错误、无命中空数组；重复完成幂等、不存在返回领域错误；Task 内部 Design Impact 冲突必须先由 TaskBuilder 规范化。
- 已核实事实：Contract 当前状态为 `OWNER_DIRECTION_APPROVED`（非 `APPROVED`），已固定 `builtin/` 包路径、10 个文件清单、公共符号（含 `All(ops)` 聚合与工具名常量）、5 个 `Parameters` schema、`Content` 结构与 INV-B01 ~ INV-B09；Coder 只能在其 `APPROVED` 后启动。
- 已核实事实：根 Module 为 `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零第三方依赖）；`Makefile` 的 `test` 等价 `go test ./...`；仓库根无 `builtin` / `tools` 之类实现包，也无 `tool.Registry` 实现。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`（无已声明 Shared Resource Kind）；`state.yaml` 采用 `schema_version: 3` 严格解码（未知字段直接 ERROR）。
- 已核实事实（T04 衔接核对）：`origin/feat/agent-session-manager-v1` 仅有一个任务定义提交，其 `task.md` 的 Scope 含「保存与读取消息历史、摘要和 Todo 状态」、AC-001 / AC-002 涉及 Todo 状态读写，但**未声明**实现 `builtin.TodoOperations`，也未定义 Todo 创建 / 列表 / 完成的对外 API；其 `Design Impact: UPDATE` 同样要改 §3 / §8 / §11 / §12。
- 已核实事实：本任务 5 个工具均由 `All(ops)` 聚合暴露（顺序固定），便于后续 `tool-registry-executor-v1` 注册；本任务不实现 Registry。
- Assumption：包名、文件名、错误码字面量、`Content` 字段与工具描述细节以 APPROVED Contract 为准，本文件不复述（避免两处分叉）。
- Assumption：本任务不启用 Deliverer（交付物是库内实现，无独立可运行产物，Owner 未声明 Milestone）；若 Owner 要求里程碑运行验收，需在本文件补 `Milestone` 标识后重走该阶段。
- OPEN QUESTION（不阻塞本任务）：T04 是否直接用具体存储实现 `TodoOperations`，还是由 T07 写适配器——决定真实装配中 Todo 工具能否工作，属 Owner 与 T04 / T07 的协调项，不改变 AC-001 ~ AC-009。
- OPEN QUESTION（不阻塞本任务）：T03 与 T04 都要修改 Design §3 / §8 / §11 / §12，修改次序与 merge 顺序由 Owner 决定；在决定前 Analyst 只落 T03 侧条文，不预设 T04 的章节归属。
- 本次规范化变更（旧 → 新，供 Analyst / Cleaner 对照）：① 删除「Design Impact 与 Owner 原文 NONE 不一致」的 Assumption，改记 Owner D6 决定 `UPDATE`；② 工具数量由 3 改为 5（Todo 拆为 create / list / complete）；③ Todo 写入方式由「待 Contract 固化」改为 `TodoOperations` 端口 + 无生产实现 + 测试专用 Fake；④ Todo 并发与隔离的 AC 由「本任务证明真实隔离」收窄为工具层证据，运行期证据指向 T04 / T07；⑤ Out of Scope 显式排除任何生产 Todo 存储、Todo ID 生成与状态机、Session Manager；⑥ 新增 AC-009（领域失败与基础设施失败分级）；⑦ 澄清 §12 所有权表以 Cleaner 审查时的双向差集为准（Design 先于实现更新时不构成不一致）。

## Verification

- AC-001 → 表驱动单元测试：四种运算符 × 整数 / 负数 / `0.5`、`2.5` 精确小数 / `1/3` 除不尽（相对误差 ≤ 1e-12）/ `0.1+0.2`；从 `ToolResult.Content` 解析数值与期望值比对，断言 `IsError=false` 且数值有限。
- AC-002 → 表驱动错误用例：`div` 且 `b=0` / `b=-0.0`、`op="pow"`、缺 `a`、`a:"2"`、`1e308*10`、`NaN` / `Infinity` 字面量、`Arguments` 为空 / `not-json` / `[]`；每条断言 `IsError=true`、`err == nil`、`error.code` 等于期望码、`Content` 不含 `result` 类字段；用 `defer recover()` 断言不 panic。
- AC-003 → 固定预置数据下对同一查询调用 ≥2 次，断言 `Content` 完全相等；断言含 Mock 标识；`Go` 与 `go` 结果一致；无命中用例断言 `results` 为空数组且 `IsError=false`；空查询 / 纯空白断言 `IsError=true`。
- AC-004 → 端到端用例（端口为记录调用参数的测试 Fake）：创建 → 查询 → 完成 → 再查询状态；断言每次调用的 `sessionID` 参数等于 `req.SessionID`；不存在标识用例断言 `IsError=true` + `todo_not_found` 且随后列表不变；空 `SessionID` 用例断言被拒绝且端口调用次数为 0。
- AC-005 → 双 Session 用例：A 建 2 条，B 列表为空（B 的 `sessionID` 传入正确）；B 用 A 的标识完成 → 端口返回 `ErrTodoNotFound` → 工具返回 `todo_not_found` 且 A 的数据逐字段不变；`Arguments` 含三种会话键名的用例断言被拒绝且端口零调用。测试注释须写明该证据不覆盖真实 Session 隔离。
- AC-006 → `go test -race -count=1 ./...`；同一 Session 并发创建 `N=50`（`sync.WaitGroup`）后断言 50 条且标识集合大小 = 50；并发完成同一标识断言终态唯一；两会话并发创建断言互不包含对方标识；测试注释与 Result 报告须写明证据边界。
- AC-007 → 干净检出执行 `go build ./...`、`go vet ./...`、`make test` 退出码 0；`go test -race ./...` 通过；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 核对新增包 import 仅含标准库与 `core` / `tool` / `session`；`git diff --stat <baseline>..HEAD -- core tool session llm contextmgr runlog` 为空；测试源码不含外网主机名、真实凭据与环境变量依赖。
- AC-008 → `git ls-files` 与 Design §12 表逐行双向比对（差集为空）；Cleaner 将 Design 的包职责 / 依赖方向 / Todo 写入路径 / 证据归属与 Contract、实现三方核对，任一处不一致即不满足。
- AC-009 → 端口注入返回普通 `error` 的 Fake → 断言 `err != nil`、`core.KindOf(err) == core.KindInternal`；端口返回 `context.Canceled` → `KindCancelled`；预取消 `ctx` → `err != nil`、`KindCancelled`、端口零调用；`All(nil)` 的 Todo 工具 → `err != nil`、`KindInternal`、不 panic；断言 `Content` 不含端口错误原文中的路径 / 凭据样例字符串。
- 环境：Linux + Go 1.24.x + GNU Make；无需 MySQL / Redis / MQ / 外网 / 真实 API Key / 真实 LLM 调用。

## Complexity

COMPLEX

原因：本任务必须决定三件会长期约束后续任务的公共事实——承载具体工具的模块边界与依赖方向、Todo 的写入路径（在不把可变 `Session` / `Todo` / `Store` 交给 Tool 的前提下细化 INV-005，并成为 T04 / T07 必须实现的接口），以及五个工具名、JSON Schema 与 `Content` 结构这类模型可见协议。同时涉及会话身份的唯一来源（不得由模型经参数选择会话）、工具层并发不得引入共享可变状态、领域失败与基础设施失败的分级语义，以及「工具层证据」与「运行期隔离证据」的归属划分。多个现实方案会产生不同的公共接口与后续实现成本，需 Analyst 在 Contract 中定稿并由 Owner 确认。

## Analyst Questions

1. Contract 定稿与正式批准：现 `contract.md` 为 `OWNER_DIRECTION_APPROVED`，`Selected Design` / `Owner Decision Record` 需按规范化后的本文件复核（工具数 5、`TodoOperations`、AC-006 证据口径、AC-009 失败分级是否已完整覆盖），再按 V3 完成两段式 `APPROVED`（A1 `contract.md` → A2 仅 `state.yaml`）。
2. Design 更新（`APPROVED` 后、Coder 前）：按 `Design Sync Plan` 落 §2 / §3 / §8 / §11 / §12 / §14；需明确 T03 只写自身条文、不预设 T04 对同章节的修改；§11 INV-005 的细化文案要同时写明「运行期证据归 T04 / T07」。
3. T04 / T07 衔接（需 Owner 决定，不阻塞本任务 Coder 启动）：T04 现有 `task.md` 未声明实现 `builtin.TodoOperations`；若 T04 定义自己的 Todo API，缺口由 T07 适配器承担，否则 Todo 工具在真实装配中不可用。是否在 T04 或 T07 的 `task.md` 中显式写入该衔接，以及 T03 / T04 的 Design 修改次序与 merge 顺序，请 Owner 决定。
4. 协议兼容：若 T02 实现表明 `Parameters` 子集（尤其 `enum`）支持范围更窄，走 Contract Revision，不在实现中自行降级。

## Review Baseline

- Base commit：`6902d2445665ec356a2db22b33b7749d39d8018e`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）`）。当前分支 `feat/builtin-agent-tools-v1`，其既有提交为 `f28458f`（任务创建）、`4eb2ca2`（Contract 起草）、`4c626f0`（按 Owner D1—D6 修订 Contract）。
- 任务开始时已有修改：无。`git status --short` 为空（创建任务时工作区 clean）；本次规范化前 HEAD = `4c626f0`，工作区同样 clean。
- 任务分支：由 Owner 预先创建并推送的 `feat/builtin-agent-tools-v1`（无 `feature/` 前缀）；TaskBuilder 不创建、不切换、不重命名分支。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/builtin-agent-tools-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`builtin/**` 与测试由 Coder 在 C1 引入；`docs/design/runtime-architecture.md` 由 Analyst 在 Contract `APPROVED` 后更新。并行的 `llm-openai-client-v1`（`llm/**`）、`tool-registry-executor-v1`（预期 `tool/**`）与本任务修改范围不重叠；发现他人遗留 dirty 文件时 STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX）
