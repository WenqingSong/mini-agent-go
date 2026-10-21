# Technical Contract

## Decision Status

APPROVED（Owner Contract Decision 2026-10-20，Q1 批准 / Q2 修改后批准 / Q3 批准 / Q4 批准；Evidence Commit A1 由 `state.contract.target` 引用，决定原文见 `Owner Decision Record`）。

任务：`runtime-observability-v1`（Design Impact `UPDATE` → `docs/design/runtime-architecture.md` §3 一行对齐 + §10 / §12 / §14）。

## Problem

`runlog` 目前只有事件形状与 `Sink` 接口（`EventKind` 仅类型无常量、`Event` 无耗时/状态字段、无任何实现），全仓库无 `slog` 使用、无任何 `runlog` 调用点。本任务要在不改变 Run 执行结果的前提下，交付可真实落地的结构化运行日志：同一 Run 的模型/工具调用可关联定位，失败与超时可从单条记录判定阶段、调用与原因，输出目标可注入，凭据、用户正文与未知载荷不落盘，并发记录不交织。

阻止直接实现的是三个必须先定的选择：耗时与结果状态在现有 `Event` 中没有承载点；脱敏是安全边界（哪些键替换、未知键如何处理）；写失败/Handler 异常/`ctx` 取消下的旁路语义。三者都会长期约束后续 Agent Loop 的发出端写法，且 Design §15 禁止实现期私自扩展公共符号。

## Verified Current Behavior

- VERIFIED：`runlog/runlog.go` 仅含 `EventKind`（无常量）、`Event{Kind, Ref, Step, CallID, Summary, Data, Error}`、`Sink`、`Discard()`；`runlog/runlog_test.go` 只有 `Discard` 无副作用测试。
- VERIFIED：全仓库无 `log/slog` 引用（`grep -rn "log/slog" --include=*.go .` 空），也无任何包 import `runlog`（除 `runlog/` 自身外为空）。
- VERIFIED：`core` 已提供 `ExecutionRef{SessionID,RunID}`、`RunStatus`（1 非终态 + 4 终态）、`IsTerminal`、9 值 `ErrorKind`、`*RunError{Kind,Op,Message,CallID,Cause}`、`StatusOf`（无错误→COMPLETED、取消/超时→CANCELLED、预算→LIMIT_EXCEEDED、其余→FAILED）、`KindOf`（`Kind==""` 归一 `internal`）；`Event.Error` 即 `*core.RunError`，无需自造错误分类。
- VERIFIED：`go.mod` 为 `module cnb.cool/framework-station/mini-agent-go` / `go 1.24` / 零 `require`；本机 `go1.24.1 linux/amd64`；基线 `go build ./...`、`go vet ./...`、`go test ./...` 全绿；`git ls-files runlog/` = `runlog/runlog.go`、`runlog/runlog_test.go`。
- VERIFIED：`git status --short` 仅 `.agent/tasks/runtime-observability-v1/` 未跟踪；分支 `feat/runtime-observability-v1` = `develop` = `origin/develop` = `6902d24`。
- VERIFIED：两条并行分支均未修改 `runlog/`、未 import `runlog`、未改 `core` → 现在扩展 `Event` 的兼容成本最低，无在途发出端需同步。
- VERIFIED：Go 1.24 标准库 `log/slog` 的 `JSONHandler` 每条 `Record` 输出一行 JSON，写 `io.Writer` 前用共享 `sync.Mutex` 串行化（`$GOROOT/src/log/slog/handler.go:313`，mutex 由 `json_handler.go:39` 创建、`clone()` 共享），同一 handler 实例（含 `WithAttrs` 克隆）并发写不会交织；`Handle` 的 writer 错误原样返回。
- VERIFIED：`.agent/workflow.yaml` 的 `resources: {}`，本任务无 Shared Resource Kind 需求（无 migration、无错误码域），`resources.reservations` 保持为空。
- INFERENCE：Design §10 的「具体落盘格式…属后续任务」在本任务落实为「JSON 记录字段语义 + 脱敏边界」；文件落盘、滚动、采样、上报仍属后续任务。
- UNKNOWN：真实 Provider/工具耗时量级（毫秒精度是否足够）在本任务无运行期证据，属 Agent Loop 任务。
- UNKNOWN：`task.md` 的 `Design Impact` 说明段仍保留「Owner 需求标注 NONE、与 UPDATE 不一致」的旧记述；字段本身已是 `UPDATE`，与 Owner Q4 决定一致。该说明段由 TaskBuilder 按权限清理，不属 Analyst 可改范围（不阻塞本 Contract）。

