package setup

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/harness"
)

func TestOwnedHarnessPlanFilesMatchSetup(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		for _, rt := range []string{"ollama", "llamacpp"} {
			for _, id := range []string{"aider", "qwen-code", "cline", "continue"} {
				t.Run(goos+"/"+rt+"/"+id, func(t *testing.T) {
					in := spec{model: "qwen3-8b", runtime: rt, goos: goos, goarch: "amd64", backend: "cuda", tuned: true, harness: id}.input(t)
					p := build(t, in)
					s := in.Harness.SetupFor(harness.Options{Port: DefaultPort(in.Profile), Model: p.LocalModelName, Tuned: true, Protocol: in.Harness.ProtocolFor(in.Profile.APIs), ContextTokens: p.ContextTokens, Home: "~/.trialm", Bridge: true})
					st := mustStep(t, p, "harness-"+id)
					if len(st.Files) != len(s.Files) {
						t.Fatalf("plan files: %d, setup files: %d", len(st.Files), len(s.Files))
					}
					for path, content := range s.Files {
						f := file(t, st, path)
						if f.Merge != "create" || !strings.HasPrefix(f.Path, "~/.trialm/harness/") {
							t.Errorf("file ownership: %+v", f)
						}
						// updatedAt reflects generation time; compare the required value's
						// format separately in the harness tests.
						if id == "cline" {
							var got, want map[string]any
							if err := json.Unmarshal([]byte(f.Content), &got); err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal([]byte(content), &want); err != nil {
								t.Fatal(err)
							}
							for _, obj := range []map[string]any{got, want} {
								for _, v := range obj["providers"].(map[string]any) {
									delete(v.(map[string]any), "updatedAt")
								}
							}
							if !reflect.DeepEqual(got, want) {
								t.Errorf("provider config differs: %v != %v", got, want)
							}
						} else if f.Content != content {
							t.Errorf("plan content differs from SetupFor:\n%s\n%s", f.Content, content)
						}
					}
					command := runs(st)
					if len(st.Commands) != 1 {
						t.Fatalf("CLI command missing: %+v", st)
					}
					if strings.Contains(command, "=~") || strings.Contains(command, "='~/") {
						t.Errorf("literal tilde in env: %s", command)
					}
					if goos == "windows" {
						contains(t, "path", command, `"$HOME\.trialm\harness\`)
					} else {
						contains(t, "path", command, `"$HOME/.trialm/harness/`)
					}
					if id == "cline" && rt == "llamacpp" {
						lacks(t, "cline", st.Why, "API Provider Ollama")
						contains(t, "cline", st.Why, "OpenAI Compatible")
					}
					if id == "qwen-code" {
						lacks(t, "qwen restart", st.Restart, "then restart")
						contains(t, "qwen", command, "QWEN_CODE_SYSTEM_DEFAULTS_PATH", "QWEN_USAGE_STATISTICS_ENABLED")
					}
					if id == "continue" {
						contains(t, "continue", st.Restart, "nothing to restart")
						contains(t, "continue", command, "cn --config")
					}
				})
			}
		}
	}
}

func TestOpenCodePlanUsesSharedInlineConfig(t *testing.T) {
	for _, tuned := range []bool{false, true} {
		in := spec{model: "qwen3-8b", runtime: "llamacpp", goos: "linux", goarch: "amd64", backend: "cuda", tuned: tuned, harness: "opencode"}.input(t)
		p := build(t, in)
		s := in.Harness.SetupFor(harness.Options{Port: DefaultPort(in.Profile), Model: p.LocalModelName, Tuned: tuned, ContextTokens: p.ContextTokens, Bridge: true})
		step := mustStep(t, p, "harness-opencode")
		if len(step.Files) != 0 {
			t.Error("inline launch requires editing a config")
		}
		contains(t, "inline command", runs(step), "OPENCODE_CONFIG_CONTENT=", "OPENCODE_DISABLE_MODELS_FETCH=1", "opencode --model trialm-local/"+p.LocalModelName)
		perm := mustStep(t, p, "harness-opencode-permanent")
		if !perm.Optional {
			t.Error("permanent edit is required")
		}
		f := file(t, perm, "~/.config/opencode/opencode.json")
		if f.Content != s.Config {
			t.Error("plan and SetupFor permanent snippets differ")
		}
	}
}

func TestClaudePlanOutputCap(t *testing.T) {
	in := spec{model: "qwen3-8b", runtime: "llamacpp", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, harness: "claude-code"}.input(t)
	in.Settings.ContextTokens = 40960
	p := build(t, in)
	st := mustStep(t, p, "harness-claude-code")
	f := file(t, st, "~/.trialm/harness/claude-code-llamacpp.json")
	var cfg struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(f.Content), &cfg); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS": fmt.Sprint(p.ContextTokens), "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "8192", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		if cfg.Env[key] != want {
			t.Errorf("%s=%s", key, cfg.Env[key])
		}
	}
	contains(t, "claude parent markers", st.Why, "CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT", "per-session attribution")
}
