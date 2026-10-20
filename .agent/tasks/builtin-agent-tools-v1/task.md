# Task: 内置 Agent 工具（Calculator / Mock Search / Todo，v1）

## Goal

交付三个可被 Agent 以统一 `tool.Tool` 契约直接调用的内置工具——计算器、Mock 搜索与待办事项：行为确定、错误可预期、结果可被模型直接消费；待办事项严格绑定 `SessionID`，不同会话完全隔离，并发调用下不丢数据、不产生重复标识；全部行为由不依赖 Agent Loop 的单元测试证明。

## Scope

- 在本任务内新增承载内置工具的实现包（包路径与包名由 APPROVED Contract 固化）。不得把具体工具实现写入 `tool` 包已有的契约文件 `tool/tool.go`——T00 长期 Design §3 明确 `tool` 模块不负责具体工具实现。
- Calculator 工具：支持加、减、乘、除四种运算；运算符与操作数的表达形式（结构化字段或表达式字符串）由 Contract 固化；成功时返回可解析的数值结果。
- Calculator 的输入校验与失败行为：除零、未知运算符、缺少操作数、操作数非数值（含非数字字符串、NaN/Inf 等边界，具体集合由 Contract 固化）、参数不是合法 JSON，均给出可恢复的失败结果（`core.ToolResult.IsError=true`），不 panic，也不返回任何看似有效的数值结果。
- Mock Search 工具：基于本地预置数据（不访问网络、不依赖外部服务）返回搜索结果；同一查询在同一数据集下结果完全确定（含顺序）；结果明确标识为 Mock；无命中时返回空结果而非错误。
- Todo 工具：支持创建、查询（列表）与完成（状态更新）。待办的身份与写入必须绑定 `tool.Request` 中的 `SessionID`；`SessionID` 为空时按 Contract 固化的规则拒绝执行且不产生写入。
- Todo 的会话隔离：任何工具调用只能读写调用方 Session 的待办；用其他 Session 的待办标识执行操作必须失败，且不改变任何会话的数据。
- Todo 的并发一致性：同一 Session 的并发创建/完成不丢失、不重复、不产生重复标识；不同 Session 的并发操作互不干扰；`go test -race` 无数据竞争。
- Todo 的写入能力注入方式（如何在不把可变 `Session` / `Todo` / `Store` 交给工具的前提下写入会话待办）由 Contract 固化，并作为公共接口交给后续 Session 持久化任务实现。
- 三个工具均实现 `tool.Tool`（`Spec()` + `Execute(ctx, tool.Request) (core.ToolResult, error)`），并可通过公共构造函数在不装配 Agent Loop、不启动 Provider 的情况下被构造与调用；工具不得 import 上层运行时包。
- 工具名、描述与 `Parameters` JSON Schema 属于模型可见的长期协议，在本任务内固化并纳入 Contract。
- 测试：三个工具的正常、失败与边界用例；Todo 会话隔离与并发用例；测试不访问网络、不读取环境变量、不依赖真实服务。
- 更新 `docs/design/runtime-architecture.md`：新增包的位置与职责、依赖方向、Todo 写入路径与既有 INV-005（会话归属）的关系；§12 文件所有权表补入本任务新增文件，且与实际 `git ls-files` 一致（表不得先于实现出现不一致状态）。

## Out of Scope

