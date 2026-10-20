# Technical Contract

任务：`agent-runtime-loop-v1`（T07 / M2-agent-runtime-loop）
基线：`36e2292`（TaskBuilder 基线 commit，父提交 `dc734d2`），工作区干净，`go test ./...` 8 包全绿。

## Decision Status

APPROVED

适用范围与前置依赖：本 Contract 的 D1 / D3 / D4 / D5 可立即实现；**AC-007 的「被覆盖消息从 Session 移除」被一个跨任务前置依赖阻塞**（见「External Dependency」），依赖由独立 Session 扩展任务交付并集成后，T07 Coder 才可完成 AC-007。T07 不得以任何降级方式（只写 Summary 不删除、重入 `Save`、绕过执行权、直接改 Session 内部数据）替代该依赖。

## Problem

Task 要求在 T00–T06 已交付的 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` 之上交付 L3 Agent Loop：一次 Run 完成 LLM → Tool → LLM 循环，并在双预算、整体超时与取消下给出准确终态与可校验消息历史。

本 Contract 收敛的四个真问题：

1. **提交事务边界**：`RunTurn` 在 `ctx` 已取消时不提交，Design §7 又写「保留已有消息，不回滚」，AC-004 要求「失败/取消不产生部分提交」。
2. **AC-007 的压缩移除缺少写入能力**：`session.Turn` 无法删除已提交基线的前缀，且 T07 被禁止直接修改 T04 的公共接口。
3. **预算与调度的计数口径**：LLM 调用与工具执行尝试的计数时点、触限时是否发出、单响应多 Call 与剩余额度的关系。
4. **终态判定**：`finish_reason` 各取值、`length` 截断、矛盾响应的终态与 `ErrorKind`。

## Verified Current Behavior

- VERIFIED：`core` 已有 `RunStatus`（`RUNNING` + 4 终态）、`RunPolicy.Validate`（两者必须 > 0）、`RunResult{ExecutionRef, Status, LLMCalls, ToolCalls, Messages, Error}` 与逐状态 `Validate`：`COMPLETED`/`RUNNING` 必须无错误；`FAILED` 只接受 `provider_failure`/`tool_failure`/`internal`/`invalid_argument`/`invalid_pairing`，`tool_failure` 必须带 `CallID`；`LIMIT_EXCEEDED` 只接受两个 limit 种类；`CANCELLED` 只接受 `cancelled`/`deadline_exceeded`（`core/run.go:80`）。全仓仍**没有任何代码构造完整 `RunResult`**。
- VERIFIED：`llm.Provider.Complete(ctx, Request) (Response, error)`；`Response.Validate` 要求 assistant 角色、消息结构合法、`FinishReason` ∈ {stop, tool_calls, length, unknown}、用量非负（`llm/provider.go:59`）。`FinishReason` 常量未导出，包外只能写字面量 `llm.FinishReason("tool_calls")`（同包风格见 `llm/openai/wire.go:16`）。
- VERIFIED：`llm/openai` 适配器把未知 `finish_reason` 归为 `provider_failure`，不会产出 `unknown`；`Messages` 中的 system 角色消息可正常映射（`llm/openai/wire.go:173`、`:346`）。
- VERIFIED：`tool.Executor.Execute` 返回值语义（`tool/executor.go:58`）：未知工具 / 参数非法 / 工具级超时 / 工具实现返回 error → `ToolResult{IsError:true}, err==nil`（可恢复）；`panic` → `*RunError{tool_failure, CallID}`；父 `ctx` 结束 → `cancelled`/`deadline_exceeded`，且**取消优先于工具结果**；去重按 `(RunID, CallID)` 占位、不释放、只对同一 `Executor` 实例有效，重复提交返回 `invalid_pairing` 且不复用首次结果。
- VERIFIED：`session.Manager.RunTurn` 仅在「`ctx` 未取消 + 回调返回 nil + Turn 仍打开 + entry 仍是当前 + 提交前 `ValidateMessages` 通过」时整轮提交；`ctx` 在回调返回前结束即整轮丢弃；排队等待执行权被取消时回调不执行（`session/manager.go:188`、`:399`）。回调错误若是 `*core.RunError` 原样返回（`session/errors.go:83`）。
- VERIFIED：`session.Turn` 只有 `Append`（每批对「base + 暂存 + 新批」跑 `core.ValidateMessages`）、`SetSummary`、Todo 能力与只读视图；**无法删除已提交基线的前缀**（`session/turn.go:76`）。
- VERIFIED：`contextmgr.Build` 超预算时按原子单元压缩最老历史，返回 `Messages`（System Prompt 首条、新摘要次之）、`Compacted`、`DroppedMessages`（输入 `Messages` 的严格前缀条数）、`Summary`；错误路径返回零值 `Result`（`contextmgr/manager.go:48`）。
- VERIFIED：`runlog` 6 个 `EventKind`、`Event{Kind, Ref, Step, CallID, Summary, Data, Error, Duration, Status}`、`Sink`、`Discard`、`NewJSONSink(io.Writer)`；`Data` 允许清单封闭；全仓 6 类事件**均无发出方**。
- VERIFIED：T04（`agent-session-manager-v1`）**已完成并集成**：`state.yaml` 为 contract APPROVED / review CLEAN / owner ACCEPTED / delivery PASS；feature 已合并进 `develop`（`73fef46`），且该提交存在于本地 `develop` 与 `origin/develop`。因此 D2 走「新建最小独立 Session 扩展任务」路径，可排除 T04 Contract Revision 路径。
- VERIFIED：既有测试全部是包内 `_test.go`，测试替身均未导出、不可跨包复用。
- UNKNOWN：真实 Provider 是否可能返回 `finish_reason=stop` 同时携带 ToolCall（本 Contract 按 D5「存在合法 Tool Calls 即继续工具流程」处理，不再拒绝）。
- UNKNOWN：Session 扩展任务的编号、分支与交付时间（由 Owner / TaskBuilder 决定，T07 前置依赖）。

## Recommendation

RECOMMENDATION：新增顶层包 `agent`，`Loop` 一次装配、`Run` 可并发；一次 Run 的全部执行包在一次 `session.Manager.RunTurn` 内，仅 `COMPLETED` 整轮原子提交；每次 Run 创建独立 `tool.Executor`；单 step 多 ToolCall 顺序执行且执行前整批预检工具预算；`finish_reason` 由 D5 的优先级规则决定终态；AC-007 的前缀删除依赖独立 Session 扩展任务。

关键取舍：取消/失败不留半截历史（本次对话不进会话），换取「Session 与运行前一致 + 同会话消息组连续 + 唯一提交路径」；代价是模型下一轮看不到被取消 Run 的工作。

## Selected Design

### D1 提交边界（Owner 批准）

- 整个 Agent Run 在同一次 `session.Manager.RunTurn` 中执行（模型调用期间持有会话执行权），因此同会话 Run 串行、跨会话并发。
- **仅 `COMPLETED`** 原子提交本 Turn 的消息、摘要与受管理的会话状态（本任务不写 Todo，`Turn` 继承基线，提交为幂等）。
- `FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 不提交本次未完成的 Turn：Session 的消息、`Summary`、`Todos` 与运行前逐字段一致。
- 已经提交的历史对话不回滚；不得假定已经发生的外部工具副作用能够自动撤销（不产生回滚、补偿或自动重试语义）。
- **失败时 `RunResult.Messages` 允许包含未完成的工具调用序列**，它是本次 Run 产出的事实记录，不是待提交批次：
  - 允许的唯一不完整形态：以「携带 ToolCalls 但其中部分/全部 Call 无结果」的 assistant 消息结尾；
  - 未配对消息**永不**进入 Session；未执行的 Call **不得**生成 `RoleTool` 消息（不伪造结果）；
  - `COMPLETED` 时 `RunResult.Messages` 必须通过 `core.ValidateMessages`；其它终态不作此要求（`RunResult.Validate()` 仍必须通过）。

