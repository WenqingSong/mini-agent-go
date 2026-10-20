# Task: Agent Runtime HTTP JSON API（agent-http-api-v1）

## Goal

本地客户端能通过 HTTP JSON API 创建会话、发起一次 Agent Run 并拿到最终结果或明确错误：默认不向公网暴露无认证接口，客户端取消与请求超时能真实传递到 Run，服务可启动并优雅关闭；全部 HTTP 自动化验收只依赖 Fake LLM。

## Scope

- 三个 Owner 已指定的端点（方法/路径固定）：`POST /v1/sessions` 创建会话、`POST /v1/chat` 执行一次 Agent Run、`GET /healthz` 健康检查。
- 请求解析与校验：`Content-Type`、JSON 结构、必填字段、未知字段、类型与长度、请求体大小上限；超限与非 JSON 的拒绝行为确定，且不在内存中无界读取请求体。
- 统一错误封套与「错误码 → HTTP 状态码」映射：至少覆盖参数非法、未认证、会话不存在 / 已过期 / 重复创建、请求体超大、不支持的媒体类型、请求超时、客户端取消、内部错误、Provider 失败。错误体只含稳定 code 与通用消息，不回显内部细节（文件路径、堆栈、Provider 原始载荷、凭据）。
- 会话与 Run 的编排边界：`POST /v1/chat` 通过 T07 装配层提供的 Agent Run 执行入口运行；会话级失败消费 `session` 包哨兵（`ErrSessionNotFound` / `ErrSessionExpired` / `ErrSessionExists`，用 `errors.Is` 判定）后再映射，不按错误文本匹配。
- 取消与超时传递：客户端断开（请求 context 取消）与请求级超时真实穿透到 Run 的 `context.Context`；两者分别映射 `cancelled` / `deadline_exceeded`，不得混为 `internal`；不产生会话部分提交（依赖 `session.Manager.RunTurn` 的原子性，HTTP 层不得自行写入半成品历史）。
- 服务生命周期：可启动入口（二进制 / 命令，路径与名称由 Contract 定稿）与可复用的 HTTP 服务构造；启动失败 fail-closed（配置非法或缺少必需凭据时不进入监听）；收到终止信号后停止接受新连接、宽限期内完成在途请求、宽限期外取消在途 Run，关闭幂等。
- 安全默认：默认监听回环地址；对外监听必须启用凭证认证，缺失/错误凭证一律拒绝且不执行 Run；Provider 配置与 API Key 只从受控来源（环境变量或等价安全注入）读取，不进入命令行明文参数，不进入任何 HTTP 响应、错误体、`/healthz` 输出与日志记录。
- 可测试性：Provider、工具、Session Manager、日志 Sink 等外部依赖可注入，测试使用 Fake `llm.Provider` + 内存 `session.Manager`。
- Design（Design Impact = NEW）：新增 `docs/design/http-api.md` 作为长期 HTTP 对外契约；同步 `docs/design/runtime-architecture.md` §2 分层、§3 模块职责、§12 文件所有权表（登记 L3 入口与新包路径）。
- 测试：`httptest` / 本地回环端到端、错误与边界表驱动、取消与超时、启动与优雅关闭、零第三方依赖与分层方向的机械核对。

## Out of Scope

