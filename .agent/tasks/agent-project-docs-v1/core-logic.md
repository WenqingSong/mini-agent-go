# 核心逻辑与验证卡

本任务无生产代码，核心价值是「文档事实可被机械核对」。因此两张卡验证的不是算法，而是**文档断言与仓库/运行时事实之间的绑定关系**：一旦绑定断了，读者会按错误事实操作。

审查结论与详细证据见 `findings.md`（Result: CLEAN，`review.target` = `05a49e8`）。

## CL-001：AI 使用记录 ↔ `state.yaml` 证据绑定（本轮修复的那条链）

- Owner 需要理解：`docs/ai-assisted-development.md` 的「实现 commit」列声称每行最后一个 SHA 就是「最终被 Owner 接受的实现快照」，且必须等于该任务 `state.yaml` 的 `review.target`。这条绑定断了，Owner 与后续角色会照着一个已被 Cleaner 判失效的实现理解历史——本轮 CLEAN-001 正是这个错误。
- 生产代码：本任务无生产代码。机制落点为文档侧规则与数据：`docs/ai-assisted-development.md:35`（字段口径）、`:38-51`（表），事实源为 `.agent/tasks/<task>/state.yaml` 的 `review.target` / `owner.review_target` / `delivery.review_target`。
- 关键测试：本任务无自动化测试（纯文档）。替代证据是本轮 Cleaner 执行的机械核对（命令见 `findings.md`「Verification」，逐行取最后一 SHA 与 `state.yaml` 比对），以及反例本身：已失效的 `08cd0ac`（`runtime-observability-v1` 首轮 C1）与被接受的 `8e4d2fd`。
- 基线验证：运行该核对脚本 → 期望 10 个已完成任务全部 `OK`（`agent-project-docs-v1` 自引用行无 SHA，指向 `state.yaml`）。
- 可选 Mutation：把 `docs/ai-assisted-development.md` 中 `runtime-observability-v1` 行最后一个 SHA 由 `8e4d2fd` 改回 `08cd0ac`（即回退 CLEAN-001 修复）。
- 预期失败：脚本该行输出 `MISMATCH`（`last=08cd0ac` vs `review.target=8e4d2fd`）；同时 `sed -n '1,20p' .agent/tasks/runtime-observability-v1/findings.md` 会显示 `08cd0ac` 是「首轮 C1（已失效）」。
- 恢复确认：`git checkout -- docs/ai-assisted-development.md` 后重跑核对脚本 → 恢复 10/10 `OK`，`git status --short` 为空。

## CL-002：README 承诺的失败语义 = 真实服务行为

- Owner 需要理解：README 写「Provider 不可用时 `POST /v1/chat` 返回 `502 provider_failure`」。这是使用者区分「配置错误 / 用法错误」与「模型服务不可用」的唯一线索；文档若与实现漂移，使用者会把服务故障误判为自己的调用错误（或反之）。
- 生产代码：`httpapi/errors.go:102`（`provider_failure` → `502`，唯一映射点）、`agent/run.go:229-240`（模型调用失败一律归 `provider_failure`）、`httpapi/handler.go:126-134`（错误封套写出）；文档断言在 `README.md:276-282`。
- 关键测试：`httpapi` 包既有测试 + `README.md:253-274` 的三条 `curl`。本轮 Cleaner 已实跑，结果与文档一致。
- 基线验证：
  ```bash
  go build -o /tmp/mini-agent ./cmd/mini-agent
  MINI_AGENT_LLM_BASE_URL=http://127.0.0.1:9/v1 MINI_AGENT_MODEL=placeholder-model /tmp/mini-agent
  # 另一个终端：
  curl -sS -i http://127.0.0.1:8080/healthz            # 200 {"status":"ok"}
  curl -sS -i -X POST http://127.0.0.1:8080/v1/sessions -H 'Content-Type: application/json' -d '{}'   # 201 + session_id
  curl -sS -i -X POST http://127.0.0.1:8080/v1/chat -H 'Content-Type: application/json' \
    -d '{"session_id":"<上一步 id>","message":"你好"}'  # 502 provider_failure
  ```
- 可选 Mutation：在隔离副本（如 `git worktree add --detach /tmp/cl / 05a49e8`）把 `httpapi/errors.go` 中 `CodeProviderFailure` 的状态改为 `http.StatusOK`。
- 预期失败：第 3 条 `curl` 返回 `200`，与 `README.md:276-282` 的 `502` 断言直接冲突——说明该文档断言可被真实运行证伪，不是自证式描述。
- 恢复确认：丢弃隔离副本（`git worktree remove --force /tmp/cl`），主工作区重跑 `go test ./httpapi/... -count=1` 与该条 `curl`，恢复 `502`；`git status --short` 为空。
