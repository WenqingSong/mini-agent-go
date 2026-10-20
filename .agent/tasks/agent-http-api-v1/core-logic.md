# Owner Core Logic 验证卡

被审查版本（`review.target`）：`3ce1869b2c1206464d59cd70ee6f1f820a2b3f43`。
以下两处决定「数据是否会脏」「接口是否会裸奔」，其余多为 DTO、字段校验与样板装配，不列入。

## CL-001：一次 chat = 一次真实会话执行权，用户消息与产出同事务

- Owner 需要理解：本轮用户消息与 Agent 产出必须落在**同一次** `session.Manager.RunTurn` 里，要么整轮提交、要么整轮丢弃。若失败轮次被提交，会话里会留下「只有用户消息、没有回答」或「半轮工具调用」的脏历史，下一次对话模型就会基于脏历史回答，且历史可能不再通过 `core.ValidateMessages`。
- 生产代码：
  - `service/runner.go:175-208`（`Runner.Run`：外层一次 `r.deps.Sessions.RunTurn`，回调内先 `turn.Append(userMessage)`，失败时 `return runErrorOf(result)` 让会话层整轮丢弃）
  - `service/turn.go:29-34`（`turnSession.RunTurn`：Loop 内部再要执行权时**直接复用当前 `*Turn`**，不再二次取锁，因此不重入死锁）
- 关键测试：
  - `service/runner_test.go:TestRunUsesExactlyOneRealRunTurn`（真实 `session.Manager` + 计数包装，断言真实 `RunTurn` 恰好 1 次、提交后角色序列 `user, assistant(tool_calls), tool, assistant`）
  - `service/runner_test.go:TestRunProviderFailureLeavesNoPartialHistory`（Provider 失败后会话消息逐条不变且仍合法）
- 基线验证：
  ```bash
  go test ./service/ -run 'TestRunUsesExactlyOneRealRunTurn|TestRunProviderFailureLeavesNoPartialHistory|TestRunSecondTurnSeesPreviousHistory' -count=1 -v
  ```
  预期：全部 `--- PASS`。
- 可选 Mutation：在 `service/runner.go` 回调内删掉
  `if result.Status != core.RunStatusCompleted { return runErrorOf(result) }`（即失败也返回 nil，让会话层照常提交）。
- 预期失败：`TestRunProviderFailureLeavesNoPartialHistory` 必失败（`before/after` 消息数不再相等）——Cleaner 已实测该变异导致 FAIL。
- 恢复确认：`git checkout -- service/runner.go` 后重跑同一命令应重新全部 PASS。

## CL-002：认证先于解析与执行，默认只监听回环，非回环缺凭证不启动

- Owner 需要理解：未认证请求必须在读 body、解析参数、拿会话执行权之前就被拒绝，且缺失凭证与错误凭证返回**同一个** `401 unauthorized`（否则凭证可被枚举）；默认监听 `127.0.0.1`，一旦监听非回环地址又没有配置凭证，必须直接启动失败而不是「降级为无认证公网服务」。
- 生产代码：
  - `httpapi/auth.go:17-27`（`authorized`：未配置凭证才放行；SHA-256 + `subtle.ConstantTimeCompare` 常量时间比较）
  - `httpapi/handler.go:45-52`、`:87-104`（两个端点都是「认证 → Content-Type → 读 body → 解析 → 会话预检 → 执行」）
  - `httpapi/config.go:71-91`、`:136-142`（`Validate` + `isLoopbackHost`：空主机/公网/IP 主机名一律按非回环，缺凭证即报错）
- 关键测试：
  - `httpapi/auth_test.go:TestAuthMissingAndWrongCredentialAreIdentical`（两种凭证失败的响应体逐字节相同、`401`、`Runner` 计数 0、无会话写入、不回显凭证）
  - `httpapi/auth_test.go:TestAuthPrecedesBodyParsing`（错误 Content-Type / 2MiB body 也一律先 401）
  - `httpapi/config_test.go:TestConfigLoopbackBoundary` + `TestNewFailClosedWithoutListening`
- 基线验证：
  ```bash
  go test ./httpapi/ -run 'TestAuth|TestConfigLoopbackBoundary|TestNewFailClosedWithoutListening' -count=1 -v
  ```
  预期：全部 `--- PASS`。
- 可选 Mutation：把 `httpapi/auth.go` 的 `authorized` 改成恒 `return true`。
- 预期失败：`TestAuthMissingAndWrongCredentialAreIdentical`、`TestAuthPrecedesBodyParsing`、`TestAuthAcceptsConfiguredCredential` 必失败，`cmd/mini-agent` 的端到端测试同样失败——Cleaner 已实测。
- 恢复确认：`git checkout -- httpapi/auth.go` 后重跑同一命令应重新全部 PASS。

## 可直接手工复核的运行证据（不需要改代码）

```bash
go build -o /tmp/mini-agent ./cmd/mini-agent

# 1) 非回环监听 + 无凭证 → 必须启动失败、不监听
env -i MINI_AGENT_LISTEN_ADDR=0.0.0.0:18099 MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:1/v1 \
  MINI_AGENT_MODEL=m /tmp/mini-agent ; echo "exit=$?"
# 期望：打印「非回环地址时必须配置认证凭证」，exit=1（实测一致）

# 2) 回环 + 凭证 → /healthz 免认证、其余端点 401→201
env -i MINI_AGENT_LISTEN_ADDR=127.0.0.1:18099 MINI_AGENT_AUTH_TOKEN=smoke-token \
  MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:1/v1 MINI_AGENT_MODEL=m /tmp/mini-agent &
curl -si http://127.0.0.1:18099/healthz | head -1                                  # 期望 200
curl -s -o /dev/null -w '%{http_code}\n' -X POST -H 'Content-Type: application/json' \
  -d '{}' http://127.0.0.1:18099/v1/sessions                                      # 期望 401
curl -s -X POST -H 'Content-Type: application/json' -H 'Authorization: Bearer smoke-token' \
  -d '{"session_id":"nope","message":"hi"}' http://127.0.0.1:18099/v1/chat          # 期望 404 session_not_found
```
