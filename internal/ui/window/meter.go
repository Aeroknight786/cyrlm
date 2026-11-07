package window

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/trial"
)

// Meter is four rendered lines with provenance and precision already applied.
type Meter struct{ Request, Cache, Wait, Spend string }

func (m Meter) String() string {
	if m.Request == "" {
		return "Request · waiting for harness\nCache · pending\nWait · local pacing\nSpend · tracked in the trial card"
	}
	return "Request · " + m.Request + "\nCache · " + m.Cache + "\nWait · " + m.Wait + "\nSpend · " + m.Spend
}
func runStore(s store.Store) store.Store { return store.Store{Dir: filepath.Join(s.Dir, "run")} }
func ReadMeter(s store.Store) Meter {
	var active struct{ ID string }
	rs := runStore(s)
	_ = rs.Read("active.json", &active)
	var m Meter
	if active.ID != "" {
		_ = rs.Read(active.ID+".meter.json", &m)
	}
	return m
}
func Track(ctx context.Context, s store.Store, id string, band int, row store.TrialEntry, report func() trial.Report) {
	if id == "" {
		return
	}
	save := func() {
		r := report()
		row.ID = id
		row.Band = band
		row.Report = r
		row.Incomplete = true
		_ = s.SaveTrial(row)
		reuse := 0
		if r.PromptTokens > 0 {
			reuse = 100 * r.CachedTokens / r.PromptTokens
		}
		m := Meter{Request: fmt.Sprintf("%d complete · %d cloud-bound", r.Requests, r.CloudBound), Cache: fmt.Sprintf("%d%% reused · %d loads", reuse, r.Reloads), Wait: figure.Band(r.LocalSeconds, band, "s local", figure.Simulated, "trial:local-schedule").String() + " · " + figure.Exact(r.CloudSeconds, "s cloud", figure.Measured, "trial:observed").String(), Spend: "unknown · endpoint prices unavailable"}
		if r.SpendKnown {
			m.Spend = figure.Exact(r.SpendUSD, "USD", figure.Measured, "provider:usage").String()
		}
		_ = runStore(s).Write(id+".meter.json", m)
	}
	go func() {
		save()
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				save()
				return
			case <-tick.C:
				save()
			}
		}
	}()
}
