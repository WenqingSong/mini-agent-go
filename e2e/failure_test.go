package e2e_test

// 本文件覆盖 AC-003（可恢复工具失败继续运行）与 AC-004（provider 4xx/5xx、双预算耗尽）。

import (
	"net/http"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// failureSeedTodos 是失败路径预置的 Todo：用于证明失败/回滚不破坏既有状态。
func failureSeedTodos() []session.Todo {
	return []session.Todo{
		{ID: "seed-1", Title: "失败前已存在", Status: session.TodoStatusInProgress},
	}
}

// TestRecoverableToolFailuresContinue 证明 AC-003 的可恢复失败：领域错误、未知工具与
// 参数非法都作为 tool 消息回填（IsError=true），下一次模型请求可见带前缀的错误内容，
// Run 仍可正常结束并整轮提交。
func TestRecoverableToolFailuresContinue(t *testing.T) {
	const (
		finalReply = "已处理失败并继续"
		callID     = "call-recoverable"
	)
	cases := []struct {
		name         string
		call         scriptCall
		wantExact    string
		wantContains []string
	}{
		{
			name:      "领域错误_除零",
			call:      scriptCall{ID: callID, Name: "calculator", Arguments: `{"op":"div","a":9,"b":0}`},
			wantExact: `{"error":{"code":"division_by_zero","message":"除数不能为 0"}}`,
		},
		{
			name:      "未知工具名",
			call:      scriptCall{ID: callID, Name: "unknown_tool_xyz", Arguments: `{}`},
			wantExact: "未知工具: unknown_tool_xyz",
		},
		{
			name:         "参数非法_缺少必填字段",
			call:         scriptCall{ID: callID, Name: "calculator", Arguments: `{"op":"add","a":1}`},
			wantContains: []string{"参数校验失败", "$.b"},
		},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{
				maxLLMCalls:  4,
				maxToolCalls: 4,
				script: []llmStep{
					toolCallsStep(item.call),
					textStep(finalReply),
				},
			})
			sessionID := h.createSession()
			h.seed(sessionID, "既有摘要", failureSeedTodos())

			response := h.chatOK(sessionID, "调用会失败的工具")
			if response.Reply != finalReply {
				t.Fatalf("reply = %q，期望 %q", response.Reply, finalReply)
			}
			if response.LLMCalls != 2 || response.ToolCalls != 1 {
				t.Fatalf("调用计数 = (llm=%d, tool=%d)，期望 (2, 1)", response.LLMCalls, response.ToolCalls)
			}
			h.assertTerminal(h.runAt(1), core.RunStatusCompleted, "")

			// Session：错误结果作为 tool 消息提交，整轮仍然合法。
			h.assertMessageRoles(sessionID, e2eRoleUser, e2eRoleAssistant, e2eRoleTool, e2eRoleAssistant)
			committed := h.snapshot(sessionID)
			result := toolMessageOf(t, committed, callID)
			if !result.ToolResult.IsError {
				t.Fatalf("tool 结果未被标记为可恢复错误：%+v", result.ToolResult)
			}
			content := result.ToolResult.Content
			if item.wantExact != "" && content != item.wantExact {
				t.Fatalf("tool 结果内容 = %q，期望 %q", content, item.wantExact)
			}
			for _, want := range item.wantContains {
				if !strings.Contains(content, want) {
					t.Fatalf("tool 结果内容 = %q，期望包含 %q", content, want)
				}
			}
			if committed.Summary != "既有摘要" || len(committed.Todos) != 1 {
				t.Fatalf("成功提交覆盖了预置状态：%s", describeSession(committed))
			}

			// wire：下一次模型请求能看到错误内容，且带固定前缀与正确 CallID。
			_, secondRequest := h.llm.requestAt(t, 2)
			if len(secondRequest.Messages) != 4 {
				t.Fatalf("第二次模型请求消息数 = %d，期望 4（摘要 system + user + assistant + tool）", len(secondRequest.Messages))
			}
			refilled := secondRequest.Messages[len(secondRequest.Messages)-1]
			if refilled.Role != string(e2eRoleTool) || refilled.ToolCallID != callID {
				t.Fatalf("回填消息 = %+v，期望 tool 消息且 ToolCallID=%q", refilled, callID)
			}
			if refilled.Content != "工具执行失败: "+content {
				t.Fatalf("wire 错误内容 = %q，期望 %q", refilled.Content, "工具执行失败: "+content)
			}

			h.assertRunCount(1)
			h.llm.assertScriptConsumed(t)
		})
	}
}

