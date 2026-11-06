package responses

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

var errClosed = errors.New("responses: stream already finished")

// NewStream implements api.Frontend.
func (f *Frontend) NewStream(w http.ResponseWriter, meta api.StreamMeta) api.Stream {
	id, base := responseID(meta)
	return &stream{
		f:     f,
		sse:   api.NewSSEWriter(w),
		meta:  meta,
		id:    id,
		base:  base,
		tools: map[int]*toolState{},
	}
}

// stream encodes llm.Events as Responses API server-sent events. Exactly one
// output item is open at a time; a change of event kind closes it.
type stream struct {
	f       *Frontend
	sse     *api.SSEWriter
	meta    api.StreamMeta
	id      string
	base    string
	seq     int
	started bool
	closed  bool
	// output holds closed items in output_index order (pointers, so late
	// tool-argument fragments can still fix up the final response).
	output []any
	cur    *openItem
	tools  map[int]*toolState // keyed by llm.Event.Index
}

type openItem struct {
	kind  llm.EventType // EventText, EventReasoning or EventToolCallStart
	index int           // output_index
	id    string
	text  strings.Builder
	tool  *toolState
}

type toolState struct {
	custom bool
	args   strings.Builder
	fc     *functionCallItem
	ctc    *customToolCallItem
}

func (t *toolState) itemID() string {
	if t.custom {
		return t.ctc.ID
	}
	return t.fc.ID
}

// sync copies the accumulated arguments into the item.
func (t *toolState) sync() {
	if t.custom {
		t.ctc.Input = customInput(t.args.String())
	} else {
		t.fc.Arguments = t.args.String()
	}
}

func (t *toolState) setStatus(s string) {
	if t.custom {
		t.ctc.Status = s
	} else {
		t.fc.Status = s
	}
}

func (t *toolState) item() any {
	if t.custom {
		return t.ctc
	}
	return t.fc
}

func (s *stream) emit(name string, ev *event) error {
	ev.Type = name
	ev.SequenceNumber = s.seq
	s.seq++
	return s.sse.Event(name, ev)
}

func (s *stream) snapshot(status string) *responseObject {
	r := newResponseObject(s.meta, s.id, status)
	r.Output = append(r.Output, s.output...)
	return r
}

// Start implements api.Stream.
func (s *stream) Start() error {
	if s.started {
		return nil
	}
	s.started = true
	if err := s.emit("response.created", &event{Response: s.snapshot("in_progress")}); err != nil {
		return err
	}
	return s.emit("response.in_progress", &event{Response: s.snapshot("in_progress")})
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
		if s.cur == nil || s.cur.kind != ev.Type {
			if err := s.closeCurrent("completed"); err != nil {
				return err
			}
			if err := s.openTextual(ev.Type); err != nil {
				return err
			}
		}
		s.cur.text.WriteString(ev.Text)
		name := "response.output_text.delta"
		e := &event{ItemID: s.cur.id, OutputIndex: intp(s.cur.index), ContentIndex: intp(0), Delta: strp(ev.Text)}
		if ev.Type == llm.EventText {
			e.Logprobs = emptyLogprobs
		} else {
			name = "response.reasoning_text.delta"
		}
		return s.emit(name, e)

	case llm.EventToolCallStart:
		if t, ok := s.tools[ev.Index]; ok {
			// Repeated start for a known call: fill in missing fields.
			if ev.Name != "" && !t.custom && t.fc.Name == "" {
				t.fc.Namespace, t.fc.Name = s.f.split(ev.Name)
			}
			return nil
		}
		if err := s.closeCurrent("completed"); err != nil {
			return err
		}
		return s.openTool(ev.Index, ev.ID, ev.Name)

	case llm.EventToolCallArgs:
		t, ok := s.tools[ev.Index]
		if !ok {
			// Arguments without a start: open the call implicitly.
			if err := s.closeCurrent("completed"); err != nil {
				return err
			}
			if err := s.openTool(ev.Index, ev.ID, ev.Name); err != nil {
				return err
			}
			t = s.tools[ev.Index]
		}
		if ev.Text == "" {
			return nil
		}
		t.args.WriteString(ev.Text)
		if s.cur == nil || s.cur.tool != t {
			// Late fragment for an already-closed call: its done events are
			// out, so only the final response object can reflect it.
			t.sync()
			return nil
		}
		if t.custom {
			return nil // JSON arguments are buffered; input is emitted at close
		}
		return s.emit("response.function_call_arguments.delta", &event{
			ItemID: t.fc.ID, OutputIndex: intp(s.cur.index), Delta: strp(ev.Text),
		})
	}
	return fmt.Errorf("responses: unsupported stream event %s", ev.Type)
}

func (s *stream) openTextual(kind llm.EventType) error {
	idx := len(s.output)
	c := &openItem{kind: kind, index: idx}
	s.cur = c
	if kind == llm.EventReasoning {
		c.id = itemID("rs", s.base, idx)
		return s.emit("response.output_item.added", &event{
			OutputIndex: intp(idx),
			Item:        &reasoningItem{Type: "reasoning", ID: c.id, Summary: []reasoningText{}, Content: []reasoningText{}},
		})
	}
	c.id = itemID("msg", s.base, idx)
	if err := s.emit("response.output_item.added", &event{
		OutputIndex: intp(idx),
		Item:        &messageItem{Type: "message", ID: c.id, Status: "in_progress", Role: "assistant", Content: []outputText{}},
	}); err != nil {
		return err
	}
	return s.emit("response.content_part.added", &event{
		ItemID: c.id, OutputIndex: intp(idx), ContentIndex: intp(0),
		Part: &outputText{Type: "output_text", Text: "", Annotations: []any{}},
	})
}

