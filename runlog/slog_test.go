package runlog

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"cnb.cool/framework-station/mini-agent-go/core"
)

// decodeRecords 把输出逐行解析为结构化记录；解析失败即说明记录不完整或被交织。
func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()

	text := strings.TrimSpace(buf.String())
	if text == "" {
		return nil
	}

	var records []map[string]any
	for i, line := range strings.Split(text, "\n") {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("第 %d 行不是可解析的单行 JSON：%v\n行内容：%s", i, err, line)
		}
		if record["msg"] != recordMessage {
			t.Fatalf("第 %d 行 msg 不符：%v", i, record["msg"])
		}
		if record["level"] != "INFO" {
			t.Fatalf("第 %d 行 level 不符：%v", i, record["level"])
		}
		records = append(records, record)
	}
	return records
}

// TestRecordKeepsRunCorrelation 证明同一 Run 的模型/工具调用可按 run_id + step + event
// 串联，且两个 Run 的记录互不混入（INV-LOG-001 / AC-001）。
func TestRecordKeepsRunCorrelation(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONSink(&buf)

	refA := core.ExecutionRef{SessionID: "session_a", RunID: "run_a"}
	refB := core.ExecutionRef{SessionID: "session_b", RunID: "run_b"}

	eventsA := []Event{
		{Kind: KindRunStarted, Ref: refA, Step: 0, Summary: "开始运行"},
		{Kind: KindLLMRequest, Ref: refA, Step: 1, Data: json.RawMessage(`{"model":"m","message_count":1}`)},
		{Kind: KindLLMResponse, Ref: refA, Step: 1, Duration: 900 * time.Millisecond},
		{Kind: KindToolStarted, Ref: refA, Step: 2, CallID: "call_a1"},
		{Kind: KindToolFinished, Ref: refA, Step: 2, CallID: "call_a1", Data: json.RawMessage(`{"name":"echo","is_error":false}`)},
		{Kind: KindRunFinished, Ref: refA, Step: 3, Status: core.RunStatusCompleted},
	}
	eventsB := []Event{
		{Kind: KindRunStarted, Ref: refB, Step: 0},
		{Kind: KindLLMRequest, Ref: refB, Step: 1},
		{Kind: KindRunFinished, Ref: refB, Step: 2, Status: core.RunStatusCompleted},
	}

	// 交错写入，确保过滤依据是 run_id 而不是写入顺序。
	for i := 0; i < len(eventsA); i++ {
		if err := sink.Record(context.Background(), eventsA[i]); err != nil {
			t.Fatalf("记录 Run A 第 %d 个事件失败：%v", i, err)
		}
		if i < len(eventsB) {
			if err := sink.Record(context.Background(), eventsB[i]); err != nil {
				t.Fatalf("记录 Run B 第 %d 个事件失败：%v", i, err)
			}
		}
	}

	records := decodeRecords(t, &buf)
	if len(records) != len(eventsA)+len(eventsB) {
		t.Fatalf("记录条数不符：得到 %d，期望 %d", len(records), len(eventsA)+len(eventsB))
	}

	var gotA []map[string]any
	for _, record := range records {
		runID, _ := record["run_id"].(string)
		sessionID, _ := record["session_id"].(string)
		switch runID {
		case "run_a":
			if sessionID != "session_a" {
				t.Fatalf("Run A 的 session_id 不符：%v", sessionID)
			}
			gotA = append(gotA, record)
		case "run_b":
			if sessionID != "session_b" {
				t.Fatalf("Run B 的 session_id 不符：%v", sessionID)
			}
		default:
			t.Fatalf("出现未知 run_id 的记录：%v", record)
		}
	}

	if len(gotA) != len(eventsA) {
		t.Fatalf("按 run_id 过滤后条数不符：得到 %d，期望 %d", len(gotA), len(eventsA))
	}
	for i, want := range eventsA {
		record := gotA[i]
		if record["event"] != string(want.Kind) {
			t.Fatalf("第 %d 条 event 不符：得到 %v，期望 %v", i, record["event"], want.Kind)
		}
		if record["step"] != float64(want.Step) {
			t.Fatalf("第 %d 条 step 不符：得到 %v，期望 %d", i, record["step"], want.Step)
		}
		if want.CallID != "" && record["call_id"] != want.CallID {
			t.Fatalf("第 %d 条 call_id 不符：得到 %v，期望 %s", i, record["call_id"], want.CallID)
		}
	}
}

