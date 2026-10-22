# 测试与验收

本页说明可重复验证的行为与现有真实调用证据。功能入口以 [README](../README.md) 的“当前能力”为准；模块测试通过不代表该能力已接入默认 HTTP 服务。

## 自动化验证

在仓库根目录执行：

```bash
make ci
```

依次运行 `go build ./...`、`go vet ./...`、`go test -count=1 ./...`、`go test -race ./...`。根业务模块共 13 个测试包，不调用真实模型，不需要 API Key；HTTP 集成测试使用本机临时端口。

| 场景 | 关键测试入口 | 检查内容 |
| --- | --- | --- |
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

前端镜像的构建阶段运行 `npm run check`：19 个存档/契约单元测试、4 个 HTTP 代理测试、TypeScript 检查与生产构建。在 Node.js 24 容器内通过；Go 的 `make ci` 也通过。具体测试入口见 [web/README.md](../web/README.md)。

本地使用独立 Compose 项目运行真实 Go API、前端容器和模拟模型，复核 `make up` 后通过浏览器验证：

- 工具计算返回 `1776`；刷新恢复记录后继续追问得到 `1800`，模拟模型检查了前一轮工具结果已进入下一轮上下文。
- 新建和切换对话、Markdown 与代码块、深浅主题正常；390px 移动端侧栏可用，页面没有横向溢出。
- 模型服务报错时显示错误，随后仍可发送新消息；主动停止后保留历史，并可直接在同一会话继续追问。
- 重启后端，再向旧 Session 发送消息时，页面保留旧记录并显示会话失效提示。

这些结果验证页面与真实运行时的连接，以及异常时的交互；模型输出由本地脚本控制，没有重新验证真实模型质量，也没有执行 CNB 远端构建或镜像推送。

### 前端稳定性复核（2026-10-22）

在 `fix/frontend-stability-v1` 上用独立进程链路复核页面：可脚本化 OpenAI 兼容 Provider → 真实 `cmd/mini-agent` 进程 → 真实代理 `web/server.mjs` → Chromium 156 headless。环境为 Linux amd64 / Go 1.24.1 / Node.js 22.23.3（仓库 CI 使用 Node.js 24）。

```bash
npm --prefix web ci
npm --prefix web run check          # 19 + 4 单元/集成测试 + tsc + 构建
node web/test/e2e/e2e.mjs           # 29 个浏览器场景
```

结果为 29/29 通过，无未捕获异常、无静态资源 404、无未预期 console error（非 2xx 的 fetch 会由浏览器记为 `Failed to load resource`，页面已把对应错误呈现给用户）。覆盖：首屏与连接指示、创建会话、多轮对话、多会话切换、跨会话并发返回顺序颠倒、五种内置工具、Provider 502、429、504、会话过期、网络中断与恢复、会话创建失败、坏 JSON 响应、401、500、输入边界与 Shift+Enter、Markdown/XSS、空回复、快速重复提交、刷新恢复、请求途中刷新、删除进行中的会话、非安全上下文、移动端布局、长对话滚动、健康检查挂起，以及产物与 localStorage 不含服务凭证或 Provider 密钥。

本轮修复的页面缺陷（均由上述链路复现后修复）：

1. 点击“停止回复”后整个会话被标记为不可继续，只能新建对话；实际服务端取消时整轮丢弃，现在可以直接在同一会话继续追问。
2. 会话创建阶段失败（聊天请求尚未发出）也把会话标记为“状态无法确认”，即使什么都没发送；现在保留输入框并允许重试。
3. 移动端侧栏打开后第一次 `Esc` 只关掉了自动弹出的 Tooltip、关不掉侧栏；现在打开时聚焦对话框本身，`Esc` 一次即可关闭。
4. 通过局域网 IP 以 `http` 打开（非安全上下文）时 `crypto.randomUUID` 不存在，点击发送直接抛错且没有任何提示；现在回退到 `crypto.getRandomValues`。
5. 等待回复期间输入框被禁用而丢失焦点，回复结束后需要重新点击；现在焦点自动回到输入框。
6. 健康检查没有超时，服务端接受连接却不响应时连接指示器永久停在“连接中”；现在 5 秒超时并显示未连接。

未验证部分：真实的 Provider 质量、CNB 远端构建与镜像推送、Safari / Firefox 行为、超过 60 秒的服务端超时路径（由 Go 侧 `deadline_exceeded` 用例覆盖）。前端不会构造 400 / 413 请求，因此页面层无法观测这两个状态码。

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
- 真实验收没有覆盖搜索、Todo、压缩、压力测试或长期稳定性；首次计算后的语义追问也未单独验收。自动化测试验证了历史回填结构。
- 单次调用耗时和 token 数只说明当次运行，不作为性能或质量指标。
- 历史记录中关于“race 未进入 CI”的文字已过时：当前 `.cnb.yml` 调用 `make ci`，其中包含 race；是否在平台执行成功需查看实际构建记录。
- 当前主文档按代码、测试与后续证据更新；`.agent/tasks/` 保留任务发生时的记录，不把历史阶段状态改写成新一次验收。
