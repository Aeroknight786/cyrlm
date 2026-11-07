package setup

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/runtime"
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
	if h.NeedsBridge(b.p.APIs) {
		pl.HarnessCompatible = true
		pl.HarnessNote = fmt.Sprintf("%s connects through `trialm bridge`, which passes its requests to %s's Chat Completions API at %s as model %s.", h.Name, b.p.Name, pl.Endpoint, pl.LocalModelName)
		b.bridgeSteps(*h)
		return
	}
	if proto == "" {
		pl.HarnessCompatible = false
		pl.HarnessNote = incompatibleNote(*h, b.p, b.in.Quant.Format, b.in.OS, b.in.Arch)
		b.warn(pl.HarnessNote)
		return
	}
	pl.HarnessCompatible = true
	pl.HarnessNote = fmt.Sprintf("%s connects to %s's %s at %s as model %s.", h.Name, b.p.Name, protocolNames[proto], pl.Endpoint, pl.LocalModelName)

	s := h.SetupFor(harness.Options{Port: b.port, Model: pl.LocalModelName, Tuned: pl.Tuned, Protocol: proto, ContextTokens: pl.ContextTokens, Bridge: true, Home: "~/.trialm"})
	switch h.ID {
	case "claude-code":
		b.claudeCodeSteps(s)
	case "opencode":
		b.openCodeStep()
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
	note := fmt.Sprintf("%s needs the %s, which %s does not serve according to triaLM's runtime profile (it serves %s), so no harness step is included.", h.Name, proto, p.Name, apiList(p.APIs))
	if len(ok) > 0 {
		note += " Use " + strings.Join(ok, " or ") + " instead, or keep `trialm serve` running in front as a translator."
	} else {
		note += " Keep `trialm serve` running in front as a translator."
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

	rel := ".trialm/harness/claude-code-" + pl.Runtime + ".json"
	launch := "claude --settings " + b.home(rel)
	why := fmt.Sprintf("Writes a separate Claude Code settings file that points it at %s (ANTHROPIC_BASE_URL=%s) for every model alias, so only sessions started with --settings use the local model; your normal Claude Code setup is untouched. CLAUDE_CODE_MAX_CONTEXT_TOKENS=%d makes Claude Code compact before the local context is full.", b.p.Name, pl.Endpoint, pl.ContextTokens)
	why += " Unset ANTHROPIC_API_KEY, CLAUDECODE and CLAUDE_CODE_ENTRYPOINT in the shell you start it from. " + strings.Join(s.Notes, " ") + " Start it with: " + launch
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
			Note: "Merge the keys into the existing \"env\" object; back the file up to ~/.claude/settings.json.bak-trialm first.",
		}},
		Restart:    "Claude Code (new sessions)",
		Undo:       []Command{b.os(b.restoreBackup(".claude/settings.json"))},
		Confidence: High,
		Docs:       []string{docClaudeSettings},
	})
}

// bridgeSteps connect a harness through `trialm bridge`.
func (b *builder) bridgeSteps(h harness.Harness) {
	pl := b.plan
	if h.ID == "codex" && pl.ContextTokens < 64000 {
		b.warn(fmt.Sprintf("Codex recommends a context window of at least 64k tokens; this plan uses %d.", pl.ContextTokens))
	}
	why := fmt.Sprintf("`trialm bridge` accepts %s's requests on port 11535 (or the next free port) and passes them to %s/v1/chat/completions, the runtime's most mature API, with system messages merged for the model's chat template and tool calls mapped back; runtime errors are shown in %s instead of being retried. It returns once it answers and prints the exact %s command.", h.Name, pl.Endpoint, h.Name, h.Name)
	if h.ID == "codex" {
		why = fmt.Sprintf("Codex sends requests that local runtimes' own Responses endpoints do not fully handle (namespaced tools, several developer messages, hosted web search; llama.cpp skips namespace tools and strict chat templates reject a second system message). ") + why
	}
	b.add(Step{
		ID: "start-bridge", Kind: KindStart,
		Title:      "Start the " + h.Name + " bridge",
		Why:        why,
		Commands:   []Command{b.any(fmt.Sprintf("trialm bridge --endpoint %s --model %s --harness %s --ctx %d --background", pl.Endpoint, pl.LocalModelName, h.ID, pl.ContextTokens))},
		Undo:       []Command{b.os(b.pkill("trialm bridge"))},
		Confidence: High,
	})
	st := Step{
		ID: "harness-" + h.ID, Kind: KindHarness,
		Title:      "Start " + h.Name + " with " + pl.LocalModelName,
		Restart:    h.Name + ": start a new session in your own terminal",
		Confidence: High,
	}
	if h.ID == "codex" {
		st.Why = "Everything is passed with -c overrides, so no Codex config file is edited, plain `codex` keeps working as before, and nothing depends on Codex's config layout (profiles moved to separate files in 0.134). Use the command the bridge printed: it has the real port and, when codex is not on your terminal's PATH, its full path. Web search is off because a local model cannot run OpenAI's hosted search."
		st.Commands = []Command{b.any(strings.Join(harness.CodexCommand("http://127.0.0.1:11535/v1", pl.LocalModelName, pl.ContextTokens), " "))}
		st.Docs = []string{docCodexConfig}
	} else {
		s := h.SetupFor(harness.Options{Port: 11535, Model: pl.LocalModelName, Tuned: pl.Tuned, ContextTokens: pl.ContextTokens, Protocol: "openai-chat", Bridge: true, Home: "~/.trialm"})
		b.manualHarnessStep(h, s)
		return
	}
	b.add(st)
}

