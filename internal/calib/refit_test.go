package calib

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSavedM5Refit(t *testing.T) {
	for _, name := range []string{"quiet", "contended"} {
		t.Run(name, func(t *testing.T) {
			b, e := os.ReadFile(filepath.Join("testdata", "calib-"+name+".json"))
			if e != nil {
				t.Fatal(e)
			}
			var c Calibration
			if e = json.Unmarshal(b, &c); e != nil {
				t.Fatal(e)
			}
			before, _ := json.Marshal(c.Rows)
			c.FitRows()
			after, _ := json.Marshal(c.Rows)
			if string(before) != string(after) {
				t.Fatal("refit mutated source rows")
			}
			if len(c.Fits) < 85 {
				t.Fatal("lost operation groups", len(c.Fits))
			}
			for _, f := range c.Fits {
				if f.Multiplier <= 0 || !finite(f.Multiplier) {
					t.Fatal("invalid multiplier", f)
				}
				if name == "contended" && (f.Mode == "decode" || f.Mode == "prefill") && f.Rejected == "" && f.BandPct < 35 {
					t.Fatal("contended main fit became falsely narrow", f)
				}
				if f.Class == "dense" || f.Class == "gemma" || f.Class == "moe" {
					if f.Rejected != "no held-out layer check" {
						t.Fatal("invented validation", f)
					}
				}
			}
			if _, _, ok := c.Factor("hybrid", "IQ3_XXS", 0, 40, "decode"); ok {
				t.Fatal("bad saved hybrid check accepted")
			}
			v, band, ok := c.Factor("sliding", "MXFP4", 0, 24, "decode")
			if name == "quiet" {
				// Original quiet band was over 100%; its check and rep spread still
				// cannot support 10-30%. Do not turn this fixture into clean data.
				if !ok || v < .8 || v > 1.4 || band < 40 || band > 60 {
					t.Fatal(v, band, ok)
				}
			} else if ok && band < 65 {
				t.Fatal("contended repetitions got a narrow band", v, band)
			}
			if name == "contended" {
				if _, _, ok = c.Factor("hybrid", "IQ3_XXS", 0, 40, "prefill"); ok {
					t.Fatal("contended prefill check accepted")
				}
			}
		})
	}
}

