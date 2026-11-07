// Package responses implements the OpenAI Responses API frontend
// (POST /v1/responses), the wire protocol Codex CLI speaks.
//
// The frontend is stateless: previous_response_id is rejected and clients
// must send the whole conversation in input (Codex does with store=false).
//
// # Custom (freeform) tools
//
// Codex declares apply_patch as {"type":"custom","format":{grammar}}: the
// model answers with a custom_tool_call whose input is raw text, not JSON.
// Upstream models only know JSON function tools, so DecodeRequest turns each
// custom tool into a function tool with a single required string property
// "input" (whose description carries the grammar) and marks the schema with
// "x-trialm-custom": true (see CustomToolMarker).
//
// Encoding needs the reverse mapping, but NewStream and WriteResponse never
// see the request. The Frontend therefore keeps a name -> custom registry
// (a sync.Map) populated by every DecodeRequest: a tool call whose name was
// last declared as a custom tool is encoded as a custom_tool_call whose
// input is the "input" member of the JSON arguments. Tool names are stable
// within a harness session, so a process-wide registry is sufficient; a name
// later re-declared as a plain function tool is removed from it.
package responses

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/llm"
)

// CustomToolMarker is the JSON-schema key set to true in the Parameters of
// tools decoded from Responses "custom" (freeform) tools. Upstream adapters
// with strict schema validation may strip it; the frontend does not rely on
// it when encoding.
const CustomToolMarker = "x-trialm-custom"

// Frontend is the Responses API frontend. Use New.
type Frontend struct {
	// custom maps tool name -> true for names last declared as custom
	// (freeform) tools. See the package documentation.
	custom sync.Map
	// ns maps a flattened tool name (namespace + NamespaceSep + name) to
	// its namespace and member name, so calls go back to the harness in
	// their namespace. Chat Completions backends have no namespaces.
	ns sync.Map
}

// NamespaceSep joins a namespace and a member tool name into the flat
// function name the backend sees.
const NamespaceSep = "__"

// split returns the namespace and member name of a flattened tool name.
func (f *Frontend) split(flat string) (namespace, name string) {
	if v, ok := f.ns.Load(flat); ok {
		p := v.([2]string)
		return p[0], p[1]
	}
	return "", flat
}

func flatName(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + NamespaceSep + name
}

// New returns the Responses API frontend.
func New() api.Frontend { return &Frontend{} }

// Name implements api.Frontend.
func (f *Frontend) Name() string { return llm.ProtocolResponses }

func (f *Frontend) isCustom(name string) bool {
	v, ok := f.custom.Load(name)
	return ok && v.(bool)
}

// DecodeRequest implements api.Frontend.
func (f *Frontend) DecodeRequest(body []byte) (*llm.Request, error) {
	var wr wireRequest
	if err := json.Unmarshal(body, &wr); err != nil {
		return nil, fmt.Errorf("invalid JSON body: %v", err)
	}
	if wr.PreviousResponseID != "" {
		return nil, errors.New("previous_response_id is not supported: this server does not store responses; send the full conversation in input (store=false)")
	}
	req := &llm.Request{
		Model:        wr.Model,
		Protocol:     llm.ProtocolResponses,
		Stream:       wr.Stream,
		Temperature:  wr.Temperature,
		TopP:         wr.TopP,
		IncludeUsage: true, // Responses always reports usage
	}
	if wr.MaxOutputTokens != nil && *wr.MaxOutputTokens > 0 {
		req.MaxTokens = *wr.MaxOutputTokens
	}
	if wr.Reasoning != nil {
		effort, ok := mapEffort(wr.Reasoning.Effort)
		if !ok {
			req.Warnings = append(req.Warnings, fmt.Sprintf("unknown reasoning.effort %q ignored", wr.Reasoning.Effort))
		}
		req.ReasoningEffort = effort
	}

	d := &decoder{req: req, callNames: map[string]string{}}
	if err := d.instructions(wr.Instructions); err != nil {
		return nil, err
	}
	if err := d.input(wr.Input); err != nil {
		return nil, err
	}
	if len(req.Messages) == 0 {
		return nil, errors.New("input is required")
	}
	for i, raw := range wr.Tools {
		if err := f.decodeTool(req, raw); err != nil {
			return nil, fmt.Errorf("tools[%d]: %v", i, err)
		}
	}
	tc, err := convertToolChoice(req, wr.ToolChoice)
	if err != nil {
		return nil, err
	}
	req.ToolChoice = tc
	return req, nil
}

