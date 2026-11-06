// Package harness knows how coding harnesses connect to a local runtime:
// which protocol they speak, how big their per-request prompt is (which
// dominates local prefill time), and how to point them at cyrlm.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/aeroknight786/cyrlm/internal/predict"
)

// Harness is one coding tool.
type Harness struct {
	ID       string
	Name     string
	Protocol string
	// Protocols are all wire protocols the harness can use with a custom
	// endpoint; a runtime is compatible when it serves one of them.
	Protocols []string
	// Scenario sizes: base prompt (system + tool definitions) as measured
	// or reported for recent versions; these drive the headline numbers.
	Scenario predict.Scenario
	// Binary is the executable for --launch (empty = GUI/editor only).
	Binary string
}

var harnesses = []Harness{
	{ID: "claude-code", Name: "Claude Code", Protocol: "anthropic", Protocols: []string{"anthropic"}, Binary: "claude",
		Scenario: predict.Scenario{Harness: "claude-code", BasePromptTokens: 33000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 45000}},
	{ID: "codex", Name: "Codex CLI", Protocol: "responses", Protocols: []string{"responses"}, Binary: "codex",
		Scenario: predict.Scenario{Harness: "codex", BasePromptTokens: 9000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 25000}},
	{ID: "opencode", Name: "OpenCode", Protocol: "openai-chat", Protocols: []string{"openai-chat", "anthropic"}, Binary: "opencode",
		Scenario: predict.Scenario{Harness: "opencode", BasePromptTokens: 7000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 22000}},
	{ID: "aider", Name: "Aider", Protocol: "ollama", Protocols: []string{"ollama", "openai-chat", "anthropic"}, Binary: "aider",
		Scenario: predict.Scenario{Harness: "aider", BasePromptTokens: 4000, NewTokensPerTurn: 1500, OutputTokensPerTurn: 600, Turns: 20, MidDepth: 12000}},
	{ID: "cline", Name: "Cline", Protocol: "openai-chat", Protocols: []string{"openai-chat", "ollama", "anthropic"},
		Scenario: predict.Scenario{Harness: "cline", BasePromptTokens: 12000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 28000}},
	{ID: "continue", Name: "Continue", Protocol: "ollama", Protocols: []string{"ollama", "openai-chat", "anthropic"},
		Scenario: predict.Scenario{Harness: "continue", BasePromptTokens: 8000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 20000}},
	{ID: "zed", Name: "Zed", Protocol: "ollama", Protocols: []string{"ollama", "openai-chat"},
		Scenario: predict.Scenario{Harness: "zed", BasePromptTokens: 6000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 18000}},
	{ID: "qwen-code", Name: "Qwen Code", Protocol: "openai-chat", Protocols: []string{"openai-chat"}, Binary: "qwen",
		Scenario: predict.Scenario{Harness: "qwen-code", BasePromptTokens: 25000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 40000}},
	{ID: "generic", Name: "Any OpenAI/Ollama-compatible tool", Protocol: "openai-chat", Protocols: []string{"openai-chat", "ollama", "anthropic", "responses"},
		Scenario: predict.Scenario{Harness: "generic", BasePromptTokens: 8000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 20000}},
}

// CompatibleWith reports whether a runtime serving apis can be used directly.
func (h Harness) CompatibleWith(apis []string) bool {
	for _, p := range h.Protocols {
		for _, a := range apis {
			if p == a {
				return true
			}
		}
	}
	return false
}

// All returns the known harnesses.
func All() []Harness { return append([]Harness(nil), harnesses...) }

// Get returns a harness by id ("claude" and "claude-code" both work).
func Get(id string) (Harness, error) {
	n := strings.ToLower(strings.TrimSpace(id))
	switch n {
	case "claude", "claudecode", "cc":
		n = "claude-code"
	case "codex-cli":
		n = "codex"
	case "qwen":
		n = "qwen-code"
	case "", "openai", "ollama":
		n = "generic"
	}
	for _, h := range harnesses {
		if h.ID == n {
			return h, nil
		}
	}
	ids := make([]string, len(harnesses))
	for i, h := range harnesses {
		ids[i] = h.ID
	}
	sort.Strings(ids)
	return Harness{}, fmt.Errorf("unknown harness %q (known: %s)", id, strings.Join(ids, ", "))
}

// Setup is how to connect a harness.
type Setup struct {
	// Env are environment variables for the harness process.
	Env [][2]string
	// Command is the command to run.
	Command []string
	// ConfigPath/Config is a config file snippet to add instead of or in
	// addition to Env.
	ConfigPath string
	Config     string
	// Steps are UI steps for editor-based harnesses.
	Steps []string
	Notes []string
}

// Options for Setup.
type Options struct {
	Port  int
	Model string
	// Tuned adds harness-side settings that keep the local prompt cache
	// working (e.g. Claude Code's attribution header).
	Tuned bool
}

