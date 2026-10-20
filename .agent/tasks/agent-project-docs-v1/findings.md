# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 | `agent-project-docs-v1`（T10，NORMAL，`Design Impact = NONE`，`contract.status = NOT_REQUIRED`） |
| 任务基线 | `d54c912540c090fa7c80961cb6a7cbee8e6ec9a0`（`origin/develop`；task.md 记录的基线） |
| 被审查的 implementation Evidence Commit（C1） | `480831d5040a5b481be87ee6ff576737662a9c15` — `docs(project): 交付工程 README 与 AI 辅助开发记录（agent-project-docs-v1）` |
| C2（metadata） | `ef49905bd11318928301a695f4bc1ae4d4b053b5` — 只把 `state.review.status` 置 `PENDING`、`review.target` 绑定 C1 |
| 审查时工作区 | `git status --short` 为空；`HEAD` = C2，`git diff --stat 480831d HEAD -- README.md docs` 为空 → 本轮审查的文档 == C1 |
| C1 变更范围 | `git diff --name-status d54c912 480831d` = 新增 `README.md`（377 行）、`docs/ai-assisted-development.md`（110 行）、`.agent/tasks/agent-project-docs-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；**无** `*.go` / `go.mod` / `Makefile` / `.cnb.yml` / `docs/design/*` 变更 |
| 任务前已有修改的区分 | 基线 `d54c912` 上工作区 clean，无他人遗留修改；本任务全部改动都在这两个文档与任务目录内 |
| 关键配置/版本事实 | `go.mod` = `go 1.24` + 零 `require`；`Makefile` 目标 `test/build/vet/test-engine`；`.cnb.yml` `pr-check` 执行 `make test`；`.agent/engine/go.mod` = `go 1.23.0` + `gopkg.in/yaml.v3`；无 migration / 错误码 Registry 需求（`resources: {}`） |
| 关键流程事实 | 审查时 `origin/feat/agent-project-docs-v1` 仍指向 `d54c912`（`git branch -vv` 显示 ahead 2），C1/C2 未推送；Coder 出口 `cleaner-start` 的 `handoffReady`（local == remote）前提当时不成立。本角色出站 push 已把 C1/C2 一并发布，该状态不再存在 |

## Result

CHANGES_REQUIRED

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | README 命令逐条实跑：`go build ./...`（退出 0）、`go vet ./...`（退出 0）、`make test` 等价命令全绿、`make test-engine` = `ok workflow-v2-engine/...`、`go build -o /tmp/...` + 启动命令成功进入监听。命令文本与 `Makefile`（`build`/`test`/`test-engine`）、`go.mod`（`go 1.24`、零 `require`）、`.cnb.yml`（`pr-check: make test`）一致；Engine 模块依赖说明与 `.agent/engine/go.mod` 一致 |
| AC-002 | PASS | `GOPROXY=off GOFLAGS=-mod=mod go test -count=1 ./...` → 12 个包全部 `ok`，零网络；README 明确写「不触网」「不依赖真实 Provider」，Engine 首次拉依赖的例外也已如实标注，未列为通过前提 |
| AC-003 | PASS | 抽查 README 架构/Agent Loop 论断并逐条定位：分层与依赖方向 → `runtime-architecture.md` §2（`agent`/`httpapi`/`service`/`cmd` 行逐条对应）；模块职责表 → §3；终态 → §5 + `core/error.go` `StatusOf`；双预算 → §6 + `agent/run.go:73`/`:163`；提交边界 → §7 + §11.1 `INV-001`（原文即「提交原子性：`COMPLETED` 才提交」）；`finish_reason` 判定 → `agent/run.go:144-160`。Todo 注册、上下文压缩、持久化均明确写作未实现，无漂移 |
| AC-004 | PASS | 实跑：`healthz` → `200 {"status":"ok"}`；`POST /v1/sessions {}` → `201 {"session_id":"<32 位 hex>"}`；`POST /v1/chat`（占位 Provider `http://127.0.0.1:9/v1`）→ `502 {"error":{"code":"provider_failure","message":"模型服务调用失败"}}`，与 README 预写结果一致；补充核对 `404 not_found`、`405 method_not_allowed` + `Allow: POST`、重复 `session_id` → `409 session_exists`；`SIGTERM` 后进程正常退出。字段名、状态码、封套与 `http-api.md` §3/§4 一致 |
| AC-005 | PASS | README 列出的 12 个变量与 `cmd/mini-agent/main.go:33-44` 完全一致；默认值逐项核对（`127.0.0.1:8080` / `60s` / `10s` / `1<<20` / `8` / `8` / `30s`，`TOOL_TIMEOUT <= 0` 表示不设）。`git grep -nE '(sk-|Bearer [A-Za-z0-9]{16,})'` 对新增文档无命中；示例值为 `REPLACE_WITH_*` / `llm.example.invalid` / `127.0.0.1`，无真实凭证与内网地址 |
| AC-006 | **FAIL** | 记录主体可核对：33 个引用 SHA 全部 `git cat-file -e` 通过，任务/契约/决策文件路径全部存在，角色提示词 6 个路径存在且职责描述与原文一致，「无记录」项（对话原文、模型名、真实 Provider 调用、T08）显式标注。但两行「实现 commit」与 `state.yaml` 的 `review.target` 不一致，其中一行把已被 Cleaner 判定失效的提交呈现为交付实现 → 见 CLEAN-001 |
| AC-007 | PASS | README「当前限制与未验证事项」覆盖 AC 要求的 7 项（真实 Provider 未验收、Todo 未注册、`TokenBudget=0`、内存会话重启丢失、无流式/TLS/CORS/限流/metrics、`-race` 未进 CI、`cancelled` 不承诺可达），并与 `http-api.md` §10、`runtime-architecture.md` §14 逐条对应；额外条目（`go get` 未验证、`HeuristicEstimator` 未校准、`session_expired` 不触发）均可在文档或代码中找到依据，无新增未证实结论 |

## Verification

环境：Linux，`go1.24.1 linux/amd64`，无外部服务；未改动被审查工作区，临时产物均在 `/tmp` 并已清理。

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| `go build ./...` | PASS | 退出 0 |
| `go vet ./...` | PASS | 退出 0 |
| `GOPROXY=off GOFLAGS=-mod=mod go test -count=1 ./...` | PASS | 12 个包全部 `ok`（含 `httpapi` 1.146s、`session` 0.204s） |
| `make test-engine` | PASS | `ok workflow-v2-engine/cmd/workflow-check`、`ok workflow-v2-engine/internal/workflow` |
| AC-004 三条 curl + 路由/冲突补充 | PASS | 见 AC-004 行；真实进程、默认 `127.0.0.1:8080` |
| `SIGTERM` 优雅关闭 | PASS | 进程在 1.5s 内退出，无遗留监听 |
| README「`go run` 不转发信号」实测断言 | PASS | `go run ./cmd/mini-agent` 后对 `go run` PID 发 SIGINT，子进程仍在监听且 `/healthz` 仍返回 `200`；与 README 第 99 行表述一致（随后已终止全部子进程） |
| 引用 SHA 存在性（33 个） | PASS | `git log -1 --format='%h %s' <sha>` 全部命中且标题与表内用途相符 |
| 引用任务/角色/Design 路径存在性 | PASS | 10 个任务目录 + 6 个角色文件 + `docs/design/*` 全部存在；`runtime-architecture.md` §2/§3/§5/§6/§7/§10/§11.1/§14、`http-api.md` §3/§4/§8/§9/§10 小节号全部存在 |
| README 引用的 26 个源码路径 | PASS | 逐个 `[ -f ]` 通过（含 `agent/fake_test.go`、`httpapi/fake_test.go`） |
| 「测试无真实 Provider 开关」 | PASS | 全仓 `*_test.go` 无 `os.Getenv`、无 `go:build` tag |
| 密钥/凭证扫描 | PASS | 新增两份文档无 `sk-` / 长 Bearer / 真实内网地址 |
| `git grep "T01"/"T02"` 编号漂移说法 | PASS | `llm-openai-client-v1/owner-decision.md:25` 称其 T01；`agent-context-manager-v1/task.md:28` 称 Provider 客户端 T02；`builtin-agent-tools-v1/contract.md:31` 称 T02 = `tool-registry-executor-v1`；Agent Loop 被预留 T01、实际 T07 → 文档 §1.2 说法成立 |
| 表内「实现 commit」↔ `state.yaml review.target` | **FAIL** | 8/10 行一致；`runtime-observability-v1`、`agent-context-manager-v1` 两行不一致（CLEAN-001） |
| `go doc ./agent` / `go doc ./httpapi` | PASS | 两条命令可用（README 文档索引给出的自查看法成立） |
| 生产代码/配置零变更 | PASS | `git diff --name-status d54c912 480831d` 无 `*.go`、`go.mod`、`Makefile`、`.cnb.yml`、`docs/design/*` |
| 工作区与远端一致性（审查对象完整性） | NOT_VERIFIED（当时） | 审查时 C1/C2 未 push（`origin` = `d54c912`）；本角色出站 push 后 C1/C2 一并发布，`handoffReady` 恢复 |
| Registry ↔ Contract ↔ 实现 | N/A | `resources: {}`，本任务不涉及全局唯一资源（无 migration version / 错误码域） |
| Release/Race | NOT_EXECUTED | 文档任务无并发与运行时行为变更；`go test -race` 未在本轮执行，不构成 AC 证据缺口 |

## Findings

### CLEAN-001：AI 使用记录把失效/被替换的实现 commit 当作交付实现

- Severity：P2
- Status：OPEN
- Location：`docs/ai-assisted-development.md:46`（`runtime-observability-v1` 行）、`docs/ai-assisted-development.md:45`（`agent-context-manager-v1` 行）；对照 `.agent/tasks/runtime-observability-v1/state.yaml` `review.target`、`.agent/tasks/agent-context-manager-v1/state.yaml` `review.target`
- AC / Invariant：AC-006（每条使用记录都能在仓库内定位到可核对证据）；同文件 §2 字段说明「实现 commit = 最终被 Owner 接受的实现」与 §2 补充「上表只列最终被 Owner 接受的实现 commit 与必要的修复 commit」
- Trigger：按该文件 §5「复核方式」执行 `cat .agent/tasks/runtime-observability-v1/state.yaml`，或直接比对表内「实现 commit」列与各任务 `review.target`
- Actual：
  - `runtime-observability-v1` 表内写 `08cd0ac`；`state.yaml` `review.target` = `8e4d2fd`（`test(runlog): 修复 CLEAN-001/002 …`），且该任务 `findings.md:12` 明确记 `08cd0ac` 为「首轮 C1（**已失效**）」——它正是被 Cleaner 判定 `CHANGES_REQUIRED` 的提交。
  - `agent-context-manager-v1` 表内写 `d92ada3` + 修复 `bb991c1`；`state.yaml` `review.target` = `61fa094`（`test(contextmgr): 补充预算临界点与保留下限回归测试（CLEAN-005 收尾）`），该任务 `findings.md:11-14` 记为 C1''，`bb991c1` 后的第三轮复审对象。
  - 其余 8 行的实现 commit 与各自 `review.target` 一致（如 `b3d2146`、`03dced5`、`3ce1869`），说明该列口径本应是最终接受快照。
- Expected：「实现 commit」列对每个任务给出最终被 Owner 接受的实现快照（= `state.yaml review.target`），与字段说明及另外 8 行一致；若保留首轮 C1，必须同时标明其已被后续提交取代/失效。
- Impact：本任务的核心价值是「可回溯的 AI 使用记录」。当前记录把一个 Cleaner 已判定失效的提交呈现为交付实现，读者按文档自带的复核步骤会得到与 `state.yaml` 不同的答案，两处证据无法对齐；对 `runtime-observability-v1` 更是把「被拒绝的实现」写成「交付实现」。
- Evidence：
  - `git log -1 --format='%h %s' 08cd0ac 8e4d2fd 61fa094` → `08cd0ac feat(runlog): 交付结构化运行日志…`、`8e4d2fd test(runlog): 修复 CLEAN-001/002 输入不可变与 Run 结果隔离断言`、`61fa094 test(contextmgr): 补充预算临界点与保留下限回归测试（CLEAN-005 收尾）`
  - `cat .agent/tasks/runtime-observability-v1/state.yaml` → `review.target: "8e4d2fd"`、`owner.review_target: "8e4d2fd"`、`delivery.review_target: "8e4d2fd"`
  - `cat .agent/tasks/agent-context-manager-v1/state.yaml` → `review.target: "61fa094…"`
  - `.agent/tasks/runtime-observability-v1/findings.md:12` → `08cd0ac` 标注「首轮 C1（已失效）」
  - `.agent/tasks/agent-context-manager-v1/findings.md:11-14` → 历史审查对象 C1=`d92ada3`、C1'=`bb991c1`、C1''=`61fa094`
- Required Fix Boundary：只修正这两行「实现 commit」的事实与口径，使其与对应任务 `state.yaml` 的 `review.target` 一致（可保留首轮 C1 但必须标明已失效/已被取代）；同时消除「字段说明 = 交付给 Cleaner 的 C1」与「补充说明 = 最终被接受的实现」之间的自相矛盾。不得改写 `state.yaml`、不得新造 commit，也不得为了对齐而改动 `docs/design/*`。

### CLEAN-002：C1 提交了其他角色 owned 的任务产物占位文件，超出 Scope 列举范围

- Severity：P3
- Status：OPEN
- Location：`480831d` 新增的 `.agent/tasks/agent-project-docs-v1/{findings.md,core-logic.md,owner-decision.md,delivery.md}`
- AC / Invariant：Task Scope「本任务自身产物：`agent-project-docs-v1` 任务目录下 `task.md` / `state.yaml` 初始化文件」；`.agent/specs/AgentCollaborationSpecification.md` §4 文件默认维护者（`findings.md`/`core-logic.md` 属 Cleaner，`owner-decision.md` 属 OwnerGate，`delivery.md` 属 Deliverer）
- Trigger：`git show --stat 480831d`
- Actual：Coder 在 C1 内创建了这四个占位文件（内容为「当前没有 Findings。」「等待 Cleaner 审查后填写。」等）
- Expected：任务目录只初始化 Scope 声明的 `task.md` / `state.yaml`；其他角色 artifact 由对应角色在自己阶段创建
- Impact：低——占位内容不改变任何审查结论，但造成 owned artifact 归属混淆与无关 diff，并使「Cleaner 是否已审查」在 `findings.md` 历史中出现歧义
- Evidence：`git show --stat 480831d` 列出 8 个新增文件，其中 4 个为其他角色 owned 的占位；`git show 480831d:.agent/tasks/agent-project-docs-v1/findings.md` = 「# Cleaner Findings\n\n当前没有 Findings。」
- Required Fix Boundary：由 Owner 决定是否处理；若处理，只清理不属于 Coder/Cleaner 当前阶段的占位文件，不得删除 Cleaner/OwnerGate/Deliverer 在各自阶段真实产出的内容。

## 审查范围声明

- 已核对：Goal、Scope、Out of Scope、AC-001—AC-007、Task 的 Verification 各条、`docs/ai-assisted-development.md` 全部引用、README 全部可执行命令与关键论断。
- 未核对/不在本轮：`docs/design/*` 内容本身的正确性（本任务只读不改）、真实 Provider 端到端行为（Task Out of Scope，且属 Owner 后续决策）。
- 未修改生产代码、测试、`task.md`、`docs/design/*`、Registry。所有临时验证（进程、二进制、日志）均落在 `/tmp`，已清理，`git status --short` 在写入本文件前为空。
