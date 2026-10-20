# Technical Contract — Agent Runtime HTTP JSON API（agent-http-api-v1）

## Decision Status

OWNER_DECIDED / TASK_SYNC_REQUIRED

- Owner 已于 2026-10-20 对 D1–D5 与 T08 编号冲突作出明确决定，内容已写入「Selected Design」与「Owner Decision Record」。
- 机器状态尚未推进：`state.contract.status` 仍为 `PENDING`，A2（`APPROVED` + `contract.target=A1`）**未提交**。
- 剩余条件（不改变任何设计决定，只影响 Task 文件事实一致性）：`task.md` 的 Design Impact 归类、T08/T09 编号、Review Baseline、T07 前置 OPEN QUESTION 需由 TaskBuilder/Owner 同步。同步后 Analyst 才提交 A2、更新 Design、运行 `coder-start` Gate。

## Problem

交付本地可用的 HTTP JSON 接口：`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`；错误语义稳定、默认不暴露无认证接口、取消/超时穿透到 Run、可启动并优雅关闭，全部自动化验收只用 Fake LLM。

真正阻塞实现的设计问题（均有代码证据）：

1. **用户消息与 Run 不在同一 Turn**：`agent.Loop.Run(ctx, RunRequest)` 只接受 `ExecutionRef` + `RunPolicy`，不接受用户消息（`agent/agent.go:63-67`、`:100`），它在内部自己调用 `Sessions.RunTurn` 取执行权（`:115`）。先提交用户消息再 Run 会留下半轮历史；在外层 `RunTurn` 内再调 `Loop.Run` 会重入同一会话执行权自死锁（`session/turn.go:14`、`session/manager.go:375-379`）。**已由 D1 决定：新增 `service` 装配层用注入适配器复用当前 Turn。**
2. **装配缺口**：`contextmgr.NewManager` 要求非 nil `Summarizer`（`contextmgr/manager.go:32-39`），`builtin` todo 工具需逐 Turn `TodoOperations`（`builtin/todo.go:22-26`、`:71`）。**已由 D2 决定：本任务禁用压缩、不注册 Todo 工具，均留后续独立任务。**
3. **基线**：Owner 已将 `develop`(`6bebdda`) 合入本分支（`aa953d9`），分支已含 `agent/` 与更新后的 `runtime-architecture.md`。

## Verified Current Behavior

- VERIFIED：`feat/agent-http-api-v1` HEAD = `origin/feat/agent-http-api-v1` = `aa953d9`（`Merge remote-tracking branch 'origin/develop' ...`），工作区干净；`agent/` 已在工作树，`develop` 是其祖先。
- VERIFIED：合并未覆盖本任务 artifact：`git show --stat aa953d9` 只含 `agent/**`、`docs/design/runtime-architecture.md`、`.agent/tasks/agent-runtime-loop-v1/**`、`.agent/tasks/session-turn-drop-leading-v1/**`。
- VERIFIED：`agent.New(Config) (*Loop, error)`、`(*Loop).Run(ctx, RunRequest) core.RunResult`；`RunRequest = {core.ExecutionRef; Policy core.RunPolicy}`（`agent/agent.go:63-67`、`:74`、`:100`）。
- VERIFIED：`agent.Config.Sessions` 是可注入接口 `SessionRunner{RunTurn(ctx, sessionID, fn)}`（`agent/agent.go:34-37`、`:43`），`*session.Manager` 结构匹配——这是不修改 T07 的唯一公开接缝。
- VERIFIED：`Loop.Run` 内部只经一次 `l.cfg.Sessions.RunTurn(...)` 完成整轮执行与提交（`agent/agent.go:115`）；提交条件由 `session.Manager.RunTurn` 裁决（`session/manager.go:188-221`）。
- VERIFIED：`Turn.Session()` 返回「已提交基线 + 本 Turn 暂存」深拷贝（`session/turn.go:55-69`），外层 Turn 内 `Append` 的用户消息对 Loop 可见。
- VERIFIED：`session.Manager` 提供 `Create`/`Get`/`Delete`/`PurgeExpired`/`RunTurn`/`Load`/`Save`；会话哨兵 `ErrSessionNotFound`/`ErrSessionExpired`/`ErrSessionExists` 挂在 `*core.RunError.Cause` 上，`errors.Is` 可用（`session/errors.go`、`session/manager.go:359-392`）。
- VERIFIED：`core` 九种 `ErrorKind` 齐全，`core.KindOf` / `core.StatusOf` 可用，`RunResult.Validate` 逐状态约束（`core/error.go:12-30`、`core/run.go:80+`）。
- VERIFIED：`RunResult.Messages` 只含本 Run 产出；COMPLETED 不保证文本非空（T07 D5-6）；`core.Message.ID` 不参与校验，`ContentPart.Type` 只接受 `text`（`core/message.go:53-95`）。
- VERIFIED：`contextmgr.NewManager` 两者必须非 nil；`TokenBudget <= 0` 时 `Build` 直接返回、`Summarizer` 不被调用（`contextmgr/manager.go:32-39`、`:72`）。仓库无生产 `Summarizer`。
- VERIFIED：`tool.NewMapRegistry()`、`builtin.NewCalculator()`、`runlog.NewJSONSink(w)`、`llm/openai.NewClient(Config{BaseURL, APIKey, HTTPClient, Timeout})` 均存在；`APIKey` 为空时不发 `Authorization`。
- VERIFIED：`go.mod` 零 `require`、`go 1.24`；`make test` = `go test ./...`；`.agent/workflow.yaml` 的 `resources: {}`，本任务不申请 Registry 资源。
- UNKNOWN：`task.md` 尚未同步 D5 的 Design Impact 归类（仍写 `NEW`）、T08/T09 编号、Review Baseline（仍写 `dc734d2`）与「T07 尚未创建或合并」的 OPEN QUESTION。

