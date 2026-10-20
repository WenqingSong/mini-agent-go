# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| 任务 | `agent-project-docs-v1`（T10，NORMAL，`Design Impact = NONE`，`contract.status = NOT_REQUIRED`） |
| 任务基线 | `d54c912540c090fa7c80961cb6a7cbee8e6ec9a0`（`origin/develop`） |
| **本轮被审查对象（C1，第二轮）** | `05a49e8c23b86190f63571c3485176d8db6bc718` — `docs(project): 修正 AI 使用记录的实现 commit 口径（CLEAN-001）` |
| C2（metadata） | `976fc0283dc5b96615d5b423adab3fda665a5627` — 只改 `state.yaml`（`review.status=PENDING`、`review.target=C1`） |
| 上一轮审查对象（已失效） | `480831d5040a5b481be87ee6ff576737662a9c15`（首轮交付；因 CLEAN-001 P2 判 `CHANGES_REQUIRED`，见 `420d078`） |
| 审查时工作区 | `git status --short` 为空；`HEAD` = C2 = `origin/feat/agent-project-docs-v1`（已推送、同步）；`git diff --stat 05a49e8 HEAD -- README.md docs` 为空 → 本轮审查的文档 == C1 |
| C1 相对上一轮的实质变更 | `git diff --name-status 420d078 05a49e8` = 仅 `M docs/ai-assisted-development.md`（4 处：字段说明 1 行、两行数据、补充说明 1 行）；`README.md` 与 `docs/design/*` 零变更，无 `*.go` 变更 |
| 累计变更集（基线 → C1） | `README.md`（377 行新增）、`docs/ai-assisted-development.md`（110 行）、`.agent/tasks/agent-project-docs-v1/{task.md,state.yaml,findings.md,core-logic.md,owner-decision.md,delivery.md}`；**无** `*.go` / `go.mod` / `Makefile` / `.cnb.yml` / `docs/design/*` |
| 关键配置/版本事实 | `go.mod` = `go 1.24` + 零 `require`；`Makefile` = `test/build/vet/test-engine`；`.cnb.yml` `pr-check` = `make test`；`.agent/engine/go.mod` = `go 1.23.0` + `gopkg.in/yaml.v3`；`resources: {}`（无 Registry 需求） |

## Result

CLEAN

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS | 首轮实跑全部命令（`go build ./...`、`go vet ./...`、`make test` 等价命令、`make test-engine`、`go build -o` + 启动）；本轮 `README.md` 相对已执行版本字节未变（`git diff --stat 480831d 05a49e8 -- README.md` 为空），本轮复跑 `go build ./...` / `go vet ./...` 退出 0。命令与 `Makefile`、`go.mod`（`go 1.24`、零 `require`）、`.cnb.yml`（`pr-check: make test`）一致 |
| AC-002 | PASS | 本轮复跑 `GOPROXY=off GOFLAGS=-mod=mod go test -count=1 ./...` → 12 包全部 `ok`，零网络；README 的「不触网 / 不依赖真实 Provider」表述与 Engine 首次拉依赖的例外说明均保留 |
| AC-003 | PASS | 首轮逐条定位（分层 §2、职责 §3、终态 §5 + `core/error.go` `StatusOf`、双预算 §6 + `agent/run.go:73/163`、提交边界 §7 + §11.1 `INV-001`、`finish_reason` `agent/run.go:144-160`）；本轮 README 未变。Todo 注册、上下文压缩、持久化仍明确写作未实现 |
| AC-004 | PASS | 首轮实跑：`/healthz` → `200 {"status":"ok"}`、`POST /v1/sessions` → `201` + 32 位 hex、`POST /v1/chat`（占位 Provider）→ `502 provider_failure`，另核 `404`/`405 Allow`/`409` 与 `SIGTERM` 退出；本轮 README 与生产代码均未变，结论沿用 |
| AC-005 | PASS | 12 个变量与 `cmd/mini-agent/main.go:33-44` 一致、默认值与 `httpapi/config.go` / `service/runner.go` 一致；密钥扫描对新增文档无命中；示例值全为占位符。本轮无相关改动 |
| AC-006 | **PASS（本轮修复）** | 机械核对「实现 commit 列最后一个 SHA == `state.yaml` `review.target`」：10 个已完成任务全部 `OK`（含本轮补入的 `61fa094`、`8e4d2fd`），仅本任务自引用行无 SHA（文档无法包含自身 commit，改为指向 `state.yaml`，属不可避免且可核对）。9 个任务的 `review.target` / `owner.review_target` / `delivery.review_target` 三者逐项相同，与新增字段说明「与 `owner.review_target` / `delivery.review_target` 相同」一致。两处修正与任务 `findings.md` 原文对齐：`contextmgr` 的 C1/C1'/C1'' 标号、`runtime-observability` 的「CLEAN-001 导致 `CHANGES_REQUIRED`」 |
| AC-007 | PASS | 限制章节覆盖 AC 要求的 7 项，且与 `http-api.md` §10 / `runtime-architecture.md` §14 逐条一致；本轮未改动 |