func (b *builder) openCodeStep() {
	pl := b.plan
	h, _ := harness.Get("opencode")
	s := h.SetupFor(harness.Options{Port: b.port, Model: pl.LocalModelName, ContextTokens: pl.ContextTokens, Tuned: pl.Tuned, Bridge: true})
	permanent := s.Config
	s.Config = ""
	if pl.ContextTokens < 64000 {
		b.warn(fmt.Sprintf("OpenCode needs a context window of 64k tokens or more; this plan uses %d.", pl.ContextTokens))
	}
	b.manualHarnessStep(h, s)
	b.add(Step{
		ID: "harness-opencode-permanent", Kind: KindHarness,
		Title: "Optional: make the local OpenCode provider permanent", Optional: true,
		Why:     "Merge the same provider and tuned settings into OpenCode's config. OPENCODE_DISABLE_MODELS_FETCH=1 avoids the models.dev startup fetch.",
		Files:   []FileEdit{{Path: "~/.config/opencode/opencode.json", Format: "json", Content: permanent, Merge: "merge", Note: "Back up opencode.json before merging."}},
		Restart: "OpenCode: start a new session", Undo: []Command{b.os(b.restoreBackup(".config/opencode/opencode.json"))},
		Confidence: Medium, Docs: []string{docOpenCode},
	})
}

// manualHarnessStep covers harnesses configured in a UI or per launch
// (Cline, Zed, Aider, Qwen Code, generic clients).
func (b *builder) manualHarnessStep(h harness.Harness, s harness.Setup) {
	pl := b.plan
	parts := append([]string(nil), s.Steps...)
	if s.Config != "" {
		parts = append(parts, "Optional: merge into "+s.ConfigPath+": "+strings.ReplaceAll(strings.TrimSpace(s.Config), "\n", " "))
	}
	st := Step{ID: "harness-" + h.ID, Kind: KindHarness,
		Title: "Connect " + h.Name + " to " + pl.LocalModelName, Confidence: Medium}
	if len(s.Command) > 0 {
		var env []string
		for _, name := range s.Unset {
			if b.win {
				env = append(env, "Remove-Item Env:"+name+" -ErrorAction SilentlyContinue;")
			}
		}
		for _, kv := range s.Env {
			if b.win {
				env = append(env, "$env:"+kv[0]+" = "+b.harnessArg(kv[1])+";")
			} else {
				env = append(env, kv[0]+"="+b.harnessArg(kv[1]))
			}
		}
		var args []string
		for _, arg := range s.Command {
			args = append(args, b.harnessArg(arg))
		}
		run := strings.Join(args, " ")
		if b.win && strings.HasPrefix(args[0], "'") {
			run = "& " + run
		}
		if len(env) > 0 {
			run = strings.Join(env, " ") + " " + run
		}
		if !b.win && len(s.Unset) > 0 {
			prefix := "env"
			for _, name := range s.Unset {
				prefix += " -u " + name
			}
			run = prefix + " " + run
		}
		st.Commands = []Command{b.os(run)}
		parts = append([]string{"Starts " + h.Name + " with the local endpoint and model. Any files below belong to triaLM."}, parts...)
		st.Restart = h.Name + ": start a new session with the command above"
	}
	paths := make([]string, 0, len(s.Files))
	for path := range s.Files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		format := strings.TrimPrefix(filepath.Ext(path), ".")
		if format == "yml" {
			format = "yaml"
		}
		st.Files = append(st.Files, FileEdit{Path: filepath.ToSlash(path), Format: format, Content: s.Files[path], Merge: "create", Note: "triaLM-owned config; create with mode 0600. Leaves the harness's default settings untouched."})
		st.Undo = append(st.Undo, b.os(b.rm(strings.TrimPrefix(filepath.ToSlash(path), "~/"))))
	}
	parts = append(parts, s.Notes...)
	st.Why = strings.Join(parts, " ")
	if h.Binary == "" {
		st.Why = "Manual: " + st.Why
	}
	if h.ID == "continue" {
		st.Restart = "cn: nothing to restart; editor extension: reload the window"
		st.Docs = []string{docContinue}
	}
	b.add(st)
}

// harnessArg expands owned plan paths in the target shell. Env vars cannot
// contain a literal tilde: the shell must turn HOME into an absolute path.
func (b *builder) harnessArg(arg string) string {
	if strings.HasPrefix(filepath.ToSlash(arg), "~/") {
		return b.home(strings.TrimPrefix(filepath.ToSlash(arg), "~/"))
	}
	return b.q(arg)
}

// restoreBackup restores a config file from the .bak-trialm backup the
// merge note asks for, or from the .bak-cyrlm backup an older plan asked for.
func (b *builder) restoreBackup(rel string) string {
	bak, old, dst := b.home(rel+".bak-trialm"), b.home(rel+".bak-cyrlm"), b.home(rel)
	if b.win {
		return `if (Test-Path ` + bak + `) { Move-Item -Force ` + bak + ` ` + dst + ` } else { Move-Item -Force ` + old + ` ` + dst + ` }`
	}
	return `if [ -f ` + bak + ` ]; then mv ` + bak + ` ` + dst + `; else mv ` + old + ` ` + dst + `; fi`
}
