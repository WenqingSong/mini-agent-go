# HTTP API

运行服务和调用示例见 [README](../../README.md)，其他启动方式见[运行指南](../running.md)。本文集中记录三个端点、错误码、配置，以及 HTTP 到 Agent 的提交边界。

## 1. 范围

本文件约束 `mini-agent-go` 对外暴露的三个 HTTP 端点及其运行边界：`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`。

不在本文件范围：流式输出（SSE / WebSocket）、异步任务轮询、webhook、TLS 终结、反向代理、CORS、限流与配额、审计、metrics、分布式追踪、日志落盘与检索、会话持久化与多实例一致性、用户体系与 RBAC、部署与发布流水线。

## 2. 包边界与依赖方向

```text
L3  httpapi        对外协议层：配置与生命周期、路由、请求解析、错误封套与映射、认证、端口定义
L3  service        装配层：HTTP 端口到 Agent Loop 的实现（同 Turn 适配器、per-Run Loop、reply 提取）
    cmd/mini-agent 可启动入口：环境变量 → 配置装配 → 监听 → 信号 → 优雅关闭
```

- `httpapi` 只依赖标准库与 `core` / `session`；不 import `agent`、`llm`、`tool`、`builtin`、`contextmgr`、`runlog`、`service`。
- `service` 依赖 `httpapi`（端口类型）+ `agent` + `core` / `llm` / `tool` / `session` / `contextmgr` / `runlog`；不 import `builtin`、`llm/openai`。
- `cmd/mini-agent` 依赖 `httpapi` + `service` + `builtin` + `llm/openai` + 其余 L0/L1 包，负责唯一的装配点。
- L0–L2 与 `agent` 不得反向 import 上述新包；`agent` 公共 API 不因 HTTP 入口而变更。

