# 测试与验收

本页说明可重复验证的行为与现有真实调用证据。功能入口以 [README](../README.md) 的能力表为准；模块测试通过不代表该能力已接入默认 HTTP 服务。最新初始化、两种镜像来源与 curl 验证见[开发者体验交付报告](developer-onboarding-delivery.md)。

## 自动化验证

在仓库根目录执行：

```bash
make ci
```

依次运行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`go test -race ./...`。包含 13 个 Runtime 测试包及 `scripts` 初始化/启动流程测试包，不调用真实模型，不需要 API Key；HTTP 集成测试使用本机临时端口。

| 场景 | 关键测试入口 | 检查内容 |
| --- | --- | --- |
| 初始化与启动流程 | [scripts/onboarding_test.go](../scripts/onboarding_test.go) | `.env` 保留、非交互模式、镜像来源不串用、失败非零退出；Healthy 但宿主机不可达时不报告成功 |
| 普通回答与多步工具调用 | [agent/agent_test.go](../agent/agent_test.go)：`TestRunLoopsThroughToolsAcrossSteps`、`TestRunHandlesMultipleCallsInSingleResponse` | 循环、回填、调用计数与终态 |
| 工具注册与参数校验 | [tool/registry_test.go](../tool/registry_test.go)、[tool/validate_test.go](../tool/validate_test.go) | 重复注册、Schema 子集、必填字段与类型 |
| 内置工具 | [builtin](../builtin/) 下各工具测试 | 四则运算、固定数据集搜索、Todo 端口的会话作用域 |
| 同会话记忆与跨会话隔离 | [e2e/chat_test.go](../e2e/chat_test.go)：`TestSessionMemoryAndCrossSessionIsolation` | 下一轮模型请求包含历史和工具结果，另一会话快照不变 |
| 失败整轮不提交 | [service/runner_test.go](../service/runner_test.go)：`TestRunProviderFailureLeavesNoPartialHistory` | 失败后没有半轮用户消息或工具历史 |
| 上下文压缩 | [e2e/context_test.go](../e2e/context_test.go)、[contextmgr/digest_test.go](../contextmgr/digest_test.go)、[agent/context_test.go](../agent/context_test.go)、[contextmgr/manager_test.go](../contextmgr/manager_test.go) | 正式 HTTP 服务配置预算后真实触发压缩：历史缩减、摘要召回、工具配对、失败回滚、预算不足、并发隔离；模块层验证摘要确定性/有界性与预算边界 |
| Todo 与压缩联合验收 | [e2e/tools_context_test.go](../e2e/tools_context_test.go)：`TestJointTodoWriteAndCompactionCommitInSameRun`、`TestJointTodoAndCompactionRollBackOnProviderFailure`、`TestJointTodoAndSummaryIsolatedAcrossSessions` | 同一 Run 内 Todo 写入与压缩一起提交；压缩与工具写入都发生后 Provider 失败时 Messages / Summary / Todos 全部回滚；跨会话的 Todo 与 Summary 互不串话 |
| 超时、取消与额度耗尽 | [e2e/failure_test.go](../e2e/failure_test.go)、[e2e/timeout_test.go](../e2e/timeout_test.go) | HTTP、Run 终态和会话提交结果 |
| 错误分类一致性 | [httpapi/handler_test.go](../httpapi/handler_test.go)：`TestProviderFailureClassificationEndToEnd`、`TestRequestTimeoutClassificationEndToEnd` | Provider 故障与请求级超时不混淆 |
| 并发 | [session/concurrency_test.go](../session/concurrency_test.go)、[e2e/concurrency_test.go](../e2e/concurrency_test.go) | 同会话串行、跨会话并发及竞态检查 |
| Trace 与日志旁路 | [agent/events_test.go](../agent/events_test.go)、[runlog](../runlog/) 测试 | 事件关联、脱敏、日志错误不改变运行结果 |

局部复核示例：

```bash
go test -count=1 ./e2e -run TestSessionMemoryAndCrossSessionIsolation
go test -count=1 ./agent -run TestRunCompactsOverBudgetHistory
go test -count=1 ./httpapi -run 'TestProviderFailureClassificationEndToEnd|TestRequestTimeoutClassificationEndToEnd'
```

2026-10-21 本地复核：macOS arm64、Go 1.27.1，`make ci` 四个步骤均通过；受检业务源码与 `f417327` 一致。该结果不等同于 CNB Runner 的平台执行记录，亦不覆盖 Workflow Engine 的独立模块。

