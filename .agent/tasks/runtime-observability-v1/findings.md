# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 | `runtime-observability-v1`（Design Impact `UPDATE`） |
| 任务基线 | `6902d24` — `chore(delivery): 记录 M1 Runtime 契约里程碑交付验收结论（runtime-core-contracts-v1）` |
| Contract 证据 | `state.contract.status=APPROVED`，`contract.target=6b68fc8`；`git diff 6b68fc8 -- .agent/tasks/runtime-observability-v1/contract.md` 为空（Contract 未漂移） |
| Design 提交 | `fdfe109`（§3/§10/§12/§14 首次落地）+ `fd068b6`（§10 补记 `call_id` 回退语义，CLEAN-003 修复） |
| **本次被审查对象 C1** | `8e4d2fd` — `test(runlog): 修复 CLEAN-001/002 输入不可变与 Run 结果隔离断言（runtime-observability-v1）` |
| 首轮 C1（已失效） | `08cd0ac` — 首轮审查对象；其 CLEAN-001 P2 导致 `CHANGES_REQUIRED` |
| C2（metadata） | `d9d7140` — 只把 `state.review.status` 置 `PENDING`、`review.target` 绑定 `8e4d2fd`，无源码改动 |
| C1 与当前工作区是否一致 | `git diff --stat 8e4d2fd HEAD -- runlog docs` 为空 → 本轮审查的代码 == C1；`git status --short` 为空 |
| C1 变更范围 | `git diff --name-status a27ce7c 8e4d2fd` = `M docs/design/runtime-architecture.md`（§10 一行）、`M runlog/slog_test.go`（+138/−26） |
| 生产代码是否变化 | `git diff --stat 08cd0ac 8e4d2fd -- runlog/{runlog,slog,sanitize,runlog_test}.go` 为空 → 生产代码与首轮 C1 完全一致，本轮改动只涉及测试与文档 |
| 分支 / 远端 | `feat/runtime-observability-v1`，审查开始时 local HEAD == `origin/feat/runtime-observability-v1` == `d9d7140` |
| 任务前已有修改 | 无（`task.md` Review Baseline 记录任务开始时 clean） |
| 全局资源 | `resources.reservations` 为空，`.agent/workflow.yaml` 为 `resources: {}`，实现无 migration / 错误码域 → 无 Shared Resource，三边一致性不适用 |

## Result

CLEAN

