package predict

import (
	"encoding/json"
	"math"
	"testing"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
)

func realFixture(t *testing.T, samples []calib.RealSample) (Input, Result, *calib.Calibration) {
	t.Helper()
	in, _, _ := calibrationFixture(t)
	in.Model, _ = catalog.Lookup("qwen3.5:9b")
	in.Target.Synthetic = true
	base := Predict(in)
	in.Target.Synthetic = false
	c := &calib.Calibration{}
	for _, s := range samples {
		tag, _ := json.Marshal(struct{ Model, Engine, Mode string }{in.Model.ID, in.Runtime.ID, s.Mode})
		c.Rows = append(c.Rows, calib.Row{Probe: "real", Quant: base.Quant.Name, Depth: s.Depth, Prompt: s.Prompt, Gen: s.Generated, TokensEstimated: s.TokensEstimated, Seconds: 1 / s.Speed, Raw: tag})
	}
	return in, base, c
}

func TestRealCorrectionUsesMeasuredInterval(t *testing.T) {
	in, base, c := realFixture(t, []calib.RealSample{
		{Mode: "decode", Depth: 65536, Prompt: 65536, Generated: 32, Speed: 8.96},
		{Mode: "prefill", Prompt: 512, Generated: 16, Speed: 368},
	})
	// A conflicting shorter-depth observation must not dilute the 64K sample.
	shorter := c.Rows[0]
	shorter.Depth, shorter.Prompt, shorter.Seconds = 16384, 16384, 1.0/100
	c.Rows = append([]calib.Row{shorter}, c.Rows...)
	r := WithCalibration(in, c)
	if math.Abs(r.Timing.DecodeTPS(65536)-8.96) > 1e-9 {
		t.Fatal(r.Timing.DecodeTPS(65536))
	}
	short, deep := r.Timing.PrefillTPS(0, 512), r.Timing.PrefillTPS(32768, 33280)
	if math.Abs(short-368) > 1e-9 || deep >= short {
		t.Fatal(short, deep)
	}
	if math.Abs(deep/short-base.Timing.PrefillTPS(32768, 33280)/base.Timing.PrefillTPS(0, 512)) > 1e-12 {
		t.Fatal("depth slowdown changed")
	}
	t.Logf("decode64=%g; prefill0=%g prefill32=%g", r.Timing.DecodeTPS(65536), short, deep)
	// Normalize deep prompt intervals too, including intervals spanning a bucket.
	c.Rows = []calib.Row{c.Rows[1], c.Rows[2]}
	c.Rows[1].Depth, c.Rows[1].Prompt = 7168, 3073
	c.Rows[1].Seconds = 1 / (.7 * base.Timing.PrefillTPS(7168, 10241))
	c.Rows[0].Depth, c.Rows[0].Prompt = 16384, 16384
	c.Rows[0].Seconds = 1 / (.6 * base.Timing.DecodeTPS(16384))
	second := c.Rows[0]
	second.Depth, second.Prompt, second.Seconds = 65536, 65536, 1/(.6*base.Timing.DecodeTPS(65536))
	c.Rows = append(c.Rows, second)
	r = WithCalibration(in, c)
	for _, d := range []int{0, 8192, 16384, 32768, 65536} {
		if math.Abs(r.Timing.DecodeTPS(d)/base.Timing.DecodeTPS(d)-.6) > 1e-12 {
			t.Fatal("depths pooled before normalization", d)
		}
	}
	if math.Abs(r.Timing.PrefillTPS(7168, 10241)/base.Timing.PrefillTPS(7168, 10241)-.7) > 1e-12 {
		t.Fatal("prefill interval changed")
	}
}

func TestRepeatedRealRunsKeepExtrapolationBand(t *testing.T) {
	for _, mode := range []string{"decode", "prefill"} {
		s := calib.RealSample{Mode: mode, Depth: 65536, Prompt: 512, Speed: 8.96}
		in, base, c := realFixture(t, []calib.RealSample{s, s})
		prior := base.Accuracy
		base.Accuracy.PrefillMeasureOnly = true
		applyMachineCalibration(&base, in, c)

		if base.Accuracy.DecodePct < prior.DecodePct || base.Accuracy.PrefillPct < prior.PrefillPct {
			t.Fatal(base.Accuracy, prior)
		}
		if mode == "decode" && !base.Accuracy.PrefillMeasureOnly {
			t.Fatal("decode-only evidence cleared prefill warning")
		}
		if mode == "prefill" && base.Accuracy.PrefillMeasureOnly {
			t.Fatal("measured prefill evidence unused")
		}
		t.Logf("%s repeated rows retain bands=%d/%d", mode, base.Accuracy.DecodePct, base.Accuracy.PrefillPct)
	}
}

func TestFirstReplyAndExactOutputCount(t *testing.T) {
	timing := &Timing{WeightsBytesPerTok: 1e7, EffBandwidth: 1e9, DecodeMult: 1, PrefillMult: 1, MatmulSecPerTok: .02, AttnFLOPSEff: 1e12, LoadColdS: 3}
	for _, n := range []int{0, 1, 49, 50, 51, 300, 301} {
		sc := Scenario{BasePromptTokens: 100, NewTokensPerTurn: 10, OutputTokensPerTurn: n, Turns: 2, MidDepth: 200}
		r := RunScenario(timing, sc, 1024)
		first := 2 + float64(n)*.01
		want := first + 3 + .2 + float64(n)*.01
		if math.Abs(r.FirstReply.Seconds()-first) > 1e-8 || math.Abs(r.ColdFirstReply.Seconds()-first-3) > 1e-8 || math.Abs(r.Session.Seconds()-want) > 1e-8 || math.Abs(r.FollowUpTurn.Seconds()-(.2+float64(n)*.01)) > 1e-8 {
			t.Fatalf("n=%d result=%+v", n, r)
		}
		if n == 301 {
			t.Logf("301 tokens first=%g cold=%g session=%g follow=%g", r.FirstReply.Seconds(), r.ColdFirstReply.Seconds(), r.Session.Seconds(), r.FollowUpTurn.Seconds())
		}
	}
}

func TestSavedSpecVerifyFitsStillApply(t *testing.T) {
	in, base, c := calibrationFixture(t)
	c.Fits = append(c.Fits, calib.Fit{Class: "dense", Quant: "Q4_K_M", Depth: 0, Mode: "verify3", Fixed: .001, PerLayer: .002, BandPct: 10})
	applyMachineCalibration(&base, in, c)
	if got, want := base.Timing.VerifySeconds[3], 3*(.001+.002*float64(in.Model.Layers)); got != want {
		t.Fatal(got, want)
	}
}
