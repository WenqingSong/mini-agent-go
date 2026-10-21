# Delivery Verification

## Milestone and Target

- Milestone：T09 完整业务模块交付前验收（Agent Runtime HTTP JSON API + 同 Turn 装配层 + 可启动入口）。
- Delivery Target：`fbb8b267760f6feabf94a53d6839c22a35338bfe`（`origin/feat/agent-http-api-v1`，即本轮实际验证的 feature snapshot）。
- Cleaner Review Target：`3ce1869b2c1206464d59cd70ee6f1f820a2b3f43`；`owner.review_target` 与之相同。
- Target Match：YES —— `git diff --stat 3ce1869 fbb8b26 -- httpapi service cmd docs/design` 为空；`3ce1869..fbb8b26` 的全部差异仅 4 个 `.agent/tasks/agent-http-api-v1/*` artifacts（Cleaner/OwnerGate review-neutral tail）。
- Develop Base：`6bebdda3770318b27dd6b578404e9465cd1583ee`（验证时 `origin/develop`）；`git merge-base --is-ancestor 6bebdda fbb8b26` 成立，因此 feature snapshot 已包含该 develop 基线。

## Environment

- OS：Debian GNU/Linux 12 (bookworm)，Linux `5.4.241-1-tlinux4-0025.13` x86_64。
- 运行时：`go1.24.1 linux/amd64`；根 module `cnb.cool/framework-station/mini-agent-go`，`go 1.24`，零 `require`。
- 验证方式：`git worktree add --detach /tmp/deliver-verify fbb8b26`（隔离、干净检出，无本地修改）；构建产物输出到 `/tmp/deliver-out/`，未写入仓库。
- 外部依赖：无 MySQL / Redis / Kafka / Docker / 外部网络。假 LLM Provider 为本次自行编写的本机回环 OpenAI 兼容服务（`127.0.0.1:18091`，仅 `/tmp` 下脚本，不属仓库代码）。
- 配置来源：全部 `MINI_AGENT_*` 环境变量注入（与 `docs/design/http-api.md` §8 一致）；凭证为本次临时假值，不在本文记录真实值。
- 测试数据与隔离：会话为进程内存（无持久化）；每个用例使用独立 `session_id` 或独立进程/端口；验收后进程全部终止、临时 worktree 事后移除。

## Verification

