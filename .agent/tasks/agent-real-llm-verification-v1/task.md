# Task: 真实大模型端到端验收与最终交付收尾（agent-real-llm-verification-v1）

## Goal

通过 mini-agent-go 自身 HTTP 接口调用真实 OpenAI 兼容 Provider（腾讯云 TokenHub），在 Owner 批准的上游调用预算内，证明「普通对话」与「由真实模型自主发起的 Calculator Tool Calling」两条主链路可用，并交付包含交付状态、验收证据、安全检查、已知限制与最终结论的最终报告；全过程零密钥泄漏、零超预算调用。

## Scope

- 离线前置核验（不发出任何真实模型请求）：
  - 复核前置交付状态：`agent-http-api-v1`、`agent-runtime-e2e-tests-v1`、`provider-timeout-classification-v1` 的 `state.yaml`，以及 DevOps 提交 `f417327`（CI/镜像发布与容器化启动配置）是否已在 `origin/develop`。
  - 复核 `make init` / `make check` / `make up` / `Dockerfile` / `compose.yaml` 的实际行为，以及 `.env.example` 与应用实际读取的环境变量是否一致。
  - 复核 `llm/openai.Client` 的端点拼接、认证头、请求体字段、响应解析容错与 `finish_reason` 白名单。
  - 复核 `cmd/mini-agent` 装配：仅注册 `calculator`、`TokenBudget=0`、模型/工具预算环境变量、runlog 输出位置。
- 真实 Provider 配置核验（由 Owner 填写本地 `.env`）：确认应用实际读取的 BaseURL 与 Model 与 Owner 指定一致；`.env` 未被跟踪；密钥不进镜像、不进日志、不进任务记录与报告。
- 容器化启动与健康核验：`make check`、`make up`、容器健康状态、`/healthz`、未授权请求拒绝且不触发上游调用、日志无敏感信息。
- 真实普通对话验收（AC-003）。
- 真实 Calculator Tool Calling 验收与来源证据（AC-004 / AC-005）。
- 错误传播验收（AC-006）：离线错误语义回归必做；真实负向请求仅在 Owner 明确授权时执行一次。
- 调用量与费用记账（AC-007），计数口径为上游 LLM 请求次数。
- 离线全量回归（AC-008）：build / vet / test / test -race / 既有 `e2e` / `.agent/engine` 独立 Module 测试。
- 最终交付报告（AC-009）：Coder 维护的可核查原始记录 `evidence.md` + Deliverer 汇总的最终报告 `delivery.md`，均位于 `.agent/tasks/agent-real-llm-verification-v1/`。
- 兼容性缺陷的最小修复：仅当真实调用暴露项目实现缺陷、且 Owner 明确批准修复范围时，允许最小生产代码改动，并按既有 Workflow 经 Cleaner 复审。

## Out of Scope

- 新增业务功能、HTTP 端点、字段、错误码或环境变量；重构 Agent Runtime 或 `llm` / `session` / `contextmgr` / `runlog` / `tool` 公共符号。
- 新增数据库、Redis、Kafka、Kubernetes、Web 前端、第三方 Agent 框架或第三方 Go 依赖。
- Fork 专项建设；修改 `.agent/engine`、`.agent/workflow.yaml`、`.agent/registry/*`、`docs/design/*`（除 Owner 明确批准的 Contract Revision / Design 变更）。
- 为验证 Todo 或上下文压缩而扩展 v1 装配（`session.TodoOperations` 适配、生产 `Summarizer`、`TokenBudget != 0`）：v1 边界不变，Todo 与压缩只能按「当前未接入」如实记录。
- 把真实 Provider 调用固化进自动化测试或 CI：任何 `go test` / `make *` 路径都不得消耗 Token；不新增依赖真实网络的用例。
- 性能压测、并发压测、流式输出、Provider 重试/限流策略、模型能力评测。
- 无限次真实重试、未授权的 Provider 或付费模型切换、用 `-k` 或关闭 TLS 校验绕过 HTTPS 检查。
- 通过删除、篡改或故意泄漏 API Key 制造错误；主动触发真实 Provider 5xx。
- 由 Agent 创建/切换/重命名 feature branch，或 commit/merge/push 到 `main` / `develop`（分支生命周期与集成决定权归 Owner）。

