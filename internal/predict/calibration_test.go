package predict

import (
	"math"
	"testing"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/runtime"
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