2026-10-22 集成快照复核：Linux amd64、Go 1.24.1，在 `integrate/tools-context-v1`（工具装配 + 上下文装配合并提交）上 `make ci` 四个步骤均通过。同日对 `e2e/tools_context_test.go` 做过反证：把 `service.Runner` 透传的 `TokenBudget` 强制为 0 时三个联合用例全部失败；让失败轮不再丢弃 Turn 时回滚用例失败并显示 Todo 泄漏。反证结论只说明这些用例有判别力，不构成平台执行记录。

### 集成测试与默认启动入口的差异

`e2e/harness_test.go` 使用真实 HTTP 层、Agent Loop、OpenAI 协议客户端和 Session Manager，模型端是本机 scripted server。测试与默认 `cmd/mini-agent` 使用同一个工具注册表构造器 `service.NewToolset`，因此两处的工具清单一致：`calculator`、`mock_search`、`todo_create`、`todo_list`、`todo_complete`。

Todo 的端到端链路（创建、查询、完成、跨会话隔离、失败/预算/超时回滚）见 [e2e/todo_test.go](../e2e/todo_test.go)；Todo 状态由 `service/todo.go` 适配到当前 Session Turn，随整轮提交或回滚。上下文压缩通过 `harnessOptions.tokenBudget` 启用正式装配路径（默认 `DigestSummarizer`，零模型调用），见 [e2e/context_test.go](../e2e/context_test.go)；`cmd/mini-agent` 的进程级用例只通过环境变量启用预算，证明不注入任何实现也能触发压缩。

### README 运行路径复核

2026-10-21 在 macOS arm64 / Docker Desktop 中，用临时仓库副本、独立 Compose 项目和本地模拟模型复核：`make init`、`make check`、`make up` 均通过，应用按原 Dockerfile 构建，`/healthz` 就绪。

直接执行 README 的创建会话、首次计算和追问命令，仅替换本机端口以避开已有服务。真实计算器先返回 `1776`，再返回 `1800`；两轮均为 `COMPLETED`，各调用模型 2 次、工具 1 次。模拟模型还检查了第二轮请求包含前一轮的工具结果。验证后已移除临时容器。

这次检查验证默认应用入口与文档命令，不调用真实模型，也不验证 CNB 开发镜像的本地交互启动方式。真实模型证据见下节。

## 对话页面验证

前端阶段的构建运行 `npm run check`，依次执行 `tsx --test test/*.test.ts`（存档与 API 契约单元测试）、`tsc -b` 类型检查与 `vite build` 生产构建，在 Node.js 24 容器内通过；Go 的 `make ci` 也通过。单镜像布局下浏览器只访问同源 `/api/*`，转发、凭证注入、跨站拒绝与路由隔离由 Go 侧用例覆盖（`httpapi/webui_test.go`、`cmd/mini-agent/main_test.go`）。

旧的 Node 前端服务（`web/server.mjs`）、`web/Dockerfile` 与 `web/test/server.test.mjs` 已随单镜像合并删除；对应的代理断言全部由 Go 用例和浏览器 E2E 接管，不是丢弃测试。具体测试入口见 [web/README.md](../web/README.md)。

以下浏览器交互验证发生在双容器布局下（独立的 Node 前端容器），页面行为本身未变；当前布局改为单镜像单容器，页面与 `/api` 由同一个 Go 进程提供，见下节。当时的记录：

- 工具计算返回 `1776`；刷新恢复记录后继续追问得到 `1800`，模拟模型检查了前一轮工具结果已进入下一轮上下文。
- 新建和切换对话、Markdown 与代码块、深浅主题正常；390px 移动端侧栏可用，页面没有横向溢出。
- 模型服务报错时显示错误，随后仍可发送新消息；主动停止后保留历史，并可直接在同一会话继续追问。
- 重启后端，再向旧 Session 发送消息时，页面保留旧记录并显示会话失效提示。

这些结果验证页面与真实运行时的连接，以及异常时的交互；模型输出由本地脚本控制，没有重新验证真实模型质量，也没有执行 CNB 远端构建或镜像推送。

## 单镜像与 CNB 流水线（本地验证）

2026-10-22，Linux amd64、Go 1.24.1、Node.js 24（容器内）、Docker 29.6.2，在被验证分支上本地执行，未触发 CNB 发布：

