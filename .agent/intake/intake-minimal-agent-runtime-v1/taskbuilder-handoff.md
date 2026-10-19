---
intake_id: intake-minimal-agent-runtime-v1
owner_decision: APPROVED
parent_branch: develop
base_commit: "b43cdfbf78e3eef626e2af6f59c7ef675196da60"
task_type: DESIGN
task_id: minimal-agent-runtime-design-v1
---
# TaskBuilder Handoff — minimal-agent-runtime-design-v1

本 Handoff 由 Discovery 在 Owner 明确决策（含 2026-10-19 base 修正）后生成；front-matter 绑定 Git 基线与任务标识，供 TaskBuilder 机器校验（`workflow-check gate discovery-handoff`）。人读要点：

- 任务：`minimal-agent-runtime-design-v1`，task_type=DESIGN，Design Impact=NEW；
- 父分支 / Base：`develop` @ `b43cdfbf78e3eef626e2af6f59c7ef675196da60`（与 `intake.yaml` 原始锚点一致，未以 Intake tip 替换）；
- 批准范围与约束：见同目录 `owner-decision.md`（设计交付 8 项；不得提前实施业务代码）；
- 决策历史：原批准 base=`c398e8ab...` 已由 Owner 明确修正作废，历史保留于 commit `43ae7766626b309b15663d0f2c209a5032399fab`；
- TaskBuilder 初始化要求（DesignWorkflow §2）：schema 4、task_type=DESIGN、contract.status=NOT_REQUIRED、design.status=PENDING、approved_version/final_version=0、各 target 为空、review=NOT_REQUESTED、owner=PENDING、delivery=NOT_RUN、blocked.active=false、resources.reservations={}；初始 Task Handoff next_role=Architect、workflow=DESIGN；
- TaskBuilder 须在原受控 Intake 工作区运行 discovery-handoff 并回读原 Owner 批准，按原 Base 使用 workflow-git 核验基线、受控创建分支/Worktree，并按准确来源 SHA 将 Intake 四文件原字节带入新工作区。
