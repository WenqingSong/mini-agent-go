# Delivery Verification

## Milestone and Target

- Milestone：`M1-runtime-core-contracts`（Runtime 核心契约与基础工程结构 v1）
- Delivery Target：`feature/runtime-core-contracts-v1` @ `f152f338c41cc21bf94f52efaa27918865da5da5`（实际参与验证的 feature snapshot）
- Cleaner Review Target：`b4b95733ff413a1f21148e763eb9062b824f78be`
- develop_base：`origin/develop` = `ebf87788d93fce6b260627295a440e9ae252d608`
- Target Match：YES（`git diff --stat b4b9573 f152f33 -- ':!.agent/tasks'` 为空；CLEAN 之后仅 3 个 review-neutral metadata commit，源码与 Cleaner 审查对象逐字节一致）
- Entry Gate：`.agent/bin/workflow-check gate delivery-start .agent/tasks/runtime-core-contracts-v1` → `PASS`（exit 0）

## Environment

- OS：Linux 5.4.241-1.tlinux4-0025.13（x86_64）
- 语言运行时：`go1.24.1 linux/amd64`（与 `.cnb.yml` 的 `golang:1.24` 镜像主版本一致）
- 构建工具：`GNU Make 4.3`
- 依赖服务：无。本里程碑无 MySQL / Redis / Kafka / Docker / HTTP Server / 可运行二进制（Contract §8 明确不交付 Agent Loop、Provider、Tool 实现、持久化与 CLI/HTTP）。
- 交付物标识：`f152f33`（Git commit），以 `git archive f152f33` 导出到 `/tmp/dlv-rtc-v1/src` 的**无 `.git`、无未跟踪文件**的干净检出作为验证对象
- 独立消费方探针：`/tmp/dlv-rtc-v1/probe`（Deliverer 自写的临时 module，`replace` 指向上述干净检出；探针不在仓库内，验证后已移除）
- 配置与 Secret：无。无需任何环境变量；`GOPROXY=off` 下仍可构建，说明无网络/私有依赖
- 测试数据与隔离：全部为内存态契约表驱动用例，无持久化数据，无需清理；未接触任何真实服务或生产环境

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（干净检出 `go build ./...`） | PASS | `/tmp/dlv-rtc-v1/src` exit 0 |
| Vet（干净检出 `go vet ./...`） | PASS | exit 0 |
| Unit/Contract Test（`go test -count=1 ./...`） | PASS | `core` ok、`llm` ok、`runlog` ok；`tool`/`session`/`contextmgr` 为纯接口声明，无测试文件 |
| Race Test（`go test -count=1 -race ./...`） | PASS | 全 ok，无 data race 报告 |
| 重复稳定性（`go test -count=3 ./...`） | PASS | exit 0，无用例抖动 |
| `make -n test` 非空且与 CI 一致 | PASS | 展开为 `go test ./...`；`.cnb.yml` `pull_request` 的 `pr-check` 为 `script: make test`，入口一致 |
| `make test`（干净检出，退出码 0） | PASS | 真实测试输出 `ok .../core`、`ok .../llm`、`ok .../runlog` |
| `make build` / `make vet` / `make test-engine` | PASS | 三个目标均 exit 0 |
| 依赖方向（AC-001 / INV-001） | PASS | `go list -f '{{.ImportPath}} => {{join .Imports " "}}' ./...`：`core => encoding/json errors fmt strings`；`contextmgr/llm/runlog/session/tool => <module>/core` + 标准库；L1 之间零 import，无反向依赖，无 import cycle |
| 根模块不吞 Engine（独立 module） | PASS | `go list ./...` 仅 6 个根包；`GOWORK=off go -C .agent/engine test ./...` → `workflow-v2-engine/cmd/workflow-check` ok、`internal/workflow` ok |
| 零第三方依赖 / 无 `go.work` / 无 `replace` | PASS | `go list -m all` 仅根 module；`go.mod` 无 `require`/`replace`；无 `go.work`；`GOPROXY=off GOFLAGS=-mod=readonly go build ./...` exit 0 |
| AC-002 独立探针（0/1/3 ToolCall 往返 + 三类非法配对） | PASS | Deliverer 自写探针 `TestProbeToolCallCardinalityRoundTrip`（顺序/ID/Arguments 原始字节逐项一致）、`TestProbePairingRejects`（缺失 Result→CallID `c2`、重复 Result→`c1`、孤儿 Result→`zz`，均 `KindInvalidPairing` 且 `ValidateMessages` 同样拒绝） |
| AC-003 独立探针（状态×错误映射、双预算、四结果可区分） | PASS | `TestProbeStatusOfMapping`（nil→COMPLETED；cancelled/deadline→CANCELLED；llm/tool limit→LIMIT_EXCEEDED；provider/tool_failure/invalid_pairing/非 RunError→FAILED）、`TestProbeRunResultValidate`（17 组正反用例：COMPLETED 带错必失败、LIMIT+tool_failure 必失败、FAILED+tool_failure 缺 CallID 必失败、CANCELLED+cancelled/deadline 必通过）、`TestProbeRunPolicy`（0/负值→invalid_argument） |
| INV-005 签名核对（独立） | PASS | 探针 `TestProbeToolRequestShape` 用 `reflect` 断言 `tool.Request` 仅 2 个字段（`core.ExecutionRef` + `core.ToolCall`），无 `session.*`/可变 Todo/Store；`session.Session` 同时持有 `Messages` 与 `Todos` |
| `runlog.Discard` 无副作用 / `llm.Response.Validate` 边界 | PASS | 探针 `TestProbeDiscardAndLLMValidate`：`Record` 返回 nil；非 assistant 角色、未知 FinishReason、负 token 用量均被拒绝 |
| AC-006 所有权表 ↔ 实际文件 | PASS | 用脚本截取 `docs/design/runtime-architecture.md` §12 表格 17 行与 `git ls-files`（排除 `.agent/`、`docs/`、`AGENTS.md`、`.cnb.yml`）17 条双向 `comm` 比对，两侧差集均为空；每个源文件只归属一个模块，`go.mod`/`Makefile` 标注为工程公共文件 |
| 未依赖本地未跟踪文件 | PASS | 验证对象是 `git archive` 导出目录（无 `.git`、无 `.agent/bin`、无本机缓存文件），`make test` 仍 exit 0 |
| Contract 未在 A1 后漂移 | PASS | `git diff 6d7aebe -- .agent/tasks/.../contract.md` 为空（与 Gate `contractValid` 一致） |
| 覆盖率参考（非门禁） | 参考 | `core` 94.0%、`llm` 100.0%、`runlog` 100.0%（仅作可信度参考，不构成 AC 判定） |

