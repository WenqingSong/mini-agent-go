# Discovery 调查报告 — Minimal Agent Runtime V1.0

- intake_id：`intake-minimal-agent-runtime-v1`
- 阶段：第一阶段（调查）产物；状态 `WAITING_FOR_OWNER_DECISION`
- Intake 记录：`.agent/intake/intake-minimal-agent-runtime-v1/intake.yaml`（parent_branch=`develop`，base_commit=`b43cdfbf78e3eef626e2af6f59c7ef675196da60`）

## 1. 结论（先行）

- 真实仓库为**全新项目**：基线仅含 Agent Workflow v4.3.0 框架与 CI 配置，无业务代码、无任何 Task/Program/Intake、无设计文档，与 Owner 需求「从零实现」一致，不存在需要保留或恢复的他人工作。
- 父分支与 Git Base 已经 Owner 指定并**远程核验通过**：`develop` 本地与 `origin/develop` 均为 `b43cdfbf78e3eef626e2af6f59c7ef675196da60`；`main` 保持 `22e4d2c1cb9b9620739fbdeee587676c96c56847` 未动。配置冲突（integration branch）已由 Owner 推送的两个提交解决。
- **流程推荐（推荐，非批准）**：整体按 Program（COORDINATED）由 Owner 规划；第一步 D01 正式设计建议 `DESIGN` 类型；T00 公共契约建议 `NORMAL`（是否升 `COMPLEX` 待 Owner 决定）；T01–T06 各建议 `NORMAL`；T07 建议 `COMPLEX`；T08–T10 建议 `NORMAL`；T11 默认暂停、须再次明确授权。
- 原执行清单的**阶段结构、文件所有权、AC 与测试约束可整体沿用**；需要的最小调整仅三点：正式 Task ID 不得沿用 T00–T11 候选编号；D01 需按 V4 声明 Design Impact=NEW 并落 `docs/design.md`；`make test` 的 Makefile 缺口需安排落地时机（见 §8）。
- 已知问题：`.cnb.yml` 的 `pr-check` 执行 `make test`，但仓库既无 Makefile 也无 `go.mod`，在 T00/T10 落地前，针对 `main|develop` 的 PR 检查必然失败。本轮只记录与分析，未做任何修改。
- Gate：`discovery-start` **PASS**（命令、退出码与原始输出见 §10）。

## 2. Owner 决策记录（本轮输入；非机器批准字段）

Owner 于 2026-10-19 确认（已逐项远程核验，未仅凭文字认定）：

1. Intake ID：`intake-minimal-agent-runtime-v1`；
2. 开发分支与最终集成目标：`develop`；
3. `main` 保持不动，不直接向 `main` 提交或合并；
4. 预期最新开发基线：`b43cdfbf78e3eef626e2af6f59c7ef675196da60`；
5. 项目规划依据：沿用此前提供的《Minimal Agent Runtime V1.0 并行开发执行任务清单》，不重新生成独立计划。

配置变更（已核验实际内容）：

- `87bc0f6f663e9d63eb32483af37d078c25027472`：`.agent/workflow.yaml` 的 `git.integration_branch` 由 `main` 改为 `develop`；`.agent/PROJECT_ADAPTATION.md` 记录 Owner 决策 D5；
- `b43cdfbf78e3eef626e2af6f59c7ef675196da60`：`.cnb.yml` 流水线触发改为分支 glob `"(main|develop)"`，`main` 规则不变。

**注意**：本轮 Owner 尚未批准具体 Task ID 与 task_type，也未批准进入第二阶段；按规范停在决策检查点，未生成 `owner-decision.md` 的 APPROVED 记录，未生成 `taskbuilder-handoff.md`。

## 3. 真实仓库与 Git 基线（核验证据）