// SetupFor returns connection instructions.
func (h Harness) SetupFor(o Options) Setup {
	base := fmt.Sprintf("http://127.0.0.1:%d", o.Port)
	m := o.Model
	var s Setup
	switch h.ID {
	case "claude-code":
		s.Env = [][2]string{
			{"ANTHROPIC_BASE_URL", base},
			{"ANTHROPIC_AUTH_TOKEN", "cyrlm"},
			{"ANTHROPIC_API_KEY", ""},
			{"ANTHROPIC_MODEL", m},
			{"ANTHROPIC_DEFAULT_HAIKU_MODEL", m},
			{"ANTHROPIC_DEFAULT_SONNET_MODEL", m},
			{"ANTHROPIC_DEFAULT_OPUS_MODEL", m},
		}
		if o.Tuned {
			s.Env = append(s.Env, [2]string{"CLAUDE_CODE_ATTRIBUTION_HEADER", "0"})
			s.Notes = append(s.Notes, "CLAUDE_CODE_ATTRIBUTION_HEADER=0 keeps the system prompt identical across requests so the local prompt cache can be reused (recommended by LM Studio's Claude Code guide).")
		} else {
			s.Notes = append(s.Notes, "Default Claude Code settings: if its system prompt changes per request, every turn re-processes the whole prompt — watch for \"cache miss\" lines. Use --tuned to apply the fix.")
		}
		s.Command = []string{"claude", "--model", m}
	case "codex":
		s.ConfigPath = "~/.codex/config.toml"
		s.Config = fmt.Sprintf(`[model_providers.cyrlm]
name = "cyrlm (simulated local model)"
base_url = "%s/v1"
wire_api = "responses"

[profiles.cyrlm]
model_provider = "cyrlm"
model = "%s"
`, base, m)
		s.Command = []string{"codex", "--profile", "cyrlm"}
		if o.Port == 11434 {
			s.Notes = append(s.Notes, fmt.Sprintf("On port 11434 you can also run: codex --oss -m %s", m))
		}
	case "opencode":
		s.ConfigPath = "opencode.json (project) or ~/.config/opencode/opencode.json"
		s.Config = fmt.Sprintf(`{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "cyrlm": {
      "npm": "@ai-sdk/openai-compatible",
      "name": "cyrlm (simulated local model)",
      "options": { "baseURL": "%s/v1" },
      "models": { "%s": { "name": "%s (simulated)", "tool_call": true } }
    }
  }
}`, base, m, m)
		s.Command = []string{"opencode", "--model", "cyrlm/" + m}
	case "aider":
		s.Env = [][2]string{{"OLLAMA_API_BASE", base}}
		s.Command = []string{"aider", "--model", "ollama_chat/" + m}
	case "cline":
		s.Steps = []string{
			"Cline settings → API Provider: Ollama",
			"Base URL: " + base,
			"Model: " + m,
			fmt.Sprintf("(or API Provider \"OpenAI Compatible\", Base URL %s/v1, any API key, Model ID %s)", base, m),
		}
	case "continue":
		s.ConfigPath = "~/.continue/config.yaml"
		s.Config = fmt.Sprintf(`models:
  - name: %s (cyrlm)
    provider: ollama
    model: %s
    apiBase: %s
    roles: [chat, edit, apply]
    capabilities: [tool_use]
`, m, m, base)
	case "zed":
		s.ConfigPath = "Zed settings.json"
		s.Config = fmt.Sprintf(`"language_models": {
  "ollama": { "api_url": "%s" }
}`, base)
		s.Steps = []string{"Open the Agent panel and pick " + m + " under Ollama"}
	case "qwen-code":
		s.Env = [][2]string{{"OPENAI_BASE_URL", base + "/v1"}, {"OPENAI_API_KEY", "cyrlm"}, {"OPENAI_MODEL", m}}
		s.Command = []string{"qwen"}
	default:
		s.Steps = []string{
			"OpenAI-compatible: base URL " + base + "/v1, any API key, model " + m,
			"Ollama-compatible: host " + base + ", model " + m,
			"Anthropic-compatible: base URL " + base,
		}
	}
	return s
}

// Render formats setup instructions for the terminal.
func (s Setup) Render() string {
	var b strings.Builder
	if s.Config != "" {
		fmt.Fprintf(&b, "Add to %s:\n\n%s\n\n", s.ConfigPath, indent(s.Config, "    "))
	}
	if len(s.Env) > 0 || len(s.Command) > 0 {
		b.WriteString("Run in another terminal:\n\n    ")
		for _, kv := range s.Env {
			fmt.Fprintf(&b, "%s=%s ", kv[0], shellQuote(kv[1]))
		}
		b.WriteString(strings.Join(quoteAll(s.Command), " "))
		b.WriteString("\n\n")
	}
	for _, st := range s.Steps {
		fmt.Fprintf(&b, "  • %s\n", st)
	}
	if len(s.Steps) > 0 {
		b.WriteString("\n")
	}
	for _, n := range s.Notes {
		fmt.Fprintf(&b, "Note: %s\n", n)
	}
	return b.String()
}

// Launch runs the harness command with the environment applied, attached
// to this terminal. It returns an error if the binary is not installed.
func (s Setup) Launch() error {
	if len(s.Command) == 0 {
		return fmt.Errorf("this harness has no command to launch; follow the steps above")
	}
	path, err := exec.LookPath(s.Command[0])
	if err != nil {
		return fmt.Errorf("%s is not installed or not on PATH", s.Command[0])
	}
	cmd := exec.Command(path, s.Command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = os.Environ()
	for _, kv := range s.Env {
		cmd.Env = append(cmd.Env, kv[0]+"="+kv[1])
	}
	return cmd.Run()
}

func indent(s, p string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = p + l
	}
	return strings.Join(lines, "\n")
}

func shellQuote(v string) string {
	if v == "" {
		return `""`
	}
	if strings.ContainsAny(v, " \t\"'$`\\") {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return v
}

func quoteAll(a []string) []string {
	out := make([]string, len(a))
	for i, s := range a {
		out[i] = shellQuote(s)
	}
	return out
}