func cleanRows(class, q string) []Row {
	var rows []Row
	for _, l := range []int{2, 6, 4} {
		for _, rep := range []float64{.99, 1, 1.01} {
			cost := (.01 + .002*float64(l)) * rep
			if l == 4 {
				cost *= 1.02
			}
			rows = append(rows, Row{Probe: class + "-" + q + "-" + string(rune('0'+l)), Class: class, Quant: q, Layers: l, Check: l == 4, Gen: 8, Seconds: cost, Baseline: .02 + .003*float64(l)})
		}
	}
	return rows
}
func TestCleanMediansYieldUsefulBand(t *testing.T) {
	c := Calibration{Rows: cleanRows("dense", "Q4_K_M")}
	c.FitRows()
	v, b, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode")
	if !ok || b < 10 || b > 30 || math.Abs(v-.128/.082) > 1e-8 {
		t.Fatal(v, b, ok)
	}
	if c.Fits[0].TrainSamples != 2 || c.Fits[0].Samples != 3 {
		t.Fatal("raw repetitions became independent observations", c.Fits[0])
	}
	// A single slow repetition changes neither the fit nor the band much.
	c.Rows[1].Seconds = 1
	c.FitRows()
	v, b, ok = c.Factor("dense", "Q4_K_M", 0, 36, "decode")
	if !ok || b > 30 || v < 1.4 || v > 1.7 {
		t.Fatal("median was not robust", v, b, ok)
	}
}
func TestBadCheckAndNoisyRepetitions(t *testing.T) {
	for _, which := range []string{"bad-check", "noisy", "missing-check"} {
		c := Calibration{Rows: cleanRows("dense", "Q4_K_M")}
		for i := range c.Rows {
			r := &c.Rows[i]
			if which == "bad-check" && r.Check {
				r.Seconds *= 2
			}
			if which == "noisy" {
				r.Seconds *= []float64{.5, 1, 2}[i%3]
			}
		}
		if which == "missing-check" {
			c.Rows = c.Rows[:6]
		}
		c.FitRows()
		_, b, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode")
		if which == "noisy" {
			if ok && b < 80 {
				t.Fatal("noisy fit supplied a narrow band", b)
			}
		} else if ok {
			t.Fatal(which, "accepted")
		}
	}
}
func TestMedianRowsRefitFromRaw(t *testing.T) {
	source := cleanRows("dense", "Q4_K_M")
	var rows []Row
	for i := 0; i < len(source); i += 3 {
		r := source[i]
		samples := []float64{}
		for _, v := range source[i : i+3] {
			samples = append(samples, v.Seconds*8e9)
		}
		r.Raw, _ = json.Marshal(BenchRow{Gen: 8, Samples: samples})
		r.Seconds = median([]float64{source[i].Seconds, source[i+1].Seconds, source[i+2].Seconds})
		rows = append(rows, r)
	}
	a, b := Calibration{Rows: source}, Calibration{Rows: rows}
	a.FitRows()
	b.FitRows()
	va, ba, oka := a.Factor("dense", "Q4_K_M", 0, 36, "decode")
	vb, bb, okb := b.Factor("dense", "Q4_K_M", 0, 36, "decode")
	if !oka || !okb || math.Abs(va-vb) > 1e-12 || ba != bb {
		t.Fatal(va, ba, vb, bb)
	}
}
func TestDepthCostTransferAndQuantComposition(t *testing.T) {
	c := Calibration{Rows: append(cleanRows("dense", "Q4_K_M"), cleanRows("dense", "Q8_0")...)}
	for _, r := range append([]Row(nil), c.Rows...) {
		if r.Quant != "Q4_K_M" || r.Layers != 6 {
			continue
		}
		for _, d := range []int{8192, 32768} {
			deep := r
			deep.Depth = d
			deep.Seconds += float64(d) * 1e-6
			deep.Baseline += float64(d) * 2e-6
			c.Rows = append(c.Rows, deep)
		}
	}
	c.FitRows()
	for _, q := range []string{"Q4_K_M", "Q8_0"} {
		for _, d := range []int{8192, 32768} {
			v, b, ok := c.Factor("dense", q, d, 36, "decode")
			if !ok || v <= 0 || b < 10 || b > 30 {
				t.Fatal(q, d, v, b, ok)
			}
		}
	}
	for _, f := range c.Fits {
		if f.Depth > 0 && f.DepthSource == "" {
			t.Fatal("depth transfer was hidden", f)
		}
	}
	// An actual bad deep held-out check takes precedence over any transfer.
	c.Fits = append(c.Fits, Fit{Class: "dense", Quant: "Q8_0", Depth: 8192, Mode: "decode", BandPct: -1, Rejected: "held-out error exceeds 35%"})
	if _, _, ok := c.Factor("dense", "Q8_0", 8192, 36, "decode"); ok {
		t.Fatal("rejected check bypassed")
	}
}
func TestPlanCoverageAndBudget(t *testing.T) {
	for _, quick := range []bool{false, true} {
		steps := Plan(quick)
		total := EstimatedSeconds(steps)
		if !quick && (total < 300 || total > 420) {
			t.Fatal("full M5 estimate", total)
		}
		if quick && (total < 80 || total > 150) {
			t.Fatal("quick M5 estimate", total)
		}
		deep := map[string]int{}
		train := map[string]int{}
		checks := map[string]int{}
		for _, s := range steps {
			args := strings.Join(s.Args("x", false), " ")
			if !strings.Contains(args, "-lm mlock") || strings.Contains(args, "--no-warmup") || !strings.Contains(args, "-r 3") {
				t.Fatal(args)
			}
			if s.Group != 0 {
				continue
			}
			key := s.Probe.Shape.Class + "/" + s.Probe.Quant
			if s.Probe.Check {
				checks[key]++
			} else {
				train[key]++
			}
			if s.Depths != "0" {
				deep[s.Probe.Shape.Class]++
				if s.Probe.Quant != referenceQuant(s.Probe.Shape.Class) {
					t.Fatal("non-reference depth sweep")
				}
			}
		}
		for k, n := range train {
			if n != 2 || checks[k] != 1 {
				t.Fatal("missing training or held-out coverage", k, n, checks[k])
			}
		}
		want := 5
		if quick {
			want = 2
		}
		if len(deep) != want {
			t.Fatal(deep)
		}
		for _, n := range deep {
			if n != 1 {
				t.Fatal("repeated family depth sweep", deep)
			}
		}
	}
}
func TestMlockWarningAndFallback(t *testing.T) {
	for _, line := range []string{"warning: failed to mlock 4096 bytes", "VirtualLock failed", "mlock: operation not permitted"} {
		if !mlockFailed(line) {
			t.Fatal(line)
		}
	}
	for _, line := range []string{"using load mode mlock", "failed to create Metal command queue"} {
		if mlockFailed(line) {
			t.Fatal(line)
		}
	}
	if os.PathSeparator == '\\' {
		t.Skip("shell helper")
	}
	engine := filepath.Join(t.TempDir(), "bench")
	script := `#!/bin/sh
if [ "$2" = mlock ]; then
 echo 'warning: failed to mlock' >&2
 echo '[]'
 exit 0
fi
echo '[{"n_gen":8,"samples_ns":[800,808,816]}]'
`
	if e := os.WriteFile(engine, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	count := 0
	e := runBench(context.Background(), engine, []string{"-lm", "mlock"}, func(json.RawMessage, BenchRow) error { count++; return nil })
	if e == nil || !strings.Contains(e.Error(), errMlockUnavailable.Error()) || count != 0 {
		t.Fatal(count, e)
	}
	e = runBench(context.Background(), engine, []string{"-lm", "mmap"}, func(json.RawMessage, BenchRow) error { count++; return nil })
	if e != nil || count != 1 {
		t.Fatal(count, e)
	}
}

func TestNonnegativeInterceptAndMissingSpread(t *testing.T) {
	rows := cleanRows("dense", "Q4_K_M")
	for i := range rows {
		r := &rows[i]
		value := .016 * float64(r.Layers)
		if r.Layers == 2 {
			value = .02
		}
		if r.Layers == 6 {
			value = .10
		}
		r.Seconds = value
	}
	c := Calibration{Rows: rows}
	c.FitRows()
	if len(c.Fits) != 1 || c.Fits[0].Fixed != 0 {
		t.Fatal("nonnegative boundary lost", c.Fits)
	}
	if _, b, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode"); !ok || b < 10 || b > 30 {
		t.Fatal(b, ok)
	}
	c.Rows = []Row{rows[0], rows[3], rows[6]}
	c.FitRows()
	if _, _, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode"); ok {
		t.Fatal("one repetition supplied an invented spread")
	}
}

