# Technical Contract

## Decision Status

APPROVED（Owner Contract Decision 2026-10-20，D1—D5 见 `Owner Decision Record`；D5 前置条件已满足——TaskBuilder 已按 Workflow V3 将 `Design Impact` 校正为 `UPDATE` 并补充 AC-006。Evidence Commit A1 由 `state.contract.target` 引用）。

任务：`tool-registry-executor-v1`。Design Impact：`UPDATE` → `docs/design/runtime-architecture.md`（§7 / §11 / §14 由 Analyst 在 A1 后落库，§12 由 Coder 在同交付内登记）。

## Problem

`tool` 包目前只有契约（`tool/tool.go`：`Request` / `Tool` / `Registry`），没有 Registry 实现、没有参数校验、没有执行器。本任务在**不改 `core` 公共符号、不引入第三方依赖、不 import `llm`** 的前提下补齐「注册 → Schema 输出 → 参数校验 → 受控执行 → 结构化结果」，并把 Owner 已定的四项语义（同 Run 去重、工具级超时归属、fail-closed Schema 子集、Registry 生命周期）固化为可测规则。

## Verified Current Behavior

- VERIFIED：`tool/tool.go` 仅含 `Request{core.ExecutionRef; Call core.ToolCall}`、`Tool{Spec(); Execute(ctx, Request) (core.ToolResult, error)}`、`Registry{Lookup(string) (Tool, bool); Specs() []core.ToolSpec}`；`go test ./...` 显示 `tool [no test files]`。
- VERIFIED：`core.ExecutionRef{SessionID, RunID}` 已存在并可表达 Run 身份（`core/run.go:9-12`），`tool.Request` 已内嵌它——**D1 不需要扩大公共契约**。
- VERIFIED：`core.ToolResult.Validate()` 要求非空 `CallID`（`core/toolcall.go:35`）；`core.ToolCall.Arguments` 为原始 `json.RawMessage`（`core/toolcall.go:5-13`）。
- VERIFIED：`core.ErrorKind` 9 值，无工具级超时/名称非法专用 Kind；`core.RunResult.Validate` 要求 `FAILED + tool_failure` 带 `CallID`（`core/run.go:114`），`cancelled`/`deadline_exceeded` 只能映射 `CANCELLED`（`core/run.go:125`、`core/error.go:120`）；`invalid_pairing` 可用于 `FAILED`（`core/run.go:150`）。
- VERIFIED：`llm.Request.Tools` 是 `[]core.ToolSpec`（`llm/provider.go:38-46`）；`runtime-architecture.md` §3 把「`Tool` 执行契约、元数据、`Registry` 查找」归 `tool`，§7 只定义整体超时与可恢复/不可恢复工具失败（**未定义工具级局部超时**），§12 要求同交付回写文件所有权表，§15 禁止实现期私自扩展公共符号。
- VERIFIED：基线 `6902d24`；当前分支 `tool-registry-executor-v1` 与 `origin/tool-registry-executor-v1` 一致，**不是** `develop`，A1/A2 不会落在共享分支；`git status --short` 仅未跟踪的 `.agent/tasks/tool-registry-executor-v1/`；`go vet ./...`、`go test ./...` 通过（go1.24.1）。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`，本任务不占用 Shared Resource。
- UNKNOWN：本项目「三个内置工具」的实际参数 Schema 尚未存在于仓库（内置工具实现属 Out of Scope）。本 Contract 的 fail-open→fail-closed 子集按对象/必填/嵌套/数组/枚举/严格字段的通用形态取最小集；未来新增关键字须走 Contract Revision。

## Recommendation

Owner 已决定，下列为最终设计（详见 `Selected Design`）：

1. **去重范围 = 同一次 Run**：键为 `(RunID, CallID)`，取自请求内已存在的 `core.ExecutionRef`；同 Run 重复 CallID **拒绝再次执行并返回 `invalid_pairing` 错误**，不复用首次结果。
2. **执行期归类**：工具级超时 / 工具 `error` / 参数与名称校验失败 → 可恢复 `ToolResult{IsError:true}`；panic → `tool_failure`（保留 `CallID`，不含堆栈）；父 ctx 取消或整体超时 → 向上返回 `cancelled` / `deadline_exceeded`；不新增 `ErrorKind`。
3. **Registry**：并发安全、注册期可写、运行期禁止热注册（`Seal()` 机械约束）、名称非空、同名拒绝、`Specs()` 按名稳定排序并返回深拷贝、空 `Parameters` 规范化为 `{"type":"object","properties":{}}`。
4. **Schema：fail-closed**：只接受 Contract 明列的关卡子集，未支持关键字或非法 Schema 一律注册期拒绝。
5. **Design Impact = `UPDATE`**：等 TaskBuilder 更新 `task.md` 后，由 Analyst 先写长期 Design，再交 Coder。

## Selected Design

### 1. Registry 生命周期与语义

- 新增 `tool.MapRegistry`（实现既有 `Registry`）+ `NewMapRegistry()` + `Register(Tool) error` + `Seal() error`。
- **注册阶段可写，运行阶段禁止热注册**：`Seal()` 由装配层在装配完成后调用（幂等）；`Seal` 之后 `Register` 返回 `ErrRegistrySealed`，`Lookup` / `Specs` 不受影响。装配层未 `Seal` 时 Registry 行为仍是「允许注册」，该责任属装配层，不由本任务强制。
- 并发安全：内部 `sync.RWMutex`；`Register` / `Lookup` / `Specs` / `Seal` 可被多 goroutine 并发调用（`-race` 验证）。
- 注册拒绝（全部返回 `*core.RunError{Kind: invalid_argument}`，`Cause` 为对应哨兵，`errors.Is` 可区分，`Op = "tool.MapRegistry.Register"`）：
  - 名称空或全空白 → `ErrEmptyToolName`；
  - 同名已注册 → `ErrDuplicateTool`；
  - Spec 非法（Schema 子集外、结构非法、`Parameters` 非法 JSON 或非 JSON object）→ `ErrInvalidToolSpec`（Message 指出字段路径与原因）。
- 注册键取 `t.Spec().Name`，**区分大小写、不 trim**。
- 注册时保存 Spec 的一份深拷贝；`Specs()` 返回扁平的 `[]core.ToolSpec` 深拷贝（`Parameters` 字节复制），外部修改返回值或后续修改工具自身 `Spec()` 均不影响 Registry 内定义。`Lookup` 返回注册时传入的**同一 `Tool` 实例**。
- `Specs()` 按 `Name` 字典序（`sort.Slice` / `slices.SortFunc`），保证 LLM 请求可复现。
- 空 `Parameters`（`nil` / `len==0`）在注册时规范化为 `{"type":"object","properties":{}}`；非空 `Parameters` 逐字节保留。

### 2. JSON Schema 校验子集（fail-closed）

注册期解析并校验 Schema，支持的关键字**仅限**：

| 允许关键字 | 位置 | 规则 |
| --- | --- | --- |
| `type` | 根 + 子 schema | 必须为字符串，取值 ∈ {`object`,`string`,`number`,`integer`,`boolean`,`array`,`null`}；**根必须是 `object`**；数组形式（联合类型）不支持 |
| `properties` | 根 + 子 schema | JSON object，值必须是合法子 schema（递归校验） |
| `required` | 根 + 子 schema | 非空字符串数组 |
| `items` | 子 schema（数组） | 单个 schema；tuple（数组形式）不支持 |
| `enum` | 根 + 子 schema | 非空数组，元素为 JSON 基础值（string/number/bool/null） |
| `additionalProperties` | 根 + 子 schema | 仅布尔；`false` 时未声明字段视为校验失败，`true` / 省略表示允许 |
| `description` / `title` | 任意 schema | 注解，忽略（不参与校验） |

其余一律**拒绝注册**（`ErrInvalidToolSpec`，Message 指明关键字路径，如 `$.properties.city.pattern`）：`oneOf` / `anyOf` / `allOf` / `not`、`$ref` / `$defs` / `definitions`、`pattern`、`format`、`minimum` / `maximum` / `exclusiveMinimum` / `exclusiveMaximum` / `multipleOf`、`minLength` / `maxLength`、`minItems` / `maxItems` / `uniqueItems`、`const`、`default`、`examples`、`dependencies`、以及任何未在上表列出的关键字。结构非法（如 `type` 非字符串、`properties` 非 object、`required` 非字符串数组、`enum` 空数组、`items` 非 object）同样拒绝。

调用期校验规则：

- `Arguments` 为空/纯空白视为 `{}`；顶层必须是 JSON object，否则失败。
- 必填：`required` 中的字段必须存在（存在但 `null` 仍需通过类型校验）。
- 类型：`null` 值仅当声明 `type:"null"` 时通过；`integer` 接受数值为整数的 JSON 数字（`1.0` 视为整数，拒绝 `1.5`）；`number` 接受任意 JSON 数字。
- `enum`：按 JSON 值语义比较（数字按数值、字符串按字面、bool/null 精确）。
- `additionalProperties:false`：出现未在 `properties` 声明的字段即失败。
- 首个错误顺序固定：`required` 声明顺序 → `properties` 键字典序 → 数组下标升序；字段路径形如 `$.address.zip`、`$.tags[0]`。
- 错误文本固定前缀：`工具 <name> 参数校验失败: <路径>: <原因>`，`<原因>` 取 `缺少必填字段` / `期望 <type>，实际 <类型>` / `不在枚举取值内` / `参数不是合法 JSON` / `参数必须是 JSON object` / `未声明的字段`。
- 校验器是 `tool` 包私有实现，不新增公共校验类型或方法；校验**必须在 `Tool.Execute` 之前**完成。

### 3. Executor 与去重

- `NewExecutor(ExecutorOptions{Registry, Timeout})`：`Registry == nil` → `invalid_argument`；`Timeout <= 0` 表示不设工具级超时（只受 ctx 约束）。
- `(*Executor).Execute(ctx, Request)` 判定顺序**固定**：`CallID` 空 → `RunID` 空 → 名称空/全空白 → 父 ctx 已结束 → 去重占位 → 查表 → 参数校验 → 执行。
- **去重键 `(RunID, CallID)`**：在收到合法请求后、查表之前以原子方式占位（`sync.Mutex` + `map[dedupKey]struct{}`）。
  - 同 Run 同 `CallID` 的第二次及以后提交：**不执行**、**不复用首次结果**，返回 `*core.RunError{Kind: invalid_pairing, Op: "tool.Executor.Execute", CallID: <请求 ID>, Message: "工具调用 ID 在本次 Run 内已提交过（重复调用）"}`。
  - 首次提交即使最终是 `IsError=true`（未知工具、参数失败、工具 error、工具超时）同样占位，后续提交仍按重复调用拒绝。
  - 并发提交同一键：只有一个 goroutine 获得占位并执行，其余立即返回上述 `invalid_pairing`（不等待、不重放结果）；不产生重复副作用。
  - 不同 `RunID`（含不同 `SessionID`）的同名 `CallID` 互相独立。
  - 占位表只记录键，不记录结果；随 `Executor` 生命周期累积，本版不淘汰（见 `Open Risks`）。
- **RunID 必填（派生规则）**：`req.RunID == ""` 时无法界定 Run 边界，返回 `*core.RunError{Kind: invalid_argument, Op: "tool.Executor.Execute", Message: "RunID 为空，无法确定去重范围"}`，不执行、不占位。该规则只使用既有 `core.ExecutionRef` 字段，不扩大公共契约。
- 结果归一化：返回 `ToolResult` 时强制 `CallID = req.Call.ID`、`Name = req.Call.Name`（不信任工具自填值）；返回 `error` 时 `*core.RunError.CallID = req.Call.ID`（`CallID` 为空的请求除外，此时不伪造）。
- 执行器不产生 `RunStatus`、不写日志后端、不做批量并发调度、不计数预算。

### 4. 执行期异常与 Context 边界

- 工具在独立 goroutine 中执行，`Execute` 通过 `select` 等待「工具返回 / 工具级超时定时器 / 父 ctx 结束」。
- **工具级超时**（父 ctx 仍存活）→ `ToolResult{IsError:true, Content:"工具 <name> 执行超时（<d>）"}`；不映射为 `CANCELLED`。
- **父 ctx 取消 / 整体超时** → 返回 `error{Kind: cancelled|deadline_exceeded, CallID}`；**取消优先于工具结果**：执行返回时若父 ctx 已结束，丢弃工具结果并返回 ctx 错误。
- **工具返回 `error`** → `ToolResult{IsError:true, Content:"工具 <name> 执行失败: <err>"}`（可恢复，交由模型/ Loop 决策）。
- **工具 panic** → 在 goroutine 内 `recover`，返回 `*core.RunError{Kind: tool_failure, Op: "tool.Executor.Execute", CallID, Message: "工具 <name> 执行发生 panic", Cause: fmt.Errorf("panic: %v", r)}`；**不记录、不拼接堆栈**，不把 `runtime.Stack` / 调用栈写入 Message 或 Content。
- **Context 边界（明确不做承诺）**：工具忽略 ctx 时，执行器只能保证**调用方在超时/取消后及时返回**——不保证终止工具 goroutine、不保证其副作用被撤销或回滚、不保证该 goroutine 一定结束、不保证 `runtime.NumGoroutine()` 不增长。被放弃 goroutine 的后续结果**必须丢弃**（不返回、不占位、不影响同键后续调用）。
- 时间参数：超时使用 `time.NewTimer`，返回前 `Stop()`，避免遗留定时器 goroutine。

### 5. Design 同步清单（D5 / AC-006）

- 归属划分：Analyst 在 A1 之后、Coder 实现之前写入 §7 / §11 / §14；§12 文件所有权表由 Coder 在同交付内登记（只有 Coder 知道最终文件集），路径名单在本 Contract 固定。Analyst 与 Coder 均不得改动 §3 等其他章节。
- §7：新增 4 行（单工具局部超时 / 参数校验失败与未知工具 / 同 Run 重复 Call ID / 工具 panic），终态列统一标注「由 Agent Loop 判定」，本任务只记录执行器返回的承载形式与 `ErrorKind`。
- §11：续编 `INV-009`—`INV-013`（不新建小节），证据列指向本任务 `tool` 包真实测试。
- §14：把「工具失败可恢复性分类」从 Agent Loop 名下收回给 `tool` 模块；「同 step 多调用顺序与并发」「跨 step 终态映射与重试」仍归 Agent Loop。
- 设计内容以本 Contract 为准；发现不一致先改 Contract 再改 Design，不得由 Coder 代写设计。

### 6. Agent Loop 侧义务（本任务不实现，仅记录约束）

Loop 必须在工具执行前后各做一次把关：执行前校验消息配对（`core.ValidateToolPairing`，含 CallID 唯一）与重复调用；执行后为每个 Call 恰好产生一条 Result 消息。执行器的 `invalid_pairing` 是**兜底检测**，不是 Loop 免于自检的理由。

## Interfaces and Data

Coder 必须保持的公共面（新增符号属本 Contract 授权范围；既有 `Request` / `Tool` / `Registry` 签名不得改动）：

```go
package tool

