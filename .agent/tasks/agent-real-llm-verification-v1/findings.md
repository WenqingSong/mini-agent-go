# Cleaner Findings

## Review Target

| 项 | 值 |
| --- | --- |
| Task | `agent-real-llm-verification-v1`（`contract.status = NOT_REQUIRED`，`Design Impact = NONE`） |
| 任务基线 | `f417327`（`origin/develop`，本次 fetch 后复核仍为 `f417327`） |
| implementation Evidence Commit（C1，被审查对象） | `aad9dd4518c90445b32812f5f7174612790598a0`（`docs(verification): 落库 agent-real-llm-verification-v1 真实验收证据`） |
| Review Request commit（C2，metadata） | `db1b8da`（只改 `state.yaml` 的 `review.status/target`） |
| 审查时工作区 | `git status --short` 为空；HEAD = `origin/feat/agent-real-llm-verification-v1` = `db1b8da` |
| 相关变更范围 | `git diff f417327..aad9dd4` 只含 `.agent/tasks/agent-real-llm-verification-v1/` 下 7 个文件：`task.md`、`state.yaml`、`evidence.md`，以及 4 个占位 Artifact（`findings.md`、`core-logic.md`、`owner-decision.md`、`delivery.md`）；**无生产代码、无测试、无构建配置改动** |
| 任务前已有修改 | 无（`f417327` 处工作区干净），C1 之前只有 `925183e` 任务定义提交，边界清晰 |
| 不在 Git 内的被审查事实 | 本地 `.env`（`.gitignore:2` 忽略、未跟踪）与容器运行时状态；以只读方式复核，未读取/打印任何凭据值 |

新增文件识别方式：使用 `git diff --name-status`（不是默认 `git diff`，避免漏掉新增文件），确认全部为新增 Artifact。

## Result

CLEAN

范围说明：`CLEAN` 只对 `review.target = aad9dd4` 有效，且只覆盖 Cleaner 阶段可判定的 AC-001 ~ AC-008。AC-009 的产物 `delivery.md` 按 Task 与 Workflow 由 Deliverer 在 OwnerGate 之后汇总，属后续阶段，不在本次 CLEAN 的判定范围内（其所需原始输入已在 `evidence.md` 齐备，见下表）。

## Acceptance Criteria

| ID | Result | Evidence |
| --- | --- | --- |
| AC-001 | PASS（含 CLEAN-002 披露的偏差） | 独立复跑 `make check` exit 0，未发起任何模型请求；`git ls-files \| grep -x .env` 无输出、`git check-ignore -v .env` → `.gitignore:2`；`.env` 实际 `MINI_AGENT_LLM_BASE_URL=https://tokenhub.tencentmaas.com/v1`，叠加 `llm/openai/client.go:89-92`（`TrimRight(path,"/") + "/chat/completions"`）得到 `https://tokenhub.tencentmaas.com/v1/chat/completions`；`docker run --rm mini-agent-go:local` 内 `/app` 只有 `mini-agent`（`.dockerignore` 排除 `.env*`）。变量名集合差异（`.env.example` 11 键 vs `main.go` 12 个 `envXxx`，差 `MINI_AGENT_LISTEN_ADDR`）为真实差异，见 CLEAN-002 |
| AC-002 | PASS | 独立验证：`/healthz` → 200 `{"status":"ok"}`；`docker compose ps` → `Up (healthy)`；带正确 Bearer → `POST /v1/sessions` 201 且返回 `session_id`；无 Token → 401；认证前不读 body、不调用 Runner（`httpapi/handler.go:44-48`）。被拒请求前后 `llm_request` 计数 0 → 0（同容器 `run_started` 亦为 0） |
| AC-003 | PASS | `evidence.md` §7 记录 HTTP 200、`COMPLETED`、中文 reply、`tool_calls=0`、`llm_calls=1`，runlog 四事件成套且 `run_id` 一致。未复跑真实调用（见「验证限制」），交叉核验：事件字段名与 `runlog/slog.go:79-113` 一致（`message_count`/`compacted`/`dropped_messages`/`finish_reason`/`tool_call_count`/`input_tokens`/`output_tokens`），`run_started` 确含 `data.model`（`agent/agent.go:107-110`） |
| AC-004 | PASS | §8：200、`COMPLETED`、reply `37 × 48 = **1776**`、`tool_calls=1`、`llm_calls=2`；事件顺序与 AC 逐条吻合，两条工具事件 `call_id` 相同。`message_count` 3→5 与「追加 assistant tool_calls 消息 + tool 结果消息」一致；`llm_calls=2` 与 2 次 `llm_request` 一致 |
| AC-005 | PASS | 四段证据齐备：`finish_reason=tool_calls`+`tool_call_count=1` 来自 Provider 响应解析（`llm/openai/wire.go:303-323`、`agent/events.go:59`）；同一 `CallID` 工具事件（`agent/events.go:66-98` 的 `CallID` 字段，`runlog/slog.go:91-93` 输出）；`builtin/calculator.go:14` 参数协议只有 `op/a/b`，仅 `op=mul,a=37,b=48` 得 1776；`git grep -n 1776 -- '*.go'` 实测零命中。工具执行唯一入口 `agent/run.go:141` 由 `resp.Message.ToolCalls` 驱动，`agent/run.go:147` 拒绝「声明 tool_calls 却无调用」，v1 registry 只注册 calculator 并 `Seal()` |
| AC-006 | PASS | 离线：复跑 `go test ./llm/... ./httpapi/... ./agent/... ./e2e/... -run 'Provider\|Timeout\|Cancel\|Failure\|Limit\|Disconnect\|ErrorSemantics\|ContextFailures'` 全 PASS，evidence 列出的 13 个用例名与包内真实函数逐一对应。真实负向：`evidence.md` §10 记录 1 次无效 Model → 502 `provider_failure`，`run_finished.error_kind` 同种类，响应与日志无密钥；恢复已独立复核（`.env` 与运行中容器均 `MODEL=deepseek/deepseek-flash`） |
| AC-007 | PASS | §11 逐条账本 4 次上游请求、input 1378 / output 92 / total 1470；口径为上游 LLM 请求次数；第 4 次 0/0 注明「Provider 未返回 usage」。三次成功调用的 token 增量自洽（441-405=36 ≈ 新增 assistant+user 两条消息；532-441=91 ≈ 工具轮消息），与轮次结构一致。Owner 授权为「不设固定上限 + 仅验收必需」，无超限 |
| AC-008 | PASS | 独立复跑：`make ci` exit 0（13 包全 ok）；`go test -race -count=1 ./...` exit 0；`go test ./e2e -count=1` exit 0；`GOWORK=off go -C .agent/engine test -count=1 ./...` exit 0；`git diff --name-status f417327 aad9dd4` 无生产/测试文件；密钥模式 `git grep` 只命中 `runlog/sanitize_test.go:105-106` 脱敏器夹具（非真实凭据）；`.env` 未跟踪且不在镜像内 |
| AC-009 | NOT_VERIFIED（归属 Deliverer） | `delivery.md` 尚未由 Deliverer 汇总，属后续阶段。Cleaner 只确认 `evidence.md` 已提供 AC-009 所需的原始事实：分支与 commit、构建/测试/Race/Docker 健康结果、Provider 与 Model、普通对话与工具调用结果、调用量与 token 汇总、费用核对口径、错误处理、安全检查、已知限制。最终结论（PASS/BLOCKED/FAIL/PARTIAL）须由 Deliverer 判定 |

