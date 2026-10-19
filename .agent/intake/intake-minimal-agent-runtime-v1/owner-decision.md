---
decision: APPROVED
intake_id: intake-minimal-agent-runtime-v1
task_id: minimal-agent-runtime-design-v1
task_type: DESIGN
parent_branch: develop
base_commit: c398e8ab2b29335889c9e093369b80f2ebe8cd68
---

# Owner 决策记录 — intake-minimal-agent-runtime-v1

记录时间：2026-10-19（UTC）。本文件由 Discovery 在收到 Owner 明确决策后机械持久化，front-matter 为机器可读绑定字段。

## 批准的 Task

- task_id：`minimal-agent-runtime-design-v1`
- task_type：`DESIGN`
- parent_branch：`develop`
- base_commit：`c398e8ab2b29335889c9e093369b80f2ebe8cd68`
- Design Impact：`NEW`

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
- base_commit 有效性：`c398e8ab2b29335889c9e093369b80f2ebe8cd68` 为真实 commit，且是 `develop` 与 `origin/develop` 的当前 tip，`verify-base` 通过；
- DESIGN 初始化要求：handoff 绑定 `task_type=DESIGN`，与 DesignWorkflow 的 TaskBuilder 初始化字段（contract.status=NOT_REQUIRED、design.status=PENDING、初始 Handoff next_role=Architect）匹配；
- **未决冲突**：本 Intake 的 `intake.yaml` 锚定 base_commit=`b43cdfbf78e3eef626e2af6f59c7ef675196da60`（调查时的 Git Base，不可变），与本次批准的 base_commit 不一致；`discovery-handoff` Gate 要求 handoff/decision 的 base 与 Intake 记录相等，且 EntryWorkflow 规定「不以 Intake tip 替换原 Base」。因此 `taskbuilder-handoff.md` 在 base 确认前无法生成，详见 `discovery-report.md` 第二阶段执行记录。
