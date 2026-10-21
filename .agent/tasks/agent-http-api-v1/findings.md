# Cleaner Findings

## Review Target

- 任务：`agent-http-api-v1`（T09），初始审查（`initial_review`）。
- 任务基线：`4852d9ce2e5c84e592431ce857b770bd2de8aa9a`（`chore(agent): 绑定 Contract 批准证据 A1=46d871d`），含 T07 已交付 `agent/**`、APPROVED Contract（A1 = `46d871d`）与已落库 Design（`b069226`）。
- **被审查对象 C1 = `3ce1869b2c1206464d59cd70ee6f1f820a2b3f43`**（`feat(httpapi): 交付 Agent Runtime HTTP JSON API、同 Turn 装配层与可启动入口`）。C1 相对基线的变更 = 新增 19 个文件、0 删除、0 修改既有文件：
  - `httpapi/{config,server,handler,request,errors,auth,run}.go` + `httpapi/{config,handler,errors,request,auth,lifecycle,fake}_test.go`
  - `service/{runner,turn}.go` + `service/runner_test.go`
  - `cmd/mini-agent/main.go` + `cmd/mini-agent/main_test.go`
- C2 = `976f681`（`chore(agent): 发起 Review Request`）只改 `state.yaml` 的 `review.status` / `review.target`，不属审查对象。
- 与 C1 同区间但不属于 C1 的提交：`1970abe`（TaskBuilder 的 `task.md` T09 编号与 Review Baseline 回写）。已单独核对 `git show --stat 3ce1869`，确认 C1 自身不含 `.agent/**`、`docs/**` 任何改动。
- 任务开始时已有修改：无（`git status --short` 为空）。审查时工作区 clean，`HEAD == origin/feat/agent-http-api-v1`。
- 关键配置/版本：`go 1.24`、本机 `go1.24.1 linux/amd64`；`go.mod` 零 `require`，`git diff 4852d9c 3ce1869 -- go.mod Makefile .cnb.yml` 为空；无 migration / 错误码域等 Registry 资源占用（`resources.reservations = {}`，`resources: {}`）。
- 关键配置与迁移版本：不涉及（本任务无 DB / migration）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `httpapi/handler_test.go:TestRealRunnerEndToEnd`（真实 `session.Manager` + 真实 `agent.Loop`（经 `service`）+ Fake Provider）：chat 响应含 `session_id`/`run_id`/`status=COMPLETED`/最终文本；第二轮 `provider.requestAt(3)` 的 `Messages` 同时含上一轮 user「1+2 等于多少」与 assistant「3」；`TestRunSecondTurnSeesPreviousHistory` 在 `service` 层独立证明同一事实；`countUser==2` 证明无重复 user 消息 |
| AC-002 | PASS | 同一测试：脚本先 `tool_calls`(call-1, `{"op":"add","a":1,"b":2}`) 再文本「3」；`reply=="3"`（不是中间 tool_call 载荷）、`(llm_calls,tool_calls)==(2,1)`；`recordingTool.executed()` 记录到 1 次 call-1 且参数原样；提交历史角色 = user, assistant(tool_calls), tool, assistant 且通过 `core.ValidateMessages` |
| AC-003 | PASS | ① `TestChatSessionPrecheck`（TTL 时钟推进）→ `session_not_found` 404 / `session_expired` 404 且 `runner.callCount()==0`；`TestChatRunnerSessionSentinel` 证明 Runner 返回的哨兵映射为同一 code（TOCTOU）。② `TestChatRejectsInvalidInputWithoutSideEffects` + `request_test.go`：错误/缺失 `Content-Type` 415、数组/`null`/尾部数据 400、未知字段 400、缺必填 400、类型/长度/UTF-8 400、超限 413，均无 panic 且 `runner.callCount()==0`、会话快照逐条不变。③ `TestChatErrorMapping`（15 例表驱动）断言 provider/tool/limit/internal 各自码与状态码，并断言响应体不含 `SECRET-PAYLOAD`、`agent.Loop`、`/root/`、`goroutine`、`stack`、CallID。④ `TestRunProviderFailureLeavesNoPartialHistory`（真实 Manager）证明失败整轮丢弃、历史仍合法 |
| AC-004 | PASS | `TestConfigDefaults`（默认 `127.0.0.1:8080`）、`TestConfigLoopbackBoundary`（`0.0.0.0:0`/`:0`/公网/IP 主机名缺凭证 → `Validate` 失败；带凭证 → 通过）、`TestNewFailClosedWithoutListening`（构造失败且 `Addr()==""`）、`TestAuthMissingAndWrongCredentialAreIdentical`（缺失/错误凭证响应体逐字节相同、均 401、`Runner` 计数 0、无会话写入、不回显凭证）、`TestAuthPrecedesBodyParsing`（认证先于 Content-Type 与 body）、`TestHealthzExemptFromAuthAndLeakFree`（`{"status":"ok"}` 且不含 `auth_token`/`api_key`/`model` 等标记）、`TestConfigStringRedaction`（`%v`/`%+v`/`%#v`/`String`/`GoString` 均 `<redacted>`/`<unset>`）。Cleaner 额外以真实 binary 复核：`0.0.0.0:18099` 无 token → 打印「非回环地址时必须配置认证凭证」并 `exit=1`，未监听 |
| AC-005 | PASS | 全部 HTTP 测试走 `httptest` / `127.0.0.1:0` + Fake `llm.Provider`（`httpapi/fake_test.go`、`service/runner_test.go`）+ 内存 `session.Manager`；无外部网络、无真实 Key、无 Docker/DB。零泄漏扫描覆盖响应体/响应头/`/healthz`/`Config.String()`（`httpapi` 侧）与运行日志字节（`cmd/mini-agent/main_test.go` 的 `syncBuffer` + `scanForSecrets`，且断言日志非空）。说明：`cmd` 端到端用进程内 OpenAI 兼容假服务（回环）而非 Fake `Provider`，属更严格的装配验证，仍满足「不依赖真实模型/真实 Key/外部网络」 |
| AC-006 | PASS | 四方核对：`docs/design/http-api.md` §3/§4 与 `contract.md`「端点与字段」「错误封套与错误码」逐条比对实现——方法/路径、请求字段（`session_id` 字符集 `^[A-Za-z0-9._:-]{1,128}$`、`message` 1..8192）、成功响应 6 字段、20 个错误码与状态码（400/401/404/405/409/413/415/429/500/502/504/408）完全一致；单点映射只在 `httpapi/errors.go`；`SessionStore`/`Runner`/`RunRequest`/`RunOutcome` 端口签名与 Contract 一致。§12 路径集合与 `git ls-files` 双向差集为空（`comm -23`/`comm -13` 均无输出）。`go list -f '{{.ImportPath}} {{join .Imports " "}}'`：`httpapi` 仅 `core`+`session`，`service` 仅 `httpapi`+`agent`+`core/llm/tool/session/contextmgr/runlog`，`cmd` 为唯一装配点；`grep` 确认 L0–L3（`core`/`llm`/`tool`/`session`/`contextmgr`/`runlog`/`builtin`/`agent`）无 `httpapi`/`service`/`cmd` import。`git diff go.mod` 为空、零 `require`；`runtime-architecture.md` §2/§3/§14 已登记新 L3 入口并区分 T09 与 T08 |
| AC-007 | PASS | `httpapi/lifecycle_test.go:TestClientDisconnectCancelsRun`：客户端 `cancelRequest()` 后 Fake Provider 观察到 `ctx.Err()==context.Canceled`，会话消息数不变且仍通过 `core.ValidateMessages`（未断言收到 408，符合 D5 修正）；`TestRequestTimeoutReturns504`：30ms `RequestTimeout` → `504 deadline_exceeded` 且 ctx 错误为 `DeadlineExceeded`；`service/runner_test.go:TestRunClientCancelLeavesNoPartialHistory` / `TestRunDeadlineIsDistinguishableFromCancel`：终态均 `CANCELLED`、错误种类分别为 `cancelled` / `deadline_exceeded`（互不相同、都不是 `internal`）、超时路径零写入 |
| AC-008 | PASS | `TestServeReadyHealthzAndGracefulShutdown`（就绪后 `/healthz` 200、关闭后新连接被拒、`Shutdown`/`Close` 重复调用不 panic）、`TestShutdownWaitsForInflightRequest`（在途请求宽限期内返回 200，`Serve` 返回 nil）、`TestShutdownCancelsInflightRunAfterGrace`（宽限期满 `Shutdown` 返回超时错误、在途 Run 的 ctx 被取消、`Serve` 返回、`Close` 幂等）、`TestServeFailsWhenAddressInUse`（监听失败返回错误且 `Addr()==""`）；`cmd/mini-agent` 侧：`TestRunFailClosedOnInvalidConfig`（9 例非法配置均返回错误、未就绪、错误文本不含凭证）、`TestRunEndToEndAndGracefulShutdown`（信号后限时退出、关闭后新连接被拒）、`TestRunShutdownCancelsInflightProvider`（宽限期满后假 Provider 观察到 `r.Context().Done()`）、`TestRunRepeatedTerminationSignals`（重复信号不 panic） |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0（`go1.24.1 linux/amd64`） |
| `go vet ./...` | PASS | 退出码 0 |
| `make test`（= `go test ./...`） | PASS | 12 个包全部 `ok` |
| `go test -count=1 -race ./...` | PASS | 12 个包全部 `ok`（`httpapi` 2.187s / `service` 1.075s / `cmd` 1.092s），无 data race 报告 |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | `workflow-check` 与 `internal/workflow` 均 `ok` |
| 分层机械核对 | PASS | `go list` 三行 import 与 Contract 一致；L0–L3 无新包 import |
| `go.mod` / `Makefile` / `.cnb.yml` 零改动 | PASS | `git diff 4852d9c 3ce1869 --` 三者均为空 |
| §12 文件所有权 ↔ `git ls-files` | PASS | 双向 `comm` 差集为空 |
| Scope 检查 | PASS | C1 仅新增 19 个文件；`agent/**` 与 L0–L2、`docs/**`、`.agent/**`（本任务目录外）零改动；无 SSE/WS、无 Todo 工具注册、无持久化、无第三方依赖、无无关重构 |
| Registry 三边一致性 | PASS | 本任务无 Shared Resource（`resources: {}`、`reservations: {}`）；HTTP 错误码为本协议封闭枚举，按 TaskBuilder 假设属 B 类命名空间，不涉 Registry |
| 真实 binary 启动（Cleaner 独立执行） | PASS | 非回环无 token → `exit=1` 且未监听；回环 + token → `/healthz` 200、无 token 401、带 token `POST /v1/sessions` 201（32 位 hex）、未知会话 chat 404 `session_not_found`；随后进程被终止，无残留 |
| Mutation 验证（隔离 worktree `/tmp/review-c1`，detached `3ce1869`，事后已 `worktree remove` + `prune`，主工作区 clean） | PASS | 4 个可逆变异全部被现有测试捕获（详见下） |

