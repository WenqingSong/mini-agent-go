# Delivery Verification

## Milestone and Target

- Milestone：`M2-agent-runtime-loop`（首次把各模块组合成可运行系统的 L3 Agent Loop）
- Delivery Target：`a77b5f1`（`feat/agent-runtime-loop-v1` HEAD，`delivery-start` Gate PASS 时的 local == remote）
- Cleaner Review Target：`e9dd3daaeffb9a7fb7da30ad94270572593bfdd9`
- Owner Acceptance Target：`e9dd3da`（`owner.review_target` == `review.target`）
- develop Base：`635e311fa6414f1fc786f0a3793f49cec8241047`（`origin/develop`，验证时未前进）
- Target Match：YES —— `git diff --name-only e9dd3da..a77b5f1` 只有 `.agent/tasks/agent-runtime-loop-v1/{state.yaml,findings.md,core-logic.md,owner-decision.md}`；`agent/`、`core/`、`llm/`、`tool/`、`session/`、`contextmgr/`、`runlog/`、`builtin/`、`go.mod`、`Makefile`、`docs/` 零差异。本次除在 `a77b5f1` 验证外，还在与 CLEAN target 完全一致的 detached worktree（`e9dd3da`）上重跑了构建、vet、`-race` 测试与外部 consumer 主链路。
- 集成关系：`git merge-base --is-ancestor origin/develop a77b5f1` → true，`develop_base + feature_head` 的集成结果即 feature 快照，无需合并提交；未创建、未推送任何永久 integration branch，未 push develop，未 merge。

## Environment

- OS：`Linux 5.4.241-1-tlinux4-0025.13 x86_64`
- 运行时：`go1.24.1 linux/amd64`；根 Module `cnb.cool/framework-station/mini-agent-go`（`go 1.24`，零 `require`，构建全程无网络依赖）
- 未使用 MySQL / Redis / Kafka / Docker / 真实模型：本任务的运行期事实源只有内存 Session（Contract「一致性说明」），全部依赖通过公开 API 在进程内注入
- 配置来源：无配置文件、无 Secret；`Config` 的 Provider / Sessions / Registry / Contexts / Sink 由验收程序显式注入
- 工作副本与隔离方式：
  - `/tmp/t07-deliverer`：`git worktree add --detach a77b5f1`（交付快照）
  - `/tmp/t07-review-target`：`git worktree add --detach e9dd3da`（CLEAN 快照）
  - `/tmp/t07-consumer`：仓库外临时 consumer module（`module t07consumer` + `replace` 指向上述 worktree），只 import 公开包，不新增仓库内文件
  - 主仓工作树全程 `git status --porcelain` 为空；验收结束后临时 worktree 与 consumer 已删除
- 测试数据与清理：内存 Session（ID `s-*`），无外部数据写入；无残留状态

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（交付快照 `a77b5f1`） | PASS | `go build ./...` exit 0 |
| Vet（交付快照） | PASS | `go vet ./...` exit 0，无输出 |
| Unit / Component（交付快照） | PASS | `make test`（CI 同入口）9 包全 `ok`；`go test -count=1 ./...` exit 0 |
| Race（交付快照） | PASS | `go test -race -count=1 ./...` 9 包全 `ok`；`agent` 1.042s |
| 用例真实执行 | PASS | `go test -count=1 -v ./agent`：50 个 `=== RUN`、22 个顶层 `--- PASS`、`--- SKIP`/`--- FAIL` 计数为 0 |
| Workflow Engine（独立 Module） | PASS | `GOWORK=off go -C .agent/engine test ./...` exit 0（含 `cmd/workflow-check`、`internal/workflow`） |
| Build / Vet / Race（CLEAN 快照 `e9dd3da`） | PASS | 同上三条命令在该 worktree 重跑，9 包全 `ok` |
| 依赖方向（INV-001） | PASS | `go list -f '{{.ImportPath}} {{join .Imports " "}}' ./agent` → 仅 `context encoding/json errors fmt strings time` + `contextmgr core llm runlog session tool`，无 `builtin` / `llm/openai` |
| Design §12 路径 ↔ `git ls-files agent/` | PASS | `comm -3` 无输出（文档登记的 `agent/*.go` 与实际文件集合一致） |
| 端到端主链路（外部 consumer，公开 API） | PASS | 10 个场景 42 项检查全 PASS，`-race` 下无 DATA RACE；在 `a77b5f1` 与 `e9dd3da` 两个快照上分别执行，结果一致 |
| 启动 / 健康检查 | NOT_APPLICABLE | 本任务 Out of Scope 明确排除 CLI / HTTP Server / 可运行二进制 / Docker；Contract 约定「Deliverer 可用临时 consumer module 通过公开 API 完成端到端验证，不需要 CLI 或可运行二进制」 |

## Acceptance Evidence

本次运行证据（Deliverer 独立执行，非引用 Coder/Cleaner 结论）：

