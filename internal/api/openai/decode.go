// Package openai implements the OpenAI Chat Completions wire protocol
// (POST /v1/chat/completions) and the /v1/models listing, in the dialect
// spoken by local OpenAI-compatible runtimes such as llama-server: reasoning
// is carried in reasoning_content and responses report a llama-server style
// timings object.
package openai

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

// Frontend is the OpenAI Chat Completions protocol.
type Frontend struct{}

// New returns the Chat Completions frontend.
func New() api.Frontend { return &Frontend{} }

// Name implements api.Frontend.
func (f *Frontend) Name() string { return llm.ProtocolOpenAIChat }

type chatRequest struct {
	Model               string          `json:"model"`
	Messages            []chatMessage   `json:"messages"`
	Tools               []chatTool      `json:"tools"`
	Functions           []functionDef   `json:"functions"`
	ToolChoice          json.RawMessage `json:"tool_choice"`
	FunctionCall        json.RawMessage `json:"function_call"`
	MaxCompletionTokens *int            `json:"max_completion_tokens"`
	MaxTokens           *int            `json:"max_tokens"`
	Temperature         *float64        `json:"temperature"`
	TopP                *float64        `json:"top_p"`
	Stop                json.RawMessage `json:"stop"`
	Stream              bool            `json:"stream"`
	StreamOptions       *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
	ReasoningEffort string `json:"reasoning_effort"`
	N               *int   `json:"n"`
}

type chatMessage struct {
	Role             string          `json:"role"`
	Content          json.RawMessage `json:"content"`
	Name             string          `json:"name"`
	ToolCalls        []chatToolCall  `json:"tool_calls"`
	ToolCallID       string          `json:"tool_call_id"`
	ReasoningContent json.RawMessage `json:"reasoning_content"`
	Reasoning        json.RawMessage `json:"reasoning"`
	FunctionCall     *chatFunction   `json:"function_call"`
}

type chatToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function chatFunction `json:"function"`
}

type chatFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type chatTool struct {
	Type     string       `json:"type"`
	Function *functionDef `json:"function"`
}

type functionDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type contentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

// DecodeRequest implements api.Frontend.
func (f *Frontend) DecodeRequest(body []byte) (*llm.Request, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		return nil, errors.New("request body is empty")
	}
	var in chatRequest
	if err := json.Unmarshal(body, &in); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	if len(in.Messages) == 0 {
		return nil, errors.New("'messages' is required and must be a non-empty array")
	}

	var warn warnings
	req := &llm.Request{
		Model:           in.Model,
		Temperature:     in.Temperature,
		TopP:            in.TopP,
		Stream:          in.Stream,
		ReasoningEffort: normalizeEffort(in.ReasoningEffort),
		Protocol:        llm.ProtocolOpenAIChat,
	}
	if in.StreamOptions != nil {
		req.IncludeUsage = in.StreamOptions.IncludeUsage
	}
	switch {
	case in.MaxCompletionTokens != nil && *in.MaxCompletionTokens > 0:
		req.MaxTokens = *in.MaxCompletionTokens
	case in.MaxTokens != nil && *in.MaxTokens > 0:
		req.MaxTokens = *in.MaxTokens
	}
	if in.N != nil && *in.N > 1 {
		warn.add("n > 1 is not supported; returning a single choice", 1)
	}

	stop, err := decodeStop(in.Stop)
	if err != nil {
		return nil, err
	}
	req.Stop = stop

	// Legacy function calling: assistant function_call / role "function"
	// carry no ids, so synthesize them to keep calls and results linked.
	legacyN := 0
	legacyPending := ""
	for i, m := range in.Messages {
		where := fmt.Sprintf("messages[%d]", i)
		text, images, err := decodeContent(m.Content, where, &warn)
		if err != nil {
			return nil, err
		}
		msg := llm.Message{Content: text, Images: images}
		switch m.Role {
		case "system", "developer":
			msg.Role = llm.RoleSystem
		case "user":
			msg.Role = llm.RoleUser
		case "assistant":
			msg.Role = llm.RoleAssistant
			msg.Reasoning = rawString(m.ReasoningContent)
			if msg.Reasoning == "" {
				msg.Reasoning = rawString(m.Reasoning)
			}
			for j, tc := range m.ToolCalls {
				if tc.Type != "" && tc.Type != "function" {
					warn.add(fmt.Sprintf("ignored assistant tool call of type %q", tc.Type), 1)
					continue
				}
				if tc.Function.Name == "" {
					return nil, fmt.Errorf("%s.tool_calls[%d].function.name is required", where, j)
				}
				msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
					ID:        tc.ID,
					Name:      tc.Function.Name,
					Arguments: decodeArguments(tc.Function.Arguments),
				})
			}
			if fc := m.FunctionCall; fc != nil && fc.Name != "" && len(m.ToolCalls) == 0 {
				legacyN++
				legacyPending = fmt.Sprintf("call_legacy_%d", legacyN)
				msg.ToolCalls = append(msg.ToolCalls, llm.ToolCall{
					ID:        legacyPending,
					Name:      fc.Name,
					Arguments: decodeArguments(fc.Arguments),
				})
			}
		case "tool":
			msg.Role = llm.RoleTool
			msg.ToolCallID = m.ToolCallID
			msg.Name = m.Name
		case "function":
			msg.Role = llm.RoleTool
			msg.Name = m.Name
			msg.ToolCallID = legacyPending
			legacyPending = ""
		case "":
			return nil, fmt.Errorf("%s.role is required", where)
		default:
			return nil, fmt.Errorf("%s.role %q is not supported (want system, developer, user, assistant or tool)", where, m.Role)
		}
		req.Messages = append(req.Messages, msg)
	}

	seen := map[string]bool{}
	for i, t := range in.Tools {
		if t.Type != "" && t.Type != "function" {
			warn.add(fmt.Sprintf("ignored non-function tool of type %q", t.Type), 1)
			continue
		}
		if t.Function == nil || t.Function.Name == "" {
			return nil, fmt.Errorf("tools[%d].function.name is required", i)
		}
		req.Tools = append(req.Tools, toTool(*t.Function))
		seen[t.Function.Name] = true
	}
	for i, fn := range in.Functions {
		if fn.Name == "" {
			return nil, fmt.Errorf("functions[%d].name is required", i)
		}
		if seen[fn.Name] {
			continue
		}
		req.Tools = append(req.Tools, toTool(fn))
		seen[fn.Name] = true
	}

	if !isNull(in.ToolChoice) {
		req.ToolChoice = compact(in.ToolChoice)
	} else if !isNull(in.FunctionCall) {
		req.ToolChoice = legacyToolChoice(in.FunctionCall)
	}

	req.Warnings = warn.list()
	return req, nil
}

