# Cleaner Core Logic（Owner 验证卡）

审查对象：C1 = `2ed50f1c225323dcf392aaea403758576513dcaf`（`feat/session-turn-drop-leading-v1`）
定位：T07 上下文压缩要在**一次 `RunTurn`** 内完成「删历史前缀 + 写摘要 + 追加新消息」的原子落库；下面两张卡是这条能力真正的因果链。

## CL-001：前缀删除只删「进入 Turn 时的已提交历史」，且必须经整轮提交才生效

- Owner 需要理解：`DropLeading(n)` 删的是 `Turn.base` 这个「进入本 Turn 时的已提交历史」的前 n 条，`n` 相对**当前剩余**累计；本 Turn 用 `Append` 新增的消息永远不会被删到。它只在 Turn 的暂存区生效，只有回调成功且 `ctx` 有效时，`commitTurn` 才用 `staged()` 的「剩余历史 + 暂存消息」**全量替换**会话快照——删错对象或只改读视图不改提交视图，都会让 T07 写回错误历史。
- 生产代码：`session/turn.go:87-114`（`DropLeading`：关闭检查 → 合法域 → 截断 `t.base`）、`session/turn.go:232-241`（`staged()`：`base + messages` 作为提交内容）
- 关键测试：`session/turn_test.go:343 TestTurnDropLeadingCommitsTruncatedHistory`（走 `RunTurn` + `Manager.Get`，逐条比对 ID / 顺序 / 内容与条数 `m-n+k`，含 `n=0` 与 `n=len(base)`）
- 基线验证：`GOPROXY=off go test -count=1 -race -run TestTurnDropLeadingCommitsTruncatedHistory ./session` → PASS
- 可选 Mutation：把 `session/turn.go:102` 的 `remaining := t.base[n:]` 改成 `remaining := t.base[:len(t.base)-n]`（变成删尾部）
- 预期失败：上面这条测试失败（`提交后的已提交历史` 指纹不符）；同时 `RejectsInvalidCountAtomically` / `RejectsInvalidPairingBoundary` / `CombinesWithSummaryAndAppend` / `FailureDiscardsWholeTurn` 一起失败（Cleaner 实测 5 个测试 FAIL）
- 恢复确认：还原该行为 `t.base[n:]` 后重跑 `GOPROXY=off go test -count=1 -run TestTurnDropLeading ./session` → 全绿

## CL-002：删除边界必须在调用点就保住 Tool Call / Tool Result 配对

- Owner 需要理解：前缀删除可能把 Tool Call 留下、把结果删掉之外，更常见的是「剩余历史以孤儿 Tool Result 开头」（结果还在、调用被删）。这种序列一旦提交，下一次模型调用就会带上非法历史。因此 `DropLeading` 不是「先删，等提交时再校验」，而是在**调用点**用 `core.ValidateMessages` 校验「剩余历史 + 暂存消息」，失败立即以 `invalid_pairing`（带 `CallID`）原子拒绝，Turn 不关闭、可换合法边界继续。
- 生产代码：`session/turn.go:102-108`（拼装 `remaining + t.messages` 并校验，失败直接返回、不截断 `t.base`）
- 关键测试：`session/turn_test.go:455 TestTurnDropLeadingRejectsInvalidPairingBoundary`（三个子用例：切断调用与结果、残留孤儿结果、带暂存消息；断言 `CallID` 精确、调用前后视图指纹相同，随后 `DropLeading(3)` 在同 Turn 内成功提交）
- 基线验证：`GOPROXY=off go test -count=1 -race -run TestTurnDropLeadingRejectsInvalidPairingBoundary ./session` → PASS
- 可选 Mutation：删除 `session/turn.go:106-108` 的 `core.ValidateMessages` 校验（改为 `_ = combined`）
- 预期失败：该测试三个子用例全部 FAIL（删除被接受，或错误种类不是 `invalid_pairing`）；Cleaner 实测 3/3 子用例 FAIL
- 恢复确认：还原校验后重跑同一条命令 → 全绿（提交时的 P5 校验仍在，属于第二道闸门，不能替代本卡）
