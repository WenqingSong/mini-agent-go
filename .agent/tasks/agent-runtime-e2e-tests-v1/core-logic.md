# Owner 核心逻辑验证

审查对象（C1）：`eed7f237f810c02eda1e4d59a03f8d7b9cb8c631`。以下两处机制决定「失败请求会不会污染会话」与「失败原因会不会被错误分类」，其余为 DTO / 装配辅助。

## CL-001：非 COMPLETED 的 Run 绝不提交（Session 无半轮）

- Owner 需要理解：一次 chat 只有在 Run `COMPLETED` 时才允许把本轮消息写进 Session；任何失败/取消/限额都必须整轮丢弃。若这条不成立，用户会看到「问题被记住、回答没出现」的半轮历史，且下一次请求会把残缺上下文继续送给模型。
- 生产代码：`service/runner.go:181-208`——`RunTurn` 回调内先 `turn.Append(user)`，再 `loop.Run`；`result.Status != COMPLETED` 时回调返回 `runErrorOf(result)` 使整轮丢弃（`:193-195`），并把已形成的结论打包成 `RunOutcome` 返回（`:199-207`）。
- 关键测试：`e2e/failure_test.go:115` `TestProviderFailures`、`:163` `TestBudgetLimits`，断言入口 `e2e/harness_test.go:687` `assertSessionUnchanged`（`Manager.Get` 深拷贝逐字段比较 + `ValidateMessages`）；预置数据由 `failureSeedTodos()` 与 `seed()` 提供，保证断言非平凡。
- 基线验证：`go test ./e2e -run 'TestProviderFailures|TestBudgetLimits|TestRecoverableToolFailuresContinue' -count=1` → 全部 PASS（失败路径会话逐字段不变；可恢复失败路径仍整轮提交）。
- 可选 Mutation：`service/runner.go:193-195` 把 `return runErrorOf(result)` 改为 `return nil`（失败轮也提交）。
- 预期失败：`TestProviderFailures/provider_4xx`（`failure_test.go:147`）报「会话在未完成的 Run 后发生变化：before messages:0 / after messages:1 roles:[user]」；`TestBudgetLimits` 两个子用例同理。
- 恢复确认：还原该行并重跑同一命令 → 全部 PASS（Cleaner 已在隔离 worktree 实测该 Mutation 与其恢复）。

## CL-002：失败三元组唯一且互斥（Provider 超时不得被当成请求超时）

- Owner 需要理解：同一个「超时」在两种来源下必须给出不同结论——Provider 自己不响应是「模型服务失败」（502 `provider_failure` + Run `FAILED`），本服务自己的请求超时才是「请求超时」（504 `deadline_exceeded` + Run `CANCELLED`）。若混淆，监控与重试策略会全部错位。
- 生产代码：判定优先级在 `httpapi/errors.go:166-212`（会话哨兵 → 链中最外层 `*core.RunError.Kind` → 裸 ctx 兜底 → `internal`）；来源侧由 `agent/run.go` 在 ctx 未终结时把 Provider 错误重分类为 `provider_failure`，`llm/openai/client.go:158-166`（Provider 自身超时）与 `:189-195`（4xx/5xx）决定错误链形状。
- 关键测试：`e2e/timeout_test.go:18` `TestProviderUnresponsive`（502 + `provider_failure` + `FAILED`）、`:58` `TestRequestTimeout`（504 + `deadline_exceeded` + `CANCELLED`）、`:92` `TestClientDisconnect`（`cancelled`）；每例同时用 `harness_test.go:810` `assertTerminal` 交叉校验 `RunOutcome` 与 `run_finished` 事件。
- 基线验证：`go test ./e2e -run 'TestProviderUnresponsive|TestRequestTimeout|TestClientDisconnect' -count=3` → 全部 PASS，且 `GOMAXPROCS=1` 下同样稳定。
- 可选 Mutation：`httpapi/errors.go:193-194` 把 `KindLLMCallLimit` 的映射改成 `CodeProviderFailure`（限额被误分类为模型失败）。
- 预期失败：`TestBudgetLimits/MaxLLMCalls耗尽`（`failure_test.go:178`）报「HTTP 状态码 = 502，期望 429（body code=provider_failure）」；说明「状态码 + 错误码 + Run 终态」三元组不是靠错误名猜出来的。
- 恢复确认：还原该行并重跑 `go test ./e2e -run 'TestBudgetLimits' -count=1` → PASS（Cleaner 已在隔离 worktree 实测）。

## 复审补充：时间观测断言不是空断言

- `e2e/timeout_test.go:46-52`、`:84-90` 的 `waitFor(2s)` 只等待「脚本服务确实观察到上游取消」，随后仍断言完整三元组。
- 判别证据：在隔离副本中删除脚本服务侧 `blockedCanceled++`（观测永不成立）→ `TestProviderUnresponsive` 在 2s 后以「脚本服务观察到请求阻塞与上游取消 未在 2s 内成立」失败。