### D2 Session 前缀删除能力（需求批准，实施归属独立任务）

- T07 **禁止**修改 T04（`session`）公共接口、禁止重入 `Session.Save`、禁止绕过执行权、禁止直接改 Session 内部数据。
- 已批准的需求语义（正式接口契约属前置任务）：
  - 在**同一次 `RunTurn` 的事务性工作副本**内删除已压缩历史的**基线前缀**，与追加消息、更新 `Summary` 一并提交或回滚；
  - 必须是 `Turn` 上的写操作，与 `Append` / `SetSummary` 同源；`n` 边界必须校验（`0 ≤ n ≤ len(base)`），切断配对或越界即整批拒绝且暂存不变；
  - 只作用于已提交基线：本 Turn 通过 `Append` 暂存的消息永不在删除范围内，因此本 Turn 的用户输入/新增消息不会被误删；
  - `Turn` 关闭后拒绝写入；不改变 `Append` 语义；不新增 `core.ErrorKind`。
- T07 侧唯一用法：提交阶段先 `SetSummary`，再对「压缩覆盖的基线前缀长度」删除一次，最后 `Append` 本 Run 未被覆盖的消息。

### D3 预算与计数（Owner 批准）

- 工具预算**整批预检**：单响应含 N 个 Call 时，若 `ToolCalls + N > MaxToolCalls` → `LIMIT_EXCEEDED` + `tool_call_limit`，整批一个都不执行，不伪造结果。
- LLM 预算：发出调用前检查 `llmCalls >= MaxLLMCalls` → `LIMIT_EXCEEDED` + `llm_call_limit`，第 `MaxLLMCalls+1` 次调用**不发出**。
- 计数口径（必须写入实现注释与测试）：
  - `LLMCalls` = **实际发起**的模型调用次数（通过预算检查后、调用前 `+1`；Provider 失败的那次也计入）；
  - `ToolCalls` = **实际发起**的工具执行尝试次数（通过预算检查与 Loop 前置重复检查后、交给 `Executor` 前 `+1`；工具失败/panic 的那次也计入；被预算拒绝或重复 `CallID` 拒绝的调用不计入）。
  - 因此恒有 `LLMCalls ≤ MaxLLMCalls`、`ToolCalls ≤ MaxToolCalls`（Design INV-004）。

