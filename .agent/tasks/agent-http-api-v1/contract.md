# Technical Contract — Agent Runtime HTTP JSON API（agent-http-api-v1）

## Decision Status

APPROVED

- Owner 于 2026-10-20 对 D1–D7 作出书面决定（记录见 `Owner Decision Record`）。本文件内容即 A1（Contract Evidence Commit）；A2 只写 `state.contract.status=APPROVED`、`state.contract.target=A1`。
- D5 在批准时附带一项修正：**不得把「客户端断开 → HTTP 408」写成必须满足的响应契约**；客户端断开只要求内部取消语义成立（Run 收到取消、终态 `CANCELLED`、错误种类 `cancelled`、无部分提交），HTTP 响应是否可达不作断言。本文件已按该修正改写 INV-H07、错误表与验证要求。
- 长期 Design 依据本 Contract 在 A1 之后、Coder 实现之前落库：新增 `docs/design/http-api.md`，同步 `docs/design/runtime-architecture.md` §2 / §3 / §12 / §14。
- 未完成前置（不属于 Analyst authority，不改变上述设计决定）：TaskBuilder 需按 D7 在 `task.md` 中把本任务登记为 **T09**、修正过时 Review Baseline（应指向当前实际基线完整 SHA）并确认分支含已交付 T07 实现。该前置完成且 `coder-start` Gate 实际 `PASS` 前，不得 HANDOFF Coder。
- 历史输入（非当前已批准事实）：本 slug 在 `9f0613d` / `ec2072c` 曾有一份 Contract，其中 Owner 已就 D1–D5 作出书面决定；这些 artifacts 已在 `f79e033` 删除，本次按 Owner 2026-10-20 的新决定重新确认并覆盖。

## Problem

交付本地可用的 HTTP JSON 接口（`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`），要求错误语义稳定、默认不暴露无认证接口、取消/超时真实穿透到 Run、可启动并优雅关闭，全部自动化验收只用 Fake LLM，并把正式 HTTP 契约沉淀为长期 Design。

实现前必须先定稿的四类约束（均有代码证据）：

1. **用户消息与 Run 不在同一 Turn**：`agent.Loop.Run(ctx, RunRequest)` 只接受 `ExecutionRef` + `RunPolicy`（`agent/agent.go:63-67`、`:100`），不接受用户消息；它在内部自己取会话执行权（`agent/agent.go:115`）。先提交用户消息再 Run 会留下半轮历史；在外层 `RunTurn` 内再调 `Loop.Run` 会重入同一会话执行权自死锁（`session/turn.go:14-15`、`session/manager.go:359-392`）。
2. **HTTP 公开协议**：端点、JSON 字段、错误封套与错误码→状态码映射一旦落地即为客户端兼容约束。
3. **安全边界**：默认监听面、非回环 fail-closed、凭证比较方式、Provider 密钥来源与零泄漏口径。
4. **生命周期**：fail-closed 启动、就绪、优雅关闭、宽限期内在途请求与宽限期后取消在途 Run。

## Verified Current Behavior

