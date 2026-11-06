package setup

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aeroknight786/cyrlm/internal/harness"
	"github.com/aeroknight786/cyrlm/internal/runtime"
)

const (
	docClaudeSettings = "https://code.claude.com/docs/en/settings"
	docClaudeEnv      = "https://code.claude.com/docs/en/env-vars"
	docClaudeWindow   = "https://code.claude.com/docs/en/model-config#correct-the-window-for-a-gateway-or-custom-model-id"
	docCodexConfig    = "https://github.com/openai/codex/blob/main/docs/config.md"
	docOpenCode       = "https://opencode.ai/docs/providers/"
	docContinue       = "https://docs.continue.dev/reference"
)

// protocolNames are human names of harness protocols.
var protocolNames = map[string]string{
	"anthropic":   "Anthropic Messages API",
	"responses":   "OpenAI Responses API",
	"openai-chat": "OpenAI Chat Completions API",
	"ollama":      "Ollama API",
}

func (b *builder) harnessSteps() {
	h := b.in.Harness
	pl := b.plan
	if h == nil {
		pl.HarnessCompatible = true
		return
	}
	pl.Harness = h.ID
	proto := h.ProtocolFor(b.p.APIs)
	if proto == "" {
		pl.HarnessCompatible = false
		pl.HarnessNote = incompatibleNote(*h, b.p, b.in.Quant.Format, b.in.OS, b.in.Arch)
		b.warn(pl.HarnessNote)
		return
	}
	pl.HarnessCompatible = true
	pl.HarnessNote = fmt.Sprintf("%s connects to %s's %s at %s as model %s.", h.Name, b.p.Name, protocolNames[proto], pl.Endpoint, pl.LocalModelName)

	s := h.SetupFor(harness.Options{Port: b.port, Model: pl.LocalModelName, Tuned: pl.Tuned, Protocol: proto, ContextTokens: pl.ContextTokens})
	switch h.ID {
	case "claude-code":
		b.claudeCodeSteps(s)
	case "codex":
		b.codexStep()
	case "opencode":
		b.openCodeStep()
	case "continue":
		b.add(Step{
			ID: "harness-continue", Kind: KindHarness,
			Title: "Add " + pl.LocalModelName + " to Continue",
			Why:   "Adds the model to Continue's model list, using " + b.p.Name + "'s Ollama API.",
			Files: []FileEdit{{
				Path: "~/.continue/config.yaml", Format: "yaml", Content: s.Config, Merge: "merge",
				Note: "Add the entry to the existing models list (create the file with `name`, `version` and `schema` keys if it does not exist); back up the file first.",
			}},
			Restart:    "Continue (reload the editor window)",
			Confidence: Medium,
			Docs:       []string{docContinue},
		})
	default:
		b.manualHarnessStep(*h, s)
	}
}

// incompatibleNote explains why a harness cannot talk to the runtime and
// which runtimes would work.
func incompatibleNote(h harness.Harness, p runtime.Profile, format, goos, goarch string) string {
	var ok []string
	for _, r := range runtime.All() {
		if r.ID != p.ID && h.CompatibleWith(r.APIs) && has(r.Formats, format) && len(r.Backends(goos, goarch)) > 0 {
			ok = append(ok, r.Name)
		}
	}
	proto := protocolNames[h.Protocol]
	if proto == "" {
		proto = h.Protocol
	}
	note := fmt.Sprintf("%s needs the %s, which %s does not serve according to cyrlm's runtime profile (it serves %s), so no harness step is included.", h.Name, proto, p.Name, apiList(p.APIs))
	if len(ok) > 0 {
		note += " Use " + strings.Join(ok, " or ") + " instead, or keep `cyrlm serve` running in front as a translator."
	} else {
		note += " Keep `cyrlm serve` running in front as a translator."
	}
	return note
}

func apiList(apis []string) string {
	out := make([]string, len(apis))
	for i, a := range apis {
		if n, ok := protocolNames[a]; ok {
			out[i] = n
		} else {
			out[i] = a
		}
	}
	return strings.Join(out, ", ")
}