## Recommendation

RECOMMENDATION（Owner 已批准，见 Selected Design）：三层交付，**不改 T07、不改 L0–L2**。

1. `httpapi`：纯协议层（`Config` / 路由 / handler / 请求解析 / 错误封套 / 认证 / `Runner` 端口），只依赖标准库 + `core` + `session`。
2. `service`：最小装配层，实现 `httpapi.Runner`——一次 `Manager.RunTurn` 内 `turn.Append(用户消息)`，再以持有当前 Turn 的注入适配器调用 `agent.Loop.Run`。
3. `cmd/mini-agent`：环境变量 → 配置 + Deps → 监听 → 信号 → 优雅关闭。

关键取舍：以 `service` 的薄适配换取 T07 公共 API 零改动；代价是适配器依赖「`Loop.Run` 只经注入的 `SessionRunner` 且只调用一次」这一公开行为，必须用计数断言钉死。替代方案（给 T07 增 in-turn 入口）已被 D1 否决。

## Selected Design

Owner 批准（2026-10-20）：

- **D1 装配接缝 = 方案 A**：新增最小 `service` 装配层，注入适配器复用当前 Turn，不修改 `agent.Loop.Run` 公共 API。必须成立：一次 HTTP chat 恰好一次真实 `session.Manager.RunTurn`；用户消息与 Agent 产出同轮提交或整轮回滚；不嵌套获取同一会话执行权、不死锁；失败/取消/超时不留下半轮历史。
- **D2 当前任务范围 = 方案 A**：不实现生产上下文压缩、不注册 Todo 工具、不实现生产 `Summarizer`；当前配置明确禁用压缩；构造接口要求非 nil `Summarizer` 时使用 fail-closed 占位实现，并以测试证明它不会被调用。后续 Todo 适配与生产 `Summarizer` 由独立任务实现与验收，不得计入本任务已完成能力。
- **D3 Git 基线**：Owner 已完成合并（`aa953d9`）；`task.md` 的 Review Baseline 由 TaskBuilder 回写。
- **D4 HTTP 协议**：静态 Bearer 认证，凭证从配置注入、不硬编码，缺失或无效配置 fail-closed；服务端请求处理超时 60s；成功响应只返回 `reply` 与调用计数；日志与错误响应不泄漏凭证。
- **D5 Design**：Design Impact = UPDATE。新增 `docs/design/http-api.md`，同步 `runtime-architecture.md` §1、§2、§3、§12、§14；准确记录服务装配边界、依赖方向、失败语义、HTTP 契约与本任务暂缓能力。
- **编号冲突**：保留 T07 已批准 Contract 中的 T08 归属（Todo 适配 + 生产 `Summarizer`）；本任务由 TaskBuilder 重新分配唯一编号（建议 T09），并同步 `task.md`、依赖关系与设计文档；不得扩大当前 Scope 或修改 T07 已批准 Contract 来消歧。

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
| `cmd/mini-agent/main.go` | 环境变量 → Config/Deps → 监听 → 信号 → 优雅关闭；可测入口 `run(...)`；fail-closed 占位 `Summarizer` |
| `httpapi/fake_test.go` | Fake `Runner` / 捕获日志 Sink / 会话快照辅助 |
| `httpapi/handler_test.go` | 端点契约、多轮、错误与副作用 |
| `httpapi/errors_test.go` | 错误码↔状态码映射表与封套形状 |
| `httpapi/request_test.go` | 媒体类型、JSON、字段、大小上限 |
| `httpapi/config_test.go` | 默认回环、fail-closed、脱敏 |
| `httpapi/lifecycle_test.go` | 就绪、关闭、宽限期、取消/超时穿透 |
| `httpapi/auth_test.go` | 缺失/错误凭证一致、无泄漏 |
| `service/runner_test.go` | 真实 `agent.Loop` + Fake Provider/Tool + 真实 `session.Manager`：多轮历史、工具链路、无半轮提交、恰好一次 RunTurn、Summarizer 未被调用 |
| `cmd/mini-agent/main_test.go` | 启动/就绪/非法配置 fail-closed/关闭幂等（注入 getenv） |