- VERIFIED：`agent.Config.Sessions` 是可注入接口 `SessionRunner{RunTurn(ctx, sessionID, fn func(*session.Turn) error) error}`（`agent/agent.go:34-37`、`:43`），`*session.Manager` 结构匹配；`Loop.Run` 全程只经一次 `l.cfg.Sessions.RunTurn(...)`（`agent/agent.go:115`；全仓库仅此一处 `Sessions.RunTurn`）。这是不修改 T07 公共 API 的唯一公开接缝。
- VERIFIED：`Loop.Run` 内 `snapshot := turn.Session()` 取「已提交基线 + 本 Turn 暂存」（`agent/run.go:56-58`、`session/turn.go:57-72`），因此回调内先 `turn.Append(userMessage)` 再跑 Loop，模型即可看到本轮用户消息。
- VERIFIED：`COMPLETED` 才在 Loop 内 `turn.Append/DropLeading/SetSummary` 提交，其余终态直接返回错误（`agent/run.go:212-225`）；`session.Manager.RunTurn` 在「ctx 未取消 + 回调成功 + Turn 仍打开 + entry 仍是当前 + 提交前校验通过」才整体发布，失败/取消/超时/panic 整轮丢弃（`session/manager.go:188-221`、`:399-428`）。
- VERIFIED：会话不存在/过期/重复创建在回调执行前即返回，哨兵挂在 `*core.RunError.Cause` 上，`errors.Is` 可判（`session/manager.go:359-392`、`session/errors.go:11-31`）。因此「未知会话不执行 Run」由会话层保证，不依赖 HTTP 层文本匹配。
- VERIFIED：`core.RunResult` 的终态与错误种类由 `core.StatusOf/KindOf` 唯一推导（`core/error.go:100-133`），`RunResult.Validate` 逐状态校验（`core/run.go:80-145`）；`KindInvalidArgument` 既可来自参数错误也可来自会话错误，HTTP 层不得把它映射为 400。
- VERIFIED：`contextmgr.NewManager(estimator, summarizer)` 两个参数都必须非 nil（`contextmgr/manager.go:31-40`），`TokenBudget <= 0` 时原样返回视图、不调用 `Summarizer`（`contextmgr/manager.go:72-75`）；仓库内无生产 `Summarizer`。
- VERIFIED：`llm/openai.Config{BaseURL, APIKey, HTTPClient, Timeout}`，`BaseURL` 必填、`APIKey` 可空（为空不发 `Authorization`）（`llm/openai/client.go:41-56`、`:64-80`）。
- VERIFIED：`tool.NewMapRegistry()` + `Register/Seal/Lookup/Specs`（`tool/registry.go:41`），`builtin.NewCalculator()`（`builtin/calculator.go:26`）；`builtin.All(ops)` 需要逐 Turn `TodoOperations`，本任务不使用。
- VERIFIED：`runlog.NewJSONSink(io.Writer)` 已含脱敏、上限与旁路语义（`runlog/slog.go:40`、`docs/design/runtime-architecture.md` §10）；`runlog.Discard()` 可用（`runlog/runlog.go:71`）。
- VERIFIED：`go list ./...` 当前 9 个包，无 `httpapi` / `service` / `cmd` 入口；`docs/design/` 无 `http-api.md`；`go.mod` 零 `require`、`go 1.24`；`make test` = `go test ./...`。
- VERIFIED：Git 基线（分析时）：分支 `feat/agent-http-api-v1`、HEAD `9677e6b` 与 `origin/feat/agent-http-api-v1` 一致，`develop`（`6bebdda`）是祖先，工作区 clean；`agent/` 包与 T07 实现已在分支内。
- INFERENCE：`http.Server.BaseContext` 派生出的请求 `context` 可在关闭阶段统一取消，从而让在途 Run 收到 `cancelled`；`Shutdown` 只等 idle 连接，宽限期满后需主动取消根 context 再 `Close`。
- UNKNOWN：真实 Provider（非 Fake）下的端到端时延与 `RequestTimeout` 默认值是否够用——本任务不验证，属部署/运维事项。
- UNKNOWN：`task.md` Review Baseline 仍写「分支指向 `f79e033`」，实际 HEAD 为 TaskBuilder 落库后的 `9677e6b`。不影响任何设计判定，但需 TaskBuilder 回写事实（见 Open Risks）。

## Recommendation

RECOMMENDATION（Owner 2026-10-20 已批准，见 `Owner Decision Record`）：新增三个 L3 包（`httpapi` 对外协议层、`service` 装配层、`cmd/mini-agent` 可启动入口），用「同 Turn 注入适配器」把用户消息与 Agent 产出放进同一次真实 `session.Manager.RunTurn`，**不修改 `agent` 公共 API**；HTTP 层自持封闭错误码枚举与单点状态码映射；默认回环监听、非回环强制静态 Bearer；服务端请求超时默认 60s、关闭宽限期默认 10s。

关键取舍：

- 选「外层 `RunTurn` + 把当前 `*Turn` 注入 `agent.Config.Sessions`」而不是「给 `agent.Loop.Run` 增加用户消息参数」。前者零公共 API 变更、一次 chat 恰好一次真实 `RunTurn`、失败整轮丢弃；代价是 `service` 必须为每次请求构造一个带 Turn 的 `Loop`，且该方案依赖 T07「Loop 只经注入的 `SessionRunner` 访问会话」这一行为（用计数断言钉死）。
- HTTP 错误码使用 `httpapi` 自己的封闭枚举，不直接把 `core.ErrorKind` 当协议码。代价是需要一张映射表与单点实现；收益是公开协议不随内部 Kind 演进漂移，且 `KindInvalidArgument` 不会被误映射成 400。

失败时系统表现：任何非 `COMPLETED` 终态都不返回 200、不产生会话写入；会话类错误只按哨兵分类；错误体只含稳定 `code` 与固定 `message`。

## Selected Design

Owner 2026-10-20 已确认的 D1–D7 落地为以下实现约束。Coder 只能按本节实现，不得自行决定公开协议或扩展清单。

