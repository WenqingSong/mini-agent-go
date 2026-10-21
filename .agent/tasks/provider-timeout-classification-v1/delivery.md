# Delivery Verification

## Milestone and Target

- Milestone：`provider-timeout-classification-v1` 里程碑交付验收（Provider 自身超时与请求级超时的错误分类一致性）
- Delivery Target：feature `fix/provider-timeout-classification-v1` @ `c5960015262751e388852088f6927da4709090a7`
  - 其中实现提交为 `43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`（`fix(httpapi): 让 Provider 自身超时统一映射为 provider_failure`）；`43ce197..c596001` 的 3 个提交只改本任务的 `.agent/tasks/provider-timeout-classification-v1/*`（findings / core-logic / owner-decision / state），生产与测试代码零变化（`git diff --name-only 43ce197 c596001` 已核对）。
- Cleaner Review Target：`43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`
- Target Match：YES（`delivery.review_target == review.target == owner.review_target`）

## Environment

- OS / 运行时：Linux x86_64，Go 1.24.1 linux/amd64（race detector 可用），GNU Make（`/usr/bin/make`）。
- 依赖：根模块零第三方依赖（`go.mod` 只有 `module` + `go 1.24`）；本任务不涉及 MySQL / Redis / Kafka / Docker / 外网。
- 集成基线：`develop_base = 0977f243df0348fa5e526b8d954e3f9a2720237d`（= 验证时的 `origin/develop`）。`git merge-base --is-ancestor origin/develop feature_head` 为真，即 `develop_base + feature_head` 是快进集成，集成结果树等于 `feature_head` 树。
- 验证工作区：`git archive c596001` 解到 `/tmp/deliver-verify`（无 `.git`、无未跟踪文件、无本机改动），构建、测试与运行二进制全部来自该副本；未依赖工作区残留文件或绝对路径。
- 运行期依赖：本机回环假上游 `127.0.0.1:18081`（OpenAI 兼容 `/chat/completions`，可控 3s 延迟），`127.0.0.1:18080` / `:18082` 启动真实 `mini-agent` 二进制；全程无外网、无付费模型。
- 配置来源：仅环境变量（`MINI_AGENT_LISTEN_ADDR` / `MINI_AGENT_AUTH_TOKEN` / `MINI_AGENT_LLM_BASE_URL` / `MINI_AGENT_MODEL` / `MINI_AGENT_REQUEST_TIMEOUT`）；`AUTH_TOKEN` 使用一次性占位值，不记录真实 Secret。
- 数据隔离：Session 为进程内 `session.Manager`，每个场景使用独立进程与独立 `session_id`，验证后进程退出、内存数据随之销毁，未触碰任何真实数据。
- 清理：临时副本 `/tmp/deliver-verify`、假上游目录及其二进制已删除；假上游与 agent 进程已全部退出；主仓库 `git status --short --untracked-files=all` 为空。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `/tmp/deliver-verify`：`go build ./...` 退出码 0；`go build -o bin/mini-agent ./cmd/mini-agent` 产出 10.3MB 可执行文件 |
| Static check | PASS | `go vet ./...` 退出码 0 |
| Unit / Integration | PASS | `go test -count=1 ./...` → 12 个包全部 ok（`httpapi` 1.784s） |
| Race | PASS | `go test -race -count=1 ./...` → 12 个包全部 ok，无 data race 报告 |
| CI 入口 | PASS | `make test` 退出码 0（等价 `go test ./...`，`.cnb.yml` pr-check 入口） |
| 关键用例稳定性 | PASS | `go test -count=3 -run 'TestProviderFailureClassificationEndToEnd\|TestRequestTimeoutClassificationEndToEnd\|TestErrorCodeOf\|TestChatErrorMapping\|TestProviderFinishSemanticsMappingEndToEnd' ./httpapi/` 通过，未观察到毫秒级超时抖动 |
| 服务启动与配置加载 | PASS | 真实二进制按环境变量启动成功；`GET /healthz` → `200 {"status":"ok"}`；无凭证 `POST /v1/chat` → `401 unauthorized`；`POST /v1/sessions` → `201 {"session_id":"s-a"}`；`SIGTERM` 后 2s 内进程干净退出（无 panic） |
| Core Flow（正常主链路） | PASS | `/tmp` 真实进程：`/v1/sessions`(201) → `/v1/chat` → `200 {"status":"COMPLETED","reply":"pong","llm_calls":1}`，`run_finished` 为 `COMPLETED`；同会话第二轮上游收到 `messages=4`（system + user + assistant + user），证明上一轮已真实提交到 Session |
| Data（失败轮原子性，进程级） | PASS | 请求级超时后的下一轮，上游收到 `messages=3`，即 "slow turn" 用户消息与半轮 assistant 消息均未进入历史 |
| Provider 非 2xx 映射（运行期） | PASS | 上游路径不匹配返回 404 时：HTTP `502 provider_failure`，`run_finished` 为 `FAILED` / `error_kind=provider_failure`（三处一致） |
| 请求级超时（运行期） | PASS | `MINI_AGENT_REQUEST_TIMEOUT=400ms` + 上游 3s 未响应：HTTP `504 {"code":"deadline_exceeded"}`，`run_finished` 为 `status=CANCELLED, error_kind=deadline_exceeded`（`duration_ms=400`） |
| AC-001 三观测点（进程内真实装配，独立探针） | PASS | Deliverer 自写探针（真实 `openai.Client` `Timeout=60ms` + 800ms 慢 `httptest` 上游 + 真实 `session.Manager` + 真实 `service.Runner` + 真实 `agent.Loop` + `httptest` 服务）：HTTP `502` + `error.code=provider_failure`；`run_finished` `FAILED`/`provider_failure`；直接 `Runner.Run` 得 `FAILED` + `KindOf(Err)=provider_failure`，且 `errors.Is(Err, context.DeadlineExceeded)==true`（证明该构造链中确有 ctx 哨兵，探针非空转）；会话 `Messages`/`Summary`/`Todos` 与基线逐字段相等 |
| AC-002（进程内，独立探针） | PASS | Provider 阻塞至 ctx 到期：HTTP `504 deadline_exceeded`、`run_finished` `CANCELLED`/`deadline_exceeded`、会话基线逐字段不变 |
| AC-003 分类优先级（独立探针，10 例） | PASS | 协议层输出：`provider_failure`+`Cause=DeadlineExceeded`/`Canceled` → `502`；`deadline_exceeded`+`Cause=DeadlineExceeded` → `504`；`cancelled`+`Cause=DeadlineExceeded` → `408`；会话哨兵包在 `RunError` 内 → `404`；裸 `DeadlineExceeded`/`Canceled`（无 RunError）→ `504`/`408`；非终态 / `COMPLETED` 带错误 / 负计数 → `500 internal` |
| 可逆 Mutation（独立判别力） | PASS | 在临时副本把 `errorCodeOf` 判定顺序临时交换回「ctx 哨兵先于 RunError.Kind」后，独立探针如期失败（`504` 而非 `502`、`408`/`504` 互相串味）；恢复后 `cmp` 一致并重新全绿，Mutation 未进入主仓库 |
| 交付边界（路径集合） | PASS | `git diff --name-status 0977f24 43ce197`：生产仅 `httpapi/errors.go`；测试 `httpapi/errors_test.go`、`httpapi/fake_test.go`、`httpapi/handler_test.go`；Design `docs/design/http-api.md`（Analyst 提交 `aa2f6e2`）；任务 Artifact `.agent/tasks/provider-timeout-classification-v1/*`。`agent/**`、`llm/**`、`core/**`、`session/**`、`service/**`、`cmd/**` 零变化 |
| Design §4 与实现一致性 | PASS | `docs/design/http-api.md` §4 的判定优先级（会话哨兵 → 最外层 `*core.RunError.Kind` → 裸 ctx 兜底 → `internal`）与三条事实，同 `httpapi/errors.go:167-214` 实现及本次探针实测结果一致；映射表与错误码枚举未变 |

