package responses

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

func TestNamespaceToolsRoundTrip(t *testing.T) {
	f := New().(*Frontend)
	req, err := f.DecodeRequest([]byte(`{"model":"m","instructions":"i","input":[
		{"type":"message","role":"developer","content":"d"},
		{"type":"message","role":"user","content":"u"},
		{"type":"function_call","call_id":"c0","name":"wait_agent","namespace":"multi_agent_v1","arguments":"{}"},
		{"type":"function_call_output","call_id":"c0","output":"done"}],
		"tools":[{"type":"function","name":"exec_command","parameters":{"type":"object"}},
		{"type":"namespace","name":"multi_agent_v1","tools":[{"type":"function","name":"wait_agent","parameters":{"type":"object"}}]},
		{"type":"web_search"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tl := range req.Tools {
		names = append(names, tl.Name)
	}
	if strings.Join(names, ",") != "exec_command,multi_agent_v1__wait_agent" {
		t.Fatalf("tools %v", names)
	}
	if len(req.Warnings) != 1 || !strings.Contains(req.Warnings[0], "web_search") {
		t.Fatalf("web_search must be reported: %v", req.Warnings)
	}
	var hist string
	for _, m := range req.Messages {
		for _, c := range m.ToolCalls {
			hist = c.Name
		}
	}
	if hist != "multi_agent_v1__wait_agent" {
		t.Fatalf("history call name %q", hist)
	}
	// Output: the call goes back with its namespace.
	w := httptest.NewRecorder()
	resp := &llm.Response{FinishReason: llm.FinishToolCalls, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "multi_agent_v1__wait_agent", Arguments: "{}"}}}
	if err := f.WriteResponse(w, api.StreamMeta{ID: "x", Model: "m"}, resp, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
	var out struct {
		Output []map[string]any `json:"output"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	got := out.Output[len(out.Output)-1]
	if got["name"] != "wait_agent" || got["namespace"] != "multi_agent_v1" {
		t.Fatalf("output item %v", got)
	}
}
