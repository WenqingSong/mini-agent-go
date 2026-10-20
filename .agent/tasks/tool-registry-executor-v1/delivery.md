# Delivery Verification

## Milestone and Target

- Milestone：`tool-registry-executor-v1` —— Tool 注册、参数校验与受控执行（完整业务模块交付）
- Delivery Target：`40a10c0017c18f166a919703846f9106b67b24e3`（feature `tool-registry-executor-v1` HEAD，含 C1' 之后的 review-neutral tail）
- Cleaner Review Target：`03dced5177ba7db864fe6c553a9ca4ce2e7c6136`（C1'，`state.review.target`）
- Target Match：YES —— `git diff 03dced5..40a10c0` 仅含 `.agent/tasks/tool-registry-executor-v1/` 下 artifacts（findings / core-logic / owner-decision / state.yaml），无生产代码、测试与设计文档变化；CLEAN 对 Delivery Target 有效
- Owner Decision Target：`03dced5`（`owner.review_target`），与 Review Target 一致

集成验证快照（三个不可变 SHA）：

```yaml
delivery:
  review_target: "03dced5177ba7db864fe6c553a9ca4ce2e7c6136"
  feature_head: "40a10c0017c18f166a919703846f9106b67b24e3"
  develop_base: "6902d2445665ec356a2db22b33b7749d39d8018e"
```

`develop_base` 经 `git merge-base --is-ancestor` 确认为 `feature_head` 祖先，集成结果为快进等价。

## Environment

- OS：Linux（x86_64）
- 运行时：go1.24.1 linux/amd64
- 外部依赖：无 MySQL / Redis / Kafka / Docker；本里程碑为纯库模块，无网络、无外部服务
- 集成环境：临时 detached worktree `/tmp/delivery-verify-tool-registry` @ `40a10c0`（`git worktree add --detach`），验收后已移除；未创建/推送任何永久 branch，未 push develop，未 merge
- 配置来源：仓库内 `go.mod`（零 `require`）与 `Makefile`；无 Secret
- 测试数据：内存态 Fake Tool（调用计数、阻塞/panic/error 行为）；无持久化副作用，无需清理
- 数据隔离：全部在临时 worktree 与进程内内存完成，未触碰共享数据

## Verification

| Check | Result | Evidence |
|---|---|---|
| 开始 Gate `delivery-start` | PASS | `.agent/bin/workflow-check gate delivery-start .agent/tasks/tool-registry-executor-v1` → exit 0 |
| Target 一致性（review.target ↔ feature_head） | PASS | `git diff --stat 03dced5..40a10c0` 仅 4 个 `.agent/tasks/` artifact 文件；`git rev-list --left-right --count origin/tool-registry-executor-v1...HEAD` = `0 0` |
| Build（`make build` = `go build ./...`） | PASS | exit 0，全包编译通过 |
| Vet（`go vet ./...`） | PASS | exit 0 |
| Test（`make test` = `go test ./...`） | PASS | `core/llm/runlog/tool` 全 ok（0.004s/0.087s） |
| Race Test（`go test -race ./tool/...`） | PASS | ok 1.128s |
| Race 稳定性（`go test -race -count=2 ./tool/...`） | PASS | ok 1.389s |
| 依赖方向（`go list -f '{{.ImportPath}} {{join .Imports " "}}' ./tool`） | PASS | 仅标准库 + `<module>/core`；无 `llm`、无第三方 |
| `go.mod` 无新增 require | PASS | `git diff 6902d24 -- go.mod go.sum` 为空 |
| 文件所有权（`git ls-files tool/` ↔ 设计 §12） | PASS | 8 个文件，差集为空（`diff` 退出 0） |
| 独立 Smoke 主链路（`go run -race ./deliverysmoke`） | PASS | 42 项断言全部 PASS，`RESULT: failed=0`，`-race` 无竞争报告；重复 3 轮均 exit 0 |

### 独立 Smoke 覆盖的主链路（Deliverer 本次执行，非引用 Coder/Cleaner 报告）

以真实消费者身份（外部 package import `tool` + `core`）构造 4 个 Fake Tool（正常 / 阻塞 2s / panic / 返回 error）与 50ms 工具级超时执行器：

1. 装配：注册 4 工具 → 重复注册 `invalid_argument`、空名称 `invalid_argument`、Seal 后 `ErrRegistrySealed`（`errors.Is` 可辨）；
2. 元数据：`Lookup` 命中同一实例、未命中 `(nil,false)`；`Specs()` 按名排序、深拷贝（篡改返回值不影响注册表）、原始 `Parameters` 逐字节保留；
3. 成功执行：`CallID`/`Name` 归一化为请求值、`IsError=false`、`ToolResult.Validate()==nil`、工具调用计数 1；
4. 拒绝语义均在 `Tool.Execute` 之前：参数类型不符 → `IsError=true` 且 Content 含 `$.city` 路径、工具计数仍为 1；未知工具 → `IsError=true`；
5. 去重：同 Run 同 CallID 第二次 → `invalid_pairing` 且带 CallID、工具不再执行；不同 Run 同 CallID → 正常执行（计数 2）；16 goroutine 并发同键 → 执行 1 次、15 个 `invalid_pairing`；
6. 执行期：工具 error → `IsError=true` 含原因文本；工具级超时 → 约 50ms 内返回、`IsError=true`、Content 含超时文本、保留 CallID；执行中父 ctx 取消 → `cancelled` 且约 20ms 返回（非 2s）；已取消 ctx → 立即 `cancelled` 且工具未被调用；panic → `tool_failure`、保留 CallID、无堆栈痕迹；
7. 边界：空 CallID → `invalid_pairing`；空 RunID → `invalid_argument`；空工具名 → `invalid_argument`。

