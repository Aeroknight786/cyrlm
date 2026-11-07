// Package harness knows how coding harnesses connect to a local runtime:
// which protocol they speak, how big their per-request prompt is (which
// dominates local prefill time), and how to point them at trialm.
package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/predict"
)

// Compat records the scope and version of a harness wiring test.
type Compat struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
	Date    string `json:"date,omitempty"`
	OS      string `json:"os,omitempty"`
}

// Harness describes a coding CLI or a manual editor integration.
type Harness struct {
	Compat   Compat `json:"compat"`
	ID       string
	Name     string
	Protocol string
	// Protocols are all wire protocols the harness can use with a custom
	// endpoint; a runtime is compatible when it serves one of them.
	Protocols []string
	// Scenario sizes: base prompt (system + tool definitions) as measured
	// or reported for recent versions; these drive the headline numbers.
	Scenario predict.Scenario
	// Binary is the executable for --launch (empty = manual setup).
	Binary string
}

var harnesses = []Harness{
	{Compat: Compat{Status: "fake-upstream", Version: "2.1.319", Date: "2026-11-06", OS: "linux"}, ID: "claude-code", Name: "Claude Code", Protocol: "anthropic", Protocols: []string{"anthropic"}, Binary: "claude",
		Scenario: predict.Scenario{Harness: "claude-code", BasePromptTokens: 12000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 45000, MinContext: 36864}},
	{Compat: Compat{Status: "untested"}, ID: "codex", Name: "Codex CLI", Protocol: "responses", Protocols: []string{"responses"}, Binary: "codex",
		Scenario: predict.Scenario{Harness: "codex", BasePromptTokens: 9000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 25000, MinContext: 65536}},
	{Compat: Compat{Status: "fake-upstream", Version: "1.19.5", Date: "2026-11-06", OS: "linux"}, ID: "opencode", Name: "OpenCode", Protocol: "openai-chat", Protocols: []string{"openai-chat", "anthropic"}, Binary: "opencode",
		Scenario: predict.Scenario{Harness: "opencode", BasePromptTokens: 7000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 22000, MinContext: 65536}},
	{Compat: Compat{Status: "fake-upstream", Version: "0.86.2", Date: "2026-11-06", OS: "linux"}, ID: "aider", Name: "Aider", Protocol: "ollama", Protocols: []string{"ollama", "openai-chat", "anthropic"}, Binary: "aider",
		Scenario: predict.Scenario{Harness: "aider", BasePromptTokens: 4000, NewTokensPerTurn: 1500, OutputTokensPerTurn: 600, Turns: 20, MidDepth: 12000}},
	{Compat: Compat{Status: "fake-upstream", Version: "3.0.74", Date: "2026-11-06", OS: "linux"}, ID: "cline", Name: "Cline", Protocol: "openai-chat", Protocols: []string{"openai-chat", "ollama"}, Binary: "cline",
		Scenario: predict.Scenario{Harness: "cline", BasePromptTokens: 5500, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 28000}},
	{Compat: Compat{Status: "fake-upstream", Version: "1.5.47", Date: "2026-11-06", OS: "linux"}, ID: "continue", Name: "Continue", Protocol: "openai-chat", Protocols: []string{"openai-chat"}, Binary: "cn",
		Scenario: predict.Scenario{Harness: "continue", BasePromptTokens: 2000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 20000}},
	{Compat: Compat{Status: "manual"}, ID: "zed", Name: "Zed", Protocol: "ollama", Protocols: []string{"ollama", "openai-chat"},
		Scenario: predict.Scenario{Harness: "zed", BasePromptTokens: 6000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 18000}},
	{Compat: Compat{Status: "fake-upstream", Version: "0.26.9", Date: "2026-11-06", OS: "linux"}, ID: "qwen-code", Name: "Qwen Code", Protocol: "openai-chat", Protocols: []string{"openai-chat"}, Binary: "qwen",
		Scenario: predict.Scenario{Harness: "qwen-code", BasePromptTokens: 13000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 30000}},
	{Compat: Compat{Status: "manual"}, ID: "generic", Name: "Any OpenAI/Ollama-compatible tool", Protocol: "openai-chat", Protocols: []string{"openai-chat", "ollama", "anthropic", "responses"},
		Scenario: predict.Scenario{Harness: "generic", BasePromptTokens: 8000, NewTokensPerTurn: 2000, OutputTokensPerTurn: 300, Turns: 30, MidDepth: 20000}},
}

