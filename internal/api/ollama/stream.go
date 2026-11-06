package ollama

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

var errClosed = errors.New("ollama: stream already finished")

// ---- wire types ---------------------------------------------------------

type outMessage struct {
	Role      string        `json:"role"`
	Content   string        `json:"content"`
	Thinking  string        `json:"thinking,omitempty"`
	ToolCalls []outToolCall `json:"tool_calls,omitempty"`
}

type outToolCall struct {
	ID       string      `json:"id,omitempty"`
	Function outFunction `json:"function"`
}

type outFunction struct {
	Index     int             `json:"index"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// metrics are the final-chunk timing fields (always present on done lines).
type metrics struct {
	TotalDuration      int64 `json:"total_duration"`
	LoadDuration       int64 `json:"load_duration"`
	PromptEvalCount    int   `json:"prompt_eval_count"`
	PromptEvalDuration int64 `json:"prompt_eval_duration"`
	EvalCount          int   `json:"eval_count"`
	EvalDuration       int64 `json:"eval_duration"`
}

type chatChunk struct {
	Model      string     `json:"model"`
	CreatedAt  string     `json:"created_at"`
	Message    outMessage `json:"message"`
	Done       bool       `json:"done"`
	DoneReason string     `json:"done_reason,omitempty"`
	*metrics
}

type generateChunk struct {
	Model      string          `json:"model"`
	CreatedAt  string          `json:"created_at"`
	Response   string          `json:"response"`
	Thinking   string          `json:"thinking,omitempty"`
	Done       bool            `json:"done"`
	DoneReason string          `json:"done_reason,omitempty"`
	Context    json.RawMessage `json:"context,omitempty"`
	*metrics
}

var emptyContext = json.RawMessage("[]")

func newMetrics(u llm.Usage, t llm.Timings) *metrics {
	m := &metrics{
		TotalDuration:      t.Total.Nanoseconds(),
		LoadDuration:       t.Load.Nanoseconds(),
		PromptEvalCount:    u.PromptTokens,
		PromptEvalDuration: t.PromptEval.Nanoseconds(),
		EvalCount:          u.CompletionTokens,
		EvalDuration:       t.Eval.Nanoseconds(),
	}
	if m.PromptEvalCount == 0 {
		m.PromptEvalCount = t.PromptEvalTokens
	}
	if m.EvalCount == 0 {
		m.EvalCount = t.EvalTokens
	}
	if parts := m.LoadDuration + m.PromptEvalDuration + m.EvalDuration; m.TotalDuration < parts {
		m.TotalDuration = parts
	}
	return m
}

// doneReason maps an llm.Finish* reason to Ollama's done_reason. Ollama
// reports "stop" after tool calls; unknown reasons ("load", "unload") pass
// through.
func doneReason(r string) string {
	switch r {
	case "", llm.FinishStop, llm.FinishToolCalls, llm.FinishError:
		return "stop"
	case llm.FinishLength:
		return "length"
	}
	return r
}

func timestamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// toolArguments renders JSON argument text as an object for Ollama clients
// (which decode arguments into a map). Invalid or non-object JSON yields {}.
func toolArguments(args string) json.RawMessage {
	trimmed := bytes.TrimSpace([]byte(args))
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
		return json.RawMessage("{}")
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, trimmed); err != nil {
		return json.RawMessage("{}")
	}
	return buf.Bytes()
}

func newCallID() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("call_%x", time.Now().UnixNano())
	}
	return "call_" + hex.EncodeToString(b)
}

// ---- stream -------------------------------------------------------------

// NewStream implements api.Frontend for /api/chat.
func (chatFrontend) NewStream(w http.ResponseWriter, meta api.StreamMeta) api.Stream {
	return newStream(w, meta, false)
}

// NewStream implements api.Frontend for /api/generate.
func (generateFrontend) NewStream(w http.ResponseWriter, meta api.StreamMeta) api.Stream {
	return newStream(w, meta, true)
}

func newStream(w http.ResponseWriter, meta api.StreamMeta, generate bool) *stream {
	f, _ := w.(http.Flusher)
	return &stream{w: w, flusher: f, model: meta.Model, generate: generate, now: time.Now, done: map[int]bool{}}
}

type stream struct {
	w        http.ResponseWriter
	flusher  http.Flusher
	model    string
	generate bool
	now      func() time.Time
	started  bool
	closed   bool
	pending  *pendingCall
	done     map[int]bool // tool indexes already emitted
}

type pendingCall struct {
	index    int
	id, name string
	args     strings.Builder
}

func (s *stream) writeLine(v any) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil { // Encode appends '\n'
		return err
	}
	if _, err := s.w.Write(buf.Bytes()); err != nil {
		return err
	}
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

// Start implements api.Stream: NDJSON headers, no preamble.
func (s *stream) Start() error {
	if s.started {
		return nil
	}
	s.started = true
	h := s.w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	s.w.WriteHeader(http.StatusOK)
	if s.flusher != nil {
		s.flusher.Flush()
	}
	return nil
}

func (s *stream) chunk(content, thinking string) error {
	ts := timestamp(s.now())
	if s.generate {
		return s.writeLine(generateChunk{Model: s.model, CreatedAt: ts, Response: content, Thinking: thinking})
	}
	return s.writeLine(chatChunk{Model: s.model, CreatedAt: ts,
		Message: outMessage{Role: "assistant", Content: content, Thinking: thinking}})
}

// Send implements api.Stream.
func (s *stream) Send(ev llm.Event) error {
	if s.closed {
		return errClosed
	}
	if err := s.Start(); err != nil {
		return err
	}
	switch ev.Type {
	case llm.EventText, llm.EventReasoning:
		if ev.Text == "" {
			return nil
		}
		if err := s.flushPending(); err != nil {
			return err
		}
		if ev.Type == llm.EventText {
			return s.chunk(ev.Text, "")
		}
		return s.chunk("", ev.Text)
	case llm.EventToolCallStart:
		if s.generate {
			return nil // /api/generate has no tool calls
		}
		if p := s.pending; p != nil && p.index == ev.Index {
			if p.id == "" {
				p.id = ev.ID
			}
			if p.name == "" {
				p.name = ev.Name
			}
			return nil
		}
		if s.done[ev.Index] {
			return nil
		}
		if err := s.flushPending(); err != nil {
			return err
		}
		s.pending = &pendingCall{index: ev.Index, id: ev.ID, name: ev.Name}
		return nil
	case llm.EventToolCallArgs:
		if s.generate {
			return nil
		}
		if s.pending == nil || s.pending.index != ev.Index {
			if s.done[ev.Index] {
				return nil // late fragment for an emitted call: cannot be amended
			}
			if err := s.flushPending(); err != nil {
				return err
			}
			s.pending = &pendingCall{index: ev.Index, id: ev.ID, name: ev.Name}
		}
		s.pending.args.WriteString(ev.Text)
		return nil
	}
	return fmt.Errorf("ollama: unsupported stream event %s", ev.Type)
}

// flushPending emits the buffered tool call, whole, as one line.
func (s *stream) flushPending() error {
	p := s.pending
	if p == nil {
		return nil
	}
	s.pending = nil
	s.done[p.index] = true
	id := p.id
	if id == "" {
		id = newCallID()
	}
	return s.writeLine(chatChunk{
		Model: s.model, CreatedAt: timestamp(s.now()),
		Message: outMessage{Role: "assistant", Content: "", ToolCalls: []outToolCall{{
			ID: id, Function: outFunction{Index: p.index, Name: p.name, Arguments: toolArguments(p.args.String())},
		}}},
	})
}

// Finish implements api.Stream: flushes a buffered tool call, then writes
// the done line with simulated timings.
func (s *stream) Finish(finishReason string, usage llm.Usage, t llm.Timings) error {
	if s.closed {
		return errClosed
	}
	if err := s.Start(); err != nil {
		return err
	}
	if finishReason == llm.FinishError {
		return s.Fail("api_error", "generation failed")
	}
	if err := s.flushPending(); err != nil {
		return err
	}
	s.closed = true
	ts := timestamp(s.now())
	m := newMetrics(usage, t)
	if s.generate {
		return s.writeLine(generateChunk{Model: s.model, CreatedAt: ts, Done: true,
			DoneReason: doneReason(finishReason), Context: emptyContext, metrics: m})
	}
	return s.writeLine(chatChunk{Model: s.model, CreatedAt: ts,
		Message: outMessage{Role: "assistant"}, Done: true, DoneReason: doneReason(finishReason), metrics: m})
}

// Fail implements api.Stream: a {"error":"..."} line.
func (s *stream) Fail(_ string, message string) error {
	if s.closed {
		return errClosed
	}
	if err := s.Start(); err != nil {
		return err
	}
	s.closed = true
	s.pending = nil
	return s.writeLine(errorBody{Error: message})
}

// ---- non-streaming ------------------------------------------------------

func outToolCalls(calls []llm.ToolCall) []outToolCall {
	var out []outToolCall
	for i, tc := range calls {
		id := tc.ID
		if id == "" {
			id = newCallID()
		}
		out = append(out, outToolCall{ID: id, Function: outFunction{Index: i, Name: tc.Name, Arguments: toolArguments(tc.Arguments)}})
	}
	return out
}

func modelName(meta api.StreamMeta, resp *llm.Response) string {
	if meta.Model != "" {
		return meta.Model
	}
	return resp.Model
}

// WriteResponse implements api.Frontend for /api/chat (stream:false).
func (chatFrontend) WriteResponse(w http.ResponseWriter, meta api.StreamMeta, resp *llm.Response, t llm.Timings) error {
	return api.WriteJSON(w, http.StatusOK, chatChunk{
		Model:     modelName(meta, resp),
		CreatedAt: timestamp(time.Now()),
		Message: outMessage{
			Role: "assistant", Content: resp.Content, Thinking: resp.Reasoning,
			ToolCalls: outToolCalls(resp.ToolCalls),
		},
		Done:       true,
		DoneReason: doneReason(resp.FinishReason),
		metrics:    newMetrics(resp.Usage, t),
	})
}

// WriteResponse implements api.Frontend for /api/generate (stream:false).
func (generateFrontend) WriteResponse(w http.ResponseWriter, meta api.StreamMeta, resp *llm.Response, t llm.Timings) error {
	return api.WriteJSON(w, http.StatusOK, generateChunk{
		Model:      modelName(meta, resp),
		CreatedAt:  timestamp(time.Now()),
		Response:   resp.Content,
		Thinking:   resp.Reasoning,
		Done:       true,
		DoneReason: doneReason(resp.FinishReason),
		Context:    emptyContext,
		metrics:    newMetrics(resp.Usage, t),
	})
}