- 仓库：`/root/projects/mini-agent-go`；remote `origin` = `https://cnb.cool/framework-station/mini-agent-go`。
- 当前分支：`develop`（tracking `origin/develop`，up to date）；工作区 clean，无 stash，无其他 worktree（`git worktree list` 仅主工作区）。
- `origin/develop` HEAD = `b43cdfbf78e3eef626e2af6f59c7ef675196da60`（`ci: trigger pipelines on develop as well as main`，2026-10-19 13:34:08 +0800）；本地 HEAD 与之一致。
- `origin/main` = `22e4d2c1cb9b9620739fbdeee587676c96c56847`，与 Owner 所述一致，未被触碰。
- 基线只读校验：`.agent/bin/workflow-git verify-base --parent develop --base b43cdfbf78e3eef626e2af6f59c7ef675196da60` → `ok`（exit 0）。
- 配置解析：`.agent/bin/workflow-check config` → `PASS config: workflow.yaml schema/registry paths valid (not Owner approval)`（exit 0）。
- CI 触发：`.cnb.yml` 现为 `"(main|develop)"` glob；`pull_request` → `pr-check`（golang:1.24，`make test`）；`push` → `main-ci-and-github-mirror`（GitHub 镜像同步）。

## 4. 项目现状调查（D00 事实）

- 业务代码：仓库根仅 `.agent/` 与 `.cnb.yml`；无 `go.mod`、`cmd/`、`internal/`、`docs/`、`README.md`、`Makefile`。
- 框架：`.agent/` 为 Workflow v4.3.0（`.agent/VERSION`=4.3.0；`.agent/upstream.yaml` 记录上游 `229edfd18e3b6e770f803db01e5010b215ba623f`，local_changes=null）；基线含 146 个受跟踪文件，`.agent/engine/` 源码在基线内；六个 CLI 已在 `.agent/bin/` 构建可用；本机 Go 1.24.1。
- 运行数据：无 `.agent/tasks/`、`.agent/programs/`、`.agent/registry/`（本 Intake 记录创建前亦无 `.agent/intake/`）；无同一目标的 ACTIVE Task/Program，无恢复场景。
- 设计文档：不存在；V1.0 设计尚未正式归档或批准（此前仅为需求文档中的计划）。
- 业务规则：宿主根 `AGENTS.md` 不存在（按规范不创建、不阻断）。
- 观察项（未改动）：`.agent/PROJECT_ADAPTATION.md` 仍存在于基线，状态 READY，记录了 D5（integration branch=develop）决策；按适配规范该类临时文件应在 adoption 完成后删除，是否清理由 Owner 决定。

## 5. 影响范围与任务边界

- 本 Intake 覆盖 Owner 需求全清单（D00–T11）的入口调查与流程推荐，不预设某一子 Task 的最终边界。
- 并行开发的硬边界来自清单本身：T00 冻结公共接口后，T01–T06 只依赖、不得修改公共契约；每个子 Task 独立文件所有权、独立分支/Worktree、独立验收证据。
- 全局资源：`.agent/workflow.yaml` 当前 `resources: {}`，无声明的 Shared Resource Kind；后续子 Task 若需新分配，须按协作规范 §12 由 Analyst 在 Contract 批准后以 Registry-only 方式落到集成分支 `develop`。

## 6. 流程推荐与依据（推荐 ≠ 批准）

