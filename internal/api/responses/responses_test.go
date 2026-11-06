package responses

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

const applyPatchGrammar = `start: begin_patch hunk+ end_patch
begin_patch: "*** Begin Patch" LF
end_patch: "*** End Patch" LF?`

// codexBody is shaped like a Codex CLI turn after one shell round trip and
// one apply_patch round trip.
var codexBody = `{
  "model": "gpt-oss:20b",
  "instructions": "You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI.",
  "input": [
    {"type":"message","role":"developer","content":[{"type":"input_text","text":"<permissions instructions>sandbox: workspace-write</permissions instructions>"}]},
    {"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>\n  <cwd>/home/me/proj</cwd>\n</environment_context>"}]},
    {"type":"message","role":"user","content":[{"type":"input_text","text":"fix the failing test"},{"type":"input_image","image_url":"data:image/png;base64,AAAA"}]},
    {"type":"reasoning","id":"rs_1","summary":[{"type":"summary_text","text":"**Running tests**"}],"content":[{"type":"reasoning_text","text":"I should run the tests first."}],"encrypted_content":null},
    {"type":"function_call","id":"fc_1","name":"shell","arguments":"{\"command\":[\"go\",\"test\",\"./...\"],\"workdir\":\"/home/me/proj\"}","call_id":"call_A"},
    {"type":"function_call","id":"fc_2","name":"shell","arguments":"{\"command\":[\"git\",\"status\"]}","call_id":"call_B"},
    {"type":"function_call_output","call_id":"call_A","output":"--- FAIL: TestAdd (0.00s)"},
    {"type":"function_call_output","call_id":"call_B","output":[{"type":"input_text","text":"On branch main"}]},
    {"type":"reasoning","id":"rs_2","summary":[{"type":"summary_text","text":"Patch the off-by-one."}],"encrypted_content":"gAAAAB..."},
    {"type":"message","role":"assistant","id":"msg_1","status":"completed","content":[{"type":"output_text","text":"The test fails because of an off-by-one; patching.","annotations":[]}]},
    {"type":"custom_tool_call","id":"ctc_1","status":"completed","call_id":"call_C","name":"apply_patch","input":"*** Begin Patch\n*** Update File: add.go\n-\treturn a+b+1\n+\treturn a+b\n*** End Patch\n"},
    {"type":"custom_tool_call_output","call_id":"call_C","output":"Success. Updated the following files:\nM add.go\n"},
    {"type":"local_shell_call","id":"lsh_1","call_id":"call_D","status":"completed","action":{"type":"exec","command":["ls"]}},
    {"role":"user","content":"great, run them again"}
  ],
  "tools": [
    {"type":"function","name":"shell","description":"Runs a shell command and returns its output.","strict":false,"parameters":{"type":"object","properties":{"command":{"type":"array","items":{"type":"string"}},"workdir":{"type":"string"}},"required":["command"],"additionalProperties":false}},
    {"type":"custom","name":"apply_patch","description":"Use the apply_patch tool to edit files.","format":{"type":"grammar","syntax":"lark","definition":` + mustJSON(applyPatchGrammar) + `}},
    {"type":"web_search"}
  ],
  "tool_choice": "auto",
  "parallel_tool_calls": false,
  "reasoning": {"effort":"high","summary":"auto"},
  "store": false,
  "stream": true,
  "include": ["reasoning.encrypted_content"],
  "prompt_cache_key": "0199a1b2-c3d4",
  "text": {"verbosity":"medium"}
}`

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func newFrontend() *Frontend { return New().(*Frontend) }