// TestRecordFailureLocatesStageAndKind 证明单条记录即可判定失败类别、位置与耗时，
// 且 cancelled 与 deadline_exceeded 可区分（INV-LOG-002 / AC-002）。
func TestRecordFailureLocatesStageAndKind(t *testing.T) {
	ref := core.ExecutionRef{SessionID: "session_1", RunID: "run_1"}

	tests := []struct {
		name       string
		event      Event
		wantKind   string
		wantOp     string
		wantCallID string
		wantStatus string
		wantMillis float64
	}{
		{
			name: "provider 调用失败定位到 llm.complete",
			event: Event{
				Kind:     KindLLMResponse,
				Ref:      ref,
				Step:     2,
				Duration: 1500 * time.Millisecond,
				Error:    &core.RunError{Kind: core.KindProviderFailure, Op: "llm.complete", Message: "上游返回 500"},
			},
			wantKind:   "provider_failure",
			wantOp:     "llm.complete",
			wantStatus: "FAILED",
			wantMillis: 1500,
		},
		{
			name: "工具失败带 call_id",
			event: Event{
				Kind:     KindToolFinished,
				Ref:      ref,
				Step:     3,
				CallID:   "call_7",
				Duration: 20 * time.Millisecond,
				Error:    &core.RunError{Kind: core.KindToolFailure, Op: "tool.execute", Message: "不可恢复", CallID: "call_7"},
			},
			wantKind:   "tool_failure",
			wantOp:     "tool.execute",
			wantCallID: "call_7",
			wantStatus: "FAILED",
			wantMillis: 20,
		},
		{
			name: "用户取消",
			event: Event{
				Kind:     KindRunFinished,
				Ref:      ref,
				Step:     4,
				Duration: 10 * time.Millisecond,
				Error:    &core.RunError{Kind: core.KindCancelled, Op: "agent.run", Message: "用户取消"},
			},
			wantKind:   "cancelled",
			wantOp:     "agent.run",
			wantStatus: "CANCELLED",
			wantMillis: 10,
		},
		{
			name: "整体超时与用户取消区分",
			event: Event{
				Kind:     KindRunFinished,
				Ref:      ref,
				Step:     4,
				Duration: 30000 * time.Millisecond,
				Error:    &core.RunError{Kind: core.KindDeadlineExceeded, Op: "agent.run", Message: "超过整体期限"},
			},
			wantKind:   "deadline_exceeded",
			wantOp:     "agent.run",
			wantStatus: "CANCELLED",
			wantMillis: 30000,
		},
		{
			name: "LLM 调用预算耗尽",
			event: Event{
				Kind:     KindRunFinished,
				Ref:      ref,
				Step:     5,
				Duration: 1 * time.Millisecond,
				Error:    &core.RunError{Kind: core.KindLLMCallLimit, Op: "agent.run", Message: "LLM 调用达上限"},
			},
			wantKind:   "llm_call_limit",
			wantOp:     "agent.run",
			wantStatus: "LIMIT_EXCEEDED",
			wantMillis: 1,
		},
		{
			name: "工具调用预算耗尽",
			event: Event{
				Kind:  KindRunFinished,
				Ref:   ref,
				Step:  5,
				Error: &core.RunError{Kind: core.KindToolCallLimit, Op: "agent.run", Message: "工具调用达上限"},
			},
			wantKind:   "tool_call_limit",
			wantOp:     "agent.run",
			wantStatus: "LIMIT_EXCEEDED",
			wantMillis: 0,
		},
		{
			name:       "工具调用成功不等于 Run 完成",
			event:      Event{Kind: KindToolFinished, Ref: ref, Step: 2, CallID: "call_8", Data: json.RawMessage(`{"name":"echo","is_error":false}`)},
			wantStatus: "RUNNING",
		},
		{
			name:       "模型响应成功不等于 Run 完成",
			event:      Event{Kind: KindLLMResponse, Ref: ref, Step: 1},
			wantStatus: "RUNNING",
		},
		{
			name:       "Run 正常完成",
			event:      Event{Kind: KindRunFinished, Ref: ref, Step: 3, Status: core.RunStatusCompleted},
			wantStatus: "COMPLETED",
		},
	}

	kinds := make(map[string]string, len(tests))
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			sink := NewJSONSink(&buf)
			if err := sink.Record(context.Background(), tt.event); err != nil {
				t.Fatalf("记录失败：%v", err)
			}

			records := decodeRecords(t, &buf)
			if len(records) != 1 {
				t.Fatalf("期望单条记录，得到 %d 条", len(records))
			}
			record := records[0]

			if record["status"] != tt.wantStatus {
				t.Fatalf("status 不符：得到 %v，期望 %s", record["status"], tt.wantStatus)
			}
			if record["duration_ms"] != tt.wantMillis {
				t.Fatalf("duration_ms 不符：得到 %v，期望 %v", record["duration_ms"], tt.wantMillis)
			}

			if tt.wantKind == "" {
				if _, ok := record["error_kind"]; ok {
					t.Fatalf("无错误事件不应输出 error_kind：%v", record["error_kind"])
				}
				return
			}

			if record["error_kind"] != tt.wantKind {
				t.Fatalf("error_kind 不符：得到 %v，期望 %s", record["error_kind"], tt.wantKind)
			}
			if record["op"] != tt.wantOp {
				t.Fatalf("op 不符：得到 %v，期望 %s", record["op"], tt.wantOp)
			}
			if tt.wantCallID != "" && record["call_id"] != tt.wantCallID {
				t.Fatalf("call_id 不符：得到 %v，期望 %s", record["call_id"], tt.wantCallID)
			}
			if tt.wantOp != "" && record["error_message"] == nil {
				t.Fatalf("错误记录缺少 error_message")
			}
			kinds[tt.name] = tt.wantKind
		})
	}

	if kinds["用户取消"] == kinds["整体超时与用户取消区分"] {
		t.Fatalf("cancelled 与 deadline_exceeded 被合并为同一类别：%v", kinds["用户取消"])
	}
}