复审结论：CLEAN-001（P2）、CLEAN-002（P3）、CLEAN-003（P3）全部关闭，且关闭依据是 Cleaner 重放原 Mutation 后的**失败证据**，不是 Coder 声明。全部 AC 有证据支持 `PASS`；无开放 P0/P1/P2。

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | `runlog/slog_test.go:TestRecordKeepsRunCorrelation`（A/B 两 Run 交错 9 条事件，逐行解析断言 `session_id`/`run_id`/`step`/`event`/`call_id`，按 `run_id` 过滤后条数与顺序正确）；Cleaner 独立探针 `TestProbeTwoRunsIsolation` 在首轮 C1 上通过，本 C1 未改该实现与事件构建逻辑。 |
| AC-002 | PASS | `TestRecordFailureLocatesStageAndKind` 覆盖 `provider_failure`、`tool_failure`(带 `CallID`)、`cancelled`、`deadline_exceeded`、`llm_call_limit`、`tool_call_limit`，断言 `error_kind`/`op`/`call_id`/`duration_ms` 等于构造值，并显式断言 `cancelled` 与 `deadline_exceeded` 的 `error_kind` 不同；另证明 `tool_finished`/`llm_response` 成功输出 `RUNNING`（不合成终态）、`run_finished+COMPLETED` 输出 `COMPLETED` 且无 `error_kind`。Cleaner 实跑通过（本 C1 生产代码与首轮一致）。 |
| AC-003 | PASS | `TestRecordRedactsSecretsAndContent` 对完整输出字节扫描 9 个敏感/正文串并断言 `model`/`name`/`input_tokens`/占位符保留、未知键名不出现；`sanitize_test.go` 覆盖命名变体、嵌套、正文长度摘要、未知键省略、数组元素规则。Cleaner 首轮独立探针（17 个 `SECRET-*` 变体：`API_KEY`/`Api-Key`/`client_secret`/`access_token`/`SESSION-TOKEN`/`Proxy-Authorization: Basic`/`Set-Cookie`/`db_password`/`private_key`/`aws.credentials.access-key-id`/`messages[].content`/`prompt`/未知键）零泄漏且允许清单字段保留；本轮改动未触碰脱敏实现。 |
| AC-004 | PASS | 行为：`TestRecordIsSideEffectFree` 覆盖失败 writer、`Handle` 返回错误的 Handler、panic Handler、已取消 `ctx`、含非法 UTF-8/超深嵌套载荷，均不 panic 且在 2s 超时内返回。测试可信度：修复后用 `eventSnapshot`/`snapshotEvent` 对 `*core.RunError` 做**值拷贝**比较，`baseEvent`/`weirdEvent` 的 `Message` 故意为 >256B 或含 `Bearer`；`runScenario` 改为由事件序列推导 `RunResult`（`LLMCalls`/`ToolCalls`/`Messages`/`Status`/`Error`），期望值用 `copyEvents` 独立副本推导，并有前置断言证明期望值确实来自事件。Cleaner 重放 Mutation M1（原地改写 `ev.Error.Message`）→ 该测试**两个子用例 FAIL**、且 `slog_test.go:619` 的 RunResult 比较同时 FAIL（见 Verification）。 |
| AC-005 | PASS | `TestRecordConcurrentRecordsIntact`（8 goroutine × 25 事件，100% 行解析 + 字段齐全 + `call_id`/`step` 唯一性）；`go test -race -count=1 ./runlog/...` → `ok 1.086s`，exit 0。 |
| AC-006 | PASS | 四方核对：Task ↔ APPROVED Contract `Selected Design` §1–§4 ↔ Design §3/§10/§12/§14 ↔ 实现一致；CLEAN-003 修复后 §10 已记录 `call_id` 回退语义（与 Contract 字段表一致），Design 改动仍只落这四节（`git diff 6902d24 8e4d2fd -- docs/design/runtime-architecture.md` = 17+/6−，全部位于 §3/§10/§12/§14）。机械项：`git ls-files runlog/` = 6 个路径，与 §12 表格双向差集为空；`go list ./runlog` = `core` + 标准库（`bytes context encoding/json errors fmt io log/slog regexp sort strconv strings time unicode unicode/utf8`）；`git diff 6902d24 8e4d2fd -- go.mod` 为空、`go.mod` 无 `require`。§14 无双重归属。 |

## Verification

环境：Linux，`go1.24.1 linux/amd64`，无外部服务。Mutation 在**隔离的可丢弃副本**（`git archive 8e4d2fd` 解包到 `/tmp/re-cleaner-mut`，无 Git 元数据）执行，未在 Review Target working tree 做临时修改；副本已删除，无 Mutation 遗留，仓库 `git status --short` 复审期间始终为空。

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `make test` | PASS | `ok core`、`ok llm`、`ok runlog`（`0.008s`），exit 0 |
| `go test -race -count=1 ./runlog/...` | PASS | `ok ... 1.086s`，exit 0 |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | Engine 独立 Module 未受影响 |
| 依赖方向 / 零第三方依赖 | PASS | `go list ./runlog` 仅 `core` + 标准库；`go.mod` 未改、无 `require` |
| 生产代码未随测试修复变化 | PASS | `git diff --stat 08cd0ac 8e4d2fd -- runlog/runlog.go runlog/slog.go runlog/sanitize.go runlog/runlog_test.go` 为空 |
| **Mutation M1 重放（原地改写 `ev.Error.Message`）** | **被检测（CLEAN-001 关闭依据）** | FAIL `TestRecordIsSideEffectFree/写入必然失败的_writer`（`slog_test.go:595`：快照显示 `Message` 由长原文变为 `[REDACTED]`）、FAIL `TestRecordIsSideEffectFree/异常载荷与非法_UTF-8`、FAIL `slog_test.go:619`「Run 结果受日志接入影响」 |
| Mutation M2 抽查（未知键原样保留） | 被检测 | FAIL `TestSanitizeDataOmitsUnknownKeys`、`TestRecordRedactsSecretsAndContent`；恢复后 `ok` |
| Mutation M3–M5（首轮已验，生产代码未变） | 被检测 | 首轮证据仍适用：伪造 `COMPLETED` → `TestRecordFailureLocatesStageAndKind`；敏感先于允许清单 → 4 个测试；移除 `recover()` → `TestRecordIsSideEffectFree` |
| 副本恢复基线 | PASS | 恢复 `slog.go`/`sanitize.go` 后 `go test ./runlog/` → `ok 0.027s` |
| Contract 未漂移 | PASS | `git diff 6b68fc8 -- contract.md` 为空 |
| C1 == 当前实现 | PASS | `git diff --stat 8e4d2fd HEAD -- runlog docs` 为空 |
| §12 路径 ↔ `git ls-files runlog/` | PASS | 6 ↔ 6，双向差集为空 |