### D4 Design Impact（Owner 批准）

- Design Impact = `UPDATE`，Task 中的 `NONE` 判定作废（OQ-2 已由 Owner 决定）。章节范围见本文件「Design Impact」。
- T07 只负责 Agent Loop；**不纳入**生产 Todo Adapter 与真实 LLM `Summarizer`。两者归**服务装配任务 T08**（编号与范围由 TaskBuilder 正式确认），Design §14 同步改指 T08，避免功能缺口无人负责。

### D5 模型结束语义（Owner 批准，按以下优先级判定）

1. `Response.Validate()` 失败 → `FAILED` + 校验返回的 Kind。
2. `finish_reason = length`（输出被截断）→ `FAILED` + `provider_failure`，**不得** `COMPLETED`，不伪造完整回答；该响应携带的 ToolCall 不执行。
3. `finish_reason = tool_calls` 但无任何 ToolCall → `FAILED` + `provider_failure`（异常模型协议响应，不归类为用户输入错误）。
4. `finish_reason = unknown` → `FAILED` + `provider_failure`（无法判定结束原因，不得当作完成；此为 D5 同源推导，Owner 未单独定义，见 Open Risks）。
5. 存在合法 ToolCall（`finish_reason ∈ {stop, tool_calls}`）→ 进入工具流程，继续循环。
6. 无 ToolCall 且 `finish_reason = stop` → `COMPLETED`（结构合法即「合法最终回答」；`core` 未禁止空文本，不新增拒绝规则）。

### 其他已批准约束

- 每次 Run 创建独立 `tool.Executor`（`dedupKey` 含 `RunID`），保证 Run 内去重、无跨 Run 错误命中、无跨 Run 内存增长。
- 同 Session 串行、不同 Session 可并发；`Loop` 无 per-run 可变状态。
- `RunResult.Validate()` 对所有返回结果必须通过。
- 测试只用 Fake LLM / Fake Tool，不调用真实付费 LLM API、不需要网络。
- 不修改 Workflow Engine、不绕过任何 Gate。

## External Dependency（T07 前置，非 T07 实施）

| 项 | 内容 |
| --- | --- |
| 能力 | `session.Turn` 增加「删除已提交基线的前 N 条消息」的事务内写操作（D2 语义），本 Contract 以 `DropLeading(n int) error` 指代，最终命名与契约由该任务定稿 |
| 归属 | **最小独立 Session 扩展任务**（T04 已集成，走新任务而非 T04 Contract Revision）；由 Owner / TaskBuilder 创建，走完整 Analyst → Coder → Cleaner → OwnerGate → Deliverer → 集成流程 |
| 建议范围 | 仅该写操作 + 其边界/原子性/关闭后拒绝的测试；不改 `Append`；不改 `Manager`；不新增 `ErrorKind`；同步 Design §8.1 / §11.1 / §12 |
| T07 受影响项 | AC-007 的「被覆盖消息从 Session 移除」。其余 AC（001–006、008）不依赖该能力 |
| 解除条件 | 该任务合并进 `develop` 后，T07 重新进入 Coder；在此之前 T07 不得开始实现（`blocked.active=true`） |

## Interfaces and Data

### 1. 公开入口（新包 `agent`）

```go
package agent // import "cnb.cool/framework-station/mini-agent-go/agent"

// SessionRunner 是 Loop 需要的唯一会话能力，由 *session.Manager 结构匹配满足（不修改 session）。
type SessionRunner interface {
	RunTurn(ctx context.Context, sessionID string, fn func(*session.Turn) error) error
}

type Config struct {
	Provider     llm.Provider       // 必填
	Sessions     SessionRunner      // 必填
	Registry     tool.Registry      // 必填（Executor 构造 + Request.Tools）
	Contexts     contextmgr.Manager // 必填（每次模型调用都经 Build 装配视图）
	Sink         runlog.Sink        // nil → runlog.Discard()
	SystemPrompt string             // 允许为空（为空时视图不含 system 消息）
	Model        string             // 透传 llm.Request.Model，不做非空校验
	TokenBudget  int                // 透传 contextmgr：<=0 不限制
	KeepRecent   int                // 透传 contextmgr：>=0
	ToolTimeout  time.Duration      // 透传 tool.ExecutorOptions.Timeout：<=0 不设工具级超时
}

type RunRequest struct {
	core.ExecutionRef     // SessionID / RunID 均必填
	Policy core.RunPolicy // 每次 Run 的预算
}

type Loop struct{ /* 只持有不可变配置 */ }

func New(cfg Config) (*Loop, error) // Provider/Sessions/Registry/Contexts 为 nil → invalid_argument
func (l *Loop) Run(ctx context.Context, req RunRequest) core.RunResult
```