- D1：`service` 装配层 + 同 Turn 适配器，`agent` 公共 API 零改动；一次 chat 恰好一次真实 `session.Manager.RunTurn`，用户消息、Agent 产出与摘要变更同事务提交或整轮回滚，不嵌套取执行权、不死锁，非 `COMPLETED` 不提交半轮历史。
- D2：只注册 `calculator`；不注册 Todo 工具；`TokenBudget` 固定 `0`（v1 不触发压缩）；`Summarizer` 用明确 fail-closed 占位实现并由测试证明零调用；不得把占位实现描述为生产级摘要能力，Todo 适配与生产 `Summarizer` 属 T08。
- D3：`run_id` 服务端生成；成功响应只含 `reply` 与调用计数；错误码为 `httpapi` 封闭枚举，不直接暴露 `core.ErrorKind`；端点/字段/错误码映射以本文件为准。
- D4：默认 `127.0.0.1:8080`；静态 Bearer；缺失/错误凭证同 `401`；非回环无凭证启动失败不监听；`/healthz` 免认证且零泄漏；凭证不硬编码、不进日志/错误/配置字符串。
- D5：请求超时 60s → `deadline_exceeded` + `504`；宽限期 10s，期满取消在途 Run；客户端断开只要求内部 `cancelled` 语义成立，不承诺 `408` 可达。
- D6：仅环境变量注入；`BaseURL` 必填、LLM `APIKey` 可空（为空不发 `Authorization`）且与 HTTP Bearer 凭证相互独立；缺失/非法必要配置 fail-closed；`Config.String()`/日志/错误信息脱敏；不新增配置文件或第三方依赖。
- D7：`Design Impact = NEW`；新增 `docs/design/http-api.md` 并同步 `runtime-architecture.md` §2/§3/§12/§14；T08 保留给 Todo 适配 + 生产 `Summarizer`，本任务编号 T09 由 TaskBuilder 回写。

### 包与依赖

| 路径 | 内容 |
| --- | --- |
| `httpapi/config.go` | `Config` / 默认值 / `Validate()` / 脱敏 `String()` |
| `httpapi/server.go` | `New(Config, Deps)`、`ServeHTTP` 分派、`Serve`/`Shutdown`/`Close`/`Addr`、`BaseContext` |
| `httpapi/handler.go` | 三个端点 handler 与成功响应写出 |
| `httpapi/request.go` | 有界读取 + 严格字段解析与字段级校验 |
| `httpapi/errors.go` | 错误码封闭枚举、`code → status` 单点映射、错误封套写出 |
| `httpapi/auth.go` | Bearer 凭证常量时间校验 |
| `httpapi/run.go` | `SessionStore` / `Runner` / `RunRequest` / `RunOutcome` 端口与结果映射 |
| `service/runner.go` | `Deps` / `Config` / `NewRunner` / `Run`（一次 `RunTurn` + `Append` + per-Run `Loop` + reply 提取） |
| `service/turn.go` | 同 Turn `agent.SessionRunner` 适配器与用户消息构造 |
| `cmd/mini-agent/main.go` | env → Config/Deps → 监听 → 信号 → 优雅关闭；可测入口 `run(...)` |
| `httpapi/{config,handler,errors,request,auth,lifecycle,fake}_test.go` | 端点契约、错误映射、请求边界、认证、生命周期、Fake |
| `service/runner_test.go` | 真实 `agent.Loop` + Fake Provider/Tool + 真实 `session.Manager` |
| `cmd/mini-agent/main_test.go` | 启动/就绪/非法配置 fail-closed/关闭幂等（注入 `getenv`） |

依赖方向（`cmd → service → httpapi → core/session`）：

- `httpapi` → 标准库 + `core` + `session`；不得 import `agent` / `llm` / `tool` / `builtin` / `contextmgr` / `runlog` / `service`。
- `service` → `httpapi`（端口类型）+ `agent` + `core` + `session` + `tool` + `contextmgr` + `runlog` + `llm`；不得 import `builtin` / `llm/openai`。
- `cmd/mini-agent` → `httpapi` + `service` + `session` + `tool` + `contextmgr` + `runlog` + `llm/openai` + `builtin`。
- L0–L2 与 `agent/**` 保持零改动；新包只向下依赖。

### 端口（`httpapi` 定义，`service` / `*session.Manager` 实现）

```go
type SessionStore interface {
    Create(ctx context.Context, id string) (session.Session, error)
    Get(ctx context.Context, id string) (session.Session, error)
}

type Runner interface {
    Run(ctx context.Context, req RunRequest) (RunOutcome, error)
}

type RunRequest struct{ SessionID, RunID, Message string }

type RunOutcome struct {
    Status    core.RunStatus
    Reply     string // 本轮最终助手文本；HTTP 层不回读 Session
    LLMCalls  int
    ToolCalls int
    Err       *core.RunError
}
```

`Runner.Run` 的 `error` 只用于「未形成 Run 结论」的会话/基础设施错误（`session` 哨兵、`ctx` 终态）；Run 已形成结论时用 `RunOutcome`。

