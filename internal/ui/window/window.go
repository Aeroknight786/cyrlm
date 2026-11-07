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
	full := append([]string{"try", "--plain", "--here"}, args...)
	var body string
	if goos == "windows" {
		quoted := []string{}
		for _, a := range full {
			quoted = append(quoted, psQuote(a))
		}
		body = "$ErrorActionPreference = 'Stop'\nSet-Location -LiteralPath " + psQuote(dir) + "\n$env:TRIALM_RUN_ID = " + psQuote(id) + "\n$env:TRIALM_HOME = " + psQuote(s.Dir) + "\nRemove-Item Env:CLAUDECODE,Env:CLAUDE_CODE_ENTRYPOINT -ErrorAction SilentlyContinue\ntry { & " + psQuote(exe) + " " + strings.Join(quoted, " ") + "\n$code = $LASTEXITCODE } catch { $code = 1; Write-Error $_ -ErrorAction Continue }\n[IO.File]::WriteAllText(" + psQuote(exit) + ", [string]$code)\nexit $code\n"
	} else {
		body = "#!/bin/sh\ncd " + Quote(dir) + " || exit 1\nunset CLAUDECODE CLAUDE_CODE_ENTRYPOINT\nexport TRIALM_HOME=" + Quote(s.Dir) + "\nexport TRIALM_RUN_ID=" + Quote(id) + "\n" + Command(exe, full) + "\nstatus=$?\nprintf '%s' \"$status\" > " + Quote(exit) + "\nexit \"$status\"\n"
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
func Run(ctx context.Context, s store.Store, args []string, w io.Writer) error {
	exe, e := os.Executable()
	if e != nil {
		return e
	}
	dir, e := os.Getwd()
	if e != nil {
		return e
	}
	fallback := Command(exe, append([]string{"try", "--here"}, args...))
	id, script, e := Generate(s, runtime.GOOS, exe, dir, args)
	if e != nil {
		return e
	}
	argv, e := Opener(runtime.GOOS, script, dir, os.Getenv, exec.LookPath)
	if e != nil {
		return &NeedsTerminal{fallback, e}
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if b, e := cmd.CombinedOutput(); e != nil {
		return &NeedsTerminal{fallback, fmt.Errorf("%w: %s", e, b)}
	}
	if w != nil {
		fmt.Fprintln(w, "Request · waiting in the new terminal\nCache · waiting for the first request\nWait · local pacing\nSpend · tracked in the trial card")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		b, e := os.ReadFile(filepath.Join(s.Dir, "run", id+".exit"))
		if e == nil {
			n, e := strconv.Atoi(strings.TrimSpace(string(b)))
			if e != nil {
				return e
			}
			if n != 0 {
				return fmt.Errorf("trial window exited %d", n)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
