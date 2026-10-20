# Technical Contract — Agent Runtime HTTP JSON API（agent-http-api-v1）

## Decision Status

WAITING_FOR_OWNER_DECISION

Revision R1（2026-10-20）：T07 `agent-runtime-loop-v1` 已交付并合入 `develop`（`6bebdda`），草案 `870056e` 的「T07 不存在」前提作废。阻塞点从「T07 缺失」变为「T07 只交付 `agent.Loop`，没有把用户消息放进同一次 `RunTurn` 的接缝」。

## Problem

交付本地可用的 HTTP JSON 接口：`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`；错误语义稳定、默认不暴露无认证接口、取消/超时穿透到 Run、可启动并优雅关闭，全部自动化验收只用 Fake LLM。

真正阻塞实现的设计问题有三个（均有代码证据）：

1. **用户消息与 Run 不在同一 Turn（阻塞 AC-001/AC-002/AC-003/AC-006）**：`agent.Loop.Run(ctx, RunRequest)` 只接受 `ExecutionRef` + `RunPolicy`，**不接受用户消息**（`agent/agent.go:63-67`、`:100`），它在内部自己调用 `Sessions.RunTurn` 取得会话执行权（`agent/agent.go:115`）。HTTP 层若先用 `Manager.Save`/`RunTurn` 提交用户消息再调 `Loop.Run`，失败/取消会留下「只有 user 消息」的半轮历史；若在外层 `RunTurn` 内再调 `Loop.Run`，则重入同一会话执行权自死锁（`session/turn.go:14` 明确禁止，`session/manager.go:188-201` 为容量 1 信号量）。因此 HTTP 层与 Loop 之间必须补一个「同 Turn 装配」接缝。
2. **分支基线早于 T07**：当前 `feat/agent-http-api-v1` HEAD `870056e` 基于 `dc734d2`，不含 `agent/` 包；`develop` 已到 `6bebdda`。Coder 在此分支上无法 import `agent`，也无法编译任何真实装配。
3. **装配缺口无人认领**：T07 Contract 把「Todo 适配 + 生产 `Summarizer`」归给「服务装配任务 T08」（`.agent/tasks/agent-runtime-loop-v1/contract.md` D4），而本任务 `task.md` 的 Out of Scope 明确排除这两项并按旧编号称之为「T07 装配层」。`contextmgr.NewManager` 要求非 nil `Summarizer`（`contextmgr/manager.go:32-39`），`builtin` todo 工具需要逐 Turn 的 `TodoOperations`（`builtin/todo.go:22-26`、`:71`）。编号与归属存在冲突，必须由 Owner 裁定。

## Verified Current Behavior