## Recommendation

Owner 已确认下列方案（Q1 批准、Q2 修改后批准、Q3/Q4 批准），最终约束见 `Selected Design`。

关键取舍：**改 `Event` 公共形状 vs 把 `duration`/`status` 塞进 `Data` 子 schema**。改形状换来强类型、无单位歧义、Cleaner 可直接断言字段；代价是公共 API 变更一次（当前无在途发出端，成本最低）。
脱敏采用 **允许清单 + 未知键省略**（Owner 要求，取代原「未知键保留」）：安全性优先，代价是新增业务字段必须同步 Design §10 的允许清单，否则不会出现在日志里。

## Selected Design

### 1. 公共符号（`runlog/runlog.go` 追加）

```go
const (
    KindRunStarted   EventKind = "run_started"
    KindLLMRequest   EventKind = "llm_request"
    KindLLMResponse  EventKind = "llm_response"
    KindToolStarted  EventKind = "tool_started"
    KindToolFinished EventKind = "tool_finished"
    KindRunFinished  EventKind = "run_finished"
)

type Event struct {
    // 现有字段不变
    Kind    EventKind
    Ref     core.ExecutionRef
    Step    int
    CallID  string
    Summary string
    Data    json.RawMessage
    Error   *core.RunError
    // 新增
    Duration time.Duration  // 发出端测量的阶段耗时；0 = 未测量或亚毫秒
    Status   core.RunStatus // 仅终态事件（run_finished）填；空时由 Error 推导
}
```

- `Sink` 接口签名、`Discard()` 行为不变。
- 新增：`runlog/slog.go` 中 `func NewSlogSink(h slog.Handler) Sink`、`func NewJSONSink(w io.Writer) Sink`；`NewJSONSink(w)` = `NewSlogSink(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))`。无 Option 结构，分级/改写由调用方注入 `slog.Handler`。
- `nil` handler / `nil` writer：构造函数返回的 Sink 在 `Record` 时返回错误，不 panic。
- 不新增其他公共符号；脱敏常量、允许清单、上限全部为包内私有。

### 2. 记录形态

每条 `Record` = 一行 `log/slog` JSON，`msg` 固定 `run_event`，`level=INFO`，`time` 由 slog 记录（测试不断言精确时刻）。

| 字段 | 类型 | 来源 | 出现条件 |
| --- | --- | --- | --- |
| `session_id` / `run_id` | string | `Event.Ref` 原样 | 总是 |
| `event` | string | `Event.Kind` | 总是 |
| `step` | int | `Event.Step` | 总是 |
| `call_id` | string | `Event.CallID`；为空且 `Error != nil` 时回退 `Error.CallID` | 非空时 |
| `duration_ms` | int64 | `Event.Duration.Milliseconds()`（向下取整） | 总是（0 = 未测量/亚毫秒） |
| `status` | string | `Event.Status` 非空时原样；否则 `Error != nil` 时 `core.StatusOf(Error)`；否则 `RUNNING` | 总是 |
| `error_kind` | string | `core.KindOf(Event.Error)` | `Error != nil` |
| `op` | string | `Event.Error.Op` | 非空时 |
| `error_message` | string | `Event.Error.Message` 经同值级脱敏 + 256B 截断 | 非空时 |
| `summary` | string | `Event.Summary` 原样（发出端负责已脱敏，见 Open Risks） | 非空时 |
| `data` | object | 允许清单脱敏后的 `Event.Data` | `Data` 非空、可解析、≤ 64 KiB |
| `data_invalid` | `true` | `Data` 非空但非法 JSON / 含尾随字节 / 顶层非对象 | 此时不输出 `data` |
| `data_truncated` | `true` | `Data` 原始字节 > 64 KiB | 此时不输出 `data` |

不输出：`Error.Cause`、堆栈、错误对象本身（`%+v`/`%v`）、原始 `Data` 字节。

状态语义（Owner Q1 边界）：`tool_finished` / `llm_response` 成功不等于整个 Run 完成，Sink 不得为其合成终态；`status` 只能来自 `Event.Status` 或由 `Error` 推导。`run_finished + Status=COMPLETED` 是唯一「无错误且 COMPLETED」的来源。

