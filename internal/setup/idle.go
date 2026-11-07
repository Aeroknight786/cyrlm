package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/aeroknight786/trialm/internal/setup/proc"
)

// LaunchLease serializes launches and idle timers with setup/undo. An empty
// token acquires a lease. Releasing it arms a new idle generation.
func (e *Executor) LaunchLease(slug, token string) (string, error) {
	path, err := e.journalPath(slug)
	if err != nil {
		return "", err
	}
	unlock, err := lock(path)
	if err != nil {
		return "", err
	}
	defer unlock()
	dir := filepath.Join(filepath.Dir(path), "leases")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	idle := filepath.Join(filepath.Dir(path), "idle")
	if token == "" {
		_ = os.Remove(idle)
		f, err := os.CreateTemp(dir, "launch-")
		if err != nil {
			return "", err
		}
		err = f.Close()
		return filepath.Base(f.Name()), err
	}
	if token != filepath.Base(token) {
		return "", errors.New("invalid launch lease")
	}
	if err = os.Remove(filepath.Join(dir, token)); err != nil {
		return "", err
	}
	return token, atomicWrite(idle, []byte(token), 0600)
}

// IdleStop waits outside the journal lock, then only stops the generation
// that armed this timer. A later launch or another active lease supersedes it.
func (e *Executor) IdleStop(ctx context.Context, slug, token string, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	path, err := e.journalPath(slug)
	if err != nil {
		return err
	}
	unlock, err := lock(path)
	if err != nil {
		return err
	}
	defer unlock()
	b, err := os.ReadFile(filepath.Join(filepath.Dir(path), "idle"))
	if errors.Is(err, os.ErrNotExist) || string(b) != token {
		return nil
	}
	if err != nil {
		return err
	}
	leases, err := os.ReadDir(filepath.Join(filepath.Dir(path), "leases"))
	if err != nil {
		return err
	}
	if len(leases) > 0 {
		return nil
	}
	j, err := e.Status(slug)
	if err != nil {
		return err
	}
	for _, r := range j.Steps {
		if r.Inverse.PIDFile != "" {
			if err = proc.Stop(r.Inverse.PIDFile); err != nil {
				return err
			}
		}
	}
	return nil
}