- VERIFIED：`develop`（`6bebdda`）已有 L3 包 `agent`：`agent.New(Config) (*Loop, error)`、`(*Loop).Run(ctx, RunRequest) core.RunResult`；`RunRequest = {core.ExecutionRef; Policy core.RunPolicy}`（`agent/agent.go:63-67`、`:74`、`:100`）。
- VERIFIED：`agent.Config.Sessions` 是**可注入接口** `SessionRunner{RunTurn(ctx, sessionID, fn)}`（`agent/agent.go:34-37`、`:43`），`*session.Manager` 结构匹配。这是不修改 T07 就能插入同 Turn 行为的唯一公开接缝。
- VERIFIED：`Loop.Run` 内部只经一次 `l.cfg.Sessions.RunTurn(...)` 完成整轮执行与提交（`agent/agent.go:115`、`agent/run.go` 提交段），提交语义依赖 `session.Manager.RunTurn`：`ctx` 未取消 + 回调 nil + Turn 未关闭 + entry 仍是当前 + 提交前校验通过才整轮发布（`session/manager.go:188-221`）。
- VERIFIED：嵌套 `RunTurn` 会自死锁，且是 Session 包显式禁止的用法（`session/turn.go:14`；执行权为容量 1 的 channel，`session/manager.go:375-379`）。
- VERIFIED：`Turn.Session()` 返回「已提交基线 + 本 Turn 暂存」深拷贝（`session/turn.go:55-69`），因此在外层 Turn 内先 `Append` 用户消息，`Loop` 读到的最新历史包含该消息。
- VERIFIED：`session.Manager` 提供 `Create`/`Get`/`Delete`/`PurgeExpired`/`RunTurn`/`Load`/`Save`；会话哨兵 `ErrSessionNotFound`/`ErrSessionExpired`/`ErrSessionExists` 挂在 `*core.RunError.Cause` 上，`errors.Is` 可用，`Kind` 均为 `invalid_argument`（`session/errors.go`、`session/manager.go:66`、`:95`、`:359-392`）。
- VERIFIED：`core` 错误种类齐全（`invalid_argument`/`invalid_pairing`/`cancelled`/`deadline_exceeded`/`llm_call_limit`/`tool_call_limit`/`tool_failure`/`provider_failure`/`internal`），`core.KindOf` / `core.StatusOf` 可用，`RunResult.Validate` 逐状态约束（`core/error.go:12-30`、`:92-120`、`core/run.go:80+`）。
- VERIFIED：`RunResult.Messages` 只含本 Run 产出（assistant 与 tool 消息），COMPLETED 时不保证文本非空（T07 D5-6 允许空回答）；`core.Message.ID` 不参与校验，`ContentPart.Type` 只接受 `text`（`core/message.go:53-95`）。
- VERIFIED：`contextmgr.NewManager(estimator, summarizer)` 两者必须非 nil；`TokenBudget <= 0` 时 `Build` 直接返回不压缩，`Summarizer` 不会被调用（`contextmgr/manager.go:32-39`、`:72`）。仓库内没有生产 `Summarizer` 实现。
- VERIFIED：`builtin` 三个 todo 工具在构造时绑定 `TodoOperations`（`builtin/todo.go:71`、`:131`、`:189`），而 `agent.Config.Registry` 在 Loop 构造时固定，因此 todo 工具需要逐 Turn 构造 registry；本任务不实现该适配。
- VERIFIED：`tool.NewMapRegistry()`、`builtin.NewCalculator()`、`runlog.NewJSONSink(w)`、`llm/openai.NewClient(Config{BaseURL, APIKey, HTTPClient, Timeout})` 均存在；`APIKey` 为空时不发 `Authorization`。
- VERIFIED：`go.mod` 零 `require`、`go 1.24`；`make test` = `go test ./...`；`.agent/workflow.yaml` 的 `resources: {}`，本任务不申请任何 Registry 资源（无 migration / 错误码域）。
- VERIFIED：`git status --short` 为空；`feat/agent-http-api-v1` 与 `origin/feat/agent-http-api-v1` 同为 `870056e`；`origin/develop` = `6bebdda`，`feat/agent-http-api-v1` 落后且非 ff 祖先关系。
- UNKNOWN：Owner 希望如何把 T07 合入本 feature（合并 `develop` / 重建分支），以及本任务是否承担最小装配层——两者都改变基线或 Scope，需 Owner 决定。

## Recommendation

RECOMMENDATION：三层交付，**不改 T07、不改 L0–L2**：

1. `httpapi`：纯协议层（`Config`/路由/handler/请求解析/错误封套/认证/`Runner` 端口），只依赖标准库 + `core` + `session`。
2. `service`：最小装配层，实现 `httpapi.Runner`——**一次** `Manager.RunTurn` 内先 `turn.Append(用户消息)`，再以注入 `agent.New` 的 per-Run `SessionRunner` 调用 `Loop.Run`（该适配器持有当前 `*session.Turn`，被 Loop 回调时直接执行 `fn(turn)`，不再向 Manager 取执行权）；Run 非 `COMPLETED` 时让外层回调返回该错误，整轮丢弃。
3. `cmd/mini-agent`：环境变量 → `httpapi.Config` + `service.Config` + Deps → 监听 → 信号 → 优雅关闭。

关键取舍：以「T08 内 ~40 行同 Turn 适配」换取 T07 公共 API 零改动；代价是装配层承担一条必须用测试钉死的不变量（INV-H11：一次 chat 恰好一次 `Manager.RunTurn`、无死锁、无半轮提交）。替代方案是给 T07 增 `Loop.RunTurn(ctx, turn, req)` 入口（语义最干净），但需 T07 Contract Revision + 二次实现/审查/合并，排期成本明显更高。

