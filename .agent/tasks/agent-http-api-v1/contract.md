# Technical Contract — Agent Runtime HTTP JSON API（agent-http-api-v1）

## Decision Status

WAITING_FOR_OWNER_DECISION

## Problem

在 L0–L2 已交付、L3 装配层（T07）**尚不存在**的仓库上，交付一个本地可用的 HTTP JSON 接口：三个固定端点（`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`），要求错误语义稳定、默认不暴露无认证接口、取消/超时能真实穿透到 Run、服务可启动并优雅关闭，且全部自动化验收只依赖 Fake LLM。

**阻塞事实（必须先由 Owner 决策）**：本任务的两条核心 AC（AC-001 多轮历史、AC-002 工具调用后返回最终文本）必须由真实的 Agent Run 执行入口产生证据，而该入口属于 T07 装配层，当前仓库内不存在任何装配或 Loop 代码。本 Contract 因此把 HTTP 层与装配层之间固定为一个**消费方定义的窄端口**（`httpapi.Runner`），但 AC-001 / AC-002 的验证证据依赖 T07 实现该端口后才可能存在。

## Verified Current Behavior

- VERIFIED：仓库内没有任何 HTTP 服务、路由、`cmd/` 入口或装配层代码；根目录只有 `core/`、`llm/`、`tool/`、`session/`、`contextmgr/`、`runlog/`、`builtin/`、`docs/`（仓库目录清单 + `git ls-files`）。
- VERIFIED：`docs/design/runtime-architecture.md` §1 明确把「CLI / HTTP 入口」排除在范围外（第 12 行），§2 把 L3 标注为「未实现」（第 24 行）。本任务落地后这两处必须同步修正，改动集是 §1 / §2 / §3 / §12（Task 只写了 §2/§3/§12，§1 的排除项同样构成矛盾）。
- VERIFIED：`session.Manager` 提供 `Create`（重名有效会话 → `ErrSessionExists`）、`Get`（不存在 → `ErrSessionNotFound`；已过期 → `ErrSessionExpired` 并物理删除）、`RunTurn`（会话执行权内整轮原子提交；取消/超时/失败/panic 都不产生部分提交）。证据：`session/manager.go:66`、`:95`、`:188-221`、`session/errors.go:14-46`。哨兵挂在 `*core.RunError.Cause` 上，`errors.Is` 可用，`Kind` 统一为 `invalid_argument`。
- VERIFIED：`llm.Provider` 只有 `Complete(ctx, llm.Request) (llm.Response, error)`；`llm/openai.Config{BaseURL, APIKey, HTTPClient, Timeout}`，`APIKey` 为空时不发 `Authorization`（`llm/provider.go:86`、`llm/openai/client.go:41-51`、`:146-148`）。本任务不新增 Provider 实现。
- VERIFIED：仓库既有领域失败 JSON 形状为 `{"error":{"code":"<snake_case>","message":"<稳定中文说明>"}}`，且已有「字段表」解析先例（`map[string]json.RawMessage`）：`builtin/builtin.go:9-15`、`:36-46`、`:97-100`。HTTP 错误封套沿用该形状，不另造词汇。
- VERIFIED：`runlog.Sink.Record` 的失败是旁路（`runlog/runlog.go:57-60`）；HTTP 访问日志不得影响响应。
- VERIFIED：`go.mod` 为零 `require`，`go 1.24`；`make test` = `go test ./...`（`Makefile:10-11`）。`.agent/workflow.yaml` 的 `resources: {}`，本任务不申请任何 Registry 资源；`.agent/registry/` 不存在。
- VERIFIED：不存在 T07 任务目录，也不存在任何装配层分支（`.agent/tasks/` 仅 T00–T06；`git branch -a` 无装配/loop 分支）。
- UNKNOWN：T07 将暴露的构造入口与 `RunPolicy`/工具注册/`Summarizer` 适配的具体签名。本 Contract 不猜测它，改为固定 HTTP 层需要的**最小端口**（见 `Interfaces and Data`），由 T07 适配。

## Recommendation

RECOMMENDATION：HTTP 层（`httpapi`）只依赖标准库 + `core` + `session`，把「一次 Agent Run」抽象为自己定义的窄端口 `httpapi.Runner`，由 T07 装配层适配；本任务交付 handler + 服务构造 + 启动入口 + 完整协议契约，**Coder 在 T07 落地后启动**，Scope 与 AC 不变。