## Verification

环境：Linux，`go1.24.1 linux/amd64`，无外部服务。本轮全部验证在 Review Target 工作区只读执行，无 Mutation、无残留。

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 修复是否只作用于 Finding 指定范围 | PASS | `git diff --name-status 420d078 05a49e8` = 仅 `M docs/ai-assisted-development.md`；4 处改动逐行核对：字段说明、`contextmgr` 行、`observability` 行、补充说明 |
| 原 Finding 触发条件是否消除 | PASS | 机械脚本逐行取「实现 commit」列最后一个 SHA 与 `state.yaml` `review.target` 比对 → 10/10 `OK`（首轮为 8/10） |
| 新增事实是否可核对 | PASS | `61fa094`、`8e4d2fd`、`d92ada3`、`bb991c1`、`08cd0ac`、`5633001`、`707cb71`、`b3d2146`、`03dced5` 全部 `git cat-file` 命中且标题与行内用途相符；`contextmgr` / `observability` 的「已失效」标注与各自 `findings.md` 原文一致 |
| 新字段说明的自洽性 | PASS（附 P3） | 「最后一个 SHA = `review.target`」在 10 行成立；「必须标明已失效」在 `contextmgr` / `observability` 两行成立，但 `llm` / `tool` 两行未标注已失效的首轮 C1 → CLEAN-003（P3，不阻塞） |
| `go build ./...` / `go vet ./...` | PASS | 本轮复跑，退出 0 |
| `GOPROXY=off GOFLAGS=-mod=mod go test -count=1 ./...` | PASS | 本轮复跑，12 包全 `ok` |
| `go test -race` | NOT_EXECUTED | 文档任务、无并发相关变更；不构成本任务 AC 证据缺口（README 已如实写明 `-race` 未进 CI） |
| 生产代码/配置/设计零变更 | PASS | `git diff --name-status d54c912 05a49e8` 无 `*.go`、`go.mod`、`Makefile`、`.cnb.yml`、`docs/design/*` |
| Registry ↔ Contract ↔ 实现 | N/A | `resources: {}` |
| 工作区 / 远端同步 | PASS | `git status --short` 为空；`HEAD` == `origin/feat/agent-project-docs-v1` |
| 上一轮 `cleaner-start` 的 `handoffReady` 缺口 | PASS（已消除） | 上一轮审查时 C1/C2 未推送（`origin` = `d54c912`）；本轮 `origin` = C2，Gate 前提成立 |

## Findings

### CLEAN-001：AI 使用记录把失效/被替换的实现 commit 当作交付实现

- Severity：P2
- Status：**CLOSED**（本轮复审关闭）
- Location：`docs/ai-assisted-development.md:35`（字段说明）、`:45`（`agent-context-manager-v1` 行）、`:46`（`runtime-observability-v1` 行）
- AC / Invariant：AC-006
- 复审结论：
  - 字段说明已改为「实现提交链 + 格内最后一个 SHA = 最终接受快照 = `state.yaml review.target`（= `owner.review_target` / `delivery.review_target`）」，并明确「被取代的首轮 C1 必须标明已失效」。
  - `runtime-observability-v1` 行：`08cd0ac`（首轮 C1，已失效：CLEAN-001 导致 `CHANGES_REQUIRED`），修复 `8e4d2fd`（= `review.target`）——与 `.agent/tasks/runtime-observability-v1/findings.md:12`、`state.yaml` 逐字一致。
  - `agent-context-manager-v1` 行：`d92ada3`（首轮 C1，已失效）+ `bb991c1`（C1'）+ `61fa094`（C1''，= `review.target`）——与该任务 `findings.md:11-14` 的标号与 `state.yaml` 一致。
  - 原触发条件（按 §5 复核得到与 `state.yaml` 不同的答案）已消除：机械脚本 10/10 `OK`。
