package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/aeroknight786/trialm/internal/engine"
)

// MaterialChange is the boundary for an additional counted approval.
func MaterialChange(before, after Plan) bool {
	if after.DownloadBytes > before.DownloadBytes+before.DownloadBytes/10 {
		return true
	}
	for _, s := range after.Steps {
		if s.Kind != Install && s.Kind != Admin && s.Kind != RunInstaller {
			continue
		}
		found := slices.ContainsFunc(before.Steps, func(old Step) bool {
			a, _ := json.Marshal(old)
			b, _ := json.Marshal(s)
			return string(a) == string(b)
		})
		if !found {
			return true
		}
	}
	return false
}
func inside(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (e *Executor) writable(path string) error {
	path = e.expand(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("setup path must be absolute: %s", path)
	}
	base := e.Root
	if !inside(base, path) {
		base = e.Home
	}
	for ancestor := path; ancestor != base && inside(base, ancestor); ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("setup refuses symlink path %s", ancestor)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	if inside(e.Root, path) {
		return nil
	}
	// These are precisely the optional persistent files the old wiring owned.
	for _, rel := range []string{".claude/settings.json", ".config/opencode/opencode.json"} {
		if path == filepath.Join(e.Home, filepath.FromSlash(rel)) {
			return nil
		}
	}
	return fmt.Errorf("setup write outside owned paths: %s", path)
}
func (e *Executor) validate(p Plan) error {
	seen := map[string]bool{}
	if len(p.Steps) == 0 || p.Steps[len(p.Steps)-1].Kind != Check {
		return errors.New("setup must end with Ready check")
	}
	for _, s := range p.Steps {
		if s.ID == "" || slug(s.ID) != s.ID || seen[s.ID] {
			return errors.New("invalid or duplicate step id")
		}
		seen[s.ID] = true
		switch s.Kind {
		case Install, Fetch, RunInstaller, Write, Start, Stop, EngineDelegate, Admin, Check:
		default:
			return fmt.Errorf("unknown step kind %q", s.Kind)
		}
		if s.Kind == Write || s.Kind == Stop || s.Path != "" {
			if err := e.writable(s.Path); err != nil {
				return err
			}
		}
		if s.Request != nil {
			r := s.Request
			digest, err := hex.DecodeString(r.SHA256)
			if err != nil || len(digest) != sha256.Size {
				return errors.New("download requires a sha256 pin")
			}
			u, err := url.Parse(r.URL)
			if err != nil || u.Scheme != "https" || u.User != nil {
				return errors.New("download requires an HTTPS URL")
			}
			if s.Kind == Fetch && u.Host != "huggingface.co" {
				return errors.New("fetch requires a Hugging Face file")
			}
			if err = e.writable(r.Dest); err != nil {
				return err
			}
		}
		if s.Kind == Check && len(s.Argv) > 0 && !slices.Equal(s.Argv, []string{"zstd", "--version"}) {
			return errors.New("check argv is not allowlisted")
		}
		if s.Kind == Check && s.ID == "resolve" {
			return errors.New("resolve file digests on this machine before applying a what-if plan")
		}
		if s.Kind == Fetch && s.Request == nil {
			return errors.New("fetch requires a file")
		}
		if s.Kind == RunInstaller && (s.Request == nil || s.Content == "") {
			return errors.New("installer requires a digest and displayed source")
		}
		if s.Kind == RunInstaller {
			digest := fmt.Sprintf("%x", sha256.Sum256([]byte(s.Content)))
			if digest != s.Request.SHA256 || !slices.Equal(s.Argv, installerArgs(s.Request.Dest, p.OS)) {
				return errors.New("installer source or argv differs from its pin")
			}
		}
		if s.Kind == Install && s.Request == nil {
			allowed := map[string][]string{"mlx-lm": {"uv", "tool", "install", "mlx-lm"}}
			expected := allowed[s.Package]
			if s.Package == "ollama-model" {
				expected = []string{"ollama", "create", p.LocalModelName, "-f", "~/.trialm/ollama/" + p.LocalModelName + ".Modelfile"}
			}
			if s.Package == "lms-model" {
				expected, _ = engine.Args("lmstudio-load", map[string]string{"model": s.ArgvModel(), "name": p.LocalModelName, "ctx": fmt.Sprint(p.ContextTokens)})
			}
			if s.Package == "lms-import" {
				expected, _ = engine.Args("lmstudio-import", map[string]string{"model": s.ArgvModel(), "namespace": s.NativeRepo})
			}
			actual := append([]string(nil), s.Argv...)
			if len(actual) > 0 && filepath.IsAbs(e.expand(actual[0])) {
				base := filepath.Base(actual[0])
				if base == "ollama" || base == "ollama.exe" {
					actual[0] = "ollama"
				}
				if base == "lms" || base == "lms.exe" {
					actual[0] = "lms"
				}
			}
			if expected == nil || !slices.Equal(expected, actual) {
				return errors.New("install is not an allowlisted package template")
			}
		}
		if s.NativeRepo != "" && s.NativeRepo != "trialm/"+p.Slug {
			return errors.New("invalid native model namespace")
		}
		if s.Kind == Admin {
			if len(s.Argv) != 3 || s.Argv[0] != "sudo" || s.Argv[1] != "sysctl" || !strings.HasPrefix(s.Argv[2], "iogpu.wired_limit_mb=") {
				return errors.New("admin command is not allowlisted")
			}
		}
		if s.Kind == Start {
			if len(s.Argv) == 0 {
				return errors.New("start requires argv")
			}
			executable := e.expand(s.Argv[0])
			allowed := slices.Contains([]string{"llama-server", "llama-server.exe", "ollama", "lms", "mlx_lm.server", "trialm", "/opt/homebrew/bin/llama-server", "/usr/local/bin/llama-server", "/home/linuxbrew/.linuxbrew/bin/llama-server"}, executable)
			if filepath.IsAbs(executable) && (inside(filepath.Join(e.Root, "engines"), executable) || filepath.Base(executable) == "ollama" || filepath.Base(executable) == "mlx_lm.server" || filepath.Base(executable) == "llama-server" || filepath.Base(executable) == "llama-server.exe") {
				allowed = true
			}
			if !allowed {
				return fmt.Errorf("start executable %q is not allowlisted", executable)
			}
		}
	}
	return nil
}
func (s Step) ArgvModel() string {
	if len(s.Argv) > 2 {
		return s.Argv[2]
	}
	return ""
}

// reconcile carries original inverses forward when a replacement is approved.
func reconcile(j *Journal, p Plan) error {
	byID := map[string]int{}
	for i, s := range p.Steps {
		byID[s.ID] = i
	}
	for i, s := range j.Plan.Steps {
		if _, ok := byID[s.ID]; !ok && j.Steps[i].State != "" && j.Steps[i].State != "undone" {
			return fmt.Errorf("replacement removes completed step %s; undo the saved setup first", s.ID)
		}
	}
	records := make([]Record, len(p.Steps))
	for i, old := range j.Plan.Steps {
		n, ok := byID[old.ID]
		if !ok {
			continue
		}
		if j.Steps[i].State == "" || j.Steps[i].State == "undone" {
			continue
		}
		next := p.Steps[n]
		if old.Request != nil && next.Request != nil && old.Request.Dest != next.Request.Dest {
			return errors.New("replacement changes a model path; undo the saved setup with --delete-models first")
		}
		if old.Kind != next.Kind || old.Path != next.Path {
			return errors.New("replacement changes a step's ownership; undo the saved setup first")
		}
		records[n] = j.Steps[i]
		a, _ := json.Marshal(old)
		b, _ := json.Marshal(next)
		if string(a) != string(b) && records[n].State == "done" {
			records[n].State = "pending"
		}
	}
	j.Steps = records
	if j.State != "undone" {
		j.Prompts.Approvals++
	}
	j.Prompts.Passwords = p.Prompts.Passwords
	j.Prompts.AfterRestart = p.Prompts.AfterRestart
	j.Report = nil
	return nil
}

// ApprovalRequired carries a concrete replacement plan for a material change.
type ApprovalRequired struct {
	Plan   Plan
	Reason string
}

func (a *ApprovalRequired) Error() string {
	return a.Reason + "; review the replacement plan and use --yes=" + a.Plan.ID
}

func installerArgs(path, goos string) []string {
	if goos == "windows" {
		return []string{"powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", path}
	}
	return []string{"sh", path, "--no-modify-path", "--quiet"}
}
