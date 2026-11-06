// Package ollama implements the Ollama native API frontends: POST /api/chat
// (NewChat) and POST /api/generate (NewGenerate), plus helpers for the
// discovery endpoints (/api/version, /api/tags, /api/show, /api/ps) that
// editors such as Zed, JetBrains and Continue probe before chatting.
//
// Streams are NDJSON (one JSON object per line, flushed per line). Unlike
// OpenAI-style protocols, Ollama emits each tool call whole, with its
// arguments as a JSON object, so the stream buffers argument fragments until
// the call is complete. The final line reports simulated local timings.
//
// Ollama requests carry no tool-call ids. DecodeRequest assigns stable ids
// ("call_<message>_<n>") to assistant tool calls without one and links each
// following tool message to a call (same tool_name first, else in order), so
// the canonical request is self-consistent for upstreams that require ids.
//
// An empty /api/chat messages list or an empty /api/generate prompt is
// Ollama's "load the model" probe: DecodeRequest returns a Request with no
// Messages and the server should answer without calling upstream (Finish or
// WriteResponse with finish reason "load" or "unload"; unknown finish
// reasons are passed through as done_reason).
package ollama

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

// NewChat returns the /api/chat frontend.
func NewChat() api.Frontend { return chatFrontend{} }

// NewGenerate returns the /api/generate frontend.
func NewGenerate() api.Frontend { return generateFrontend{} }

type chatFrontend struct{}

type generateFrontend struct{}

func (chatFrontend) Name() string     { return llm.ProtocolOllama }
func (generateFrontend) Name() string { return llm.ProtocolOllama }

// ---- wire types ---------------------------------------------------------

type chatWire struct {
	Model    string                     `json:"model"`
	Messages []messageWire              `json:"messages"`
	Tools    []toolWire                 `json:"tools"`
	Stream   *bool                      `json:"stream"`
	Format   json.RawMessage            `json:"format"`
	Options  map[string]json.RawMessage `json:"options"`
	Think    json.RawMessage            `json:"think"`
	// Ignored: keep_alive, truncate, shift.
}

type messageWire struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content"`
	Images     []json.RawMessage `json:"images"`
	Thinking   string            `json:"thinking"`
	ToolCalls  []toolCallWire    `json:"tool_calls"`
	ToolName   string            `json:"tool_name"`
	ToolCallID string            `json:"tool_call_id"`
	Name       string            `json:"name"`
}