关键取舍：以「消费方定义端口」换取 T08 不依赖 T07 内部实现细节，代价是 AC-001 / AC-002 的真实证据必须等 T07；若 Owner 要求 T08 立即开工，唯一不削弱 AC 的路径是在 T08 内实现最小装配层（Scope 扩大，需 Owner/TaskBuilder 改 `task.md`）。Analyst 不自行扩 Scope。

## Selected Design

等待 Owner 确认（含 T07 前置选项、认证形态、超时来源、响应暴露面）。

## Interfaces and Data

### 包与文件（供 `runtime-architecture.md` §12 登记，封闭清单）

新增 L3：

| 路径 | 内容 |
| --- | --- |
| `httpapi/config.go` | `Config`、默认值、`Validate()`、脱敏 `String()` |
| `httpapi/server.go` | `New(Config, Deps)`、`http.Handler` 分派、`Serve`/`Shutdown`/`Close`/`Addr`、`BaseContext` |
| `httpapi/handler.go` | 三个端点 handler 与成功响应写出 |
| `httpapi/request.go` | 有界请求体读取 + 字段表严格解析与字段级校验 |
| `httpapi/errors.go` | 错误码封闭枚举、`code → HTTP status` 单点映射、错误封套写出 |
| `httpapi/auth.go` | Bearer 凭证常量时间校验 |
| `httpapi/run.go` | `Runner` / `RunRequest` / `RunOutcome` 与结果→响应映射 |
| `cmd/mini-agent/main.go` | 启动入口：环境变量 → `Config` → `Deps` → 信号 → 优雅关闭 |
| `httpapi/fake_test.go` | Fake `Runner` / Fake `Provider` / 捕获日志 Sink |
| `httpapi/handler_test.go` | 端点契约、多轮、错误与副作用 |
| `httpapi/errors_test.go` | 错误码↔状态码映射表与封套形状 |
| `httpapi/request_test.go` | 媒体类型、JSON、字段、大小上限 |
| `httpapi/config_test.go` | 默认回环、fail-closed、脱敏 |
| `httpapi/lifecycle_test.go` | 就绪、关闭、宽限期、取消/超时穿透 |
| `httpapi/auth_test.go` | 缺失/错误凭证一致、无泄漏 |

禁止新增清单外文件；需要拆分文件时走 Contract Revision（§12 与 `git ls-files` 差集为空的硬性 AC 依赖此清单）。

### 依赖方向

`httpapi` 只 import 标准库 + `core` + `session`；不得 import `llm` / `tool` / `builtin` / `contextmgr` / `runlog` / T07 包。`cmd/mini-agent` 允许 import `httpapi` + `session` + `llm/openai` + T07 装配包。L0–L2 不得 import 新包。

### 端口（本任务定义，T07 实现）

```go
type SessionStore interface {
    Create(ctx context.Context, id string) (session.Session, error)
    Get(ctx context.Context, id string) (session.Session, error)
}

type Runner interface {
    Run(ctx context.Context, req RunRequest) (RunOutcome, error)
}

type RunRequest struct {
    SessionID string
    RunID     string // 由 HTTP 层生成（32 位小写 hex），T07 用作 core.ExecutionRef.RunID
    Message   string
}

type RunOutcome struct {
    Status    core.RunStatus
    Reply     string        // 本轮最终助手文本；HTTP 层不回读 Session 拼装
    LLMCalls  int
    ToolCalls int
    Err       *core.RunError
}
```

`Runner.Run` 返回的 `error` 只用于「未形成 Run 结论」的基础设施/会话错误（session 哨兵、`context` 取消）；Run 已形成结论时用 `RunOutcome`（`Status` + `Err`）。`*session.Manager` 直接满足 `SessionStore`。

### 端点

`POST /v1/sessions`

- 请求：`Content-Type: application/json`（允许 `; charset=utf-8`），body 可为空（等价 `{}`）；字段表只允许 `session_id`（可选，`^[A-Za-z0-9._:-]{1,128}$`）。缺失时服务端生成 32 位小写 hex。
- 成功：`201`，body `{"session_id":"<id>"}`。
- 失败：重复且仍有效 → `409 session_exists`；其余见错误表。