| Check | Result | Evidence |
|---|---|---|
| Build | PASS | 隔离 worktree 内 `go build ./...` 退出码 0；`go build -o /tmp/deliver-out/mini-agent ./cmd/mini-agent` 成功；`gofmt -l httpapi service cmd` 无输出 |
| Static check | PASS | `go vet ./...` 退出码 0 |
| Unit/Component 测试 | PASS | `make test`（= `go test ./...`）12 个包全部 `ok`（httpapi 1.146s / service 0.067s / cmd 0.079s） |
| Race 测试 | PASS | `go test -count=1 -race ./...` 12 个包全部 `ok`，无 data race 报告 |
| Engine 回归 | PASS | `GOWORK=off go -C .agent/engine test ./...` → `workflow-check`、`internal/workflow` 均 `ok` |
| Service 启动与就绪 | PASS | 真实 binary `127.0.0.1:18090` 启动后 `GET /healthz` → `200 {"status":"ok"}`；默认监听面 `127.0.0.1:8080` 亦可启动且 healthz 200 |
| Core Flow（创建会话） | PASS | `POST /v1/sessions` `{}` → `201`，服务端生成 32 位小写 hex；`{"session_id":"sess-smoke-1"}` → `201` 并回显同 ID |
| Core Flow（chat + 工具链路） | PASS | 同一会话 `POST /v1/chat`（"1+2 等于多少"）→ `200 {"status":"COMPLETED","reply":"3","llm_calls":2,"tool_calls":1}`；假 Provider 收到的第 2 次请求消息角色序列为 `user, assistant(tool_calls), tool{"operation":"add","result":3}, assistant`，证明工具真实执行且只返回最终文本 |
| Multi-turn 历史 | PASS | 同会话第二次 chat 的 Provider 请求含上一轮 `user:1+2 等于多少` 与 `assistant:3`，本轮仅新增 1 条 user（无重复 user、无客户端回传历史） |
| Data / 无副作用 | PASS | Provider 失败（502）后同会话再成功对话，Provider 收到的历史仅 1 条 `user:正常问题`；触发超时、客户端断开、`llm_call_limit`、`tool_call_limit` 后同样仅剩后续正常轮，历史无半轮提交 |
| 错误封套与参数类错误 | PASS | `Content-Type` 缺失/非 JSON → 415 `unsupported_media_type`；数组/`null`/尾部数据/坏 JSON → 400 `invalid_json`；未知字段 → 400 `unknown_field`；缺必填 → 400 `missing_field`；类型/字符集/超长(8193) → 400 `invalid_field`；2MiB body → 413 `payload_too_large`；未知路径 → 404 `not_found`；`GET /v1/chat` → 405 `method_not_allowed` + `Allow: POST` |
| 会话类错误 | PASS | 未知会话 → 404 `session_not_found`；重复创建同 ID → 409 `session_exists`；均未触及 Provider（Provider 调用计数不增加） |
| 认证与监听边界 | PASS | 无凭证 / 错凭证 / 非 Bearer → 三者响应体逐字节相同 `401 unauthorized`，且 Provider 调用计数为 0；`0.0.0.0:18099` 无凭证启动 → 打印「非回环地址时必须配置认证凭证」、`exit=1`、端口不可达（`curl` 000） |
| fail-closed 启动 | PASS | 非法监听地址、缺 `MINI_AGENT_LLM_BASE_URL`、缺 `MINI_AGENT_MODEL`、`MINI_AGENT_REQUEST_TIMEOUT=-1s` 均 `exit=1` 且未监听 |
| 请求级超时 | PASS | `RequestTimeout=5s` + 阻塞 Provider：`504 deadline_exceeded`，实测 5.01s；该 Run 日志 `run_finished status=CANCELLED error_kind=deadline_exceeded`；会话无部分提交 |
| 客户端断开 | PASS | `curl --max-time 1` 断开阻塞中的请求：客户端无响应（`000`，符合 D5 不承诺 408）；日志 `run_finished status=CANCELLED error_kind=cancelled`；随后同会话历史仅含新消息。两种终态错误种类互不相同且均非 `internal` |
| 预算耗尽映射 | PASS | `MINI_AGENT_MAX_LLM_CALLS=1` + tool 脚本 → `429 llm_call_limit`；`MINI_AGENT_MAX_TOOL_CALLS=1` + 持续 `tool_calls` → `429 tool_call_limit`；两者均无部分提交 |
| 优雅关闭（宽限期内） | PASS | 在途 SLOW 请求进行中发 `SIGTERM`：新连接被拒（000），在途请求仍返回 `200 {"reply":"slow-ok"}`，进程随后退出且日志无 `panic` |
| 优雅关闭（宽限期满） | PASS | `ShutdownGrace=1s` + 阻塞 Run，连续两次 `SIGTERM`：进程 1.01s 内退出，在途 Run 日志 `status=CANCELLED error_kind=cancelled`，`panic` 计数 0（重复关闭幂等） |
| 零泄漏 | PASS | `/healthz` 与全部响应体、运行日志（JSON `runlog`）中检索假 Bearer 值与假 LLM Key 均为 0 命中；`/healthz` 仅 `{"status":"ok"}`，不含 model/auth/api_key 等字段；Provider 失败响应体仅 `provider_failure` + 固定文案，未回显上游 `SECRET-PAYLOAD` |
| Contract/Design 机械一致 | PASS | `httpapi/errors.go` 的 code→status 映射与 `docs/design/http-api.md` §4 表 20 项逐条一致；端点方法/路径与 §3 一致；`runtime-architecture.md` §12 登记的 19 个 `.go` 路径与 `git ls-files httpapi service cmd` 一致（提取规则差异外无差集）；`go list` 确认 `httpapi` 只依赖 `core`+`session`、`service` 按 Contract 依赖、`cmd` 为唯一装配点，L0–L3 无任何反向 import；`git diff 6bebdda -- go.mod Makefile .cnb.yml` 为空、零 `require` |