### 装配语义（`service`，本任务最关键约束）

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
    // 会话哨兵 / ctx 终态 → 原样返回；已形成结论 → RunOutcome
}
```

- `loopConfig` 每次 Run 构造，`Sessions` 为 `turnRunner{sessions: r.sessions, turn: turn, sessionID: sid}`；其 `RunTurn(ctx, id, fn)` 在 `id == sessionID` 时直接 `return fn(turn)`，否则回落 `r.sessions.RunTurn`（防御性，正常路径不触发）。
- `Append` / `agent.New` / Run 失败都必须让外层回调返回非 nil → 整轮丢弃，用户消息不落库。
- reply：取 `res.Messages` 中最后一条 `Role == core.Role("assistant")` 的 `Content` 文本拼接；没有则 `""`（`COMPLETED` 允许空回答）。
- 用户消息：`core.Message{Role: core.Role("user"), Content: []core.ContentPart{{Type: core.ContentType("text"), Text: req.Message}}}`；不 trim、不改写、不生成业务 ID。
- `registry` 由 `cmd` 注入并直接透传（v1 只注册 `builtin.NewCalculator()`）；不做逐 Turn registry。
- `contextmgr.NewManager(contextmgr.HeuristicEstimator{}, failClosedSummarizer{})`：`failClosedSummarizer` 返回 `internal` 错误；`TokenBudget` 恒为 0，正常路径不会调用（用测试证明零调用）。

### 端点与字段

`POST /v1/sessions`

- 请求：`Content-Type: application/json`（允许 `; charset=utf-8`），body 可为空（等价 `{}`）；字段表只允许 `session_id`（可选，`^[A-Za-z0-9._:-]{1,128}$`）；缺失时服务端生成 32 位小写 hex。
- 成功：`201` + `{"session_id":"<id>"}`。
- 失败：同 ID 仍有效 → `409 session_exists`；其余见错误表。

`POST /v1/chat`

- 请求：字段表只允许 `session_id`（必填，同上字符集）、`message`（必填，1..8192 个 UTF-8 字符；非法 UTF-8 视为字段错误）。不 trim、不归一化。
- 成功：`200` + `{"session_id":"...","run_id":"...","status":"COMPLETED","reply":"...","llm_calls":N,"tool_calls":N}`。不返回消息序列、`Raw`、Provider 载荷、usage、系统提示。
- 失败：见错误表；任何非 `COMPLETED` 终态都不返回 200。

`GET /healthz`

- 免认证、无请求体；成功 `200` + `{"status":"ok"}`。不探测 Session/Provider，不输出配置、密钥、版本、会话数、内部状态。

路由：单入口 `ServeHTTP` 按「方法 + 精确路径」分派，不用会做路径清理/重定向的默认 `ServeMux` 语义；未知路径 → `404 not_found`；已知路径方法不符 → `405 method_not_allowed` + `Allow`。所有响应（含 404/405）都是 JSON 错误封套。

### 错误封套与错误码（封闭枚举）

封套固定 `{"error":{"code":"<snake_case>","message":"<固定文案>"}}`，`code`/`message` 一对一常量、不含动态内容；`code → status` 只在 `httpapi/errors.go` 单点实现。

| code | HTTP | 判定依据（唯一来源） |
| --- | --- | --- |
| `invalid_json` | 400 | body 不是单个合法 JSON 对象（尾部数据、数组、`null`） |
| `unknown_field` | 400 | 出现契约外键 |
| `missing_field` | 400 | 必填键缺失 |
| `invalid_field` | 400 | 类型错误、长度/字符集/UTF-8 非法 |
| `unsupported_media_type` | 415 | `Content-Type` 非 `application/json` |
| `payload_too_large` | 413 | 超过 `MaxRequestBytes`（`*http.MaxBytesError`） |
| `unauthorized` | 401 | 凭证缺失或错误（必须同码） |
| `not_found` | 404 | 未知路径 |
| `method_not_allowed` | 405 | 已知路径方法不符 |
| `session_not_found` | 404 | `errors.Is(err, session.ErrSessionNotFound)` |
| `session_expired` | 404 | `errors.Is(err, session.ErrSessionExpired)` |
| `session_exists` | 409 | `errors.Is(err, session.ErrSessionExists)` |
| `deadline_exceeded` | 504 | `core.KindDeadlineExceeded` / 派生 ctx 超时 |
| `cancelled` | 408（best-effort，见下） | `core.KindCancelled` / ctx `Canceled`；客户端已断开时不承诺可达 |
| `provider_failure` | 502 | `core.KindProviderFailure` |
| `tool_failure` | 500 | `core.KindToolFailure` |
| `llm_call_limit` | 429 | `core.KindLLMCallLimit` |
| `tool_call_limit` | 429 | `core.KindToolCallLimit` |
| `invalid_pairing` | 500 | `core.KindInvalidPairing` |
| `internal` | 500 | 其余（含 `KindInternal`、非 `*core.RunError`、`RunOutcome` 自相矛盾） |

判定优先级固定：**先会话哨兵（`errors.Is`）→ 再 `core.KindOf` → 最后兜底 `internal`**；运行期 `KindInvalidArgument` 一律 `internal`，不得映射 400。

`cancelled` 的边界（D5 修正）：`cancelled` 仍是封闭枚举中的一类，用于「请求 context 被取消且连接仍可写」时的一次 best-effort 写出。客户端断开连接属于**响应不可达**情形，此时只要求内部语义成立（Run 的 ctx 被取消、终态 `CANCELLED`、错误种类 `cancelled`、会话无部分提交、日志可观察），不得在测试中断言客户端收到 `408`，也不得为了让 `408` 可写而阻塞或重试。

### 配置与 fail-closed

```go
type Config struct { // httpapi
    ListenAddr      string        // 默认 127.0.0.1:8080
    AuthToken       string        // 敏感
    RequestTimeout  time.Duration // 默认 60s，必须 > 0
    ShutdownGrace   time.Duration // 默认 10s，必须 > 0
    MaxRequestBytes int64         // 默认 1 MiB，必须 > 0
}

