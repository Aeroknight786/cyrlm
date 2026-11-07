package core

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/predict"
)

func TestSetupPredictionUsesReadyTargetDepth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("TRIALM_HOME", filepath.Join(home, ".trialm"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	machine, err := predict.SyntheticSystem("cpu", 64, 0)
	if err != nil {
		t.Fatal(err)
	}
	target := predict.TargetFromSystem(machine, 0)
	target.Synthetic = true
	model, _ := catalog.Lookup("qwen3:8b")
	profile, _ := engine.Get("llamacpp")
	h, _ := harness.Get("aider")
	options := Options{Runtime: "llamacpp", Quant: "Q4_K_M", Context: 32768, Tuned: true}
	plan, err := (Session{}).Plan(context.Background(), PlanOptions{Options: options, Model: model, Target: target, Scenario: h.Scenario, Harness: h})
	if err != nil {
		t.Fatal(err)
	}
	result := predict.Predict(predict.Input{Model: model, QuantName: options.Quant, Runtime: profile, Target: target, Tuned: true, CtxOverride: options.Context, Scenario: h.Scenario})
	expected := result.Timing.DecodeTPS(plan.ContextTokens)
	if math.Abs(plan.Predicted.DecodeTPSTarget-expected) > 1e-9 {
		t.Fatalf("target prediction %g, want %g at %d tokens", plan.Predicted.DecodeTPSTarget, expected, plan.ContextTokens)
	}
}