## Acceptance Evidence

- AC-001（Provider 自身超时三观测点一致）：独立探针 `TestDelivererProbeProviderTimeoutTriObservation` —— `502 provider_failure` / `run_finished=FAILED+provider_failure` / `RunOutcome=FAILED+KindProviderFailure`，且链中 `errors.Is(..., context.DeadlineExceeded)` 为真；同一构造在修复前会被 ctx 哨兵翻转为 `504`（Mutation 已复现该翻转）。
- AC-002（请求级超时统一为 deadline_exceeded）：真实进程 `MINI_AGENT_REQUEST_TIMEOUT=400ms` → `504 deadline_exceeded` + `run_finished=CANCELLED/deadline_exceeded`；独立探针（Provider 已观察到 ctx 到期）复现同一结论。
- AC-003（优先级可判定、不混搭）：独立探针 10 例覆盖「结论优先」「Cause 不得翻转」「会话哨兵最高」「矛盾结论一律非 2xx」；运行期另验证 Provider 404 → `502 provider_failure`。
- AC-004（既有映射不回归）：`go test ./...` 与 `-race` 全绿（含 `TestErrorCodeOf`、`TestChatErrorMapping`、`TestClientDisconnectCancelsRun`、会话哨兵、`checkedCode` 矛盾拒绝、工具失败/重复 `CallID`、预算耗尽、`finish_reason` 异常）；运行期验证 `401 unauthorized`、`404 session_not_found`、`502 provider_failure`。
- AC-005（Session 原子性）：探针逐字段比对（`Messages` 逐条、`Summary`、`Todos`）相等；进程级用上游收到的 `messages` 计数独立佐证失败轮未提交（超时轮后仍为 3 条）。
- AC-006（测试有效性与可逆 Mutation）：Deliverer 自建探针 + 自建 Mutation，证明当前实现与修复前实现可被明确区分；Coder/Cleaner 的 Mutation 记录（含 4 组）作为输入证据，不作为本次结论依据。
- AC-007（交付边界）：`review.target=43ce197` 的完整路径集合落在 APPROVED Contract 的 Allowed 清单内；`checkedCode` 与错误码枚举、状态码映射表零变化；Design §4 只新增优先级列表与三条事实。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| Provider 自身超时在「真实 OS 进程 + 网络超时」层面的复现 | Owner D4 冻结 `cmd/mini-agent` 装配，生产路径不设置 Provider `Timeout` 也不注入 `HTTPClient`，进程级无合法途径触发该形状 | 低：分类规则已在「进程内真实装配」（真实 `openai.Client` + 真实 `agent.Loop` + `service.Runner` + `httptest` 服务）验证；默认装配下的可达性属后续独立任务，Contract `Open Risks` 已记录 |
| `.agent/engine` 测试重跑 | 本次交付路径集合不含 `.agent/engine`，Engine 是独立 Module，与本任务无交集 | 无 |
| 重启恢复 / 回滚 / 性能 / 并发压测 | Task 与 Contract 均未要求（无持久化、无外部存储、无性能指标） | 无；Session 为进程内存，重启后不保留属既有设计 |

## Remaining Risks

- 默认生产装配未设 Provider `Timeout`（Owner D4），因此「Provider 自身超时且错误链含 ctx 哨兵」在默认配置下的触发途径有限；本次修复纠正的是分类规则，不改变可达性。
- 若直接错误路径收到链中不含 `*core.RunError` 的裸 `*url.Error`，其 ctx 哨兵仍按兜底映射为 `504`；该形状在当前装配不产生，Contract `Open Risks` 已显式接受。
- `KindInvalidArgument` 携带 ctx `Cause` 时由 `504` 变为 `internal`，是 Owner D1「结论优先」的直接推论，现有用例已固化。

## Result

PASS