## Acceptance Evidence

- **AC-001（构建/静态检查/依赖方向）**：干净检出 `go build ./...`、`go vet ./...`、`go test ./...` 均 exit 0；`go list` 依赖输出证明 `core` 仅依赖标准库、五个 L1 包只依赖 `<module>/core`，无反向依赖与 import cycle（本次独立执行）。
- **AC-002（消息与配对）**：assistant 消息 0/1/3 个 ToolCall 的 JSON 往返无损（顺序、ID、Arguments 逐项一致）；缺失 Result、重复 Result、孤儿 Result 三类均返回 `*core.RunError{Kind: KindInvalidPairing}` 并带精确 `CallID`，不静默接受（本次独立探针，非引用仓库测试）。
- **AC-003（六项语义可执行 + 四结果映射）**：Session/Todo 归属（`session.Session.Messages/Todos` + `Store`）、工具身份显式传递（`tool.Request` 仅 `ExecutionRef`+`Call`，reflect 断言）、双预算（`RunPolicy.Validate` 正反用例）、取消/超时（`KindCancelled`/`KindDeadlineExceeded`→`CANCELLED`）、工具失败（`FAILED+tool_failure` 必须带 CallID）、消息配对（`ValidateToolPairing`）均有可执行定义；四种结果 `(Status, ErrorKind)` 映射经 17 组正反用例独立固化。
- **AC-004（Owner APPROVED + 四方一致）**：`state.contract.status=APPROVED`、`contract.target=6d7aebe`；`state.owner.status=ACCEPTED` 且 `owner.review_target == review.target == b4b9573`；本次验证源码与该 review target 逐字节一致，Gate `delivery-start` 机械 PASS。四方一致性的实质比对由 Cleaner 完成（`findings.md`），Deliverer 只复核版本一致性与可运行性。
- **AC-005（`make test` 真实运行且与 CI 一致）**：干净检出 `make -n test` 非空（`go test ./...`）、`make test` exit 0 并输出真实测试结果；`.cnb.yml` `pr-check` 同为 `make test`；根模块 `go build`/`go vet` 通过；`.agent/engine` 独立 module 测试单独通过且未被根模块吞掉。
- **AC-006（文件所有权）**：Design §12 表格 17 条与 `git ls-files` 17 条双向零差集，每个源文件单归属，工程公共文件已标注。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 服务启动 / 健康检查 / 配置加载 | Contract §8 明确 T00 不交付可运行二进制、CLI 或 HTTP Server，不存在启动入口 | 低：本里程碑交付物就是契约层，启动链路属后续任务；未验证项不会被当作已验证 |
| HTTP API Smoke 主链路 | 同上，无 HTTP 接口 | 低 |
| MySQL / Redis / Kafka 真实交互 | Contract「数据约束」明确本任务无数据库、无 Redis、无 MQ、无 migration，唯一状态抽象 `session.Store` 不实现 | 低：无外部依赖即无交互风险 |
| Agent Loop 端到端（多轮对话、并发工具调度、工具失败回填顺序） | Out of Scope；Contract `Delegated Verification` 明确归属后续 Agent Loop 任务 | 中：INV-006/INV-007/INV-008 至今只有签名与契约级证据，需在 Loop 任务中补运行期验证 |
| 真实 Provider / 真实 Tool 调用 | Out of Scope，无任何实现 | 中：Provider 适配成本与 `Raw` 回放行为未经验证 |
| Session 持久化运行期行为（INV-005 运行期证据） | `session.Store` 仅接口，无实现 | 中：归属 Session 持久化任务 |
| 性能 / 基准 / 并发压测 | 非本任务 AC，纯契约层无性能目标 | 低 |
| 重启、恢复、回滚演练 | 无状态、无部署、无 migration，无演练对象 | 低 |
| 外部 `go get` 可用性（module path） | Owner 决定本轮不验证（Contract Open Risks） | 中：`cnb.cool/framework-station/mini-agent-go` 一旦被外部消费方引用即难更改 |

