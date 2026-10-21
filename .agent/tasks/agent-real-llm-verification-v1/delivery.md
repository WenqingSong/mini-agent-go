# Delivery Verification

## Milestone and Target

- Milestone：`real-provider-acceptance-v1`（真实 Provider 端到端验收与最终交付收尾）
- Delivery Target：`feature_head = 2d947b4c273fd30461588d749dc3eb3f531e2ae4`（`feat/agent-real-llm-verification-v1`，验证时 local HEAD == `origin/feat/agent-real-llm-verification-v1`）
- 代码/制品标识：无生产代码改动，交付物是本任务目录的 `evidence.md` + 本报告；验证用镜像 `mini-agent-go:local` = `sha256:d191b2a8ee0706fcad30a99380cc71a780fc2bc740e04d8d5ebe3df6f056d95d`（由当前源码 `docker compose build --no-cache` 重建）
- Cleaner Review Target：`aad9dd4518c90445b32812f5f7174612790598a0`
- Owner 接受 Target：`aad9dd4518c90445b32812f5f7174612790598a0`（`ACCEPTED`）
- Develop Base：`f417327443afd381d87de1168980cb9c0b22911f`（`git rev-parse origin/develop`）
- Target Match：YES（`review.target` 是 `feature_head` 的祖先；`git diff f417327 2d947b4 --name-status` 仅新增任务目录 7 个 Artifact，无生产代码/测试/构建配置改动）
- 入口 Gate：`.agent/bin/workflow-check gate delivery-start .agent/tasks/agent-real-llm-verification-v1` → `PASS`

## Environment

