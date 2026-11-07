package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

var _ api.Frontend = New()

func ptr[T any](v T) *T { return &v }

// claudeCodeBody is shaped like a Claude Code turn: cached system blocks,
// tools with input_schema plus a server tool, thinking, and a full
// tool_use / tool_result round trip.
const claudeCodeBody = `{
  "model": "claude-sonnet-4-5-20250929",
  "max_tokens": 32000,
  "stream": true,
  "metadata": {"user_id": "user_abc_account__session_123"},
  "service_tier": "auto",
  "system": [
    {"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude.", "cache_control": {"type": "ephemeral"}},
    {"type": "text", "text": "You are an interactive CLI tool that helps users with software engineering tasks.", "cache_control": {"type": "ephemeral", "ttl": "1h"}}
  ],
  "tools": [
    {"name": "Bash", "description": "Executes a given bash command",
     "input_schema": {"type": "object", "properties": {"command": {"type": "string"}, "timeout": {"type": "number"}},
                      "required": ["command"], "additionalProperties": false, "$schema": "http://json-schema.org/draft-07/schema#"}},
    {"name": "Read", "description": "Reads a file", "type": "custom",
     "input_schema": {"type": "object", "properties": {"file_path": {"type": "string"}}, "required": ["file_path"]},
     "cache_control": {"type": "ephemeral"}},
    {"type": "web_search_20250305", "name": "web_search", "max_uses": 5}
  ],
  "tool_choice": {"type": "auto"},
  "thinking": {"type": "enabled", "budget_tokens": 10000},
  "temperature": 1,
  "stop_sequences": ["</stop>"],
  "messages": [
    {"role": "user", "content": [
      {"type": "text", "text": "<system-reminder>context</system-reminder>"},
      {"type": "text", "text": "Run the tests", "cache_control": {"type": "ephemeral"}}
    ]},
    {"role": "assistant", "content": [
      {"type": "thinking", "thinking": "I should run go test.", "signature": "EqQBCkYIBxgCKkA"},
      {"type": "text", "text": "Running tests."},
      {"type": "tool_use", "id": "toolu_01A", "name": "Bash", "input": {"command": "go test ./...", "timeout": 120000}},
      {"type": "tool_use", "id": "toolu_01B", "name": "Read", "input": {"file_path": "/repo/go.mod"}}
    ]},
    {"role": "user", "content": [
      {"type": "tool_result", "tool_use_id": "toolu_01A", "content": "ok  \tpkg\t0.01s", "is_error": false},
      {"type": "tool_result", "tool_use_id": "toolu_01B", "is_error": true, "content": [
        {"type": "text", "text": "permission denied"},
        {"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "AAAA"}}
      ]},
      {"type": "text", "text": "Now fix the lint."}
    ]},
    {"role": "assistant", "content": [{"type": "redacted_thinking", "data": "abc"}, {"type": "text", "text": "Done."}]},
    {"role": "user", "content": "thanks"}
  ]
}`

func TestDecodeRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *llm.Request
	}{
		{
			name: "claude code turn",
			body: claudeCodeBody,
			want: &llm.Request{
				Model: "claude-sonnet-4-5-20250929",
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: "You are Claude Code, Anthropic's official CLI for Claude.\nYou are an interactive CLI tool that helps users with software engineering tasks."},
					{Role: llm.RoleUser, Content: "<system-reminder>context</system-reminder>\nRun the tests"},
					{Role: llm.RoleAssistant, Content: "Running tests.", Reasoning: "I should run go test.", ToolCalls: []llm.ToolCall{
						{ID: "toolu_01A", Name: "Bash", Arguments: `{"command":"go test ./...","timeout":120000}`},
						{ID: "toolu_01B", Name: "Read", Arguments: `{"file_path":"/repo/go.mod"}`},
					}},
					{Role: llm.RoleTool, ToolCallID: "toolu_01A", Name: "Bash", Content: "ok  \tpkg\t0.01s"},
					{Role: llm.RoleTool, ToolCallID: "toolu_01B", Name: "Read", Content: "Error: permission denied", Images: 1},
					{Role: llm.RoleUser, Content: "Now fix the lint."},
					{Role: llm.RoleAssistant, Content: "Done."},
					{Role: llm.RoleUser, Content: "thanks"},
				},
				Tools: []llm.Tool{
					{Name: "Bash", Description: "Executes a given bash command",
						Parameters: json.RawMessage(`{"type":"object","properties":{"command":{"type":"string"},"timeout":{"type":"number"}},"required":["command"],"additionalProperties":false,"$schema":"http://json-schema.org/draft-07/schema#"}`)},
					{Name: "Read", Description: "Reads a file",
						Parameters: json.RawMessage(`{"type":"object","properties":{"file_path":{"type":"string"}},"required":["file_path"]}`)},
				},
				ToolChoice:      json.RawMessage(`"auto"`),
				MaxTokens:       32000,
				Temperature:     ptr(1.0),
				Stop:            []string{"</stop>"},
				Stream:          true,
				ReasoningEffort: "medium",
				Protocol:        llm.ProtocolAnthropic,
				Warnings: []string{
					"dropped image/document block(s) (text-only simulation)",
					"ignored redacted_thinking block in assistant message",
					`dropped server tool "web_search" of type "web_search_20250305" (not simulated)`,
				},
			},
		},
		{
			name: "count_tokens body without max_tokens",
			body: `{"model":"claude-haiku-4-5","system":"Be brief.","messages":[{"role":"user","content":"hi"}],
				"tools":[{"name":"t","input_schema":{"type":"object"}}]}`,
			want: &llm.Request{
				Model: "claude-haiku-4-5",
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: "Be brief."},
					{Role: llm.RoleUser, Content: "hi"},
				},
				Tools:    []llm.Tool{{Name: "t", Parameters: json.RawMessage(`{"type":"object"}`)}},
				Protocol: llm.ProtocolAnthropic,
			},
		},
		{
			name: "mid-conversation system message, adaptive thinking, server tool blocks",
			body: `{"model":"m","max_tokens":10,"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"},
				"messages":[
					{"role":"user","content":[{"type":"text","text":"search"},{"type":"document","source":{}}]},
					{"role":"assistant","content":[
						{"type":"server_tool_use","id":"srvtoolu_1","name":"web_search","input":{"query":"x"}},
						{"type":"web_search_tool_result","tool_use_id":"srvtoolu_1","content":[]},
						{"type":"text","text":"found"}]},
					{"role":"user","content":"ok"},
					{"role":"system","content":"Be terse from now on."},
					{"role":"system","content":[]}
				]}`,
			want: &llm.Request{
				Model: "m",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "search", Images: 1},
					{Role: llm.RoleAssistant, Content: "found"},
					{Role: llm.RoleUser, Content: "ok"},
					{Role: llm.RoleSystem, Content: "Be terse from now on."},
				},
				MaxTokens:       10,
				ReasoningEffort: "high",
				Protocol:        llm.ProtocolAnthropic,
				Warnings: []string{
					"dropped image/document block(s) (text-only simulation)",
					"ignored server_tool_use block in assistant message",
					"ignored web_search_tool_result block in assistant message",
				},
			},
		},
		{
			name: "tool results only, empty tool_use input",
			body: `{"model":"m","messages":[
				{"role":"assistant","content":[{"type":"tool_use","id":"toolu_x","name":"Glob","input":{}},{"type":"tool_use","id":"toolu_y","name":"LS"}]},
				{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_x"},{"type":"tool_result","tool_use_id":"toolu_y","content":[]}]}]}`,
			want: &llm.Request{
				Model: "m",
				Messages: []llm.Message{
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
						{ID: "toolu_x", Name: "Glob", Arguments: "{}"},
						{ID: "toolu_y", Name: "LS", Arguments: "{}"},
					}},
					{Role: llm.RoleTool, ToolCallID: "toolu_x", Name: "Glob"},
					{Role: llm.RoleTool, ToolCallID: "toolu_y", Name: "LS"},
				},
				Protocol: llm.ProtocolAnthropic,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := New().DecodeRequest([]byte(tc.body))
			if err != nil {
				t.Fatalf("DecodeRequest: %v", err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				gj, _ := json.MarshalIndent(got, "", "  ")
				wj, _ := json.MarshalIndent(tc.want, "", "  ")
				t.Fatalf("mismatch\n got: %s\nwant: %s", gj, wj)
			}
		})
	}
}

