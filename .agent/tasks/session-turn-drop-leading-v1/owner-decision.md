# Owner Decision

## Review Target

`2ed50f1c225323dcf392aaea403758576513dcaf`（C1，`feat/session-turn-drop-leading-v1`）

有效性：`owner-gate-start` Gate PASS；`2ed50f1..HEAD` 仅任务产物（`core-logic.md` / `findings.md` / `state.yaml`）变化，`session` / `core` / `contextmgr` / `docs` / `go.mod` / `Makefile` 零变化，CLEAN 未失效。

## Core Logic

- CL-001：`DropLeading(n)` 只删「进入 Turn 时已提交历史」(`Turn.base`) 的前 n 条，本 Turn `Append` 新增消息永不被删到；仅整轮提交（`commitTurn` 用 `staged()` 全量替换快照）才生效，回调出错 / 取消 / 超时则整轮丢弃。
- CL-002：删除边界在调用点就用 `core.ValidateMessages` 校验「剩余历史 + 暂存消息」，违反配对立即以 `invalid_pairing`（带 `CallID`）原子拒绝，不截断 `base`、不关闭 Turn。

## Owner Decision

ACCEPTED

## Decision Evidence

2026-10-20，Owner 明确回复 ACCEPT，依据与适用范围如下（OwnerGate 仅机械持久化，未替 Owner 决策）：

- **接受对象**：仅 `2ed50f1c225323dcf392aaea403758576513dcaf` 这个 Cleaner 已 CLEAN 的具体 snapshot，不是「抽象地接受本任务」。
- **接受依据**：`owner-gate-start` Gate 已 PASS；CLEAN 仍有效；前缀删除与 Tool Call / Tool Result 配对保护两项核心机制均有独立验证与有效 Mutation 证据（CL-001 变异为删尾部时 5 个测试 FAIL；CL-002 变异为删除调用点校验时 `TestTurnDropLeadingRejectsInvalidPairingBoundary` 3/3 子用例 FAIL）。
- **CLEAN-001（P3）**：本轮暂不修复，不阻塞 ACCEPT。当前问题仅涉及测试命名与场景区分度，不要求扩大本任务范围；如后续需要，可单独完善测试。
- **边界声明**：本次 ACCEPT 不代表 Deliverer 已完成交付验收，也不代表已授权合并到 `develop`；不得因此跳过任何后续 Gate。若 `delivery-start` 未通过，须保留真实状态并报告阻塞原因。

## State Binding

- `state.owner.status = ACCEPTED`
- `state.owner.review_target = 2ed50f1c225323dcf392aaea403758576513dcaf`