| 项目 | 结果 |
| --- | --- |
| `docker build .` | 通过；三阶段（Node 24 前端产物 → Go 24 静态二进制 → Alpine），前端 `npm run check` 在镜像构建内通过 |
| 镜像内容 | 7,039,693 字节 / 5 层；`/app/mini-agent` + `/app/web/{index.html,assets,favicon.svg}`；无 Node 运行时、无 `.env`、无 `.git` |
| 单容器启动 | `docker run` 与 `docker compose up -d` 均只产生 1 个应用容器、1 个端口 |
| `GET /` | 200 `text/html; charset=utf-8`，`Cache-Control: no-cache`，含 CSP / `nosniff` / `Referrer-Policy` |
| `GET /assets/index-*.js`、`*.css` | 200 `text/javascript` / `text/css`，`Cache-Control: public, max-age=31536000, immutable` |
| `GET /healthz`、`/api/healthz` | 200 `{"status":"ok"}` |
| `POST /api/v1/sessions`、`POST /api/v1/chat` | 201 / 200 `COMPLETED`，模拟 Provider 收到 `Authorization`（凭证由进程注入，浏览器未持有） |
| `POST /v1/sessions` | 无凭证 401，带凭证 201（同源入口没有放宽 `/v1` 认证） |
| 路由隔离 | `/v1/unknown`、`/api/unknown`、`/v1`、`/api` → 404 JSON；`GET /v1/sessions` → 405 + `Allow: POST` |
| 路径安全 | `/.env`、`/../go.mod`、`/%2e%2e%2f.env`、`/assets/../../go.mod`、缺失静态资源 → 404 |
| 跨站写入 | `Origin: https://evil.example` 与 `Sec-Fetch-Site: cross-site` → 403 `forbidden_origin`，未触达 Runner |
| 配置 fail-closed | 缺 `MINI_AGENT_MODEL`、非回环缺 `MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_WEB_DIR` 不存在 → 进程退出码 1 并输出明确原因 |
| 停止 | `docker stop` 后退出码 0，约 0.4s；容器日志为 JSON 事件，不含 HTTP 凭证或模型 Key |
| 回归 | `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`go test -race ./...`、`make ci`、`make web-stage` 均通过 |

证据边界：

- 聊天链路使用本机模拟 Provider（固定返回文本），不调用付费模型，也不验证模型质量。
- `.cnb.yml` 的发布只在 `main` push 触发；本次未推送 `main`，因此**没有**远端镜像构建与推送结果，不声称已发布。
- 镜像标签与 digest 规则由流水线脚本定义，实际 Registry 制品以 CNB 构建记录为准。

### 前端稳定性缺陷与修复（2026-10-22）

缺陷均在「可脚本化 Provider → 真实 Go 进程 → Chromium」链路上复现后修复，现已迁移到单镜像进程（见下节 E2E 复核）：

1. 点击“停止回复”后整个会话被标记为不可继续，只能新建对话；实际服务端取消时整轮丢弃，现在可以直接在同一会话继续追问。
2. 会话创建阶段失败（聊天请求尚未发出）也把会话标记为“状态无法确认”，即使什么都没发送；现在保留输入框并允许重试。
3. 移动端侧栏打开后第一次 `Esc` 只关掉了自动弹出的 Tooltip、关不掉侧栏；现在打开时聚焦对话框本身，`Esc` 一次即可关闭。
4. 通过局域网 IP 以 `http` 打开（非安全上下文）时 `crypto.randomUUID` 不存在，点击发送直接抛错且没有任何提示；现在回退到 `crypto.getRandomValues`。
5. 等待回复期间输入框被禁用而丢失焦点，回复结束后需要重新点击；现在焦点自动回到输入框。
6. 健康检查没有超时，服务端接受连接却不响应时连接指示器永久停在“连接中”；现在 5 秒超时并显示未连接。

未验证部分：Safari / Firefox 行为、超过 60 秒的服务端超时路径（由 Go 侧 `deadline_exceeded` 用例覆盖）。前端不会构造 400 / 413 请求，因此页面层无法观测这两个状态码。

## 单镜像集成验收（2026-10-22，`integrate/final-delivery-verification-v1`）

合并基线：`develop @ 8b9f1dd`，先合入 `feat/single-image-cnb-release-v1 @ 2a7357c`（单镜像 + Go 内嵌前端 + CNB 单镜像发布），再合入 `fix/frontend-stability-v1 @ 5b3312b`（六类页面缺陷修复 + 浏览器 E2E）。Git 自动合并只有三处文本冲突（`docs/verification.md`、`web/README.md`、`web/package.json`），但架构冲突在代码里：Agent 2 的 E2E 装配依赖已被删除的 `web/server.mjs`。

集成处置：

- 保留单镜像架构，不引入任何 Node 运行时服务；`web/server.mjs`、`web/Dockerfile`、`web/test/server.test.mjs` 保持删除。
- `web/test/e2e/stack.mjs` 重写为单进程装配：可脚本化 Provider → 真实 `cmd/mini-agent`（`MINI_AGENT_WEB_DIR=web/dist`），页面与 `/api`、`/v1`、`/healthz` 同端口同源，不再有代理层。
- 新增容器模式（`MINI_AGENT_E2E_IMAGE=<镜像>`）：同一套 31 个场景直接在最终镜像的容器里执行，宿主机用 `host.docker.internal` 提供可脚本化 Provider。
- `web/package.json`：删除 `start`（Node 服务已不存在），`test` = `tsx --test test/*.test.ts`，新增 `test:e2e` = `node test/e2e/e2e.mjs`，缺 `dist` 时先自动构建，保证入口能真正跑到全部现存测试。

### 浏览器 E2E（最终单镜像容器内）

环境：Linux amd64、Go 1.24.1、Node.js 22.23.3（仓库 CI 为 24）、Docker 29.6.2（rootless）、Playwright 1.64.1 / Chromium headless。

```bash
npm --prefix web ci
npm --prefix web run check                                            # 19 个单元测试 + tsc + vite build
MINI_AGENT_E2E_IMAGE=mini-agent-go:final-test npm --prefix web run test:e2e   # 容器内 31 个场景
npm --prefix web run test:e2e                                         # 本地进程模式同一套场景
```

结果：容器模式 **31/31 通过**，本地进程模式同样 31/31；每个场景都断言无未捕获异常、无未预期 console error、无静态资源加载失败（`Failed to load resource` 仅由场景自身断言的 4xx/5xx 产生）。

覆盖：首屏与连接指示、创建会话、普通聊天与多轮追问、多会话切换、跨会话并发（返回顺序颠倒）、五种内置工具（calculator / mock_search / todo_create / todo_list / todo_complete）、Provider 502、429 调用上限、504 请求超时、会话过期（后端重启）、网络中断与恢复、会话创建失败重试、聊天响应不可解析、聊天响应缺字段、404、401、500、502、输入边界与 Shift+Enter、Markdown 与 XSS、空回复、快速重复提交、刷新恢复、请求途中刷新、删除进行中的会话、非安全上下文（局域网 http）、390px 移动端侧栏 Esc、焦点恢复、长对话滚动、健康检查挂起，以及“前端产物与浏览器 localStorage 不含服务凭证 / Provider 密钥”。

相对 Agent 2 的 29 个场景，调整如下（均为迁移，不是删除）：

1. 「网络中断」由 `stopProxy/restartProxy` 改为 `stopServer/restartServer`：单镜像没有可单独停掉的代理进程，整机不可达在浏览器侧同样是传输失败。
2. 「会话创建失败」桩改为命中 `/api/v1/sessions` 并返回缺字段 JSON（原名写的是“非法 JSON”，与实际桩不符，改名与断言对齐）。
3. 「401 凭证错误」由 `proxyToken` 错配改为固定响应桩：单镜像里 `/api/*` 凭证由进程注入，浏览器无法制造 401；真实 401 由 Go 用例与容器 curl 覆盖。
4. 「代理层 502」改名为「上游 502 backend_unavailable（桩）」：单镜像不会返回该码，保留为网关层防御性用例。
5. 新增 3 个场景：「聊天响应缺字段」「404 not_found」「上游 502 桩」拆分为独立用例，补齐 401/404/429/500/502/504 与“JSON 格式错误 / 字段缺失”两条验收线。

静态桩说明：401、404、坏 JSON、缺字段、500、502 与健康检查挂起由 `startStubStack` 提供固定响应，静态资源仍来自真实构建产物。正式单镜像不会产生这些响应，用例验证的是页面自身的错误呈现与恢复；服务端真实行为由容器 curl 与 Go 用例覆盖。

### 容器与安全（最终镜像 `mini-agent-go:final-test`）

| 项目 | 结果 |
| --- | --- |
| 镜像 | 7,040,069 字节 / 5 层；`/app/mini-agent` + `/app/web/{index.html,assets,favicon.svg}`；无 `node`/`npm`、无 `.env`、无 `.git` |
| 运行形态 | 1 个应用容器、1 个发布端口（`127.0.0.1:8080`）、非 root 用户 `miniagent` |
| 同源 | `GET /` 200、`/assets/index-*.js` 200（`immutable`）、`/healthz` 与 `/api/healthz` 200、`POST /api/v1/sessions` 201 |
| `/v1` 认证 | 无凭证 `POST /v1/sessions` → 401；带凭证 → 201（同源入口没有放宽 `/v1`） |
| `/api` 允许集合 | `/api/unknown` → 404、`GET /api/v1/chat` → 405、`POST /api/healthz` → 405；只转发三个登记端点 |
| 跨站写入 | `Sec-Fetch-Site: cross-site` → 403 `forbidden_origin`；`Origin: https://evil.example` → 403；同源 201 |
| 路径安全 | `/.env` → 404；未知无扩展名路径 → `index.html`（SPA fallback）；未知带扩展名路径 → 404 |
| 密钥边界 | 镜像内全文检索 Owner 真实 `MINI_AGENT_LLM_API_KEY` 与 `MINI_AGENT_AUTH_TOKEN`：均不存在；浏览器请求不含 `Authorization`（E2E「安全」场景断言） |
| 配置 fail-closed | 缺 `MINI_AGENT_MODEL`、非回环缺 `MINI_AGENT_AUTH_TOKEN`、`MINI_AGENT_WEB_DIR` 不存在 → 退出码 1 且输出具体原因 |
| 停止 / 重启 | `docker stop` 退出码 0、约 349ms（无 OOM）；重启后 `/healthz` 200、页面 200；服务端会话按设计丢失（旧 session → `session_not_found`） |
| Compose | 端口只发布到宿主机回环（`127.0.0.1:${MINI_AGENT_HOST_PORT:-8080}`）；`/api/*` 无访客登录机制，不能当作可直接公网暴露的安全 API |

### 真实模型验收（最终单镜像 + 正式 HTTP 链路）

Owner 已配置的 Provider 凭证通过 `--env-file .env` 注入容器，凭证未出现在任何输出、日志或前端产物中。Provider：TokenHub / `deepseek/deepseek-flash`。

| 场景 | HTTP | 上游 LLM 调用 | 工具调用 | 结果 |
| --- | --- | --- | --- | --- |
| 普通中文对话 | 200 `COMPLETED` | 1 | 0 | 正常中文自我介绍 |
| 同一 Session 连续追问 | 200 `COMPLETED` | 1 | 0 | 正确复述上一轮回答 |
| Calculator 自主调用 | 200 `COMPLETED` | 2 | 1（calculator） | 返回 `1776` |
| Mock Search 自主调用 | 200 `COMPLETED` | 2 | 1（mock_search） | 说明内置数据集 0 条命中，未编造结果 |
| Todo 创建 | 200 `COMPLETED` | 2 | 2（todo_create） | `todo-1` / `todo-2` 均为 pending |
| Todo 查询 | 200 `COMPLETED` | 2 | 1（todo_list） | 列出两条待办 |
| Todo 完成 | 200 `COMPLETED` | 3 | 2（todo_complete + todo_list） | `复核镜像` 变为 completed |
| 会话隔离（A 记住代号） | 200 `COMPLETED` | 1 | 0 | A 回答“记住了” |
| 会话隔离（B 被问代号） | 200 `COMPLETED` | 1 | 0 | B 回答“不知道”，未串话 |
| 会话隔离（A 回忆） | 200 `COMPLETED` | 1 | 0 | A 正确回答“蓝鲸七号” |

合计 16 次上游 LLM 调用、7 次工具执行；Token 用量 input 17,857 / output 654 / total 18,511（来自 `llm_response` 事件，非估算）。全部为真实 Provider 成功调用，脚本化 Provider 的成功没有被用来冒充真实结果。

真实链路暴露并修复的缺陷：Owner 的 `.env` 把 `MINI_AGENT_LLM_BASE_URL` 写成了完整端点（`.../v1/chat/completions`），而客户端固定追加 `/chat/completions`，导致全部请求命中 404 → HTTP 502 `provider_failure`（19ms 失败，未消耗 Token）。最小修复：`openai.NewClient` 在 BaseURL 已以 `/chat/completions` 结尾时不再重复拼接，并补 2 个回归测试（端点拼接 + 真实请求路径）。

### 上下文压缩与失败回滚（容器 + 可脚本化 Provider）

| 项目 | 结果 |
| --- | --- |
| 上下文压缩（`MINI_AGENT_CONTEXT_TOKEN_BUDGET=450`、`KEEP_RECENT=2`、6 轮） | 6 轮全部 `COMPLETED`；请求消息条数 `2,4,6,8,4,4`（压缩后回落）；`compacted=true` 事件 2 次；末轮请求携带 `[早期对话摘要]` |
| 失败回滚（工具写入成功后 Provider 500） | 失败轮返回 502 `provider_failure`；下一轮请求只剩 2 条消息，不含失败轮用户消息、工具结果或已写入的 Todo 标题 |
| 预算不可满足（如 300 token + 长消息） | 显式失败，不降级：日志 `error_kind=invalid_argument`（“TokenBudget 无法满足”），HTTP 侧按既有设计映射为 500 `internal` |

已知边界：预算不可满足时 HTTP 状态是 500 `internal` 而不是 4xx，这是 `httpapi.errorCodeOf` 的既有设计（`core.KindInvalidArgument` 一律 internal，避免与会话错误的参数归因混淆），本次未改动；只在实际把预算配到低于摘要本身时才出现，`.env.example` 建议的生产预算是 2048 / 4096。

## 既有真实模型验收

以下为 **2026-10-21 已存档验收记录的摘要**，本次文档整理没有重新调用真实模型。

| 项目 | 记录 |
| --- | --- |
| 业务源码基线 | `f417327443afd381d87de1168980cb9c0b22911f` |
| 环境 | Debian 12 / Go 1.24.1 / Docker Compose 应用容器 |
| Provider / Model | 腾讯云 TokenHub / `deepseek/deepseek-flash` |
| 普通对话 | HTTP 200、`COMPLETED`，1 次模型请求，无工具调用 |
| 计算器 | 输入“使用计算工具计算 37 × 48”，返回 1776，2 次模型请求、1 次工具调用 |
| 真实错误请求 | 临时无效模型 ID，返回 HTTP 502 / `provider_failure`，记录称随后恢复配置 |
| 认证 | 缺失/错误 HTTP Token 返回 401，被拒请求不调用模型 |

计算器场景的事件链为：

```text
run_started
  llm_request
  llm_response (finish_reason=tool_calls)
  tool_started (calculator)
  tool_finished (相同 call_id)
  llm_request (包含工具结果)
  llm_response (finish_reason=stop)
run_finished (COMPLETED)
```

证据入口：

- [首次验证及响应/日志摘录](../.agent/tasks/agent-real-llm-verification-v1/evidence.md)。
- [独立交付复核记录](../.agent/tasks/agent-real-llm-verification-v1/delivery.md)。
- [证据局限与审查记录](../.agent/tasks/agent-real-llm-verification-v1/findings.md)。

两轮记录合计 7 次上游请求；按各轮明细合计 input 2748、output 179，共 2927 tokens。无效模型请求没有返回 usage，因此实际账单不能仅由这份汇总推定。交付报告汇总行把首次 input 写成了 1370，首次明细为 1378；这里按逐项明细汇总，历史报告保留原文。

## 证据边界

- 真实调用只留存文档中的响应与日志摘录，没有完整原始 JSONL，无法离线重放当时模型输出。
- 上述 2026-10-21 记录没有覆盖搜索、Todo、压缩或语义追问；后续 2026-10-22 单镜像集成记录补充了搜索、Todo 与多轮真实调用。压缩使用模拟 Provider 验证，压力测试与长期稳定性仍不在这些证据范围内。
- 单次调用耗时和 token 数只说明当次运行，不作为性能或质量指标。
- 历史记录中关于“race 未进入 CI”的文字已过时：当前 `.cnb.yml` 调用 `make ci`，其中包含 race；是否在平台执行成功需查看实际构建记录。
- 当前主文档按代码、测试与后续证据更新；`.agent/tasks/` 保留任务发生时的记录，不把历史阶段状态改写成新一次验收。