func TestDecodeCodexRequest(t *testing.T) {
	f := newFrontend()
	req, err := f.DecodeRequest([]byte(codexBody))
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "gpt-oss:20b" || !req.Stream || req.ReasoningEffort != "high" || req.Protocol != llm.ProtocolResponses {
		t.Fatalf("scalars: %+v", req)
	}
	if string(req.ToolChoice) != `"auto"` {
		t.Fatalf("tool_choice %s", req.ToolChoice)
	}
	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are Codex, based on GPT-5. You are running as a coding agent in the Codex CLI."},
		{Role: llm.RoleSystem, Content: "<permissions instructions>sandbox: workspace-write</permissions instructions>"},
		{Role: llm.RoleUser, Content: "<environment_context>\n  <cwd>/home/me/proj</cwd>\n</environment_context>"},
		{Role: llm.RoleUser, Content: "fix the failing test", Images: 1},
		{Role: llm.RoleAssistant, Reasoning: "I should run the tests first.", ToolCalls: []llm.ToolCall{
			{ID: "call_A", Name: "shell", Arguments: `{"command":["go","test","./..."],"workdir":"/home/me/proj"}`},
			{ID: "call_B", Name: "shell", Arguments: `{"command":["git","status"]}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "call_A", Name: "shell", Content: "--- FAIL: TestAdd (0.00s)"},
		{Role: llm.RoleTool, ToolCallID: "call_B", Name: "shell", Content: "On branch main"},
		{Role: llm.RoleAssistant, Reasoning: "Patch the off-by-one.", Content: "The test fails because of an off-by-one; patching.", ToolCalls: []llm.ToolCall{
			{ID: "call_C", Name: "apply_patch", Arguments: mustJSON(map[string]string{"input": "*** Begin Patch\n*** Update File: add.go\n-\treturn a+b+1\n+\treturn a+b\n*** End Patch\n"})},
		}},
		{Role: llm.RoleTool, ToolCallID: "call_C", Name: "apply_patch", Content: "Success. Updated the following files:\nM add.go\n"},
		{Role: llm.RoleUser, Content: "great, run them again"},
	}
	if len(req.Messages) != len(want) {
		for i, m := range req.Messages {
			t.Logf("%d: %+v", i, m)
		}
		t.Fatalf("got %d messages, want %d", len(req.Messages), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(req.Messages[i], want[i]) {
			t.Errorf("message %d:\n got %+v\nwant %+v", i, req.Messages[i], want[i])
		}
	}

	if len(req.Tools) != 2 {
		t.Fatalf("tools: %+v", req.Tools)
	}
	if req.Tools[0].Name != "shell" || IsCustomTool(req.Tools[0]) {
		t.Errorf("shell tool: %+v", req.Tools[0])
	}
	if llm.CanonicalJSON(string(req.Tools[0].Parameters)) != `{"additionalProperties":false,"properties":{"command":{"items":{"type":"string"},"type":"array"},"workdir":{"type":"string"}},"required":["command"],"type":"object"}` {
		t.Errorf("shell params %s", req.Tools[0].Parameters)
	}
	ap := req.Tools[1]
	if ap.Name != "apply_patch" || ap.Description != "Use the apply_patch tool to edit files." || !IsCustomTool(ap) {
		t.Errorf("apply_patch tool: %+v", ap)
	}
	var schema struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if err := json.Unmarshal(ap.Parameters, &schema); err != nil {
		t.Fatal(err)
	}
	in := schema.Properties["input"]
	if schema.Type != "object" || in.Type != "string" || !reflect.DeepEqual(schema.Required, []string{"input"}) ||
		!strings.Contains(in.Description, "lark grammar") || !strings.Contains(in.Description, applyPatchGrammar) {
		t.Errorf("custom schema: %s", ap.Parameters)
	}
	if !f.isCustom("apply_patch") || f.isCustom("shell") {
		t.Error("custom registry not populated")
	}

	warns := strings.Join(req.Warnings, "\n")
	for _, w := range []string{"input_image", "local_shell_call", `"web_search"`} {
		if !strings.Contains(warns, w) {
			t.Errorf("missing warning about %s in %q", w, warns)
		}
	}
}

func TestDecodeVariants(t *testing.T) {
	f := newFrontend()
	req, err := f.DecodeRequest([]byte(`{"model":"m","input":"hello","max_output_tokens":256,"temperature":0.2,"top_p":0.9,
		"tool_choice":{"type":"function","name":"shell"},"reasoning":{"effort":"minimal"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 1 || !reflect.DeepEqual(req.Messages[0], llm.Message{Role: llm.RoleUser, Content: "hello"}) {
		t.Fatalf("messages %+v", req.Messages)
	}
	if req.MaxTokens != 256 || *req.Temperature != 0.2 || *req.TopP != 0.9 || req.Stream || req.ReasoningEffort != "low" {
		t.Fatalf("scalars %+v", req)
	}
	if llm.CanonicalJSON(string(req.ToolChoice)) != `{"function":{"name":"shell"},"type":"function"}` {
		t.Fatalf("tool_choice %s", req.ToolChoice)
	}

	// Re-declaring apply_patch as a function tool clears the custom flag.
	if _, err := f.DecodeRequest([]byte(`{"input":"x","tools":[{"type":"custom","name":"apply_patch"}]}`)); err != nil {
		t.Fatal(err)
	}
	if !f.isCustom("apply_patch") {
		t.Fatal("expected custom")
	}
	if _, err := f.DecodeRequest([]byte(`{"input":"x","tools":[{"type":"function","name":"apply_patch","parameters":{"type":"object"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if f.isCustom("apply_patch") {
		t.Fatal("function re-declaration should clear custom flag")
	}

	for _, tc := range []struct{ body, want string }{
		{`{"input":"x","previous_response_id":"resp_123"}`, "previous_response_id"},
		{`{"input":[]}`, "input is required"},
		{`{"input":42}`, "input"},
		{`{"input":[{"type":"message","role":"robot","content":"x"}]}`, "role"},
		{`not json`, "invalid JSON"},
	} {
		if _, err := f.DecodeRequest([]byte(tc.body)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err %v, want containing %q", tc.body, err, tc.want)
		}
	}
}

type sseEvent struct {
	name string
	data map[string]any
	raw  string
}

func parseSSE(t *testing.T, body string) []sseEvent {
	t.Helper()
	var out []sseEvent
	var cur sseEvent
	sc := bufio.NewScanner(strings.NewReader(body))
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			cur.name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			cur.raw = strings.TrimPrefix(line, "data: ")
			if err := json.Unmarshal([]byte(cur.raw), &cur.data); err != nil {
				t.Fatalf("bad data %q: %v", cur.raw, err)
			}
		case line == "":
			if cur.name != "" || cur.raw != "" {
				out = append(out, cur)
			}
			cur = sseEvent{}
		}
	}
	return out
}

func names(evs []sseEvent) []string {
	var n []string
	for _, e := range evs {
		n = append(n, e.name)
	}
	return n
}

func checkSequence(t *testing.T, evs []sseEvent) {
	t.Helper()
	for i, e := range evs {
		if e.data["type"] != e.name {
			t.Errorf("event %d: type %v != event name %s", i, e.data["type"], e.name)
		}
		if seq, _ := e.data["sequence_number"].(float64); int(seq) != i {
			t.Errorf("event %d (%s): sequence_number %v", i, e.name, e.data["sequence_number"])
		}
	}
}

var meta = api.StreamMeta{ID: "abc123", Model: "gpt-oss:20b", Created: time.Unix(1762400000, 0)}

func TestStreamReasoningTextAndToolCalls(t *testing.T) {
	f := newFrontend()
	if _, err := f.DecodeRequest([]byte(codexBody)); err != nil { // registers apply_patch as custom
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	s := f.NewStream(rec, meta)
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	patch := "*** Begin Patch\n*** Add File: hello.txt\n+hi\n*** End Patch\n"
	patchArgs := mustJSON(map[string]string{"input": patch})
	evs := []llm.Event{
		{Type: llm.EventReasoning, Text: "Let"},
		{Type: llm.EventReasoning, Text: " me"},
		{Type: llm.EventReasoning, Text: " think."},
		{Type: llm.EventText, Text: "Hel"},
		{Type: llm.EventText, Text: ""}, // ignored
		{Type: llm.EventText, Text: "lo!"},
		{Type: llm.EventToolCallStart, Index: 0, ID: "call_1", Name: "shell"},
		{Type: llm.EventToolCallArgs, Index: 0, Text: `{"com`},
		{Type: llm.EventToolCallArgs, Index: 0, Text: `mand":`},
		{Type: llm.EventToolCallArgs, Index: 0, Text: `["ls"]}`},
		{Type: llm.EventToolCallStart, Index: 1, Name: "apply_patch"}, // no ID: generated
	}
	for i := 0; i < len(patchArgs); i += 3 {
		end := min(i+3, len(patchArgs))
		evs = append(evs, llm.Event{Type: llm.EventToolCallArgs, Index: 1, Text: patchArgs[i:end]})
	}
	for _, ev := range evs {
		if err := s.Send(ev); err != nil {
			t.Fatal(err)
		}
	}
	usage := llm.Usage{PromptTokens: 1200, CompletionTokens: 80, ReasoningTokens: 5, CachedPromptTokens: 1024}
	if err := s.Finish(llm.FinishToolCalls, usage, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Send(llm.Event{Type: llm.EventText, Text: "x"}); err == nil {
		t.Error("Send after Finish should fail")
	}

	got := parseSSE(t, rec.Body.String())
	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added",
		"response.reasoning_text.delta", "response.reasoning_text.delta", "response.reasoning_text.delta",
		"response.reasoning_text.done", "response.output_item.done",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.output_item.added",
		"response.function_call_arguments.delta", "response.function_call_arguments.delta", "response.function_call_arguments.delta",
		"response.function_call_arguments.done", "response.output_item.done",
		"response.output_item.added",
		"response.custom_tool_call_input.delta", "response.custom_tool_call_input.done", "response.output_item.done",
		"response.completed",
	}
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("event sequence:\n got %q\nwant %q", names(got), want)
	}
	checkSequence(t, got)

	created := got[0].data["response"].(map[string]any)
	if created["id"] != "resp_abc123" || created["object"] != "response" || created["status"] != "in_progress" ||
		created["model"] != "gpt-oss:20b" || created["created_at"].(float64) != 1762400000 || created["usage"] != nil ||
		len(created["output"].([]any)) != 0 {
		t.Errorf("created response: %s", got[0].raw)
	}

	// Spot-check event shapes.
	expectJSON(t, got[2].raw, `{"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"type":"reasoning","id":"rs_abc123_0","summary":[],"content":[]}}`)
	expectJSON(t, got[3].raw, `{"type":"response.reasoning_text.delta","sequence_number":3,"output_index":0,"item_id":"rs_abc123_0","content_index":0,"delta":"Let"}`)
	expectJSON(t, got[6].raw, `{"type":"response.reasoning_text.done","sequence_number":6,"output_index":0,"item_id":"rs_abc123_0","content_index":0,"text":"Let me think."}`)
	expectJSON(t, got[8].raw, `{"type":"response.output_item.added","sequence_number":8,"output_index":1,"item":{"type":"message","id":"msg_abc123_1","status":"in_progress","role":"assistant","content":[]}}`)
	expectJSON(t, got[9].raw, `{"type":"response.content_part.added","sequence_number":9,"output_index":1,"item_id":"msg_abc123_1","content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}`)
	expectJSON(t, got[10].raw, `{"type":"response.output_text.delta","sequence_number":10,"output_index":1,"item_id":"msg_abc123_1","content_index":0,"delta":"Hel","logprobs":[]}`)
	expectJSON(t, got[15].raw, `{"type":"response.output_item.added","sequence_number":15,"output_index":2,"item":{"type":"function_call","id":"fc_abc123_2","call_id":"call_1","name":"shell","arguments":"","status":"in_progress"}}`)
	expectJSON(t, got[16].raw, `{"type":"response.function_call_arguments.delta","sequence_number":16,"output_index":2,"item_id":"fc_abc123_2","delta":"{\"com"}`)
	expectJSON(t, got[19].raw, `{"type":"response.function_call_arguments.done","sequence_number":19,"output_index":2,"item_id":"fc_abc123_2","arguments":"{\"command\":[\"ls\"]}"}`)

	added := got[21].data["item"].(map[string]any)
	callID, _ := added["call_id"].(string)
	if added["type"] != "custom_tool_call" || added["input"] != "" || !strings.HasPrefix(callID, "call_") || len(callID) < 10 {
		t.Errorf("custom added: %s", got[21].raw)
	}
	if got[22].data["delta"] != patch || got[23].data["input"] != patch {
		t.Errorf("custom input events: %s / %s", got[22].raw, got[23].raw)
	}

	final := got[len(got)-1].data["response"].(map[string]any)
	if final["status"] != "completed" || final["incomplete_details"] != nil || final["error"] != nil {
		t.Errorf("final: %s", got[len(got)-1].raw)
	}
	var out struct {
		Output []map[string]any `json:"output"`
		Usage  usageJSON        `json:"usage"`
	}
	b, _ := json.Marshal(final)
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	wantUsage := usageJSON{InputTokens: 1200, OutputTokens: 80, TotalTokens: 1280}
	wantUsage.InputTokensDetails.CachedTokens = 1024
	wantUsage.OutputTokensDetails.ReasoningTokens = 5
	if out.Usage != wantUsage {
		t.Errorf("usage %+v", out.Usage)
	}
	if len(out.Output) != 4 {
		t.Fatalf("output items: %s", b)
	}
	expectJSON(t, mustJSON(out.Output[0]), `{"type":"reasoning","id":"rs_abc123_0","summary":[],"content":[{"type":"reasoning_text","text":"Let me think."}]}`)
	expectJSON(t, mustJSON(out.Output[1]), `{"type":"message","id":"msg_abc123_1","status":"completed","role":"assistant","content":[{"type":"output_text","text":"Hello!","annotations":[]}]}`)
	expectJSON(t, mustJSON(out.Output[2]), `{"type":"function_call","id":"fc_abc123_2","call_id":"call_1","name":"shell","arguments":"{\"command\":[\"ls\"]}","status":"completed"}`)
	expectJSON(t, mustJSON(out.Output[3]), `{"type":"custom_tool_call","id":"ctc_abc123_3","call_id":"`+callID+`","name":"apply_patch","input":`+mustJSON(patch)+`,"status":"completed"}`)
	// output_item.done items equal the final output items.
	for i, evIdx := range []int{7, 14, 20, 24} {
		if !reflect.DeepEqual(got[evIdx].data["item"], out.Output[i]) {
			t.Errorf("output_item.done %d differs from final output", i)
		}
	}

	// The output round-trips through DecodeRequest as Codex would replay it.
	replay := map[string]any{"input": append([]any{map[string]any{"role": "user", "content": "hi"}}, anySlice(out.Output)...)}
	req, err := f.DecodeRequest([]byte(mustJSON(replay)))
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("replay messages %+v", req.Messages)
	}
	m := req.Messages[1]
	if m.Reasoning != "Let me think." || m.Content != "Hello!" || len(m.ToolCalls) != 2 ||
		m.ToolCalls[0].Arguments != `{"command":["ls"]}` || customInput(m.ToolCalls[1].Arguments) != patch {
		t.Errorf("replayed assistant message %+v", m)
	}
}

func anySlice(items []map[string]any) []any {
	out := make([]any, len(items))
	for i, it := range items {
		out[i] = it
	}
	return out
}

func expectJSON(t *testing.T, got, want string) {
	t.Helper()
	if llm.CanonicalJSON(got) != llm.CanonicalJSON(want) {
		t.Errorf("JSON mismatch:\n got %s\nwant %s", llm.CanonicalJSON(got), llm.CanonicalJSON(want))
	}
}

func TestStreamIncomplete(t *testing.T) {
	f := newFrontend()
	rec := httptest.NewRecorder()
	s := f.NewStream(rec, meta)
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "partial"})
	if err := s.Finish(llm.FinishLength, llm.Usage{PromptTokens: 10, CompletionTokens: 1}, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	got := parseSSE(t, rec.Body.String())
	checkSequence(t, got)
	last := got[len(got)-1]
	if last.name != "response.incomplete" {
		t.Fatalf("last event %s", last.name)
	}
	r := last.data["response"].(map[string]any)
	if r["status"] != "incomplete" || r["incomplete_details"].(map[string]any)["reason"] != "max_output_tokens" {
		t.Errorf("incomplete response %s", last.raw)
	}
	item := r["output"].([]any)[0].(map[string]any)
	if item["status"] != "incomplete" || got[len(got)-2].data["item"].(map[string]any)["status"] != "incomplete" {
		t.Errorf("message status: %v", item)
	}
	if r["usage"].(map[string]any)["total_tokens"].(float64) != 11 {
		t.Errorf("usage %v", r["usage"])
	}
}

func TestStreamFailed(t *testing.T) {
	f := newFrontend()
	rec := httptest.NewRecorder()
	s := f.NewStream(rec, meta)
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "par"})
	if err := s.Fail("context_length_exceeded", "prompt is too long"); err != nil {
		t.Fatal(err)
	}
	if err := s.Finish(llm.FinishStop, llm.Usage{}, llm.Timings{}); err == nil {
		t.Error("Finish after Fail should error")
	}
	got := parseSSE(t, rec.Body.String())
	checkSequence(t, got)
	want := []string{"response.created", "response.in_progress", "response.output_item.added", "response.content_part.added", "response.output_text.delta", "response.failed"}
	if !reflect.DeepEqual(names(got), want) {
		t.Fatalf("events %q", names(got))
	}
	r := got[5].data["response"].(map[string]any)
	e := r["error"].(map[string]any)
	if r["status"] != "failed" || e["code"] != "context_length_exceeded" || e["message"] != "prompt is too long" {
		t.Errorf("failed: %s", got[5].raw)
	}
	if out := r["output"].([]any); len(out) != 1 || out[0].(map[string]any)["status"] != "incomplete" {
		t.Errorf("failed output %v", out)
	}
}

func TestStreamArgsWithoutStartAndLateArgs(t *testing.T) {
	f := newFrontend()
	rec := httptest.NewRecorder()
	s := f.NewStream(rec, api.StreamMeta{Model: "m"})
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventToolCallArgs, Index: 0, Text: `{"a":`})
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "x"})
	_ = s.Send(llm.Event{Type: llm.EventToolCallArgs, Index: 0, Text: `1}`}) // late
	_ = s.Finish(llm.FinishStop, llm.Usage{}, llm.Timings{})
	got := parseSSE(t, rec.Body.String())
	checkSequence(t, got)
	r := got[len(got)-1].data["response"].(map[string]any)
	if !strings.HasPrefix(r["id"].(string), "resp_") || len(r["id"].(string)) < 10 {
		t.Errorf("generated id %v", r["id"])
	}
	fc := r["output"].([]any)[0].(map[string]any)
	if fc["type"] != "function_call" || fc["arguments"] != `{"a":1}` || !strings.HasPrefix(fc["call_id"].(string), "call_") {
		t.Errorf("function call %v", fc)
	}
}

