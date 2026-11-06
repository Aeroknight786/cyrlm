package upstream

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

const openRouterStream = `: OPENROUTER PROCESSING

data: {"choices":[{"delta":{"role":"assistant","content":"","reasoning":"Let me check."}}]}

data: {"choices":[{"delta":{"content":"Hello"}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"read_file","arguments":""}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"path\":"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":321,"completion_tokens":17,"completion_tokens_details":{"reasoning_tokens":4}}}

data: [DONE]

`

func TestParseSSE(t *testing.T) {
	out := make(chan llm.Event, 64)
	parseSSE(context.Background(), strings.NewReader(openRouterStream), out, nil, nil, 0)
	close(out)
	var r llm.Response
	var types []string
	for ev := range out {
		r.Apply(ev)
		types = append(types, ev.Type.String())
	}
	if r.Reasoning != "Let me check." || r.Content != "Hello" {
		t.Fatalf("content/reasoning: %+v", r)
	}
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "read_file" || r.ToolCalls[0].Arguments != `{"path":"a.go"}` {
		t.Fatalf("tool calls: %+v", r.ToolCalls)
	}
	// "stop" with tool calls is normalised to tool_calls (llama.cpp quirk).
	if r.FinishReason != llm.FinishToolCalls {
		t.Fatalf("finish %q", r.FinishReason)
	}
	if r.Usage.PromptTokens != 321 || r.Usage.ReasoningTokens != 4 {
		t.Fatalf("usage %+v", r.Usage)
	}
	if types[len(types)-1] != "done" {
		t.Fatalf("last event %v", types)
	}
}

func TestBuildBodyAndErrors(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			w.WriteHeader(401)
			w.Write([]byte(`{"error":{"message":"bad key"}}`))
			return
		}
		json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	c := &OpenAICompat{BaseURL: srv.URL, APIKey: "k", Model: "m", Extra: map[string]any{"provider": map[string]any{"only": []string{"x"}}}}
	req := &llm.Request{Messages: []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "bash"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "out"},
	}, Tools: []llm.Tool{{Name: "bash", Parameters: json.RawMessage(`{"type":"object","x-cyrlm-custom":true}`)}}}
	ch, err := c.Stream(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	for range ch {
	}
	msgs := got["messages"].([]any)
	asst := msgs[1].(map[string]any)
	if asst["content"] != nil || asst["tool_calls"].([]any)[0].(map[string]any)["function"].(map[string]any)["arguments"] != "{}" {
		t.Errorf("assistant tool-call message: %v", asst)
	}
	params := got["tools"].([]any)[0].(map[string]any)["function"].(map[string]any)["parameters"].(map[string]any)
	if _, ok := params["x-cyrlm-custom"]; ok {
		t.Error("internal schema marker must be stripped")
	}
	if got["provider"] == nil || got["stream"] != true {
		t.Errorf("extra/stream missing: %v", got)
	}
	c.APIKey = "wrong"
	_, err = c.Stream(context.Background(), req)
	var he *HTTPError
	if err == nil || !strings.Contains(err.Error(), "bad key") {
		t.Fatalf("expected HTTP error with message, got %v", err)
	}
	_ = he
}

func TestReasoningAndRequireParameters(t *testing.T) {
	c := &OpenAICompat{BaseURL: OpenRouterBaseURL, Model: "m", Extra: map[string]any{"provider": map[string]any{"only": []string{"x"}}}}
	plain := c.BuildBody(&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if _, ok := plain["reasoning"]; ok {
		t.Error("non-reasoning models must not get a reasoning parameter")
	}
	if p := plain["provider"].(map[string]any); p["require_parameters"] != nil {
		t.Error("require_parameters only when tools are sent")
	}
	withTools := c.BuildBody(&llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}, Tools: []llm.Tool{{Name: "bash"}}})
	p := withTools["provider"].(map[string]any)
	if p["require_parameters"] != true || p["only"] == nil {
		t.Errorf("tools must require tool-capable providers and keep pinning: %v", p)
	}
	c.Reasoning = true
	if r := c.BuildBody(&llm.Request{ReasoningEffort: "high"})["reasoning"].(map[string]any); r["effort"] != "high" {
		t.Errorf("reasoning effort not passed: %v", r)
	}
}

func TestStalledStreamEnds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // then stall forever
	}))
	defer srv.Close()
	c := &OpenAICompat{BaseURL: srv.URL, Model: "m", IdleTimeout: 200 * time.Millisecond}
	ch, err := c.Stream(context.Background(), &llm.Request{Messages: []llm.Message{{Role: llm.RoleUser, Content: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	var last llm.Event
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			last = ev
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stalled stream never ended")
	}
	if last.Type != llm.EventError || !strings.Contains(last.Err.Error(), "sent nothing") {
		t.Fatalf("expected a stall error, got %+v", last)
	}
}
