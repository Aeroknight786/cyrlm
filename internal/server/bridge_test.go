package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/upstream"
)

func newBridge(t *testing.T, h http.Handler) *httptest.Server {
	up := httptest.NewServer(h)
	t.Cleanup(up.Close)
	s := New(Config{Bridge: true, Upstream: &upstream.OpenAICompat{BaseURL: up.URL + "/v1", APIKey: "x", Model: "local"}, Model: api.ModelInfo{Name: "local"}})
	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)
	return ts
}

func TestBridgeResponsesToolCall(t *testing.T) {
	fu := &fakeUpstream{}
	ts := newBridge(t, fu)
	body := `{"model":"m","stream":true,"instructions":"i","input":[{"role":"developer","content":"d"},{"role":"user","content":"list files"}],
		"tools":[{"type":"function","name":"bash","parameters":{"type":"object"}}]}`
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := string(b)
	for _, want := range []string{"response.created", `"type":"function_call"`, `"name":"bash"`, "response.function_call_arguments.done", "response.completed"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in:\n%s", want, out)
		}
	}
	// The runtime got one leading system message (instructions + developer).
	msgs := fu.bodies[0]["messages"].([]any)
	roles := []string{}
	for _, m := range msgs {
		roles = append(roles, m.(map[string]any)["role"].(string))
	}
	if strings.Join(roles, ",") != "system,user" {
		t.Fatalf("roles %v", roles)
	}
	if fu.bodies[0]["model"] != "local" {
		t.Fatalf("model %v", fu.bodies[0]["model"])
	}
}

func TestBridgeDeterministicErrorIsNotRetryable(t *testing.T) {
	ts := newBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"Jinja Exception: System message must be at the beginning."}}`, 500)
	}))
	resp, err := http.Post(ts.URL+"/v1/responses", "application/json", strings.NewReader(`{"model":"m","input":"hi"}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 400 || !strings.Contains(string(b), "Jinja Exception") {
		t.Fatalf("got %d %s; want 400 with the runtime's message (5xx makes Codex retry and show 'high demand')", resp.StatusCode, b)
	}
	// A loading runtime stays retryable.
	ts = newBridge(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "Loading model", 503) }))
	resp, _ = http.Post(ts.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	resp.Body.Close()
	if resp.StatusCode != 503 {
		t.Fatalf("loading: %d", resp.StatusCode)
	}
}