// TestProviderFailures 证明 AC-004 的 provider 4xx / 5xx：HTTP 502 + provider_failure、
// Run 终态 FAILED，且响应不泄漏上游载荷、会话不留下半轮。
func TestProviderFailures(t *testing.T) {
	const upstreamMarker = "upstream-body-marker-7d1"
	cases := []struct {
		name   string
		status int
	}{
		{name: "provider_4xx", status: http.StatusBadRequest},
		{name: "provider_5xx", status: http.StatusInternalServerError},
	}

	for _, item := range cases {
		t.Run(item.name, func(t *testing.T) {
			h := newHarness(t, harnessOptions{
				maxLLMCalls:  4,
				maxToolCalls: 4,
				script: []llmStep{
					statusStep(item.status, `{"error":{"message":"`+upstreamMarker+`"}}`),
				},
			})
			sessionID := h.createSession()
			h.seed(sessionID, "既有摘要", failureSeedTodos())
			before := h.snapshot(sessionID)

			result := h.chat(sessionID, "触发 provider 失败")
			payload := h.assertErrorResponse(result, http.StatusBadGateway, "provider_failure")
			if payload.Error.Message != "模型服务调用失败" {
				t.Fatalf("错误 message = %q，期望固定的服务端文案", payload.Error.Message)
			}
			if strings.Contains(string(result.Body), upstreamMarker) {
				t.Fatalf("响应泄漏了上游载荷：%s", result.Body)
			}

			h.assertSessionUnchanged(before)
			run := h.runAt(1)
			h.assertTerminal(run, core.RunStatusFailed, core.KindProviderFailure)
			if run.Outcome.LLMCalls != 1 || run.Outcome.ToolCalls != 0 {
				t.Fatalf("Run 计数 = (llm=%d, tool=%d)，期望 (1, 0)", run.Outcome.LLMCalls, run.Outcome.ToolCalls)
			}
			if got := h.llm.requestCount(); got != 1 {
				t.Fatalf("模型请求次数 = %d，期望 1（失败后不重试）", got)
			}
			h.assertRunCount(1)
		})
	}
}

// TestBudgetLimits 证明 AC-004 的双预算耗尽：分别得到 429 + llm_call_limit /
// tool_call_limit 与 LIMIT_EXCEEDED，且不产生半轮提交、超出预算的调用不会被发出。
func TestBudgetLimits(t *testing.T) {
	t.Run("MaxLLMCalls耗尽", func(t *testing.T) {
		h := newHarness(t, harnessOptions{
			maxLLMCalls:  2,
			maxToolCalls: 5,
			script: []llmStep{
				toolCallsStep(scriptCall{ID: "call-1", Name: "calculator", Arguments: `{"op":"add","a":1,"b":1}`}),
				toolCallsStep(scriptCall{ID: "call-2", Name: "calculator", Arguments: `{"op":"add","a":2,"b":2}`}),
			},
		})
		sessionID := h.createSession()
		h.seed(sessionID, "既有摘要", failureSeedTodos())
		before := h.snapshot(sessionID)

		result := h.chat(sessionID, "持续请求工具直到模型预算耗尽")
		h.assertErrorResponse(result, http.StatusTooManyRequests, "llm_call_limit")
		h.assertSessionUnchanged(before)

		run := h.runAt(1)
		h.assertTerminal(run, core.RunStatusLimitExceeded, core.KindLLMCallLimit)
		if run.Outcome.LLMCalls != 2 || run.Outcome.ToolCalls != 2 {
			t.Fatalf("Run 计数 = (llm=%d, tool=%d)，期望 (2, 2)", run.Outcome.LLMCalls, run.Outcome.ToolCalls)
		}
		if got := h.llm.requestCount(); got != 2 {
			t.Fatalf("模型请求次数 = %d，期望 2（第 3 次调用不得发出）", got)
		}
		h.assertRunCount(1)
	})

	t.Run("MaxToolCalls耗尽", func(t *testing.T) {
		h := newHarness(t, harnessOptions{
			maxLLMCalls:  6,
			maxToolCalls: 2,
			script: []llmStep{
				toolCallsStep(scriptCall{ID: "call-1", Name: "calculator", Arguments: `{"op":"add","a":1,"b":1}`}),
				toolCallsStep(
					scriptCall{ID: "call-2", Name: "calculator", Arguments: `{"op":"add","a":2,"b":2}`},
					scriptCall{ID: "call-3", Name: "calculator", Arguments: `{"op":"add","a":3,"b":3}`},
				),
			},
		})
		sessionID := h.createSession()
		h.seed(sessionID, "既有摘要", failureSeedTodos())
		before := h.snapshot(sessionID)

		result := h.chat(sessionID, "单轮请求超过剩余工具预算")
		h.assertErrorResponse(result, http.StatusTooManyRequests, "tool_call_limit")
		h.assertSessionUnchanged(before)

		run := h.runAt(1)
		h.assertTerminal(run, core.RunStatusLimitExceeded, core.KindToolCallLimit)
		if run.Outcome.LLMCalls != 2 || run.Outcome.ToolCalls != 1 {
			t.Fatalf("Run 计数 = (llm=%d, tool=%d)，期望 (2, 1)：第二批调用整批拒绝、一个都不执行",
				run.Outcome.LLMCalls, run.Outcome.ToolCalls)
		}
		if got := h.llm.requestCount(); got != 2 {
			t.Fatalf("模型请求次数 = %d，期望 2", got)
		}
		h.assertRunCount(1)
	})
}