- `Run` **只返回** `core.RunResult`：`RunResult.Error` 是唯一权威失败语义，避免「返回值与字段互相矛盾」。任何路径返回的结果都满足 `Validate() == nil`。
- 前置契约错误（`ctx == nil`、`SessionID`/`RunID` 为空、`Policy.Validate` 失败）在获取执行权前返回 `FAILED` + `invalid_argument`，不发 runlog 事件（`ExecutionRef` 不可信，不构成一次 Run）。

### 2. 单次 Run 的算法（顺序即语义）

```text
Run(ctx, req):
  前置校验 → 失败即 FAILED(invalid_argument)，不发事件
  emit run_started(step=0, data{model})
  err := Sessions.RunTurn(ctx, req.SessionID, func(turn) error { return loop(ctx, turn, &state) })
  err == nil → COMPLETED，否则 Status=core.StatusOf(err)，Error=err(as *RunError)
  emit run_finished(step=最后 step, Status, Error, Duration)
  return RunResult{Ref, Status, state.llmCalls, state.toolCalls, state.messages, Error}

loop（整个 Run 期间持有会话执行权）:
  history := clone(turn.Session().Messages); baseLen := len(history)
  head := 0; summary := turn.Session().Summary; compacted := false; seen := {}
  for step := 1; ; step++ {
      ctx 已结束 → cancelled/deadline_exceeded
      llmCalls >= Policy.MaxLLMCalls → llm_call_limit（不发出下一次调用）
      view := Contexts.Build({Ref, SystemPrompt, PreviousSummary: summary,
                              Messages: history[head:], TokenBudget, KeepRecent})
      Build 失败 → 按返回 Kind 终止（cancelled/deadline 优先）
      view.Compacted → head += view.DroppedMessages; summary = view.Summary; compacted = true
      llmCalls++
      emit llm_request；resp, err := Provider.Complete({Ref, Model, System: SystemPrompt,
            Messages: stripSystemPrompt(view.Messages), Tools: Registry.Specs()})；emit llm_response
      err != nil → ctx 已结束 ? cancelled/deadline_exceeded : provider_failure
      resp.Validate() 失败 → 按其 Kind 终止
      history += resp.Message；state.messages += resp.Message
      // D5 优先级
      finish == length → provider_failure
      finish == tool_calls && len(ToolCalls)==0 → provider_failure
      finish == unknown → provider_failure
      len(ToolCalls)==0 → 跳出循环（COMPLETED，仅 finish==stop 会走到这里）
      toolCalls + len(ToolCalls) > Policy.MaxToolCalls → tool_call_limit（整批不执行）
      for i, call := range ToolCalls {           // 数组序，串行
          ctx 已结束 → cancelled/deadline_exceeded
          seen[call.ID] 已存在 → invalid_pairing(CallID)（不执行、不计数）
          seen.add；toolCalls++；emit tool_started
          res, err := executor.Execute(ctx, {Ref, Call: call})；emit tool_finished
          err != nil → ctx 已结束 ? cancelled/deadline_exceeded : 按 err 的 Kind 终止
          history += toolMessage(res)；state.messages += toolMessage(res)   // 每条 Call 恰一条
      }
  }
  // COMPLETED 提交（仍在执行权内）
  compacted → turn.SetSummary(summary)
  dropBase := min(head, baseLen) → >0 时 turn.DropLeading(dropBase)   // 依赖 External Dependency
  turn.Append(history[max(head, baseLen):]...)                        // 只追加本 Run 未被覆盖的消息
  return nil
```

- `stripSystemPrompt`：`SystemPrompt != ""` 时去掉首条 system 消息（Build 保证存在且文本等于 `SystemPrompt`，不匹配即 `internal`）；摘要 system 消息保留在 `llm.Request.Messages` 中。
- `head` 单调递增，`Build` 输入始终从 `history[head:]` 开始、`PreviousSummary` 始终传当前 `summary`，同一段历史不重复摘要；`head` 允许越过 `baseLen`（压缩吞掉本次 Run 自己产生的早期消息），此时这些消息不再追加。
- `state.messages` 记录本次 Run 全部产出消息（含被压缩覆盖、含未提交），与 Session 提交解耦。
- 工具结果消息只承载 `ToolResult`（`Role: core.Role("tool")`，`Content` 留空），`Name` 用 `ToolResult.Name`。
- Loop 侧 `seen` 只用于**执行尝试计数口径**与前置拒绝（D3）；执行器去重仍是 INV-012 的最后防线。

### 3. 运行日志接线（只用 §10 允许清单内的键）