`POST /v1/chat`

- 请求：字段表只允许 `session_id`（必填，同上字符集）、`message`（必填，1..8192 个 UTF-8 字符，非法 UTF-8 视为字段错误）。不修改用户文本（不做 trim / 归一化）。
- 成功：`200`，body `{"session_id":"...","run_id":"...","status":"COMPLETED","reply":"...","llm_calls":N,"tool_calls":N}`。不返回消息序列、`Raw`、Provider 原始载荷、usage、系统提示。
- 失败：见错误表；任何非 `COMPLETED` 终态都不返回 200。

`GET /v1/healthz`

- 无需认证，无请求体。成功：`200`，body `{"status":"ok"}`。不探测 Session/Provider，不输出配置、密钥、版本、会话数、内部状态。

路由语义：单入口 `ServeHTTP` 按「方法 + 精确路径」分派，不使用会自动清理路径或产生重定向的默认 `ServeMux` 语义；未知路径 → `404 not_found`，已知路径方法不符 → `405 method_not_allowed` + `Allow` 头。所有响应（含 404 / 405）都使用 JSON 错误封套。

### 错误封套与错误码（封闭枚举）

封套固定为 `{"error":{"code":"<snake_case>","message":"<固定文案>"}}`，`code` 与 `message` 都是一对一常量，不含动态内容（不回显会话 ID、字段值、路径、堆栈、Provider 载荷、凭据）。`code → status` 只在 `httpapi/errors.go` 单点实现。

| code | HTTP | 判定依据（唯一来源） |
| --- | --- | --- |
| `invalid_json` | 400 | body 不是单个合法 JSON 对象（含空 body 之外的尾部数据、数组、`null`） |
| `unknown_field` | 400 | 字段表出现契约外键 |
| `missing_field` | 400 | 必填键缺失 |
| `invalid_field` | 400 | 类型错误、长度/字符集/UTF-8 非法 |
| `unsupported_media_type` | 415 | `Content-Type` 非 `application/json` |
| `payload_too_large` | 413 | 超过 `MaxRequestBytes`（`http.MaxBytesError`） |
| `unauthorized` | 401 | 凭证缺失或错误（两者必须同码） |
| `not_found` | 404 | 未知路径 |
| `method_not_allowed` | 405 | 已知路径方法不符 |
| `session_not_found` | 404 | `errors.Is(err, session.ErrSessionNotFound)` |
| `session_expired` | 404 | `errors.Is(err, session.ErrSessionExpired)` |
| `session_exists` | 409 | `errors.Is(err, session.ErrSessionExists)` |
| `deadline_exceeded` | 504 | 请求级超时（`core.KindDeadlineExceeded` / 派生 ctx 超时） |
| `cancelled` | 408 | 客户端断开或关闭期取消（`core.KindCancelled` / ctx `Canceled`） |
| `provider_failure` | 502 | `core.KindProviderFailure` |
| `tool_failure` | 500 | `core.KindToolFailure` |
| `llm_call_limit` | 429 | `core.KindLLMCallLimit` |
| `tool_call_limit` | 429 | `core.KindToolCallLimit` |
| `invalid_pairing` | 500 | `core.KindInvalidPairing` |
| `internal` | 500 | 其余（`KindInternal`、非 `*core.RunError`、`RunOutcome` 结构自相矛盾） |

判定优先级固定：**先会话哨兵（`errors.Is`），再 `core.KindOf`，最后兜底 `internal`**；运行期 `core.KindInvalidArgument`（例如 Session 拒绝非法历史）不是客户端参数错误，一律映射 `internal`，不得映射 400。

### 配置（fail-closed）

```go
type Config struct {
    ListenAddr      string        // 默认 127.0.0.1:8080
    AuthToken       string        // 敏感
    RequestTimeout  time.Duration // 默认 60s，必须 > 0
    ShutdownGrace   time.Duration // 默认 10s，必须 > 0
    MaxRequestBytes int64         // 默认 1 MiB，必须 > 0
}
```

