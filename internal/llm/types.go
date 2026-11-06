// Package llm defines the protocol-neutral request, response and streaming
// event model that every wire protocol (OpenAI Chat Completions, Anthropic
// Messages, OpenAI Responses, Ollama native) is translated to and from.
//
// The simulator only ever sees these types: frontends decode a protocol into
// a Request, the upstream produces Events, the pacer re-times Events, and the
// frontend encodes them back into the protocol's wire format.
package llm

import (
	"encoding/json"
	"strings"
	"time"
)

// Protocol identifiers.
const (
	ProtocolOpenAIChat = "openai-chat"
	ProtocolAnthropic  = "anthropic"
	ProtocolResponses  = "responses"
	ProtocolOllama     = "ollama"
)

// Message roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Finish reasons (OpenAI vocabulary; frontends map to their own).
const (
	FinishStop      = "stop"
	FinishLength    = "length"
	FinishToolCalls = "tool_calls"
	FinishError     = "error"
)

// Request is a protocol-neutral chat request.
type Request struct {
	// Model is the model name the client asked for (may be an alias).
	Model string
	// Messages in conversation order. System/developer instructions are
	// messages with Role == RoleSystem, placed where the protocol put them
	// (Anthropic's top-level system becomes the first message).
	Messages []Message
	Tools    []Tool
	// ToolChoice is an OpenAI-style tool_choice value ("auto", "none",
	// "required", or {"type":"function","function":{"name":...}}), or nil.
	ToolChoice json.RawMessage
	// MaxTokens is the client's output cap; 0 means unspecified.
	MaxTokens   int
	Temperature *float64
	TopP        *float64
	Stop        []string
	Stream      bool
	// ReasoningEffort is "", "low", "medium" or "high".
	ReasoningEffort string
	// NumCtx is a client-requested context window (Ollama options.num_ctx);
	// 0 means the client did not set one.
	NumCtx int
	// IncludeUsage is true when the client asked for a usage chunk in the
	// stream (OpenAI stream_options.include_usage).
	IncludeUsage bool
	// Protocol is the wire protocol the request arrived on.
	Protocol string
	// Warnings collects lossy-conversion notes (e.g. dropped images).
	Warnings []string
}

// Message is one conversation turn.
type Message struct {
	Role string
	// Content is the text content; multiple text parts are joined with "\n".
	Content string
	// Reasoning is assistant reasoning/thinking carried in history.
	Reasoning string
	// ToolCalls are the tool calls an assistant message made.
	ToolCalls []ToolCall
	// ToolCallID links a RoleTool message to the call it answers.
	ToolCallID string
	// Name is the tool name for RoleTool messages, when known.
	Name string
	// Images counts image parts that were dropped (text-only simulation).
	Images int
}

// ToolCall is a function call made by the assistant.
type ToolCall struct {
	ID   string
	Name string
	// Arguments is the JSON-encoded argument object.
	Arguments string
}

// Tool is a function the model may call.
type Tool struct {
	Name        string
	Description string
	// Parameters is the JSON schema of the arguments (may be nil).
	Parameters json.RawMessage
}

// EventType enumerates streaming event kinds.
type EventType int

const (
	// EventText carries a fragment of visible assistant text in Text.
	EventText EventType = iota + 1
	// EventReasoning carries a fragment of reasoning/thinking text in Text.
	EventReasoning
	// EventToolCallStart opens tool call number Index with ID and Name.
	EventToolCallStart
	// EventToolCallArgs carries a fragment of tool call Index's JSON
	// arguments in Text.
	EventToolCallArgs
	// EventDone ends the stream with FinishReason and (optionally) Usage.
	EventDone
	// EventError ends the stream with Err.
	EventError
)

func (t EventType) String() string {
	switch t {
	case EventText:
		return "text"
	case EventReasoning:
		return "reasoning"
	case EventToolCallStart:
		return "tool_call_start"
	case EventToolCallArgs:
		return "tool_call_args"
	case EventDone:
		return "done"
	case EventError:
		return "error"
	}
	return "unknown"
}

// Event is one streaming event.
type Event struct {
	Type         EventType
	Text         string
	Index        int
	ID           string
	Name         string
	FinishReason string
	Usage        *Usage
	Err          error
}

// Usage reports token counts.
type Usage struct {
	PromptTokens       int `json:"prompt_tokens"`
	CompletionTokens   int `json:"completion_tokens"`
	ReasoningTokens    int `json:"reasoning_tokens,omitempty"`
	CachedPromptTokens int `json:"cached_prompt_tokens,omitempty"`
}

// Timings are the simulated local-runtime timings of one request. Frontends
// that report timings (Ollama's final chunk) use these, never cloud timings.
type Timings struct {
	Load             time.Duration // model load (0 when already resident)
	PromptEval       time.Duration // prefill of the uncached prompt span
	Eval             time.Duration // decode
	Total            time.Duration // request arrival to last token
	PromptEvalTokens int           // tokens actually prefilled (cache misses)
	EvalTokens       int           // tokens decoded
}

// Response is a complete (non-streamed) assistant response. It can be built
// by applying a stream of Events.
type Response struct {
	ID           string
	Model        string
	Content      string
	Reasoning    string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
}

// Apply accumulates one event into the response.
func (r *Response) Apply(ev Event) {
	switch ev.Type {
	case EventText:
		r.Content += ev.Text
	case EventReasoning:
		r.Reasoning += ev.Text
	case EventToolCallStart:
		for len(r.ToolCalls) <= ev.Index {
			r.ToolCalls = append(r.ToolCalls, ToolCall{})
		}
		if ev.ID != "" {
			r.ToolCalls[ev.Index].ID = ev.ID
		}
		if ev.Name != "" {
			r.ToolCalls[ev.Index].Name = ev.Name
		}
	case EventToolCallArgs:
		for len(r.ToolCalls) <= ev.Index {
			r.ToolCalls = append(r.ToolCalls, ToolCall{})
		}
		r.ToolCalls[ev.Index].Arguments += ev.Text
	case EventDone:
		r.FinishReason = ev.FinishReason
		if ev.Usage != nil {
			r.Usage = *ev.Usage
		}
	case EventError:
		r.FinishReason = FinishError
	}
}

// CanonicalJSON re-encodes a JSON document with sorted keys and no
// insignificant whitespace, so tool-call arguments compare equal regardless
// of how a client re-serialised them. Invalid JSON is returned unchanged.
func CanonicalJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	var v any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if err := dec.Decode(&v); err != nil {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return s
	}
	return string(b)
}
