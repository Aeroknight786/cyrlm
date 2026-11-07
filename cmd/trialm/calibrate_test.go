package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
