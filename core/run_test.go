package core

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// TestRunPolicyValidate 覆盖双预算必填正数（AC-003 / INV-004）。
func TestRunPolicyValidate(t *testing.T) {
	cases := []struct {
		name    string
		policy  RunPolicy
		wantErr bool
	}{
		{name: "合法双预算", policy: RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 1}},
		{name: "LLM 预算为零", policy: RunPolicy{MaxLLMCalls: 0, MaxToolCalls: 1}, wantErr: true},
		{name: "工具预算为零", policy: RunPolicy{MaxLLMCalls: 1, MaxToolCalls: 0}, wantErr: true},
		{name: "LLM 预算为负", policy: RunPolicy{MaxLLMCalls: -1, MaxToolCalls: 5}, wantErr: true},
		{name: "工具预算为负", policy: RunPolicy{MaxLLMCalls: 5, MaxToolCalls: -1}, wantErr: true},
		{name: "两者均为零", policy: RunPolicy{}, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.policy.Validate()
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("不应返回错误：%v", err)
				}
				return
			}
			expectKind(t, err, KindInvalidArgument)
		})
	}
}

// TestRunResultValidate 逐状态校验状态与错误的一致性（INV-003），不使用"终态必须有错误"的笼统规则。
func TestRunResultValidate(t *testing.T) {
	cases := []struct {
		name    string
		result  RunResult
		wantErr bool
	}{
		{
			name:   "RUNNING 无错误",
			result: RunResult{Status: RunStatusRunning, LLMCalls: 1, ToolCalls: 1},
		},
		{
			name:    "RUNNING 携带错误",
			result:  RunResult{Status: RunStatusRunning, Error: NewError(KindCancelled, "test", "取消")},
			wantErr: true,
		},
		{
			name:   "COMPLETED 无错误",
			result: RunResult{Status: RunStatusCompleted, LLMCalls: 2, ToolCalls: 1},
		},
		{
			name:    "COMPLETED 携带错误",
			result:  RunResult{Status: RunStatusCompleted, Error: NewError(KindProviderFailure, "llm.complete", "超时")},
			wantErr: true,
		},
		{
			name:   "FAILED + provider_failure",
			result: RunResult{Status: RunStatusFailed, Error: NewError(KindProviderFailure, "llm.complete", "5xx")},
		},
		{
			name: "FAILED + tool_failure 带 CallID",
			result: RunResult{
				Status: RunStatusFailed,
				Error:  &RunError{Kind: KindToolFailure, Op: "tool.execute", Message: "不可恢复", CallID: "call_1"},
			},
		},
		{
			name: "FAILED + tool_failure 缺 CallID",
			result: RunResult{
				Status: RunStatusFailed,
				Error:  NewError(KindToolFailure, "tool.execute", "不可恢复"),
			},
			wantErr: true,
		},
		{
			name:    "FAILED 缺少错误",
			result:  RunResult{Status: RunStatusFailed},
			wantErr: true,
		},
		{
			name:    "FAILED + cancelled",
			result:  RunResult{Status: RunStatusFailed, Error: NewError(KindCancelled, "run", "用户取消")},
			wantErr: true,
		},
		{
			name:   "LIMIT_EXCEEDED + llm_call_limit",
			result: RunResult{Status: RunStatusLimitExceeded, LLMCalls: 3, Error: NewError(KindLLMCallLimit, "loop", "LLM 调用达上限")},
		},
		{
			name:   "LIMIT_EXCEEDED + tool_call_limit",
			result: RunResult{Status: RunStatusLimitExceeded, ToolCalls: 5, Error: NewError(KindToolCallLimit, "loop", "工具调用达上限")},
		},
		{
			name:    "LIMIT_EXCEEDED + tool_failure",
			result:  RunResult{Status: RunStatusLimitExceeded, Error: NewError(KindToolFailure, "tool.execute", "失败")},
			wantErr: true,
		},
		{
			name:    "LIMIT_EXCEEDED 缺少错误",
			result:  RunResult{Status: RunStatusLimitExceeded},
			wantErr: true,
		},
		{
			name:   "CANCELLED + cancelled",
			result: RunResult{Status: RunStatusCancelled, Error: NewError(KindCancelled, "loop", "用户取消")},
		},
		{
			name:   "CANCELLED + deadline_exceeded",
			result: RunResult{Status: RunStatusCancelled, Error: NewError(KindDeadlineExceeded, "loop", "整体超时")},
		},
		{
			name:    "CANCELLED + provider_failure",
			result:  RunResult{Status: RunStatusCancelled, Error: NewError(KindProviderFailure, "llm.complete", "失败")},
			wantErr: true,
		},
		{
			name:    "CANCELLED 缺少错误",
			result:  RunResult{Status: RunStatusCancelled},
			wantErr: true,
		},
		{
			name:    "未知状态",
			result:  RunResult{Status: RunStatus("PAUSED")},
			wantErr: true,
		},
		{
			name:    "空状态",
			result:  RunResult{},
			wantErr: true,
		},
		{
			name:    "LLMCalls 为负",
			result:  RunResult{Status: RunStatusRunning, LLMCalls: -1},
			wantErr: true,
		},
		{
			name:    "ToolCalls 为负",
			result:  RunResult{Status: RunStatusRunning, ToolCalls: -1},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.result.Validate()
			if tc.wantErr {
				expectKind(t, err, KindInvalidArgument)
				return
			}
			if err != nil {
				t.Fatalf("不应返回错误：%v", err)
			}
		})
	}
}

