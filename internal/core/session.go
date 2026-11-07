// Package core is the shared application seam for command and UI clients.
package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
)

type Next struct {
	Cmd string `json:"cmd"`
	Why string `json:"why"`
}
type Event struct {
	Type string
	Data any
	Next []Next
}
type Sink func(Event)
type Session struct{ Sink Sink }

func (s Session) Emit(kind string, data any, next ...Next) {
	if s.Sink != nil {
		s.Sink(Event{Type: kind, Data: data, Next: next})
	}
}

type Options struct {
	Runtime, Quant, KV, Backend, Device string
	Context                             int
	RAM, VRAM                           float64
	Probe, Tuned                        bool
}
type Detection struct {
	System   *hw.System            `json:"system"`
	Target   predict.Target        `json:"target"`
	Runtimes []hw.InstalledRuntime `json:"runtimes"`
}

func (s Session) Detect(ctx context.Context, o Options) (Detection, error) {
	var d Detection
	var err error
	if o.Device != "" {
		d.System, err = predict.SyntheticSystem(o.Device, o.RAM, o.VRAM)
	} else {
		d.System, err = hw.Detect(ctx)
	}
	if err != nil && d.System == nil {
		return d, err
	}
	bw := 0.0
	if o.Probe {
		bw, _ = hw.MeasureMemoryBandwidth(ctx, 1500*time.Millisecond)
	}
	d.Target = predict.TargetFromSystem(d.System, bw)
	if o.Device != "" {
		d.Target.Synthetic = true
		d.Target.Name = "what-if: " + d.Target.Name
	} else {
		d.Runtimes = hw.DetectRuntimes(ctx)
	}
	s.Emit("detect", d)
	return d, nil
}

type Listing struct {
	Rows      []Row
	Installed Installed
	Target    predict.Target
	Harness   harness.Harness
	Scenario  predict.Scenario
}

func (s Session) Find(ctx context.Context, o Options, t predict.Target, sc predict.Scenario, hh harness.Harness, query string, top int) (Listing, error) {
	out := Listing{Installed: DetectInstalled(t), Target: t, Harness: hh, Scenario: sc}
	for _, m := range catalog.All() {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		if !m.Tools && hh.ID != "aider" {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(m.OllamaTag+" "+m.Name+" "+m.ID), strings.ToLower(query)) {
			continue
		}
		row, ok, err := BestFileFor(o, m, t, sc, out.Installed, hh)
		if err != nil {
			return out, err
		}
		if ok {
			out.Rows = append(out.Rows, row)
		}
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		if out.Rows[i].Rank != out.Rows[j].Rank {
			return out.Rows[i].Rank < out.Rows[j].Rank
		}
		return out.Rows[i].Result.Model.TotalParamsB > out.Rows[j].Result.Model.TotalParamsB
	})
	if top > 0 && len(out.Rows) > top {
		out.Rows = out.Rows[:top]
	}
	s.Emit("recipes", out)
	return out, nil
}

type Trial struct {
	Result predict.Result
	Why    string
}

func (s Session) Try(ctx context.Context, o Options, m catalog.Model, t predict.Target, sc predict.Scenario, h harness.Harness, force bool) (Trial, error) {
	if err := ctx.Err(); err != nil {
		return Trial{}, err
	}
	p, why, err := ChooseRuntime(o, t, m, sc, h)
	if err != nil {
		return Trial{}, err
	}
	r := predict.Predict(predict.Input{Model: m, QuantName: o.Quant, Runtime: p, Target: t, Tuned: o.Tuned, CtxOverride: o.Context, KVCacheType: o.KV, Scenario: sc, BackendOverride: o.Backend})
	if r.Unsupported != "" {
		return Trial{}, fmt.Errorf("%s", r.Unsupported)
	}
	if r.Memory.Level == predict.FitNo && !force {
		return Trial{}, fmt.Errorf("%s does not fit on %s with %s: needs %.1f GB, %.1f GB available%s\n(use --force to simulate anyway, a smaller --quant, or a smaller --ctx)", m.Name, t.Name, p.Name, r.Memory.TotalBytes/(1<<30), r.Memory.BudgetBytes/(1<<30), suffix("; ", r.Memory.Fix))
	}
	return Trial{Result: r, Why: why}, nil
}
func suffix(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}
