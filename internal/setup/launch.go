package setup

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/setup/proc"
)

// LaunchEngine reuses the journal's exact start steps and port substitutions.
// The bridge is owned by the launch caller, never a detached start step.
func (e *Executor) LaunchEngine(ctx context.Context, slug string, stop bool) (Journal, error) {
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
	if err != nil {
		return j, err
	}
	if j.State != "ready" || j.Report == nil || !j.Report.Ready {
		return j, &NotReady{Check: "run trialm setup"}
	}
	if err = e.validate(j.Plan); err != nil {
		return j, err
	}
	if stop {
		if err = proc.Stop(filepath.Join(e.Root, "run", slug+"-idle.pid")); err != nil {
			return j, err
		}
		return j, e.stopEngine(ctx, j)
	}
	for i, s := range j.Plan.Steps {
		if s.Kind == Admin && s.RebootReset && !stop {
			current, readErr := exec.CommandContext(ctx, "sysctl", "-n", "iogpu.wired_limit_mb").Output()
			want := strings.TrimPrefix(s.Argv[2], "iogpu.wired_limit_mb=")
			if readErr != nil {
				return j, fmt.Errorf("read wired limit: %w", readErr)
			}
			n, parseErr := strconv.Atoi(strings.TrimSpace(string(current)))
			target, targetErr := strconv.Atoi(want)
			if parseErr != nil || targetErr != nil {
				return j, errors.New("invalid wired limit")
			}
			if n < target {
				e.emit("setup_log", Update{Lines: []string{"Re-apply wired limit · 1 OS password prompt after restart"}})
				if e.Admin == nil {
					return j, &PasswordRequired{}
				}
				if err = e.Admin(ctx, s.Argv); err != nil {
					return j, err
				}
			}
		}
		if s.Kind != Start {
			continue
		}
		pid := j.Steps[i].Inverse.PIDFile
		if s.ID == "bridge" {
			if pid != "" {
				if err = proc.Stop(pid); err != nil {
					return j, err
				}
			}
		} else {
			err = e.run(ctx, s, &j, i, path, Update{Index: i + 1, Total: len(j.Steps), Kind: Start, Title: s.Title})
		}
		if err != nil {
			return j, err
		}
	}
	return j, nil
}

// Effective exposes the persisted address substitutions without changing the plan.
func (j Journal) Effective(value string) string { return j.effective(value) }

func (e *Executor) stopEngine(ctx context.Context, j Journal) error {
	for i, s := range j.Plan.Steps {
		if s.Kind != Start {
			continue
		}
		if pid := j.Steps[i].Inverse.PIDFile; pid != "" {
			if err := proc.Stop(pid); err != nil {
				return err
			}
		}
		if len(s.Inverse) == 0 {
			continue
		}
		argv, env := j.effectiveArgs(s.Inverse), j.effectiveArgs(s.Env)
		for i := range argv {
			argv[i] = e.expand(argv[i])
		}
		for i := range env {
			env[i] = e.expand(env[i])
		}
		if err := e.command(ctx, argv, env, filepath.Join(e.Root, "logs", j.Plan.Slug, s.ID+"-stop.log"), Update{}); err != nil {
			return err
		}
	}
	return nil
}

// PasswordRequired keeps non-TTY agents from attempting an OS prompt.
type PasswordRequired struct{}

func (*PasswordRequired) Error() string   { return "1 OS password prompt needs an interactive terminal" }
func (*PasswordRequired) NeedsUser() bool { return true }
func (*ApprovalRequired) NeedsUser() bool { return true }
