package cli

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/verify"
)

func launchChild(t *testing.T, root string, args ...string) (string, int) {
	t.Helper()
	exe, _ := os.Executable()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe)
	b, _ := json.Marshal(args)
	cmd.Env = append(os.Environ(), goldenChildEnv+"="+string(b), "HOME="+filepath.Join(root, "home"), "XDG_CONFIG_HOME="+filepath.Join(root, "config"), "XDG_DATA_HOME="+filepath.Join(root, "data"), "XDG_CACHE_HOME="+filepath.Join(root, "cache"), "TRIALM_HOME="+root, "TRIALM_OFFLINE=1", "TRIALM_NO_INPUT=1")
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return string(out), exit.ExitCode()
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(out), 0
}
func TestLaunchNeedsSetup(t *testing.T) {
	out, code := launchChild(t, t.TempDir(), "launch", "codex", "--json")
	if code != 3 || !strings.Contains(out, `"needs":"setup"`) || !strings.Contains(out, "trialm setup") {
		t.Fatalf("%d %s", code, out)
	}
}
func TestHomeReadyAndLaunchPrintContract(t *testing.T) {
	root := t.TempDir()
	p := setup.Plan{ID: "fixture", Slug: "local", ModelName: "Local model", LocalModelName: "local-model", Runtime: "llamacpp", Harness: "codex", Endpoint: "http://127.0.0.1:49999", ContextTokens: 65536}
	j := setup.Journal{Plan: p, State: "ready", Report: &verify.Report{Ready: true}}
	dir := filepath.Join(root, "setups", "local")
	_ = os.MkdirAll(dir, 0700)
	raw, _ := json.Marshal(j)
	_ = os.WriteFile(filepath.Join(dir, "journal.json"), raw, 0600)
	out, code := launchChild(t, root, "--json")
	if code != 0 || !strings.Contains(out, `"ready_setups"`) || !strings.Contains(out, `trialm launch codex`) {
		t.Fatalf("%d %s", code, out)
	}
	out, code = launchChild(t, root, "launch", "codex", "--print", "--json")
	if code != 0 || !strings.Contains(out, "wire_api=responses") || !strings.Contains(out, `"type":"result"`) {
		t.Fatalf("%d %s", code, out)
	}
	if _, err := os.Stat(filepath.Join(root, "harness")); !os.IsNotExist(err) {
		t.Fatal("print wrote harness files")
	}
	if _, err := os.Stat(filepath.Join(root, "logs")); !os.IsNotExist(err) {
		t.Fatal("print wrote logs")
	}
}

func TestPasswordPromptNeedsTerminalExit(t *testing.T) {
	err := setupError(&setup.PasswordRequired{})
	exit, ok := err.(*exitError)
	if !ok || exit.Code != 3 || !strings.Contains(exit.Message, "1 OS password prompt") {
		t.Fatal(err)
	}
}
