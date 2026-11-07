package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/catalog"
	runtime "github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// fakeUpstream is an OpenAI-compatible endpoint that answers with text, or a
// tool call when tools are offered and the last message is from the user.
type fakeUpstream struct {
	mu     sync.Mutex
	bodies []map[string]any
}

func (f *fakeUpstream) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	f.bodies = append(f.bodies, body)
	f.mu.Unlock()
	msgs, _ := body["messages"].([]any)
	last, _ := msgs[len(msgs)-1].(map[string]any)
	_, hasTools := body["tools"]
	w.Header().Set("Content-Type", "text/event-stream")
	fl := w.(http.Flusher)
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		fl.Flush()
	}
	send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"role": "assistant"}}}})
	if hasTools && last["role"] == "user" {
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "call_1", "function": map[string]any{"name": "bash", "arguments": ""}}}}}}})
		for _, frag := range []string{`{"cmd":`, `"ls -la"}`} {
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "function": map[string]any{"arguments": frag}}}}}}})
		}
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}})
	} else {
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"reasoning": "Thinking briefly."}}}})
		for _, w := range strings.Fields("Here is the answer you asked for, with a few words.") {
			send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": w + " "}}}})
		}
		send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}})
	}
	send(map[string]any{"choices": []any{}, "usage": map[string]any{"prompt_tokens": 1200, "completion_tokens": 14}})
	fmt.Fprint(w, "data: [DONE]\n\n")
	fl.Flush()
}

// fastTiming gives small but measurable delays: 0.3 s cold load, ~0.1 ms
// prefill per token, 5 ms per generated token.
func fastTiming(m catalog.Model) *predict.Timing {
	return &predict.Timing{Model: m, WeightsBytesPerTok: 5e8, KVElem: 2, EffBandwidth: 1e11, MatmulSecPerTok: 0.0001,
		AttnFLOPSEff: 1e18, DecodeMult: 1, PrefillMult: 1, LoadColdS: 0.3, LoadWarmS: 0.05}
}

