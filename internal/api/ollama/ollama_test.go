package ollama

import (
	"bufio"
	"encoding/json"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

// zedBody is shaped like an agent-panel request from Zed / Continue after a
// two-tool round trip (tool results in reverse order).
const zedBody = `{
  "model": "qwen3-coder:30b",
  "messages": [
    {"role": "system", "content": "You are a highly skilled software engineer."},
    {"role": "user", "content": "what's in main.go?", "images": ["iVBORw0KGgo="]},
    {"role": "assistant", "content": "", "thinking": "I need to read the file.", "tool_calls": [
      {"function": {"name": "read_file", "arguments": {"path": "main.go", "limit": 200}}},
      {"function": {"index": 1, "name": "list_dir", "arguments": {"path": "."}}}
    ]},
    {"role": "tool", "content": "main.go\ngo.mod", "tool_name": "list_dir"},
    {"role": "tool", "content": "package main", "tool_name": "read_file"},
    {"role": "user", "content": "thanks"}
  ],
  "tools": [
    {"type": "function", "function": {"name": "read_file", "description": "Read a file", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
    {"type": "function", "function": {"name": "list_dir", "description": "List a directory", "parameters": {"type": "object", "properties": {"path": {"type": "string"}}}}}
  ],
  "options": {"num_ctx": 32768, "temperature": 0.6, "top_p": 0.95, "num_predict": -1, "stop": ["<|im_end|>"]},
  "think": "high",
  "keep_alive": "30m",
  "format": "json"
}`

func TestChatDecode(t *testing.T) {
	f := NewChat()
	if f.Name() != llm.ProtocolOllama || NewGenerate().Name() != llm.ProtocolOllama {
		t.Fatal("names")
	}
	req, err := f.DecodeRequest([]byte(zedBody))
	if err != nil {
		t.Fatal(err)
	}
	if req.Model != "qwen3-coder:30b" || !req.Stream || req.NumCtx != 32768 || req.MaxTokens != 0 ||
		*req.Temperature != 0.6 || *req.TopP != 0.95 || !reflect.DeepEqual(req.Stop, []string{"<|im_end|>"}) ||
		req.ReasoningEffort != "high" || req.Protocol != llm.ProtocolOllama {
		t.Fatalf("scalars %+v", req)
	}
	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "You are a highly skilled software engineer."},
		{Role: llm.RoleUser, Content: "what's in main.go?", Images: 1},
		{Role: llm.RoleAssistant, Reasoning: "I need to read the file.", ToolCalls: []llm.ToolCall{
			{ID: "call_2_0", Name: "read_file", Arguments: `{"path":"main.go","limit":200}`},
			{ID: "call_2_1", Name: "list_dir", Arguments: `{"path":"."}`},
		}},
		{Role: llm.RoleTool, Name: "list_dir", ToolCallID: "call_2_1", Content: "main.go\ngo.mod"},
		{Role: llm.RoleTool, Name: "read_file", ToolCallID: "call_2_0", Content: "package main"},
		{Role: llm.RoleUser, Content: "thanks"},
	}
	if !reflect.DeepEqual(req.Messages, want) {
		for i, m := range req.Messages {
			t.Logf("%d: %+v", i, m)
		}
		t.Fatal("messages mismatch")
	}
	if len(req.Tools) != 2 || req.Tools[0].Name != "read_file" || req.Tools[0].Description != "Read a file" ||
		llm.CanonicalJSON(string(req.Tools[0].Parameters)) != `{"properties":{"path":{"type":"string"}},"required":["path"],"type":"object"}` {
		t.Fatalf("tools %+v", req.Tools)
	}
	warns := strings.Join(req.Warnings, "\n")
	if !strings.Contains(warns, "image") || !strings.Contains(warns, "format") {
		t.Errorf("warnings %q", warns)
	}
}

