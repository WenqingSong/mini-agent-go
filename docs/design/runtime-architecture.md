# Runtime 核心架构（mini-agent-go）

> 本文件是项目级长期设计事实（WHY / architecture）：模块边界、依赖方向、数据模型、状态机、不变量、失败语义与文件所有权。
> 任务内的决策过程与逐条接口契约见 `.agent/tasks/runtime-core-contracts-v1/contract.md`（契约层）与 `.agent/tasks/builtin-agent-tools-v1/contract.md`（内置工具，Owner `APPROVED`，Evidence Commit A1）。
> 两者冲突时：Contract 记录本任务已确认的取舍，本文件记录跨任务长期事实；出现不一致必须回写，不允许长期漂移。
> 本文件中 `contextmgr` 的实现级契约由 `.agent/tasks/agent-context-manager-v1/contract.md` 落实（Owner `APPROVED`，Evidence Commit A1 = `624a094`）。

## 1. 范围

本文件描述 Runtime 的架构基线：L0 `core` 契约层、五个 L1 抽象包与 L2 `builtin` 内置工具包，供 Agent Loop、Provider、Session 持久化、Context 管理、运行日志与装配层共同依赖。

不在本文件范围：具体 Provider 协议、工具沙箱与权限、持久化选型（DB / Redis）、CLI / HTTP 入口、部署流水线。

## 2. 分层与依赖方向

```text
L0  core          公共契约：消息、工具调用、运行状态、错误、配对与校验（仅标准库）
L1  llm           LLM Provider 接口
    tool          Tool / Registry 接口
    session       Session / Todo / 摘要 类型与内存 Session Manager
    contextmgr    上下文预算、窗口与压缩接口
    runlog        运行日志 Sink
L2  builtin       内置工具实现：calculator / mock_search / todo_create / todo_list / todo_complete
L3（未实现）      Agent Loop / 装配层：本文件只声明其依赖方向，尚未交付
```

规则：

- 依赖只能向上看：L1 依赖 L0；L0 不依赖任何 L1；L1 之间零 import。
- 跨模块共享的数据结构一律放在 `core`（例如 `core.ToolSpec` 同时被 `llm.Request.Tools` 与 `tool.Tool.Spec` 使用），以此避免 L1 互相依赖。
- L2 `builtin` 只依赖 L0 `core`、L1 `tool`、L1 `session` 与标准库，不依赖 `llm` / `contextmgr` / `runlog`，也不依赖 L3 装配层。
- L1 不得反向依赖 L2：`session` 不 import `builtin`。Todo 写入端口 `builtin.TodoOperations` 的具体实现（含 Todo ID 生成与状态迁移）由 L3 装配层适配，不由 L1 提供。
- L3 装配层可以依赖任意 L0 / L1 / L2，但 L0 / L1 / L2 不得反向依赖 L3。

机械核对（AC-001）：

```bash
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...
```

判定：`core` 行的 import 不含 `<module>/`；五个 L1 包只出现 `<module>/core`；`builtin` 行只出现 `<module>/core`、`<module>/tool`、`<module>/session`；出现任何其他模块内 import 即违反分层。

根 Module 为 `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零第三方依赖）。外部 `go get` 可用性未验证。

## 3. 模块职责

| 模块 | 职责 | 明确不负责 |
| --- | --- | --- |
| `core` | 统一消息模型、工具调用/结果与配对规则、工具元数据、运行状态与策略、统一错误类型、可独立验证的纯校验 | I/O、日志、调度、Provider 细节 |
| `llm` | 一次 LLM 调用的请求/响应契约与 `Provider` 接口 | HTTP、鉴权、流式、重试、限流、Token 估算 |
| `tool` | `Tool` 执行契约、工具元数据、`Registry` 查找 | 具体工具实现、沙箱、权限、并发调度 |
| `session` | Session / Todo / 摘要数据类型、`Store` 抽象，以及单进程内存 Session Manager（会话生命周期、Turn 执行权与原子提交、过期清理、Todo 生命周期） | 持久化实现、跨进程锁与多副本一致性、摘要生成算法、Agent Loop 调度 |
| `builtin` | 五个内置工具（`calculator`、`mock_search`、`todo_create`、`todo_list`、`todo_complete`）、固定有序 Mock 数据集、`TodoOperations` 写入端口与领域错误码 | `tool.Registry` 实现、工具调度与失败回填、Todo 存储 / ID 生成 / 生命周期、网络与第三方 SDK |
| `contextmgr` | 上下文预算、窗口选择与压缩：在估算预算内构建可送 Provider 的合法消息视图 | 生产 Summarizer（LLM 摘要适配器）、真实 tokenizer、摘要跨 Run 持久化 |
| `runlog` | 运行事件模型、`Sink` 接口与结构化 JSON 记录（字段语义、脱敏与上限） | 文件落盘/滚动策略、采样、上报后端 |

## 4. 数据模型

- 消息是**单一 struct**（`core.Message`）+ 类型化可选字段，不是 content-part 接口多态：`Role`、`Content []ContentPart`、`ToolCalls []ToolCall`、`ToolResult *ToolResult`、`Raw json.RawMessage`。
- 正文统一为 `ContentPart`，当前仅支持文本类型；多模态通过新增 `ContentType` 扩展，不改变 struct 形态。
- `Raw` 保存 Provider 原始载荷，用于日志与回放，契约层不解析。
- 工具调用与结果通过 `ToolCall.ID` / `ToolResult.CallID` 配对；结果是独立的 `RoleTool` 消息，一条消息只承载一个结果，多结果即多条消息。
- 参数 `ToolCall.Arguments` 保持原始 JSON，契约层不解析、不改写，避免在公共契约里绑定 provider 的 schema 语义。

配对规则（可独立验证的不变量）：

1. assistant 消息内 `ToolCall.ID` 非空且唯一；
2. 每个 ToolCall 恰好被一条后续 `RoleTool` 消息引用；
3. 不存在引用未出现过 Call 的孤儿 Result；
4. 违反以上任一条必须返回 `*core.RunError{Kind: invalid_pairing}`，并定位到具体 CallID，不得静默接受。

配对校验是独立入口（`ValidateToolPairing` / `ValidateMessages`），不在构造期强制——Message 是纯数据，构造期强制会迫使所有模块走构造函数，阻碍并行开发。

## 5. 运行状态机

```text
RUNNING ──► COMPLETED        （模型不再要求工具调用）
        ├─► FAILED           （provider_failure / tool_failure(不可恢复) / internal / invalid_*）
        ├─► LIMIT_EXCEEDED   （llm_call_limit / tool_call_limit）
        └─► CANCELLED        （cancelled / deadline_exceeded）