type Config struct { // service
    MaxLLMCalls, MaxToolCalls int           // 默认 8 / 8，必须 > 0
    SystemPrompt, Model       string        // Model 必须非空
    TokenBudget               int           // v1 必须为 0（> 0 视为配置非法）
    KeepRecent                int           // 默认 0，必须 >= 0
    ToolTimeout               time.Duration // 默认 30s
}
```

- `httpapi.Config.Validate()` 必须解析 `ListenAddr`；`Config` 实现脱敏 `String()`（`%v` / `%+v` / `%#v` 均不输出明文凭证，输出 `<redacted>` / `<unset>`）。
- 未配置 `AuthToken` 且监听非回环 → 构造/启动返回错误且**不进入监听**；未配置 `AuthToken` 且监听回环 → 不要求认证。
- `/healthz` 永不要求认证，且不含任何配置信息。
- `cmd` 只从环境变量读取（不进入命令行参数）：`MINI_AGENT_LISTEN_ADDR`、`MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_LLM_BASE_URL`（必填）、`MINI_AGENT_LLM_API_KEY`（可空，与 `llm/openai` 契约一致）、`MINI_AGENT_MODEL`（必填）、`MINI_AGENT_SYSTEM_PROMPT`、`MINI_AGENT_REQUEST_TIMEOUT`、`MINI_AGENT_SHUTDOWN_GRACE`、`MINI_AGENT_MAX_REQUEST_BYTES`、`MINI_AGENT_MAX_LLM_CALLS`、`MINI_AGENT_MAX_TOOL_CALLS`、`MINI_AGENT_TOOL_TIMEOUT`。

### 请求处理顺序（`/v1/chat`）

1. 路由（方法 + 路径）。2. 认证（读 body 之前）。3. `Content-Type` 校验。4. 有界读取 body → 严格字段解析。5. `SessionStore.Get` 预检（不调用 `Runner`，`Get` 不续期 TTL）。6. 生成 `run_id`（`crypto/rand` 16 字节 → 32 位小写 hex）；`context.WithTimeout(r.Context(), RequestTimeout)` 派生 Run context；调用 `Runner.Run`。7. 结果映射（会话哨兵 → `core.KindOf` → 兜底），`Runner` 返回的会话哨兵必须映射成与第 5 步相同的 code（TOCTOU 防御）。

`GET /healthz` 与 `POST /v1/sessions` 不做 Session 预检。

## Interfaces and Data

- 不改动 `agent.Config` / `RunRequest` / `Loop.Run` 的签名与语义；`service` 只作为 `SessionRunner` 的注入实现方。
- 不改动 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `builtin` 的公共契约与既有测试。
- 会话仍是消息历史的唯一事实源；HTTP 层不持会话状态副本、不回读 Session 生成 reply。
- 长期 Design：新增 `docs/design/http-api.md`（端点、请求/响应 JSON、错误封套与错误码→状态码、取消/超时语义、认证与默认监听边界、启动/关闭生命周期、包边界与端口）；同步 `docs/design/runtime-architecture.md` §2 分层、§3 模块职责、§12 文件所有权（登记上表全部新路径）、§14 归属（区分本任务与服务装配任务 T08 的能力边界）。§1 无需修改（HTTP 入口仍由独立文件承载）。

## Business Invariants

任务局部编号，不占用 `runtime-architecture.md` 的全局 `INV-0xx`。