func TestDecodeToolUseInputCanonical(t *testing.T) {
	input := `{ "edits": [ {"old_string": "a\n\tb", "new_string": "ü \"q\"  "} ], "file_path": "/x.go", "replace_all": false, "n": 1.50 }`
	body := `{"model":"m","messages":[{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"MultiEdit","input":` + input + `}]}]}`
	req, err := New().DecodeRequest([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	got := req.Messages[0].ToolCalls[0].Arguments
	var want bytes.Buffer
	if err := json.Compact(&want, []byte(input)); err != nil {
		t.Fatal(err)
	}
	if got != want.String() {
		t.Fatalf("arguments not compact JSON of input:\n got %q\nwant %q", got, want.String())
	}
	if llm.CanonicalJSON(got) != llm.CanonicalJSON(input) {
		t.Fatalf("canonical mismatch:\n%s\n%s", llm.CanonicalJSON(got), llm.CanonicalJSON(input))
	}
}

func TestDecodeToolChoiceAndThinking(t *testing.T) {
	tests := []struct {
		extra      string
		wantChoice string
		wantEffort string
	}{
		{`"tool_choice":{"type":"auto"}`, `"auto"`, ""},
		{`"tool_choice":{"type":"any","disable_parallel_tool_use":true}`, `"required"`, ""},
		{`"tool_choice":{"type":"tool","name":"Bash"}`, `{"function":{"name":"Bash"},"type":"function"}`, ""},
		{`"tool_choice":{"type":"none"}`, `"none"`, ""},
		{`"thinking":{"type":"enabled","budget_tokens":1024}`, "", "low"},
		{`"thinking":{"type":"enabled","budget_tokens":4096}`, "", "medium"},
		{`"thinking":{"type":"enabled","budget_tokens":16384}`, "", "high"},
		{`"thinking":{"type":"disabled"}`, "", ""},
		{`"thinking":{"type":"disabled"},"output_config":{"effort":"high"}`, "", ""},
		{`"thinking":{"type":"between_tools"}`, "", ""},
		{`"thinking":{"type":"adaptive"}`, "", "medium"},
		{`"thinking":{"type":"adaptive","display":"summarized"},"output_config":{"effort":"low"}`, "", "low"},
		{`"output_config":{"effort":"max"}`, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.extra, func(t *testing.T) {
			body := `{"model":"m","max_tokens":1,"messages":[{"role":"user","content":"x"}],` + tc.extra + `}`
			req, err := New().DecodeRequest([]byte(body))
			if err != nil {
				t.Fatal(err)
			}
			if string(req.ToolChoice) != tc.wantChoice {
				t.Fatalf("tool_choice %s want %s", req.ToolChoice, tc.wantChoice)
			}
			if req.ReasoningEffort != tc.wantEffort {
				t.Fatalf("effort %q want %q", req.ReasoningEffort, tc.wantEffort)
			}
		})
	}
}

func TestDecodeRequestErrors(t *testing.T) {
	tests := []struct{ name, body, wantSub string }{
		{"empty body", ` `, "empty"},
		{"invalid json", `{"messages": [`, "invalid JSON"},
		{"missing messages", `{"model":"m","max_tokens":5}`, "messages"},
		{"empty messages", `{"messages":[]}`, "messages"},
		{"bad role", `{"messages":[{"role":"tool","content":"x"}]}`, `"tool"`},
		{"bad content", `{"messages":[{"role":"user","content":{"text":"x"}}]}`, "content"},
		{"bad system", `{"system":5,"messages":[{"role":"user","content":"x"}]}`, "system"},
		{"tool_choice tool without name", `{"messages":[{"role":"user","content":"x"}],"tool_choice":{"type":"tool"}}`, "tool_choice.name"},
		{"tool without name", `{"messages":[{"role":"user","content":"x"}],"tools":[{"input_schema":{}}]}`, "tools.0.name"},
		{"tool_use without name", `{"messages":[{"role":"assistant","content":[{"type":"tool_use","id":"t"}]}]}`, "messages.0.content.0.name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New().DecodeRequest([]byte(tc.body))
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("err = %v, want containing %q", err, tc.wantSub)
			}
		})
	}
}

// ---- streaming ----

type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++; f.ResponseRecorder.Flush() }

func newRecorder() *flushRecorder { return &flushRecorder{ResponseRecorder: httptest.NewRecorder()} }