| 范围 | 推荐类型 | 依据 | 批准后首个执行角色 |
| --- | --- | --- | --- |
| 整体（D00–T11 全清单） | **Program（COORDINATED）** | 多个可独立验收子 Task、依赖明确、需整体集成交付；Program 无机器 Intake 锚点，由 Owner 按 CoordinatedWorkflow 规划 | Owner 规划；子 Task 各自 Discovery/TaskBuilder |
| D01 正式设计（`docs/design.md`） | **DESIGN** | 主要交付物是正式工程设计规范，先架构批准再落地，无生产实现要求 | Architect |
| T00 公共契约与骨架 | **NORMAL（候选）** | 目标与 AC 清楚；属新建公共契约而非修改既有公开协议，当前无既有分配需要承继；若 Owner 希望接口冻结获得正式 Contract 约束，可升 COMPLEX | Coder（NORMAL）/ Analyst（COMPLEX） |
| T01 LLM Client / T02 Registry+Executor / T03 三工具 / T04 Session / T05 Context / T06 Observability | **NORMAL** | 各自局部实现、AC 与测试命令明确，均可用 Mock/httptest/Fake Summarizer 验证，不触碰公共契约 | Coder |
| T07 Agent Runtime 与 Loop | **COMPLEX（推荐）** | 涉及并发、超时/取消、错误策略与循环终止语义，属「一致性/失败语义需先确认 Contract」的情形 | Analyst |
| T08 HTTP API | NORMAL | 仅 HTTP 协议与请求校验，依赖 T04/T07 交付证据 | Coder |
| T09 端到端本地集成 | NORMAL | 独立测试目录/文件，验证模块组合，不改他人生产代码 | Coder |
| T10 README 与 AI 工作记录 | NORMAL | 文档交付，依赖 T00–T09 实际结果 | Coder |
| T11 真实 API 验收 | NORMAL（默认暂停） | 须 Owner 再次明确授权；最多 3 次真实模型请求，显式开关隔离 | Coder |

类型判断均依据 `.agent/specs/workflows/EntryWorkflow.md` 推荐表；以上为 Discovery 推荐，**不构成批准**，最终以 Owner 决策为准。

## 7. 以原清单为基础的依赖核验：沿用与最小调整

**依赖关系核验**（与框架工作流一致性）：

- D00（本 Intake）→ D01 设计归档批准 → P00 Owner 规划 Program → T00 公共契约冻结 → T01–T06 并行 → T07（等 T01–T06 交付证据）→ T08（等 T04、T07）→ T09（等 T01–T08）→ T10 → T11（最后，显式授权）。该图与 CoordinatedWorkflow「子 Task 独立 Discovery/TaskBuilder/实施/验收，Collector 登记、Assembler 集成」及并行执行规则不冲突。
- 每个子 Task 仍分别经公共 Discovery 入口与独立 Owner 批准，Program 本身不生成 COORDINATED 类型的 Task Handoff。

**可整体沿用**：阶段划分与出口条件；各 Task 文件所有权清单；AC 与验收命令（`go test ./internal/...`、`-race`、`go vet ./...`）；测试约束（Mock LLM、`httptest.Server`、Fake Summarizer、确定性工具，`go test ./...` 不触达真实 API）；T00 后接口冻结纪律；T11 收费控制规则。

**最小调整（不重新规划）**：

1. **正式 Task ID**：T00–T11 仅为候选编号（清单第七节亦明确「正式 ID、分支和依赖登记由实际 Agent Workflow 流程决定」）；每个子 Task 启动时另立 Intake 并由 Owner 批准真实 task_id，本报告不代拟。
2. **D01 交付物落点**：`docs/design.md` 按 V4 约定作为项目级长期 Design（`docs/design/*`）声明 Design Impact=NEW，并在 task.md 纳入 Scope/Deliverables/AC；DESIGN 任务内另有架构与设计两阶段检查点（Architect → Owner 架构批准 → DocsSpecWriter）。
3. **T00 类型**：NORMAL/COMPLEX 二选一待 Owner 决定（见 §6 依据）。
4. **T07 类型**：建议 COMPLEX，待 Owner 确认。
5. **T11**：保持「默认暂停 + 显式授权变量」设计，不并入普通 `go test ./...`。

## 8. 已知问题与影响评估（只记录，不修改）

1. **`make test` 缺 Makefile**：`.cnb.yml` `pr-check` 在 golang:1.24 执行 `make test`，但仓库无 `Makefile`，且 T00 前无 `go.mod`。影响：本轮回合之后、T00/T10 落地之前，任何针对 `main|develop` 的 PR 检查会因缺 Makefile/模块失败；不影响本地 Discovery/Gate 与后续 Task 的本地验收命令。可选方向（供 Owner 决定，本轮不执行）：(a) 接受 CI 红灯至 T00/T10；(b) 在 T00 范围外提前补最小 Makefile（需 Owner 批准扩权）；(c) 后续调整 `.cnb.yml`。
2. **全局资源 Registry 为空**：`resources: {}`；后续新分配必须走 §12 流程，不得在 Feature 分支内自宣 RESERVED。
3. **`PROJECT_ADAPTATION.md` 残留**：适配临时文件仍在基线（READY 状态）；按规范 adoption 完成后应删除，是否清理由 Owner 决定，本轮回合未触碰。
4. **父分支写权限纪律**：`develop` 现为集成分支；Agent 对其的直接写仅限于各自受控产物（本 Intake 的 metadata 提交即属此类），业务集成仍只由 Merger 在明确授权下执行。