Mutation 记录（全部在隔离副本执行、随后 `git checkout --` 恢复并 `worktree remove`，主仓库未受影响）：

| # | 变异 | 预期失败 | 实测 |
|---|---|---|---|
| M1 | `httpapi/auth.go` 中 `authorized` 恒 `true`（去掉凭证校验） | 认证相关测试 | `TestAuthMissingAndWrongCredentialAreIdentical`、`TestAuthPrecedesBodyParsing`（3 子例）、`TestAuthAcceptsConfiguredCredential`、`cmd TestRunEndToEndAndGracefulShutdown` 全 FAIL |
| M2 | `service/runner.go` 删除回调内 `if result.Status != COMPLETED { return runErrorOf(result) }`（失败轮次不再丢弃） | 无部分提交 | `TestRunProviderFailureLeavesNoPartialHistory` FAIL |
| M3 | `service/runner.go` 重复 `turn.Append(userMessage(...))`（重复 user 消息） | 同 Turn 一次用户消息 | `TestRunUsesExactlyOneRealRunTurn`、`TestRunSecondTurnSeesPreviousHistory`、`TestRunSameSessionConcurrentChatsSerialize`、`TestRealRunnerEndToEnd` FAIL |
| M4 | `httpapi/errors.go` 把 `core.KindDeadlineExceeded` 映射为 `cancelled`（取消/超时不可区分） | AC-007 可区分性 | `TestErrorCodeOf/deadline_exceeded`、`TestChatErrorMapping/超时终态`、`TestRequestTimeoutReturns504` FAIL |