## Remaining Risks

- `INV-006`（取消/超时的实际中断）、`INV-007`（工具失败可恢复性分类与回填顺序）、`INV-008`（runlog 旁路在真实运行时成立）、`INV-005`（会话归属运行期证据）本里程碑只有签名/映射/契约测试证据，已由 Contract `Delegated Verification` 显式归属后续任务；本次 PASS 不覆盖它们。
- `RunStatus` 无独立 `TOOL_FAILED`，工具失败终态靠 `(FAILED, tool_failure)` 组合表达，只读 Status 的消费方必须同时读取 `ErrorKind`。
- `contextmgr.Request.TokenBudget` 缺乏 token 估算实现，字段语义待 Context 任务落实，存在接口 Revision 可能。
- module path 的外部 `go get` 可用性未验证（Owner 已接受为本轮未验证事项）。
- 两项 P3 Finding 仍 OPEN 且 Owner 未表态：`CLEAN-001`（`Role`/`ContentType`/`FinishReason`/`TodoStatus`/`EventKind` 字面量未导出，消费者只能字符串强转）、`CLEAN-002`（tool 消息缺 `ToolResult` 经 `ValidateMessages` 报 `invalid_argument`、经 `ValidateToolPairing` 报 `invalid_pairing`）。二者不影响当前终态与 Run 行为，但会影响后续并行任务的可维护性与错误分类精度。
- 依赖方向检查（AC-001 的 `go list` 机械核对）未进入 CI（`.cnb.yml` 属本任务 Out of Scope），当前依赖每次人工/Agent 执行。

## Result

PASS
