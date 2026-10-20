# Owner 核心逻辑验证卡（agent-runtime-loop-v1）

对象：`review.target = e9dd3da`（CLEAN 只对该 commit 有效）。以下三张卡覆盖本任务真正决定业务正确性的机制：提交边界、双预算、压缩删除。每张卡的 Mutation 都已在隔离 worktree（`git worktree add --detach`）中实际执行并确认「测试必然失败」，未在审查对象工作树内做改动。

## CL-001：一次 Run 只在 `COMPLETED` 时整轮原子提交

- Owner 需要理解：整个 Run（可能包含多次模型调用与多次工具执行）都在同一次 `session.Manager.RunTurn` 内完成，Success 才把消息与摘要一次提交；失败、超限、取消、超时都是「整轮丢弃」，会话与运行前逐字段一致。但**已发生的工具副作用不会回滚、不补偿、不自动重试**——历史回滚 ≠ 世界回滚。若这条判断错位，用户会看到「assistant 请求了工具却没有结果」的半截对话，或误以为失败的副作用已被撤销。
- 生产代码：`agent/agent.go:115`（单次 `RunTurn` 包裹整个 Run）、`agent/agent.go:125-130`（`err` → `core.StatusOf` 终态映射）、`agent/run.go:143-160`（D5 结束语义：只有 `stop` 且无 ToolCall 才 `break` 到提交）、`agent/run.go:212-226`（`SetSummary` → `DropLeading` → `Append` 的提交段，仅在循环正常退出后执行）。
- 关键测试：`agent/agent_test.go` 的 `TestRunFailureSemantics`（六个子用例，逐个断言终态 + 计数 + `Validate()` + `assertSessionUnchanged`）、`TestRunFinishReasonSemantics`（`length` / `tool_calls` 空 Call / `unknown` 都不得 `COMPLETED`）、`TestRunStopsOnCancellationDuringToolExecution`。
- 基线验证：`go test -race -count=1 ./agent -run 'TestRunFailureSemantics|TestRunFinishReasonSemantics|TestRunStopsOnCancellationDuringToolExecution'` → 预期全部 PASS（已随 `go test -race -count=1 ./agent` 全量通过）。
- 可选 Mutation：把 `agent/run.go:145-146` 的 `return providerFailure("模型输出被截断…")` 改成 `break`（即把「输出被截断」当作可以继续/完成）。
- 预期失败：`TestRunFinishReasonSemantics/length + ToolCall → provider_failure 且不执行工具` 必须失败（实际观测：该响应携带的 ToolCall 被执行，`LLMCalls` 由 1 变 2）；这证明「截断响应不得进入工具流程、不得提交」被测试守住。
- 恢复确认：`git checkout -- agent/run.go`，重跑上面的基线命令应全部 PASS，`git status --short` 为空。

## CL-002：双预算在调用前预检，计数等于「实际发起」次数

- Owner 需要理解：LLM 预算在**发出前**检查，所以第 `MaxLLMCalls+1` 次调用根本不会发给模型；工具预算对「一个响应里的整批 Call」一次性预检，额度不足时**一个都不执行、也不生成假结果**（否则模型会看到不存在的执行结果）。计数口径是「实际发起」：Provider 失败的那次、工具 panic 的那次都计入，被预算拒绝或被重复 `CallID` 拒绝的不计入。若这条错位，用户会为未发生的调用付费，或拿到伪造的工具结果。
- 生产代码：`agent/run.go:72-76`（`llmCalls >= MaxLLMCalls` → `llm_call_limit`，在 `Complete` 之前）、`agent/run.go:162-167`（`toolCalls+len(calls) > MaxToolCalls` → `tool_call_limit`，整批拒绝）、`agent/run.go:111`（LLM 计数在调用前 `+1`）、`agent/run.go:184-207`（工具计数在通过预算与重复检查之后、交给 `Executor` 之前 `+1`）。
- 关键测试：`agent/agent_test.go` 的 `TestRunCompletesAtExactBudget`（恰好用满 `MaxLLMCalls=3` / `MaxToolCalls=2` 仍 `COMPLETED`）、`TestRunFailureSemantics/模型调用预算耗尽时不再发出下一次调用`（断言 `provider.callCount()==2`）、`TestRunFailureSemantics/工具预算整批预检：额度不足一个都不执行`（断言 `ToolCalls=0`、执行次数 0、无 `RoleTool` 结果）。
- 基线验证：`go test -count=1 ./agent -run 'TestRunCompletesAtExactBudget|TestRunFailureSemantics'` → 预期全部 PASS。
- 可选 Mutation：把 `agent/run.go:163` 的 `> req.Policy.MaxToolCalls` 改成 `> req.Policy.MaxToolCalls+100`（额度不足时仍然整批执行）。
- 预期失败：`TestRunFailureSemantics/工具预算整批预检` 必须失败（实际观测：终态由 `LIMIT_EXCEEDED` 变为 `FAILED`，因为工具被执行到脚本耗尽）；这证明「整批预检 + 不伪造结果」有测试保护。
- 恢复确认：`git checkout -- agent/run.go`，重跑基线命令应全部 PASS。

## CL-003：压缩偏移在提交时一次性生效（会**删除**已提交历史）

- Owner 需要理解：这是本任务唯一会「删数据」的路径。`contextmgr.Build` 说「前 N 条被摘要覆盖」时，Loop 只把 `N` 累加到 `head`；真正删除发生在提交阶段：`DropLeading(min(head, 运行前基线长度))`，然后只追加 `history[max(head, 基线长度):]`。因此被摘要覆盖的历史（含本 Run 自己的早期消息）不会留在 Session，而摘要文本写进 `Session.Summary`。若偏移算错，用户会永久丢失未被摘要覆盖的消息，或 Session 里出现无法配对的残留。
- 生产代码：`agent/run.go:78-108`（`Build` 调用、`head` 累加与越界自检）、`agent/run.go:212-226`（`SetSummary` → `DropLeading` → `Append`，同一 `RunTurn` 内原子提交）。
- 关键测试：`agent/context_test.go` 的 `TestRunCompactsOverBudgetHistory`（断言 Session 只剩 `[a2, tool(c1), a3]`、`Summary` 被替换）、`TestRunDropsRunMessagesWhenCompactionCrossesBaseline`（覆盖 `head` 越过基线长度）、`TestRunKeepsHistoryWhenWithinBudget`（未超预算不删、摘要不变）。
- 基线验证：`go test -count=1 ./agent -run 'TestRunCompactsOverBudgetHistory|TestRunDropsRunMessagesWhenCompactionCrossesBaseline|TestRunKeepsHistoryWhenWithinBudget'` → 预期全部 PASS。
- 可选 Mutation：把 `agent/run.go:216` 的 `if drop := min(head, baseLen); drop > 0` 改成 `drop < 0`（永不删除被覆盖前缀，只写摘要）。
- 预期失败：`TestRunCompactsOverBudgetHistory` 必须失败（实际观测：Session 仍为 `[u1 a1 a2  a3]`，被覆盖消息未移除）；`TestRunDropsRunMessagesWhenCompactionCrossesBaseline` 同样失败（`[u1 a2]`）。
- 恢复确认：`git checkout -- agent/run.go`，重跑基线命令应全部 PASS。

说明：三张卡的 Mutation 都是可逆小改动，破坏的是真实业务不变量（提交边界 / 预算预检 / 删除偏移），不是制造编译错误；实际执行时测试均按预期失败，未出现「破坏后仍然通过」的情况。