## Selected Design

等待 Owner 确认（D1 装配接缝归属、D2 装配缺口处置、D3 分支基线、D4 协议三选、D5 Design 落点）。

## Interfaces and Data

### 包与文件（供 `runtime-architecture.md` §12 登记，封闭清单）

| 路径 | 内容 |
| --- | --- |
| `httpapi/config.go` | `Config`、默认值、`Validate()`、脱敏 `String()` |
| `httpapi/server.go` | `New(Config, Deps)`、`ServeHTTP` 分派、`Serve`/`Shutdown`/`Close`/`Addr`、`BaseContext` |
| `httpapi/handler.go` | 三个端点 handler 与成功响应写出 |
| `httpapi/request.go` | 有界请求体读取 + 字段表严格解析与字段级校验 |
| `httpapi/errors.go` | 错误码封闭枚举、`code → HTTP status` 单点映射、错误封套写出 |
| `httpapi/auth.go` | Bearer 凭证常量时间校验 |
| `httpapi/run.go` | `Runner` / `RunRequest` / `RunOutcome` 端口与结果→响应映射 |
| `service/runner.go` | `Deps` / `Config` / `NewRunner` / `Run`（一次 RunTurn + Append + per-Run Loop + reply 提取） |
| `service/turn.go` | 同 Turn `agent.SessionRunner` 适配器与用户消息构造 |
| `cmd/mini-agent/main.go` | 环境变量 → Config/Deps → 监听 → 信号 → 优雅关闭；`run(...)` 可测入口 |
| `httpapi/fake_test.go` | Fake `Runner` / 捕获日志 Sink / 会话快照辅助 |
| `httpapi/handler_test.go` | 端点契约、多轮、错误与副作用 |
| `httpapi/errors_test.go` | 错误码↔状态码映射表与封套形状 |
| `httpapi/request_test.go` | 媒体类型、JSON、字段、大小上限 |
| `httpapi/config_test.go` | 默认回环、fail-closed、脱敏 |
| `httpapi/lifecycle_test.go` | 就绪、关闭、宽限期、取消/超时穿透 |
| `httpapi/auth_test.go` | 缺失/错误凭证一致、无泄漏 |
| `service/runner_test.go` | 真实 `agent.Loop` + Fake Provider/Tool + 真实 `session.Manager`：多轮历史、工具链路、无半轮提交、恰好一次 RunTurn |
| `cmd/mini-agent/main_test.go` | 启动/就绪/非法配置 fail-closed/关闭幂等（注入 getenv） |

禁止新增清单外文件；需要拆分走 Contract Revision（AC-008 的 §12 差集校验依赖此清单）。

### 依赖方向

- `httpapi` → 标准库 + `core` + `session`；不得 import `agent` / `llm` / `tool` / `builtin` / `contextmgr` / `runlog` / `service`。
- `service` → `httpapi`（端口类型）+ `agent` + `core` + `session` + `tool` + `contextmgr` + `runlog` + `llm`；不得 import `builtin` / `llm/openai`（具体实现由 `cmd` 注入）。
- `cmd/mini-agent` → `httpapi` + `service` + `session` + `tool` + `contextmgr` + `runlog` + `llm/openai` + `builtin`。
- L0–L2 与 `agent` 不得 import 新包。

### 端口（`httpapi` 定义，`service` 实现）

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
    RunID     string // HTTP 层生成（32 位小写 hex）
    Message   string
}

