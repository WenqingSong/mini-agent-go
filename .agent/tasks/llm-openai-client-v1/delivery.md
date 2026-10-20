# Delivery Verification

## Milestone and Target

- Milestone：`llm-openai-client-v1`（OpenAI-compatible LLM Client v1）首个交付验收。`task.md` 未声明 `Milestone` 字段，Milestone 由本次 Owner 明确发起 `mode: milestone_verification` 与 Task Goal 确定；验收对象是该任务的可运行交付面（`llm.Provider` 实现），不是可部署服务。
- Delivery Target：`feature_head = 8602515b883aaef70837add50faf2305f0695627`（branch `llm-openai-client-v1`，local HEAD == `origin/llm-openai-client-v1`）；`develop_base = 6902d2445665ec356a2db22b33b7749d39d8018e`（fetch 后 `origin/develop` 未前进）。
- Cleaner Review Target：`b3d21464e74b5528bc94dae8da406702c7ca4ada`（`state.review.target` = `state.owner.review_target`）。
- Target Match：YES。`git diff --name-only b3d2146 8602515` 仅 4 个 `.agent/tasks/llm-openai-client-v1/` artifact（`core-logic.md` / `findings.md` / `owner-decision.md` / `state.yaml`），业务代码、测试、`docs/design` 零差异，即验收的代码快照与 CLEAN snapshot 完全相同。
- `delivery-start` Gate：`PASS`（exit 0），三个 SHA 均为真实 commit 且 `origin/develop` 是 feature 的祖先（fast-forward 可集成，无冲突）。

## Environment

- 系统：Linux（容器内），`amd64`。
- Go：`go1.24.1 linux/amd64`，与 Task「Linux + Go 1.24.x」一致。
- 依赖：`go.mod` 零 `require`（`grep -c '^require'` = 0），`git diff 6902d24 8602515 -- go.mod go.sum` 为空；无 MySQL / Redis / MQ / Docker 参与，符合 Task「无需外部服务」。
- 冻结方式：`git worktree add --detach /tmp/dv-feature 8602515`，从已提交树取材料，`git status` 干净 —— 构建与测试不依赖未提交文件或本机绝对路径。
- 离线方式：全部命令以 `GOWORK=off GOPROXY=off GOFLAGS=-mod=readonly` 执行，未下载任何模块。
- 配置来源：仅测试内 `httptest.Server` 地址；APIKey 为本次临时生成的假值（形如 `sk-dv-…`），非真实凭据，未记录 Secret 值。
- 测试数据与隔离：独立 smoke 模块 `/tmp/dv-smoke`（`replace` 指向该 worktree，不在仓库内），服务端为进程内 `httptest`，无持久化数据；验证结束后删除临时 worktree 与 smoke 模块，主工作区与远端未被改动。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | `go build ./...` exit 0、`go vet ./...` exit 0（冻结快照、离线、`-mod=readonly`） |
| Unit / Race | PASS | `go test ./... -count=1` 全绿（core / llm / llm/openai / runlog）；`go test -race ./... -count=1` 全绿，无 race 报告 |
| Core Flow（独立 smoke，非 Coder 测试） | PASS | `/tmp/dv-smoke`（`go run -race`，exit 0，73 项检查全 PASS）：真实 HTTP 两轮工具调用主链路 —— 请求构造 → 响应解析出 2 个 `tool_calls` → assistant 回传 `tool_calls` + 两条 `tool` 消息（1 成功 / 1 可恢复失败）→ 第二轮返回最终文本 |
| Data（服务端实际收到的请求） | PASS | 第 1 轮：`POST /v1/chat/completions`、`Content-Type: application/json`、`Authorization: Bearer <key>`（无 Key 时该头缺失）、`model`/`max_tokens`/`temperature` 一致、`messages[0]` 为 system 且为纯文本、user 正文按片段拼装、`tools[0]` 为 `type=function` 且 `parameters` 与 JSON Schema 原文一致；第 2 轮：assistant 的 2 个 `tool_calls` 的 `id`/`type`/`name`/`arguments` 字节级透传、`tool` 消息带 `tool_call_id`、失败结果文本为「工具执行失败: 车票服务超时」可与成功结果区分 |
| Failure semantics | PASS | 400/401/429/500 → `provider_failure` 且错误文本含状态码、每次只发 1 次请求；非法 JSON / 缺失 `choices` / `choices: []` / `message: null` / `arguments` 非法 JSON / 未知 `finish_reason` → `provider_failure` 且不返回半成品 `Response`（Role 为空、`Raw` 为空）；ctx 超时 → `deadline_exceeded`；调用前取消 → `cancelled`；连接失败 → `provider_failure` 且 `RunError.Cause` 非空；任一错误文本不含 APIKey 明文 |
| Layering | PASS | `go list -f '{{.ImportPath}} => {{join .Imports " "}}' ./llm/openai` = 标准库 + `<module>/core` + `<module>/llm`，无 `tool` / `session` / `contextmgr` / `runlog` |
| 外部网络 / 真实凭据 | PASS | 源码中 `api.example.com` 仅用于端点拼接断言（`client_test.go:826-836`，不发起请求），其余为 `httptest` 的 `127.0.0.1`；未访问外网、无收费调用 |

