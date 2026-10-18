# PROJECT ADAPTATION

Status: TEMPORARY

Purpose:
Workflow V2 Project Adoption Workspace

Lifecycle:
DISCOVER → RESOLVE → MATERIALIZE → VERIFY → CLEANUP

Deletion Rule:
Only remove this file after ADOPTION READY.

---

## 1. Adoption Metadata

- Project: mini-agent-go
- Repository: https://cnb.cool/framework-station/mini-agent-go
- Date: 2026-10-18
- Current Branch: adoption/agent-workflow-v4.2.1（已由 Owner 授权合入 main）
- Workflow Version: 4.2.1（上游 commit 1fc5c6dc88fc7f417374e37e7354a3a6c4e07a65）

Adoption Status:

- READY

（2026-10-18 Owner 指令“合并到 main 并且发布”：D1/D2/D4 已解决，框架、配置、基线、资源与四流程入口检查通过。D3 属首个 Task 前置，不改变接入结论。）

> 这些状态只属于 Adoption 临时工作区。不要把它们加入正常 Workflow V2 state schema。

---

## 2. Detected Project Facts

由 Agent 在 DISCOVER 阶段填写。只记录真实检测到的事实，不臆造。

- Language: 尚无源码；仓库尚无 Go module（仅由仓库名与 CI 镜像 golang:1.24 表明 Go 方向）
- Go Module: 无（仓库根无 go.mod、无 go.work）
- Repository Model: 单仓库，`main` 为唯一分支，origin/HEAD → origin/main，适配前 HEAD=993f874e6910a9f6efc92512baf477e5d887d921
- Framework: 未检测到
- Database: 未检测到
- Cache: 未检测到
- Messaging: 未检测到
- Deployment: CNB 流水线 `.cnb.yml`（vscode 环境检查；PR 执行 `make test`；main push 镜像到 GitHub）

Branch Candidates:

- `main`（本地与 origin/main 同 SHA，适配前工作区干净）

Build System:

- 无 Makefile、无 go.mod；`.cnb.yml` 的 `make test` 当前无对应目标

Test System:

- 无业务测试；仅有本次安装的 `.agent/engine` 独立 Go module 测试

Migration Tool:
Migration Path:
Migration Version Strategy:

- 项目无迁移工具/路径/版本策略证据，不声明 migration resource kind

Existing Design Root:

- 无

Existing Workflow Assets:

- 适配前无 `.agent/`、无其他 Workflow 实现

---

## 3. Proposed Workflow Mapping

- Shared Integration Branch: `main`（唯一分支且为远端默认分支）
- Feature Branch Convention: 未建立；适配分支为 `adoption/agent-workflow-v4.2.1`
- Design Authority Root: 未建立
- Workflow Task Root: `.agent/tasks/`（首个真实 Task 由 Discovery/TaskBuilder 创建）
- Registry Root: `.agent/registry/`（当前无已确认共享资源，未创建文件）
- Machine Config (.agent/workflow.yaml): integration_branch=main + resources={}

Migration Registry: NOT_APPLICABLE

Error Code Registry: NOT_APPLICABLE

Validation Commands:

- `GOWORK=off go -C .agent/engine test ./...`
- `GOWORK=off go -C .agent/engine vet ./...`
- `GOWORK=off go -C .agent/engine build ./...`
- `.agent/bin/workflow-check config`
- `.agent/bin/workflow-git verify-base --parent main --base <完整SHA>`

---

## 4. Owner Decisions

只记录真正需要 Owner 决策的内容。每项使用：

### D1 集成分支确认

- Decision ID: D1
- Question: 是否确认 `main` 为 Workflow `git.integration_branch`？
- Detected Evidence: `git branch -a` 仅有 main 与 origin/main；origin/HEAD → origin/main；两 ref 同 SHA 993f874e6910a9f6efc92512baf477e5d887d921
- Options: main / 其他待建分支
- Recommendation: main（唯一既有约定）
- Owner Decision: 已确认 main（2026-10-18 Owner 明确授权合并到 main 并发布）
- Status: RESOLVED

### D2 适配分支集成授权

- Decision ID: D2
- Question: 是否批准将 `adoption/agent-workflow-v4.2.1` 合入 main 并推送，使其成为后续 Task 的可用基线？
- Detected Evidence: 适配提交仅新增 `.agent/`，未改 `.cnb.yml` 等既有文件
- Options: 批准合并 / 要求修改后合并 / 暂不合并
- Recommendation: Owner 按项目既有流程审阅后决定
- Owner Decision: 已授权（2026-10-18“合并到 main 并且发布”）；本提交经 --no-ff 合并至 main 并推送 origin/main
- Status: RESOLVED

### D3 真实业务验收命令