func TestWriteResponse(t *testing.T) {
	f := newFrontend()
	if _, err := f.DecodeRequest([]byte(`{"input":"x","tools":[{"type":"custom","name":"apply_patch"}]}`)); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	resp := &llm.Response{
		Reasoning: "think", Content: "done", FinishReason: llm.FinishToolCalls,
		ToolCalls: []llm.ToolCall{
			{ID: "call_1", Name: "shell", Arguments: `{"command":["ls"]}`},
			{ID: "call_2", Name: "apply_patch", Arguments: `{"input":"*** Begin Patch\n*** End Patch\n"}`},
		},
		Usage: llm.Usage{PromptTokens: 3, CompletionTokens: 4},
	}
	if err := f.WriteResponse(rec, meta, resp, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("status %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	var r map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r["id"] != "resp_abc123" || r["status"] != "completed" || r["object"] != "response" {
		t.Errorf("response %s", rec.Body)
	}
	out := r["output"].([]any)
	if len(out) != 4 {
		t.Fatalf("output %s", rec.Body)
	}
	expectJSON(t, mustJSON(out[2]), `{"type":"function_call","id":"fc_abc123_2","call_id":"call_1","name":"shell","arguments":"{\"command\":[\"ls\"]}","status":"completed"}`)
	expectJSON(t, mustJSON(out[3]), `{"type":"custom_tool_call","id":"ctc_abc123_3","call_id":"call_2","name":"apply_patch","input":"*** Begin Patch\n*** End Patch\n","status":"completed"}`)
	if r["usage"].(map[string]any)["total_tokens"].(float64) != 7 {
		t.Errorf("usage %v", r["usage"])
	}
}

func TestWriteError(t *testing.T) {
	f := newFrontend()
	rec := httptest.NewRecorder()
	f.WriteError(rec, 400, "context_length_exceeded", "too many tokens")
	if rec.Code != 400 {
		t.Fatalf("status %d", rec.Code)
	}
	expectJSON(t, rec.Body.String(), `{"error":{"message":"too many tokens","type":"invalid_request_error","param":null,"code":"context_length_exceeded"}}`)

	rec = httptest.NewRecorder()
	f.WriteError(rec, 503, "overloaded_error", "busy")
	expectJSON(t, rec.Body.String(), `{"error":{"message":"busy","type":"server_error","param":null,"code":"server_is_overloaded"}}`)
}

func TestCustomInput(t *testing.T) {
	for in, want := range map[string]string{
		`{"input":"abc"}`:   "abc",
		`{"patch":"xyz"}`:   "xyz",
		`raw text`:          "raw text",
		`{"input":{"a":1}}`: `{"a":1}`,
		``:                  "",
	} {
		if got := customInput(in); got != want {
			t.Errorf("customInput(%q) = %q, want %q", in, got, want)
		}
	}
}
