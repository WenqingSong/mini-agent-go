# Delivery Verification

## Milestone and Target

- Milestone：`runtime-offline-acceptance-v1`（全系统离线集成测试与可靠性验收）
- Delivery Target：`c99ace913e4d3fe55004976f5efa7efd1e40f395`（`feat/agent-runtime-e2e-tests-v1`，= `origin/feat/agent-runtime-e2e-tests-v1`）
- Cleaner Review Target：`eed7f237f810c02eda1e4d59a03f8d7b9cb8c631`（`review.status=CLEAN`，`owner.status=ACCEPTED`，`owner.review_target` 同值）
- Target Match：YES — `git diff --name-only eed7f23..c99ace9` 仅含本任务 4 个 Artifact（`core-logic.md` / `findings.md` / `owner-decision.md` / `state.yaml`）；`e2e/*`、`docs/design/runtime-architecture.md` 及全部生产包零差异，CLEAN 对本次验收对象有效
- develop_base：`4f160699c934d49374d46b680eadc8bd42d67cef`（`origin/develop`，验收时未前进；其为 Delivery Target 的祖先）

开始 Gate：`.agent/bin/workflow-check gate delivery-start .agent/tasks/agent-runtime-e2e-tests-v1` → `PASS`（exit 0）。

## Environment

