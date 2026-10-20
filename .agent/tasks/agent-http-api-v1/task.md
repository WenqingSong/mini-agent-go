# Task: Agent Runtime HTTP JSON API（agent-http-api-v1）

## Goal

本地客户端能通过 HTTP JSON API 创建会话、发起一次 Agent Run，并拿到该轮最终结果或稳定、可判定的错误；服务默认不向公网暴露无认证接口，客户端取消与请求超时真实穿透到 Run，服务可启动并在终止信号下优雅关闭；全部 HTTP 自动化验收只用 Fake LLM，且正式 HTTP 契约沉淀为长期 Design。

## Scope

- 三个 Owner 指定端点（方法/路径固定）：`POST /v1/sessions` 创建会话、`POST /v1/chat` 执行一次 Agent Run、`GET /healthz` 健康检查。
- 请求解析与校验：`Content-Type`、JSON 结构（尾部数据、数组、`null`）、必填字段、未知字段、类型与长度限制；请求体有界读取（超限拒绝，不无界缓存、不挂死）。
- 统一错误封套与「错误码 → HTTP 状态码」单点映射：至少覆盖参数类错误、未认证、会话不存在/已过期/重复创建、请求体超限、不支持媒体类型、请求超时、客户端取消、Provider 失败、工具失败、内部错误。错误体只含稳定 `code` 与固定 `message`，不含路径、堆栈、Provider 原始载荷、会话正文或凭据。
- 会话与 Run 的编排接缝：`POST /v1/chat` 必须把本轮用户消息与 Agent 产出放进**同一次会话 Turn**（同一轮整体提交或整轮丢弃），不得出现只有 user 消息、重复 user 消息或半轮工具调用的历史；会话级失败按 `session` 包哨兵（`ErrSessionNotFound` / `ErrSessionExpired` / `ErrSessionExists`，`errors.Is`）分类，不按错误文本匹配。
- 取消与超时传递：客户端断开（请求 `context` 取消）与服务端请求级超时必须真实传递到 Run 的 `context.Context`，两者在终态或错误码上可区分，不得混为 `internal`/泛化 5xx；任何非 `COMPLETED` 路径不产生会话部分提交。
- 服务生命周期与可启动入口：可复用的服务构造 + 可启动命令；配置非法或缺少必需凭据时 fail-closed（返回错误且不进入监听）；监听就绪后 `GET /healthz` 可用；收到终止信号后停止接受新连接、宽限期内完成在途请求、宽限期外取消在途 Run，关闭幂等、`panic`-free、进程在限定时间内退出。
- 安全默认：默认监听回环地址；对外（非回环）监听必须启用凭证认证，缺失/错误凭证一律拒绝且不执行 Run；Provider 配置与 API Key 只从受控来源（环境变量或等价安全注入）读取，不进入命令行明文参数，也不出现在 HTTP 响应、错误体、`/healthz`、配置打印或日志中。
- 可测试性：Provider、工具注册表、Session Manager、日志 Sink、时钟/超时等外部依赖可注入；测试使用 Fake `llm.Provider` + 内存 `session.Manager` + `httptest` / 回环随机端口。
- Design（`Design Impact: NEW`）：新增 `docs/design/http-api.md` 作为长期对外 HTTP 契约（端点、请求/响应字段、错误封套与错误码→状态码、取消/超时语义、认证与默认监听边界、启动与关闭生命周期）；同步 `docs/design/runtime-architecture.md` §2 分层、§3 模块职责、§12 文件所有权表、§14 归属（登记新增 L3 入口与包/文件路径，并把本任务承担项与 T08 装配能力区分开）。
- 测试：端点契约与多轮、工具链路、错误与副作用表驱动、取消/超时、启动与优雅关闭、密钥泄漏扫描、分层方向与零第三方依赖的机械核对。

## Out of Scope

