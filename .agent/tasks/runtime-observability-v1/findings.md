# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 | `runtime-observability-v1`（Design Impact `UPDATE`） |
| 任务基线 | `6902d24` — `chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）` |
| Contract 证据 | `state.contract.status=APPROVED`，`contract.target=6b68fc8`；`git diff 6b68fc8 -- .agent/tasks/runtime-observability-v1/contract.md` 为空（Contract 未漂移） |
| Design 提交 A2 | `fdfe109` — `docs(design): 按 T06 APPROVED Contract 沉淀运行日志字段语义、脱敏边界与所有权` |
| 被审查对象 C1 | `08cd0ac` — `feat(runlog): 交付结构化运行日志与防御性脱敏（runtime-observability-v1）`（5 个文件：`runlog/runlog.go` 改，`runlog/slog.go`、`runlog/sanitize.go`、`runlog/slog_test.go`、`runlog/sanitize_test.go` 新增） |
| C2（metadata） | `b4e7a15` — 只把 `state.review.status` 置 `PENDING`、`review.target` 绑定 C1，无源码改动 |
| C1 与当前工作区是否一致 | `git diff --stat 08cd0ac HEAD -- runlog docs` 为空 → 本次审查的代码 == C1；`git status --short` 为空 |
| 分支 / 远端 | `feat/runtime-observability-v1`，审查开始时 local HEAD == `origin/feat/runtime-observability-v1` == `b4e7a15` |
| 变更范围界定 | `git diff --name-status 6902d24 HEAD`：`docs/design/runtime-architecture.md`(M)、`runlog/runlog.go`(M)、`runlog/{slog,sanitize,slog_test,sanitize_test}.go`(A)，其余为 `.agent/tasks/runtime-observability-v1/` 任务元数据 |
| 任务前已有修改 | 无（`task.md` Review Baseline 记录任务开始时 clean） |
| 全局资源 | `resources.reservations` 为空，`.agent/workflow.yaml` 为 `resources: {}`，实现无 migration / 错误码域 → 无 Shared Resource，三边一致性不适用 |

## Result

CHANGES_REQUIRED