- OS / 运行时：Linux 容器，`go1.24.1 linux/amd64`；根模块 `go 1.24`，零 `require`。
- 依赖服务：无。本里程碑为进程内离线验收，不需要 MySQL / Redis / Kafka / Docker；未使用、也未调用任何外部服务。
- 隔离方式：`git worktree add --detach /tmp/deliver-verify-c99ace9 c99ace9` 独立工作树（clean，仅含已跟踪文件），全部构建、测试与二进制冒烟在该目录执行；主工作树未改动，验证后 `git worktree remove`。
- 凭据与配置来源：验收环境未设置 `MINI_AGENT_LLM_BASE_URL` / `MINI_AGENT_LLM_API_KEY`（`env | grep -c MINI_AGENT_LLM` → `0`）；`openai.Client` 的 `APIKey` 为空（脚本服务记录 `Authorization` 头均为空）；二进制冒烟仅使用本机 loopback 与环境变量中的非敏感端口/模型名，不记录也无 Secret。
- 测试数据与清理：Session 为进程内 `session.Manager`（内存），随进程结束释放；脚本化 OpenAI 服务与冒烟程序位于仓库外 `/tmp/deliver-smoke`，不属于交付物；验证后进程已终止、临时 worktree 已移除。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 正式构建 `go build ./...` | PASS | 隔离 worktree exit 0；`go build ./e2e` 报 `no non-test Go files`，与 Contract D2 声明的预期行为一致，不计为构建失败 |
| 静态检查 `go vet ./...` | PASS | exit 0 |
| 全量测试 `go test ./... -count=1` | PASS | 13 个包全 `ok`，`e2e` 0.295s，exit 0 |
| 验收套件逐例 `go test ./e2e -count=1 -v` | PASS | 13 个顶层用例全 PASS（`TestMultiToolChainCommitsFullTurn`、`TestSingleRoundMultipleToolCalls`、`TestSessionMemoryAndCrossSessionIsolation`、`TestConcurrentChatsSameSession`、`TestConcurrentChatsDifferentSessions`、`TestConcurrentToolCalls`、`TestRecoverableToolFailuresContinue`、`TestProviderFailures`、`TestBudgetLimits`、`TestNoRealProviderOrCredential`、`TestProviderUnresponsive`、`TestRequestTimeout`、`TestClientDisconnect`），无 SKIP |
| 并发 `go test ./e2e -race -count=3` | PASS | exit 0，`ok e2e 2.018s`，无 data race 报告 |
| 全量 `go test -race ./... -count=1` | PASS | 13 个包全 `ok`（`e2e` 1.373s、`httpapi` 2.851s），无 race 告警 |
| 重复稳定性 `go test ./... -count=3` | PASS | 全包 `ok`，时间敏感用例未抖动 |
| 受限调度 `GOMAXPROCS=1 go test ./... -count=1` | PASS | 全包 `ok` |
| 受限调度时间敏感重复 `GOMAXPROCS=1 go test ./e2e -run 'TestProviderUnresponsive\|TestRequestTimeout\|TestClientDisconnect' -count=10` | PASS | `ok e2e 2.613s`，30 次运行全部通过（CLEAN-001 原触发条件未复现） |
| Engine 回归 `GOWORK=off go -C .agent/engine test ./...` | PASS | `cmd/workflow-check`、`internal/workflow` 均 `ok`（本任务未改动 Engine） |
| 真实二进制启动 + 健康检查 | PASS | 由 `c99ace9` 构建 `cmd/mini-agent` 二进制；`MINI_AGENT_LISTEN_ADDR=127.0.0.1:38081`、`LLM_BASE_URL=http://127.0.0.1:38080/v1`、`API_KEY` 为空启动成功，`GET /healthz` → 200 `{"status":"ok"}`；`POST /v1/sessions` → 201；未带凭证的 `POST /v1/chat` → 401 |
| 二进制主链路（真实 HTTP → 真实工具 → 真实 Client） | PASS | `POST /v1/chat {"message":"帮我算 6 乘 7"}` → 200 `{"status":"COMPLETED","reply":"6 乘 7 等于 42","llm_calls":2,"tool_calls":1}`；脚本服务第 2 次请求确认 `role=tool` 消息含 `42` 且 `tool_call_id=call_smoke_1` 回填，否则脚本返回 500 —— 说明真实 `builtin` calculator 执行结果进入了下一轮模型请求 |
| 二进制跨请求记忆 | PASS | 同 Session 第二次 `POST /v1/chat` → 200 `"reply":"上一轮的结果是 42"`；脚本服务第 3 次请求消息角色为 `user, assistant, tool, assistant, user`，并命中首轮 user 原文与首轮 assistant 回复，否则脚本 500 |
| 运行事件（`runlog`） | PASS | 二进制 stdout 的 `run_finished` 事件：两轮 `status=COMPLETED`；先前失败注入响应中 `status=FAILED, error_kind=provider_failure, op=agent.Loop.loop` —— 终态与 HTTP 结果一致 |
| 零真实 Token / 零外网（AC-005） | PASS | 环境无 `MINI_AGENT_LLM_*`；`e2e/*.go` 无非 loopback `http(s)` 字面量、无真实 Provider 域名与密钥、无 `t.Skip` / `testing.Short(` / build tag（`offline_test.go` 内的禁用词表为拼接常量，不构成真实地址）；运行期脚本服务请求 `Host=127.0.0.1:38080`、`Authorization=""`、请求次数与脚本步数一致（3/3） |
| 文件所有权（AC-008） | PASS | `git ls-files 'e2e/*'` 6 项与 Design §12（`runtime-architecture.md:362-367`）6 项双向 `diff` 无差异；`git diff --name-status origin/develop..c99ace9` 仅 `e2e/` 6 文件 + `docs/design/runtime-architecture.md` + 本任务 Artifact，无生产包改动 |
| 断言强度抽查 | PASS | `e2e/harness_test.go:687` `assertSessionUnchanged` 用 `reflect.DeepEqual` 全字段比较 + `core.ValidateMessages`；`:710` `assertMessageRoles` 校验角色序列与历史合法性；`:810` `assertTerminal` 交叉校验 `RunOutcome` 与 `run_finished`；核心用例断言 reply/计数/工具内容/wire 请求，而非仅 HTTP 200 |

## Acceptance Evidence