- 实现或修改 Agent Loop 本身（T07 `agent` 已交付）；默认不改 `agent` 公共 API，是否允许改由 Contract 与 Owner 决定（见 Relevant Context / OPEN QUESTION）。
- Todo 端口适配（`session.TodoOperations` → `builtin.TodoOperations`）、Todo ID 生成与状态迁移、Todo 端到端行为；以及生产 LLM `Summarizer` 适配器与真实 tokenizer 校准——这些仍属 `runtime-architecture.md` §14 的「服务装配任务（T08）」。
- 生产上下文压缩：v1 服务明确禁用压缩（`TokenBudget=0`），长会话触达 Provider 限制时按 `provider_failure` 结束，不实现摘要。
- 流式输出（SSE / WebSocket）、异步任务轮询、webhook 回调；只交付一次性请求-响应。
- 用户体系、注册/登录、角色/RBAC、多租户、OAuth/OIDC；只要求「默认不暴露无认证接口」。
- TLS 终结、反向代理、CORS、限流/配额、审计、metrics、分布式追踪、日志采集与轮转、日志检索界面。
- Session 持久化、跨进程共享、多实例一致性、重启恢复（现状为进程内存，属 L0–L2 既有契约）。
- 修改 L0–L2 既有公共契约与既有测试（`core/**`、`llm/**`、`tool/**`、`session/**`、`contextmgr/**`、`runlog/**`、`builtin/**`）；确需变更走 Contract Revision 回 Analyst / Owner。
- 引入第三方依赖（路由、校验、断言库等）或新增 `require`；修改 `go.mod` 的 module 与 go 版本、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）；在共享 `develop` 上提交。
- 部署与发布（Dockerfile、k8s、CI 发布阶段）、公网暴露、Vault / KMS 等密钥管理服务接入。
- 重试、熔断、连接池调优、Provider 侧重限流与鉴权（Provider 重试仍属独立任务）。
- 全项目 HTTP/日志规范推广；本任务只覆盖本服务暴露的三个端点与自身运行日志接线。

## Design Impact

Design Impact: NEW
Design Artifact: `docs/design/http-api.md`（新增：长期 HTTP 对外契约——端点、请求/响应 JSON、错误封套与错误码域、认证与默认监听边界、取消/超时语义、启动与关闭生命周期），并同步 `docs/design/runtime-architecture.md`（§2 分层、§3 模块职责、§12 文件所有权、§14 归属）

判定依据：本任务新增一套跨越单任务、约束后续客户端与部署的长期公开协议（端点、字段、错误码域、中断语义），并新增鉴权与监听安全边界，属于「新增长期公开协议 + 新增安全边界」；新增的 `docs/design/http-api.md` 是新的项目级设计事实载体。因此判为 `NEW` 而不是局部实现细节。

与 Git 历史记录的差异：本 slug 在 2026-10-20 曾有一份已删除的 Contract（commit `ec2072c`），其中 Owner D5 批准的是 `Design Impact = UPDATE`、同样新增 `docs/design/http-api.md`。两种归类指向同一交付物（新增文件 + 同步既有文档），区别仅在分类口径。本文件按 Owner 本次需求的 `NEW` 记录；Analyst 在 Contract 中应就此口径与 Owner 校正，并保证无论取 `NEW` 还是 `UPDATE`，Design Artifact 都在 Scope / AC 内。

## Acceptance Criteria

