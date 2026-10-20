# Technical Contract

## Decision Status

APPROVED（Owner Contract Decision 2026-10-20；Evidence Commit A1 由 `state.contract.target` 引用，决定原文见 `Owner Decision Record`）。

任务：`runtime-core-contracts-v1`（Milestone `M1-runtime-core-contracts`，Design Impact `NEW` → `docs/design/runtime-architecture.md`）。

## Problem

仓库当前没有业务 Go 代码，也没有根 `go.mod` / `Makefile`。本任务要在这一空基线上一次性固定 Runtime 公共契约：根 Module、包分层与依赖方向、统一消息与工具调用模型、运行状态与错误语义、Session/Todo 归属、取消与预算语义、文件所有权，以及可真实执行的 `make test`。

难点不在“写出接口”，而在**这些选择被五个并行模块（LLM / Tool / Session / Context / RunLog）依赖后不可回头**。本 Contract 固化已由 Owner 确认的选择，作为 Coder 实现与后续任务（T01—T06）的共同前提。

## Verified Current Behavior

- VERIFIED：仓库根无 `go.mod`、无 `Makefile`、无 Go 源码；`.agent/tasks/` 此前完全未跟踪（`git ls-files .agent/tasks` 为空）。
- VERIFIED：当前任务分支 `feature/runtime-core-contracts-v1` 基线 `367c655`（= 本地 `develop`，领先 `origin/develop` `ebf8778` 1 个 commit）；任务开始时工作区除未跟踪任务文件外无修改。
- VERIFIED：`origin` = `https://cnb.cool/framework-station/mini-agent-go`；GitHub 镜像 = `https://github.com/WenqingSong/mini-agent-go`（`.cnb.yml` push 阶段）。任务分支创建时尚未存在于 `origin`（`git ls-remote --heads origin 'refs/heads/feature/*'` 为空），需 Analyst push 后才能满足 `handoffReady`。
- VERIFIED：`.cnb.yml` 在 `(main|develop)` 的 `pull_request` 阶段以 `golang:1.24` 执行单条 `make test`。
- VERIFIED：`.agent/engine` 是独立 Module（`module workflow-v2-engine`、`go 1.23.0`、依赖 `gopkg.in/yaml.v3`）；根 `go.mod` 存在时 `go list ./...` / `go build ./...` 不会进入它（本机临时目录实测：点目录忽略 + 嵌套 Module 剪枝）。
- VERIFIED：本机 `go1.24.1 linux/amd64`、`GNU Make 4.3`。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`；本任务不占用任何全局资源（`resources.reservations` 为空，错误语义使用代码内枚举，不申请 `error_code_domain`）。
- VERIFIED：`.agent/bin/workflow-check gate coder-start` 的判定为 `blocked.active=false` + `handoffReady` + `contractValid`（`contract.status=APPROVED`、`contract.target` 为真实且为 HEAD 祖先的 commit、`contract.md@target` 必须与工作区逐字节一致）+ `resourcesAuthorized`（本任务跳过）。
- UNKNOWN：`cnb.cool` 是否提供 `go-import` 元信息，即该 module 路径能否被外部 `go get` 拉取。按 Owner 决定本轮不验证，记录为未验证事项。

## Recommendation

RECOMMENDATION（Owner 已确认并调整，最终选择见 `Selected Design`）：

1. module `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，根模块零第三方依赖。
2. 一个 L0 契约包 `core` + 五个 L1 接口包，依赖只能指向 `core`。
3. 单一 `core.Message` struct + 类型化可选字段；正文 `[]ContentPart`（v1 仅 `text`）；保留 `Raw` 作为 Provider 载荷回放位；配对校验为独立入口，不在构造期强制。
4. Session 抽象 `session.Store` 接口，不实现持久化；Session 是消息历史与 Todo 的唯一所有者。
5. 会话身份显式传参（`core.ExecutionRef`），`context` 只承担取消/超时。
6. LLM 调用次数与工具调用次数**分别**计数与限制；工具失败默认回填继续。
7. 统一 `*core.RunError` + `ErrorKind`，不引入跨模块错误码域。
8. 仅接口 + 契约测试，不做 reference 实现。
9. `make test` 与 `.cnb.yml` 入口一致。