func toTool(fn functionDef) llm.Tool {
	t := llm.Tool{Name: fn.Name, Description: fn.Description}
	if !isNull(fn.Parameters) {
		t.Parameters = compact(fn.Parameters)
	}
	return t
}

// legacyToolChoice maps the deprecated request-level function_call
// ("none" | "auto" | {"name": X}) to a tool_choice value.
func legacyToolChoice(raw json.RawMessage) json.RawMessage {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		b, _ := json.Marshal(s)
		return b
	}
	var obj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(raw, &obj) == nil && obj.Name != "" {
		b, _ := json.Marshal(map[string]any{
			"type":     "function",
			"function": map[string]string{"name": obj.Name},
		})
		return b
	}
	return nil
}

// decodeContent accepts a string, null, or an array of content parts. Text
// parts are joined with "\n"; image, audio and file parts are counted.
func decodeContent(raw json.RawMessage, where string, warn *warnings) (string, int, error) {
	raw = bytes.TrimSpace(raw)
	if isNull(raw) {
		return "", 0, nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", 0, fmt.Errorf("%s.content: %v", where, err)
		}
		return s, 0, nil
	case '[':
		var parts []contentPart
		if err := json.Unmarshal(raw, &parts); err != nil {
			return "", 0, fmt.Errorf("%s.content: invalid content parts: %v", where, err)
		}
		var texts []string
		images := 0
		for _, p := range parts {
			switch p.Type {
			case "text", "input_text", "output_text":
				texts = append(texts, p.Text)
			case "refusal":
				texts = append(texts, p.Refusal)
			case "image_url", "input_image", "input_audio", "file", "input_file":
				images++
			default:
				warn.add(fmt.Sprintf("ignored content part of type %q", p.Type), 1)
			}
		}
		if images > 0 {
			warn.add("dropped image/audio/file content part(s) (text-only simulation)", images)
		}
		return strings.Join(texts, "\n"), images, nil
	}
	return "", 0, fmt.Errorf("%s.content must be a string, null or an array of content parts", where)
}

// decodeArguments returns tool-call arguments as a JSON string. Clients send
// a JSON-encoded string; an inline object is tolerated and compacted.
func decodeArguments(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if isNull(raw) {
		return ""
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s
		}
	}
	return string(compact(raw))
}

func decodeStop(raw json.RawMessage) ([]string, error) {
	raw = bytes.TrimSpace(raw)
	if isNull(raw) {
		return nil, nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if s == "" {
			return nil, nil
		}
		return []string{s}, nil
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, errors.New("'stop' must be a string or an array of strings")
	}
	out := arr[:0]
	for _, v := range arr {
		if v != "" {
			out = append(out, v)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// rawString returns raw as a string when it is a JSON string, else "".
func rawString(raw json.RawMessage) string {
	var s string
	if len(raw) > 0 && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

func normalizeEffort(e string) string {
	switch strings.ToLower(strings.TrimSpace(e)) {
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh", "max":
		return "high"
	}
	return ""
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
