package tui

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/selfupdate"
	"github.com/aeroknight786/trialm/internal/store"
)

// SessionConfig adapts the stdlib session once for both home and find.
func SessionConfig(ctx context.Context, s store.Store, p store.Preferences, o core.Options, top int, version string) Config {
	cfg := Config{Context: ctx, Store: s, Preferences: p}
	cfg.Headless = os.Getenv("SSH_CONNECTION") != "" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "")
	cfg.Authorize = func(ctx context.Context, key string) error {
		return Authorize(ctx, s, openrouter.Client{}, "", OpenBrowser, key)
	}
	cfg.Detect = func(ctx context.Context) (core.Detection, error) { return (core.Session{}).Detect(ctx, o) }
	cfg.Find = func(ctx context.Context, p store.Preferences) (core.Listing, error) {
		opts := o
		opts.Context = p.Context
		opts.Use = p.Use
		opts.Priority = p.Priority
		opts.Engines = p.Engines
		opts.Boosts = p.Boosts
		opts.QualityFloor = p.QualityFloor
		opts.MaxDownloadGB = p.MaxDownloadGB
		opts.Tuned = p.Boosts != "off"
		hh, e := harness.Get(p.Tool)
		if e != nil {
			return core.Listing{}, e
		}
		d, e := (core.Session{}).Detect(ctx, opts)
		if e != nil {
			return core.Listing{}, e
		}
		query := p.Model
		if query == "any" {
			query = ""
		}
		l, e := (core.Session{}).Find(ctx, opts, d.Target, hh.Scenario, hh, query, top)
		if e == nil {
			rows := []store.Recipe{}
			for _, r := range l.Rows {
				rows = append(rows, store.Recipe{Model: r.Result.Model.OllamaTag, Quant: r.Result.Quant.Name, Runtime: r.Result.Runtime.ID, Harness: hh.ID})
			}
			e = s.Write("last.json", rows)
		}
		return l, e
	}
	cfg.TrialArgs = func(row core.Row, p store.Preferences) []string {
		r := row.Result
		a := []string{r.Model.OllamaTag, "--runtime", r.Runtime.ID, "--quant", r.Quant.Name, "--for", p.Tool, "--ctx", strconv.Itoa(p.Context), "--use", p.Use, "--budget", strconv.FormatFloat(p.BudgetUSD, 'f', -1, 64), "--privacy", p.Privacy, "--boosts", p.Boosts}
		if o.Device != "" {
			a = append(a, "--device", o.Device, "--ram", fmt.Sprint(o.RAM), "--vram", fmt.Sprint(o.VRAM))
		}
		return a
	}
	cfg.UpdateNotice = func(ctx context.Context) string {
		n := selfupdate.Cached(s)
		if os.Getenv("TRIALM_OFFLINE") != "1" && selfupdate.Claim(s, time.Now()) {
			check, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			_ = (selfupdate.Client{}).Refresh(check, s)
			n = selfupdate.Cached(s)
		}
		if selfupdate.Newer(n.Version, version) {
			return n.Version + " available · trialm update"
		}
		return ""
	}
	return cfg
}
