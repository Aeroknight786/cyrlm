package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/harness"
	"github.com/aeroknight786/cyrlm/internal/hw"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
)

// profileIDsFor maps an installed runtime to the simulation profiles that
// describe it, with a note when the mapping is approximate.
func profileIDsFor(ir hw.InstalledRuntime) ([]string, string) {
	switch ir.ID {
	case hw.RuntimeOllama:
		return []string{"ollama"}, ""
	case hw.RuntimeLlamaCpp:
		return []string{"llamacpp"}, ""
	case hw.RuntimeLMStudio:
		return []string{"lmstudio", "lmstudio-mlx"}, ""
	case hw.RuntimeMLXLM:
		return []string{"mlx-lm"}, ""
	case hw.RuntimeJan, hw.RuntimeLemonade, hw.RuntimeDocker:
		return []string{"llamacpp"}, "runs llama.cpp underneath: simulated with the llama.cpp profile (its own defaults are not modelled yet)"
	case hw.RuntimeVLLM:
		return nil, "detected but not simulated yet: vLLM serves safetensors/FP8/AWQ models, which the catalog does not describe yet"
	}
	return nil, "not simulated"
}

var runtimeTitles = map[string]string{
	hw.RuntimeOllama: "Ollama", hw.RuntimeLMStudio: "LM Studio", hw.RuntimeLlamaCpp: "llama.cpp", hw.RuntimeMLXLM: "mlx-lm",
	hw.RuntimeVLLM: "vLLM", hw.RuntimeJan: "Jan", hw.RuntimeLemonade: "Lemonade", hw.RuntimeDocker: "Docker Model Runner",
}

var installHints = map[string]string{
	"ollama":       "https://ollama.com/download",
	"llamacpp":     "brew install llama.cpp · winget install llama.cpp · or a prebuilt release from github.com/ggml-org/llama.cpp",
	"lmstudio":     "https://lmstudio.ai",
	"lmstudio-mlx": "https://lmstudio.ai (MLX engine, Apple Silicon)",
	"mlx-lm":       "pip install mlx-lm (Apple Silicon)",
}

// installed describes the runtimes found on this machine.
type installed struct {
	all       []hw.InstalledRuntime
	byProfile map[string][]hw.InstalledRuntime
	detected  bool // false for --device what-if targets
}

