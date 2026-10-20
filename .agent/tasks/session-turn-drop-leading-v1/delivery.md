# Delivery Verification

## Milestone and Target

- Milestone：`session-turn-drop-leading-v1` 能力交付（`Turn.DropLeading` 可被 T07 装配消费的公共接口事实）
- Delivery Target：`feat/session-turn-drop-leading-v1` 的 `b19699b0bc2935cc4148849be676f3bef1fa3a9a`
- Cleaner Review Target：`2ed50f1c225323dcf392aaea403758576513dcaf`（C1）
- Target Match：YES
  - `git diff 2ed50f1..b19699b -- session core contextmgr docs go.mod Makefile` 为空（C1 之后仅任务产物 `state.yaml` / `findings.md` / `core-logic.md` / `owner-decision.md` 变化）
  - `session/turn.go` 在两个 commit 的 blob sha256 相同：`44690c8dce6fc11c86ceb92e807a8356ae14a81a3b66c97ce2b87288f59fe43a`
- 参与验证的三个 SHA：`review_target=2ed50f1c225323dcf392aaea403758576513dcaf`、`feature_head=b19699b0bc2935cc4148849be676f3bef1fa3a9a`、`develop_base=dc734d20a732dcc7fc53dc2313c15dd003e919f9`（与验证时 `origin/develop` 相同；`develop` 是 `feature_head` 的祖先，无需要合并的 develop 新增提交）

## Environment

- OS：Linux（容器环境），验证 shell 为 Bash
- 运行时：`go version go1.24.1 linux/amd64`（race detector 可用）；`GNU Make 4.3`
- 依赖：根模块零第三方依赖（`go list -m all` 仅 `cnb.cool/framework-station/mini-agent-go`）；本任务不需要 MySQL / Redis / Kafka / Docker
- 网络：全部命令以 `GOPROXY=off` 执行，机械证明不依赖网络与真实 LLM API
- 验证工作区：临时 detached worktree `/tmp/deliverer-verify` @ `b19699b`，创建时 `git status --short` 为空；验证结束后已 `git worktree remove`，主工作区未被改动
- 配置来源：无外部配置、无 Secret、无环境变量开关
- 测试数据与隔离：进程内临时 `Manager`，会话 ID 为一次性字面量；验证后随 worktree 删除，不残留数据

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | 在 `/tmp/deliverer-verify`（@`b19699b`，`GOPROXY=off`）：`go build ./...` exit 0；`go vet ./...` exit 0 |
| No new dependency | PASS | `go list -m all` 仅输出根 module |
| Unit tests（全仓） | PASS | `go test -count=1 ./...` 8 个包全部 `ok`（builtin / contextmgr / core / llm / llm/openai / runlog / session / tool） |
| Race tests | PASS | `go test -race -count=5 ./...` 8 个包全部 `ok`，无 race 报告（session 2.05s） |
| `make test` | PASS | exit 0，8 个包 `ok` |
| Workflow Engine（独立 module） | PASS | `GOWORK=off go -C .agent/engine test ./...` → `cmd/workflow-check`、`internal/workflow` 全 `ok` |
| Core Flow：Turn 内原子提交（独立 smoke） | PASS | Deliverer 自写黑盒 smoke（仅用 `Manager.Create/Save/RunTurn/Get` 公共 API，置于临时 worktree，未进入交付物）：Seed 6 条已提交历史（system / user / assistant(ToolCall `call_A`) / tool / assistant / user）→ 同一 `RunTurn` 内 `DropLeading(2)` + `SetSummary("compressed:v1")` + `Append(m6,m7)` → `Get` 得到 ID 序列 `m2,m3,m4,m5,m6,m7`、摘要 `compressed:v1`，`call_A` 配对内容未被改写；`core.ValidateMessages` 对读回历史返回 nil |
| Data（最终快照一致性） | PASS | 连续两次 `Get` 的整份 `Session` `reflect.DeepEqual` 相等（快照未被共享可变底层破坏）；`n=0` 提交后历史保持 `z0,z1,z2`；`n=len(base)` 提交后历史长度为 0 |
| 非法输入原子拒绝 | PASS | 同一 Turn 内 `n=-1`、`n=5`（len(base)=4）均返回 `invalid_argument`，拒绝后 `Turn.Session()` 与调用前逐字段相等；随后 `DropLeading(2)` 在同一 Turn 内成功提交为 `r2,r3`（+ `r4` 追加），摘要保持 `base-sum` |
| 配对边界拒绝 | PASS | `DropLeading(3)`（边界落在 `tool(call_B)` 之前）返回 `invalid_pairing`，`*core.RunError.CallID == "call_B"`；拒绝后视图仍为 `r0..r3`，Turn 未关闭 |
| 整轮回滚（error / cancel / timeout） | PASS | 三条路径（回调返回 `errors.New` → `internal`；`cancel()` → `cancelled`；30ms 超时 → `deadline_exceeded`）均已暂存 `DropLeading(2)` + `SetSummary` + `Append`，`Get` 的整份 `Session`（历史 / 摘要 / Todo）与调用前 `reflect.DeepEqual` 相等 |
| 关闭后写入拒绝 | PASS | 回调返回后持有 `*Turn` 调 `DropLeading(0)` → `invalid_argument` 且文本含 `Turn 已结束`，会话状态不变 |
| Turn 内并发写 | PASS | 同一 Turn 内 6×`DropLeading(1)` + 6×`Append` + 6×(`SetSummary`/`Session()`) 并发，`sync.WaitGroup` 同步、10s 超时仅作卡死保护：无 panic / 死锁，`-race` 下无报告；`drops=6 appends=6`，最终条数 `6-6+6=6` 且与基线后缀逐条对账，提交后 `core.ValidateMessages` 通过 |
| Design ↔ 实现抽查（AC-007 运行侧） | PASS | `docs/design/runtime-architecture.md:128-129` 已把 Turn 写入能力描述为含 `DropLeading` 前缀删除并固化计数/拒绝语义；`INV-017`（`:215`）与 §12 `session/turn.go` 单归属登记（`:250`）均存在 |
| Service startup / 健康检查 | NOT_APPLICABLE | 根模块为库，无 `main` 包（仓库唯一 `main.go` 属 `.agent/engine` 只读 Gate 工具）；本任务 Out of Scope 明确不含 CLI / HTTP / Docker |
| MySQL / Redis / MQ / Docker 真实交互 | NOT_APPLICABLE | 交付物为单进程内存 Session 能力，Contract 明确无外部依赖；本任务不涉及持久化 |

