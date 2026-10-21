# 验收证据：agent-real-llm-verification-v1

本文件由 Coder 维护，记录可核查的原始证据。未经实跑的项一律标记 `NOT_EXECUTED` / `NOT_VERIFIED`。

## 0. 逐 AC 事实汇总

| AC | 结果 | 关键事实 |
| --- | --- | --- |
| AC-001 | 通过（含一处已披露的配置修正） | `make check` exit 0；`.env` 未被跟踪；实际端点 = `https://tokenhub.tencentmaas.com/v1/chat/completions`；镜像内无 `.env`；`.env.example` 与 `main.go` 变量名差 `MINI_AGENT_LISTEN_ADDR`（compose 注入，见 §5） |
| AC-002 | 通过 | `/healthz` 200 `{"status":"ok"}`、容器 healthy、带 Token 201、无/错 Token 401、被拒请求零上游调用 |
| AC-003 | 通过 | 200 / `COMPLETED` / 中文 reply / `tool_calls=0` / `llm_calls=1`；runlog 四事件成套且 Run ID 一致 |
| AC-004 | 通过 | 200 / `COMPLETED` / reply 含 `1776` / `tool_calls=1` / `llm_calls=2`；事件顺序与 AC 要求逐条吻合 |
| AC-005 | 通过 | 四段证据齐备（§9），工具调用来自 Provider 响应 |
| AC-006 | 通过 | 离线错误语义全 PASS；真实负向请求 1 次 → HTTP 502 `provider_failure`，runlog 同种类，已恢复并核验 |
| AC-007 | 通过 | 上游请求 4 次、input 1378 / output 92 / total 1470 tokens，未超授权；无上限授权下按「仅验收必需」执行 |
| AC-008 | 通过 | `make ci`、`-race -count=1`、`e2e`、Engine 全 exit 0；无生产代码改动；无真实密钥 |
| AC-009 | 待 Deliverer | 本文件提供原始记录，`delivery.md` 由 Deliverer 汇总 |

生产代码零改动：本轮 `git status` 只包含本任务目录产物。

## 1. 环境与基线

| 项 | 值 |
| --- | --- |
| 分支 | `feat/agent-real-llm-verification-v1`（跟踪 `origin/feat/agent-real-llm-verification-v1`） |
| 起点 commit | `925183e`（`f417327` + 任务定义与离线证据） |
| develop 基线 | `f417327` |
| Go / Docker / Compose | `go1.24.1` / `29.6.2-7` / `v5.3.1` |
| Provider | 腾讯云 TokenHub |
| Model | `deepseek/deepseek-flash` |
| BaseURL | `https://tokenhub.tencentmaas.com/v1` |
| 推导端点 | `https://tokenhub.tencentmaas.com/v1/chat/completions` |
| 预算装载 | `MINI_AGENT_MAX_LLM_CALLS=8`、`MINI_AGENT_MAX_TOOL_CALLS=8`（单 Run 预算，非总预算） |

## 2. Owner 授权记录（真实调用前门禁）

Owner 于 2026-10-21 明确授权，内容如下（原话要点）：

- Provider：腾讯云 TokenHub；Model ID 以 `.env` 实际配置为准（`deepseek/deepseek-flash`）。
- 不设固定金额上限、不设固定 Token 上限、不预设固定上游请求次数，**仅允许执行 Task 11 验收所必需的请求**。
- 授权最多 1 次临时无效 Model ID 的负向测试。
- 明确禁止：无限重试、无意义重复测试、扩大验收范围；发现异常连续调用/请求循环/费用异常须立即停止并报告。
- 完成全部 AC 后立即停止真实调用并记录用量。

实际执行：**4 次上游 LLM 请求**，无任何重试，无越界调用。

## 3. 配置修正声明（必须披露）

Owner 填写的 `.env` 中 `MINI_AGENT_LLM_BASE_URL` 为**完整请求地址** `https://tokenhub.tencentmaas.com/v1/chat/completions`。而 `llm/openai/client.go:89-92` 固定追加 `/chat/completions`，直接使用会得到 `.../v1/chat/completions/chat/completions`，必然失败并浪费 1 次预算。

Coder 在发出任何真实请求前，只修改了该**一行**为 Provider 根地址 `https://tokenhub.tencentmaas.com/v1`：

