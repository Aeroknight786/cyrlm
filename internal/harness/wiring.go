package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aeroknight786/trialm/internal/paths"
)

// Detect selects the parent agent, then an installed CLI, then Claude Code.
// env and lookPath are injectable so detection does not depend on the test host.
// The note is nonempty only when detection falls back to Claude Code.
func Detect(env []string, lookPath func(string) (string, error)) (Harness, string) {
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if v != "" && (k == "CLAUDECODE" || k == "CLAUDE_CODE_ENTRYPOINT") {
			h, _ := Get("claude-code")
			return h, ""
		}
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		if v != "" && strings.HasPrefix(k, "CODEX_") {
			h, _ := Get("codex")
			return h, ""
		}
	}
	for _, id := range []string{"claude-code", "codex", "opencode", "qwen-code", "cline", "continue", "aider"} {
		h, _ := Get(id)
		if _, err := lookPath(h.Binary); err == nil {
			return h, ""
		}
	}
	h, _ := Get("claude-code")
	return h, "No coding CLI detected; using Claude Code (override with --harness)."
}

// DetectInstalled detects the current parent and installed binaries, including
// the known app-bundle and per-user install locations used by --launch.
func DetectInstalled() (Harness, string) {
	return Detect(os.Environ(), func(name string) (string, error) {
		return exec.LookPath(FindBinary(name))
	})
}

func (o Options) ownedPath(rel string) string {
	home := o.Home
	if home == "" {
		home, _ = paths.Home()
		// paths.Home can contain a relative TRIALM_HOME override. Env values
		// must work when pasted into a terminal in a different directory.
		home, _ = filepath.Abs(home)
	}
	// Symbolic home paths are plan paths, not paths on the current host.
	if strings.HasPrefix(home, "~/") {
		return path.Join(home, "harness", rel)
	}
	return filepath.Join(home, "harness", filepath.FromSlash(rel))
}

func compactJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func prettyJSON(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b) + "\n"
}

func claudeOutputTokens(ctx int) int {
	if ctx < 65536 {
		return 8192
	}
	return min(32000, ctx/4)
}

// OutputTokens is the shared per-response limit for OpenCode and setup files.
func OutputTokens(ctx int) int { return min(32768, max(4096, ctx/4)) }

// OpenCodeConfig builds both the inline configuration and permanent snippet.
func OpenCodeConfig(o Options) (map[string]any, string) {
	id, label, modelLabel := "trialm", "triaLM (simulated local model)", o.Model+" (simulated)"
	if o.Bridge {
		id, label, modelLabel = "trialm-local", "triaLM bridge (local runtime)", o.Model
	}
	model := map[string]any{"name": modelLabel, "tool_call": true}
	if o.ContextTokens > 0 {
		model["limit"] = map[string]int{"context": o.ContextTokens, "output": OutputTokens(o.ContextTokens)}
	}
	cfg := map[string]any{
		"$schema": "https://opencode.ai/config.json",
		"provider": map[string]any{id: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": label,
			"options": map[string]any{"baseURL": fmt.Sprintf("http://127.0.0.1:%d/v1", o.Port)},
			"models":  map[string]any{o.Model: model},
		}},
	}
	if o.Tuned {
		cfg["agent"] = map[string]any{"title": map[string]bool{"disable": true}}
	}
	return cfg, id
}

// WriteFiles creates only the dedicated files listed by SetupFor, never the
// user's harness settings. All owned files are private, including dummy keys.
func (s Setup) WriteFiles() error {
	keys := make([]string, 0, len(s.Files))
	for p := range s.Files {
		keys = append(keys, p)
	}
	sort.Strings(keys)
	for _, p := range keys {
		if !filepath.IsAbs(p) {
			return fmt.Errorf("harness config path must be absolute: %s", p)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return fmt.Errorf("create harness config directory: %w", err)
		}
		if err := writePrivateFile(p, s.Files[p]); err != nil {
			return fmt.Errorf("write harness config %s: %w", p, err)
		}
	}
	return nil
}

func writePrivateFile(path, content string) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}

func (s Setup) childEnv(parent []string) []string {
	// These describe the parent Claude Code process and must never mark a
	// freshly launched CLI as that same process (including custom commands).
	replaced := map[string]bool{"CLAUDECODE": true, "CLAUDE_CODE_ENTRYPOINT": true}
	for _, k := range s.Unset {
		replaced[k] = true
	}
	for _, kv := range s.Env {
		replaced[kv[0]] = true
	}
	var env []string
	for _, kv := range parent {
		k, _, _ := strings.Cut(kv, "=")
		if !replaced[k] {
			env = append(env, kv)
		}
	}
	for _, kv := range s.Env {
		env = append(env, kv[0]+"="+kv[1])
	}
	return env
}
