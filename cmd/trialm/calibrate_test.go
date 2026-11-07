package main

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/predict"
)

func TestCalibrationPromptEligibility(t *testing.T) {
	empty := func(string) string { return "" }
	for _, args := range [][]string{nil, {"detect"}, {"recommend"}} {
		if !calibrationPromptEligible(args, true, true, empty) {
			t.Fatal(args)
		}
	}
	for _, args := range [][]string{{"predict", "m"}, {"calibrate"}, {"detect", "--json"}, {"detect", "--json=true"}, {"detect", "--device=x"}, {"recommend", "--plain"}, {"detect", "--help"}} {
		if calibrationPromptEligible(args, true, true, empty) {
			t.Fatal(args)
		}
	}
	if calibrationPromptEligible(nil, false, true, empty) || calibrationPromptEligible(nil, true, false, empty) {
		t.Fatal("prompt on pipe")
	}
	for _, key := range []string{"CI", "TRIALM_NO_INPUT"} {
		if calibrationPromptEligible(nil, true, true, func(k string) string {
			if k == key {
				return "1"
			}
			return ""
		}) {
			t.Fatal(key)
		}
	}
}
func TestCalibrationSkipPreservesPreferences(t *testing.T) {
	home := t.TempDir()
	if e := os.WriteFile(filepath.Join(home, "prefs.json"), []byte(`{"unrelated":{"keep":true}}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := saveCalibrationChoice(home, "skipped"); e != nil {
		t.Fatal(e)
	}
	prefs, e := calibrationPrefs(home)
	if e != nil {
		t.Fatal(e)
	}
	var choice string
	_ = json.Unmarshal(prefs["calibration_offer"], &choice)
	var other map[string]bool
	if e = json.Unmarshal(prefs["unrelated"], &other); e != nil || !other["keep"] || choice != "skipped" {
		t.Fatal(prefs, e)
	}
	t.Setenv("TRIALM_HOME", home)
	if e = maybeOfferCalibration([]string{"detect", "--json"}); e != nil {
		t.Fatal(e)
	}
	if calibrationReminderEligible([]string{"recommend", "--json"}) || calibrationReminderEligible([]string{"recommend", "--device", "M5"}) {
		t.Fatal("reminder contaminates machine output")
	}
}
func TestPredictionJSONCarriesKind(t *testing.T) {
	r := predict.Result{Kind: "calibrated", Timing: &predict.Timing{VerifySeconds: map[int]float64{2: .03}}}
	js := jsonResults([]predict.Result{r})
	if len(js) != 1 || js[0].Kind != "calibrated" || js[0].VerifySeconds[2] != .03 {
		t.Fatal(js)
	}
}

func TestCalibrationPlanAndRefitNeedNoEngine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	// No PATH engine and no engines directory. Neither path fingerprints hardware.
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"--plan", "--json"}, {"--plan", "--quick"}} {
		if e := cmdCalibrate(args); e != nil {
			t.Fatal(args, e)
		}
	}
	b, e := os.ReadFile(filepath.Join("..", "..", "internal", "calib", "testdata", "calib-quiet.json"))
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, "calib.json"), b, 0600); e != nil {
		t.Fatal(e)
	}
	if e = cmdCalibrate([]string{"--refit", "--json"}); e != nil {
		t.Fatal(e)
	}
	c, e := calib.Load(home)
	if e != nil || c.FittingVersion != calib.FittingVersion || len(c.Fits) == 0 {
		t.Fatal(c, e)
	}
	for _, args := range [][]string{{"--plan", "--refit"}, {"--status", "--refit"}, {"--plan", "--status"}} {
		if e = cmdCalibrate(args); e == nil {
			t.Fatal("conflicting modes accepted", args)
		}
	}
}

func TestCalibrationCheckModes(t *testing.T) {
	t.Setenv("TRIALM_HOME", t.TempDir())
	for _, args := range [][]string{{"--check", "model.gguf", "--plan"}, {"--check", "model.gguf", "--refit"}, {"--check", "model.gguf", "--status"}, {"--check", "model.gguf", "--quick"}, {"--check"}, {"--check", ""}} {
		if e := cmdCalibrate(args); e == nil {
			t.Fatal("invalid mode accepted", args)
		}
	}
	// A URL is rejected locally before hardware access or a benchmark.
	if e := calibrationCheck(context.Background(), t.TempDir(), "absent-bench", "https://example.com/model.gguf", false); e == nil || !strings.Contains(e.Error(), "local GGUF") {
		t.Fatal(e)
	}
}

func TestCalibrationCheckPredictor(t *testing.T) {
	r := predict.Result{Kind: "calibrated", Accuracy: predict.Accuracy{DecodePct: 80, PrefillPct: 90, Basis: "test path"}, Timing: &predict.Timing{EffBandwidth: 1e9, WeightsBytesPerTok: 5e9, DecodeMult: 1, PrefillMult: 1, MatmulSecPerTok: .01, AttnFLOPSEff: 1e12}}
	for _, row := range []calib.BenchRow{{Gen: 8, Depth: 32768}, {Prompt: 512, Depth: 0}} {
		p, e := calibrationCheckPredictor(r)(row)
		if e != nil || p.Basis != "test path" {
			t.Fatal(p, e)
		}
		want, band := 100., 90
		if row.Gen > 0 {
			want, band = .2, 80
		}
		if math.Abs(p.TPS-want) > 1e-8 || p.BandPct != band {
			t.Fatal(p, want, band)
		}
	}
	if _, e := calibrationCheckPredictor(r)(calib.BenchRow{}); e == nil {
		t.Fatal("zero-token prediction")
	}
	r.Kind = "simulated"
	if _, e := calibrationCheckPredictor(r)(calib.BenchRow{Gen: 8}); e == nil {
		t.Fatal("uncalibrated validation")
	}
}
