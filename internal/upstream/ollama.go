package upstream

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aeroknight786/trialm/internal/llm"
)

var ollamaCallID atomic.Uint64

// buildOllamaBody uses the native API because Ollama's Chat Completions API
// cannot set num_ctx. Tool arguments are objects on the native wire.
func (c *OpenAICompat) buildOllamaBody(req *llm.Request) map[string]any {
	chat := c.BuildBody(req)
	msgs := chat["messages"].([]map[string]any)
	toolNames := map[string]string{}
	for _, m := range msgs {
		if m["content"] == nil {
			m["content"] = ""
		}
		if reasoning, ok := m["reasoning_content"]; ok {
			m["thinking"] = reasoning
			delete(m, "reasoning_content")
		}
		if calls, ok := m["tool_calls"].([]map[string]any); ok {
			for _, call := range calls {
				fn := call["function"].(map[string]any)
				if id, ok := call["id"].(string); ok && id != "" {
					toolNames[id], _ = fn["name"].(string)
				}
				if args, ok := fn["arguments"].(string); ok && json.Valid([]byte(args)) {
					fn["arguments"] = json.RawMessage(args)
				}
			}
		}
		if m["role"] == llm.RoleTool {
			name, _ := m["name"].(string)
			if name == "" {
				id, _ := m["tool_call_id"].(string)
				name = toolNames[id]
			}
			if name != "" {
				m["tool_name"] = name
			}
			delete(m, "name")
		}
	}
	body := map[string]any{"model": c.Model, "messages": msgs, "stream": true}
	if tools, ok := chat["tools"]; ok {
		body["tools"] = tools
	}
	opts := map[string]any{}
	if req.NumCtx > 0 {
		opts["num_ctx"] = req.NumCtx
	}
	if req.MaxTokens > 0 {
		opts["num_predict"] = req.MaxTokens
	}
	if req.Temperature != nil {
		opts["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		opts["top_p"] = *req.TopP
	}
	if len(req.Stop) > 0 {
		opts["stop"] = req.Stop
	}
	if len(opts) > 0 {
		body["options"] = opts
	}
	if req.ReasoningEffort != "" {
		body["think"] = true
	}
	if len(req.ResponseFormat) > 0 {
		var format struct {
			Type       string `json:"type"`
			JSONSchema struct {
				Schema json.RawMessage `json:"schema"`
			} `json:"json_schema"`
		}
		if json.Unmarshal(req.ResponseFormat, &format) == nil {
			switch format.Type {
			case "json_object":
				body["format"] = "json"
			case "json_schema":
				body["format"] = format.JSONSchema.Schema
			}
		}
	}
	return body
}

func (c *OpenAICompat) streamOllama(ctx context.Context, req *llm.Request) (<-chan llm.Event, error) {
	b, err := json.Marshal(c.buildOllamaBody(req))
	if err != nil {
		return nil, err
	}
	base := strings.TrimSuffix(strings.TrimRight(c.BaseURL, "/"), "/v1")
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/chat", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		hr.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{}
	}
	resp, err := hc.Do(hr)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(raw))}
	}
	idle := c.IdleTimeout
	if idle <= 0 {
		idle = 120 * time.Second
	}
	out := make(chan llm.Event, 64)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		var stalled atomic.Bool
		timer := time.AfterFunc(idle, func() { stalled.Store(true); resp.Body.Close() })
		defer timer.Stop()
		send := func(ev llm.Event) bool {
			select {
			case out <- ev:
				return true
			case <-ctx.Done():
				return false
			}
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
		toolIndex := 0
		for sc.Scan() {
			timer.Reset(idle)
			var ch struct {
				Message struct {
					Content   string `json:"content"`
					Thinking  string `json:"thinking"`
					ToolCalls []struct {
						ID       string `json:"id"`
						Function struct {
							Name      string          `json:"name"`
							Arguments json.RawMessage `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				Done                  bool   `json:"done"`
				DoneReason            string `json:"done_reason"`
				PromptEvalCount       int    `json:"prompt_eval_count"`
				PromptEvalCachedCount int    `json:"prompt_eval_cached_count"`
				EvalCount             int    `json:"eval_count"`
				Error                 string `json:"error"`
			}
			if err := json.Unmarshal(sc.Bytes(), &ch); err != nil {
				send(llm.Event{Type: llm.EventError, Err: fmt.Errorf("Ollama stream: %w", err)})
				return
			}
			if ch.Error != "" {
				send(llm.Event{Type: llm.EventError, Err: fmt.Errorf("Ollama: %s", ch.Error)})
				return
			}
			if ch.Message.Thinking != "" && !send(llm.Event{Type: llm.EventReasoning, Text: ch.Message.Thinking}) {
				return
			}
			if ch.Message.Content != "" && !send(llm.Event{Type: llm.EventText, Text: ch.Message.Content}) {
				return
			}
			for _, call := range ch.Message.ToolCalls {
				id := call.ID
				if id == "" {
					id = fmt.Sprintf("call_ollama_%d", ollamaCallID.Add(1))
				}
				if !send(llm.Event{Type: llm.EventToolCallStart, Index: toolIndex, ID: id, Name: call.Function.Name}) {
					return
				}
				if !send(llm.Event{Type: llm.EventToolCallArgs, Index: toolIndex, Text: string(call.Function.Arguments)}) {
					return
				}
				toolIndex++
			}
			if ch.Done {
				finish := ch.DoneReason
				if finish == "" {
					finish = llm.FinishStop
				}
				if toolIndex > 0 && finish == llm.FinishStop {
					finish = llm.FinishToolCalls
				}
				send(llm.Event{Type: llm.EventDone, FinishReason: finish, Usage: &llm.Usage{PromptTokens: ch.PromptEvalCount, CachedPromptTokens: ch.PromptEvalCachedCount, CompletionTokens: ch.EvalCount}})
				return
			}
		}
		err := sc.Err()
		if stalled.Load() {
			err = fmt.Errorf("upstream sent nothing for %s; gave up", idle)
		}
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		send(llm.Event{Type: llm.EventError, Err: fmt.Errorf("Ollama stream: %w", err)})
	}()
	return out, nil
}