type toolCallWire struct {
	ID       string `json:"id"`
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type toolWire struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type generateWire struct {
	Model    string                     `json:"model"`
	Prompt   string                     `json:"prompt"`
	Suffix   string                     `json:"suffix"`
	System   string                     `json:"system"`
	Template string                     `json:"template"`
	Images   []json.RawMessage          `json:"images"`
	Stream   *bool                      `json:"stream"`
	Raw      bool                       `json:"raw"`
	Format   json.RawMessage            `json:"format"`
	Options  map[string]json.RawMessage `json:"options"`
	Think    json.RawMessage            `json:"think"`
	// Ignored: context, keep_alive.
}

// ---- decoding -----------------------------------------------------------

// DecodeRequest implements api.Frontend for /api/chat.
func (chatFrontend) DecodeRequest(body []byte) (*llm.Request, error) {
	var cw chatWire
	if err := json.Unmarshal(body, &cw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	req := newRequest(cw.Model, cw.Stream)
	if err := applyCommon(req, cw.Options, cw.Think, cw.Format); err != nil {
		return nil, err
	}
	for i, mw := range cw.Messages {
		m, err := decodeMessage(req, mw)
		if err != nil {
			return nil, fmt.Errorf("messages[%d]: %v", i, err)
		}
		req.Messages = append(req.Messages, m)
	}
	linkToolCalls(req.Messages)
	for i, tw := range cw.Tools {
		if tw.Type != "" && tw.Type != "function" {
			req.Warnings = append(req.Warnings, fmt.Sprintf("tool type %q is not supported and was dropped", tw.Type))
			continue
		}
		if tw.Function.Name == "" {
			return nil, fmt.Errorf("tools[%d]: function name is required", i)
		}
		var params json.RawMessage
		if p := bytes.TrimSpace(tw.Function.Parameters); len(p) > 0 && string(p) != "null" {
			params = append(json.RawMessage(nil), p...)
		}
		req.Tools = append(req.Tools, llm.Tool{Name: tw.Function.Name, Description: tw.Function.Description, Parameters: params})
	}
	return req, nil
}

// DecodeRequest implements api.Frontend for /api/generate.
func (generateFrontend) DecodeRequest(body []byte) (*llm.Request, error) {
	var gw generateWire
	if err := json.Unmarshal(body, &gw); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	req := newRequest(gw.Model, gw.Stream)
	if err := applyCommon(req, gw.Options, gw.Think, gw.Format); err != nil {
		return nil, err
	}
	if gw.Prompt == "" && len(gw.Images) == 0 {
		return req, nil // load/unload probe
	}
	if gw.Raw {
		req.Warnings = append(req.Warnings, "raw prompt forwarded as a plain user message")
	}
	if gw.Suffix != "" {
		req.Warnings = append(req.Warnings, "suffix (fill-in-the-middle) is not supported and was ignored")
	}
	if gw.Template != "" {
		req.Warnings = append(req.Warnings, "template override ignored")
	}
	if gw.System != "" {
		req.Messages = append(req.Messages, llm.Message{Role: llm.RoleSystem, Content: gw.System})
	}
	user := llm.Message{Role: llm.RoleUser, Content: gw.Prompt, Images: len(gw.Images)}
	if user.Images > 0 {
		req.Warnings = append(req.Warnings, fmt.Sprintf("%d image(s) dropped (text-only simulation)", user.Images))
	}
	req.Messages = append(req.Messages, user)
	return req, nil
}

func newRequest(model string, stream *bool) *llm.Request {
	return &llm.Request{
		Model:    model,
		Protocol: llm.ProtocolOllama,
		Stream:   stream == nil || *stream, // Ollama streams by default
		// Ollama always reports prompt_eval_count / eval_count.
		IncludeUsage: true,
	}
}

func decodeMessage(req *llm.Request, mw messageWire) (llm.Message, error) {
	var m llm.Message
	switch mw.Role {
	case "system", "developer":
		m.Role = llm.RoleSystem
	case "user":
		m.Role = llm.RoleUser
	case "assistant":
		m.Role = llm.RoleAssistant
	case "tool":
		m.Role = llm.RoleTool
		m.Name = mw.ToolName
		if m.Name == "" {
			m.Name = mw.Name
		}
		m.ToolCallID = mw.ToolCallID
	default:
		return m, fmt.Errorf("unsupported role %q", mw.Role)
	}
	text, err := contentText(mw.Content)
	if err != nil {
		return m, err
	}
	m.Content = text
	if mw.Role == "assistant" {
		m.Reasoning = mw.Thinking
	}
	if n := len(mw.Images); n > 0 {
		m.Images = n
		req.Warnings = append(req.Warnings, fmt.Sprintf("%d image(s) dropped from a %s message (text-only simulation)", n, mw.Role))
	}
	for j, tc := range mw.ToolCalls {
		args, err := argumentsString(tc.Function.Arguments)
		if err != nil {
			return m, fmt.Errorf("tool_calls[%d].function.arguments: %v", j, err)
		}
		m.ToolCalls = append(m.ToolCalls, llm.ToolCall{ID: tc.ID, Name: tc.Function.Name, Arguments: args})
	}
	return m, nil
}

// contentText accepts Ollama's string content or, leniently, an array of
// {type:"text",text} parts.
func contentText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", errors.New("content must be a string")
	}
	var texts []string
	for _, p := range parts {
		if p.Text != "" {
			texts = append(texts, p.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}

// argumentsString converts tool-call arguments (an object; leniently a JSON
// string) to a compact JSON string, preserving the client's key order.
func argumentsString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		raw = []byte(s)
		if !json.Valid(raw) {
			return s, nil
		}
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// linkToolCalls assigns stable ids to assistant tool calls that lack one and
// links id-less tool messages to the calls they answer.
func linkToolCalls(msgs []llm.Message) {
	var open []*llm.ToolCall // unanswered calls of the latest assistant message
	for i := range msgs {
		m := &msgs[i]
		switch m.Role {
		case llm.RoleAssistant:
			open = open[:0]
			for j := range m.ToolCalls {
				if m.ToolCalls[j].ID == "" {
					m.ToolCalls[j].ID = fmt.Sprintf("call_%d_%d", i, j)
				}
				open = append(open, &m.ToolCalls[j])
			}
		case llm.RoleTool:
			pick := -1
			for k, c := range open {
				if m.ToolCallID != "" {
					if c.ID == m.ToolCallID {
						pick = k
						break
					}
				} else if m.Name != "" && c.Name == m.Name {
					pick = k
					break
				}
			}
			if pick < 0 && m.ToolCallID == "" && len(open) > 0 {
				pick = 0
			}
			if pick < 0 {
				continue
			}
			c := open[pick]
			open = append(open[:pick], open[pick+1:]...)
			if m.ToolCallID == "" {
				m.ToolCallID = c.ID
			}
			if m.Name == "" {
				m.Name = c.Name
			}
		}
	}
}

// applyCommon applies options, think and format (shared by chat/generate).
func applyCommon(req *llm.Request, opts map[string]json.RawMessage, think, format json.RawMessage) error {
	if err := applyOptions(req, opts); err != nil {
		return err
	}
	if err := applyThink(req, think); err != nil {
		return err
	}
	if f := bytes.TrimSpace(format); len(f) > 0 && string(f) != "null" && string(f) != `""` {
		req.Warnings = append(req.Warnings, "format (structured output) is not enforced")
	}
	return nil
}

func applyOptions(req *llm.Request, opts map[string]json.RawMessage) error {
	num := func(key string) (float64, bool, error) {
		raw, ok := opts[key]
		if !ok || string(bytes.TrimSpace(raw)) == "null" {
			return 0, false, nil
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			return 0, false, fmt.Errorf("options.%s must be a number", key)
		}
		return f, true, nil
	}
	if v, ok, err := num("num_ctx"); err != nil {
		return err
	} else if ok && v > 0 {
		req.NumCtx = int(v)
	}
	if v, ok, err := num("num_predict"); err != nil {
		return err
	} else if ok && v > 0 { // -1 (infinite) and -2 (fill context) mean no cap
		req.MaxTokens = int(v)
	}
	if v, ok, err := num("temperature"); err != nil {
		return err
	} else if ok {
		req.Temperature = &v
	}
	if v, ok, err := num("top_p"); err != nil {
		return err
	} else if ok {
		req.TopP = &v
	}
	if raw, ok := opts["stop"]; ok {
		raw = bytes.TrimSpace(raw)
		switch {
		case len(raw) == 0 || string(raw) == "null":
		case raw[0] == '"':
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return errors.New("options.stop must be a string array")
			}
			req.Stop = []string{s}
		default:
			if err := json.Unmarshal(raw, &req.Stop); err != nil {
				return errors.New("options.stop must be a string array")
			}
		}
	}
	return nil
}

func applyThink(req *llm.Request, raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch string(raw) {
	case "true":
		req.ReasoningEffort = "medium"
		return nil
	case "false":
		req.ReasoningEffort = ""
		return nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return errors.New(`think must be a boolean or "low", "medium" or "high"`)
	}
	switch strings.ToLower(s) {
	case "low", "medium", "high":
		req.ReasoningEffort = strings.ToLower(s)
	case "":
	default:
		req.Warnings = append(req.Warnings, fmt.Sprintf("unknown think level %q ignored", s))
	}
	return nil
}

// ---- errors -------------------------------------------------------------

type errorBody struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, message string) {
	_ = api.WriteJSON(w, status, errorBody{Error: message})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteError implements api.Frontend: {"error":"..."} with status.
func (chatFrontend) WriteError(w http.ResponseWriter, status int, _ string, message string) {
	writeError(w, status, message)
}

// WriteError implements api.Frontend: {"error":"..."} with status.
func (generateFrontend) WriteError(w http.ResponseWriter, status int, _ string, message string) {
	writeError(w, status, message)
}
