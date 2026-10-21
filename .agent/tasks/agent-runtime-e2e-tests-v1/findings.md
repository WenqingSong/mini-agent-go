# Cleaner Findings

## Review Target

- 任务基线（TaskBuilder 记录）：`d54c912540c090fa7c80961cb6a7cbee8e6ec9a0`；恢复基线：`f4414138e31250c8d1b49bae5dd01b91f048c155`（`merge: 合并 origin/develop 到 feat/agent-runtime-e2e-tests-v1`）。
- Implementation Evidence Commit（C1，本次复审对象）：`eed7f237f810c02eda1e4d59a03f8d7b9cb8c631`（`test(e2e): 时间敏感用例改为有界轮询观测服务端取消（CLEAN-001）`）。该 commit 相对上一审查对象 `dcdc076` **只改** `e2e/timeout_test.go`（+13/-4）；`findings.md` / `state.yaml` 的差异是 Cleaner 上一轮的 review-neutral tail。
- Coder 的 Review Request metadata commit（C2）：`b2b319ae303c5b58b0081ab516f6d39e1cefeda7`（只改 `state.yaml` 的 `review.*`），不作为审查对象。
- 审查时仓库状态：分支 `feat/agent-runtime-e2e-tests-v1`，工作区 clean（`git status --short` 为空），local HEAD == `origin/feat/agent-runtime-e2e-tests-v1` == `b2b319a`。
- 本任务实际变更范围（相对 shared `develop` = `4f16069`，排除他任务已合并提交）：

```text
A e2e/harness_test.go       A e2e/chat_test.go        A e2e/failure_test.go
A e2e/timeout_test.go       A e2e/concurrency_test.go A e2e/offline_test.go
M docs/design/runtime-architecture.md（仅 §12 六行 + 仅测试包边界说明 + §14 一条缺口）
```

- 任务前已有修改的区分方式：`origin/develop` 与 C1 的差集只有上述 7 条路径；`.agent/tasks/provider-timeout-classification-v1/*` 等本分支可见文件与 `origin/develop` 完全一致，未计入本任务成果。
- 关键配置/版本：根模块 `go 1.24`（实测 `go1.24.1 linux/amd64`），零 `require`；`state.resources.reservations` 为空，`.agent/workflow.yaml` `resources: {}`，本任务无全局资源。

## Result

CLEAN

## 复审记录（第二轮）

- 原 Finding：`CLEAN-001` P2（时间敏感用例瞬时读取服务端取消计数）。
- Coder 修复说明：`eed7f23` commit message「时间敏感用例改为有界轮询观测服务端取消（CLEAN-001）」；实际 Diff 与 Required Fix Boundary 逐条对齐：
  - `e2e/timeout_test.go:46-52`（`TestProviderUnresponsive`）与 `:84-90`（`TestRequestTimeout`）在断言前增加 `waitFor(t, 2*time.Second, ...)`，等待 `blocked == 1 && canceled == 1`；
  - 观察项未删除，断言仍在等待成立后校验完整三元组 `(blocked, canceled, exhausted) == (1, 1, 0)`；
  - 已通过的状态码/错误码/Run 终态/无半轮断言一字未改；未引入 `Sleep` 固定等待，未改生产代码。
