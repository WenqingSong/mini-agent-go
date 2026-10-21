# Owner Core Logic 验证卡

被审查对象（CLEAN 绑定的实现 Evidence Commit）：`8e4d2fd`。
本文件只承载 Core Logic 验证卡与因果说明，不承载 Owner 决策状态。

## CL-001：脱敏边界——允许清单 + 未知键省略 + 正文只留长度

- Owner 需要理解：`Data` 里**只有**白名单键会原样落盘；凭据类键整体替换为 `[REDACTED]`，用户正文只留 `[REDACTED:len=N]`，其余未知键连键名一起丢弃。任何一条规则失效，用户的原始正文或 API Key 就会出现在日志里。
- 生产代码：`runlog/sanitize.go:217-240`（`object` 的固定优先级判定：允许清单 → 敏感 → 正文 → 未知省略）、`runlog/sanitize.go:188-214`（非法/超 64 KiB 载荷只留标记）、`runlog/sanitize.go:34-101`（允许清单、敏感片段、正文键、凭据正则）、`runlog/slog.go:99-127`（`error_message` 同值级脱敏；`data` 走脱敏结果）
- 关键测试：
  - `runlog/sanitize_test.go:195` `TestSanitizeDataOmitsUnknownKeys`（未知键名与值都不出现）
  - `runlog/sanitize_test.go:72` `TestSanitizeDataRedactsSensitiveKeys`（`api_key`/`apiKey`/`X-Api-Key`/`authorization`/`token`/`secret`/`password`/私钥/嵌套容器）
  - `runlog/sanitize_test.go:150` `TestSanitizeDataRedactsUserContent`（正文键 → 长度摘要）
  - `runlog/slog_test.go:288` `TestRecordRedactsSecretsAndContent`（对**完整输出字节**做泄漏扫描，并断言 `model`/`input_tokens` 仍在）
- 基线验证：`go test -count=1 -run 'TestSanitize|TestRecordRedacts' ./runlog/` → 全部 PASS
- 可选 Mutation：把 `runlog/sanitize.go:235-237` 的 `default:` 分支由「整键省略」改为 `out[key] = m[key]`
- 预期失败：`TestSanitizeDataOmitsUnknownKeys`（`foo`/`bar-value` 出现在输出）、`TestRecordRedactsSecretsAndContent`（`unknown-value` 泄漏）；Cleaner 已实测两者 FAIL
- 恢复确认：恢复 `default:` 分支为空后重跑同命令 → `ok`（Cleaner 已实测 `ok 0.027s`）

## CL-002：日志旁路——Sink 只读事件，不改变 Run 结论

- Owner 需要理解：日志只能观察。若 Sink 为了脱敏「就地」改写调用方传入的 `*core.RunError`，错误原文会被 `[REDACTED]` 或 256B 截断污染，调用方据此构造的 `RunResult`/工具消息就会变成假事实。
- 生产代码：`runlog/slog.go:54-80`（`Record`：只读入参、`recover` 隔离 handler panic、错误原样返回不吞不抛）、`runlog/slog.go:83-127`（`recordAttrs` 用 `sanitizeString(ev.Error.Message)` 的**返回值**构造字段，不回写 `Event`）
- 关键测试：`runlog/slog_test.go:511` `TestRecordIsSideEffectFree`
  - `runlog/slog_test.go:475-507` 用 `snapshotEvent` 对 `*core.RunError` 做值拷贝后比较（覆盖 >256B 与含 `Bearer` 的错误文本）
  - `runlog/slog_test.go:600-641` 用 `runScenario`（由事件序列推导 `RunResult`）比较「接入失败 Sink / 不接入 / 独立副本期望值」三路结果
- 基线验证：`go test -count=1 -run TestRecordIsSideEffectFree ./runlog/` → PASS
- 可选 Mutation：在 `runlog/slog.go:104-107` 改为先 `ev.Error.Message = sanitizeString(ev.Error.Message)` 再写 `slog.String("error_message", ev.Error.Message)`
- 预期失败：`TestRecordIsSideEffectFree/写入必然失败的_writer`（`slog_test.go:595` 打印调用前后快照差异）与同测试的 `slog_test.go:619`「Run 结果受日志接入影响」；Cleaner 已在隔离副本实测两处 FAIL
- 恢复确认：恢复为直接把 `sanitizeString(...)` 的返回值传给 `slog.String` 后重跑 → PASS（Cleaner 已实测全绿）

## 补充说明（不需要 Owner 逐行验证）

- 状态语义（不合成终态、`cancelled` 与 `deadline_exceeded` 可分）由 `runlog/slog.go:140-150` 与 `TestRecordFailureLocatesStageAndKind` 覆盖；Cleaner 用「无错误事件返回 `COMPLETED`」的 Mutation 验证过该测试会失败。
- 并发不交织由单一 `slog.JSONHandler` 的共享 mutex 保证（`runlog/slog.go:40-45`），`TestRecordConcurrentRecordsIntact` + `go test -race ./runlog/...` 提供证据。