- [ ] AC-001：给定服务以测试配置启动并注入 Fake Provider，当客户端 `POST /v1/sessions` 成功后用返回的会话标识 `POST /v1/chat` 提交一条用户消息，则响应给出该会话标识、Run 标识、`COMPLETED` 终态与该轮最终助手文本；当同一会话再次 `POST /v1/chat` 时，Fake Provider 收到的请求中包含上一轮的用户消息与助手回复（多轮历史由 Session 提供，不由客户端回传）。
- [ ] AC-002：给定 Fake Provider 先返回带 `tool_calls` 的 assistant 响应、再返回最终文本，当客户端发起一次 `POST /v1/chat` 时，HTTP 响应返回该轮最终文本（不是中间的 tool_call 载荷）、终态 `COMPLETED`，被请求的工具确实被执行（Fake 记录到调用与参数），响应中的 LLM/工具调用计数与脚本一致。
- [ ] AC-003：给定无效 Session、非法输入与内部异常，当客户端请求时系统返回明确错误，且都不产生 Session/Run 副作用：① 未知或已过期会话标识 → 会话类错误码（不得退化为 `internal` 或泛化 5xx），且不执行 Run；② 非法 JSON、缺少必填字段、未知字段、类型/长度非法、错误 `Content-Type`、超限请求体 → 各自稳定的参数类错误码与状态码，服务不 panic、连接不被静默挂死；③ Provider 失败等内部/上游异常 → 稳定 5xx 错误码，响应体不含 Provider 原始载荷、文件路径、堆栈或凭据；④ 以上任一情况下会话历史与 Todo 列表与请求前逐条一致。
- [ ] AC-004：默认配置下服务只监听回环地址（不监听 `0.0.0.0` 或公网地址）；当配置为对外监听时必须要求凭证：缺失凭证与错误凭证返回同一认证类错误码（401），两种情况都不执行 Agent Run（Fake Provider 调用计数为 0、Session 无写入），错误体不回显预期凭证；`/healthz` 不泄漏配置、密钥或内部状态。
- [ ] AC-005：全部 HTTP 自动化测试在进程内以 `httptest` / 本地回环随机端口 + Fake `llm.Provider` + 内存 `session.Manager` 完成，不依赖真实模型、真实 API Key、外部网络、MySQL/Redis/MQ/Docker；测试注入的假密钥值不得出现在任何 HTTP 响应体、响应头、`/healthz` 输出与捕获的日志字节中。
- [ ] AC-006：给定 APPROVED Contract，`docs/design/http-api.md` 已沉淀端点语义、请求/响应 JSON、错误封套与错误码→状态码映射、取消/超时语义、认证与默认监听边界、启动/关闭生命周期，并逐项与实现一致（方法/路径、字段名、错误码、状态码可机械核对）；`docs/design/runtime-architecture.md` §2/§3/§12/§14 已登记本任务新增的 L3 入口与文件路径，§12 新增路径与 `git ls-files` 差集为空；新包只向下依赖、L0–L2 未 import 新包；`git diff go.mod` 为空（零第三方依赖）。
- [ ] AC-007：给定阻塞到 `ctx` 结束的 Fake Provider，当客户端在 Run 中断开连接（请求 context 取消）时，Run 的 `ctx` 被取消、Fake Provider 观察到取消，会话历史不出现部分提交（`session.Manager.Get` 取回的消息序列仍通过 `core.ValidateMessages`，且不含半轮调用）；当请求级超时触发时，Run 终态为 `CANCELLED` + `deadline_exceeded`（不得为 `FAILED` 或 `internal`），连接仍可写时返回契约规定的超时错误码，且与用户主动取消在错误码或终态上可区分。
- [ ] AC-008：给定回环随机端口启动服务，`GET /healthz` 在监听就绪后返回成功、就绪前不可达；配置非法（端口非法、缺少必需凭据等）时启动返回明确错误且未进入监听；收到终止信号（或等价关闭入口）后停止接受新连接，已进入处理的请求在宽限期内完成并返回正确结果，宽限期后仍在途的 Run 被取消（Fake Provider 观察到取消），进程在限定时间内退出且不 panic，重复关闭不 panic。

AC-007 / AC-008 的来源：Owner 需求「实施内容」中的「传递客户端取消和请求超时」「实现服务启动及优雅关闭」与「验证」中的「取消测试、服务启动及优雅关闭测试」。它们不引入新的业务规则，只是把 Owner 已指定的行为写成可观察验收条件；AC-001～AC-006 与 Owner 编号一一对应。

## Relevant Context

已核实事实（代码与仓库现状）：

