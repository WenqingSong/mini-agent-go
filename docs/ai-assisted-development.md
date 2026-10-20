# AI 辅助开发记录（mini-agent-go）

> 本文件记录本项目如何使用 AI Agent 完成需求、决策与实现，以及每条记录的可核对证据。
> 它不是长期设计事实源：架构与协议仍以 `docs/design/runtime-architecture.md`、`docs/design/http-api.md` 为准。
> 记录原则：只写仓库内可核对的事实；没有留档的内容显式标注「无记录」，不补写、不转述、不推测。

## 1. 输入来源

### 1.1 角色提示词

Agent 的行为约束来自仓库内的角色提示词，每次任务按角色加载，不在聊天里临时发明：

| 角色 | 提示词路径 | 主要职责 |
| --- | --- | --- |
| Task Builder | `.agent/roles/TaskBuilderPrompt.md` | 把 Owner 需求整理为可验收任务（Goal / Scope / AC） |
| Analyst | `.agent/roles/AnalystAgent.md` | 技术契约、方案对比与 Design Impact 判定 |
| Coder | `.agent/roles/CoderAgent.md` | 按已批准 Contract 实现代码与测试 |
| Cleaner | `.agent/roles/CleanerAgent.md` | 独立审查实现，输出 Findings 与审查结论 |
| Owner Gate | `.agent/roles/OwnerGateAgent.md` | 向 Owner 呈现核心机制与决定点，机械落库 Owner 决定 |
| Deliverer | `.agent/roles/DelivererAgent.md` | 交付验收、集成与里程碑结论 |

配套规范：`.agent/specs/AgentCollaborationSpecification.md`（协作与 Git 权限）、`.agent/specs/Five-AgentResponsibilityBoundary.md`（职责边界）、`.agent/workflow.yaml`（机器配置）、`docs/design/agent-workflow.md`（Workflow 架构）。角色提示词位于仓库内，本文件只引用路径，不复述全文。

### 1.2 Owner 任务需求

每个任务的需求都先落库为 `.agent/tasks/<task_id>/task.md`，再进入决策与实现。任务事实（状态、绑定 SHA）在 `.agent/tasks/<task_id>/state.yaml`，Owner 决定在 `owner-decision.md`，审查结论在 `findings.md`，交付结论在 `delivery.md`。

任务编号在历史任务文件中存在漂移（例如 `llm-openai-client-v1` 在不同任务文件里被写作 T01 或 T02；Agent Loop 一度被预留为 T01，实际交付编号为 T07）。为避免二次编造，本文件以**任务 slug** 为主键，只在任务文件明确记录时附编号。

## 2. 使用记录

字段说明：

- 「决策证据」＝ 该任务的技术契约（`contract.md`）或 Owner 决定（`owner-decision.md`）的落库 commit；任务的 `state.yaml` 中 `contract.target` / `owner.review_target` 指向同一批 SHA。
- 「实现 commit」＝ 该任务的实现（或审查后修复）提交，即 Coder 交付给 Cleaner 审查的 C1。
- 「集成」＝ 合并入 `develop` 的 merge commit；无 merge commit 表示直接提交在 `develop` 上。

