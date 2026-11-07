//go:build unix

package main

import (
	"os"
	"os/exec"
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
		if strings.HasPrefix(v, "HOME=") || strings.HasPrefix(v, "XDG_") || strings.HasPrefix(v, "CLAUDE_CONFIG_DIR=") || strings.HasPrefix(v, "CODEX_HOME=") || strings.HasPrefix(v, "QWEN_HOME=") || strings.HasPrefix(v, "CONTINUE_GLOBAL_DIR=") || strings.HasPrefix(v, "CLINE_DIR=") || strings.HasPrefix(v, "TRIALM_HOME=") || strings.HasPrefix(v, "UV_") {
			if !strings.Contains(v, root) {
				t.Fatalf("not isolated: %s", v)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(root, "codex/config.toml")); err != nil {
		t.Fatal(err)
	}
}

func TestUVToolDirectories(t *testing.T) {
	uv, err := exec.LookPath("uv")
	if err != nil {
		t.Skip("uv is only required for harness e2e")
	}
	root := t.TempDir()
	if err := setup(root); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"tool", "dir"}, filepath.Join(root, "uv-tools")},
		{[]string{"tool", "dir", "--bin"}, filepath.Join(root, "bin")},
	} {
		cmd := exec.Command(uv, tc.args...)
		cmd.Env = env(root, filepath.Join(root, "bin"))
		out, err := cmd.CombinedOutput()
		if err != nil || strings.TrimSpace(string(out)) != tc.want {
			t.Fatalf("uv %v = %q, %v; want %q", tc.args, out, err, tc.want)
		}
	}
}

func TestHeadlessPromptArguments(t *testing.T) {
	for _, id := range []string{"codex", "cline", "continue", "aider"} {
		t.Run(id, func(t *testing.T) {
			root := t.TempDir()
			// Record argv after shell expansion, including a prompt with spaces.
			stub := filepath.Join(root, "cli")
			if err := os.WriteFile(stub, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			var extra string
			for _, s := range specs {
				if s.id == id {
					extra = s.extra
				}
			}
			cmd := exec.Command("sh", "-c", `"$CLI" `+extra)
			cmd.Env = append(env(root, root), "CLI="+stub, "TASK="+greetingTask)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%v: %s", err, out)
			}
			args := strings.Split(strings.TrimSpace(string(out)), "\n")
			prompts := 0
			for _, arg := range args {
				if arg == greetingTask {
					prompts++
				}
			}
			if prompts != 1 || len(strings.Fields(greetingTask)) < 2 {
				t.Fatalf("need one multiword prompt argument: %q", args)
			}
			if id == "codex" && (!strings.Contains(string(out), "--sandbox\nworkspace-write\n") || !strings.Contains(string(out), "approval_policy=\"never\"\n") || strings.Contains(string(out), "--full-auto")) {
				t.Fatalf("Codex must use explicit sandbox and approval flags: %q", args)
			}
			if id == "aider" && !strings.Contains(string(out), "--no-show-release-notes\n") {
				t.Fatalf("Aider must not open a browser on first run: %q", args)
			}
			if id == "continue" && !strings.Contains(string(out), "--allow\nWrite\n") {
				t.Fatalf("Continue must expose Write in headless mode: %q", args)
			}
		})
	}
}

func TestToolFailure(t *testing.T) {
	blocked := []byte(`{"role":"tool","content":"sandbox-exec: sandbox_apply: Operation not permitted"}`)
	if got := toolFailure("codex", blocked); !strings.Contains(got, "nested macOS Seatbelt") {
		t.Fatal(got)
	}
	for _, tc := range []struct {
		id       string
		requests []byte
	}{{"codex", nil}, {"continue", blocked}} {
		if got := toolFailure(tc.id, tc.requests); got != "tool.txt does not contain tool-ok" {
			t.Fatal(got)
		}
	}
}