| AC | 本次运行结果 |
|---|---|
| AC-001 单步纯文本 | consumer S1 前置态 + `agent` 包 `TestRunCompletesOnPlainTextResponse` 在隔离快照全绿；S1 多步用例证明 `COMPLETED`、`Error=nil`、`Validate()=nil`、Session 历史 `core.ValidateMessages` 通过 |
| AC-002 LLM→Tool→LLM | consumer S1：`LLMCalls=2`、`ToolCalls=1`；记录型工具真实执行 1 次且参数为 `{"v":1}`；第二次 `llm.Request.Messages` 含 `c1` 的 `RoleTool` 结果；Session 提交 4 条且配对合法；`RunResult.Messages` 恰 3 条 |
| AC-003 单响应多 Call | `agent` 包 `TestRunHandlesMultipleCallsInSingleResponse` 在隔离快照执行通过（成功 + 未知工具 + 参数非法 → 3 条 `RoleTool`、`CallID` 一一对应、下一请求含全部结果、最终 `COMPLETED`） |
| AC-004 失败/预算/取消 | consumer S2（Provider error → `FAILED + provider_failure`，`LLMCalls=1`、`ToolCalls=0`）、S3（`MaxLLMCalls=1` → `LIMIT_EXCEEDED + llm_call_limit`，Provider 实际调用 1 次）、S4（单响应 2 Call / `MaxToolCalls=1` → `LIMIT_EXCEEDED + tool_call_limit`，`ToolCalls=0`、无伪造结果）、S6（150ms 整体超时 → `CANCELLED + deadline_exceeded`）；四例均 `Validate()=nil` 且 Session 与运行前一致 |
| AC-005 并发 | consumer S9：同 Session 3 个并发 Run 全部 `COMPLETED`（各 `LLMCalls=2/ToolCalls=1`），Session 10 条、配对合法、每组 assistant(ToolCalls)→tool→assistant 连续不交错；S10：两个不同 Session 同时进入阻塞 Provider（3s 内双方到达），证明无跨会话全局锁；全链路 `-race` 无告警 |
| AC-006 副作用安全 | consumer S5：同 Run 重复 `CallID=c1` → 工具真实执行 1 次、`ToolCalls=1`、`FAILED + invalid_pairing`、`Error.CallID=c1`、Session 无部分提交；`agent` 包 `TestRunNeverRepeatsSideEffects` 覆盖「工具已执行后 Provider 失败」（结果保留、无回滚/补偿措辞、不重试） |
| AC-007 压缩与移除 | consumer S8：`TokenBudget=12`、`KeepRecent=1` 的超预算历史 → 发给 Provider 的首条为摘要 system 消息（压缩视图）、`Session.Summary="S"`、被覆盖的前 2 条历史已从 Session 移除，剩余 2 条且配对合法；未超预算路径由 S1（`Summary` 保持空）与 `TestRunKeepsHistoryWhenWithinBudget` 覆盖 |
| AC-008 事件与旁路 | consumer S1 解析 `runlog.NewJSONSink` 逐行 JSON：`run_started → llm_request → llm_response → tool_started → tool_finished → llm_request → llm_response → run_finished` 顺序正确（字段名为 `event`）；S7：必定失败的 Sink 下仍 `COMPLETED`、计数 `2/1`、消息序列 3 条、Session 提交 4 条，与正常 Sink 一致 |
| AC-009 工程与一致性 | 构建 / vet / `make test` / `-race` / Engine 测试全 exit 0；依赖方向仅六个允许包；Design §12 路径集合与 `git ls-files agent/` 一致。四方语义核对与 Mutation 证据属 Cleaner 职责，本次未重复审查（见下） |

未提交历史风险抽查：S2/S3/S4/S5/S6 全部在失败后对比 Session 运行前后（消息条数与 `Summary`），均无部分提交。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| CLI / HTTP Server / 二进制启动 / 健康检查 / Docker | 本任务 Out of Scope，Contract 明确以公开 API 的 consumer 作为端到端验证方式 | 服务装配任务（T08）前不存在可启动进程，属既定范围，不构成本里程碑缺口 |
| 真实 LLM Provider 联调、真实 tokenizer 校准、生产 `Summarizer` | Out of Scope（T08）；本次 Provider / Estimator / Summarizer 均为注入替身 | 真实 Provider 的 `finish_reason` 边界（如 `stop` 携带 ToolCall）仍未在真实模型验证，由 Contract D5 规则覆盖 |
| Session 持久化 / 重启恢复 / 多副本一致性 | Out of Scope（内存 Session 为唯一事实源） | 进程退出即丢数据，属后续任务 |
| 性能、并发吞吐、回滚或补偿演练 | Task / Contract 未要求；本任务明确不做并行工具执行优化，且语义上不存在回滚 | 无性能结论；「工具副作用不回滚/不补偿」是设计选择，不能解读为已验证可回滚 |
| Cleaner 四方核对与 Mutation 复现 | Deliverer 不重做完整 Diff Review、不关闭 Finding | 依赖 `CLEAN` 有效性；本次已确认 CLEAN 后无代码/Design 变化 |

## Remaining Risks

- `CLEAN-001`（P3，Design / Contract 措辞滞后于实现）仍 OPEN，Owner 已接受为不阻塞交付；文档同步归 Analyst，不影响运行语义。
- D1 提交边界：失败 / 超限 / 取消的整轮消息不进入 Session，下一轮模型看不到被取消 Run 的工作（Contract 已列为 Open Risk，Owner 已批准该取舍）。
- 整个 Run（含 LLM 与工具耗时）持有会话执行权：同会话其他 Run 必须排队（AC-005 的必然代价）；跨会话不受影响，本次 S10 已证。
- `KeepRecent=0` 且超预算时，压缩可能覆盖最后一条 user 消息（Contract Open Risk）；本次 S8 使用 `KeepRecent=1`，装配侧需按设计控制。
- 环境差异：验收在 Linux + Go 1.24.1 进程内完成；未在 CI 容器镜像（`golang:1.24`）中复跑，但根 Module 零外部依赖、测试不触网，判定无影响。

## Result

PASS