- `Validate()` 必须解析 `ListenAddr`；回环判定见 INV-H07。
- `Config` 必须实现 `String()`（`%v` / `%+v` / `%#v` 都不得输出明文凭证；输出 `<redacted>` 或 `<unset>`）。
- 环境变量（唯一来源，`cmd` 读取）：`MINI_AGENT_LISTEN_ADDR`、`MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_REQUEST_TIMEOUT`、`MINI_AGENT_SHUTDOWN_GRACE`、`MINI_AGENT_SESSION_TTL`（映射 `session.ManagerConfig.TTL`，默认 30m，`0` 表示不过期）、`MINI_AGENT_PROVIDER_BASE_URL`、`MINI_AGENT_PROVIDER_API_KEY`、`MINI_AGENT_MODEL`。密钥不进命令行参数、不进响应/日志/`/healthz`。Provider 侧接线由 T07 构造承担；`BASE_URL` / `MODEL` 缺失属于启动配置非法（fail-closed），`API_KEY` 允许为空（本地兼容服务）。
- HTTP 服务器超时：`ReadHeaderTimeout = 5s`、`ReadTimeout = RequestTimeout + 5s`、`WriteTimeout = RequestTimeout + 5s`、`IdleTimeout = 60s`（`WriteTimeout` 必须大于 `RequestTimeout`，否则超时响应会被截断）。

### 认证与监听

- 默认监听 `127.0.0.1:8080`（回环）。
- 配置了 `AuthToken` → 两个业务端点要求 `Authorization: Bearer <token>`；校验对双方 SHA-256 做 `subtle.ConstantTimeCompare`，拒绝其他 scheme；缺失与错误一律 `401 unauthorized`，在读取 body 与调用 `Runner` 之前判定。
- 未配置 `AuthToken` 且监听非回环 → `New`/启动返回错误，**不进入监听**（fail-closed）。
- 未配置 `AuthToken` 且监听回环 → 不要求认证（本地开发默认）。
- `/healthz` 永不要求认证，且不含任何配置信息。

### 请求处理顺序（`/v1/chat`）

1. 路由（方法 + 路径）。
2. 认证（读 body 之前）。
3. `Content-Type` 校验。
4. 有界读取 body（`http.MaxBytesReader(1 MiB)`）→ 字段表解析与字段级校验。
5. `SessionStore.Get(ctx, session_id)` 预检：会话错误在**不调用 `Runner`** 的情况下返回 `404`。
6. 生成 `run_id`；用 `context.WithTimeout(r.Context(), RequestTimeout)` 派生 Run 上下文；调用 `Runner.Run`。
7. 结果映射（会话哨兵 → `KindOf` → 兜底）。`Runner` 返回的会话哨兵必须映射成与第 5 步相同的 code（TOCTOU 防御）。

`GET /healthz` 与 `POST /v1/sessions` 不需要 Session 预检。

## Business Invariants

任务局部编号，不占用 `runtime-architecture.md` 的全局 `INV-0xx`。

- INV-H01：`200` 只在 `RunOutcome.Status == COMPLETED` 且 `Err == nil` 时返回；任何失败、取消、超时、预算耗尽、非终态或自相矛盾的 `RunOutcome` 都不得返回 200。
- INV-H02：认证先于 body 解析与 Run 执行；未认证请求的 `Runner` 调用计数为 0、会话快照逐条不变。
- INV-H03：会话错误只按哨兵分类（`errors.Is`），不得按错误文本，也不得退化为 `internal`/5xx；HTTP 层调用 `Runner` 前必须完成一次 `SessionStore.Get` 预检。
- INV-H04：HTTP 层不写会话：除 `Runner`（内部走 `RunTurn`）外没有任何 `Create/Delete/Save/Append/PutTodo` 写路径；失败、取消、超时后会话历史与 Todo 逐条不变，且历史仍通过 `core.ValidateMessages`。
- INV-H05：取消与超时不可混淆且都不得映射为 `internal`：客户端断开 → Run 的 `ctx` 被取消（Fake Provider 观察到）→ 终态/错误 `cancelled`；请求级超时 → `deadline_exceeded`；两者在错误码与终态上可区分。
- INV-H06：凭证零泄漏：注入的假密钥值不得出现在任何响应体、响应头、`/healthz`、`Config.String()` 输出与捕获的日志字节中。
- INV-H07：默认监听面是回环（`127.0.0.1` / `::1` / `localhost`）；任何非回环监听必须携带凭证，否则启动失败且不监听（`Addr()` 不可用、端口未被占用）。
- INV-H08：请求体有界：读取受 `MaxBytesReader` 限制，超限返回 `413`；服务不 panic、不挂死连接、不在内存中无界缓存。
- INV-H09：关闭语义：关闭后新连接被拒；宽限期内进入处理的请求正常返回；宽限期结束后仍在途的 Run 的 `ctx` 被取消；重复 `Shutdown`/`Close` 幂等且不 panic，进程在限定时间内退出。
- INV-H10：错误封套封闭：`code` ∈ 上表，`message` 为固定常量，响应体不含路径、堆栈、Provider 载荷、会话内文本或凭据；`code → status` 只有一处映射。