type RunOutcome struct {
    Status    core.RunStatus
    Reply     string // 本轮最终助手文本；HTTP 层不回读 Session
    LLMCalls  int
    ToolCalls int
    Err       *core.RunError
}
```

`Runner.Run` 返回的 `error` 只用于「未形成 Run 结论」的会话/基础设施错误（`session` 哨兵、`ctx` 终态）；Run 已形成结论时用 `RunOutcome`。`*session.Manager` 直接满足 `SessionStore`。

### 装配语义（`service`，本任务最关键的实现约束）

```go
func (r *Runner) Run(ctx context.Context, req httpapi.RunRequest) (httpapi.RunOutcome, error) {
    var res core.RunResult
    var runErr error
    err := r.sessions.RunTurn(ctx, req.SessionID, func(turn *session.Turn) error {
        if e := turn.Append(userMessage(req.Message)); e != nil { runErr = asRunError(e); return e }
        loop, e := agent.New(r.loopConfig(turn, req.SessionID))
        if e != nil { runErr = asRunError(e); return e }
        res = loop.Run(ctx, agent.RunRequest{
            ExecutionRef: core.ExecutionRef{SessionID: req.SessionID, RunID: req.RunID},
            Policy:       r.cfg.Policy(),
        })
        if res.Status != core.RunStatusCompleted { runErr = res.Error; return runErrorOf(res) }
        return nil
    })
    ...
}
```

- `loopConfig` 每次 Run 构造，`Sessions` 为 `turnRunner{sessions: r.sessions, turn: turn, sessionID: sid}`；`turnRunner.RunTurn(ctx, id, fn)` 在 `id == sessionID` 时直接 `return fn(turn)`，否则回落到 `r.sessions.RunTurn`。
- `Append`/`agent.New`/Run 失败都必须让外层回调返回非 nil 错误 → 整轮丢弃（用户消息不落库）。
- 会话不存在/过期/被删除：`RunTurn` 在回调执行前返回哨兵，`Run` 原样透传（`errors.Is` 可判）。
- `reply`：取 `res.Messages` 中**最后一条** `Role == core.Role("assistant")` 的 `Content` 文本拼接；没有则 `""`（COMPLETED 允许空回答）。
- 用户消息：`core.Message{Role: core.Role("user"), Content: []core.ContentPart{{Type: core.ContentType("text"), Text: req.Message}}}`，不改写用户文本，不生成业务 ID。

### 端点

`POST /v1/sessions`

- 请求：`Content-Type: application/json`（允许 `; charset=utf-8`），body 可为空（等价 `{}`）；字段表只允许 `session_id`（可选，`^[A-Za-z0-9._:-]{1,128}$`）。缺失时服务端生成 32 位小写 hex。
- 成功：`201`，body `{"session_id":"<id>"}`。
- 失败：重复且仍有效 → `409 session_exists`；其余见错误表。

`POST /v1/chat`

- 请求：字段表只允许 `session_id`（必填，同上字符集）、`message`（必填，1..8192 个 UTF-8 字符，非法 UTF-8 视为字段错误）。不 trim、不归一化。
- 成功：`200`，body `{"session_id":"...","run_id":"...","status":"COMPLETED","reply":"...","llm_calls":N,"tool_calls":N}`。不返回消息序列、`Raw`、Provider 载荷、usage、系统提示。
- 失败：见错误表；任何非 `COMPLETED` 终态都不返回 200。

`GET /healthz`

- 无需认证，无请求体。成功：`200`，body `{"status":"ok"}`。不探测 Session/Provider，不输出配置、密钥、版本、会话数、内部状态。

路由：单入口 `ServeHTTP` 按「方法 + 精确路径」分派，不使用会清理路径或重定向的默认 `ServeMux` 语义；未知路径 → `404 not_found`，已知路径方法不符 → `405 method_not_allowed` + `Allow`。所有响应（含 404/405）都是 JSON 错误封套。

### 错误封套与错误码（封闭枚举）

封套固定 `{"error":{"code":"<snake_case>","message":"<固定文案>"}}`，`code`/`message` 一对一常量，不含动态内容。`code → status` 只在 `httpapi/errors.go` 单点实现。

| code | HTTP | 判定依据（唯一来源） |
| --- | --- | --- |
| `invalid_json` | 400 | body 不是单个合法 JSON 对象（尾部数据、数组、`null`） |
| `unknown_field` | 400 | 字段表出现契约外键 |
| `missing_field` | 400 | 必填键缺失 |
| `invalid_field` | 400 | 类型错误、长度/字符集/UTF-8 非法 |
| `unsupported_media_type` | 415 | `Content-Type` 非 `application/json` |
| `payload_too_large` | 413 | 超过 `MaxRequestBytes`（`http.MaxBytesError`） |
| `unauthorized` | 401 | 凭证缺失或错误（必须同码） |
| `not_found` | 404 | 未知路径 |
| `method_not_allowed` | 405 | 已知路径方法不符 |
| `session_not_found` | 404 | `errors.Is(err, session.ErrSessionNotFound)` |
| `session_expired` | 404 | `errors.Is(err, session.ErrSessionExpired)` |
| `session_exists` | 409 | `errors.Is(err, session.ErrSessionExists)` |
| `deadline_exceeded` | 504 | `core.KindDeadlineExceeded` / 派生 ctx 超时 |
| `cancelled` | 408 | `core.KindCancelled` / ctx `Canceled` |
| `provider_failure` | 502 | `core.KindProviderFailure` |
| `tool_failure` | 500 | `core.KindToolFailure` |
| `llm_call_limit` | 429 | `core.KindLLMCallLimit` |
| `tool_call_limit` | 429 | `core.KindToolCallLimit` |
| `invalid_pairing` | 500 | `core.KindInvalidPairing` |
| `internal` | 500 | 其余（`KindInternal`、非 `*core.RunError`、`RunOutcome` 自相矛盾） |

判定优先级固定：**先会话哨兵（`errors.Is`），再 `core.KindOf`，最后兜底 `internal`**；运行期 `KindInvalidArgument` 一律 `internal`，不得映射 400。

### 配置（fail-closed）

```go
type Config struct { // httpapi
    ListenAddr      string        // 默认 127.0.0.1:8080
    AuthToken       string        // 敏感
    RequestTimeout  time.Duration // 默认 60s，必须 > 0
    ShutdownGrace   time.Duration // 默认 10s，必须 > 0
    MaxRequestBytes int64         // 默认 1 MiB，必须 > 0
}