func mapEffort(e string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(e)) {
	case "", "none":
		return "", true
	case "minimal", "low":
		return "low", true
	case "medium":
		return "medium", true
	case "high", "xhigh":
		return "high", true
	}
	return "", false
}

// decoder turns input items into canonical messages.
type decoder struct {
	req *llm.Request
	// assistantOpen is true while the last message is an assistant message
	// that following function_call items should merge into.
	assistantOpen bool
	// reasoning is pending reasoning text for the next assistant message.
	reasoning string
	// callNames maps call_id -> tool name so tool outputs carry Name.
	callNames map[string]string
}

func (d *decoder) warn(format string, args ...any) {
	d.req.Warnings = append(d.req.Warnings, fmt.Sprintf(format, args...))
}

func (d *decoder) instructions(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("instructions: %v", err)
		}
		if s != "" {
			d.req.Messages = append(d.req.Messages, llm.Message{Role: llm.RoleSystem, Content: s})
		}
		return nil
	}
	if raw[0] == '[' {
		// Instructions given as input items (prompt templates): decode them
		// in place as leading messages.
		return d.input(raw)
	}
	return errors.New("instructions: must be a string")
}

func (d *decoder) input(raw json.RawMessage) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	switch raw[0] {
	case '"':
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return fmt.Errorf("input: %v", err)
		}
		d.push(llm.Message{Role: llm.RoleUser, Content: s})
		return nil
	case '[':
	default:
		return errors.New("input: must be a string or an array of items")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("input: %v", err)
	}
	for i, ir := range items {
		var it wireItem
		if err := json.Unmarshal(ir, &it); err != nil {
			return fmt.Errorf("input[%d]: %v", i, err)
		}
		if err := d.item(i, &it); err != nil {
			return fmt.Errorf("input[%d]: %v", i, err)
		}
	}
	return nil
}

func (d *decoder) item(i int, it *wireItem) error {
	typ := it.Type
	if typ == "" && it.Role != "" {
		typ = "message"
	}
	switch typ {
	case "message":
		return d.message(it)
	case "function_call":
		args, err := argumentsString(it.Arguments)
		if err != nil {
			return fmt.Errorf("function_call arguments: %v", err)
		}
		d.toolCall(llm.ToolCall{ID: it.CallID, Name: flatName(it.Namespace, it.Name), Arguments: args})
	case "custom_tool_call":
		b, _ := json.Marshal(struct {
			Input string `json:"input"`
		}{it.Input})
		d.toolCall(llm.ToolCall{ID: it.CallID, Name: flatName(it.Namespace, it.Name), Arguments: string(b)})
	case "function_call_output", "custom_tool_call_output":
		text, images, warns := toolOutputText(it.Output)
		for _, w := range warns {
			d.warn("input[%d] %s: %s", i, typ, w)
		}
		d.push(llm.Message{
			Role:       llm.RoleTool,
			Content:    text,
			ToolCallID: it.CallID,
			Name:       d.callNames[it.CallID],
			Images:     images,
		})
	case "reasoning":
		text := joinParts(partsFromRaw(it.Content), "reasoning_text", "text")
		if text == "" {
			text = joinParts(it.Summary, "summary_text", "text", "")
		}
		if text != "" {
			if d.reasoning != "" {
				d.reasoning += "\n"
			}
			d.reasoning += text
		}
	default:
		d.warn("input[%d]: item type %q is not supported and was ignored", i, typ)
	}
	return nil
}

func (d *decoder) message(it *wireItem) error {
	var role string
	switch it.Role {
	case "user":
		role = llm.RoleUser
	case "assistant":
		role = llm.RoleAssistant
	case "system", "developer":
		role = llm.RoleSystem
	default:
		return fmt.Errorf("unsupported message role %q", it.Role)
	}
	text, images, warns, err := messageContent(it.Content)
	if err != nil {
		return err
	}
	for _, w := range warns {
		d.warn("%s message: %s", it.Role, w)
	}
	m := llm.Message{Role: role, Content: text, Images: images}
	if role == llm.RoleAssistant {
		m.Reasoning, d.reasoning = d.reasoning, ""
		d.req.Messages = append(d.req.Messages, m)
		d.assistantOpen = true
		return nil
	}
	d.push(m)
	return nil
}