| 项 | 值 |
| --- | --- |
| OS / Kernel | Debian GNU/Linux 12 (bookworm) / Linux 5.4.241-1.tlinux4-0025.13 x86_64 |
| Go | `go1.24.1 linux/amd64`（构建阶段 `golang:1.24`，运行阶段 `alpine:3.21`） |
| Docker / Compose | `29.6.2-7` / `v5.3.1`（Compose v2 语法） |
| 外部依赖 | 无 MySQL / Redis / Kafka / K8s：v1 只有进程内存 Session 与一个外部 Provider |
| Provider / Model | 腾讯云 TokenHub / `deepseek/deepseek-flash` |
| BaseURL（非密钥） | `https://tokenhub.tencentmaas.com/v1`，按 `llm/openai/client.go:89-92` 固定追加 `/chat/completions` → 实际端点 `https://tokenhub.tencentmaas.com/v1/chat/completions` |
| 配置来源 | 本地 `.env`（`compose.yaml` 的 `env_file`）；`MINI_AGENT_LISTEN_ADDR` 由 `compose.yaml:15` 注入 `0.0.0.0:8080`。本报告不记录任何 Secret 值 |
| 认证方式 | `MINI_AGENT_AUTH_TOKEN` 以 Bearer 头传入；命令中以 `$(grep ... .env)` 在 shell 内取值，未回显 |
| 测试数据 | 本次新建会话 `d17c18a3382437615098042742e954be`，仅 2 次 `/v1/chat`；未写任何持久化存储 |
| 隔离与清理 | 交付验证前执行 `make down` 并 `docker compose build --no-cache` 重建镜像，容器与镜像为本次新建；容器仍在运行以便复核，`make down` 可清理 |
| 与 Task 环境一致性 | 一致：Task 指定 `make check` / `make up` / Docker Compose 本机环境与真实 TokenHub；本次未换用 Mock 或更易通过的路径 |

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（离线全量） | PASS | `make ci` exit 0：`go build ./...` / `go vet ./...` / `go test -count=1 ./...` / `go test -race ./...`，13 包全 `ok`；另跑 `go test -race -count=1 ./...`（避开缓存）exit 0，13 包全 `ok` |
| Engine 独立 Module | PASS | `GOWORK=off go -C .agent/engine test -count=1 ./...` exit 0（`cmd/workflow-check`、`internal/workflow` ok） |
| 配置前置检查 | PASS | `make check` exit 0，输出「✓ 配置与环境检查通过（未发起任何模型请求）」 |
| 镜像构建 | PASS | `docker compose build --no-cache` exit 0，镜像 ID `sha256:d191b2a8...56d95d`；`docker run --rm --entrypoint /bin/sh mini-agent-go:local -c 'test ! -e /app/.env'` exit 0，`ls -a /app` 只有 `mini-agent` |
| 服务启动与健康 | PASS | `make up` exit 0（`. ✓`）；`docker inspect` → `running health=healthy restarts=0`；`GET /healthz` → `http=200`，body `{"status":"ok"}` |
| 认证拒绝/放行 | PASS | 无 Token `POST /v1/sessions` → `401` `{"error":{"code":"unauthorized","message":"未通过认证"}}`；错误 Token → 同样 `401`；正确 Token → `201` `{"session_id":"d17c18a3382437615098042742e954be"}` |
| 拒绝请求零上游调用 | PASS | 上述拒绝请求前后 `docker compose logs` 中 `"llm_request"` 0 → 0、`"run_started"` 0 → 0 |
| 真实普通对话（真实 Provider） | PASS | `POST /v1/chat` → `http=200 time=1.245s`，`status=COMPLETED`、`reply="你好！有什么我可以帮你的吗？"`、`llm_calls=1`、`tool_calls=0`；runlog 4 事件成套（见 AC-003） |
| 真实 Calculator Tool Calling | PASS | 同一会话 `POST /v1/chat`（37×48）→ `http=200 time=2.562s`，`status=COMPLETED`、`reply="37 × 48 = 1776"`、`llm_calls=2`、`tool_calls=1`；事件序列逐条吻合（见 AC-004） |
| Session 提交 | PASS | 同会话 `message_count` 跨 Run 递增 1 → 3 → 5（语义见 `agent/events.go:33-34`：不含 System Prompt；提交在 COMPLETED 分支 `agent/run.go:212-225`），证明上一轮对话与工具整轮已提交 |
| 运行日志无凭据 | PASS | 以 `.env` 实际值做计数比对（只输出命中数）：`api_key_hits_in_logs=0`、`auth_token_hits_in_logs=0`、`Bearer` 命中 0 |
| 离线错误语义 | PASS | `go test ./llm/... ./httpapi/... ./agent/... ./e2e/... -count=1 -run 'Provider|Timeout|Cancel|Failure|Limit|Disconnect|ErrorSemantics|ContextFailures'` exit 0；覆盖 `TestCompleteErrorSemantics`、`TestCompleteContextFailures`、`TestProviderFailureClassificationEndToEnd`、`TestRequestTimeoutReturns504`、`TestClientDisconnectCancelsRun`、`TestShutdownCancelsInflightRunAfterGrace`、`TestChatFailureHasNoSideEffects`、`TestProviderFailures`、`TestProviderUnresponsive`、`TestBudgetLimits` 等 |
| 交付材料无未提交依赖 | PASS | 验证期间 `git status --short` 为空；镜像由 `--no-cache` 从当前 HEAD 源码重建，不依赖未提交文件 |
| 工作区/历史密钥 | PASS | `git ls-files | grep -x .env` 无输出；`git check-ignore -v .env` → `.gitignore:2`；密钥模式 `git grep` 仅命中 `runlog/sanitize_test.go:105-106` 的脱敏器夹具（非真实凭据） |

## Acceptance Evidence

- **AC-001（基线/端点/密钥）** → `make check` exit 0 且无模型请求；`.env` 未跟踪且不在镜像内；运行时 `BASE_URL=https://tokenhub.tencentmaas.com/v1`、`MODEL=deepseek/deepseek-flash`，按 `client.go:89-92` 得 `https://tokenhub.tencentmaas.com/v1/chat/completions`，并由本次两次 200 真实响应反向证实（错误路径会 404）。**偏差**：`.env.example` 11 键 vs `cmd/mini-agent/main.go` 12 个 `MINI_AGENT_*` 常量，差集恰为 `MINI_AGENT_LISTEN_ADDR`（由 `compose.yaml:15` 注入，非 `.env` 模板项），CLEAN-002 已登记、Owner 未调整 AC 措辞，本报告不判定为 FAIL 但如实保留为未达成项。`make init` 对既有 `.env` 不覆盖（`Makefile:27-32`）。
- **AC-002（启动/健康/认证）** → 容器 `healthy`、`/healthz` 200 `{"status":"ok"}`、正确 Token 201、缺失/错误 Token 均 401 且被拒请求未产生任何上游调用（`llm_request` 0→0）。
- **AC-003（真实普通对话）** → `http=200`、`COMPLETED`、中文 reply、`tool_calls=0`、`llm_calls=1`；runlog（`run_id=2d4da25fb82f5f1835dacc14b895bfd2`）：
  - `run_started(model=deepseek/deepseek-flash)` → `llm_request(step=1, message_count=1, compacted=false)` → `llm_response(finish_reason=stop, tool_call_count=0, input_tokens=405, output_tokens=9)` → `run_finished(COMPLETED)`
  - Run ID 与 HTTP `run_id` 一致；同 Run `llm_request` 次数 1 == `llm_calls=1`，无重复调用。
