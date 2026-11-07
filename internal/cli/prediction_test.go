package cli

import (
	"testing"

	"github.com/aeroknight786/trialm/internal/predict"
)

func TestPredictionJSONCarriesKind(t *testing.T) {
	r := predict.Result{Kind: "calibrated", Timing: &predict.Timing{VerifySeconds: map[int]float64{2: .03}}}
	js := jsonResults([]predict.Result{r})
	if len(js) != 1 || js[0].Kind != "calibrated" || js[0].VerifySeconds[2] != .03 {
		t.Fatal(js)
	}
}