关键取舍：

- **无 reference 实现**（Owner 确认）：工作量最小、契约最纯；代价是「会话归属」「日志旁路」等不变量本轮无运行期证据，只能靠签名核对，并在本 Contract 中明确归属后续任务，不虚报 PASS。
- **状态集不含独立 `TOOL_FAILED`**（Owner 确认）：工具失败默认不是终态；「工具失败」通过 `FAILED + tool_failure` 与 `(Status, ErrorKind)` 联合映射区分，满足 AC-003 的“`RunStatus` 与错误类型能区分”。
- **`contextmgr` 而非 `context`**（Owner 确认）：避开标准库 `context` 遮蔽，代价是与 Task 文稿中的「Context 模块」措辞不同。

## Selected Design

### 1. Module 与包分层

```text
go.mod          module cnb.cool/framework-station/mini-agent-go / go 1.24 / 零 require
Makefile        test / build / vet / test-engine
core/           L0 公共契约：消息、工具调用、运行状态、错误、配对与校验
llm/            L1 LLM Provider 接口
tool/           L1 Tool / Registry 接口
session/        L1 Session / Todo Store 接口
contextmgr/     L1 上下文预算、窗口与压缩接口
runlog/         L1 运行日志 Sink
```

- L0 `core` 只 import 标准库；L1 各包只 import 标准库与 `core`；L1 之间零 import（无循环、无隐式耦合）。
- 全部为公共包（非 `internal/`），供后续模块与外部消费者复用。
- 根模块零第三方依赖（不引入 yaml/测试框架）。

### 2. 消息与工具调用模型

- 单一 `core.Message` struct + 类型化可选字段（不做 content-part 接口多态）。
- 正文统一为 `Content []ContentPart`，v1 只支持 `ContentTypeText`；多模态是后续 Revision 的扩展点，不改 struct 形态。
- `Raw json.RawMessage` 保存 Provider 原始载荷，供日志与回放；契约层不解析。
- Tool Call 与 Result 通过 `ToolCall.ID` / `ToolResult.CallID` 配对；结果是独立的 `RoleTool` 消息（一条消息一个 Result），多结果 = 多条消息。
- 配对在**独立校验入口**（`ValidateToolPairing` / `ValidateMessages`）强制，不在构造期强制：Message 是纯数据，构造期强制会迫使全部模块走构造函数。

### 3. 运行状态

终态 4 值 + 非终态 1 值（字符串值严格大写，按 Owner 指定）：

| Status | 语义 | 终态 | 要求的 Error |
| --- | --- | --- | --- |
| `RUNNING` | 运行中 | 否 | 无（Error 必须为 nil） |
| `COMPLETED` | 正常完成 | 是 | 无（Error 必须为 nil） |
| `FAILED` | 失败（Provider / 不可恢复工具错误 / 内部错误 / 契约错误） | 是 | 有，Kind ∈ {`provider_failure`,`tool_failure`,`internal`,`invalid_argument`,`invalid_pairing`} |
| `LIMIT_EXCEEDED` | 预算耗尽 | 是 | 有，Kind ∈ {`llm_call_limit`,`tool_call_limit`} |
| `CANCELLED` | 用户取消或整体超时 | 是 | 有，Kind ∈ {`cancelled`,`deadline_exceeded`} |

`RunResult.Validate()` 按上表**逐状态**校验，不采用“所有终态都必须有错误”的笼统规则。

### 4. 预算

- `RunPolicy{MaxLLMCalls, MaxToolCalls}`：两者分别计数、分别限制，任一 `<= 0` 判 `invalid_argument`；不再使用单一 step 预算。
- `RunResult.LLMCalls` / `ToolCalls` 为实际发生次数；始终满足 `LLMCalls <= MaxLLMCalls`、`ToolCalls <= MaxToolCalls`。
- 触限即终态 `LIMIT_EXCEEDED` + 对应 `ErrorKind`；不得表现为 `COMPLETED`，也不得伪装成工具失败继续运行。
- 取消（`ctx.Done()`）与整体超时同样不得伪装成工具失败；二者的区分靠 `ErrorKind`（`cancelled` / `deadline_exceeded`），终态同为 `CANCELLED`。

