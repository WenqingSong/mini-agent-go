# Cleaner Findings

## Review Target

- 任务基线：`6902d2445665ec356a2db22b33b7749d39d8018e`（`origin/develop`，Review Baseline）。
- 被审查对象（C1）：`5633001ac0b21820f230d773631f063cb2f71206` `feat(llm): 新增 OpenAI 兼容 Chat Completions Provider 实现`。
- C1 变更集（`git show --stat 5633001`）：
  - 新增 `llm/openai/client.go`（236）、`llm/openai/wire.go`（358）、`llm/openai/client_test.go`（821）；
  - `docs/design/runtime-architecture.md` §12 表新增 3 行（+3）；
  - `core/**`、`llm/provider.go`、`llm/provider_test.go`、`go.mod` 无改动。
- Coder metadata commit（C2）：`ab4697ebb41169692b7253cc920aef1cef67fb1b`，`git diff 5633001 ab4697e --stat` 仅 `.agent/tasks/llm-openai-client-v1/state.yaml`（2 行）。
- 审查时工作区：branch `llm-openai-client-v1`，`git status --short` 为空，local HEAD == `origin/llm-openai-client-v1` == C2。
- 任务前已有修改：无（与 task.md `Review Baseline` 一致）。任务级 Artifact（`.agent/tasks/llm-openai-client-v1/*`）不参与业务行为判定。
- Contract：`state.contract.status = NOT_REQUIRED`，Design Impact = `NONE`（Owner 判定）；本任务只做 §12 表的机械一致性回写，不新增模块边界 / 数据模型 / 状态机 / 公开协议。
- Shared Resource：`.agent/workflow.yaml` 的 `resources: {}`，`state.resources.reservations` 为空，无 Registry 参与，三边一致性检查不适用（Registry 无条目 ↔ Contract 无声明 ↔ 实现无资源常量，一致）。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestCompleteRequestConstruction`（完整请求逐字段映射：method/path/Content-Type/Authorization + `model`/`messages` 角色顺序/多 text 片段拼接/assistant `tool_calls` 的 id/type/name/arguments 原样/`tool_call_id`/`tools` 三字段与 JSON Schema 原文/`max_tokens`/`temperature`；另含「未配置 APIKey 不发送 Authorization」「BaseURL 自带 `/v1` 与结尾斜杠」「tool 失败结果可区分」子用例）。断言使用独立 `wireBody`/`wireMessage` 结构，不复用生产 wire 类型。 |
| AC-002 | PASS | `TestCompleteResponseParsing` 表驱动（文本 / usage 缺失 / 单 Tool Call 且 content=null / 多 Tool Call 顺序 / length / content 空串）：断言 `Message.Role == assistant`、文本片段、`FinishReason`、`Usage`、每个 ToolCall 的 ID/Name/Arguments 字节一致且顺序保持、`Response.Raw == 响应体原文`、`Message.Raw` 非空，并对每个成功响应调用 `Response.Validate()` 断言 nil。 |
| AC-003 | PASS | `TestCompleteErrorSemantics`（400/401/429/500、非法 JSON、`{}` 缺 choices、`choices: []`、缺 message、arguments 非法 JSON、arguments 空串、缺 ID、未知/缺失 `finish_reason`、role 非 assistant、usage 负数、空响应体）：断言 `core.KindOf == provider_failure`、错误文本含状态码/定位词、返回零值 Response（无半成品）、`hits == 1`（429/5xx 不重发）、错误文本不含测试 APIKey。`TestCompleteContextFailures`：ctx 超时 → `deadline_exceeded`；HTTPClient 超时 → `deadline_exceeded`；调用前与调用中取消 → `cancelled`（调用前取消另断言不发请求）；连接失败 → `provider_failure` 且 `RunError.Cause` 非空。 |
| AC-004 | PASS | 独立执行 `go build ./...`、`go vet ./...`、`go test ./...`、`go test -race ./...`，全部退出码 0（Go 1.24.1 linux/amd64）。`git diff 6902d24 5633001 -- go.mod` 为空（零 require）。测试源码扫描无真实 LLM 主机名与真实 Key（仅 `sk-test-secret-key-do-not-log` 假凭据、`api.example.com` 保留域且不发起请求）。 |
| AC-005 | PASS | `go list -f '{{.ImportPath}} | {{join .Imports " "}}' ./...`：`llm/openai` 仅 import 标准库（bytes/context/encoding/json/errors/fmt/io/net/net/http/net/url/strings/time）+ `<module>/core` + `<module>/llm`，无 `tool`/`session`/`contextmgr`/`runlog`。`git diff 6902d24 5633001 --stat -- core/ llm/provider.go llm/provider_test.go` 为空。 |
| AC-006 | PASS | `git ls-files llm/` 共 5 个文件（`llm/provider.go`、`llm/provider_test.go`、`llm/openai/client.go`、`llm/openai/wire.go`、`llm/openai/client_test.go`），与 §12 表逐行一一对应，无遗漏、无改名未回写；§12 其他章节未改动（Diff 只有 +3 行）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出 |
| `go vet ./...` | PASS | 退出码 0，无输出 |
| `go test ./...` | PASS | core / llm / llm/openai / runlog 全部 ok（`llm/openai` 0.071s） |
| `go test -race ./...` | PASS | 全部 ok（`llm/openai` 1.100s），无 race 报告 |
| `go list` 分层核对 | PASS | 见 AC-005 |
| `git diff 6902d24 5633001 -- core/ llm/provider.go llm/provider_test.go go.mod` | PASS | 空（公共契约与依赖清单未变） |
| `git ls-files llm/` ↔ §12 表 | PASS | 5/5 一一对应 |
| Mutation M1（`resp.StatusCode > 299` → `>= 500`） | PASS（测试识别到） | `TestCompleteErrorSemantics/HTTP_400|HTTP_401|HTTP_429_只请求一次` 失败：错误文本只剩 `parseChatResponse: 响应缺少 choices`，丢失状态码 |
| Mutation M2（反转解析 tool_calls 遍历顺序） | PASS（测试识别到） | `TestCompleteResponseParsing/多个_Tool_Call_保持顺序` 失败：ToolCall[0] 期望 `call_a` 实得 `call_b` |
| Mutation M3（`contextRunError` 取消分支短路为 false） | PASS（测试识别到） | `TestCompleteContextFailures/调用前已取消` 与 `调用中取消` 失败：`cancelled` 被判成 `deadline_exceeded` |
| Mutation 隔离方式 | PASS | `git worktree add --detach /tmp/cleaner-mut 5633001`，在独立 worktree 内改码 + 只跑 `go test ./llm/openai/`；每次 `git checkout --` 复原；结束后 `git worktree remove --force`，主工作区未被改动（`git status --short` 为空）。 |
| 探测：`choices[0].message` 为 `null` | 观察到（见 CLEAN-002） | 一次性 probe 测试（在隔离 worktree 内，已随 worktree 删除）：`err=<nil>`，返回 assistant + 空 Content，`finish_reason=stop` |
| 探测：BaseURL 带 query | 观察到（见 CLEAN-001） | 隔离 probe：`NewClient("https://api.example.com/v1?api-version=...")` 的 endpoint 为 `https://api.example.com/v1/chat/completions`，query 被静默丢弃 |
| 真实外网 / 真实凭据 | PASS | 全部测试仅用 `httptest.Server`；未访问外网 |