type Config struct { // service
    MaxLLMCalls  int           // 默认 8，必须 > 0
    MaxToolCalls int           // 默认 8，必须 > 0
    SystemPrompt string
    Model        string
    TokenBudget  int           // 默认 0（不压缩）
    KeepRecent   int           // 默认 0，必须 >= 0
    ToolTimeout  time.Duration // 默认 30s
}
```

- `httpapi.Config.Validate()` 必须解析 `ListenAddr`；`Config` 实现脱敏 `String()`（`%v`/`%+v`/`%#v` 都不输出明文凭证，输出 `<redacted>` / `<unset>`）。
- 环境变量（唯一来源，`cmd` 读取）：`MINI_AGENT_LISTEN_ADDR`、`MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_REQUEST_TIMEOUT`、`MINI_AGENT_SHUTDOWN_GRACE`、`MINI_AGENT_MAX_REQUEST_BYTES`、`MINI_AGENT_SESSION_TTL`（→ `session.ManagerConfig.TTL`，默认 30m，`0` 表示不过期）、`MINI_AGENT_PROVIDER_BASE_URL`（必填）、`MINI_AGENT_PROVIDER_API_KEY`（允许为空）、`MINI_AGENT_MODEL`（必填）、`MINI_AGENT_SYSTEM_PROMPT`、`MINI_AGENT_MAX_LLM_CALLS`、`MINI_AGENT_MAX_TOOL_CALLS`、`MINI_AGENT_TOKEN_BUDGET`、`MINI_AGENT_KEEP_RECENT`、`MINI_AGENT_TOOL_TIMEOUT`。
- 密钥只从环境读取，不进命令行参数、不进响应/日志/`/healthz`。
- 服务器超时：`ReadHeaderTimeout=5s`、`ReadTimeout=RequestTimeout+5s`、`WriteTimeout=RequestTimeout+5s`、`IdleTimeout=60s`（`WriteTimeout` 必须大于 `RequestTimeout`）。
- v1 装配限制（D2 待 Owner 确认）：`TokenBudget=0` 不启用压缩（`Summarizer` 不会被调用），registry 不注册 todo 工具（`cmd` 只注册 `builtin.NewCalculator()`）。

### 认证与监听

- 默认监听 `127.0.0.1:8080`（回环）。
- 配置了 `AuthToken` → 两个业务端点要求 `Authorization: Bearer <token>`；双方 SHA-256 后 `subtle.ConstantTimeCompare`，拒绝其他 scheme；缺失与错误一律 `401 unauthorized`，在读取 body 与调用 `Runner` 之前判定。
- 未配置 `AuthToken` 且监听非回环 → `New`/启动返回错误，**不进入监听**（fail-closed）。
- 未配置 `AuthToken` 且监听回环 → 不要求认证。
- `/healthz` 永不要求认证，且不含任何配置信息。

