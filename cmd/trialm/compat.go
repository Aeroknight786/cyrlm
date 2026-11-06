package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// HarnessCheck is the result of a real tool-call round trip in the
// harness's own request shape. It is separate from the speed measurement:
// a runtime can be fast and still reject the harness's requests.
type HarnessCheck struct {
	Harness  string `json:"harness"`
	Path     string `json:"path"` // what was exercised, e.g. "Responses via trialm bridge → Chat Completions"
	Greeting string `json:"greeting"`
	ToolCall string `json:"tool_call"`
	FollowUp string `json:"follow_up"`
	Passed   bool   `json:"passed"`
	Error    string `json:"error,omitempty"`
}

const (
	checkPass = "pass"
	checkFail = "fail"
	checkSkip = "not run"
)

// harnessCheck sends requests shaped like the harness's to the runtime at
// endpoint. Codex (and any harness that needs the bridge) is checked
// through an in-process trialm bridge with Codex's real request layout:
// instructions plus several developer messages, a namespaced function tool
// and a hosted tool. Other harnesses get a tool round trip over Chat
// Completions, which is what the runtime's template and tool parser must
// handle.
func harnessCheck(ctx context.Context, endpoint, model, apiKey string, h harness.Harness, apis []string) HarnessCheck {
	hc := HarnessCheck{Harness: h.ID, Greeting: checkSkip, ToolCall: checkSkip, FollowUp: checkSkip}
	if apiKey == "" {
		apiKey = "local"
	}
	base := strings.TrimRight(endpoint, "/")
	if h.NeedsBridge(apis) {
		client := &upstream.OpenAICompat{BaseURL: base + "/v1", APIKey: apiKey, Model: model, IdleTimeout: 10 * time.Minute}
		srv := server.New(server.Config{Bridge: true, Upstream: client, Model: api.ModelInfo{Name: model}})
		port, err := srv.Listen("127.0.0.1", 0, false)
		if err != nil {
			hc.Error = err.Error()
			return hc
		}
		go func() { _ = srv.Serve() }()
		defer srv.Shutdown(context.Background())
		hc.Path = "Responses API via trialm bridge → " + base + "/v1/chat/completions"
		runResponsesCheck(ctx, &hc, fmt.Sprintf("http://127.0.0.1:%d/v1/responses", port), model)
	} else {
		hc.Path = "Chat Completions at " + base + "/v1/chat/completions"
		runChatCheck(ctx, &hc, base+"/v1/chat/completions", model, apiKey)
	}
	hc.Passed = hc.Greeting == checkPass && hc.ToolCall == checkPass && hc.FollowUp == checkPass
	return hc
}

const checkPrompt = "This is an automated check. Call the record_result tool exactly once with value \"ok\". Do not answer in text."

func runResponsesCheck(ctx context.Context, hc *HarnessCheck, url, model string) {
	instr := "You are a coding agent. Be brief."
	dev := []any{
		map[string]any{"type": "message", "role": "developer", "content": "Sandbox: workspace-write. Approvals: never."},
		map[string]any{"type": "message", "role": "developer", "content": "Use tools when asked."},
	}
	greet := map[string]any{"model": model, "instructions": instr, "stream": false, "store": false, "max_output_tokens": 2048,
		"input": append(dev, map[string]any{"type": "message", "role": "user", "content": "Reply with the single word OK."})}
	out, err := postJSON(ctx, url, "", greet)
	if err != nil {
		hc.Greeting, hc.Error = checkFail, "greeting: "+err.Error()
		return
	}
	if responsesText(out) == "" {
		hc.Greeting, hc.Error = checkFail, "greeting: the model returned no text"
		return
	}
	hc.Greeting = checkPass

	tools := []any{
		map[string]any{"type": "namespace", "name": "trialm_check", "tools": []any{map[string]any{
			"type": "function", "name": "record_result", "description": "Record the check result.",
			"parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}},
		}}},
		map[string]any{"type": "web_search"},
	}
	input := append(dev, map[string]any{"type": "message", "role": "user", "content": checkPrompt})
	out, err = postJSON(ctx, url, "", map[string]any{"model": model, "instructions": instr, "stream": false, "store": false, "max_output_tokens": 2048, "tools": tools, "input": input})
	if err != nil {
		hc.ToolCall, hc.Error = checkFail, "tool call: "+err.Error()
		return
	}
	var call map[string]any
	for _, it := range items(out) {
		if it["type"] == "function_call" && it["name"] == "record_result" {
			call = it
		}
	}
	if call == nil {
		hc.ToolCall, hc.Error = checkFail, "tool call: the model did not call record_result (text: "+clip(responsesText(out), 120)+")"
		return
	}
	if call["namespace"] != "trialm_check" {
		hc.ToolCall, hc.Error = checkFail, fmt.Sprintf("tool call: namespace %v, want trialm_check", call["namespace"])
		return
	}
	hc.ToolCall = checkPass

	input = append(input,
		map[string]any{"type": "function_call", "call_id": call["call_id"], "name": "record_result", "namespace": "trialm_check", "arguments": call["arguments"]},
		map[string]any{"type": "function_call_output", "call_id": call["call_id"], "output": "recorded"},
	)
	out, err = postJSON(ctx, url, "", map[string]any{"model": model, "instructions": instr, "stream": false, "store": false, "max_output_tokens": 2048, "tools": tools, "input": input})
	if err != nil {
		hc.FollowUp, hc.Error = checkFail, "follow-up: "+err.Error()
		return
	}
	if responsesText(out) == "" && len(items(out)) == 0 {
		hc.FollowUp, hc.Error = checkFail, "follow-up: empty response"
		return
	}
	hc.FollowUp = checkPass
}

