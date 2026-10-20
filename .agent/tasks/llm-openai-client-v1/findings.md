# Cleaner Findings

## Review Target

当前审查对象（C1'）：`b3d21464e74b5528bc94dae8da406702c7ca4ada` `fix(llm): 拒绝 choices[].message 为 null 的响应载荷（CLEAN-002）`。

- 任务基线：`6902d2445665ec356a2db22b33b7749d39d8018e`（`origin/develop`）。
- 上一轮审查对象（已失效的 C1）：`5633001ac0b21820f230d773631f063cb2f71206`；其 `CLEAN` 已被 Owner `REJECTED` 作废，本轮不沿用。
- C1' 相对 C1 的实质变更（`git diff 5633001 b3d2146 --stat -- llm/ core/ go.mod`）：
  - `llm/openai/wire.go` +11/−3：`choices[0].message` 改为指针解码 + `nil` 拒绝；
  - `llm/openai/client_test.go` +16：新增 `message: null` 与 `message` 非对象两条失败用例，并为既有「content 为 null + 有效 tool_calls」用例补充兼容说明；
  - `core/**`、`llm/provider.go`、`llm/provider_test.go`、`go.mod` 无改动；未新增源文件（§12 表无需再回写）。
- Coder metadata commit（C2'）：`1ea295d8434907b8f6cd6238bf01b14328cf47be`，只改 `.agent/tasks/llm-openai-client-v1/state.yaml`（`review.status=PENDING`、`review.target=C1'`）。
- 审查时工作区：branch `llm-openai-client-v1`，`git status --short` 为空，local HEAD == `origin/llm-openai-client-v1` == C2'。
- 任务前已有修改：无。
- Contract：`NOT_REQUIRED`；Design Impact = `NONE`（Owner 判定），本任务只做 §12 表机械回写。
- Shared Resource：`resources: {}`，Registry 无条目 ↔ Contract 无声明 ↔ 实现无资源常量，一致。
- Owner 指令范围（`owner-decision.md`）：仅要求修复 `CLEAN-002`；`CLEAN-001` 本轮暂缓，作为 P3 保留，不要求修复。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `TestCompleteRequestConstruction`（完整请求逐字段映射：method/path/Content-Type/Authorization + `model`/`messages` 角色顺序/多 text 片段拼接/assistant `tool_calls` 的 id/type/name/arguments 原样/`tool_call_id`/`tools` 三字段与 JSON Schema 原文/`max_tokens`/`temperature`；另含「未配置 APIKey 不发送 Authorization」「BaseURL 自带 `/v1` 与结尾斜杠」「tool 失败结果可区分」）。断言使用独立 `wireBody`/`wireMessage`，不复用生产 wire 类型。本轮请求侧代码未变（`client.go`、`wire.go` 请求映射段无 diff），C1 证据继续有效。 |
| AC-002 | PASS | `TestCompleteResponseParsing` 表驱动（文本 / usage 缺失 / 单 Tool Call 且 content=null / 多 Tool Call 顺序 / length / content 空串）：断言 `Message.Role == assistant`、文本片段、`FinishReason`、`Usage`、ToolCall 的 ID/Name/Arguments 字节一致且顺序保持、`Raw == 响应体原文`、`Message.Raw` 非空，并调用 `Response.Validate()` 断言 nil。C1' 复审实测：`content: null` + 有效 `tool_calls` 仍解析成功（`finish_reason=tool_calls`、1 个 ToolCall）。 |
| AC-003 | PASS | `TestCompleteErrorSemantics`（400/401/429/500、非法 JSON、`{}` 缺 choices、`choices: []`、缺 message、**message 为 null**、**message 非对象**、arguments 非法 JSON、arguments 空串、缺 ID、未知/缺失 `finish_reason`、role 非 assistant、usage 负数、空响应体）：断言 `core.KindOf == provider_failure`、错误文本含状态码/定位词、返回零值 Response、`hits == 1`、错误文本不含 APIKey。`TestCompleteContextFailures`：ctx 超时与 HTTPClient 超时 → `deadline_exceeded`；调用前/调用中取消 → `cancelled`；连接失败 → `provider_failure` 且 `Cause` 非空。C1' 新增两条用例直接覆盖 CLEAN-002。 |
| AC-004 | PASS | 独立执行 `go build ./...`、`go vet ./...`、`go test ./... -count=1`、`go test -race ./... -count=1`，全部退出码 0（Go 1.24.1 linux/amd64）。`git diff 6902d24 b3d2146 -- go.mod` 为空（零 require）。测试源码无真实 LLM 主机名/真实 Key。 |
| AC-005 | PASS | `go list -f '{{.ImportPath}} | {{join .Imports " "}}' ./llm/openai`：仅标准库 + `<module>/core` + `<module>/llm`；`git diff 6902d24 b3d2146 --stat -- core/ llm/provider.go llm/provider_test.go` 为空。修复未新增 import。 |
| AC-006 | PASS | `git ls-files llm/` 仍为 5 个文件，与 §12 表逐行一致；C1' 未新增源文件，表无需变更（C1 已回写 3 行仍与实现一致）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | 退出码 0，无输出（C1'） |
| `go vet ./...` | PASS | 退出码 0，无输出（C1'） |
| `go test ./... -count=1` | PASS | core / llm / llm/openai / runlog 全部 ok |
| `go test -race ./... -count=1` | PASS | 全部 ok，无 race 报告 |
| `go list` 分层核对 | PASS | 见 AC-005 |
| `git diff 6902d24 b3d2146 -- core/ llm/provider.go llm/provider_test.go go.mod` | PASS | 空（公共契约与依赖清单未变） |
| `git ls-files llm/` ↔ §12 表 | PASS | 5/5 一一对应 |
| 旧 C1 的 Mutation M1/M2/M3 | PASS（C1 已验） | HTTP 状态码分流、tool_calls 顺序、取消/超时映射三类 Mutation 当时均被测试识别；C1' 未改动这些逻辑，相关测试仍全绿 |
| **复审** Mutation M4：把 `llm/openai/wire.go` 回退为 C1 版本（去掉 null 拒绝） | PASS（新回归测试生效） | `TestCompleteErrorSemantics/choices_的_message_为_null` 失败：「期望失败，实际成功并返回 assistant/stop 空响应」——证明该回归测试可区分修复前后 |
| **复审** 边界探测（隔离 worktree 内一次性 probe） | PASS | `message:null` → `provider_failure: 响应 choices[0].message 为 null`；`choices[0]` 缺 `message` 键 → `provider_failure: 缺少 message`；`message: []` → `provider_failure`（不是对象）；`message: "oops"` → `provider_failure`；`message: {}` → 成功（message 对象存在，行为与修复前一致，未过度拒绝）；`content:null` + 有效 `tool_calls` → 成功（Tool Calling 兼容保持） |
| 复审 Mutation 隔离方式 | PASS | `git worktree add --detach /tmp/cleaner-mut2 b3d2146`；回退文件与 probe 均在该 worktree 内完成，`git checkout --` 复原后 `git worktree remove --force`；主工作区未被改动（`git status --short` 为空），未提交任何 Mutation |
| 真实外网 / 真实凭据 | PASS | 全部测试仅用 `httptest.Server`；未访问外网，无收费 API 调用 |

## Findings

### CLEAN-001：BaseURL 携带 query / fragment 时被静默丢弃

- Severity：P3
- Status：OPEN（Owner 已明确本轮暂缓，不要求修复，不扩大 Scope）
- Location：`llm/openai/client.go:89-92`
- AC / Invariant：AC-001（`POST <BaseURL>/chat/completions`）
- Trigger：`BaseURL = "https://host/v1?api-version=2024-01-01"` 这类带查询参数的兼容端点
- Actual：`NewClient` 接受配置，endpoint 变为 `https://host/v1/chat/completions`，query 被静默清空
- Expected：保留 query，或在 `NewClient` 明确返回 `invalid_argument`
- Impact：低（标准 OpenAI 端点不用 query）；配置错误表现为难以定位的鉴权/路由失败
- Evidence：C1 轮隔离 probe；`client.go` 在 C1' 未变，结论不变
- Required Fix Boundary：query/fragment 必须「保留」或「拒绝」二选一，且与 `Config` 注释一致；不得改变固定 `/chat/completions` 端点与 `/v1` 前缀行为

