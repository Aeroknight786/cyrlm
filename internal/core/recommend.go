package core

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
)

// ProfileIDsFor maps an installed runtime to the simulation profiles that
// describe it, with a note when the mapping is approximate.
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
func usable(r predict.Result) bool {
	return r.Unsupported == "" &&
		(r.Memory.Level == predict.FitOK || r.Memory.Level == predict.FitTight) &&
		r.Scenario.FitsInContext && !r.Accuracy.NoClaim
}

// Recommend returns the fastest working setup overall and among installed
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
// --runtime, else the best working setup among installed runtimes, else the
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
	for _, r := range rs {
		if r.Unsupported == "" {
			return r.Runtime, "no runtime gives a working setup with these settings; simulating anyway (try a smaller model or --ctx)", nil
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
func VerdictFor(r predict.Result) (string, int) {
	first := r.Scenario.ColdFirstReply.Seconds()
	turn := r.Scenario.FollowUpTurn.Seconds()
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
		floor := 0.
		switch c.QualityFloor {
		case "3bit-ok":
			floor = 3
		case "4bit+":
			floor = 4
		case "8bit":
			floor = 8
		}
		if floor > 0 && quantBits(qn) < floor {
			continue
		}

		cq := c
		cq.Quant = qn
		modes := []bool{true}
		if c.Boosts == "off" {
			modes = []bool{false}
		}
		rs, err := PredictAll(cq, m, t, sc, modes)
		if err != nil {
			continue // this file does not exist for any runtime here
		}
		filtered := rs[:0]
		for _, r := range rs {
			if rejectCandidate(c, r) || (c.Engines == "installed" && !in.Has(r.Runtime.ID)) {
				continue
			}
			filtered = append(filtered, r)
		}
		rs = filtered
		best, mine := Recommend(rs, in, hh)
		pick := best
		if mine != nil && best != nil && mine.Scenario.Session.Seconds() <= best.Scenario.Session.Seconds()*1.05 {
			pick = mine
		}
		if pick == nil {
			continue
		}
		v, rank := VerdictFor(*pick)
		cands = append(cands, Row{Result: *pick, Verdict: v, Rank: rank})
	}
	if len(cands) == 0 {
		return Row{}, false, nil
	}
	top := cands[0].Rank
	for _, r := range cands {
		if r.Rank < top {
			top = r.Rank
		}
	}
	def := m.Quants[0]
	var pick *Row
	for i := range cands {
		r := &cands[i]
		if r.Rank != top {
			continue
		}
		if r.Result.Quant.Name == def.Name || (def.Format == "gguf" && r.Result.Quant.Format == "mlx" && r.Result.Quant.FileBytes <= def.FileBytes*11/10) {
			// The default file (or its MLX equivalent) reaches the top tier.
			if pick == nil || pick.Result.Quant.FileBytes < def.FileBytes*9/10 || r.Result.Scenario.Session < pick.Result.Scenario.Session {
				pick = r
			}
			continue
		}
		if r.Result.Quant.FileBytes > def.FileBytes {
			continue // bigger than the default: slower for a small quality gain
		}
		if pick == nil || (pick.Result.Quant.FileBytes < def.FileBytes*9/10 && r.Result.Quant.FileBytes > pick.Result.Quant.FileBytes) {
			pick = r
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
			r := predict.Predict(predict.Input{
				Model: m, QuantName: c.Quant, Runtime: p, Target: t, Tuned: tuned,
				CtxOverride: c.Context, KVCacheType: c.KV, Scenario: sc, Calibrated: c.Probe && t.CPUOnly,
				BackendOverride: c.Backend,
			})
			if r.Unsupported != "" && (c.Runtime == "" || c.Runtime == "all") {
				continue // runtimes that can't run this model here are simply not listed
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func rejectCandidate(c Options, r predict.Result) bool {
	return (c.MaxDownloadGB > 0 && float64(r.Quant.FileBytes)/1e9 > c.MaxDownloadGB) || (c.MinTPS > 0 && r.DecodeTPSDeep < c.MinTPS) || (c.MaxTurn > 0 && r.Scenario.FollowUpTurn.Seconds() > c.MaxTurn) || (c.MinBits > 0 && r.Model.TotalParamsB > 0 && float64(r.Quant.FileBytes)*8/(r.Model.TotalParamsB*1e9) < c.MinBits)
}

func quantBits(q string) float64 {
	q = strings.ToUpper(q)
	if q == "BF16" || q == "F16" {
		return 16
	}
	for _, r := range q {
		if r >= '0' && r <= '9' {
			return float64(r - '0')
		}
	}
	return 0
}
