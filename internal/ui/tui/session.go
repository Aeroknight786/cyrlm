package tui

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/store"
)

// SessionConfig adapts the stdlib session once for both home and find.
func SessionConfig(ctx context.Context, s store.Store, p store.Preferences, o core.Options, top int, version string) Config {
	cfg := Config{Context: ctx, Store: s, Preferences: p}
	for _, h := range harness.All() {
		if h.Binary != "" {
			if _, e := exec.LookPath(harness.FindBinary(h.Binary)); e == nil {
				cfg.Tools = append(cfg.Tools, h.ID)
			}
		}
	}
	cfg.Headless = os.Getenv("SSH_CONNECTION") != "" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "")
	cfg.Authorize = func(ctx context.Context, key string) error {
		return Authorize(ctx, s, openrouter.Client{}, "", OpenBrowser, key)
	}
	cfg.Detect = func(ctx context.Context) (core.Detection, error) { return (core.Session{}).Detect(ctx, o) }
	cfg.Find = func(ctx context.Context, p store.Preferences) (core.Listing, error) {
		return (core.Session{}).FindPreferences(ctx, s, p, o, top)
	}
	cfg.TrialArgs = func(row core.Row, p store.Preferences) []string {
		return core.TrialArgs(row, p, o)
	}
	cfg.UpdateNotice = func(ctx context.Context) string {
		return core.UpdateNotice(ctx, s, version)
	}
	cfg.Models = func(ctx context.Context, d core.Detection, p store.Preferences) ([]core.ModelChoice, error) {
		opts := o
		opts.Context, opts.Use, opts.Priority, opts.QualityFloor = p.Context, p.Use, p.Priority, p.QualityFloor
		h, e := harness.Get(p.Tool)
		if e != nil {
			return nil, e
		}
		return (core.Session{}).Models(ctx, opts, d.Target, h)
	}
	cfg.Plan = func(ctx context.Context, row core.Row, p store.Preferences) (setup.Plan, error) {
		opts := o
		opts.Context = p.Context
		return (core.Session{}).PlanRecipe(ctx, row.Result, opts, p.Tool, row.LocalPath)
	}

	cfg.Apply = func(ctx context.Context, p setup.Plan, sink core.Sink) (setup.Journal, error) {
		session := core.Session{Sink: sink, Admin: func(ctx context.Context, argv []string) error {
			done := make(chan error, 1)
			sink(core.Event{Type: "setup_admin", Data: adminRequest{ctx, argv, done}})
			select {
			case err := <-done:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}}
		return session.Apply(ctx, p, setup.Policy{PlanID: p.ID})
	}

	return cfg
}