## Verification

| Check | Result | Evidence / Reason |
| --- | --- | --- |
| 变更范围与基线 | PASS | `git diff --name-status f417327 aad9dd4` 仅 7 个任务 Artifact；`git rev-parse origin/develop` = `f417327`；`git merge-base --is-ancestor f417327 HEAD` = yes |
| 证据绑定机检 | PASS | `.agent/bin/workflow-check gate cleaner-start .agent/tasks/agent-real-llm-verification-v1` → `PASS`（C1 为 HEAD 祖先、tail 只含 review-neutral `state.yaml`） |
| `make check` | PASS | exit 0，`✓ 配置与环境检查通过（未发起任何模型请求）` |
| `make ci` | PASS | exit 0（build + vet + test + test -race，13 包全 ok） |
| `go test -race -count=1 ./...` | PASS | exit 0（非缓存） |
| `go test ./e2e -count=1` | PASS | exit 0 |
| `GOWORK=off go -C .agent/engine test -count=1 ./...` | PASS | exit 0（2 包 ok） |
| 离线错误语义子集复跑 | PASS | AC-006 的 13 个用例全部 `PASS`；用例名与包内函数逐一对应 |
| 镜像不含 `.env` | PASS | `docker run --rm --entrypoint /bin/sh mini-agent-go:local -c 'test ! -e /app/.env'` exit 0；`/app` 只有 `mini-agent` |
| 容器健康与认证 | PASS | `compose ps` → `Up (healthy)`；`/healthz` 200/`{"status":"ok"}`；无 Token 401；正确 Token 201 |
| 被拒请求零上游调用 | PASS | 认证拒绝前后 `docker compose logs` 中 `llm_request` 0 → 0 |
| 运行中配置恢复 | PASS | 容器内 `MODEL=deepseek/deepseek-flash`、`LISTEN_ADDR=0.0.0.0:8080`、`BASE_URL=https://tokenhub.tencentmaas.com/v1` |
| 密钥安全 | PASS | `.env` 未被跟踪、`.gitignore:2` 命中；`git grep` 密钥模式仅命中脱敏测试夹具；本次复核未打印任何凭据值 |
| 真实 Provider 调用复跑 | NOT_EXECUTED | 预算与授权为「仅验收必需」，AC-003/004/006 的真实请求已由 Coder 在授权内完成；Cleaner 不重复消耗预算，改以「代码事实 + 日志结构 + token 增量」交叉核验 |
| Mutation：calculator `mul` → `a*b+1` | 预期失败（已隔离实测） | `/tmp` 隔离副本执行 `go test ./builtin -run TestCalculatorSuccess -count=1` → FAIL（`负数乘法: want=-10 got=-9 diff=1`），证明「1776 来自真实计算」有测试保护 |
| Mutation：`mapFinishReason` 放行 `content_filter` | 预期失败（已隔离实测） | 同一隔离副本执行 `go test ./llm/openai -run TestCompleteErrorSemantics -count=1` → FAIL（`未知 finish_reason` 子用例），证明白名单 fail-closed 有测试保护 |
| AC-009 报告一致性 | NOT_VERIFIED | 待 Deliverer 汇总 `delivery.md` 后核对 |