## Failure and Consistency Semantics

- **成功响应代表什么**：`200` 表示该轮 Run 以 `COMPLETED` 结束且会话提交已发生（提交先于响应写出）。`201` 表示会话已在内存中创建（无持久化）。
- **不代表什么**：不代表日志已落盘、不代表任何外部持久化、不代表客户端已收到响应。
- **响应写失败**：客户端断开导致响应不可达时，会话仍可能已提交（`RunTurn` 提交在 handler 写响应之前）；这是既定语义，监控只能依赖日志与 `cancelled` 终态，不得回滚会话。
- **会话不存在 / 过期 / 重复创建**：分别映射 `session_not_found`(404) / `session_expired`(404) / `session_exists`(409)；过期时 Session 会被物理删除，不需要 HTTP 层额外清理。
- **重复与并发**：无幂等键；同一会话的两次并发 `POST /v1/chat` 由 `RunTurn` 执行权串行，后到者排队，排队超过 `RequestTimeout` 即 `504 deadline_exceeded`（可能尚未执行任何 LLM 调用）。
- **Provider / 工具失败**：`FAILED + provider_failure` → 502；`FAILED + tool_failure` → 500；两者都不泄露原始载荷；失败路径不产生部分提交（由 `RunTurn` 保证）。
- **部分完成**：一次 Run 内部分工具成功、部分失败由 Loop 决定终态；HTTP 层只映射最终结论，不做重试、不做补偿、不写会话。
- **关闭期**：`Shutdown(ctx=ShutdownGrace)` 停止接受新连接并等待在途请求；宽限期满后取消根 context（在途 Run 收到 `cancelled`）并 `Close` 强制断开。

## Allowed / Forbidden Changes