// TestRecordRedactsSecretsAndContent 在完整输出字节上证明凭据、正文与未知载荷不落盘（INV-LOG-003）。
func TestRecordRedactsSecretsAndContent(t *testing.T) {
	var buf bytes.Buffer
	sink := NewJSONSink(&buf)

	const userText = "用户原始正文：帮我查一下昨天的订单"

	event := Event{
		Kind:   KindLLMRequest,
		Ref:    core.ExecutionRef{SessionID: "session_1", RunID: "run_1"},
		Step:   1,
		CallID: "call_1",
		Data: json.RawMessage(`{
			"model": "gpt-test",
			"name": "search",
			"is_error": false,
			"usage": {"input_tokens": 12, "output_tokens": 34},
			"api_key": "sk-live-TOP-SECRET",
			"nested": {"apiKey": "nested-SECRET", "authorization": "Bearer auth-TOKEN-VALUE"},
			"password": "p@ssw0rd",
			"content": "用户原始正文：帮我查一下昨天的订单",
			"messages": [{"role": "user", "content": "用户第二条正文"}],
			"unknown_field": "unknown-value"
		}`),
		Error: &core.RunError{
			Kind:    core.KindProviderFailure,
			Op:      "llm.complete",
			Message: "鉴权失败：Bearer leaked-ERROR-token",
			Cause:   errors.New("postgres://user:p@ss@db:5432/agent"),
		},
	}

	if err := sink.Record(context.Background(), event); err != nil {
		t.Fatalf("记录失败：%v", err)
	}

	out := buf.String()
	for _, leaked := range []string{
		"sk-live-TOP-SECRET",
		"nested-SECRET",
		"auth-TOKEN-VALUE",
		"p@ssw0rd",
		userText,
		"用户第二条正文",
		"unknown-value",
		"leaked-ERROR-token",
		"postgres://user:p@ss@db:5432/agent",
	} {
		if strings.Contains(out, leaked) {
			t.Fatalf("敏感内容 %q 泄漏到输出：%s", leaked, out)
		}
	}

	for _, expected := range []string{
		`"model":"gpt-test"`,
		`"name":"search"`,
		`"input_tokens":12`,
		`"error_kind":"provider_failure"`,
		`"op":"llm.complete"`,
		redactedValue,
		"[REDACTED:len=",
	} {
		if !strings.Contains(out, expected) {
			t.Fatalf("输出缺少 %q：%s", expected, out)
		}
	}

	if strings.Contains(out, "unknown_field") {
		t.Fatalf("未知键名出现在输出中：%s", out)
	}
}