```

- `RUNNING` 是非终态，唯一不带错误的运行状态。
- 四个终态都是“结果”，但语义互斥：不得把取消、超时、预算耗尽归类为工具失败，也不得把预算耗尽伪装成正常完成。
- 「工具失败」不作为独立状态：可恢复工具失败不是终态（通过 `ToolResult.IsError` 回填模型），只有不可恢复且终止 Run 的工具错误才产生 `FAILED + tool_failure`。
- 状态与错误的一致性逐状态校验，不使用“所有终态都必须有错误”的笼统规则：`COMPLETED` 必须无错误；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 必须带匹配的 `ErrorKind`。

## 6. 预算模型

- 计数与限制按**类别**分别进行：`MaxLLMCalls` 与 `MaxToolCalls`，不使用单一 step 预算。
- 两者都是必填正数；非正值属于调用方契约错误（`invalid_argument`），不允许“0 表示不限制”的隐式约定。
- 触限即终态 `LIMIT_EXCEEDED`，错误种类区分是 LLM 上限还是工具上限，便于调用方与日志定位。
- 实际计数（`LLMCalls` / `ToolCalls`）写入 `RunResult`，是运行结果的组成部分，不是日志副产品。

## 7. 失败与一致性语义

| 情形 | 终态 | ErrorKind | 备注 |
| --- | --- | --- | --- |
| 正常完成 | `COMPLETED` | — | 成功不表示任何持久化发生 |
| Provider 调用失败 | `FAILED` | `provider_failure` | 重试属 Provider/Loop 任务 |
| 可恢复工具失败 | 保持 `RUNNING` | — | 以 `ToolResult{IsError:true}` 回填 |
| 单工具局部超时（父 Context 未结束） | 保持 `RUNNING`（终态由 Agent Loop 判定） | — | 执行器返回 `ToolResult{IsError:true, CallID}`；不映射为 `CANCELLED` |
| 参数校验失败 / 未知工具 | 保持 `RUNNING`（终态由 Agent Loop 判定） | — | 同上；工具实现未被调用 |
| 同一 Run 内重复 Tool Call ID | 由 Agent Loop 判定（上抛则 `FAILED`） | `invalid_pairing` | 执行器拒绝再次执行、不复用首次结果；重复 Result 仍由配对校验拒绝 |
| 工具 panic | `FAILED`（由 Agent Loop 判定） | `tool_failure` | 执行器恢复为 `*RunError`，保留 `CallID`，不记录堆栈 |
| 不可恢复工具失败终止 Run | `FAILED` | `tool_failure` | 必须带 `CallID` |
| LLM 调用达上限 | `LIMIT_EXCEEDED` | `llm_call_limit` | |
| 工具调用达上限 | `LIMIT_EXCEEDED` | `tool_call_limit` | |
| 用户取消 | `CANCELLED` | `cancelled` | 保留已有消息，不回滚 |
| 整体超时 | `CANCELLED` | `deadline_exceeded` | 与用户取消靠 Kind 区分 |
| 消息序列非法 | `FAILED` | `invalid_pairing` / `invalid_argument` | 在进入下一次 LLM 调用前拒绝 |

一致性范围：本契约层无多存储、无消息队列、无异步复制，因此不存在跨系统最终一致性问题。Run 内的部分完成（一次响应中多个 ToolCall 只部分成功）必须通过“每个 Call 各自产生 Result”表达，不允许丢弃成功结果或静默跳过失败调用。重复 Result 由配对校验拒绝，不做静默覆盖。

工具执行层的 Context 边界：工具忽略 `Context` 时，执行器只保证**调用方**在超时/取消后及时返回；不保证终止工具 goroutine、不保证副作用被撤销或回滚、不保证该 goroutine 一定结束。被放弃 goroutine 的后续结果必须丢弃，不得影响同键后续调用。

## 8. 会话归属与工具身份

- Session 是消息历史、摘要与 Todo 的**唯一所有者**；Run 只是通过 `ExecutionRef{SessionID, RunID}` 引用会话。
- 工具执行身份通过显式参数传递（`tool.Request` 内嵌 `ExecutionRef` + `core.ToolCall`），不使用 `context.Value` 承载业务身份；`context.Context` 只承担取消与超时。
- 不向 Tool 传递可变的 `Session`、`Todo` 或 `Store`：Tool 若需要会话状态，必须由装配层显式注入能力或传入值，避免工具绕过运行时直接改写会话状态。
- 注入会话能力时使用受限接口而不是数据副本：Todo 的读写能力是 `session.TodoOperations`（由 `*Turn` 实现），摘要写入是 `Turn.SetSummary`。注入的是能力接口，不违反 INV-005。

### 8.1 内存 Session Manager（`session` 模块内的单进程实现）

- 事实源：`session.Manager` 在进程内存中持有会话，是 `session.Session` 的唯一写入口；`Store`（`Load` / `Save`）由 `*Manager` 实现。**无持久化**：进程退出即丢失，重启恢复属后续任务。摘要属于 Session 数据，由 Context Manager 生成/更新，Manager 只负责存储。
- 执行权：每个会话一把**可被 `context` 取消的**执行权（容量 1 的信号量），同一会话的 Turn 串行，不同会话互不阻塞；禁止跨会话全局锁，`map` 锁不得跨越调用方回调持有。
- Turn 与提交：`Manager.RunTurn` 在会话执行权内执行调用方回调，回调通过 `*Turn` 写入**隔离暂存区**（消息追加 `Append`、已提交历史前缀删除 `DropLeading`、摘要 `SetSummary`、Todo）；提交是**全量替换**——`Turn.staged()` 拼装「剩余已提交历史 + 本 Turn 暂存消息」后整体发布，只有「`ctx` 未取消 + 回调成功 + Turn 仍打开 + 会话仍是当前 entry + 提交前校验通过」才整体提交。失败、取消、超时、panic 都不产生部分提交，也不刷新 TTL。
- 前缀删除语义（`Turn.DropLeading(n)`）：`n` 是本次调用新删除的条数，作用于**当前剩余**的已提交历史（`Turn.base`，进入 Turn 时的私有深拷贝）；同一 Turn 内累计删除量单调不减、每次 `0 <= n <= len(base)`，`n=0` 为空操作，`n=len(base)` 允许清空（含提交空历史）；已删除消息不能通过后续调用恢复。删除边界必须使「剩余已提交历史 + 本 Turn 暂存消息」通过 `core.ValidateMessages`，否则该次调用以 `invalid_pairing`（带 `CallID`）原子拒绝；本 Turn 经 `Append` 新增的消息永不被删除、移动或重排。拒绝不关闭 Turn，也不改变 `base` / 暂存消息 / 摘要 / Todo。
- 读取隔离：`Get` / `Load` / `Turn.Session` / `Turn.Todos` 返回深拷贝快照，调用方修改不影响会话；Turn 内的读取看到「已提交 + 本 Turn 已暂存」的视图（read-your-writes）。
- 生命周期：TTL 由配置提供，创建与**成功提交**刷新，普通读取不续期；无后台清理 goroutine，过期判定为惰性访问 + 显式 `PurgeExpired`。过期以「能取得执行权且已过 TTL」为唯一判定，因此正在执行的 Turn 不被清理破坏；过期即物理删除。
- 重入约束：Turn 内运行的 Tool / Context Manager 只能使用注入的 Turn 能力，**不得持有 `*Manager` 并回调其 `RunTurn` / `Delete` / `Save`**（会重入同一会话执行权而死锁）。
- 会话级错误：不存在 / 已过期 / 重复创建用 `session` 包哨兵（`ErrSessionNotFound` / `ErrSessionExpired` / `ErrSessionExists`）+ `*core.RunError{Kind: KindInvalidArgument}` 表达，调用方用 `errors.Is` 细分；不新增 `core.ErrorKind`，也不构成 Run 终态。

内置工具侧的落地规则（`builtin`）：

- 会话身份唯一来源是可信的 `tool.Request.SessionID`：`todo_*` 工具每次调用都把 `req.SessionID` 显式传给端口；`SessionID` 为空时拒绝执行（`missing_session_id`）且不调用端口。
- 模型不得经 JSON 参数选择会话：`Arguments` 中出现 `session_id` / `sessionId` / `session` 一律 `invalid_arguments`。
- Todo 工具不持有 Todo 状态副本、不生成 ID、不做读改写：`Create` / `List` / `Complete` 的会话作用域、ID 生成、状态迁移与幂等「完成」全部由注入的 `builtin.TodoOperations` 实现方完成；端口以 `builtin.ErrTodoNotFound` 表示「本会话不存在该待办」。
- 失败分级：领域失败返回 `core.ToolResult{IsError: true}` 且 `error` 为 nil，内容为 `{"error":{"code","message"}}`；基础设施失败（端口返回非领域错误、`ctx` 已结束、端口未注入）返回 `*core.RunError`，原始错误只进 `Cause`，不得回显内部细节。
- 装配层（T07）负责把 `session.TodoOperations` 适配成 `builtin.TodoOperations`，并承担 Todo ID 生成与状态迁移；`builtin` 不 import `session` 的实现，`session` 也不 import `builtin`。

## 9. 上下文管理（contextmgr）

- 职责边界：决定“送进 LLM 的消息视图”——token 估算、窗口选择、压缩。以下为实现级契约（T05，Owner `APPROVED`，R1 修订 A1' = `8e84932`）。
- 公开契约：
  - `Request{ExecutionRef, SystemPrompt, PreviousSummary, Messages, TokenBudget, KeepRecent}`；`Result{Messages, Compacted, DroppedMessages, Summary}`；`Manager.Build(ctx, Request) (Result, error)`；`NewManager(estimator, summarizer) (Manager, error)`。
  - `Messages` 只允许 `user` / `assistant` / `tool`；System Prompt 与历史摘要通过 `SystemPrompt` / `PreviousSummary` 显式提供，`Messages` 携带 `RoleSystem` 即 `invalid_argument`。
  - `Estimator` 接口 + 默认 `HeuristicEstimator`（4 字符/token 启发式，可注入替换）；`TokenBudget` 是**估算预算**（`<= 0` 不限制），不宣称等于真实模型 token 数；工具 Schema、模型输出等预算由调用方在视图之外预留。
  - `Summarizer` 接口（`Summarize(ctx, SummarizeRequest) (string, error)`）产出纯文本摘要；仓库内只提供测试用 Fake，生产 LLM 适配器归 T07 且其调用计入 `RunPolicy`。
- 压缩语义：`TokenBudget` 为唯一触发阈值——完整视图估算未超预算时保持原样（`Compacted=false`、`DroppedMessages=0`、`Summary=""`）；超预算时按原子单元（assistant 消息携带的 ToolCalls 与其全部 ToolResults 同属一个单元）整体压缩保留尾部之前的最老历史，保留不少于 `KeepRecent` 条的近期消息。`KeepRecent` 是硬下限，不通过继续删减保留消息凑预算；单次 `Build` 只调用一次 `Summarizer`，压缩后仍超预算即 `invalid_argument`。System Prompt 与摘要永不压缩。
- 跨轮次持久化（供 Session Manager 使用）：`Result.Summary` 是新摘要的权威文本；被覆盖消息恰为 `req.Messages` 的前 `DroppedMessages` 条；`Compacted=true` 时调用方以 `Summary` 全量替换旧摘要并删除前 `DroppedMessages` 条消息，`Compacted=false` 时保留旧摘要不变。`contextmgr` 不读写 Session Store。
- 失败语义：预算不可满足（含 System Prompt、`KeepRecent` 目标、摘要本身）→ `invalid_argument`；`Summarizer` 失败或空/全空白摘要 → `internal`（保留 Cause）；输入消息结构/配对非法 → 原样返回 `core` 错误；`ctx` 取消/超时 → `cancelled` / `deadline_exceeded`。错误路径返回零值 `Result`，不返回部分视图，调用方不得据此修改持久化状态。
- 输出视图仍必须是合法的配对序列（`core.ValidateMessages`），不得因压缩产生孤儿 Result。
- 摘要（`session.Session.Summary`）由 Session 持有：Context Manager 只负责生成与更新（经 Turn 能力写入，随 Turn 原子提交），不自行维护一份摘要状态。

## 10. 运行日志（runlog）

- 日志是**观察者**：`Sink.Record` 返回错误不得改变 Run 状态、计数或消息序列（旁路原则）。写入失败、Handler 返回错误或 panic、`ctx` 已取消、载荷异常时不得 panic，也不得修改传入的 `Event` 与 `Data`。
- 事件以 run 为单位组织（`ExecutionRef` + step + `CallID` + 已脱敏 `Data`）。耗时与终态由发出端随事件传入：`Event.Duration` 由发出端测量，输出为毫秒（`duration_ms`）；`Event.Status` 只允许 `run_finished` 等适用事件携带，Sink 不合成终态——工具调用成功不代表整个 Run 完成。
- 记录形态：每条事件 = 一行 `log/slog` JSON（`msg=run_event`、`level=INFO`），字段固定为 `session_id`、`run_id`、`event`、`step`、`call_id`、`duration_ms`、`status`、`error_kind`、`op`、`error_message`、`summary`、`data`（不适用时省略；`call_id` 取事件自带的 `Event.CallID`，为空且 `Error != nil` 时回退 `Error.CallID`，Sink 不从 `Data` 或名称推断调用身份）；`status` 空且无错误时为 `RUNNING`，有错误时由 `core.StatusOf` 推导；`error_kind` 取 `core.KindOf`，因此 `cancelled` 与 `deadline_exceeded` 是两个不同取值。`Error.Cause`、堆栈与原始错误对象不进入日志。
- 脱敏边界（Sink 侧防御性脱敏，发出端仍必须先脱敏）：`Data` 键名归一化（小写，去 `_`、`-`、`.`、空格）后按固定优先级判定——① 允许清单原样输出值：`model`、`provider`、`finish_reason`、`stop_reason`、`name`、`tool`、`tool_name`、`is_error`、`ok`、`status`、`attempt`、`max_attempts`、`retry_count`、`usage`、`input_tokens`、`output_tokens`、`total_tokens`、`cached_tokens`、`compacted`、`dropped_messages`、`message_count`、`tool_call_count`、`id`、`index`、`truncated`；② 敏感键替换为 `[REDACTED]`（`apikey`/`secret`/`token`/`password`/`credential`/`privatekey` 等片段，或 `authorization`/`auth`/`cookie` 等键，含大小写与命名变体）；③ 正文键输出 `[REDACTED:len=N]`：`content`、`text`、`messages`、`prompt`、`input`、`raw`、`body`；④ 未知键整键省略。字符串值命中 `Bearer`/`Basic` 凭据模式时整体替换为 `[REDACTED]`。允许清单是封闭集合，新增业务字段必须同步本节。
- 上限执行：深度 > 8、或节点预算 1024 耗尽（按对象键字典序遍历，超出部分替换为 `[TRUNCATED]`）；单字符串 > 256 字节截断至 256 字节（UTF-8 边界）并追加 `…`；`Data` 原始字节 > 64 KiB 或非法 JSON/顶层非对象时不输出 `data`，只置 `data_truncated` / `data_invalid`。
- 并发：每条记录必须是可独立解析的完整单行且不交织；由单一 `slog.Handler` 实例保证（标准库 `JSONHandler` 以共享 mutex 串行写），Sink 自身无可变状态。
- 运行日志不是会话事实来源：消息历史归 Session，运行结论归 `RunResult`。本节固定 JSON 记录格式与字段语义；文件落盘位置、滚动/轮转、采样、异步批量上报与日志检索仍属后续任务。

## 11. 不变量

| 编号 | 不变量 | T00 证据 |
| --- | --- | --- |
| INV-001 | 分层方向：`core` 仅标准库；L1 仅依赖 `core`；L1 互不依赖 | `go list` import 核对 |
| INV-002 | 配对完整性：Call 与 Result 一一对应，非法配对必须报 `invalid_pairing` | 表驱动测试（0/1/N 调用 + 三类非法配对） |
| INV-003 | 状态/错误逐状态一致（`RUNNING`、`COMPLETED` 无错误；三个失败终态必须带匹配 Kind） | 映射表正反用例 |
| INV-004 | 双预算：两个上限都必须为正，计数值不越界，触限只能 `LIMIT_EXCEEDED` | 策略校验 + 映射测试 |
| INV-005 | 会话归属：Session 独占消息、摘要与 Todo，Tool 只拿 ID 或受限能力接口（不持会话状态副本） | 签名核对 + 能力注入约束（`session.TodoOperations` 由 `*Turn` 实现，见 §8.1）+ 工具层测试（运行期证据归 Agent Loop 集成 / 装配任务） |
| INV-006 | 取消/超时只能 `CANCELLED`，靠 Kind 区分 | 映射测试（运行期中断行为归 Loop 任务） |
| INV-007 | 可恢复工具失败回填继续；不可恢复才 `FAILED + tool_failure` | 映射测试（单工具级分类见 INV-010，跨 step 决策归 Loop 任务） |
| INV-008 | 日志旁路：Sink 失败不改变 Run 结果 | `Discard` 副作用测试（运行期旁路归 Loop 任务） |
| INV-009 | 单工具执行结果保留 `CallID`：`ToolResult.CallID` 或 `*core.RunError.CallID` 必等于请求 `Call.ID` | `tool/executor_test.go`（成功 / 工具 error / 超时 / panic / 取消 + 空 CallID 归一化） |
| INV-010 | 单工具失败归类：工具级超时、工具 `error`、参数与名称校验失败 → `ToolResult{IsError:true}`；panic → `tool_failure`；父 Context 取消/整体超时 → `cancelled`/`deadline_exceeded`，且取消优先于工具结果；不新增 `ErrorKind` | `tool/executor_test.go` 表驱动 + `tool/fake_test.go` |
| INV-011 | 执行前把关：未知工具、参数非法、重复 Call ID、名称或 RunID 非法时 `Tool.Execute` 不被调用 | `tool/executor_test.go` 调用计数表驱动 + `tool/validate_test.go` |
| INV-012 | 去重范围 = 同一 Run（`RunID + CallID`）内同一 CallID 至多执行一次，重复提交返回 `invalid_pairing` 且不复用首次结果；不同 Run 相互独立；并发同键不产生重复副作用 | `tool/executor_test.go`（同键 / 跨 Run / 并发 `-race`） |
| INV-013 | Registry 定义不可篡改：并发安全；`Specs()` 按名称稳定排序并返回深拷贝；空 `Parameters` 规范化为 `{"type":"object","properties":{}}`；`Seal` 之后拒绝注册 | `tool/registry_test.go` + `tool/validate_test.go` |

### 11.1 内置工具不变量（`builtin-agent-tools-v1`，任务局部编号）

本小节使用任务局部编号，不占用全局 `INV-0xx` 号段；全局重编号在集成时统一处理。

| 编号 | 不变量 | 证据 |
| --- | --- | --- |
| INV-B01 | 工具身份回填：结果恒满足 `CallID == req.Call.ID`、`Name == Spec().Name`；五个工具名唯一 | `builtin/spec_test.go` |
| INV-B02 | 失败分级：领域失败只表现为 `IsError=true` + `error==nil`；只有端口错误、`ctx` 已结束、端口未注入才返回 `*core.RunError` | 表驱动失败用例 |
| INV-B03 | 计算有限性：`calculator` 任何成功结果的 `result` 都是有限 `float64`；除数为 0（含 `-0`）必然失败 | 数值边界用例 |
| INV-B04 | 搜索确定性：同一查询 `Content` 逐字节一致、顺序等于数据集顺序；无命中为空数组且 `IsError=false`；成功结果都含 `"mock":true` | 重复调用与大小写用例 |
| INV-B05 | 会话作用域来源唯一：只由 `req.SessionID` 决定；空 `SessionID` 拒绝且不调端口；会话字段一律拒绝；跨会话标识映射为 `todo_not_found` 且无副作用 | `builtin/todo_test.go` |
| INV-B06 | 工具层并发：工具实例无共享可变状态，可并发 `Execute`；并发安全端口下并发创建 N 条恰为 N 条且标识唯一，重复完成终态唯一；`-race` 干净 | `builtin/todo_concurrency_test.go`（不构成真实会话隔离证据） |
| INV-B07 | 无生产存储：`builtin` 生产代码不含 Todo 存储或 ID 生成；端口实现只出现在测试与后续装配任务 | 代码核对 |
| INV-B08 | 依赖方向：`builtin` 只 import 标准库 + `core` / `tool` / `session`；`core/**`、`tool/tool.go`、`session/session.go` 未变 | `go list` + `git diff` |
| INV-B09 | Schema 子集：五个 `Parameters` 只用 `type` / `properties` / `description` / `enum` / `required` | 机械遍历 schema 键集合 |

### 11.1 会话管理不变量（`session` 内存实现）

来源：`agent-session-manager-v1` 的 APPROVED `contract.md`（该 Contract 内编号 INV-001—INV-008 依次对应下表 INV-009—INV-016；Contract INV-009「分层与模型稳定」由本表 INV-001 与 §3 职责共同覆盖）。

| 编号 | 不变量 | 运行期证据 |
| --- | --- | --- |
| INV-009 | 提交原子性：一次 Turn 的消息、摘要与 Todo 变更要么整体生效、要么整体不生效 | 失败/取消/超时/panic 用例断言历史与内容哈希不变 |
| INV-010 | 会话串行：同一会话至多一个 Turn 执行回调；提交后消息组连续不交错，总数等于各成功 Turn 之和 | 并发写入测试（`-race -count=5`）+ 分组连续性扫描 |
| INV-011 | 跨会话独立：无跨会话全局锁，不同会话可同时执行，一个会话的变更/删除/过期不影响其他会话 | 双 Turn 交叉同步点测试 + 可逆 mutation（改成全局锁必须失败） |
| INV-012 | 读隔离与明确失败：读取返回深拷贝快照；不存在/已过期用哨兵错误表达并可 `errors.Is` 区分 | 读写隔离、`errors.Is` 表驱动 |
| INV-013 | 校验原子：非法消息序列或非法 Todo 被拒绝时历史与 Todo 列表逐条不变 | 孤儿/缺失 Result、非法 Todo 的表驱动 + 前后哈希比对 |
| INV-014 | 执行权不泄漏：正常、错误、取消、超时、过期、删除、panic 路径都释放执行权 | 取消/超时后新请求能取得执行权；panic 用例 |
| INV-015 | 过期不破坏在途 Turn：以「可取得执行权 + 超过 TTL」判定过期；成功提交刷新 TTL，失败/取消不刷新 | 注入时钟 + 并发 `PurgeExpired` 测试 |
| INV-016 | Todo 单一事实源与暂存可见性：正式 Todo 只有 `Session.Todos` 一份；Turn 内后续操作能看到未提交修改；Turn 关闭后写入被拒 | `PutTodo` 后立即读、关闭后写入用例 |
| INV-017 | 前缀删除：`Turn.DropLeading(n)` 只作用于进入 Turn 时的已提交历史前缀，累计删除量单调不减且不超过该历史长度；本 Turn `Append` 的消息不被删除或移动；每次成功返回后「剩余历史 + 暂存消息」满足 `core.ValidateMessages`；拒绝是单次调用原子拒绝且不关闭 Turn；Turn 关闭后写入被拒 | `session/turn_test.go`（删除合法域、配对边界拒绝、`DropLeading`+`SetSummary`+`Append` 组合提交、失败/取消不提交、关闭后拒绝）+ `session/concurrency_test.go`（Turn 内并发 `-race`） |

INV-017 来源：`session-turn-drop-leading-v1` 的 APPROVED `contract.md`（该 Contract 内 INV-001—INV-006 由本条覆盖）。

## 12. 文件所有权

`go.mod` 与 `Makefile` 是**工程公共文件**，不属于任何业务模块，只由契约层任务或 Owner 授权变更。

| 路径 | 归属 | 说明 |
| --- | --- | --- |
| `go.mod` | 工程公共 | module + go 版本，零 require |
| `Makefile` | 工程公共 | `test` / `build` / `vet` / `test-engine` |
| `core/message.go` | core | Role / ContentType / ContentPart / Message / `Message.Validate` |
| `core/toolcall.go` | core | ToolCall / ToolResult / ToolSpec / `ToolResult.Validate` |
| `core/run.go` | core | ExecutionRef / RunStatus / IsTerminal / RunPolicy / RunResult 及校验 |
| `core/error.go` | core | ErrorKind / RunError / NewError / KindOf / StatusOf |
| `core/pairing.go` | core | `ValidateToolPairing` / `ValidateMessages` |
| `core/message_test.go` | core | 消息与多 ToolCall 场景 |
| `core/pairing_test.go` | core | 合法/非法配对表驱动 |
| `core/run_test.go` | core | 状态×错误映射与预算校验 |
| `llm/provider.go` | llm | Provider / Request / Response / Usage / FinishReason |
| `llm/provider_test.go` | llm | `Response.Validate` 边界 |
| `llm/openai/client.go` | llm | OpenAI 兼容 `llm.Provider`：配置校验、单次请求、失败语义 |
| `llm/openai/wire.go` | llm | Chat Completions 请求/响应 wire 结构与字段映射 |
| `llm/openai/client_test.go` | llm | `httptest` 覆盖请求构造、响应解析与错误注入 |
| `tool/tool.go` | tool | Tool / Request / Registry |
| `tool/registry.go` | tool | `MapRegistry` / `NewMapRegistry` / `Register` / `Seal` / `Lookup` / `Specs` 与注册哨兵错误 |
| `tool/validate.go` | tool | JSON Schema 子集注册校验、调用期参数校验与错误文本 |
| `tool/executor.go` | tool | `Executor` / `ExecutorOptions` / 同 Run 去重占位 / 超时、取消与 panic 处理 |
| `tool/registry_test.go` | tool | 注册拒绝、查找、`Specs` 深拷贝与排序、`Seal`、并发访问 |
| `tool/validate_test.go` | tool | Schema fail-closed 与参数校验表驱动 |
| `tool/executor_test.go` | tool | 执行结果、失败归类、去重范围与 goroutine 收敛 |
| `tool/fake_test.go` | tool | Fake Tool 与测试辅助（调用计数、watchdog、请求构造） |
| `session/session.go` | session | TodoStatus（含四个导出常量）/ Todo 与 `Todo.Validate` / Session（含 `Summary`）/ Store |
| `session/manager.go` | session | ManagerConfig / Manager / Create·Get·Delete·PurgeExpired·RunTurn / 会话执行权 |
| `session/turn.go` | session | Turn / TodoOperations 实现 / 暂存视图、`DropLeading` 前缀删除与原子提交 |
| `session/errors.go` | session | ErrSessionNotFound / ErrSessionExpired / ErrSessionExists |
| `session/manager_test.go` | session | 会话 CRUD 与隔离、Store 适配、不存在/重复创建错误 |
| `session/turn_test.go` | session | 提交条件、暂存可见性、非法拒绝原子性、前缀删除语义与拒绝边界、Turn 关闭、panic |
| `session/concurrency_test.go` | session | 同会话串行、跨会话并发、Turn 内并发写入、无跨会话全局锁 |
| `session/expiry_test.go` | session | TTL 续期、惰性过期、`PurgeExpired` 与在途 Turn |
| `session/cancel_test.go` | session | 排队取消/超时、执行中取消、执行权释放 |
| `builtin/builtin.go` | builtin | 包文档 / 工具名与错误码常量 / 成功与失败 Content 编码 / 参数解码与会话字段拒绝 |
| `builtin/calculator.go` | builtin | `calculator` 工具实现 |
| `builtin/search.go` | builtin | `mock_search` 工具实现 |
| `builtin/search_data.go` | builtin | 固定有序 Mock 数据集 |
| `builtin/todo.go` | builtin | `TodoOperations` 端口 / `ErrTodoNotFound` / 三个 todo 工具 |
| `builtin/spec_test.go` | builtin | Spec schema 子集、工具名唯一、`All` 聚合、身份回填 |
| `builtin/calculator_test.go` | builtin | `calculator` 成功与失败/数值边界 |
| `builtin/search_test.go` | builtin | `mock_search` 确定性与边界 |
| `builtin/todo_test.go` | builtin | todo 工具会话作用域、领域/基础设施失败分级 |
| `builtin/todo_concurrency_test.go` | builtin | 工具层并发与测试专用 Fake |
| `contextmgr/contextmgr.go` | contextmgr | Request / Result / Manager |
| `contextmgr/estimator.go` | contextmgr | Estimator / HeuristicEstimator |
| `contextmgr/summarizer.go` | contextmgr | SummarizeRequest / Summarizer |
| `contextmgr/manager.go` | contextmgr | NewManager 与构建/压缩逻辑 |
| `contextmgr/estimator_test.go` | contextmgr | `Estimator` / `HeuristicEstimator` 边界 |
| `contextmgr/manager_test.go` | contextmgr | `Build` 的预算、保留、原子单元与失败语义 |
| `runlog/runlog.go` | runlog | EventKind（含六个常量）/ Event（含 `Duration`、`Status`）/ Sink / Discard |
| `runlog/slog.go` | runlog | `NewSlogSink` / `NewJSONSink`、记录构建、panic 恢复与错误返回 |
| `runlog/sanitize.go` | runlog | 允许清单、敏感/正文/未知键规则、深度与节点上限、键序稳定遍历 |
| `runlog/runlog_test.go` | runlog | `Discard` 无副作用 |
| `runlog/slog_test.go` | runlog | 字段与关联性、失败与超时定位、旁路与异常隔离、并发记录 |
| `runlog/sanitize_test.go` | runlog | 脱敏表驱动：嵌套、未知键、超大输入、泄漏扫描 |

规则：

- 每个源文件只归属一个模块；同一模块的后续维护任务才能修改其文件。
- 实现确需新增或重命名文件时，必须在同一交付内回写本表；表内路径与实际 `git ls-files` 不一致视为 AC-006 失败。
- 禁止在模块目录外新增业务源码；禁止跨模块混放（如把 `tool` 接口写进 `core`）。

## 13. 工程入口

- `make test` → 根模块 `go test ./...`，与 `.cnb.yml` 的 `pr-check` 入口一致。
- `make build` → `go build ./...`；`make vet` → `go vet ./...`。
- `make test-engine` → `GOWORK=off go -C .agent/engine test ./...`：`.agent/engine` 是独立 Module，根模块 `./...` 不会包含它（点目录 + 嵌套 Module 都被剪枝），因此必须单独运行。
- 不使用 `go.work` / `replace` 关联 Engine 与业务 Module。

## 14. 未验证事项与后续任务边界

未验证：

- `cnb.cool/framework-station/mini-agent-go` 的外部 `go get` 可用性。
- INV-006 / INV-007 / INV-008 的运行期行为（T00 只有签名、映射函数与契约级测试）；INV-005 现由 §8.1 的能力注入约束与 `session` 测试补充证据。
- INV-009—INV-013 只有 `tool` 包单元测试证据，尚未在 Agent Loop 中集成验证（集成与终态映射归 Agent Loop 任务）。
- INV-B05 / INV-B06 的真实会话隔离、跨会话不可写、Todo 生命周期与持久化并发：`builtin` 只有测试专用 Fake 证据，运行期证据归 Agent Loop 集成与装配层。
- Todo 端到端行为（模型发起 `todo_*` 调用到真实 Todo 落库）：需要 T07 把 `session.TodoOperations` 适配成 `builtin.TodoOperations` 后才可验证。
- `HeuristicEstimator` 与真实模型 token 数的偏差幅度未验证（按 4 字符/token 估算，对 CJK 偏低）；`TokenBudget` 已固定为估算预算，精确校准依赖真实 tokenizer 评估（T07）。
- 日志记录在真实 Agent Loop 中产生是否完整、调用方是否真正隔离日志错误，尚未验证；本仓库目前只有 Sink 层的进程内证据。
- `-race` 与 `go test -race` 未进入 CI（`.cnb.yml` 只有 `make test`），会话并发不变量依赖本地/审查时人工执行。
- `session` 内存实现无持久化，进程重启数据丢失是既定行为，不做恢复验证。

归属后续任务：

| 内容 | 归属 |
| --- | --- |
| 取消/超时在 Loop 中的实际中断点与消息保留策略 | Agent Loop 任务 |
| 跨 step 的 `RunStatus` 终态映射、是否继续/重试决策、同 step 多调用顺序与并发 | Agent Loop 任务 |
| 真实运行期事件完整性、日志旁路在 Agent Loop 中成立（发出点接线与调用方隔离日志错误） | Agent Loop 任务 |
| `session.Store` 的持久化实现（DB / Redis）、重启恢复与数据迁移 | Session 持久化任务 |
| `builtin.TodoOperations` 的适配实现、Todo ID 生成、状态迁移、`ErrTodoNotFound` 映射 | 装配层任务（T07） |
| `session.TodoOperations` 与 `builtin.TodoOperations` 两套端口的集成、真实会话隔离与 Todo 端到端验收 | 装配层 + 集成验收 |
| token 预算、窗口截断、压缩算法 | 已完成（T05，见 §9） |
| 生产 LLM `Summarizer` 适配器（调用计入 `RunPolicy`）、真实 tokenizer 与估算校准 | T07 摘要/Provider 集成 |
| Provider 重试、限流、streaming、鉴权 | Provider 任务 |

归属收窄（2026-10-20，`tool-registry-executor-v1`）：单工具级失败分类（工具级超时、工具 `error`、参数校验失败、未知工具、panic）、参数校验、Registry 生命周期与同一 Run 内重复 Call ID 去重已落入 §7 / §11，不再属于 Agent Loop 任务；Agent Loop 只承担跨 step 的 `RunStatus` 终态映射、是否继续/重试决策与同 step 多调用顺序并发。

归属收窄（2026-10-20，`runtime-observability-v1`）：Sink 层旁路（写入失败、Handler 异常、`ctx` 取消、异常载荷）与记录完整性（单行 JSON、并发不交织、字段语义与脱敏）已落入 §10，不再属于 Agent Loop 任务；Agent Loop 仅承担发出点接线、事件完整性以及调用方隔离日志错误。

## 15. 演进规则

- 公共契约变更必须走 Contract Revision 并由 Owner 重新 `APPROVED`，随后同步更新本文件；旧版本由 Git 历史承载，本文件始终是最新权威。
- 后续任务不得在实现中私自扩展公共符号（新增类型、字段、方法）；需要时先回到 Contract。
- 本文件不记录任务级决策过程（谁在何时选了哪个方案），只记录对后续任务有约束力的架构事实。
