package tok

import (
	"strings"
	"testing"

	"github.com/aeroknight786/cyrlm/internal/llm"
)

func TestPiecesRoundTrip(t *testing.T) {
	for _, s := range []string{
		"Hello world, this is a test of the tokenizer.",
		"func getUserName(id int) string {\n\treturn users[id].Name\n}\n",
		"    indented code_with_underscores = 12345678   \n\n\nend",
		"unicode: naïve café — 東京 🚀",
		"",
	} {
		if got := strings.Join(Pieces(s), ""); got != s {
			t.Fatalf("pieces do not reassemble: %q != %q", got, s)
		}
	}
}

func TestPiecesDensity(t *testing.T) {
	// Real BPE tokenizers average ~3.5-4.5 chars/token on English prose and
	// ~3 on code; the estimator should land in that neighbourhood (the
	// calibrator corrects the rest).
	prose := strings.Repeat("The quick brown fox jumps over the lazy dog while the developer reviews a pull request. ", 20)
	code := strings.Repeat("func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) error {\n\treturn s.writeJSON(w, 200, resp)\n}\n", 20)
	for name, s := range map[string]string{"prose": prose, "code": code} {
		cpt := float64(len(s)) / float64(Count(s))
		if cpt < 2.5 || cpt > 6 {
			t.Errorf("%s: %.2f chars/token outside plausible range", name, cpt)
		}
	}
}

func TestPrefixStability(t *testing.T) {
	a := "The system prompt stays the same across turns. "
	b := a + "Then a new tool result arrives with more text."
	pa, pb := Pieces(a), Pieces(b)
	// All but the last piece of a must be a prefix of b's pieces.
	for i := 0; i < len(pa)-1; i++ {
		if pa[i] != pb[i] {
			t.Fatalf("piece %d differs: %q vs %q", i, pa[i], pb[i])
		}
	}
}

func TestRenderLCPReusesHistory(t *testing.T) {
	sys := llm.Message{Role: llm.RoleSystem, Content: strings.Repeat("You are a coding agent. ", 200)}
	tools := []llm.Tool{{Name: "bash", Description: "run a command", Parameters: []byte(`{"type":"object","properties":{"cmd":{"type":"string"}}}`)}}
	turn1 := &llm.Request{Messages: []llm.Message{sys, {Role: llm.RoleUser, Content: "fix the bug"}}, Tools: tools}
	r1 := Render(turn1, RenderOptions{StripPastReasoning: true})

	// Simulated cache after generation: prompt + generated reply with
	// reasoning (reasoning is in the KV cache while generating).
	cache := r1.Clone()
	RenderAssistantBody(cache, "let me think", "", []llm.ToolCall{{Name: "bash", Arguments: `{ "cmd" : "ls" }`}}, true)

	turn2 := &llm.Request{Messages: append(append([]llm.Message{}, turn1.Messages...),
		llm.Message{Role: llm.RoleAssistant, Reasoning: "let me think", ToolCalls: []llm.ToolCall{{Name: "bash", Arguments: `{"cmd":"ls"}`}}},
		llm.Message{Role: llm.RoleTool, Content: "main.go\n"},
	), Tools: tools}
	r2 := Render(turn2, RenderOptions{StripPastReasoning: true})
	lcp := LCP(cache, r2)
	// Divergence must be right after the first prompt (the assistant marker
	// is shared, then reasoning was stripped from history).
	if lcp < r1.Len()-1 || lcp > r1.Len()+1 {
		t.Fatalf("lcp %d, want ≈ %d (prompt 1 length)", lcp, r1.Len())
	}

	// Without reasoning, the generated reply itself is reused too.
	cache2 := r1.Clone()
	RenderAssistantBody(cache2, "", "", []llm.ToolCall{{Name: "bash", Arguments: `{ "cmd" : "ls" }`}}, true)
	turn2.Messages[2].Reasoning = ""
	r3 := Render(turn2, RenderOptions{StripPastReasoning: true})
	if got := LCP(cache2, r3); got < cache2.Len() {
		t.Fatalf("reply without reasoning should be fully reused: lcp %d < %d", got, cache2.Len())
	}
}

func TestCalibrator(t *testing.T) {
	c := NewCalibrator(1)
	for i := 0; i < 10; i++ {
		c.Observe(1300, 1000)
	}
	if c.Ratio < 1.25 || c.Ratio > 1.35 {
		t.Fatalf("ratio %.2f, want ≈1.3", c.Ratio)
	}
	if got := c.Real(100); got != 130 {
		t.Fatalf("Real(100)=%d", got)
	}
	c.Observe(5, 10) // ignored: too small
	if c.Samples != 10 {
		t.Fatal("tiny sample should be ignored")
	}
}