禁止新增清单外文件；需要拆分走 Contract Revision（AC-008 的 §12 差集校验依赖此清单）。

### 依赖方向

- `httpapi` → 标准库 + `core` + `session`；不得 import `agent` / `llm` / `tool` / `builtin` / `contextmgr` / `runlog` / `service`。
- `service` → `httpapi`（端口类型）+ `agent` + `core` + `session` + `tool` + `contextmgr` + `runlog` + `llm`；不得 import `builtin` / `llm/openai`。
- `cmd/mini-agent` → `httpapi` + `service` + `session` + `tool` + `contextmgr` + `runlog` + `llm/openai` + `builtin`。
- L0–L2 与 `agent` 不得 import 新包；`agent/**` 保持零改动。

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
    err := r.sessions.RunTurn(ctx, req.SessionID, func(turn *session.Turn) error {
        if e := turn.Append(userMessage(req.Message)); e != nil { return e }
        loop, e := agent.New(r.loopConfig(turn, req.SessionID))
        if e != nil { return e }
        res = loop.Run(ctx, agent.RunRequest{
            ExecutionRef: core.ExecutionRef{SessionID: req.SessionID, RunID: req.RunID},
            Policy:       r.cfg.Policy(),
        })
        if res.Status != core.RunStatusCompleted { return runErrorOf(res) }
        return nil
    })
    // 会话哨兵 / ctx 终态 → 原样返回；已形成结论 → 返回 RunOutcome
}
```

- `loopConfig` 每次 Run 构造，`Sessions` 为 `turnRunner{sessions: r.sessions, turn: turn, sessionID: sid}`；`turnRunner.RunTurn(ctx, id, fn)` 在 `id == sessionID` 时直接 `return fn(turn)`，否则回落到 `r.sessions.RunTurn`。
- `Append` / `agent.New` / Run 失败都必须让外层回调返回非 nil → 整轮丢弃（用户消息不落库）。
- 会话不存在/过期/被删除：`RunTurn` 在回调执行前返回哨兵，`Run` 原样透传（`errors.Is` 可判）。
- `reply`：取 `res.Messages` 中**最后一条** `Role == core.Role("assistant")` 的 `Content` 文本拼接；没有则 `""`（COMPLETED 允许空回答）。
- 用户消息：`core.Message{Role: core.Role("user"), Content: []core.ContentPart{{Type: core.ContentType("text"), Text: req.Message}}}`；不改写文本、不 trim、不生成业务 ID。
- registry：由 `cmd` 注入的 `tool.Registry` 直接透传（v1 只注册 `builtin.NewCalculator()`）；不做逐 Turn registry。

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
    Model        string        // 必须非空
    TokenBudget  int           // v1 必须为 0（明确禁用压缩）；> 0 视为配置非法
    KeepRecent   int           // 默认 0，必须 >= 0
    ToolTimeout  time.Duration // 默认 30s
}
```