// toolCall appends tc to the open assistant message (merging consecutive
// function_call items and a directly preceding assistant message) or starts
// a new assistant message.
func (d *decoder) toolCall(tc llm.ToolCall) {
	if tc.ID != "" {
		d.callNames[tc.ID] = tc.Name
	}
	msgs := d.req.Messages
	if d.assistantOpen && len(msgs) > 0 && msgs[len(msgs)-1].Role == llm.RoleAssistant {
		last := &msgs[len(msgs)-1]
		if d.reasoning != "" {
			if last.Reasoning != "" {
				last.Reasoning += "\n"
			}
			last.Reasoning += d.reasoning
			d.reasoning = ""
		}
		last.ToolCalls = append(last.ToolCalls, tc)
		return
	}
	m := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{tc}}
	m.Reasoning, d.reasoning = d.reasoning, ""
	d.req.Messages = append(d.req.Messages, m)
	d.assistantOpen = true
}

// push appends a non-assistant message; pending reasoning that was not
// followed by an assistant item is dropped.
func (d *decoder) push(m llm.Message) {
	d.req.Messages = append(d.req.Messages, m)
	d.assistantOpen = false
	d.reasoning = ""
}

// argumentsString accepts the arguments as a JSON string (the spec) or,
// leniently, as an inline JSON value.
func argumentsString(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	if raw[0] == '"' {
		var s string
		err := json.Unmarshal(raw, &s)
		return s, err
	}
	var buf bytes.Buffer
	if err := json.Compact(&buf, raw); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func partsFromRaw(raw json.RawMessage) []wirePart {
	var parts []wirePart
	_ = json.Unmarshal(raw, &parts)
	return parts
}

func joinParts(parts []wirePart, types ...string) string {
	var out []string
	for _, p := range parts {
		for _, t := range types {
			if p.Type == t {
				if p.Text != "" {
					out = append(out, p.Text)
				}
				break
			}
		}
	}
	return strings.Join(out, "\n")
}

// messageContent decodes message content: a string or an array of parts.
func messageContent(raw json.RawMessage) (text string, images int, warns []string, err error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0, nil, nil
	}
	if raw[0] == '"' {
		err = json.Unmarshal(raw, &text)
		return text, 0, nil, err
	}
	if raw[0] != '[' {
		return "", 0, nil, errors.New("content must be a string or an array of parts")
	}
	var parts []wirePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		return "", 0, nil, fmt.Errorf("content: %v", err)
	}
	text, images, warns = joinContent(parts)
	return text, images, warns, nil
}

func joinContent(parts []wirePart) (text string, images int, warns []string) {
	var texts []string
	for _, p := range parts {
		switch p.Type {
		case "input_text", "output_text", "text":
			texts = append(texts, p.Text)
		case "refusal":
			texts = append(texts, p.Refusal)
		case "input_image", "input_file", "image", "file":
			images++
			warns = append(warns, fmt.Sprintf("%s part dropped (text-only simulation)", p.Type))
		default:
			warns = append(warns, fmt.Sprintf("content part type %q dropped", p.Type))
		}
	}
	return strings.Join(texts, "\n"), images, warns
}

// toolOutputText decodes a function_call_output / custom_tool_call_output
// output: a string, an array of content parts, or (leniently) any JSON.
func toolOutputText(raw json.RawMessage) (text string, images int, warns []string) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", 0, nil
	}
	switch raw[0] {
	case '"':
		_ = json.Unmarshal(raw, &text)
		return text, 0, nil
	case '[':
		var parts []wirePart
		if json.Unmarshal(raw, &parts) == nil {
			return joinContent(parts)
		}
	}
	return string(raw), 0, nil
}

// decodeTool converts one Responses tool into req.Tools.
func (f *Frontend) decodeTool(req *llm.Request, raw json.RawMessage) error {
	var t wireTool
	if err := json.Unmarshal(raw, &t); err != nil {
		return err
	}
	return f.addTool(req, t, "")
}

