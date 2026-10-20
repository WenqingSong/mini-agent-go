# Cleaner Findings

## Review Target

- 任务基线（Base commit）：`6902d2445665ec356a2db22b33b7749d39d8018e`。
- Immutable implementation Evidence Commit **C1 = `92068976d73d29384c9227fc90d648fb3ddb3261`**（`feat(builtin): 交付五个内置工具与 Todo 写入端口`）。
- Coder metadata commit **C2 = `7ba3dbb`**（只改 `state.yaml` 写 `review.status=PENDING` / `review.target=C1`），不作为实现审查对象。
- 审查对象 = **C1**，非 C2、非当前 working tree。审查时分支 `feat/builtin-agent-tools-v1`，HEAD = `7ba3dbb`，working tree clean。
- C1 新增 10 个受跟踪文件，全部位于 `builtin/`：实现 `builtin.go` / `calculator.go` / `search.go` / `search_data.go` / `todo.go`；测试 `spec_test.go` / `calculator_test.go` / `search_test.go` / `todo_test.go` / `todo_concurrency_test.go`。
- 冻结文件 `core/**`、`tool/tool.go`、`session/session.go`、`llm/**`、`contextmgr/**`、`runlog/**` 自基线至 C1 **逐字节未变**（`git diff --stat 6902d24..9206897 -- core tool session llm contextmgr runlog` 为空）。
- Design 更新在 `08b2cb3`（Analyst，`docs/design/runtime-architecture.md`）；Contract A1=`786d6fd` / A2=`44b01ef`（`state.contract.status=APPROVED`）。
- 任务前已有修改区分：TaskBuilder 记录任务开始时工作区 clean；C1 是唯一实现提交，未与既有修改混淆。
- 全局资源：无。`state.yaml.resources.reservations={}`、`.agent/workflow.yaml` 的 `resources: {}`；无 migration、无错误码域、无 Registry 文件。领域错误码（`invalid_arguments` 等）是任务局部 Content 协议，非注册的 `error_code_domain`，故 Registry↔Contract↔实现三边一致性为 N/A（无漂移面）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestCalculatorSuccess`（9 例：整数/负数/精确小数/除不尽相对误差 ≤1e-12/`0.1+0.2`/大数乘法/下溢为 0），断言 `IsError=false`、`result` 有限、相对误差容差比对。`go test -race ./builtin` ok。 |
| AC-002 | PASS | `TestCalculatorFailure`（20 例：除 0/-0、未知/空 op、缺 op、op null/非串、缺 a/b、a 串/null/对象、`1e309` 越界、`NaN`/`Infinity` 字面量、乘/除溢出、空/非 JSON/数组/null），每条断言 `IsError=true`、`err==nil`、精确错误码、无 `result`/`operation` 字段、`recover` 防 panic。 |
| AC-003 | PASS | `TestMockSearchDeterministic`（逐字节一致 + `mock:true`）、`CaseInsensitive`（`Go`/`go`/首尾空白一致且回显归一化 query）、`OrderFollowsDataset`（`doc-5,doc-6` 数据集序）、`NoMatch`（空数组 + `mock:true`）、`QueryValidation`（空/纯空白→`empty_query`，其余→`invalid_arguments`）。 |
| AC-004 | PASS | `TestTodoLifecycle`（建→列→完→列，端口每次收到 `sessionID==req.SessionID`，状态 pending→completed）、`CompleteIsIdempotent`、`CompleteUnknownIDHasNoSideEffect`（`todo_not_found` 且无副作用）、`EmptySessionIDRejected`（`missing_session_id` 且端口 0 调用）。 |
| AC-005 | PASS | `TestTodoCrossSessionScope`（B 列表空；B 用 A 的 ID→`todo_not_found` 且 A 数据逐字段不变）、`SessionFieldsRejected`（`session_id`/`sessionId`/`session`→`invalid_arguments` 且端口 0 调用）。测试注释明确 Fake 分桶不构成真实 Session 隔离证据。 |
| AC-006 | PASS | `TestTodoConcurrentCreate`（N=50 唯一）、`ConcurrentCompleteSameID`（终态唯一 completed）、`ConcurrentTwoSessions`（两会话互不含对方标识）。`go test -race -count=1 ./...` 全模块干净。工具层证据，未声称真实隔离/持久化并发。 |
| AC-007 | PASS | Cleaner 实跑：`go version`=go1.24.1、`go build ./...`、`go vet ./...`、`make test`、`go test -race -count=1 ./...` 均 exit 0；`go list` 显示 `builtin` 仅 import 标准库 + `core`/`tool`/`session`；冻结目录 diff 为空。测试源无 `net`/`os`/外网主机名/环境变量。 |
| AC-008 | PASS | §12 所有权表 10 个 `builtin/` 文件与 `git ls-files builtin/` **双向差集为空**；Task↔APPROVED Contract↔Design↔实现四方一致（§2 依赖方向、§3 模块职责、§8 Todo 写入路径与 INV-005、§11 INV-B01—B09、§14 证据归属边界逐项相符）。 |
| AC-009 | PASS | `TestTodoPortInfrastructureFailure`（普通 error→`KindInternal`、`context.Canceled`→`KindCancelled`、`DeadlineExceeded`→`KindDeadlineExceeded`；错误链保留；不降级 `IsError`；Content 不含 `password`/`/var/lib`）、`TestTodoCanceledContext`（预取消→`KindCancelled` 且端口 0 调用）、`TestTodoWithoutOperations`（`All(nil)`→`KindInternal`、不 panic、不伪装领域失败）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| Go 版本 | PASS | go1.24.1 linux/amd64，符合任务环境要求 |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `make test`（=`go test ./...`） | PASS | exit 0，builtin ok |
| `go test -race -count=1 ./...` | PASS | 全模块 ok，无 data race |
| 依赖方向 INV-B08 | PASS | `go list` builtin imports = stdlib + core/tool/session；无 llm/contextmgr/runlog/L3；无反向依赖 |
| 冻结文件 INV-B08 | PASS | `git diff --stat 6902d24..9206897 -- core tool session llm contextmgr runlog` 为空 |
| 无生产存储/ID 生成 INV-B07 | PASS | `git grep -E "type .*Store struct|uuid|rand\.|nanoid"` 生产代码无命中；Fake 仅在 `_test.go` |
| Schema 子集 INV-B09 | PASS | `TestToolSpecsUseAllowedSchemaSubset` 递归遍历键集合 ⊆ {type,properties,description,enum,required} |
| §12 所有权 ↔ git ls-files | PASS | 10 文件双向差集为空 |
| Design 四方一致 AC-008 | PASS | §2/§3/§8/§11/§14 与 Contract、实现逐项相符 |
| Scope 边界 | PASS | C1 仅新增 10 个 `builtin/` 文件；无越界改动、无调试残留 |
| Mutation：会话作用域（INV-B05） | 测试有效 | 隔离 worktree neutralize 空 SessionID 拒绝 → `TestTodoEmptySessionIDRejected` 三工具全部 FAIL |
| Mutation：失败分级/防泄漏（INV-B02） | 测试有效 | 隔离 worktree 将端口错误原文写入 Content → `TestTodoPortInfrastructureFailure` FAIL |

Mutation 均在 `git worktree add --detach` 临时隔离副本执行，未触碰 review-target 工作区、未 commit、已 `worktree remove --force` + `prune` 清理；主工作区全程 clean。

## Findings

No actionable findings.

说明（非 Finding）：证据边界纪律良好——`todo_test.go` / `todo_concurrency_test.go` / `fakeTodoPort` 注释与 Design §14 均明确「测试专用 Fake 不证明真实 Session 隔离 / 跨会话不可写 / 持久化并发」，运行期证据归属 Session Manager（T04）与装配层（T07）。`builtin.TodoOperations` 与 `session.TodoOperations` 为两套端口（Owner D7 方案②），适配与 Todo ID 生成/状态迁移由 T07 显式承担，已在 Contract C4 与 Design §14 记录，不属本任务缺陷。