## Findings

### CLEAN-001：BaseURL 携带 query / fragment 时被静默丢弃

- Severity：P3
- Status：OPEN
- Location：`llm/openai/client.go:89-92`（`parsed.Path = ... + chatPath; parsed.RawQuery = ""; parsed.Fragment = ""`）
- AC / Invariant：AC-001（`POST <BaseURL>/chat/completions`）；任务 OPEN QUESTION（是否支持 BaseURL 已含 `/v1`）
- Trigger：配置 `BaseURL = "https://host/v1?api-version=2024-01-01"` 这类带查询参数的兼容端点（Azure 风格）
- Actual：`NewClient` 接受该配置，但 endpoint 变为 `https://host/v1/chat/completions`，query 被静默清空
- Expected：要么保留 query，要么在 `NewClient` 明确拒绝并返回 `invalid_argument`；不应出现「配置合法但语义被静默改写」
- Impact：低。标准 OpenAI 端点不使用 query，任务未要求支持；但配置错误会表现为难以定位的鉴权/路由失败，而不是构造期报错
- Evidence：隔离 worktree probe（见 Verification 表最后一行）
- Required Fix Boundary：BaseURL 携带 query/fragment 时必须「保留」或「拒绝」二选一，且与 `Config` 注释一致；不得改变固定 `/chat/completions` 端点与 `/v1` 前缀行为

### CLEAN-002：`choices[0].message` 为 `null` 时被当作空 assistant 响应接受

- Severity：P3
- Status：OPEN
- Location：`llm/openai/wire.go:266-273`（`len(choice.Message) == 0` 只拦空字节，`null` 长度为 4 可直接 Unmarshal 为零值）
- AC / Invariant：AC-002 / AC-003（协议错误 → `provider_failure`）
- Trigger：服务端返回 `{"choices":[{"message":null,"finish_reason":"stop"}]}`
- Actual：解析成功，返回 `Message{ID, Role: assistant, Raw: "null"}` + `FinishReason=stop`，无错误
- Expected：这是非法协议载荷（成功响应必须带 message 对象），应返回 `provider_failure`
- Impact：低。健康服务不会返回该形态；若出现，装配层会拿到「空文本 + stop」的成功响应，而不是明确的 provider 失败
- Evidence：隔离 worktree probe（见 Verification 表）
- Required Fix Boundary：`message` 缺失 / `null` / 非对象时统一按协议错误返回 `provider_failure`；不得放宽其他字段的既有校验

### 备注（不构成 Finding）

- `docs/design/runtime-architecture.md` §2 / INV-001 的机械判定语句原文是「五个 L1 包只出现 `<module>/core`，出现任何其他模块内 import 即违反分层」。新增 `llm/openai` 是 `llm` 的子包，import `<module>/llm` 属同层 parent 依赖，与任务 AC-005 的显式允许一致，不构成实现缺陷；但该句字面读法会产生歧义。按 TaskBuilder 的 Assumption（若判定为 Design `UPDATE` 则回 Owner 校正），此类表述精度问题留给后续 Design 维护，不在本任务内改文档其他章节。
- P3 未处理不阻塞 `CLEAN`；CLEAN-001 / CLEAN-002 是否在本任务修复由 Owner 决定。
