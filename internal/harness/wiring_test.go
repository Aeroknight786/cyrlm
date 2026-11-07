package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func envMap(s Setup) map[string]string {
	m := map[string]string{}
	for _, kv := range s.Env {
		m[kv[0]] = kv[1]
	}
	return m
}

func fileContent(t *testing.T, s Setup, suffix string) string {
	t.Helper()
	for p, data := range s.Files {
		if strings.HasSuffix(filepath.ToSlash(p), suffix) {
			return data
		}
	}
	t.Fatalf("no owned file %s in %v", suffix, s.Files)
	return ""
}

func decodeObject(t *testing.T, content string) map[string]any {
	t.Helper()
	var v map[string]any
	if err := json.Unmarshal([]byte(content), &v); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, content)
	}
	return v
}

func TestSetupForEveryHarness(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	for _, h := range All() {
		for _, proto := range []string{"openai-chat", "ollama"} {
			t.Run(h.ID+"/"+proto, func(t *testing.T) {
				s := h.SetupFor(Options{Port: 12345, Model: "test:model", Protocol: proto, Tuned: true, ContextTokens: 73728})
				env := envMap(s)
				if h.Binary == "" {
					if len(s.Command) != 0 || len(s.Files) != 0 || len(s.Steps) == 0 {
						t.Fatalf("manual harness: %+v", s)
					}
				} else if len(s.Command) == 0 {
					t.Fatal("CLI has no command")
				}
				for p := range s.Files {
					if !filepath.IsAbs(p) || !strings.HasPrefix(p, filepath.Join(home, "harness")+string(filepath.Separator)) {
						t.Errorf("not an absolute owned path: %s", p)
					}
				}
				switch h.ID {
				case "claude-code":
					for k, want := range map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:12345", "ANTHROPIC_AUTH_TOKEN": "trialm", "ANTHROPIC_API_KEY": "", "ANTHROPIC_MODEL": "test:model", "ANTHROPIC_DEFAULT_HAIKU_MODEL": "test:model", "ANTHROPIC_DEFAULT_SONNET_MODEL": "test:model", "ANTHROPIC_DEFAULT_OPUS_MODEL": "test:model", "CLAUDE_CODE_MAX_CONTEXT_TOKENS": "73728", "CLAUDE_CODE_MAX_OUTPUT_TOKENS": "18432", "CLAUDE_CODE_ATTRIBUTION_HEADER": "0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
						if got, ok := env[k]; !ok || got != want {
							t.Errorf("%s=%q, want %q", k, got, want)
						}
					}
					if !reflect.DeepEqual(s.Unset, []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}) {
						t.Errorf("unset: %v", s.Unset)
					}
				case "codex":
					cmd := strings.Join(s.Command, " ")
					for _, want := range []string{"wire_api=responses", "base_url=http://127.0.0.1:12345/v1", "model_context_window=73728", "stream_idle_timeout_ms=600000", "-m test:model"} {
						if !strings.Contains(cmd, want) {
							t.Errorf("missing %s: %s", want, cmd)
						}
					}
				case "opencode":
					cfg := decodeObject(t, env["OPENCODE_CONFIG_CONTENT"])
					if env["OPENCODE_DISABLE_MODELS_FETCH"] != "1" || strings.Contains(env["OPENCODE_CONFIG_CONTENT"], "\n") {
						t.Errorf("inline env: %v", env)
					}
					if !reflect.DeepEqual(cfg, decodeObject(t, s.Config)) {
						t.Error("inline and permanent configs differ")
					}
					prov := cfg["provider"].(map[string]any)["trialm"].(map[string]any)
					model := prov["models"].(map[string]any)["test:model"].(map[string]any)
					limit := model["limit"].(map[string]any)
					if limit["context"] != float64(73728) || limit["output"] != float64(18432) {
						t.Errorf("limits: %v", limit)
					}
					if cfg["agent"].(map[string]any)["title"].(map[string]any)["disable"] != true {
						t.Error("title enabled")
					}
					if !reflect.DeepEqual(s.Command, []string{"opencode", "--model", "trialm/test:model"}) {
						t.Errorf("command: %v", s.Command)
					}
				case "aider":
					cmd := strings.Join(s.Command, " ")
					if proto == "ollama" {
						if env["OLLAMA_API_BASE"] != "http://127.0.0.1:12345" || !strings.Contains(cmd, "--model ollama_chat/test:model --model-settings-file ") {
							t.Errorf("Ollama wiring: %+v", s)
						}
						if !strings.Contains(fileContent(t, s, "model.settings.yml"), "num_ctx: 73728") {
							t.Error("num_ctx missing")
						}
					} else {
						if env["OPENAI_API_BASE"] != "http://127.0.0.1:12345/v1" || env["OPENAI_API_KEY"] != "local" || !strings.Contains(cmd, "--model openai/test:model --model-metadata-file ") {
							t.Errorf("OpenAI wiring: %+v", s)
						}
						meta := decodeObject(t, fileContent(t, s, "model.metadata.json"))["openai/test:model"].(map[string]any)
						for k, want := range map[string]any{"max_input_tokens": float64(73728), "max_tokens": float64(73728), "max_output_tokens": float64(8192), "litellm_provider": "openai", "mode": "chat", "input_cost_per_token": float64(0), "output_cost_per_token": float64(0)} {
							if meta[k] != want {
								t.Errorf("%s: %v", k, meta[k])
							}
						}
					}
				case "qwen-code":
					cfg := decodeObject(t, fileContent(t, s, "qwen-code/settings.json"))
					if cfg["model"].(map[string]any)["generationConfig"].(map[string]any)["contextWindowSize"] != float64(73728) || cfg["memory"].(map[string]any)["enableManagedAutoMemory"] != false || cfg["privacy"].(map[string]any)["usageStatisticsEnabled"] != false {
						t.Errorf("settings: %v", cfg)
					}
					if env["QWEN_USAGE_STATISTICS_ENABLED"] != "false" || env["OPENAI_BASE_URL"] != "http://127.0.0.1:12345/v1" || env["OPENAI_API_KEY"] != "trialm" || env["OPENAI_MODEL"] != "test:model" {
						t.Errorf("env: %v", env)
					}
					if _, ok := s.Files[env["QWEN_CODE_SYSTEM_DEFAULTS_PATH"]]; !ok {
						t.Error("env does not point at settings")
					}
				case "cline":
					cfg := decodeObject(t, fileContent(t, s, "cline/providers.json"))
					provider, base := "openai-compatible", "http://127.0.0.1:12345/v1"
					if proto == "ollama" {
						provider, base = "ollama", "http://127.0.0.1:12345"
					}
					if cfg["version"] != float64(1) || cfg["lastUsedProvider"] != provider || len(cfg["modes"].(map[string]any)) != 0 {
						t.Errorf("provider header: %v", cfg)
					}
					p := cfg["providers"].(map[string]any)[provider].(map[string]any)
					if _, err := time.Parse(time.RFC3339, p["updatedAt"].(string)); err != nil {
						t.Error(err)
					}
					settings := p["settings"].(map[string]any)
					if settings["provider"] != provider || settings["baseUrl"] != base || settings["contextWindow"] != float64(73728) || settings["model"] != "test:model" || p["tokenSource"] != "manual" {
						t.Errorf("settings: %v", p)
					}
					if _, ok := s.Files[env["CLINE_PROVIDER_SETTINGS_PATH"]]; !ok {
						t.Error("env does not point at provider file")
					}
				case "continue":
					cfg := fileContent(t, s, "continue/config.yaml")
					for _, want := range []string{"name: triaLM", "version: 0.0.1", "schema: v1", "provider: openai", "apiBase: http://127.0.0.1:12345/v1", "apiKey: trialm", "roles: [chat, edit, apply]", "capabilities: [tool_use]", "defaultCompletionOptions:", "contextLength: 73728", "maxTokens: 4096"} {
						if !strings.Contains(cfg, want) {
							t.Errorf("missing %s in %s", want, cfg)
						}
					}
					if s.Command[0] != "cn" || s.Command[1] != "--config" || s.Files[s.Command[2]] == "" {
						t.Errorf("command: %v", s.Command)
					}
				}
			})
		}
	}
}

