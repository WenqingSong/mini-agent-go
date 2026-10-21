# 验收证据：agent-real-llm-verification-v1

本文件由 Coder 维护，记录可核查的原始证据。未经实跑的项一律标记 `NOT_EXECUTED` / `NOT_VERIFIED`，不以离线结果冒充真实调用结论。

## 0. 当前状态

`BLOCKED`：真实 Provider 调用未授权，且三项前置条件未满足（见 §6）。本文件目前只覆盖离线核验部分（AC-001 / AC-002 / AC-006 离线 / AC-008）。

## 1. 环境与基线

| 项 | 值 |
| --- | --- |
| 采集时间（UTC） | 2026-10-21T09:15:55Z |
| 当前分支 | `develop`（**不是**本任务 feature branch） |
| HEAD | `f417327443afd381d87de1168980cb9c0b22911f`（`ci(release): 补充镜像发布流水线与容器化启动配置`，2026-10-21 16:50:49 +0800） |
| `origin/develop` | `f417327`（与本地 HEAD 一致） |
| 工作区 | `git status --short` 仅 `?? .agent/tasks/agent-real-llm-verification-v1/` |
| Go | `go1.24.1 linux/amd64` |
| Docker | `29.6.2-7` |
| Docker Compose | `v5.3.1` |
| 本地镜像 | `mini-agent-go:local` = `dc8e5d8dd984` |

调用预算的计数口径为**上游 LLM 请求次数**（一次 `/v1/chat` 可能触发多次）。

## 2. AC 状态总览

| AC | 状态 | 说明 |
| --- | --- | --- |
| AC-001 | FAIL（部分通过） | 端点未指向 TokenHub；`.env.example` 变量名与 `main.go` 差一个 `MINI_AGENT_LISTEN_ADDR` |
| AC-002 | PASS | `make up` + healthy + `/healthz` + 201/401 + 零上游调用，全部实测 |
| AC-003 | NOT_EXECUTED | 未获真实调用授权 |
| AC-004 | NOT_EXECUTED | 未获真实调用授权 |
| AC-005 | NOT_EXECUTED | 依赖 AC-004 的 runlog 事实 |
| AC-006 | 部分 | 离线错误语义 PASS；真实负向请求 `NOT_VERIFIED`（未授权） |
| AC-007 | PASS（空账） | 上游请求 0 次、token 0，未超预算 |
| AC-008 | PASS | `make ci` / `-race -count=1` / `e2e` / Engine 全通过 |
| AC-009 | NOT_EXECUTED | `delivery.md` 由 Deliverer 汇总；本文件提供原始记录 |

## 3. 逐项证据

### AC-001 交付基线、配置一致性与密钥安全

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| `make check` 通过且不发请求 | `make check` | exit 0，输出 `✓ 配置与环境检查通过（未发起任何模型请求）` |
| `.env` 未被跟踪 | `git ls-files \| grep -x '.env'` | 无输出（只有 `.env.example` 被跟踪） |
| `git status` 不含 `.env` | `git status --short` | 只有 `?? .agent/tasks/agent-real-llm-verification-v1/` |
| 镜像内无 `.env` | `docker run --rm --entrypoint /bin/sh mini-agent-go:local -c 'test ! -e /app/.env'` | exit 0；`ls -a /app` 只有 `mini-agent` |
| 构建上下文排除凭据 | `cat .dockerignore` | 显式排除 `.env` / `.env.*` / `.env.example` |
| `make init` 不覆盖 `.env` | `make init` + sha256 前后比对 | 输出「.env 已存在，保留现有内容」；`env_unchanged=YES` |

**端点拼接规则**（`llm/openai/client.go:89-92` + `chatPath = "/chat/completions"`）：只 `TrimRight` 掉 BaseURL path 的结尾斜杠，再拼 `/chat/completions`，保留 BaseURL 自带前缀。已有测试 `llm/openai/client_test.go:826` 子用例「合法配置拼接端点」断言 `https://api.example.com/v1` → `https://api.example.com/v1/chat/completions`；`e2e/offline_test.go:59` 断言运行期实际请求路径为 `/v1/chat/completions`。

**实测端点 ≠ 目标端点**（FAIL）：

```text
容器内 MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:9/v1
容器内 MINI_AGENT_MODEL=final-consistency-model
推导端点 = http://127.0.0.1:9/v1/chat/completions
AC 目标端点 = https://tokenhub.tencentmaas.com/v1/chat/completions
```

