// Package api defines the Frontend interface implemented by the wire
// protocols the simulated runtime speaks. Implementations live in
// subpackages: api/openai (Chat Completions + /v1/models), api/anthropic
// (Messages), api/responses (OpenAI Responses) and api/ollama (native
// /api/chat, /api/generate and discovery endpoints). Each decodes HTTP
// request bodies into llm.Request and encodes llm.Event streams back into
// the protocol.
//
// Frontends never sleep or pace: the server re-times events and calls
// Stream.Send at the simulated release time. Every Send must reach the
// client immediately (flush after each write).
package api

import (
	"net/http"
	"time"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

// StreamMeta describes the response being produced.
type StreamMeta struct {
	// ID is a unique response/message id (frontends may prefix it).
	ID string
	// Model is the model name echoed to the client.
	Model string
	// Created is the response creation time.
	Created time.Time
	// PromptTokens is the best-known prompt token count at stream start
	// (Anthropic's message_start carries input_tokens).
	PromptTokens int
	// IncludeUsage mirrors llm.Request.IncludeUsage.
	IncludeUsage bool
}

// Frontend is one wire protocol.
type Frontend interface {
	// Name returns the protocol identifier (llm.Protocol* constants).
	Name() string
	// DecodeRequest parses a request body. It returns an error suitable for
	// WriteError(w, 400, ...) when the body is invalid.
	DecodeRequest(body []byte) (*llm.Request, error)
	// NewStream prepares a streaming response on w. Headers are written by
	// Start.
	NewStream(w http.ResponseWriter, meta StreamMeta) Stream
	// WriteResponse writes a complete non-streaming response.
	WriteResponse(w http.ResponseWriter, meta StreamMeta, resp *llm.Response, t llm.Timings) error
	// WriteError writes a protocol-shaped error response with HTTP status.
	// errType is a short machine-readable kind such as
	// "invalid_request_error", "context_length_exceeded", "overloaded_error"
	// or "api_error"; frontends map it to their vocabulary.
	WriteError(w http.ResponseWriter, status int, errType, message string)
}

// Stream encodes one streaming response.
type Stream interface {
	// Start writes headers and any protocol preamble (e.g. Anthropic
	// message_start, Responses response.created). Called exactly once,
	// before the first Send, at the simulated time the response begins.
	Start() error
	// Send encodes one event of type EventText, EventReasoning,
	// EventToolCallStart or EventToolCallArgs, then flushes. Fragments can
	// be any size (a single token or many). Tool calls are started with
	// EventToolCallStart (Index, ID, Name) before their argument fragments;
	// tool call indexes start at 0 and increase. Text, reasoning and tool
	// calls may interleave in any order; encoders open and close protocol
	// content blocks as the event kind changes.
	Send(ev llm.Event) error
	// Finish closes the stream normally with an llm.Finish* reason, final
	// usage and simulated timings, then flushes.
	Finish(finishReason string, usage llm.Usage, t llm.Timings) error
	// Fail ends the stream with an error after Start (mid-stream failure).
	Fail(errType, message string) error
}

// ModelInfo describes the simulated model for discovery endpoints
// (/v1/models, Ollama /api/tags and /api/show).
type ModelInfo struct {
	// Name is the name clients use, e.g. "gpt-oss:20b".
	Name string
	// Family is the architecture family, e.g. "gptoss", "qwen3moe", "llama".
	Family string
	// ParameterSize is a display size, e.g. "20.9B".
	ParameterSize string
	// Quantization is the quantization label, e.g. "MXFP4", "Q4_K_M".
	Quantization string
	// ContextLength is the simulated context window in tokens.
	ContextLength int
	// SizeBytes is the model file size.
	SizeBytes int64
	// Capabilities, e.g. ["completion","tools","thinking"].
	Capabilities []string
	// ModifiedAt is reported as the model's modification time.
	ModifiedAt time.Time
}