func TestSetupModesAndLimits(t *testing.T) {
	t.Setenv("TRIALM_HOME", t.TempDir())
	cc, _ := Get("claude-code")
	for _, tc := range []struct{ ctx, want int }{{32768, 8192}, {65535, 8192}, {65536, 16384}, {131072, 32000}} {
		if got := envMap(cc.SetupFor(Options{Tuned: true, ContextTokens: tc.ctx}))["CLAUDE_CODE_MAX_OUTPUT_TOKENS"]; got != fmt.Sprint(tc.want) {
			t.Errorf("ctx %d: %s", tc.ctx, got)
		}
	}
	for _, id := range []string{"claude-code", "aider", "opencode", "qwen-code"} {
		h, _ := Get(id)
		s := h.SetupFor(Options{ContextTokens: 73728})
		env := envMap(s)
		for _, k := range []string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "QWEN_USAGE_STATISTICS_ENABLED"} {
			if _, ok := env[k]; ok {
				t.Errorf("stock %s sets %s", id, k)
			}
		}
		if id == "aider" && len(s.Files) > 0 {
			t.Error("stock aider pins context")
		}
		if id == "opencode" {
			if _, ok := decodeObject(t, env["OPENCODE_CONFIG_CONTENT"])["agent"]; ok {
				t.Error("stock title disabled")
			}
		}
	}
	for _, ctx := range []int{0, 4096, 65536, 262144} {
		h, _ := Get("opencode")
		s := h.SetupFor(Options{Port: 12345, Model: `model"with'quotes`, ContextTokens: ctx, Bridge: true})
		cfg := envMap(s)["OPENCODE_CONFIG_CONTENT"]
		decodeObject(t, cfg)
		if strings.Contains(cfg, "simulated") || s.Command[2] != `trialm-local/model"with'quotes` {
			t.Errorf("bridge label: %+v", s)
		}
	}
	cn, _ := Get("continue")
	if cfg := fileContent(t, cn.SetupFor(Options{ContextTokens: 8192}), "continue/config.yaml"); !strings.Contains(cfg, "maxTokens: 1024") {
		t.Error(cfg)
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name           string
		env, installed []string
		want           string
		fallback       bool
	}{
		{"parent claude", []string{"CLAUDECODE=1", "CODEX_HOME=/tmp/codex"}, []string{"codex"}, "claude-code", false},
		{"entrypoint", []string{"CLAUDE_CODE_ENTRYPOINT=cli"}, nil, "claude-code", false},
		{"parent codex", []string{"CODEX_THREAD_ID=abc"}, []string{"claude"}, "codex", false},
		{"empty markers", []string{"CLAUDECODE=", "CODEX_HOME="}, []string{"opencode"}, "opencode", false},
		{"order", nil, []string{"aider", "cn", "cline", "qwen", "opencode", "codex", "claude"}, "claude-code", false},
		{"none", nil, nil, "claude-code", true},
	}
	for _, pair := range [][2]string{{"codex", "codex"}, {"opencode", "opencode"}, {"qwen", "qwen-code"}, {"cline", "cline"}, {"cn", "continue"}, {"aider", "aider"}} {
		cases = append(cases, struct {
			name           string
			env, installed []string
			want           string
			fallback       bool
		}{pair[0], nil, []string{pair[0]}, pair[1], false})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, note := Detect(tc.env, func(name string) (string, error) {
				for _, b := range tc.installed {
					if name == b {
						return "/bin/" + b, nil
					}
				}
				return "", errors.New("missing")
			})
			if h.ID != tc.want || (note != "") != tc.fallback {
				t.Errorf("got %s %q", h.ID, note)
			}
		})
	}
}

