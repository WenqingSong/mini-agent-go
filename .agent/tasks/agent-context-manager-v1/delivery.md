# Delivery Verification

## Milestone and Target

- Milestone：未声明。`task.md` 无 `Milestone` 字段，并明确记录「未由 Owner 指定 Deliverer 验收的里程碑标识，Deliverer 不得自行推断」；本次按 T05 全量 `AC-001`～`AC-007` 整体验收，未缩小或替换验收范围。
- Delivery Target：`feature_head` = `63a4e336eb31c20639a91808d34b51f6efce41f0`（`feat/agent-context-manager-v1`，验证时与 `origin` 同 SHA）；`develop_base` = `6902d2445665ec356a2db22b33b7749d39d8018e`（`origin/develop`，`git ls-remote` 实测）。develop 是 feature 的祖先，`develop_base + feature_head` 为快进关系，无冲突面。
- Cleaner Review Target：`61fa094ad44a14ea608ffcc773c20689e057e00b`（`state.review.target`），`delivery.review_target` 记录同值。
- Target Match：YES。`61fa094..63a4e33` 只改 `.agent/tasks/agent-context-manager-v1/{findings,core-logic,owner-decision}.md` 与 `state.yaml`，`contextmgr/**`、`docs/design/**` 零变化，因此本次运行证据对 CLEAN target 同样成立。

## Environment

- OS / 运行时：Linux（容器），`go version go1.24.1 linux/amd64`；根模块 `go 1.24`，零第三方依赖。
- 依赖服务：无。Task 的 Out of Scope 明确排除 CLI / HTTP / 可运行二进制 / Docker / MySQL / Redis / MQ，本次不构造这些环境，也未改用更宽松的环境。
- 交付材料：`git archive 63a4e33` 导出的干净快照 `/tmp/t05-verify`（无 `.git`、无未提交文件、不依赖本机绝对路径）；`contextmgr/` 内 `grep '/root/|/tmp/|http(s)://|net/http'` 无命中。
- 网络：全部构建与测试以 `GOPROXY=off GOFLAGS=-mod=readonly` 执行，机械证明无网络与真实模型依赖。
- 测试数据与隔离：独立探针模块 `/tmp/t05-probe`（独立 module + `replace` 指向快照），只使用 `contextmgr` + `core` 公开 API，自带包外 Fake `Summarizer` 与自实现确定性 `Estimator`；不修改工作区、不写 Session Store、不调用真实模型。临时目录仅承载本次证据，未进入仓库。
- 配置来源：无配置文件、无 Secret。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | 快照内 `go build ./...` exit 0；`go vet ./...` exit 0；`gofmt -l .` 无输出 |
| CI 入口 `make test` | PASS | `make test`（= `go test ./...`，与 `.cnb.yml` pr-check 同入口）：`contextmgr` / `core` / `llm` / `runlog` 全 `ok`，exit 0 |
| Race | PASS | `go test -race ./... -count=1` exit 0 |
| 依赖边界（INV-007） | PASS | `go list -f '{{.ImportPath}}|{{join .Imports " "}}' ./contextmgr` → `core` + `context errors fmt strings unicode/utf8`；`go list -deps ./contextmgr` 无第三方 Module |
| Core Flow：未超预算视图（AC-001） | PASS | 探针 A1–A13：视图 6 条（SystemPrompt + PreviousSummary + 4 条输入原样保序）、`Compacted=false`、`DroppedMessages=0`、`Summary=""`、注入估算器合计 18 = 预算 18、重复构建 `DeepEqual`、`Summarizer` 0 次、输出过 `core.ValidateMessages` |
| Core Flow：压缩主链路（AC-002） | PASS | 探针 B1–B11：12 条历史 + 旧摘要，预算 21 触发压缩；`DroppedMessages=8` 且被覆盖恰为输入前 8 条，保留 4 条 = `KeepRecent`，`Summary` 与视图摘要消息逐字相等，仅一条摘要消息，压缩后估算 18 ≤ 21，`Summarizer` 恰 1 次且入参 = 前缀 + `PreviousSummary`，输出合法 |
| Core Flow：工具单元原子性（AC-003） | PASS | 探针 C1–C7：`KeepRecent=1` → 覆盖 6 条、尾部仅最后 1 条、视图不以 tool 起头；`KeepRecent=3` → 边界落在「1 调用 + 2 结果」单元内部时整组保留（5 条，2 个结果齐全）；保留尾部超预算 → `invalid_argument` + 零值 `Result` |
| Core Flow：失败语义（AC-004 / AC-005） | PASS | 探针 D1–D4 / E1–E10：`Summarizer` 报错 → `internal` + `errors.Is` 可追溯 + 零值 `Result`；空串/全空白摘要 → `internal` 且无占位文本；`KeepRecent<0`、`Messages` 含 system、预算小于 SystemPrompt、仅 system+摘要即超预算、`KeepRecent` 覆盖全部输入 → 均 `invalid_argument` 且 `Summarizer` 0 次、`Result` 零值；输入孤儿 `ToolResult` → 原样 `invalid_pairing` 并定位 `CallID=c-missing`；取消/超时 → `cancelled` / `deadline_exceeded`；`TokenBudget=0` → 不限制且不压缩；`NewManager` 拒绝 nil 依赖 |
| 默认装配（HeuristicEstimator） | PASS | 探针 F1–F4：默认估算器 + 普通长历史（20 条，预算 102）压缩成功；按估算器独立复算视图总量 58 ≤ 102（不采信实现自报）、保留尾部为输入后缀、保留 3 条 = `KeepRecent` |
| 最终结果一致性（INV-004 / INV-005） | PASS | 每条失败路径逐一断言 `Result` 四字段为零值；每条成功路径断言 `DroppedMessages` 等于被覆盖前缀长度且 `Summary` 与摘要消息一致；全部成功视图过 `core.ValidateMessages`；探针 55 项断言全 PASS，`probe_exit=0` |
| 交付范围（范围证据，非 Diff Review） | PASS | `git diff --stat 6902d24..63a4e33 -- go.mod Makefile .cnb.yml core llm tool session runlog` 为空；变更文件仅 `contextmgr/**`、`docs/design/runtime-architecture.md`、本任务目录 |
| Design 登记一致性（AC-007 的交付侧核对） | PASS | `git ls-files contextmgr` 6 个文件与 Design §12 六个登记行一一对应；§9 描述 `Request{SystemPrompt, PreviousSummary, Messages, TokenBudget, KeepRecent}` / `Result{Summary...}` 与实现签名一致；§14 已把 `TokenBudget` 记录为已验证的估算预算（精确校准留 T07） |