- AC-001 → `TestMultiToolChainCommitsFullTurn`、`TestSingleRoundMultipleToolCalls` 本次 PASS；二进制冒烟独立复现同形链路（两轮模型调用 + 一次真实 calculator 执行 + 终稿 200）。
- AC-002 → `TestSessionMemoryAndCrossSessionIsolation` 本次 PASS；二进制冒烟证实同 Session 第二轮 wire 含首轮 user/assistant/tool 内容。
- AC-003 → `TestRecoverableToolFailuresContinue`（除零/未知工具/参数非法回填后仍 COMPLETED）、`TestRequestTimeout` / `TestClientDisconnect`（`CANCELLED` + 无半轮）本次 PASS。
- AC-004 → `TestProviderFailures`（4xx/5xx → 502 `provider_failure` + `FAILED`）、`TestProviderUnresponsive`（Provider 侧超时 → 502 `provider_failure` + `FAILED`）、`TestRequestTimeout`（504 `deadline_exceeded` + `CANCELLED`）、`TestClientDisconnect`（`cancelled`，状态码按设计不承诺）、`TestBudgetLimits`（429 `llm_call_limit` / `tool_call_limit` + `LIMIT_EXCEEDED`）本次 PASS，七行矩阵互斥；每例交叉校验 `run_finished` 与无半轮提交。
- AC-005 → `TestNoRealProviderOrCredential` 本次 PASS + 上述源码/运行期机械核对 + 二进制冒烟 `Authorization` 为空、全部 loopback。
- AC-006 → 构建、vet、全量测试、`-race`（含 `-count=3`）、`GOMAXPROCS=1` 重复运行全部 exit 0 且无 race 告警；`e2e/` 无 `t.Skip` / build tag / 外网依赖。
- AC-007 → 交付记录逐 AC 映射见 `task.md` §Verification 与 `contract.md` §Verification Requirements；本次交付证据为本文件；缺口在 `Not Executed` 与本文件 `Remaining Risks` 中逐项写明，未标为 PASS。
- AC-008 → Design §12 六行与 `git ls-files` 双向一致；`docs/design/runtime-architecture.md` 相对 `origin/develop` 只有 §12 登记 + 仅测试包边界说明 + §14 一条缺口，§7 / §11 未改。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 真实 LLM Provider 联调与真实 Token 消耗 | Task Out of Scope；本里程碑要求零真实 Token | 真实 Provider 的限流、超时、错误体差异未被覆盖；wire 层只按 OpenAI 兼容脚本验证 |
| HTTP 层触发 Todo 工具端到端 | v1 装配不注册 Todo 工具（`cmd/mini-agent/main.go` 仅 `calculator`），归服务装配任务 T08 | 跨会话 Todo 隔离仅有 Session 快照级证据，不代表 Todo 端口适配已可用 |
| HTTP 层上下文压缩 | `service.Config.validate` 拒绝 `TokenBudget != 0`，v1 不可达 | 压缩仅由 `agent` / `contextmgr` 层既有测试覆盖，未经 HTTP 全链路验证 |
| Session 持久化 / 进程重启恢复 / 多进程部署 | Task Out of Scope（v1 为进程内内存 Session） | 重启后历史与 Run 终态不可恢复，属既定 v1 边界 |
| Docker 镜像、压测与容量评估 | Task Out of Scope；本里程碑无运行环境交付物 | 生产部署形态与吞吐/延迟特性未知 |
| `-race` 纳入 CI | 共享工程文件（`Makefile` / `.cnb.yml`）改动需 Owner 授权，本任务显式排除 | 回归只靠本地 `-race`，CI 仍只跑 `make test`，race 缺陷可能晚发现 |

## Remaining Risks

- Provider 自身超时经 `agent` 重分类为 502 `provider_failure` + `FAILED`，而请求级超时为 504 `deadline_exceeded` + `CANCELLED`；该组合未写入 Design §7，本任务按实际行为断言并在契约 Open Risks 记录，若需固化应另开设计任务。
- `contract.md` 的 AC-001 验证表行把 Session 角色序列写成 5 条（`user,assistant,tool,tool,assistant`），与 AC 正文「连续两轮 `tool_calls`」及实现（6 条）不一致；已按 AC 正文验收，措辞修正需 Contract Revision。
- `e2e/` 装配的 registry 为 `calculator` + `mock_search`，与 v1 生产 registry（仅 `calculator`）不同，证据强度覆盖 Runtime 能力，不等于生产注册集合。
- `e2e/` 六文件由 `e0b4021` 与另一任务 Artifact 同 commit 引入（历史不可改写）；验收按树内容判定归属，未按 commit message。
- 时间敏感用例依赖真实计时（`RequestTimeout=200ms`、Provider `Timeout=50ms`）；本次在 `GOMAXPROCS=1` 与 `-count=10` 下稳定，但极端调度下仍有抖动可能。

## Result

PASS