## Findings

### CLEAN-001：关闭耗时最坏情况可达约 2×`ShutdownGrace`

- Severity：P3
- Status：OPEN
- Location：`httpapi/server.go:149-174`（`Shutdown` 中 `s.http.Shutdown(ctx)` 超时后 `s.cancel()` → `s.waitIdle(s.cfg.ShutdownGrace)` → `s.http.Close()`）
- AC / Invariant：AC-008「进程在限定时间内退出」；INV-H11（关闭幂等、期满取消在途 Run、不遗留执行中任务）
- Trigger：在途 Run 不响应取消（或响应较慢），且 `Shutdown(ctx)` 的 `ctx` 已到期
- Actual：`Shutdown` 先在调用方 `ctx`（`cmd` 传入 `ShutdownGrace`）上等满一个宽限期，返回超时后再用一个完整 `ShutdownGrace` 做 `waitIdle`，最坏总耗时约 2×`ShutdownGrace`（默认 10s → 约 20s）
- Expected：关闭行为有界；契约与 Design 未承诺精确上界，但 `k8s terminationGracePeriodSeconds` 等外部编排若按 1× grace 设定可能被提前 SIGKILL
- Impact：极端情况下进程退出晚于运维预期；不影响正确性（不遗留执行中 Run 的目标反而靠 `waitIdle` 保证），现有测试（50ms grace）实测仍在 3s 内返回
- Evidence：`httpapi/server.go` 的 `Shutdown` 实现；`TestShutdownCancelsInflightRunAfterGrace` 通过（未断言绝对耗时上界，仅断言 `<3s`）
- Required Fix Boundary：不需要修改实现即可 CLEAN。若 Owner 认为需要确定上界，应把「取消后等待」与「宽限期」共享同一时间预算（或把该上界写进 `docs/design/http-api.md` §7），属 Contract/Design 级决定，不由 Coder 自行改语义

> 无 P0 / P1 / P2；未发现开放阻塞项。

## 复审提示

- `CLEAN` 仅对 `review.target = 3ce1869b2c1206464d59cd70ee6f1f820a2b3f43` 有效。
- 此后若 `httpapi/**`、`service/**`、`cmd/mini-agent/**`、`docs/design/http-api.md` 或任务约束出现实质变化（task-scoped default-deny），本结论即机械失效，需重新审查新对象。
- C2（`976f681`）与本次 Cleaner artifacts commit 均为 review-neutral tail，`review.target` 不前移。