## 9. 风险与依赖

- 公共契约冻结是后续并发的唯一基线：T00 未完成前不得启动 T01–T06；冻结后任何接口变更须走正式决策流程。
- T07 依赖 T01–T06 的正式交付证据（非自述），T09 依赖完整模块集成；任一子 Task 失败都保留原始证据并返回有权角色，不跳 Gate。
- 收费 API 风险集中在 T11：默认暂停，最多 3 次请求，失败即停止并重新申请授权。
- 无宿主根业务规则文件；业务边界以 Owner 需求清单与后续各 Task 的 task.md 为准。

## 10. Gate 验证证据

- 运行时间（UTC）：2026-10-19T05:51:34Z
- 命令（宿主根执行，只读）：

```bash
.agent/bin/workflow-check gate discovery-start .agent/intake/intake-minimal-agent-runtime-v1
```

- 退出码：`0`
- 原始输出：

```text
PASS     gate=discovery-start
```

- 说明：Gate 只读校验 Intake 记录结构与 Git 基线（父分支存在、base_commit 真实且为父分支祖先），不启动任何 Agent、不构成任何批准；运行前后未改动被检 Git 快照。此时 `owner-decision.md` / `taskbuilder-handoff.md` 尚未形成（Owner 未批准 task_id/task_type），`discovery-decision` / `discovery-handoff` 按规范不运行。

## 11. 需要 Owner 决定的事项

1. 是否按 **Program（COORDINATED）** 整体规划：Program ID/版本、父基线（建议 `develop` + 批准时的最新 tip）、子 Task 清单与类型、整体 AC；Program 规划由 Owner 按 CoordinatedWorkflow 记录，Discovery 不代拟。
2. **D01 是否批准为 DESIGN 任务**：task_id、task_type=DESIGN、parent_branch=`develop`、base_commit（批准时提供完整 SHA）。批准后本 Intake 进入第二阶段（finalize），产出 `taskbuilder-handoff.md` 并依次通过 `discovery-decision` / `discovery-handoff`。
3. **T00 类型**：NORMAL 还是 COMPLEX。
4. **T07 是否接受 COMPLEX 推荐**。
5. **Makefile/CI 缺口**处理时机（§8 方向 a/b/c）。
6. `PROJECT_ADAPTATION.md` 是否清理（观察项，非阻塞）。

## 12. 停止条件检查

- 父分支候选与 Git Base 已可靠识别并远程核验；无跨模块不可界定边界；无关键业务规则缺失导致的范围分歧；Owner 决策中未含 task_id/task_type——按规范属于「尚未确认具体 Task/类型」，停在决策检查点，不生成 APPROVED，不进入 finalize。

## 13. 第二阶段执行记录与 Gate 证据（2026-10-19）

Owner 已批准首个正式设计任务（task_id=`minimal-agent-runtime-design-v1`，task_type=DESIGN，parent_branch=develop，base_commit=`c398e8ab2b29335889c9e093369b80f2ebe8cd68`，Design Impact=NEW），并附带项目级决策（Program/COORDINATED 规划基线、develop 为唯一集成目标、收费 API 控制等）。

### 13.1 字段核验结果（先核验后记录）