- 原触发条件复验：`GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive|TestRequestTimeout' -count=10` **PASS**（修复前同命令多次 FAIL）；`go test ./... -count=1` 连跑 3 次全部 PASS；`GOMAXPROCS=1 go test ./... -count=1` PASS。
- 新风险检查：修复是否把该断言变成空断言 —— 隔离 Mutation M4（删除脚本服务侧 `blockedCanceled++`，使观测永不成立）→ `TestProviderUnresponsive` 在 `timeout_test.go:48` 以「脚本服务观察到请求阻塞与上游取消 未在 2s 内成立」失败，证明等待仍有判别力，没有把「观测不到」变成「通过」。
- 结论：`CLEAN-001` 关闭；无新增 P0/P1/P2。

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `e2e/chat_test.go:35` `TestMultiToolChainCommitsFullTurn`：`calculator`+`mock_search` 两轮 `tool_calls` → `stop`；200 + `reply` == 终稿 + `llm_calls/tool_calls = 3/2`；Session 角色序列 `user,assistant,tool,assistant,tool,assistant` 且 `core.ValidateMessages` 通过；calculator 结果 `{"operation":"mul","result":42}`、mock_search 唯一命中 `doc-3`；第 2/3 次 wire 请求分别含 3/5 条消息、`tool_call_id` 与调用一一对应、第 3 次请求回传参数未被改写、`tools[]` 为 `[calculator mock_search]` 且 `parameters` 为合法 JSON。另有 `TestSingleRoundMultipleToolCalls` 覆盖单轮多调用。Mutation M3（清空 wire 层 `ToolCallID`）使该用例在 `chat_test.go:130` 失败。 |
| AC-002 | PASS | `e2e/chat_test.go:216` `TestSessionMemoryAndCrossSessionIsolation`：A 第二轮请求共 6 条消息（summary + 首轮 4 条 + 本轮 user），包含首轮 user/assistant 原文、`replyA1` 与 `"result":42`；B 请求 2 条消息且对 `sessionAFirst/replyA1/calcCall/aSummary` 全部不含；A→B、B→A 两个方向对未被运行会话做 `reflect.DeepEqual` 整会话快照比较（`Summary`、`Todos` 均非空种子）；并断言两会话最终 `Summary`/`Todos` 未被成功运行覆盖、消息增量为 6/2。 |
| AC-003 | PASS | 可恢复失败：`e2e/failure_test.go:24` `TestRecoverableToolFailuresContinue` 三个子用例（除零 `division_by_zero`、未知工具、参数非法 `$.b`）均回填 `IsError=true` 的 tool 消息、下一请求 wire 内容为 `工具执行失败: <content>` 且 `ToolCallID` 正确、Run 仍 `COMPLETED` 并整轮提交；取消/超时：`TestRequestTimeout`/`TestClientDisconnect` 断言 `CANCELLED` + `kind=deadline_exceeded`/`cancelled`、模型调用次数不再增加、会话逐字段不变。Mutation M1（失败轮也提交）使三个失败用例族在 `assertSessionUnchanged` 处失败。 |
| AC-004 | PASS | 七行矩阵逐行取证：`TestProviderFailures`（4xx/5xx → 502 `provider_failure` + `FAILED`，响应不含上游 marker、`LLMCalls=1` 不重试）、`TestProviderUnresponsive`（Provider 侧超时 → 502 `provider_failure` + `FAILED`，脚本观测到阻塞与取消）、`TestRequestTimeout`（504 `deadline_exceeded` + `CANCELLED`）、`TestClientDisconnect`（`CANCELLED` + `cancelled`，按设计不承诺状态码）、`TestBudgetLimits`（`MaxLLMCalls` → 429 `llm_call_limit` 且第 3 次调用不发出；`MaxToolCalls` → 429 `tool_call_limit`，第二批整批拒绝、`ToolCalls=1`）。每例交叉校验 `recordingRunner` 的 `RunOutcome` 与 `run_finished` 事件的 `status`/`error_kind`/`run_id`（`harness_test.go:810` `assertTerminal`），并断言无半轮提交。Mutation M2（`KindLLMCallLimit` → `CodeProviderFailure`）使限额行在 `failure_test.go:178` 失败。复审后该 AC 涉及的稳定性缺陷已关闭。 |
| AC-005 | PASS | `e2e/offline_test.go:29` `TestNoRealProviderOrCredential`：源码扫描断言目录只含 `*_test.go`、6 个登记文件齐全、无真实 Provider 域名、无 `MINI_AGENT_LLM_` 前缀、无 `sk-`、无 `t.Skip`/`testing.Short(`/`//go:build`、所有 `http(s)://` 字面量均为 loopback；运行期断言脚本服务与 HTTP 服务均为 loopback、请求 `Host` == 脚本服务 `Host`、`Authorization` 为空、请求数 == 脚本步数。`openai.Client` 未设置 `APIKey`，测试未读取环境变量。 |
| AC-006 | PASS | `go build ./...`、`go vet ./...` 退出码 0；`go test ./... -count=1` 连跑 3 次与 `-count=3` 全 PASS；`go test -race ./... -count=2` 全 PASS 无告警；`GOMAXPROCS=1 go test ./... -count=1` 与 `GOMAXPROCS=1 ... -count=10` 全 PASS（修复前 `GOMAXPROCS=1` 下稳定失败）。`e2e/` 无 `t.Skip`、无 build tag、无外网访问（由 `TestNoRealProviderOrCredential` 机械扫描）。`-race` 未纳入 CI 属任务 Out of Scope，已记录为缺口而非 AC 失败。 |
| AC-007 | PASS | 逐 AC 的测试入口与可复核命令：`task.md` §Verification（AC-001—AC-008 各一条入口 + 命令）与 `contract.md` §Verification Requirements（AC → 测试名 → 关键断言 → 命令）逐条对应且与实现一致；本次真实失败（`CLEAN-001`）在 `findings.md` 中明确标为 FAIL 且已关闭，未被标为 PASS；缺口（v1 不注册 Todo 工具、`TokenBudget=0` 不压缩、`-race` 未入 CI、e2e registry 与生产 registry 不同）在 `task.md` Relevant Context、Contract Open Risks、Design §14 三处逐项写明。核心结论均来自业务字段（reply/计数）、Session 存储状态（`Manager.Get` 快照 + `ValidateMessages`）与运行事件（`run_finished`/wire 请求），无只断言 HTTP 200 或仅断言替身调用次数的用例。`delivery.md` 的里程碑运行记录属 Deliverer 出站产物，其环境/结果字段由 `delivery-start` Gate 之后填写，不作为本 AC 记录完整性的前置条件。 |
| AC-008 | PASS | Design §12 新增 6 行与本任务 `git ls-files 'e2e/*'` 六项双向一致；§12 规则处新增「`e2e/` 是仅测试包」边界说明；§14 新增 1 条缺口（HTTP 层 Todo/压缩不可达、registry 与生产不同、归 T08），与 APPROVED Contract 的 Design Update 两条逐条对应；`git diff origin/develop..C1 -- docs/design/runtime-architecture.md` 只有这两处，未改动 §7/§11 或其他长期事实。Task ↔ APPROVED Contract ↔ `docs/design/*` ↔ 实现四者一致。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0（`go1.24.1 linux/amd64`）；`go build ./e2e` 报 `no non-test Go files` 属 Contract D2 已声明的预期行为 |
| `go vet ./...` | PASS | exit 0 |
| `go test ./e2e -count=1 -v` | PASS | 13 个顶层用例（含子用例）全 PASS，`ok ... 0.288s` |
| `go test ./... -count=1`（连跑 3 次） | PASS | 三次均 13 个包全 ok，`e2e` 0.306s / 0.326s / 0.352s |
| `go test ./... -count=3` | PASS | 全包 ok |
| `go test -race ./... -count=2` | PASS | 全包 ok，无 data race 报告 |
| `GOMAXPROCS=1 go test ./... -count=1` | PASS | 全包 ok（上一轮同环境下 `TestProviderUnresponsive`/`TestRequestTimeout` 稳定失败） |
| `GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive\|TestRequestTimeout' -count=10` | PASS | 修复前该命令多次 FAIL；修复后全 PASS，`CLEAN-001` 触发条件消除 |
| Mutation M1：`service.Runner.Run` 非 COMPLETED 分支不返回错误 | PASS（如期失败） | `TestProviderFailures/provider_4xx`、`provider_5xx`、`TestBudgetLimits/*` 在 `assertSessionUnchanged` 失败（会话由 0 条变为 1 条 user） |
| Mutation M2：`httpapi.errorCodeOf` 的 `KindLLMCallLimit` → `CodeProviderFailure` | PASS（如期失败） | `TestBudgetLimits/MaxLLMCalls耗尽` 得到 502 `provider_failure`（期望 429 `llm_call_limit`） |
| Mutation M3：`llm/openai/wire.go` 把 tool 消息 `ToolCallID` 置空 | PASS（如期失败） | `TestMultiToolChainCommitsFullTurn` 在 `chat_test.go:130` 失败（`ToolCallID=""`） |
| Mutation M4（复审新增）：脚本服务不再记录上游取消（观测永不成立） | PASS（如期失败） | `TestProviderUnresponsive` 在 `timeout_test.go:48` 失败：「脚本服务观察到请求阻塞与上游取消 未在 2s 内成立」 → 新增的有界等待不是空断言 |
| 隔离方式与残留 | PASS | Mutation 只在 `git worktree add --detach` 的独立 worktree（`/tmp/e2e-mut-dcdc076`、`/tmp/e2e-remut`）中执行，未在 Review Target working tree 修改，未 commit；验证后 `git worktree remove --force`，`git worktree list` 仅剩主工作树，`git status --short` 为空 |
| 全局资源一致性 | PASS | Contract/`state.yaml` 声明无 Shared Resource；`.agent/workflow.yaml` `resources: {}`；实现未引入错误码域或 migration → Registry ↔ Contract ↔ 实现三边无漂移 |
| 生产文件零改动 | PASS | `git diff --name-status origin/develop..C1` 不含任何 `core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`/`agent`/`httpapi`/`service`/`builtin`/`cmd` 文件；C1 相对上一审查对象只改 `e2e/timeout_test.go` |

