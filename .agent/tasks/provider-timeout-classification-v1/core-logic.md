# Core Logic

审查对象：`43ce197ee61ff00ac5b78ec866beae1b42c4bfaf`（详见 `findings.md` Review Target）。

## CL-001：Run 结论唯一权威（`Cause` 中的 ctx 哨兵不得覆盖已完成分类）

- Owner 需要理解：一次 Provider 自身超时已经得出结论「模型服务失败」（`provider_failure` / `FAILED`）。如果 HTTP 层因为错误链深处恰好出现 `context.DeadlineExceeded` 就改判 `504 deadline_exceeded`，客户端会把「模型服务故障」误读为「自己等太久」，同一请求的响应、Run 终态与运行日志三处互相矛盾。
- 生产代码：`httpapi/errors.go:180-202`（`errorCodeOf` 中「链中存在 `*core.RunError` → 只按最外层 `Kind` 映射」分支）。
- 关键测试：
  - `httpapi/handler_test.go:703` `TestProviderFailureClassificationEndToEnd`（子用例「客户端自身超时（链含 `context.DeadlineExceeded`）」：真实 `openai.Client` 50ms 超时 + 500ms 慢上游，断言 `502 + provider_failure`、`run_finished=FAILED/provider_failure`、`RunOutcome=FAILED/provider_failure`，并断言错误链确实含 ctx 哨兵）；
  - `httpapi/handler_test.go:233` / `:255` / `:267`（表驱动：`provider_failure` + `Cause=DeadlineExceeded` / `Canceled` / 多层包装 → 一律 `502`）。
- 基线验证：`go test -count=1 ./httpapi/` → `ok`；`go test -race -count=1 ./...` → 全包 `ok`。
- 可选 Mutation：在 `httpapi/errors.go` 的 `errors.As(err, &runErr)` 分支之前插入 `errors.Is(err, context.DeadlineExceeded)` / `errors.Is(err, context.Canceled)` 的提前返回（即恢复修复前的判定顺序）。
- 预期失败：`TestChatErrorMapping/provider 失败且 Cause 为 ctx 超时`、`.../Cause 为 ctx 取消`、`.../Cause 为多层包装的 ctx 哨兵`、`.../取消终态且 Cause 为 ctx 超时`、`.../invalid_argument 携带 ctx 超时` 与 `TestProviderFailureClassificationEndToEnd/客户端自身超时（链含 context.DeadlineExceeded）` 全部失败（期望 502 实际 504/408）。
- 恢复确认：删除该 Mutation 后 `go test -count=1 ./httpapi/` 全绿。

## CL-002：没有 Run 结论时才允许 Context 哨兵兜底（直接 ctx 超时 / 取消不回归）

- Owner 需要理解：请求级超时、客户端取消这类「还没形成 Run 结论」的错误必须继续按 `504 deadline_exceeded` / `408 cancelled` 表达；如果为了修 CL-001 而把 ctx 兜底整段删掉，这些真实场景会退化成 `500 internal`。
- 生产代码：`httpapi/errors.go:204-211`（`errors.As` 未命中 `*core.RunError` 后才检查裸 `context.DeadlineExceeded` / `context.Canceled`）。
- 关键测试：`httpapi/errors_test.go:113` `TestErrorCodeOf`（裸 ctx 子用例）、`httpapi/handler_test.go:335` / `:329` / `:311` / `:317`（`Runner` 返回 ctx 超时/取消及其包装 → `504` / `408`）、`httpapi/handler_test.go:784` `TestRequestTimeoutClassificationEndToEnd`（请求级超时 → `504` + `CANCELLED/deadline_exceeded`，含「尚未发起模型调用」分支）。
- 基线验证：`go test -count=1 ./httpapi/` → `ok`。
- 可选 Mutation：删除 `httpapi/errors.go` 中兜底的两段 `errors.Is(ctx…)` 判断（直接 `return CodeInternal`）。
- 预期失败：`TestErrorCodeOf/ctx 超时`、`TestErrorCodeOf/ctx 取消`、`TestChatErrorMapping/Runner 返回 ctx 超时`、`.../Runner 返回 ctx 取消`、`.../Runner 返回包装的 ctx 超时`、`.../Runner 返回包装的 ctx 取消` 全部失败（得到 `500 internal`）。
- 恢复确认：恢复兜底分支后 `go test -count=1 ./httpapi/` 全绿。