type sseEvent struct {
	name string
	data map[string]any
	raw  string
}

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("body does not end with a blank line")
	}
	var out []sseEvent
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		lines := strings.Split(frame, "\n")
		if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
			t.Fatalf("bad frame %q", frame)
		}
		ev := sseEvent{name: strings.TrimPrefix(lines[0], "event: "), raw: strings.TrimPrefix(lines[1], "data: ")}
		if err := json.Unmarshal([]byte(ev.raw), &ev.data); err != nil {
			t.Fatalf("frame %q: %v", frame, err)
		}
		if ev.data["type"] != ev.name {
			t.Fatalf("event %q carries type %v", ev.name, ev.data["type"])
		}
		out = append(out, ev)
	}
	return out
}

func obj(v any) map[string]any { m, _ := v.(map[string]any); return m }

func idx(ev sseEvent) int { return int(ev.data["index"].(float64)) }

// label summarises an event as name[:index[:subtype]].
func label(ev sseEvent) string {
	switch ev.name {
	case "content_block_start":
		return "start:" + itoa(idx(ev)) + ":" + obj(ev.data["content_block"])["type"].(string)
	case "content_block_delta":
		return "delta:" + itoa(idx(ev)) + ":" + obj(ev.data["delta"])["type"].(string)
	case "content_block_stop":
		return "stop:" + itoa(idx(ev))
	}
	return ev.name
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func fragments(s string, n int) []string {
	var out []string
	for len(s) > n {
		out = append(out, s[:n])
		s = s[n:]
	}
	return append(out, s)
}

func TestStreamSequence(t *testing.T) {
	rec := newRecorder()
	meta := api.StreamMeta{ID: "01HXYZ", Model: "claude-sonnet-4-5", PromptTokens: 1200}
	s := New().NewStream(rec, meta)

	args0 := `{"command":"go test ./internal/... -count=1","timeout":120000,"description":"Run tests"}`
	args1 := `{"file_path":"/repo/naïve_日本.go","edits":[{"old":"a","new":"b"}]}`

	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if rec.Header().Get("Content-Type") != "text/event-stream" || rec.Code != 200 {
		t.Fatalf("headers %v %d", rec.Header(), rec.Code)
	}
	send := func(ev llm.Event) {
		t.Helper()
		before, flushes := rec.Body.Len(), rec.flushes
		if err := s.Send(ev); err != nil {
			t.Fatalf("Send(%v): %v", ev.Type, err)
		}
		if rec.Body.Len() != before && rec.flushes == flushes {
			t.Fatalf("Send(%v) wrote without flushing", ev.Type)
		}
	}
	send(llm.Event{Type: llm.EventReasoning, Text: "The user wants "})
	send(llm.Event{Type: llm.EventReasoning, Text: "tests run."})
	send(llm.Event{Type: llm.EventText, Text: "I'll run"})
	send(llm.Event{Type: llm.EventText, Text: " them."})
	send(llm.Event{Type: llm.EventToolCallStart, Index: 0, ID: "toolu_up_0", Name: "Bash"})
	frags0 := fragments(args0, 1)
	for _, f := range frags0 {
		send(llm.Event{Type: llm.EventToolCallArgs, Index: 0, Text: f})
	}
	send(llm.Event{Type: llm.EventToolCallStart, Index: 1, Name: "MultiEdit"})
	frags1 := fragments(args1, 2)
	for _, f := range frags1 {
		send(llm.Event{Type: llm.EventToolCallArgs, Index: 1, Text: f})
	}
	usage := llm.Usage{PromptTokens: 1200, CompletionTokens: 85, CachedPromptTokens: 1000}
	if err := s.Finish(llm.FinishToolCalls, usage, llm.Timings{}); err != nil {
		t.Fatal(err)
	}

	events := parseSSE(t, rec.Body.String())
	var got []string
	for _, ev := range events {
		got = append(got, label(ev))
	}
	want := []string{"message_start", "ping",
		"start:0:thinking", "delta:0:thinking_delta", "delta:0:thinking_delta", "delta:0:signature_delta", "stop:0",
		"start:1:text", "delta:1:text_delta", "delta:1:text_delta", "stop:1",
		"start:2:tool_use"}
	for range frags0 {
		want = append(want, "delta:2:input_json_delta")
	}
	want = append(want, "stop:2", "start:3:tool_use")
	// args1 contains multi-byte runes split across 2-byte fragments; the
	// encoder holds back partial runes, so count non-empty emitted pieces.
	var c utf8Carry
	for _, f := range frags1 {
		if c.take(f) != "" {
			want = append(want, "delta:3:input_json_delta")
		}
	}
	want = append(want, "stop:3", "message_delta", "message_stop")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sequence\n got %v\nwant %v", got, want)
	}

	// message_start shape.
	msg := obj(events[0].data["message"])
	wantMsg := map[string]any{
		"id": "msg_01HXYZ", "type": "message", "role": "assistant", "model": "claude-sonnet-4-5",
		"content": []any{}, "stop_reason": nil, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 1200.0, "output_tokens": 1.0, "cache_creation_input_tokens": 0.0, "cache_read_input_tokens": 0.0},
	}
	if !reflect.DeepEqual(msg, wantMsg) {
		t.Fatalf("message_start %s", events[0].raw)
	}

	// Block starts and reassembled deltas.
	var thinking, text, signature string
	partial := map[int]string{}
	starts := map[int]map[string]any{}
	for _, ev := range events {
		switch ev.name {
		case "content_block_start":
			starts[idx(ev)] = obj(ev.data["content_block"])
		case "content_block_delta":
			d := obj(ev.data["delta"])
			switch d["type"] {
			case "thinking_delta":
				thinking += d["thinking"].(string)
			case "signature_delta":
				signature = d["signature"].(string)
			case "text_delta":
				text += d["text"].(string)
			case "input_json_delta":
				partial[idx(ev)] += d["partial_json"].(string)
			}
		}
	}
	if !reflect.DeepEqual(starts[0], map[string]any{"type": "thinking", "thinking": "", "signature": ""}) {
		t.Fatalf("thinking start %v", starts[0])
	}
	if !reflect.DeepEqual(starts[1], map[string]any{"type": "text", "text": ""}) {
		t.Fatalf("text start %v", starts[1])
	}
	if !reflect.DeepEqual(starts[2], map[string]any{"type": "tool_use", "id": "toolu_up_0", "name": "Bash", "input": map[string]any{}}) {
		t.Fatalf("tool start %v", starts[2])
	}
	if id, _ := starts[3]["id"].(string); !strings.HasPrefix(id, "toolu_") || len(id) <= len("toolu_") || starts[3]["name"] != "MultiEdit" {
		t.Fatalf("generated tool start %v", starts[3])
	}
	if thinking != "The user wants tests run." || text != "I'll run them." || signature != SimulatedSignature {
		t.Fatalf("thinking %q text %q signature %q", thinking, text, signature)
	}
	for i, orig := range map[int]string{2: args0, 3: args1} {
		if !json.Valid([]byte(partial[i])) {
			t.Fatalf("block %d partial_json not valid: %q", i, partial[i])
		}
		if llm.CanonicalJSON(partial[i]) != llm.CanonicalJSON(orig) {
			t.Fatalf("block %d: %q != %q", i, partial[i], orig)
		}
	}

	// message_delta shape.
	md := events[len(events)-2]
	wantMD := map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
		"usage": map[string]any{"input_tokens": 200.0, "output_tokens": 85.0, "cache_creation_input_tokens": 0.0, "cache_read_input_tokens": 1000.0},
	}
	if !reflect.DeepEqual(md.data, wantMD) {
		t.Fatalf("message_delta %s", md.raw)
	}

	// Round trip: replay the streamed message as history; tool_use input
	// decodes back to the original arguments.
	var blocks []map[string]any
	blocks = append(blocks, map[string]any{"type": "thinking", "thinking": thinking, "signature": signature})
	blocks = append(blocks, map[string]any{"type": "text", "text": text})
	for _, i := range []int{2, 3} {
		var input any
		if err := json.Unmarshal([]byte(partial[i]), &input); err != nil {
			t.Fatal(err)
		}
		blocks = append(blocks, map[string]any{"type": "tool_use", "id": starts[i]["id"], "name": starts[i]["name"], "input": input})
	}
	history, _ := json.Marshal(map[string]any{
		"model":    "m",
		"messages": []any{map[string]any{"role": "user", "content": "go"}, map[string]any{"role": "assistant", "content": blocks}},
	})
	req, err := New().DecodeRequest(history)
	if err != nil {
		t.Fatal(err)
	}
	am := req.Messages[1]
	if am.Reasoning != thinking || am.Content != text || len(am.ToolCalls) != 2 {
		t.Fatalf("round trip %+v", am)
	}
	for i, orig := range []string{args0, args1} {
		if llm.CanonicalJSON(am.ToolCalls[i].Arguments) != llm.CanonicalJSON(orig) {
			t.Fatalf("round trip args %d: %q", i, am.ToolCalls[i].Arguments)
		}
	}

	if err := s.Send(llm.Event{Type: llm.EventText, Text: "late"}); err == nil {
		t.Fatal("Send after Finish should fail")
	}
}

