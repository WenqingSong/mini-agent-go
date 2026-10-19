---
decision: APPROVED
intake_id: intake-minimal-agent-runtime-v1
task_id: minimal-agent-runtime-design-v1
task_type: DESIGN
parent_branch: develop
base_commit: b43cdfbf78e3eef626e2af6f59c7ef675196da60
---

# Owner 决策记录 — intake-minimal-agent-runtime-v1

记录时间：2026-10-19（UTC）；base 修正时间：2026-10-19（UTC）。本文件由 Discovery 在收到 Owner 明确决策（含修正）后机械持久化；front-matter 为机器可读绑定字段，以最近一次明确修正为准，历史不在本文件抹去（见「决策修正记录」）。

## 批准的 Task（当前有效值）

- task_id：`minimal-agent-runtime-design-v1`
- task_type：`DESIGN`
- parent_branch：`develop`
- base_commit：`b43cdfbf78e3eef626e2af6f59c7ef675196da60`
- Design Impact：`NEW`

## 决策修正记录（保留历史，不抹去）

- **原批准（2026-10-19）**：base_commit=`c398e8ab2b29335889c9e093369b80f2ebe8cd68`。该值是本 Intake 的证据提交；Owner 说明原因是「误将 Intake 证据提交误认为任务开发基线」，该值**不再作为本任务的批准 Base**。
- **本次修正（2026-10-19，Owner 明确指令）**：base_commit 修正为 `b43cdfbf78e3eef626e2af6f59c7ef675196da60`，与 `intake.yaml` 的原始锚点一致，符合「不以 Intake tip 替换原 Base」。
- **历史保留**：含原批准值的 `owner-decision.md` 版本保存在 commit `43ae7766626b309b15663d0f2c209a5032399fab`，未改写、未回滚；`intake.yaml` 的原始 Base 未做任何修改；task_id / task_type / parent_branch / Design Impact 自原批准起未变。

## 批准的设计交付范围

以此前冻结的 Minimal Agent Runtime V1.0 架构和《并行开发执行任务清单》为依据，重点完成：

1. 系统架构及模块职责；
2. 公共数据模型与接口契约；
3. LLM API、Tool Calling 和 Agent Loop；
4. Tool Registry、参数校验与执行边界；
5. Session 隔离、并发控制和 Context 压缩；
6. 超时、错误处理、日志及测试策略；
7. 模块文件所有权、任务依赖图和并行开发边界；
8. Mock 测试和真实 LLM API 的费用控制要求。

设计交付物按 DESIGN Workflow 规范写入批准位置，走正式架构批准、独立审查和 OwnerGate 流程；不得提前实施业务代码。

## 项目级决策（上下文）

- 整体项目采用 Program / COORDINATED 流程；原执行清单继续作为规划基线，不重新从零拆分；
- 首个设计任务批准后，按正式流程推进整体 Program 规划与公共契约任务（候选编号 T00）；接口冻结并正式交付后，再启动六个独立模块任务并行开发；
- T00、T01 等仅为候选编号，正式 ID、依赖、Base、分支、Worktree 和验收条件按真实 Workflow 规范登记；
- 所有开发和最终集成目标均为 `develop`，不直接修改 `main`；
- 开发阶段不调用收费 LLM API；`go test ./...`、`go test -race ./...`、`go vet ./...` 不得产生真实 LLM API 费用；
- 真实 API 验收须在全部本地验证完成后由 Owner 单独明确授权；
- 不跳过 Gate，不伪造审批、测试或交付证据。

## 核验状态（Discovery 第二阶段核对）

- task_id 格式：符合（单一路径段标识，可用于 `.agent/tasks/<id>/`）；
- task_id 唯一性：符合（无同名 Task/Program/分支/Worktree，无既有引用）；
- base_commit 有效性：`b43cdfbf78e3eef626e2af6f59c7ef675196da60` 为真实 commit，且为 `develop` 当前 tip 的祖先（`verify-base` 通过）；
- DESIGN 初始化要求：符合（handoff 绑定 `task_type=DESIGN`；DesignWorkflow 规定 TaskBuilder 初始化 schema 4：contract.status=NOT_REQUIRED、design.status=PENDING、初始 Handoff next_role=Architect）；
- base 与 Intake 锚点绑定：已一致（owner-decision / taskbuilder-handoff / intake.yaml 三方均为 `b43cdfbf78e3eef626e2af6f59c7ef675196da60`）；`taskbuilder-handoff.md` 已按修正值生成，Gate 实测证据见 `discovery-report.md` §13–§14。