- `tool.Registry` 实现、工具查找 / 调度 / 执行器、同 step 多工具并发调度与失败回填顺序（属 `tool-registry-executor-v1` / Agent Loop 任务）。
- Agent Loop 多轮调度、消息历史组装、预算与取消在 Loop 中的落地。
- LLM Provider 调用或任何真实模型请求；工具结果如何进入 prompt 的措辞设计。
- 真实搜索引擎、HTTP 抓取、外部网络、第三方 SDK（根模块保持零 `require`）。
- Todo 的持久化（DB / Redis / 文件）、migration、跨进程共享、重启恢复、多实例一致性；`session.Store` 的完整实现。
- 修改既有公共契约：`core/**`、`tool/tool.go`、`session/session.go`、`llm/**`、`contextmgr/**`、`runlog/**` 的既有类型、字段、方法与错误种类；确需变更时走 Contract Revision 回 Analyst / Owner。
- 沙箱、权限模型、审计、限流、工具级超时与重试策略。
- CLI / HTTP Server / 可运行二进制 / Docker。
- 修改 `.agent/workflow.yaml`、`.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`AGENTS.md`。
- 全局资源：本任务不占用任何 Shared Resource（无 migration、无错误码域、不新增 Registry 文件）；`resources.reservations` 保持为空。

## Design Impact

Design Impact: UPDATE
Design Artifact: `docs/design/runtime-architecture.md`

理由：新增内置工具包会改变 §2 分层与 §3 模块职责（新增一条模块边界）；Todo 工具需要一条不违反 T00 §8「不向 Tool 传递可变 `Session` / `Todo` / `Store`」的写入路径，会修改 INV-005（会话归属）的可执行定义并新增长期公开接口；工具名与 JSON Schema 成为模型可见的长期协议。因此不属于局部实现。

## Acceptance Criteria

- [ ] AC-001：给定一个 Calculator 工具实例，当以合法操作数分别调用加、减、乘、除时，均返回 `IsError=false` 的结果，且结果内容可解析为与数学答案数值相等的值（覆盖整数、负数、可精确表示的小数与除不尽的样例；除不尽样例按 Contract 固化的精度/表达规则判定）。
- [ ] AC-002：给定 Calculator，当输入除零、未知运算符、缺少操作数、操作数非数值、参数不是合法 JSON 时，均返回 `IsError=true` 的结果并给出可定位到具体原因的文本，`Execute` 返回的 `error` 为 nil，不产生任何看似有效的数值结果，进程不 panic。
- [ ] AC-003：给定 Mock Search 与固定预置数据，当对同一查询连续调用多次时，返回结果逐字节一致（含顺序）；结果文本明确包含 Mock 标识；当查询无命中时返回 `IsError=false` 的空结果集合并同样带 Mock 标识。
- [ ] AC-004：给定一个非空 `SessionID`，当通过 Todo 工具创建待办、查询列表、完成某个待办时，创建返回唯一标识，查询能列出该 Session 的全部待办及其状态，完成后该待办状态为 `completed`；当完成一个不存在的待办标识时返回 `IsError=true` 且不产生副作用；当 `SessionID` 为空时按 Contract 固化规则拒绝执行且不写入。
- [ ] AC-005：给定 Session A 与 Session B，当 A 创建（并完成）若干待办后，B 的查询结果中不出现 A 的任何待办；当 B 使用 A 的待办标识执行完成操作时，调用返回失败结果，且 A 的待办数据与状态保持不变。
- [ ] AC-006：给定同一 Session，当并发创建 N 个待办后查询时，返回恰好 N 条且标识互不重复；并发完成同一待办时最终状态一致且无数据竞争；不同 Session 的并发操作互不影响；`go test -race ./...` 通过。
- [ ] AC-007：给定一份干净检出，当不启动 Agent Loop、不连接网络与外部服务、仅通过公共构造函数与 `tool.Tool` 接口调用三个工具时，行为满足 AC-001 ~ AC-006；`go build ./...`、`go vet ./...`、`make test` 退出码 0；新增包不 import 上层运行时包，`go list` 核对无反向依赖。
- [ ] AC-008：给定更新后的 `docs/design/runtime-architecture.md`，当与 APPROVED Contract 及最终实现逐项核对时，新增包的职责、依赖方向、Todo 写入路径与 INV-005 的解释三者一致；§12 所有权表与 `git ls-files` 的双向差集为空（每个源文件只归属一个模块）。

## Relevant Context

- 已核实事实：T00 `runtime-core-contracts-v1` 已 `CLEAN` / Owner `ACCEPTED` / 交付 `PASS`；`tool.Tool` 冻结为 `Spec() core.ToolSpec` 与 `Execute(ctx context.Context, req Request) (core.ToolResult, error)`；`tool.Request` 内嵌 `core.ExecutionRef{SessionID, RunID}` 与 `core.ToolCall`（`tool/tool.go`）。仓库中不存在任何具体工具实现。
- 已核实事实：`core.ToolResult{CallID, Name, Content, IsError}`；可恢复失败以 `IsError=true` 表达且 `error` 为 nil，只有不可恢复失败才返回 `error`（T00 Contract §5、§7）。
- 已核实事实：`session` 包已有 `Todo{ID, Title, Status}`、`TodoStatus`（`pending` / `in_progress` / `completed` / `cancelled`）与 `Store{Load, Save}` 接口，但无任何实现；`TodoStatus` 字面量未导出常量（T00 `CLEAN-001`），构造状态值需字符串转换。
- 已核实事实：T00 Design §3 明确 `tool` 模块「不负责具体工具实现」；§8 规定 Session 是 Todo 唯一所有者、禁止向 Tool 传递可变 `Session` / `Todo` / `Store`；§12 要求每个源文件只归属一个模块，新增文件必须同步回写所有权表。
- 已核实事实：根 Module 为 `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零第三方依赖）；`make test` 等价 `go test ./...`；仓库根目前没有 `builtin` / `tools` 之类的实现包，也没有 `tool.Registry` 实现。
- 已核实事实：`.agent/workflow.yaml` 的 `resources: {}`（无已声明 Shared Resource Kind）；`state.yaml` 采用 `schema_version: 3` 严格解码（未知字段直接 ERROR）。
- Assumption：三个工具放在同一个新实现包内（包粒度与包路径由 Analyst 在 Contract 固化），不要求每个工具独立成模块。
- Assumption：本任务不启用 Deliverer（交付物是库内实现，无独立可运行产物）；若 Owner 要求里程碑运行验收，需在本文件补 `Milestone` 标识后重走该阶段。
- Assumption（与 Owner 原文不一致，需 Owner 确认，不阻塞 Analyst 启动）：Owner 需求标注 `Design Impact: NONE`，本任务判定为 `UPDATE`，因为新增模块边界、Todo 写入路径与模型可见工具协议均属项目级长期事实。若 Analyst / Owner 判定仍为 `NONE`，则 AC-008 与 Design 回写退出 Scope，其余 AC 不变。
- OPEN QUESTION（不阻塞）：三个工具（尤其 Todo）在模型可见协议中是「每个操作一个工具」还是「一个工具带 action 参数」；具体工具名、参数 schema 与结果文本格式由 Contract 固化。
- OPEN QUESTION（不阻塞）：本任务与并行分支 `tool-registry-executor-v1`（Registry / 执行器）如何衔接——是否必须额外提供「一次返回全部内置工具」的聚合构造函数以便注册；不改变 AC-001 ~ AC-007 的可观察结果。
- OPEN QUESTION（不阻塞）：工具对已取消 / 已超时 `ctx` 的响应语义（纯计算类工具是否需要响应取消）；不同答案不改变本任务 AC 的行为范围。