行为实现与 APPROVED Contract 一致（Cleaner 独立探针复验通过），但 AC-004 中「记录不修改传入 `Event`」这一契约不变量在现有测试下**不可失败**：Cleaner 的 Mutation 证明一个真实的违规实现可以全绿通过（CLEAN-001，P2）。其余开放项为 P3。

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | `runlog/slog_test.go:TestRecordKeepsRunCorrelation`（A/B 两个 Run 交错写入 9 条事件，逐行解析断言 `session_id`/`run_id`/`step`/`event`/`call_id`，按 `run_id` 过滤后条数与顺序正确）；Cleaner 独立探针 `TestProbeTwoRunsIsolation`（`/tmp/cleaner-probe`，6 行全解析、`run_id` 与 `session_id`/`event`/`call_id` 一一对应）通过。 |
| AC-002 | PASS | `TestRecordFailureLocatesStageAndKind` 覆盖 `provider_failure`、`tool_failure`(带 `CallID`)、`cancelled`、`deadline_exceeded`、`llm_call_limit`、`tool_call_limit`，断言 `error_kind`/`op`/`call_id`/`duration_ms` 等于构造值；末尾显式断言 `cancelled` 与 `deadline_exceeded` 的 `error_kind` 不同；另有两例证明 `tool_finished`/`llm_response` 成功事件输出 `RUNNING`（不合成终态）、`run_finished+COMPLETED` 输出 `COMPLETED` 且无 `error_kind`。Cleaner 实跑通过。 |
| AC-003 | PASS | `runlog/slog_test.go:TestRecordRedactsSecretsAndContent` 对完整输出字节扫描 9 个敏感/正文串，并断言 `model`/`name`/`input_tokens`/占位符保留、未知键名不出现；`sanitize_test.go` 覆盖 `api_key`/`apiKey`/`X-Api-Key`/`Authorization: Bearer`/`token`/`secret`/`password`/`passwd`/私钥/`access_key`/`cookie`/嵌套容器内凭据/正文键长度摘要/未知键省略/数组元素规则。Cleaner 独立探针 `TestProbeSecretVariants` 用**另一套**变体（`API_KEY`、`Api-Key`、`client_secret`、`access_token`、`SESSION-TOKEN`、`Proxy-Authorization: Basic`、`Set-Cookie`、`db_password`、`private_key`、`aws.credentials.access-key-id`、`messages[].content`、`prompt`、未知键 `unknown`/`freeform`）实跑：17 个 `SECRET-*` 全部未出现在输出字节中，`model`/`usage.input_tokens`/`total_tokens` 保留，`data` 为 JSON 对象。 |
| AC-004 | PASS（行为），测试保护不足见 CLEAN-001 | 行为侧：`TestRecordIsSideEffectFree` 覆盖失败 writer、`Handle` 返回错误的 Handler、panic Handler、已取消 `ctx`、含非法 UTF-8 与超深嵌套的载荷，均不 panic 且在 2s 超时内返回；Cleaner 独立探针 `TestProbeErrorPointerNotMutated`（`json.Marshal` 快照比较 `*RunError`）与 `TestProbeDataBytesNotMutated`（独立副本比较 `Data` 字节）在 C1 上通过 → 当前实现确实未改写入参。测试保护侧：见 CLEAN-001，一个原地改写 `Error.Message` 的实现可通过全部现有测试。 |
| AC-005 | PASS | `TestRecordConcurrentRecordsIntact`（8 goroutine × 25 事件，100% 行解析 + 字段齐全 + `call_id`/`step` 唯一性）；Cleaner 实跑 `go test -race -count=1 ./runlog/...` → `ok ... 1.066s`，exit 0，无 race 告警。 |
| AC-006 | PASS（含 CLEAN-003 的 P3 文档缺口） | 四方核对：Task AC/Scope ↔ APPROVED Contract `Selected Design` §1–§4 与实现逐符号一致（`Event.Duration`/`Event.Status`、六个 `EventKind` 常量、`NewSlogSink`/`NewJSONSink`，无 Contract 未列出的公共符号；`Sink` 签名与 `Discard()` 未变）。Design：§3 `runlog` 行（Q3 允许）、§10 字段表 + 允许清单 + 上限 + 旁路/并发、§12 登记 6 个路径、§14 归属收窄，改动只落这四节（`git diff 6902d24 HEAD -- docs/design/runtime-architecture.md` 仅 5 个 hunk，均位于 §3/§10/§12/§14）。机械核对：`git ls-files runlog/` = 6 个路径，与 §12 表格**差集为空**；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./runlog` = `core` + 标准库（`bytes context encoding/json errors fmt io log/slog regexp sort strconv strings time unicode unicode/utf8`）；`git diff go.mod` 为空、`grep -c require go.mod` = 0。§14 已把 Sink 层旁路/记录完整性从「Agent Loop 任务」移出，无双重归属。 |

## Verification

环境：Linux，`go1.24.1 linux/amd64`，无外部服务；Mutation 与探针均在**隔离的可丢弃副本**（`git archive` 解包到 `/tmp/cleaner-probe`、`/tmp/cleaner-mut`，无 Git 元数据）执行，未在 Review Target working tree 做任何临时修改，仓库 `git status --short` 审查期间始终为空。

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `make test` | PASS | `go test ./...`：`ok core`、`ok llm`、`ok runlog`，exit 0 |
| `go test -race -count=1 ./runlog/...` | PASS | `ok cnb.cool/framework-station/mini-agent-go/runlog 1.066s`，exit 0 |
| `go test -count=1 -v ./runlog/...` | PASS | 16 个顶层测试全部 PASS（含全部表驱动子用例） |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | `ok workflow-v2-engine/...`（Engine 独立 Module 未受影响） |
| 依赖方向 / 零第三方依赖 | PASS | `go list ./runlog` 仅 `core` + 标准库；`git diff go.mod` 空；`go.mod` 无 `require` |
| §12 路径 ↔ `git ls-files runlog/` | PASS | 6 ↔ 6，双向差集为空 |
| Contract 未漂移 | PASS | `git diff 6b68fc8 -- .agent/tasks/runtime-observability-v1/contract.md` 为空 |
| C1 == 当前实现 | PASS | `git diff --stat 08cd0ac HEAD -- runlog docs` 为空 |
| Cleaner 独立探针（AC-001/003/004 复核） | PASS | `/tmp/cleaner-probe`：`TestProbeSecretVariants`、`TestProbeErrorPointerNotMutated`、`TestProbeDataBytesNotMutated`、`TestProbeTwoRunsIsolation` 全 PASS |
| Mutation M1：`slog.go` 原地改写 `ev.Error.Message` | **未被检测** | `/tmp/cleaner-mut`：`go test -count=1 ./runlog/` → `ok`，exit 0（全部现有测试通过）→ CLEAN-001 |
| Mutation M2：`sanitize.go` 未知键原样保留 | 被检测 | FAIL `TestSanitizeDataOmitsUnknownKeys`、`TestRecordRedactsSecretsAndContent` |
| Mutation M3：无错误事件伪造 `COMPLETED` | 被检测 | FAIL `TestRecordFailureLocatesStageAndKind` |
| Mutation M4：敏感判定先于允许清单 | 被检测 | FAIL 4 个测试（`KeepsAllowedFields`/`RedactsSensitiveKeys`/`ArrayElementRules`/`RecordRedactsSecretsAndContent`） |
| Mutation M5：移除 `recover()` | 被检测 | FAIL `TestRecordIsSideEffectFree`（panic 子用例） |
| 探针对 M1 的检测能力 | PASS | 同一 M1 副本中加入深拷贝探针 → FAIL `TestProbeErrorPointerNotMutated`（`Message` 被截断为 256B+`…`），证明修复方向可行 |

## Findings

### CLEAN-001：AC-004「不修改传入 `Event`（含 `*core.RunError`）」在现有测试下不可失败

- Severity：P2
- Status：OPEN
- Location：`runlog/slog_test.go:544-561`（`TestRecordIsSideEffectFree` 的 `before := tt.event` 与 `reflect.DeepEqual`）、`runlog/slog.go:99-108`（被保护的生产代码）
- AC / Invariant：AC-004；INV-LOG-004（「`Event`（含 `*RunError`）调用前后 `reflect.DeepEqual` 相等」）；Contract §4「记录过程只读入参：不修改 `Event` 任何字段、不修改 `Data` 字节、不修改 `*Event.Error`」
- Trigger：实现改为就地脱敏（例如 `ev.Error.Message = sanitizeString(ev.Error.Message)` 后再写入 `error_message`）——一种常见的省一次分配写法
- Actual：`before := tt.event` 是浅拷贝，与 `tt.event` 共享同一 `*core.RunError` 指针，`reflect.DeepEqual(before, tt.event)` 比较的是两个指向同一对象的指针，因此 `*RunError` 内部任何字段被改写都不会失败。Mutation M1 实证：把 `runlog/slog.go` 改成原地改写 `ev.Error.Message` 后，`go test ./runlog/` 仍 **exit 0 全绿**
- Expected：破坏「不修改 `*Event.Error`」的实现必须让至少一个测试失败
- Impact：AC-004 声称由测试证明的输入不可变性，实际只证明了「`Event` 结构体字段与 `Data` 字节未被改写」；错误对象内容被改写不会被发现。调用方若复用同一 `*RunError` 构造 `RunResult` 或二次记录，会把被截断/被替换的错误文本当成业务事实（违反「日志只是观察者」），且没有任何回归保护
- Evidence：Mutation M1（`/tmp/cleaner-mut`，隔离副本）结果见 Verification 表；Cleaner 深拷贝探针在同一 M1 副本上 FAIL `TestProbeErrorPointerNotMutated`，在 C1 原始副本上 PASS → 说明这是纯测试缺陷，当前实现正确
- Required Fix Boundary：AC-004 的「输入不可变」断言必须能区分修复前后——对 `*RunError`（及 `Event` 内任何指针/引用字段）用值快照或深拷贝比较（例如先 `json.Marshal` 快照、或复制一份 `core.RunError` 值再比较），不得通过放宽断言或跳过测试制造通过。生产代码无需改动；不得引入第三方断言库（INV-LOG-006）

### CLEAN-002：AC-004「接入 / 不接入 Sink 的 `RunResult` 逐字段相同」断言恒真

- Severity：P3
- Status：OPEN
- Location：`runlog/slog_test.go:564-586`（`runScenario` 返回固定字面量 `core.RunResult`，`withFailing` / `withoutSink` 比较）
- AC / Invariant：AC-004；Contract §4「`Sink` 契约规定调用方必须忽略，Run 状态、计数、消息序列不变」
- Trigger：任何不 panic 的 `Record` 实现
- Actual：`runScenario` 的结果与传入的 `Sink`、`events` 无关，两条路径返回同一字面量，`reflect.DeepEqual` 必然相等；该断言实际只能发现 `Record` panic
- Expected：结果应由同一份输入在「接入 / 不接入」两条路径下真实推导（状态、`LLMCalls`/`ToolCalls` 计数、消息序列来自事件序列），从而能发现日志接入改变了业务结果
- Impact：任务 Goal 的核心承诺（「日志只是观察者，不改变任何业务执行结果」）缺少有效测试保护；`Sink` 通过值传递接收 `Event`，结构上风险较低，故列为 P3 而非阻塞项
- Evidence：`runlog/slog_test.go:574-586` 代码本身；Mutation M1 副本中该断言同样通过（与 Sink 行为无关）
- Required Fix Boundary：要么让 `runScenario` 从事件序列推导计数字段，要么删除会误导维护者的恒真断言并保留真正有效的隔离断言；不得只改注释

### CLEAN-003：Design §10 未记录 `call_id` 回退到 `Error.CallID` 的字段语义

- Severity：P3
- Status：OPEN
- Location：`docs/design/runtime-architecture.md:123`（§10 记录形态段）对比 `runlog/slog.go:91-93,130-138` 与 `contract.md` 字段表 `call_id` 行
- AC / Invariant：AC-006（Task ↔ Contract ↔ Design ↔ 实现一致无未记录偏差）
- Trigger：工具失败事件只带 `Error.CallID`、`Event.CallID` 为空
- Actual：实现与 APPROVED Contract 都有「`call_id` 为空且 `Error != nil` 时回退 `Error.CallID`」，Design §10 只写「字段固定为 … `call_id` …（不适用时省略）」，未记录该回退来源
- Expected：Design §10 的字段语义与 Contract 表一致（补一句回退规则）
- Impact：文档级缺口；行为与 Contract 一致，不改变任何运行时结果，但会让后续维护者误判 `call_id` 的唯一来源
- Evidence：Contract `Selected Design` §2 表格 `call_id` 行；`runlog/slog.go:130-138`；Design §10 文本
- Required Fix Boundary：由 Analyst 在 Design §10 补记该回退语义（Design 属 Analyst 权限，不在 Coder 的 Allowed Changes 内）；若 Owner 认为可延后，可作为后续文档任务

Mutation 复验结论：脱敏（允许清单优先级、未知键省略、正文键）、状态语义（不合成终态）、panic 恢复三条核心不变量均受测试保护（M2–M5 全部被检测）；仅输入不可变性（M1）例外，已建 CLEAN-001。