- Decision ID: D3
- Question: 后续 Task 的正式验收命令是什么？
- Detected Evidence: `.cnb.yml` PR 阶段调用 `make test`，但仓库无 Makefile 与 Go module，命令当前不可执行
- Options: 待业务代码落地后确认 / 现在就提供脚本
- Recommendation: 在首个业务 Task 前由项目确定，不得虚构
- Owner Decision: 未决（不阻断 ADOPTION_READY；阻断首个 Task 的正式交付）
- Status: OPEN_TASK_PREREQUISITE

### D4 共享资源种类

- Decision ID: D4
- Question: 项目是否需要声明 Shared Resource Kind（如 migration_version / error_code_domain）？
- Detected Evidence: 无迁移工具、无错误码域、无其他全局分配冲突证据
- Options: NONE / 声明具体 kind
- Recommendation: NONE（`resources: {}`），出现真实全局分配冲突时再增加
- Owner Decision: 已确认 NONE（随本次合并一并确认；出现真实全局分配冲突时另行声明）
- Status: RESOLVED

---

## 5. Materialization Plan

| Item | Final Decision | Authoritative Destination | Action | Status |
| --- | --- | --- | --- | --- |
| Workflow 版本与来源 | 4.2.1 / 1fc5c6dc88fc7f417374e37e7354a3a6c4e07a65 | `.agent/upstream.yaml` | 已创建 | DONE |
| 集成分支 | main | `.agent/workflow.yaml` → `git.integration_branch` | 已创建 | DONE |
| 共享资源 kind | NONE | `.agent/workflow.yaml` → `resources: {}` | 已创建 | DONE |
| 框架文件 | 官方原样 | `.agent/{engine,roles,specs,templates,...}` | 逐字节复制 | DONE |
| 宿主根 AGENTS.md | 不存在，不创建 | — | 不适用 | DONE |
| 业务验收命令 | 未确认 | 项目构建/测试配置（Owner 决定） | 待决 | OPEN（首个 Task 前置） |

---

## 6. Shared Resource Kinds 与 Registry Initialization

已确认：当前项目无共享资源种类 → `resources: {}`，不初始化任何 Registry 文件。

| Resource Kind | Registry Path | Namespace | Initial State | Authority |
| --- | --- | --- | --- | --- |
| NONE | — | — | — | — |

---

## 7. Validator Adaptation

workflow-check（`.agent/engine/`，独立 Module `workflow-v2-engine`）:

Compatibility:
- READY（未修改任何 Engine 源码）

Project Coupling Found:
- 无。Engine 无宿主 module import、无 go.work、无 replace；宿主当前也没有 Go module

Adaptation Applied:
- 无源码改动

Validation:
- `.agent/engine/go.mod` 存在（`module workflow-v2-engine`, `go 1.23.0`）— PASS
- `go -C .agent/engine build ./...` PASS
- 六个 CLI `go -C .agent/engine build -o ../bin/<cli> ./cmd/<cli>` PASS
- build 后 `git status --short` 不因 binary 变 dirty（`.agent/bin/` 被 ignore）— PASS
- `.agent/bin/workflow-check config` 读取 materialized `.agent/workflow.yaml` — PASS
- Engine 不依赖宿主业务 Go Module — PASS

---

## 8. Verification

- Workflow Design: PASS（官方 Spec 原样，未改）
- Agent Prompts: PASS（25 份 Role Prompt 可读，未定制）
- Task templates: PASS（`.agent/templates/` 原样）
- Git authority: PASS（`verify-base --parent main --base 993f874...` 返回 ok；本记录随合并提交进入 main 后，集成基线为包含 `.agent/` 的 main）
- Registry: NOT_APPLICABLE（无资源 kind）
- Validator: PASS（test/vet/build + 六 CLI）
- Machine Config: PASS（`workflow-check config`）
- Project build/test mapping: BLOCKED（无业务代码与验收命令，首个 Task 前置）
- Owner decisions: D1/D2/D4 RESOLVED；D3 OPEN（Task 级前置）
- Materialization completeness: 框架与配置已物化；业务验收命令待项目提供

---

## 9. Cleanup Check

- [x] All required facts resolved（可检测事实已记录）
- [x] Owner decisions resolved（D1/D2/D4 已解决；D3 属 Task 级前置）
- [x] Facts materialized（业务验收命令待项目提供，已记录在 D3）
- [x] Registry initialized（NONE，合法空配置）
- [x] Validator verified
- [x] No blocker（接入无阻塞；首个 Task 仍缺真实验收命令）
- [x] No critical information exists only in this temporary file

只有全部通过：PROJECT_ADAPTATION.md may be deleted.

---

## 10. Adoption Result

RESULT: READY

Remaining Issues:

- D3：无业务代码、无 Go module、无真实验收命令（CI 的 `make test` 无对应目标），阻断首个 Task 的正式交付，不阻断接入
- 首个 Task 仍需真实 Discovery 与 Owner 批准；本记录不代表任何 Task 被批准

Cleanup:

- 保留本临时文件作为接入决策记录；其事实已在 `.agent/workflow.yaml`、`.agent/upstream.yaml` 与提交历史中长期留存，Owner 可随时删除

PROJECT_ADAPTATION.md: RETAINED