- INV-H01：仅当 `RunOutcome.Status == COMPLETED` 且 `Err == nil` 时返回 `200`；失败、取消、超时、预算耗尽、非终态、自相矛盾的 `RunOutcome` 一律不得 200。
- INV-H02：认证先于 body 解析与 Run 执行；未认证请求 `Runner` 调用计数为 0、会话快照逐条不变；缺失与错误凭证返回同一 code（`unauthorized` / 401）。
- INV-H03：会话错误只按哨兵（`errors.Is`）分类，不得按错误文本，不得退化为 `internal`/5xx。
- INV-H04：失败无副作用——任何非 `COMPLETED` 路径下会话消息与 Todo 与请求前逐条一致，历史仍通过 `core.ValidateMessages`。
- INV-H05：一次 `POST /v1/chat` 恰好产生一次真实 `session.Manager.RunTurn`；用户消息、Run 产出的全部消息与摘要变更（v1 下摘要不变）在同一次提交或整轮丢弃；不出现「只有 user 消息」「重复 user 消息」或半轮工具调用。
- INV-H06：`service` 不重入同一会话执行权——Run 期间不出现第二次向 `Manager` 获取执行权；同会话并发由外层 `RunTurn` 串行，任何路径不死锁。
- INV-H07：取消与超时真实穿透——客户端断开时 Run 的 ctx 被取消、Provider 观察到取消、终态 `CANCELLED` + 错误种类 `cancelled`，且响应是否可达不作断言（best-effort 408）；请求级超时 → 终态 `CANCELLED` + `deadline_exceeded`，连接仍可写时返回 `504`。两者在终态错误种类上必须可区分，且都不得归为 `internal` 或泛化 5xx。
- INV-H08：默认监听面为回环（`127.0.0.1` / `::1` / `localhost`）；任何非回环监听必须携带凭证，否则启动失败且不监听。
- INV-H09：零泄漏——注入的假密钥不得出现在响应体、响应头、`/healthz`、`Config.String()` 与捕获日志字节中；错误体只含 `code`/固定 `message`，不含路径、堆栈、Provider 原始载荷、会话正文或凭据。
- INV-H10：请求体有界——读取受 `MaxBytesReader` 限制，超限 413；不 panic、不挂死、不无界缓存；非法输入各自返回稳定参数类 code。
- INV-H11：关闭语义——关闭后新连接被拒；宽限期内已进入处理的请求正常返回；宽限期满后在途 Run 的 ctx 被取消（Provider 观察到取消）且不遗留仍在执行的 Run；重复 `Shutdown`/`Close` 幂等不 panic，进程限时退出。
- INV-H12：v1 禁用压缩——`service.Config.TokenBudget` 只接受 `0`；占位 `Summarizer` 在任何测试路径（含超长历史）都不得被调用，且代码与文档都不得把它表述为生产级摘要能力。

## Failure and Consistency Semantics

- **成功代表什么**：`200` 表示该轮 Run `COMPLETED` 且会话提交已发生（提交先于响应写出）；`201` 表示会话已在进程内存创建（无持久化）。**不代表**日志已落盘、外部持久化已完成或客户端已收到响应。
- **客户端断开**：Run 的 ctx 被取消，终态 `CANCELLED` + 错误种类 `cancelled`，写入错误由 `runlog` 记录；响应几乎必然不可达，因此**不承诺任何 HTTP 状态码**，也不得为此阻塞或重试。若断开发生在 `COMPLETED` 提交之后，会话已提交（既定语义），不回滚。
- **响应写失败**：会话仍可能已提交（既定语义）；只留终态与日志证据，不回滚会话。
- **会话不存在 / 过期 / 重复创建**：`session_not_found` 404 / `session_expired` 404 / `session_exists` 409；过期由 Session 自己物理删除，HTTP 层不清理。
- **重复与并发**：无幂等键；同会话两次并发 chat 由外层 `RunTurn` 串行，后到者排队，排队超过 `RequestTimeout` 即 504（可能尚未发起任何 LLM 调用）；同一消息重复提交各自成为一轮。
- **Provider / 工具失败**：`FAILED + provider_failure` → 502；`FAILED + tool_failure` → 500；不回显原始载荷；失败路径不产生部分提交。
- **部分完成**：一次 Run 内部分工具成功、部分失败由 Loop 决定终态；`service` 与 HTTP 只映射最终结论，不重试、不补偿、不写会话。
- **关闭期**：`Shutdown(ShutdownGrace)` 停止接受新连接并等待在途请求；宽限期满后取消根 context（在途 Run 收到 `cancelled`）再 `Close`，可能截断正在写出的响应。关闭返回后不得遗留仍在执行的 Run（Fake Provider 观察到取消、Run 终态已定）。
- **日志**：只经 `runlog.Sink` 记录，Sink 失败不得改变响应、Run 状态与计数。