### 3. 脱敏（允许清单，`runlog/sanitize.go`）

键名归一化：小写 → 去 `_`、`-`、`.`、空格。每个对象成员按**固定优先级**判定，命中即停：

1. **允许清单**（归一化后精确匹配）→ 输出值：`model`、`provider`、`finish_reason`、`stop_reason`、`name`、`tool`、`tool_name`、`is_error`、`ok`、`status`、`attempt`、`max_attempts`、`retry_count`、`usage`、`input_tokens`、`output_tokens`、`total_tokens`、`cached_tokens`、`compacted`、`dropped_messages`、`message_count`、`tool_call_count`、`id`、`index`、`truncated`。
2. **敏感键**（归一化后等于或包含下列片段）→ `"[REDACTED]"`，不再遍历其值：`apikey`、`secret`、`token`、`password`、`passwd`、`credential`、`privatekey`、`accesskey`；或等于 `authorization`、`auth`、`proxyauthorization`、`cookie`、`setcookie`。允许清单优先，因此 `total_tokens` / `input_tokens` 等计数不会被 `token` 片段误伤。
3. **正文键**（归一化后精确匹配）→ `"[REDACTED:len=N]"`（N = 值字节长度）：`content`、`text`、`messages`、`prompt`、`input`、`raw`、`body`。
4. **其他（未知键）** → 整键省略，不输出原始值、不输出标记。

值级规则：字符串命中 `(?i)(bearer|basic)\s+\S+` → `"[REDACTED]"`；字符串 > 256B → 截断至前 256B（对齐 UTF-8 边界）并追加 `…`。允许清单键的值为对象/数组时递归套用同一规则；数组元素：对象→成员规则，数字/布尔→保留，字符串→按正文处理输出 `"[REDACTED:len=N]"`。

上限执行（Owner Q2 第 7 项）：

- **深度**：`Data` 顶层对象为 depth 0，其成员值为 depth 1；`depth > 8` 的值整体替换为 `"[TRUNCATED]"`，不再遍历。
- **节点**：单条记录内最多访问 1024 个值，按**对象键字典序**遍历；预算耗尽后剩余值一律 `"[TRUNCATED]"`（键序固定 → 截断结果可复现）。
- **字符串**：单值 > 256B 截断并追加 `…`（见上）。
- **载荷**：`len(Event.Data) > 64 KiB` → 不解析、不输出 `data`，输出 `data_truncated=true`。
- 顶层非对象（字符串/数字/布尔/数组）按 `data_invalid=true` 处理，不输出原文。

### 4. 失败与旁路语义

- `Record` 同步执行：不启 goroutine、不做异步队列/批量/采样、不加内部超时（避免乱序与泄漏）。阻塞的 `io.Writer` 会阻塞调用方，这是显式边界。
- `Record` 完整包 `defer recover()`：handler panic 被恢复，返回 `runlog: slog handler panic recovered (<panic 类型>)`，不含载荷内容，不向调用方抛出 panic。
- writer / handler 返回的错误原样返回给调用方；`Sink` 契约规定调用方必须忽略，Run 状态、计数、消息序列不变。
- `ctx` 只透传给 handler；已取消/已超时的 `ctx` 不阻止记录、不返回 `ctx.Err()`。
- 记录过程只读入参：不修改 `Event` 任何字段、不修改 `Data` 字节、不修改 `*Event.Error`。
- `Data` 非法/超限不返回错误，只输出 `data_invalid` / `data_truncated`。
- 不做去重、不排序语义、不缓存：重复记录即重复行，顺序即调用顺序；`step`/`call_id` 的唯一性由发出端保证。
- 并发：安全性依赖 `slog.Handler` 可并发契约 + 单一 handler 实例（标准库 JSONHandler 共享 mutex 串行写字）；Sink 自身无可变状态。

### 5. Design 更新（`docs/design/runtime-architecture.md`）

