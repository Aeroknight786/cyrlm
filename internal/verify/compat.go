package verify

import (
	"bufio"
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

// HarnessCheck is the result of a real tool or edit round trip in the
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
// and a hosted tool. Chat harnesses exercise streaming tool deltas and
// usage; Aider exercises whole-format edits and a trailing system reminder.
func HarnessCheckRun(ctx context.Context, endpoint, model, apiKey string, h harness.Harness, apis []string) HarnessCheck {
	hc := HarnessCheck{Harness: h.ID, Greeting: checkSkip, ToolCall: checkSkip, FollowUp: checkSkip}
	if apiKey == "" {
		apiKey = "local"
	}
	base := strings.TrimRight(endpoint, "/")
	if h.ID == "aider" {
		ollama := h.ProtocolFor(apis) == "ollama"
		path := "/v1/chat/completions"
		if ollama {
			path = "/api/chat"
		}
		hc.Path = "Aider whole-format edit at " + base + path
		runAiderCheck(ctx, &hc, base+path, model, apiKey, ollama)
	} else if h.NeedsBridge(apis) {
		client := &upstream.OpenAICompat{BaseURL: base + "/v1", APIKey: apiKey, Model: model, IdleTimeout: 10 * time.Minute}
		srv := server.New(server.Config{Bridge: true, Upstream: client, Model: api.ModelInfo{Name: model}})
		port, err := srv.Listen("127.0.0.1", 0, false)
		if err != nil {
			hc.Error = err.Error()
			return hc
		}
		go func() { _ = srv.Serve() }()
		defer srv.Shutdown(context.Background())
		if h.ID == "codex" {
			hc.Path = "Responses API via trialm bridge → " + base + "/v1/chat/completions"
			runResponsesCheck(ctx, &hc, fmt.Sprintf("http://127.0.0.1:%d/v1/responses", port), model)
		} else {
			hc.Path = "Chat Completions via trialm bridge → " + base + "/v1/chat/completions"
			runChatCheck(ctx, &hc, fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", port), model, "")
		}
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
	out, err := postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048, "stream": true, "stream_options": map[string]any{"include_usage": true},
		"messages": []any{sys, map[string]any{"role": "user", "content": "Reply with the single word OK."}}})
	if err != nil {
		hc.Greeting, hc.Error = checkFail, "greeting: "+err.Error()
		return
	}
	if text, _ := chatMessage(out)["content"].(string); strings.TrimSpace(text) == "" {
		hc.Greeting, hc.Error = checkFail, "greeting: no message"
		return
	}
	hc.Greeting = checkPass
	tools := []any{map[string]any{"type": "function", "function": map[string]any{
		"name": "record_result", "description": "Record the check result.",
		"parameters": map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}, "required": []string{"value"}},
	}}}
	msgs := []any{sys, map[string]any{"role": "user", "content": checkPrompt}}
	out, err = postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048, "stream": true, "stream_options": map[string]any{"include_usage": true}, "tools": tools, "tool_choice": "auto", "messages": msgs})
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
	c0, _ := calls[0].(map[string]any)
	fn, _ := c0["function"].(map[string]any)
	args, _ := fn["arguments"].(string)
	if len(calls) != 1 || fn["name"] != "record_result" || !json.Valid([]byte(args)) || c0["id"] == "" {
		hc.ToolCall, hc.Error = checkFail, "tool call: incomplete record_result deltas"
		return
	}
	hc.ToolCall = checkPass
	assistant := map[string]any{"role": "assistant", "content": "", "tool_calls": calls}
	if reasoning, ok := msg["reasoning_content"]; ok {
		assistant["reasoning_content"] = reasoning
	}
	msgs = append(msgs, assistant,
		map[string]any{"role": "tool", "tool_call_id": c0["id"], "content": "recorded"})
	if out, err = postJSON(ctx, url, key, map[string]any{"model": model, "max_tokens": 2048, "stream": true, "stream_options": map[string]any{"include_usage": true}, "tools": tools, "tool_choice": "auto", "messages": msgs}); err != nil {
		hc.FollowUp, hc.Error = checkFail, "follow-up: "+err.Error()
		return
	}
	if text, _ := chatMessage(out)["content"].(string); strings.TrimSpace(text) == "" {
		hc.FollowUp, hc.Error = checkFail, "follow-up: no text"
		return
	}
	hc.FollowUp = checkPass
}