| 事件 | 发出点 | Step | CallID | Data | Duration / Error / Status |
| --- | --- | --- | --- | --- | --- |
| `run_started` | `RunTurn` 之前 | 0 | "" | `model` | — |
| `llm_request` | `Complete` 之前 | step | "" | `model`、`message_count`、`compacted`、`dropped_messages` | — |
| `llm_response` | `Complete` 返回后 | step | "" | `finish_reason`、`tool_call_count`、`input_tokens`、`output_tokens` | `Duration`=模型调用耗时；失败时 `Error` |
| `tool_started` | `Execute` 之前 | step | `ToolCall.ID` | `tool_name`、`index` | — |
| `tool_finished` | `Execute` 返回后（含失败） | step | `ToolCall.ID` | `tool_name`、`is_error`、`index` | `Duration`=工具耗时；失败时 `Error` |
| `run_finished` | `RunTurn` 返回后 | 最后 step | "" | 空 | `Status`、`Error`、`Duration`=总耗时 |

- `step` = 该阶段所属模型调用的序号（从 1 开始）；没有模型调用时为 0。
- `Sink.Record` 的返回值一律忽略：不重试、不降级、不改变 `RunResult` 与提交行为。
- 不新增 `Data` 键、不修改 `runlog` 任何符号。

### 4. 身份与数据

- 身份一律显式传参（`llm.Request` / `tool.Request` 内嵌 `core.ExecutionRef`），不使用 `context.Value`。
- assistant 消息原样采用 Provider 返回（含 `Raw`）；`RoleTool` 消息的 `ToolResult.CallID == ToolCall.ID`（Design INV-009）。
- `RunResult.Messages` 只含本次 Run 产出的消息（不含运行前的会话历史）。

## Business Invariants

- INV-001（提交原子性）：`COMPLETED` ⇒ Session 恰好新增本次 Run 未被压缩覆盖的全部消息，提交后整段历史通过 `core.ValidateMessages`；`FAILED`/`LIMIT_EXCEEDED`/`CANCELLED` ⇒ Session 的消息、`Summary`、`Todos` 与运行前逐字段一致。
- INV-002（入口一致性）：任何路径返回的 `RunResult` 都满足 `Validate() == nil`；`Status == COMPLETED` 当且仅当 `Error == nil` 且本 Turn 已提交。
- INV-003（计数口径与边界）：`0 ≤ LLMCalls ≤ MaxLLMCalls`、`0 ≤ ToolCalls ≤ MaxToolCalls`；`LLMCalls` = 实际发起的模型调用次数，`ToolCalls` = 实际发起的工具执行尝试次数（预算拒绝与重复 `CallID` 拒绝不计入）。
- INV-004（副作用不重复）：同一 Run 同一 `CallID` 的工具实现至多执行一次；`ToolCalls` 不因重复请求增加；任何终态都不自动重试已执行的调用。
- INV-005（结果不丢不造）：每个已执行的 ToolCall 在 `RunResult.Messages` 中恰有一条 `RoleTool` 消息（`CallID` 相同），不合并、不丢弃；未执行的 Call 不生成结果；可恢复工具失败以 `IsError=true` 回填且不终止 Run；未配对消息永不进入 Session。
- INV-006（中断归因）：`ctx` 终态优先于其它错误来源——任一中断点上的 `ctx` 结束都只能得到 `CANCELLED` + `cancelled`/`deadline_exceeded`。
- INV-007（会话串行与日志旁路）：同一 Session 同一时刻至多一个 Run 持有执行权（单一 `RunTurn` 保证）；跨 Session 无全局锁；Sink 失败不改变 `RunResult`、计数、消息或提交结果。
- INV-008（模型结束语义）：`finish_reason` 判定严格按 D5 优先级；`length`、`tool_calls`（无 Call）、`unknown` 三者都不得产生 `COMPLETED`，且都只能归入 `provider_failure`。

## Failure and Consistency Semantics