| 任务 slug | 编号（任务文件记录） | 需求 | 决策证据 | 实现 commit | 集成 | 结果 |
| --- | --- | --- | --- | --- | --- | --- |
| `runtime-core-contracts-v1` | T00 | [task.md](../.agent/tasks/runtime-core-contracts-v1/task.md) | [contract.md](../.agent/tasks/runtime-core-contracts-v1/contract.md)（Selected Design §1—§9）`6d7aebe` | `b4b9573` | 直接提交在 `develop`（无 merge commit） | CLEAN / ACCEPTED / delivery PASS（`6902d24`） |
| `llm-openai-client-v1` | 未记录（部分任务文件称 T02，但 T02 亦被 `tool-registry-executor-v1` 使用） | [task.md](../.agent/tasks/llm-openai-client-v1/task.md) | Contract `NOT_REQUIRED`；[owner-decision.md](../.agent/tasks/llm-openai-client-v1/owner-decision.md) | `5633001`，修复 `b3d2146`（CLEAN-002） | `492c88d` | CLEAN / ACCEPTED / delivery PASS |
| `tool-registry-executor-v1` | T02 | [task.md](../.agent/tasks/tool-registry-executor-v1/task.md) | [contract.md](../.agent/tasks/tool-registry-executor-v1/contract.md) `95e10b2`；[owner-decision.md](../.agent/tasks/tool-registry-executor-v1/owner-decision.md)（CL-001 / CL-002） | `707cb71`，修复 `03dced5` | `4106722` | CLEAN / ACCEPTED / delivery PASS |
| `builtin-agent-tools-v1` | T03（该任务 `task.md` 自述 T03） | [task.md](../.agent/tasks/builtin-agent-tools-v1/task.md) | [contract.md](../.agent/tasks/builtin-agent-tools-v1/contract.md)（D1—D4、D7②）`786d6fd` | `9206897` | `7e0e6ba` | CLEAN / ACCEPTED / delivery PASS |
| `agent-session-manager-v1` | T04 | [task.md](../.agent/tasks/agent-session-manager-v1/task.md) | [contract.md](../.agent/tasks/agent-session-manager-v1/contract.md)（D1—D6）`5263d2f` | `f23082a` | `73fef46` | CLEAN / ACCEPTED / delivery PASS |
| `agent-context-manager-v1` | T05 | [task.md](../.agent/tasks/agent-context-manager-v1/task.md) | [contract.md](../.agent/tasks/agent-context-manager-v1/contract.md) `05544f9`；R1 修订 `8e84932`（CLEAN-005） | `d92ada3`，修复 `bb991c1` | `7437e0d` | CLEAN / ACCEPTED / delivery PASS |
| `runtime-observability-v1` | T06 | [task.md](../.agent/tasks/runtime-observability-v1/task.md) | [contract.md](../.agent/tasks/runtime-observability-v1/contract.md) `6b68fc8` | `08cd0ac` | `dc734d2` | CLEAN / ACCEPTED / delivery PASS |
| `session-turn-drop-leading-v1` | 无编号（T07 前置依赖） | [task.md](../.agent/tasks/session-turn-drop-leading-v1/task.md) | [contract.md](../.agent/tasks/session-turn-drop-leading-v1/contract.md) `bcc8e96` | `2ed50f1` | `635e311` | CLEAN / ACCEPTED / delivery PASS |
| `agent-runtime-loop-v1` | T07 | [task.md](../.agent/tasks/agent-runtime-loop-v1/task.md) | [contract.md](../.agent/tasks/agent-runtime-loop-v1/contract.md)（D1—D5）`4918efa` | `e9dd3da` | `6bebdda` | CLEAN / ACCEPTED / delivery PASS |
| T08（保留编号：Todo 端口适配 + 生产 `Summarizer`） | T08 | 无记录（任务未创建） | 无记录 | 无记录 | 无记录 | 未交付 |
| `agent-http-api-v1` | T09 | [task.md](../.agent/tasks/agent-http-api-v1/task.md) | [contract.md](../.agent/tasks/agent-http-api-v1/contract.md)（D1—D7）`46d871d` | `3ce1869` | `d54c912` | CLEAN / ACCEPTED / delivery PASS |
| `agent-project-docs-v1` | T10 | [task.md](../.agent/tasks/agent-project-docs-v1/task.md) | Contract `NOT_REQUIRED`（Complexity NORMAL、Design Impact NONE） | 见该任务 `state.yaml` 的 `review.target`（Evidence Commit C1） | 待 Owner 与 Deliverer 流程 | 实现完成，待审查 |

补充说明（与上表一一对应，均为仓库内可核对事实）：

- 多数任务都经历了「Cleaner 提 Finding → Coder 修复 → Cleaner 复审」的循环，上表只列最终被 Owner 接受的实现 commit 与必要的修复 commit；完整链路见各任务 `findings.md`、`state.yaml` 的 `review.target` 历史。
- 每个任务在实现前都先把决策落库（Contract 或 Owner 决定），实现 commit 不含决策改写。例外是 `runtime-core-contracts-v1`：它本身就是契约层任务，决策文档与代码同属一次交付。
- 所有任务都以「真实命令 + 真实结果」记录验证，未执行项在 `delivery.md` 中标注为未验证，不以「测试通过」替代真实 Provider 验收。

## 3. 技术决策记录

长期设计事实与其决策来源的对应关系。左边是 `docs/design/*` 中已固化的取舍，右边是形成该取舍的任务内决策记录。

