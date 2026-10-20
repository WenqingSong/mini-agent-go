# Owner Decision

## Review Target

`5633001ac0b21820f230d773631f063cb2f71206`

Task: `llm-openai-client-v1`（OpenAI-compatible LLM Client v1）

## Core Logic

- CL-001：错误种类分流决定 Run 终态（`cancelled` / `deadline_exceeded` → `CANCELLED`，`provider_failure` → `FAILED`），且默认只发一次请求（429 / 5xx 不重发）。
- CL-002：工具调用 `arguments` 以原始 JSON 字节透传、`tool_calls` 顺序保持，契约层不解析、不改写。

## Owner Decision

REJECTED

## Decision Evidence

Owner 明确原文："当前暂不 ACCEPT T01 的 Review Target"，"我认可 Cleaner 对 CL-001、CL-002 的验证结果，但要求在最终接受前修复以下问题"。

适用范围与条件：

1. 本轮不接受的仅是 `5633001ac0b21820f230d773631f063cb2f71206` 这个 snapshot 的最终接受；CL-001 / CL-002 两项核心机制的验证结论 Owner 已认可，无需重新解释。
2. `CLEAN-002`（`choices[].message` 缺失或为 `null` 被解析为合法空 assistant 成功响应）要求修复，在现有 T01 Scope 内由 Coder 处理，修复要求：
   - 缺失 / `null` 的完整 assistant message 必须返回明确的 Provider 协议错误（`provider_failure`）；
   - 新增回归测试证明该异常响应不会被解析为成功；
   - 保持合法 Tool Calling 响应兼容，包括 `message` 对象存在、`content = null` 但含有效 `tool_calls`；
   - 不引入额外 API 请求或自动重试，不调用真实付费模型。
3. `CLEAN-001`（BaseURL 携带 query / fragment 被静默丢弃）本轮暂缓，作为 P3 保留，不要求修复，不扩大 Scope。
4. 修复必须形成新的 Implementation Evidence Commit，并由 Cleaner 针对新版本复审重新取得 `CLEAN`；不得沿用 `5633001` 的审查结论。
5. 不得提前标记 `ACCEPTED`，不得绕过 Cleaner 复审，不得合并到 `develop`。

`owner.review_target` 保持为空：本轮未接受任何 snapshot。
