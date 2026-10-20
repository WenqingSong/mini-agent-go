# Owner Decision

## Review Target

`e9dd3daaeffb9a7fb7da30ad94270572593bfdd9`（Cleaner `CLEAN` 的实现证据 commit C1）

Owner 接受的是该 commit 对应的具体 snapshot，不是「抽象地接受本任务」。

## Core Logic

- CL-001：一次 Run 只在 `COMPLETED` 时整轮原子提交——整个 Run 在同一次 `RunTurn` 内完成，失败/超限/取消/超时整轮丢弃、会话与运行前一致；但已发生的工具副作用不回滚、不补偿、不自动重试（历史回滚 ≠ 世界回滚）。
- CL-002：双预算在调用前预检，计数只算「实际发起」——第 `MaxLLMCalls+1` 次不会发出；工具预算整批预检，额度不足一个都不执行、不伪造结果；Provider 失败与工具 panic 的那次计入，被预算或重复 `CallID` 拒绝的不计入。
- CL-003：压缩偏移在提交时一次性生效——`SetSummary` + `DropLeading(min(head, 运行前基线长度))` + 只追加 `history[max(head, 基线长度):]`，被摘要覆盖的历史（含本 Run 早期消息）从 Session 永久移除，摘要文本写入 `Session.Summary`。

## Owner Decision

ACCEPTED

## Decision Evidence

- 决策来源：Owner 在 OwnerGate Session 中、阅读上述三个核心机制（生产代码位置、关键测试、基线验证与变异预期失败）后，明确回复 `ACCEPT`。OwnerGate 仅作 File Writer，未自行生成该决定。
- 决策前置：`gate owner-gate-start` PASS（`review.status=CLEAN` 且 CLEAN Validity 有效）；当前 HEAD `1dbe646` 为 Cleaner 元数据 tail，`git diff e9dd3da -- agent/` 为空，CLEAN 未失效。
- 基线复验：OwnerGate 实跑 `go test -race -count=1 ./agent -run 'TestRunFailureSemantics|TestRunFinishReasonSemantics|TestRunStopsOnCancellationDuringToolExecution|TestRunCompletesAtExactBudget|TestRunCompactsOverBudgetHistory|TestRunDropsRunMessagesWhenCompactionCrossesBaseline|TestRunKeepsHistoryWhenWithinBudget'` → 全部 PASS。
- 变异证据：三个机制的可逆变异的实际执行与「测试必然失败」结论来自 Cleaner `findings.md` / `core-logic.md`（隔离 worktree 内执行，未污染审查对象工作树）。
- 适用范围：仅覆盖 `review.target = e9dd3da`；`review.target` 之后的任何实质代码变化都会使本决定失效，需重新走 Cleaner → OwnerGate。
- 非阻塞风险：`CLEAN-001`（P3，Design / Contract 措辞滞后于实现，如 `core.Message` 无 `Name` 字段的要求）被 Owner 接受为不阻塞交付；文档同步仍归 Analyst，不改变本 AC 与运行时语义。