### 5. 工具失败

- 普通（可恢复）工具执行失败：生成 `core.ToolResult{IsError: true, CallID: <对应 Call>}` 回填给模型继续决策，**不终止 Run**，RunStatus 保持 `RUNNING`。
- 只有不可恢复且最终导致 Run 失败的工具错误，才产出 `FAILED + tool_failure`。
- 工具失败的可恢复性分类策略、以及回调顺序与并发调度属于 Agent Loop Task，**不在 T00 定义也不在 T00 验证**。

### 6. Session / Todo 归属与身份传递

- Session 是消息历史与 Todo 的唯一所有者（`session.Session` 同时持有 `Messages` 与 `Todos`）。
- `session.Store` 只定义 `Load` / `Save` 抽象；T00 不实现任何 Store（不落盘、不内存实现）。
- 工具执行身份通过 `tool.Request` 内嵌 `core.ExecutionRef{SessionID, RunID}` + `core.ToolCall` 显式传递；禁止 `context.Value` 携带业务身份，禁止向 Tool 传可变 `Session` / `Todo` / `Store`。
- `context.Context` 只承担取消与超时。

### 7. 上下文（contextmgr）

- 职责边界：上下文预算、窗口选择与压缩的**契约**。
- T00 只定义 `Manager` 接口与 `Request` / `Result`；token 估算、截断算法、压缩策略归属后续任务。

### 8. 接口深度

- T00 交付：公共模型、接口、可独立验证的纯校验逻辑、契约测试、`Makefile`、长期 Design。
- 明确不交付：Agent Loop、并发工具调度、Provider 客户端、任何真实 Tool、任何持久化、CLI / HTTP Server。
- 「需要后续模块才能验证」的不变量在 `Delegated Verification` 中列名归属，不得在 T00 声明为已验证。

### 9. 工程入口

- `make test` → `go test ./...`（根模块，不含 `.agent/engine`），与 `.cnb.yml` 的 `pr-check` 一致。
- `make build` → `go build ./...`；`make vet` → `go vet ./...`；`make test-engine` → `GOWORK=off go -C .agent/engine test ./...`（保持 Engine 独立 Module，不被根模块吞掉）。
- `go.mod` 与 `Makefile` 是工程公共文件，不属于任何业务模块。

## Interfaces and Data

以下公共符号是 Coder 必须保持的契约；包内私有实现、注释、测试组织自由。