var (
    ErrEmptyToolName    error // 名称空或全空白
    ErrDuplicateTool    error // 同名已注册
    ErrInvalidToolSpec  error // Schema 子集外或结构非法
    ErrRegistrySealed   error // Seal 之后调用 Register
)

type MapRegistry struct{ /* mu sync.RWMutex; sealed bool; ... */ }

func NewMapRegistry() *MapRegistry
func (r *MapRegistry) Register(t Tool) error
func (r *MapRegistry) Seal() error // 幂等
func (r *MapRegistry) Lookup(name string) (Tool, bool)
func (r *MapRegistry) Specs() []core.ToolSpec

type ExecutorOptions struct {
    Registry Registry
    Timeout  time.Duration
}

type Executor struct{ /* registry, timeout, mu, seen map[dedupKey]struct{} */ }

func NewExecutor(opts ExecutorOptions) (*Executor, error)
func (e *Executor) Execute(ctx context.Context, req Request) (core.ToolResult, error)
```

编译期约束：`var _ Registry = (*MapRegistry)(nil)`。`Executor` 与 `MapRegistry` 均须可并发使用。

## Business Invariants

- INV-101：`tool` 包只 import 标准库与 `<module>/core`；不 import `llm`；`go.mod` 无新增 `require`。
- INV-102：所有注册/校验/查找/去重失败都发生在 `Tool.Execute` 之前——对应场景下 Fake Tool 调用计数恒为 0。
- INV-103：结果可定位：返回 `ToolResult` 时 `CallID == req.Call.ID` 且非空；返回 `error` 时 `*core.RunError.CallID == req.Call.ID`（`CallID` 为空的请求除外）。
- INV-104：同 Run 同 `CallID` 至多执行一次，重复提交必被显式拒绝（`invalid_pairing`），**不得复用首次结果**；不同 `RunID` 或不同 `CallID` 互不影响；并发同键不产生重复副作用。
- INV-105：父 ctx 取消/超时、工具级超时、工具 panic 下 `Execute` 必在有限时间内返回，不 panic 逃逸、不永久阻塞调用方。
- INV-106：超时与取消绝不伪装成功：工具级超时 → `IsError=true`；父 ctx 取消/超时 → `error{Kind: cancelled|deadline_exceeded}`，不存在 `IsError=false` 的结果。
- INV-107：Registry 定义不可被外部篡改：`Specs()` 返回值与注册值一致（空 Schema 规范化除外），修改返回值或工具自身 `Spec()` 不影响后续 `Specs()`；`Seal` 之后注册被拒绝。

## Failure and Consistency Semantics

| 情形 | 返回形式 | Kind / 标记 | 工具被调用 | 是否占位 |
| --- | --- | --- | --- | --- |
| `Call.ID == ""` | `error` | `invalid_pairing`（CallID 空） | 否 | 否 |
| `RunID == ""` | `error` | `invalid_argument` | 否 | 否 |
| 工具名空/全空白 | `error` | `invalid_argument` | 否 | 否 |
| 父 ctx 已取消 / 整体超时 | `error` | `cancelled` / `deadline_exceeded` | 否 | 否 |
| 同 Run 同 CallID 重复提交 | `error` | `invalid_pairing`（CallID=请求 ID） | 否 | — |
| 未知工具 | `ToolResult` | `IsError=true` | 否 | 是 |
| 参数非法（JSON / 必填 / 类型 / 枚举 / 未声明字段） | `ToolResult` | `IsError=true`，含路径 | 否 | 是 |
| 工具返回 `error` | `ToolResult` | `IsError=true` | 是 | 是 |
| 工具返回结果（IsError 任一） | 原样返回（归一化 CallID/Name） | 保持工具值 | 是 | 是 |
| 工具级超时（父 ctx 存活） | `ToolResult` | `IsError=true` | 是 | 是 |
| 工具 panic | `error` | `tool_failure`（无堆栈） | 是 | 是 |
| 执行结束但父 ctx 已结束 | `error` | `cancelled` / `deadline_exceeded`（取消优先） | 是 | 否 |

一致性说明：

- 单进程内存语义，无多存储/异步系统。执行器占位表是**过程防护**，不是会话事实来源；消息序列权威仍是 Session + `ValidateToolPairing`。
- 成功返回只表示「该 Call 有了可回填的结果」（可能 `IsError=true`），不表示持久化、不表示 Run 继续。
- 部分完成：多 Call 场景由 Loop 逐个提交、逐个产生 Result；执行器不聚合、不丢弃其他 Call 的结果。
- 放弃的 goroutine（超时/取消后仍在运行的工具）不写占位、不返回结果；其副作用不可撤销。

## Allowed / Forbidden Changes

- 允许：新增 `tool/` 下这些源文件与测试（路径已固定，不得增删）：`tool/registry.go`、`tool/validate.go`、`tool/executor.go`、`tool/registry_test.go`、`tool/validate_test.go`、`tool/executor_test.go`、`tool/fake_test.go`。
- 允许（D5 已生效，由 Analyst 在 A1 后执行，Coder 不得代写）：更新 `docs/design/runtime-architecture.md` 的 §7（新增 4 行）、§11（`INV-009`—`INV-013`）、§14（归属收窄）；不得改动其他章节。
- 允许（由 Coder 在同交付内执行）：仅登记 §12 文件所有权表，内容必须与上述固定路径名单及 `git ls-files tool/` 一一对应。
- 禁止：修改 `core/**`、`llm/**`、其他 L1 包；新增 `go.mod` `require`；`tool` import `llm` 或第三方库；实现放 `internal/`；新增与 `core.ToolSpec` 并行的元数据类型。
- 禁止：改既有公共符号签名（`Request` / `Tool` / `Registry`）；新增 `core.ErrorKind`；新增 `runlog` 公共符号；实现批量并发调度、Agent Loop、预算计数、`RunStatus` 映射。
- 禁止：修改 `.agent/**`、`docs/design/agent-workflow.md`、`.cnb.yml`、`.agent/engine/**`；任何 `develop` 上的提交（Registry-only 例外不适用）；调用真实收费 LLM API。

## Verification Requirements

- INV-101 → `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool` 只含标准库与 `<module>/core`；`git diff go.mod` 为空。
- INV-102 → 表驱动覆盖：名称空/全空白、重复注册、Seal 后注册、未知工具、非法 JSON、非 object、缺必填、类型不符、枚举不符、未声明字段（`additionalProperties:false`）；每条断言语义与上表一致、Fake Tool 计数 0、无 panic；`Lookup` 未命中断言 `(nil,false)`。
- INV-103 → 成功 / 工具 error / 工具级超时 / panic / 父 ctx 取消五条路径断言 `CallID == 请求 ID`；空 `CallID` 结果由 Fake Tool 返回时断言被归一化。
- INV-104 → 同 Run 同 CallID 连两次：计数 1，第二次 `core.KindOf == invalid_pairing` 且无结果复用；不同 RunID 同 CallID：计数 2；不同 CallID：计数 2；N 个 goroutine 并发同键（`-race`）：执行次数 1，其余 N-1 为 `invalid_pairing`。
- INV-105 → 阻塞型 Fake Tool（超时后完成）用 `select` + `time.After` watchdog 断言返回时机；panic 用例断言 `tool_failure` 且 Content/Message 不含 `goroutine`/`stack`/`runtime/debug` 痕迹；已取消 ctx 断言立即返回；连续 N 次超时后，在**等待收敛窗口**内轮询 `runtime.NumGoroutine()` 判断不单调增长（禁止固定 sleep 单点断言）。
- INV-106 → 工具级超时断言 `IsError=true` 且 Content 含超时文本；父 ctx 超时/取消断言 `deadline_exceeded`/`cancelled` 且从不 `IsError=false`。
- INV-107 → `Specs()` 返回后修改切片元素与 `Parameters` 字节，再次 `Specs()` 与注册值一致；空 `Parameters` 断言规范化为 `{"type":"object","properties":{}}`；`Seal` 后 `Register` 返回 `ErrRegistrySealed` 且 `Lookup`/`Specs` 正常。
- R3 Schema fail-closed → 表驱动逐条断言未支持关键字（`pattern`、`format`、`oneOf`、`$ref`、联合 `type`、tuple `items`、`additionalProperties` 为对象等）注册被拒绝且 Message 含关键字路径；合法子集（嵌套对象、数组 `items`、`enum`、`additionalProperties:false`）注册通过并按调用期规则生效。
- AC-005 → 仓库根 `make test`、`go vet ./...`、`go test -race ./tool/...` 退出码 0；`git ls-files tool/` 与 `docs/design/runtime-architecture.md` §12 表一致。
- AC-006 → 核对 Design：§7 四行存在且终态/`ErrorKind` 与本文失败语义表逐字对应；§11 的 `INV-009`—`INV-013` 证据列指向真实测试；§14 无一条语义同时归属 `tool` 与 Agent Loop；§12 与 `git ls-files tool/` 差集为空；设计文档 diff 不超出 §7/§11/§12/§14。
- 环境：Linux + go1.24.1，无外部服务、无网络。

## Open Risks

- 占位表只增不删：长期复用的 `Executor` 会随 Call 数累积内存；本版不提供清理 API（如需按 Run 淘汰，走后续 Revision）。
- 拒绝空 `RunID` 提高了集成前置要求：装配层/Loop 必须设置 `ExecutionRef.RunID`，否则每次调用都是 `invalid_argument`。
- fail-closed 子集可能缺少未来内置工具所需的真实关键字（三个内置工具尚未实现），补齐必须走 Contract Revision，不能在实现期放行。
- 忽略 ctx 的工具：goroutine 可能长期存活，副作用不可撤销；AC-003 的「不持续增长」只对会自行返回的工具成立。
- `Seal()` 是 D3 的派生机制（D3 只要求「运行阶段不支持动态热注册」）：若装配层忘记调用，热注册在运行期仍可能发生，Registry 无法自证。
- 重复调用返回 `invalid_pairing`：若 Loop 未在执行前自检，该错误会让 Run 进入 `FAILED`（`core` 允许 `invalid_pairing` 作为 FAILED 的 Kind），行为是「显式失败」而非「静默继续」。
- 时间相关测试（超时、watchdog、goroutine 收敛）在慢速 CI 上可能不稳定，需留足余量。
- panic 归 `tool_failure` 会终止整个 Run：单个工具缺陷的影响面是 Run 级。
- `task.md` 把「登记 §12 文件所有权表」交给 Coder，而 §7/§11/§14 由 Analyst 写：若 Coder 选择超出 Contract 固定名单的文件名，AC-006 的「§12 ↔ `git ls-files` 一一对应」会失败，必须先回 Contract。

## Owner Decision Record

Owner Decision（2026-10-20，任务 `tool-registry-executor-v1`；TaskBuilder 已按 D5 更新 `task.md`，`contract.status` 由本 Contract + A2 共同确认）：

- **D1 去重（已决定，修改原推荐）**：去重身份必须限定在同一次 Run 内，使用 `RunID + CallID`（`core.ExecutionRef.RunID` 已可表达，**无需扩大公共契约**）；同 Run 重复 CallID 必须被识别并**拒绝再次执行**，不允许静默复用首次结果；并发下相同调用不得产生重复副作用；不同 Run 的同名 CallID 相互独立。Agent Loop 后续必须在工具执行前检查重复调用与消息配对。
- **D2 异常归类（有条件接受）**：工具级局部超时 → `IsError=true` 的 Tool Result，不映射整个 Run 为 `CANCELLED`；普通工具失败与非法参数 → 结构化 Tool Result；父 ctx 取消或整体超时 → 向上终止，由 Runtime 映射 `CANCELLED` + 对应 ErrorKind；panic → 恢复后按不可恢复 `tool_failure` 处理、保留 CallID、不泄漏内部堆栈；不新增公共 `ErrorKind`；必须写明工具忽略 Context 时的行为边界，不得承诺强制终止或副作用撤销。
- **D3 Registry（接受）**：注册阶段允许添加工具，运行阶段不支持动态热注册；并发安全；名称不能为空；重复名称拒绝；`Specs()` 按名称稳定排序且返回值不可被外部修改破坏内部定义；空 `Parameters` 规范化为合法空对象 Schema。
- **D4 Schema（fail-closed）**：仅支持本项目三个内置工具实际需要的子集并在 Contract 明确列出；未支持关键字或非法 Schema 拒绝注册，不允许静默忽略约束；参数校验必须发生在工具执行之前。
- **D5 Design Impact（同意调整）**：由有权限的 TaskBuilder 先按 Workflow V3 更新任务定义（`NONE` → `UPDATE`），再由 Analyst 将批准的设计同步到 Contract 与长期 Design；Coder 不得在实现期自行扩展公共接口。
- **Git 前置条件**：A1/A2 不得落在共享 `develop`；当前会话已在独立 feature 分支 `tool-registry-executor-v1`（与 `origin` 一致，基线 `6902d24`），任务目录完整保留，条件满足。
- **范围限制**：本次不实现 Agent Loop、不调用真实收费 LLM API、不扩大到后续任务。
- **兼容性核对（Analyst）**：与现有 `core` 契约无冲突——`invalid_pairing` 可用于 FAILED、`tool_failure` 要求 CallID 已满足、`cancelled`/`deadline_exceeded` 仍只由父级取消/超时产生；工具级超时归为 `ToolResult.IsError` 属现有「可恢复工具失败」语义的**新增分类行**，需随 D5 写入长期 Design 的 §7 表。

- **D5 前置条件（已于 2026-10-20 满足）**：`task.md` 已由 TaskBuilder 更新——`Design Impact: UPDATE`、Design Artifact 明确为 `docs/design/runtime-architecture.md` 的 §7 / §11 / §12 / §14、新增 AC-006、Out of Scope 明确「不得重写其他章节」「实现阶段不得发明架构决定」。

Analyst 派生实现细节（Owner 已定方向下的机械推导，未单独逐条表态；若不同意请在 Coder 实现前指出）：

1. **空 `RunID` 拒绝**：返回 `invalid_argument`（D1 要求去重限定在 Run 内，空 RunID 无法界定边界）。
2. **重复调用使用 `invalid_pairing`**：与 INV-002「CallID 重复即配对非法」一致，且 `core` 已允许该 Kind 作为 `FAILED`。
3. **`Seal()`**：D3「运行阶段不支持动态热注册」的机械约束方式。

后续步骤（Analyst）：A1 提交本 Contract 与任务文件 → 更新长期 Design（§7 / §11 / §14）→ A2 写入 `state.contract.status=APPROVED`、`contract.target=A1` → push → `gate coder-start` PASS 后交 Coder（Coder 只补 §12 路径登记）。