## Findings

### CLEAN-001：AC-004「不修改传入 `Event`（含 `*core.RunError`）」在现有测试下不可失败

- Severity：P2
- Status：**CLOSED**（复审关闭，依据 Cleaner 重放 Mutation 的失败证据）
- Location：`runlog/slog_test.go:475-507`（`eventSnapshot`/`snapshotEvent`）、`:582-598`（快照比较）；被保护生产代码 `runlog/slog.go:99-108`
- AC / Invariant：AC-004；INV-LOG-004；Contract §4「记录过程只读入参」
- 原触发条件：实现改为就地脱敏（`ev.Error.Message = sanitizeString(ev.Error.Message)`）
- 首轮 Actual：浅拷贝比较共享同一 `*core.RunError` 指针，Mutation M1 全绿通过
- 修复：`snapshotEvent` 把 `*core.RunError` 复制为独立值、`Data` 复制为独立字符串；测试数据改为 >256B 且含 `Bearer` 的错误文本，使任何就地脱敏/截断必然可见
- 复审证据：同一 Mutation M1 在新 C1 的隔离副本上重放 → `TestRecordIsSideEffectFree` 的 `写入必然失败的_writer` 与 `异常载荷与非法_UTF-8` 两个子用例 FAIL（`slog_test.go:595` 打印调用前后快照差异）；恢复代码后 `go test ./runlog/` 全绿。修复未引入新的恒真断言，也未放宽原有断言
- 结论：触发条件已不可重现，回归保护有效 → CLOSED

### CLEAN-002：AC-004「接入 / 不接入 Sink 的 `RunResult` 逐字段相同」断言恒真

- Severity：P3
- Status：**CLOSED**
- Location：`runlog/slog_test.go:600-641`（三路比较 + 前置校验）、`:643-698`（`copyEvents`、`runScenario` 由事件推导结论）
- 原触发条件：任何不 panic 的 `Record` 实现（旧 `runScenario` 返回固定字面量）
- 修复：`runScenario` 现在只依据事件序列推导 `ExecutionRef`/`LLMCalls`/`ToolCalls`/`Messages`/`Status`/`Error`，期望值由 `copyEvents` 独立副本推导；工具失败消息回填 `ev.Error.Message`，使 Sink 改写共享错误对象会传导到业务结论；并新增前置断言（`expected.LLMCalls==1`、`len(expected.Messages)==2`、`ToolResult.Content==longPlainMessage`）证明期望值不是常量
- 复审证据：Mutation M1 重放时 `slog_test.go:619`「Run 结果受日志接入影响」FAIL，说明该断言已可失败；修复后正常路径 PASS
- 结论：CLOSED

### CLEAN-003：Design §10 未记录 `call_id` 回退到 `Error.CallID` 的字段语义

- Severity：P3
- Status：**CLOSED**（`fd068b6`，Analyst 权限范围内补记）
- 复审证据：`docs/design/runtime-architecture.md:123` 现写明「`call_id` 取事件自带的 `Event.CallID`，为空且 `Error != nil` 时回退 `Error.CallID`，Sink 不从 `Data` 或名称推断调用身份」，与 `contract.md` 字段表 `call_id` 行、`runlog/slog.go:130-138` 实现三者一致；改动只落在 §10 一行，未外溢其他章节
- 结论：CLOSED

首轮 Mutation 结论仍然成立：脱敏（允许清单优先级、未知键省略、正文键）、状态语义（不合成终态）、panic 恢复均受测试保护；本轮在此基础上补齐了唯一的空洞（输入不可变 / Run 结果隔离）。
