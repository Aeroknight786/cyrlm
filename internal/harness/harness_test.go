package harness

import (
	"strings"
	"testing"
)

func TestHarnessSetup(t *testing.T) {
	for _, id := range []string{"claude", "codex", "opencode", "aider", "cline", "continue", "zed", "qwen", "generic"} {
		h, err := Get(id)
		if err != nil {
			t.Fatalf("Get(%s): %v", id, err)
		}
		s := h.SetupFor(Options{Port: 11500, Model: "gpt-oss:20b"})
		out := s.Render()
		if !strings.Contains(out, "11500") {
			t.Errorf("%s setup does not mention the port:\n%s", id, out)
		}
		if h.Scenario.BasePromptTokens <= 0 {
			t.Errorf("%s has no scenario", id)
		}
	}
	cc, _ := Get("claude-code")
	tuned := cc.SetupFor(Options{Port: 1, Model: "m", Tuned: true}).Render()
	if !strings.Contains(tuned, "CLAUDE_CODE_ATTRIBUTION_HEADER=0") {
		t.Error("tuned Claude Code setup must disable the attribution header")
	}
	def := cc.SetupFor(Options{Port: 1, Model: "m"}).Render()
	if strings.Contains(def, "CLAUDE_CODE_ATTRIBUTION_HEADER=0 ") {
		t.Error("default setup should simulate stock Claude Code")
	}
	if _, err := Get("vim"); err == nil {
		t.Error("unknown harness should error")
	}
}

func TestRenderPowerShell(t *testing.T) {
	cc, _ := Get("claude-code")
	out := cc.SetupFor(Options{Port: 11434, Model: "m"}).RenderFor("windows")
	for _, want := range []string{"$env:ANTHROPIC_BASE_URL='http://127.0.0.1:11434';", "Remove-Item Env:ANTHROPIC_API_KEY", "claude --model m"} {
		if !strings.Contains(out, want) {
			t.Errorf("PowerShell rendering lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ANTHROPIC_BASE_URL=http") {
		t.Error("POSIX env syntax in PowerShell output")
	}
}