```text
修改前：MINI_AGENT_LLM_BASE_URL=https://tokenhub.tencentmaas.com/v1/chat/completions
修改后：MINI_AGENT_LLM_BASE_URL=https://tokenhub.tencentmaas.com/v1
文件行数 11 → 11；MINI_AGENT_* 键数量 11 → 11（其余键与取值未触碰）
```

依据：Task「Relevant Context」明确要求该变量「不能填完整请求地址」。未改动任何凭据，未打印任何取值。

## 4. AC-001 交付基线、配置一致性与密钥安全

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| `make check` | `make check` | exit 0，`✓ 配置与环境检查通过（未发起任何模型请求）` |
| `.env` 未被跟踪 | `git ls-files \| grep -x '.env'` | 无输出 |
| `git status` 不含 `.env` | `git status --short` | 仅任务目录产物，无 `.env` |
| 容器实际生效配置 | `docker compose exec -T mini-agent env` 指定键 | `BASE_URL=https://tokenhub.tencentmaas.com/v1`、`MODEL=deepseek/deepseek-flash` |
| 实际端点 | 根地址 + `chatPath` | `https://tokenhub.tencentmaas.com/v1/chat/completions`；由 AC-003/AC-004 的 200 真实响应反向证实（错误路径会 404） |
| 镜像内无 `.env` | `docker run --rm --entrypoint /bin/sh mini-agent-go:local -c 'test ! -e /app/.env'` | exit 0；`ls -a /app` 只有 `mini-agent` |
| 构建上下文排除凭据 | `.dockerignore` | 显式排除 `.env` / `.env.*` / `.env.example` |
| `make init` 不覆盖 | 哈希前后比对 | 保留已有 `.env` |

端点拼接规则（`llm/openai/client.go:89-92`）：`parsed.Path = TrimRight(path,"/") + "/chat/completions"`，清空 query/fragment。既有测试 `llm/openai/client_test.go:826`（`https://api.example.com/v1` → `.../v1/chat/completions`）与 `e2e/offline_test.go:59` 提供机械证明。

## 5. 变量名一致性（偏差已披露，未擅自修改）

```text
.env.example 键 11 个；cmd/mini-agent/main.go envXxx 常量 12 个；差集 = MINI_AGENT_LISTEN_ADDR
```

该变量由 `compose.yaml:15` 的 `environment` 注入 `0.0.0.0:8080`（Compose `environment` 优先级高于 `env_file`），并在 `README.md:373`、`docs/design/http-api.md:184` 有文档。属「容器编排固定项不进 `.env`」的设计选择，与 AC-001 字面要求存在差异。Coder 未修改 `.env.example`（属 Scope 变更），交 Owner 裁定。

## 6. AC-002 容器化启动、健康检查与认证拒绝

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 启动 | `make up` | exit 0，`服务已启动` |
| 健康端点 | `curl -sS -w '%{http_code}' /healthz` | `http=200`，`{"status":"ok"}` |
| 容器健康 | `docker compose ps` | `state=running health=healthy`（start-period 后稳定 healthy） |
| 正确 Bearer 创建会话 | `POST /v1/sessions` + Bearer + JSON | `http=201`，`{"session_id":"0d5fb96f5afce45ed6d22c5daf05ae3f"}` |
| 不带 Token | 同端点无 `Authorization` | `http=401`，`{"error":{"code":"unauthorized","message":"未通过认证"}}` |
| 错误 Token | `Bearer wrong-token-value` | `http=401`，响应同上（与缺失凭证不可区分） |
| 被拒请求无上游调用 | 上述全部请求前后统计 | `llm_request` 0 → 0，`run_started` 0 |
| 未授权 `/v1/chat` | 无 Token 直接请求 | `http=401`，`llm_request` 仍为 0 |

代码依据：`httpapi/handler.go:44-48`（认证 → Content-Type → 解析）、`httpapi/auth.go:13-27`（未认证不读 body、不调用 Runner、不写会话）。单测：`TestAuthPrecedesBodyParsing`、`TestAuthMissingAndWrongCredentialAreIdentical`、`TestAuthAcceptsConfiguredCredential`、`TestHealthzExemptFromAuthAndLeakFree`。

## 7. AC-003 真实普通对话

请求：`POST /v1/chat`，`session_id=0d5fb96f5afce45ed6d22c5daf05ae3f`，message=`你好，请用一句话回复我。`

