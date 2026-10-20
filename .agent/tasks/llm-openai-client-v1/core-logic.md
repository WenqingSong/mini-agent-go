# Owner 核心逻辑验证卡

被审查对象：C1 `5633001ac0b21820f230d773631f063cb2f71206`（结论 `CLEAN`，见 `findings.md`）。

本文件只承载 Owner 核心逻辑验证卡与因果说明，不代表 Owner 已接受。

## CL-001：错误种类分流决定 Run 终态，且默认只发一次请求

- Owner 需要理解：`cancelled` / `deadline_exceeded` 让 Run 走 `CANCELLED`，`provider_failure` 走 `FAILED`（`core.StatusOf`）。若把用户取消误判成 provider 失败，一次主动停止会被记成故障；若把 5xx/429 当可重试而重发，就会违反「默认关闭自动重试」并在故障时放大调用量。
- 生产代码：
  - 调用前 ctx 检查与 `invalid_argument` 分流：`llm/openai/client.go:115-120`
  - 传输层失败分流（先看 `ctx.Err()`，再看 `net.Error.Timeout()`，否则 provider_failure 保留 Cause）：`llm/openai/client.go:150-173`
  - HTTP 非 2xx → `provider_failure`（含状态码）：`llm/openai/client.go:189-195`
  - `contextRunError`：取消 → `cancelled`，超时 → `deadline_exceeded`：`llm/openai/client.go:207-223`
- 关键测试：
  - `TestCompleteContextFailures`（`llm/openai/client_test.go:665-779`）：服务端阻塞 + ctx 超时 → `deadline_exceeded`；`HTTPClient.Timeout` → `deadline_exceeded`；调用前/调用中取消 → `cancelled`；连接失败 → `provider_failure` 且 `Cause` 非空
  - `TestCompleteErrorSemantics`（`llm/openai/client_test.go:507-662`）：400/401/429/500 → `provider_failure` 且错误文本可定位状态码，`hits == 1` 证明只发一次请求
- 基线验证：`go test ./llm/openai/ -run 'TestCompleteErrorSemantics|TestCompleteContextFailures' -count=1` → `ok`
- 可选 Mutation：把 `llm/openai/client.go:189` 的 `resp.StatusCode > 299` 改成 `resp.StatusCode >= 500`（模拟「只把 5xx 当失败」），或把 `:209` 的 `errors.Is(ctxErr, context.Canceled)` 短路为 `false`
- 预期失败：
  - 前者 → `TestCompleteErrorSemantics/HTTP_400`、`/HTTP_401`、`/HTTP_429_只请求一次` 失败（错误文本丢失状态码，只剩 `parseChatResponse: 响应缺少 choices`）
  - 后者 → `TestCompleteContextFailures/调用前已取消`、`/调用中取消` 失败（`cancelled` 被判成 `deadline_exceeded`）
  - 已实测：两种 Mutation 均按预期失败
- 恢复确认：`git checkout -- llm/openai/client.go`，重跑 `go test ./llm/openai/ -count=1` → `ok`

## CL-002：工具调用参数原始 JSON 透传与顺序保持

- Owner 需要理解：`tool_calls[].function.arguments` 是模型给出的原始 JSON 文本，契约层不解析、不改写。若实现把它反序列化再序列化（或打乱 `tool_calls` 顺序），模型后续看到的参数可能与原始输出不一致，工具会拿到被改写/错配的入参——这是工具调用正确性的根。
- 生产代码：
  - 请求侧：`llm/openai/wire.go:181-198`（assistant `ToolCalls` → `function.arguments` 用 `string(call.Arguments)` 原样透传，空参数回填 `{}`）
  - 响应侧：`llm/openai/wire.go:298-318`（按服务端返回顺序遍历，`Arguments: json.RawMessage(call.Function.Arguments)` 保留原始字节，并用 `json.Valid` 拒绝非法 JSON）
- 关键测试：
  - `TestCompleteResponseParsing/多个_Tool_Call_保持顺序`（`llm/openai/client_test.go:417-426`，断言字节级一致与下标顺序）
  - `TestCompleteRequestConstruction/完整请求逐字段映射`（`llm/openai/client_test.go:236-238`，断言请求侧 `arguments` 原样 `{"text":"hi"}`）
  - `TestCompleteErrorSemantics/arguments_非法_JSON`（`llm/openai/client_test.go:572-577`，非法参数必须 `provider_failure`）
- 基线验证：`go test ./llm/openai/ -run 'TestCompleteResponseParsing|TestCompleteRequestConstruction' -count=1` → `ok`
- 可选 Mutation：把 `llm/openai/wire.go:300` 的遍历改为逆序（`for i := len(message.ToolCalls) - 1; i >= 0; i--`）
- 预期失败：`TestCompleteResponseParsing/多个_Tool_Call_保持顺序` 失败（`ToolCall[0].ID` 期望 `call_a` 实得 `call_b`）；已实测按预期失败
- 恢复确认：`git checkout -- llm/openai/wire.go`，重跑 `go test ./llm/openai/ -count=1` → `ok`

## 说明

- Mutation 全部在独立 worktree（`/tmp/cleaner-mut`，detached 于 C1）内完成，主工作区未改动，worktree 已删除，未提交任何 Mutation。
- 未列入验证卡的 P3（`CLEAN-001` BaseURL query 被丢弃、`CLEAN-002` `message: null` 被接受）不影响上述两条核心不变量，是否修复由 Owner 决定。
