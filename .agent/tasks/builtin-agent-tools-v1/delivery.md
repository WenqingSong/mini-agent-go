# Delivery Verification

## Milestone and Target

- Milestone：`builtin-agent-tools-v1`（T03 内置 Agent 工具：calculator / mock_search / todo_create / todo_list / todo_complete）。
- Delivery Target：`feat/builtin-agent-tools-v1` HEAD = `16554ef704f5182b62b92e80fbe6c3b69dd2ae86`（Owner ACCEPT 决策提交，含实现证据提交 C1）。
- Cleaner Review Target：`92068976d73d29384c9227fc90d648fb3ddb3261`（C1）。
- Target Match：YES（C1 为 feature HEAD 的祖先；集成树 = `develop_base` + `feature_head`）。
- 三个不可变 SHA：`review_target=92068976d73d29384c9227fc90d648fb3ddb3261`；`feature_head=16554ef704f5182b62b92e80fbe6c3b69dd2ae86`；`develop_base=6902d2445665ec356a2db22b33b7749d39d8018e`（`origin/develop`，恰为任务基线）。

## Environment

- OS：Linux x86_64（5.4.241-1-tlinux4）。
- Go：go1.24.1 linux/amd64（符合 Task 的 Linux + Go 1.24.x 要求）。
- 外部依赖：无 MySQL / Redis / MQ / Docker / 外网 / API Key；根 `go.mod` 零 `require`，未新增。
- 验证环境：一次性临时 worktree `/tmp/mini-agent-delivery`（detached 于 `develop_base`，ff 合并 `feature_head`，合并无冲突）；主工作区未改动，未创建/推送永久 integration branch、未 push develop、未 merge。
- 测试数据：Smoke 使用进程内一次性内存 `TodoOperations` Fake（仅本 Smoke 程序私有，不属于交付材料），无外部状态、无清理负担。
- 配置来源：无（任务不读取环境变量与真实凭据）。

## Verification

| Check | Result | Evidence |
|---|---|---|
| 集成环境构建（`go build ./...`） | PASS | 临时集成 worktree 内 exit 0 |
| `go vet ./...` | PASS | exit 0 |
| `make test`（=`go test ./...`） | PASS | exit 0，`builtin ok 0.007s` |
| `go test -race -count=1 ./...` | PASS | exit 0，全模块无 data race |
| 依赖方向 INV-B08（`go list`） | PASS | `builtin` imports = stdlib（bytes/context/encoding/json/errors/fmt/math/strings）+ core/tool/session，无 llm/contextmgr/runlog |
| 冻结文件 INV-B08（`git diff --stat 6902d24..HEAD -- core tool session llm contextmgr runlog`） | PASS | 输出为空 |
| 无生产存储/ID 生成 INV-B07（`git grep -E "type .*Store struct\|uuid\|rand\.\|nanoid" -- builtin/ ':!*_test.go'`） | PASS | 生产代码无命中 |
| §12 所有权表 ↔ `git ls-files builtin/`（AC-008） | PASS | 10 个文件双向差集为空 |
| Core Flow Smoke（Deliverer 独立程序，`go run -race`） | PASS | 124 项断言全部 PASS、exit 0，见下 |
| Target 一致性 | PASS | `delivery.review_target = review.target = C1`；C1 为 feature HEAD 祖先 |

Smoke 主链路（经公共构造函数与 `tool.Tool` 接口，不启动 Agent Loop / Provider）：