## Milestone

Milestone: `real-provider-acceptance-v1`

本任务启用 Deliverer：Deliverer 在该里程碑下核对真实运行证据与最终报告，`delivery.md` 记录环境、逐 AC 结果、未验证项与剩余风险。

## Acceptance Criteria

- [ ] AC-001：给定交付基线（`origin/develop` = `f417327`）与 Owner 填好的本地 `.env`，当执行 `make check` 且不发起任何模型请求时，则检查通过；`.env` 仍未被 Git 跟踪（`git ls-files` 无 `.env`，`git status` 不含 `.env`）；应用实际请求端点等于 `https://tokenhub.tencentmaas.com/v1/chat/completions`（即 `MINI_AGENT_LLM_BASE_URL=https://tokenhub.tencentmaas.com/v1`，因为客户端固定追加 `/chat/completions`）；`.env.example` 的变量名与 `cmd/mini-agent` 读取的变量名一致；镜像内不含 `.env`（`docker run --rm --entrypoint /bin/sh <image> -c 'test ! -e /app/.env'` 成功）。
- [ ] AC-002：给定已执行 `make up`，当访问 `/healthz` 时则返回 200 且 body 为 `{"status":"ok"}`，容器健康状态为 healthy；当携带正确 Bearer Token 请求 `POST /v1/sessions` 时则返回 201 与会话 ID；当同一端点不带 Token 或带错误 Token 时则返回 401 `unauthorized`，且被拒请求没有产生任何上游模型请求（runlog 中 `llm_request` 事件计数不变）。
- [ ] AC-003（真实普通对话）：给定已创建的会话，当通过 `POST /v1/chat` 提交一句简单问候时，则 HTTP 200 且 `status=COMPLETED`、`reply` 为非空中文回答且与问候语义相符、`tool_calls=0`、`llm_calls>=1`；runlog 中出现成套的 `run_started → llm_request(model=<配置的模型 ID>) → llm_response(finish_reason=stop) → run_finished(COMPLETED)`，事件中的 Run ID 与 HTTP `run_id` 一致；`llm_calls` 与 runlog 中同 Run 的 `llm_request` 次数一致（无重复或多余调用）。
- [ ] AC-004（真实 Tool Calling，必选）：给定会话，当通过 `POST /v1/chat` 提交「请使用你可用的计算工具计算 37 × 48，并给出最终结果」时，则 HTTP 200 且 `status=COMPLETED`、`reply` 中出现 `1776`、`tool_calls=1`、`llm_calls>=2`；runlog 按顺序出现 `llm_response(finish_reason=tool_calls, tool_call_count=1)` → `tool_started(tool_name=calculator)` → `tool_finished(tool_name=calculator, is_error=false)`（两条工具事件 `CallID` 相同）→ 新的 `llm_request` → `llm_response(finish_reason=stop)` → `run_finished(COMPLETED)`。
- [ ] AC-005（工具调用来源证据，必选）：`evidence.md` 必须给出四段可核查的关联证据，证明工具调用来自 Provider 真实响应，而非应用关键字路由、测试预构造、硬编码回答或 Mock：(1) `llm_response` 事件的 `finish_reason=tool_calls` 与 `tool_call_count=1`（该值由 `llm/openai` 解析 Provider 响应的 `choices[0].message.tool_calls` 得到）；(2) 同一 `CallID` 的 `tool_started` / `tool_finished` 事件；(3) 计算器只接受 `op`/`a`/`b`，`1776` 与 37×48 唯一匹配，参数错误不会产生该结果；(4) 最终 `reply` 含 `1776`，且整轮 `finish_reason` 依次为 `tool_calls`、`stop`。任一段缺失即不得宣称本 AC 通过。若模型未选择调用工具，必须先排查工具定义、请求格式与模型能力；只有在 Owner 授权预算内才允许有限次重试，重试计入预算，不得伪造成功。
- [ ] AC-006（错误传播）：当未单独授权真实负向请求时，则以现有离线用例证明 Provider 4xx / 5xx、超时、取消的错误语义未回归（记录用例名与结果），并在报告中把「真实 Provider 负向响应」标注为 `NOT_VERIFIED`；当 Owner 明确授权一次真实负向请求时，则通过临时使用明显无效的 Model ID 触发，预期 HTTP 502 `provider_failure`，且 runlog 的 `run_finished` 与该错误种类一致、日志不含密钥，测试后恢复原 Model ID 并以 `make check` 与运行中配置核对证明已恢复（不要求为恢复再消耗一次真实调用）。两种情形都不得用离线测试冒充真实负向验收，也不得篡改或删除真实 API Key 制造错误。
- [ ] AC-007（预算合规）：`evidence.md` 逐条记录每次上游 LLM 请求的用途（普通对话 / 工具调用第 1 步 / 工具调用第 2 步 / 负向测试）、实际次数、结果，以及 Provider 返回的 token usage（runlog `llm_response` 的 `input_tokens` / `output_tokens`；缺失时注明缺失原因），并汇总 Prompt / Completion / Total；总请求次数与总 token 均不超过 Owner 批准上限；计数口径是上游 LLM 请求次数（一次 `/v1/chat` 可能触发多次），不是外部 HTTP 请求数；达到上限立即停止，未发生的调用不得写入记录；所有记录不含 API Key 或其片段。
- [ ] AC-008（离线回归与密钥安全）：当执行 `make ci`（`go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`go test -race ./...`）时全部退出码为 0；既有 `go test ./e2e -count=1` 全部通过；`GOWORK=off go -C .agent/engine test ./...` 通过；除经 Owner 批准的最小修复外无生产代码改动；仓库历史与工作区均无真实密钥（对密钥模式的 `git grep` 无命中，`.env` 未被跟踪），镜像内不含 `.env`。
- [ ] AC-009（最终报告）：`delivery.md` 与 `evidence.md` 共同构成最终报告，至少包含：最终分支与 commit SHA；构建 / 测试 / Race / Docker 健康检查结果；Provider 与 Model；普通对话结果；Calculator Tool Calling 结果；Run 终态与 Session 提交结论；上游调用次数与 token 汇总；费用或费用核对方式；错误处理结果；安全检查结论；已知限制；最终结论（PASS / BLOCKED / FAIL / PARTIAL 之一）。结论必须与证据一致：仅普通对话成功不得判 PASS；Calculator Tool Calling 未由真实模型发起不得判 PASS；缺少预算授权、凭据或环境时判 BLOCKED。已知限制须按当前装配如实列出（Todo 未接入 HTTP 服务、`TokenBudget=0` 不压缩、内存 Session 无持久化、CNB 发布无平台证据时标注待验证），不得把离线测试覆盖的能力写成真实部署已支持。

## Relevant Context

已核实事实（代码、配置与 Git，2026-10-21）：

- Git：当前分支 `develop`，HEAD = `f417327`（`ci(release): 补充镜像发布流水线与容器化启动配置`），与 `origin/develop` 一致；`git status --short` 为空；仓库内不存在本任务 feature branch（现有分支：`develop`、`main`、`feat/agent-*`、`fix/provider-timeout-classification-v1`）。
- 前置状态：13 个任务目录的 `state.yaml` 均为 `contract=APPROVED|NOT_REQUIRED` + `review=CLEAN` + `owner=ACCEPTED` + `delivery=PASS`；DevOps 提交 `f417327` 已在 `origin/develop`。
- 端点拼接：`llm/openai/client.go:89-92` 把 BaseURL 的 path 去掉结尾斜杠后固定拼 `/chat/completions`，因此 TokenHub 的 `MINI_AGENT_LLM_BASE_URL` 必须是 `https://tokenhub.tencentmaas.com/v1`，不能填完整请求地址。
- 认证：`llm/openai/client.go:145-148` 仅在 `APIKey` 非空时发送 `Authorization: Bearer <key>`；本服务对外凭证是独立配置 `MINI_AGENT_AUTH_TOKEN`（`httpapi/auth.go:17-27` 常量时间比较）。两者互不复用。
- 响应容错：`llm/openai/wire.go:257-340` 使用 `json.Unmarshal`，未知字段（如 `reasoning_content`）被忽略，`message.content` 允许为 `null`；但 `finish_reason` 只接受 `stop` / `tool_calls` / `length`（`wire.go:346-353`），其它取值一律 `provider_failure`；`tool_calls[].function.arguments` 必须是合法 JSON，空串同样视为协议错误。
- 请求体：仅发送 `model` / `messages` / `tools` 与可选 `max_tokens`、`temperature`（`wire.go:40-46`），不发送 `stream`；失败不自动重试（429 / 5xx 也不重发）。
- 装配：`cmd/mini-agent/main.go:170-182` 只注册 `calculator`（工具名 `calculator`，参数 `op`/`a`/`b`，见 `builtin/calculator.go:14`），并 `Seal()` 注册表；`TokenBudget` 固定 0（`main.go:236-237`），`service/runner.go:90-92` 拒绝非 0 取值；预算来自 `MINI_AGENT_MAX_LLMCALLS` / `MINI_AGENT_MAX_TOOLCALLS`，默认 8 / 8。
- HTTP 入口：只有 `GET /healthz`、`POST /v1/sessions`、`POST /v1/chat`（`httpapi/server.go:120-141`）；**没有会话读取端点**，Session 内容无法从 HTTP 观察；`/v1/chat` 响应字段为 `session_id` / `run_id` / `status` / `reply` / `llm_calls` / `tool_calls`（`httpapi/handler.go:26-33`）。
- 运行日志：`cmd/mini-agent` 用 `runlog.NewJSONSink(os.Stderr)`（`main.go:105`），事件含 `run_started`、`llm_request(model, message_count, compacted, dropped_messages)`、`llm_response(finish_reason, tool_call_count, input_tokens, output_tokens)`、`tool_started(tool_name, index)`、`tool_finished(tool_name, is_error, index)`、`run_finished`（`agent/events.go`）；日志不含消息正文、工具参数与工具结果正文，可用 `make logs` 读取。
- 部署：`Dockerfile` 多阶段构建、非 root 运行、`HEALTHCHECK` 打 `/healthz`；`compose.yaml` 使用 `env_file: .env`、只映射 `127.0.0.1:${MINI_AGENT_HOST_PORT:-8080}`、`stop_grace_period: 15s`；`Makefile` 的 `up` 依赖 `check` 并轮询 `/healthz`，`init` 不覆盖已存在的 `.env`。
- 本地 `.env` 现状（只读取变量名、是否为空与 host，未读取任何密钥值）：文件存在且不含 `REPLACE_WITH_` 占位符；但 `MINI_AGENT_LLM_BASE_URL` 指向 `http://127.0.0.1:9`、`MINI_AGENT_MODEL=final-consistency-model`，是此前交付验证遗留的非真实配置，**尚未**指向 TokenHub 真实 Provider；两个凭据变量均已填写。
- `.gitignore` 忽略 `.env` / `.env.local`；`git ls-files` 只有 `.env.example`；`scripts/check-env.sh` 只校验存在性与格式，不打印取值、不访问 Provider。
- Design 既有边界（`docs/design/runtime-architecture.md` §14）：Todo 端到端需服务装配任务（T08）适配；v1 不压缩、不注册 Todo 工具；内存 Session 无持久化；`-race` 未进入 CI（以 `make ci` 本地执行）。
- Design Impact 判定为 `NONE`：本任务不改变数据模型、状态机、模块边界、公开协议或安全边界；如发现缺陷只做最小修复（Bugfix 语义），不新增项目级长期事实。

