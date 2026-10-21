# Task: 工程文档与 AI 辅助开发记录（agent-project-docs-v1，T10）

Task 编号：T10（Owner 2026-10-21 需求；前置 T08）。
Complexity：NORMAL。Design Impact：NONE。

## Goal

让一个此前没有参与本项目的 Go 开发者，只依赖仓库内文档就能完成本地构建、离线测试、启动服务、调用 HTTP API，并清楚知道项目架构、Agent Loop、测试模式与真实 Provider 模式的差别，以及当前尚未验证的能力边界。

## Scope

- 新增仓库根 `README.md`，覆盖：
  - 项目定位与当前能力范围（单进程 Agent Runtime + HTTP JSON API）。
  - 快速开始：安装前提（Go 1.24）、构建、离线测试、启动、停止；命令必须逐条可执行。
  - 架构总览：分层与依赖方向、核心模块职责（`core` / `llm` / `tool` / `builtin` / `session` / `contextmgr` / `runlog` / `agent` / `httpapi` / `service` / `cmd`），并指向 `docs/design/runtime-architecture.md`。
  - Agent Loop：一次 `Run` 的生命周期、双预算（`MaxLLMCalls` / `MaxToolCalls`）、终态判定与提交边界（`COMPLETED` 整轮提交、其余终态整轮丢弃）。
  - HTTP API：三个端点（`POST /v1/sessions`、`POST /v1/chat`、`GET /healthz`）的请求/响应形状、错误封套、常用错误码与状态码，附可复制 `curl` 示例。
  - 环境变量：完整变量表（必填性、默认值、含义），并提供可复制的示例块；示例值一律为占位符。
  - 测试模式与真实 Provider 模式的区别：测试全部使用 `httptest` / Fake Provider，不触网；真实 Provider 模式需要真实 `MINI_AGENT_LLM_BASE_URL` / `MINI_AGENT_MODEL`（可选 `MINI_AGENT_LLM_API_KEY`）。
  - 当前功能限制与未验证事项（见 AC-007）。
  - 文档索引：`docs/design/*`（权威契约）、`AGENTS.md`（工程规范）、`.agent/`（WorkflowV3 与任务记录）。
- 新增 `docs/ai-assisted-development.md`，整理：
  - AI 辅助开发使用的角色提示词来源（`.agent/roles/*.md`）与 Owner 任务需求来源（`.agent/tasks/*/task.md`）。
  - 使用记录：按任务（T00—T09 及本任务）记录「需求 → Contract/决策 → 实现 commit」，每条附证据路径与 commit SHA。
  - 技术决策记录：`docs/design/*` 中已固化的关键取舍与 `.agent/tasks/*/contract.md` / `owner-decision.md` 的对应关系。
- `README.md` 与 `docs/design/*` 的分工：README 只做「怎么用、怎么跑、怎么理解」的入口性说明，长期权威契约仍以 `docs/design/*` 为准；README 与 `docs/design/*` 冲突时修正 README，不得改写 `docs/design/*`。
- 本任务自身产物：`agent-project-docs-v1` 任务目录下 `task.md` / `state.yaml` 初始化文件。

## Out of Scope

- 不修改任何生产代码与测试（`*.go`），不修改 `go.mod` / `Makefile` / `.cnb.yml`。
- 不修改 `docs/design/*`（长期设计事实源）、`AGENTS.md`、`.agent/specs/*`、`.agent/roles/*`、`.agent/workflow.yaml`、`.agent/registry/*`。
- 不新增 `.env`、`.env.example` 或任何配置文件；环境变量示例只在 README 中以可复制代码块呈现。
- 不引入文档生成工具、静态站点、图示工具或任何新依赖。
- 不执行真实 Provider 端到端验收（不配置真实 API Key、不发起真实模型调用、不把未执行的验收写成已通过）。
- 不补齐 T08 能力（Todo 端口生产适配、生产 LLM `Summarizer`），不新增端点、工具或配置项。
- 不改版本号、不写 CHANGELOG、不重写 `docs/design/*` 为教程。
- 不做源码级 API 全量参考（以 `go doc` 替代）。

## Milestone

Milestone: M10-agent-project-docs

## Acceptance Criteria

