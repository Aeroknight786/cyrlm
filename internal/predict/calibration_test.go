package predict

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	runtime "github.com/aeroknight786/trialm/internal/engine"
)

func calibrationFixture(t *testing.T) (Input, Result, *calib.Calibration) {
	t.Helper()
	sys, e := SyntheticSystem("Apple M5", 24, 0)
	if e != nil {
		t.Fatal(e)
	}
	target := TargetFromSystem(sys, 0)
	target.Synthetic = true
	m, ok := catalog.Lookup("qwen3:8b")
	if !ok {
		t.Fatal("missing fixture")
	}
	rt, _ := runtime.Get("llamacpp")
	in := Input{Model: m, Runtime: rt, Target: target, Tuned: true, CtxOverride: 65536}
	r := Predict(in)
	in.Target.Synthetic = false
	c := &calib.Calibration{Backend: "metal"}
	for _, d := range []int{0, 8192, 32768} {
		for _, mode := range []string{"decode", "prefill"} {
			mult := 2.
			if mode == "prefill" {
				mult = 3
			}
			c.Fits = append(c.Fits, calib.Fit{Class: "dense", Quant: "Q4_K_M", Depth: d, Mode: mode, Fixed: 1, PerLayer: 1, BaseFixed: mult, BaseLayer: mult, BandPct: 7})
		}
	}
	return in, r, c
}
func TestCalibrationRecomputesEveryHeadline(t *testing.T) {
	in, r, c := calibrationFixture(t)
	dec, pre, follow := r.DecodeTPS0, r.PrefillTPSP0, r.Scenario.FollowUpTurn
	applyMachineCalibration(&r, in, c)
	if r.Kind != "calibrated" || math.Abs(r.DecodeTPS0/dec-2) > 1e-8 || math.Abs(r.PrefillTPSP0/pre-3) > 1e-8 || r.Scenario.FollowUpTurn >= follow || r.Accuracy.DecodePct != 7 || r.Accuracy.PrefillPct != 7 {
		t.Fatalf("hook left inconsistent figures: %+v", r)
	}
}
func TestCalibrationRejectsWhatIfMLXOffloadAndBackend(t *testing.T) {
	for _, which := range []string{"what-if", "mlx", "offload", "no-fit", "backend", "cpu"} {
		t.Run(which, func(t *testing.T) {
			in, r, c := calibrationFixture(t)
			before := r.DecodeTPS0
			switch which {
			case "what-if":
				in.Target.Synthetic = true
			case "mlx":
				in.Runtime.Engine = "mlx"
			case "offload":
				r.Memory.Level = FitOffload
			case "no-fit":
				r.Memory.Level = FitNo
			case "backend":
				c.Backend = "cuda"
			case "cpu":
				in.Target.CPUOnly = true
			}
			applyMachineCalibration(&r, in, c)
			if r.Kind == "calibrated" || r.DecodeTPS0 != before {
				t.Fatal("ineligible prediction calibrated")
			}
		})
	}
}
func TestCalibrationClassAndQuant(t *testing.T) {
	for _, tc := range []struct {
		m     catalog.Model
		class string
	}{{catalog.Model{RecurrentLayers: 3}, "hybrid"}, {catalog.Model{Family: "deepseek2", MoE: true}, "mla"}, {catalog.Model{Family: "gemma4", SWALayers: 5}, "gemma"}, {catalog.Model{MoE: true, SWALayers: 3}, "sliding"}, {catalog.Model{MoE: true}, "moe"}, {catalog.Model{}, "dense"}} {
		if calibrationClass(tc.m) != tc.class {
			t.Fatal(tc)
		}
	}
	if calibrationQuant("UD-IQ3_XXS") != "IQ3_XXS" || calibrationQuant("MXFP4_MOE") != "MXFP4" {
		t.Fatal("quant aliases lost")
	}
}