func detectInstalled(t predict.Target) installed {
	inst := installed{byProfile: map[string][]hw.InstalledRuntime{}}
	if t.Synthetic {
		return inst
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	inst.detected = true
	inst.all = hw.DetectRuntimes(ctx)
	for _, ir := range inst.all {
		ids, _ := profileIDsFor(ir)
		for _, id := range ids {
			inst.byProfile[id] = append(inst.byProfile[id], ir)
		}
	}
	return inst
}

func (in installed) has(profileID string) bool { return len(in.byProfile[profileID]) > 0 }

func (in installed) describe(profileID string) string {
	var parts []string
	for _, ir := range in.byProfile[profileID] {
		s := runtimeTitles[ir.ID]
		if ir.Version != "" {
			s += " " + ir.Version
		}
		if ir.Running {
			s += " (running)"
		}
		parts = append(parts, s)
	}
	return strings.Join(parts, ", ")
}

// usable reports whether a prediction is a working setup for the scenario:
// it fits in memory, the harness prompt fits the context, and it makes a
// timing claim.
func usable(r predict.Result) bool {
	return r.Unsupported == "" &&
		(r.Memory.Level == predict.FitOK || r.Memory.Level == predict.FitTight) &&
		r.Scenario.FitsInContext && !r.Accuracy.NoClaim
}

// recommend returns the fastest working setup overall and among installed
// runtimes (nil when none qualifies), ranked by the scenario's task time.
func recommend(rs []predict.Result, in installed, h harness.Harness) (best, bestInstalled *predict.Result) {
	idx := make([]int, 0, len(rs))
	for i := range rs {
		if usable(rs[i]) && h.CompatibleWith(rs[i].Runtime.APIs) {
			idx = append(idx, i)
		}
	}
	sort.SliceStable(idx, func(a, b int) bool {
		ra, rb := rs[idx[a]], rs[idx[b]]
		if ra.Memory.Level != rb.Memory.Level {
			return ra.Memory.Level == predict.FitOK
		}
		return ra.Scenario.Session < rb.Scenario.Session
	})
	for _, i := range idx {
		if best == nil {
			best = &rs[i]
		}
		if bestInstalled == nil && in.has(rs[i].Runtime.ID) {
			bestInstalled = &rs[i]
		}
	}
	return best, bestInstalled
}

func modeLabel(r *predict.Result) string {
	if r.Settings.Tuned {
		return "tuned settings"
	}
	return "default settings"
}

func printRecommendation(rs []predict.Result, in installed, h harness.Harness) {
	best, mine := recommend(rs, in, h)
	if best == nil {
		fmt.Printf("Recommendation: no runtime gives a working %s setup for this model here — try a smaller quant (--quant), a smaller model, or --ctx.\n", h.Name)
		return
	}
	fmt.Printf("Best setup: %s with %s — task ≈ %s, first reply %s, then ~%s per turn.\n",
		best.Runtime.Name, modeLabel(best), dur(best.Scenario.Session), dur(best.Scenario.ColdFirstReply), dur(best.Scenario.FollowUpTurn))
	switch {
	case !in.detected:
		fmt.Printf("  (what-if machine: installed runtimes not checked) Install: %s\n", installHints[best.Runtime.ID])
	case mine == nil && len(in.all) == 0:
		fmt.Printf("  No local runtime is installed. Install: %s\n", installHints[best.Runtime.ID])
	case mine == nil:
		fmt.Printf("  None of your installed runtimes (%s) gives a working setup for this model. Install: %s\n", installedNames(in), installHints[best.Runtime.ID])
	case mine.Runtime.ID == best.Runtime.ID && mine.Settings.Tuned == best.Settings.Tuned:
		fmt.Printf("  You already have it: %s.\n", in.describe(mine.Runtime.ID))
	default:
		slower := 100 * (mine.Scenario.Session.Seconds()/best.Scenario.Session.Seconds() - 1)
		fmt.Printf("  With what you have — %s — use %s (%s): task ≈ %s", in.describe(mine.Runtime.ID), mine.Runtime.Name, modeLabel(mine), dur(mine.Scenario.Session))
		if slower >= 5 {
			fmt.Printf(", %.0f%% slower than the best; to get the best install: %s", slower, installHints[best.Runtime.ID])
		} else {
			fmt.Print(", about as fast as the best")
		}
		fmt.Println(".")
	}
	if pick := choose(best, mine); pick.Settings.Tuned && len(pick.TuneSteps) > 0 {
		fmt.Printf("  Settings for %s:\n", pick.Runtime.Name)
		for _, s := range pick.TuneSteps {
			fmt.Printf("    - %s\n", s)
		}
	}
	for _, ir := range in.all {
		if _, note := profileIDsFor(ir); note != "" {
			fmt.Printf("  · %s: %s\n", runtimeTitles[ir.ID], note)
		}
	}
}

func choose(best, mine *predict.Result) *predict.Result {
	if mine != nil && mine.Scenario.Session.Seconds() <= best.Scenario.Session.Seconds()*1.05 {
		return mine
	}
	return best
}

func installedNames(in installed) string {
	var n []string
	for _, ir := range in.all {
		n = append(n, runtimeTitles[ir.ID])
	}
	return strings.Join(n, ", ")
}

// chooseRuntime picks the runtime to simulate for serve/try: explicit
// --runtime, else the best working setup among installed runtimes, else the
// best working setup overall.
func chooseRuntime(c common, t predict.Target, m catalog.Model, sc predict.Scenario, h harness.Harness) (runtime.Profile, string, error) {
	if c.runtime != "" && c.runtime != "all" {
		p, err := runtime.Get(c.runtime)
		return p, "you chose it with --runtime", err
	}
	in := detectInstalled(t)
	c.runtime = "all"
	rs, err := predictAll(c, m, t, sc, []bool{c.tuned})
	if err != nil {
		return runtime.Profile{}, "", err
	}
	best, mine := recommend(rs, in, h)
	switch {
	case mine != nil:
		return mine.Runtime, "best of your installed runtimes: " + in.describe(mine.Runtime.ID), nil
	case best != nil:
		return best.Runtime, "the fastest working option here; not installed yet", nil
	}
	for _, r := range rs {
		if r.Unsupported == "" {
			return r.Runtime, "no runtime gives a working setup with these settings; simulating anyway (try a smaller model or --ctx)", nil
		}
	}
	return runtime.Profile{}, "", fmt.Errorf("no runtime profile supports this machine")
}