## Acceptance Evidence

- AC-001 → 第 1 轮真实请求逐字段核对（method / path / Content-Type / Authorization / model / system+user 消息 / tools / max_tokens / temperature），无 APIKey 客户端实测收到空 `Authorization`。
- AC-002 → 2 个 `tool_calls` 顺序与 `arguments` 字节级一致（`{"query":"Q1","top":3}`、`{"id":"X-9"}`），`Message.Role=assistant`、`FinishReason=tool_calls`、`Usage` 41/17、`Raw` 与服务端响应体逐字节相等、`Response.Validate()` = nil；第二轮文本回答为 `stop` + 88/12。
- AC-003 → 见 Failure semantics 行：4 个状态码、6 类协议错误、超时、取消、连接失败、非法配置/缺失 Model（且 `hits == 0`，未发出请求）全部符合契约 Kind。
- AC-004 → 构建 / vet / 测试 / race 全绿，离线执行，`go.mod` 零 `require` 且相对 develop 未变。
- AC-005 → 分层 import 实测只含标准库与 `core`、`llm`；`core/**`、`llm/provider.go` 相对 `6902d24` 无改动。
- AC-006 → `git ls-files llm/` 为 5 个文件；`docs/design/runtime-architecture.md:156-158` 三行覆盖本任务新增的 `client.go` / `wire.go` / `client_test.go`，路径一一对应。
- CL-001（错误种类决定 Run 终态、默认单次请求）→ 本次实测 400/401/429/500 均 `hits == 1` 且 Kind 分流正确。
- CL-002（`arguments` 原始 JSON 透传与顺序保持）→ 请求侧与响应侧双向字节级核对通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 服务启动 / 健康检查 / 配置加载入口 | 交付物是库内 `llm.Provider` 实现；Task Out of Scope 明确不含 CLI、HTTP Server、可运行二进制 | 无独立进程可启动，不构成当前里程碑缺失 |
| 真实兼容服务端调用（TLS、真实鉴权、gzip、连接复用、真实 `usage`） | Task Out of Scope 与 AC-003 明确禁止真实 API 调用与外部网络 | 真实服务端行为差异未被本次证据覆盖，见 Remaining Risks |
| 压测 / 重启 / 回滚 / 故障恢复演练 | Task 与 Contract 未要求 | 无 |

## Remaining Risks

- 真实 OpenAI 兼容服务端未联网验证（Task 禁止），TLS、真实鉴权失败形态、gzip、长连接复用行为属推断范围，仅有本地 `httptest` 证据。
- `CLEAN-001`（P3，`NewClient` 静默丢弃 BaseURL 的 query / fragment）保持 `OPEN`，Owner 已明确本轮暂缓：带查询参数的兼容端点会表现为难以定位的路由/鉴权失败。
- 未知 `finish_reason`（如真实服务的 `content_filter`）按契约整体失败为 `provider_failure`，行为符合 AC-003，但线上会把「内容被安全策略拦截」呈现为 Provider 失败而非可读业务语义；属 Contract 现状，不在本次判定范围。
- `task.md` 未声明 `Milestone` 字段，且其中 Assumption 记录「本任务不启用 Deliverer」；本次由 Owner 明确以 `mode: milestone_verification` 发起，`delivery-start` Gate 亦为 PASS，故按运行验收执行。文档层面存在表述未同步，需 Owner 决定是否回写 TaskBuilder artifact。

## Result

PASS