// TestRecordPayloadGuards 证明非法与超限载荷只留标记，不输出原始字节。
func TestRecordPayloadGuards(t *testing.T) {
	tests := []struct {
		name  string
		data  json.RawMessage
		want  string
		banes []string
	}{
		{
			name:  "非法 JSON",
			data:  json.RawMessage(`{"credential":"sk-broken"`),
			want:  "data_invalid",
			banes: []string{"sk-broken"},
		},
		{
			name:  "顶层非对象",
			data:  json.RawMessage(`["sk-array-secret"]`),
			want:  "data_invalid",
			banes: []string{"sk-array-secret"},
		},
		{
			name:  "超过 64 KiB",
			data:  json.RawMessage(`{"model":"` + strings.Repeat("a", maxDataBytes) + `"}`),
			want:  "data_truncated",
			banes: []string{strings.Repeat("a", 128)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			sink := NewJSONSink(&buf)
			event := Event{
				Kind: KindLLMRequest,
				Ref:  core.ExecutionRef{SessionID: "s", RunID: "r"},
				Data: tt.data,
			}
			if err := sink.Record(context.Background(), event); err != nil {
				t.Fatalf("记录失败：%v", err)
			}

			record := decodeRecords(t, &buf)[0]
			if record[tt.want] != true {
				t.Fatalf("缺少 %s 标记：%v", tt.want, record)
			}
			if _, ok := record["data"]; ok {
				t.Fatalf("异常载荷不应输出 data：%v", record["data"])
			}
			out := buf.String()
			for _, banned := range tt.banes {
				if strings.Contains(out, banned) {
					t.Fatalf("原始载荷被输出：%s", out)
				}
			}
		})
	}
}

// failingWriter 模拟写入失败的输出目标（磁盘满、管道断开等）。
type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("写入失败") }

// failingHandler 模拟 Handler 返回错误。
type failingHandler struct{}