### 请求处理顺序（`/v1/chat`）

1. 路由（方法 + 路径）。
2. 认证（读 body 之前）。
3. `Content-Type` 校验。
4. 有界读取 body（`http.MaxBytesReader` 1 MiB）→ 字段表解析与字段级校验。
5. `SessionStore.Get(ctx, session_id)` 预检：会话错误在不调用 `Runner` 的情况下返回 404。
6. 生成 `run_id`；`context.WithTimeout(r.Context(), RequestTimeout)` 派生 Run 上下文；调用 `Runner.Run`。
7. 结果映射（会话哨兵 → `KindOf` → 兜底）。`Runner` 返回的会话哨兵必须映射成与第 5 步相同的 code（TOCTOU 防御）。

`GET /healthz` 与 `POST /v1/sessions` 不需要 Session 预检。

## Business Invariants

任务局部编号，不占用 `runtime-architecture.md` 的全局 `INV-0xx`。

- INV-H01：`200` 只在 `RunOutcome.Status == COMPLETED` 且 `Err == nil` 时返回；失败、取消、超时、预算耗尽、非终态、自相矛盾的 `RunOutcome` 一律不得 200。
- INV-H02：认证先于 body 解析与 Run 执行；未认证请求 `Runner` 调用计数为 0、会话快照逐条不变。
- INV-H03：会话错误只按哨兵分类（`errors.Is`），不得按错误文本，不得退化为 `internal`/5xx；HTTP 层调用 `Runner` 前必须完成一次 `SessionStore.Get` 预检。
- INV-H04：HTTP 层不写会话；`service` 的唯一写路径是 Run 所在的那次 `RunTurn`（`Append` 用户消息 + Loop 提交）。失败、取消、超时后会话历史与 Todo 逐条不变，历史仍通过 `core.ValidateMessages`。
- INV-H05：取消与超时不可混淆且都不得映射 `internal`：客户端断开 → Run `ctx` 取消（Fake Provider 观察到）→ `cancelled`；请求级超时 → `deadline_exceeded`；两者在错误码或终态上可区分。
- INV-H06：凭证零泄漏：注入的假密钥值不得出现在响应体、响应头、`/healthz`、`Config.String()` 与捕获的日志字节中。
- INV-H07：默认监听面是回环（`127.0.0.1`/`::1`/`localhost`）；任何非回环监听必须携带凭证，否则启动失败且不监听（`Addr()` 不可用、端口未占用）。
- INV-H08：请求体有界：读取受 `MaxBytesReader` 限制，超限 413；不 panic、不挂死、不无界缓存。
- INV-H09：关闭语义：关闭后新连接被拒；宽限期内进入处理的请求正常返回；宽限期满后仍在途 Run 的 `ctx` 被取消；重复 `Shutdown`/`Close` 幂等不 panic，进程限时退出。
- INV-H10：错误封套封闭：`code` ∈ 上表，`message` 为固定常量，响应体不含路径、堆栈、Provider 载荷、会话文本或凭据；`code → status` 只有一处映射。
- INV-H11：一次 `POST /v1/chat` 恰好产生一次 `session.Manager.RunTurn`：用户消息与 Run 产出在同一 Turn 提交或整轮回滚；任何路径都不出现「只有 user 消息」「重复 user 消息」或半轮工具调用。
- INV-H12：`service` 不重入同一会话执行权：Run 期间不出现第二次 `Manager.RunTurn`（同会话并发 chat 由外层 `RunTurn` 串行），任何路径都不死锁，等待超过 `RequestTimeout` 时以 `deadline_exceeded` 返回。

## Failure and Consistency Semantics

