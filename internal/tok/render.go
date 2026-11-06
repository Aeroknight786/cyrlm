package tok

import (
	"strings"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

// RenderOptions controls template behaviour that affects cache reuse.
type RenderOptions struct {
	// StripPastReasoning drops reasoning of earlier assistant turns, as the
	// chat templates of gpt-oss, Qwen3 and most reasoning models do. This
	// is why each agent turn re-prefills from the previous assistant reply.
	StripPastReasoning bool
}

// Render renders a request roughly the way chat templates do: system text,
// then tool definitions, then the conversation, then the generation prompt.
// The exact markers don't matter; ordering and stability do.
func Render(req *llm.Request, opt RenderOptions) *Seq {
	s := &Seq{}
	// Leading system messages and tools form the "system block" as in
	// Qwen/Llama/gpt-oss templates.
	i := 0
	s.AppendSpecial("<|system|>")
	for i < len(req.Messages) && req.Messages[i].Role == llm.RoleSystem {
		s.Append(req.Messages[i].Content)
		s.Append("\n")
		i++
	}
	if len(req.Tools) > 0 {
		s.Append("# Tools\n")
		for _, t := range req.Tools {
			s.Append(renderTool(t))
			s.Append("\n")
		}
	}
	s.AppendSpecial("<|end|>")
	for ; i < len(req.Messages); i++ {
		RenderMessage(s, req.Messages[i], opt.StripPastReasoning)
	}
	s.AppendSpecial("<|assistant|>")
	return s
}

// RenderMessage appends one message.
func RenderMessage(s *Seq, m llm.Message, stripReasoning bool) {
	switch m.Role {
	case llm.RoleAssistant:
		s.AppendSpecial("<|assistant|>")
		RenderAssistantBody(s, m.Reasoning, m.Content, m.ToolCalls, !stripReasoning)
	case llm.RoleTool:
		s.AppendSpecial("<|tool|>")
		s.Append(m.Content)
	case llm.RoleSystem:
		s.AppendSpecial("<|system|>")
		s.Append(m.Content)
	default:
		s.AppendSpecial("<|user|>")
		s.Append(m.Content)
	}
	s.AppendSpecial("<|end|>")
}

// RenderAssistantBody appends an assistant turn's body. The same function
// renders history and freshly generated output, so a reply without
// reasoning is reused from cache on the next turn.
func RenderAssistantBody(s *Seq, reasoning, content string, calls []llm.ToolCall, includeReasoning bool) {
	if includeReasoning && reasoning != "" {
		s.AppendSpecial("<|think|>")
		s.Append(reasoning)
		s.AppendSpecial("<|/think|>")
	}
	s.Append(content)
	for _, c := range calls {
		s.AppendSpecial("<|call|>")
		s.Append(c.Name)
		s.Append(llm.CanonicalJSON(c.Arguments))
	}
}

func renderTool(t llm.Tool) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteString(": ")
	b.WriteString(t.Description)
	if len(t.Parameters) > 0 {
		b.WriteString(" ")
		b.WriteString(llm.CanonicalJSON(string(t.Parameters)))
	}
	return b.String()
}

// ToolsJSONSize returns the estimated tokens spent on tool definitions.
func ToolsJSONSize(req *llm.Request) int {
	n := 0
	for _, t := range req.Tools {
		n += Count(renderTool(t))
	}
	return n
}