```go
// package core

type Role string // "system" | "user" | "assistant" | "tool"

type ContentType string // v1 仅 "text"

type ContentPart struct {
	Type ContentType
	Text string
	Raw  json.RawMessage // 可选：Provider 原始片段
}

type Message struct {
	ID         string
	Role       Role
	Content    []ContentPart
	ToolCalls  []ToolCall   // 仅 RoleAssistant 允许非空
	ToolResult *ToolResult  // 仅 RoleTool 必须非空
	Raw        json.RawMessage
}

type ToolCall struct {
	ID        string          // 所属 assistant 消息内唯一；配对主键
	Name      string
	Arguments json.RawMessage // 原始 JSON，契约层不解析、不改写
}

type ToolResult struct {
	CallID  string // 配对主键
	Name    string // 便于日志与模型阅读，不参与配对
	Content string // 模型可见结果文本；IsError=true 时为错误描述
	IsError bool
}

type ToolSpec struct { // llm.Request.Tools 与 tool.Tool.Spec 共用，避免 L1 互相依赖
	Name        string
	Description string
	Parameters  json.RawMessage // JSON Schema
}

type ExecutionRef struct {
	SessionID string
	RunID     string
}

type RunStatus string

const (
	RunStatusRunning       RunStatus = "RUNNING"
	RunStatusCompleted     RunStatus = "COMPLETED"
	RunStatusFailed        RunStatus = "FAILED"
	RunStatusLimitExceeded RunStatus = "LIMIT_EXCEEDED"
	RunStatusCancelled     RunStatus = "CANCELLED"
)

type RunPolicy struct {
	MaxLLMCalls  int // 必填 > 0
	MaxToolCalls int // 必填 > 0
}

type RunResult struct {
	ExecutionRef
	Status    RunStatus
	LLMCalls  int
	ToolCalls int
	Messages  []Message
	Error     *RunError
}

type ErrorKind string

const (
	KindInvalidArgument  ErrorKind = "invalid_argument"
	KindInvalidPairing   ErrorKind = "invalid_pairing"
	KindCancelled        ErrorKind = "cancelled"
	KindDeadlineExceeded ErrorKind = "deadline_exceeded"
	KindLLMCallLimit     ErrorKind = "llm_call_limit"
	KindToolCallLimit    ErrorKind = "tool_call_limit"
	KindToolFailure      ErrorKind = "tool_failure"
	KindProviderFailure  ErrorKind = "provider_failure"
	KindInternal         ErrorKind = "internal"
)

type RunError struct {
	Kind    ErrorKind
	Op      string // "<pkg>.<func>"，如 "tool.execute"
	Message string
	CallID  string // 工具失败时定位到具体调用
	Cause   error
}

func (e *RunError) Error() string
func (e *RunError) Unwrap() error
func NewError(kind ErrorKind, op, msg string) *RunError
func KindOf(err error) ErrorKind // 非 RunError → internal
func StatusOf(err error) RunStatus // nil → COMPLETED；cancelled/deadline_exceeded → CANCELLED；
                                   // llm_call_limit/tool_call_limit → LIMIT_EXCEEDED；其余 → FAILED
func IsTerminal(s RunStatus) bool // RUNNING → false
func (p RunPolicy) Validate() error
func (r RunResult) Validate() error
func (m Message) Validate() error
func (r ToolResult) Validate() error
func ValidateToolPairing(msgs []Message) error
func ValidateMessages(msgs []Message) error
```

```go
// package llm

type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

type Request struct {
	core.ExecutionRef
	Model       string
	System      string // 纯文本 system prompt（当前 Provider 共同子集）
	Messages    []core.Message
	Tools       []core.ToolSpec
	MaxTokens   int
	Temperature *float64
}

type Response struct {
	Message      core.Message // Role 必须为 assistant
	FinishReason FinishReason // stop | tool_calls | length | unknown
	Usage        Usage
	Raw          json.RawMessage
}

type Usage struct{ InputTokens, OutputTokens int }

func (r Response) Validate() error
```

```go
// package tool

type Tool interface {
	Spec() core.ToolSpec
	Execute(ctx context.Context, req Request) (core.ToolResult, error)
}

type Request struct {
	core.ExecutionRef
	Call core.ToolCall // 调用标识与原始参数
}

type Registry interface {
	Lookup(name string) (Tool, bool)
	Specs() []core.ToolSpec
}
```

```go
// package session

type TodoStatus string // pending | in_progress | completed | cancelled

type Todo struct {
	ID     string
	Title  string
	Status TodoStatus
}

type Session struct {
	ID       string
	Messages []core.Message // 消息历史归属 Session
	Todos    []Todo         // Todo 归属 Session
}

type Store interface {
	Load(ctx context.Context, id string) (Session, error)
	Save(ctx context.Context, s Session) error
}
```

```go
// package contextmgr

type Request struct {
	core.ExecutionRef
	Messages    []core.Message
	TokenBudget int // > 0 表示上下文 token 上限；<= 0 表示不限制
	KeepRecent  int // 至少保留最近 N 条消息
}

type Result struct {
	Messages        []core.Message
	Compacted       bool // 是否发生压缩
	DroppedMessages int  // 窗口截断丢弃的消息数
}

type Manager interface {
	Build(ctx context.Context, req Request) (Result, error)
}
```