func TestLaunchWritesFilesAndScrubsParent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "cli")
	t.Setenv("ANTHROPIC_BASE_URL", "wrong-parent")
	h, _ := Get("claude-code")
	s := h.SetupFor(Options{Port: 12345, Model: "local"})
	out := filepath.Join(home, "child-env.json")
	s.Env = append(s.Env, [2]string{"TRIALM_LAUNCH_HELPER", out})
	s.Command = []string{os.Args[0], "-test.run=^TestLaunchHelper$"}
	owned := filepath.Join(home, "harness", "private.json")
	s.Files = map[string]string{owned: `{"apiKey":"secret"}`}
	if err := s.Launch(); err != nil {
		t.Fatal(err)
	}
	var env []string
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &env); err != nil {
		t.Fatal(err)
	}
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDECODE=") || strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=") || kv == "ANTHROPIC_BASE_URL=wrong-parent" {
			t.Errorf("leaked parent: %s", kv)
		}
	}
	if !strings.Contains(strings.Join(env, "\n"), "ANTHROPIC_BASE_URL=http://127.0.0.1:12345") {
		t.Error("missing child override")
	}
	data, err = os.ReadFile(owned)
	if err != nil || string(data) != s.Files[owned] {
		t.Fatalf("config not written: %s %v", data, err)
	}
	fi, _ := os.Stat(owned)
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0600 {
		t.Errorf("mode %o", fi.Mode().Perm())
	}
}

