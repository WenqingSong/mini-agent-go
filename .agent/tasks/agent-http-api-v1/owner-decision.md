# Owner Decision

## Review Target

`3ce1869b2c1206464d59cd70ee6f1f820a2b3f43`（`feat(httpapi): 交付 Agent Runtime HTTP JSON API、同 Turn 装配层与可启动入口`）

## Core Logic

- CL-001：一次 chat = 一次真实会话执行权，用户消息与产出同事务——本轮用户消息与 Agent 产出必须落在同一次 `session.Manager.RunTurn`，失败轮次整轮丢弃；否则会话留下「只有提问、没有回答」或半轮工具调用的脏历史，下一轮模型基于脏历史作答，历史甚至可能不再通过 `core.ValidateMessages`。
- CL-002：认证先于解析与执行，默认只监听回环，非回环缺凭证不启动——未认证请求在读 body、解析、获取执行权之前被拒绝；缺失与错误凭证返回逐字节相同的 `401`（不可枚举）；监听非回环地址又无凭证必须启动失败，不得降级为无认证公网服务。

## Owner Decision

ACCEPTED

## Decision Evidence

- 基础：Cleaner 结论 `CLEAN`，`review.target = 3ce1869b2c1206464d59cd70ee6f1f820a2b3f43`；`owner-gate-start` Gate 于本 Session PASS，CLEAN Validity 有效（该 commit 之后仅 `976f681` / `d3a055a` 等 review-neutral tail，未发生实质变更）。
- OwnerGate 独立复跑基线：`go test ./service/ -run 'TestRunUsesExactlyOneRealRunTurn|TestRunProviderFailureLeavesNoPartialHistory|TestRunSecondTurnSeesPreviousHistory' -count=1` 与 `go test ./httpapi/ -run 'TestAuth|TestConfigLoopbackBoundary|TestNewFailClosedWithoutListening' -count=1` 全部 `--- PASS`；前述 mutation 结论引自 Cleaner 隔离 worktree 实测（M1/M2）。
- Owner 于 OwnerGate Decision Checkpoint 明确回复 `ACCEPT`，接受对象为上述具体 CLEAN snapshot，而非抽象接受任务。
- 适用范围：本接受仅覆盖 `3ce1869` snapshot。此后若 `httpapi/**`、`service/**`、`cmd/mini-agent/**`、`docs/design/http-api.md` 或任务约束出现实质变化，CLEAN 与本 owner 决定一并失效，需重新审查并重新取得 Owner 决定。
- 非阻塞风险接受：`CLEAN-001`（P3，OPEN）关闭耗时最坏约 2×`ShutdownGrace`（`httpapi/server.go:149-174`），Owner 未要求本次收紧，不影响正确性；若后续需要确定上界，走 Contract/Design 级变更。
