package responses

import "encoding/json"

// ---- request side -------------------------------------------------------

type wireRequest struct {
	Model              string            `json:"model"`
	Instructions       json.RawMessage   `json:"instructions"`
	Input              json.RawMessage   `json:"input"`
	Tools              []json.RawMessage `json:"tools"`
	ToolChoice         json.RawMessage   `json:"tool_choice"`
	MaxOutputTokens    *int              `json:"max_output_tokens"`
	Temperature        *float64          `json:"temperature"`
	TopP               *float64          `json:"top_p"`
	Stream             bool              `json:"stream"`
	Reasoning          *wireReasoning    `json:"reasoning"`
	PreviousResponseID string            `json:"previous_response_id"`
	// Ignored: store, include, parallel_tool_calls, text, truncation,
	// metadata, prompt_cache_key, service_tier, user, background.
}

type wireReasoning struct {
	Effort string `json:"effort"`
}

// wireItem is the union of every input item shape we understand.
type wireItem struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`
	Content   json.RawMessage `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Summary   []wirePart      `json:"summary"`
}

// wirePart is one content part (message content, reasoning content or
// summary, function_call_output content).
type wirePart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type wireTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
	Format      *wireFormat     `json:"format"`
	// Function is accepted for clients that send Chat-Completions-shaped
	// tools ({"type":"function","function":{...}}) to /v1/responses.
	Function *struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

// wireFormat is a custom tool's input format: {"type":"text"} or
// {"type":"grammar","syntax":"lark"|"regex","definition":"..."}.
type wireFormat struct {
	Type       string `json:"type"`
	Syntax     string `json:"syntax"`
	Definition string `json:"definition"`
}

type wireToolChoice struct {
	Type string `json:"type"`
	Name string `json:"name"`
	Mode string `json:"mode"`
	// Function is accepted for Chat-Completions-shaped tool_choice.
	Function *struct {
		Name string `json:"name"`
	} `json:"function"`
}

// customSchema is the JSON schema given to the model for a custom
// (freeform) tool: a single required string property "input".
type customSchema struct {
	Type                 string            `json:"type"`
	Properties           customSchemaProps `json:"properties"`
	Required             []string          `json:"required"`
	AdditionalProperties bool              `json:"additionalProperties"`
	Marker               bool              `json:"x-cyrlm-custom"`
}

type customSchemaProps struct {
	Input schemaString `json:"input"`
}

type schemaString struct {
	Type        string `json:"type"`
	Description string `json:"description"`
}

// ---- response side ------------------------------------------------------

type responseObject struct {
	ID                 string             `json:"id"`
	Object             string             `json:"object"`
	CreatedAt          int64              `json:"created_at"`
	Status             string             `json:"status"`
	Error              *responseError     `json:"error"`
	IncompleteDetails  *incompleteDetails `json:"incomplete_details"`
	Instructions       *string            `json:"instructions"`
	MaxOutputTokens    *int               `json:"max_output_tokens"`
	Model              string             `json:"model"`
	Output             []any              `json:"output"`
	ParallelToolCalls  bool               `json:"parallel_tool_calls"`
	PreviousResponseID *string            `json:"previous_response_id"`
	Store              bool               `json:"store"`
	Text               textConfig         `json:"text"`
	ToolChoice         string             `json:"tool_choice"`
	Tools              []any              `json:"tools"`
	Truncation         string             `json:"truncation"`
	Usage              *usageJSON         `json:"usage"`
	Metadata           map[string]string  `json:"metadata"`
}

type textConfig struct {
	Format struct {
		Type string `json:"type"`
	} `json:"format"`
}

type responseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type incompleteDetails struct {
	Reason string `json:"reason"`
}

type usageJSON struct {
	InputTokens        int `json:"input_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokens        int `json:"output_tokens"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
	TotalTokens int `json:"total_tokens"`
}

type messageItem struct {
	Type    string       `json:"type"`
	ID      string       `json:"id"`
	Status  string       `json:"status"`
	Role    string       `json:"role"`
	Content []outputText `json:"content"`
}

type outputText struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Annotations []any  `json:"annotations"`
}

type reasoningItem struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	Summary []reasoningText `json:"summary"`
	Content []reasoningText `json:"content"`
}

type reasoningText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type functionCallItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
	Status    string `json:"status"`
}

type customToolCallItem struct {
	Type   string `json:"type"`
	ID     string `json:"id"`
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
	Status string `json:"status"`
}

// event is the union of every streaming event payload. Pointer fields are
// emitted only when set so each event carries exactly its documented keys.
type event struct {
	Type           string          `json:"type"`
	SequenceNumber int             `json:"sequence_number"`
	Response       *responseObject `json:"response,omitempty"`
	OutputIndex    *int            `json:"output_index,omitempty"`
	ItemID         string          `json:"item_id,omitempty"`
	ContentIndex   *int            `json:"content_index,omitempty"`
	Item           any             `json:"item,omitempty"`
	Part           *outputText     `json:"part,omitempty"`
	Delta          *string         `json:"delta,omitempty"`
	Text           *string         `json:"text,omitempty"`
	Arguments      *string         `json:"arguments,omitempty"`
	Input          *string         `json:"input,omitempty"`
	Logprobs       json.RawMessage `json:"logprobs,omitempty"`
}

type errorEnvelope struct {
	Error errorBody `json:"error"`
}

type errorBody struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func intp(i int) *int       { return &i }
func strp(s string) *string { return &s }

var emptyLogprobs = json.RawMessage("[]")