Assumption：

- 「T09、T10」按 Owner 称呼指仓库内 `agent-http-api-v1`（HTTP 入口，其 Contract 编号为 T09）、`agent-runtime-e2e-tests-v1`（离线 E2E 验收）与 DevOps 提交 `f417327`；编号差异不影响执行，若 Owner 指其他交付需在预算授权前说明。
- TokenHub 的 Chat Completions 返回满足 `finish_reason ∈ {stop, tool_calls}` 且 `usage` 提供 `prompt_tokens` / `completion_tokens`；若真实返回偏离（如 `finish_reason=end_turn`、`arguments` 非 JSON），按 Scope 的最小修复流程处理，不静默放宽协议。
- 一次 Calculator 验收预期消耗 2 次上游请求（工具调用 + 终稿），普通对话 1 次；预算应按覆盖这些次数批准。
- 真实负向请求可通过临时替换 `MINI_AGENT_MODEL` 为明显无效 ID 触发，不需要改动 Provider 凭据或关闭 TLS。

OPEN QUESTION（不阻塞离线阶段，阻塞真实调用阶段，需 Owner 决定）：

- 真实 Model ID（Owner 此前成功响应为 `deepseek/deepseek-flash`，须按账户实际可用填写）。
- 预算：最大上游 LLM 请求次数、最大 token 或等价费用上限。
- 是否授权一次真实负向请求（无效 Model ID）。
- 是否需要在 `README.md` / `docs/` 对外发布验收结论（当前按 Design Impact = NONE 只写入任务目录；若要对外文档化属 Scope 变更）。
- Feature branch 名称与基线（由 Owner 创建；建议从 `develop@f417327` 出发）。

