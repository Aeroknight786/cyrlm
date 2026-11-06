package anthropic

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

// SimulatedSignature is the placeholder signature attached to thinking
// blocks. Clients echo it back in history; DecodeRequest ignores it.
const SimulatedSignature = "trialm-simulated"

// ---- wire types ----

type usageJSON struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
}

type messageJSON struct {
	ID           string    `json:"id"`
	Type         string    `json:"type"`
	Role         string    `json:"role"`
	Model        string    `json:"model"`
	Content      []any     `json:"content"`
	StopReason   *string   `json:"stop_reason"`
	StopSequence *string   `json:"stop_sequence"`
	Usage        usageJSON `json:"usage"`
}

type messageStartEvent struct {
	Type    string      `json:"type"`
	Message messageJSON `json:"message"`
}

type typeOnly struct {
	Type string `json:"type"`
}

type blockStartEvent struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type blockDeltaEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type blockStopEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type messageDeltaEvent struct {
	Type  string `json:"type"`
	Delta struct {
		StopReason   string  `json:"stop_reason"`
		StopSequence *string `json:"stop_sequence"`
	} `json:"delta"`
	Usage usageJSON `json:"usage"`
}

type thinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type thinkingDelta struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type signatureDelta struct {
	Type      string `json:"type"`
	Signature string `json:"signature"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type inputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type errorBody struct {
	Type  string      `json:"type"`
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// ---- streaming ----

type blockKind int

const (
	kindNone blockKind = iota
	kindThinking
	kindText
	kindTool
)

type stream struct {
	sse      *api.SSEWriter
	meta     api.StreamMeta
	started  bool
	finished bool

	open    blockKind   // kind of the open content block, kindNone if none
	index   int         // index of the open block
	next    int         // index the next block will get
	curTool int         // event tool index of the open tool_use block
	tools   map[int]int // event tool index -> content block index

	text, reasoning utf8Carry
	args            map[int]*utf8Carry
}

// NewStream implements api.Frontend.
func (f *Frontend) NewStream(w http.ResponseWriter, meta api.StreamMeta) api.Stream {
	return &stream{sse: api.NewSSEWriter(w), meta: meta, tools: map[int]int{}, args: map[int]*utf8Carry{}}
}

// Start writes message_start and a ping.
func (s *stream) Start() error {
	if s.started {
		return nil
	}
	s.started = true
	ev := messageStartEvent{
		Type: "message_start",
		Message: messageJSON{
			ID:      messageID(s.meta.ID),
			Type:    "message",
			Role:    "assistant",
			Model:   s.meta.Model,
			Content: []any{},
			Usage:   usageJSON{InputTokens: s.meta.PromptTokens, OutputTokens: 1},
		},
	}
	if err := s.sse.Event("message_start", ev); err != nil {
		return err
	}
	return s.sse.Event("ping", typeOnly{Type: "ping"})
}

// Send implements api.Stream.
func (s *stream) Send(ev llm.Event) error {
	if s.finished {
		return errors.New("anthropic: send after stream finished")
	}
	if !s.started {
		if err := s.Start(); err != nil {
			return err
		}
	}
	switch ev.Type {
	case llm.EventReasoning:
		frag := s.reasoning.take(ev.Text)
		if frag == "" {
			return nil
		}
		if s.open != kindThinking {
			if err := s.openBlock(kindThinking, thinkingBlock{Type: "thinking"}); err != nil {
				return err
			}
		}
		return s.delta(s.index, thinkingDelta{Type: "thinking_delta", Thinking: frag})
	case llm.EventText:
		frag := s.text.take(ev.Text)
		if frag == "" {
			return nil
		}
		if s.open != kindText {
			if err := s.openBlock(kindText, textBlock{Type: "text"}); err != nil {
				return err
			}
		}
		return s.delta(s.index, textDelta{Type: "text_delta", Text: frag})
	case llm.EventToolCallStart:
		if _, ok := s.tools[ev.Index]; ok {
			return nil
		}
		return s.openTool(ev.Index, ev.ID, ev.Name)
	case llm.EventToolCallArgs:
		bi, ok := s.tools[ev.Index]
		if !ok {
			if err := s.openTool(ev.Index, ev.ID, ev.Name); err != nil {
				return err
			}
			bi = s.index
		}
		frag := s.args[ev.Index].take(ev.Text)
		if frag == "" {
			return nil
		}
		// Arguments for a tool block that was already closed cannot be
		// placed in a new block; they are delivered against the block's
		// original index, which index-keyed accumulators (the SDKs) handle.
		return s.delta(bi, inputJSONDelta{Type: "input_json_delta", PartialJSON: frag})
	case llm.EventDone:
		var u llm.Usage
		if ev.Usage != nil {
			u = *ev.Usage
		}
		return s.Finish(ev.FinishReason, u, llm.Timings{})
	case llm.EventError:
		msg := "upstream error"
		if ev.Err != nil {
			msg = ev.Err.Error()
		}
		return s.Fail("api_error", msg)
	}
	return fmt.Errorf("anthropic: unsupported event type %v", ev.Type)
}

func (s *stream) openTool(index int, id, name string) error {
	if id == "" {
		id = randomID("toolu_")
	}
	err := s.openBlock(kindTool, toolUseBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage("{}")})
	if err != nil {
		return err
	}
	s.curTool = index
	s.tools[index] = s.index
	s.args[index] = &utf8Carry{}
	return nil
}

func (s *stream) openBlock(kind blockKind, block any) error {
	if err := s.closeBlock(); err != nil {
		return err
	}
	s.open = kind
	s.index = s.next
	s.next++
	return s.sse.Event("content_block_start", blockStartEvent{
		Type:         "content_block_start",
		Index:        s.index,
		ContentBlock: block,
	})
}

func (s *stream) closeBlock() error {
	if s.open == kindNone {
		return nil
	}
	// Bytes still held back by a UTF-8 carry belong to this block.
	var err error
	switch s.open {
	case kindThinking:
		if rest := s.reasoning.flush(); rest != "" {
			err = s.delta(s.index, thinkingDelta{Type: "thinking_delta", Thinking: rest})
		}
		if err == nil {
			err = s.delta(s.index, signatureDelta{Type: "signature_delta", Signature: SimulatedSignature})
		}
	case kindText:
		if rest := s.text.flush(); rest != "" {
			err = s.delta(s.index, textDelta{Type: "text_delta", Text: rest})
		}
	case kindTool:
		if rest := s.args[s.curTool].flush(); rest != "" {
			err = s.delta(s.index, inputJSONDelta{Type: "input_json_delta", PartialJSON: rest})
		}
	}
	if err != nil {
		return err
	}
	s.open = kindNone
	return s.sse.Event("content_block_stop", blockStopEvent{Type: "content_block_stop", Index: s.index})
}

func (s *stream) delta(index int, d any) error {
	return s.sse.Event("content_block_delta", blockDeltaEvent{Type: "content_block_delta", Index: index, Delta: d})
}

// Finish closes the open block and writes message_delta and message_stop.
func (s *stream) Finish(finishReason string, usage llm.Usage, _ llm.Timings) error {
	if s.finished {
		return nil
	}
	if !s.started {
		if err := s.Start(); err != nil {
			return err
		}
	}
	s.finished = true
	if err := s.closeBlock(); err != nil {
		return err
	}
	md := messageDeltaEvent{Type: "message_delta", Usage: usageFor(usage)}
	md.Delta.StopReason = stopReasonFor(finishReason, len(s.tools) > 0)
	if err := s.sse.Event("message_delta", md); err != nil {
		return err
	}
	return s.sse.Event("message_stop", typeOnly{Type: "message_stop"})
}

// Fail writes an error event.
func (s *stream) Fail(errType, message string) error {
	if s.finished {
		return nil
	}
	s.finished = true
	return s.sse.Event("error", errorBody{Type: "error", Error: errorDetail{Type: errorTypeFor(errType), Message: message}})
}

// ---- non-streaming ----

// WriteResponse implements api.Frontend.
func (f *Frontend) WriteResponse(w http.ResponseWriter, meta api.StreamMeta, resp *llm.Response, _ llm.Timings) error {
	if resp == nil {
		resp = &llm.Response{}
	}
	id := meta.ID
	if id == "" {
		id = resp.ID
	}
	model := meta.Model
	if model == "" {
		model = resp.Model
	}
	content := []any{}
	if resp.Reasoning != "" {
		content = append(content, thinkingBlock{Type: "thinking", Thinking: resp.Reasoning, Signature: SimulatedSignature})
	}
	if resp.Content != "" {
		content = append(content, textBlock{Type: "text", Text: resp.Content})
	}
	for _, tc := range resp.ToolCalls {
		tid := tc.ID
		if tid == "" {
			tid = randomID("toolu_")
		}
		content = append(content, toolUseBlock{Type: "tool_use", ID: tid, Name: tc.Name, Input: toolInput(tc.Arguments)})
	}
	stop := stopReasonFor(resp.FinishReason, len(resp.ToolCalls) > 0)
	return api.WriteJSON(w, http.StatusOK, messageJSON{
		ID:         messageID(id),
		Type:       "message",
		Role:       "assistant",
		Model:      model,
		Content:    content,
		StopReason: &stop,
		Usage:      usageFor(resp.Usage),
	})
}

// WriteError implements api.Frontend.
func (f *Frontend) WriteError(w http.ResponseWriter, status int, errType, message string) {
	_ = api.WriteJSON(w, status, errorBody{Type: "error", Error: errorDetail{Type: errorTypeFor(errType), Message: message}})
}

// WriteCountTokens writes the POST /v1/messages/count_tokens response.
func (f *Frontend) WriteCountTokens(w http.ResponseWriter, inputTokens int) {
	_ = api.WriteJSON(w, http.StatusOK, struct {
		InputTokens int `json:"input_tokens"`
	}{inputTokens})
}

// ---- helpers ----

// toolInput returns Arguments as a JSON object, preserving the original
// key order; empty or unparsable arguments become {}.
func toolInput(args string) json.RawMessage {
	args = strings.TrimSpace(args)
	if args == "" || args[0] != '{' || !json.Valid([]byte(args)) {
		return json.RawMessage("{}")
	}
	return compact(json.RawMessage(args))
}

// stopReasonFor maps an llm.Finish* reason. Any emitted tool call makes a
// normal stop "tool_use"; truncation stays "max_tokens".
func stopReasonFor(reason string, sawTools bool) string {
	switch reason {
	case llm.FinishLength:
		return "max_tokens"
	case llm.FinishToolCalls:
		return "tool_use"
	case "content_filter":
		return "refusal"
	}
	if sawTools {
		return "tool_use"
	}
	return "end_turn"
}

// usageFor converts canonical usage (prompt tokens include cached ones) to
// Anthropic's, where input_tokens excludes cache reads.
func usageFor(u llm.Usage) usageJSON {
	in := u.PromptTokens - u.CachedPromptTokens
	if in < 0 {
		in = 0
	}
	return usageJSON{
		InputTokens:          in,
		OutputTokens:         u.CompletionTokens,
		CacheReadInputTokens: u.CachedPromptTokens,
	}
}

var knownErrorTypes = map[string]bool{
	"invalid_request_error": true,
	"authentication_error":  true,
	"billing_error":         true,
	"permission_error":      true,
	"not_found_error":       true,
	"request_too_large":     true,
	"rate_limit_error":      true,
	"timeout_error":         true,
	"api_error":             true,
	"overloaded_error":      true,
}

func errorTypeFor(errType string) string {
	if errType == "context_length_exceeded" {
		return "invalid_request_error"
	}
	if knownErrorTypes[errType] {
		return errType
	}
	return "api_error"
}

func messageID(id string) string {
	if id == "" {
		return randomID("msg_")
	}
	if strings.HasPrefix(id, "msg_") {
		return id
	}
	return "msg_" + id
}

func randomID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}
