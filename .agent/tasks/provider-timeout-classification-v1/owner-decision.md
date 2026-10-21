# Owner Decision

## Review Target

`43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`（`fix(httpapi): 让 Provider 自身超时统一映射为 provider_failure`）

## Core Logic

- CL-001：已形成的 `*core.RunError.Kind` 是 HTTP 错误分类的唯一权威依据；`Cause` 中的 `context.DeadlineExceeded` / `context.Canceled` 不得覆盖该结论（生产位置 `httpapi/errors.go` 的 `errorCodeOf`，`errors.As` 命中 `*core.RunError` 后只按最外层 `Kind` 映射）。
- CL-002：仅当错误链中不存在 `*core.RunError`（未形成 Run 结论）时，才用裸 `context.DeadlineExceeded` / `context.Canceled` 作为兜底，分别映射 `504 deadline_exceeded` / `408 cancelled`。

## Owner Decision

ACCEPTED

## Decision Evidence

Owner 于 2026-10-21 明确回复「Owner Decision：ACCEPT」，接受 Review Target `43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`，并认可本次 `provider-timeout-classification-v1` 的 CLEAN 结论及上述两项核心机制（原文：「已形成的 `RunError.Kind` 是 HTTP 错误分类的权威依据，底层 Cause 中的 Context 哨兵不得覆盖它」「仅在不存在 `RunError` 时，使用裸 Context 超时或取消作为兜底分类」）。

适用范围与限制：

- 本接受绑定 Cleaner 已 CLEAN 的该具体 snapshot（`review.target = 43ce197`），不是「抽象地接受本任务」。
- Owner 接受 Cleaner 报告的三项非阻塞风险：默认生产装配未设置 Provider `Timeout` 导致该形状可达途径有限；直接错误路径中无 `*core.RunError` 的裸 `*url.Error` 仍映射 `504`；`KindInvalidArgument` 携带 ctx `Cause` 由 `504` 改为 `internal`。
- 本轮不扩大修复范围、不新增 Provider `Timeout` 配置、不继续修改生产代码。
- 本次 ACCEPT 不等于授权合并 `develop`，也不代表 `agent-runtime-e2e-tests-v1` 已验收通过；`develop` 集成需 Owner 独立授权，原 E2E 任务在修复正式进入 `develop` 后按 Owner 指示恢复并重跑全部端到端测试（重点 `TestProviderUnresponsive`）。
