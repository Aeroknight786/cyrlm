package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runCLI(t *testing.T, home string, args ...string) ([]byte, []byte, int) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(args)
	cmd := exec.Command(exe)
	cmd.Env = append(goldenEnv(home, t.TempDir(), string(raw)), "TRIALM_HOME="+filepath.Join(home, ".trialm"), "TRIALM_OFFLINE=1")
	var out, diagnostics bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &diagnostics
	err = cmd.Run()
	code := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			code = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	return out.Bytes(), diagnostics.Bytes(), code
}
func decodeEvents(t *testing.T, b []byte) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte{'\n'}) {
		var e map[string]any
		if err := json.Unmarshal(line, &e); err != nil {
			t.Fatalf("NDJSON: %v: %s", err, b)
		}
		events = append(events, e)
	}
	return events
}
func unwrap(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if _, ok := x["src"]; ok {
			if n, ok := x["v"]; ok {
				return n
			}
		}
		for k, v := range x {
			x[k] = unwrap(v)
		}
	case []any:
		for i, v := range x {
			x[i] = unwrap(v)
		}
	}
	return v
}

// This compares every field to the pre-extraction content, including ranking,
// commands, settings, timings and uncertainty. Only numeric wrappers differ.
func TestFindLegacyEquivalence(t *testing.T) {
	for _, fx := range goldenFixtures {
		for _, h := range []string{"claude-code", "codex"} {
			t.Run(fx.name+"/"+h, func(t *testing.T) {
				home := t.TempDir()
				out, errout, code := runCLI(t, home, "find", "--json", "--for", h, "--as", fx.device, "--ram", fx.ram)
				if code != 0 {
					t.Fatalf("%d: %s", code, errout)
				}
				events := decodeEvents(t, out)
				if len(events) != 2 || events[0]["type"] != "recipes" || events[1]["type"] != "result" {
					t.Fatal(events)
				}
				got, _ := json.Marshal(unwrap(events[0]["data"]))
				name := "recommend.json"
				if h == "codex" {
					name = "recommend-codex.json"
				}
				b, err := os.ReadFile(filepath.Join("testdata", "golden", fx.name, name))
				if err != nil {
					t.Fatal(err)
				}
				var want any
				_ = json.Unmarshal(b, &want)
				expected, _ := json.Marshal(want)
				if normalizeFloats(string(got)) != normalizeFloats(string(expected)) {
					t.Fatalf("find differs from legacy golden\ngot %s\nwant %s", got, expected)
				}
			})
		}
	}
}
func validateNumbers(t *testing.T, v any) {
	t.Helper()
	switch x := v.(type) {
	case float64:
		t.Fatalf("raw payload number: %v", x)
	case []any:
		for _, v := range x {
			validateNumbers(t, v)
		}
	case map[string]any:
		if _, ok := x["v"]; ok {
			for _, key := range []string{"v", "lo", "hi", "unit", "kind", "src"} {
				if _, ok = x[key]; !ok {
					t.Fatalf("incomplete Figure: %v", x)
				}
			}
			if x["kind"] != "claimed" && x["kind"] != "simulated" && x["kind"] != "calibrated" && x["kind"] != "measured" {
				t.Fatal(x)
			}
			return
		}
		for _, v := range x {
			validateNumbers(t, v)
		}
	}
}
func TestNDJSONContract(t *testing.T) {
	for _, args := range [][]string{{"--json"}, {"find", "qwen", "--device", "RTX 4090", "--ram", "64", "--json"}, {"setup", "qwen3.6:35b-a3b", "--device", "RTX 4090", "--ram", "64", "--dry-run", "--json"}, {"launch", "--json"}, {"try", "qwen3:8b", "--device", "RTX 4090", "--ram", "64", "--json"}, {"try", "--budget", "-1", "--json"}} {
		out, _, _ := runCLI(t, t.TempDir(), args...)
		for _, e := range decodeEvents(t, out) {
			if e["v"] != float64(1) {
				t.Fatal(e)
			}
			valid := map[string]bool{"detect": true, "recipes": true, "plan": true, "setup_plan": true, "setup_step": true, "setup_progress": true, "setup_log": true, "setup_ready": true, "setup_paused": true, "setup_undone": true, "step": true, "progress": true, "check": true, "needs_user": true, "result": true, "error": true}
			if !valid[e["type"].(string)] {
				t.Fatal(e)
			}
			validateNumbers(t, e["data"])
			if e["type"] == "result" {
				if _, ok := e["next"].([]any); !ok {
					t.Fatal("result lacks next", e)
				}
			}
		}
	}
}
func TestPlainPipedNoEscapes(t *testing.T) {
	for _, args := range [][]string{{}, {"find", "--device", "RTX 4090", "--ram", "64", "--plain"}, {"help", "advanced"}, {"setup", "qwen3:8b", "--device", "RTX 4090", "--ram", "64", "--dry-run", "--plain"}} {
		out, errout, code := runCLI(t, t.TempDir(), args...)
		if code != 0 {
			t.Fatalf("%v: %d: %s", args, code, errout)
		}
		if bytes.ContainsAny(append(out, errout...), "\x1b") {
			t.Fatal("escape byte in piped output")
		}
	}
}
func TestSetupHashApproval(t *testing.T) {
	home := t.TempDir()
	args := []string{"setup", "qwen3:8b", "--device", "RTX 4090", "--ram", "64", "--dry-run", "--json"}
	out, errout, code := runCLI(t, home, args...)
	if code != 0 {
		t.Fatalf("%d: %s", code, errout)
	}
	events := decodeEvents(t, out)
	data := events[0]["data"].(map[string]any)
	id := data["plan_id"].(string)
	if len(id) != 64 {
		t.Fatal(id)
	}
	matched := append(append([]string{}, args...), "--yes="+id)
	out, errout, code = runCLI(t, home, matched...)
	if code != 0 {
		t.Fatalf("same plan rejected: %s", errout)
	}
	changed := append(append([]string{}, matched...), "--ctx", "32768")
	_, errout, code = runCLI(t, home, changed...)
	if code != 4 || !strings.Contains(string(errout), "plan changed") {
		t.Fatalf("changed plan accepted: %d %s", code, errout)
	}
	_, _, code = runCLI(t, home, append(args, "--yes")...)
	if code != 0 {
		t.Fatal("bare --yes rejected")
	}
}
func TestExitCodesAndQuery(t *testing.T) {
	for _, tc := range []struct {
		args []string
		code int
		flag string
	}{{[]string{"unknown"}, 2, "help"}, {[]string{"find", "--bad"}, 2, "bad"}, {[]string{"try", "--use", "chat", "--json"}, 3, "--serve"}, {[]string{"try", "qwen3:8b", "--device", "RTX 4090", "--ram", "64", "--json"}, 3, "trialm key"}, {[]string{"try", "1", "--device", "RTX 4090", "--ram", "64"}, 3, "trialm find"}, {[]string{"setup", "missing-model"}, 1, "catalog"}} {
		_, errout, code := runCLI(t, t.TempDir(), tc.args...)
		if code != tc.code || !strings.Contains(string(errout), tc.flag) {
			t.Fatalf("%v: %d %s", tc.args, code, errout)
		}
	}
	home := t.TempDir()
	out, errout, code := runCLI(t, home, "find", "gpt-oss", "--device", "RTX 4090", "--ram", "64", "--json")
	if code != 0 {
		t.Fatal(string(errout))
	}
	data := unwrap(decodeEvents(t, out)[0]["data"]).(map[string]any)
	for _, o := range data["options"].([]any) {
		if !strings.Contains(o.(map[string]any)["model"].(string), "gpt-oss") {
			t.Fatal(o)
		}
	}
	var saved []map[string]any
	b, err := os.ReadFile(filepath.Join(home, ".trialm", "last.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(b, &saved)
	if reflect.ValueOf(saved).Len() == 0 {
		t.Fatal("listing not saved")
	}
}