## Verification

- AC-001 → 表驱动单元测试：四种运算符 × 整数 / 负数 / 可精确表示的小数 / 除不尽样例；从 `ToolResult.Content` 解析数值与期望值比对（除不尽按 Contract 精度规则），断言 `IsError=false`。
- AC-002 → 表驱动错误用例：除以 0、未知运算符、缺操作数、非数值字符串、`Arguments` 为非法 JSON；断言 `IsError=true`、`err == nil`、`Content` 含可定位原因；断言不 panic（必要时用 `recover` 包裹）；断言失败输出中不存在被误判为结果的数值。
- AC-003 → 固定预置数据下对同一查询调用 ≥2 次，断言结果字符串完全相等；断言结果含 Mock 标识；无命中用例断言空集合且 `IsError=false`。
- AC-004 → 端到端用例：创建 → 查询 → 完成 → 再查询状态；不存在标识用例断言 `IsError=true` 且随后查询数据不变；空 `SessionID` 用例断言被拒绝且无写入。
- AC-005 → 双 Session 用例：A 建 2 条、B 查询为空；B 用 A 的标识完成 → 失败；A 再查询数据与状态不变；断言 A 的标识不出现在 B 的列表中。
- AC-006 → `go test -race -count=1 ./...`；同一 Session 并发创建 `N=50`（`sync.WaitGroup`）后断言 50 条且标识集合大小 = 50；并发完成同一标识断言终态唯一；并发操作两个 Session 断言互不影响。
- AC-007 → 干净检出执行 `go build ./...`、`go vet ./...`、`make test` 退出码 0；`go test -race ./...` 通过；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 核对新增包 import 仅含标准库与契约层包，无上层运行时依赖；测试源码不含外网主机名、真实凭据与环境变量依赖。
- AC-008 → `git ls-files` 与 Design §12 表逐行双向比对（差集为空）；Cleaner 将 Design 的包职责 / 依赖方向 / Todo 写入路径与 Contract、实现三方核对，任一处不一致即不满足。
- 环境：Linux + Go 1.24.x + GNU Make；无需 MySQL / Redis / MQ / 外网 / 真实 API Key。