// TestRunResultStatusKindMatrix 覆盖全部 (Status, ErrorKind) 组合：
// 只有"无错误 + RUNNING/COMPLETED"或"错误种类能推导出同一状态"的组合才合法。
// 该矩阵同时锁住 StatusOf 与 RunResult.Validate 的一致性（INV-003）。
func TestRunResultStatusKindMatrix(t *testing.T) {
	statuses := []RunStatus{
		RunStatusRunning,
		RunStatusCompleted,
		RunStatusFailed,
		RunStatusLimitExceeded,
		RunStatusCancelled,
	}
	kinds := []ErrorKind{
		"",
		KindInvalidArgument,
		KindInvalidPairing,
		KindCancelled,
		KindDeadlineExceeded,
		KindLLMCallLimit,
		KindToolCallLimit,
		KindToolFailure,
		KindProviderFailure,
		KindInternal,
	}

	for _, status := range statuses {
		for _, kind := range kinds {
			t.Run(fmt.Sprintf("%s/%s", status, kind), func(t *testing.T) {
				result := RunResult{Status: status, LLMCalls: 1, ToolCalls: 1}
				if kind != "" {
					result.Error = &RunError{Kind: kind, Op: "test", Message: "测试错误", CallID: "call_1"}
				}

				var wantValid bool
				if status == RunStatusRunning || status == RunStatusCompleted {
					wantValid = kind == ""
				} else {
					wantValid = kind != "" && StatusOf(result.Error) == status
				}

				err := result.Validate()
				if gotValid := err == nil; gotValid != wantValid {
					t.Fatalf("期望合法=%v，实际合法=%v（错误：%v）", wantValid, gotValid, err)
				}
			})
		}
	}
}

// TestStatusCodeMapping 覆盖四种终态结果的可区分性（AC-003）：正常完成 / 被取消 / 工具失败 / 预算耗尽。
func TestStatusCodeMapping(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantKind ErrorKind
		want     RunStatus
	}{
		{name: "正常完成", err: nil, wantKind: KindInternal, want: RunStatusCompleted},
		{name: "用户取消", err: NewError(KindCancelled, "loop.run", "用户取消"), wantKind: KindCancelled, want: RunStatusCancelled},
		{name: "整体超时", err: NewError(KindDeadlineExceeded, "loop.run", "整体超时"), wantKind: KindDeadlineExceeded, want: RunStatusCancelled},
		{name: "LLM 预算耗尽", err: NewError(KindLLMCallLimit, "loop.run", "LLM 调用达上限"), wantKind: KindLLMCallLimit, want: RunStatusLimitExceeded},
		{name: "工具预算耗尽", err: NewError(KindToolCallLimit, "loop.run", "工具调用达上限"), wantKind: KindToolCallLimit, want: RunStatusLimitExceeded},
		{name: "不可恢复工具失败", err: NewError(KindToolFailure, "tool.execute", "失败"), wantKind: KindToolFailure, want: RunStatusFailed},
		{name: "Provider 失败", err: NewError(KindProviderFailure, "llm.complete", "失败"), wantKind: KindProviderFailure, want: RunStatusFailed},
		{name: "普通 error", err: errors.New("底层失败"), wantKind: KindInternal, want: RunStatusFailed},
		{
			name:     "包装后的 RunError 仍保留种类",
			err:      fmt.Errorf("装配层补充上下文: %w", NewError(KindToolFailure, "tool.execute", "失败")),
			wantKind: KindToolFailure,
			want:     RunStatusFailed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusOf(tc.err); got != tc.want {
				t.Fatalf("StatusOf 期望 %s，实际 %s", tc.want, got)
			}
			if got := KindOf(tc.err); got != tc.wantKind {
				t.Fatalf("KindOf 期望 %s，实际 %s", tc.wantKind, got)
			}
		})
	}
}

// TestIsTerminal 覆盖终态判定。
func TestIsTerminal(t *testing.T) {
	cases := []struct {
		status RunStatus
		want   bool
	}{
		{status: RunStatusRunning, want: false},
		{status: RunStatusCompleted, want: true},
		{status: RunStatusFailed, want: true},
		{status: RunStatusLimitExceeded, want: true},
		{status: RunStatusCancelled, want: true},
		{status: RunStatus("PAUSED"), want: false},
		{status: RunStatus(""), want: false},
	}

	for _, tc := range cases {
		if got := IsTerminal(tc.status); got != tc.want {
			t.Fatalf("IsTerminal(%q) 期望 %v，实际 %v", tc.status, tc.want, got)
		}
	}
}

// TestRunErrorChain 覆盖错误文本与原始错误链：CallID 可定位、Cause 可 errors.Is。
func TestRunErrorChain(t *testing.T) {
	root := errors.New("connection reset")
	runErr := &RunError{
		Kind:    KindToolFailure,
		Op:      "tool.execute",
		Message: "工具执行失败",
		CallID:  "call_1",
		Cause:   root,
	}

	if !errors.Is(runErr, root) {
		t.Fatal("RunError 必须保留原始错误链")
	}
	text := runErr.Error()
	for _, want := range []string{"tool.execute", string(KindToolFailure), "call_id=call_1", "connection reset"} {
		if !strings.Contains(text, want) {
			t.Fatalf("错误文本应包含 %q，实际为 %q", want, text)
		}
	}
	if KindOf(runErr) != KindToolFailure {
		t.Fatalf("KindOf 应返回 %s", KindToolFailure)
	}
}