## Acceptance Evidence

- AC-001 → 创建会话 + chat 返回 `session_id`/`run_id`/`COMPLETED`/最终文本；第二轮 Provider 请求含上一轮 user 与 assistant 文本（多轮历史由 Session 提供）。
- AC-002 → 工具脚本轮返回最终文本 `"3"`（非 tool_call 载荷），`llm_calls=2`、`tool_calls=1`，Provider 记录到 `calculator` 被调用且参数 `{"op":"add","a":1,"b":2}`，工具结果 `{"operation":"add","result":3}` 进入历史。
- AC-003 → 会话类 404/409、参数类 400/413/415、Provider 失败 502 均返回稳定 code；四类失败后再次成功对话时历史中无失败轮残留（会话无副作用）；错误体不含路径、堆栈、Provider 原始载荷。
- AC-004 → 默认 `127.0.0.1` 监听可用；非回环缺凭证 `exit=1` 且不监听；缺失/错误凭证响应逐字节相同 401、Provider 调用 0 次；`/healthz` 无配置与密钥泄漏。
- AC-005 → 全部运行验收使用本机回环假 Provider + 内存 Session；无真实模型/Key、无外部网络、无 DB/Redis/MQ/Docker；假密钥在响应体、响应头、`/healthz`、日志中零命中。
- AC-006 → 端点/字段/错误码/状态码与 `docs/design/http-api.md` 机械一致；§12 路径与 `git ls-files` 一致；分层方向正确；`go.mod` 零改动。
- AC-007 → 客户端断开：Run `CANCELLED` + `cancelled`、会话无部分提交；请求超时：`CANCELLED` + `deadline_exceeded` + `504`；两者可区分且都不是 `internal`。
- AC-008 → 就绪后 `/healthz` 200；非法配置 fail-closed 不监听；关闭后新连接被拒；宽限期内请求 200 完成、宽限期满在途 Run 被取消；重复信号不 panic、进程限时退出。

## Not Executed

| Check | Reason | Risk |
|---|---|---|
| `tool_failure`（500）运行证据 | 内置 `calculator` 对可预期输入只返回领域失败（`IsError`），无公开配置可制造工具级 `error`；该映射由 `httpapi/errors_test.go` 表驱动覆盖，属 Cleaner 审查证据而非本次运行证据 | 该错误码分支未在本轮真实链路被触发；映射为纯查表，实现风险低 |
| `invalid_pairing`（500）运行证据 | 只能由 Agent Loop 内部消息配对异常触发，无外部可注入路径；同上由单元表驱动覆盖 | 同上 |
| `session_expired` 运行证据 | Session TTL 由 L0 契约固定（默认 30min），v1 无运行期可配开关；本次未等待 TTL 到期 | 过期判定依赖 `session` 包既有行为与 Cleaner 单元测试（注入时钟）证据 |
| 真实 Provider 端到端时延 / `RequestTimeout=60s` 是否够用 | Task 明确属部署/运维评估，Contract 列为 UNKNOWN | 生产接入真实模型时可能需调整超时 |
| 重启恢复 / 数据持久化 / 多实例一致性 | Task Out of Scope（会话为进程内存） | 无 |
| 性能指标（QPS/P95 等） | Task 未要求性能验收 | 无 |

## Remaining Risks

- `CLEAN-001`（P3，OPEN）：关闭耗时最坏约 2×`ShutdownGrace`。本次实测 1s 宽限场景 1.01s 退出、5s 宽限场景在途请求正常完成，未观察到超界行为；Owner 已在 `owner-decision.md` 接受该风险。
- 同会话并发 chat 排队可能在后到请求尚未发起模型调用前就以 504 结束（Contract 已列为已知取舍），本轮未单独压测并发排队。
- v1 无上下文压缩、无 Todo 工具，长会话触达 Provider 限制时以 `provider_failure`（502）结束（Task/Contract 既定范围）。
- 真实 Provider 的时延与鉴权行为未验证，仅验证了 OpenAI 兼容协议形状与错误映射。

## Result

PASS
