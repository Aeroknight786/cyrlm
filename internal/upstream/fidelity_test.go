package upstream

import (
	"encoding/json"
	"testing"

	"github.com/aeroknight786/trialm/internal/api/openai"
)

func TestChatReasoningRoundTrip(t *testing.T) {
	req, err := openai.New().DecodeRequest([]byte(`{"messages":[{"role":"user","content":"run"},{"role":"assistant","content":"","reasoning_content":"I should call the tool","tool_calls":[{"id":"c","type":"function","function":{"name":"exec","arguments":"{}"}}]},{"role":"tool","tool_call_id":"c","content":"ok"}],"response_format":{"type":"json_schema","json_schema":{"name":"output","schema":{"type":"object"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ url, key string }{{"http://localhost/v1", "reasoning_content"}, {OpenRouterBaseURL, "reasoning"}} {
		body := (&OpenAICompat{BaseURL: tc.url}).BuildBody(req)
		msg := body["messages"].([]map[string]any)[1]
		if msg[tc.key] != "I should call the tool" {
			t.Fatalf("%s: %v", tc.url, msg)
		}
		if string(body["response_format"].(json.RawMessage)) != string(req.ResponseFormat) {
			t.Fatalf("format: %v", body)
		}
	}
}