- **AC-004（真实 Tool Calling）** → `http=200`、`COMPLETED`、reply 含 `1776`、`tool_calls=1`、`llm_calls=2`；runlog（`run_id=8046f37241961ba2eca4d10a36e4a983`）按序：
  - `llm_response(finish_reason=tool_calls, tool_call_count=1, input_tokens=437, output_tokens=69)`
  - `tool_started(tool_name=calculator, index=0, call_id=call_00_ET_8oqzn19169VYB9o2zZdj5817)`
  - `tool_finished(tool_name=calculator, index=0, is_error=false, call_id=call_00_ET_8oqzn19169VYB9o2zZdj5817)`
  - `llm_request(step=2, message_count=5)` → `llm_response(finish_reason=stop, input_tokens=528, output_tokens=9)` → `run_finished(COMPLETED)`
  - 两条工具事件 `call_id` 相同；`message_count` 3→5 与追加 assistant 工具调用消息 + tool 结果一致。
- **AC-005（来源证据，Deliverer 独立核对四段）** →（1）`finish_reason=tool_calls` + `tool_call_count=1` 来自 Provider 响应解析（`tool_call_count` 取 `len(resp.Message.ToolCalls)`，`resp` 解码自 `choices[0].message.tool_calls`）；（2）同一 `call_00_...` 的 `tool_started`/`tool_finished`，`call_id` 格式由 Provider 生成、应用仅透传；（3）`builtin/calculator.go:14` 参数协议仅 `op`(enum add/sub/mul/div)/`a`/`b` 且全 required，只有 `mul,37,48` 得 `1776`；`git grep -n '1776' -- '*.go'` 零命中，非硬编码/预构造；（4）reply 含 `1776`，整轮 `finish_reason` 依次 `tool_calls` → `stop`。另核对：非测试代码中按消息文本选工具的分支零命中（`strings.Contains|HasPrefix(..., msg|message|content|text)` 无结果）、v1 registry 仅注册 `calculator`。
- **AC-006（错误传播）** → 离线错误语义本轮独立复跑全部 PASS（用例见 Verification）。真实负向请求为 Owner 授权「最多 1 次」，该 1 次已由 Coder 在 `evidence.md` §10 消耗（HTTP 502 `provider_failure`，`run_finished.error_kind` 同种类），Deliverer 未重复执行以避免超出授权次数；本次以「`.env`/运行中配置均为 `deepseek/deepseek-flash`（已恢复）」+ 离线 fail-closed 用例 + `mapFinishReason` 白名单机制交叉确认。该项在本次运行中属 **未重跑**，见 Not Executed。
- **AC-007（预算与账本，本次 Deliverer 自身消耗）** →

  | # | 用途 | 上游请求 | 结果 | input | output |
  | --- | --- | --- | --- | --- | --- |
  | 1 | AC-003 普通对话 | 1 | `finish_reason=stop`，200 | 405 | 9 |
  | 2 | AC-004 工具第 1 步 | 1 | `finish_reason=tool_calls` | 437 | 69 |
  | 3 | AC-004 工具第 2 步 | 1 | `finish_reason=stop` | 528 | 9 |
  | 合计 | | **3** | | **1370** | **87** |

  Total = 1370 + 87 = **1457 tokens**。计数方式：`docker compose logs | grep -c '"event":"llm_request"'` = 3；token 按同 Run `llm_response` 的 `input_tokens`/`output_tokens` 求和。与 Coder 轮（4 次、1370+92=1470）合计本任务共 **7 次上游请求、2927 tokens**，仍在 Owner「不设固定上限、仅验收必需」授权内，无重试、无循环、无异常连续调用。费用金额未获取 Provider 计费口径，不做金额结论，以 TokenHub 控制台实际账单为准。