## Acceptance Evidence

- AC-001（提交后历史 = 原历史 `n` 之后 + 本次新增，条数 `m-n+k`）→ 独立 smoke：`Get` 读到 `m2,m3,m4,m5,m6,m7`（m=6,n=2,k=2，总 6 条），逐条 ID/顺序/内容一致；`n=0` 空操作；`n=len(base)` 清空为长度 0。
- AC-002（负数 / 超过已提交历史长度 → `invalid_argument` 且状态不变）→ `n=-1`、`n=5`（base 4 条）均 `invalid_argument`，视图逐字段不变，同 Turn 换 `n=2` 仍成功提交。
- AC-003（配对边界拒绝 + 定位 CallID + 原子）→ 孤儿 Tool Result 边界返回 `invalid_pairing`、`CallID=call_B`，历史与暂存区不变，Turn 未失效。
- AC-004（`DropLeading`+`SetSummary`+`Append` 同提交；失败三者全回滚）→ 成功路径一次 `Get` 同时核对历史与摘要；失败路径整份 `Session` 不变。
- AC-005（error / cancel / timeout 整轮丢弃；关闭后拒绝）→ 三种失败路径 `internal` / `cancelled` / `deadline_exceeded` 且 `Session` 完全不变；关闭后 `DropLeading` 返回 `invalid_argument`。
- AC-006（并发无 race / 无死锁 / 按成功数结算）→ 并发 smoke 在 `-race` 下通过，`drops=6 appends=6` → 6 条；全仓 `go test -race -count=5 ./...` 全绿。
- AC-007（四方核对）→ Cleaner 已给出 PASS；Deliverer 本次独立抽查 Design 三处（§8.1 写入能力、INV-017、§12 所有权）与实现一致，未做全量 Diff Review。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| HTTP / API Smoke | 本任务无 HTTP 入口（Out of Scope） | 无：交付物是库内方法，公共 API 已由 `RunTurn` + `Get` 黑盒 smoke 覆盖 |
| 服务启动 / 健康检查 / 配置加载 | 根模块无 `main` 包，非可运行服务 | 无：装配与启动属 T07 任务 |
| Docker / 容器化交付 | 本任务不涉及 | 无 |
| 真实 MySQL / Redis / MQ 交互 | Contract 明确无持久化与外部依赖 | 无 |
| 性能 / 压测 / 重启恢复 / 回滚演练 | Task 与 Contract 均未要求（内存态、重启恢复属后续任务） | 无：不得据此推断已具备持久化或恢复能力 |

## Remaining Risks

- CLEAN-001（P3，OPEN）：`TestTurnDropLeadingRejectsInvalidPairingBoundary` 第三子用例无法区分「仅校验剩余历史」与「校验剩余历史 + 暂存消息」（M5 等价变异）。仅影响测试可读性，Owner 已决定本轮不修复、不阻塞接受；本次运行未发现对应业务缺陷。
- `n == len(base)` 且未追加消息时，提交后的 `Session.Messages` 为长度 0（非 nil）；T07 必须自行处理空历史视图，不能假设至少一条消息。
- `-race` 未进入 CI（`.cnb.yml` 仅 `make test`），并发证据来自本地显式执行；后续改动需重跑。
- `DropLeading` 不返回实际删除条数；T07 若需要该信息需另起 Contract Revision。
- 本任务为进程内内存态：交付 PASS 不代表具备持久化、重启恢复或跨进程一致性。

## Result

PASS
