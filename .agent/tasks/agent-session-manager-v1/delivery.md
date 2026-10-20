# Delivery Verification

## Milestone and Target

- Milestone：M2 Runtime 会话层——单进程内存 Session Manager（`agent-session-manager-v1`，Task 未声明独立 Milestone 字段，本里程碑由 Owner 启用 Deliverer 明确）。
- Delivery Target：feature snapshot `0750cb2f2a27a42bfd02bf878d5ce5d420d4590d`（`feat/agent-session-manager-v1`，本地 HEAD == `origin/feat/agent-session-manager-v1`）。
- Cleaner Review Target：`f23082ad0c25a65317cffe27ca91982dd437ed3f`（C1）。
- Target Match：YES。C1 为 feature_head 祖先；`C1..feature_head` 仅含 `.agent/tasks/agent-session-manager-v1/` 下 4 个 review-neutral artifact 文件（core-logic.md / findings.md / owner-decision.md / state.yaml），`session/**` 与 `docs/design/**` 零变更，CLEAN 对实际验证代码仍有效。

## Environment

- OS：Debian GNU/Linux 12 (bookworm)，linux/amd64。
- 语言运行时：go1.24.1 linux/amd64（race detector 可用，与 Task 环境要求一致）。
- 构建工具：GNU Make 4.3。
- 外部依赖（MySQL / Redis / Kafka / Docker / 网络）：不需要，Task 明确声明；本次未使用。
- 集成环境：临时 detached worktree `/tmp/deliverer-verify-t04` @ `0750cb2`（`develop_base` `6902d2445665ec356a2db22b33b7749d39d8018e` 为其祖先，等价于验证 `develop_base + feature_head`）；未创建/推送任何永久 integration branch，未 push develop，未 merge；验证后已删除。
- 独立 Smoke 环境：临时外部模块 `/tmp/deliverer-smoke-t04`（`replace` 指向上述 worktree，模拟 T01 装配层等外部消费方）；验证后已删除。
- 配置来源：`ManagerConfig{TTL, Now}`；TTL 零值关闭、过期测试用注入可控时钟（无 sleep 证据）；无 Secret、无未说明配置、无本机绝对路径依赖。
- 测试数据与隔离：全部为进程内临时会话（s1—s5 / t1 / c-a / c-b），不触碰任何真实数据；临时目录随验收结束清理。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build（`go build ./...`） | PASS | exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `go test ./...` | PASS | core / llm / runlog / session 全 ok |
| `make test`（CI `pr-check` 同入口） | PASS | exit 0 |
| `go test -race -count=5 ./...` | PASS | 全包 ok，无 data race 报告 |
| session 长期保留测试（`go test -race -v ./session/`） | PASS | 31 个具名测试全部 PASS（并发/取消/过期/非法拒绝/隔离/Store 适配/panic） |
| 独立外部 Smoke（`go run -race`，外部模块） | PASS | 主链路 A—G 全部 PASS，无 race 报告 |
| `GOWORK=off go -C .agent/engine test ./...` | PASS | Engine 独立 module 不受影响 |
| `go list -m all` | PASS | 仅根 module，零第三方依赖 |
| session import 核对（INV-009 分层） | PASS | 仅标准库 + `<module>/core` |
| Design §12 所有权表机械核对 | PASS | `git ls-files session/` 9 个文件全部在表内且单归属；Design 无「不实现任何存储」残留 |

## Acceptance Evidence

- AC-001 → Smoke A（本次运行）：Create → RunTurn（user + assistant 带 ToolCall + tool Result + SetSummary + PutTodo）→ Get 逐字段断言（条数/顺序/ID/内容/摘要/Todo 状态）一致；改写返回值后再次读取不受污染（深拷贝隔离成立）。
- AC-002 → Smoke B（本次运行）：双会话数据互不相交；Delete s1 后 s2 消息/摘要/Todo 逐字段不变；删除后 Get → `KindOf=invalid_argument` 且 `errors.Is(ErrSessionNotFound)`；重复 Create → `errors.Is(ErrSessionExists)`。
- AC-003 → Smoke E（本次运行）：8 个 goroutine 各一次 RunTurn 写入 3 条唯一消息：总数 = 24、无重复 ID、按组前缀收集索引后严格连续（无跨组交错）。
- AC-004 → Smoke E（本次运行）：c-a / c-b 两个 Turn 在回调内经 channel barrier 互证「已进入执行」并相遇；5s 超时仅作卡死保护，不用作性能判定。
- AC-005 → Smoke F（本次运行）：注入时钟越过 TTL 期间 `PurgeExpired` 跳过在途会话（purged=0）、在途 Get 不阻塞；在途 Turn 正常完成、提交生效并刷新 TTL（提交后立即可读）；再次越期且无在途时 Get → `ErrSessionExpired` 且物理删除（再次 Get → `ErrSessionNotFound`）。失败/取消 Turn 不刷新 TTL 由本次实跑的 `TestFailedTurnDoesNotRefreshTTL` / `TestCancelledTurnDoesNotRefreshTTL`（-race PASS）覆盖。
- AC-006 → Smoke D（本次运行）：(a) 排队请求 ctx 取消 → `cancelled`、`atomic.Bool` 证明回调未执行、历史零写入；(b) 执行中取消 → 整轮丢弃、历史 JSON 指纹不变；(c) 取消后新 Turn 正常取得执行权并提交（执行权无泄漏）。
- AC-007 → Smoke C（本次运行）：孤儿 tool Result → `invalid_pairing` 且 `CallID=call-nonexistent`；写入前后历史 JSON 指纹一致（原子拒绝）。
- AC-008 → 机械核对（本次运行）：`git ls-files session/` 与 Design §12 逐条一致、9 文件单归属 `session`；Design 无冲突残留陈述。完整 Task ↔ Contract ↔ Design ↔ 实现四方核对以 Cleaner `findings.md`（CLEAN）为输入，本次未重做 Diff Review。
- 通用 → 构建 / vet / 全量测试 / make test / `-race -count=5` / Engine 独立测试 / 依赖清单 / import 分层均为本次实跑通过。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| Cleaner 变异测试（M1—M4）复跑 | Cleaner 职责，非 Deliverer 范围；结论以其 findings.md 为输入 | 低：CLEAN 有效且 review target 与实际验证版本一致 |
| 完整 Diff Review | Cleaner 职责 | 低：同上 |
| 服务启动 / 健康检查 / HTTP Smoke | 本任务为进程内库模块，无服务进程与对外 API（Task Out of Scope） | 无 |
| MySQL / Redis / MQ / Docker 真实交互 | Task 明确不需要外部依赖 | 无 |
| 重启 / 恢复 / 回滚 / 性能基准 | Task 与 Contract 均未要求（持久化、重启恢复与基准为 Out of Scope） | 无 |

## Remaining Risks

- `TodoOperations` 与 T03 的集成验证、CI 纳入 `-race`、长会话每批全量校验的 O(n²) 为 Owner 已批露的非阻塞风险，按原任务归属处理，不影响本里程碑结论。
- 内存实现进程退出即丢失数据（Contract 已声明，持久化属后续任务）。

## Result

PASS
