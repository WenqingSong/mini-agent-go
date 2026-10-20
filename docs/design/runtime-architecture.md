# Runtime 核心架构（mini-agent-go）

> 本文件是项目级长期设计事实（WHY / architecture）：模块边界、依赖方向、数据模型、状态机、不变量、失败语义与文件所有权。
> 任务内的决策过程与逐条接口契约见 `.agent/tasks/runtime-core-contracts-v1/contract.md`（契约层）与 `.agent/tasks/builtin-agent-tools-v1/contract.md`（内置工具，Owner `APPROVED`，Evidence Commit A1）。
> 两者冲突时：Contract 记录本任务已确认的取舍，本文件记录跨任务长期事实；出现不一致必须回写，不允许长期漂移。

## 1. 范围

本文件描述 Runtime 的架构基线：L0 `core` 契约层、五个 L1 抽象包与 L2 `builtin` 内置工具包，供 Agent Loop、Provider、Session 持久化、Context 管理、运行日志与装配层共同依赖。

不在本文件范围：具体 Provider 协议、工具沙箱与权限、持久化选型（DB / Redis）、CLI / HTTP 入口、部署流水线。

## 2. 分层与依赖方向

```text
L0  core          公共契约：消息、工具调用、运行状态、错误、配对与校验（仅标准库）
L1  llm           LLM Provider 接口
    tool          Tool / Registry 接口
    session       Session / Todo 存储接口
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
| `session` | Session / Todo 数据类型与 `Store` 存取抽象 | 任何持久化实现、内存实现、迁移 |
| `builtin` | 五个内置工具（`calculator`、`mock_search`、`todo_create`、`todo_list`、`todo_complete`）、固定有序 Mock 数据集、`TodoOperations` 写入端口与领域错误码 | `tool.Registry` 实现、工具调度与失败回填、Todo 存储 / ID 生成 / 生命周期、网络与第三方 SDK |
| `contextmgr` | 上下文预算、窗口选择与压缩的契约 | 具体压缩算法、tokenizer、消息存储 |
| `runlog` | 运行事件模型与 `Sink` 接口 | 落盘格式、采样、上报后端 |

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
| 不可恢复工具失败终止 Run | `FAILED` | `tool_failure` | 必须带 `CallID` |
| LLM 调用达上限 | `LIMIT_EXCEEDED` | `llm_call_limit` | |
| 工具调用达上限 | `LIMIT_EXCEEDED` | `tool_call_limit` | |
| 用户取消 | `CANCELLED` | `cancelled` | 保留已有消息，不回滚 |
| 整体超时 | `CANCELLED` | `deadline_exceeded` | 与用户取消靠 Kind 区分 |
| 消息序列非法 | `FAILED` | `invalid_pairing` / `invalid_argument` | 在进入下一次 LLM 调用前拒绝 |

一致性范围：本契约层无多存储、无消息队列、无异步复制，因此不存在跨系统最终一致性问题。Run 内的部分完成（一次响应中多个 ToolCall 只部分成功）必须通过“每个 Call 各自产生 Result”表达，不允许丢弃成功结果或静默跳过失败调用。重复 Result 由配对校验拒绝，不做静默覆盖。

## 8. 会话归属与工具身份

- Session 是消息历史与 Todo 的**唯一所有者**；Run 只是通过 `ExecutionRef{SessionID, RunID}` 引用会话。
- 工具执行身份通过显式参数传递（`tool.Request` 内嵌 `ExecutionRef` + `core.ToolCall`），不使用 `context.Value` 承载业务身份；`context.Context` 只承担取消与超时。
- 不向 Tool 传递可变的 `Session`、`Todo` 或 `Store`：Tool 若需要会话状态，必须由装配层显式注入能力或传入值，避免工具绕过运行时直接改写会话状态。
- 会话持久化是后续任务；本层只固定 `Store` 抽象（`Load` / `Save`），不实现任何存储。

内置工具侧的落地规则（`builtin`）：

- 会话身份唯一来源是可信的 `tool.Request.SessionID`：`todo_*` 工具每次调用都把 `req.SessionID` 显式传给端口；`SessionID` 为空时拒绝执行（`missing_session_id`）且不调用端口。
- 模型不得经 JSON 参数选择会话：`Arguments` 中出现 `session_id` / `sessionId` / `session` 一律 `invalid_arguments`。
- Todo 工具不持有 Todo 状态副本、不生成 ID、不做读改写：`Create` / `List` / `Complete` 的会话作用域、ID 生成、状态迁移与幂等「完成」全部由注入的 `builtin.TodoOperations` 实现方完成；端口以 `builtin.ErrTodoNotFound` 表示「本会话不存在该待办」。
- 失败分级：领域失败返回 `core.ToolResult{IsError: true}` 且 `error` 为 nil，内容为 `{"error":{"code","message"}}`；基础设施失败（端口返回非领域错误、`ctx` 已结束、端口未注入）返回 `*core.RunError`，原始错误只进 `Cause`，不得回显内部细节。
- 装配层（T07）负责把 `session.TodoOperations` 适配成 `builtin.TodoOperations`，并承担 Todo ID 生成与状态迁移；`builtin` 不 import `session` 的实现，`session` 也不 import `builtin`。

## 9. 上下文管理（contextmgr）

- 职责边界：决定“送进 LLM 的消息视图”——token 预算、窗口选择、压缩。
- 契约只暴露 `Request{Messages, TokenBudget, KeepRecent}` 与 `Result{Messages, Compacted, DroppedMessages}`；token 估算与压缩算法属后续任务。
- 输出视图仍必须是合法的配对序列（不得因截断产生孤儿 Result），否则装配层必须报 `invalid_pairing` 而不是把非法消息发给 Provider。

## 10. 运行日志（runlog）

- 日志是**观察者**：`Sink.Record` 返回错误不得改变 Run 状态、计数或消息序列（旁路原则）。
- 事件以 run 为单位组织（`ExecutionRef`），携带 step、CallID 与已脱敏的结构化载荷；具体落盘格式、采样、上报属后续任务。
- 运行日志不是会话事实来源：消息历史归 Session，运行结论归 `RunResult`。

## 11. 不变量

| 编号 | 不变量 | T00 证据 |
| --- | --- | --- |
| INV-001 | 分层方向：`core` 仅标准库；L1 仅依赖 `core`；L1 互不依赖 | `go list` import 核对 |
| INV-002 | 配对完整性：Call 与 Result 一一对应，非法配对必须报 `invalid_pairing` | 表驱动测试（0/1/N 调用 + 三类非法配对） |
| INV-003 | 状态/错误逐状态一致（`RUNNING`、`COMPLETED` 无错误；三个失败终态必须带匹配 Kind） | 映射表正反用例 |
| INV-004 | 双预算：两个上限都必须为正，计数值不越界，触限只能 `LIMIT_EXCEEDED` | 策略校验 + 映射测试 |
| INV-005 | 会话归属：Session 独占消息与 Todo，Tool 只拿 ID 或受限能力接口（不持会话状态副本） | 签名核对 + 工具层测试（运行期证据归 Session Manager / 装配任务） |
| INV-006 | 取消/超时只能 `CANCELLED`，靠 Kind 区分 | 映射测试（运行期中断行为归 Loop 任务） |
| INV-007 | 可恢复工具失败回填继续；不可恢复才 `FAILED + tool_failure` | 映射测试（分类策略归 Loop 任务） |
| INV-008 | 日志旁路：Sink 失败不改变 Run 结果 | `Discard` 副作用测试（运行期旁路归 Loop 任务） |

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
| `tool/tool.go` | tool | Tool / Request / Registry |
| `session/session.go` | session | TodoStatus / Todo / Session / Store |
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
| `runlog/runlog.go` | runlog | EventKind / Event / Sink / Discard |
| `runlog/runlog_test.go` | runlog | `Discard` 无副作用 |

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
- INV-005 / INV-006 / INV-007 / INV-008 的运行期行为（T00 只有签名、映射函数与契约级测试）。
- INV-B05 / INV-B06 的真实会话隔离、跨会话不可写、Todo 生命周期与持久化并发：`builtin` 只有测试专用 Fake 证据，运行期证据归 Session Manager 与装配层。
- Todo 端到端行为（模型发起 `todo_*` 调用到真实 Todo 落库）：需要 T07 把 `session.TodoOperations` 适配成 `builtin.TodoOperations` 后才可验证。
- `contextmgr.Request.TokenBudget` 的实际语义需要 tokenizer 支撑，尚未实现。

归属后续任务：

| 内容 | 归属 |
| --- | --- |
| 取消/超时在 Loop 中的实际中断点与消息保留策略 | Agent Loop 任务 |
| 工具失败可恢复性分类、同 step 多调用顺序与并发 | Agent Loop 任务 |
| runlog 旁路在真实运行时成立、事件完整性 | Agent Loop 任务 |
| `session.Store` 的内存与持久化实现、Todo 生命周期 | Session 持久化任务 |
| `builtin.TodoOperations` 的适配实现、Todo ID 生成、状态迁移、`ErrTodoNotFound` 映射 | 装配层任务（T07） |
| `session.TodoOperations` 与 `builtin.TodoOperations` 两套端口的集成、真实会话隔离与 Todo 端到端验收 | 装配层 + 集成验收 |
| token 预算、窗口截断、压缩算法 | Context 任务 |
| Provider 重试、限流、streaming、鉴权 | Provider 任务 |

## 15. 演进规则

- 公共契约变更必须走 Contract Revision 并由 Owner 重新 `APPROVED`，随后同步更新本文件；旧版本由 Git 历史承载，本文件始终是最新权威。
- 后续任务不得在实现中私自扩展公共符号（新增类型、字段、方法）；需要时先回到 Contract。
- 本文件不记录任务级决策过程（谁在何时选了哪个方案），只记录对后续任务有约束力的架构事实。
