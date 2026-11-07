package cli

import (
	"context"
	"flag"
	"strings"

	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/ui/tui"
)

func runTUI(ctx context.Context, verb string, args []string) error {
	fs := flag.NewFlagSet(verb, flag.ContinueOnError)
	var c common
	c.register(fs, "all")
	top := fs.Int("top", 5, "recipes to show")
	claims := fs.Bool("claims", false, "published claims gallery")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if err = c.validateRequirements(); err != nil {
		return err
	}
	p := c.prefs
	p.Tool = c.harness
	p.Context = c.ctx
	p.Hardware = c.device
	h, err := harness.Get(p.Tool)
	if err != nil {
		return err
	}
	if p.Context == 0 {
		p.Context = max(49152, ((h.Scenario.MinContext+16383)/16384)*16384)
	}

	if len(pos) > 0 {
		p.Model = strings.Join(pos, " ")
	}
	cfg := tui.SessionConfig(ctx, store.Default(), p, c.options(), *top, version)
	if verb == "find" {
		cfg.Start = tui.Recipes
	}
	if *claims {
		cfg.Start = tui.Gallery
	}
	cfg.Authorize = func(ctx context.Context, pasted string) error {
		return tui.Authorize(ctx, cfg.Store, openrouter.Client{}, "", openBrowser, pasted)
	}
	cfg.AutomaticWindow = autoWindow()
	cfg.Window = func(ctx context.Context, args []string) error { return runWindow(ctx, args, nil) }
	return tui.Run(cfg)
}

func runTrialTUI(ctx context.Context, c common, r predict.Result, args []string, window bool) error {
	p := c.prefs
	p.Tool = c.harness
	p.Context = r.Settings.ContextTokens
	cfg := tui.SessionConfig(ctx, store.Default(), p, c.options(), 5, version)
	cfg.Start = tui.Trial
	if window {
		cfg.WindowArgs = windowArgs(args)
		cfg.Window = func(ctx context.Context, args []string) error { return runWindow(ctx, args, nil) }
	} else {
		cfg.DirectArgs = windowArgs(args)
	}
	return tui.Run(cfg)
}
