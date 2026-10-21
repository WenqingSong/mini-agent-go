package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"cnb.cool/framework-station/mini-agent-go/core"
	"cnb.cool/framework-station/mini-agent-go/runlog"
	"cnb.cool/framework-station/mini-agent-go/session"
)

// logRecord 是 runlog JSON 记录在测试中的最小反序列化形态。
type logRecord struct {
	Event     string         `json:"event"`
	SessionID string         `json:"session_id"`
	RunID     string         `json:"run_id"`
	Step      int            `json:"step"`
	CallID    string         `json:"call_id"`
	Status    string         `json:"status"`
	Data      map[string]any `json:"data"`
}

// parseRecords 逐行解析 JSON Sink 的输出，任一行非法即视为事件完整性问题。
func parseRecords(t *testing.T, buf *bytes.Buffer) []logRecord {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	records := make([]logRecord, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record logRecord
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("事件行不是合法 JSON：%v（%q）", err, line)
		}
		records = append(records, record)
	}
	return records
}

// TestRunEmitsEventSequence 覆盖 AC-008：六类事件的顺序、身份与定位字段。
func TestRunEmitsEventSequence(t *testing.T) {
	var buf bytes.Buffer
	provider := newScriptedProvider(
		callsResponse("a1", call("c1", "echo", `{}`)),
		textResponse("a2", "完成"),
	)
	echo := newRecordingTool("echo")
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "你好"))

	cfg := baseConfig(t, provider, newRegistry(t, echo), manager)
	cfg.Sink = runlog.NewJSONSink(&buf)
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 2, MaxToolCalls: 1},
	})
	assertTerminal(t, result, core.RunStatusCompleted, "", "")

	records := parseRecords(t, &buf)
	wantKinds := []string{
		"run_started",
		"llm_request", "llm_response",
		"tool_started", "tool_finished",
		"llm_request", "llm_response",
		"run_finished",
	}
	if len(records) != len(wantKinds) {
		t.Fatalf("事件条数不符：期望 %d，实际 %d：%+v", len(wantKinds), len(records), records)
	}
	wantSteps := []int{0, 1, 1, 1, 1, 2, 2, 2}
	for i, want := range wantKinds {
		record := records[i]
		if record.Event != want {
			t.Fatalf("第 %d 个事件应为 %s，实际 %s", i, want, record.Event)
		}
		if record.SessionID != "s-1" || record.RunID != "r-1" {
			t.Fatalf("第 %d 个事件缺少可定位身份：%+v", i, record)
		}
		if record.Step != wantSteps[i] {
			t.Fatalf("第 %d 个事件的 step 应为 %d，实际 %d", i, wantSteps[i], record.Step)
		}
	}

	if got := records[3].CallID; got != "c1" {
		t.Fatalf("tool_started.call_id 必须等于 ToolCall.ID，实际 %q", got)
	}
	if got := records[4].CallID; got != "c1" {
		t.Fatalf("tool_finished.call_id 必须等于 ToolCall.ID，实际 %q", got)
	}
	if got := records[0].Data["model"]; got != "fake-model" {
		t.Fatalf("run_started.data.model 不符：%v", got)
	}
	if got := records[1].Data["message_count"]; got != float64(1) {
		t.Fatalf("llm_request.data.message_count 不符：%v", got)
	}
	if got := records[2].Data["finish_reason"]; got != "tool_calls" {
		t.Fatalf("llm_response.data.finish_reason 不符：%v", got)
	}
	if got := records[2].Data["tool_call_count"]; got != float64(1) {
		t.Fatalf("llm_response.data.tool_call_count 不符：%v", got)
	}
	if got := records[2].Data["input_tokens"]; got != float64(5) {
		t.Fatalf("llm_response.data.input_tokens 不符：%v", got)
	}
	if got := records[6].Data["finish_reason"]; got != "stop" {
		t.Fatalf("第二次 llm_response 的 finish_reason 不符：%v", got)
	}
	if got := records[7].Status; got != "COMPLETED" {
		t.Fatalf("run_finished.status 不符：%v", got)
	}
	if records[7].Data != nil {
		t.Fatalf("run_finished 不应携带 Data：%v", records[7].Data)
	}
}

// runRecordedScenario 用同一脚本执行一次 Run，返回结论与会话快照，供 Sink 对照使用。
func runRecordedScenario(t *testing.T, sink runlog.Sink) (core.RunResult, session.Session) {
	t.Helper()
	provider := newScriptedProvider(
		callsResponse("a1", call("c1", "echo", `{}`)),
		textResponse("a2", "完成"),
	)
	echo := newRecordingTool("echo")
	manager := newSessionManager(t)
	createSession(t, manager, "s-1", "", userMsg("u1", "你好"))

	cfg := baseConfig(t, provider, newRegistry(t, echo), manager)
	cfg.Sink = sink
	loop := mustNewLoop(t, cfg)

	result := loop.Run(context.Background(), RunRequest{
		ExecutionRef: core.ExecutionRef{SessionID: "s-1", RunID: "r-1"},
		Policy:       core.RunPolicy{MaxLLMCalls: 2, MaxToolCalls: 1},
	})
	return result, sessionSnapshot(t, manager, "s-1")
}

// TestRunIgnoresSinkFailures 覆盖 INV-007 的运行期证据：
// Sink 必定失败时，RunResult 与 Session 历史与正常 Sink 逐字段相同。
func TestRunIgnoresSinkFailures(t *testing.T) {
	normalResult, normalSession := runRecordedScenario(t, &recordingSink{})
	failedResult, failedSession := runRecordedScenario(t, failingSink{})

	assertTerminal(t, failedResult, core.RunStatusCompleted, "", "")
	if !reflect.DeepEqual(normalResult, failedResult) {
		t.Fatalf("Sink 失败不得改变 RunResult：\n正常：%+v\n失败：%+v", normalResult, failedResult)
	}
	if !reflect.DeepEqual(normalSession, failedSession) {
		t.Fatalf("Sink 失败不得改变会话历史：\n正常：%+v\n失败：%+v", normalSession, failedSession)
	}
}