- 实现 T07 装配层本身（Agent Loop 编排、工具调度与失败回填、`session.TodoOperations` → `builtin.TodoOperations` 适配、Todo ID 生成与状态迁移、生产 `Summarizer` 适配）——它是本任务的前置依赖，不在本任务重复实现。
- 流式输出（SSE / WebSocket）、异步任务轮询、webhook 回调；只交付一次性请求-响应。
- 用户体系与授权模型（注册 / 登录 / 角色 / RBAC）、多租户、OAuth/OIDC 接入；只要求默认不暴露无认证接口。
- TLS 终结、反向代理配置、CORS 开放策略、限流、配额、审计、metrics、分布式追踪。
- Session 持久化、跨进程共享、多实例一致性、重启恢复（现状为进程内存，属 L0–L2 既有契约）。
- 修改 L0–L2 既有公共契约（`core/**`、`llm/**`、`tool/**`、`session/**`、`contextmgr/**`、`runlog/**`、`builtin/**`）；确需变更走 Contract Revision 回 Analyst / Owner。
- 引入第三方依赖（路由、参数校验、测试断言库等）或新增 `require`；修改 `go.mod` 的 module 与 go 版本、`Makefile`、`.cnb.yml`、`.agent/**`（本任务目录除外）。
- 部署与发布（Dockerfile、k8s、CI 发布阶段）、公网暴露、密钥管理服务（Vault / KMS）接入。
- 重试、熔断、连接池调优与 Provider 侧重限流（Provider 重试仍是独立任务）。

## Design Impact

Design Impact: NEW
Design Artifact: `docs/design/http-api.md`（新增：长期 HTTP 对外契约 / 错误码域 / 认证与监听边界 / 生命周期），并同步 `docs/design/runtime-architecture.md`（§2 分层、§3 模块职责、§12 文件所有权）

判定依据：本任务新增一套对外长期公开协议（HTTP 端点、请求/响应 JSON、错误码→状态码映射、取消与超时语义），并新增 L3 入口与安全边界（默认监听面 + 认证要求）。这些事实跨越单个任务、约束后续客户端与部署，因此是 `NEW`，不是局部实现细节。`runtime-architecture.md` 的改动只因 §12 要求新增路径必须在同一交付内登记，§2/§3 需要把「L3 未实现」更新为已交付入口。

## Acceptance Criteria

- [ ] AC-001：给定服务以测试配置启动并注入 Fake Provider，当客户端 `POST /v1/sessions` 成功后用返回的会话标识 `POST /v1/chat` 提交一条用户消息，则响应给出该会话标识、Run 标识、`COMPLETED` 终态与该轮最终助手文本；当同一会话再次 `POST /v1/chat` 时，Fake Provider 收到的请求中包含上一轮的用户消息与助手回复（多轮历史由 Session 提供，而不是由客户端回传）。
- [ ] AC-002：给定 Fake Provider 先返回带 `tool_calls` 的 assistant 响应、再返回最终文本，当客户端发起一次 `POST /v1/chat` 时，HTTP 响应返回的是最终文本（不是中间的 tool_call 载荷）、终态 `COMPLETED`，且被请求的工具确实被执行（Fake 记录到调用及其参数），Run 的 LLM / 工具调用计数与脚本一致。
- [ ] AC-003：给定非法输入与内部异常，当客户端请求时系统返回明确错误，且都不产生 Session / Run 副作用：① 未知或已过期会话标识 → 会话类错误码（不得退化为 `internal` 或 5xx 泛化错误），且不执行 Run；② 非法 JSON、缺少必填字段、未知字段、错误 `Content-Type`、超限请求体 → 各自稳定的参数类错误码与状态码，服务不 panic，连接不被静默挂死；③ Fake Provider 返回 `provider_failure` → 内部/上游类错误码，响应体不含 Provider 原始载荷、文件路径、堆栈或凭据；④ 以上任一情况下会话历史与 Todo 列表与请求前逐条一致。
- [ ] AC-004：默认配置下服务只监听回环地址（不监听 `0.0.0.0` 或公网地址）；当配置为对外监听时必须要求凭证：缺失凭证与错误凭证返回同一认证类错误码（401），两种情况都不执行 Agent Run（Fake Provider 调用计数为 0、Session 无写入），错误体不回显预期凭证；`/healthz` 不泄漏配置、密钥或内部状态。
- [ ] AC-005：全部 HTTP 自动化测试在进程内以 `httptest` / 本地回环随机端口 + Fake `llm.Provider` + 内存 `session.Manager` 完成，不依赖真实模型、真实 API Key、外部网络、MySQL / Redis / MQ / Docker；测试注入的假密钥值不得出现在任何 HTTP 响应体、响应头、`/healthz` 输出与捕获的日志字节中。
- [ ] AC-006：给定一个阻塞直到 `ctx` 结束的 Fake Provider，当客户端在 Run 进行中断开连接（请求 context 取消）时，Run 的 `ctx` 被取消、Fake Provider 观察到取消，会话历史不出现部分提交（`session.Manager.Get` 取回的消息序列仍通过 `core.ValidateMessages`，且不含半轮调用）；当请求级超时触发时，Run 终态为 `CANCELLED` + `deadline_exceeded`（不是 `FAILED` / `internal`），连接仍可写时返回契约规定的超时错误码，且与用户主动取消在错误码或终态上可区分。
- [ ] AC-007：给定随机回环端口启动服务，`GET /healthz` 在监听就绪后返回成功、就绪前不可达；配置非法（端口非法、缺少必需凭据等）时启动失败并返回明确错误，不进入监听状态；收到终止信号后停止接受新连接（新连接被拒绝），已进入处理的请求在宽限期内完成并返回正确结果，宽限期结束后仍在途的 Run 被取消（Fake Provider 观察到取消），进程在限定时间内退出且不 panic，重复关闭不 panic。
- [ ] AC-008：给定 APPROVED Contract，`docs/design/http-api.md` 已沉淀端点语义、请求/响应 JSON、错误封套与错误码→状态码映射、取消/超时语义、认证与默认监听边界、启动/关闭生命周期；逐项与实现一致（路由方法/路径、字段名、错误码、状态码可机械核对）；`docs/design/runtime-architecture.md` §2/§3/§12 已登记本任务新增的 L3 入口与文件路径，§12 新增路径与 `git ls-files` 差集为空；`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./...` 显示新包只向下依赖、L0–L2 未 import 新包，`git diff go.mod` 为空（零第三方依赖）。

