// Package window opens triaLM-owned scripts, never commands from claims/pastes.
package window

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
)

type ChildExit struct{ Code int }

func (e *ChildExit) Error() string { return fmt.Sprintf("trial window exited %d", e.Code) }

type NeedsTerminal struct {
	Run   string
	Cause error
}

func (e *NeedsTerminal) Error() string { return "a trial needs a terminal; run " + e.Run }
func Automatic(getenv func(string) string, stdinTTY bool) bool {
	return getenv("CLAUDECODE") != "" || getenv("CODEX_SANDBOX") != "" || getenv("TRIALM_AGENT") != "" || !stdinTTY
}
func Quote(s string) string   { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
func psQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
func Command(exe string, args []string) string {
	q := []string{Quote(exe)}
	for _, a := range args {
		q = append(q, Quote(a))
	}
	return strings.Join(q, " ")
}

// Opener returns argv without invoking a shell. Linux TERMINAL must name an
// executable, not a shell expression. SSH and VS Code callers choose --here.
func Opener(goos, script, dir string, getenv func(string) string, look func(string) (string, error)) ([]string, error) {
	switch goos {
	case "darwin":
		return []string{"open", strings.TrimSuffix(script, ".sh") + ".command"}, nil
	case "windows":
		if _, err := look("wt"); err != nil {
			return nil, err
		}
		return []string{"wt", "-w", "0", "nt", "-d", dir, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", script}, nil
	default:
		if getenv("TMUX") != "" {
			return []string{"tmux", "new-window", "-c", dir, Command("sh", []string{script})}, nil
		}
		if term := getenv("TERMINAL"); term != "" {
			if _, err := look(term); err != nil {
				return nil, err
			}
			return []string{term, "-e", "sh", script}, nil
		}
		if p, err := look("x-terminal-emulator"); err == nil {
			return []string{p, "-e", "sh", script}, nil
		}
	}
	return nil, errors.New("no supported terminal opener")
}

// Generate writes a private executable script. Completion is recorded even
// after the harness exits with an error, so the parent can resume its card.
func Generate(s store.Store, goos, exe, dir string, args []string) (string, string, error) {
	return generate(s, goos, exe, dir, "try", args)
}
func generate(s store.Store, goos, exe, dir, verb string, args []string) (string, string, error) {
	raw := make([]byte, 12)
	if _, e := rand.Read(raw); e != nil {
		return "", "", e
	}
	id := hex.EncodeToString(raw)
	run := filepath.Join(s.Dir, "run")
	if e := os.MkdirAll(run, 0700); e != nil {
		return "", "", e
	}
	ext := ".sh"
	if goos == "windows" {
		ext = ".ps1"
	}
	script := filepath.Join(run, id+ext)
	exit := filepath.Join(run, id+".exit")
	started := filepath.Join(run, id+".started")
	full := append([]string{verb, "--plain", "--here"}, args...)
	var body string
	if goos == "windows" {
		quoted := []string{}
		for _, a := range full {
			quoted = append(quoted, psQuote(a))
		}
		body = "[IO.File]::WriteAllText(" + psQuote(started) + ", [string]$PID)\n$ErrorActionPreference = 'Stop'\nSet-Location -LiteralPath " + psQuote(dir) + "\n$env:TRIALM_RUN_ID = " + psQuote(id) + "\n$env:TRIALM_HOME = " + psQuote(s.Dir) + "\nRemove-Item Env:CLAUDECODE,Env:CLAUDE_CODE_ENTRYPOINT -ErrorAction SilentlyContinue\ntry { & " + psQuote(exe) + " " + strings.Join(quoted, " ") + "\n$code = $LASTEXITCODE } catch { $code = 1; Write-Error $_ -ErrorAction Continue }\n[IO.File]::WriteAllText(" + psQuote(exit) + ", [string]$code)\nexit $code\n"
	} else {
		body = "#!/bin/sh\nprintf '%s' \"$$\" > " + Quote(started) + "\ncd " + Quote(dir) + " || exit 1\nunset CLAUDECODE CLAUDE_CODE_ENTRYPOINT\nexport TRIALM_HOME=" + Quote(s.Dir) + "\nexport TRIALM_RUN_ID=" + Quote(id) + "\nstatus=0\nfinish() { printf '%s' \"$status\" > " + Quote(exit) + "; }\ntrap finish 0\ntrap 'status=130; exit 130' HUP INT TERM\n" + Command(exe, full) + "\nstatus=$?\nprintf '%s' \"$status\" > " + Quote(exit) + "\nexit \"$status\"\n"
	}
	if e := os.WriteFile(script, []byte(body), 0700); e != nil {
		return "", "", e
	}
	if goos == "darwin" {
		if e := os.WriteFile(filepath.Join(run, id+".command"), []byte(body), 0700); e != nil {
			return "", "", e
		}
	}
	return id, script, nil
}

type Launcher struct {
	Platform, Executable, Directory, Verb string
	Getenv                                func(string) string
	Lookup                                func(string) (string, error)
	Open                                  func([]string) error
	AckTimeout                            time.Duration
}

func Run(ctx context.Context, s store.Store, args []string, w io.Writer) error {
	return (Launcher{}).Run(ctx, s, args, w)
}
func (l Launcher) Run(ctx context.Context, s store.Store, args []string, w io.Writer) error {
	if l.Verb == "" {
		l.Verb = "try"
	}
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	dir, e := os.Getwd()
	if e != nil {
		return e
	}
	if l.Platform == "" {
		l.Platform = runtime.GOOS
	}
	if l.Executable != "" {
		exe = l.Executable
	}
	if l.Directory != "" {
		dir = l.Directory
	}
	if l.Getenv == nil {
		l.Getenv = os.Getenv
	}
	if l.Lookup == nil {
		l.Lookup = exec.LookPath
	}
	if l.AckTimeout == 0 {
		l.AckTimeout = 10 * time.Second
	}
	if l.Open == nil {
		l.Open = func(a []string) error {
			b, e := exec.CommandContext(ctx, a[0], a[1:]...).CombinedOutput()
			if e != nil {
				return fmt.Errorf("%w: %s", e, b)
			}
			return nil
		}
	}
	fallback := Command(exe, append([]string{l.Verb, "--here"}, args...))
	if l.Platform == "windows" {
		q := []string{psQuote(exe)}
		for _, a := range append([]string{l.Verb, "--here"}, args...) {
			q = append(q, psQuote(a))
		}
		fallback = "& " + strings.Join(q, " ")
	}
	id, script, e := generate(s, l.Platform, exe, dir, l.Verb, args)
	if e != nil {
		return e
	}
	argv, e := Opener(l.Platform, script, dir, l.Getenv, l.Lookup)
	if e != nil {
		return &NeedsTerminal{fallback, e}
	}
	if e = l.Open(argv); e != nil {
		return &NeedsTerminal{fallback, e}
	}
	_ = runStore(s).Write("active.json", map[string]string{"ID": id})
	if w != nil {
		fmt.Fprintln(w, (Meter{}).String())
	}
	launched := time.Now()
	acknowledged := false

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, e := os.ReadFile(filepath.Join(s.Dir, "run", id+".exit"))
		if e == nil {
			n, e := strconv.Atoi(strings.TrimSpace(string(b)))
			if e != nil {
				return e
			}
			if w != nil {
				fmt.Fprintln(w, ReadMeter(s).String())
			}
			if n != 0 {
				if hasCard(s, id) {
					return nil
				}
				return &ChildExit{n}
			}
			return nil
		}
		if !acknowledged {
			_, e = os.Stat(filepath.Join(s.Dir, "run", id+".started"))
			acknowledged = e == nil
			if !acknowledged && time.Since(launched) > l.AckTimeout {
				return &NeedsTerminal{fallback, errors.New("terminal did not start the trial script")}
			}
		}
		if acknowledged {
			b, _ := os.ReadFile(filepath.Join(s.Dir, "run", id+".started"))
			pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
			if pid > 0 && !processAlive(pid) {
				if hasCard(s, id) {
					return nil
				}
				return fmt.Errorf("trial window closed before a summary was recorded")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Args removes parent display/launch flags before starting a terminal child.
func Args(args []string) []string {
	out := []string{}
	for _, arg := range args {
		name, _, _ := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if strings.HasPrefix(arg, "--") && (name == "window" || name == "json" || name == "launch" || name == "plain" || name == "here") {
			continue
		}
		out = append(out, arg)
	}
	return out
}

func hasCard(s store.Store, id string) bool {
	rows, _ := s.History()
	for _, r := range rows {
		if r.ID == id {
			return true
		}
	}
	return false
}