原因：Owner 尚未把本地 `.env` 换成 TokenHub 真实配置（任务事实记录中的已知现状）。**不是代码缺陷**。

**变量名一致性**（偏差，需 Owner 裁定）：

```text
.env.example 键 = 11 个
main.go envXxx 常量 = 12 个
差集：main.go 多出 MINI_AGENT_LISTEN_ADDR
```

`MINI_AGENT_LISTEN_ADDR` 由 `compose.yaml:15` 的 `environment` 注入 `0.0.0.0:8080`（Compose `environment` 优先级高于 `env_file`），并在 `README.md:373`、`docs/design/http-api.md:184` 有文档。因此这是「容器编排固定项不进 `.env`」的设计选择，但与 AC-001「`.env.example` 的变量名与 `cmd/mini-agent` 读取的变量名一致」的字面要求不符。Coder 不擅自改 `.env.example`（属 Scope 变更），提交 Owner 决定：(a) 接受该设计并在 AC 中承认例外，或 (b) 批准最小改动把该变量以注释形式补入 `.env.example`。

### AC-002 容器化启动、健康检查与认证拒绝

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 构建并启动 | `make up` | exit 0，`服务已启动：http://127.0.0.1:8080/healthz` |
| 健康端点 | `curl -sS -w '%{http_code}' /healthz` | `http=200`，body `{"status":"ok"}` |
| 容器健康状态 | `docker compose ps` | `state=running health=healthy` |
| 正确 Bearer 创建会话 | `POST /v1/sessions` + `Authorization: Bearer <MINI_AGENT_AUTH_TOKEN>` + `Content-Type: application/json` | `http=201`，`{"session_id":"fe3d840534214479740deeda8dab8992"}` |
| 不带 Token | 同端点无 `Authorization` | `http=401`，`{"error":{"code":"unauthorized","message":"未通过认证"}}` |
| 错误 Token | `Authorization: Bearer wrong-token-value` | `http=401`，同上 |
| 被拒请求不触发上游 | 上述全部请求前后统计 runlog | `llm_request` = 0 → 0，`run_started` = 0 |
| 未授权 `/v1/chat` 也不触发上游 | `POST /v1/chat` 无 Token | `http=401`； thereafter `llm_request`=0、`run_started`=0 |
| 日志无凭据 | 对 `docker compose logs` 全文做 `grep -F` 比对 API Key / Auth Token / `Authorization` | 三者均 `NO`（当时日志 0 行，因为没有 Run 发生） |

认证先于解析与执行（`httpapi/handler.go:44-48`、`httpapi/auth.go:13-27`：未通过认证时不读 body、不调用 Runner、不写会话），与上表的零 `llm_request` 一致。对应单元级证明：`httpapi.TestAuthPrecedesBodyParsing`（未认证时不解析 body）、`httpapi.TestAuthMissingAndWrongCredentialAreIdentical`（缺失与错误凭证响应不可区分）、`httpapi.TestAuthAcceptsConfiguredCredential`、`httpapi.TestHealthzExemptFromAuthAndLeakFree`，均在 `make ci` 中 `PASS`。

**结论：AC-002 的 6 项要求全部实测通过。**

### AC-003 真实普通对话

`NOT_EXECUTED`。本地 `.env` 未指向真实 Provider，且未取得预算授权，因此未发出任何真实请求。

### AC-004 / AC-005 真实 Tool Calling 与来源证据

`NOT_EXECUTED`。依赖 AC-003 的授权前提。四段来源证据（`finish_reason=tool_calls` 与 `tool_call_count=1`、同 `CallID` 的工具事件、`op`/`a`/`b` 唯一产出 `1776`、`reply` 含 `1776`）目前均无真实 runlog 支撑，不得宣称通过。

### AC-006 错误传播

**离线部分（PASS）**：`go test ./llm/... ./httpapi/... ./agent/... ./e2e/... -count=1 -run 'Provider|Timeout|Cancel|Failure|Limit|Disconnect|ErrorSemantics|ContextFailures'`