```json
{"session_id":"0d5fb96f5afce45ed6d22c5daf05ae3f","run_id":"c874b0306ea1ce827c332b8463085674","status":"COMPLETED","reply":"你好！很高兴见到你，有什么我可以帮你的吗？","llm_calls":1,"tool_calls":0}
```

HTTP 200，`time=1.27s`。runlog（同 `run_id`）：

```text
run_started   step=0 status=RUNNING     data.model=deepseek/deepseek-flash
llm_request   step=1 status=RUNNING     data.model=deepseek/deepseek-flash message_count=1 compacted=false
llm_response  step=1 status=RUNNING     data.finish_reason=stop tool_call_count=0 input_tokens=405 output_tokens=13 duration_ms=1272
run_finished  step=1 status=COMPLETED   duration_ms=1272
```

核对：事件成套且顺序符合；Run ID 与 HTTP `run_id` 一致；`llm_calls=1` 与同 Run `llm_request` 次数 1 一致（无重复调用）；reply 为非空中文且与问候语义相符；`tool_calls=0`。

## 8. AC-004 真实 Calculator Tool Calling

请求：同一会话，message=`请使用你可用的计算工具计算 37 × 48，并给出最终结果。`

```json
{"session_id":"0d5fb96f5afce45ed6d22c5daf05ae3f","run_id":"b48507812184cf0d6274bf735e96c405","status":"COMPLETED","reply":"37 × 48 = **1776**","llm_calls":2,"tool_calls":1}
```

HTTP 200，`time=1.84s`。runlog（同 `run_id`，按时间顺序）：

```text
run_started   step=0  model=deepseek/deepseek-flash
llm_request   step=1  message_count=3
llm_response  step=1  finish_reason=tool_calls tool_call_count=1 input_tokens=441 output_tokens=69 duration_ms=896
tool_started  step=1  tool_name=calculator index=0 call_id=call_00_ET_rCn8pCROTr6FlYjcT83K6706
tool_finished step=1  tool_name=calculator index=0 is_error=false call_id=call_00_ET_rCn8pCROTr6FlYjcT83K6706 duration_ms=0
llm_request   step=2  message_count=5
llm_response  step=2  finish_reason=stop tool_call_count=0 input_tokens=532 output_tokens=10 duration_ms=898
run_finished  step=2  status=COMPLETED duration_ms=1796
```

核对：`reply` 含 `1776`；`tool_calls=1`；`llm_calls=2` 与 2 次 `llm_request` 一致；`message_count` 由 3 增至 5（追加 assistant 工具调用消息与 tool 结果消息），与工具回合的实际发生一致。

## 9. AC-005 工具调用来源四段证据

**（1）`finish_reason=tool_calls` 与 `tool_call_count=1` 来自 Provider 响应解析**

```text
step=1 llm_response data={"finish_reason":"tool_calls","input_tokens":441,"output_tokens":69,"tool_call_count":1}
```

`tool_call_count` 由 `agent/events.go:59` 直接取 `len(resp.Message.ToolCalls)`，而 `resp` 来自 `llm/openai` 对 Provider 响应 `choices[0].message.tool_calls` 的解码（`wire.go:303-323`）；`finish_reason` 由 `mapFinishReason` 白名单映射（`wire.go:346-353`），非白名单取值会整体失败为 `provider_failure`，不会产生本事件。

**（2）同一 `CallID` 的工具事件**

```text
tool_started  call_id=call_00_ET_rCn8pCROTr6FlYjcT83K6706 tool_name=calculator index=0
tool_finished call_id=call_00_ET_rCn8pCROTr6FlYjcT83K6706 tool_name=calculator index=0 is_error=false
```

`call_id` 形如 `call_00_...`，是 Provider 生成的工具调用 ID，应用侧仅透传（`agent/run.go:141` 起从 `resp.Message.ToolCalls` 逐个执行），不存在本地生成该格式的代码路径。

**（3）计算器参数协议与 `1776` 的唯一匹配**

- `builtin/calculator.go:14`：参数协议只含 `op`（enum `add|sub|mul|div`）、`a`、`b`，三项均 required；无其他入参，也不接受"表达式"字符串。
- `builtin/calculator.go:80-93`：只有 `op=mul` 且 `a=37`、`b=48` 才能得到 `1776`。参数错误不会产生该结果：`op=add` → 85，`op=sub` → -11，`op=div` → 0.7708…，任一缺参或类型错误直接返回 `IsError=true` 的领域失败，不会进入成功结果。
- `git grep -n '1776'` 在**所有 Go 源码与测试**中零命中；仅出现在 `task.md` / 本文件中（即验收要求文本自身）。因此 `1776` 不是硬编码答案，也不是测试预构造结果。

