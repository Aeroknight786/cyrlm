package upstream

import (
	"testing"

	"github.com/aeroknight786/trialm/internal/llm"
)

func TestNormalizeForTemplates(t *testing.T) {
	in := []llm.Message{
		{Role: llm.RoleSystem, Content: "instructions"},
		{Role: llm.RoleSystem, Content: "developer 1"},
		{Role: llm.RoleSystem, Content: "developer 2"},
		{Role: llm.RoleUser, Content: "env context"},
		{Role: llm.RoleUser, Content: "task"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "c1", Name: "exec", Arguments: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "c1", Content: "out"},
		{Role: llm.RoleSystem, Content: "later update"},
		{Role: llm.RoleUser, Content: "next"},
	}
	out := NormalizeForTemplates(in)
	roles := ""
	for _, m := range out {
		roles += m.Role + ","
	}
	if roles != "system,user,assistant,tool,user," {
		t.Fatalf("roles %s", roles)
	}
	if out[0].Content != "instructions\n\ndeveloper 1\n\ndeveloper 2" {
		t.Fatalf("system %q", out[0].Content)
	}
	if out[1].Content != "env context\n\ntask" {
		t.Fatalf("users not joined: %q", out[1].Content)
	}
	if out[4].Content != "[System note]\nlater update\n\nnext" {
		t.Fatalf("late system: %q", out[4].Content)
	}
	// The input is not modified.
	if in[3].Content != "env context" {
		t.Fatal("input mutated")
	}
}
