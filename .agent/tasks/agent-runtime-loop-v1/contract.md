# Technical Contract

任务：`agent-runtime-loop-v1`（T07 / M2-agent-runtime-loop）
基线：`36e2292`（TaskBuilder 基线 commit，父提交 `dc734d2`），工作区干净，`go test ./...` 8 包全绿。

## Decision Status

WAITING_FOR_OWNER_DECISION

## Problem

Task 要求在 T00–T06 已交付的 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` 之上交付 L3 Agent Loop：一次 Run 完成 LLM → Tool → LLM 循环，并在双预算、整体超时与取消下给出准确终态与可校验消息历史。

阻止直接实现的问题（每条都会长期约束后续任务）：

1. **提交事务边界**：`session.Manager.RunTurn` 在 `ctx` 已取消时不提交（P1），而 Design §7 写「用户取消：保留已有消息，不回滚」，AC-004 又要求「失败/取消不产生部分提交」；同时 `Turn.Append` 要求「携带 ToolCalls 的 assistant 消息与其全部 `RoleTool` 结果同批提交」。三者必须收敛为一条可测试策略。
2. **AC-007 的压缩移除无可用的写入能力**：Design §9 要求「`Compacted=true` 时调用方删除前 `DroppedMessages` 条消息」，但 `session.Turn` 只有 Append / SetSummary / Todo 能力，**无法删除已提交基线的前缀**；`Save` 在 `RunTurn` 内会重入执行权死锁。这是一个真实的公共 API 缺口，非实现细节。
3. **预算与调度规则未定**：计数在调用前还是调用后、触限那次是否发出、单响应 N 个 Call 而剩余预算不足 N 如何处理、多 Call 顺序还是并发。
4. **终态判定与失败分类**：`Provider` error、`Response.Validate` 失败、`Executor` 的各类 `RunError`、`finish_reason` 矛盾响应分别落到哪个终态。
5. **入口形态与执行器生命周期**：`Run` 是否返回 `error`、`Loop` 是否并发安全、`tool.Executor` 去重范围如何恰好等于「一次 Run」。

## Verified Current Behavior

- VERIFIED：`core` 已有 `RunStatus`（`RUNNING` + 4 终态）、`RunPolicy.Validate`（两者必须 > 0）、`RunResult{ExecutionRef, Status, LLMCalls, ToolCalls, Messages, Error}` 与逐状态 `Validate`：`COMPLETED`/`RUNNING` 必须无错误；`FAILED` 只接受 `provider_failure`/`tool_failure`/`internal`/`invalid_argument`/`invalid_pairing`，且 `tool_failure` 必须带 `CallID`；`LIMIT_EXCEEDED` 只接受两个 limit 种类；`CANCELLED` 只接受 `cancelled`/`deadline_exceeded`（`core/run.go:80`）。全仓仍**没有任何代码构造完整 `RunResult`**。
- VERIFIED：`llm.Provider.Complete(ctx, Request) (Response, error)`；`Response.Validate` 要求 assistant 角色、消息结构合法、`FinishReason` ∈ {stop, tool_calls, length, unknown}、用量非负（`llm/provider.go:59`）。`FinishReason` 常量未导出，包外只能写字面量 `llm.FinishReason("tool_calls")`（同包风格见 `llm/openai/wire.go:16`）。
- VERIFIED：`tool.Executor.Execute` 返回值语义（`tool/executor.go:58`）：未知工具 / 参数非法 / 工具级超时 / 工具实现返回 error → `ToolResult{IsError:true}, err==nil`（可恢复）；`panic` → `*RunError{tool_failure, CallID}`；父 `ctx` 结束 → `cancelled`/`deadline_exceeded`，且**取消优先于工具结果**；去重按 `(RunID, CallID)` 占位、不释放、**只对同一 `Executor` 实例有效**，重复提交返回 `invalid_pairing` 且不复用首次结果。
- VERIFIED：`session.Manager.RunTurn` 仅在「`ctx` 未取消 + 回调返回 nil + Turn 仍打开 + entry 仍是当前 + 提交前 `ValidateMessages` 通过」时整轮提交；`ctx` 在回调返回前结束即整轮丢弃；排队等待执行权被取消时 `fn` 不执行（`session/manager.go:188`、`399`）。回调错误若是 `*core.RunError` 原样返回（`session/errors.go:83`）。
- VERIFIED：`session.Turn` 的能力集只有 `Append`（每批都对「base + 本 Turn 暂存 + 新批」跑 `core.ValidateMessages`，非法即整批拒绝）、`SetSummary`、`SetTodos`/`PutTodo`/`DeleteTodo`、只读视图 `Session()`/`Todos()`（`session/turn.go:76`）。**没有删除或重写已提交基线消息的入口**。
- VERIFIED：`contextmgr.Build` 超预算时按原子单元压缩最老历史，返回 `Messages`（System Prompt 为首条 system 消息、新摘要次之）、`Compacted`、`DroppedMessages`（恰为输入 `Messages` 的严格前缀条数）、`Summary`；错误路径返回零值 `Result`（`contextmgr/manager.go:48`）。`Messages` 输入不允许 `RoleSystem`。
- VERIFIED：`runlog` 已有 6 个 `EventKind`、`Event{Kind, Ref, Step, CallID, Summary, Data, Error, Duration, Status}`、`Sink`、`Discard`、`NewJSONSink(io.Writer)`；`Data` 允许清单是封闭集合（`runlog/runlog.go`、`runlog/slog.go:40`、Design §10）。全仓 6 类事件**均无发出方**。
- VERIFIED：`llm/openai` 适配器会把 `Messages` 中的 system 角色消息正常映射（`llm/openai/wire.go:173`），因此「摘要仍以 system 消息留在 `Messages`」对真实 Provider 可用；该适配器把未知 `finish_reason` 归为 `provider_failure`，不会产出 `unknown`。
- VERIFIED：现有测试全部为包内 `_test.go`（`package tool` / `package session` 等），测试替身均未导出、不可跨包复用。
- UNKNOWN：真实 Provider 是否可能返回 `finish_reason=stop` 同时携带 ToolCall（契约按「以 ToolCalls 数据为准」处理，见下）。
- UNKNOWN：Owner 是否接受为 AC-007 扩展一个 `session` 公共符号（见 Owner Decision Q2）。这是**阻塞实现**的未知项。

## Recommendation

RECOMMENDATION：新增顶层包 `agent`（`cnb.cool/framework-station/mini-agent-go/agent`），`Loop` 一次装配、`Run` 可并发；**一次 Run 的全部执行（含所有模型调用与工具执行）包在一次 `session.Manager.RunTurn` 内**，只有 `COMPLETED` 才整轮提交；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 整轮不提交（Session 与运行前逐字节一致），本次产出保留在 `RunResult.Messages`；单 step 多 ToolCall 按数组序**顺序执行**，且在执行前对整批做工具预算预检；为满足 AC-007 的「被覆盖消息从 Session 移除」，把 `session.Turn` 扩展为**一个前缀删除方法** `DropLeading(n int) error`。

关键取舍：取消/失败后本次对话不进入会话（模型下一轮看不到自己的半截工作），换取「无半截历史 + 同会话消息组连续（AC-005）+ 唯一提交路径」。反过来若坚持「取消后也提交已有消息」，就必须在释放执行权后二次获取执行权提交，期间另一个 Run 可能提交，直接破坏 AC-005 的分组连续性，并让「已取消的 Run 写会话」成为第二条事实来源。

对比过的其他方案：

- **commit-by-`Save`（不改 `session`）**：放弃 `RunTurn`，用 L3 自建 per-session 锁 + `Store.Save` 全量替换。可绕开 AC-007 的删除缺口，但引入第二套执行权（与 Design §8.1 / INV-010 的「每会话一把执行权」冲突），且任何仍走 `RunTurn` 的写入方会与之形成丢失更新；不推荐。
- **不删除被压缩消息（只写 Summary）**：改动最小，但直接违反 AC-007「被覆盖消息按 Design §9 语义从 Session 移除」，等于改验收标准；不推荐。

## Selected Design

等待 Owner 确认（见 Owner Decision Q1–Q5）。

## Interfaces and Data

### 1. 公开入口（新包 `agent`）

```go
package agent // import "cnb.cool/framework-station/mini-agent-go/agent"

