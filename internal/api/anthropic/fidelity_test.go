package anthropic

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

func TestDeferredToolsAndReferences(t *testing.T) {
	req, err := New().DecodeRequest([]byte(`{"messages":[
 {"role":"assistant","content":[{"type":"tool_use","id":"s","name":"ToolSearch","input":{}}]},
 {"role":"user","content":[{"type":"tool_result","tool_use_id":"s","content":[{"type":"text","text":"found"},{"type":"tool_reference","tool_name":"B"},{"type":"tool_reference","tool_name":"A"},{"type":"tool_reference","tool_name":"B"}]}]}],
 "tools":[{"name":"A","defer_loading":true},{"name":"ToolSearch"},{"name":"DeferredToolPlaceholder","defer_loading":true},{"name":"B","defer_loading":true}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range req.Tools {
		names = append(names, tool.Name)
	}
	if strings.Join(names, ",") != "ToolSearch,B,A" {
		t.Fatalf("tools: %v", names)
	}
	want := "found\nTool loaded: B (its schema is now in your tool list; call it directly)."
	if !strings.HasPrefix(req.Messages[1].Content, want) || len(req.Warnings) != 0 {
		t.Fatalf("result: %+v warnings %v", req.Messages[1], req.Warnings)
	}
}

func TestOutputSchema(t *testing.T) {
	req, err := New().DecodeRequest([]byte(`{"messages":[{"role":"user","content":"title"}],"output_config":{"effort":"high","format":{"type":"json_schema","schema":{"type":"object","properties":{"title":{"type":"string"}}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var format map[string]any
	if err := json.Unmarshal(req.ResponseFormat, &format); err != nil {
		t.Fatal(err)
	}
	if format["type"] != "json_schema" || format["json_schema"].(map[string]any)["name"] != "output" || req.ReasoningEffort != "" {
		t.Fatalf("request: %+v", req)
	}
}

func TestMessageStartCacheSplit(t *testing.T) {
	rec := newRecorder()
	s := New().NewStream(rec, api.StreamMeta{PromptTokens: 1200, CachedPromptTokens: 1000})
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	events := parseSSE(t, rec.Body.String())
	usage := obj(obj(events[0].data["message"])["usage"])
	if usage["input_tokens"] != 200.0 || usage["cache_read_input_tokens"] != 1000.0 {
		t.Fatalf("usage %v", usage)
	}
	if err := s.Finish(llm.FinishStop, llm.Usage{}, llm.Timings{}); err != nil {
		t.Fatal(err)
	}
}