- **成功代表什么**：`200` 表示该轮 Run `COMPLETED` 且会话提交已发生（提交先于响应写出）；`201` 表示会话已在进程内存创建（无持久化）。
- **不代表什么**：不代表日志已落盘、不代表任何外部持久化、不代表客户端已收到响应。
- **响应写失败**：客户端断开导致响应不可达时会话仍可能已提交（`RunTurn` 提交在写响应之前）；这是既定语义，只能靠日志与终态观察，不回滚会话。
- **会话不存在 / 过期 / 重复创建**：`session_not_found`(404) / `session_expired`(404) / `session_exists`(409)；过期时 Session 自己物理删除，HTTP 层不清理。
- **重复与并发**：无幂等键；同一会话两次并发 chat 由外层 `RunTurn` 串行，后到者排队，排队超过 `RequestTimeout` 即 `504 deadline_exceeded`（可能尚未执行任何 LLM 调用）。重复提交同一条消息会各自成为一轮（无去重）。
- **Provider / 工具失败**：`FAILED + provider_failure` → 502；`FAILED + tool_failure` → 500；都不泄漏原始载荷；失败路径不产生部分提交（外层回调返回错误）。
- **部分完成**：一次 Run 内部分工具成功、部分失败由 Loop 决定终态；`service`/HTTP 只映射最终结论，不重试、不补偿、不写会话。
- **关闭期**：`Shutdown(ctx=ShutdownGrace)` 停止接受新连接并等待在途请求；宽限期满后取消根 context（在途 Run 收到 `cancelled`）再 `Close` 强制断开，可能截断正在写出的响应。

## Allowed / Forbidden Changes

- 允许：新增上表清单内的 `httpapi/*`、`service/*`、`cmd/mini-agent/*`；新增 `docs/design/http-api.md`；更新 `docs/design/runtime-architecture.md` §1 / §2 / §3 / §12（必要时 §14 归属）。
- 允许：`httpapi` / `service` 内部新增类型、常量与错误码；`core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `builtin` / `agent` 保持零改动。
- 禁止：修改 L0–L3 公共契约与既有测试；修改 `go.mod`、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）；引入第三方依赖。
- 禁止：实现 Agent Loop、Todo 端口适配、生产 `Summarizer`；SSE/WS、异步轮询、TLS、CORS、限流/配额、metrics、持久化、用户体系与 RBAC。
- 禁止：新增清单外文件；新增契约未列的错误码或状态码（需 Contract Revision）。

## Verification Requirements

- INV-H01 → `httpapi/handler_test.go`：Fake `Runner` 表驱动各终态/错误，断言仅 `COMPLETED` 得 200。
- INV-H02 → `httpapi/auth_test.go`：缺失/错误凭证，断言 401 + 同 code + `Runner` 计数 0 + 无会话写入。
- INV-H03 → `httpapi/handler_test.go`：真实 `session.Manager`（TTL 注入时钟），未知 → `session_not_found`、过期 → `session_expired`、重复创建 → `session_exists`，且不调用 `Runner`。
- INV-H04 → `handler_test.go`：每例前后 `Manager.Get` 深比对消息与 Todo 逐条一致，`core.ValidateMessages` 通过。
- INV-H05 → `httpapi/lifecycle_test.go` + `service/runner_test.go`：阻塞到 `ctx.Done()` 的 Fake Provider；客户端取消 → 观察到取消、无部分提交；短 `RequestTimeout` → `504 deadline_exceeded`，与取消可区分。
- INV-H06 → `auth_test.go` + `config_test.go`：响应体/头/`/healthz`/日志字节子串扫描假密钥；`fmt.Sprintf("%+v", cfg)` 不含明文。
- INV-H07 → `config_test.go`：默认主机回环；`0.0.0.0:0` / `:0` + 无 token → `New` 返回错误且未监听；`0.0.0.0` + token → 允许。
- INV-H08 → `request_test.go`：`MaxRequestBytes+1` → 413；`Content-Type` 变体 → 415；坏 JSON/未知字段/缺字段/类型错/超长 message → 各自 code；`httptest` 断言不 panic。
- INV-H09 → `lifecycle_test.go` + `cmd/mini-agent/main_test.go`：`127.0.0.1:0` 启动后轮询 `/healthz` 就绪；关闭后新连接被拒；在途请求宽限期内返回；宽限期后 Run 被取消；重复 `Close` 不 panic。
- INV-H10 → `httpapi/errors_test.go`：遍历封闭枚举断言 code→status 一一对应；错误体只含 `error.code`/`error.message`。
- INV-H11 / INV-H12 → `service/runner_test.go`：包一层计数 `RunTurn` 的 Manager 断言每次 chat 恰好 1 次；真实 `agent.Loop` + Fake Provider 两步脚本（tool_calls → 文本）断言历史为 `user, assistant(tool_calls), tool, assistant` 且无重复 user；Fake Provider 失败/取消后历史与 Todo 与请求前逐条一致；同会话两个并发 chat 串行完成、无死锁（`-race`）。
- AC-001 / AC-002 → `service/runner_test.go`（真实 Loop）+ `httpapi/handler_test.go`（经真实 Runner 的端到端）。
- AC-008 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对；§12 路径与 `git ls-files` 差集为空；`go list` 分层核对；`git diff go.mod` 为空；`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 退出码 0。