1. `builtin.All(port)` 聚合 5 工具、名称与顺序符合 Contract、`Parameters` 均为 JSON object；
2. calculator：add/sub/mul/div 成功值可从 Content 解析且数值正确（含 `1/3` 除不尽相对误差 ≤1e-12、`0.1+0.2` = `0.30000000000000004` 浮点边界）；除 0/-0、未知运算符、缺 op/操作数、字符串操作数、`1e308*10` 溢出、非法 JSON、数组、`NaN` 字面量均 `IsError=true` + `err=nil` + 精确错误码 + Content 无 `result` 字段；
3. mock_search：命中含 `mock:true`、同查询（含大小写/空白差异）逐字节一致、无命中空数组且 `IsError=false`、空查询 `empty_query`；
4. todo 建→列→完→列：创建返回唯一标识（pending），列表含本会话 2 条，完成后 `completed`，重复完成幂等；端口每次收到的 `sessionID` 与 `req.SessionID` 完全一致（6 次调用全部 `sess-A` 作用域）；不存在标识 `todo_not_found` 且端口数据逐字段不变；空 `SessionID` → `missing_session_id` 且端口 0 调用；`session_id`/`sessionId`/`session` 参数 → 拒绝且 0 调用；会话 B 列表为空、B 用 A 标识 → `todo_not_found`；
5. 失败分级：端口普通错误（含 `password`/路径样例串）→ 非 nil `*core.RunError` 且 `KindOf=KindInternal`，Content 不含内部细节；端口 `context.Canceled` → `KindCancelled`；预取消 ctx → `KindCancelled` 且端口 0 调用；`All(nil)` 三个 todo 工具 → `KindInternal` 且不 panic；
6. INV-B01：每次 Execute 的 `CallID == req.Call.ID`、`Name == Spec().Name`，`Validate()` 通过（成功与失败路径均断言）；
7. 工具层并发：同一 `todo_create` 实例 50 goroutine 并发创建后恰 50 条且标识互异（`-race` 下运行）。

## Acceptance Evidence

- AC-001 → Smoke：四种运算符成功值正确、有限，除不尽按相对误差判定。PASS
- AC-002 → Smoke：9 类失败输入均 `IsError=true` + `err=nil` + 精确错误码，无 `result` 字段，不 panic。PASS
- AC-003 → Smoke：逐字节一致（含 `Go`/`  GO `）、mock 标识、无命中空数组、空查询拒绝。PASS
- AC-004 → Smoke：生命周期 + 端口 sessionID 逐次核对 + 幂等 + 不存在无副作用 + 空 SessionID 零调用。PASS
- AC-005 → Smoke：跨会话列表隔离（端口语义）+ B 用 A 标识 `todo_not_found` + 三种会话字段拒绝且零调用。PASS（仅工具层证据）
- AC-006 → Smoke 并发 50 创建恰 50 且互异；`go test -race -count=1 ./...` 全模块干净。PASS（仅工具层证据）
- AC-007 → `go build` / `go vet` / `make test` / `-race` 均 exit 0；`go list` 依赖方向合规；冻结目录 diff 为空；`go.mod` 零 require。PASS
- AC-008 → §12 表与 `git ls-files builtin/` 双向差集为空；Design 内容一致性以 Cleaner 四方核对结论为输入，本轮机械复核所有权表。PASS
- AC-009 → Smoke：三类基础设施失败 Kind 正确、Content 防泄漏、端口零调用场景成立。PASS
- INV-B01/B02/B03/B04/B05/B06/B07/B08 → 分别由 Smoke 对应断言与机械核对覆盖。PASS

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 服务启动 / 健康检查 / HTTP API Smoke | 交付物为库内实现，Task Out of Scope 明确排除 CLI / HTTP Server / 可运行二进制 / Docker | 无；运行期启动证据归后续装配与 Agent Loop 任务 |
| MySQL / Redis / MQ / Docker 真实交互 | Task 与 Contract 明确无此类依赖（零 `require`、无外部服务） | 无 |
| 重启 / 恢复 / 回滚 / 性能检查 | Task/Contract 未要求；且无持久化生产实现可演练 | 无；未演练项不声称已验证 |
| 真实 Session 隔离 / 跨会话不可写 / Todo 生命周期与持久化并发 | Contract D7② 与本任务证据边界：归 Session Manager（T04）与装配层（T07），T03 只证明工具层 | 已记录为剩余风险，非本任务缺陷 |

## Remaining Risks

- 证据边界（Contract 已声明）：本验收的 Todo 证据均为工具层（Fake 端口）；真实会话隔离、跨会话不可写与 Todo 生命周期不由本任务证明，归 T04 / T07。
- 集成期适配（D7② 代价）：`builtin.TodoOperations` 与 T04 的 `session.TodoOperations` 为两套端口，适配、Todo ID 生成、状态迁移与错误映射须由 T07 显式承担并验收（Contract C4）；遗漏将导致 Todo 工具在真实装配中不可用。
- `float64` 精度：`0.1+0.2 != 0.3` 为协议固化行为（本轮实测 `0.30000000000000004`），模型侧需知悉；十进制精确算术需 Contract Revision。

## Result

PASS
