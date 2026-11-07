package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractConnection(t *testing.T) {
	text := "banner\n── Connect OpenCode ──\n\nAdd to opencode.json:\n\n    {\n      \"provider\": {}\n    }\n\nRun in a new terminal window:\n\n    opencode --model trialm/fake\n\nNote: test\n"
	cmd, config := extract(text)
	if cmd != "opencode --model trialm/fake" || config != "{\n  \"provider\": {}\n}" {
		t.Fatalf("got command %q config %q", cmd, config)
	}
	cmd, config = extract("no connection")
	if cmd != "" || config != "" {
		t.Fatal("unexpected connection")
	}
}
func TestIsolation(t *testing.T) {
	root := t.TempDir()
	if err := setup(root); err != nil {
		t.Fatal(err)
	}
	for _, v := range env(root, filepath.Join(root, "bin")) {
		if strings.HasPrefix(v, "HOME=") || strings.HasPrefix(v, "XDG_") || strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(v, "CODEX_HOME=") || strings.HasPrefix(v, "QWEN_HOME=") || strings.HasPrefix(v, "CONTINUE_GLOBAL_DIR=") || strings.HasPrefix(v, "CLINE_DIR=") || strings.HasPrefix(v, "TRIALM_HOME=") {
			if !strings.Contains(v, root) {
				t.Fatalf("not isolated: %s", v)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "codex/config.toml")); err != nil {
		t.Fatal(err)
	}
}