func TestLoadingOldFitsRebuildsFromRaw(t *testing.T) {
	home := t.TempDir()
	c := Calibration{Version: GeneratorVersion, BaselineVer: BaselineVersion, Rows: cleanRows("dense", "Q4_K_M"), Notes: []string{"old 75% interval"}, Fits: []Fit{{Class: "dense", Quant: "Q4_K_M", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 100, BaseLayer: 100, BandPct: 1}}}
	if e := c.Save(home); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(home)
	if e != nil {
		t.Fatal(e)
	}
	v, b, ok := loaded.Factor("dense", "Q4_K_M", 0, 36, "decode")
	if loaded.FittingVersion != FittingVersion || !ok || v > 2 || b < 10 || strings.Contains(strings.Join(loaded.Notes, " "), "75%") {
		t.Fatal("old fitting rules survived", v, b, loaded.Notes)
	}
	if loaded.Usable() {
		t.Fatal("depth-zero fit claimed full timing coverage")
	}
}

func TestOlderPredictorRebaselinesWithoutLosingRows(t *testing.T) {
	home := t.TempDir()
	rows := append(cleanRows("dense", "Q4_K_M"), Row{Probe: "real", Class: "real", Seconds: 0.05})
	c := Calibration{Version: GeneratorVersion, FittingVersion: FittingVersion, Complete: true, Rows: rows, Fits: []Fit{{Class: "dense", Quant: "Q4_K_M", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 1, BaseLayer: 1, BandPct: 5}}}
	if e := c.Save(home); e != nil { // no baseline_version: written by the older predictor
		t.Fatal(e)
	}
	loaded, e := Load(home)
	if e != nil || !loaded.NeedsRebaseline || len(loaded.Fits) != 0 || len(loaded.Rows) != len(rows) {
		t.Fatal("old-baseline fits were used, or measurements were lost", loaded, e)
	}
	loaded.Rebaseline(func(Probe, BenchRow) float64 { return 0.01 })
	if loaded.NeedsRebaseline || loaded.BaselineVer != BaselineVersion {
		t.Fatal("rebaseline did not take", loaded)
	}
	for _, r := range loaded.Rows {
		if r.Probe != "real" && r.Baseline != 0.01 {
			t.Fatal("row kept its old baseline", r)
		}
	}
}