func runChatCheck(ctx context.Context, hc *HarnessCheck, url, model, key string) {
	sys := map[string]any{"role": "system", "content": "You are a coding agent. Be brief."}
	out, err := postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048,
		"messages": []any{sys, map[string]any{"role": "user", "content": "Reply with the single word OK."}}})
	if err != nil {
		hc.Greeting, hc.Error = checkFail, "greeting: "+err.Error()
		return
	}
	if chatMessage(out)["content"] == nil {
		hc.Greeting, hc.Error = checkFail, "greeting: no message"
		return
	}
	hc.Greeting = checkPass
	tools := []any{map[string]any{"type": "function", "function": map[string]any{
		"name": "record_result", "description": "Record the check result.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}},
	}}}
	msgs := []any{sys, map[string]any{"role": "user", "content": checkPrompt}}
	out, err = postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048, "tools": tools, "messages": msgs})
	if err != nil {
		hc.ToolCall, hc.Error = checkFail, "tool call: "+err.Error()
		return
	}
	msg := chatMessage(out)
	calls, _ := msg["tool_calls"].([]any)
	if len(calls) == 0 {
		hc.ToolCall, hc.Error = checkFail, fmt.Sprintf("tool call: the model did not call record_result (text: %s)", clip(fmt.Sprint(msg["content"]), 120))
		return
	}
	hc.ToolCall = checkPass
	c0, _ := calls[0].(map[string]any)
	msgs = append(msgs, map[string]any{"role": "assistant", "content": nil, "tool_calls": calls},
		map[string]any{"role": "tool", "tool_call_id": c0["id"], "content": "recorded"})
	if _, err = postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048, "tools": tools, "messages": msgs}); err != nil {
		hc.FollowUp, hc.Error = checkFail, "follow-up: "+err.Error()
		return
	}
	hc.FollowUp = checkPass
}

func postJSON(ctx context.Context, url, key string, body any) (map[string]any, error) {
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("bad JSON: %v", err)
	}
	return out, nil
}

func items(r map[string]any) []map[string]any {
	var out []map[string]any
	arr, _ := r["output"].([]any)
	for _, a := range arr {
		if m, ok := a.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func responsesText(r map[string]any) string {
	var b strings.Builder
	for _, it := range items(r) {
		if it["type"] != "message" {
			continue
		}
		parts, _ := it["content"].([]any)
		for _, p := range parts {
			if m, ok := p.(map[string]any); ok {
				if t, ok := m["text"].(string); ok {
					b.WriteString(t)
				}
			}
		}
	}
	return strings.TrimSpace(b.String())
}

func chatMessage(r map[string]any) map[string]any {
	ch, _ := r["choices"].([]any)
	if len(ch) == 0 {
		return map[string]any{}
	}
	c, _ := ch[0].(map[string]any)
	m, _ := c["message"].(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