| 情形 | 终态 | `ErrorKind` | 计数与提交 |
| --- | --- | --- | --- |
| 正常完成（`finish_reason=stop`、无 ToolCall） | `COMPLETED` | — | 整轮提交 |
| `Provider.Complete` 返回 error | `FAILED` | `provider_failure` | 该次调用计入 `LLMCalls`；不提交；此后不再执行任何工具 |
| `Response.Validate` 失败 | `FAILED` | 校验返回的 Kind（`invalid_argument` / `invalid_pairing` / `provider_failure`） | 不执行工具、不提交 |
| `finish_reason = length` | `FAILED` | `provider_failure` | 该次调用计入；不执行该响应的 ToolCall；不提交 |
| `finish_reason = tool_calls` 且无 ToolCall | `FAILED` | `provider_failure` | 不提交 |
| `finish_reason = unknown` | `FAILED` | `provider_failure` | 不提交 |
| 存在合法 ToolCall 且 `finish_reason ∈ {stop, tool_calls}` | 保持循环 | — | 进入工具流程 |
| 工具 panic | `FAILED` | `tool_failure` + `CallID` | 该次尝试计入 `ToolCalls`；不提交；已产生的结果保留在 `RunResult.Messages` |
| 同 Run 重复 `CallID` | `FAILED` | `invalid_pairing` + `CallID` | 第二次不执行、不计入；不提交 |
| 工具 error / 未知工具 / 参数非法 / 工具级超时 | 保持循环 | — | `ToolResult{IsError:true}` 回填（Design INV-010） |
| 需要第 `MaxLLMCalls+1` 次模型调用 | `LIMIT_EXCEEDED` | `llm_call_limit` | 不发出、不计入；不提交 |
| 本响应 N 个 Call 使 `ToolCalls+N > MaxToolCalls` | `LIMIT_EXCEEDED` | `tool_call_limit` | 整批不执行、不伪造结果；不提交 |
| `Contexts.Build` 失败 | `FAILED` | `invalid_argument`（预算不可满足）/ `internal`（Summarizer 失败）/ `CANCELLED`（ctx） | 不提交 |
| 会话不存在 / 已过期（`errors.Is` 可细分） | `FAILED` | `invalid_argument` | 未提交、无计数 |
| 提交阶段 `ctx` 结束（`RunTurn` P1） | `CANCELLED` | `cancelled` / `deadline_exceeded` | 整轮丢弃 |

中断点（逐一覆盖，每个阻塞调用返回后先看 `ctx.Err()`）：

1. 会话执行权排队（`RunTurn` acquire）→ 回调未执行，`LLMCalls=0`、`Messages` 为空、不提交；
2. `Contexts.Build`（含 `Summarizer`）；
3. `Provider.Complete` 期间；
4. `tool.Executor.Execute` 期间（执行器已保证取消优先于工具结果）；
5. 提交阶段（`RunTurn` P1 兜底）：回调返回 nil 但 `ctx` 已结束 → 整轮丢弃，终态 `CANCELLED`。

一致性说明：本任务只有内存 Session 一个事实源，无 DB/Redis/MQ；「成功」只表示本次 Turn 已提交到内存 Session，不表示任何持久化。工具副作用不回滚、不补偿、不自动重试。

## Allowed / Forbidden Changes

允许：

- 新增 `agent/` 包：`agent/agent.go`（`Config`/`RunRequest`/`SessionRunner`/`Loop`/`New`/`Run` 与前置校验）、`agent/run.go`（循环、预算、D5 判定、终态映射、提交）、`agent/events.go`（事件发出）、`agent/fake_test.go`（Scripted Provider / 记录型与失败注入工具 / Fake `Estimator`、`Summarizer`）、`agent/agent_test.go`、`agent/concurrency_test.go`、`agent/context_test.go`、`agent/events_test.go`。
- `docs/design/runtime-architecture.md` 按本 Contract 的 Design Impact 章节更新（不外溢到其它章节）。

禁止：

- 修改 `session`（T04）任何公共符号，包括 T07 自己实现 `DropLeading`；禁止重入 `Save`、绕过执行权、直接改 Session 内部数据。该能力由前置任务交付。
- 修改 `core` / `llm`（含 `llm/openai`）/ `tool` / `contextmgr` / `runlog` 的任何公共符号；新增 `ErrorKind`；导出 `Role` / `FinishReason` 常量（包内用字面量转换）。
- 引入 `builtin` / `llm/openai` 依赖（`agent` 只 import 标准库 + `core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`）；做 Todo 端口适配、生产 `Summarizer`、Provider 重试/限流/streaming、并行工具执行、CLI/HTTP。
- 以「只写 `Summary` 不删除被覆盖消息」等方式降级实现 AC-007。
- 修改 `go.mod` / `Makefile` / `.cnb.yml` / `AGENTS.md` / `.agent/**`（本任务目录除外）/ Workflow Engine；在 `develop` 上提交（`resources: {}`，本任务不占用全局资源）。
- 用 `time.Sleep` 等待取消/超时/并发；必须用 `chan` 同步点或注入时钟。

## Verification Requirements