## Findings

### CLEAN-001：时间敏感用例瞬时读取服务端取消计数，`go test ./...` 会间歇失败

- Severity：P2
- Status：CLOSED（复审确认，关闭证据见下）
- Location：`e2e/timeout_test.go:46-52`、`:84-90`（修复前为 `:46-49`、`:79-82`）
- AC / Invariant：AC-006（`go test ./...` 退出码 0、时间敏感用例重复执行稳定）、AC-004 两行的证据可信度；Contract R4「断言服务端收到 N 次请求用有界轮询（≤2s）而不是瞬时读取」
- Trigger：任一让客户端先于脚本服务 goroutine 得到结论的调度（普通 `go test ./...` 的包内并行、受限 CPU）
- Actual（修复前）：HTTP 响应返回后立即 `h.llm.stats()` 并断言 `canceled == 1`，脚本服务可能尚未观察到上游取消 → `canceled=0` 失败
- Expected：服务端异步观测在有界时间内等待成立后再断言，且不放宽已通过的语义
- Impact（修复前）：仓库级 `go test ./...` 不可重复，CI/`pr-check` 随机红灯
- Evidence（修复前）：`go test ./... -count=1` → `timeout_test.go:48 脚本观测 = (blocked=1, canceled=0, exhausted=0)`；`GOMAXPROCS=1 ... -count=10` 多次复现
- **关闭证据**：`eed7f23` 在断言前加 `waitFor(2s)` 等待观测，观察项与三元组断言保留；原触发命令 `GOMAXPROCS=1 ... -count=10` 与 `go test ./... -count=1` ×3 全部 PASS；Mutation M4（观测永不成立）仍能使该用例失败，说明等待未把断言变成空断言。
- Required Fix Boundary：已在 `eed7f23` 范围内满足；无残留修复项。

说明（非 Finding，供 Owner / 后续角色参考）：

- 本任务 `e2e/` 与 `origin/develop` 的差集、Design §12/§14 与 APPROVED Contract 一致；四处 Mutation（M1–M4）均能杀死对应错误实现，测试判别力在 AC-001/003/004 与时间观测上成立。
- Contract `Verification Requirements` 中 AC-001 行把 Session 角色序列写成 `user,assistant,tool,tool,assistant`（5 条），与 AC-001 正文「连续两轮 `tool_calls`」及实际实现（6 条）不一致；`task.md` 已记录该措辞偏差并以 AC 正文为准。修正属 Contract Revision（Analyst/Owner），不在 Coder 修复范围，本审查不据此阻塞。
- 产物归属：`e2e/` 六文件由 `e0b4021` 与另一任务的 task 文件同 commit 引入（历史不可改写）；本审查按 C1 的树而非 commit message 判定归属。