- [ ] AC-001：给定任务基线代码与 Go 1.24 环境，读者按 README「快速开始」顺序执行构建、测试、启动命令时逐条成功；README 出现的每条命令都能在 `Makefile` 目标或 `go` 子命令中找到对应实现，且与 `Makefile`、`go.mod`（`go 1.24`、零 `require`）、`.cnb.yml`（`pr-check` 使用 `make test`）不矛盾。
- [ ] AC-002：给定离线环境（`GOPROXY=off`，不访问任何网络），按 README 的测试指引执行离线测试命令时全部通过；README 明确说明测试不依赖网络、不依赖真实 Provider，且不得把需要网络或真实 Provider 的步骤列为「验证通过」的必需步骤。
- [ ] AC-003：给定 README 的架构与 Agent Loop 章节，其中每个关键论断（分层与依赖方向、模块职责、Run 生命周期与终态、提交边界、预算语义）都能在 `docs/design/runtime-architecture.md`、`docs/design/http-api.md` 或代码符号（文件 + 标识符）中找到对应证据；不得把尚未实现的能力（尤其是 Todo 工具生产注册、上下文压缩、持久化）表述为已实现。
- [ ] AC-004：给定 README 的 HTTP API 章节，按示例在本地（默认 `127.0.0.1:8080`、无真实 Provider）启动服务后，`GET /healthz` 返回 `200` 与 `{"status":"ok"}`；`POST /v1/sessions` 返回 `201` 并给出 `session_id`；`POST /v1/chat` 返回文档中预先写明的无 Provider 失败语义（`502` + `provider_failure` 错误封套）。示例中的字段名、状态码、错误封套形状与 `docs/design/http-api.md` §3/§4 一致。
- [ ] AC-005：给定 README 的环境变量小节，必须覆盖 `cmd/mini-agent/main.go` 读取的全部 12 个变量（`MINI_AGENT_LISTEN_ADDR` / `AUTH_TOKEN` / `LLM_BASE_URL` / `LLM_API_KEY` / `MODEL` / `SYSTEM_PROMPT` / `REQUEST_TIMEOUT` / `SHUTDOWN_GRACE` / `MAX_REQUEST_BYTES` / `MAX_LLM_CALLS` / `MAX_TOOL_CALLS` / `TOOL_TIMEOUT`），必填性与默认值与代码一致；示例中所有凭证位为占位符，仓库新增内容中不出现任何真实密钥、Token 或内网地址。
- [ ] AC-006：给定 `docs/ai-assisted-development.md`，其中每条 Prompt 来源、使用记录、技术决策都能在仓库内定位到证据（文件路径 + commit SHA 或 `.agent/tasks/*` 文件）；没有留档的对话或调用必须显式标注「无记录」，不得出现编造的 Prompt 原文、虚构的工具调用、虚构的模型名或虚构的验收结论。
- [ ] AC-007：文档中存在明确的「当前限制与未验证事项」章节，至少写清：真实 OpenAI 兼容 Provider 的端到端调用与验收尚未执行；Todo 工具未在生产装配中注册（属 T08）；`TokenBudget = 0` 即不做上下文压缩；会话为进程内内存实现、重启丢失；未提供流式输出、TLS、CORS、限流/配额、metrics；`go test -race` 未进入 CI；客户端断开时 `cancelled` 响应不承诺可达。每条与 `docs/design/http-api.md` §10、`docs/design/runtime-architecture.md` §14 一致，不新增未出现的结论也不遗漏其中与使用者相关的项。

## Relevant Context

- 已核实事实（基线 `d54c912`，`go1.24.1 linux/amd64`）：`go build ./...`、`go vet ./...`、`go test ./...` 全部通过；`go.mod` 为 `module cnb.cool/framework-station/mini-agent-go` + `go 1.24`、零 `require`，因此离线测试可行。
- 已核实事实：根目录**没有** `README.md`，`docs/` 下只有 `docs/design/{agent-workflow,runtime-architecture,http-api}.md`；`docs/` 下没有任何 AI 使用记录文件。
- 已核实事实：`Makefile` 提供 `test` / `build` / `vet` / `test-engine` 四个目标；`.cnb.yml` 的 `pr-check` 执行 `make test`。
- 已核实事实：可启动入口为 `cmd/mini-agent`，配置只来自环境变量，`MINI_AGENT_LLM_BASE_URL` 与 `MINI_AGENT_MODEL` 必填（缺失即 fail-closed），默认监听 `127.0.0.1:8080`；非回环监听必须配置 `MINI_AGENT_AUTH_TOKEN`（`httpapi/config.go` `Validate`）。
- 已核实事实：HTTP 端点只有 `POST /v1/sessions`、`POST /v1/chat`、`GET /healthz` 三个（`httpapi/server.go` `ServeHTTP`），契约已固化在 `docs/design/http-api.md` §3/§4/§8。
- 已核实事实：v1 工具注册表只注册 `calculator` 并 `Seal`（`cmd/mini-agent/main.go` `newRegistry`）；`service/runner.go` 包注释写明 Todo 端口适配、生产 `Summarizer` 与 tokenizer 校准不属于该层，`TokenBudget` 固定 0。
- 已核实事实：测试全部使用 `httptest` / Fake Provider（如 `llm/openai/client_test.go`、`httpapi/fake_test.go`），没有任何环境变量开关或 `build tag` 驱动的真实 Provider 测试。
- 已核实事实：`.agent/roles/` 存在 6 个角色提示词文件（`TaskBuilderPrompt.md` / `AnalystAgent.md` / `CoderAgent.md` / `CleanerAgent.md` / `OwnerGateAgent.md` / `DelivererAgent.md`）；`.agent/tasks/` 下有 10 个已存在任务目录，可作为 AI 使用记录的可回溯来源。
- 已核实事实（前置依赖）：Owner 指定前置 T08（Todo 端口适配 + 生产 `Summarizer`）。创建本任务时 T08 **尚未交付**：`origin/develop` 头为 `d54c912`，不存在对应任务目录或分支；`service/runner.go` 与 `cmd/mini-agent/main.go` 注释仍写明该能力属 T08，`docs/design/http-api.md` §10 与 `runtime-architecture.md` §14 也仍记录为未交付。
- 已核实事实：`AGENTS.md`「宿主项目工程规范」小节仍写着「当前仓库根尚无 `go.mod`，也没有 `Makefile`」，与现状不符；本任务默认**不修改** `AGENTS.md`，README 的命令与事实以 `Makefile` / `go.mod` / 代码为准，不得据 `AGENTS.md` 该段描述编写。
- 判定说明：`Design Impact = NONE`。本任务只新增使用性文档，不改变数据模型、状态机、公开协议、模块边界或安全边界；`docs/design/*` 为唯一长期设计事实源，本任务只读不改。
- Assumption：Owner 认可 `docs/ai-assisted-development.md` 作为 AI 记录落点（不在 `docs/design/` 内，避免与长期设计事实混淆）。若 Owner 要求放入其他路径，属目录级调整，Coder 可在保持内容不变的前提下改路径并同步 README 链接。
- OPEN QUESTION（不阻塞本任务定义，需 Owner 确认时序）：T08 尚未交付。若 T08 在本任务之后合并，README 的能力清单、限制与工具列表需要随之更新。请 Owner 确认其一：① 本任务在 T08 合并入本分支后再开工（推荐，可使文档一次到位）；② 接受以当前基线出文档，T08 交付时同步更新 README 相关章节。在任何情况下，文档只能描述开工基线的真实代码，不得把 T08 计划写成已实现能力。