## Allowed / Forbidden Changes

- 允许：新增上表清单内的 `httpapi/*`、`service/*`、`cmd/mini-agent/*`；新增 `docs/design/http-api.md`；更新 `docs/design/runtime-architecture.md` §2 / §3 / §12 / §14。
- 允许：`httpapi` / `service` 内部新增类型、常量、错误码（限上表封闭枚举）。
- 禁止：修改 `agent/**`（含公共 API 与既有测试）；修改 `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog` / `builtin` 的公共契约与既有测试。
- 禁止：修改 `go.mod`、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）；引入第三方依赖。
- 禁止：实现 Agent Loop、Todo 端口适配、生产 `Summarizer`；SSE/WS、异步轮询、TLS、CORS、限流/配额、metrics、持久化、用户体系与 RBAC。
- 禁止：把 fail-closed 占位 `Summarizer` 表述为生产级摘要能力，或在注释/文档/提交信息中宣称 v1 支持上下文压缩。
- 禁止：新增清单外文件；新增契约未列的错误码或状态码（需 Contract Revision）。

## Verification Requirements

- INV-H01 / INV-H03 / INV-H10 → `httpapi/handler_test.go` + `httpapi/request_test.go` + `httpapi/errors_test.go`：Fake `Runner` 表驱动各终态与各非法输入，断言状态码 + 稳定 code + 响应体不含内部细节；真实 `session.Manager`（注入时钟）区分未知/过期/重复创建且不调用 `Runner`。
- INV-H02 / INV-H07（HTTP 侧）/ INV-H09 → `httpapi/auth_test.go` + `httpapi/config_test.go`：缺失/错误凭证 → 401 同码 + `Runner` 计数 0 + 无会话写入；假密钥在响应体/头/`/healthz`/`Config.String()`/日志字节中零命中。
- INV-H04 / INV-H05 / INV-H06 / INV-H12 → `service/runner_test.go`：包装计数 `RunTurn` 的真实 `session.Manager`，断言每次 chat 恰好 1 次真实 `RunTurn`、Run 期间无第二次获取执行权；两步脚本（`tool_calls` → 文本）断言历史为 `user, assistant(tool_calls), tool, assistant` 且无重复 user；Provider 失败/取消后历史与 Todo 逐条不变；同会话两个并发 chat 串行完成、无死锁（`-race`）；被调用即 `t.Fatal` 的 `Summarizer` 零调用。
- AC-001 / AC-002 → `service/runner_test.go`（真实 Loop）+ `httpapi/handler_test.go`（经真实 Runner 的端到端，断言只返回最终文本与调用计数）。
- INV-H08 → `httpapi/config_test.go`：默认 host 为回环；`0.0.0.0:0` / `:0` + 无 token → 构造返回错误且未监听；`0.0.0.0` + token → 允许。
- INV-H11 / AC-008 → `httpapi/lifecycle_test.go` + `cmd/mini-agent/main_test.go`：`127.0.0.1:0` 启动后轮询 `/healthz` 就绪；关闭后新连接被拒；在途请求宽限期内返回；宽限期满后阻塞式 Fake Provider 观察到取消、Run 终态已定且无遗留执行；重复 `Close` 不 panic；非法配置 `run(...)` 返回错误且未监听。
- AC-007 → `httpapi/lifecycle_test.go` + `service/runner_test.go`：`context.WithCancel` 断开 → Provider 观察 `ctx.Done()`、终态 `CANCELLED` + `cancelled`、历史无部分提交（**不断言客户端收到 408**）；短 `RequestTimeout` → `504 deadline_exceeded` + 终态 `CANCELLED`；两种终态错误种类必须不同，且都不是 `internal`。
- AC-006 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对（方法/路径、字段名、错误码、状态码逐条）；§12 路径与 `git ls-files` 差集为空；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 分层核对；`git diff go.mod` 为空。
- 通用：`make test`、`go build ./...`、`go vet ./...`、`go test -race ./...` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 保持通过。

## Open Risks