## Complexity

COMPLEX

原因：本任务必须决定两件会长期约束后续任务的公共事实——承载具体工具的模块边界，以及 Todo 的写入路径（在不把可变 `Session` / `Todo` / `Store` 交给 Tool 的前提下仍保持 Session 是唯一所有者，并成为后续 Session 持久化任务必须实现的接口）。同时涉及会话隔离这一安全边界、同一 Session 并发写入的原子性、模型可见的工具名与 JSON Schema 协议，以及可恢复 `IsError` 与不可恢复 `error` 的失败语义划分。多个现实方案会产生不同的公共接口与后续实现成本，需 Analyst 分析并在 Contract 中由 Owner 确认。

## Analyst Questions

1. 内置工具包的模块边界：包路径与分层（是否新增一层 `tools` / `builtin` 包、是否按工具分子包）；如何保证不修改 `tool/tool.go`、不与并行任务 `tool-registry-executor-v1`（Registry / 执行器，预期落在 `tool/**`）在文件与接口上冲突；用 `go list` 机械判定依赖方向的判定标准。
2. Todo 写入路径：在不把可变 `Session` / `Todo` / `Store` 交给 Tool 的前提下，如何让 Todo 工具写入会话待办？可选方向（不是最终方案）：注入按 Session 作用域的最小 Todo 存取接口；注入 `session.Store` 并在工具内保证原子读改写；由装配层注入带锁的内存实现。需明确公共符号、会话隔离的强制点（`SessionID` 缺失、跨会话标识访问）、重复完成的幂等语义、ID 生成规则，以及该接口与 `session.Store`、后续 Session 持久化任务的关系。
3. Todo 并发一致性：同一 Session 并发创建/完成的原子性边界属于工具实现还是注入的存储实现；失败时的部分写入语义；`-race` 下的可判定标准。
4. 工具协议形态：Calculator / Mock Search / Todo 的工具数量划分（Todo 拆 3 个操作还是 1 个带 action 的工具）、工具名、描述、`Parameters` JSON Schema、`ToolResult.Content` 的文本结构（纯文本或 JSON 字符串）、错误文本是否结构化。这些将成为模型可见的长期协议，需在 Contract 固化。
5. 失败语义划分：除零 / 未知运算符 / 参数非法 / 待办不存在 / 空 `SessionID` / 存储层异常分别映射为 `IsError=true`（可恢复，`error` 为 nil）还是返回 `*core.RunError`（不可恢复）；`RunError.Op` 的约定；对已取消 `ctx` 的响应是否纳入本任务。
6. Mock Search 的确定性定义：预置数据放在源码内 fixture 还是测试内；查询匹配规则（精确 / 大小写 / 子串）、排序规则、结果字段；「Mock 标识」的承载位置（`Content` 前缀 / 结构化字段 / Spec 描述）。需给出用测试证明确定性而不绑定实现细节的方式。
7. Design Impact 校正：TaskBuilder 判定为 `UPDATE`（新增模块边界 + Todo 写入路径与 INV-005 解释 + 模型可见工具协议），Owner 原文标注 `NONE`。若 Analyst 认为应为 `NEW` 或仍为 `NONE`，需在 Contract 记录理由；改变 Scope（是否回写 Design）时回 Owner / TaskBuilder，不在实现阶段自行改判。

## Review Baseline

- Base commit：`6902d2445665ec356a2db22b33b7749d39d8018e`（`chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）`）。当前分支 `feat/builtin-agent-tools-v1`，本地 HEAD == `origin/feat/builtin-agent-tools-v1` == `origin/develop` == 该 commit（`git rev-list --left-right --count origin/develop...HEAD` 为 `0 0`）。
- 任务开始时已有修改：无。`git status --short` 为空（本任务文件落盘前工作区 clean）。
- 任务分支：由 Owner 预先创建并推送的 `feat/builtin-agent-tools-v1`（无 `feature/` 前缀）；TaskBuilder 不创建、不切换、不重命名分支。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/builtin-agent-tools-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；新增工具实现包、测试与 `docs/design/runtime-architecture.md` 的更新由 Analyst / Coder 在各自 commit 中引入。并行的 `llm-openai-client-v1`（`llm/**`）与 `tool-registry-executor-v1`（预期 `tool/**`）不在本任务修改范围内；发现他人遗留 dirty 文件时 STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX）