端口由 `httpapi` 定义、`service`（与 `*session.Manager`）实现：

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
    Reply     string
    LLMCalls  int
    ToolCalls int
    Err       *core.RunError
}
```

`Runner.Run` 的 `error` 只表达「未形成 Run 结论」的会话/基础设施失败（`session` 哨兵、`ctx` 终态）；已形成结论时用 `RunOutcome`。

### 2.1 同 Turn 装配接缝

`agent.Loop.Run` 不接受用户消息，且在内部唯一一次调用 `Config.Sessions.RunTurn` 取得执行权并完成整轮提交。服务的装配契约是：

1. `service` 在外层调用一次真实的 `session.Manager.RunTurn`；
2. 在回调内先 `turn.Append(userMessage)`，再用 `agent.Config.Sessions` 指向「直接返回当前 `*Turn`」的适配器构造 per-Run `agent.Loop` 并执行；
3. 回调返回非 nil（任一失败）→ 整轮丢弃；返回 nil → 会话层原子提交。

因此：一次 `POST /v1/chat` 恰好一次真实 `RunTurn`；用户消息、本轮产出与摘要变更同事务提交或整轮回滚；不得在执行权内二次获取同一会话执行权（死锁）。

## 3. 端点契约

三个端点的方法与路径固定。路由按「方法 + 精确路径」分派，不使用会做路径清理或重定向的 `ServeMux` 默认语义。

### 3.1 `POST /v1/sessions`

| 项 | 契约 |
| --- | --- |
| 认证 | 服务启用认证时必须通过（见 §6） |
| `Content-Type` | `application/json`（允许 `; charset=utf-8`）；否则 `415 unsupported_media_type` |
| 请求字段 | 仅 `session_id`（可选）：`^[A-Za-z0-9._:-]{1,128}$`；body 可为空（等价 `{}`） |
| 成功 | `201`，body `{"session_id":"<id>"}`；缺失 `session_id` 时服务端生成 32 位小写 hex |
| 失败 | `409 session_exists`（同 ID 仍有效）；参数类错误见 §4 |

### 3.2 `POST /v1/chat`

| 项 | 契约 |
| --- | --- |
| 认证 | 服务启用认证时必须通过（见 §6） |
| `Content-Type` | `application/json`（允许 `; charset=utf-8`）；否则 `415` |
| 请求字段 | 仅 `session_id`（必填，同上字符集）、`message`（必填，1..8192 个 UTF-8 字符）；不 trim、不归一化 |
| 成功 | `200`，body `{"session_id":"...","run_id":"...","status":"COMPLETED","reply":"...","llm_calls":N,"tool_calls":N}` |
| 不返回 | 消息序列、`Raw`、Provider 载荷、token usage、system prompt、会话内部状态 |
| `run_id` | 服务端生成（`crypto/rand` 16 字节 → 32 位小写 hex） |
| 失败 | 只有 `COMPLETED` 且无错误才 `200`；其余终态与错误见 §4 |

### 3.3 `GET /healthz`

- 免认证、无请求体；成功 `200`，body `{"status":"ok"}`。
- 不探测 Session / Provider，不输出配置、密钥、版本、会话数或内部状态。
- 就绪语义：仅在监听成功后可达。

### 3.4 路由级错误

| 情形 | 响应 |
| --- | --- |
| 未知路径 | `404 not_found` |
| 已知路径方法不符 | `405 method_not_allowed` + `Allow` 头 |

所有路由错误响应（含 404/405）都使用 §4 的 JSON 错误封套。

## 4. 错误封套与错误码域

封套固定：

```json
{"error":{"code":"<snake_case>","message":"<固定文案>"}}
```

`code` 与 `message` 一对一常量化，不含动态内容、路径、堆栈、Provider 原始载荷、会话正文或凭据。`code → HTTP status` 只有一处实现（`httpapi/errors.go`）。错误码域是本协议内的封闭枚举（协议常量），不占用 Shared Resource Registry。

| code | HTTP | 判定依据（唯一来源） |
| --- | --- | --- |
| `invalid_json` | 400 | body 不是单个合法 JSON 对象（尾部数据、数组、`null`） |
| `unknown_field` | 400 | 出现契约外键 |
| `missing_field` | 400 | 必填键缺失 |
| `invalid_field` | 400 | 类型错误、长度/字符集/UTF-8 非法 |
| `unsupported_media_type` | 415 | `Content-Type` 非 `application/json` |
| `payload_too_large` | 413 | 超过 `MaxRequestBytes`（`*http.MaxBytesError`） |
| `unauthorized` | 401 | 凭证缺失或错误（同一 code、同一响应） |
| `not_found` | 404 | 未知路径 |
| `method_not_allowed` | 405 | 已知路径方法不符 |
| `session_not_found` | 404 | `errors.Is(err, session.ErrSessionNotFound)` |
| `session_expired` | 404 | `errors.Is(err, session.ErrSessionExpired)` |
| `session_exists` | 409 | `errors.Is(err, session.ErrSessionExists)` |
| `deadline_exceeded` | 504 | `core.KindDeadlineExceeded` / 派生 ctx 超时 |
| `cancelled` | 408（best-effort） | `core.KindCancelled` / ctx `Canceled`；客户端已断开时不承诺可达 |
| `provider_failure` | 502 | `core.KindProviderFailure` |
| `tool_failure` | 500 | `core.KindToolFailure` |
| `llm_call_limit` | 429 | `core.KindLLMCallLimit` |
| `tool_call_limit` | 429 | `core.KindToolCallLimit` |
| `invalid_pairing` | 500 | `core.KindInvalidPairing` |
| `internal` | 500 | 其余（`KindInternal`、非 `*core.RunError`、`RunOutcome` 自相矛盾） |

判定优先级固定：

1. 会话哨兵（`errors.Is`，最高优先级）；
2. 错误链中最外层 `*core.RunError` 的 `Kind`（`core.KindOf`）；
3. 错误链中不存在 `*core.RunError` 时，裸 Context 哨兵（`errors.Is(err, context.DeadlineExceeded)` / `errors.Is(err, context.Canceled)`）作为兜底；
4. 其余兜底 `internal`。

由此成立三条长期事实：

- 已有的 `core.RunError.Kind` 是 HTTP 错误分类的权威依据：Run 结论一旦自洽成立，`error.code` 与状态码只由该 `Kind` 决定。
- 底层 `Cause` 中的 `context.Canceled` / `context.DeadlineExceeded` 不得覆盖已形成的分类（例如 Provider 自身超时归因为 `provider_failure` 时，即使其错误链含 `context.DeadlineExceeded`，也必须是 `502 provider_failure`，不得退化为 `504 deadline_exceeded`）。
- 错误链中没有 `*core.RunError` 时，Context 哨兵作为兜底判定（例如直接传入的请求 ctx 超时 / 取消 → `504 deadline_exceeded` / `408 cancelled`）。

运行期 `KindInvalidArgument` 一律 `internal`，不得映射为 400：它既可能来自调用参数，也可能来自会话错误，只有哨兵能区分。

`/v1/chat` 的处理顺序固定：路由 → 认证 → `Content-Type` → 有界读取与解析 → `SessionStore.Get` 预检（不调用 `Runner`，`Get` 不续期 TTL）→ 派生请求 context 并调用 `Runner` → 结果映射。`Runner` 返回的会话哨兵必须映射为与预检相同的 code（TOCTOU 防御）。

## 5. 取消、超时与失败语义

- **请求级超时**：服务端为每个请求派生 `context.WithTimeout(请求 context, RequestTimeout)`；到期后 Run 终态为 `CANCELLED` + `deadline_exceeded`，连接仍可写时返回 `504 deadline_exceeded`。等待同会话执行权同样受该超时约束（可能尚未发起任何模型调用）。
- **客户端断开**：请求 context 取消 → Run 的 ctx 被取消 → Provider 观察到取消 → 终态 `CANCELLED` + `cancelled`，本 Turn 不提交。此时响应几乎必然不可达，**不承诺任何 HTTP 状态码**（`408` 仅为连接仍可写时的 best-effort），不得为写出响应而阻塞或重试。
- **可区分性**：取消与超时靠终态错误种类（`cancelled` / `deadline_exceeded`）区分，HTTP `code` 是次要证据；两者都不得归为 `internal` 或泛化 5xx。
- **成功代表什么**：`200` 表示该轮 Run `COMPLETED` 且会话提交已发生（提交先于响应写出）；`201` 表示会话已在进程内存创建。都不表示日志已落盘、外部持久化已完成或客户端已收到响应。
- **失败无副作用**：任何非 `COMPLETED` 路径下会话消息、摘要与 Todo 与请求前逐条一致，历史仍通过 `core.ValidateMessages`；HTTP 层不写会话，`service` 的唯一写路径是 Run 所在的那次 `RunTurn`。
- **重复与并发**：无幂等键；同会话并发 chat 由会话执行权串行，后到者排队，排队超过 `RequestTimeout` 即 `504`；同一消息重复提交各自成为一轮。
- **部分完成**：一次 Run 内部分工具成功、部分失败由 Agent Loop 决定终态；`service` 与 HTTP 只映射最终结论，不重试、不补偿、不回滚。

## 6. 认证与监听边界

- 默认监听 `127.0.0.1:8080`（仅本机）；回环地址视为 `127.0.0.1` / `::1` / `localhost`。
- 对外（非回环）监听必须启用静态 Bearer 认证：缺失凭证时启动失败，**不得**降级为无认证服务，也不进入监听。
- 认证在读取 body 之前完成；凭证缺失与凭证错误返回同一 `401 unauthorized` 响应，不回显预期凭证。
- 比较使用常量时间比较（对两侧摘要比较），不因长度或前缀差异提前返回。
- `/healthz` 免认证，且不包含任何配置或内部状态。
- 凭证只从受控来源（环境变量）注入，不硬编码、不进入命令行参数、不出现在日志、错误响应、配置字符串或 `/healthz` 中。

## 7. 启动与关闭生命周期

- **fail-closed 启动**：配置非法（监听地址不可解析、超时/宽限期/大小上限非正、`Model` 为空、`BaseURL` 缺失、`TokenBudget != 0`、非回环监听缺凭证）时构造返回错误，不进入监听。
- **就绪**：监听成功后才可服务 `/healthz`；就绪前连接不可达。
- **关闭**：收到终止信号（或等价关闭入口）后停止接受新连接；已进入处理的请求在 `ShutdownGrace` 内正常返回；宽限期满后取消服务根 context（在途 Run 收到 `cancelled`，本 Turn 不提交）再强制关闭连接，可能截断正在写出的响应。
- **幂等与退出**：重复关闭不 panic；关闭返回后不遗留仍在执行的 Run，进程在限定时间内退出。

## 8. 配置契约

应用读取进程环境变量；Compose 从 `.env` 注入变量，二进制不会自行读取该文件。默认值来自代码，`.env.example` 可显式覆盖默认值：

| 变量 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- |
| `MINI_AGENT_LISTEN_ADDR` | 否 | `127.0.0.1:8080` | 实际监听地址 |
| `MINI_AGENT_AUTH_TOKEN` | 非回环时必填 | — | HTTP Bearer 凭证，与 LLM `APIKey` 相互独立 |
| `MINI_AGENT_LLM_BASE_URL` | 是 | — | OpenAI 兼容服务根地址 |
| `MINI_AGENT_LLM_API_KEY` | 否 | — | 为空时不发送 `Authorization` |
| `MINI_AGENT_MODEL` | 是 | — | 透传 `llm.Request.Model` |
| `MINI_AGENT_SYSTEM_PROMPT` | 否 | 空 | 纯文本 system prompt |
| `MINI_AGENT_REQUEST_TIMEOUT` | 否 | `60s` | 必须为正 |
| `MINI_AGENT_SHUTDOWN_GRACE` | 否 | `10s` | 必须为正 |
| `MINI_AGENT_MAX_REQUEST_BYTES` | 否 | `1MiB` | 必须为正 |
| `MINI_AGENT_MAX_LLM_CALLS` | 否 | `8` | `RunPolicy.MaxLLMCalls` |
| `MINI_AGENT_MAX_TOOL_CALLS` | 否 | `8` | `RunPolicy.MaxToolCalls` |
| `MINI_AGENT_TOOL_TIMEOUT` | 否 | `30s` | 工具级超时，`<= 0` 表示不设 |
| `MINI_AGENT_CONTEXT_TOKEN_BUDGET` | 否 | `0` | 上下文估算预算；`0` 表示禁用压缩，必须为非负整数 |
| `MINI_AGENT_CONTEXT_KEEP_RECENT` | 否 | `8` | 压缩时保留的近期消息条数下限；必须为非负整数，`0` 视为未配置 |

- 配置结构必须实现脱敏 `String()`：`%v` / `%+v` / `%#v` 都不输出明文凭证，输出 `<redacted>` / `<unset>`。
- 运行日志只经 `runlog.Sink` 记录，Sink 失败不得改变响应、Run 状态或计数。
- 上下文压缩默认关闭：`MINI_AGENT_CONTEXT_TOKEN_BUDGET` 未配置或为 `0` 时行为与旧版本一致（不压缩、历史继续累积）。配置正整数后，`service.Runner` 把预算与 `KeepRecent` 透传 Agent Loop；默认 `Summarizer` 是 `contextmgr.DigestSummarizer`（确定性规则化摘录，零模型调用），不需要额外服务或真实模型。
- 预算不可满足时 `Build` 返回 `invalid_argument`，按既有错误分类映射为 `internal`（不新增协议码）；HTTP 响应仍只有固定 `code`/`message`，精确原因通过 Run 结论与运行日志观察。