| 用例 | 覆盖的错误语义 |
| --- | --- |
| `llm/openai.TestCompleteErrorSemantics` | Provider 4xx/5xx → `provider_failure`，协议错误分类 |
| `llm/openai.TestCompleteContextFailures` | 取消 / 超时 → `cancelled` / `deadline_exceeded`，保留 `Cause` |
| `httpapi.TestProviderFailureClassificationEndToEnd` | 端到端错误分类未被吞掉 |
| `httpapi.TestRequestTimeoutReturns504` / `TestRequestTimeoutClassificationEndToEnd` | 请求超时映射 |
| `httpapi.TestClientDisconnectCancelsRun` / `TestShutdownCancelsInflightRunAfterGrace` | 客户端断开与关闭取消 |
| `httpapi.TestChatFailureHasNoSideEffects` | 失败不产生副作用提交 |
| `e2e.TestProviderFailures/provider_4xx`、`/provider_5xx` | 全链路 4xx/5xx |
| `e2e.TestProviderUnresponsive`、`TestRequestTimeout`、`TestClientDisconnect` | 阻塞 / 超时 / 断连 |
| `e2e.TestBudgetLimits/MaxLLMCalls耗尽`、`MaxToolCalls耗尽` | 预算耗尽拒绝 |

全部 `PASS`。

**真实 Provider 负向响应：`NOT_VERIFIED`**（未授权一次真实负向请求）。未使用任何离线用例冒充真实负向验收，未篡改或删除真实 API Key。

### AC-007 预算合规

| # | 用途 | 上游请求次数 | 结果 | input_tokens | output_tokens |
| --- | --- | --- | --- | --- | --- |
| — | 无（尚未发出任何真实请求） | 0 | — | 0 | 0 |
| 合计 | | **0** | | **0** | **0** |

未超出任何上限（尚无批准上限，见 §6）。本文件不含 API Key 及其片段。

### AC-008 离线回归与密钥安全

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全量 CI | `make ci`（`go build ./...` → `go vet ./...` → `go test -count=1 ./...` → `go test -race ./...`） | exit 0；13 个包全部 `ok` |
| Race 非缓存复跑 | `go test -race -count=1 ./...` | exit 0；13 个包全部 `ok`（`make ci` 的 race 命中缓存，故补跑） |
| 既有 E2E | `go test ./e2e -count=1 -v` | exit 0；13 个 `Test*` 全部 `PASS`，无 `SKIP` |
| Engine 独立 Module | `GOWORK=off go -C .agent/engine test -count=1 ./...` | exit 0；`cmd/workflow-check`、`internal/workflow` 均 `ok` |
| 生产代码改动 | `git status --short` | 无任何生产代码 / 测试改动（本任务仅新增任务目录产物） |
| 密钥模式扫描 | `git grep -nIE 'sk-[A-Za-z0-9]{16,}\|Bearer [A-Za-z0-9._-]{20,}\|AKID[A-Za-z0-9]{10,}\|-----BEGIN [A-Z ]*PRIVATE KEY-----'` | 仅命中 `runlog/sanitize_test.go:105-106`，是脱敏器的**测试夹具**（`-----BEGIN PRIVATE KEY-----abc` 与 `ak-9`），非真实凭据 |
| `.env` 跟踪状态 | `git ls-files \| grep -x '.env'` | 无输出 |
| 镜像内 `.env` | 见 AC-001 | 不存在 |

`git grep` 未对 `.agent/` 历史与全部提交做全历史扫描；本次只扫描当前 worktree 的已跟踪文件。

### AC-009 最终报告

`NOT_EXECUTED`。`delivery.md` 由 Deliverer 在 Coder → Cleaner → OwnerGate 之后汇总；本文件为其原始证据来源。

## 4. 离线复核结论（Task Scope 第 1 段）

