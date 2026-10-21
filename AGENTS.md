# AGENTS

本仓库使用 **Workflow V3**。Agent 进入仓库后，先按职责定位规则，再阅读宿主项目长期工程规范。

## Workflow 入口

执行 Workflow Task 前，根据职责读取：

1. 协作规范：`.agent/specs/AgentCollaborationSpecification.md`
2. 职责边界：`.agent/specs/Five-AgentResponsibilityBoundary.md`
3. 本角色 Prompt：`.agent/roles/<Role>.md`
4. 机器配置：`.agent/workflow.yaml`
5. 当前任务事实：`.agent/tasks/<task-id>/` 下的 `task.md` / `state.yaml` / `contract.md` 等

架构 Design（WHY / architecture）：

`docs/design/agent-workflow.md`

首次跨项目 Adoption：

`.agent/specs/WorkflowAdoption.md` + `.agent/templates/PROJECT_ADAPTATION.template.md`

`.agent/` namespace 索引见 `.agent/README.md`。

Workflow Engine（只读 Gate Validator `workflow-check`）是独立 Go Module，位于 `.agent/engine/`（`module workflow-v2-engine`），不依赖宿主业务 Go Module、不通过 `go.work`/`replace` 关联：

```text
# 构建本地 binary（进入 .agent/bin/，被 Git ignore）
go -C .agent/engine build -o ../bin/workflow-check ./cmd/workflow-check

# 运行（正式 Gate 机器入口优先 direct binary，保留 0/1/2 exit code）
.agent/bin/workflow-check gate <gate> <task>
```

Engine 测试与业务测试分开：Engine 用 `GOWORK=off go -C .agent/engine test ./...`；业务模块在宿主 repo 根按其自身方式测试（不含 Engine）。

注意：不要从历史 `.agent/tasks/*` 推断当前 Workflow 规则。当前 Workflow 权威来源是：

- `docs/design/agent-workflow.md`（Architecture）
- `.agent/specs/*`（Execution Specifications）
- `.agent/roles/*`（Role Instructions）
- `.agent/workflow.yaml`（Machine Configuration）

---

## 宿主项目工程规范

本仓库为 `framework-station/mini-agent-go`，Go 语言项目，业务代码尚未建立。

### 仓库与分支

- 共享集成分支：`develop`（`.agent/workflow.yaml` 的 `git.integration_branch`）。
- `main` 为稳定发布分支，不由 Agent 直接修改、提交或推送。
- 业务开发使用独立任务分支，从 `develop` 创建，最终按 Workflow V3 流程由 Owner 决定集成。
- Agent 的 Git 权限与禁止操作见 `.agent/specs/AgentCollaborationSpecification.md` §11。

### 工具链与验证

- Go 版本：仓库 CI 使用 `golang:1.24`；`.agent/engine/go.mod` 声明 `go 1.23.0`。
- Workflow Engine（独立 Module，与业务测试分开）：

```text
GOWORK=off go -C .agent/engine build ./...
GOWORK=off go -C .agent/engine vet ./...
GOWORK=off go -C .agent/engine test ./...
GOWORK=off go -C .agent/engine test -race ./...
```

- 业务模块：当前仓库根尚无 `go.mod`，也没有 `Makefile`。新增业务模块时，在本节 materialize 真实的 build / test 命令。

### CI

- `.cnb.yml` 在 `main` 与 `develop` 上触发。
- 现有 `pr-check` 阶段执行 `make test`；当前仓库没有 `Makefile`，该命令暂无对应目标（既有缺口，未在本轮 Workflow 部署中扩展 CI）。

### 共享资源

- 当前无已确认的 Shared Resource Kind（`.agent/workflow.yaml` 的 `resources: {}`），未创建 `.agent/registry/` 文件。
- 出现需要全局分配的资源（如 migration version、错误码域）时，按 `.agent/specs/AgentCollaborationSpecification.md` §12 先由 Owner 确认，再在 machine config 中声明对应 kind 与 Registry 路径。