func TestChatDecodeVariants(t *testing.T) {
	f := NewChat()
	req, err := f.DecodeRequest([]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":false,"think":true,"options":{"num_predict":128}}`))
	if err != nil {
		t.Fatal(err)
	}
	if req.Stream || req.ReasoningEffort != "medium" || req.MaxTokens != 128 || req.NumCtx != 0 || req.Temperature != nil {
		t.Fatalf("%+v", req)
	}
	req, err = f.DecodeRequest([]byte(`{"model":"m","messages":[]}`))
	if err != nil || len(req.Messages) != 0 || !req.Stream {
		t.Fatalf("load probe: %+v %v", req, err)
	}
	// Explicit ids are kept and matched.
	req, err = f.DecodeRequest([]byte(`{"model":"m","messages":[
		{"role":"assistant","tool_calls":[{"id":"abc","function":{"name":"x","arguments":"{\"a\": 1}"}}]},
		{"role":"tool","tool_call_id":"abc","content":"ok"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if tc := req.Messages[0].ToolCalls[0]; tc.ID != "abc" || tc.Arguments != `{"a":1}` {
		t.Errorf("tool call %+v", tc)
	}
	if m := req.Messages[1]; m.ToolCallID != "abc" || m.Name != "x" {
		t.Errorf("tool message %+v", m)
	}
	for _, body := range []string{
		`{"messages":[{"role":"robot","content":"x"}]}`,
		`{"messages":[],"options":{"num_ctx":"big"}}`,
		`{"messages":[],"think":3}`,
		`{`,
	} {
		if _, err := f.DecodeRequest([]byte(body)); err == nil {
			t.Errorf("%s: expected error", body)
		}
	}
}

func parseLines(t *testing.T, body string) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(strings.NewReader(body))
	for sc.Scan() {
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	if !strings.HasSuffix(body, "\n") {
		t.Error("stream does not end with a newline")
	}
	return out
}

func canon(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return llm.CanonicalJSON(string(b))
}

var (
	fixed   = time.Date(2026, 11, 6, 12, 0, 0, 123456789, time.FixedZone("X", 3600))
	fixedTS = "2026-11-06T11:00:00.123456789Z"
	meta    = api.StreamMeta{ID: "x1", Model: "qwen3-coder:30b", Created: fixed}
	usage   = llm.Usage{PromptTokens: 1500, CompletionTokens: 42}
	timings = llm.Timings{Load: 2 * time.Second, PromptEval: 800 * time.Millisecond, Eval: 1200 * time.Millisecond,
		Total: 4100 * time.Millisecond, PromptEvalTokens: 300, EvalTokens: 42}
)

func TestChatStream(t *testing.T) {
	rec := httptest.NewRecorder()
	s := NewChat().NewStream(rec, meta)
	s.(*stream).now = func() time.Time { return fixed }
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Fatalf("content-type %q", ct)
	}
	for _, ev := range []llm.Event{
		{Type: llm.EventReasoning, Text: "Hmm"},
		{Type: llm.EventText, Text: "Let me "},
		{Type: llm.EventText, Text: "look."},
		{Type: llm.EventToolCallStart, Index: 0, ID: "call_a", Name: "read_file"},
		{Type: llm.EventToolCallArgs, Index: 0, Text: `{"pa`},
		{Type: llm.EventToolCallArgs, Index: 0, Text: `th": "a.go",`},
		{Type: llm.EventToolCallArgs, Index: 0, Text: ` "limit": 10}`},
		{Type: llm.EventToolCallStart, Index: 1, Name: "list_dir"},
		{Type: llm.EventToolCallArgs, Index: 1, Text: `{"path"`},
		{Type: llm.EventToolCallArgs, Index: 1, Text: `:"."}`},
	} {
		if err := s.Send(ev); err != nil {
			t.Fatal(err)
		}
	}
	// Only the first call is emitted until it is known to be complete.
	if n := strings.Count(rec.Body.String(), "\n"); n != 4 {
		t.Fatalf("expected 4 lines before Finish, got %d:\n%s", n, rec.Body)
	}
	if err := s.Finish(llm.FinishToolCalls, usage, timings); err != nil {
		t.Fatal(err)
	}
	if s.Send(llm.Event{Type: llm.EventText, Text: "x"}) == nil {
		t.Error("Send after Finish should fail")
	}
	lines := parseLines(t, rec.Body.String())
	if len(lines) != 6 {
		t.Fatalf("lines:\n%s", rec.Body)
	}
	want := []string{
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":"","thinking":"Hmm"},"done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":"Let me "},"done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":"look."},"done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":"","tool_calls":[{"id":"call_a","function":{"index":0,"name":"read_file","arguments":{"path":"a.go","limit":10}}}]},"done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":"","tool_calls":[{"id":"ID","function":{"index":1,"name":"list_dir","arguments":{"path":"."}}}]},"done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","message":{"role":"assistant","content":""},"done":true,"done_reason":"stop",` +
			`"total_duration":4100000000,"load_duration":2000000000,"prompt_eval_count":1500,"prompt_eval_duration":800000000,"eval_count":42,"eval_duration":1200000000}`,
	}
	// The second call's id is generated.
	tc := lines[4]["message"].(map[string]any)["tool_calls"].([]any)[0].(map[string]any)
	if id, _ := tc["id"].(string); !strings.HasPrefix(id, "call_") || len(id) < 10 {
		t.Errorf("generated id %v", tc["id"])
	}
	tc["id"] = "ID"
	for i := range want {
		if got := canon(t, lines[i]); got != llm.CanonicalJSON(want[i]) {
			t.Errorf("line %d:\n got %s\nwant %s", i, got, llm.CanonicalJSON(want[i]))
		}
	}
	// Arguments keep the model's key order (compacted, not re-sorted).
	if !strings.Contains(rec.Body.String(), `"arguments":{"path":"a.go","limit":10}`) {
		t.Errorf("argument order not preserved:\n%s", rec.Body)
	}
}

func TestChatStreamLengthAndFail(t *testing.T) {
	rec := httptest.NewRecorder()
	s := NewChat().NewStream(rec, meta)
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "a"})
	_ = s.Finish(llm.FinishLength, llm.Usage{}, llm.Timings{EvalTokens: 1, PromptEvalTokens: 5, Eval: time.Millisecond})
	lines := parseLines(t, rec.Body.String())
	last := lines[len(lines)-1]
	if last["done"] != true || last["done_reason"] != "length" || last["eval_count"].(float64) != 1 ||
		last["prompt_eval_count"].(float64) != 5 || last["total_duration"].(float64) != 1e6 {
		t.Errorf("final %v", last)
	}
	if _, ok := last["load_duration"]; !ok {
		t.Error("load_duration missing")
	}

	rec = httptest.NewRecorder()
	s = NewChat().NewStream(rec, meta)
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventToolCallStart, Index: 0, Name: "x"})
	if err := s.Fail("api_error", "upstream exploded"); err != nil {
		t.Fatal(err)
	}
	if rec.Body.String() != `{"error":"upstream exploded"}`+"\n" {
		t.Errorf("fail body %q", rec.Body)
	}
}