```go
// package runlog

type EventKind string // run_started | llm_request | llm_response | tool_started | tool_finished | run_finished

type Event struct {
	Kind    EventKind
	Ref     core.ExecutionRef
	Step    int
	CallID  string
	Summary string
	Data    json.RawMessage // 已脱敏的结构化载荷
	Error   *core.RunError
}

type Sink interface {
	Record(ctx context.Context, ev Event) error
}

func Discard() Sink // 丢弃实现（默认装配与测试用）
```

数据约束：本任务无数据库、无 Redis、无 MQ、无 migration；唯一状态抽象是 `session.Store`，不实现。

全局资源：**无**。`resources.reservations` 保持为空；`ErrorKind` 是代码内枚举，不是 Registry 资源。

## Business Invariants

- INV-001（依赖方向）：`core` 只 import 标准库；`llm` / `tool` / `session` / `contextmgr` / `runlog` 只 import 标准库与 `<module>/core`，L1 之间零 import。
- INV-002（配对完整性）：每条 assistant 消息内 `ToolCall.ID` 非空且唯一；每个 ToolCall 恰好被一条 `RoleTool` 消息的结果引用；不存在孤儿 Result；缺失、重复标识、孤儿 Result 一律返回 `*core.RunError{Kind: KindInvalidPairing}`，错误可定位到具体 CallID。
- INV-003（状态/错误逐状态一致）：`RUNNING` 与 `COMPLETED` 的 `Error` 必须为 nil；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 必须携带与 `Selected Design` §3 表一致的 `ErrorKind`；`StatusOf(err)` 与 `Status` 必须一致。
- INV-004（双预算）：`RunPolicy.MaxLLMCalls <= 0` 或 `MaxToolCalls <= 0` 判 `invalid_argument`；任何 `RunResult` 满足 `LLMCalls <= MaxLLMCalls` 且 `ToolCalls <= MaxToolCalls`；触限只能表现为 `LIMIT_EXCEEDED`，不得表现为 `COMPLETED`。
- INV-005（会话归属）：Session 是消息历史与 Todo 的唯一所有者；`tool.Request` 只携带 `SessionID` / `RunID` / `CallID`，不携带可变 `Session`、`Todo` 或 `Store`。
- INV-006（取消与超时）：取消或整体超时只能产生 `CANCELLED`，靠 `ErrorKind` 区分 `cancelled` 与 `deadline_exceeded`；不得归类为 `COMPLETED`、`FAILED` 或工具失败继续运行。
- INV-007（工具失败不越级）：可恢复工具失败必须以 `ToolResult{IsError:true}` 回填并使 Run 保持 `RUNNING`；只有不可恢复且终止 Run 的工具失败才产生 `FAILED + tool_failure`。
- INV-008（日志旁路）：`runlog.Sink` 返回错误不得改变 `RunStatus`、计数值或消息序列。

## Failure and Consistency Semantics

| 情形 | 终态 | ErrorKind | 说明 |
| --- | --- | --- | --- |
| 模型返回无 Tool Call 的 assistant 消息 | `COMPLETED` | 无 | 成功不表示任何持久化已发生 |
| Provider 调用失败 | `FAILED` | `provider_failure` | 本轮不实现重试/退避，是否重试由调用方决定 |
| 可恢复工具失败 | 保持 `RUNNING` | 无（`ToolResult.IsError`） | 回填模型继续决策 |
| 不可恢复工具失败终止 Run | `FAILED` | `tool_failure` | `RunError.CallID` 必填 |
| LLM 调用次数达上限 | `LIMIT_EXCEEDED` | `llm_call_limit` | 不再发起新调用 |
| 工具调用次数达上限 | `LIMIT_EXCEEDED` | `tool_call_limit` | 不再执行新工具 |
| 用户取消 | `CANCELLED` | `cancelled` | 已有消息保留，不回滚、不截断 |
| 整体超时 | `CANCELLED` | `deadline_exceeded` | 与用户取消靠 Kind 区分 |
| 消息序列非法 | `FAILED` | `invalid_pairing` / `invalid_argument` | 在进入下一次 LLM 调用前拒绝 |

一致性规则：