## Verification

- AC-001 → `make check`（退出码 0、不发起模型请求）；`git ls-files | grep '^\.env$'` 无输出；`git status --short` 不含 `.env`；以只读命令打印 `MINI_AGENT_LLM_BASE_URL` 的 host + path 并与 `openai.Client` 的 `/chat/completions` 拼接规则核对实际端点；比对 `.env.example` 与 `cmd/mini-agent/main.go` 的变量名集合；`docker run --rm --entrypoint /bin/sh mini-agent-go:local -c 'test ! -e /app/.env'`。环境：本机 Docker + Docker Compose v2 + 已填写的 `.env`。
- AC-002 → `make up` 后 `curl -fsS http://127.0.0.1:${MINI_AGENT_HOST_PORT}/healthz`；`docker compose ps` 观察 healthy；带 Token 与不带/错 Token 各一次 `POST /v1/sessions`（`curl -s -o /dev/null -w '%{http_code}'`），并对 `docker compose logs` 中 `llm_request` 计数做前后对比。
- AC-003 → 记录 `POST /v1/sessions` 返回的会话 ID；`POST /v1/chat` 提交问候并保存完整响应体；用 `make logs`（或 `docker compose logs`）抽出该 `run_id` 的 `run_started` / `llm_request` / `llm_response` / `run_finished` 事件，核对 model、finish_reason、计数一致。
- AC-004 / AC-005 → 同一会话提交 37×48 请求，保存完整响应体与同 `run_id` 的 runlog 事件序列；按 AC-005 四段证据逐项写入 `evidence.md`，并说明「应用无关键字路由、v1 registry 只有 calculator」的代码依据。环境：真实 TokenHub（消耗预算）。
- AC-006 → 先运行既有离线错误语义用例（`go test ./llm/... ./httpapi/... ./agent/... ./e2e/... -run 'Provider|Timeout|Cancel|Failure|Limit' -count=1` 等，按实际用例名记录）；若获批，再临时改 `MINI_AGENT_MODEL` 为明显无效 ID（不打印原值）、`docker compose up -d` 重启、发 1 次 `POST /v1/chat`，记录状态码与错误码，恢复后 `make check` 并通过运行中环境核对 Model 已恢复。
- AC-007 → 由 runlog 汇总：`llm_request` 事件数 = 上游请求次数；`llm_response` 的 `input_tokens` / `output_tokens` 求和；逐条写入 `evidence.md` 并给出与 Owner 批准上限的对比。
- AC-008 → 依次执行 `make ci`、`go test ./e2e -count=1`、`GOWORK=off go -C .agent/engine test ./...`、`git status --short`、`git log --oneline -5`、密钥模式 `git grep`；记录退出码与输出摘要。
- AC-009 → Deliverer 逐条核对 `evidence.md` 与 `delivery.md` 的结论一致性，任何未执行项必须标 `NOT_VERIFIED` / `NOT_EXECUTED`。

