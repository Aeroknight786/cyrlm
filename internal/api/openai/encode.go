package openai

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/llm"
)

// ---- wire types ----

type chunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *usageJSON    `json:"usage,omitempty"`
	Timings *timingsJSON  `json:"timings,omitempty"`
}

type chunkChoice struct {
	Index        int             `json:"index"`
	Delta        delta           `json:"delta"`
	Logprobs     json.RawMessage `json:"logprobs"`
	FinishReason *string         `json:"finish_reason"`
}

type delta struct {
	Role             string          `json:"role,omitempty"`
	Content          *string         `json:"content,omitempty"`
	ReasoningContent *string         `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCallDelta `json:"tool_calls,omitempty"`
}

type toolCallDelta struct {
	Index    int           `json:"index"`
	ID       string        `json:"id,omitempty"`
	Type     string        `json:"type,omitempty"`
	Function functionDelta `json:"function"`
}

type functionDelta struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments"`
}

type usageJSON struct {
	PromptTokens        int `json:"prompt_tokens"`
	CompletionTokens    int `json:"completion_tokens"`
	TotalTokens         int `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
	CompletionTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"completion_tokens_details"`
}

// timingsJSON is llama-server's per-request timings object.
type timingsJSON struct {
	CacheN              int     `json:"cache_n"`
	PromptN             int     `json:"prompt_n"`
	PromptMS            float64 `json:"prompt_ms"`
	PromptPerTokenMS    float64 `json:"prompt_per_token_ms"`
	PromptPerSecond     float64 `json:"prompt_per_second"`
	PredictedN          int     `json:"predicted_n"`
	PredictedMS         float64 `json:"predicted_ms"`
	PredictedPerTokenMS float64 `json:"predicted_per_token_ms"`
	PredictedPerSecond  float64 `json:"predicted_per_second"`
}

type completion struct {
	ID      string             `json:"id"`
	Object  string             `json:"object"`
	Created int64              `json:"created"`
	Model   string             `json:"model"`
	Choices []completionChoice `json:"choices"`
	Usage   usageJSON          `json:"usage"`
	Timings *timingsJSON       `json:"timings,omitempty"`
}

type completionChoice struct {
	Index        int             `json:"index"`
	Message      respMessage     `json:"message"`
	Logprobs     json.RawMessage `json:"logprobs"`
	FinishReason string          `json:"finish_reason"`
}

