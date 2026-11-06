package openai

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

func ptr[T any](v T) *T { return &v }

func TestDecodeRequest(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *llm.Request
	}{
		{
			name: "harness request with parts, tool round trip and stream_options",
			body: `{
				"model": "qwen3-coder:30b",
				"messages": [
					{"role": "system", "content": "You are a coding agent."},
					{"role": "developer", "content": [{"type": "text", "text": "Prefer small diffs."}]},
					{"role": "user", "content": [
						{"type": "text", "text": "Fix the failing test."},
						{"type": "image_url", "image_url": {"url": "data:image/png;base64,AAAA"}},
						{"type": "text", "text": "See screenshot."}
					]},
					{"role": "assistant", "content": null, "reasoning_content": "Need to read the file first.",
					 "tool_calls": [
						{"id": "call_1", "type": "function", "function": {"name": "read_file", "arguments": "{\"path\": \"main.go\"}"}},
						{"id": "call_2", "type": "function", "function": {"name": "grep", "arguments": "{\"pattern\":\"TODO\",\"glob\":\"*.go\"}"}}
					 ]},
					{"role": "tool", "tool_call_id": "call_1", "name": "read_file", "content": "package main"},
					{"role": "tool", "tool_call_id": "call_2", "content": [{"type": "text", "text": "main.go:3: TODO"}]},
					{"role": "assistant", "content": "Done.", "reasoning": "All good."}
				],
				"tools": [
					{"type": "function", "function": {"name": "read_file", "description": "Read a file",
						"parameters": {"type": "object", "properties": {"path": {"type": "string"}}, "required": ["path"]}}},
					{"type": "function", "function": {"name": "grep"}},
					{"type": "web_search_preview"}
				],
				"tool_choice": {"type": "function", "function": {"name": "read_file"}},
				"max_completion_tokens": 4096,
				"max_tokens": 100,
				"temperature": 0.2,
				"top_p": 0.9,
				"stop": "</done>",
				"stream": true,
				"stream_options": {"include_usage": true},
				"reasoning_effort": "high"
			}`,
			want: &llm.Request{
				Model: "qwen3-coder:30b",
				Messages: []llm.Message{
					{Role: llm.RoleSystem, Content: "You are a coding agent."},
					{Role: llm.RoleSystem, Content: "Prefer small diffs."},
					{Role: llm.RoleUser, Content: "Fix the failing test.\nSee screenshot.", Images: 1},
					{Role: llm.RoleAssistant, Reasoning: "Need to read the file first.", ToolCalls: []llm.ToolCall{
						{ID: "call_1", Name: "read_file", Arguments: `{"path": "main.go"}`},
						{ID: "call_2", Name: "grep", Arguments: `{"pattern":"TODO","glob":"*.go"}`},
					}},
					{Role: llm.RoleTool, ToolCallID: "call_1", Name: "read_file", Content: "package main"},
					{Role: llm.RoleTool, ToolCallID: "call_2", Content: "main.go:3: TODO"},
					{Role: llm.RoleAssistant, Content: "Done.", Reasoning: "All good."},
				},
				Tools: []llm.Tool{
					{Name: "read_file", Description: "Read a file",
						Parameters: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`)},
					{Name: "grep"},
				},
				ToolChoice:      json.RawMessage(`{"type":"function","function":{"name":"read_file"}}`),
				MaxTokens:       4096,
				Temperature:     ptr(0.2),
				TopP:            ptr(0.9),
				Stop:            []string{"</done>"},
				Stream:          true,
				ReasoningEffort: "high",
				IncludeUsage:    true,
				Protocol:        llm.ProtocolOpenAIChat,
				Warnings: []string{
					"dropped image/audio/file content part(s) (text-only simulation)",
					`ignored non-function tool of type "web_search_preview"`,
				},
			},
		},
		{
			name: "max_tokens fallback, stop array, string tool_choice",
			body: `{"model":"m","messages":[{"role":"user","content":"hi"}],
				"max_tokens": 256, "stop": ["a", "", "b"], "tool_choice": "required"}`,
			want: &llm.Request{
				Model:      "m",
				Messages:   []llm.Message{{Role: llm.RoleUser, Content: "hi"}},
				MaxTokens:  256,
				Stop:       []string{"a", "b"},
				ToolChoice: json.RawMessage(`"required"`),
				Protocol:   llm.ProtocolOpenAIChat,
			},
		},
		{
			name: "legacy functions and function_call",
			body: `{"model":"m",
				"messages":[
					{"role":"user","content":"weather?"},
					{"role":"assistant","content":null,"function_call":{"name":"get_weather","arguments":"{\"city\":\"Oslo\"}"}},
					{"role":"function","name":"get_weather","content":"rainy"}
				],
				"functions":[{"name":"get_weather","parameters":{"type":"object"}}],
				"function_call":{"name":"get_weather"}}`,
			want: &llm.Request{
				Model: "m",
				Messages: []llm.Message{
					{Role: llm.RoleUser, Content: "weather?"},
					{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call_legacy_1", Name: "get_weather", Arguments: `{"city":"Oslo"}`}}},
					{Role: llm.RoleTool, Name: "get_weather", ToolCallID: "call_legacy_1", Content: "rainy"},
				},
				Tools:      []llm.Tool{{Name: "get_weather", Parameters: json.RawMessage(`{"type":"object"}`)}},
				ToolChoice: json.RawMessage(`{"function":{"name":"get_weather"},"type":"function"}`),
				Protocol:   llm.ProtocolOpenAIChat,
			},
		},
		{
			name: "arguments sent as an object, null tool_choice",
			body: `{"model":"m","tool_choice":null,"messages":[
				{"role":"assistant","tool_calls":[{"id":"c","type":"function","function":{"name":"f","arguments":{"a": 1}}}]}]}`,
			want: &llm.Request{
				Model:    "m",
				Messages: []llm.Message{{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c", Name: "f", Arguments: `{"a":1}`}}}},
				Protocol: llm.ProtocolOpenAIChat,
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

func TestDecodeRequestErrors(t *testing.T) {
	tests := []struct {
		name, body, wantSub string
	}{
		{"empty body", ``, "empty"},
		{"invalid json", `{"model":`, "invalid JSON"},
		{"missing messages", `{"model":"m"}`, "messages"},
		{"empty messages", `{"model":"m","messages":[]}`, "messages"},
		{"bad role", `{"messages":[{"role":"robot","content":"x"}]}`, "robot"},
		{"missing role", `{"messages":[{"content":"x"}]}`, "role"},
		{"bad content", `{"messages":[{"role":"user","content":42}]}`, "content"},
		{"bad stop", `{"messages":[{"role":"user","content":"x"}],"stop":7}`, "stop"},
		{"tool without name", `{"messages":[{"role":"user","content":"x"}],"tools":[{"type":"function","function":{}}]}`, "tools[0]"},
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

// flushRecorder counts flushes so tests can assert every write is flushed.
type flushRecorder struct {
	*httptest.ResponseRecorder
	flushes int
}

func (f *flushRecorder) Flush() { f.flushes++; f.ResponseRecorder.Flush() }

func newRecorder() *flushRecorder { return &flushRecorder{ResponseRecorder: httptest.NewRecorder()} }

// sseData returns the data payloads of an OpenAI-style SSE body, asserting
// no event names are used.
func sseData(t *testing.T, body string) []string {
	t.Helper()
	if !strings.HasSuffix(body, "\n\n") {
		t.Fatalf("body does not end with a blank line: %q", body)
	}
	var out []string
	for _, frame := range strings.Split(strings.TrimSuffix(body, "\n\n"), "\n\n") {
		if !strings.HasPrefix(frame, "data: ") || strings.Contains(frame, "\n") {
			t.Fatalf("unexpected SSE frame %q", frame)
		}
		out = append(out, strings.TrimPrefix(frame, "data: "))
	}
	return out
}

type testChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index        int                        `json:"index"`
		Delta        map[string]json.RawMessage `json:"delta"`
		FinishReason *string                    `json:"finish_reason"`
	} `json:"choices"`
	Usage   map[string]any `json:"usage"`
	Timings map[string]any `json:"timings"`
}

type testToolDelta struct {
	Index    *int    `json:"index"`
	ID       *string `json:"id"`
	Type     *string `json:"type"`
	Function struct {
		Name      *string `json:"name"`
		Arguments *string `json:"arguments"`
	} `json:"function"`
}

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
	created := time.Unix(1700000000, 0)
	meta := api.StreamMeta{ID: "abc123", Model: "gpt-oss:20b", Created: created, PromptTokens: 900, IncludeUsage: true}
	s := New().NewStream(rec, meta)

	args0 := `{"path":"internal/llm/types.go","limit":200,"opts":{"raw":true,"tags":["a","b"]}}`
	args1 := `{"command":"go test ./... -run 'TestX'","note":"unicode é and \"quotes\""}`

	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "text/event-stream" {
		t.Fatalf("status %d content-type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	send := func(ev llm.Event) {
		t.Helper()
		before, flushes := rec.Body.Len(), rec.flushes
		if err := s.Send(ev); err != nil {
			t.Fatalf("Send(%v): %v", ev.Type, err)
		}
		if rec.Body.Len() != before && rec.flushes == flushes {
			t.Fatalf("Send(%v) wrote %d bytes without flushing", ev.Type, rec.Body.Len()-before)
		}
		if ev.Type != llm.EventToolCallArgs && rec.Body.Len() == before {
			t.Fatalf("Send(%v) wrote nothing", ev.Type)
		}
	}
	send(llm.Event{Type: llm.EventReasoning, Text: "Let me "})
	send(llm.Event{Type: llm.EventReasoning, Text: "think."})
	send(llm.Event{Type: llm.EventText, Text: "Reading"})
	send(llm.Event{Type: llm.EventText, Text: " files."})
	send(llm.Event{Type: llm.EventToolCallStart, Index: 0, ID: "call_up0", Name: "read_file"})
	for _, f := range fragments(args0, 1) {
		send(llm.Event{Type: llm.EventToolCallArgs, Index: 0, Text: f})
	}
	send(llm.Event{Type: llm.EventToolCallStart, Index: 1, Name: "bash"})
	for _, f := range fragments(args1, 3) {
		send(llm.Event{Type: llm.EventToolCallArgs, Index: 1, Text: f})
	}
	usage := llm.Usage{PromptTokens: 900, CompletionTokens: 120, ReasoningTokens: 4, CachedPromptTokens: 800}
	timings := llm.Timings{PromptEval: 250 * time.Millisecond, Eval: 2 * time.Second, PromptEvalTokens: 100, EvalTokens: 120}
	if err := s.Finish(llm.FinishToolCalls, usage, timings); err != nil {
		t.Fatal(err)
	}

	frames := sseData(t, rec.Body.String())
	if frames[len(frames)-1] != "[DONE]" {
		t.Fatalf("last frame %q, want [DONE]", frames[len(frames)-1])
	}
	frames = frames[:len(frames)-1]

	var kinds []string
	args := map[int]string{}
	ids := map[int]string{}
	names := map[int]string{}
	var reasoning, content string
	var finish testChunk
	var usageChunk testChunk
	for i, f := range frames {
		var c testChunk
		if err := json.Unmarshal([]byte(f), &c); err != nil {
			t.Fatalf("frame %d: %v: %s", i, err, f)
		}
		if c.ID != "chatcmpl-abc123" || c.Object != "chat.completion.chunk" || c.Model != "gpt-oss:20b" || c.Created != created.Unix() {
			t.Fatalf("frame %d envelope: %s", i, f)
		}
		if len(c.Choices) == 0 {
			kinds = append(kinds, "usage")
			if !strings.Contains(f, `"choices":[]`) {
				t.Fatalf("usage chunk must carry an empty choices array: %s", f)
			}
			usageChunk = c
			continue
		}
		ch := c.Choices[0]
		d := ch.Delta
		switch {
		case ch.FinishReason != nil:
			kinds = append(kinds, "finish")
			if len(d) != 0 {
				t.Fatalf("finish chunk delta not empty: %s", f)
			}
			finish = c
		case d["role"] != nil:
			kinds = append(kinds, "role")
			if string(d["role"]) != `"assistant"` || string(d["content"]) != `""` || len(d) != 2 {
				t.Fatalf("role chunk: %s", f)
			}
		case d["reasoning_content"] != nil:
			kinds = append(kinds, "reasoning")
			var s string
			_ = json.Unmarshal(d["reasoning_content"], &s)
			reasoning += s
		case d["content"] != nil:
			kinds = append(kinds, "content")
			var s string
			_ = json.Unmarshal(d["content"], &s)
			content += s
		case d["tool_calls"] != nil:
			var tcs []testToolDelta
			if err := json.Unmarshal(d["tool_calls"], &tcs); err != nil || len(tcs) != 1 {
				t.Fatalf("tool_calls delta: %s", f)
			}
			tc := tcs[0]
			if tc.Index == nil || tc.Function.Arguments == nil {
				t.Fatalf("tool delta missing index/arguments: %s", f)
			}
			if tc.ID != nil {
				kinds = append(kinds, "tool_start")
				if tc.Type == nil || *tc.Type != "function" || tc.Function.Name == nil || *tc.Function.Arguments != "" {
					t.Fatalf("tool start shape: %s", f)
				}
				ids[*tc.Index] = *tc.ID
				names[*tc.Index] = *tc.Function.Name
			} else {
				kinds = append(kinds, "tool_args")
				if tc.Type != nil || tc.Function.Name != nil {
					t.Fatalf("args delta must carry only index+arguments: %s", f)
				}
				args[*tc.Index] += *tc.Function.Arguments
			}
		default:
			t.Fatalf("unclassified chunk: %s", f)
		}
	}

	var want []string
	want = append(want, "role", "reasoning", "reasoning", "content", "content", "tool_start")
	for range fragments(args0, 1) {
		want = append(want, "tool_args")
	}
	want = append(want, "tool_start")
	for range fragments(args1, 3) {
		want = append(want, "tool_args")
	}
	want = append(want, "finish", "usage")
	if !reflect.DeepEqual(kinds, want) {
		t.Fatalf("sequence\n got %v\nwant %v", kinds, want)
	}

	if reasoning != "Let me think." || content != "Reading files." {
		t.Fatalf("reasoning %q content %q", reasoning, content)
	}
	if ids[0] != "call_up0" || !strings.HasPrefix(ids[1], "call_") || len(ids[1]) <= len("call_") {
		t.Fatalf("ids %v", ids)
	}
	if names[0] != "read_file" || names[1] != "bash" {
		t.Fatalf("names %v", names)
	}
	for i, orig := range []string{args0, args1} {
		if !json.Valid([]byte(args[i])) {
			t.Fatalf("tool %d args not valid JSON: %q", i, args[i])
		}
		if llm.CanonicalJSON(args[i]) != llm.CanonicalJSON(orig) {
			t.Fatalf("tool %d args %q != %q", i, args[i], orig)
		}
	}

	if *finish.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("finish_reason %q", *finish.Choices[0].FinishReason)
	}
	wantTimings := map[string]any{
		"cache_n": 800.0, "prompt_n": 100.0, "prompt_ms": 250.0, "prompt_per_token_ms": 2.5, "prompt_per_second": 400.0,
		"predicted_n": 120.0, "predicted_ms": 2000.0, "predicted_per_token_ms": 2000.0 / 120, "predicted_per_second": 60.0,
	}
	if !reflect.DeepEqual(finish.Timings, wantTimings) {
		t.Fatalf("timings %v", finish.Timings)
	}
	wantUsage := map[string]any{
		"prompt_tokens": 900.0, "completion_tokens": 120.0, "total_tokens": 1020.0,
		"prompt_tokens_details":     map[string]any{"cached_tokens": 800.0},
		"completion_tokens_details": map[string]any{"reasoning_tokens": 4.0},
	}
	if !reflect.DeepEqual(usageChunk.Usage, wantUsage) {
		t.Fatalf("usage %v", usageChunk.Usage)
	}

	if err := s.Send(llm.Event{Type: llm.EventText, Text: "late"}); err == nil {
		t.Fatal("Send after Finish should fail")
	}
}

func TestStreamFinishVariants(t *testing.T) {
	tests := []struct {
		name         string
		events       []llm.Event
		reason       string
		includeUsage bool
		wantReason   string
	}{
		{"plain stop", []llm.Event{{Type: llm.EventText, Text: "hi"}}, llm.FinishStop, false, "stop"},
		{"tool sent but upstream said stop", []llm.Event{{Type: llm.EventToolCallStart, Name: "f"}}, llm.FinishStop, false, "tool_calls"},
		{"truncated", []llm.Event{{Type: llm.EventText, Text: "hi"}}, llm.FinishLength, true, "length"},
		{"args without start", []llm.Event{{Type: llm.EventToolCallArgs, Name: "f", Text: "{}"}}, "", false, "tool_calls"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorder()
			s := New().NewStream(rec, api.StreamMeta{ID: "x", Model: "m", IncludeUsage: tc.includeUsage})
			if err := s.Start(); err != nil {
				t.Fatal(err)
			}
			for _, ev := range tc.events {
				if err := s.Send(ev); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Finish(tc.reason, llm.Usage{}, llm.Timings{}); err != nil {
				t.Fatal(err)
			}
			frames := sseData(t, rec.Body.String())
			n := len(frames)
			if frames[n-1] != "[DONE]" {
				t.Fatal("missing [DONE]")
			}
			finishIdx := n - 2
			if tc.includeUsage {
				finishIdx = n - 3
				if !strings.Contains(frames[n-2], `"choices":[]`) {
					t.Fatalf("usage chunk: %s", frames[n-2])
				}
			} else if strings.Contains(rec.Body.String(), `"usage"`) {
				t.Fatal("usage emitted without include_usage")
			}
			var c testChunk
			if err := json.Unmarshal([]byte(frames[finishIdx]), &c); err != nil {
				t.Fatal(err)
			}
			if got := *c.Choices[0].FinishReason; got != tc.wantReason {
				t.Fatalf("finish_reason %q want %q", got, tc.wantReason)
			}
			if c.Timings == nil {
				t.Fatal("finish chunk without timings")
			}
		})
	}
}

func TestStreamFail(t *testing.T) {
	rec := newRecorder()
	s := New().NewStream(rec, api.StreamMeta{ID: "x", Model: "m"})
	_ = s.Start()
	_ = s.Send(llm.Event{Type: llm.EventText, Text: "partial"})
	if err := s.Fail("overloaded_error", "upstream overloaded"); err != nil {
		t.Fatal(err)
	}
	frames := sseData(t, rec.Body.String())
	if len(frames) != 4 || frames[3] != "[DONE]" {
		t.Fatalf("frames %v", frames)
	}
	var e struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal([]byte(frames[2]), &e); err != nil {
		t.Fatal(err)
	}
	if e.Error["message"] != "upstream overloaded" || e.Error["type"] != "overloaded_error" {
		t.Fatalf("error chunk %s", frames[2])
	}
	if err := s.Finish("stop", llm.Usage{}, llm.Timings{}); err != nil || strings.Count(rec.Body.String(), "[DONE]") != 1 {
		t.Fatal("Finish after Fail must be a no-op")
	}

	// Send with EventError routes to Fail.
	rec = newRecorder()
	s = New().NewStream(rec, api.StreamMeta{ID: "y", Model: "m"})
	_ = s.Start()
	if err := s.Send(llm.Event{Type: llm.EventError, Err: errors.New("boom")}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rec.Body.String(), `"message":"boom"`) {
		t.Fatalf("body %s", rec.Body.String())
	}
}

func TestWriteResponse(t *testing.T) {
	tests := []struct {
		name string
		resp llm.Response
		want string
	}{
		{
			name: "tool calls with no content",
			resp: llm.Response{
				Reasoning:    "thinking",
				ToolCalls:    []llm.ToolCall{{ID: "call_9", Name: "ls", Arguments: `{"dir":"."}`}, {Name: "pwd"}},
				FinishReason: llm.FinishToolCalls,
				Usage:        llm.Usage{PromptTokens: 10, CompletionTokens: 5, CachedPromptTokens: 2},
			},
			want: `{"role":"assistant","content":null,"reasoning_content":"thinking","tool_calls":[
				{"id":"call_9","type":"function","function":{"name":"ls","arguments":"{\"dir\":\".\"}"}},
				{"id":"GEN","type":"function","function":{"name":"pwd","arguments":"{}"}}]}`,
		},
		{
			name: "plain text",
			resp: llm.Response{Content: "hello", FinishReason: llm.FinishStop},
			want: `{"role":"assistant","content":"hello"}`,
		},
		{
			name: "empty",
			resp: llm.Response{FinishReason: llm.FinishLength},
			want: `{"role":"assistant","content":""}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			meta := api.StreamMeta{ID: "r1", Model: "llama3.1:8b", Created: time.Unix(1700000000, 0)}
			tm := llm.Timings{PromptEval: time.Second, Eval: time.Second, PromptEvalTokens: 50, EvalTokens: 5}
			if err := New().WriteResponse(rec, meta, &tc.resp, tm); err != nil {
				t.Fatal(err)
			}
			if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("status %d", rec.Code)
			}
			var got struct {
				ID      string `json:"id"`
				Object  string `json:"object"`
				Created int64  `json:"created"`
				Model   string `json:"model"`
				Choices []struct {
					Message      map[string]any `json:"message"`
					FinishReason string         `json:"finish_reason"`
				} `json:"choices"`
				Usage   map[string]any `json:"usage"`
				Timings map[string]any `json:"timings"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if got.ID != "chatcmpl-r1" || got.Object != "chat.completion" || got.Created != 1700000000 || got.Model != "llama3.1:8b" {
				t.Fatalf("envelope %s", rec.Body.String())
			}
			msg := got.Choices[0].Message
			if tcs, ok := msg["tool_calls"].([]any); ok && len(tcs) > 1 {
				id := tcs[1].(map[string]any)["id"].(string)
				if !strings.HasPrefix(id, "call_") {
					t.Fatalf("generated id %q", id)
				}
				tcs[1].(map[string]any)["id"] = "GEN"
			}
			var want map[string]any
			if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(msg, want) {
				t.Fatalf("message %v\nwant %v", msg, want)
			}
			wantReason := tc.resp.FinishReason
			if got.Choices[0].FinishReason != wantReason {
				t.Fatalf("finish_reason %q", got.Choices[0].FinishReason)
			}
			if got.Timings["prompt_per_second"] != 50.0 || got.Timings["predicted_n"] != 5.0 {
				t.Fatalf("timings %v", got.Timings)
			}
			if got.Usage["total_tokens"] != float64(tc.resp.Usage.PromptTokens+tc.resp.Usage.CompletionTokens) {
				t.Fatalf("usage %v", got.Usage)
			}
		})
	}
}

func TestWriteError(t *testing.T) {
	tests := []struct {
		errType, wantType string
		wantCode          any
	}{
		{"context_length_exceeded", "invalid_request_error", "context_length_exceeded"},
		{"invalid_request_error", "invalid_request_error", nil},
		{"overloaded_error", "overloaded_error", nil},
		{"", "api_error", nil},
	}
	for _, tc := range tests {
		t.Run(tc.errType, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New().WriteError(rec, 400, tc.errType, "too long")
			var got struct {
				Error map[string]any `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			code, hasCode := got.Error["code"]
			if rec.Code != 400 || got.Error["message"] != "too long" || got.Error["type"] != tc.wantType || !hasCode || code != tc.wantCode {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestWriteModels(t *testing.T) {
	rec := httptest.NewRecorder()
	mod := time.Unix(1710000000, 0)
	WriteModels(rec, []api.ModelInfo{{Name: "gpt-oss:20b", ModifiedAt: mod}, {Name: "qwen3:8b"}})
	var got struct {
		Object string           `json:"object"`
		Data   []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Object != "list" || len(got.Data) != 2 {
		t.Fatalf("%s", rec.Body.String())
	}
	d := got.Data[0]
	if d["id"] != "gpt-oss:20b" || d["object"] != "model" || d["created"] != 1710000000.0 || d["owned_by"] != "cyrlm" {
		t.Fatalf("%v", d)
	}
	if c, _ := got.Data[1]["created"].(float64); c <= 0 {
		t.Fatalf("created for zero ModifiedAt: %v", got.Data[1])
	}

	rec = httptest.NewRecorder()
	WriteModels(rec, nil)
	if !strings.Contains(rec.Body.String(), `"data":[]`) {
		t.Fatalf("empty list: %s", rec.Body.String())
	}
}

func TestName(t *testing.T) {
	if New().Name() != llm.ProtocolOpenAIChat {
		t.Fatal("name")
	}
}

func TestStreamByteSplitUTF8(t *testing.T) {
	text := "héllo 日本語 🎉"
	args := `{"msg":"çà 日本 🎉","n":1}`
	rec := newRecorder()
	s := New().NewStream(rec, api.StreamMeta{ID: "u", Model: "m"})
	_ = s.Start()
	for i := 0; i < len(text); i++ {
		if err := s.Send(llm.Event{Type: llm.EventText, Text: text[i : i+1]}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Send(llm.Event{Type: llm.EventToolCallStart, Index: 0, ID: "c", Name: "echo"})
	for i := 0; i < len(args); i++ {
		_ = s.Send(llm.Event{Type: llm.EventToolCallArgs, Index: 0, Text: args[i : i+1]})
	}
	_ = s.Send(llm.Event{Type: llm.EventReasoning, Text: "\xe6\x97"}) // stream ends mid-rune
	if err := s.Finish(llm.FinishStop, llm.Usage{}, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	var gotText, gotArgs string
	sawReasoning := false
	for _, f := range sseData(t, rec.Body.String()) {
		if f == "[DONE]" {
			continue
		}
		var c testChunk
		if err := json.Unmarshal([]byte(f), &c); err != nil {
			t.Fatal(err)
		}
		d := c.Choices[0].Delta
		if raw, ok := d["content"]; ok && d["role"] == nil {
			var s string
			_ = json.Unmarshal(raw, &s)
			gotText += s
		}
		if raw, ok := d["tool_calls"]; ok {
			var tcs []testToolDelta
			_ = json.Unmarshal(raw, &tcs)
			gotArgs += *tcs[0].Function.Arguments
		}
		if _, ok := d["reasoning_content"]; ok {
			sawReasoning = true
		}
	}
	if gotText != text {
		t.Fatalf("text %q want %q", gotText, text)
	}
	if gotArgs != args || llm.CanonicalJSON(gotArgs) != llm.CanonicalJSON(args) {
		t.Fatalf("args %q want %q", gotArgs, args)
	}
	if !sawReasoning {
		t.Fatal("pending bytes not flushed at Finish")
	}
}

func TestUTF8Carry(t *testing.T) {
	var c utf8Carry
	if got := c.take("ab\xe6\x97"); got != "ab" || c.pending != "\xe6\x97" {
		t.Fatalf("got %q pending %q", got, c.pending)
	}
	if got := c.take("\xa5x"); got != "日x" || c.pending != "" {
		t.Fatalf("got %q pending %q", got, c.pending)
	}
	if got := c.take("\xff"); got != "\xff" { // invalid byte: not held back
		t.Fatalf("got %q", got)
	}
}
