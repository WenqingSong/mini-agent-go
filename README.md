# mini-agent-go

单进程 Agent Runtime，对外暴露 HTTP JSON API。一次请求驱动一次完整的 Agent Run：模型调用 → 工具执行 → 模型调用，直到得到终态结论；只有正常完成的那一轮才会写入会话历史。

- 语言与工具链：Go 1.24（`go.mod`：`go 1.24`，零 `require`，无第三方依赖）。
- 入口：`cmd/mini-agent`；对外协议：`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`。
- 当前能力范围内置工具只注册 `calculator`；Todo 工具、上下文压缩、会话持久化尚未进入生产装配（见[当前限制与未验证事项](#当前限制与未验证事项)）。

本文件只回答「怎么用、怎么跑、怎么理解」。长期权威契约（模块边界、不变量、对外协议字段与错误码）以 [`docs/design/`](docs/design/) 为准；两者冲突时以 `docs/design/*` 为准并修正本文件。

## 目录

- [快速开始](#快速开始)
- [架构总览](#架构总览)
- [Agent Loop：一次 Run 的生命周期](#agent-loop一次-run-的生命周期)
- [HTTP API](#http-api)
- [环境变量](#环境变量)
- [镜像与发布](#镜像与发布)
- [Fork 与 CNB 流水线](#fork-与-cnb-流水线)
- [测试模式与真实 Provider 模式](#测试模式与真实-provider-模式)
- [当前限制与未验证事项](#当前限制与未验证事项)
- [文档索引](#文档索引)

## 快速开始

### 前提

- Go 1.24 或更高的 1.24.x（CI 使用 `golang:1.24` 镜像，见 `.cnb.yml`）。
- 容器化启动需要 Docker 与 Docker Compose v2（`docker compose version` 可执行）。
- 无需数据库、缓存、消息队列；根模块零第三方依赖，测试不触网。
- 构建与测试不需要 API Key；只有**真正启动服务**（`make up` 或直接运行二进制）才需要 `.env` 里的 Provider 配置。

先确认工具链：

```bash
go version
```

预期输出形如 `go version go1.24.1 linux/amd64`（1.24.x 即可）。

### 第一次启动

```bash
git clone https://cnb.cool/framework-station/mini-agent-go   # 或你自己的 Fork 地址
cd mini-agent-go
make init          # 检查 Docker，并从 .env.example 生成 .env（已存在则不覆盖）
```

然后手动编辑 `.env`，填写 `MINI_AGENT_LLM_BASE_URL`、`MINI_AGENT_MODEL`、`MINI_AGENT_LLM_API_KEY`、`MINI_AGENT_AUTH_TOKEN` 四项（详见[容器化启动](#容器化启动推荐)），再执行：

```bash
make check         # 只校验配置与环境，不调用模型
make up            # 构建镜像 + 后台启动 + 等待 /healthz 就绪
```

### 构建

```bash
make build      # 产出 bin/mini-agent（等价于 go build -trimpath -o bin/mini-agent ./cmd/mini-agent）
```

`bin/` 已被 `.gitignore` 忽略，产物不入库。只检查能否编译（不落产物）用 `go build ./...`，CI 走的就是这条。

### 测试

```bash
make test       # 等价于 go test -count=1 ./...
```

完全离线执行（不访问网络、不访问真实 Provider）：

```bash
GOPROXY=off GOFLAGS=-mod=mod go test -count=1 ./...
```

预期所有包输出 `ok`。根模块没有第三方依赖，因此该命令在无网络环境下可全部通过。

CI 与本地共用同一个入口，`make ci` 就是 `.cnb.yml` 里执行的全部内容：

```bash
make ci         # go build ./... && go vet ./... && go test -count=1 ./... && go test -race ./...
```

`.agent/engine/` 是 Workflow 校验器的独立 Go Module（`workflow-v2-engine`），不在根模块的 `./...` 内，需单独执行：

```bash
make test-engine   # 等价于 GOWORK=off go -C .agent/engine test ./...
```

注意：Engine 模块依赖 `gopkg.in/yaml.v3`，首次运行需要能取到该模块（或本地已有 module cache）；它与业务代码无关，业务测试不需要它。

### 容器化启动（推荐）

一次性准备，再启动：

```bash
make init      # 检查 Docker/Compose，并在 .env 不存在时从 .env.example 复制
# 手动编辑 .env，填写下面 4 项
make check     # 只校验 .env 与本地环境，不发起任何模型请求
make up        # 构建镜像、后台启动，并用 /healthz 确认就绪
```

`make init` 只做三件事：检查 `docker` / `docker compose` 可用、检查 `.env.example` 存在、必要时复制出 `.env`。**它不会覆盖已存在的 `.env`**，也不启动服务、不发送模型请求，因此可以反复执行。

`.env` 中必须填写的 4 项（变量名与 `cmd/mini-agent/main.go` 的 `loadConfig` 一致）：

| 变量 | 说明 |
| --- | --- |
| `MINI_AGENT_LLM_BASE_URL` | Provider 的 OpenAI 兼容根地址，可带 `/v1` 前缀；客户端固定请求 `<BaseURL>/chat/completions` |
| `MINI_AGENT_MODEL` | 该服务支持的模型名 |
| `MINI_AGENT_LLM_API_KEY` | 模型服务密钥 |
| `MINI_AGENT_AUTH_TOKEN` | 本服务对外的 Bearer 凭证。容器内监听 `0.0.0.0:8080`，应用要求非回环监听必须带凭证；生成示例 `openssl rand -hex 32` |

其余变量（超时、预算、system prompt 等）不填即使用应用默认值，完整列表见[环境变量](#环境变量)。

启动后的常用操作：

```bash
curl -sS http://127.0.0.1:8080/healthz   # {"status":"ok"}；免认证，只证明本服务在运行
make logs                                # 跟随日志（Ctrl-C 退出）
make ps                                  # 容器状态与健康检查结果
make down                                # 停止并移除容器；不删除 .env
```

端口只映射到宿主机回环地址 `127.0.0.1:8080`，不会暴露到局域网。需要换端口时：

```bash
MINI_AGENT_HOST_PORT=9090 make up        # 宿主机 9090 → 容器 8080
```

`make up` 的失败会明确报出原因：`make check` 不通过时它会直接中止且不创建容器；`/healthz` 30 秒内未就绪时它会打印最近 50 行容器日志并以非零状态退出。

排查思路：

- `✗ MINI_AGENT_LLM_BASE_URL 仍是示例地址`：`.env` 还是 `.env.example` 的占位值，填真实地址。
- `✗ MINI_AGENT_AUTH_TOKEN 未配置`：容器内是非回环监听，必须给凭证。
- `✗ Docker daemon 不可用`：启动 Docker Desktop，或把当前用户加入 `docker` 组后重新登录。
- 容器启动即退出：`make logs` 看 stderr；应用是 fail-closed 的，配置非法时不会半启动。

`make up` 只保证本服务可用，**不代表真实 Provider 已验证**——它不会发起任何模型调用，见[测试模式与真实 Provider 模式](#测试模式与真实-provider-模式)。

### 启动（直接运行二进制，不使用容器）

容器化启动之外的另一种方式，适合本地改代码调试：

配置只来自环境变量，缺失必填项即启动失败（fail-closed）。先构建可执行文件，再用占位地址启动（不产生任何真实模型调用）：

```bash
go build -o /tmp/mini-agent ./cmd/mini-agent
MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:9/v1 \
MINI_AGENT_MODEL=placeholder-model \
/tmp/mini-agent
```

预期行为：进程进入监听（默认 `127.0.0.1:8080`），运行事件以 JSON 行写入 stderr。若看到 `环境变量 ... 必填` 或 `httpapi: ...` 之类的错误并非零退出，说明配置非法，服务不会进入监听。

真实调用模型时，把 `MINI_AGENT_LLM_BASE_URL` 换成你的 OpenAI 兼容服务根地址、`MINI_AGENT_MODEL` 换成该服务支持的模型名，必要时设置 `MINI_AGENT_LLM_API_KEY`。可用变量与默认值见[环境变量](#环境变量)。

也可以用 `go run ./cmd/mini-agent` 临时启动（无需产出文件），停止方式见下节。

### 停止

在前台按 `Ctrl-C`（SIGINT），或向该进程发送 `SIGTERM`：

```bash
kill -TERM <pid>
```

收到信号后停止接受新连接；已进入处理的请求在 `MINI_AGENT_SHUTDOWN_GRACE`（默认 `10s`）内正常返回，宽限期满则取消在途 Run 并强制关闭连接。重复发送信号不会 panic，会触发强制关闭。

注意：若用 `go run` 启动，请在前台按 `Ctrl-C` 停止。单独把信号发给 `go run` 进程不保证转发到它启动的子进程（本仓库实测：给 `go run` 进程发 SIGINT 后子进程仍在监听，需对子进程另行终止）。

## 架构总览

分层与依赖方向（权威定义见 [`docs/design/runtime-architecture.md`](docs/design/runtime-architecture.md) §2 / §3）：

```text
L0  core          公共契约：消息、工具调用、运行状态、错误、配对与校验（仅标准库）
L1  llm           LLM Provider 接口
    tool          Tool / Registry 接口
    session       Session / Todo / 摘要类型与内存 Session Manager
    contextmgr    上下文预算、窗口与压缩接口
    runlog        运行日志 Sink
L2  builtin       内置工具实现：calculator / mock_search / todo_create / todo_list / todo_complete
L3  agent         Agent Loop：一次 Run 的循环、预算、终态、提交与事件接线
    httpapi       HTTP 对外协议层：配置、路由、请求解析、错误封套与映射、认证
    service       HTTP ↔ Agent 装配层：同 Turn 适配器、per-Run Loop、reply 提取
    cmd/mini-agent 可启动入口：环境变量装配、监听、终止信号与优雅关闭
```

依赖方向的硬约束：

- 只能向上依赖：`core` 只依赖标准库；L1 依赖 `core` 且互不 import；`builtin` 只依赖 `core` / `tool` / `session`。
- `agent` 只依赖 L0 / L1，不 import `builtin`、`llm/openai`；具体 Provider 与工具由 `cmd/mini-agent` 装配。
- `httpapi` 只依赖标准库与 `core` / `session`，不调用 Agent Loop；`service` 实现 `httpapi.Runner` 端口。
- `cmd/mini-agent` 是唯一装配点，不被任何其他包 import。

自查依赖方向（命令来自 `runtime-architecture.md` §2 的机械核对）：

```bash
go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...
```

模块职责一览（细节与「明确不负责」见 `runtime-architecture.md` §3）：

| 模块 | 职责 | 关键位置 |
| --- | --- | --- |
| `core` | 消息模型、工具调用/结果配对、运行状态与错误种类、纯校验 | `core/message.go`、`core/run.go`、`core/pairing.go`、`core/error.go` |
| `llm` | 一次 LLM 调用的请求/响应契约与 `Provider` 接口 | `llm/provider.go` |
| `llm/openai` | OpenAI 兼容 Chat Completions 实现（单次请求、不自动重试） | `llm/openai/client.go` |
| `tool` | `Tool` / `Registry` 契约、Schema 与参数校验、受控执行器 | `tool/tool.go`、`tool/registry.go`、`tool/executor.go` |
| `builtin` | 五个内置工具与 `TodoOperations` 写入端口 | `builtin/` |
| `session` | 内存 Session Manager：会话串行、Turn 原子提交、Todo 暂存 | `session/manager.go`、`session/turn.go` |
| `contextmgr` | 上下文预算、窗口选择与压缩 | `contextmgr/manager.go` |
| `runlog` | 运行事件与结构化 JSON 日志（含脱敏与上限） | `runlog/runlog.go`、`runlog/slog.go` |
| `agent` | Agent Loop：循环、双预算、终态、整轮提交、事件接线 | `agent/agent.go`、`agent/run.go` |
| `httpapi` | 对外协议：路由、严格解析、错误码→状态码单点映射、认证、生命周期 | `httpapi/server.go`、`httpapi/handler.go`、`httpapi/errors.go` |
| `service` | 一次 chat = 一次真实 `RunTurn` 的装配 | `service/runner.go`、`service/turn.go` |
| `cmd/mini-agent` | 环境变量 → 装配 → 监听 → 信号 → 优雅关闭 | `cmd/mini-agent/main.go` |

## Agent Loop：一次 Run 的生命周期

入口是 `agent.Loop.Run`（`agent/agent.go`），内部由 `agent/run.go` 的循环主体驱动。一次 Run 的顺序：

1. 前置校验 `ExecutionRef`（`SessionID` / `RunID` 非空）与 `core.RunPolicy`（两个预算必须为正）。不成立时直接返回 `FAILED + invalid_argument`，不取得会话执行权、不发出运行事件。
2. 发出 `run_started` 事件。
3. 在同一次 `session.Manager.RunTurn` 内完成整轮执行：本次 Run 的全部模型调用与工具执行都在这一把会话执行权里，同会话严格串行、跨会话互不阻塞。
4. 每次模型调用前，用 `contextmgr.Build` 装配可送 Provider 的消息视图（v1 预算为 0，不触发压缩，消息视图等于完整历史；system prompt 单独经 `llm.Request.System` 传递），再调用 `llm.Provider`。
5. 按 `finish_reason` 判定：只有 `stop` 且无工具调用才结束循环；`tool_calls` 则先整批预检剩余工具额度，额度不足则整批不执行并判 `tool_call_limit`，否则按数组序串行执行，把每个结果作为独立的 `RoleTool` 消息回填后回到第 4 步。
6. `length`（输出被截断）、`tool_calls` 却没有任何调用、`unknown` 都不算完成：一律 `FAILED + provider_failure`，被截断响应的工具调用不执行。
7. 循环结束或失败后发出 `run_finished`，返回 `core.RunResult`（`Status`、`Error`、`LLMCalls`、`ToolCalls`、`Messages`）。

双预算语义（`runtime-architecture.md` §6）：

- 两个上限按类别分别计数：`MaxLLMCalls`（模型调用次数）与 `MaxToolCalls`（工具执行尝试次数），不使用单一 step 预算。
- 都在调用发出**之前**预检；上限不可绕过，非正值属于调用方契约错误。
- 触限即终态 `LIMIT_EXCEEDED`，并用不同错误种类区分是 `llm_call_limit` 还是 `tool_call_limit`。
- 实际计数写入 `RunResult.LLMCalls` / `ToolCalls`；HTTP 响应里的 `llm_calls` / `tool_calls` 就来自这里。

终态（`runtime-architecture.md` §5）：

| 终态 | 含义 | 错误种类 |
| --- | --- | --- |
| `COMPLETED` | 模型不再要求工具调用，正常结束 | 无错误 |
| `FAILED` | Provider 失败、不可恢复工具失败、非法消息序列等 | `provider_failure` / `tool_failure` / `invalid_pairing` / `invalid_argument` / `internal` |
| `LIMIT_EXCEEDED` | 触达模型调用或工具调用预算 | `llm_call_limit` / `tool_call_limit` |
| `CANCELLED` | 客户端取消、请求超时或服务关闭 | `cancelled` / `deadline_exceeded` |

提交边界（`runtime-architecture.md` §7「提交边界」、§11.1 Agent Loop 不变量 INV-001）：

- 只有 `COMPLETED` 才提交本轮的消息、摘要与受管理的会话状态；`FAILED` / `LIMIT_EXCEEDED` / `CANCELLED` 整轮丢弃，Session 与运行前逐字段一致。
- 已提交的历史不会被回滚；外部工具副作用也不存在回滚、补偿或自动重试。
- `RunResult.Messages` 是本轮产出消息的完整记录，包含未提交内容；未配对消息永不进入 Session。
- 因此一次失败请求之后，同一会话再次提问不会看到「只有提问、没有回答」的半轮历史。

## HTTP API

三项端点、字段与错误码的权威定义见 [`docs/design/http-api.md`](docs/design/http-api.md) §3 / §4 / §8。要点：

- 路由按「方法 + 精确路径」分派；未知路径 `404 not_found`，已知路径方法不符 `405 method_not_allowed`（带 `Allow` 头）。
- 请求体必须是单个 JSON 对象，`Content-Type` 必须是 `application/json`（允许 `; charset=utf-8`）；契约外字段、类型/长度/字符集非法都会被拒绝，不做静默忽略。
- 请求体上限由 `MINI_AGENT_MAX_REQUEST_BYTES` 控制（默认 1 MiB），超限 `413 payload_too_large`。
- 只有 `COMPLETED` 且无错误才返回 `200`；其余终态一律错误封套。
- 认证只在配置了 `MINI_AGENT_AUTH_TOKEN` 时启用（非回环监听必须配置），`/healthz` 始终免认证。

### 请求与响应形状

`POST /v1/sessions`

| 项 | 内容 |
| --- | --- |
| 请求 | `{"session_id":"<可选，^[A-Za-z0-9._:-]{1,128}$>"}`；body 可为空（等价 `{}`） |
| 成功 | `201`，`{"session_id":"<id>"}`；未提供 `session_id` 时服务端生成 32 位小写 hex |
| 失败 | 同 ID 会话仍有效时 `409 session_exists`；参数类错误见错误码表 |

`POST /v1/chat`

| 项 | 内容 |
| --- | --- |
| 请求 | `{"session_id":"<必填，已在服务中存在>","message":"<必填，1..8192 个 UTF-8 字符>"}` |
| 成功 | `200`，`{"session_id":"...","run_id":"<32 位 hex>","status":"COMPLETED","reply":"...","llm_calls":N,"tool_calls":N}` |
| 不返回 | 消息序列、Provider 原始载荷、token 用量、system prompt、会话内部状态 |
| 失败 | 会话不存在 `404 session_not_found`；其余见错误码表 |

`GET /healthz`

- 免认证、无请求体；成功 `200`，`{"status":"ok"}`。
- 不探测 Session / Provider，不输出配置、密钥、版本或会话数；仅在监听成功后可达。

错误响应统一封套（所有状态码都是这个形状）：

```json
{"error":{"code":"<snake_case>","message":"<固定文案>"}}
```

`code` 与 HTTP 状态码的对应关系（`httpapi/errors.go` 是唯一映射点，与 `http-api.md` §4 一致）：

| code | HTTP | 触发条件 |
| --- | --- | --- |
| `invalid_json` | 400 | body 不是单个合法 JSON 对象 |
| `unknown_field` | 400 | 出现契约外字段 |
| `missing_field` | 400 | 必填字段缺失 |
| `invalid_field` | 400 | 类型、长度、字符集或 UTF-8 非法 |
| `unsupported_media_type` | 415 | `Content-Type` 不是 `application/json` |
| `payload_too_large` | 413 | 请求体超过上限 |
| `unauthorized` | 401 | 凭证缺失或错误（同一响应，不可枚举） |
| `not_found` | 404 | 未知路径 |
| `method_not_allowed` | 405 | 已知路径方法不符 |
| `session_not_found` | 404 | 会话不存在 |
| `session_expired` | 404 | 会话已过期（v1 未启用 TTL，正常情况下不会出现） |
| `session_exists` | 409 | 同 ID 会话仍有效 |
| `deadline_exceeded` | 504 | 请求级超时（`MINI_AGENT_REQUEST_TIMEOUT`） |
| `cancelled` | 408 | 客户端取消；连接已断开时不承诺该响应可达 |
| `provider_failure` | 502 | 模型服务调用失败 |
| `tool_failure` | 500 | 不可恢复的工具失败 |
| `llm_call_limit` | 429 | 模型调用预算耗尽 |
| `tool_call_limit` | 429 | 工具调用预算耗尽 |
| `invalid_pairing` | 500 | 工具调用与结果配对非法 |
| `internal` | 500 | 其余内部错误 |

### curl 示例

以默认回环地址、未启用认证、且 Provider 地址不可用（占位地址）为前提：

```bash
# 1. 健康检查
curl -sS -i http://127.0.0.1:8080/healthz
```

预期 `200` 与 `{"status":"ok"}`。

```bash
# 2. 创建会话（不传 session_id，由服务端生成）
curl -sS -i -X POST http://127.0.0.1:8080/v1/sessions \
  -H 'Content-Type: application/json' \
  -d '{}'
```

预期 `201` 与 `{"session_id":"<32 位小写 hex>"}`。

```bash
# 3. 发起一轮对话（把上一步返回的 session_id 填进来）
curl -sS -i -X POST http://127.0.0.1:8080/v1/chat \
  -H 'Content-Type: application/json' \
  -d '{"session_id":"<上一步返回的 session_id>","message":"你好"}'
```

在「Provider 地址不可用」这一前提下，预期是 `502` 与：

```json
{"error":{"code":"provider_failure","message":"模型服务调用失败"}}
```

这不是使用错误，而是 v1 的既定失败语义：模型调用失败即 `FAILED + provider_failure`，本轮整轮丢弃。要看到 `200 + COMPLETED`，需要配置一个真实可用的 OpenAI 兼容服务（见下节）。

若启用了认证（设置过 `MINI_AGENT_AUTH_TOKEN`，非回环监听时必须），三个端点中除 `/healthz` 外都要带凭证：

```bash
curl -sS -i -X POST http://127.0.0.1:8080/v1/sessions \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <你的 MINI_AGENT_AUTH_TOKEN>' \
  -d '{}'
```

## 环境变量

配置只从环境变量读取（不读配置文件、不接受命令行参数），由 `cmd/mini-agent/main.go` 的 `loadConfig` 装配，默认值与校验以 `httpapi/config.go`、`service/runner.go` 为准。

容器化启动时，这些变量由 `compose.yaml` 的 `env_file: .env` 原样注入容器；`.env.example` 是唯一模板（只含占位符），`make init` 负责复制、`make check` 负责校验。容器内监听地址被固定为 `0.0.0.0:8080`（宿主机侧只映射回环端口），因此容器内**必须**配置 `MINI_AGENT_AUTH_TOKEN`。

| 变量 | 必填 | 默认 | 含义 |
| --- | --- | --- | --- |
| `MINI_AGENT_LISTEN_ADDR` | 否 | `127.0.0.1:8080` | 监听地址；非回环地址必须配置认证凭证 |
| `MINI_AGENT_AUTH_TOKEN` | 非回环监听时必填 | 空（不启用认证） | HTTP 静态 Bearer 凭证，与 LLM `APIKey` 相互独立 |
| `MINI_AGENT_LLM_BASE_URL` | **是** | 无 | OpenAI 兼容服务根地址；实际请求发往 `<BaseURL>/chat/completions` |
| `MINI_AGENT_LLM_API_KEY` | 否 | 空 | 为空时不发送 `Authorization` 头 |
| `MINI_AGENT_MODEL` | **是** | 无 | 透传给 Provider 的模型名 |
| `MINI_AGENT_SYSTEM_PROMPT` | 否 | 空 | 纯文本 system prompt |
| `MINI_AGENT_REQUEST_TIMEOUT` | 否 | `60s` | 单个请求的服务端超时，必须为正 |
| `MINI_AGENT_SHUTDOWN_GRACE` | 否 | `10s` | 优雅关闭宽限期，必须为正 |
| `MINI_AGENT_MAX_REQUEST_BYTES` | 否 | `1048576`（1 MiB） | 请求体读取上限，必须为正 |
| `MINI_AGENT_MAX_LLM_CALLS` | 否 | `8` | 单次 Run 的模型调用上限，必须为正 |
| `MINI_AGENT_MAX_TOOL_CALLS` | 否 | `8` | 单次 Run 的工具调用上限，必须为正 |
| `MINI_AGENT_TOOL_TIMEOUT` | 否 | `30s` | 工具级超时；取值 `<= 0` 表示不设工具级超时 |

可复制示例（**全部为占位符，请替换为真实值；不要把真实密钥提交进仓库**）：

```bash
export MINI_AGENT_LISTEN_ADDR=127.0.0.1:8080
export MINI_AGENT_AUTH_TOKEN=REPLACE_WITH_HTTP_BEARER_TOKEN
export MINI_AGENT_LLM_BASE_URL=https://llm.example.invalid/v1
export MINI_AGENT_LLM_API_KEY=REPLACE_WITH_LLM_API_KEY
export MINI_AGENT_MODEL=REPLACE_WITH_MODEL_NAME
export MINI_AGENT_SYSTEM_PROMPT='你是一个简洁的助手。'
export MINI_AGENT_REQUEST_TIMEOUT=60s
export MINI_AGENT_SHUTDOWN_GRACE=10s
export MINI_AGENT_MAX_REQUEST_BYTES=1048576
export MINI_AGENT_MAX_LLM_CALLS=8
export MINI_AGENT_MAX_TOOL_CALLS=8
export MINI_AGENT_TOOL_TIMEOUT=30s
```

启动失败的典型原因：`MINI_AGENT_LLM_BASE_URL` 或 `MINI_AGENT_MODEL` 缺失；时长/上限配置成非正值；监听地址不可解析；监听非回环地址却没配 `MINI_AGENT_AUTH_TOKEN`。服务在进入监听前失败，不会半启动。

## 镜像与发布

镜像由仓库根的 `Dockerfile` 多阶段构建：`golang:1.24` 编译出静态二进制，运行阶段只保留 `alpine:3.21` + 该二进制，以 UID 10001 的非 root 用户运行，镜像约 22 MB。`.dockerignore` 排除了 `.env`、`.git`、`.agent`、`bin`、`docs`，构建上下文里没有凭据。

镜像仓库地址由当前仓库决定，命名遵循 CNB Docker 制品库规范（`<registry>/<仓库路径小写>`）：

```text
docker.cnb.cool/<你的仓库路径>:latest
docker.cnb.cool/<你的仓库路径>:v<VERSION>-<8 位提交 SHA>
```

本仓库即 `docker.cnb.cool/framework-station/mini-agent-go`。

两种标签的分工：

| 标签 | 含义 | 是否会变 |
| --- | --- | --- |
| `v0.1.0-1a2b3c4d` | 不可变版本标签：`VERSION` 文件 + 构建所用提交短 SHA | 不会；同一提交永远对应同一标签，可反查 `git show 1a2b3c4d` |
| `latest` | 最近一次 main 上**成功**发布的镜像 | 会；构建或测试失败时不会移动 |

`VERSION` 是版本的唯一来源，格式 `X.Y.Z`，不自动修改、不自动创建 Git Tag。

拉取与运行（运行时不内置任何凭据，配置全部由环境变量传入）：

```bash
docker pull docker.cnb.cool/framework-station/mini-agent-go:latest

docker run -d --name mini-agent \
  -p 127.0.0.1:8080:8080 \
  -e MINI_AGENT_LISTEN_ADDR=0.0.0.0:8080 \
  -e MINI_AGENT_AUTH_TOKEN="$(openssl rand -hex 32)" \
  -e MINI_AGENT_LLM_BASE_URL=https://your-provider.example.com/v1 \
  -e MINI_AGENT_MODEL=your-model \
  -e MINI_AGENT_LLM_API_KEY=your-key \
  docker.cnb.cool/framework-station/mini-agent-go:latest
```

本地用 `make up` 等价于「构建本地镜像 + 用 `.env` 注入 + 回环端口映射」，日常开发不必手动执行上面的 `docker run`。

## Fork 与 CNB 流水线

### Fork 后怎么用

1. 在 CNB 仓库页点 Fork，得到 `<你的组织>/mini-agent-go`。
2. `git clone https://cnb.cool/<你的组织>/mini-agent-go`，然后 `make init` → 填 `.env` → `make check` → `make up`。这一步不需要原作者的任何令牌：Go 代码零第三方依赖，测试与镜像构建都可以离线完成。
3. Fork 仓库默认**不会**自动触发流水线（CNB 平台行为，不是 `.cnb.yml` 能决定的）。需要在仓库页开启：
   - 路径：仓库 **设置 → 云原生构建**，勾选「允许自动触发」；
   - 若希望本仓 Fork 出去的仓库也默认自动触发，再勾选「Fork 的仓库默认允许自动触发」；
   - 若根组织在 **组织设置 → 云原生构建管控** 关闭了总开关，仓库侧开关无效，需要组织管理员放行。
4. 流水线触发后，镜像推送到 **Fork 仓库自己的**制品库：`docker.cnb.cool/<你的仓库路径>`。`.cnb.yml` 里没有写死任何上游地址，用的是 CNB 内置变量 `CNB_DOCKER_REGISTRY` 与 `CNB_REPO_SLUG_LOWERCASE`。

### 流水线触发与发布

| 事件 | 执行内容 | 发布镜像 |
| --- | --- | --- |
| 任意分支 push（含 `develop`） | `make ci`：build + vet + test + race | 否 |
| 任意目标分支的 Pull Request | `make ci` | 否 |
| `main` 的 push / 合并后 | `make ci` 通过后，构建并推送 `v<VERSION>-<短SHA>` 与 `latest` | 是 |

要点：

- 检查与发布在**同一个** release 流水线里顺序执行，所以测试失败一定不会推送镜像，也不会移动 `latest`。
- 一次构建只 `docker build` 一次，再用 `docker tag` 派生出两个标签，所以两个标签指向同一镜像制品。
- 先推不可变版本标签、后推 `latest`：任一步失败，`latest` 仍指向最后一次成功发布。
- 推送认证只用平台注入的临时令牌 `CNB_TOKEN`（`docker login --password-stdin`，不落盘、不进日志参数），不硬编码账号密码，也没有任何模型 Key 进入 CI。PR 等不可信事件的令牌是只读的，天然无法发布。
- 版本标签不可变：每个标签对应唯一提交；后续提交只会产生新标签，不会覆盖旧标签。

### 关于 GitHub 镜像同步

`.cnb.yml` **不包含** GitHub 镜像同步流水线。它依赖上游私有密钥仓库与固定 GitHub 地址，与镜像发布无关；移除后流水线不引用本仓库之外的任何凭据。如仍需同步到 GitHub，请单独新增一条 pipeline 并自备凭据，不要塞进发布流水线。

`.cnb/settings.yml` 只影响仓库页面上 Fork / 云原生开发按钮的文案与悬浮图，**不是** Fork 权限开关，本仓库未创建该文件也不需要它。

## 测试模式与真实 Provider 模式

两者是不同性质的环境，别混用结论：

| | 测试模式 | 真实 Provider 模式 |
| --- | --- | --- |
| 触发方式 | `go test ./...` / `make test` | 启动服务并配置真实 `MINI_AGENT_LLM_BASE_URL`、`MINI_AGENT_MODEL`（可选 `MINI_AGENT_LLM_API_KEY`） |
| 模型来源 | `httptest` 服务与 Fake Provider（如 `llm/openai/client_test.go`、`httpapi/fake_test.go`、`agent/fake_test.go`） | 真实 OpenAI 兼容服务 |
| 网络 | 不触网，可在 `GOPROXY=off` 下完成 | 需要可达的服务与有效凭证 |
| 是否收费 | 不产生任何计费调用 | 按服务方计费 |
| 能证明什么 | 请求构造、响应解析、终态映射、提交边界、协议与错误码等进程内行为 | 真实模型的实际输出质量、时延与上下文长度表现 |
| 本次是否已验收 | 是，CI 与本地均可执行 | **否**，本仓库文档交付未执行真实 Provider 端到端验收 |

测试里没有「真实 Provider 开关」，也没有靠环境变量或 `build tag` 切换的联网测试：`go test ./...` 永远不会发出真实模型请求。因此不要把「测试全绿」理解为「真实 Provider 链路已验收」。

## 当前限制与未验证事项

与 [`docs/design/http-api.md`](docs/design/http-api.md) §10、[`docs/design/runtime-architecture.md`](docs/design/runtime-architecture.md) §14 保持一致：

- 真实 OpenAI 兼容 Provider 的端到端调用与验收**尚未执行**：没有配置真实 API Key，也没有发起真实模型调用；`200 + COMPLETED` 的实际链路与 `RequestTimeout = 60s` 是否够用均未验证。
- Todo 工具（`todo_create` / `todo_list` / `todo_complete`）已在 `builtin` 包实现，但**未在生产装配中注册**（`cmd/mini-agent/main.go` 的 `newRegistry` 只注册 `calculator`）；把 `session.TodoOperations` 适配成 `builtin.TodoOperations`、Todo ID 生成与状态迁移属 T08，未交付。
- 上下文压缩未启用：`TokenBudget = 0` 即不做压缩，长会话触达 Provider 上下文上限时以 `provider_failure`（502）结束，这是既定边界而非缺陷；生产 LLM `Summarizer` 与真实 tokenizer 校准同样属 T08。
- 会话是**进程内内存实现**，无持久化：进程退出即丢失，重启不恢复；`session.Manager` 未启用 TTL（`ManagerConfig{}` 的 TTL 为 0），因此会话不会自动过期。
- 未提供流式输出（SSE / WebSocket）、异步任务轮询、TLS 终结、CORS、限流/配额、metrics、用户体系与 RBAC、文件落盘的日志轮转。日志事件只写 stderr。
- `go test -race` 已进入 CI（`make ci` 的最后一个步骤）；它是否在 CNB Runner 上首次真实跑通、以及镜像首次发布，都要等改动合入 `main` 才发生，见下面三条。
- 容器化启动只验证了「进程起来 + `/healthz` 正常 + 非 root + 无 `.env` 入镜像」，**没有**发起真实 Chat Completions 调用；`/healthz` 成功不代表真实 Provider 链路可用。
- CNB 流水线（含镜像推送）**未在平台上真实运行**：本次只做了 `.cnb.yml` 的语法/结构校验与本地等价命令验证；首次真实发布需要 Owner 合入 `main` 后确认。
- Fork 仓库的「允许自动触发」与组织级开关只能由平台管理员在 CNB UI 勾选，仓库内配置无法代劳；`.cnb/settings.yml` 只影响按钮文案，与 Fork 权限无关。
- 客户端断开时响应几乎必然不可达：`cancelled`（408）只是连接仍可写时的 best-effort，**不承诺客户端实际收到该状态码**。
- `HeuristicEstimator` 的 4 字符/token 估算与真实模型 token 数的偏差未校准（对 CJK 偏低）；v1 因为不压缩，暂不影响行为。
- 仓库外 `go get cnb.cool/framework-station/mini-agent-go` 的可用性未验证。
- `session_expired` 错误码已定义，但 v1 未启用 TTL，正常运行时不会出现。

## 文档索引

| 位置 | 内容 |
| --- | --- |
| [`docs/design/runtime-architecture.md`](docs/design/runtime-architecture.md) | 长期架构事实：分层与依赖方向、数据模型、状态机、预算、失败语义、不变量、文件所有权、未验证事项 |
| [`docs/design/http-api.md`](docs/design/http-api.md) | HTTP 对外契约：三个端点、错误码域与状态码映射、取消/超时、认证与监听边界、启动关闭生命周期、配置契约 |
| [`docs/design/agent-workflow.md`](docs/design/agent-workflow.md) | 本仓库 Workflow V3 的架构说明（Agent 协作流程本身，不是 Runtime 架构） |
| [`docs/ai-assisted-development.md`](docs/ai-assisted-development.md) | AI 辅助开发记录：角色提示词与任务需求来源、逐任务「需求 → 决策 → 实现 commit」、技术决策与契约的对应关系 |
| [`AGENTS.md`](AGENTS.md) | 工程规范与 Workflow 入口 |
| `.agent/` | Workflow V3 的执行规范（`.agent/specs/`）、角色提示词（`.agent/roles/`）、任务事实（`.agent/tasks/`）、机器配置（`.agent/workflow.yaml`）、Gate 校验器（`.agent/engine/`） |

源码级 API 参考请用 `go doc`，例如：

```bash
go doc ./agent
go doc ./httpapi
```