| 复核项 | 结论 |
| --- | --- |
| 前置交付状态 | 已逐目录重读 `state.yaml`：除本任务外的 13 个目录全部 `contract=APPROVED\|NOT_REQUIRED` + `review=CLEAN` + `owner=ACCEPTED` + `delivery=PASS`；`f417327` 已在 `origin/develop`。本任务自身为 `NOT_REQUESTED / PENDING / NOT_RUN` |
| `make init` | 保留已存在 `.env`，实测哈希未变 |
| `make check` | 只读校验，不访问 Provider，不打印取值；通过 |
| `make up` | 依赖 `check`，构建镜像后轮询 `/healthz` 最多 30 次；实测通过 |
| `Dockerfile` | 多阶段、`CGO_ENABLED=0` 静态二进制、非 root（uid 10001）、`HEALTHCHECK` 打 `/healthz`；最终层只 `COPY` 二进制 |
| `compose.yaml` | `env_file: .env` + `environment` 注入 `MINI_AGENT_LISTEN_ADDR=0.0.0.0:8080`；端口只映射 `127.0.0.1:${MINI_AGENT_HOST_PORT:-8080}`；`stop_grace_period: 15s` |
| `.env.example` 与 `main.go` | 11 vs 12，差 `MINI_AGENT_LISTEN_ADDR`（见 AC-001） |
| `llm/openai` 端点 | `TrimRight(path,"/") + "/chat/completions"`，清空 query/fragment；BaseURL 需含 `/v1` |
| `llm/openai` 认证 | 仅 `APIKey` 非空时发 `Authorization: Bearer <key>`（`client.go:145-148`）；与服务对外 `MINI_AGENT_AUTH_TOKEN` 完全独立 |
| `llm/openai` 请求体 | 只发 `model` / `messages` / `tools` 与可选 `max_tokens`、`temperature`；不发 `stream`；失败不自动重试 |
| `llm/openai` 响应容错 | `json.Unmarshal` 忽略未知字段；`message.content` 允许 `null`；`choices`/`message` 缺失或为 null、非 assistant 角色、`tool_calls` 缺 ID/函数名、`arguments` 空串或非 JSON、`usage` 负数 → 一律 `provider_failure` |
| `finish_reason` 白名单 | 只接受 `stop` / `tool_calls` / `length`（`wire.go:346-353`），其它取值与缺失同样 `provider_failure`。**这是与 TokenHub 的唯一高风险兼容点**：若真实返回 `end_turn`、`content_filter`、`null`，AC-003 会直接失败，需按 Owner 批准的最小修复流程处理，不得静默放宽 |
| `cmd/mini-agent` 装配 | 只注册 `calculator` 并 `Seal()`（`main.go:173-182`）；`TokenBudget=0`、`KeepRecent=0`（`main.go:236-239`）；预算 `MINI_AGENT_MAX_LLM_CALLS` / `MINI_AGENT_MAX_TOOL_CALLS` 默认 8/8；runlog 为 `runlog.NewJSONSink(os.Stderr)`（`main.go:105`），容器内即 `docker compose logs` |
| 应用无关键字路由 | 工具调用只来自 Provider 响应的 `choices[0].message.tool_calls`；`httpapi` 只做认证/解析/调用 Runner，无任何按消息文本选工具的分支 |

## 5. 已知限制（按当前装配如实记录）

- Todo 未接入 HTTP 服务：`session.TodoOperations` 端口适配属于服务装配任务 T08，v1 registry 只有 `calculator`。
- `TokenBudget=0`，`service/runner.go` 拒绝非 0 取值，因此**不做上下文压缩**；超长会话不会被摘要。
- Session 是进程内存，无持久化；容器重启后会话丢失，跨实例不共享。
- 没有会话读取端点（只有 `GET /healthz`、`POST /v1/sessions`、`POST /v1/chat`），会话内容无法从 HTTP 观察。
- 生产代码无 Provider 重试 / 限流；429 与 5xx 不重发。
- 项目无流式输出；`stream` 字段不发送。
- `-race` 未进入 CI（`.cnb.yml` 只跑 `make ci`），本次以本地 `make ci` + `-race -count=1` 执行。
- CNB 镜像发布（`f417327`）只在 `main` push 时触发；本任务在 feature 分支，**无平台发布证据**，标注待验证。

## 6. 阻塞与 Owner Action

真实调用授权门禁（Task「真实调用授权门禁」）要求 Coder 在第一次真实请求前取得明确授权。以下三项均为 Owner-only 动作或决定，未满足前不得发出真实请求：

1. **feature branch 未创建**。当前在 `develop`。Coder 禁止创建分支（`AgentCollaborationSpecification` §11），因此本轮不产出 C1/C2 commit，也无法 push 与通过 `cleaner-start` Gate。
2. **`.env` 未指向真实 Provider**。实测 `MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:9/v1`、`MINI_AGENT_MODEL=final-consistency-model`，AC-001 的端点要求未满足。
3. **预算未授权**。需要 Owner 明确：Provider（腾讯云 TokenHub）、Model ID（任务记录提及 `deepseek/deepseek-flash`，须按账户实际可用确认）、最大上游 LLM 请求次数、最大 token 或等价费用上限、是否授权一次真实负向请求（临时无效 Model ID）。

另需 Owner 裁定 AC-001 的变量名一致性偏差（见 AC-001 末段），以及是否把验收结论写入 `README.md` / `docs/`（当前 Design Impact = NONE，只写任务目录）。