| 核验项 | 结果 | 证据 |
| --- | --- | --- |
| task_id 格式 | 符合 | `minimal-agent-runtime-design-v1` 匹配安全单一路径段规则（`^[A-Za-z0-9][A-Za-z0-9._-]*$`） |
| task_id 唯一性 | 符合 | 无 `.agent/tasks/`、无同名分支（`workflow-git branch-exists` → missing）、无同名 Worktree、仓库无既有引用 |
| base_commit 有效性 | 符合（作为 Git Base） | `c398e8a...` 为真实 commit，且为 `develop` 与 `origin/develop` 当前 tip；`verify-base --parent develop --base c398e8a...` → ok |
| DESIGN 初始化要求 | 符合 | handoff 绑定 `task_type=DESIGN`；DesignWorkflow 规定 TaskBuilder 初始化 schema 4：contract.status=NOT_REQUIRED、design.status=PENDING、approved_version/final_version=0、review=NOT_REQUESTED、owner=PENDING、delivery=NOT_RUN、初始 Handoff next_role=Architect |
| base 与 Intake 锚点绑定 | **不符合（阻塞）** | 见 13.3 |

### 13.2 已持久化的产物

- `.agent/intake/intake-minimal-agent-runtime-v1/owner-decision.md`：按 Owner 明确决策机械记录，front-matter 为 decision=APPROVED、intake_id、task_id、task_type=DESIGN、parent_branch=develop、base_commit=`c398e8ab2b29335889c9e093369b80f2ebe8cd68`；正文含批准范围与核验状态。

### 13.3 实际 Gate 运行证据

运行时间（UTC）：2026-10-19T07:47:13Z 前后；均在宿主根执行，只读。

```bash
.agent/bin/workflow-check gate discovery-decision .agent/intake/intake-minimal-agent-runtime-v1
```

退出码：`0`；原始输出：

```text
PASS     gate=discovery-decision
```

为取得真实机器结果，曾按 Owner 批准值临时写入 `taskbuilder-handoff.md`（未提交、运行后即删除，未保留无效产物），并运行：

```bash
.agent/bin/workflow-check gate discovery-handoff .agent/intake/intake-minimal-agent-runtime-v1
```

退出码：`1`；原始输出：

```text
FAIL     gate=discovery-handoff task=intake-minimal-agent-runtime-v1 check=Discovery Handoff actual=taskbuilder-handoff.base_commit=c398e8ab2b29335889c9e093369b80f2ebe8cd68 expected=b43cdfbf78e3eef626e2af6f59c7ef675196da60 reason=TaskBuilder Handoff 的 base_commit 与 Intake 基线不一致
```

### 13.4 冲突根因（机器规则，已核验引擎实现）

1. `intake.yaml` 锚定 `base_commit=b43cdfbf78e3eef626e2af6f59c7ef675196da60`（调查时的 Git Base，属「初始父分支与 Commit 不会丢失」的不可变事实，不得改写）；
2. `GateDiscoveryHandoff` 要求 `taskbuilder-handoff.base_commit == intake.yaml.base_commit`（引擎 `discovery_gate.go` 的 Discovery Handoff 检查）；
3. `intakeDecisionBinding` 进一步要求 owner-decision 的 base 与 handoff、Intake 三方相等；
4. EntryWorkflow 明确规定 TaskBuilder「不以 Intake tip 替换原 Base」——Owner 批准的 `c398e8a` 正是本 Intake 的证据提交（Intake tip）。

因此：按批准值生成 handoff → `discovery-handoff` FAIL（已实测）；若改用 Intake 锚点值 → 属于擅自替换 Owner 批准的基线，明令禁止。在 Owner 就 base 作出新的明确决定前，不生成 `taskbuilder-handoff.md`，不进入 TaskBuilder 初始化。

### 13.5 待 Owner 决定后执行的下一步

- 若 Owner 确认 base=`b43cdfbf78e3eef626e2af6f59c7ef675196da60`：按明确指令机械更正 owner-decision.md 的 base 字段（git 历史保留原记录），生成 `taskbuilder-handoff.md`，重跑 `discovery-decision` / `discovery-handoff`（按引擎逻辑预期 PASS），提交推送；
- 若 Owner 坚持 `c398e8a`：需 Owner 对该规则冲突作明确决定，Discovery 不自行偏离。