## Open Risks

- **装配层依赖 T07 的公开行为**：同 Turn 适配器成立的前提是「`Loop.Run` 的全部会话交互都经由注入的 `SessionRunner.RunTurn`，且只调用一次」。若 T07 后续改变该调用模式，本适配器需同步复审（用 `service/runner_test.go` 的计数断言做护栏）。
- **T07 编号冲突**：T07 Contract 把「Todo 适配 + 生产 `Summarizer`」记为「T08 服务装配」，与本任务（同号、不同范围）重叠；若 Owner 不裁定，该缺口会长期无人负责。
- **v1 无上下文压缩**：`TokenBudget=0`，长会话触达 Provider 上限时以 `provider_failure`/502 结束；真实 tokenizer 校准与生产 `Summarizer` 仍缺。
- **无 todo 工具**：`todo_create` / `todo_list` / `todo_complete` 在 v1 服务中不可用（需逐 Turn registry，属装配缺口）。
- `cancelled → 408`、`deadline_exceeded → 504` 是契约约定映射：客户端断开时响应通常不可达，可区分性主要落在终态与日志。
- `llm_call_limit` / `tool_call_limit → 429` 复用限流语义，实际是「本 Run 预算耗尽」，重试不自愈。
- 同会话并发 chat 排队可能让后到请求在未调用 LLM 前就 504，客户端无法区分「排队超时」与「模型慢」。
- JSON 重复键按标准库「后者覆盖」，契约不拒绝（不做 token 级扫描）。
- 基线未更新前 Coder 不可启动；分支合并方式属 Owner 权限。

## Owner Decision Record

等待 Owner 确认。待决问题：

1. **D1 装配接缝归属（阻塞）**：(A) 推荐——本任务新增最小装配层 `service`，不改 T07；(B) 给 T07 增 in-turn 入口，需 T07 Contract Revision + 二次交付；(C) 本任务只用 Fake `Runner`，AC-001/AC-002 降级为端口级验证（需改 Task/AC）。
2. **D2 装配缺口处置**：(A) 推荐——v1 接受 `TokenBudget=0`、不注册 todo 工具，生产 `Summarizer` 与 Todo 适配留后续任务并在 Design §14 登记；(B) 把两者并入本任务（明显扩 Scope，需改 `task.md`）。
3. **D3 分支基线（阻塞）**：推荐 Owner 将 `develop`（`6bebdda`，含 T07）合入 `feat/agent-http-api-v1` 并更新 `task.md` Review Baseline；替代方案是重建分支（Owner 权限）。Agent 不执行合并。
4. **D4 协议三选**（沿用草案推荐）：静态 Bearer Token（`MINI_AGENT_AUTH_TOKEN`，常量时间比较，缺失/错误同 401；非回环无 token 启动失败）；服务端全局 `RequestTimeout`（默认 60s，不开放客户端参数）；响应只返回 `reply` + `llm_calls` + `tool_calls`。
5. **D5 Design 落点**：新增 `docs/design/http-api.md`，同步 `runtime-architecture.md` §1（移除「CLI / HTTP 入口不在范围」）/§2（L3 增加 `httpapi`、`service`、`cmd`）/§3（模块职责）/§12（文件表）/§14（Todo 适配、生产 `Summarizer` 归属改指后续任务）。