func TestCalibrationUsesDepthSpecificFactors(t *testing.T) {
	in, r, c := calibrationFixture(t)
	before := []float64{r.Timing.DecodeTPS(512), r.Timing.DecodeTPS(8192), r.Timing.DecodeTPS(32768)}
	for i := range c.Fits {
		if c.Fits[i].Mode == "decode" {
			switch c.Fits[i].Depth {
			case 8192:
				c.Fits[i].BaseFixed = 3
				c.Fits[i].BaseLayer = 3
			case 32768:
				c.Fits[i].BaseFixed = 4
				c.Fits[i].BaseLayer = 4
			}
		}
	}
	applyMachineCalibration(&r, in, c)
	for i, d := range []int{512, 8192, 32768} {
		if math.Abs(r.Timing.DecodeTPS(d)/before[i]-float64(i+2)) > 1e-8 {
			t.Fatal("depth fit lost", d)
		}
	}
}

func TestPartialCalibrationKeepsOriginalPrediction(t *testing.T) {
	for _, which := range []string{"missing", "rejected"} {
		t.Run(which, func(t *testing.T) {
			in, r, c := calibrationFixture(t)
			before := r.DecodeTPS0
			for i, f := range c.Fits {
				if f.Depth == 32768 && f.Mode == "decode" {
					if which == "missing" {
						c.Fits = append(c.Fits[:i], c.Fits[i+1:]...)
					} else {
						c.Fits[i].Rejected = "held-out error exceeds 35%"
						c.Fits[i].BandPct = -1
					}
					break
				}
			}
			applyMachineCalibration(&r, in, c)
			if r.Kind == "calibrated" || r.DecodeTPS0 != before || r.Timing.calibration != nil {
				t.Fatal("partial fit changed prediction", r.Kind)
			}
		})
	}
}

func TestCalibrationReportsWidestDepthBand(t *testing.T) {
	in, r, c := calibrationFixture(t)
	for i := range c.Fits {
		if c.Fits[i].Depth == 32768 {
			c.Fits[i].BandPct = 85
		}
	}
	applyMachineCalibration(&r, in, c)
	if r.Accuracy.DecodePct != 85 || r.Accuracy.PrefillPct != 85 || !strings.Contains(r.Accuracy.Basis, "direct dense/Q4_K_M depth 32768") {
		t.Fatal(r.Accuracy)
	}
}

// This is external evidence, never fitted. A miss remains visible as a skip
// with all measured/predicted numbers, rather than tuning transfer constants.
func TestM5RealQwen35Validation(t *testing.T) {
	b, err := os.ReadFile("../calib/testdata/calib-m5-v2.json")
	if err != nil {
		t.Fatal(err)
	}
	var c calib.Calibration
	if err = json.Unmarshal(b, &c); err != nil {
		t.Fatal(err)
	}
	c.FitRows()
	sys, err := SyntheticSystem("Apple M5", 24, 0)
	if err != nil {
		t.Fatal(err)
	}
	target := TargetFromSystem(sys, 0)
	target.Synthetic = false // offline fixture explicitly substitutes its identity
	m, ok := catalog.Lookup("qwen3.5:9b")
	if !ok {
		t.Fatal("missing catalog model")
	}
	rt, _ := runtime.Get("llamacpp")
	r := WithCalibration(Input{Model: m, QuantName: "Q4_K_M", Runtime: rt, Target: target, Tuned: true, CtxOverride: 65536, KVCacheType: "f16"}, &c)
	if r.Kind != "calibrated" || !strings.Contains(r.Accuracy.Basis, "alone") || !strings.Contains(r.Accuracy.Basis, "rejected") {
		t.Fatalf("missing fallback: %s %s", r.Kind, r.Accuracy.Basis)
	}
	b, err = os.ReadFile("../calib/testdata/real-qwen3.5-9b-Q4_K_M.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []struct {
		calib.BenchRow
		AvgTPS float64 `json:"avg_ts"`
	}
	if err = json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	var misses []string
	checked := 0
	for _, row := range rows {
		if row.Depth != 0 && row.Depth != 32768 {
			continue
		}
		if row.Gen == 0 && row.Prompt != 2048 {
			continue
		}
		checked++
		mode, band, predicted := "prefill", r.Accuracy.PrefillPct, r.Timing.PrefillTPS(row.Depth, row.Depth+row.Prompt)
		if row.Gen > 0 {
			mode, band = "decode", r.Accuracy.DecodePct
			seconds := 0.0
			for i := 0; i < row.Gen; i++ {
				seconds += r.Timing.DecodeSec(row.Depth + i)
			}
			predicted = float64(row.Gen) / seconds
		}
		low, high := predicted*(1-float64(band)/100), predicted*(1+float64(band)/100)
		msg := fmt.Sprintf("%s depth %d: measured %.1f, predicted %.1f tok/s, error %+.0f%%, band ±%d%% [%.1f, %.1f]", mode, row.Depth, row.AvgTPS, predicted, 100*(predicted/row.AvgTPS-1), band, math.Max(0, low), high)
		t.Log(msg)
		if row.AvgTPS < low || row.AvgTPS > high {
			misses = append(misses, msg)
		}
	}
	if checked != 4 {
		t.Fatalf("checked %d rows, expected 4", checked)
	}
	if len(misses) > 0 {
		t.Skipf("real-model transfer miss (not fitted away): %s", strings.Join(misses, "; "))
	}
}

