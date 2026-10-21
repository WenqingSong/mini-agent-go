# Cleaner Findings

## Review Target

- 任务基线（TaskBuilder 记录）：`d54c912540c090fa7c80961cb6a7cbee8e6ec9a0`；恢复基线：`f4414138e31250c8d1b49bae5dd01b91f048c155`（`merge: 合并 origin/develop 到 feat/agent-runtime-e2e-tests-v1`）。
- Implementation Evidence Commit（C1，本次唯一审查对象）：`dcdc076edd0f1715f6afe1a77de9c6cc2b0b03b4`。该 commit 由 TaskBuilder 用于解除阻塞，其**树**已包含本任务全部实现（`e2e/` 六个文件由 `e0b4021` 引入，此后零改动，`git log -- e2e/` 只有 `e0b4021` 一条）。
- Coder 的 Review Request metadata commit（C2）：`f99bcad58fd1dd69b059615b26d67238d74f4039`（只改 `state.yaml` 的 `review.*`），不作为审查对象。
- 审查时仓库状态：分支 `feat/agent-runtime-e2e-tests-v1`，工作区 clean（`git status --short` 为空），local HEAD == `origin/feat/agent-runtime-e2e-tests-v1` == `f99bcad`。
- 本任务实际变更范围（相对 shared `develop` = `4f16069`，排除他任务已合并提交）：

```text
A e2e/harness_test.go       A e2e/chat_test.go        A e2e/failure_test.go
A e2e/timeout_test.go       A e2e/concurrency_test.go A e2e/offline_test.go
M docs/design/runtime-architecture.md（仅 §12 六行 + 仅测试包边界说明 + §14 一条缺口）
```