func TestStreamStopReasons(t *testing.T) {
	tests := []struct {
		name   string
		events []llm.Event
		reason string
		want   string
	}{
		{"end_turn", []llm.Event{{Type: llm.EventText, Text: "hi"}}, llm.FinishStop, "end_turn"},
		{"max_tokens", []llm.Event{{Type: llm.EventText, Text: "hi"}}, llm.FinishLength, "max_tokens"},
		{"tool_calls", []llm.Event{{Type: llm.EventToolCallStart, Name: "f"}}, llm.FinishToolCalls, "tool_use"},
		{"tool sent, upstream said stop", []llm.Event{{Type: llm.EventToolCallStart, Name: "f"}, {Type: llm.EventToolCallArgs, Text: "{}"}}, llm.FinishStop, "tool_use"},
		{"empty reason", nil, "", "end_turn"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			s := New().NewStream(rec, api.StreamMeta{ID: "x", Model: "m"})
			_ = s.Start()
			for _, ev := range tc.events {
				if err := s.Send(ev); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Finish(tc.reason, llm.Usage{CompletionTokens: 3}, llm.Timings{}); err != nil {
				t.Fatal(err)
			}
			events := parseSSE(t, rec.Body.String())
			md := events[len(events)-2]
			if md.name != "message_delta" || obj(md.data["delta"])["stop_reason"] != tc.want {
				t.Fatalf("got %s", md.raw)
			}
			if events[len(events)-1].name != "message_stop" {
				t.Fatal("missing message_stop")
			}
			// Every opened block is closed exactly once.
			open := map[int]int{}
			for _, ev := range events {
				switch ev.name {
				case "content_block_start":
					open[idx(ev)]++
				case "content_block_stop":
					open[idx(ev)]--
				}
			}
			for i, n := range open {
				if n != 0 {
					t.Fatalf("block %d unbalanced", i)
				}
			}
		})
	}
}