- 无多存储、无异步系统；本任务不存在最终一致性、重复投递、重试幂等、部分提交问题。
- 部分完成：一次 LLM 响应内多个 ToolCall 必须各自产生 Result（成功与失败都算），顺序以 assistant 消息中 `ToolCalls` 数组为准；并发执行不在 T00。
- 重试：T00 不定义重试；契约只保证同一 Run 内 `(RunID, CallID)` 唯一，重复 Result 由 INV-002 拒绝，不做“最后一次获胜”静默覆盖。

## Allowed / Forbidden Changes

- 允许：根 `go.mod`（`module cnb.cool/framework-station/mini-agent-go`、`go 1.24`、零 require）；根 `Makefile`（`test` / `build` / `vet` / `test-engine`）；`core/`、`llm/`、`tool/`、`session/`、`contextmgr/`、`runlog/` 下的契约源码与 `_test.go`。
- 允许（Analyst，已在本轮执行）：`docs/design/runtime-architecture.md`。
- 禁止：`go.work`、跨 module `replace`、任何第三方依赖、修改 `.agent/engine/**`、`.agent/specs/**`、`.agent/roles/**`、`.agent/workflow.yaml`、`docs/design/agent-workflow.md`、`.cnb.yml`。
- 禁止：Agent Loop 调度、并发工具执行、Provider 客户端、真实 Tool 实现、Session/Todo 持久化、CLI / HTTP Server、可运行二进制。
- 禁止：新增本 Contract 未列出的公共符号（需 Contract Revision）。
- 禁止：在共享 `develop` 上提交（本任务无资源预留，Registry-only 例外不适用）。

## Verification Requirements

- INV-001 / AC-001 → 根目录 `go build ./... && go vet ./... && go test ./...` 退出码 0；再执行

  ```bash
  go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...
  ```

  核对：`core` 行不含 `<module>/`；`llm` / `tool` / `session` / `contextmgr` / `runlog` 行只含 `<module>/core`。出现其他 `<module>/xxx` 即失败。
- INV-002 / AC-002 → 表驱动测试：assistant 消息 0 / 1 / N（≥2）个 ToolCall 的构造与往返；三类非法配对（缺失 Result、重复 Result 标识、孤儿 Result）各断言 `KindOf(err) == KindInvalidPairing` 且错误指向具体 CallID。
- INV-003 / INV-004 / INV-006 / INV-007 / AC-003 → 表驱动测试：`(Status, ErrorKind)` 映射表逐项正反用例（`COMPLETED` 带 error 必须失败、`LIMIT_EXCEEDED + tool_failure` 必须失败、`CANCELLED + cancelled` / `+ deadline_exceeded` 必须通过、`RUNNING` 带 error 必须失败）；`RunPolicy{0,1}` / `{1,0}` 必须报错。
- INV-005 → 代码核对：`tool.Request` 字段集合（`go doc` 或源码）不含 `session.*` 类型，Cleaner 记录证据。无运行期测试，见 `Delegated Verification`。
- INV-008 → 契约测试覆盖 `runlog.Discard()` 无副作用；旁路行为归属后续任务。
- AC-005 → `make -n test` 非空；`make test` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 单独通过。
- AC-006 → `git ls-files` 与 `docs/design/runtime-architecture.md` 文件所有权表逐条比对；每个源文件只归属一个模块，`go.mod` / `Makefile` 标记为工程公共文件。
- AC-004 → Owner 已 `APPROVED`（`state.contract.status=APPROVED`、`contract.target=A1`）；Cleaner 做 Task ↔ Contract ↔ Design ↔ 实现四方核对。

## Delegated Verification

以下不变量或语义在 T00 无运行期证据，明确归属后续任务，T00 不得声明为已验证：

| 项 | 归属 |
| --- | --- |
| INV-006 取消/超时在 Loop 中的实际中断行为 | Agent Loop 任务 |
| INV-007 工具失败可恢复性分类与回填顺序 | Agent Loop 任务 |
| INV-008 runlog 旁路在真实运行时成立 | Agent Loop 任务 |
| INV-005 会话归属的运行期证据（Store 实现约束） | Session 持久化任务 |
| `contextmgr` token 预算、窗口截断、压缩算法 | Context 任务 |
| Provider 重试/限流/streaming | Provider 任务 |