- `httpapi.Config.Validate()` 必须解析 `ListenAddr`；`Config` 实现脱敏 `String()`（`%v`/`%+v`/`%#v` 都不输出明文凭证，输出 `<redacted>` / `<unset>`）。
- 环境变量（唯一来源，`cmd` 读取）：`MINI_AGENT_LISTEN_ADDR`、`MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_REQUEST_TIMEOUT`、`MINI_AGENT_SHUTDOWN_GRACE`、`MINI_AGENT_MAX_REQUEST_BYTES`、`MINI_AGENT_SESSION_TTL`（→ `session.ManagerConfig.TTL`，默认 30m，`0` 表示不过期）、`MINI_AGENT_PROVIDER_BASE_URL`（必填）、`MINI_AGENT_PROVIDER_API_KEY`（允许为空）、`MINI_AGENT_MODEL`（必填）、`MINI_AGENT_SYSTEM_PROMPT`、`MINI_AGENT_MAX_LLM_CALLS`、`MINI_AGENT_MAX_TOOL_CALLS`、`MINI_AGENT_TOKEN_BUDGET`（v1 只接受 `0`）、`MINI_AGENT_KEEP_RECENT`、`MINI_AGENT_TOOL_TIMEOUT`。
- 密钥只从环境读取，不进命令行参数、不进响应/日志/`/healthz`。
- 服务器超时：`ReadHeaderTimeout=5s`、`ReadTimeout=RequestTimeout+5s`、`WriteTimeout=RequestTimeout+5s`、`IdleTimeout=60s`（`WriteTimeout` 必须大于 `RequestTimeout`）。
- 占位 `Summarizer`（`cmd` 内实现）：任何调用都返回 `core.KindInternal` 错误，绝不产出摘要；因为 `TokenBudget=0`，`contextmgr.Build` 不会调用它。

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
- INV-H07：默认监听面是回环（`127.0.0.1`/`::1`/`localhost`）；任何非回环监听必须携带凭证，否则启动失败且不监听。
- INV-H08：请求体有界：读取受 `MaxBytesReader` 限制，超限 413；不 panic、不挂死、不无界缓存。
- INV-H09：关闭语义：关闭后新连接被拒；宽限期内进入处理的请求正常返回；宽限期满后仍在途 Run 的 `ctx` 被取消；重复 `Shutdown`/`Close` 幂等不 panic，进程限时退出。
- INV-H10：错误封套封闭：`code` ∈ 上表，`message` 为固定常量，响应体不含路径、堆栈、Provider 载荷、会话文本或凭据；`code → status` 只有一处映射。
- INV-H11（D1）：一次 `POST /v1/chat` 恰好产生一次真实 `session.Manager.RunTurn`；用户消息与 Run 产出的全部消息在同一 Turn 提交或整轮回滚；任何路径都不出现「只有 user 消息」「重复 user 消息」或半轮工具调用。
- INV-H12（D1）：`service` 不重入同一会话执行权：Run 期间不出现第二次向 `Manager` 获取执行权；同会话并发 chat 由外层 `RunTurn` 串行；任何路径都不死锁，等待超过 `RequestTimeout` 时以 `deadline_exceeded` 返回。
- INV-H13（D2）：v1 禁用压缩：`service.Config.TokenBudget` 只接受 `0`，占位 `Summarizer` 在任何测试路径下都不得被调用（含长历史用例）。

## Failure and Consistency Semantics

- **成功代表什么**：`200` 表示该轮 Run `COMPLETED` 且会话提交已发生（提交先于响应写出）；`201` 表示会话已在进程内存创建（无持久化）。
- **不代表什么**：不代表日志已落盘、不代表任何外部持久化、不代表客户端已收到响应。
- **响应写失败**：客户端断开导致响应不可达时会话仍可能已提交；这是既定语义，只能靠日志与终态观察，不回滚会话。
- **会话不存在 / 过期 / 重复创建**：`session_not_found`(404) / `session_expired`(404) / `session_exists`(409)；过期时 Session 自己物理删除，HTTP 层不清理。
- **重复与并发**：无幂等键；同一会话两次并发 chat 由外层 `RunTurn` 串行，后到者排队，排队超过 `RequestTimeout` 即 `504 deadline_exceeded`（可能尚未执行任何 LLM 调用）。重复提交同一消息会各自成为一轮（无去重）。
- **Provider / 工具失败**：`FAILED + provider_failure` → 502；`FAILED + tool_failure` → 500；都不泄漏原始载荷；失败路径不产生部分提交。
- **部分完成**：一次 Run 内部分工具成功、部分失败由 Loop 决定终态；`service`/HTTP 只映射最终结论，不重试、不补偿、不写会话。
- **关闭期**：`Shutdown(ctx=ShutdownGrace)` 停止接受新连接并等待在途请求；宽限期满后取消根 context（在途 Run 收到 `cancelled`）再 `Close` 强制断开，可能截断正在写出的响应。