## 9. 服务层不变量

沿用 `.agent/tasks/agent-http-api-v1/contract.md` 已批准的编号（不占用 `runtime-architecture.md` 的全局 `INV-0xx` 号段）：

| 编号 | 不变量 |
| --- | --- |
| INV-H01 | 仅 `COMPLETED` 且无错误才 `200`；其余终态与自相矛盾结果一律不得 `200` |
| INV-H02 | 认证先于 body 解析与 Run 执行；未认证零 `Runner` 调用、零会话写入；缺失/错误凭证同码 |
| INV-H03 | 会话错误只按哨兵分类，不退化为 `internal` / 泛化 5xx |
| INV-H04 | 非 `COMPLETED` 路径无任何会话写入，历史仍通过 `core.ValidateMessages` |
| INV-H05 | 一次 chat 恰好一次真实 `RunTurn`；用户消息、产出与摘要变更同事务提交或整轮丢弃 |
| INV-H06 | Run 期间不二次获取同一会话执行权；不重入、不死锁 |
| INV-H07 | 取消与超时穿透到 Run 且终态可区分；不得归为 `internal`；断开时不承诺响应可达 |
| INV-H08 | 默认回环监听；非回环无凭证启动失败且不监听 |
| INV-H09 | 凭证零泄漏：响应体/响应头/`/healthz`/`Config.String()`/日志字节中均不出现 |
| INV-H10 | 请求体有界、超限 413；非法输入各自稳定参数类 code；不 panic、不挂死 |
| INV-H11 | 关闭幂等；宽限期内请求完成，期满取消在途 Run 且不遗留执行中任务 |
| INV-H12 | v1 禁用压缩：`TokenBudget` 只接受 `0`，占位 `Summarizer` 零调用 |