func (b *builder) claudeCodeSteps(s harness.Setup) {
	pl := b.plan
	env := map[string]string{}
	for _, kv := range s.Env {
		// An empty ANTHROPIC_API_KEY in a settings file only blanks a
		// real key; ANTHROPIC_AUTH_TOKEN already authenticates.
		if kv[0] == "ANTHROPIC_API_KEY" && kv[1] == "" {
			continue
		}
		env[kv[0]] = kv[1]
	}
	// Claude Code assumes 200K+ tokens for unknown model ids and would
	// overflow the local context before compacting.
	env["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] = fmt.Sprint(pl.ContextTokens)
	content, _ := json.MarshalIndent(map[string]any{"env": env}, "", "  ")

	rel := ".cyrlm/harness/claude-code-" + pl.Runtime + ".json"
	launch := "claude --settings " + b.home(rel)
	why := fmt.Sprintf("Writes a separate Claude Code settings file that points it at %s (ANTHROPIC_BASE_URL=%s) for every model alias, so only sessions started with --settings use the local model; your normal Claude Code setup is untouched. CLAUDE_CODE_MAX_CONTEXT_TOKENS=%d makes Claude Code compact before the local context is full.", b.p.Name, pl.Endpoint, pl.ContextTokens)
	if pl.Tuned {
		why += " CLAUDE_CODE_ATTRIBUTION_HEADER=0 keeps the system prompt identical across requests so the local prompt cache is reused."
	}
	why += " Unset ANTHROPIC_API_KEY in the shell you start it from. Start it with: " + launch
	b.add(Step{
		ID: "harness-claude-code", Kind: KindHarness,
		Title: "Connect Claude Code to " + pl.LocalModelName,
		Why:   why,
		Files: []FileEdit{{
			Path: "~/" + rel, Format: "json", Content: string(content) + "\n", Merge: "create",
			Note: "Use with: " + launch,
		}},
		Restart:    "Claude Code: start a new session with `" + launch + "`",
		Undo:       []Command{b.os(b.rm(rel))},
		Confidence: High,
		Docs:       []string{docClaudeSettings, docClaudeEnv, docClaudeWindow},
	})

	b.add(Step{
		ID: "harness-claude-code-default", Kind: KindHarness,
		Title:    "Optional: make the local model Claude Code's default",
		Why:      "Merges the same env block into ~/.claude/settings.json so every new Claude Code session uses the local model, including the agent running this plan. Skip this if you also use Claude Code with your Anthropic account.",
		Optional: true,
		Files: []FileEdit{{
			Path: "~/.claude/settings.json", Format: "json", Content: string(content) + "\n", Merge: "merge",
			Note: "Merge the keys into the existing \"env\" object; back the file up to ~/.claude/settings.json.bak-cyrlm first.",
		}},
		Restart:    "Claude Code (new sessions)",
		Undo:       []Command{b.os(b.restoreBackup(".claude/settings.json"))},
		Confidence: High,
		Docs:       []string{docClaudeSettings},
	})
}

func (b *builder) codexStep() {
	pl := b.plan
	id := "cyrlm-" + pl.Runtime
	content := fmt.Sprintf(`[model_providers.%s]
name = "%s (local)"
base_url = "%s/v1"
wire_api = "responses"

[profiles.%s]
model_provider = "%s"
model = "%s"
`, id, shortName(b.p), pl.Endpoint, id, id, pl.LocalModelName)
	launch := "codex --profile " + id
	why := fmt.Sprintf("Adds a model provider for %s's Responses API at %s/v1 and a profile that selects %s, so `%s` uses the local model and plain `codex` keeps its current setup.", b.p.Name, pl.Endpoint, pl.LocalModelName, launch)
	if pl.Runtime == "ollama" || pl.Runtime == "lmstudio" {
		why += fmt.Sprintf(" (Codex's built-in --oss provider also works: `codex --oss -m %s`.)", pl.LocalModelName)
	}
	if pl.ContextTokens < 64000 {
		b.warn(fmt.Sprintf("Codex recommends a context window of at least 64k tokens; this plan uses %d.", pl.ContextTokens))
	}
	b.add(Step{
		ID: "harness-codex", Kind: KindHarness,
		Title: "Connect Codex to " + pl.LocalModelName,
		Why:   why,
		Files: []FileEdit{{
			Path: "~/.codex/config.toml", Format: "toml", Content: content, Merge: "merge",
			Note: "Add these tables (replace them if they exist); back the file up to ~/.codex/config.toml.bak-cyrlm first.",
		}},
		Restart:    "Codex: start it with `" + launch + "`",
		Undo:       []Command{b.os(b.restoreBackup(".codex/config.toml"))},
		Confidence: High,
		Docs:       []string{docCodexConfig},
	})
}

func (b *builder) openCodeStep() {
	pl := b.plan
	id := "cyrlm-" + pl.Runtime
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{
			id: map[string]any{
				"npm":     "@ai-sdk/openai-compatible",
				"name":    shortName(b.p) + " (local)",
				"options": map[string]any{"baseURL": pl.Endpoint + "/v1"},
				"models": map[string]any{
					pl.LocalModelName: map[string]any{
						"name":      pl.ModelName + " " + pl.Quant + " (" + shortName(b.p) + ")",
						"tool_call": true,
						"limit":     map[string]int{"context": pl.ContextTokens, "output": outputTokens(pl.ContextTokens)},
					},
				},
			},
		},
	}
	content, _ := json.MarshalIndent(cfg, "", "  ")
	launch := "opencode --model " + b.q(id+"/"+pl.LocalModelName)
	if pl.ContextTokens < 64000 {
		b.warn(fmt.Sprintf("OpenCode needs a context window of 64k tokens or more; this plan uses %d.", pl.ContextTokens))
	}
	b.add(Step{
		ID: "harness-opencode", Kind: KindHarness,
		Title: "Connect OpenCode to " + pl.LocalModelName,
		Why:   fmt.Sprintf("Adds an OpenAI-compatible provider for %s at %s/v1 with the model's context limit, so OpenCode compacts before the window is full.", b.p.Name, pl.Endpoint),
		Files: []FileEdit{{
			Path: "~/.config/opencode/opencode.json", Format: "json", Content: string(content) + "\n", Merge: "merge",
			Note: "Merge into the \"provider\" object of the global config (or a project's opencode.json); back the file up to opencode.json.bak-cyrlm first.",
		}},
		Restart:    "OpenCode: start it with `" + launch + "`",
		Undo:       []Command{b.os(b.restoreBackup(".config/opencode/opencode.json"))},
		Confidence: Medium,
		Docs:       []string{docOpenCode},
	})
}