type respMessage struct {
	Role             string         `json:"role"`
	Content          *string        `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	ToolCalls        []respToolCall `json:"tool_calls,omitempty"`
}

type respToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function respFunction `json:"function"`
}

type respFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

type modelList struct {
	Object string      `json:"object"`
	Data   []modelJSON `json:"data"`
}

type modelJSON struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	OwnedBy string `json:"owned_by"`
}

// ---- streaming ----

type stream struct {
	sse      *api.SSEWriter
	meta     api.StreamMeta
	id       string
	created  int64
	started  bool
	finished bool
	tools    map[int]bool // tool-call indexes already announced

	text, reasoning utf8Carry
	args            map[int]*utf8Carry
}

// NewStream implements api.Frontend.
func (f *Frontend) NewStream(w http.ResponseWriter, meta api.StreamMeta) api.Stream {
	return &stream{
		sse:     api.NewSSEWriter(w),
		meta:    meta,
		id:      completionID(meta.ID),
		created: unixSeconds(meta.Created),
		tools:   map[int]bool{},
		args:    map[int]*utf8Carry{},
	}
}

func (s *stream) chunk(d delta, finish *string) chunk {
	return chunk{
		ID:      s.id,
		Object:  "chat.completion.chunk",
		Created: s.created,
		Model:   s.meta.Model,
		Choices: []chunkChoice{{Index: 0, Delta: d, FinishReason: finish}},
	}
}

// Start writes SSE headers and the role chunk.
func (s *stream) Start() error {
	if s.started {
		return nil
	}
	s.started = true
	empty := ""
	return s.sse.Event("", s.chunk(delta{Role: "assistant", Content: &empty}, nil))
}

// Send implements api.Stream.
func (s *stream) Send(ev llm.Event) error {
	if s.finished {
		return errors.New("openai: send after stream finished")
	}
	if !s.started {
		if err := s.Start(); err != nil {
			return err
		}
	}
	switch ev.Type {
	case llm.EventText:
		return s.sendText(s.text.take(ev.Text))
	case llm.EventReasoning:
		return s.sendReasoning(s.reasoning.take(ev.Text))
	case llm.EventToolCallStart:
		if s.tools[ev.Index] {
			return nil
		}
		return s.startTool(ev.Index, ev.ID, ev.Name)
	case llm.EventToolCallArgs:
		if !s.tools[ev.Index] {
			if err := s.startTool(ev.Index, ev.ID, ev.Name); err != nil {
				return err
			}
		}
		return s.sendArgs(ev.Index, s.args[ev.Index].take(ev.Text))
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
	return fmt.Errorf("openai: unsupported event type %v", ev.Type)
}

func (s *stream) sendText(text string) error {
	if text == "" {
		return nil
	}
	return s.sse.Event("", s.chunk(delta{Content: &text}, nil))
}

func (s *stream) sendReasoning(text string) error {
	if text == "" {
		return nil
	}
	return s.sse.Event("", s.chunk(delta{ReasoningContent: &text}, nil))
}

func (s *stream) sendArgs(index int, frag string) error {
	if frag == "" {
		return nil
	}
	return s.sse.Event("", s.chunk(delta{ToolCalls: []toolCallDelta{{
		Index:    index,
		Function: functionDelta{Arguments: frag},
	}}}, nil))
}

// flushPending emits bytes still held back by the UTF-8 carries (only
// non-empty when a stream ends inside a rune).
func (s *stream) flushPending() error {
	if err := s.sendReasoning(s.reasoning.flush()); err != nil {
		return err
	}
	if err := s.sendText(s.text.flush()); err != nil {
		return err
	}
	idx := make([]int, 0, len(s.args))
	for i := range s.args {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	for _, i := range idx {
		if err := s.sendArgs(i, s.args[i].flush()); err != nil {
			return err
		}
	}
	return nil
}

func (s *stream) startTool(index int, id, name string) error {
	if id == "" {
		id = randomID("call_")
	}
	s.tools[index] = true
	s.args[index] = &utf8Carry{}
	return s.sse.Event("", s.chunk(delta{ToolCalls: []toolCallDelta{{
		Index:    index,
		ID:       id,
		Type:     "function",
		Function: functionDelta{Name: name, Arguments: ""},
	}}}, nil))
}

// Finish writes the finish chunk (with timings), the optional usage chunk
// and the [DONE] sentinel.
func (s *stream) Finish(finishReason string, usage llm.Usage, t llm.Timings) error {
	if s.finished {
		return nil
	}
	if !s.started {
		if err := s.Start(); err != nil {
			return err
		}
	}
	if err := s.flushPending(); err != nil {
		return err
	}
	s.finished = true
	reason := finishReasonFor(finishReason, len(s.tools) > 0)
	c := s.chunk(delta{}, &reason)
	c.Timings = timingsFor(t, usage.CachedPromptTokens)
	if err := s.sse.Event("", c); err != nil {
		return err
	}
	if s.meta.IncludeUsage {
		u := usageFor(usage)
		uc := chunk{
			ID:      s.id,
			Object:  "chat.completion.chunk",
			Created: s.created,
			Model:   s.meta.Model,
			Choices: []chunkChoice{},
			Usage:   &u,
		}
		if err := s.sse.Event("", uc); err != nil {
			return err
		}
	}
	return s.sse.Raw("[DONE]")
}

// Fail writes an error chunk followed by [DONE].
func (s *stream) Fail(errType, message string) error {
	if s.finished {
		return nil
	}
	s.finished = true
	if err := s.sse.Event("", errorBody{Error: errorFor(errType, message)}); err != nil {
		return err
	}
	return s.sse.Raw("[DONE]")
}

// ---- non-streaming ----

// WriteResponse implements api.Frontend.
func (f *Frontend) WriteResponse(w http.ResponseWriter, meta api.StreamMeta, resp *llm.Response, t llm.Timings) error {
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
	msg := respMessage{Role: "assistant", ReasoningContent: resp.Reasoning}
	for _, tc := range resp.ToolCalls {
		callID := tc.ID
		if callID == "" {
			callID = randomID("call_")
		}
		args := tc.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		msg.ToolCalls = append(msg.ToolCalls, respToolCall{
			ID:       callID,
			Type:     "function",
			Function: respFunction{Name: tc.Name, Arguments: args},
		})
	}
	if resp.Content != "" || len(msg.ToolCalls) == 0 {
		content := resp.Content
		msg.Content = &content
	}
	out := completion{
		ID:      completionID(id),
		Object:  "chat.completion",
		Created: unixSeconds(meta.Created),
		Model:   model,
		Choices: []completionChoice{{
			Index:        0,
			Message:      msg,
			FinishReason: finishReasonFor(resp.FinishReason, len(msg.ToolCalls) > 0),
		}},
		Usage:   usageFor(resp.Usage),
		Timings: timingsFor(t, resp.Usage.CachedPromptTokens),
	}
	return api.WriteJSON(w, http.StatusOK, out)
}

// WriteError implements api.Frontend.
func (f *Frontend) WriteError(w http.ResponseWriter, status int, errType, message string) {
	_ = api.WriteJSON(w, status, errorBody{Error: errorFor(errType, message)})
}

// WriteModels writes the GET /v1/models listing.
func WriteModels(w http.ResponseWriter, models []api.ModelInfo) {
	out := modelList{Object: "list", Data: []modelJSON{}}
	for _, m := range models {
		out.Data = append(out.Data, modelJSON{
			ID:      m.Name,
			Object:  "model",
			Created: unixSeconds(m.ModifiedAt),
			OwnedBy: "cyrlm",
		})
	}
	_ = api.WriteJSON(w, http.StatusOK, out)
}

// ---- helpers ----

func errorFor(errType, message string) errorDetail {
	d := errorDetail{Message: message, Type: errType}
	switch errType {
	case "context_length_exceeded":
		code := "context_length_exceeded"
		d.Type = "invalid_request_error"
		d.Code = &code
	case "":
		d.Type = "api_error"
	}
	return d
}

// finishReasonFor maps an llm.Finish* reason to Chat Completions. A stream
// that emitted tool calls and stopped normally reports "tool_calls";
// truncation ("length") is preserved.
func finishReasonFor(reason string, sawTools bool) string {
	switch reason {
	case llm.FinishLength, "content_filter":
		return reason
	case llm.FinishToolCalls:
		return llm.FinishToolCalls
	}
	if sawTools {
		return llm.FinishToolCalls
	}
	return llm.FinishStop
}

func usageFor(u llm.Usage) usageJSON {
	var out usageJSON
	out.PromptTokens = u.PromptTokens
	out.CompletionTokens = u.CompletionTokens
	out.TotalTokens = u.PromptTokens + u.CompletionTokens
	out.PromptTokensDetails.CachedTokens = u.CachedPromptTokens
	out.CompletionTokensDetails.ReasoningTokens = u.ReasoningTokens
	return out
}

func timingsFor(t llm.Timings, cached int) *timingsJSON {
	out := &timingsJSON{
		CacheN:      cached,
		PromptN:     t.PromptEvalTokens,
		PromptMS:    ms(t.PromptEval),
		PredictedN:  t.EvalTokens,
		PredictedMS: ms(t.Eval),
	}
	out.PromptPerTokenMS, out.PromptPerSecond = rates(out.PromptMS, out.PromptN)
	out.PredictedPerTokenMS, out.PredictedPerSecond = rates(out.PredictedMS, out.PredictedN)
	return out
}

// rates returns ms-per-token and tokens-per-second, 0 when undefined (JSON
// cannot encode Inf/NaN).
func rates(totalMS float64, n int) (perTokenMS, perSecond float64) {
	if n > 0 {
		perTokenMS = totalMS / float64(n)
	}
	if totalMS > 0 {
		perSecond = float64(n) * 1000 / totalMS
	}
	return perTokenMS, perSecond
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

func completionID(id string) string {
	if id == "" {
		return randomID("chatcmpl-")
	}
	if strings.HasPrefix(id, "chatcmpl-") {
		return id
	}
	return "chatcmpl-" + id
}

func unixSeconds(t time.Time) int64 {
	if t.IsZero() {
		return time.Now().Unix()
	}
	return t.Unix()
}

func randomID(prefix string) string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return prefix + hex.EncodeToString(b[:])
}