// CompatibleWith reports whether a runtime serving apis can be used directly.
func (h Harness) CompatibleWith(apis []string) bool {
	return h.ProtocolFor(apis) != "" || h.NeedsBridge(apis)
}

// NeedsBridge reports whether the harness should reach a runtime with
// these APIs through triaLM's bridge: whenever the runtime has Chat
// Completions for the bridge to call. Local engines apply the model's own
// chat template, and strict ones (Qwen, Gemma, Mistral) reject what tools
// send: Claude Code's mid-conversation system messages, Codex's message
// layout. The bridge normalises every request (upstream.NormalizeForTemplates)
// and speaks each tool's protocol.
func (h Harness) NeedsBridge(apis []string) bool { return slices.Contains(apis, "openai-chat") }

// ProtocolFor returns the harness's preferred protocol among apis ("" if
// none matches).
func (h Harness) ProtocolFor(apis []string) string {
	for _, p := range h.Protocols {
		for _, a := range apis {
			if p == a {
				return p
			}
		}
	}
	return ""
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

// CodexCommand is the codex command line that talks to a Responses
// endpoint at baseURL (".../v1") with model, using -c overrides only.
func CodexCommand(baseURL, model string, contextTokens int) []string {
	c := []string{FindBinary("codex"),
		"-c", "model_providers.trialm.name=trialm",
		"-c", "model_providers.trialm.base_url=" + baseURL,
		"-c", "model_providers.trialm.wire_api=responses",
		"-c", "model_providers.trialm.stream_idle_timeout_ms=600000",
		"-c", "model_provider=trialm",
		"-c", "web_search=disabled",
	}
	if contextTokens > 0 {
		c = append(c, "-c", fmt.Sprintf("model_context_window=%d", contextTokens),
			"-c", fmt.Sprintf("model_auto_compact_token_limit=%d", contextTokens*8/10))
	}
	return append(c, "-m", model)
}

// KnownBinaries are install locations that are often missing from the
// user's shell PATH (an app bundle, a per-user npm prefix).
var KnownBinaries = map[string][]string{
	"codex": {
		"/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex",
		"/Applications/Codex.app/Contents/Resources/codex",
	},
	"claude": {"~/.claude/local/claude", "~/.local/bin/claude"},
}

// FindBinary returns name when it is on PATH in a normal location, or an
// absolute path when the harness lives somewhere the user's own terminal
// may not look (inside an .app bundle, or only in a known install dir).
func FindBinary(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		if strings.Contains(p, ".app/") {
			return p
		}
		return name
	}
	home, _ := os.UserHomeDir()
	for _, p := range KnownBinaries[name] {
		if strings.HasPrefix(p, "~/") {
			p = filepath.Join(home, p[2:])
		}
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return name
}

// Setup is how to connect a harness.
type Setup struct {
	// Files are complete, triaLM-owned files to write before printing or launch.
	Files map[string]string
	// Unset removes parent-only variables from the launched child.
	Unset []string
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
	// Bridge selects real local runtime labels, also used by permanent setup.
	Bridge bool
	// Home overrides the triaLM home for plan generation; runtime paths are absolute.
	Home      string
	Namespace string
	Port      int
	Model     string
	// Tuned adds harness-side settings that keep the local prompt cache
	// working (e.g. Claude Code's attribution header).
	Tuned bool
	// Protocol to connect with ("" = the harness's default). Aider,
	// Continue and Zed configs differ between Ollama and OpenAI-compatible.
	Protocol string
	// ContextTokens, when set with Tuned, tells harnesses that assume a
	// large window (Claude Code: 200K) the real local context size.
	ContextTokens int
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
			{"ANTHROPIC_AUTH_TOKEN", "trialm"},
			{"ANTHROPIC_API_KEY", ""},
			{"ANTHROPIC_MODEL", m},
			{"ANTHROPIC_DEFAULT_HAIKU_MODEL", m},
			{"ANTHROPIC_DEFAULT_SONNET_MODEL", m},
			{"ANTHROPIC_DEFAULT_OPUS_MODEL", m},
		}
		if o.Tuned && o.ContextTokens > 0 {
			s.Env = append(s.Env, [2]string{"CLAUDE_CODE_MAX_CONTEXT_TOKENS", fmt.Sprint(o.ContextTokens)},
				[2]string{"CLAUDE_CODE_MAX_OUTPUT_TOKENS", fmt.Sprint(claudeOutputTokens(o.ContextTokens))})
			s.Notes = append(s.Notes, "Claude Code keeps up to 20k tokens of the window free for its reply; triaLM lowers that so a context below 64k does not compact on every turn.")
		}
		if o.Tuned {
			s.Env = append(s.Env, [2]string{"CLAUDE_CODE_ATTRIBUTION_HEADER", "0"}, [2]string{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1"})
			s.Notes = append(s.Notes, "CLAUDE_CODE_ATTRIBUTION_HEADER=0 removes a per-session attribution line at the start of the system prompt, so new sessions and subagents reuse the cached prompt.")
			s.Notes = append(s.Notes, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1 stops Claude Code's telemetry, bootstrap and update calls to Anthropic; model requests go to the configured endpoint.")
		} else {
			s.Notes = append(s.Notes, "Default Claude Code settings: the attribution header varies between sessions and subagents, which prevents shared prompt-cache reuse. triaLM's tuned settings (the default) remove it.")
		}
		s.Command = []string{"claude", "--model", m}
		s.Unset = []string{"CLAUDECODE", "CLAUDE_CODE_ENTRYPOINT"}
		s.Notes = append(s.Notes, `Headless: claude -p "<task>" --permission-mode acceptEdits --allowedTools Bash < /dev/null (the redirect avoids a 3-second stdin wait).`)
	case "codex":
		// Everything goes on the command line (-c overrides): no config file
		// edit, and nothing that depends on Codex's config/profile layout,
		// which changed in 0.134. Values without quotes are read as strings,
		// so the command is the same in every shell.
		s.Command = CodexCommand(base+"/v1", m, o.ContextTokens)
		s.Notes = append(s.Notes, "Codex may warn \"Model metadata ... not found\" for local models; that is expected. Web search is turned off because a local model cannot run OpenAI's hosted search.")
	case "opencode":
		cfg, id := OpenCodeConfig(o)
		s.Env = [][2]string{{"OPENCODE_CONFIG_CONTENT", compactJSON(cfg)}, {"OPENCODE_DISABLE_MODELS_FETCH", "1"}}
		s.ConfigPath = "~/.config/opencode/opencode.json"
		s.Config = prettyJSON(cfg)
		s.Command = []string{"opencode", "--model", id + "/" + m}
		if o.Tuned {
			s.Notes = append(s.Notes, "Title generation is off: on a single-slot local runtime it costs an extra prefill and evicts the prompt cache.")
		}
		s.Notes = append(s.Notes, `Headless: opencode run --model `+id+`/`+m+` "<task>" < /dev/null`)
	case "aider":
		if o.Protocol == "openai-chat" {
			s.Env = [][2]string{{"OPENAI_API_BASE", base + "/v1"}, {"OPENAI_API_KEY", "local"}}
			s.Command = []string{"aider", "--model", "openai/" + m}
			if o.Tuned && o.ContextTokens > 0 {
				p := o.ownedPath("aider/model.metadata.json")
				s.Files = map[string]string{p: prettyJSON(map[string]any{"openai/" + m: map[string]any{
					"max_input_tokens": o.ContextTokens, "max_tokens": o.ContextTokens, "max_output_tokens": 8192,
					"input_cost_per_token": 0, "output_cost_per_token": 0, "litellm_provider": "openai", "mode": "chat",
				}})}
				s.Command = append(s.Command, "--model-metadata-file", p)
			}
		} else {
			s.Env = [][2]string{{"OLLAMA_API_BASE", base}}
			s.Command = []string{"aider", "--model", "ollama_chat/" + m}
			if o.Tuned && o.ContextTokens > 0 {
				p := o.ownedPath("aider/model.settings.yml")
				s.Files = map[string]string{p: fmt.Sprintf("- name: %s\n  extra_params:\n    num_ctx: %d\n", compactJSON("ollama_chat/"+m), o.ContextTokens)}
				s.Command = append(s.Command, "--model-settings-file", p)
			}
		}
		s.Notes = append(s.Notes, "Aider needs Python 3.10–3.12: pipx install aider-chat or uv tool install --python 3.12 aider-chat.", `One-shot: add --message "<task>" --yes-always (and --no-auto-commits if wanted).`)
	case "cline":
		provider, api, key, label := "openai-compatible", base+"/v1", "trialm", "OpenAI Compatible"
		if o.Protocol == "ollama" {
			provider, api, key, label = "ollama", base, "local", "Ollama"
		}
		settings := map[string]any{"provider": provider, "apiKey": key, "baseUrl": api, "model": m}
		if o.ContextTokens > 0 {
			settings["contextWindow"] = o.ContextTokens
		}
		p := o.ownedPath("cline/providers.json")
		s.Files = map[string]string{p: prettyJSON(map[string]any{
			"version": 1, "lastUsedProvider": provider, "modes": map[string]any{},
			"providers": map[string]any{provider: map[string]any{"settings": settings, "updatedAt": time.Now().UTC().Format(time.RFC3339), "tokenSource": "manual"}},
		})}
		s.Env = [][2]string{{"CLINE_PROVIDER_SETTINGS_PATH", p}}
		s.Command = []string{"cline"}
		extension := fmt.Sprintf("VS Code extension: API Provider %s, Base URL %s, any API key, Model ID %s", label, api, m)
		if o.ContextTokens > 0 {
			extension += fmt.Sprintf("; Model Configuration → Context Window Size %d", o.ContextTokens)
		}
		s.Steps = []string{extension}
		s.Notes = append(s.Notes, `One-shot: cline "<task>" auto-approves by default; --json gives machine output.`)
		if provider == "openai-compatible" {
			s.Notes = append(s.Notes, fmt.Sprintf("Alternative: cline auth -p openai-compatible -k trialm -b %s -m %s (changes Cline's default provider).", api, m))
		}
	case "continue":
		p := o.ownedPath("continue/config.yaml")
		cfg := fmt.Sprintf("name: triaLM\nversion: 0.0.1\nschema: v1\nmodels:\n  - name: %s\n    provider: openai\n    model: %s\n    apiBase: %s/v1\n    apiKey: trialm\n    roles: [chat, edit, apply]\n    capabilities: [tool_use]\n", compactJSON(m+" (triaLM)"), compactJSON(m), base)
		if o.ContextTokens > 0 {
			cfg += fmt.Sprintf("    defaultCompletionOptions:\n      contextLength: %d\n      maxTokens: %d\n", o.ContextTokens, min(4096, o.ContextTokens/8))
		}
		s.Files = map[string]string{p: cfg}
		s.Command = []string{"cn", "--config", p}
		s.Notes = append(s.Notes, "For the VS Code/JetBrains extension, merge the models entry into ~/.continue/config.yaml.", "For fewer prompts add --allow Write --allow Edit (or --auto); cn -p drops Write/Edit unless allowed.")
	case "zed":
		if o.Protocol == "openai-chat" {
			s.Steps = []string{"Zed → Agent panel settings → add an OpenAI-compatible provider: API URL " + base + "/v1, model " + m}
			break
		}
		s.ConfigPath = "Zed settings.json"
		s.Config = fmt.Sprintf(`"language_models": {
  "ollama": { "api_url": "%s" }
}`, base)
		s.Steps = []string{"Open the Agent panel and pick " + m + " under Ollama"}
	case "qwen-code":
		s.Env = [][2]string{{"OPENAI_BASE_URL", base + "/v1"}, {"OPENAI_API_KEY", "trialm"}, {"OPENAI_MODEL", m}}
		s.Command = []string{"qwen"}
		if o.Tuned {
			s.Env = append(s.Env, [2]string{"QWEN_USAGE_STATISTICS_ENABLED", "false"})
		}
		if o.ContextTokens > 0 {
			p := o.ownedPath("qwen-code/settings.json")
			s.Files = map[string]string{p: prettyJSON(map[string]any{
				"model":   map[string]any{"generationConfig": map[string]int{"contextWindowSize": o.ContextTokens}},
				"memory":  map[string]bool{"enableManagedAutoMemory": false},
				"privacy": map[string]bool{"usageStatisticsEnabled": false},
			})}
			s.Env = append(s.Env, [2]string{"QWEN_CODE_SYSTEM_DEFAULTS_PATH", p})
		}
		s.Notes = append(s.Notes, `Headless / scripted: qwen "<task>" --approval-mode auto-edit --allowed-tools run_shell_command (the default and auto modes hide the shell tool; put the prompt before --allowed-tools).`, "Qwen Code sends usage statistics to Alibaba RUM and fetches models.dev unless QWEN_USAGE_STATISTICS_ENABLED=false; triaLM sets this in tuned mode.")
	default:
		s.Steps = []string{
			"OpenAI-compatible: base URL " + base + "/v1, any API key, model " + m,
			"Ollama-compatible: host " + base + ", model " + m,
			"Anthropic-compatible: base URL " + base,
		}
	}
	return s
}

// Render formats setup instructions for this OS's terminal.
func (s Setup) Render() string { return s.RenderFor(goruntime.GOOS) }

// RenderFor formats setup instructions for goos ("windows" uses PowerShell).
func (s Setup) RenderFor(goos string) string {
	var b strings.Builder
	if len(s.Env) > 0 || len(s.Command) > 0 {
		if goos == "windows" {
			b.WriteString("Run in a new PowerShell window:\n\n    ")
			for _, name := range s.Unset {
				fmt.Fprintf(&b, "Remove-Item Env:%s -ErrorAction SilentlyContinue; ", name)
			}
			for _, kv := range s.Env {
				if kv[1] == "" {
					fmt.Fprintf(&b, "Remove-Item Env:%s -ErrorAction SilentlyContinue; ", kv[0])
				} else {
					fmt.Fprintf(&b, "$env:%s='%s'; ", kv[0], strings.ReplaceAll(kv[1], "'", "''"))
				}
			}
			b.WriteString(strings.Join(psCommand(s.Command), " "))
		} else {
			b.WriteString("Run in a new terminal window:\n\n    ")
			if len(s.Unset) > 0 {
				b.WriteString("env ")
				for _, name := range s.Unset {
					fmt.Fprintf(&b, "-u %s ", name)
				}
			}
			for _, kv := range s.Env {
				fmt.Fprintf(&b, "%s=%s ", kv[0], shellQuote(kv[1]))
			}
			b.WriteString(strings.Join(quoteAll(s.Command), " "))
		}
		b.WriteString("\n\n")
	}
	if s.Config != "" {
		fmt.Fprintf(&b, "Optional: to make it permanent, merge into %s:\n\n%s\n\n", s.ConfigPath, indent(s.Config, "    "))
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
	if err := s.WriteFiles(); err != nil {
		return err
	}
	if len(s.Command) == 0 {
		return fmt.Errorf("this harness has no command to launch; follow the steps above")
	}
	path, err := exec.LookPath(FindBinary(s.Command[0]))
	if err != nil {
		return fmt.Errorf("%s is not installed or not on PATH", s.Command[0])
	}
	cmd := exec.Command(path, s.Command[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	cmd.Env = s.childEnv(os.Environ())
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
	if strings.ContainsAny(v, " \t\r\n\"'$`\\;&|<>(){}[]*?!#~") {
		return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
	}
	return v
}

// psCommand quotes a command for PowerShell: arguments with spaces or
// quotes in single quotes, and a call operator when the program path needs
// quoting.
func psCommand(a []string) []string {
	out := make([]string, len(a))
	for i, v := range a {
		if strings.ContainsAny(v, " '\"$`;&|(){}") {
			v = "'" + strings.ReplaceAll(v, "'", "''") + "'"
			if i == 0 {
				v = "& " + v
			}
		}
		out[i] = v
	}
	return out
}

func quoteAll(a []string) []string {
	out := make([]string, len(a))
	for i, s := range a {
		out[i] = shellQuote(s)
	}
	return out
}

// DisplayName is a tool's name for people ("Codex CLI", not "codex").
func DisplayName(id string) string {
	if h, err := Get(id); err == nil && h.Name != "" {
		return h.Name
	}
	return id
}