## Allowed / Forbidden Changes

- 允许：新增上表清单内的 `httpapi/*`、`service/*`、`cmd/mini-agent/*`；新增 `docs/design/http-api.md`；更新 `docs/design/runtime-architecture.md` §1 / §2 / §3 / §12 / §14。
- 允许：`httpapi` / `service` 内部新增类型、常量与错误码。
- 禁止：修改 `agent/**`（D1 明确不修改 T07 公共 API）；修改 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `builtin` 公共契约与既有测试。
- 禁止：修改 `go.mod`、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）；引入第三方依赖。
- 禁止：实现 Agent Loop、Todo 端口适配、生产 `Summarizer`（D2 明确暂缓）；SSE/WS、异步轮询、TLS、CORS、限流/配额、metrics、持久化、用户体系与 RBAC。
- 禁止：新增清单外文件；新增契约未列的错误码或状态码（需 Contract Revision）。

## Verification Requirements

- INV-H01 → `httpapi/handler_test.go`：Fake `Runner` 表驱动各终态/错误，断言仅 `COMPLETED` 得 200。
- INV-H02 → `httpapi/auth_test.go`：缺失/错误凭证，断言 401 + 同 code + `Runner` 计数 0 + 无会话写入。
- INV-H03 → `httpapi/handler_test.go`：真实 `session.Manager`（TTL 注入时钟），未知 → `session_not_found`、过期 → `session_expired`、重复创建 → `session_exists`，且不调用 `Runner`。
- INV-H04 → `handler_test.go`：每例前后 `Manager.Get` 深比对消息与 Todo 逐条一致，`core.ValidateMessages` 通过。
- INV-H05 → `httpapi/lifecycle_test.go` + `service/runner_test.go`：阻塞到 `ctx.Done()` 的 Fake Provider；客户端取消 → 观察到取消、无部分提交；短 `RequestTimeout` → `504 deadline_exceeded`，与取消可区分。
- INV-H06 → `auth_test.go` + `config_test.go`：响应体/头/`/healthz`/日志字节子串扫描假密钥；`fmt.Sprintf("%+v", cfg)` 不含明文。
- INV-H07 → `config_test.go`：默认主机回环；`0.0.0.0:0` / `:0` + 无 token → `New` 返回错误且未监听；`0.0.0.0` + token → 允许。
- INV-H08 → `request_test.go`：`MaxRequestBytes+1` → 413；`Content-Type` 变体 → 415；坏 JSON/未知字段/缺字段/类型错/超长 message → 各自 code；不 panic。
- INV-H09 → `lifecycle_test.go` + `cmd/mini-agent/main_test.go`：`127.0.0.1:0` 启动后轮询 `/healthz` 就绪；关闭后新连接被拒；在途请求宽限期内返回；宽限期后 Run 被取消；重复 `Close` 不 panic。
- INV-H10 → `httpapi/errors_test.go`：遍历封闭枚举断言 code→status 一一对应；错误体只含 `error.code`/`error.message`。
- INV-H11 / INV-H12 → `service/runner_test.go`：包一层计数 `RunTurn` 的 `session.Manager`，断言每次 chat 恰好 1 次、Run 期间无第二次获取执行权；真实 `agent.Loop` + Fake Provider 两步脚本（tool_calls → 文本）断言历史为 `user, assistant(tool_calls), tool, assistant` 且无重复 user；Provider 失败/取消后历史与 Todo 逐条不变；同会话两个并发 chat 串行完成、无死锁（`-race`）。
- INV-H13 → `service/runner_test.go`：注入被调用即 `t.Fatal` 的 `contextmgr.Summarizer`，用超长历史跑成功 Run，断言零调用；`service.Config` 表驱动断言 `TokenBudget > 0` 被拒绝。
- AC-001 / AC-002 → `service/runner_test.go`（真实 Loop）+ `httpapi/handler_test.go`（经真实 Runner 的端到端）。
- AC-008 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对；§12 路径与 `git ls-files` 差集为空；`go list` 分层核对；`git diff go.mod` 为空；`go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...` 退出码 0。