- **AC-008（离线回归与安全）** → `make ci` / `go test -race -count=1 ./...` / `go test ./e2e -count=1`（含在 `./...` 内）/ Engine 测试全 exit 0；`git diff f417327 2d947b4` 只新增任务目录 7 个 Artifact，无生产代码/测试改动；`.env` 未跟踪、镜像内无 `.env`；密钥扫描仅命中脱敏器测试夹具。
- **AC-009（最终报告）** → 本文件即汇总（分支与 SHA、构建/测试/Race/Docker 健康、Provider 与 Model、普通对话、Tool Calling、Run 终态与 Session 提交、调用与 token 汇总、费用口径、错误处理、安全检查、已知限制、结论）。结论与证据一致：普通对话与真实模型自主发起的 Tool Calling 均由本次运行取证，故不因「仅普通对话」或缺工具来源而降级。

## 已知限制（按当前装配）

- Todo 未接入 HTTP 服务（`session.TodoOperations` 端口适配属 T08），v1 registry 只有 `calculator`。
- `TokenBudget=0`（`service/runner.go` 拒绝非 0），**不做上下文压缩**；`compacted=false` 在两次运行中均出现。
- Session 仅进程内存，无持久化；容器重建即丢失（本任务 Coder 轮负向测试重建过容器，会话未跨重启验证）。
- 仅 `GET /healthz`、`POST /v1/sessions`、`POST /v1/chat` 三个端点，**无会话读取端点**；Session 提交结论只能由日志 `message_count` 递增与 COMPLETED 提交路径推断，无法直接读取会话内容。
- 无 Provider 重试/限流；429 与 5xx 不重发；无流式输出（不发送 `stream`）。
- `-race` 不在 CNB 流水线内（`make ci` 只含 `go test -race ./...`，本地执行）。
- CNB 镜像发布只在 `main` push 触发；本任务在 feature 分支，无平台发布证据。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 真实 Provider 负向请求（无效 Model → 502） | Owner 授权「最多 1 次」已由 Coder 消耗；Deliverer 重跑将超授权 | 低：失败链路机制由本轮离线复跑的 fail-closed 用例 + 运行中已恢复的 Model 配置 + Coder 的 502 记录共同支撑；但无 Deliverer 一手 5xx/4xx 真实证据 |
| 会话内容直接核对（Session 读取） | 服务无会话读取端点（v1 范围） | 低：以 `message_count` 递增（1→3→5）与 COMPLETED 提交代码路径替代 |
| 重启/恢复、并发或性能压测 | Task 明确 Out of Scope，且 Contract/Task 未要求 | 无：超出本里程碑要求 |
| CNB 镜像发布与生产部署 | 需 `main` push 与 Owner 授权，本任务不改 `main` | 中：发布链路本次无平台证据（Task 已标注待验证） |

## Remaining Risks

- CLEAN-001（P3，OPEN）：真实调用原始 runlog 未归档，仅有 `evidence.md` 摘录；本轮 Deliverer 证据同样来自运行中容器日志的即时抽取，随容器删除不可再取。
- CLEAN-002（P3，OPEN）：`.env.example` 缺 `MINI_AGENT_LISTEN_ADDR`，AC-001「变量名集合一致」字面未达成（无运行时影响）。
- CLEAN-003（P3，OPEN）：Coder 在首次真实请求前修改了 Owner 本地 `.env` 的 `MINI_AGENT_LLM_BASE_URL` 一行；OwnerGate 未单独追认，按事实保留。
- 本次交付验证额外消耗 3 次上游请求 / 1457 tokens（含在「仅验收必需」授权口径内），需 Owner 在其计费与预算记录中一并计入。
- 无费用金额结论：未取得 Provider 计费接口/单价，无法核对金额。
- 交付物为报告，无生产代码改动；集成后行为取决于 `f417327` 基线与既有实现，不引入新风险面。

## Result

PASS
