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
| 上下文压缩 | [agent/context_test.go](../agent/context_test.go)、[contextmgr/manager_test.go](../contextmgr/manager_test.go) | 使用 Fake Summarizer 验证摘要提交、预算、近期消息下限、Call/Result 配对 |
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

### 集成测试与默认启动入口的差异

`e2e/harness_test.go` 使用真实 HTTP 层、Agent Loop、OpenAI 协议客户端和 Session Manager，模型端是本机 scripted server。测试与默认 `cmd/mini-agent` 使用同一个工具注册表构造器 `service.NewToolset`，因此两处的工具清单一致：`calculator`、`mock_search`、`todo_create`、`todo_list`、`todo_complete`。

Todo 的端到端链路（创建、查询、完成、跨会话隔离、失败/预算/超时回滚）见 [e2e/todo_test.go](../e2e/todo_test.go)；Todo 状态由 `service/todo.go` 适配到当前 Session Turn，随整轮提交或回滚。压缩使用注入的 Fake Summarizer 单独验证，默认 HTTP 服务仍不启用该路径。

### README 运行路径复核

2026-10-21 在 macOS arm64 / Docker Desktop 中，用临时仓库副本、独立 Compose 项目和本地模拟模型复核：`make init`、`make check`、`make up` 均通过，应用按原 Dockerfile 构建，`/healthz` 就绪。

直接执行 README 的创建会话、首次计算和追问命令，仅替换本机端口以避开已有服务。真实计算器先返回 `1776`，再返回 `1800`；两轮均为 `COMPLETED`，各调用模型 2 次、工具 1 次。模拟模型还检查了第二轮请求包含前一轮的工具结果。验证后已移除临时容器。

这次检查验证默认应用入口与文档命令，不调用真实模型，也不验证 CNB 开发镜像的本地交互启动方式。真实模型证据见下节。

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