// SessionRunner 是 Loop 需要的唯一会话能力，由 *session.Manager 直接满足（结构匹配，不修改 session）。
type SessionRunner interface {
	RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error
}

type Config struct {
	Provider     llm.Provider       // 必填
	Sessions     SessionRunner      // 必填
	Registry     tool.Registry      // 必填（Executor 构造 + Request.Tools）
	Contexts     contextmgr.Manager // 必填（每次模型调用都经 Build 装配视图）
	Sink         runlog.Sink        // nil → runlog.Discard()
	SystemPrompt string             // 允许为空（为空时 Build 视图不含 system 消息）
	Model        string             // 透传 llm.Request.Model，不做非空校验
	TokenBudget  int                // 透传 contextmgr：<=0 不限制
	KeepRecent   int                // 透传 contextmgr：>=0
	ToolTimeout  time.Duration      // 透传 tool.ExecutorOptions.Timeout：<=0 不设工具级超时
}

type RunRequest struct {
	core.ExecutionRef        // SessionID / RunID 均必填
	Policy core.RunPolicy    // 每次 Run 的预算（Design §6：RunPolicy 是一次运行的预算）
}

type Loop struct{ /* 只持有不可变配置，无 per-run 可变字段 */ }

func New(cfg Config) (*Loop, error) // Provider/Sessions/Registry/Contexts 为 nil → invalid_argument
func (l *Loop) Run(ctx context.Context, req RunRequest) core.RunResult
```

- **`Run` 只返回 `core.RunResult`，不返回 `error`**：`RunResult.Error` 是唯一权威失败语义，`core.RunResult.Validate()` 已强制状态与 `ErrorKind` 一致；再返回一个 `error` 会产生「两处都说错」的可能。不变式：`Run` 在**任何**路径返回的 `RunResult` 都满足 `Validate() == nil`（含前置契约错误）。
- `Loop` 并发安全：所有 per-run 状态（计数、历史副本、去重集合、`Executor`）都在 `Run` 栈内构造；同一 `Loop` 可被不同 Run / 不同 Session 并发调用。
- 前置契约错误（`ctx == nil`、`SessionID`/`RunID` 为空、`Policy.Validate` 失败）在获取会话执行权前返回 `FAILED`/`invalid_argument`，**不发出任何 runlog 事件**（此时 `ExecutionRef` 不可信，不构成一次 Run）。
- 每次 `Run` 内部构造**一个** `tool.NewExecutor(tool.ExecutorOptions{Registry: cfg.Registry, Timeout: cfg.ToolTimeout})`：使去重范围恰好等于「本次 Run」（`dedupKey` 含 `RunID`），并避免 `Executor.seen` 无释放导致跨 Run 内存增长。
- 循环内不做工具名称/参数预校验、不解析 `Arguments`：全部交给 `tool.Executor`（INV-011）。

### 2. 单次 Run 的算法（Coder 必须按此实现，顺序即语义）

```text
Run(ctx, req):
  前置校验（ctx/Ref/Policy）→ 失败即 FAILED(invalid_argument)，不发事件
  emit run_started(step=0, data{model})
  err := Sessions.RunTurn(ctx, req.SessionID, func(turn) error { return loop(ctx, turn, &state) })
  err == nil → COMPLETED，否则 Status=core.StatusOf(err)，Error=err(as *RunError)
  emit run_finished(step=最后 step, Status, Error, Duration)
  return RunResult{Ref, Status, state.llmCalls, state.toolCalls, state.messages, Error}
