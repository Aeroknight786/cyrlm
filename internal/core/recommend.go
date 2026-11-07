package core

import (
	"context"
	"fmt"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
	"sort"
	"strings"
	"time"
)

// ProfileIDsFor maps an Installed runtime to the simulation profiles that
// Describe it, with a note when the mapping is approximate.
func ProfileIDsFor(ir hw.InstalledRuntime) ([]string, string) {
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

var RuntimeTitles = map[string]string{
	hw.RuntimeOllama: "Ollama", hw.RuntimeLMStudio: "LM Studio", hw.RuntimeLlamaCpp: "llama.cpp", hw.RuntimeMLXLM: "mlx-lm",
	hw.RuntimeVLLM: "vLLM", hw.RuntimeJan: "Jan", hw.RuntimeLemonade: "Lemonade", hw.RuntimeDocker: "Docker Model Runner",
}

var InstallHints = map[string]string{
	"ollama":       "https://ollama.com/download",
	"llamacpp":     "brew install llama.cpp · winget install llama.cpp · or a prebuilt release from github.com/ggml-org/llama.cpp",
	"lmstudio":     "https://lmstudio.ai",
	"lmstudio-mlx": "https://lmstudio.ai (MLX engine, Apple Silicon)",
	"mlx-lm":       "pip install mlx-lm (Apple Silicon)",
}

// Installed describes the runtimes found on this machine.
type Installed struct {
	All       []hw.InstalledRuntime
	ByProfile map[string][]hw.InstalledRuntime
	Detected  bool // false for --device what-if targets
}

func DetectInstalled(t predict.Target) Installed {
	inst := Installed{ByProfile: map[string][]hw.InstalledRuntime{}}
	if t.Synthetic {
		return inst
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	inst.Detected = true
	inst.All = hw.DetectRuntimes(ctx)
	for _, ir := range inst.All {
		ids, _ := ProfileIDsFor(ir)
		for _, id := range ids {
			inst.ByProfile[id] = append(inst.ByProfile[id], ir)
		}
	}
	return inst
}

func (in Installed) Has(profileID string) bool { return len(in.ByProfile[profileID]) > 0 }

func (in Installed) Describe(profileID string) string {
	var parts []string
	for _, ir := range in.ByProfile[profileID] {
		s := RuntimeTitles[ir.ID]
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
func usable(Result predict.Result) bool {
	return Result.Unsupported == "" &&
		(Result.Memory.Level == predict.FitOK || Result.Memory.Level == predict.FitTight) &&
		Result.Scenario.FitsInContext && !Result.Accuracy.NoClaim
}

// Recommend returns the fastest working setup overall and among Installed
// runtimes (nil when none qualifies), ranked by the scenario's task time.
func Recommend(rs []predict.Result, in Installed, h harness.Harness) (best, bestInstalled *predict.Result) {
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
		if bestInstalled == nil && in.Has(rs[i].Runtime.ID) {
			bestInstalled = &rs[i]
		}
	}
	return best, bestInstalled
}

func Choose(best, mine *predict.Result) *predict.Result {
	if mine != nil && mine.Scenario.Session.Seconds() <= best.Scenario.Session.Seconds()*1.05 {
		return mine
	}
	return best
}

func InstalledNames(in Installed) string {
	var n []string
	for _, ir := range in.All {
		n = append(n, RuntimeTitles[ir.ID])
	}
	return strings.Join(n, ", ")
}

// ChooseRuntime picks the runtime to simulate for serve/try: explicit
// --runtime, else the best working setup among Installed runtimes, else the
// best working setup overall.
func ChooseRuntime(c Options, t predict.Target, m catalog.Model, sc predict.Scenario, h harness.Harness) (runtime.Profile, string, error) {
	if c.Runtime != "" && c.Runtime != "all" {
		p, err := runtime.Get(c.Runtime)
		return p, "you chose it with --runtime", err
	}
	in := DetectInstalled(t)
	c.Runtime = "all"
	rs, err := PredictAll(c, m, t, sc, []bool{c.Tuned})
	if err != nil {
		return runtime.Profile{}, "", err
	}
	best, mine := Recommend(rs, in, h)
	switch {
	case mine != nil:
		return mine.Runtime, "best of your installed runtimes: " + in.Describe(mine.Runtime.ID), nil
	case best != nil:
		return best.Runtime, "the fastest working option here; not installed yet", nil
	}
	for _, Result := range rs {
		if Result.Unsupported == "" {
			return Result.Runtime, "no runtime gives a working setup with these settings; simulating anyway (try a smaller model or --ctx)", nil
		}
	}
	return runtime.Profile{}, "", fmt.Errorf("no runtime profile supports this machine")
}

type Row struct {
	Result  predict.Result
	Verdict string
	Rank    int
}

// VerdictFor classifies how a setup feels in an agent session.
func VerdictFor(Result predict.Result) (string, int) {
	first := Result.Scenario.ColdFirstReply.Seconds()
	turn := Result.Scenario.FollowUpTurn.Seconds()
	switch {
	case turn <= 20 && first <= 120:
		return "comfortable", 0
	case turn <= 60 && first <= 300:
		return "usable", 1
	}
	return "slow", 2
}

// BestFileFor picks the model file (quantization) and runtime to show for
// m. Every file is tried; the model's default file wins when it reaches the
// best speed tier any file reaches, otherwise the largest file (closest to
// the default's quality) that does — so a 3-bit file is chosen only when
// it is what lets the model fit or feel fast here. --quant pins the file.
func BestFileFor(c Options, m catalog.Model, t predict.Target, sc predict.Scenario, in Installed, hh harness.Harness) (Row, bool, error) {
	quants := []string{c.Quant}
	if c.Quant == "" {
		quants = quants[:0]
		for _, q := range m.Quants {
			quants = append(quants, q.Name)
		}
	}
	var cands []Row
	for _, qn := range quants {
		cq := c
		cq.Quant = qn
		rs, err := PredictAll(cq, m, t, sc, []bool{true})
		if err != nil {
			continue // this file does not exist for any runtime here
		}
		best, mine := Recommend(rs, in, hh)
		pick := best
		if mine != nil && best != nil && mine.Scenario.Session.Seconds() <= best.Scenario.Session.Seconds()*1.05 {
			pick = mine
		}
		if pick == nil {
			continue
		}
		v, Rank := VerdictFor(*pick)
		cands = append(cands, Row{Result: *pick, Verdict: v, Rank: Rank})
	}
	if len(cands) == 0 {
		return Row{}, false, nil
	}
	top := cands[0].Rank
	for _, Result := range cands {
		if Result.Rank < top {
			top = Result.Rank
		}
	}
	def := m.Quants[0]
	var pick *Row
	for i := range cands {
		Result := &cands[i]
		if Result.Rank != top {
			continue
		}
		if Result.Result.Quant.Name == def.Name || (def.Format == "gguf" && Result.Result.Quant.Format == "mlx" && Result.Result.Quant.FileBytes <= def.FileBytes*11/10) {
			// The default file (or its MLX equivalent) reaches the top tier.
			if pick == nil || pick.Result.Quant.FileBytes < def.FileBytes*9/10 || Result.Result.Scenario.Session < pick.Result.Scenario.Session {
				pick = Result
			}
			continue
		}
		if Result.Result.Quant.FileBytes > def.FileBytes {
			continue // bigger than the default: slower for a small quality gain
		}
		if pick == nil || (pick.Result.Quant.FileBytes < def.FileBytes*9/10 && Result.Result.Quant.FileBytes > pick.Result.Quant.FileBytes) {
			pick = Result
		}
	}
	if pick == nil {
		for i := range cands {
			if cands[i].Rank == top && (pick == nil || cands[i].Result.Quant.FileBytes < pick.Result.Quant.FileBytes) {
				pick = &cands[i]
			}
		}
	}
	return *pick, true, nil
}

func PredictAll(c Options, m catalog.Model, t predict.Target, sc predict.Scenario, tunedModes []bool) ([]predict.Result, error) {
	var profiles []runtime.Profile
	if c.Runtime == "" || c.Runtime == "all" {
		profiles = runtime.All()
	} else {
		p, err := runtime.Get(c.Runtime)
		if err != nil {
			return nil, err
		}
		profiles = []runtime.Profile{p}
	}
	var out []predict.Result
	for _, p := range profiles {
		for _, tuned := range tunedModes {
			Result := predict.Predict(predict.Input{
				Model: m, QuantName: c.Quant, Runtime: p, Target: t, Tuned: tuned,
				CtxOverride: c.Context, KVCacheType: c.KV, Scenario: sc, Calibrated: c.Probe && t.CPUOnly,
				BackendOverride: c.Backend,
			})
			if Result.Unsupported != "" && (c.Runtime == "" || c.Runtime == "all") {
				continue // runtimes that can't run this model here are simply not listed
			}
			out = append(out, Result)
		}
	}
	return out, nil
}
