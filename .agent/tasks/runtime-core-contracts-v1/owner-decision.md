# Owner Decision

## Review Target

`b4b95733ff413a1f21148e763eb9062b824f78be` — `feat(runtime): 落地 Runtime 核心契约与工程入口（runtime-core-contracts-v1）`

## Core Logic

- CL-001：Tool Call 与 Tool Result 必须一一配对——一次调用恰好被一条结果应答一次；缺失、重复或孤儿结果都会让 Loop 在「模型看到的工具输出」与「实际执行」之间错位（`core/pairing.go`，测试 `core/pairing_test.go:81`）。
- CL-002：RunStatus 与 ErrorKind 逐状态一致——取消 / 超时 / 预算耗尽 / 工具失败四类终态互斥，不可混淆；`tool_failure` 必须携带 CallID（`core/error.go`、`core/run.go`，测试 `core/run_test.go:40/168/214`）。

## Owner Decision

ACCEPTED

## Decision Evidence

- Owner 在 OwnerGate Decision Checkpoint 中明确回复 `ACCEPT`，接受对象为 Cleaner 已判 CLEAN 的具体 snapshot `b4b95733ff413a1f21148e763eb9062b824f78be`，而非「抽象地接受本任务」。
- 决定范围：仅接受该 snapshot 上的 CL-001 与 CL-002 两项核心机制及其验证证据；不含对后续任务（Agent Loop / Provider / Session 持久化）的接受。
- OwnerGate 在决策前复跑基线：`go test -count=1 ./...` 与 `go test -count=1 -race ./...` 均 ok；`git diff --stat b4b9573 HEAD -- ':!.agent/tasks'` 为空，CLEAN 仍有效。
- Mutation 证据来自 Cleaner 隔离 worktree 实测（CL-001 删除缺失结果扫描 → 2 条 FAIL；CL-002 去掉 `KindDeadlineExceeded` → 3 条 FAIL），OwnerGate 本轮未重做破坏实验。
- 未决事项（不影响本次 ACCEPTED）：两项 P3 Finding（CLEAN-001 枚举字面量未导出、CLEAN-002 同一缺陷经不同入口返回不同 ErrorKind）Owner 在本次决策中未表态处理方式，保持 OPEN，非阻塞；如需处理须由 Owner 指定走 Contract Revision 或新建后续任务。
- `INV-005`~`INV-008` 在本任务仅有签名与契约级测试，明确归属后续 Agent Loop / Session 持久化任务，不随本次 ACCEPTED 被视为已验证。
