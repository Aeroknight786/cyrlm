// Package anthropic implements the Anthropic Messages wire protocol
// (POST /v1/messages and POST /v1/messages/count_tokens), as spoken by
// Claude Code and other harnesses pointed at ANTHROPIC_BASE_URL.
package anthropic

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

// Frontend is the Anthropic Messages protocol.
type Frontend struct{}

// New returns the Messages frontend.
func New() *Frontend { return &Frontend{} }

// Name implements api.Frontend.
func (f *Frontend) Name() string { return llm.ProtocolAnthropic }

type messagesRequest struct {
	Model         string          `json:"model"`
	System        json.RawMessage `json:"system"`
	Messages      []inMessage     `json:"messages"`
	Tools         []inTool        `json:"tools"`
	ToolChoice    *inToolChoice   `json:"tool_choice"`
	MaxTokens     int             `json:"max_tokens"`
	Temperature   *float64        `json:"temperature"`
	TopP          *float64        `json:"top_p"`
	StopSequences []string        `json:"stop_sequences"`
	Stream        bool            `json:"stream"`
	Thinking      *inThinking     `json:"thinking"`
	OutputConfig  *outputConfig   `json:"output_config"`
}

type outputConfig struct {
	Effort string `json:"effort"`
}

type inMessage struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type inBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Content   json.RawMessage `json:"content"`
	IsError   bool            `json:"is_error"`
	Thinking  string          `json:"thinking"`
}

type inTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type inToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type inThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// DecodeRequest implements api.Frontend. It accepts both /v1/messages and
// /v1/messages/count_tokens bodies (max_tokens optional).
func (f *Frontend) DecodeRequest(body []byte) (*llm.Request, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("request body is empty")
	}
	var in messagesRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	if len(in.Messages) == 0 {
		return nil, errors.New("messages: field required (at least one message)")
	}
	var warn warnings
	req := &llm.Request{
		Model:       in.Model,
		MaxTokens:   in.MaxTokens,
		Temperature: in.Temperature,
		TopP:        in.TopP,
		Stream:      in.Stream,
		Protocol:    llm.ProtocolAnthropic,
	}
	for _, s := range in.StopSequences {
		if s != "" {
			req.Stop = append(req.Stop, s)
		}
	}
	req.ReasoningEffort = effortFor(in.Thinking, in.OutputConfig)

	sys, err := decodeSystem(in.System, &warn)
	if err != nil {
		return nil, err
	}
	if sys != "" {
		req.Messages = append(req.Messages, llm.Message{Role: llm.RoleSystem, Content: sys})
	}

	toolNames := map[string]string{} // tool_use id -> name, for tool results
	for i, m := range in.Messages {
		where := fmt.Sprintf("messages.%d", i)
		blocks, err := decodeBlocks(m.Content, where+".content")
		if err != nil {
			return nil, err
		}
		switch m.Role {
		case "user":
			req.Messages = append(req.Messages, decodeUser(blocks, toolNames, &warn)...)
		case "assistant":
			msg, err := decodeAssistant(blocks, where, &warn)
			if err != nil {
				return nil, err
			}
			for _, tc := range msg.ToolCalls {
				toolNames[tc.ID] = tc.Name
			}
			req.Messages = append(req.Messages, msg)
		case "system":
			// Mid-conversation operator instruction. An effort-only system
			// message carries no content and is dropped.
			text := joinText(blocks, "system message", &warn)
			if text != "" {
				req.Messages = append(req.Messages, llm.Message{Role: llm.RoleSystem, Content: text})
			}
		case "":
			return nil, fmt.Errorf("%s.role: field required", where)
		default:
			return nil, fmt.Errorf("%s.role: unexpected role %q (want \"user\" or \"assistant\")", where, m.Role)
		}
	}

	for i, t := range in.Tools {
		if t.Type != "" && t.Type != "custom" {
			warn.add(fmt.Sprintf("dropped server tool %q of type %q (not simulated)", t.Name, t.Type), 1)
			continue
		}
		if t.Name == "" {
			return nil, fmt.Errorf("tools.%d.name: field required", i)
		}
		tool := llm.Tool{Name: t.Name, Description: t.Description}
		if !isNull(t.InputSchema) {
			tool.Parameters = compact(t.InputSchema)
		}
		req.Tools = append(req.Tools, tool)
	}

	if tc := in.ToolChoice; tc != nil {
		switch tc.Type {
		case "auto", "":
			req.ToolChoice = json.RawMessage(`"auto"`)
		case "any":
			req.ToolChoice = json.RawMessage(`"required"`)
		case "none":
			req.ToolChoice = json.RawMessage(`"none"`)
		case "tool":
			if tc.Name == "" {
				return nil, errors.New("tool_choice.name: field required when type is \"tool\"")
			}
			b, _ := json.Marshal(map[string]any{
				"type":     "function",
				"function": map[string]string{"name": tc.Name},
			})
			req.ToolChoice = b
		default:
			warn.add(fmt.Sprintf("ignored tool_choice of type %q", tc.Type), 1)
		}
	}

	req.Warnings = warn.list()
	return req, nil
}

// decodeUser converts one user turn. Tool results become RoleTool messages
// (in block order, first, since they answer the preceding assistant turn);
// remaining text and media follow as one RoleUser message.
func decodeUser(blocks []inBlock, toolNames map[string]string, warn *warnings) []llm.Message {
	var out []llm.Message
	var texts []string
	images := 0
	for _, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "image", "document":
			images++
		case "tool_result":
			out = append(out, decodeToolResult(b, toolNames, warn))
		default:
			warn.add(fmt.Sprintf("ignored %s block in user message", b.Type), 1)
		}
	}
	if images > 0 {
		warn.add("dropped image/document block(s) (text-only simulation)", images)
	}
	if len(texts) > 0 || images > 0 || len(out) == 0 {
		out = append(out, llm.Message{
			Role:    llm.RoleUser,
			Content: strings.Join(texts, "\n"),
			Images:  images,
		})
	}
	return out
}