func (f *Frontend) addTool(req *llm.Request, t wireTool, namespace string) error {
	if t.Type == "namespace" {
		if t.Name == "" {
			return errors.New("namespace tool without a name")
		}
		for i, raw := range t.Tools {
			var m wireTool
			if err := json.Unmarshal(raw, &m); err != nil {
				return fmt.Errorf("%s.tools[%d]: %v", t.Name, i, err)
			}
			if err := f.addTool(req, m, flatName(namespace, t.Name)); err != nil {
				return fmt.Errorf("%s.tools[%d]: %v", t.Name, i, err)
			}
		}
		return nil
	}
	if t.Type == "function" && t.Function != nil && t.Name == "" {
		t.Name, t.Description, t.Parameters = t.Function.Name, t.Function.Description, t.Function.Parameters
	}
	if namespace != "" && t.Name != "" {
		flat := flatName(namespace, t.Name)
		f.ns.Store(flat, [2]string{namespace, t.Name})
		t.Name = flat
	}
	switch t.Type {
	case "function":
		if t.Name == "" {
			return errors.New("function tool without a name")
		}
		f.custom.Delete(t.Name)
		var params json.RawMessage
		if p := bytes.TrimSpace(t.Parameters); len(p) > 0 && string(p) != "null" {
			params = append(json.RawMessage(nil), p...)
		}
		req.Tools = append(req.Tools, llm.Tool{Name: t.Name, Description: t.Description, Parameters: params})
	case "custom":
		if t.Name == "" {
			return errors.New("custom tool without a name")
		}
		f.custom.Store(t.Name, true)
		schema, _ := json.Marshal(customSchema{
			Type: "object",
			Properties: customSchemaProps{Input: schemaString{
				Type:        "string",
				Description: formatSummary(t.Format),
			}},
			Required: []string{"input"},
			Marker:   true,
		})
		req.Tools = append(req.Tools, llm.Tool{Name: t.Name, Description: t.Description, Parameters: schema})
	default:
		req.Warnings = append(req.Warnings, fmt.Sprintf("tool type %q is a hosted tool a local model cannot run; it was not offered to the model", t.Type))
	}
	return nil
}

// formatSummary describes a custom tool's input format for the model.
func formatSummary(f *wireFormat) string {
	const plain = "The raw freeform text input for this tool, passed to it verbatim (not JSON)."
	if f == nil || f.Type != "grammar" || strings.TrimSpace(f.Definition) == "" {
		return plain
	}
	syntax := f.Syntax
	if syntax == "" {
		syntax = "formal"
	}
	return fmt.Sprintf("The raw freeform input for this tool, passed to it verbatim (not JSON). It must match this %s grammar:\n%s", syntax, f.Definition)
}

// convertToolChoice maps a Responses tool_choice to the OpenAI Chat style.
func convertToolChoice(req *llm.Request, raw json.RawMessage) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return nil, fmt.Errorf("tool_choice: %v", err)
		}
		switch s {
		case "auto", "none", "required":
			return json.RawMessage(fmt.Sprintf("%q", s)), nil
		}
		return nil, fmt.Errorf("tool_choice: unsupported value %q", s)
	}
	var tc wireToolChoice
	if err := json.Unmarshal(raw, &tc); err != nil {
		return nil, fmt.Errorf("tool_choice: %v", err)
	}
	switch tc.Type {
	case "function", "custom":
		name := tc.Name
		if name == "" && tc.Function != nil {
			name = tc.Function.Name
		}
		if name == "" {
			return nil, errors.New("tool_choice: missing name")
		}
		b, _ := json.Marshal(map[string]any{"type": "function", "function": map[string]string{"name": name}})
		return b, nil
	case "allowed_tools":
		mode := tc.Mode
		if mode != "required" {
			mode = "auto"
		}
		req.Warnings = append(req.Warnings, "tool_choice allowed_tools approximated as "+mode)
		return json.RawMessage(fmt.Sprintf("%q", mode)), nil
	}
	req.Warnings = append(req.Warnings, fmt.Sprintf("tool_choice type %q is not supported and was ignored", tc.Type))
	return nil, nil
}

// ---- encoding helpers shared by the stream and WriteResponse ------------

func responseID(meta api.StreamMeta) (id, base string) {
	base = strings.TrimPrefix(meta.ID, "resp_")
	if base == "" {
		base = randomHex(12)
	}
	return "resp_" + base, base
}