## Acceptance Evidence

- AC-001（注册/查找/Specs/成功执行/依赖方向）→ Smoke 项 1–3 + `go list` 输出；`Lookup` 同一实例、`Specs` 逐字节一致、`Validate()==nil` 均由本次运行证据支持。
- AC-002（六类拒绝 + 不调工具）→ Smoke 项 4：参数类型、未知工具实测 `IsError=true` 且工具调用计数不变；重复注册/空名称/Seal 后注册为 `invalid_argument` 系 `errors.Is` 可辨；`Lookup` 未命中 `(nil,false)`。
- AC-003（超时/取消/panic/有限返回/保留 CallID/无 RunStatus）→ Smoke 项 6：超时约 50ms、取消约 20ms 返回；panic 为 `tool_failure` 且无 `goroutine`/`runtime/debug` 痕迹；`-race` 下 16 并发无竞争；执行器不产生 `RunStatus`（`tool` 包源码无 `RunStatus` 引用，构建期确认）。
- AC-004（同 Run 去重与范围）→ Smoke 项 5：同 Run 同 CallID 计数不增且 `invalid_pairing`；跨 Run/并发边界实测符合 Contract §3。
- AC-005（make test / vet / race / go.mod）→ Verification 表对应 7 行全部 PASS。
- AC-006（设计一致性）→ 本次轻量核对：§11 `INV-009`—`INV-013` 存在且证据列指向 `tool` 包真实测试文件；§12 与 `git ls-files tool/` 差集为空。完整四方核对（§7 逐字对应、§14 无双重归属、设计 diff 不越界）为 Cleaner 已执行的审查职责，本次未重做全量 Diff Review；文档内容无本次运行反证。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 完整 Design 四方核对（Task↔Contract↔Design↔实现逐字） | Cleaner 审查职责，Deliverer 不重复 Diff Review | 低——本次对 §11/§12 做了机械核对且结果一致；若需更强保证应信任 Cleaner `CLEAN` 结论而非 Deliverer 重审 |
| 恢复 / 回滚 / 重启演练 | Task 与 Contract 均未要求（纯库模块，无持久状态与服务生命周期） | 无——不适用而非遗漏 |
| 性能 / 并发容量指标（QPS、P50/P95/P99） | Task 未要求性能验收；本里程碑只要求功能正确与 `-race` 并发安全 | 低——性能不属本里程碑交付要求 |
| 忽略 Context 工具的 goroutine 终止与副作用撤销 | Contract §4 明确不承诺；Cleaner 已用收敛探针验证会自行返回的场景 | 中（已知边界，非本次新增）——已在 Remaining Risks 记录 |
| 真实外部依赖（MySQL/Redis/MQ/LLM）交互 | 本里程碑无外部依赖（零 `require`、无网络），Task 不涉及 | 无——不适用而非遗漏 |

## Remaining Risks

- CLEAN-003（P3，Owner 已明确暂缓）：执行期参数校验读取工具实时 `Spec()` 而非注册快照（`tool/executor.go:107`），需工具自身注册后改 Spec 才可触发；不影响本任务任何 AC。
- 去重占位表只增不删（Contract Open Risks，Owner 决定第 3 条）：长期共享 `Executor` 会累积内存；「不得无限积累」要求已明确移交后续 Agent Runtime 装配任务，当前未实现、未验证，不得宣称已解决。
- 拒绝空 `RunID` 提高集成前置要求：装配层必须设置 `ExecutionRef.RunID`。
- fail-closed Schema 子集未来扩充须走 Contract Revision。
- 忽略 Context 的工具：goroutine 可能长期存活、副作用不可撤销（Contract 明确边界）。
- `Seal()` 依赖装配层主动调用，未调用时运行期仍可注册。
- 时间相关用例在慢速 CI 上需留余量（本次 `-race -count=2` 稳定，但极端慢环境仍可能抖动）。
- panic 归 `tool_failure`：单工具缺陷的影响面为 Run 级。

## Result

PASS

所有关键交付检查均有本次独立运行证据并通过：目标与 CLEAN/ACCEPT 快照一致；构建、vet、全量测试、`-race`（含 `-count=2`）全部通过；独立 Smoke 42 项断言覆盖注册→Seal→Specs→执行→拒绝→去重→超时/取消/panic 全主链路，`-race` 下无竞争。未执行项均为 Cleaner 职责或不适用项，无核心交付要求缺失。本结论对应交付版本 `40a10c0`（review target `03dced5`，develop base `6902d24`）与上述临时集成环境。