- 任务前已有修改的区分方式：`origin/develop` 与 C1 的差集只有上述 7 条路径；`.agent/tasks/provider-timeout-classification-v1/*` 等本分支可见文件与 `origin/develop` **完全一致**（`git diff --stat origin/develop HEAD -- .agent/tasks/provider-timeout-classification-v1/` 无输出），未计入本任务成果。
- 关键配置/版本：根模块 `go 1.24`（实测 `go1.24.1 linux/amd64`），零 `require`；`state.resources.reservations` 为空，`.agent/workflow.yaml` `resources: {}`，本任务无全局资源。

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `e2e/chat_test.go:35` `TestMultiToolChainCommitsFullTurn`：`calculator`+`mock_search` 两轮 `tool_calls` → `stop`；200 + `reply` == 终稿 + `llm_calls/tool_calls = 3/2`；Session 角色序列 `user,assistant,tool,assistant,tool,assistant` 且 `core.ValidateMessages` 通过；calculator 结果 `{"operation":"mul","result":42}`、mock_search 唯一命中 `doc-3`；第 2/3 次 wire 请求分别含 3/5 条消息、`tool_call_id` 与调用一一对应、第 3 次请求回传的参数未被改写、`tools[]` 为 `[calculator mock_search]` 且 `parameters` 为合法 JSON。另有 `TestSingleRoundMultipleToolCalls` 覆盖单轮多调用。独立 Mutation M3（清空 wire 层 `ToolCallID`）使该用例在 `chat_test.go:130` 失败，证明配对断言有判别力。独立执行 `go test ./e2e -count=1 -v` 全部 PASS。 |
| AC-002 | PASS | `e2e/chat_test.go:216` `TestSessionMemoryAndCrossSessionIsolation`：A 第二轮请求共 6 条消息（summary + 首轮 4 条 + 本轮 user），包含首轮 user/assistant 原文、`replyA1` 与 `"result":42`；B 请求 2 条消息且对 `sessionAFirst/replyA1/calcCall/aSummary` 全部不含；A→B、B→A 两个方向对未被运行会话做 `reflect.DeepEqual` 整会话快照比较（`Summary`、`Todos` 均非空种子，`Messages` 非平凡）；同时断言两会话最终 `Summary`/`Todos` 未被成功运行覆盖、消息增量为 6/2。 |
| AC-003 | PASS | 可恢复失败：`e2e/failure_test.go:24` `TestRecoverableToolFailuresContinue` 三个子用例（除零 `division_by_zero`、未知工具、参数非法 `$.b`）均回填 `IsError=true` 的 tool 消息、下一请求 wire 内容为 `工具执行失败: <content>` 且 `ToolCallID` 正确、Run 仍 `COMPLETED` 并整轮提交；取消/超时：`TestRequestTimeout`/`TestClientDisconnect`（`e2e/timeout_test.go`）断言 `CANCELLED` + `kind=deadline_exceeded`/`cancelled`、模型调用次数不再增加、会话逐字段不变。独立 Mutation M1（失败轮也提交）使三个失败用例族在 `assertSessionUnchanged` 处失败，证明「无半轮提交」断言有判别力。 |
| AC-004 | PASS | 七行矩阵逐行取证：`TestProviderFailures`（4xx/5xx → 502 `provider_failure` + `FAILED`，且响应不含上游 marker、`LLMCalls=1` 不重试）、`TestProviderUnresponsive`（Provider 侧超时 → 502 `provider_failure` + `FAILED`，脚本观测到阻塞与取消）、`TestRequestTimeout`（504 `deadline_exceeded` + `CANCELLED`）、`TestClientDisconnect`（`CANCELLED` + `cancelled`，按设计不承诺状态码，仅断言断连后无可用响应）、`TestBudgetLimits`（`MaxLLMCalls` → 429 `llm_call_limit` + `LIMIT_EXCEEDED` 且第 3 次调用不发出；`MaxToolCalls` → 429 `tool_call_limit`，第二批整批拒绝、`ToolCalls=1`）。每例均交叉校验 `recordingRunner` 的 `RunOutcome` 与 `run_finished` 事件的 `status`/`error_kind`/`run_id`（`harness_test.go:810` `assertTerminal`），并断言无半轮提交。独立 Mutation M2（`KindLLMCallLimit` → `CodeProviderFailure`）使限额行在 `failure_test.go:178` 失败。行为证据成立；其中两例附带的「服务端取消计数」断言不稳定，见 CLEAN-001（不影响本 AC 的三元组结论，但影响 AC-006）。 |
| AC-005 | PASS | `e2e/offline_test.go:29` `TestNoRealProviderOrCredential`：源码扫描断言目录只含 `*_test.go`、6 个登记文件齐全、无真实 Provider 域名（按片段拼接，避免自命中）、无 `MINI_AGENT_LLM_` 前缀、无 `sk-`、无 `t.Skip`/`testing.Short(`/`//go:build`、所有 `http(s)://` 字面量均为 loopback；运行期断言脚本服务与 HTTP 服务均为 loopback、请求 `Host` == 脚本服务 `Host`、`Authorization` 为空、请求数 == 脚本步数。全部模型交互只发生在进程内 `httptest`，`openai.Client` 未设置 `APIKey`，测试未读取环境变量。 |
| AC-006 | FAIL | `go build ./...`、`go vet ./...`、`go test -race ./...` 退出码 0，并发三例在 `-race` 下无告警；但 `go test ./... -count=1` 在本次独立执行中**退出码 1**：`TestProviderUnresponsive` 失败（`timeout_test.go:48: 脚本观测 = (blocked=1, canceled=0, exhausted=0)，期望 (1, 1, 0)`）。`GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive|TestRequestTimeout' -count=10` 稳定复现 `TestRequestTimeout`（`timeout_test.go:81`）与 `TestProviderUnresponsive`（`timeout_test.go:48`）同类失败。见 CLEAN-001。「时间敏感用例重复执行结果稳定」不成立。 |
| AC-007 | NOT_VERIFIED | 可由 Cleaner 独立核对的部分成立：核心结论均来自业务字段（reply/计数）、Session 存储状态（`Manager.Get` 快照、`ValidateMessages`）与运行事件（`run_finished`/wire 请求），不存在只断言 HTTP 200 或替身调用次数的核心用例；`e2e/` 无 `t.Skip`。**缺少条件**：AC 要求的「任务交付记录逐条列出每个 AC 的测试入口与可复核命令 / 未通过项 / 被跳过检查 / 现存缺口」属 `delivery.md`（Deliverer 产物），当前仍是占位「等待 Deliverer」；Cleaner 不代写，也不把未落库的记录判为 PASS。 |
| AC-008 | PASS | Design §12 新增 6 行与本任务 `git ls-files 'e2e/*'` 六项双向一致；§12 规则处新增「`e2e/` 是仅测试包」边界说明；§14 新增 1 条缺口（HTTP 层 Todo/压缩不可达、registry 与生产不同、归 T08），与 APPROVED Contract 的 Design Update 两条逐条对应；`git diff origin/develop..C1 -- docs/design/runtime-architecture.md` 只有这两处，未改动 §7/§11 或其他长期事实。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0（`go1.24.1 linux/amd64`） |
| `go vet ./...` | PASS | exit 0 |
| `go test ./e2e -count=1 -v` | PASS | 13 个顶层用例（含 7 个子用例）全 PASS，`ok ... 0.285s` |
| `go test ./... -count=1` | **FAIL** | e2e 包失败：`TestProviderUnresponsive`（`timeout_test.go:48`，`canceled=0`），整体 exit 1 |
| `go test -race ./... -count=1` | PASS | 13 个包全 ok，无 data race 报告 |
| `go test ./e2e -count=10` | PASS | 该轮未触发抖动 |
| `go test ./... -count=3` | PASS | 该轮未触发抖动 |
| `go test ./e2e -run 'TestProviderUnresponsive\|TestRequestTimeout' -count=20` | PASS | 默认 `GOMAXPROCS` 下该轮未触发 |
| `GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive\|TestRequestTimeout' -count=10` | **FAIL** | 两用例多次失败，失败点均为「脚本观测 canceled=0」；证明该断言对调度敏感 |
| 诊断验证：隔离副本中把两处瞬时统计改为 `waitFor` 有界等待后重跑 | PASS | `GOMAXPROCS=1 ... -count=5` 全通过 → 生产行为正确，缺陷在测试断言时序 |
| Mutation M1：`service.Runner.Run` 非 COMPLETED 分支不返回错误（失败轮也提交） | PASS（如期失败） | `TestProviderFailures/provider_4xx`、`provider_5xx`、`TestBudgetLimits/*` 在 `assertSessionUnchanged` 失败（会话由 0 条变为 1 条 user） |
| Mutation M2：`httpapi.errorCodeOf` 的 `KindLLMCallLimit` → `CodeProviderFailure` | PASS（如期失败） | `TestBudgetLimits/MaxLLMCalls耗尽` 得到 502 `provider_failure`（期望 429 `llm_call_limit`） |
| Mutation M3：`llm/openai/wire.go` 把 tool 消息 `ToolCallID` 置空 | PASS（如期失败） | `TestMultiToolChainCommitsFullTurn` 在 `chat_test.go:130` 失败（`ToolCallID=""`） |
| 隔离方式与残留 | PASS | Mutation 只在 `git worktree add --detach /tmp/e2e-mut-dcdc076 dcdc076` 的独立 worktree 中执行，未在 Review Target working tree 修改，未 commit；验证后 `git worktree remove --force`，`git worktree list` 仅剩主工作树，仓库 `git status --short` 为空 |
| 全局资源一致性 | PASS | Contract/`state.yaml` 声明无 Shared Resource；`.agent/workflow.yaml` `resources: {}`；实现未引入错误码域或 migration → Registry ↔ Contract ↔ 实现三边无漂移 |
| 生产文件零改动 | PASS | `git diff --name-status origin/develop..C1` 不含任何 `core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`/`agent`/`httpapi`/`service`/`builtin`/`cmd` 文件，符合 Contract `Allowed / Forbidden Changes` |