### 真实调用授权门禁（先于任何真实请求）

Coder 在发出第一次真实模型请求前，必须以 `WAITING_FOR_OWNER_DECISION` 取得 Owner 对以下内容的明确授权，并写入 `evidence.md`：使用哪个 Provider、哪个 Model、最多允许多少次上游 LLM 请求、最大 token / 费用上限、是否允许一次真实负向请求。未取得授权时只完成 AC-001 / AC-002 / AC-006 的离线部分与 AC-008 回归，随后以 `BLOCKED` 交 Owner，不得自行推理调用。

## Complexity

NORMAL

原因：Owner 已把验收流程、验收入口、预算门禁与失败处置规则固定下来；实现上复用现有 `httpapi` / `service` / `agent` / `llm/openai` 装配与已交付的 `e2e` 用例，唯一需要先确认的技术事实（BaseURL 是否含 `/v1`）已由 `llm/openai/client.go:89-92` 确定。不存在需要在多个方案间取舍的架构、数据模型、并发或协议选择；如需修复兼容性缺陷，按 Owner 批准的最小范围执行，不构成设计决策。因此走 `TaskBuilder → Coder`。

## Review Baseline

- Base commit：`f417327`（`ci(release): 补充镜像发布流水线与容器化启动配置`），当前分支 `develop` 与 `origin/develop` 一致。
- 任务开始时已有修改：无。`git status --short` 为空，无未跟踪文件（`.env` 被 `.gitignore` 忽略，不属于可提交内容）。
- 重叠修改的区分方式：TaskBuilder 只拥有本任务目录下的 `task.md` / `state.yaml` / `findings.md` / `core-logic.md` / `owner-decision.md` / `delivery.md`；`evidence.md` 由 Coder 创建与维护；`delivery.md` 最终由 Deliverer 汇总；如发生经 Owner 批准的兼容性修复，生产代码在独立 commit 中引入并由 Cleaner 复审。发现来源不明的 dirty 文件 → STOP 并报告，不 `git add -A` / `git add .`、不 stash、不覆盖。
- 分支前置：当前无本任务 feature branch，且 TaskBuilder 不得创建分支。Owner 创建 feature branch（建议 `feat/agent-real-llm-verification-v1`，基线 `f417327`）后，本任务产物才能提交、push 并进入 `Coder` 阶段。

## Initial Route

交 Coder（NORMAL，`contract.status = NOT_REQUIRED`，`review.status = NOT_REQUESTED`）。

两个必须先满足的前置条件（顺序固定）：

1. Owner 创建并切换本任务 feature branch（从 `f417327` 出发）；TaskBuilder 不创建分支，产物在当前工作区待提交。
2. 真实调用前由 Owner 给出预算与授权（见「真实调用授权门禁」）；在此之前 Coder 只做离线核验。