## Open Risks

- **装配层依赖 T07 公开行为**：同 Turn 适配器成立的前提是「`Loop.Run` 的全部会话交互都经由注入 `SessionRunner.RunTurn`，且只调用一次」。T07 后续改变该模式需同步复审（`service/runner_test.go` 的计数断言是护栏）。
- **v1 无上下文压缩**（D2 已批准）：长会话触达 Provider 上限时以 `provider_failure`/502 结束；生产 `Summarizer` 与真实 tokenizer 校准仍缺。
- **无 Todo 工具**（D2 已批准）：`todo_create` / `todo_list` / `todo_complete` 在 v1 服务中不可用。
- `cancelled → 408`、`deadline_exceeded → 504` 是契约约定映射：客户端断开时响应通常不可达，可区分性主要落在终态与日志。
- `llm_call_limit` / `tool_call_limit → 429` 复用限流语义，实际是「本 Run 预算耗尽」，重试不自愈。
- 同会话并发 chat 排队可能让后到请求在未调用 LLM 前就 504，客户端无法区分「排队超时」与「模型慢」。
- JSON 重复键按标准库「后者覆盖」，契约不拒绝。
- `task.md` 未同步：Design Impact 归类（`NEW` vs Owner 的 `UPDATE`）、T08/T09 编号、Review Baseline（`dc734d2` → `aa953d9`）、OPEN QUESTION 仍称 T07 未创建。Cleaner 四方核对前必须修正。

## Owner Decision Record

Owner 决定（2026-10-20，来源：Owner 对 R1 待决问题 D1–D5 的书面答复）：

| 项 | Owner 选择 | 适用范围 / 落地约束 |
| --- | --- | --- |
| D1 装配接缝 | **方案 A**：新增最小 `service` 装配层，注入适配器复用当前 Turn，不修改 `agent.Loop.Run` | 一次 chat 恰好一次真实 `RunTurn`；同轮提交或整轮回滚；不嵌套取执行权、不死锁；失败/取消/超时无半轮历史 → INV-H11 / INV-H12 |
| D2 当前范围 | **方案 A**：不实现生产压缩、不注册 Todo 工具、不实现生产 `Summarizer`；构造接口要求非 nil 时用 fail-closed 占位并以测试证明不被调用 | 后续 Todo 适配与生产 `Summarizer` 由独立任务实现与验收，不得计入本任务 → INV-H13 |
| D3 Git 基线 | Owner 已执行合并：`origin/develop`(`6bebdda`) → `aa953d9`；Review Baseline 由 TaskBuilder 回写 | Agent 不执行合并；基线未回写前不得开始实现 |
| D4 HTTP 协议 | 批准推荐：静态 Bearer（配置注入、缺失/无效 fail-closed）、服务端 60s 超时、成功响应只含 `reply` 与调用计数、日志与错误不泄漏凭证 | 见「端点」「配置」「认证与监听」 |
| D5 Design | 批准 Design Impact = UPDATE：新增 `docs/design/http-api.md`，同步 `runtime-architecture.md` §1/§2/§3/§12/§14；记录装配边界、依赖方向、失败语义、HTTP 契约与暂缓能力 | 在 Contract `APPROVED` 后、Coder 实现前落地 |
| 编号冲突 | 保留 T07 已批准 Contract 的 T08 归属（Todo 适配 + 生产 `Summarizer`）；本任务由 TaskBuilder 重分配唯一编号（建议 T09），同步 `task.md`、依赖关系与设计文档 | 不得扩大当前 Scope，不得修改 T07 已批准 Contract |

未决 / 未完成事项（不影响上述设计判定，属 Task 文件与执行前置）：

1. `task.md` 的 4 处事实同步（Design Impact 归类、T08/T09 编号、Review Baseline `aa953d9`、删除已失效的 T07 OPEN QUESTION）——由 TaskBuilder/Owner 完成。
2. 上述同步完成后，Analyst 提交 A2（`state.contract.status=APPROVED`、`contract.target=A1`），再更新 Design，最后运行 `coder-start` Gate；Gate 实际 `PASS` 前不 HANDOFF Coder。