## 10. 当前限制与验证边界

- 已有真实 Provider 普通对话和计算器调用记录，见[验证说明](../verification.md)。少量成功请求不证明默认 60 秒超时适合所有模型、长对话与负载。
- 客户端断开时响应不可达的观察口径：只能依赖终态、日志与 `Fake Provider` 的取消观察，无法验证客户端实际收到状态码。
- Todo 已接入默认启动入口：`service/todo.go` 完成 `*session.Turn` → `builtin.TodoOperations` 适配（含 ID 生成与状态迁移），`todo_create` / `todo_list` / `todo_complete` 的写入与消息同事务提交或整轮回滚。生产 LLM `Summarizer` 与真实 tokenizer 校准仍未接入，长会话触达 Provider 限制时以 `provider_failure`（502）结束。
- 流式输出、异步轮询、TLS、CORS、限流/配额、metrics、持久化、用户体系与 RBAC：未设计，属后续任务。

## 11. 演进规则

- 端点、字段、错误码、状态码映射、认证与监听边界属于公开协议：变更必须走 Contract Revision 并由 Owner 重新 `APPROVED`，随后同步本文件与 `runtime-architecture.md`。
- 错误码是封闭枚举：新增 code 必须同时更新本文件、实现与测试，不得由实现单方面引入。
- 本文件只记录对后续任务与客户端有约束力的长期事实，不记录任务级决策过程；旧版本由 Git 历史承载。