## Verification

- AC-001 → 在干净工作树逐条执行 README 命令：`go build ./...`、`go vet ./...`、`make test`、`make build`、`make vet`、`make test-engine`（若文档列出 Engine 命令）、以及带示例环境变量的启动命令；预期全部退出码 0（启动命令为「成功进入监听」）。同时 `grep` 核对命令文本与 `Makefile` / `go.mod` 一致，无过时命令。
- AC-002 → 设置 `GOPROXY=off GOFLAGS=-mod=mod`（必要时 `GOMODCACHE` 指向空目录）执行文档中的离线测试命令，预期全部包 `ok`；确认文档已写明离线前提与不触网结论。
- AC-003 → 按 README 架构/Agent Loop 章节逐条抽取论断，映射到 `docs/design/runtime-architecture.md` 对应小节或代码符号（文件 + 标识符）；出现无法定位的论断记为不符。重点反例：Todo 工具、上下文压缩、持久化是否被写成已实现。
- AC-004 → 以示例命令启动服务（默认回环、`MINI_AGENT_LLM_BASE_URL` 指向一个不可用的占位地址、`MINI_AGENT_MODEL` 任意占位值），执行三条 `curl`：核对状态码与响应体；`/v1/chat` 预期 `502 provider_failure`，且文档已预先说明该结果。随后用 `Ctrl-C` 确认优雅关闭（进程正常退出）。
- AC-005 → 对照 `cmd/mini-agent/main.go` 的 `env*` 常量与 `httpapi/config.go` 默认值逐项核对变量表；对新增文件执行密钥扫描（如 `git grep -nE '(sk-|Bearer [A-Za-z0-9]{16,})'`），预期无命中；示例值全部为占位符。
- AC-006 → 对 `docs/ai-assisted-development.md` 中每条记录抽取引用的路径与 SHA，执行 `git cat-file -e <sha>` 与文件存在性核对；抽查 3 条 Prompt 来源是否与 `.agent/roles/*` 或 `.agent/tasks/*/task.md` 原文一致；不得存在无法核对的条目（标注「无记录」者除外）。
- AC-007 → 与 `docs/design/http-api.md` §10、`docs/design/runtime-architecture.md` §14 逐条比对，核对限制清单不缺项、无新增未经证实的结论；确认「真实 Provider 验收未执行」为显式表述。
- 通用检查：`go test ./...` 与 `git status --short`（预期文档任务不引入代码变更、测试保持全绿）；本任务不涉及 MQ / DB / 外部服务。

## Complexity

NORMAL

原因：只新增两份文档，不涉及架构、数据模型、公开协议或一致性机制的选择；事实来源（`docs/design/*` + 代码 + Git 历史）已存在，无需 Analyst 做方案对比与 Contract。主要风险是「文档与实现漂移」和「虚构 AI 记录」，已由 AC-003 / AC-004 / AC-006 的可执行核对路径覆盖，不需升级为 COMPLEX。

## Initial Route

交 Coder（NORMAL）

开工前置：Owner 指定的前置 T08 尚未交付（见 Relevant Context）。Coder 开始前应确认本 feature 分支已包含 T08 交付；若未包含，先向 Owner 确认 OPEN QUESTION 的时序选择，不得按 T08 计划撰写「已实现」描述。