## Open Risks

- module 路径 `cnb.cool/framework-station/mini-agent-go` 的外部 `go get` 可用性未验证（UNKNOWN）；路径一旦被消费方引用即难更改。
- 无 reference 实现使 INV-005、INV-006、INV-007、INV-008 缺乏运行期证据，只能靠签名、映射函数与文档核对。
- `go list` 依赖方向检查未进入 CI（`.cnb.yml` 属 Out of Scope），依赖 Cleaner 与后续任务人工执行。
- `contextmgr.Request.TokenBudget` 在 T00 无可用的 token 估算实现，字段语义待后续任务落实；若后续需要 tokenizer，可能引发接口 Revision。
- `RunStatus` 无独立 `TOOL_FAILED`，工具失败终态依赖 `(FAILED, tool_failure)` 组合表达；任何只读 Status 的消费方需同时读取 ErrorKind。

## Owner Decision Record

决定时间：2026-10-20。决定来源：Owner 明确 Contract Decision（两轮，第二轮回应对第一轮的澄清）。适用范围：本任务 `runtime-core-contracts-v1` 及后续依赖该契约的 T01—T06。

1. **Module Path**：采用 `cnb.cool/framework-station/mini-agent-go`；本轮不要求验证外部 `go get` 可用性，记录为未验证事项。备选 `github.com/WenqingSong/mini-agent-go` 未被选择。
2. **架构与接口深度**：批准 `core` L0 + 其余 L1 分层；T00 只实现公共模型、接口、必要的纯校验逻辑与契约测试；**不**实现完整内存 Session Store、Echo Provider 或后续模块。可独立验证的不变量必须有真实测试；依赖后续模块才能验证的不变量明确归属后续 Task，不得虚报 PASS。
3. **运行状态与失败语义**：`RunStatus` 采用 `RUNNING`、`COMPLETED`、`FAILED`、`LIMIT_EXCEEDED`、`CANCELLED`；普通可恢复工具失败默认生成 Tool Result 并继续让 LLM 决策，不直接终止 Run；取消、整体超时、预算耗尽不得伪装成工具失败继续运行；LLM 调用次数与工具调用次数分别计数与限制，不以 step 作为唯一预算。
4. **包布局**：`core` + `llm` / `tool` / `session` / `contextmgr` / `runlog`；`contextmgr` 负责上下文预算、窗口及压缩；L1 模块不得形成循环依赖。
5. **超时映射**（第二轮明确）：整体超时 → `CANCELLED + deadline_exceeded`；用户主动取消 → `CANCELLED + cancelled`。
6. **状态校验规则**（第二轮明确）：`RunResult.Validate()` 必须保证 `COMPLETED` 无错误；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 有匹配的 `ErrorKind`；**不得**使用“所有终态都必须有错误”的规则。
7. **工具失败边界**（第二轮明确）：只有不可恢复且最终导致 Run 失败的工具错误才映射为 `FAILED + tool_failure`。
8. **范围约束**：以上决定不得扩大 T00 Scope，不得提前实施 T01—T06 功能；若与 Task AC / Scope 冲突，先报告，不自行修改验收目标。
9. **Git 约束**：在 Owner 确认 feature 分支就绪前不得进行 A1/A2 提交，不得将任务文件提交到 `develop`，必须保留现有文件。Owner 已确认 `feature/runtime-core-contracts-v1` 就绪；本轮按 A0（任务基线）→ A1（Contract 证据）→ A2（仅 `state.yaml` 的 Metadata）→ Design 的顺序落库。

AC 兼容性核对结论：Owner 决定与 AC-001 ~ AC-006 及 Scope 均无冲突。AC-003 要求的「工具失败」通过 `(FAILED, tool_failure)` 联合映射满足；`RUNNING` 为非终态，不进入 AC-003 的四结果终态映射表。