| 长期设计事实 | 落点 | 决策来源 |
| --- | --- | --- |
| 分层与依赖方向（L0 `core` / L1 抽象 / L2 `builtin` / L3 装配） | `runtime-architecture.md` §2、§3、§12 | `runtime-core-contracts-v1/contract.md` Selected Design §1、§8；T09 追加 L3 入口：`agent-http-api-v1/contract.md` D7（`46d871d`） |
| 消息是单一 struct + 类型化可选字段；配对不变量 INV-002 | `runtime-architecture.md` §4、§7 | `runtime-core-contracts-v1/contract.md` §2、§3 |
| 运行状态机：四个终态互斥，`COMPLETED` 无错误 | `runtime-architecture.md` §5 | `runtime-core-contracts-v1/contract.md` §3；`finish_reason` 判定细化见 `agent-runtime-loop-v1/contract.md` D5 |
| 双预算按类别计数、发出前预检、非正值即契约错误 | `runtime-architecture.md` §6 | `runtime-core-contracts-v1/contract.md` §4；`agent-runtime-loop-v1/contract.md` D3 |
| 提交边界：仅 `COMPLETED` 整轮提交，其余整轮丢弃 | `runtime-architecture.md` §7「提交边界」、§11.1 | `agent-runtime-loop-v1/contract.md` D1（Owner 批准） |
| 会话归属与能力注入（Tool 不持会话状态副本） | `runtime-architecture.md` §8、INV-005 | `builtin-agent-tools-v1/contract.md` D1 / D7②；`agent-session-manager-v1/contract.md` D1—D6 |
| 内存 Session Manager：会话串行、Turn 原子提交、惰性过期、前缀删除 INV-017 | `runtime-architecture.md` §8.1、§11.1 | `agent-session-manager-v1/contract.md`（D1—D6）；`session-turn-drop-leading-v1/contract.md`（`DropLeading` 语义） |
| 单工具失败分类、执行前把关、同 Run 去重 | `runtime-architecture.md` §7、§11 INV-009—INV-013 | `tool-registry-executor-v1/contract.md`；`owner-decision.md` CL-001 / CL-002（含去重生命周期交由装配层落实的决定） |
| 内置工具协议：会话作用域只取 `req.SessionID`、领域失败不进 `RunError` | `runtime-architecture.md` §8.1、§11.1 INV-B01—INV-B09 | `builtin-agent-tools-v1/contract.md` D2—D4 |
| 上下文压缩：原子单元、`KeepRecent` 硬下限、单次 `Build` 只调用一次 `Summarizer` | `runtime-architecture.md` §9 | `agent-context-manager-v1/contract.md`；R1 修订 `8e84932`（CLEAN-005） |
| 运行日志：观察者旁路、固定 JSON 字段、允许清单脱敏与上限 | `runtime-architecture.md` §10 | `runtime-observability-v1/contract.md`（含 Owner Decision Record） |
| HTTP 对外契约：三端点、错误码封闭枚举与 `code → status` 单点映射、取消/超时语义、认证与默认回环、生命周期 | `http-api.md` §3—§9 | `agent-http-api-v1/contract.md` D1—D7（`46d871d`） |
| v1 边界：`TokenBudget = 0` 不压缩、不注册 Todo 工具、`Summarizer` 占位 | `http-api.md` §8、§10；`runtime-architecture.md` §14 | `agent-http-api-v1/contract.md`（D7 与 Owner 边界裁定） |

约束：以上取舍的变更必须走 Contract Revision 并由 Owner 重新批准，再回写 `docs/design/*`；`docs/design/*` 是唯一长期权威，任务文件只记录决策过程与证据。

## 4. 无记录项

以下内容在本仓库中**没有留档**，本文件不作补写、不推测原文，也不把未执行的动作写成已执行：

- Owner 与各 Agent 的对话逐字记录：无记录。仓库只落库结论（`task.md` / `contract.md` / `owner-decision.md` / `findings.md` / `delivery.md` / `state.yaml`）。
- 执行这些任务时使用的具体模型名称与版本：无记录。
- 真实 Provider 调用记录与真实 API Key：无记录，且从未执行（见 `http-api.md` §10、`runtime-architecture.md` §14）。
- 各任务中 Agent 的完整工具调用轨迹（读了哪些文件、执行了哪些命令的逐条日志）：无记录；可核对的只有提交后的代码、测试与上述文档。
- T08（Todo 端口适配 + 生产 `Summarizer`）：无记录，任务目录、Contract 与实现均不存在；`runtime-architecture.md` §14 与 `http-api.md` §10 仍将其列为后续归属。
- `.agent/tasks/*/findings.md` 之外的审查过程细节（例如临时 worktree、一次性验证脚本）：无记录。

## 5. 复核方式

任一条记录都可以用下面几条命令独立核对：

```bash
# 决策与实现 commit 是否存在，以及它们的标题
git cat-file -e <sha>^{commit} && git log -1 --format='%h %s' <sha>

# 某任务涉及的全部提交
git log --oneline --all -- .agent/tasks/<task_id>

# 集成到 develop 的 merge 记录
git log --merges --oneline develop

# 任务的真实状态（contract / review / owner / delivery 绑定）
cat .agent/tasks/<task_id>/state.yaml
```

核对结论与本次文档交付的一致性由 Cleaner 与 Owner 判定；本文件不宣布审查通过，也不代替 Owner 的接受决定。
