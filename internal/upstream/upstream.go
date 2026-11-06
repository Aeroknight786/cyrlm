// Package upstream sends canonical requests to the model that produces the
// real output: any OpenAI-compatible Chat Completions endpoint (OpenRouter by
// default, with provider/precision pinning), or an offline mock.
package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

// Client streams a model response.
type Client interface {
	// Stream starts a request; the channel closes after EventDone or
	// EventError. Cancel ctx to abort.
	Stream(ctx context.Context, req *llm.Request) (<-chan llm.Event, error)
	// Describe returns a one-line description for logs.
	Describe() string
}

// OpenAICompat is an OpenAI-compatible Chat Completions client.
type OpenAICompat struct {
	BaseURL string // e.g. https://openrouter.ai/api/v1
	APIKey  string
	Model   string // upstream model id
	// Extra is merged into every request body (e.g. OpenRouter "provider").
	Extra map[string]any
	// Reasoning is true for reasoning models: ask for their reasoning text
	// so its decode time can be simulated. Sending reasoning parameters to
	// other models can make OpenRouter find no provider.
	Reasoning bool
	// IdleTimeout ends a stream that sends nothing for this long
	// (default 120 s), so a stalled provider can't block the device.
	IdleTimeout time.Duration
	HTTP        *http.Client
}

// OpenRouterBaseURL is the default upstream.
const OpenRouterBaseURL = "https://openrouter.ai/api/v1"

// Describe implements Client.
func (c *OpenAICompat) Describe() string {
	return fmt.Sprintf("%s @ %s", c.Model, c.BaseURL)
}

func (c *OpenAICompat) isOpenRouter() bool {
	return strings.Contains(c.BaseURL, "openrouter.ai")
}

// BuildBody builds the upstream request body.
func (c *OpenAICompat) BuildBody(req *llm.Request) map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages))
	for _, m := range NormalizeForTemplates(req.Messages) {
		om := map[string]any{"role": m.Role}
		switch m.Role {
		case llm.RoleAssistant:
			if m.Content != "" || len(m.ToolCalls) == 0 {
				om["content"] = m.Content
			} else {
				om["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					args := tc.Arguments
					if strings.TrimSpace(args) == "" {
						args = "{}"
					}
					calls[i] = map[string]any{"id": tc.ID, "type": "function", "function": map[string]any{"name": tc.Name, "arguments": args}}
				}
				om["tool_calls"] = calls
			}
		case llm.RoleTool:
			om["content"] = m.Content
			om["tool_call_id"] = m.ToolCallID
			if m.Name != "" {
				om["name"] = m.Name
			}
		default:
			om["content"] = m.Content
		}
		msgs = append(msgs, om)
	}
	body := map[string]any{
		"model":          c.Model,
		"messages":       msgs,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, len(req.Tools))
		for i, t := range req.Tools {
			params := json.RawMessage(`{"type":"object","properties":{}}`)
			if len(t.Parameters) > 0 {
				params = stripCyrlmKeys(t.Parameters)
			}
			tools[i] = map[string]any{"type": "function", "function": map[string]any{"name": t.Name, "description": t.Description, "parameters": params}}
		}
		body["tools"] = tools
		if len(req.ToolChoice) > 0 {
			body["tool_choice"] = req.ToolChoice
		}
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		body["stop"] = req.Stop
	}
	if c.Reasoning {
		switch {
		case c.isOpenRouter() && req.ReasoningEffort != "":
			body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
		case c.isOpenRouter():
			body["reasoning"] = map[string]any{"exclude": false}
		case req.ReasoningEffort != "":
			body["reasoning_effort"] = req.ReasoningEffort
		}
	}
	for k, v := range c.Extra {
		body[k] = v
	}
	if c.isOpenRouter() && len(req.Tools) > 0 {
		// Only route to providers that support tool calling.
		p := map[string]any{}
		if existing, ok := body["provider"].(map[string]any); ok {
			for k, v := range existing {
				p[k] = v
			}
		}
		p["require_parameters"] = true
		body["provider"] = p
	}
	return body
}