func itemID(prefix, base string, outputIndex int) string {
	return fmt.Sprintf("%s_%s_%d", prefix, base, outputIndex)
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func newCallID() string { return "call_" + randomHex(12) }

func newResponseObject(meta api.StreamMeta, id, status string) *responseObject {
	created := meta.Created
	if created.IsZero() {
		created = time.Now()
	}
	r := &responseObject{
		ID:                id,
		Object:            "response",
		CreatedAt:         created.Unix(),
		Status:            status,
		Model:             meta.Model,
		Output:            []any{},
		ParallelToolCalls: true,
		ToolChoice:        "auto",
		Tools:             []any{},
		Truncation:        "disabled",
		Metadata:          map[string]string{},
	}
	r.Text.Format.Type = "text"
	return r
}

func convertUsage(u llm.Usage) *usageJSON {
	out := &usageJSON{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
		TotalTokens:  u.PromptTokens + u.CompletionTokens,
	}
	out.InputTokensDetails.CachedTokens = u.CachedPromptTokens
	out.OutputTokensDetails.ReasoningTokens = u.ReasoningTokens
	return out
}

// customInput extracts a custom tool call's raw input from the JSON
// arguments the upstream model produced for the synthesized function tool.
func customInput(args string) string {
	trimmed := strings.TrimSpace(args)
	if trimmed == "" {
		return ""
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
		return args // not JSON: the model wrote the raw input directly
	}
	pick := func(v json.RawMessage) string {
		var s string
		if json.Unmarshal(v, &s) == nil {
			return s
		}
		return string(v)
	}
	if v, ok := obj["input"]; ok {
		return pick(v)
	}
	if len(obj) == 1 {
		for _, v := range obj {
			return pick(v)
		}
	}
	return args
}

// buildToolItem encodes a complete tool call as a function_call item, or as
// a custom_tool_call item when its name is a registered custom tool.
func (f *Frontend) buildToolItem(tc llm.ToolCall, id, status string) any {
	callID := tc.ID
	if callID == "" {
		callID = newCallID()
	}
	ns, name := f.split(tc.Name)
	if f.isCustom(tc.Name) {
		return &customToolCallItem{Type: "custom_tool_call", ID: id, CallID: callID, Name: name, Namespace: ns, Input: customInput(tc.Arguments), Status: status}
	}
	return &functionCallItem{Type: "function_call", ID: id, CallID: callID, Name: name, Namespace: ns, Arguments: tc.Arguments, Status: status}
}

// WriteResponse implements api.Frontend.
func (f *Frontend) WriteResponse(w http.ResponseWriter, meta api.StreamMeta, resp *llm.Response, _ llm.Timings) error {
	if meta.Model == "" {
		meta.Model = resp.Model
	}
	id, base := responseID(meta)
	r := newResponseObject(meta, id, "completed")
	itemStatus := "completed"
	switch resp.FinishReason {
	case llm.FinishLength:
		r.Status, itemStatus = "incomplete", "incomplete"
		r.IncompleteDetails = &incompleteDetails{Reason: "max_output_tokens"}
	case "content_filter":
		r.Status, itemStatus = "incomplete", "incomplete"
		r.IncompleteDetails = &incompleteDetails{Reason: "content_filter"}
	case llm.FinishError:
		r.Status = "failed"
		r.Error = &responseError{Code: "server_error", Message: "the response failed"}
	}
	if resp.Reasoning != "" {
		r.Output = append(r.Output, &reasoningItem{
			Type: "reasoning", ID: itemID("rs", base, len(r.Output)),
			Summary: []reasoningText{},
			Content: []reasoningText{{Type: "reasoning_text", Text: resp.Reasoning}},
		})
	}
	if resp.Content != "" || len(resp.ToolCalls) == 0 {
		r.Output = append(r.Output, &messageItem{
			Type: "message", ID: itemID("msg", base, len(r.Output)), Status: itemStatus, Role: "assistant",
			Content: []outputText{{Type: "output_text", Text: resp.Content, Annotations: []any{}}},
		})
	}
	for _, tc := range resp.ToolCalls {
		prefix := "fc"
		if f.isCustom(tc.Name) {
			prefix = "ctc"
		}
		r.Output = append(r.Output, f.buildToolItem(tc, itemID(prefix, base, len(r.Output)), itemStatus))
	}
	r.Usage = convertUsage(resp.Usage)
	return api.WriteJSON(w, http.StatusOK, r)
}

// WriteError implements api.Frontend.
func (f *Frontend) WriteError(w http.ResponseWriter, status int, errType, message string) {
	typ, code := errorShape(errType)
	_ = api.WriteJSON(w, status, errorEnvelope{Error: errorBody{Message: message, Type: typ, Code: code}})
}

// errorShape maps a trialm error kind to OpenAI's error type and code.
func errorShape(errType string) (string, *string) {
	switch errType {
	case "context_length_exceeded":
		return "invalid_request_error", strp("context_length_exceeded")
	case "", "invalid_request_error":
		return "invalid_request_error", nil
	case "not_found_error", "model_not_found":
		return "invalid_request_error", strp("model_not_found")
	case "rate_limit_error", "rate_limit_exceeded":
		return "rate_limit_error", strp("rate_limit_exceeded")
	case "overloaded_error":
		return "server_error", strp("server_is_overloaded")
	case "api_error", "server_error":
		return "server_error", nil
	}
	return errType, nil
}

// failureCode maps a trialm error kind to a response.failed error code.
func failureCode(errType string) string {
	switch errType {
	case "context_length_exceeded":
		return "context_length_exceeded"
	case "rate_limit_error", "rate_limit_exceeded":
		return "rate_limit_exceeded"
	case "invalid_request_error":
		return "invalid_prompt"
	}
	return "server_error"
}