- §3：`runlog` 行「不负责：落盘格式、采样、上报后端」→「不负责：文件落盘/滚动策略、采样、上报后端」；职责列补「结构化 JSON 记录（字段语义与脱敏）」。
- §10：用字段表 + 允许清单 + 上限 + 旁路/并发语义替换「具体落盘格式属后续任务」，并明确文件落盘/滚动/采样/上报仍属后续任务。
- §12：登记 `runlog/runlog.go`（更新说明）、`runlog/slog.go`、`runlog/sanitize.go`、`runlog/slog_test.go`、`runlog/sanitize_test.go`。
- §14：「runlog 旁路在真实运行时成立、事件完整性」收窄为「Sink 层旁路与记录完整性已由本任务证明；真实运行期事件完整性与发出点接线归 Agent Loop 任务」，并补一条未验证明细。

## Interfaces and Data

必须保持（不得修改）：

- `runlog.Sink` 接口 `Record(ctx context.Context, ev Event) error` 签名；`runlog.Discard()` 行为与现有测试。
- 依赖方向：`runlog` 只 import 标准库与 `<module>/core`；`go.mod` 零 `require`。
- `core` 全部类型与语义（`RunStatus`、`ErrorKind`、`RunError`、`StatusOf`、`KindOf`）。

拟新增（见 Selected Design §1）：`Event.Duration`、`Event.Status`、六个 `EventKind` 常量、`NewSlogSink`、`NewJSONSink`。文件计划：

| 路径 | 状态 | 内容 |
| --- | --- | --- |
| `runlog/runlog.go` | 修改 | 追加 `Duration`/`Status` 与六个常量，更新包注释（落盘位置/采样/上报仍属后续） |
| `runlog/slog.go` | 新增 | `NewSlogSink` / `NewJSONSink` / 记录构建 / panic 恢复与错误返回 |
| `runlog/sanitize.go` | 新增 | 允许清单、敏感/正文/未知键规则、上限执行、键序稳定遍历 |
| `runlog/slog_test.go` | 新增 | 字段与关联性、失败与超时定位、旁路与异常隔离、并发记录 |
| `runlog/sanitize_test.go` | 新增 | 脱敏表驱动：嵌套、未知键、超大输入、泄漏扫描 |
| `runlog/runlog_test.go` | 不变 | `Discard` 无副作用 |

全局资源：无（`resources: {}`，不产生 Registry 变更）。

## Business Invariants

任务级编号 `INV-LOG-*`，不占用 Design §11 的 `INV-001..013`。

- INV-LOG-001：每条记录都带发出事件自身的 `session_id`/`run_id`；工具事件与 `tool_failure` 的 `call_id` 等于发起该次调用的 `CallID`。Sink 不改写入参身份字段，也不从 `Data` 推断身份。
- INV-LOG-002：`error_kind` 等于 `core.KindOf(ev.Error)`；`Error != nil` 时 `status` 等于 `core.StatusOf(ev.Error)`；`cancelled` 与 `deadline_exceeded` 输出为两个不同取值。Sink 不合成终态：`tool_finished` / `llm_response` 成功事件输出 `RUNNING`，只有 `run_finished` 携带 `COMPLETED`。
- INV-LOG-003：输出字节中不出现凭据原始值、用户正文原文、未知字段原始值；敏感键为 `[REDACTED]`，正文键为 `[REDACTED:len=N]`，未知键不出现；允许清单内非敏感业务字段必须保留；无法解析的载荷只留标记。
- INV-LOG-004：记录不改变输入与结果：`Event`（含 `*RunError`）调用前后 `reflect.DeepEqual` 相等、`Data` 字节不变；失败写入、Handler 返回错误、Handler panic、已取消 `ctx`、异常载荷下不 panic、在测试超时内返回；同一 `core.RunResult` 在接入/不接入该 Sink 时逐字段相同。
- INV-LOG-005：并发下每条记录是完整可独立解析的单行单元（无交织、无字段丢失）。
- INV-LOG-006：零第三方依赖：`go.mod` 无 `require`，`runlog` 只 import 标准库与 `<module>/core`。
- INV-LOG-007：脱敏有界且确定：同一输入重复脱敏结果相同（键序固定）；深度 ≤ 8、节点 ≤ 1024、字符串 ≤ 256B+`…`、`Data` ≤ 64 KiB，超限以 `[TRUNCATED]` / `data_truncated` 表达，绝不输出原始字节。

## Failure and Consistency Semantics