## Relevant Context

已核实事实：

- 分层现状：L0 `core`；L1 `llm` / `tool` / `session` / `contextmgr` / `runlog`；L2 `builtin` 均已交付。`docs/design/runtime-architecture.md` §2 把 L3「Agent Loop / 装配层」明确标注为**未实现**，仓库内无任何 HTTP 服务或装配代码。
- T07（装配层）在 `.agent/tasks/` 中**尚不存在**：目录内只有 T00–T06 七个任务（`runtime-core-contracts-v1`、`llm-openai-client-v1`、`tool-registry-executor-v1`、`builtin-agent-tools-v1`、`agent-session-manager-v1`、`agent-context-manager-v1`、`runtime-observability-v1`），也没有对应 feature 分支。见 OPEN QUESTION。
- `session.Manager` 已提供 `Create` / `Get` / `Delete` / `PurgeExpired` / `RunTurn` / `Load` / `Save`；`RunTurn(ctx, sessionID, fn)` 在会话执行权内原子提交（失败、取消、超时、panic 都不产生部分提交）；会话级失败用哨兵 `ErrSessionNotFound` / `ErrSessionExpired` / `ErrSessionExists` + `*core.RunError{KindInvalidArgument}` 表达，可 `errors.Is` 区分。
- `llm.Provider` 为 `Complete(ctx, llm.Request) (llm.Response, error)`；`llm/openai.Config{BaseURL, APIKey, HTTPClient, Timeout}` 中 `APIKey` 为空时不发送 `Authorization` 头。因此「API Key 注入」在 HTTP 层只需从受控来源读取并交给 Provider 构造，本任务不新增 Provider 实现。
- 既有领域失败 JSON 形状为 `{"error":{"code":"<snake_case>","message":"<稳定说明>"}}`（`builtin`），HTTP 错误封套可复用同一词汇；具体封套与错误码集合由 Contract 定稿。
- `runlog` 已有脱敏 JSON Sink；HTTP 层写日志必须旁路（`Sink` 失败不得影响响应、Run 状态与计数，INV-008）。
- 工程：根 module `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，零 `require`；`make test` = `go test ./...`；本机 `go1.24.1 linux/amd64`；基线 `go build ./...`、`go vet ./...`、`go test ./...` 全部通过。
- `.agent/workflow.yaml` 的 `resources: {}`，当前无已声明 Shared Resource Kind；本任务不占用任何全局资源（`resources.reservations` 为空），不申请 migration、错误码域等 Registry 资源。
- Git 基线：分支 `feat/agent-http-api-v1`，HEAD `dc734d2`（`merge: 合并 feat/runtime-observability-v1 到 develop`），与本地 `develop`、`origin/develop`、`origin/feat/agent-http-api-v1` 完全一致；`git status --short` 为空。
- Owner 本次未提供 Deliverer 里程碑标识，因此本文件未声明 `Milestone`；若 Owner 要启用里程碑运行验收，需先给出标识（TaskBuilder 不自行推断）。

Assumption：

- T07 将提供「可注入 Provider 的 Agent Run 执行入口」，HTTP 层只消费该入口做编排，不在本任务重复实现 Loop；入口符号以 T07 的 APPROVED Contract 为准。
- HTTP 错误码是本契约内的封闭枚举（纯协议常量），不是项目级 registry 资源，不需要新的 Shared Resource Kind。
- 测试环境为 Linux + Go 1.24.x，使用回环随机端口，不需要固定端口、Docker 或外部服务。

OPEN QUESTION：

- **阻塞 Coder 启动（不阻塞任务定义）**：T07 尚未创建或合并，装配入口签名未知。Analyst 需在 T07 落地后固定 HTTP 层与装配层的边界；若 Owner 希望 T08 不等待 T07，则「在 T08 内定义最小装配入口」会扩大 Scope，必须由 Owner 决定后回写本文件。
- 认证凭证形态（环境变量静态凭证 / 其他注入方式）与「对外监听 fail-closed」的具体行为涉及安全边界，需 Owner 在 Contract Decision 中确认；本文件只固定外部可观察结果（默认回环、无凭证不执行 Run）。
- 请求级超时的来源与语义（服务端配置 deadline 还是客户端可指定、宽限期取值）需 Contract 定稿；本文件只要求取消与超时可区分且不产生部分提交。

## Verification

- AC-001 → `httptest.Server` + Fake Provider：断言响应字段、终态与最终文本；第二次 chat 时检查 Fake Provider 收到的 `llm.Request.Messages` 含首轮用户消息与助手回复。
- AC-002 → Fake Provider 按脚本先回 `tool_calls` 再回文本；Fake Tool 记录调用与参数；断言只返回最终文本、工具被调用、LLM / 工具计数与脚本一致。
- AC-003 → 表驱动：未知/过期会话、坏 JSON、缺字段、未知字段、错误 `Content-Type`、超限 body、Fake Provider 失败。断言状态码 + 稳定错误码 + 响应体不含内部细节；每例前后用 `session.Manager.Get` / `Todos` 比对无副作用。
- AC-004 → 默认配置下断言监听地址主机为回环；对外监听 + 无凭证 / 错凭证断言 401 + 相同错误码 + Fake Provider 调用计数为 0 + 无会话写入；`/healthz` 响应体扫描密钥与内部字段。
- AC-005 → 全套 HTTP 测试在无网络、无真实密钥条件下通过；对捕获的响应与日志字节做密钥子串扫描（断言不出现）。
- AC-006 → 阻塞式 Fake Provider + 客户端 `context.WithCancel` 断开：断言 Provider 观察到 `ctx.Done()`、Session 无部分提交；另用短请求超时断言 `CANCELLED` + `deadline_exceeded`，且与取消可区分。
- AC-007 → 以 `127.0.0.1:0` 启动，轮询 `/healthz` 验证就绪；非法配置断言启动返回错误且未监听；触发关闭（关闭函数 / 信号路径）断言新连接被拒、在途请求完成、宽限期后 Run 被取消、进程返回、重复关闭不 panic。
- AC-008 → Cleaner 做 Task ↔ Contract ↔ `docs/design/http-api.md` ↔ 实现四方核对：逐条比对路由、字段、错误码、状态码；§12 与 `git ls-files` 差集为空；`go list` 分层核对；`git diff go.mod` 为空。
- 通用检查：`make test`、`go build ./...`、`go vet ./...`、`go test -race ./...` 退出码 0 且有真实测试输出；`GOWORK=off go -C .agent/engine test ./...` 仍通过。环境：Linux + Go 1.24.x，无需外部服务。

## Complexity

COMPLEX

原因：需要先定稿三类长期约束：(1) 新的对外公开协议（端点、JSON 结构、错误码→状态码映射、取消与超时语义），一旦落地即成为客户端兼容约束；(2) 安全边界（默认监听面、认证要求、Provider 密钥注入与不泄漏），不同方案的风险差异明显；(3) 服务生命周期与在途请求取消（fail-closed 启动、优雅关闭、宽限期），它与 Session 的原子提交语义耦合。此外任务前置的 T07 装配入口尚未落地，HTTP 层与 Runtime 的边界必须由 Analyst 固定，不能让 Coder 自行猜测。

## Analyst Questions

1. HTTP 契约定稿：三个端点的请求/响应字段、错误封套（是否沿用 `{"error":{"code","message"}}`）、错误码枚举与状态码映射的确切子集（400 / 401 / 404 / 408 / 409 / 413 / 415 / 429 / 500 / 502 / 504）、`/v1/chat` 响应是否包含消息序列 / 调用计数 / usage。
2. 认证与监听：默认是否强制回环；非回环监听是 fail-closed（拒绝启动）还是允许但强制认证；凭证形态与来源（环境变量名）、比较方式、缺失与错误凭证是否必须返回同一错误码；`/healthz` 是否也需要认证。
3. 取消与超时：请求级超时从哪里来（服务端配置 / 客户端参数）、宽限期取值、客户端断开后已完成的 Turn 是否提交、连接已断时如何表达错误（无响应时如何留证据）。
4. T07 边界：装配入口（Run 执行、Provider 注入、工具注册、Todo 适配、Summarizer）由 T07 提供哪些符号；T08 是否只交付 handler + server 构造 + 启动入口；若 T07 未落地，Scope 是否需要扩展由 Owner 决定。
5. 密钥注入：环境变量集合、启动时缺失密钥是否 fail-closed、配置结构是否可能出现被 `%+v` 打印的明文字段、日志与健康检查的脱敏断言口径。
6. 错误与副作用：非法输入、未知会话、Provider 失败、超时取消四种路径下 HTTP 层是否有任何会话写入；是否需要 `request_id` 便于排查且不泄漏内部信息。
7. Design 落点：`docs/design/http-api.md` 的章节结构；`runtime-architecture.md` §2/§3/§12 的最小改动集；HTTP 层包路径、`cmd` 入口名与文件清单（供 §12 登记）。

## Review Baseline

- Base commit：`dc734d2`。当前分支 `feat/agent-http-api-v1` 指向 `dc734d2`，与本地 `develop`、`origin/develop`、`origin/feat/agent-http-api-v1` 全部一致。
- 任务开始时已有修改：无。`git status --short` 为空，无暂存与未跟踪文件；基线 `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
- 重叠修改的区分方式：TaskBuilder 只拥有 `.agent/tasks/agent-http-api-v1/` 下的 task artifacts；HTTP 生产代码与测试由 Coder 在独立 commit 引入；`docs/design/*` 由 Analyst 在 Contract `APPROVED` 后更新。T07 装配层代码不属于本任务，若在本分支出现装配实现先于 T07 落地 → STOP 并交 Owner。发现他人遗留 dirty 文件 → STOP，不 `git add -A` / `git add .`、不 stash、不覆盖。

## Initial Route

交 Analyst（COMPLEX，`contract.status = PENDING`）。Coder 启动前需先满足 T07 前置（见 OPEN QUESTION）。