// NormalizeForTemplates reshapes a conversation so strict chat templates
// accept it. Harnesses such as Codex send several system/developer
// messages, some mid-conversation, and consecutive user messages; many
// GGUF templates (Qwen, Gemma, Mistral) raise on that ("System message
// must be at the beginning", "roles must alternate"). Leading system
// messages are joined into one; later ones become user messages marked as
// system notes, so the prompt prefix (and the runtime's prompt cache) stays
// stable; consecutive user messages are joined. Nothing is dropped.
func NormalizeForTemplates(in []llm.Message) []llm.Message {
	out := make([]llm.Message, 0, len(in))
	i := 0
	var sys []string
	for ; i < len(in) && in[i].Role == llm.RoleSystem; i++ {
		if strings.TrimSpace(in[i].Content) != "" {
			sys = append(sys, in[i].Content)
		}
	}
	if len(sys) > 0 {
		out = append(out, llm.Message{Role: llm.RoleSystem, Content: strings.Join(sys, "\n\n")})
	}
	for ; i < len(in); i++ {
		m := in[i]
		if m.Role == llm.RoleSystem {
			m = llm.Message{Role: llm.RoleUser, Content: "[System note]\n" + m.Content}
		}
		if n := len(out); n > 0 && m.Role == llm.RoleUser && out[n-1].Role == llm.RoleUser && m.Images == 0 && out[n-1].Images == 0 {
			out[n-1].Content += "\n\n" + m.Content
			continue
		}
		out = append(out, m)
	}
	return out
}

// stripCyrlmKeys removes cyrlm-internal schema markers (x-cyrlm-*) that
// strict upstream schema validators reject.
func stripCyrlmKeys(raw json.RawMessage) json.RawMessage {
	if !bytes.Contains(raw, []byte("x-cyrlm-")) {
		return raw
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return raw
	}
	for k := range m {
		if strings.HasPrefix(k, "x-cyrlm-") {
			delete(m, k)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return b
}

// Stream implements Client.
func (c *OpenAICompat) Stream(ctx context.Context, req *llm.Request) (<-chan llm.Event, error) {
	b, err := json.Marshal(c.BuildBody(req))
	if err != nil {
		return nil, err
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	hreq.Header.Set("Accept", "text/event-stream")
	if c.APIKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.isOpenRouter() {
		hreq.Header.Set("X-Title", "cyrlm local-model trial")
		hreq.Header.Set("HTTP-Referer", "https://github.com/aeroknight786/cyrlm")
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 0}
	}
	resp, err := hc.Do(hreq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(msg))}
	}
	idle := c.IdleTimeout
	if idle <= 0 {
		idle = 120 * time.Second
	}
	out := make(chan llm.Event, 64)
	go func() {
		defer close(out)
		var stalled atomic.Bool
		timer := time.AfterFunc(idle, func() { stalled.Store(true); resp.Body.Close() })
		defer timer.Stop()
		defer resp.Body.Close()
		parseSSE(ctx, resp.Body, out, func() { timer.Reset(idle) }, stalled.Load, idle)
	}()
	return out, nil
}

// HTTPError is a non-200 upstream response.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	msg := e.Body
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(e.Body), &parsed) == nil && parsed.Error.Message != "" {
		msg = parsed.Error.Message
	}
	return fmt.Sprintf("upstream returned HTTP %d: %s", e.Status, msg)
}