- 分层现状：L0 `core`；L1 `llm` / `tool` / `session` / `contextmgr` / `runlog`；L2 `builtin`；L3 `agent` 均已交付。`go list ./...` 当前只有 9 个包，**不存在** `httpapi` / `service` / `cmd` 等入口或装配代码；`docs/design/` 只有 `agent-workflow.md` 与 `runtime-architecture.md`，**不存在** `http-api.md`。
- `agent.Loop.Run(ctx, RunRequest)` 的入参是 `RunRequest{core.ExecutionRef; core.RunPolicy}`，**不接受用户消息**（`agent/agent.go:63-67`、`:100`）；它内部自己调用 `cfg.Sessions.RunTurn` 取得会话执行权并完成整轮提交（`agent/agent.go:115`）。`agent.Config.Sessions` 是可注入接口 `SessionRunner{RunTurn(ctx, sessionID, fn)}`（`agent/agent.go:34-37`、`:43`），`*session.Manager` 结构匹配。
- `session.Manager.RunTurn` 在同一会话的执行权内执行回调（`session/manager.go:188`），因此「先提交用户消息再 Run」会留下半轮历史，「外层 `RunTurn` 内再调用 `Loop.Run`」会重入同一会话执行权。用户消息与 Run 如何落在同一 Turn，是本任务实现前必须由 Contract 定稿的装配接缝。
- `session.Manager` 提供 `Create/Get/Delete/PurgeExpired/RunTurn/Load/Save`（`session/manager.go:66`、`:95`、`:130`、`:146`、`:188`、`:224`、`:233`）；`ManagerConfig{TTL, Now}`（`session/manager.go:16-19`）；会话哨兵 `ErrSessionNotFound` / `ErrSessionExpired` / `ErrSessionExists` 挂在 `*core.RunError.Cause` 上，`errors.Is` 可判。
- `contextmgr.NewManager(estimator, summarizer)` 两个参数都必须非 nil（`contextmgr/manager.go:30-41`）；`Build` 在 `TokenBudget <= 0` 时原样返回视图，不调用 `Summarizer`（`contextmgr/manager.go:66-75`）。仓库内**没有**生产 `Summarizer` 实现。
- `llm.Provider` 为 `Complete(ctx, llm.Request) (llm.Response, error)`（`llm/provider.go:86-88`）；`llm/openai.Config{BaseURL, APIKey, HTTPClient, Timeout}`，`APIKey` 为空时不发送 `Authorization`（`llm/openai/client.go:41-50`、`:64`）。因此「API Key 注入」在本任务只需从受控来源读取后交给 Provider 构造，本任务不新增 Provider 实现。
- `builtin` 暴露 `NewCalculator()` / `NewMockSearch()` / `NewTodoCreate|List|Complete(ops)` / `All(ops TodoOperations)`（`builtin/*.go`）；Todo 工具需要逐 Turn 的 `TodoOperations`，本任务不注册 Todo 工具。
- `tool.NewMapRegistry()`（`tool/registry.go:41`）、`runlog.NewJSONSink(io.Writer)`（`runlog/slog.go:40`）均存在，可作为装配素材；`runlog` 已有脱敏 JSON Sink，HTTP 层日志必须旁路（`Sink` 失败不得影响响应、Run 状态与计数）。
- 既有领域失败 JSON 形状为 `{"error":{"code":"<snake_case>","message":"<稳定说明>"}}`（`builtin`），HTTP 错误封套可沿用同一词汇；具体封套与错误码集合由 Contract 定稿。
- 工程：根 module `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，零 `require`；`make test` = `go test ./...`；本机 `go1.24.1 linux/amd64`；基线 `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- `.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空），不申请 migration / 错误码域等 Registry 资源。
- T07 前置已满足：`agent-runtime-loop-v1` 的 `state.yaml` 为 `contract=APPROVED`、`review=CLEAN`、`owner=ACCEPTED`、`delivery=PASS`，并已合并入 `develop`（`6bebdda`）。
- Git 基线：分支 `feat/agent-http-api-v1`，HEAD `f79e033`，与 `origin/feat/agent-http-api-v1` 一致；`develop`（`6bebdda`）是 HEAD 的祖先；`git status --short` 为空。
- 本 slug 的历史事实：该分支历史上已有过本任务的 Contract（`9f0613d`、`ec2072c`），并在 `f79e033` 提交中删除了 `.agent/tasks/agent-http-api-v1/` 下全部 artifacts；当前目录内**没有** `contract.md`，因此上述历史 Contract 不是本任务当前的已确认 Contract。历史 Contract 中 Owner 已于 2026-10-20 对 D1–D5 作出书面决定，其要点为：D1 新增最小 `service` 装配层复用当前 Turn、不改 `agent` 公共 API；D2 不实现生产压缩/Todo 工具/生产 `Summarizer`；D3 Git 基线由 Owner 合并后由 TaskBuilder 回写；D4 静态 Bearer 认证 + 服务端请求超时 + 成功响应最小字段；D5 新增 `docs/design/http-api.md` 并同步 `runtime-architecture.md`。这些是 Owner 的既有意图，Analyst 在重新形成 Contract 时应作为输入参考并在 Contract Decision 中重新确认。
- 编号事实冲突：`docs/design/runtime-architecture.md` §14 把「服务装配任务（T08）」分配给 Todo 端口适配 + 生产 `Summarizer` + Provider 接线；本次 Owner 需求把 `agent-http-api-v1` 称为「Task 08」。历史 Contract 记录 Owner 曾裁定「保留 T07 已批准 Contract 的 T08 归属，本任务另行分配编号（建议 T09）」，但该裁定随 artifacts 删除未留在当前任务事实中。

Assumption：

- HTTP 错误码是本任务契约内的封闭枚举（协议常量），按协作规范属 B 类命名空间资源，不需要新增 Shared Resource Kind 或在 Registry 注册。
- 本任务全部 AC 可在进程内证明（`httptest` + Fake Provider + 内存 `session.Manager` + `-race`），不需要真实 MySQL/Redis/MQ/Docker 或外部网络。
- 测试环境为 Linux + Go 1.24.x，使用回环随机端口（`127.0.0.1:0`），不依赖固定端口。
- Design 文档由 Analyst 在 Contract `APPROVED` 后、Coder 实现前落库；Coder 只做一致落地与 §12 路径登记。

OPEN QUESTION（均不阻塞本任务定义，但必须在 Coder 开始实现前由 Analyst 定稿、必要时由 Owner 决定）：

1. 装配接缝：如何在**不修改** `agent.Loop.Run` 公共 API 的前提下，让用户消息与 Agent 产出落在同一次 `session.Manager.RunTurn` 内（历史 D1 选择新增最小装配层；若 Analyst 判定必须改 `agent` 公共 API，属 Scope 扩张，回 Owner）。
2. 编号与 Design Impact 口径：本任务称「Task 08」与 §14 既有 T08 归属（Todo 适配 + 生产 `Summarizer`）冲突；本文件按 Owner 需求记 `Design Impact = NEW`，历史 D5 为 `UPDATE`。需在 Contract 期与 Owner 校正，并确保 Design/§14 不把两者混同。
3. 认证凭证形态（环境变量静态凭证或其他注入方式）、`/healthz` 是否免认证、「非回环监听 + 无凭证」的 fail-closed 具体行为，涉及安全边界，需 Owner 在 Contract Decision 中确认。
4. 请求级超时的来源与取值、宽限期取值、客户端断开且响应已不可达时的证据口径（终态 + 日志）。
5. 会话不存在时的预检与 TOCTOU：HTTP 层是否在调用 Run 前先 `Get` 一次会话（影响「无效会话不执行 Run」的可观察性定义）。

## Verification

- AC-001 → `httptest.Server` + Fake Provider：断言创建响应含会话标识、chat 响应含 `run_id` / 终态 / 最终文本；第二次 chat 时检查 Fake Provider 收到的 `llm.Request.Messages` 含首轮用户消息与助手回复。
- AC-002 → Fake Provider 按脚本先回 `tool_calls` 再回文本；Fake Tool 记录调用与参数；断言只返回最终文本、工具被调用、LLM/工具计数与脚本一致。
- AC-003 → 表驱动：未知/过期会话、坏 JSON（数组 / `null` / 尾部数据）、缺字段、未知字段、类型/长度非法、错误 `Content-Type`、超限 body、Provider 失败。断言状态码 + 稳定错误码 + 响应体不含内部细节；每例前后用 `session.Manager.Get` / `Todos` 逐条比对无副作用。
- AC-004 → 默认配置断言监听主机为回环；对外监听 + 无凭证 / 错凭证断言 401 + 同一错误码 + Fake Provider 调用计数 0 + 无会话写入；`/healthz` 响应体扫描密钥与内部字段。
- AC-005 → 全套 HTTP 测试在无网络、无真实密钥条件下通过；对捕获的响应、响应头与日志字节做假密钥子串扫描（断言不出现）。
- AC-006 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对：逐条比对方法/路径、字段名、错误码、状态码；`runtime-architecture.md` §12 路径与 `git ls-files` 差集为空；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 分层核对；`git diff go.mod` 为空。
- AC-007 → 阻塞式 Fake Provider + 客户端 `context.WithCancel` 断开：断言 Provider 观察到 `ctx.Done()`、会话无部分提交；再用短请求超时断言 `CANCELLED` + `deadline_exceeded` 且与取消可区分。
- AC-008 → 以 `127.0.0.1:0` 启动，轮询 `/healthz` 验证就绪；非法配置断言启动返回错误且未监听；触发关闭断言新连接被拒、在途请求完成、宽限期后 Run 被取消、进程返回、重复关闭不 panic。
- 通用检查：`make test`、`go build ./...`、`go vet ./...`、`go test -race ./...` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 仍通过。环境：Linux + Go 1.24.x，无需外部服务。

## Complexity

COMPLEX

原因：本任务在实现前必须先定稿四类会长期约束外部客户端与后续任务的约束——(1) 新的对外公开协议（端点、JSON 字段、错误封套与错误码→状态码映射、取消与超时语义），一旦落地即成为客户端兼容约束；(2) 安全边界（默认回环监听、非回环 fail-closed、凭证校验与比较方式、Provider 密钥注入与零泄漏口径）；(3) 装配接缝——现有 `agent.Loop.Run` 不接受用户消息且内部自取会话执行权，而用户消息与 Run 必须同 Turn 提交，方案选择会改变是否修改 T07 公共 API；(4) 生命周期与在途请求取消（fail-closed 启动、宽限期、关闭期取消），与 Session 原子提交语义耦合。上述均属「重要架构 / 公开协议 / 安全边界」判断项，需 Analyst 出 Contract 并由 Owner 确认后再交 Coder。

## Analyst Questions

1. 装配接缝：在不改 `agent.Loop.Run`（历史 D1）的前提下，如何把用户消息与 Agent 产出放进同一次 `session.Manager.RunTurn`（新增装配层 + 同 Turn 注入适配器？）；若必须改 `agent` 公共 API，代价与替代方案是什么，需 Owner 批准哪些范围变化？如何用测试钉死「一次 chat 恰好一次真实 `RunTurn`、无重入死锁、无半轮提交」。
2. HTTP 契约定稿：三个端点的请求/响应字段（`POST /v1/chat` 是否返回消息序列 / usage / 计数）、错误封套形状、错误码封闭枚举与状态码映射子集、路径与方法不符的响应（404/405 + `Allow`）、`run_id` 生成方式与是否返回。
3. 认证与监听：凭证形态与来源（环境变量名）、比较方式（常量时间）、缺失与错误凭证是否必须同码、`/healthz` 是否免认证、非回环监听是 fail-closed 还是允许但强制认证。
4. 取消与超时：请求级超时来源（服务端配置）与取值、宽限期取值、客户端断开后已提交 Turn 的处理（既定：不回滚，只需可观察终态与日志）、连接不可达时如何留下证据。
5. 密钥注入与脱敏：环境变量集合、启动时缺失是否 fail-closed、配置结构能否被 `%+v` 打印出明文、日志与 `/healthz` 的零泄漏断言口径，以及 Provider 失败时不回显原始载荷的具体规则。
6. 错误与副作用：非法输入、未知会话、Provider 失败、取消/超时四条路径下 HTTP 层是否有任何会话写入；是否需要 `request_id` 且不泄漏内部信息。
7. 当前范围边界确认：v1 是否明确禁用压缩（`TokenBudget=0` + fail-closed 占位 `Summarizer`）、不注册 Todo 工具、只注册 `calculator`（与历史 D2 一致）；后续 Todo 适配与生产 `Summarizer` 是否仍归 T08。
8. Design 落点：`docs/design/http-api.md` 章节结构；`runtime-architecture.md` §2/§3/§12/§14 的最小改动集；新包路径、包名与命令入口名及文件清单（供 §12 登记）；编号与 `Design Impact` 分类（`NEW` vs 历史 `UPDATE`）的最终口径。

## Review Baseline

- Base commit：`f79e033`（`chore(agent): 移除 agent-http-api-v1 任务目录下全部契约与状态文件`）。当前分支 `feat/agent-http-api-v1` 指向 `f79e033`，与 `origin/feat/agent-http-api-v1` 一致；`develop`（`6bebdda`）是 HEAD 的祖先。
- 任务开始时已有修改：无。`git status --short` 为空，工作区 clean，无未跟踪文件；基线 `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- 历史遗留的区分方式：本分支在 `9f0613d` / `ec2072c` 曾提交过本任务 Contract，`f79e033` 已将其全部删除。这些历史内容不属于当前交付物，也不能被 Cleaner 当作「已确认 Contract」；需要参考时以 Git 历史为证据并在 findings / contract 中显式引用。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/agent-http-api-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；`contract.md` 由 Analyst 维护；新包与测试由 Coder 在独立 commit 引入；`docs/design/*` 由 Analyst 在 Contract `APPROVED` 后更新；`agent/**` 与 L0–L2 不在本任务修改范围。发现他人遗留 dirty 文件 → STOP，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX，`contract.status = PENDING`）。