func (failingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (failingHandler) Handle(context.Context, slog.Record) error { return errors.New("handler 失败") }

func (h failingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h failingHandler) WithGroup(string) slog.Handler { return h }

// panickingHandler 模拟 Handler 内部 panic。
type panickingHandler struct{}

func (panickingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (panickingHandler) Handle(context.Context, slog.Record) error { panic("handler panic") }

func (h panickingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h panickingHandler) WithGroup(string) slog.Handler { return h }

// recordWithin 在测试超时内调用 Record，并断言不向上抛出 panic（AC-004）。
func recordWithin(t *testing.T, sink Sink, ctx context.Context, ev Event) error {
	t.Helper()

	type outcome struct {
		err   error
		panic any
	}
	done := make(chan outcome, 1)

	go func() {
		result := outcome{}
		defer func() {
			result.panic = recover()
			done <- result
		}()
		result.err = sink.Record(ctx, ev)
	}()

	select {
	case result := <-done:
		if result.panic != nil {
			t.Fatalf("Record 未隔离 panic：%v", result.panic)
		}
		return result.err
	case <-time.After(2 * time.Second):
		t.Fatal("Record 未在测试超时内返回")
		return nil
	}
}

// TestRecordIsSideEffectFree 证明写入失败、Handler 异常、ctx 取消与异常载荷都不改变输入，
// 且不改变 Run 结果（INV-LOG-004 / AC-004）。
func TestRecordIsSideEffectFree(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	baseEvent := Event{
		Kind:     KindToolFinished,
		Ref:      core.ExecutionRef{SessionID: "session_1", RunID: "run_1"},
		Step:     2,
		CallID:   "call_1",
		Summary:  "工具调用结束",
		Data:     json.RawMessage(`{"name":"echo","is_error":true,"content":"不可复现的正文"}`),
		Duration: 42 * time.Millisecond,
		Error:    &core.RunError{Kind: core.KindToolFailure, Op: "tool.execute", Message: "失败", CallID: "call_1"},
	}
	weirdEvent := Event{
		Kind:  KindLLMRequest,
		Ref:   core.ExecutionRef{SessionID: "s", RunID: "r"},
		Data:  json.RawMessage(`{"model":"` + "\xff" + `","usage":` + strings.Repeat(`{"usage":`, 12) + `"x"` + strings.Repeat(`}`, 12) + `}`),
		Error: &core.RunError{},
	}

	tests := []struct {
		name    string
		sink    Sink
		ctx     context.Context
		event   Event
		wantErr bool
	}{
		{
			name:    "写入必然失败的 writer",
			sink:    NewJSONSink(failingWriter{}),
			ctx:     context.Background(),
			event:   baseEvent,
			wantErr: true,
		},
		{
			name:    "Handler 返回错误",
			sink:    NewSlogSink(failingHandler{}),
			ctx:     context.Background(),
			event:   baseEvent,
			wantErr: true,
		},
		{
			name:    "Handler panic 被恢复为错误",
			sink:    NewSlogSink(panickingHandler{}),
			ctx:     context.Background(),
			event:   baseEvent,
			wantErr: true,
		},
		{
			name:    "已取消的 ctx 仍然记录且不返回取消错误",
			sink:    NewJSONSink(&bytes.Buffer{}),
			ctx:     cancelledCtx,
			event:   baseEvent,
			wantErr: false,
		},
		{
			name:    "异常载荷与非法 UTF-8",
			sink:    NewJSONSink(&bytes.Buffer{}),
			ctx:     context.Background(),
			event:   weirdEvent,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.event
			beforeData := append(json.RawMessage(nil), tt.event.Data...)

			err := recordWithin(t, tt.sink, tt.ctx, tt.event)

			if tt.wantErr && err == nil {
				t.Fatal("期望返回日志错误，实际为 nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("不应返回错误：%v", err)
			}
			if !reflect.DeepEqual(before, tt.event) {
				t.Fatalf("Event 被改写：\n调用前：%+v\n调用后：%+v", before, tt.event)
			}
			if !bytes.Equal(beforeData, tt.event.Data) {
				t.Fatalf("Data 字节被改写：\n调用前：%s\n调用后：%s", beforeData, tt.event.Data)
			}
		})
	}

	// 同一 RunResult 在接入与不接入该 Sink 时逐字段相同。
	events := []Event{baseEvent, weirdEvent}

	withFailing := runScenario(NewJSONSink(failingWriter{}), events)
	withoutSink := runScenario(Discard(), events)
	if !reflect.DeepEqual(withFailing, withoutSink) {
		t.Fatalf("Run 结果受日志失败影响：\n接入：%+v\n不接入：%+v", withFailing, withoutSink)
	}
}

// runScenario 模拟调用方：记录事件（忽略日志错误）并返回该次 Run 的结论。
func runScenario(sink Sink, events []Event) core.RunResult {
	for _, ev := range events {
		_ = sink.Record(context.Background(), ev)
	}
	return core.RunResult{
		ExecutionRef: core.ExecutionRef{SessionID: "session_1", RunID: "run_1"},
		Status:       core.RunStatusCompleted,
		LLMCalls:     2,
		ToolCalls:    1,
		Messages:     []core.Message{{ID: "m1", Role: "user", Content: []core.ContentPart{{Type: "text", Text: "你好"}}}},
	}
}

// TestNewSinkRejectsNilDependencies 证明缺省构造参数不会导致 panic。
func TestNewSinkRejectsNilDependencies(t *testing.T) {
	if err := recordWithin(t, NewSlogSink(nil), context.Background(), Event{}); err == nil {
		t.Fatal("nil handler 应返回错误")
	}
	if err := recordWithin(t, NewJSONSink(nil), context.Background(), Event{}); err == nil {
		t.Fatal("nil writer 应返回错误")
	}
}

// TestRecordConcurrentRecordsIntact 证明并发记录时每条记录都是完整可解析单元（INV-LOG-005 / AC-005）。
func TestRecordConcurrentRecordsIntact(t *testing.T) {
	const (
		goroutines = 8
		perRoutine = 25
	)

	var buf bytes.Buffer
	sink := NewJSONSink(&buf)

	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			ref := core.ExecutionRef{SessionID: fmt.Sprintf("session_%d", g), RunID: fmt.Sprintf("run_%d", g)}
			for step := 0; step < perRoutine; step++ {
				ev := Event{
					Kind:   KindToolFinished,
					Ref:    ref,
					Step:   step,
					CallID: fmt.Sprintf("call_%d_%d", g, step),
					Data:   json.RawMessage(`{"name":"echo","is_error":false,"message_count":1}`),
				}
				if err := sink.Record(context.Background(), ev); err != nil {
					t.Errorf("并发记录失败：%v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	records := decodeRecords(t, &buf)
	if len(records) != goroutines*perRoutine {
		t.Fatalf("记录条数不符：得到 %d，期望 %d", len(records), goroutines*perRoutine)
	}

	seen := make(map[string]map[int]bool, goroutines)
	for _, record := range records {
		for _, field := range []string{"session_id", "run_id", "event", "step", "duration_ms", "status"} {
			if _, ok := record[field]; !ok {
				t.Fatalf("记录缺少字段 %s：%v", field, record)
			}
		}
		callID, _ := record["call_id"].(string)
		if callID == "" {
			t.Fatalf("记录缺少 call_id：%v", record)
		}
		step, ok := record["step"].(float64)
		if !ok {
			t.Fatalf("step 不是数字：%v", record["step"])
		}
		if seen[callID] == nil {
			seen[callID] = map[int]bool{}
		}
		if seen[callID][int(step)] {
			t.Fatalf("call_id %s 的 step %d 重复出现，说明记录被拼接：%v", callID, int(step), record)
		}
		seen[callID][int(step)] = true
		if record["event"] != string(KindToolFinished) {
			t.Fatalf("并发记录 event 字段异常：%v", record["event"])
		}
		data, ok := record["data"].(map[string]any)
		if !ok || data["name"] != "echo" {
			t.Fatalf("并发记录 data 字段丢失：%v", record["data"])
		}
	}
}