- INV-001 → 每个终态用例对 `session.Session`（Messages/Summary/Todos）做运行前后深比较；`COMPLETED` 断言新增消息 = 本 Run 未被压缩覆盖的消息且 `core.ValidateMessages(session.Messages) == nil`。（AC-001/002/004）
- INV-002 → 全部用例（含空 `RunID`、非法 `Policy`、`ctx == nil`）断言 `RunResult.Validate() == nil` 且状态与错误一致。（AC-004）
- INV-003 → 边界测试：恰好用满双预算 → `COMPLETED` 且 `LLMCalls == MaxLLMCalls`、`ToolCalls == MaxToolCalls`；各超一次 → `LIMIT_EXCEEDED` 且计数不越界；Provider 失败后 `LLMCalls` 等于实际发起次数；工具 panic 那次计入 `ToolCalls`；重复 `CallID` 不增加计数。（AC-004、AC-006）
- INV-004 → 脚本第 1 步调用 `c1`、第 2 步再次请求 `c1`：工具执行次数为 1、`ToolCalls` 不增加、终态非 `COMPLETED`、`Error.CallID == "c1"`、无自动重试。（AC-006①）
- INV-005 → 单响应含「成功 + 未知工具 + 参数非法」3 个 Call：3 条 `RoleTool`、`CallID` 一一对应、下一次请求包含全部结果、Run 继续并最终 `COMPLETED`；另断言非 `COMPLETED` 用例中没有为未执行 Call 生成结果、且 Session 未写入未配对消息。（AC-003、AC-004）
- INV-006 → 取消与超时分别在「等执行权」「模型调用中」「工具调用中」触发，断言 `CANCELLED` + 对应 Kind 且 Session 不变；覆盖「工具已执行后 Provider 失败/超时」：`ToolCalls` 等于真实执行次数、结果消息仍在且未被改写、无回滚/补偿语义。（AC-004⑤⑥、AC-006②）
- INV-007 → 同会话 N≥3 并发 Run：消息组连续不交错、总数 = 各 Run 之和；不同 Session + 阻塞 Provider 可同时推进；`go test -race ./...` 退出码 0；失败 Sink 下 `RunResult` 与 Session 逐字段与正常 Sink 相同。（AC-005、AC-008）
- INV-008 → Fake LLM 的确定性表驱动：`stop`+纯文本 → `COMPLETED`；`length`（带/不带 ToolCall）→ `FAILED + provider_failure`；`tool_calls`+空 Call → `FAILED + provider_failure`；`unknown` → `FAILED + provider_failure`；`stop`+ToolCall → 执行工具并继续。（D5、AC-001、AC-004）
- 上下文（AC-007）→ 超预算用例断言发给 Provider 的 `Messages` 是压缩视图（保留 ≥ `KeepRecent` 条、首条不含 System Prompt）、`Session.Summary` 更新、被覆盖消息已从 `Session.Messages` 移除；未超预算断言 `Compacted=false` 且摘要不变；`Build` 失败断言终态与 Session 完全不变。**该组用例在前置 Session 扩展任务集成后才可通过，Coder 不得以跳过/软化断言方式交付。**
- 日志（AC-008）→ `runlog.NewJSONSink(&bytes.Buffer)` 逐行解析：事件种类顺序、`session_id`/`run_id`/`step`/`call_id` 可定位（`tool_finished.call_id == ToolCall.ID`）。
- 工程检查（AC-009）→ `make test`、`go vet ./...`、`go test -race ./...` 退出码 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./agent` 只出现标准库与六个允许包；`git ls-files` 与 Design §12 差集为空。

## Design Impact（`UPDATE`，已由 Owner 批准；Contract `APPROVED` 后由 Analyst 落库）

- §2 分层表：`L3（未实现）` 改为 `agent` Agent Loop 行（§3 新增行的必要配套，否则与 §3 自相矛盾），并声明 `agent` 只依赖 L0/L1。
- §3 模块职责表：新增 `agent` 行（职责 + 明确不负责）。
- §7：新增「提交边界 / 中断点 / `COMPLETED` ⇔ 本 Turn 已提交」；`length`、`tool_calls`（无 Call）、`unknown` 三行归入 `provider_failure`；重复 `CallID` 落为 `FAILED + invalid_pairing`；「用户取消：保留已有消息，不回滚」收窄为本 Contract 的 D1 语义。
- §9：补充 Loop 的 `Build` 输入（未被摘要覆盖的历史 + 本 Run 已产生消息）、`PreviousSummary` 传递、压缩偏移在提交时一次性生效；前缀删除能力标注为**独立 Session 扩展任务交付**（T07 前置依赖）。
- §10：新增发出点接线表（本 Contract §3），声明未新增 `Data` 键。
- §11.1：新增「Agent Loop 不变量（T07，任务局部编号）」小节（INV-001—INV-008 的长期版本）。
- §12：登记 `agent/*.go` 与 `agent/*_test.go`。
- §14：把 Agent Loop 三条归属标注为「T07 交付中（Contract APPROVED，实现未交付，证据待补）」；新增「Session 前缀删除能力 → 独立 Session 扩展任务（T07 前置依赖）」与「Todo 适配 / 生产 `Summarizer` → 服务装配任务 T08」；原「装配层（T07）」引用改指 T08。

## Open Risks

- 取消/失败不提交：模型下一轮看不到被取消 Run 的半截工作；这是 D1 的直接后果。
- 整个 Run 持有会话执行权：同会话其他 Run 在 LLM/工具耗时期间排队（AC-005 的必然代价）。
- `finish_reason=unknown` 按 D5 同源推导为 `FAILED + provider_failure`（Owner 未单独定义）；若 Owner 要求不同映射，需 Contract Revision。
- 空文本 `finish_reason=stop` 视为合法完成（`core` 不禁止空文本）；若要求拒绝，需新增规则并说明 `ErrorKind`。
- `KeepRecent=0` 且超预算时，压缩可能覆盖最后一条 user 消息（会话只保留摘要）。`DropLeading` 不会删除本 Turn 追加的消息，但被摘要覆盖的历史文本不再逐字留在 Session；装配侧应以 `KeepRecent ≥ 1` 控制。
- 「工具执行尝试」口径：取消恰好落在某次工具调用时该次尝试计入 `ToolCalls`，但工具实现可能并未真正运行（执行器在 `ctx` 已结束时提前返回）；AC-006② 覆盖的场景不受影响。
- `head` 越过 `baseLen` 依赖 `contextmgr` 的原子单元压缩语义；若该语义变更需回归。
- 前置 Session 扩展任务的编号/分支/排期未定，T07 处于阻塞；该任务的接口命名若与本 Contract 的 `DropLeading` 不同，T07 只做适配，Design 以该任务 Contract 为准。

## Owner Decision Record

来源：Owner 在 `analysis` Decision Checkpoint 的明确书面决定（2026-10-20，本 Session）。

| 编号 | Owner 决定 | 在本文档中的落地 |
| --- | --- | --- |
| D1 | 批准仅 `COMPLETED` 提交当前 Turn；整个 Run 在同一次 `RunTurn` 中执行；失败/超限/取消不提交；已提交历史不回滚；不假定外部副作用可撤销；明确失败时 `RunResult.Messages` 可含未完成工具调用序列，但不得把未配对消息提交到 Session、不得伪造结果 | Selected Design §D1、Business Invariants INV-001/INV-005、Failure Semantics |
| D2 | 批准 `DropLeading(n int) error` 的设计需求，但**不授权 T07 修改 T04 公共接口**；必须在同一 `RunTurn` 的事务性工作副本内删除已压缩前缀、与追加和 Summary 一并提交或回滚；必须校验 `n` 边界、不得误删本轮输入；先确认 T04 交付状态：已集成则新建最小独立 Session 扩展任务；禁止重入 `Save`／绕锁／直接改内部数据 | External Dependency、Selected Design §D2、Allowed/Forbidden、Verification（AC-007） |
| D3 | 批准工具预算整批预检：额度不足返回 `LIMIT_EXCEEDED + tool_call_limit`，整批不执行、不伪造结果；分别统计实际发起的 LLM 调用与工具执行尝试，计数规则入 Contract 并有边界测试 | Selected Design §D3、INV-003、Verification（边界测试）、Decision Status 前置说明 |
| D4 | 批准 `Design Impact = UPDATE`；先解决 Task 中的 Design Impact 冲突再同步长期 Design；T07 不纳入生产 Todo Adapter 与真实 LLM `Summarizer`，归入 T08 服务装配并由 TaskBuilder 正式确认，不得形成无人负责的缺口 | Design Impact、Selected Design §D4 |
| D5 | 修改模型结束语义：`stop` + 合法最终回答 → `COMPLETED`；存在合法 ToolCall → 继续工具流程；`length` → `FAILED + provider_failure`；`tool_calls` 无 Call → `FAILED + provider_failure`；规则必须有 Fake LLM 确定性测试 | Selected Design §D5、INV-008、Failure Semantics、Verification（D5 表驱动） |
| 其他 | 每次 Run 独立 Executor；同 Session 串行、不同 Session 并发；`RunResult.Validate()` 全通过；不调用真实付费 LLM；不修改 Workflow Engine、不绕过 Gate | Interfaces/Data、INV-002/INV-004/INV-007、Verification |

由本决定关闭的 Task 待决项：OQ-2（Design Impact = `UPDATE`）、OQ-1（不纳入 Todo / 生产 `Summarizer`）、OQ-3（里程碑 `M2-agent-runtime-loop` 沿用）。OQ-4（测试替身归属）由 Analyst 定为「包内 `_test.go`、不导出」，与仓库既有约定一致。

T04 交付状态核实：`agent-session-manager-v1` 的 contract / review / owner / delivery 全部为终态 PASS，feature 已合并进 `develop`（`73fef46`，存在于 `origin/develop`），因此 D2 走「新建最小独立 Session 扩展任务」路径。

未由 T07 实施、需其他角色承接的事项（本 Contract 不代替其批准）：

- TaskBuilder：关闭 `task.md` 的 OQ-1/OQ-2/OQ-3；登记前置 Session 扩展任务与 T08 服务装配范围（含 Todo 适配、生产 `Summarizer`、真实 tokenizer 校准的归属）。
- Owner / TaskBuilder：创建前置 Session 扩展任务及其 feature branch，走完整流程并集成到 `develop`；之后 T07 解除 `blocked` 再进入 Coder。