func newTestServer(t *testing.T, profile string, ctxTokens int) (*httptest.Server, *fakeUpstream) {
	t.Helper()
	fu := &fakeUpstream{}
	ups := httptest.NewServer(fu)
	t.Cleanup(ups.Close)
	p, _ := runtime.Get(profile)
	m, _ := catalog.Lookup("qwen3-coder:30b")
	s := p.Resolve(runtime.ResolveInput{GPUMemGiB: 24, NativeCtx: m.NativeCtx, CtxOverride: ctxTokens})
	dev := sim.NewDevice(sim.Config{RuntimeName: p.Name, RuntimeID: p.ID, Settings: s, Timing: fastTiming(m), Model: m,
		Render: tok.RenderOptions{StripPastReasoning: true}})
	srv := New(Config{
		Device: dev, Upstream: &upstream.OpenAICompat{BaseURL: ups.URL, Model: "fake/model"},
		Model: api.ModelInfo{Name: m.OllamaTag, Family: m.Family, ParameterSize: m.ParamLabel(), Quantization: "Q4_K_M", ContextLength: s.ContextTokens, Capabilities: []string{"completion", "tools"}},
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, fu
}

func post(t *testing.T, url, body string) (*http.Response, time.Duration, []string) {
	t.Helper()
	start := time.Now()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	var first time.Duration
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		l := sc.Text()
		if l == "" {
			continue
		}
		if first == 0 && (nonEmptyContent.MatchString(l) || strings.Contains(l, "text_delta") || strings.Contains(l, "output_text.delta") || strings.Contains(l, "thinking_delta")) {
			first = time.Since(start)
		}
		lines = append(lines, l)
	}
	resp.Body.Close()
	return resp, first, lines
}

var nonEmptyContent = regexp.MustCompile(`"(content|reasoning_content)":"[^"]`)

var sys = strings.Repeat("You are a coding agent. Be careful and precise. ", 150)

func TestOpenAIChatStreamPaced(t *testing.T) {
	ts, _ := newTestServer(t, "llamacpp", 32768)
	body := fmt.Sprintf(`{"model":"qwen3-coder:30b","stream":true,"stream_options":{"include_usage":true},"messages":[{"role":"system","content":%q},{"role":"user","content":"hello"}]}`, sys)
	resp, first, lines := post(t, ts.URL+"/v1/chat/completions", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if first < 300*time.Millisecond {
		t.Fatalf("first token after %v: the 0.3 s simulated cold load must delay it", first)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"Here", "answer", `"reasoning_content"`, "[DONE]", `"usage"`} {
		if !strings.Contains(all, want) {
			t.Errorf("stream missing %s", want)
		}
	}
	// Pieces are re-chunked to roughly one token per event.
	if n := strings.Count(all, `"content":`); n < 10 {
		t.Errorf("expected per-token chunks, got %d content chunks", n)
	}
}

func TestAnthropicToolRoundTrip(t *testing.T) {
	ts, fu := newTestServer(t, "ollama", 65536)
	tools := `[{"name":"bash","description":"run","input_schema":{"type":"object","properties":{"cmd":{"type":"string"}}}}]`
	body := fmt.Sprintf(`{"model":"claude-x","max_tokens":1024,"stream":true,"system":[{"type":"text","text":%q}],"tools":%s,"messages":[{"role":"user","content":"list files"}]}`, sys, tools)
	resp, _, lines := post(t, ts.URL+"/v1/messages", body)
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	all := strings.Join(lines, "\n")
	for _, want := range []string{"event: message_start", `"type":"tool_use"`, "input_json_delta", `"stop_reason":"tool_use"`, "event: message_stop"} {
		if !strings.Contains(all, want) {
			t.Errorf("anthropic stream missing %s\n%s", want, all)
		}
	}
	// Second turn: tool result → text; cache should be reused.
	body2 := fmt.Sprintf(`{"model":"claude-x","max_tokens":1024,"stream":true,"system":[{"type":"text","text":%q}],"tools":%s,"messages":[
		{"role":"user","content":"list files"},
		{"role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"bash","input":{"cmd":"ls -la"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"call_1","content":"main.go\ngo.mod"}]}]}`, sys, tools)
	_, _, lines2 := post(t, ts.URL+"/v1/messages", body2)
	if !strings.Contains(strings.Join(lines2, "\n"), "text_delta") {
		t.Fatal("second turn should produce text")
	}
	// The upstream received the tool result as an OpenAI tool message.
	fu.mu.Lock()
	b := fu.bodies[len(fu.bodies)-1]
	fu.mu.Unlock()
	msgs := b["messages"].([]any)
	lastMsg := msgs[len(msgs)-1].(map[string]any)
	if lastMsg["role"] != "tool" || lastMsg["tool_call_id"] != "call_1" {
		t.Fatalf("upstream last message = %v", lastMsg)
	}
}

func TestResponsesAndOllamaProtocols(t *testing.T) {
	ts, _ := newTestServer(t, "ollama", 65536)
	body := fmt.Sprintf(`{"model":"qwen3-coder:30b","stream":true,"instructions":%q,"input":[{"role":"user","content":[{"type":"input_text","text":"hi"}]}]}`, sys)
	_, _, lines := post(t, ts.URL+"/v1/responses", body)
	all := strings.Join(lines, "\n")
	for _, want := range []string{"response.created", "response.output_text.delta", "response.completed"} {
		if !strings.Contains(all, want) {
			t.Errorf("responses stream missing %s", want)
		}
	}
	body = fmt.Sprintf(`{"model":"qwen3-coder:30b","messages":[{"role":"system","content":%q},{"role":"user","content":"hi"}]}`, sys)
	_, _, lines = post(t, ts.URL+"/api/chat", body)
	last := lines[len(lines)-1]
	var done map[string]any
	if err := json.Unmarshal([]byte(last), &done); err != nil || done["done"] != true {
		t.Fatalf("ollama final line %q", last)
	}
	if done["eval_count"] == nil || done["prompt_eval_duration"].(float64) <= 0 {
		t.Fatalf("ollama final line should carry simulated timings: %v", done)
	}
}

func TestContextErrorAndTruncation(t *testing.T) {
	big := strings.Repeat("token ", 6000)
	// llama.cpp profile with 2k context rejects.
	ts, _ := newTestServer(t, "llamacpp", 2048)
	body := fmt.Sprintf(`{"model":"m","stream":true,"messages":[{"role":"user","content":%q}]}`, big)
	resp, _, lines := post(t, ts.URL+"/v1/chat/completions", body)
	if resp.StatusCode != 400 || !strings.Contains(strings.Join(lines, ""), "context") {
		t.Fatalf("expected 400 context error, got %d %v", resp.StatusCode, lines)
	}
	// Ollama profile truncates: the upstream gets a shorter prompt.
	ts2, fu := newTestServer(t, "ollama", 2048)
	body = fmt.Sprintf(`{"model":"m","stream":true,"messages":[{"role":"system","content":"be brief"},{"role":"user","content":%q},{"role":"assistant","content":"ok"},{"role":"user","content":"summarize"}]}`, big)
	resp, _, _ = post(t, ts2.URL+"/v1/chat/completions", body)
	if resp.StatusCode != 200 {
		t.Fatalf("ollama profile should truncate, got %d", resp.StatusCode)
	}
	fu.mu.Lock()
	msgs := fu.bodies[0]["messages"].([]any)
	fu.mu.Unlock()
	if len(msgs) >= 4 {
		t.Fatalf("upstream should receive the truncated conversation, got %d messages", len(msgs))
	}
}

func TestDiscoveryAndProtections(t *testing.T) {
	ts, _ := newTestServer(t, "ollama", 65536)
	for _, path := range []string{"/", "/api/version", "/api/tags", "/v1/models", "/api/ps", "/trialm/stats"} {
		r, err := http.Get(ts.URL + path)
		if err != nil || r.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, err, r)
		}
		r.Body.Close()
	}
	r, _ := http.Post(ts.URL+"/api/show", "application/json", strings.NewReader(`{"model":"qwen3-coder:30b"}`))
	b, _ := io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "context_length") || !strings.Contains(string(b), "tools") {
		t.Fatalf("/api/show = %s", b)
	}
	r, _ = http.Post(ts.URL+"/v1/messages/count_tokens", "application/json", strings.NewReader(`{"model":"x","messages":[{"role":"user","content":"hello world"}]}`))
	b, _ = io.ReadAll(r.Body)
	r.Body.Close()
	if !strings.Contains(string(b), "input_tokens") {
		t.Fatalf("count_tokens = %s", b)
	}
	// Browser origin rejected; foreign Host rejected (DNS rebinding).
	req, _ := http.NewRequest("GET", ts.URL+"/api/tags", nil)
	req.Header.Set("Origin", "https://evil.example")
	r, _ = http.DefaultClient.Do(req)
	if r.StatusCode != 403 {
		t.Fatalf("foreign origin should be 403, got %d", r.StatusCode)
	}
	req, _ = http.NewRequest("GET", ts.URL+"/api/tags", nil)
	req.Host = "attacker.example:11434"
	r, _ = http.DefaultClient.Do(req)
	if r.StatusCode != 403 {
		t.Fatalf("foreign host should be 403, got %d", r.StatusCode)
	}
}

func TestNonStreamingHoldsUntilSimulatedEnd(t *testing.T) {
	ts, _ := newTestServer(t, "llamacpp", 32768)
	body := fmt.Sprintf(`{"model":"m","stream":false,"messages":[{"role":"system","content":%q},{"role":"user","content":"hi"}]}`, sys)
	start := time.Now()
	r, err := http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	json.NewDecoder(r.Body).Decode(&out)
	r.Body.Close()
	if time.Since(start) < 300*time.Millisecond {
		t.Fatal("non-streaming response must wait for the simulated load + generation")
	}
	if out["choices"] == nil {
		t.Fatalf("bad response %v", out)
	}
}