- Evidence：`git diff 420d078 05a49e8 -- docs/ai-assisted-development.md`；机械比对脚本输出（10 行 `OK`）；`git log -1 --format='%h %s' 61fa094 8e4d2fd 08cd0ac`；各任务 `state.yaml` 三元组逐项相同。
- 关闭依据：Cleaner 独立重跑原触发路径，不是 Coder 声明。

### CLEAN-002：C1 提交了其他角色 owned 的任务产物占位文件，超出 Scope 列举范围

- Severity：P3
- Status：OPEN（本轮未处理，非本轮修复范围；交 Owner 决定）
- Location：`480831d` 新增的 `.agent/tasks/agent-project-docs-v1/{findings.md,core-logic.md,owner-decision.md,delivery.md}`
- AC / Invariant：Task Scope「本任务自身产物：`task.md` / `state.yaml` 初始化文件」；`.agent/specs/AgentCollaborationSpecification.md` §4 默认维护者
- Trigger：`git show --stat 480831d`
- Actual：首轮 C1 创建了 4 个其他角色 owned 的占位文件
- Expected：其他角色 artifact 由对应角色在自己阶段创建
- Impact：低——占位内容不改变结论；本轮的 `findings.md` / `core-logic.md` 已由 Cleaner 覆盖为真实内容，`owner-decision.md` / `delivery.md` 仍为占位，待对应角色写入
- Evidence：`git show --stat 480831d`（8 个新增文件中 4 个为占位）；当前 `owner-decision.md` / `delivery.md` 内容仍为「等待…填写」
- Required Fix Boundary：由 Owner 决定是否处理；不得删除 Cleaner/OwnerGate/Deliverer 在各自阶段真实产出的内容

### CLEAN-003：新字段口径要求「被取代的首轮 C1 必须标明已失效」，但 `llm` / `tool` 两行未标注

- Severity：P3
- Status：OPEN
- Location：`docs/ai-assisted-development.md:35`（规则本身）、`:41`（`llm-openai-client-v1` 行）、`:42`（`tool-registry-executor-v1` 行）
- AC / Invariant：AC-006（记录内部自洽、可核对）
- Trigger：按 `:35` 的字段说明逐行检查「首轮 C1 是否被取代、是否标注已失效」
- Actual：`llm-openai-client-v1` 行写 `` `5633001`，修复 `b3d2146`（CLEAN-002） ``；`tool-registry-executor-v1` 行写 `` `707cb71`，修复 `03dced5` ``。两者的首轮 C1 均已被后续提交取代（`llm-openai-client-v1/findings.md:8` 明确 `5633001` 的 CLEAN 已被 Owner `REJECTED` 作废；`tool-registry-executor-v1/findings.md:8,73,86` 记 `707cb71` 的 CLEAN-001/002 由 `03dced5` 修复），但格内未标注「已失效/已被取代」。
- Expected：与 `contextmgr` / `observability` 两行一致，标明首轮 C1 已失效
- Impact：低——两行的最后一个 SHA 均等于 `review.target`，接受快照仍唯一可判；仅表述未完全遵守本轮新写入的规则，可能让读者把 `5633001` / `707cb71` 误读为交付实现的一部分
- Evidence：`docs/ai-assisted-development.md:41,42`；`.agent/tasks/llm-openai-client-v1/findings.md:8`；`.agent/tasks/tool-registry-executor-v1/findings.md:8,73,86`
- Required Fix Boundary：只在这两行补「首轮 C1，已失效」类标注（或调整 `:35` 的规则为「如有更强的后续修复则酌情标注」），不改动 SHA 事实、不改 `state.yaml`

## 复审范围声明

- 本轮定位与验证原 Finding 的触发条件、修复 Diff、新增事实与回归风险；未重新逐行审查 `README.md` 与 `docs/design/*` 内容（相对首轮 `README.md` 字节未变；`docs/design/*` 全程只读未改）。
- 未修改生产代码、测试、`task.md`、`docs/design/*`、Registry、`owner-decision.md`、`delivery.md`。本轮无临时 Mutation，无 `/tmp` 残留，`git status --short` 在写入本文件前为空。