func decodeToolResult(b inBlock, toolNames map[string]string, warn *warnings) llm.Message {
	msg := llm.Message{Role: llm.RoleTool, ToolCallID: b.ToolUseID, Name: toolNames[b.ToolUseID]}
	raw := bytes.TrimSpace(b.Content)
	switch {
	case isNull(raw):
	case raw[0] == '"':
		_ = json.Unmarshal(raw, &msg.Content)
	case raw[0] == '[':
		var parts []inBlock
		if err := json.Unmarshal(raw, &parts); err != nil {
			warn.add("ignored malformed tool_result content", 1)
			break
		}
		var texts []string
		for _, p := range parts {
			switch p.Type {
			case "text":
				texts = append(texts, p.Text)
			case "image", "document":
				msg.Images++
			default:
				warn.add(fmt.Sprintf("ignored %s block in tool_result", p.Type), 1)
			}
		}
		if msg.Images > 0 {
			warn.add("dropped image/document block(s) (text-only simulation)", msg.Images)
		}
		msg.Content = strings.Join(texts, "\n")
	default:
		warn.add("ignored malformed tool_result content", 1)
	}
	if b.IsError {
		msg.Content = "Error: " + msg.Content
	}
	return msg
}

func decodeAssistant(blocks []inBlock, where string, warn *warnings) (llm.Message, error) {
	msg := llm.Message{Role: llm.RoleAssistant}
	var texts, thoughts []string
	for i, b := range blocks {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "thinking":
			if b.Thinking != "" {
				thoughts = append(thoughts, b.Thinking)
			}
		case "tool_use":
			if b.Name == "" {
				return msg, fmt.Errorf("%s.content.%d.name: field required", where, i)
			}
			args := "{}"
			if !isNull(b.Input) {
				args = string(compact(b.Input))
			}
			msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{ID: b.ID, Name: b.Name, Arguments: args})
		case "image", "document":
			msg.Images++
			warn.add("dropped image/document block(s) (text-only simulation)", 1)
		default:
			// redacted_thinking, server_tool_use, web_search_tool_result, ...
			warn.add(fmt.Sprintf("ignored %s block in assistant message", b.Type), 1)
		}
	}
	msg.Content = strings.Join(texts, "\n")
	msg.Reasoning = strings.Join(thoughts, "\n")
	return msg, nil
}

// decodeSystem accepts a string or an array of text blocks (cache_control
// is ignored) and returns the joined text.
func decodeSystem(raw json.RawMessage, warn *warnings) (string, error) {
	blocks, err := decodeBlocks(raw, "system")
	if err != nil {
		return "", err
	}
	return joinText(blocks, "system", warn), nil
}

func joinText(blocks []inBlock, where string, warn *warnings) string {
	var texts []string
	for _, b := range blocks {
		if b.Type == "text" {
			texts = append(texts, b.Text)
		} else {
			warn.add(fmt.Sprintf("ignored %s block in %s", b.Type, where), 1)
		}
	}
	return strings.Join(texts, "\n")
}

// decodeBlocks normalises string | null | [blocks] content to blocks; a
// plain string becomes a single text block.
func decodeBlocks(raw json.RawMessage, where string) ([]inBlock, error) {
	raw = bytes.TrimSpace(raw)
	if isNull(raw) {
		return nil, nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("%s: %v", where, err)
		}
		if s == "" {
			return nil, nil
		}
		return []inBlock{{Type: "text", Text: s}}, nil
	case '[':
		var blocks []inBlock
		if err := json.Unmarshal(raw, &blocks); err != nil {
			return nil, fmt.Errorf("%s: invalid content blocks: %v", where, err)
		}
		return blocks, nil
	}
	return nil, fmt.Errorf("%s: must be a string or an array of content blocks", where)
}

// effortFor maps Anthropic thinking / output_config.effort to the canonical
// reasoning effort.
func effortFor(th *inThinking, oc *outputConfig) string {
	effort := ""
	if oc != nil {
		switch strings.ToLower(oc.Effort) {
		case "low":
			effort = "low"
		case "medium":
			effort = "medium"
		case "high", "xhigh", "max":
			effort = "high"
		}
	}
	if th == nil {
		return effort
	}
	switch th.Type {
	case "enabled":
		switch {
		case th.BudgetTokens < 4096:
			return "low"
		case th.BudgetTokens < 16384:
			return "medium"
		default:
			return "high"
		}
	case "adaptive":
		if effort == "" {
			return "medium"
		}
		return effort
	case "disabled", "between_tools":
		return ""
	}
	return effort
}

func isNull(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) == 0 || bytes.Equal(raw, []byte("null"))
}

func compact(raw json.RawMessage) json.RawMessage {
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return raw
	}
	return buf.Bytes()
}

// warnings collects de-duplicated lossy-conversion notes in first-seen order.
type warnings struct {
	order  []string
	counts map[string]int
}

func (w *warnings) add(msg string, n int) {
	if w.counts == nil {
		w.counts = map[string]int{}
	}
	if _, ok := w.counts[msg]; !ok {
		w.order = append(w.order, msg)
	}
	w.counts[msg] += n
}

func (w *warnings) list() []string {
	var out []string
	for _, m := range w.order {
		if n := w.counts[m]; n > 1 {
			out = append(out, fmt.Sprintf("%s (x%d)", m, n))
		} else {
			out = append(out, m)
		}
	}
	return out
}