- 成功定义：`Record` 返回 `nil` 表示记录已交给 `slog.Handler`，**不表示**已落盘、已上报或已持久化；日志不是会话事实来源（消息历史归 Session，运行结论归 `RunResult`）。
- 写入失败 / Handler 返回错误：`Record` 原样返回错误，调用方必须忽略，Run 结果不变（INV-008）。
- Handler panic：内部 `recover` 后返回错误，不传播 panic。
- `ctx` 已取消/已超时：仍记录，不返回 `ctx.Err()`。
- `Data` 非法/超限：不返回错误，`data_invalid` / `data_truncated` 标记，丢弃原始字节。
- 重复/乱序：Sink 无去重、无排序、无缓存；顺序即调用顺序；`step`/`call_id` 语义由发出端保证。
- 一致性范围：无多存储、无异步复制、无 MQ，因此不存在跨系统最终一致性问题；本任务不引入异步批量，避免「日志顺序」与「Run 进度」之间的可见性歧义。

## Allowed / Forbidden Changes

- 允许：`runlog/runlog.go` 追加 `Duration`/`Status` 与六个常量、更新包注释；新增 `runlog/slog.go`、`runlog/sanitize.go`、`runlog/slog_test.go`、`runlog/sanitize_test.go`；`docs/design/runtime-architecture.md` §3 `runlog` 行（Q3）、§10、§12、§14（Analyst 在 APPROVED 后先行落地）。
- 禁止：修改 `Sink` 接口签名与 `Discard` 行为；修改 `core`/`llm`/`tool`/`session`/`contextmgr`；新增第三方依赖或测试库；修改 `go.mod`、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）、`docs/design/agent-workflow.md`、Design 其他章节；在事件发出点接线或实现 Agent Loop；实现文件落盘/滚动/采样/上报/metrics/trace；调用真实 LLM API；`runlog` 内新增本 Contract 未列出的公共符号。

## Verification Requirements

- INV-LOG-001 → `runlog/slog_test.go`：两个 Run 的事件序列写入注入的 `bytes.Buffer`，逐行 `json.Unmarshal`，断言 `session_id`/`run_id`/`step`/`event`/`call_id` 与构造值一致；按 `run_id` 过滤只得到该 Run 的记录且顺序一致。
- INV-LOG-002 → 表驱动注入 `provider_failure`、`tool_failure`（带 `CallID`）、`cancelled`、`deadline_exceeded`、`llm_call_limit`、`tool_call_limit`：断言 `error_kind`、`op`、`call_id`、`duration_ms` 等于构造值；断言 `cancelled` 与 `deadline_exceeded` 的 `error_kind` 不同；`tool_finished` 成功事件 `status=RUNNING`；`run_finished + COMPLETED` 输出 `status=COMPLETED` 且无 `error_kind`。
- INV-LOG-003 → `runlog/sanitize_test.go`：顶层/嵌套 `api_key`、`apiKey`、`x-api-key`、`authorization: Bearer …`、`token`、`secret`、`password`、私钥片段、`content`/`text`/`messages` 正文样例；对完整输出字节做子串扫描：原始值不出现、`[REDACTED]` / `[REDACTED:len=…]` 出现、允许清单字段（如 `model`、`name`、`is_error`、`usage.input_tokens`）保留；未知键（如 `foo`、`blob`）的键名与值都不出现在输出中；非法 JSON 与 >64 KiB 载荷只出现 `data_invalid` / `data_truncated`。
- INV-LOG-004 → `runlog/slog_test.go`：失败 writer、`Handle` 返回错误的自定义 handler、panic handler、已取消 `ctx`、含非法 UTF-8/嵌套超深/空前缀的事件：断言不 panic、在测试给定超时内返回、`Event` 与 `Data` 调用前后一致（`reflect.DeepEqual`）；对同一 `core.RunResult` 做「接入 vs 不接入」逐字段比较。
- INV-LOG-005 → 并发用例（N goroutine × M 事件）逐行解析成功率 100%、字段集合齐全；`go test -race ./runlog/...` 退出码 0。
- INV-LOG-006 → `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./runlog` 只出现标准库与 `<module>/core`；`git diff go.mod` 为空。
- INV-LOG-007 → 深嵌套（>8 层）与超节点预算样例断言 `[TRUNCATED]` 且不 panic；同一输入两次脱敏输出字节相同；超长字符串长度 ≤ 256B+`…`。
- 通用：`make test`、`go vet ./...`、`go test -race ./runlog/...` 退出码 0；`GOWORK=off go -C .agent/engine test ./...` 仍通过。环境：Linux + Go 1.24.x，无外部服务，无真实 LLM 调用。
- AC-006（Design 四方核对）→ Cleaner 核对 §3/§10/§12/§14 与实现一致、§12 路径与 `git ls-files runlog/` 差集为空、§14 无双重归属。

