# Cleaner Findings

## Review Target

- 任务基线：`0977f243df0348fa5e526b8d954e3f9a2720237d`（`merge: 合并 feat/agent-project-docs-v1 到 develop`）；任务开始时工作区 clean、无未跟踪文件。
- Implementation Evidence Commit（C1，本次审查对象）：`43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`（`fix(httpapi): 让 Provider 自身超时统一映射为 provider_failure`）。
- C2（Coder 的 Review Request metadata commit）：`0132b31`（只改 `state.yaml` 的 `review.*`），不作为审查对象。
- 相关变更范围：`git diff 0977f24 43ce197`，完整路径集合：
  - 生产代码：`httpapi/errors.go`（唯一生产文件，只改 `errorCodeOf` 及其注释）；
  - 测试：`httpapi/errors_test.go`（仅注释）、`httpapi/fake_test.go`（测试替身与辅助）、`httpapi/handler_test.go`（新增用例）；
  - Design：`docs/design/http-api.md` §4（Analyst 交付物，commit `aa2f6e2`，不属于 Coder commit）；
  - 任务 Artifact：`.agent/tasks/provider-timeout-classification-v1/*`（TaskBuilder / Analyst 所有）。
- 任务前已有修改的区分方式：任务基线为 `0977f24`，其上 8 个 commit 依次为 TaskBuilder（`94a9c53`、`53248a4`）、Analyst（`f107fa8`、`100743a`、`34b1505`、`aa2f6e2`、`89ed3c4`）、Coder（`43ce197`、`0132b31`）；Coder 自己的 commit `43ce197` 只含 `httpapi/errors.go` + 三个 `httpapi/*_test.go`。
- 审查时仓库状态：分支 `fix/provider-timeout-classification-v1`，工作区 clean，local HEAD == `origin/fix/provider-timeout-classification-v1` == `0132b31`。
- 关键配置/版本：根模块 Go 1.24.1（`go.mod` go 1.24）、零第三方依赖；无 migration / Registry 变更；`state.resources.reservations` 为空。

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
|---|---|---|
| AC-001 | PASS | `httpapi/handler_test.go:703` `TestProviderFailureClassificationEndToEnd`（子用例「客户端自身超时（链含 context.DeadlineExceeded）」用真实 `openai.Client`（`Timeout=50ms`）+ 500ms 慢 `httptest` 上游 + 真实 `session.Manager` + 真实 `service.Runner` + 真实 `agent.Loop`）：HTTP `502` + `error.code=provider_failure`（`decodeError` 断言状态码与 code）、`run_finished.status=FAILED`、`error_kind=provider_failure`、`RunOutcome.Status=FAILED` + `KindOf(Err)=provider_failure`，并用 `errors.Is(outcome.Err, context.DeadlineExceeded)==true` 证明该构造的错误链确实含 ctx 哨兵（测试不是空转）。另有「链中无 ctx 哨兵」「上游 429」「上游 500」三个同族子用例。独立 Mutation（回退旧优先级）使该子用例失败（504 而非 502），见 Verification。 |
| AC-002 | PASS | `httpapi/handler_test.go:784` `TestRequestTimeoutClassificationEndToEnd` 两个子用例：(a) Provider 已观察到 ctx 到期 → `504` + `deadline_exceeded`，`run_finished` 为 `CANCELLED` + `deadline_exceeded`；(b) 排队等待会话执行权期间到期（Provider 未发起调用，`provider.callCount()==0`）→ `504` + `deadline_exceeded`。两例均断言会话逐字段未变。独立 Mutation（session `contextError` 把 deadline 判成 cancelled；agent `classifyProviderError` 忽略 ctx 终态）分别使 (b)/(a) 失败，证明该用例能区分两种超时串味。 |
| AC-003 | PASS | `httpapi/errors_test.go:113` `TestErrorCodeOf` 与 `httpapi/handler_test.go:170` `TestChatErrorMapping` 表驱动覆盖：`provider_failure` + `Cause=DeadlineExceeded`/`Canceled`/多层包装 → `502`；`deadline_exceeded`/`cancelled` 终态（含 `Cause=DeadlineExceeded` 交叉）→ `504`/`408`；会话哨兵包在 `RunError` 内 → `404`；`invalid_argument` 携带 ctx 超时 → `internal`；裸包装 ctx 超时/取消 → `504`/`408`；矛盾结论五类（非终态、终态缺错误、`COMPLETED` 带错、`Status` 与 `StatusOf` 不符、负计数）→ `internal` 非 2xx。判定顺序见 `httpapi/errors.go:166-212`（会话哨兵 → 链中最外层 `*core.RunError.Kind` → 裸 ctx 兜底 → `internal`）。 |
| AC-004 | PASS | 直接 ctx 超时/取消：`TestErrorCodeOf`、`TestChatErrorMapping`（4 个子用例）通过；客户端断开：`httpapi/lifecycle_test.go:234` `TestClientDisconnectCancelsRun` 通过；Provider 429/500 → `502 provider_failure`：`TestProviderFailureClassificationEndToEnd` 子用例；会话三类哨兵与「预检不执行 Run、零写入」：`TestChatSessionPrecheck`、`TestChatRunnerSessionSentinel` 通过；`checkedCode` 矛盾拒绝、工具失败/重复 `CallID`、LLM/工具预算：`TestChatErrorMapping` 既有子用例通过；`finish_reason` 三类异常 → `502 provider_failure`：`TestProviderFinishSemanticsMappingEndToEnd`（新增，含 `run_finished` 与会话快照断言）。全量 `go test ./...` 与 `-race` 均通过。 |
| AC-005 | PASS | `httpapi/fake_test.go:325` `assertSessionStateEqual` 用 `Manager.Get` 快照对 `Messages`（逐条 `reflect.DeepEqual`）、`Summary`、`Todos` 比对并跑 `core.ValidateMessages`；基线由 `timeoutBaseline()`（历史 + 摘要 + Todo）+ `seedSessionState`（`Create`+`Save`）预置。该断言在 AC-001 的 4 个子用例、AC-002 的 2 个子用例、AC-003 的 3 个结束语义子用例中全部执行并通过。 |
| AC-006 | PASS | 反复执行 `go test -count=8 -run 'TestProviderFailure…|TestRequestTimeout…|TestProviderFinishSemantics…' ./httpapi/` 与 `-race -count=3` 均通过（无 flaky 迹象）；全程 `httptest` + 测试替身 + loopback 慢上游，无外网、无付费模型。Cleaner 在隔离副本 `git archive 43ce197` → `/tmp/cleaner-mut` 独立执行 4 组可逆 Mutation，均按预期使新增/既有用例失败，副本已删除；仓库工作区自始至终 clean，无 Mutation 残留。 |
| AC-007 | PASS | Coder commit `43ce197` 路径集合只有 `httpapi/errors.go` + `httpapi/errors_test.go` + `httpapi/fake_test.go` + `httpapi/handler_test.go`，落在 Contract Allowed 清单内；`checkedCode` 校验集合未变（`httpapi/run.go` 零改动）；`agent/**`、`llm/**`、`core/**`、`session/**`、`service/**`、`cmd/**`、`runlog/**`、`.agent/engine/**`、`.agent/workflow.yaml`、`.agent/registry/**` 零改动；`ErrorCode` 枚举与 `errorStatuses` 映射未变；无 `e2e/` 或其他任务文件混入。Design `docs/design/http-api.md` §4 只新增判定优先级列表与三条事实，映射表、枚举、协议、状态机未动，与 APPROVED Contract D1/D3 及最终实现一致（Analyst commit `aa2f6e2` 是 C1 的祖先，不属于 Coder 变更）。 |