- 允许：新增 `httpapi/*.go`、`cmd/mini-agent/main.go`（仅上表清单）；新增 `docs/design/http-api.md`；更新 `docs/design/runtime-architecture.md` 的 §1 / §2 / §3 / §12（必要时 §14 未验证项）。
- 允许：HTTP 层按本契约新增 `httpapi` 内的类型、常量与错误码；`core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `builtin` 保持零改动。
- 禁止：修改 L0–L2 公共契约与既有测试；修改 `go.mod`（module / go 版本 / `require`）、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）；引入第三方依赖（路由、校验、断言库）。
- 禁止：实现 T07 装配层、Agent Loop、Todo 适配、生产 `Summarizer`；SSE/WS、异步轮询、TLS、CORS、限流/配额、metrics、持久化、用户体系与 RBAC。
- 禁止：新增清单外文件；新增本契约未列的错误码或状态码（客户端兼容约束，需 Contract Revision）。

## Verification Requirements

- INV-H01 → `httpapi/handler_test.go`：Fake `Runner` 表驱动返回各终态/错误，断言仅 `COMPLETED` 得 200，其余状态码与 code 精确匹配。
- INV-H02 → `httpapi/auth_test.go`：缺失/错误凭证两例，断言 401 + 相同 code + `Runner` 调用计数 0 + `SessionStore` 无写入。
- INV-H03 / 会话错误 → `httpapi/handler_test.go`：真实 `session.Manager`（含 TTL 注入时钟），断言未知 → `session_not_found`、过期 → `session_expired`、重复创建 → `session_exists`，且不调用 `Runner`。
- INV-H04 → `handler_test.go`：每例前后 `Manager.Get` 深比对消息与 Todo 逐条一致，且 `core.ValidateMessages` 通过。
- INV-H05 → `httpapi/lifecycle_test.go`：阻塞到 `ctx.Done()` 的 Fake `Runner`/Fake `Provider`；客户端 `context.WithCancel` 断开 → 断言观察到取消、无部分提交；短 `RequestTimeout` → 断言 `504 deadline_exceeded`，并与取消在 code/终态上可区分。
- INV-H06 → `auth_test.go` + `config_test.go`：响应体/响应头/`/healthz`/日志字节子串扫描假密钥；`fmt.Sprintf("%+v", cfg)` 不含明文。
- INV-H07 → `config_test.go`：默认配置主机为回环；`0.0.0.0:0` / `:0` + 无 token → `New` 返回错误且未监听；`0.0.0.0` + token → 允许。
- INV-H08 → `httpapi/request_test.go`：`MaxRequestBytes+1` 的 body → 413；`Content-Type` 变体 → 415；坏 JSON / 未知字段 / 缺字段 / 类型错 / 超长 message → 各自 code；`httptest` 断言不 panic。
- INV-H09 → `lifecycle_test.go`：`127.0.0.1:0` 启动后轮询 `/healthz` 就绪；关闭后新连接被拒；在途请求在宽限期内返回；宽限期后 Run 被取消；重复 `Close` 不 panic。
- INV-H10 → `httpapi/errors_test.go`：遍历封闭枚举断言 code→status 一一对应；错误体只含 `error.code` / `error.message` 两个键；所有 code 都在封套中被覆盖。
- AC-001 / AC-002 → **依赖 T07**：T07 实现 `httpapi.Runner` 后，用真实 Runner + Fake `llm.Provider` 在 `handler_test.go` 补充多轮历史与工具调用端到端用例；T07 未落地前标记 `NOT_VERIFIED`（阻塞项）。
- AC-008 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对；`§12` 路径与 `git ls-files` 差集为空；`go list` 分层核对（`httpapi` 不含 `llm/tool/builtin/contextmgr/runlog`，L0–L2 不含新包）；`git diff go.mod` 为空；`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 退出码 0。

## Open Risks

- **T07 缺失（阻塞）**：AC-001 / AC-002 在 T07 落地前无法产生真实证据；`Runner` 端口只是把边界固定下来，不等于已有编排实现。
- `cancelled → 408` 与 `deadline_exceeded → 504` 是契约约定映射：RFC 的 408 更偏「接收请求超时」，客户端断开时响应通常不可达；可区分性主要落在终态与日志上。
- `llm_call_limit` / `tool_call_limit → 429` 复用了限流语义，实际含义是「本次 Run 预算耗尽」，重试不会自愈。
- 同一会话并发 `chat` 排队可能让后到请求在未调用 LLM 前就 `504`，客户端需要能区分「排队超时」与「模型慢」（当前只能用 `RequestTimeout` 与日志推断）。
- JSON 重复键按标准库行为「后者覆盖」，契约不拒绝（不做 token 级扫描）。
- `Shutdown` 宽限期满后的强制 `Close` 会截断正在写出的响应；需在 `docs/design/http-api.md` 中写明。
- Provider 侧接线细节（T07 构造入口、`RunPolicy`、工具注册、`Summarizer`）仍未知；`cmd` 的环境变量集合可能随 T07 Contract 微调，需 Contract Revision。

## Owner Decision Record

等待 Owner 确认。待决问题：

1. **T07 前置**（阻塞）：(A) 推荐 —— T08 等 T07 落地后启动 Coder，Scope/AC 不变；(B) T08 内实现最小装配层（扩 Scope，需改 `task.md`）；(C) 缩减 AC-001/AC-002 到 Fake Runner 级（降级验收）。
2. **认证形态**：推荐单一静态 Bearer Token（`MINI_AGENT_AUTH_TOKEN`，常量时间比较，缺失/错误同 401）；非回环 + 无 token → 启动失败。
3. **请求级超时来源**：推荐服务端全局 `RequestTimeout`（默认 60s），不开放客户端参数，避免客户端自行延长或绕过。
4. **响应暴露面**：推荐只返回 `reply` + `llm_calls` + `tool_calls`（不返回消息序列 / `Raw` / usage）。
