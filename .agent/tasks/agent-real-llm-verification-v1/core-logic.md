# Owner Core Logic 验证卡

本任务不修改生产代码，但它的结论（「真实 Provider 发起了 Calculator 工具调用」与「Provider 错误按预期分类」）只成立于两条既有机制。Owner 只需确认这两条。

结论依据版本：`review.target = aad9dd4518c90445b32812f5f7174612790598a0`。

## CL-001：工具调用只能由 Provider 响应驱动，`1776` 只能是 calculator 的真实计算结果

- Owner 需要理解：应用里没有任何「看到「计算」两个字就去调工具」的分支；工具列表完全来自 Provider 返回的 `tool_calls`，`1776` 也只有 `op=mul, a=37, b=48` 的真实运算能得到。若这条不成立，AC-004 / AC-005 就只是自导自演。
- 生产代码：
  - `agent/run.go:141` `calls := resp.Message.ToolCalls`（全文件唯一的工具分发入口）
  - `agent/run.go:147` `finish_reason=tool_calls` 但无工具调用 → `provider_failure`（不接受"声明了却没给"）
  - `builtin/calculator.go:14` 参数协议只有 `op` / `a` / `b`；`builtin/calculator.go:80-93` 的 `mul` 分支
- 关键测试：
  - `builtin/calculator_test.go:36 TestCalculatorSuccess`（各运算符结果断言）
  - `agent/agent_test.go:487 TestRunFinishReasonSemantics`（子用例「tool_calls 却无任何 ToolCall → provider_failure」）
  - `e2e/chat_test.go:35 TestMultiToolChainCommitsFullTurn`（工具轮 + 终稿整轮提交）
- 基线验证：`go test ./builtin ./agent ./e2e -run 'TestCalculatorSuccess|TestRunFinishReasonSemantics|TestMultiToolChainCommitsFullTurn' -count=1` → 预期全部 `ok`（已由 Cleaner 复跑，与 `make ci` 结果一致）。
- 可选 Mutation：把 `builtin/calculator.go:86` 的 `result = a * b` 改成 `result = a*b + 1`。
- 预期失败：`TestCalculatorSuccess` 的乘法子用例必须失败（Cleaner 已在隔离副本实测：`负数乘法: want=-10 got=-9 diff=1`）。若仍通过，说明「1776 来自真实计算」这一环没有测试保护。
- 恢复确认：还原该行并重跑上面的命令，全部通过。

## CL-002：`finish_reason` 白名单 fail-closed，决定 AC-006 的 502 `provider_failure`

- Owner 需要理解：只有 `stop` / `tool_calls` / `length` 被接受，其它取值（如 `content_filter`、`end_turn`）一律按协议错误归为 `provider_failure`，不会被当成正常回答继续往下走；这正是负向测试拿到 HTTP 502 而不是 200 的机制。
- 生产代码：`llm/openai/wire.go:346-353 mapFinishReason`（`default` 直接 `protocolError` → `provider_failure`）；第二层防御在 `llm.Response.Validate`；`agent/run.go:145-158` 保证三种结束语义都不得 `COMPLETED`。
- 关键测试：`llm/openai/client_test.go:508 TestCompleteErrorSemantics`（子用例「未知 finish_reason」、「finish_reason 缺失」）；`agent/agent_test.go:487 TestRunFinishReasonSemantics`
- 基线验证：`go test ./llm/openai ./agent -run 'TestCompleteErrorSemantics|TestRunFinishReasonSemantics' -count=1` → 预期全部 `ok`（已由 Cleaner 复跑）。
- 可选 Mutation：在 `mapFinishReason` 的 `default` 之前插入 `case "content_filter": return llm.FinishReason(reason), nil`（即放行一个未知取值）。
- 预期失败：`TestCompleteErrorSemantics/未知 finish_reason` 必须失败（Cleaner 已在隔离副本实测：错误文本不再可定位 `finish_reason`，被 `llm.Response.Validate` 以 `invalid_argument` 拦下）。
- 恢复确认：还原该 `case` 并重跑上面的命令，全部通过。

说明：两处 Mutation 均在 `/tmp` 下的隔离副本（不含 `.git`）执行，未触碰 review target 工作区，副本已删除；仓库工作区只保留 Cleaner 自己的 Artifact 变更。