// manualHarnessStep covers harnesses configured in a UI or per launch
// (Cline, Zed, Aider, Qwen Code, generic clients).
func (b *builder) manualHarnessStep(h harness.Harness, s harness.Setup) {
	pl := b.plan
	var parts []string
	for i, st := range s.Steps {
		parts = append(parts, fmt.Sprintf("%d) %s", i+1, st))
	}
	if s.Config != "" {
		parts = append(parts, "Add to "+s.ConfigPath+": "+strings.ReplaceAll(strings.TrimSpace(s.Config), "\n", " "))
	}
	if len(s.Command) > 0 {
		var env []string
		for _, kv := range s.Env {
			env = append(env, kv[0]+"="+shQuote(kv[1]))
		}
		run := strings.TrimSpace(strings.Join(env, " ") + " " + joinSh(s.Command))
		if b.win && len(s.Env) > 0 {
			var set []string
			for _, kv := range s.Env {
				set = append(set, "$env:"+kv[0]+" = "+psQuote(kv[1]))
			}
			run = strings.Join(set, "; ") + "; " + joinPS(s.Command)
		}
		parts = append(parts, "Start it with: "+run)
	}
	parts = append(parts, s.Notes...)
	b.add(Step{
		ID: "harness-" + h.ID, Kind: KindHarness,
		Title:      "Connect " + h.Name + " to " + pl.LocalModelName,
		Why:        "Manual: " + strings.Join(parts, " "),
		Restart:    h.Name,
		Confidence: Medium,
	})
}

// restoreBackup restores a config file from the .bak-cyrlm backup the
// merge note asks for.
func (b *builder) restoreBackup(rel string) string {
	if b.win {
		return `Move-Item -Force ` + b.home(rel+".bak-cyrlm") + ` ` + b.home(rel)
	}
	return `mv ` + b.home(rel+".bak-cyrlm") + ` ` + b.home(rel)
}