```

`loop`（在会话执行权内，整个 Run 期间持有）：

```text
history := clone(turn.Session().Messages)   // 已提交基线
baseLen := len(history); head := 0          // head = 已被压缩覆盖的前缀长度
summary := turn.Session().Summary; compacted := false
for step := 1; ; step++ {
    ctx 已结束 → cancelled/deadline_exceeded
    llmCalls >= Policy.MaxLLMCalls → llm_call_limit（第 MaxLLMCalls+1 次调用不发出）
    view := Contexts.Build(Request{Ref, SystemPrompt, PreviousSummary: summary,
                                   Messages: history[head:], TokenBudget, KeepRecent})
    Build 失败 → 按返回 Kind 终止
    view.Compacted → head += view.DroppedMessages; summary = view.Summary; compacted = true
    llmCalls++
    emit llm_request；resp, err := Provider.Complete(ctx, Request{Ref, Model, System: SystemPrompt,
        Messages: stripSystemPrompt(view.Messages), Tools: Registry.Specs()})；emit llm_response
    err != nil → ctx 已结束 ? cancelled/deadline_exceeded : provider_failure
    resp.Validate() 失败 → 按返回 Kind 终止
    history += resp.Message；state.messages += resp.Message
    len(ToolCalls) == 0 → finish_reason == tool_calls ? invalid_argument : 跳出循环（COMPLETED）
    toolCalls + len(ToolCalls) > Policy.MaxToolCalls → tool_call_limit（整批一个都不执行）
    for i, call := range ToolCalls {           // 数组序，串行
        ctx 已结束 → cancelled/deadline_exceeded
        call.ID ∈ seen → invalid_pairing(CallID)（不执行、不计入）
        seen.add；toolCalls++；emit tool_started
        res, err := executor.Execute(ctx, tool.Request{Ref, Call: call})；emit tool_finished
        err != nil → ctx 已结束 ? cancelled/deadline_exceeded : 按 err 的 Kind 终止
        history += toolMessage(res)；state.messages += toolMessage(res)   // 每条 Call 恰一条
    }
}
// COMPLETED 提交（仍在执行权内）
compacted → turn.SetSummary(summary)
dropBase := min(head, baseLen) → >0 时 turn.DropLeading(dropBase)
turn.Append(history[max(head, baseLen):]...)   // 只追加本 Run 未被压缩覆盖的消息
return nil
```

- `stripSystemPrompt(view.Messages)`：`SystemPrompt != ""` 时去掉首条 system 消息（Build 保证其存在且文本等于 `SystemPrompt`，不匹配即 `internal`），其余消息（含摘要 system 消息）原样进入 `llm.Request.Messages`。这样 System Prompt 以 `llm.Request.System` 传递，不混入 `Messages`（AC-007）。
- `state.messages` 是**本次 Run 产出消息的完整事实记录**（含被压缩覆盖的消息），与 Session 是否提交解耦（AC-006② 要求已产生的工具结果不被删除）。
- `head` 单调递增：`Build` 的输入始终从 `history[head:]` 开始、`PreviousSummary` 始终传当前 `summary`，因此同一段历史不会被重复摘要；`head` 允许越过 `baseLen`（压缩吞掉本次 Run 自己的早期消息），提交时只对 `base` 前缀 `DropLeading`，被吞掉的本次消息不再追加。
- 工具结果消息只承载 `ToolResult`（`Role: core.Role("tool")`，`Content` 留空），与 `llm/openai` 的映射一致；`Name` 用 `ToolResult.Name`。

### 3. `session` 的最小扩展（Q2 批准后）

```go
// session/turn.go
// DropLeading 丢弃已提交基线的前 n 条消息，随本 Turn 原子提交。
// n<=0 → invalid_argument；n>len(base) → invalid_argument；
// 丢弃后 base[n:]+暂存 必须通过 core.ValidateMessages（切断配对即整批拒绝，暂存不变）。
func (t *Turn) DropLeading(n int) error
```

- 只影响「已提交基线」的前缀，不改变 `Append` 的既有语义；`Turn` 关闭后返回 `invalid_argument`（与既有写方法一致）。
- 该方法是 AC-007「被覆盖消息从 Session 移除」的唯一可行落地点；Design §9 已把该职责写给「调用方」，缺口只是能力入口。

### 4. 运行日志接线（只使用 §10 允许清单内的键）

| 事件 | 发出点 | Step | CallID | Data（允许清单内键） | Duration / Error / Status |
| --- | --- | --- | --- | --- | --- |
| `run_started` | `RunTurn` 之前 | 0 | "" | `model` | — |
| `llm_request` | `Provider.Complete` 之前 | step | "" | `model`、`message_count`、`compacted`、`dropped_messages` | — |
| `llm_response` | `Complete` 返回后 | step | "" | `finish_reason`、`tool_call_count`、`input_tokens`、`output_tokens` | `Duration`=本次模型调用耗时；失败时 `Error` |
| `tool_started` | `Execute` 之前 | step | `ToolCall.ID` | `tool_name`、`index` | — |
| `tool_finished` | `Execute` 返回后（含失败） | step | `ToolCall.ID` | `tool_name`、`is_error`、`index` | `Duration`=本次工具耗时；失败时 `Error` |
| `run_finished` | `RunTurn` 返回后 | 最后 step | "" | 空 | `Status`、`Error`、`Duration`=Run 总耗时 |

- `step` = 该阶段所属模型调用的序号（从 1 开始）；不发生模型调用时 `run_started`/`run_finished` 的 `step` 为 0。
- `Sink.Record` 的返回值一律忽略：不重试、不降级、不改变 `RunResult` 与提交行为（INV-008 运行期证据）。
- 不新增 `Data` 键、不修改 `runlog` 任何符号；需要新键必须同步 Design §10（本任务不做）。

### 5. 使用的既有身份与数据

- 身份一律显式传参：`llm.Request`/`tool.Request` 内嵌 `core.ExecutionRef`；不使用 `context.Value`。
- 消息身份：assistant 消息原样采用 Provider 返回（含 `Raw`）；`RoleTool` 消息的 `ToolResult.CallID == ToolCall.ID`（INV-009）。
- `RunResult.Messages` 只含本次 Run 产出的消息（不含运行前的会话历史）。

## Business Invariants

- INV-001（提交原子性）：`COMPLETED` ⇒ Session 恰好新增本次 Run 未被压缩覆盖的全部消息，且提交后整段历史通过 `core.ValidateMessages`；`FAILED`/`LIMIT_EXCEEDED`/`CANCELLED` ⇒ Session 的消息、`Summary`、`Todos` 与运行前逐字段一致。
- INV-002（入口一致性）：`Run` 在任何路径返回的 `RunResult` 都满足 `Validate() == nil`；`Status == COMPLETED` 当且仅当 `Error == nil` 且本次 Turn 已提交。
- INV-003（计数不越界）：`0 ≤ LLMCalls ≤ Policy.MaxLLMCalls`、`0 ≤ ToolCalls ≤ Policy.MaxToolCalls`；`LLMCalls` = 已发出的模型调用次数，`ToolCalls` = 已受理并交给 `tool.Executor` 的工具调用次数（重复 CallID 与预算拒绝的调用不计入）。
- INV-004（副作用不重复）：同一 Run 内同一 `CallID` 的工具实现至多执行一次；`RunResult.ToolCalls` 不因重复请求而增加；任何终态都不自动重试已执行的调用。
- INV-005（结果不丢不造）：每个已执行的 ToolCall 在 `RunResult.Messages` 中恰有一条 `RoleTool` 消息（`CallID` 相同），不合并、不丢弃、不为未执行的调用伪造结果；可恢复工具失败以 `IsError=true` 回填且不终止 Run。
- INV-006（中断归因）：`ctx` 的终态优先于其它错误来源——`ctx` 已结束的任一中断点都只能得到 `CANCELLED` + `cancelled`/`deadline_exceeded`。
- INV-007（会话串行）：同一 Session 同一时刻至多一个 Run 持有执行权（由单一 `RunTurn` 保证）；跨 Session 无全局锁。日志 Sink 失败不改变上述任何结果。

## Failure and Consistency Semantics

| 情形 | 终态 | `ErrorKind` | 计数与提交 |
| --- | --- | --- | --- |
| 正常完成（无 ToolCall） | `COMPLETED` | — | 整轮提交 |
| `Provider.Complete` 返回 error | `FAILED` | `provider_failure` | 该次调用计入 `LLMCalls`；不提交；本次不再执行任何工具 |
| `Response.Validate` 失败 | `FAILED` | 校验返回的 Kind（`invalid_argument` / `invalid_pairing`） | 不执行工具、不提交 |
| `finish_reason=tool_calls` 但无 ToolCall | `FAILED` | `invalid_argument` | 不提交（矛盾响应不得当作完成） |
| `finish_reason=stop/length/unknown` 且无 ToolCall | `COMPLETED` | — | 提交（Design §5 以「模型不再请求工具」定义完成；截断事实进入 `llm_response.finish_reason`） |
| 工具 panic | `FAILED` | `tool_failure` + `CallID` | 该调用计入 `ToolCalls`；不提交；此前已产生的工具结果保留在 `RunResult.Messages` |
| 同 Run 重复 `CallID` | `FAILED` | `invalid_pairing` + `CallID` | 第二次不执行、不计入；不提交 |
| 工具 error / 未知工具 / 参数非法 / 工具级超时 | 保持循环 | — | `ToolResult{IsError:true}` 回填，继续下一步（Design §7 / INV-010） |
| 需要发出第 `MaxLLMCalls+1` 次模型调用 | `LIMIT_EXCEEDED` | `llm_call_limit` | 该次不发出、不计入；不提交 |
| 本响应 N 个 Call 使 `ToolCalls+N > MaxToolCalls` | `LIMIT_EXCEEDED` | `tool_call_limit` | 整批一个都不执行、不伪造结果；不提交 |
| `Contexts.Build` 失败 | `FAILED` | `invalid_argument`（预算不可满足）/ `internal`（Summarizer 失败）/ `CANCELLED`（ctx） | 不提交 |
| 会话不存在 / 已过期（`errors.Is` 可细分） | `FAILED` | `invalid_argument` | 未提交、无计数 |
| 提交阶段 `ctx` 结束（`RunTurn` P1） | `CANCELLED` | `cancelled` / `deadline_exceeded` | 整轮丢弃 |

中断点（必须逐一覆盖，且每个阻塞调用返回后先检查 `ctx.Err()`）：

1. 会话执行权排队（`RunTurn` acquire）→ `fn` 未执行，`LLMCalls=0`、`Messages` 为空、不提交；
2. `Contexts.Build` 内（含 `Summarizer` 调用）；
3. 模型调用 `Provider.Complete` 期间；
4. 工具调用 `tool.Executor.Execute` 期间（执行器已保证取消优先于工具结果）；
5. 提交阶段（`RunTurn` P1 兜底）：回调已返回 nil 但 `ctx` 已结束 → 整轮丢弃，终态 `CANCELLED`。

一致性说明：本任务只有内存 Session 一个事实源，无 DB/Redis/MQ；「成功响应」只表示本次 Turn 已提交到内存 Session，不表示任何持久化。工具副作用不回滚、不补偿、不自动重试（Design §7「保留已有消息，不回滚」按此落地：Session 不产生半截历史，本 Run 已产出的消息保留在 `RunResult.Messages`，已执行的工具结果不被删除或改写）。

## Allowed / Forbidden Changes

允许：

- 新增 `agent/` 包：`agent/agent.go`（`Config`/`RunRequest`/`SessionRunner`/`Loop`/`New`/`Run` 入口与前置校验）、`agent/run.go`（循环、预算、step 调度、终态映射、提交）、`agent/events.go`（事件发出）、`agent/fake_test.go`（Scripted Provider / 记录型与失败注入工具 / Fake `Estimator`/`Summarizer`）、`agent/agent_test.go`、`agent/concurrency_test.go`、`agent/context_test.go`、`agent/events_test.go`。
- Q2 批准后：`session/turn.go` 增加 `Turn.DropLeading`，并在 `session/turn_test.go` 补该方法的用例（含切断配对时整批拒绝、关闭后拒绝）。
- `docs/design/runtime-architecture.md` 按本 Contract 的 Design Impact 章节更新（A1 之后再改，且不外溢到其它章节）。

禁止：

- 修改 `core` / `llm`（含 `llm/openai`）/ `tool` / `contextmgr` / `runlog` 的任何公共符号；新增 `ErrorKind`；导出 `Role` / `FinishReason` 常量（包内用字面量转换，与 `contextmgr` / `openai` 同风格）。
- 除 `Turn.DropLeading` 外修改 `session` 公共符号；修改 `Turn.Append` 的既有校验语义。
- 引入 `builtin` / `llm/openai` 依赖（`agent` 只 import 标准库 + `core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`）；做 Todo 端口适配、生产 `Summarizer`、Provider 重试/限流/streaming、并行工具执行、CLI/HTTP。
- 修改 `go.mod` / `Makefile` / `.cnb.yml` / `AGENTS.md` / `.agent/**`（本任务目录除外）；在 `develop` 上提交（Registry 例外不适用：`resources: {}`，本任务不占用全局资源）。
- 引入 `time.Sleep` 等待取消/超时；取消、超时、并发用例必须用 `chan` 同步点或注入时钟。

## Verification Requirements

- INV-001 → 表驱动：每个终态用例运行前后对 `session.Session`（Messages/Summary/Todos）做深比较；`COMPLETED` 断言新增消息数 = `RunResult.Messages` 中未被压缩覆盖的条数，且 `core.ValidateMessages(session.Messages) == nil`。（AC-001/002/004）
- INV-002 → 全部用例（含前置契约错误：空 `RunID`、非法 `Policy`、`ctx == nil`）断言 `RunResult.Validate() == nil` 与 `Status`/`Error` 一致。（AC-004）
- INV-003 → Scripted Provider 记录实际调用次数，工具替身记录实际执行次数：断言 `LLMCalls`/`ToolCalls` 与替身计数相等且不越界；`MaxLLMCalls` 用例断言 Provider 调用次数 = `MaxLLMCalls`。（AC-003/004）
- INV-004 → 脚本在第 1 步调用 `c1`、第 2 步再次请求 `c1`：断言工具执行次数为 1、`ToolCalls` 不因重复请求增加、终态非 `COMPLETED`、`Error.CallID == "c1"`、无自动重试。（AC-006①）
- INV-005 → 单响应含「成功 + 未知工具 + 参数非法」3 个 Call：断言 3 条 `RoleTool` 消息、`CallID` 一一对应、下一次请求包含全部结果、Run 继续并最终 `COMPLETED`；`ToolCalls=3`。（AC-003）
- INV-006 → `ctx` 取消与 `context.WithTimeout` 两类用例，分别在「等执行权」「模型调用中」「工具调用中」触发，断言 `CANCELLED` + 对应 Kind 且 Session 不变；同时覆盖「工具已执行后 Provider 失败/超时」：`ToolCalls` 等于真实执行次数、`RunResult.Messages` 中的工具结果仍存在且未被改写、无回滚/补偿语义。（AC-004⑤⑥、AC-006②）
- INV-007 → 同会话 N≥3 并发 Run（不同 RunID）：断言消息组连续不交错、总消息数 = 各 Run 消息数之和；不同 Session + 阻塞 Provider 证明可同时推进（证明无全局锁）；`go test -race ./...` 退出码 0。（AC-005）
- 上下文（AC-007）→ Fake `Estimator`/`Summarizer`：超预算用例断言发给 Provider 的 `Messages` 是压缩视图（保留 ≥ `KeepRecent` 条、首条不含 System Prompt）、`Session.Summary` 更新、被覆盖消息已从 `Session.Messages` 移除；未超预算用例断言 `Compacted=false` 且摘要不变；`Build` 失败用例断言终态与 Session 完全不变。
- 日志（AC-008）→ `runlog.NewJSONSink(&bytes.Buffer)`：逐行解析断言事件种类顺序、`session_id`/`run_id`/`step`/`call_id` 可定位（`tool_finished.call_id == ToolCall.ID`）；换成必定失败的 Sink 后 `RunResult` 与 Session 逐字段相同。
- 工程检查（AC-009）→ `make test`、`go vet ./...`、`go test -race ./...` 退出码 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./agent` 只出现标准库与 `core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`；`git ls-files` 与 Design §12 差集为空。

## Design Impact（`UPDATE`，Contract `APPROVED` 后由 Analyst 落库，仅限以下章节）

- §2 分层表：`L3（未实现）` 改为 `agent` Agent Loop（依赖方向只到 L0/L1）；§3 模块职责表新增 `agent` 行（职责 + 明确不负责：Provider 实现、工具实现、Todo 适配、持久化、CLI）。
- §7：把「用户取消：保留已有消息，不回滚」收窄为已批准的唯一策略；新增「RUNNING 生命周期与提交边界」「中断点清单」「`COMPLETED` ⇔ 本 Turn 已提交」。
- §8.1 / §12：登记 `Turn.DropLeading` 能力与文件归属（Q2 批准后）；`§12` 新增 `agent/*.go` 与测试文件行。
- §9：补充「Loop 每次 `Build` 的输入 = 未被摘要覆盖的历史 + 本次 Run 已产生消息；`PreviousSummary` 传递；压缩偏移在提交时一次性生效」。
- §10：新增发出点接线表（本 Contract §4 的表），声明未新增 `Data` 键。
- §11.1：新增「Agent Loop 不变量（T07，任务局部编号）」小节（INV-001—INV-007 的长期版本）。
- §14：删除已由本任务完成的条目（取消/超时中断点、跨 step 终态映射与同 step 多调用顺序、日志发出点接线），把仍在未验证清单中的 INV-006/007/008 与 INV-009—INV-013 的运行期证据改为「已完成（T07）」；把原指向「装配层（T07）」的 Todo 端口适配与生产 `Summarizer` 条目改指后续装配任务（编号待 Owner/后续 TaskBuilder 确定），避免悬空或与本任务双重归属。

## Open Risks

- 取消/失败不提交：模型下一轮看不到被取消 Run 的半截工作；这是 Q1 取舍的直接后果，需 Owner 明确接受。
- 整个 Run 持有会话执行权：同会话其他 Run 在 LLM/工具耗时期间排队（AC-005 的必然代价），长超时会拖住队列；排队可被调用方 `ctx` 取消。
- `finish_reason=length` 被视为 `COMPLETED`（按 Design §5 定义），截断只体现在事件 `finish_reason`，不体现在终态；若 Owner 认为截断必须失败，需要新增语义（当前无匹配 `ErrorKind`）。
- 宽松处理「`finish_reason=stop` 但带 ToolCall」：执行工具而非报错，可能与「严格契约」预期不一致（真实 Provider 行为未验证，见 UNKNOWN）。
- 「已受理并交给执行器」的计数口径：取消恰好落在某次工具调用时，该调用计入 `ToolCalls`，但工具实现可能并未真正运行（执行器在 `ctx` 已结束时提前返回）；AC-006② 覆盖的场景（工具已执行后再失败）不受影响。
- `head` 越过 `baseLen` 时依赖 `Build` 以原子单元压缩；若后续 `contextmgr` 语义变更（例如允许非单元对齐丢弃），此处需回归。
- 若 Owner 拒绝 Q2，AC-007 无法满足，任务应回 TaskBuilder 调整 AC 而非在实现中降级。

## Owner Decision Record

等待 Owner 确认。