**（4）最终 reply 含 `1776`，整轮 `finish_reason` 依次为 `tool_calls` → `stop`**

```text
reply="37 × 48 = **1776**"
step=1 finish_reason=tool_calls ; step=2 finish_reason=stop
```

**补充：排除关键字路由 / 测试预构造 / Mock（代码依据）**

- 工具执行唯一入口是 `agent/run.go:141` 的 `calls := resp.Message.ToolCalls`，即完全由 Provider 响应驱动；`agent/run.go:147` 对「`finish_reason=tool_calls` 但无 tool_calls」直接判为协议错误。
- 非测试代码中不存在按消息文本选工具的分支：对 `httpapi` / `service` / `agent` 包检索 `关键字`、`keyword`、`strings.Contains(…message)` 等模式零命中；中文「计算」仅出现在注释与错误文案中。
- v1 registry 只注册 `calculator` 并 `Seal()`（`cmd/mini-agent/main.go:173-182`），没有可被关键字命中的第二工具。
- 本次运行的是真实容器与真实 TokenHub 端点（HTTP 200 + Provider 生成 `call_id` + Provider 返回 token usage），不是离线 Mock。

## 10. AC-006 错误传播

**离线错误语义（PASS）**：`go test ./llm/... ./httpapi/... ./agent/... ./e2e/... -count=1 -run 'Provider|Timeout|Cancel|Failure|Limit|Disconnect|ErrorSemantics|ContextFailures'`

| 用例 | 覆盖 |
| --- | --- |
| `llm/openai.TestCompleteErrorSemantics` | 4xx/5xx → `provider_failure`、协议错误分类 |
| `llm/openai.TestCompleteContextFailures` | 取消/超时 → `cancelled`/`deadline_exceeded`，保留 `Cause` |
| `httpapi.TestProviderFailureClassificationEndToEnd` | 端到端错误分类不被吞 |
| `httpapi.TestRequestTimeoutReturns504`、`TestRequestTimeoutClassificationEndToEnd` | 请求超时映射 |
| `httpapi.TestClientDisconnectCancelsRun`、`TestShutdownCancelsInflightRunAfterGrace` | 断连与关闭取消 |
| `httpapi.TestChatFailureHasNoSideEffects` | 失败无副作用提交 |
| `e2e.TestProviderFailures/provider_4xx`、`/provider_5xx` | 全链路 4xx/5xx |
| `e2e.TestProviderUnresponsive`、`TestRequestTimeout`、`TestClientDisconnect` | 阻塞/超时/断连 |
| `e2e.TestBudgetLimits/MaxLLMCalls耗尽`、`MaxToolCalls耗尽` | 预算耗尽拒绝 |

**真实负向请求（1 次，授权上限内）**：临时把 `MINI_AGENT_MODEL` 改为无效 ID（不打印原值），重建容器后请求：

```json
{"error":{"code":"provider_failure","message":"模型服务调用失败"}}
```

`http=502`，`time=0.059s`。runlog：

```text
run_started   model=__no_such_model_for_negative_test__
llm_request   step=1
llm_response  step=1 status=FAILED error_kind=provider_failure op=agent.Loop.loop error_message=模型调用失败 input_tokens=0 output_tokens=0
run_finished  step=1 status=FAILED error_kind=provider_failure
```

`run_finished` 的错误种类与 HTTP 错误码一致（`provider_failure`）。对外响应与日志均不含密钥、上游原始报文或堆栈。

**恢复**：`MINI_AGENT_MODEL` 改回 `deepseek/deepseek-flash`（长度 23，与修改前一致），`make check` exit 0，重建后容器 `health=healthy`，运行中配置核对 `MODEL=deepseek/deepseek-flash`。恢复过程未消耗真实调用。

## 11. AC-007 调用量与 Token 账本

计数口径：**上游 LLM 请求次数**（一次 `/v1/chat` 可能触发多次）。