## Verification

| Check | Result | Evidence / Reason |
|---|---|---|
| `go build ./...` | PASS | exit 0（Go 1.24.1 linux/amd64） |
| `go vet ./...` | PASS | exit 0 |
| `go test -count=1 ./...` | PASS | 12 个包全 ok（`httpapi` 1.796s） |
| `go test -race -count=1 ./...` | PASS | 12 个包全 ok，无 data race 报告 |
| `make test` | PASS | exit 0（等价 `go test ./...`） |
| 新增用例重复稳定性 | PASS | `-count=8`（默认）与 `-race -count=3`（含 `TestChatErrorMapping`/`TestErrorCodeOf`）全通过，未观察到毫秒级超时用例抖动 |
| Mutation A：在 `errors.As` 分支前恢复旧优先级（ctx 哨兵先于 `Kind`） | PASS（如期失败） | 隔离副本中 `TestChatErrorMapping/{provider 失败且 Cause 为 ctx 超时, provider 失败且 Cause 为 ctx 取消, provider 失败且 Cause 为多层包装的 ctx 哨兵, 取消终态且 Cause 为 ctx 超时, invalid_argument 携带 ctx 超时}` 与 `TestProviderFailureClassificationEndToEnd/客户端自身超时（链含 context.DeadlineExceeded）` 失败 |
| Mutation B：删除裸 ctx 兜底分支 | PASS（如期失败） | `TestErrorCodeOf/{ctx 取消, ctx 超时}` 与 `TestChatErrorMapping/{Runner 返回包装的 ctx 超时/取消, Runner 返回 ctx 超时/取消}` 失败，证明 AC-004 直接 ctx 路径受保护 |
| Mutation C：session `contextError` 把 deadline 判成 `cancelled` | PASS（如期失败） | `TestRequestTimeoutClassificationEndToEnd/等待会话执行权期间到期（尚未发起模型调用）` 得到 `408 cancelled`（期望 504） |
| Mutation D：agent `classifyProviderError` 忽略 ctx 终态 | PASS（如期失败） | `TestRequestTimeoutClassificationEndToEnd/Provider 已观察到请求 ctx 到期` 得到 `502 provider_failure`（期望 504） |
| 隔离方式与残留 | PASS | Mutation 只在 `git archive 43ce197` 解出的 `/tmp/cleaner-mut` 中执行，未触碰 Review Target working tree，未 commit，结束后 `rm -rf` 删除；仓库 `git status --short --untracked-files=all` 为空 |
| `.agent/engine` 未受影响 | PASS | `git diff --name-status 0977f24 43ce197` 不含 `.agent/engine`，未重跑 Engine 测试 |
| 全局资源一致性 | PASS | Contract 声明无 Shared Resource（不新增错误码）；`state.resources.reservations` 为空；`.agent/workflow.yaml` `resources: {}`、无 `.agent/registry/`；实现未引入新的错误码/迁移版本 → 三边无漂移 |

## Findings

No actionable findings.

说明（不构成 Finding 的残余风险，供 Owner 参考）：

- 默认生产装配未设置 Provider `Timeout`（Owner D4 保持不变），因此「Provider 自身超时且链含 ctx 哨兵」在默认配置下的可达途径有限；本次修复纠正的是分类规则，可达性变化属后续独立任务。
- 直接错误路径若收到链中无 `*core.RunError` 的裸 `*url.Error`（当前装配不产生），其 ctx 哨兵仍会映射为 `504`；Contract `Open Risks` 已显式接受该形状。
- `KindInvalidArgument` 携带 ctx `Cause` 时由 `504` 变为 `internal`，是 Owner D1「结论优先」的直接推论，本次已由 `TestChatErrorMapping/invalid_argument 携带 ctx 超时` 固化。