func TestLaunchHelper(t *testing.T) {
	if p := os.Getenv("TRIALM_LAUNCH_HELPER"); p != "" {
		data, _ := json.Marshal(os.Environ())
		if err := os.WriteFile(p, data, 0600); err != nil {
			os.Exit(2)
		}
		os.Exit(0)
	}
}

func TestWriteFilesAndLaunchErrors(t *testing.T) {
	if got := (Setup{}).childEnv([]string{"CLAUDECODE=1", "CLAUDE_CODE_ENTRYPOINT=cli", "KEEP=yes"}); !reflect.DeepEqual(got, []string{"KEEP=yes"}) {
		t.Errorf("custom launch retained parent markers: %v", got)
	}
	p := filepath.Join(t.TempDir(), "harness", "settings.json")
	s := Setup{Files: map[string]string{p: "private"}}
	if err := s.WriteFiles(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, 0644); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteFiles(); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0600 {
		t.Errorf("existing mode %o", fi.Mode().Perm())
	}
	if err := (Setup{Files: map[string]string{"relative": "x"}}).WriteFiles(); err == nil {
		t.Error("accepted relative path")
	}
	if err := s.Launch(); err == nil || !strings.Contains(err.Error(), "no command to launch") {
		t.Errorf("launch: %v", err)
	}
	if err := (Setup{Command: []string{"trialm-nonexistent-harness-test"}}).Launch(); err == nil {
		t.Error("launched missing binary")
	}
}

func TestOpenCodePrintedShellJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell environment is not reliable on Windows")
	}
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("POSIX shell unavailable")
	}
	h, _ := Get("opencode")
	s := h.SetupFor(Options{Port: 12345, Model: "a'quoted\\model\"", Tuned: true, ContextTokens: 65536})
	s.Command = []string{"env"}
	rendered := s.RenderFor("linux")
	var line string
	for _, l := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(l, "    OPENCODE_CONFIG_CONTENT=") {
			line = strings.TrimSpace(l)
			break
		}
	}
	out, err := exec.Command("sh", "-c", line).Output()
	if err != nil {
		t.Fatalf("printed command: %v\n%s", err, line)
	}
	want := "OPENCODE_CONFIG_CONTENT=" + envMap(s)["OPENCODE_CONFIG_CONTENT"]
	if !strings.Contains(string(out), want) {
		t.Fatalf("JSON changed in shell: %s", out)
	}
	ps := s.RenderFor("windows")
	if !strings.Contains(ps, "a''quoted") || !strings.Contains(ps, "$env:OPENCODE_CONFIG_CONTENT='") {
		t.Error(ps)
	}
}

func TestScenarioAndCompatMetadata(t *testing.T) {
	for _, h := range All() {
		switch h.ID {
		case "zed", "generic":
			if h.Compat.Status != "manual" {
				t.Error(h)
			}
		case "codex":
			if h.Compat.Status != "untested" {
				t.Error(h)
			}
		default:
			if h.Compat.Status != "fake-upstream" || h.Compat.Version == "" || h.Compat.Date != "2026-11-06" || h.Compat.OS != "linux" {
				t.Error(h)
			}
		}
	}
	cc, _ := Get("claude-code")
	if cc.Scenario.BasePromptTokens != 12000 || cc.Scenario.MinContext != 36864 {
		t.Error(cc.Scenario)
	}
	q, _ := Get("qwen-code")
	if q.Scenario.BasePromptTokens != 13000 || q.Scenario.MidDepth != 30000 {
		t.Error(q.Scenario)
	}
	cl, _ := Get("cline")
	if cl.Scenario.BasePromptTokens != 5500 || cl.ProtocolFor([]string{"anthropic"}) != "" {
		t.Error(cl)
	}
	cn, _ := Get("continue")
	if cn.Scenario.BasePromptTokens != 2000 || cn.Protocol != "openai-chat" {
		t.Error(cn)
	}
}