func postJSON(ctx context.Context, url, key string, body any) (map[string]any, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, clip(string(raw), 300))
	}
	if strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		return readChatStream(resp.Body)
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "ndjson") {
		return readOllamaCheckStream(resp.Body)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
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

// readChatStream assembles tool ids, names and argument fragments by index,
// including chunks where choices is empty and only usage is present.
func readChatStream(r io.Reader) (map[string]any, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	msg := map[string]any{"role": "assistant", "content": ""}
	calls := map[int]map[string]any{}
	out := map[string]any{"choices": []any{map[string]any{"message": msg}}}
	complete := false
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			complete = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					Reasoning        string `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage json.RawMessage `json:"usage"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, fmt.Errorf("bad stream JSON: %w", err)
		}
		if chunk.Error != nil {
			return nil, fmt.Errorf("stream: %s", chunk.Error.Message)
		}
		if len(chunk.Usage) > 0 && string(chunk.Usage) != "null" {
			out["usage"] = chunk.Usage
		}
		for _, choice := range chunk.Choices {
			d := choice.Delta
			msg["content"] = msg["content"].(string) + d.Content
			if d.ReasoningContent == "" {
				d.ReasoningContent = d.Reasoning
			}
			if d.ReasoningContent != "" {
				old, _ := msg["reasoning_content"].(string)
				msg["reasoning_content"] = old + d.ReasoningContent
			}
			for _, delta := range d.ToolCalls {
				if delta.Index < 0 || delta.Index > 128 {
					return nil, fmt.Errorf("invalid tool index %d", delta.Index)
				}
				call := calls[delta.Index]
				if call == nil {
					call = map[string]any{"id": "", "type": "function", "function": map[string]any{"name": "", "arguments": ""}}
					calls[delta.Index] = call
				}
				if delta.ID != "" {
					call["id"] = delta.ID
				}
				fn := call["function"].(map[string]any)
				fn["name"] = fn["name"].(string) + delta.Function.Name
				fn["arguments"] = fn["arguments"].(string) + delta.Function.Arguments
			}
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if !complete {
		return nil, fmt.Errorf("stream ended before [DONE]")
	}
	if len(calls) > 0 {
		ordered := make([]any, len(calls))
		for i := range ordered {
			if calls[i] == nil {
				return nil, fmt.Errorf("missing tool index %d", i)
			}
			ordered[i] = calls[i]
		}
		msg["tool_calls"] = ordered
	}
	return out, nil
}

func runAiderCheck(ctx context.Context, hc *HarnessCheck, url, model, key string, ollama bool) {
	system := "You are an expert software developer. Use whole-file edit format: for every changed file, put its path on a line followed immediately by a fenced code block containing the complete file. Return all file contents, with no omissions."
	msgs := []any{
		map[string]any{"role": "system", "content": system},
		map[string]any{"role": "user", "content": "Create example.txt containing hello."},
		map[string]any{"role": "assistant", "content": "example.txt\n```text\nhello\n```"},
		map[string]any{"role": "user", "content": "These are the files in my chat:\n<files>\nNo files have been shared yet.\n</files>"},
	}
	request := func(messages []any, stream bool) map[string]any {
		b := map[string]any{"model": model, "messages": messages, "stream": stream}
		if ollama {
			b["options"] = map[string]any{"num_ctx": 8192, "num_predict": 2048}
		} else {
			b["max_tokens"] = 2048
			if stream {
				b["stream_options"] = map[string]any{"include_usage": true}
			}
		}
		return b
	}
	text := func(out map[string]any) string {
		if ollama {
			m, _ := out["message"].(map[string]any)
			s, _ := m["content"].(string)
			return s
		}
		s, _ := chatMessage(out)["content"].(string)
		return s
	}
	greeting := append(append([]any{}, msgs...), map[string]any{"role": "user", "content": "Reply with the single word OK. Do not edit any files."})
	out, err := postJSON(ctx, url, key, request(greeting, true))
	if err != nil {
		hc.Greeting, hc.Error = checkFail, "greeting: "+err.Error()
		return
	}
	if strings.TrimSpace(text(out)) == "" {
		hc.Greeting, hc.Error = checkFail, "greeting: no text"
		return
	}
	hc.Greeting = checkPass
	msgs = append(msgs, map[string]any{"role": "user", "content": "Create tool.txt containing exactly ok on one line. Use whole-file edit format."})
	out, err = postJSON(ctx, url, key, request(msgs, true))
	if err != nil {
		hc.ToolCall, hc.Error = checkFail, "edit: "+err.Error()
		return
	}
	reply := text(out)
	if !aiderEditBlock(reply) {
		hc.ToolCall, hc.Error = checkFail, "edit: missing tool.txt whole-file fenced block"
		return
	}
	hc.ToolCall = checkPass
	msgs = append(msgs, map[string]any{"role": "assistant", "content": reply}, map[string]any{"role": "user", "content": "The edit was applied. Give a short commit message."}, map[string]any{"role": "system", "content": "Reminder: give only a short commit message. Do not edit files."})
	out, err = postJSON(ctx, url, key, request(msgs, false))
	if err != nil {
		hc.FollowUp, hc.Error = checkFail, "follow-up: "+err.Error()
		return
	}
	if strings.TrimSpace(text(out)) == "" {
		hc.FollowUp, hc.Error = checkFail, "follow-up: no text"
		return
	}
	hc.FollowUp = checkPass
}

func aiderEditBlock(text string) bool {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "tool.txt" && i+3 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```") {
			for j := i + 3; j < len(lines); j++ {
				if strings.TrimSpace(lines[j]) == "```" {
					return true
				}
			}
		}
	}
	return false
}

func readOllamaCheckStream(r io.Reader) (map[string]any, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	var text strings.Builder
	for sc.Scan() {
		var ch struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Done  bool   `json:"done"`
			Error string `json:"error"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ch); err != nil {
			return nil, err
		}
		if ch.Error != "" {
			return nil, fmt.Errorf("stream: %s", ch.Error)
		}
		text.WriteString(ch.Message.Content)
		if ch.Done {
			return map[string]any{"message": map[string]any{"content": text.String()}}, nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("Ollama stream ended before done")
}