验证限制（如实记录）：AC-003/004/005 的运行证据来自 Coder 保存的 runlog 摘录；容器为恢复配置后重建，原始容器日志已随容器删除，Cleaner 无法从运行中的容器重放该片段。交叉核验依据为：事件字段名与 `runlog` 实现完全一致（含摘要中省略的 `compacted`/`dropped_messages`）、`session_id`/`run_id` 为 32 位 hex 与 `httpapi` 生成规则一致、`call_id` 形如 Provider 生成的 `call_00_...`、三次调用的 token 增量与消息轮次自洽、`1776` 在全部 Go 源码与测试中零命中。据此判定证据可信；该限制已转为 CLEAN-001。

## Findings

### CLEAN-001：真实调用原始 runlog 未归档，仅保留人工摘录

- Severity：P3
- Status：OPEN
- Location：`.agent/tasks/agent-real-llm-verification-v1/evidence.md` §7 / §8 / §11；容器日志已随容器重建删除
- AC / Invariant：AC-003 / AC-004 / AC-005 / AC-007 的可复核性（非行为正确性）
- Trigger：Owner 或 Deliverer 事后想核验 runlog 原始片段
- Actual：仓库内只有 evidence.md 的排版后摘录；原始日志文件不在仓库、也不在运行中的容器里
- Expected：可复核证据最好保留原始日志片段（如按 Run ID 抽出的 JSONL），避免只有二手记录
- Impact：不影响功能正确性；降低事后审计强度，复核需依赖 Coder 摘录
- Evidence：`docker compose logs` 中 `run_started`/`llm_request` 计数为 0（当前容器为恢复后重建，无历史事件）
- Required Fix Boundary：无需修改生产代码；如 Owner 要求，可在本任务目录补充原始日志片段文件，或在后续任务中把「保留原始 runlog」写进验收要求

### CLEAN-002：`.env.example` 缺少 `MINI_AGENT_LISTEN_ADDR`，AC-001 字面集合一致性未达成

- Severity：P3
- Status：OPEN
- Location：`.env.example`（11 键）↔ `cmd/mini-agent/main.go:33-44`（12 个 `envXxx`）
- AC / Invariant：AC-001「`.env.example` 的变量名与 `cmd/mini-agent` 读取的变量名一致」
- Trigger：按模板逐项比对变量名集合
- Actual：`.env.example` 无 `MINI_AGENT_LISTEN_ADDR`；该变量由 `compose.yaml:15` 的 `environment` 固定注入 `0.0.0.0:8080`
- Expected：两种都可接受——要么模板补齐该键（含默认值 `127.0.0.1:8080`），要么 AC 措辞改为「反向包含：模板中的每个键都必须被应用读取」
- Impact：无运行时影响（容器内监听地址由 compose 注入，README 与 `docs/design/http-api.md` 已文档化）；仅影响模板完整性与验收措辞一致性
- Evidence：`cat .env.example` 与 `grep -n MINI_AGENT cmd/mini-agent/main.go` 的差集恰好为 `MINI_AGENT_LISTEN_ADDR`；Coder 已在 `evidence.md` §5 主动披露并未擅改（属 Scope 变更）
- Required Fix Boundary：由 Owner 决定补齐模板或调整 AC 措辞；不由 Coder 在未授权范围内自行改动仓库配置文件

### CLEAN-003：Coder 修改了 Owner 提供的本地 `.env` 一行，需 Owner 追认

- Severity：P3
- Status：OPEN
- Location：本地 `.env`（未跟踪）`MINI_AGENT_LLM_BASE_URL`
- AC / Invariant：AC-001 前提「Owner 填好的本地 `.env`」
- Trigger：Owner 填写的值含完整请求路径 `.../v1/chat/completions`
- Actual：Coder 在首次真实请求前把该行改为 `https://tokenhub.tencentmaas.com/v1`，其余键未动，未打印任何取值，并在 `evidence.md` §3 主动披露
- Expected：属必要且已被 Task「Relevant Context」明确要求（不能填完整请求地址）；但被验证配置由 Coder 改动，需 Owner 明确追认
- Impact：无功能风险；仅涉及「验证者修改被验证对象」的流程透明度
- Evidence：`evidence.md` §3；当前 `.env` 实际值为 `https://tokenhub.tencentmaas.com/v1`，容器内同值
- Required Fix Boundary：无需代码改动；Owner 在 OwnerGate 阶段确认该行改动即可