| # | 用途 | 上游请求 | 结果 | input_tokens | output_tokens |
| --- | --- | --- | --- | --- | --- |
| 1 | AC-003 普通对话 | 1 | `finish_reason=stop`，HTTP 200 | 405 | 13 |
| 2 | AC-004 工具调用第 1 步 | 1 | `finish_reason=tool_calls` | 441 | 69 |
| 3 | AC-004 工具调用第 2 步（终稿） | 1 | `finish_reason=stop` | 532 | 10 |
| 4 | AC-006 负向测试（无效 Model） | 1 | `provider_failure`，HTTP 502 | 0 | 0 |
| 合计 | | **4** | | **1378** | **92** |

Prompt 1378 + Completion 92 = **Total 1470 tokens**。

- 第 4 行 0/0 的原因：Provider 在返回 `usage` 前就拒绝了无效 Model，响应体中不含用量，故按「缺失」记为 0 并注明原因，未用其他值填充。
- 汇总方式：`grep -c '"llm_request"'` 计请求数；`grep -o '"input_tokens":[0-9]*'` / `'"output_tokens"'` 求和。日志取自保存的原始 runlog（容器为加载新 env 重建过，重建前的日志已随容器删除，故以调用当时保存的原始片段为准）。
- 授权为「不设固定上限 + 仅验收必需」，实际 4 次即止，无重试、无重复调用。
- 全部记录不含 API Key 或其片段。

**费用信息**：未获得 TokenHub 计费接口或单价，因此无法给出金额结论，不臆造价格。可核对口径为：4 次请求、1378 input + 92 output tokens，按账户在 TokenHub 控制台的实际计费为准。本次无任何异常连续调用、请求循环或重放。

## 12. AC-008 离线回归与密钥安全

| 检查 | 命令 | 结果 |
| --- | --- | --- |
| 全量 CI | `make ci` | exit 0，13 包全 `ok` |
| Race 非缓存 | `go test -race -count=1 ./...` | exit 0，全部 `ok`（`make ci` 的 race 命中缓存，故补跑） |
| E2E | `go test ./e2e -count=1` | exit 0 |
| Engine 独立 Module | `GOWORK=off go -C .agent/engine test -count=1 ./...` | exit 0 |
| 生产代码改动 | `git status --short` | 无 |
| 密钥模式扫描 | `git grep -nIE 'sk-[A-Za-z0-9]{16,}\|Bearer [A-Za-z0-9._-]{20,}\|AKID[A-Za-z0-9]{10,}\|-----BEGIN [A-Z ]*PRIVATE KEY-----'` | 仅命中 `runlog/sanitize_test.go:105-106` 的脱敏器测试夹具，非真实凭据 |
| `.env` 跟踪状态 | `git ls-files \| grep -x '.env'` | 无输出 |
| 镜像内 `.env` | 见 AC-001 | 不存在 |

## 13. 已知限制（按当前装配如实记录）

- Todo 未接入 HTTP 服务：`session.TodoOperations` 端口适配属服务装配任务 T08，v1 registry 只有 `calculator`。
- `TokenBudget=0`，`service/runner.go` 拒绝非 0 取值，**不做上下文压缩**；超长会话不会被摘要。
- Session 是进程内存，无持久化；容器重启即丢失，跨实例不共享。本轮为执行负向测试重建过容器，`0d5fb96f...` 会话随之销毁，故无跨重启会话证据。
- 只有 `GET /healthz`、`POST /v1/sessions`、`POST /v1/chat` 三个端点，**无会话读取端点**，会话内容无法从 HTTP 观察；本轮未验证 Session 记忆在多轮后的表现（同一 Run 内工具回合已覆盖）。
- 生产代码无 Provider 重试 / 限流；429 与 5xx 不重发。
- 无流式输出；`stream` 字段不发送。
- `-race` 未进入 CI（`.cnb.yml` 只跑 `make ci`），本轮以本地 `make ci` + `-race -count=1` 执行。
- CNB 镜像发布（`f417327`）只在 `main` push 时触发；本任务在 feature 分支，**无平台发布证据**，标注待验证。
- 本轮未验证 `finish_reason=length`（截断）与并发/压测场景；这两项在 Task 中属 Out of Scope 或仅离线覆盖。

## 14. 交接

- 真实调用已全部停止（授权要求）。容器仍在运行，便于复核；`make down` 可清理。
- 生产代码零改动，`review.target` 即本轮证据提交。
- `AC-009` 的 `delivery.md` 由 Deliverer 在 Cleaner / OwnerGate 之后汇总，本文件为其原始证据来源。
