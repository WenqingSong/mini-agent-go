# Owner 核心逻辑验证卡

本文件只承载验证卡与证据。Owner 决策状态由 OwnerGate 在 Owner 明确决定后写入 `owner-decision.md` 与 `state.owner`；本文件不代表 Owner 已接受，也不改变 `review.target`。

## CL-001：Tool Call 与 Tool Result 的配对完整性

- Owner 需要理解：模型发出的一次工具调用必须恰好被一条结果消息应答一次。缺失结果会让 Loop 在「模型看到的工具输出」和「实际执行」之间错位，重复或孤儿结果会让同一次调用被记账两次或凭空出现结果，最终表现为重复执行工具或错误地把失败当成成功。
- 生产代码：`core/pairing.go:26-78`（`ValidateToolPairing`：按消息顺序维护 `pending` / `answered`，逐条校验调用 ID 非空唯一、结果必须指向已出现且未应答的调用）；`core/pairing.go:9-16`（`ValidateMessages` 先逐条结构校验再配对校验）；`core/toolcall.go:35-48`（`ToolResult.CallID` / `ToolCall.ID` 缺失判 `invalid_pairing`）。
- 关键测试：`core/pairing_test.go:81` `TestValidateToolPairingInvalid`（缺失 Result / 重复 Result / 孤儿 Result / 结果早于调用 / 跨消息 ID 复用 / 空 ID / tool 消息缺结果，均断言 `KindOf(err)==invalid_pairing` 且 `CallID` 定位精确）；正向对照 `core/pairing_test.go:19` `TestValidateToolPairingValid`。
- 基线验证：`go test -count=1 -run TestValidateToolPairingInvalid ./core/` → `ok`（Cleaner 已执行）。
- 可选 Mutation：在 `core/pairing.go` 删除末尾「按 ToolCall 顺序补报缺失结果」的循环（当前 69-75 行）。
- 预期失败：`TestValidateToolPairingInvalid/缺失_Result` 报「期望错误种类 invalid_pairing，实际没有返回错误」；`TestValidateMessagesAddsIndexAndKeepsKind` 同样 FAIL。（Cleaner 在隔离 worktree 实测：正是这两条 FAIL，其余用例仍通过。）
- 恢复确认：恢复该循环后 `go test -count=1 ./...` 全绿。

## CL-002：状态与错误种类逐状态一致（含取消 / 超时 / 预算 / 工具失败四类结果不可混淆）

- Owner 需要理解：取消、整体超时、预算耗尽、工具失败在 `RunResult` 上是互斥的四种结论。若把超时算成普通失败、或把预算耗尽算成正常完成，调用方与日志会得出错误结论（例如把「用户取消」当成「模型异常」告警，或把「预算耗尽」当成成功交付）。
- 生产代码：`core/error.go:116-133`（`StatusOf`：nil → `COMPLETED`；`cancelled`/`deadline_exceeded` → `CANCELLED`；`llm_call_limit`/`tool_call_limit` → `LIMIT_EXCEEDED`；其余 → `FAILED`）；`core/run.go:80-145`（`RunResult.Validate`：`RUNNING`/`COMPLETED` 必须无错误；`FAILED` 只接受 5 类失败 Kind 且 `tool_failure` 必须带 `CallID`；`LIMIT_EXCEEDED`/`CANCELLED` 各自白名单；最后用 `StatusOf` 反查状态一致性）；`core/run.go:50-58`（双预算必须为正）。
- 关键测试：`core/run_test.go:40` `TestRunResultValidate`（逐状态正反用例）、`core/run_test.go:168` `TestRunResultStatusKindMatrix`（5 状态 × 10 错误种类全组合，逐一断言合法/非法）、`core/run_test.go:214` `TestStatusCodeMapping`（正常完成 / 取消 / 超时 / LLM 上限 / 工具上限 / 不可恢复工具失败 / Provider 失败 / 普通 error / 包装后的 RunError）。
- 基线验证：`go test -count=1 -run 'TestRunResultStatusKindMatrix|TestStatusCodeMapping|TestRunResultValidate' ./core/` → `ok`（Cleaner 已执行）。
- 可选 Mutation：在 `core/error.go` 的 `StatusOf` 中把 `case KindCancelled, KindDeadlineExceeded:` 改成只匹配 `KindCancelled`（即让整体超时落到 `FAILED`）。
- 预期失败：`TestRunResultValidate/CANCELLED_+_deadline_exceeded`、`TestRunResultStatusKindMatrix/FAILED/deadline_exceeded`、`TestStatusCodeMapping/整体超时` 三条 FAIL。（Cleaner 在隔离 worktree 实测一致。）
- 恢复确认：恢复该 `case` 后 `go test -count=1 -race ./...` 全绿。

## 附：本任务未提供运行期证据的部分（非 Owner 需要手工验证）

INV-005（会话归属）、INV-006（取消在 Loop 中的实际中断）、INV-007（工具失败可恢复性分类与回填顺序）、INV-008（runlog 旁路在真实运行时成立）在 T00 只有签名、映射函数与契约级测试，已由 Contract `Delegated Verification` 与 Design §14 明确归属后续 Agent Loop / Session 持久化任务，不得视为已验证。