func TestStreamByteSplitUTF8(t *testing.T) {
	thinking := "Ünïcödé 思考 🤔"
	text := "naïve 日本語 🎉 done"
	rec := newRecorder()
	s := New().NewStream(rec, api.StreamMeta{ID: "u", Model: "m"})
	_ = s.Start()
	for i := 0; i < len(thinking); i++ {
		_ = s.Send(llm.Event{Type: llm.EventReasoning, Text: thinking[i : i+1]})
	}
	for i := 0; i < len(text); i++ {
		_ = s.Send(llm.Event{Type: llm.EventText, Text: text[i : i+1]})
	}
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "\xe6"}) // ends mid-rune: flushed (lossily) at close
	if err := s.Finish(llm.FinishStop, llm.Usage{}, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	var gotThinking, gotText string
	for _, ev := range parseSSE(t, rec.Body.String()) {
		if ev.name != "content_block_delta" {
			continue
		}
		d := obj(ev.data["delta"])
		switch d["type"] {
		case "thinking_delta":
			gotThinking += d["thinking"].(string)
		case "text_delta":
			gotText += d["text"].(string)
		}
	}
	if gotThinking != thinking {
		t.Fatalf("thinking %q", gotThinking)
	}
	if gotText != text+"�" {
		t.Fatalf("text %q", gotText)
	}
}

func TestStreamFail(t *testing.T) {
	tests := []struct{ errType, want string }{
		{"overloaded_error", "overloaded_error"},
		{"api_error", "api_error"},
		{"upstream_timeout", "api_error"},
	}
	for _, tc := range tests {
		t.Run(tc.errType, func(t *testing.T) {
			rec := newRecorder()
			s := New().NewStream(rec, api.StreamMeta{ID: "x", Model: "m"})
			_ = s.Start()
			_ = s.Send(llm.Event{Type: llm.EventText, Text: "partial"})
			if err := s.Fail(tc.errType, "Overloaded"); err != nil {
				t.Fatal(err)
			}
			events := parseSSE(t, rec.Body.String())
			last := events[len(events)-1]
			want := map[string]any{"type": "error", "error": map[string]any{"type": tc.want, "message": "Overloaded"}}
			if last.name != "error" || !reflect.DeepEqual(last.data, want) {
				t.Fatalf("got %s", last.raw)
			}
			if err := s.Finish("stop", llm.Usage{}, llm.Timings{}); err != nil || strings.Contains(rec.Body.String(), "message_stop") {
				t.Fatal("Finish after Fail must be a no-op")
			}
		})
	}

	rec := newRecorder()
	s := New().NewStream(rec, api.StreamMeta{ID: "y", Model: "m"})
	_ = s.Start()
	if err := s.Send(llm.Event{Type: llm.EventError, Err: errors.New("boom")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), "event: error\n") || !strings.Contains(rec.Body.String(), `"message":"boom"`) {
		t.Fatalf("body %s", rec.Body.String())
	}
}