func TestGenerate(t *testing.T) {
	f := NewGenerate()
	req, err := f.DecodeRequest([]byte(`{"model":"m","prompt":"Why is the sky blue?","system":"Be brief.","images":["AAAA"],
		"options":{"num_ctx":8192,"stop":"\n\n"},"think":"low","raw":true}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []llm.Message{
		{Role: llm.RoleSystem, Content: "Be brief."},
		{Role: llm.RoleUser, Content: "Why is the sky blue?", Images: 1},
	}
	if !reflect.DeepEqual(req.Messages, want) || !req.Stream || req.NumCtx != 8192 ||
		!reflect.DeepEqual(req.Stop, []string{"\n\n"}) || req.ReasoningEffort != "low" || len(req.Warnings) != 2 {
		t.Fatalf("%+v", req)
	}
	req, err = f.DecodeRequest([]byte(`{"model":"m","keep_alive":0}`))
	if err != nil || len(req.Messages) != 0 {
		t.Fatalf("load probe %+v %v", req, err)
	}

	rec := httptest.NewRecorder()
	s := f.NewStream(rec, meta)
	s.(*stream).now = func() time.Time { return fixed }
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventReasoning, Text: "think"})
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "Rayleigh"})
	_ = s.Send(llm.Event{Type: llm.EventToolCallStart, Index: 0, Name: "ignored"})
	_ = s.Send(llm.Event{Type: llm.EventText, Text: " scattering."})
	_ = s.Finish(llm.FinishStop, usage, timings)
	lines := parseLines(t, rec.Body.String())
	wantLines := []string{
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","response":"","thinking":"think","done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","response":"Rayleigh","done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","response":" scattering.","done":false}`,
		`{"model":"qwen3-coder:30b","created_at":"` + fixedTS + `","response":"","done":true,"done_reason":"stop","context":[],` +
			`"total_duration":4100000000,"load_duration":2000000000,"prompt_eval_count":1500,"prompt_eval_duration":800000000,"eval_count":42,"eval_duration":1200000000}`,
	}
	if len(lines) != len(wantLines) {
		t.Fatalf("lines:\n%s", rec.Body)
	}
	for i := range wantLines {
		if got := canon(t, lines[i]); got != llm.CanonicalJSON(wantLines[i]) {
			t.Errorf("line %d:\n got %s\nwant %s", i, got, llm.CanonicalJSON(wantLines[i]))
		}
	}

	rec = httptest.NewRecorder()
	if err := f.WriteResponse(rec, meta, &llm.Response{Content: "Blue.", Reasoning: "r", FinishReason: llm.FinishStop, Usage: usage}, timings); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got["response"] != "Blue." || got["thinking"] != "r" || got["done"] != true || got["done_reason"] != "stop" ||
		got["eval_count"].(float64) != 42 || canon(t, got["context"]) != "[]" {
		t.Errorf("generate response %s", rec.Body)
	}
}

func TestChatWriteResponseAndError(t *testing.T) {
	f := NewChat()
	rec := httptest.NewRecorder()
	resp := &llm.Response{Model: "upstream", Content: "Reading.", Reasoning: "plan", FinishReason: llm.FinishToolCalls,
		ToolCalls: []llm.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"a.go"}`}, {ID: "call_2", Name: "bad", Arguments: `not json`}},
		Usage:     usage}
	if err := f.WriteResponse(rec, api.StreamMeta{}, resp, timings); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	delete(got, "created_at")
	want := `{"model":"upstream","message":{"role":"assistant","content":"Reading.","thinking":"plan","tool_calls":[` +
		`{"id":"call_1","function":{"index":0,"name":"read_file","arguments":{"path":"a.go"}}},` +
		`{"id":"call_2","function":{"index":1,"name":"bad","arguments":{}}}]},` +
		`"done":true,"done_reason":"stop","total_duration":4100000000,"load_duration":2000000000,"prompt_eval_count":1500,` +
		`"prompt_eval_duration":800000000,"eval_count":42,"eval_duration":1200000000}`
	if canon(t, got) != llm.CanonicalJSON(want) {
		t.Errorf("chat response:\n got %s\nwant %s", canon(t, got), llm.CanonicalJSON(want))
	}

	rec = httptest.NewRecorder()
	f.WriteError(rec, 404, "not_found_error", `model "x" not found, try pulling it first`)
	if rec.Code != 404 || strings.TrimSpace(rec.Body.String()) != `{"error":"model \"x\" not found, try pulling it first"}` {
		t.Errorf("error %d %s", rec.Code, rec.Body)
	}
}
