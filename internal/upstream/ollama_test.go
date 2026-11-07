package upstream

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/llm"
)

func TestOllamaNativeForwarding(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			t.Errorf("path %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		opts := body["options"].(map[string]any)
		if opts["num_ctx"] != 8192.0 || opts["num_predict"] != 256.0 {
			t.Errorf("options %v", opts)
		}
		format := body["format"].(map[string]any)
		if format["type"] != "object" {
			t.Errorf("format %v", format)
		}
		msg := body["messages"].([]any)[1].(map[string]any)
		if msg["thinking"] != "plan" {
			t.Errorf("history %v", msg)
		}
		fn := msg["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)
		if _, ok := fn["arguments"].(map[string]any); !ok {
			t.Errorf("arguments %v", fn)
		}
		fmt.Fprintln(w, `{"message":{"thinking":"thought","content":"hi","tool_calls":[{"function":{"name":"exec","arguments":{"cmd":"ls"}}}]}}`)
		fmt.Fprintln(w, `{"done":true,"done_reason":"stop","prompt_eval_count":123,"prompt_eval_cached_count":100,"eval_count":7}`)
	}))
	defer ts.Close()
	c := &OpenAICompat{BaseURL: ts.URL + "/v1", Ollama: true, Model: "m"}
	req := &llm.Request{NumCtx: 8192, MaxTokens: 256, ResponseFormat: json.RawMessage(`{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object"}}}`), Messages: []llm.Message{{Role: llm.RoleUser, Content: "run"}, {Role: llm.RoleAssistant, Reasoning: "plan", ToolCalls: []llm.ToolCall{{Name: "exec", Arguments: `{"cmd":"ls"}`}}}}}
	events, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	var res llm.Response
	for ev := range events {
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
		res.Apply(ev)
	}
	if res.Content != "hi" || res.Reasoning != "thought" || res.FinishReason != llm.FinishToolCalls || res.Usage.PromptTokens != 123 || res.Usage.CachedPromptTokens != 100 || len(res.ToolCalls) != 1 || !strings.Contains(res.ToolCalls[0].Arguments, "ls") {
		t.Fatalf("response %+v", res)
	}
}

func TestOllamaStreamErrors(t *testing.T) {
	for _, reply := range []string{`{"error":"bad template"}`, `{`, ""} {
		t.Run(reply, func(t *testing.T) {
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, reply) }))
			defer ts.Close()
			c := &OpenAICompat{BaseURL: ts.URL + "/v1", Ollama: true}
			events, err := c.Stream(context.Background(), &llm.Request{})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			for ev := range events {
				failed = failed || ev.Type == llm.EventError
			}
			if !failed {
				t.Fatal("missing stream error")
			}
		})
	}
}