### CLEAN-002：`choices[0].message` 为 `null` 时被当作空 assistant 响应接受

- Severity：P3（复审后关闭）
- Status：CLOSED
- Location：`llm/openai/wire.go:266-278`（`parseChatResponse`）
- AC / Invariant：AC-002 / AC-003（协议错误 → `provider_failure`）
- Trigger：`{"choices":[{"message":null,"finish_reason":"stop"}]}`，以及 `message` 非对象的载荷
- Actual（修复前）：解析成功，返回 `assistant` + 空 Content + `stop`，无错误
- Actual（修复后）：`llm/openai/wire.go:272-278` 以 `*chatRespMessage` 指针解码并在 `nil` 时返回 `provider_failure: 响应 choices[0].message 为 null`；`message: []` / `"oops"` 等非对象载荷由 Unmarshal 失败路径返回 `provider_failure`
- Expected：缺失 / `null` / 非对象 message 一律 `provider_failure`，同时保留 `message` 对象存在、`content: null` 且含有效 `tool_calls` 的合法响应
- 修复验证：
  - 代码路径：`wire.go:270-278`（指针解码 + nil 拒绝）；
  - 回归测试：`client_test.go` `TestCompleteErrorSemantics/choices_的_message_为_null`、`/choices_的_message_不是对象`；
  - 修复前区分度：Mutation M4（回退 wire.go 到 C1）使新用例失败，见 Verification；
  - 兼容性：`TestCompleteResponseParsing/单个_Tool_Call（content_为_null）` 仍通过；
  - 无新增请求/重试（`hits == 1` 断言仍通过），未引入真实调用。
- 新风险检查：指针解码未改变其余字段语义；`message: {}`（对象存在）行为与修复前一致，未被过度拒绝；无新增 import，未触碰公共契约。

### 备注（不构成 Finding）

- `docs/design/runtime-architecture.md` §2 / INV-001 的机械判定语句「五个 L1 包只出现 `<module>/core`」在新增 `llm/openai` 子包后字面读法有歧义（子包 import `<module>/llm` 属同层 parent 依赖，AC-005 已显式允许）。属后续 Design 表述精度，不在本任务改文档其他章节。
- `CLEAN-001` 保持 OPEN，等待 Owner 在后续任务中决定是否修复。
