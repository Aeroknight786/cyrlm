package setup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aeroknight786/trialm/internal/setup/proc"
)

func (e *Executor) Undo(ctx context.Context, slug string, o UndoOptions) (Journal, error) {
	path, err := e.journalPath(slug)
	if err != nil {
		return Journal{}, err
	}
	unlock, err := lock(path)
	if err != nil {
		return Journal{}, err
	}
	defer unlock()
	j, err := e.Status(slug)
	if errors.Is(err, os.ErrNotExist) {
		return e.undoLegacy(ctx, slug, o)
	}
	if err != nil {
		return j, err
	}
	for i := len(j.Steps) - 1; i >= 0; i-- {
		r := &j.Steps[i]
		if r.State == "" || r.State == "undone" {
			continue
		}
		inv := r.Inverse
		if (inv.Category == Fetch || inv.Model) && !o.Models || (inv.Category == Install || inv.Category == RunInstaller) && !inv.Model && !o.Engines {
			continue
		}
		if err = ctx.Err(); err != nil {
			return j, err
		}
		if inv.NativeRepo != "" {
			if err = e.undoNativeModels(j, inv); err != nil {
				return j, err
			}
		}
		if inv.Stopped != nil {
			stopped := inv.Stopped
			if _, err = proc.Start(ctx, proc.Options{Argv: stopped.Argv, Env: stopped.Env, PIDFile: inv.PIDFile, Log: stopped.Log, Health: stopped.Health}); err != nil {
				return j, err
			}
			r.State = "undone"
			if err = save(path, &j); err != nil {
				return j, err
			}
			continue
		}
		if inv.PIDFile != "" {
			if err = proc.Stop(inv.PIDFile); err != nil {
				return j, err
			}
			e.prune(inv.PIDFile)
		}
		if inv.Path != "" {
			if err = e.writable(inv.Path); err != nil {
				return j, err
			}
			if inv.Existed {
				if inv.Category == Write {
					err = atomicWrite(inv.Path, inv.Data, inv.Mode)
				}
			} else {
				err = os.RemoveAll(inv.Path)
			}
			if err != nil {
				return j, err
			}
			for _, suffix := range []string{".part", ".archive", ".archive.part", ".install"} {
				if err = os.RemoveAll(inv.Path + suffix); err != nil {
					return j, err
				}
			}
			e.prune(inv.Path)
		}
		if inv.Category == Admin && len(inv.Argv) > 0 {
			if e.Admin == nil {
				return j, fmt.Errorf("OS password prompt required for %q", inv.Argv)
			}
			j.Prompts.Passwords++
			if err = save(path, &j); err != nil {
				return j, err
			}
			e.emit("setup_log", Update{Kind: Admin, Lines: []string{fmt.Sprintf("argv %q", inv.Argv)}})
			if err = e.Admin(ctx, inv.Argv); err != nil {
				return j, err
			}
		} else if len(inv.Argv) > 0 {
			logPath := filepath.Join(e.Root, "logs", slug, "undo.log")
			if err = e.command(ctx, e.expandArgs(j.effectiveArgs(inv.Argv)), e.expandArgs(j.effectiveArgs(inv.Env)), logPath, Update{}); err != nil {
				return j, err
			}
		}
		r.State = "undone"
		if err = save(path, &j); err != nil {
			return j, err
		}
	}
	logDir := filepath.Join(e.Root, "logs", slug)
	if err = os.RemoveAll(logDir); err != nil {
		return j, err
	}
	e.prune(logDir)
	j.State = "undone"
	if err = save(path, &j); err != nil {
		return j, err
	}
	e.emit("setup_undone", j)
	return j, nil
}
func (e *Executor) prune(path string) {
	for parent := filepath.Dir(path); inside(e.Root, parent) && parent != e.Root; parent = filepath.Dir(parent) {
		if os.Remove(parent) != nil {
			break
		}
	}
}
func (e *Executor) undoLegacy(ctx context.Context, slug string, o UndoOptions) (Journal, error) {
	// Legacy plans had scripts and .bak-cyrlm files but no journal.
	if !strings.HasSuffix(slug, "-cyrlm") && !strings.HasSuffix(slug, "-trialm") {
		return Journal{}, errors.New("no setup journal or recognized legacy setup")
	}
	j := Journal{State: "undone", Plan: Plan{Slug: slug}}
	for _, rel := range []string{".claude/settings.json", ".config/opencode/opencode.json"} {
		dest := filepath.Join(e.Home, filepath.FromSlash(rel))
		if err := e.writable(dest); err != nil {
			return j, err
		}
		for _, suffix := range []string{".bak-trialm", ".bak-cyrlm"} {
			backup := dest + suffix
			if _, err := os.Stat(backup); err == nil {
				if err = os.Rename(backup, dest); err != nil {
					return j, err
				}
				break
			}
		}
	}
	for _, dir := range []string{"llamacpp", "mlx-lm", "ollama"} {
		for _, ext := range []string{".sh", ".ps1", ".Modelfile", ".log"} {
			file := filepath.Join(e.Root, dir, slug+ext)
			if err := e.writable(file); err != nil {
				return j, err
			}
			if err := os.Remove(file); err != nil && !errors.Is(err, os.ErrNotExist) {
				return j, err
			}
			e.prune(file)
		}
	}
	if o.Models {
		if err := e.command(ctx, []string{"ollama", "rm", slug}, nil, filepath.Join(e.Root, "logs", slug, "undo.log"), Update{}); err != nil {
			return j, err
		}
	}
	path, _ := e.journalPath(slug)
	if err := save(path, &j); err != nil {
		return j, err
	}
	e.emit("setup_undone", j)
	return j, nil
}

// Latest returns the most recently updated journal, excluding completed undo.
func Latest(root string) (string, error) {
	entries, err := os.ReadDir(filepath.Join(root, "setups"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var latest string
	var modified int64
	for _, entry := range entries {
		info, err := os.Stat(filepath.Join(root, "setups", entry.Name(), "journal.json"))
		if err == nil && info.ModTime().UnixNano() > modified {
			latest = entry.Name()
			modified = info.ModTime().UnixNano()
		}
	}
	return latest, nil
}