## Acceptance Evidence

- AC-001 → 探针 A1–A10：未超预算时视图逐条等于「SystemPrompt + PreviousSummary + 原始历史」，`Compacted=false`、`DroppedMessages=0`、估算总量 = 预算、重复调用结果相同。
- AC-002 → 探针 B1–B11：压缩触发、`Compacted=true`、`Summary` 来自注入 Fake、保留条数 = `KeepRecent`、`DroppedMessages` 与实际覆盖条数一致、压缩后满足预算。
- AC-003 → 探针 C1–C6：预算/保留边界刻意落在工具单元内部时整组保留，无孤儿/缺失/重复 Result，输出过 `core.ValidateMessages`。
- AC-004 → 探针 D1–D4：`Summarizer` 报错、空摘要、全空白摘要均返回 `*core.RunError`（`internal`）与零值 `Result`，不产生占位摘要文本。
- AC-005 → 探针 A11–A13、C7、E1–E10：预算不可满足（System Prompt 自身、无历史、`KeepRecent` 覆盖全部输入、保留尾部超预算）与非法配置均 `invalid_argument` 且零值 `Result`；空历史 + `TokenBudget<=0` 有确定行为；输入配对非法原样返回 `invalid_pairing`。
- AC-006 → 快照内 `go build` / `go vet` / `make test` / `-race` 全 exit 0；依赖 `go list` 机械核对仅标准库 + `core`；全程 `GOPROXY=off`，无网络与模型调用。
- AC-007 → 本次仅做交付侧核对（文件清单与签名、§14 状态）；四方一致的实质审查结论属 Cleaner（`findings.md` AC-007 PASS），不重复计为 Deliverer 证据。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| 服务启动 / 健康检查 / HTTP API Smoke | 本任务 Out of Scope（无 CLI、HTTP、可运行二进制、Docker），交付物为库 `contextmgr` | 无；装配与 Provider 联调归 T01 / T07 |
| 真实 LLM Summarizer、真实 tokenizer 精度 | Out of Scope（D5 归 T07），仓库内只允许测试 Fake | `HeuristicEstimator` 对 CJK 可能低估、摘要输入长度控制未量化，已由 Owner 记录并归 T07 |
| 重启 / 恢复 / 回滚 / 性能 / 并发压测 | Task 与 Contract 未要求；本模块无外部持久化状态 | 无：结论不依赖这些检查 |

## Remaining Risks

- `HeuristicEstimator` 按 4 字符/token 估算，对 CJK 可能低估；`TokenBudget` 只是估算预算，生产装配须注入校准估算器（Design §14 已登记）。
- 单次压缩后仍超预算只能显式失败，调用方需增大预算或缩减输入；T07 接入真实 LLM Summarizer 时须把该次调用计入 `RunPolicy`。
- `Messages` 禁止 system 消息是比 `core` 更窄的输入契约：T01 若把 system 内容放进 `Messages` 会直接拿到 `invalid_argument`。
- 本次证据绑定 `feature_head=63a4e33` 与 `develop_base=6902d24`；此后任何代码、测试或 Design 变化都会使本 PASS 失效（文档/state 类 delivery-neutral 变更除外）。

## Result

PASS