## Open Risks

- `Event.Summary` 按 Task 既有 Assumption（发出端负责的已脱敏人读摘要）原样输出，Sink 不过滤；若发出端把正文写进 `Summary`，AC-003 的字节扫描在真实运行时可能被绕过。需要过滤属 Scope 变更，回 TaskBuilder。
- 允许清单是封闭集合：新增业务字段必须同步更新 Design §10 与本清单，否则不会出现在日志中（可观测性缺失，但不泄漏）。这是 Owner Q2 选择的已知代价。
- 结构化脱敏只能识别键位语义：`arguments`、`summary` 等自由文本字段中的正文/凭据无法识别；这是「不解码业务语义」的固有边界。
- 自定义 `slog.Handler` 时输出格式、并发安全与错误行为由该 handler 决定；本契约只对 `NewJSONSink` 路径保证「单行 JSON、可解析、不交织」。
- 阻塞型 `io.Writer` 会阻塞 `Record` 调用方（本任务刻意不引入 goroutine/超时）；若后续改为异步批量，当前同步语义（顺序 = 调用顺序）需重新确认。
- 仅验证 Sink 自身旁路；Agent Loop 是否真正隔离日志错误、是否发出完整事件，由后续 Runtime 集成任务验证，本任务不得宣称已完成。
- 并行任务若自行在 `Data` 内携带 `duration`/`status`，会与记录字段重复且可能被允许清单丢弃；Design §10 固定「耗时/状态只走 `Event.Duration`/`Event.Status`」。

## Owner Decision Record

Owner 决定 2026-10-20（T06 Contract 有条件批准），原文要点：

- **Q1 批准**：允许 `Event` 增加 `Duration time.Duration`、`Status core.RunStatus`，并定义六个 `EventKind` 常量；`Duration` 由发出端测量、统一输出毫秒；`Status` 遵守 `core.RunStatus` 语义；工具调用成功不等于整个 Run 完成，事件不适用的状态字段不得伪造终态；允许 `NewSlogSink`、`NewJSONSink`，不引入第三方依赖。
- **Q2 修改后批准**：同意防御性脱敏，但**不接受未知 `Data` 字段原样保留**；已知安全元数据走允许清单；敏感字段/凭据/Token 统一脱敏；Prompt/Message/Content/Text 等正文不记录原文，可输出长度摘要；未知字段默认不记录原始值（省略或固定标记）；`Cause`、堆栈、原始错误对象不得直接进入日志；脱敏匹配需覆盖大小写与常见命名变体；接受深度 8 / 节点 1024 / 字符串 256B / `Data` 64KiB 并需明确执行方式；必须测试嵌套、未知键、异常大输入与敏感泄漏。→ 本 Contract 采用「允许清单 + 未知键省略 + 固定键序遍历」。
- **Q3 批准**：Design §3「不负责落盘格式」调整为「不负责文件落盘/滚动策略」；本任务负责 JSON 日志格式与输出，不负责文件管理、轮转与持久化存储策略。
- **Q4 批准**：Design Impact = `UPDATE`；要求 `task.md`、Contract、Design 一致；若 `task.md` 仍有冲突标注，由 TaskBuilder 按权限修订（本 Contract 已核实 `task.md` 的 `Design Impact` 字段为 `UPDATE`，仅说明段残留旧记述，交 TaskBuilder 清理，不阻塞）。
- **运行边界**：日志记录失败不得改变 Run 最终结果；Sink 避免 panic 传播且调用方必须隔离日志错误；`Record` 不得修改传入 `Event` 或 `Data`；并发日志保持每条 JSON 记录完整；日志系统不调用真实 LLM API；本任务只验证 Sink 自身行为，Agent Loop 的真实旁路由后续 Runtime 集成任务验证，不得提前宣称完成。
- 另：不得修改 T00 之外模块或突破既有公共契约；如需突破须先报告冲突，不得自行扩大范围（本 Contract 的 `Allowed / Forbidden Changes` 已受限）。