// ---- non-streaming ----

func TestWriteResponse(t *testing.T) {
	resp := &llm.Response{
		Content:   "Let me check.",
		Reasoning: "Need to look.",
		ToolCalls: []llm.ToolCall{
			{ID: "toolu_A", Name: "Read", Arguments: `{"file_path": "/a.go", "limit": 10, "z": 1, "a": 2}`},
			{Name: "Bash", Arguments: `{"command": "ls`}, // truncated
			{ID: "toolu_C", Name: "LS"},
		},
		FinishReason: llm.FinishToolCalls,
		Usage:        llm.Usage{PromptTokens: 5000, CompletionTokens: 42, CachedPromptTokens: 4096},
	}
	rec := httptest.NewRecorder()
	if err := New().WriteResponse(rec, api.StreamMeta{ID: "abc", Model: "claude-opus-4-1"}, resp, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 {
		t.Fatal(rec.Code)
	}
	raw := rec.Body.String()
	if !strings.Contains(raw, `"input":{"file_path":"/a.go","limit":10,"z":1,"a":2}`) {
		t.Fatalf("tool input should keep the original key order: %s", raw)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	content := got["content"].([]any)
	if id := obj(content[3])["id"].(string); !strings.HasPrefix(id, "toolu_") {
		t.Fatalf("generated id %q", id)
	}
	obj(content[3])["id"] = "GEN"
	want := map[string]any{
		"id": "msg_abc", "type": "message", "role": "assistant", "model": "claude-opus-4-1",
		"content": []any{
			map[string]any{"type": "thinking", "thinking": "Need to look.", "signature": SimulatedSignature},
			map[string]any{"type": "text", "text": "Let me check."},
			map[string]any{"type": "tool_use", "id": "toolu_A", "name": "Read", "input": map[string]any{"file_path": "/a.go", "limit": 10.0, "z": 1.0, "a": 2.0}},
			map[string]any{"type": "tool_use", "id": "GEN", "name": "Bash", "input": map[string]any{}},
			map[string]any{"type": "tool_use", "id": "toolu_C", "name": "LS", "input": map[string]any{}},
		},
		"stop_reason": "tool_use", "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 904.0, "output_tokens": 42.0, "cache_creation_input_tokens": 0.0, "cache_read_input_tokens": 4096.0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %s", raw)
	}

	// Plain text, no reasoning.
	rec = httptest.NewRecorder()
	_ = New().WriteResponse(rec, api.StreamMeta{ID: "msg_x", Model: "m"}, &llm.Response{Content: "hi", FinishReason: llm.FinishLength}, llm.Timings{})
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "msg_x" || got["stop_reason"] != "max_tokens" || len(got["content"].([]any)) != 1 {
		t.Fatalf("got %s", rec.Body.String())
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		status           int
		errType, message string
		wantType         string
	}{
		{400, "context_length_exceeded", "prompt is too long: 215000 tokens > 200000 maximum", "invalid_request_error"},
		{529, "overloaded_error", "Overloaded", "overloaded_error"},
		{400, "invalid_request_error", "messages: field required", "invalid_request_error"},
		{429, "rate_limit_error", "slow down", "rate_limit_error"},
		{500, "", "boom", "api_error"},
	}
	for _, tc := range tests {
		t.Run(tc.errType, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New().WriteError(rec, tc.status, tc.errType, tc.message)
			var got map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"type": "error", "error": map[string]any{"type": tc.wantType, "message": tc.message}}
			if rec.Code != tc.status || !reflect.DeepEqual(got, want) {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWriteCountTokens(t *testing.T) {
	rec := httptest.NewRecorder()
	New().WriteCountTokens(rec, 1234)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"input_tokens":1234}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestName(t *testing.T) {
	if New().Name() != llm.ProtocolAnthropic {
		t.Fatal("name")
	}
}