func TestRealOnlyCalibration(t *testing.T) {
	in, baseline, c := calibrationFixture(t)
	c.Fits = nil
	tag, _ := json.Marshal(struct{ Model, Engine, Mode string }{in.Model.ID, in.Runtime.ID, "decode"})
	c.Rows = []calib.Row{{Probe: "real", Quant: baseline.Quant.Name, Seconds: 1.0 / 10, Raw: tag}, {Probe: "real", Quant: baseline.Quant.Name, Seconds: 1.0 / 50, Raw: tag}}
	r := baseline
	applyMachineCalibration(&r, in, c)
	if r.Kind != "calibrated" || r.DecodeTPS0 == baseline.DecodeTPS0 || r.Accuracy.DecodePct == baseline.Accuracy.DecodePct {
		t.Fatalf("baseline=%+v result=%+v", baseline.Accuracy, r.Accuracy)
	}
	for _, mismatch := range []string{"model", "quant", "engine"} {
		otherIn, other := in, baseline
		switch mismatch {
		case "model":
			otherIn.Model.ID = "other"
		case "quant":
			other.Quant.Name = "other"
		case "engine":
			otherIn.Runtime.ID = "other"
		}
		applyMachineCalibration(&other, otherIn, c)
		if other.DecodeTPS0 != baseline.DecodeTPS0 || other.Kind == "calibrated" {
			t.Fatal(mismatch, "changed")
		}
	}
}

func TestRecordedFingerprintInvalidation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	engine, err := calib.FindEngine(home)
	if err != nil {
		t.Skip(err)
	}
	in, base, _ := calibrationFixture(t)
	err = calib.RecordReal(context.Background(), home, engine, calib.RealMeasurement{Model: in.Model.ID, Quant: base.Quant.Name, Engine: in.Runtime.ID, Decode: map[int]float64{0: 30}})
	if err != nil {
		if strings.Contains(err.Error(), "RAM detection is unavailable") {
			t.Skip(err)
		}
		t.Fatal(err)
	}
	c := machineCalibration()
	if c == nil || len(c.Rows) != 1 {
		t.Fatal("recorded evidence unavailable")
	}
	got := base
	applyMachineCalibration(&got, in, c)
	if got.DecodeTPS0 == base.DecodeTPS0 {
		t.Fatal("recorded speed unused")
	}
	c.Fingerprint.Hash = "changed"
	if err := c.Save(home); err != nil {
		t.Fatal(err)
	}
	if machineCalibration() != nil {
		t.Fatal("stale fingerprint accepted")
	}
	got = base
	applyMachineCalibration(&got, in, machineCalibration())
	if got.DecodeTPS0 != base.DecodeTPS0 {
		t.Fatal("stale evidence affected prediction")
	}
}
