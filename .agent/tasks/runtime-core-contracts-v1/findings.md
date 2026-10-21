# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 | `runtime-core-contracts-v1`（Milestone `M1-runtime-core-contracts`） |
| 被审查对象 C1 | `b4b95733ff413a1f21148e763eb9062b824f78be` — `feat(runtime): 落地 Runtime 核心契约与工程入口（runtime-core-contracts-v1）` |
| C1 父提交（基线） | `231e22f` — `docs(design): 新增 Runtime 核心架构长期设计`（再往前 `367c655` = 任务基线） |
| C2（metadata） | `180960a` — 只改 `state.yaml` 的 `review.status=PENDING` / `review.target=C1` |
| Contract 证据 | `state.contract.status=APPROVED`，`contract.target=6d7aebe`；`git diff 6d7aebe -- .agent/tasks/runtime-core-contracts-v1/contract.md` 为空（Contract 未漂移） |
| 分支 / 远端 | `feature/runtime-core-contracts-v1`，审查开始时工作区 clean，local HEAD == `origin/feature/runtime-core-contracts-v1` |
| 变更范围界定 | `git diff --name-status 231e22f b4b9573` = 17 个新增文件（`go.mod`、`Makefile`、`core/*`、`llm/*`、`tool/*`、`session/*`、`contextmgr/*`、`runlog/*`），无删除、无修改既有文件 |
| C1 之后源码是否变化 | `git diff --stat b4b9573 HEAD -- ':!.agent/tasks'` 为空 → 源码与 C1 完全一致，CLEAN 对 C1 有效 |

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | `go build ./...`、`go vet ./...`、`go test ./...` 全部 exit 0；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 显示 `core` 仅 import 标准库（`encoding/json errors fmt strings`），`llm/tool/session/contextmgr/runlog` 只 import `<module>/core` + 标准库，无 L1↔L1、无反向依赖、无 import cycle。`go.mod` 零 `require`，无 `go.work`/`replace`。 |
| AC-002 | PASS | `core/message_test.go:TestMessageRoundTripToolCalls` 覆盖 0/1/3 个 ToolCall 的 JSON 往返（顺序、ID、Arguments 逐项断言 + `ValidateMessages` 通过）；`core/pairing_test.go:TestValidateToolPairingInvalid` 覆盖缺失 Result、重复 Result 标识、孤儿 Result、Result 先于 Call、跨消息 ID 复用、空 ID、tool 消息缺结果 7 类，均断言 `KindOf(err)==invalid_pairing` 且 `CallID` 精确定位。Mutation 证明可被识别（见 Verification M1）。 |
| AC-003 | PASS | 六项语义均有可执行定义：Session/Todo 归属 → `session.Session{Messages,Todos}` + `session.Store`；工具身份传递 → `tool.Request` 内嵌 `core.ExecutionRef`（`go doc ./tool Request` 只有 `ExecutionRef` + `Call`，不含 `session.*`）；双预算 → `core.RunPolicy.Validate`；取消/超时 → `KindCancelled`/`KindDeadlineExceeded` + `core.StatusOf`；工具失败 → `ToolResult.IsError` + `KindToolFailure` + `RunResult.Validate` 的 CallID 强制；配对 → `ValidateToolPairing`。四结果映射由 `TestStatusCodeMapping` 与 `TestRunResultStatusKindMatrix`（5 状态 × 10 种类全组合）表驱动固化。Mutation 证明超时映射受测试保护（见 Verification M2）。 |
| AC-004 | PASS | 四方核对：Task AC/Scope ↔ APPROVED Contract（`Selected Design` §1–§9 与 `Interfaces and Data` 逐符号比对，无未记录偏差）↔ `docs/design/runtime-architecture.md`（§2 依赖方向、§3 模块职责、§4 数据模型、§5 状态机、§6 预算、§7 失败语义、§8 会话归属、§11 不变量、§12 文件所有权表均在）↔ 实现。Contract 列出的公共符号在实现中逐一存在，且实现未引入 Contract 未列出的公共符号（Role/ContentType/FinishReason/TodoStatus/EventKind 的枚举字面量按 Contract 只给类型，实现用未导出 const）。 |
| AC-005 | PASS | `make -n test` 输出 `go test ./...`（非空）；在**干净检出**（`git worktree add --detach /tmp/cleaner-mut-b4b9573 b4b9573`）执行 `make test` exit 0，输出真实测试结果（`ok .../core`、`ok .../llm`、`ok .../runlog`）；`.cnb.yml:31` 的 `pr-check` 为 `script: make test`，入口一致；`make test-engine` 与 `GOWORK=off go -C .agent/engine test ./...` 均通过，Engine 未被根模块吞掉。 |
| AC-006 | PASS | 机械比对 `docs/design/runtime-architecture.md` §12 表格路径（17 条）与 `git ls-files` 的实现文件集合（17 个）：两边差集均为空，每个源文件仅归属一个模块，`go.mod` / `Makefile` 标注为工程公共文件。 |