## Findings

### CLEAN-001：时间敏感用例瞬时读取服务端取消计数，`go test ./...` 会间歇失败

- Severity：P2
- Status：OPEN
- Location：`e2e/timeout_test.go:46-49`（`TestProviderUnresponsive`）、`e2e/timeout_test.go:79-82`（`TestRequestTimeout`）
- AC / Invariant：AC-006（`go test ./...` 退出码 0、时间敏感用例重复执行稳定）、AC-004 两行的证据可信度；Contract R4「断言服务端收到 N 次请求用有界轮询（≤2s）而不是瞬时读取」
- Trigger：任一让客户端先于脚本服务 goroutine 得到结论的调度（普通 `go test ./...` 的包内并行、受限 CPU）；客户端超时/ctx 取消在客户端侧同步返回，而脚本服务侧的 `r.Context().Done()` 由连接中断异步送达
- Actual：HTTP 响应返回后立即 `h.llm.stats()` 并断言 `canceled == 1`，此时脚本服务可能尚未观察到上游取消，得到 `canceled=0` 而失败。本次独立执行 `go test ./... -count=1` 即出现 FAIL；`GOMAXPROCS=1` 下 `count=10` 多次复现两用例失败
- Expected：时间敏感断言使用有界轮询（同文件 `TestClientDisconnect` 已用 `waitFor` 的做法），在不放宽语义的前提下稳定通过
- Impact：仓库级 `go test ./...` 结果不可重复，CI/`pr-check` 会随机红灯；Deliverer 与 Owner 无法把「全绿」当作可靠证据；违反 Contract R4 与 AC-006 的稳定性要求
- Evidence：`go test ./... -count=1` → `--- FAIL: TestProviderUnresponsive (0.05s) timeout_test.go:48: 脚本观测 = (blocked=1, canceled=0, exhausted=0)，期望 (1, 1, 0)`；`GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive|TestRequestTimeout' -count=10` 多次同类失败（含 `timeout_test.go:81`）；在隔离副本中仅把该统计改为 `waitFor(t, 2*time.Second, ...)` 后，同一环境下 `-count=5` 全部通过
- Required Fix Boundary：这两处（及同类）服务端异步观测必须在有界时间内等待成立后再断言，或改为不依赖异步观测的等价断言；不得删除 `canceled`/`blocked` 观察项本身、不得放宽已通过的「状态码 + 错误码 + Run 终态 + 无半轮」断言，也不得引入 `Sleep` 固定等待

说明（非 Finding，供 Owner / 后续角色参考）：

- 本任务 `e2e/` 与 `origin/develop` 的差集、Design §12/§14 与 APPROVED Contract 一致；三处 Mutation（M1/M2/M3）均能杀死对应错误实现，测试判别力在 AC-001/003/004 上成立。
- Contract `Verification Requirements` 中 AC-001 行把 Session 角色序列写成 `user,assistant,tool,tool,assistant`（5 条），与 AC-001 正文「连续两轮 `tool_calls`」及实际实现（6 条）不一致；`task.md` 已记录该措辞偏差并以 AC 正文为准。修正属 Contract Revision（Analyst/Owner），不在 Coder 修复范围，本 Finding 不据此阻塞。
- 产物归属：`e2e/` 六文件由 `e0b4021` 与另一任务的 task 文件同 commit 引入（历史不可改写）；本审查按 C1 的树而非 commit message 判定归属。