func (s *stream) openTool(index int, callID, name string) error {
	idx := len(s.output)
	if callID == "" {
		callID = newCallID()
	}
	t := &toolState{custom: name != "" && s.f.isCustom(name)}
	ns, member := s.f.split(name)
	var item any
	if t.custom {
		t.ctc = &customToolCallItem{Type: "custom_tool_call", ID: itemID("ctc", s.base, idx), CallID: callID, Name: member, Namespace: ns, Status: "in_progress"}
		item = *t.ctc // snapshot: the added event shows the empty item
	} else {
		t.fc = &functionCallItem{Type: "function_call", ID: itemID("fc", s.base, idx), CallID: callID, Name: member, Namespace: ns, Status: "in_progress"}
		item = *t.fc
	}
	s.tools[index] = t
	s.cur = &openItem{kind: llm.EventToolCallStart, index: idx, id: t.itemID(), tool: t}
	return s.emit("response.output_item.added", &event{OutputIndex: intp(idx), Item: item})
}

// closedItem builds the final form of the open item c with status.
func (s *stream) closedItem(c *openItem, status string) any {
	switch c.kind {
	case llm.EventReasoning:
		return &reasoningItem{
			Type: "reasoning", ID: c.id, Summary: []reasoningText{},
			Content: []reasoningText{{Type: "reasoning_text", Text: c.text.String()}},
		}
	case llm.EventText:
		return &messageItem{
			Type: "message", ID: c.id, Status: status, Role: "assistant",
			Content: []outputText{{Type: "output_text", Text: c.text.String(), Annotations: []any{}}},
		}
	}
	c.tool.sync()
	c.tool.setStatus(status)
	return c.tool.item()
}

// closeCurrent emits the done events of the open item, if any.
func (s *stream) closeCurrent(status string) error {
	c := s.cur
	if c == nil {
		return nil
	}
	s.cur = nil
	item := s.closedItem(c, status)
	s.output = append(s.output, item)
	idx := intp(c.index)
	switch c.kind {
	case llm.EventReasoning:
		if err := s.emit("response.reasoning_text.done", &event{
			ItemID: c.id, OutputIndex: idx, ContentIndex: intp(0), Text: strp(c.text.String()),
		}); err != nil {
			return err
		}
	case llm.EventText:
		text := c.text.String()
		if err := s.emit("response.output_text.done", &event{
			ItemID: c.id, OutputIndex: idx, ContentIndex: intp(0), Text: strp(text), Logprobs: emptyLogprobs,
		}); err != nil {
			return err
		}
		if err := s.emit("response.content_part.done", &event{
			ItemID: c.id, OutputIndex: idx, ContentIndex: intp(0),
			Part: &outputText{Type: "output_text", Text: text, Annotations: []any{}},
		}); err != nil {
			return err
		}
	default:
		t := c.tool
		if t.custom {
			if t.ctc.Input != "" {
				if err := s.emit("response.custom_tool_call_input.delta", &event{
					ItemID: c.id, OutputIndex: idx, Delta: strp(t.ctc.Input),
				}); err != nil {
					return err
				}
			}
			if err := s.emit("response.custom_tool_call_input.done", &event{
				ItemID: c.id, OutputIndex: idx, Input: strp(t.ctc.Input),
			}); err != nil {
				return err
			}
		} else if err := s.emit("response.function_call_arguments.done", &event{
			ItemID: c.id, OutputIndex: idx, Arguments: strp(t.fc.Arguments),
		}); err != nil {
			return err
		}
	}
	return s.emit("response.output_item.done", &event{OutputIndex: idx, Item: item})
}

// Finish implements api.Stream.
func (s *stream) Finish(finishReason string, usage llm.Usage, _ llm.Timings) error {
	if s.closed {
		return errClosed
	}
	if err := s.Start(); err != nil {
		return err
	}
	if finishReason == llm.FinishError {
		return s.Fail("api_error", "the response ended with an error")
	}
	var incomplete string
	switch finishReason {
	case llm.FinishLength:
		incomplete = "max_output_tokens"
	case "content_filter":
		incomplete = "content_filter"
	}
	itemStatus := "completed"
	if incomplete != "" {
		itemStatus = "incomplete"
	}
	if err := s.closeCurrent(itemStatus); err != nil {
		return err
	}
	s.closed = true
	if incomplete != "" {
		r := s.snapshot("incomplete")
		r.IncompleteDetails = &incompleteDetails{Reason: incomplete}
		r.Usage = convertUsage(usage)
		return s.emit("response.incomplete", &event{Response: r})
	}
	r := s.snapshot("completed")
	r.Usage = convertUsage(usage)
	return s.emit("response.completed", &event{Response: r})
}

// Fail implements api.Stream. The open item, if any, is reported in the
// failed response with status "incomplete" (no done events are emitted).
func (s *stream) Fail(errType, message string) error {
	if s.closed {
		return errClosed
	}
	if err := s.Start(); err != nil {
		return err
	}
	s.closed = true
	r := s.snapshot("failed")
	if c := s.cur; c != nil {
		s.cur = nil
		r.Output = append(r.Output, s.closedItem(c, "incomplete"))
	}
	r.Error = &responseError{Code: failureCode(errType), Message: message}
	return s.emit("response.failed", &event{Response: r})
}