Global Resource 三边一致性：本任务 `resources.reservations` 为空、`.agent/workflow.yaml` 为 `resources: {}`、实现中 `ErrorKind` 是代码内枚举而非 Registry 资源 → 无 Registry 漂移，三边一致。

## Verification

环境：Linux，`go1.24.1 linux/amd64`，`GNU Make`，无外部服务（无 DB / Redis / MQ）。命令均在仓库根或标注的隔离 worktree 执行。

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test -count=1 ./...` | PASS | `core 0.004s`、`llm 0.002s`、`runlog 0.002s` 全 ok；`tool`/`session`/`contextmgr` 为纯接口声明，no test files |
| `go test -count=1 -race ./...` | PASS | 全 ok（无 goroutine/共享状态，Race 无发现） |
| `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` | PASS | 分层符合 INV-001 |
| `make test`（主工作区） | PASS | exit 0，真实测试输出 |
| `make -n test` | PASS | 展开为 `go test ./...`，非空目标 |
| `make test`（干净检出 worktree @ C1） | PASS | exit 0，证明不依赖本地未跟踪文件 |
| `make test-engine` / `GOWORK=off go -C .agent/engine test ./...` | PASS | `workflow-v2-engine/cmd/workflow-check`、`internal/workflow` 均 ok |
| `git diff 6d7aebe -- contract.md` | PASS | 空 → Contract 未在 A1 之后被改写（与 Gate `contractValid` 一致） |
| `go doc ./tool Request` | PASS | 字段仅 `core.ExecutionRef` + `core.ToolCall`，无 `session.*`，INV-005 签名核对通过 |
| 所有权表 ↔ `git ls-files` | PASS | 17 ↔ 17，双向差集为空 |
| `grep -c require go.mod` | PASS | 0 → 零第三方依赖 |
| M1 Mutation（隔离 worktree）：删除 `ValidateToolPairing` 末尾缺失 Result 扫描 | PASS（测试能识别） | `TestValidateToolPairingInvalid/缺失_Result` 与 `TestValidateMessagesAddsIndexAndKeepsKind` 双双 FAIL：「期望错误种类 invalid_pairing，实际没有返回错误」 |
| M2 Mutation（隔离 worktree）：`core/error.go` 的 `StatusOf` 去掉 `KindDeadlineExceeded` | PASS（测试能识别） | 3 个测试 FAIL：`TestRunResultValidate/CANCELLED_+_deadline_exceeded`、`TestRunResultStatusKindMatrix/FAILED/deadline_exceeded`、`TestStatusCodeMapping/整体超时` |
| M3 Mutation（隔离 worktree）：删除 `RunResult.Validate` 对 `tool_failure` 必须带 `CallID` 的约束 | PASS（测试能识别） | `TestRunResultValidate/FAILED_+_tool_failure_缺_CallID` FAIL |
| Mutation 环境清理 | PASS | 三次 Mutation 后均 `git checkout -- .`，最终 `git worktree remove`，主工作区 `git status --short` 为空，无 Mutation 遗留 |

审查观察（不构成 Finding）：M1 变体的另一形式——删除 `roleTool` 分支中的「重复结果」显式检查——测试仍全绿，因为后续 `pending` 检查会把同一输入判为「引用了不存在的工具调用」。该不变量仍被拒绝，属多余的防御层，不影响正确性，也不建议为此改动测试。

## Findings

No actionable findings（P0 / P1 / P2 = 0）。

### CLEAN-001：枚举字面量未导出，跨模块消费者只能写 `core.Role("assistant")` 这类字符串转换

- Severity：P3
- Status：OPEN（不阻塞 CLEAN；**不是本任务的 Coder 修复项**，需走 Contract Revision）
- Location：`core/message.go:15-22`（`Role` / `ContentType`）、`core/run.go:17-28` 与 `core/error.go:15-34`（这两处已导出）、`llm/provider.go:19-26`（`FinishReason`）、`session/session.go:14`（`TodoStatus`）、`runlog/runlog.go:16`（`EventKind`）
- AC / Invariant：AC-004（实现服从 Contract）；Contract `Allowed / Forbidden Changes`「禁止新增本 Contract 未列出的公共符号」
- Trigger：T01—T06 的 Provider / Tool / Loop 实现需要构造 `Role`、`ContentType`、`FinishReason`、`TodoStatus`、`EventKind` 取值时
- Actual：只存在未导出常量，消费者必须写 `core.Role("assistant")`、`core.ContentType("text")`、`llm.FinishReason("stop")`，拼写错误不会在编译期暴露，只能由运行期 `Validate` 兜住
- Expected：Contract Revision 补一组导出常量后，可编译期防错
- Impact：后续并行任务的可维护性与错误暴露时机；不影响当前契约语义正确性
- Evidence：`llm/provider_test.go:15` 已出现 `core.ContentType("text")` 的写法；`go doc` 未列出任何 `Role*` 导出常量
- Required Fix Boundary：无（本任务内不得新增公共符号）；由 Owner 决定是否在下一轮 Contract Revision 处理
- Owner：可决定忽略

### CLEAN-002：同一缺陷经不同入口返回不同 `ErrorKind`（tool 消息缺少 `ToolResult`）

- Severity：P3
- Status：OPEN（不阻塞 CLEAN；不要求本任务修复）
- Location：`core/message.go:84-86`（`Message.Validate` → `invalid_argument`）与 `core/pairing.go:51-53`（`ValidateToolPairing` → `invalid_pairing`）
- AC / Invariant：INV-002 把「配对不完整」统一归为 `invalid_pairing`；Contract `Failure and Consistency Semantics` 对消息序列非法同时接受 `invalid_pairing` / `invalid_argument`
- Trigger：构造 `Message{Role: RoleTool}` 后分别调用 `ValidateMessages` 与 `ValidateToolPairing`
- Actual：`ValidateMessages` 先走 `Message.Validate`，返回 `invalid_argument`；直接调用 `ValidateToolPairing` 返回 `invalid_pairing`
- Expected：同一缺陷在两个入口给出同一 `ErrorKind`，便于消费者按 Kind 分支
- Impact：只影响错误分类的精确性；两种 Kind 都经 `StatusOf` 映射为 `FAILED`，终态与 Run 行为无差异，测试已分别固化两种行为
- Evidence：`core/pairing_test.go:190` 断言 `invalid_argument`，`core/pairing_test.go:137-143` 断言 `invalid_pairing`，两处都由实现行为支撑
- Required Fix Boundary：无强制修复；若统一，需同时确认「角色组合错误」与「配对身份错误」的边界不被混淆，且不得只改测试
- Owner：可决定忽略