- 装配层依赖 T07 行为：同 Turn 适配器成立的前提是「`Loop.Run` 的全部会话交互只经注入 `SessionRunner.RunTurn` 且只调用一次」。T07 后续改变该模式需同步复审；`service/runner_test.go` 的计数断言是护栏。
- v1 无上下文压缩：长会话触达 Provider 限制时以 `provider_failure` / 502 结束；生产 `Summarizer` 与 tokenizer 校准仍缺。
- v1 无 Todo 工具：`todo_create` / `todo_list` / `todo_complete` 在服务中不可用。
- `deadline_exceeded → 504` 是契约映射；`cancelled → 408` 只在连接仍可写时有意义（D5 修正）。客户端断开时响应不可达，可区分性落在终态错误种类与日志，测试不得断言 `408` 送达。
- `llm_call_limit` / `tool_call_limit → 429` 复用限流语义，实际是「本 Run 预算耗尽」，重试不自愈。
- 同会话并发 chat 排队可能让后到请求在未发起 LLM 调用前就 504，客户端无法区分「排队超时」与「模型慢」。
- JSON 重复键按标准库「后者覆盖」，契约不拒绝。
- 未完成前置（TaskBuilder authority，不改变任何设计决定）：`task.md` 需按 D7 把本任务登记为 T09（与 §14 的 T08 区分）、修正过时的 Review Baseline 为实际基线完整 SHA、并确认分支含已交付 T07 实现；完成前不得 HANDOFF Coder。

## Owner Decision Record

Owner 决定（2026-10-20，书面指令；Analyst 只做机械持久化，不构成 Analyst 自批）。所有项均为 `APPROVE`，其中 D5 附带一项强制性修正。

| 项 | Owner 选择 | 落地约束 |
| --- | --- | --- |
| D1 装配接缝 | APPROVE 方案 A：新增 `service` 装配层，同 Turn 适配器复用当前 `session.Turn`，不修改 T07 的 `agent.Loop.Run` 公共 API | 一次 chat 恰好一次真实 `RunTurn`；用户消息、产出与摘要同事务提交或整轮回滚；不嵌套取执行权、不死锁；非 `COMPLETED` 无半轮历史 → INV-H05 / INV-H06 |
| D2 v1 功能边界 | APPROVE 方案 A：只注册 `calculator`，不注册 Todo 工具，不实现生产级压缩；`TokenBudget` 固定 `0`；`Summarizer` 用 fail-closed 占位并由测试证明零调用 | 不得把占位实现描述为生产级摘要能力；Todo 适配与生产 `Summarizer` 属 T08 → INV-H12 |
| D3 HTTP 协议 | APPROVE 推荐：`run_id` 服务端生成；成功响应仅 `reply` + 调用计数；错误码为 `httpapi` 封闭枚举，不直接暴露 `core.ErrorKind`；认证/解析/会话/执行四类错误有稳定映射 | 端点、字段、错误码映射以本文件为准；Coder 不得自行决定公开协议 → INV-H01 / INV-H03 / INV-H10 |
| D4 认证与监听 | APPROVE 推荐：默认 `127.0.0.1:8080`；静态 Bearer；非回环无凭证启动失败不降级；缺失/错误凭证同 `401`；`/healthz` 免认证不泄漏；凭证不硬编码、不进日志/错误/配置字符串 | → INV-H02 / INV-H08 / INV-H09 |
| D5 超时与关闭 | APPROVE **并修正**：请求超时 60s → `deadline_exceeded` + `504`；宽限期 10s 后取消在途 Run；客户端断开只要求内部 `cancelled` 语义成立，**不得把「断开 → 408」写成必须满足的响应契约**，须区分离内部取消语义与实际可发送的 HTTP 响应 | 必须测试超时与取消均不产生半轮提交，并验证关闭后无遗留运行中任务 → INV-H07 / INV-H11 |
| D6 配置来源 | APPROVE：仅环境变量；`BaseURL` 必填；LLM `APIKey` 可空且为空不发 `Authorization`；HTTP Bearer 与 LLM `APIKey` 相互独立；缺失/非法必要配置 fail-closed；`Config.String()`、日志、错误信息脱敏；不新增配置文件或第三方依赖 | → INV-H09；`go.mod` 零改动 |
| D7 Design 与编号 | APPROVE：`Design Impact = NEW`（全链路口径一致）；新增 `docs/design/http-api.md`；同步 `runtime-architecture.md` §2/§3/§12/§14；T08 保留给 Todo 适配 + 生产 `Summarizer`；本任务由 TaskBuilder 登记为 T09 | → AC-006；Design 在 A1 之后、Coder 实现之前落库 |

Owner 明确声明：以上决定不代表 Coder 已获准实现。

剩余前置（不属于本 Contract 的设计决定，需其他角色/权限完成）：

1. TaskBuilder：`task.md` 登记 T09、修正过时 Review Baseline 为实际基线完整 SHA、确认分支含已交付 T07 实现。
2. Analyst：A1 = 本文件内容 commit；A2 = `state.contract.status=APPROVED` + `contract.target=A1`；Design 依据本 Contract 在 A1 之后落库。
3. 上述完成后运行 `coder-start` Gate；仅 Gate 实际 `PASS` 才能 HANDOFF Coder，不得绕过，也不得把未验证事项标记为完成。