type chunk struct {
	Choices []struct {
		Delta struct {
			Content          *string `json:"content"`
			ReasoningContent *string `json:"reasoning_content"`
			Reasoning        *string `json:"reasoning"`
			ToolCalls        []struct {
				Index    *int   `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// parseSSE converts an OpenAI chat.completion.chunk stream into events.
func parseSSE(ctx context.Context, r io.Reader, out chan<- llm.Event, onLine func(), stalled func() bool, idle time.Duration) {
	send := func(ev llm.Event) bool {
		select {
		case out <- ev:
			return true
		case <-ctx.Done():
			return false
		}
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	started := map[int]bool{}
	finish := ""
	var usage *llm.Usage
	sawTool := false
	for sc.Scan() {
		if onLine != nil {
			onLine()
		}
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue // comments (": OPENROUTER PROCESSING"), event names, blanks
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if err := json.Unmarshal([]byte(data), &ch); err != nil {
			continue
		}
		if ch.Error != nil {
			send(llm.Event{Type: llm.EventError, Err: errors.New(ch.Error.Message)})
			return
		}
		for _, c := range ch.Choices {
			d := c.Delta
			if d.ReasoningContent != nil && *d.ReasoningContent != "" {
				if !send(llm.Event{Type: llm.EventReasoning, Text: *d.ReasoningContent}) {
					return
				}
			} else if d.Reasoning != nil && *d.Reasoning != "" {
				if !send(llm.Event{Type: llm.EventReasoning, Text: *d.Reasoning}) {
					return
				}
			}
			if d.Content != nil && *d.Content != "" {
				if !send(llm.Event{Type: llm.EventText, Text: *d.Content}) {
					return
				}
			}
			for i, tc := range d.ToolCalls {
				idx := i
				if tc.Index != nil {
					idx = *tc.Index
				}
				if !started[idx] {
					started[idx] = true
					sawTool = true
					if !send(llm.Event{Type: llm.EventToolCallStart, Index: idx, ID: tc.ID, Name: tc.Function.Name}) {
						return
					}
				}
				if tc.Function.Arguments != "" {
					if !send(llm.Event{Type: llm.EventToolCallArgs, Index: idx, Text: tc.Function.Arguments}) {
						return
					}
				}
			}
			if c.FinishReason != nil && *c.FinishReason != "" {
				finish = *c.FinishReason
			}
		}
		if ch.Usage != nil {
			u := llm.Usage{PromptTokens: ch.Usage.PromptTokens, CompletionTokens: ch.Usage.CompletionTokens}
			if ch.Usage.PromptTokensDetails != nil {
				u.CachedPromptTokens = ch.Usage.PromptTokensDetails.CachedTokens
			}
			if ch.Usage.CompletionTokensDetails != nil {
				u.ReasoningTokens = ch.Usage.CompletionTokensDetails.ReasoningTokens
			}
			usage = &u
		}
	}
	if stalled != nil && stalled() {
		send(llm.Event{Type: llm.EventError, Err: fmt.Errorf("upstream sent nothing for %s; gave up", idle)})
		return
	}
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		send(llm.Event{Type: llm.EventError, Err: fmt.Errorf("upstream stream: %w", err)})
		return
	}
	if finish == "" {
		finish = llm.FinishStop
	}
	if finish == "stop" && sawTool {
		finish = llm.FinishToolCalls
	}
	send(llm.Event{Type: llm.EventDone, FinishReason: finish, Usage: usage})
}

// Mock is an offline upstream that streams a fixed explanatory reply at
// datacenter speed. It never makes tool calls, so it is safe to point a real
// harness at it for a timing demo.
type Mock struct {
	TTFT time.Duration
	TPS  float64
	// Words is the approximate reply length.
	Words int
}

// Describe implements Client.
func (m *Mock) Describe() string { return "offline mock (no model; fixed text)" }

// Stream implements Client.
func (m *Mock) Stream(ctx context.Context, req *llm.Request) (<-chan llm.Event, error) {
	ttft, tps, words := m.TTFT, m.TPS, m.Words
	if ttft <= 0 {
		ttft = 300 * time.Millisecond
	}
	if tps <= 0 {
		tps = 300
	}
	if words <= 0 {
		words = 120
	}
	last := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == llm.RoleUser {
			last = req.Messages[i].Content
			break
		}
	}
	if len(last) > 80 {
		last = last[:80] + "…"
	}
	text := "This reply comes from cyrlm's offline mock upstream, not a language model. " +
		"It exists so you can feel the simulated timing of your device: the delay before this text appeared is the predicted model load and prompt processing, " +
		"and the pace of these words is the predicted generation speed. "
	if last != "" {
		text += fmt.Sprintf("Your last message was: %q. ", last)
	}
	text += "Connect a real upstream (for example OPENROUTER_API_KEY with --upstream openrouter) to try the actual model. "
	for len(strings.Fields(text)) < words {
		text += "The simulated device keeps generating at its predicted rate so longer answers show sustained speed. "
	}
	out := make(chan llm.Event, 16)
	go func() {
		defer close(out)
		timer := time.NewTimer(ttft)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return
		}
		fields := strings.SplitAfter(text, " ")
		per := time.Duration(float64(time.Second) / tps * 1.3)
		n := 0
		for _, f := range fields {
			select {
			case out <- llm.Event{Type: llm.EventText, Text: f}:
			case <-ctx.Done():
				return
			}
			n++
			time.Sleep(per)
		}
		out <- llm.Event{Type: llm.EventDone, FinishReason: llm.FinishStop, Usage: &llm.Usage{CompletionTokens: int(float64(n) * 1.3)}}
	}()
	return out, nil
}
