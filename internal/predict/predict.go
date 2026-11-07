// Package predict estimates memory fit and inference timing of a model on a
// target machine and runtime.
//
// Decode is memory-bound: seconds/token = bytes read (active weights + KV at
// the current depth) / (efficiency × spec bandwidth) + a per-token overhead.
// Prefill is compute-bound: seconds/token = matmul FLOPs / (eff × spec
// FLOPS) + attention FLOPs at depth / (eff_attn × spec FLOPS). Efficiencies
// are fitted per backend on 565 public llama.cpp results (held out by device:
// decode median error ≈10%, prefill ≈14–18%; see docs/RESEARCH.md).
package predict

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/runtime"
	"github.com/aeroknight786/trialm/internal/specs"
)

type decodeEff struct{ eff, t0ms float64 }

// Decode efficiency (fraction of spec bandwidth) and per-token overhead,
// least-squares fits on llama.cpp tg results (research/out_primary.txt).
var decodeTab = map[string]decodeEff{
	"cuda":   {0.74, 0.87},
	"metal":  {0.85, 4.25},
	"rocm":   {0.64, 0.24},
	"vulkan": {0.75, 0.88},
	"sycl":   {0.70, 0.50},
	"mlx":    {0.85, 3.00}, // little data: Metal-like assumption
	"cpu":    {0.29, 0.00}, // vs SoC/DRAM spec; x86 with probe uses measured bandwidth
}

type prefillEff struct{ dense, moe, attn float64 }

// Prefill efficiencies (fraction of spec dense FP16 FLOPS) for dense matmul,
// MoE matmul and attention, multi-term fits on llama.cpp pp results.
var prefillTab = map[string]prefillEff{
	"cuda":   {0.497, 0.258, 0.313},
	"metal":  {0.705, 0.711, 0.279},
	"rocm":   {0.369, 0.177, 0.090},
	"vulkan": {0.474, 0.163, 0.080}, // fitted attn 0.03 is driven by AMDVLK on Strix Halo; floored
	"sycl":   {0.203, 0.203, 0.203},
	"mlx":    {0.705, 0.711, 0.279},
	"cpu":    {0.90, 0.90, 0.90},
}

// Timing computes simulated local timings. All methods are pure.
type Timing struct {
	calibration   *timingCalibration
	VerifySeconds map[int]float64 // small-prompt verify costs at depth zero

	Model              catalog.Model
	WeightsBytesPerTok float64
	KVElem             float64
	EffBandwidth       float64 // bytes/s
	T0                 float64 // s/token overhead
	MatmulSecPerTok    float64
	AttnFLOPSEff       float64 // FLOP/s for attention
	DecodeMult         float64
	PrefillMult        float64
	LoadColdS          float64 // from disk
	LoadWarmS          float64 // from OS page cache
}

// DecodeSec is seconds to decode one token at context depth d.
func (t *Timing) DecodeSec(d int) float64 {
	bytes := t.WeightsBytesPerTok + t.Model.KVReadBytes(d, t.KVElem)
	return (bytes/t.EffBandwidth + t.T0) / t.DecodeMult / t.calibrationFactor("decode", d)
}

// DecodeTPS is decode tokens/s at depth d.
func (t *Timing) DecodeTPS(d int) float64 { return 1 / t.DecodeSec(d) }

// PrefillSec is seconds to prefill tokens [from, to).
func (t *Timing) PrefillSec(from, to int) float64 {
	if to <= from {
		return 0
	}
	const chunk = 512
	sec := 0.0
	for x := from; x < to; x += chunk {
		n := chunk
		if x+n > to {
			n = to - x
		}
		mid := x + n/2
		sec += float64(n) * (t.MatmulSecPerTok + t.Model.AttnFLOPs(mid)/t.AttnFLOPSEff) / t.calibrationFactor("prefill", mid)
	}
	return sec / t.PrefillMult
}

// PrefillTPS is the average prefill rate over [from, to).
func (t *Timing) PrefillTPS(from, to int) float64 {
	s := t.PrefillSec(from, to)
	if s <= 0 {
		return 0
	}
	return float64(to-from) / s
}

// Memory is the memory estimate.
type Memory struct {
	WeightsBytes  float64
	KVBytes       float64
	ComputeBytes  float64
	OverheadBytes float64
	TotalBytes    float64
	BudgetBytes   float64
	// Level: "fits" (≤90% of budget), "tight" (≤100%), "offload" (spills to
	// system RAM: much slower and not predictable), "no".
	Level string
	// MaxCtx is the largest context that fits at Level "fits" (≥ 0).
	MaxCtx int
	// Fix is a suggested remedy when it doesn't fit.
	Fix string
}

// Fit levels.
const (
	FitOK      = "fits"
	FitTight   = "tight"
	FitOffload = "offload"
	FitNo      = "no"
)

func computeBuffer(m catalog.Model, ctx int, fa bool) float64 {
	b := 0.25*gib + 0.012*gib*m.MatmulParamsB + m.RecurrentStateBytes
	if !fa {
		// Without flash attention the attention-score tensor of one
		// micro-batch (512) is materialised in f32.
		b += float64(ctx) * float64(m.QHeads) * 512 * 4
	}
	return b
}

func runtimeOverhead(backend string) float64 {
	switch backend {
	case "cuda", "rocm", "vulkan", "sycl":
		return 0.5 * gib
	}
	return 0.3 * gib
}

// EstimateMemory estimates resident memory for a model at ctx tokens.
func EstimateMemory(m catalog.Model, q catalog.Quant, ctx int, kvElem float64, fa bool, backend string, t Target) Memory {
	mem := Memory{
		WeightsBytes:  float64(q.FileBytes),
		KVBytes:       m.KVCacheBytes(ctx, kvElem),
		ComputeBytes:  computeBuffer(m, ctx, fa),
		OverheadBytes: runtimeOverhead(backend),
		BudgetBytes:   t.GPUMemBytes,
	}
	mem.TotalBytes = mem.WeightsBytes + mem.KVBytes + mem.ComputeBytes + mem.OverheadBytes
	switch {
	case mem.TotalBytes <= 0.9*mem.BudgetBytes:
		mem.Level = FitOK
	case mem.TotalBytes <= mem.BudgetBytes:
		mem.Level = FitTight
	case !t.Unified && !t.CPUOnly && mem.TotalBytes <= mem.BudgetBytes+0.85*t.RAMBytes-4*gib:
		mem.Level = FitOffload
	default:
		mem.Level = FitNo
	}
	// Largest context at "fits".
	fixed := mem.WeightsBytes + computeBuffer(m, 0, true) + mem.OverheadBytes
	perTok := m.KVBytesPerToken(kvElem)
	if perTok > 0 {
		mem.MaxCtx = int(math.Max(0, (0.9*mem.BudgetBytes-fixed)/perTok))
	}
	if mem.Level == FitNo && t.Unified && t.OS == "darwin" && mem.TotalBytes < t.RAMBytes-6e9 {
		mb := int(math.Ceil((mem.TotalBytes/0.9 + 0.5e9) / 1e6))
		mem.Fix = fmt.Sprintf("raise the macOS GPU memory limit: sudo sysctl iogpu.wired_limit_mb=%d (resets on reboot; leaves %.0f GB for everything else)", mb, (t.RAMBytes-float64(mb)*1e6)/gib)
	}
	return mem
}

// Accuracy is the expected error band of a prediction.
type Accuracy struct {
	DecodePct  int
	PrefillPct int
	// PrefillMeasureOnly is true where spec-based prefill is unreliable.
	PrefillMeasureOnly bool
	NoClaim            bool
	CalibrateAdvice    string
	Basis              string
}

// AccuracyFor returns the error band for a segment and conditions.
func AccuracyFor(d specs.Device, backend string, depth int, calibrated, offload bool) Accuracy {
	type band struct{ dec, decCal, pre, preCal int }
	b := band{35, 25, 50, 40}
	a := Accuracy{}
	switch d.Segment {
	case specs.AppleBase, specs.ApplePro, specs.AppleMax, specs.AppleUltra:
		b = band{15, 10, 15, 10}
		a.Basis = "Apple M1–M4 llama.cpp results (n≈230): decode median error 4–10%"
		if strings.Contains(strings.ToLower(d.Name), "m5") {
			b = band{25, 15, 30, 15}
			a.Basis = "Apple M5: new generation, little benchmark data"
		}
	case specs.NVDesktop:
		b = band{20, 10, 35, 25}
		a.Basis = "NVIDIA desktop results (n≈120): decode median error ≈11%, prompt processing ≈20% (varies with llama.cpp version)"
	case specs.NVLaptop:
		b = band{35, 25, 45, 40}
		a.Basis = "NVIDIA laptop GPUs vary with power limit; few results"
		a.CalibrateAdvice = "laptop GPU: run a calibration probe"
	case specs.AMDDiscrete:
		b = band{20, 15, 50, 35}
		a.Basis = "AMD discrete results (n≈44): decode is predictable; prompt processing depends on ROCm/Vulkan build settings"
		if backend == "rocm" {
			a.PrefillMeasureOnly = true
			b.pre = 60
		}
	case specs.AMDLaptop, specs.AMDiGPU, specs.InteliGPU:
		b = band{35, 25, 50, 40}
		a.Basis = "integrated GPUs depend on installed memory speed"
		a.CalibrateAdvice = "integrated GPU: run a calibration probe"
	case specs.StrixHalo:
		b = band{20, 15, 60, 50}
		a.PrefillMeasureOnly = true
		a.Basis = "Strix Halo decode is predictable; prefill varies 2–3× with driver/backend settings"
		a.CalibrateAdvice = "Strix Halo: prefill must be measured"
	case specs.IntelArc:
		b = band{40, 25, 70, 50}
		a.PrefillMeasureOnly = true
		a.Basis = "Intel Arc depends strongly on backend (SYCL vs Vulkan) and driver"
		a.CalibrateAdvice = "Intel Arc: run a calibration probe"
	case specs.NVUnified:
		b = band{25, 15, 30, 20}
	case specs.CPUApple, specs.CPUx86, specs.CPUOther:
		b = band{50, 25, 50, 50}
		a.Basis = "CPU-only depends on memory configuration and threads"
		a.CalibrateAdvice = "CPU-only: run `trialm detect --probe` to measure memory bandwidth"
	}
	if calibrated {
		a.DecodePct, a.PrefillPct = b.decCal, b.preCal
	} else {
		a.DecodePct, a.PrefillPct = b.dec, b.pre
	}
	if backend == "mlx" {
		a.DecodePct += 10
		a.PrefillPct += 10
		a.Basis += "; MLX timings extrapolated from Metal (little MLX data)"
	}
	if depth >= 16384 {
		a.DecodePct += 10
		a.PrefillPct += 15
	}
	if !d.Confident && a.CalibrateAdvice == "" {
		a.DecodePct += 10
		a.PrefillPct += 10
		a.CalibrateAdvice = "device spec partly assumed: a calibration probe would tighten this"
	}
	if offload {
		a.NoClaim = true
		a.Basis = "model spills out of GPU memory: speed with CPU offload is not predictable (2–7× errors)"
	}
	return a
}

// Scenario describes an agentic session for the headline numbers.
type Scenario struct {
	Harness string
	// BasePromptTokens is the harness's system prompt + tool definitions.
	BasePromptTokens int
	// NewTokensPerTurn is uncached input per follow-up turn (tool output +
	// the previous reply re-processed after reasoning is stripped).
	NewTokensPerTurn int
	// OutputTokensPerTurn is generated tokens per turn.
	OutputTokensPerTurn int
	// Turns is the number of model calls in the example task.
	Turns int
	// MidDepth is the context depth used for "typical follow-up".
	MidDepth int
	// MinContext is the smallest context the harness works in (0 = base
	// prompt + 8k); e.g. Codex recommends 64k.
	MinContext int
}

// DefaultScenario is a Claude Code-sized session.
func DefaultScenario() Scenario {
	return Scenario{Harness: "claude-code", BasePromptTokens: 12000, NewTokensPerTurn: 2500, OutputTokensPerTurn: 300, Turns: 40, MidDepth: 45000, MinContext: 36864}
}

// ScenarioResult are the headline numbers.
type ScenarioResult struct {
	ColdFirstReply  time.Duration // model not loaded, nothing cached
	FirstReply      time.Duration // model loaded, prompt not cached
	FollowUpTTFT    time.Duration // typical turn: new tokens only
	FollowUpTurn    time.Duration // TTFT + generating the reply
	Session         time.Duration // whole example task
	CloudSession    time.Duration // the same task at datacenter speed
	FitsInContext   bool
	ContextTokens   int
	FollowUpDecodeT float64 // tok/s at follow-up depth
	DecodeDepth     int     // approximate context depth during the follow-up reply
}

// Cloud reference speeds for the comparison line.
const (
	cloudTTFT = 1.0   // s
	cloudTPS  = 150.0 // tok/s
)

// RunScenario computes headline numbers for a scenario.
func RunScenario(t *Timing, sc Scenario, ctx int) ScenarioResult {
	r := ScenarioResult{ContextTokens: ctx, FitsInContext: ctx >= MinContext(sc)}
	p0 := sc.BasePromptTokens
	first := t.PrefillSec(0, p0) + t.DecodeSec(p0)
	r.FirstReply = dur(first)
	r.ColdFirstReply = dur(first + t.LoadColdS)
	d := sc.MidDepth
	if d > ctx {
		d = ctx
	}
	if d < p0 {
		d = p0
	}
	r.DecodeDepth = d + sc.NewTokensPerTurn + sc.OutputTokensPerTurn/2
	ttft := t.PrefillSec(d, d+sc.NewTokensPerTurn)
	r.FollowUpTTFT = dur(ttft)
	gen := 0.0
	for i := 0; i < sc.OutputTokensPerTurn; i++ {
		gen += t.DecodeSec(d + sc.NewTokensPerTurn + i)
	}
	r.FollowUpTurn = dur(ttft + gen)
	r.FollowUpDecodeT = float64(sc.OutputTokensPerTurn) / gen
	// Session: first turn cold, then turns-1 follow-ups with growing depth.
	sess := first + t.LoadColdS
	depth := p0
	for i := 1; i < sc.Turns; i++ {
		if depth+sc.NewTokensPerTurn+sc.OutputTokensPerTurn > ctx {
			depth = p0 + (depth-p0)/2 // harness compaction
		}
		sess += t.PrefillSec(depth, depth+sc.NewTokensPerTurn)
		for k := 0; k < sc.OutputTokensPerTurn; k += 50 {
			sess += 50 * t.DecodeSec(depth+sc.NewTokensPerTurn+k)
		}
		depth += sc.NewTokensPerTurn
	}
	r.Session = dur(sess)
	r.CloudSession = dur(float64(sc.Turns) * (cloudTTFT + float64(sc.OutputTokensPerTurn)/cloudTPS))
	return r
}

func dur(s float64) time.Duration { return time.Duration(s * float64(time.Second)) }

// Input is a prediction request.
type Input struct {
	Model       catalog.Model
	QuantName   string // "" = runtime/format default
	Runtime     runtime.Profile
	Target      Target
	Tuned       bool
	CtxOverride int
	KVCacheType string // "" = runtime default
	Scenario    Scenario
	Calibrated  bool
	// BackendOverride forces a backend (e.g. "vulkan" vs "rocm").
	BackendOverride string
}

// Result is a prediction.
type Result struct {
	Kind string // simulated or calibrated

	Model    catalog.Model
	Quant    catalog.Quant
	Runtime  runtime.Profile
	Backend  string
	Target   Target
	Settings runtime.Settings
	Memory   Memory
	Timing   *Timing
	Accuracy Accuracy
	Scenario ScenarioResult

	DecodeTPS0    float64 // decode at short context
	DecodeTPSDeep float64 // decode at the scenario's mid depth
	PrefillTPS2k  float64 // prefill of a 2k prompt
	PrefillTPSP0  float64 // average prefill over the harness base prompt

	Supported   bool
	Unsupported string
	Warnings    []string
	TuneSteps   []string
}

// Predict runs a prediction.
func Predict(in Input) Result {
	res := Result{Kind: "simulated", Model: in.Model, Runtime: in.Runtime, Target: in.Target}
	t := in.Target
	// Backend.
	backend := in.BackendOverride
	if backend == "" {
		b, ok := in.Runtime.ChooseBackend(t.OS, t.Arch, t.HWBackends)
		if !ok {
			res.Unsupported = fmt.Sprintf("%s does not run on %s/%s with this hardware", in.Runtime.Name, t.OS, t.Arch)
			return res
		}
		backend = b
	}
	if t.CPUOnly {
		backend = "cpu"
	}
	res.Backend = backend
	// Quant in the runtime's format.
	format := in.Runtime.Formats[0]
	var q catalog.Quant
	var err error
	if in.QuantName != "" {
		q, err = in.Model.Quant(in.QuantName)
		if err == nil && q.Format != format {
			if alt, ok := in.Model.QuantFor(format); ok {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s uses %s files; using %s instead of %s", in.Runtime.Name, strings.ToUpper(format), alt.Name, q.Name))
				q = alt
			} else {
				err = fmt.Errorf("no %s build of %s", format, in.Model.Name)
			}
		}
	} else {
		var ok bool
		q, ok = in.Model.QuantFor(format)
		if !ok {
			err = fmt.Errorf("no %s build of %s in the catalog", strings.ToUpper(format), in.Model.Name)
		}
	}
	if err != nil {
		res.Unsupported = err.Error()
		return res
	}
	res.Quant = q

	// Settings (needs max-fit context first).
	kv := in.KVCacheType
	if kv == "" {
		kv = in.Runtime.Defaults.KVCacheType
	}
	probe := EstimateMemory(in.Model, q, 4096, runtime.KVElemBytes(kv), true, backend, t)
	rec := RecommendedContext(in.Scenario, in.Model.NativeCtx, probe.MaxCtx)
	// Tuned: when memory limits the context below what the harness needs,
	// an 8-bit KV cache roughly doubles the context that fits.
	kvSwitched := false
	if in.Tuned && in.KVCacheType == "" && kv == "f16" && rec < MinContext(in.Scenario) {
		p8 := EstimateMemory(in.Model, q, 4096, runtime.KVElemBytes("q8_0"), true, backend, t)
		if r8 := RecommendedContext(in.Scenario, in.Model.NativeCtx, p8.MaxCtx); r8 > rec {
			probe, rec, kv, kvSwitched = p8, r8, "q8_0", true
		}
	}
	s := in.Runtime.Resolve(runtime.ResolveInput{
		GPUMemGiB: t.TierMemBytes() / gib, NativeCtx: in.Model.NativeCtx, MaxFitCtx: probe.MaxCtx,
		Tuned: in.Tuned, CtxOverride: in.CtxOverride, RecommendedCtx: rec,
	})
	if in.KVCacheType != "" || kvSwitched {
		s.KVCacheType = kv
	}
	res.Settings = s
	if in.Tuned {
		res.TuneSteps = in.Runtime.TunedSteps(s.ContextTokens)
		if kvSwitched {
			res.TuneSteps = append(res.TuneSteps, in.Runtime.KVQ8Step())
		}
	}
	elem := runtime.KVElemBytes(s.KVCacheType)
	res.Memory = EstimateMemory(in.Model, q, s.ContextTokens, elem, s.FlashAttention, backend, t)

	// Timing.
	de, ok := decodeTab[backend]
	if !ok {
		de = decodeTab["vulkan"]
	}
	pe := prefillTab[backend]
	if pe.dense == 0 {
		pe = prefillTab["vulkan"]
	}
	bw := t.Device.BandwidthGBs * 1e9
	eff := de.eff
	if backend == "cpu" && t.Device.Segment == specs.CPUx86 {
		eff = 0.6
		if strings.Contains(t.Device.Note, "measured") {
			eff = 0.85
		}
	}
	flops := t.Device.FP16TFLOPS * 1e12
	mm := pe.dense
	if in.Model.MoE {
		mm = pe.moe
	}
	tm := &Timing{
		Model:              in.Model,
		WeightsBytesPerTok: in.Model.ReadBytes(q),
		KVElem:             elem,
		EffBandwidth:       eff * bw,
		T0:                 de.t0ms / 1000,
		MatmulSecPerTok:    2 * in.Model.MatmulParamsB * 1e9 / (mm * flops),
		AttnFLOPSEff:       pe.attn * flops,
		DecodeMult:         nz(in.Runtime.Perf.DecodeMult) * nz(in.Model.DecodeEff),
		PrefillMult:        nz(in.Runtime.Perf.PrefillMult),
	}
	tm.LoadColdS = float64(q.FileBytes)/(t.DiskGBs*1e9) + in.Runtime.Perf.LoadOverheadS
	tm.LoadWarmS = float64(q.FileBytes)/(15e9) + in.Runtime.Perf.LoadOverheadS
	if res.Memory.Level == FitOffload {
		// Rough two-tier slowdown for display only; flagged as no-claim.
		frac := (res.Memory.TotalBytes - res.Memory.BudgetBytes) / res.Memory.TotalBytes
		tm.DecodeMult *= 1 / (1 + 6*frac)
		tm.PrefillMult *= 1 / (1 + 3*frac)
	}
	res.Timing = tm
	res.DecodeTPS0 = tm.DecodeTPS(512)
	res.PrefillTPS2k = tm.PrefillTPS(0, 2048)
	sc := in.Scenario
	if sc.BasePromptTokens == 0 {
		sc = DefaultScenario()
	}
	res.PrefillTPSP0 = tm.PrefillTPS(0, sc.BasePromptTokens)
	res.Scenario = RunScenario(tm, sc, s.ContextTokens)
	res.DecodeTPSDeep = res.Scenario.FollowUpDecodeT
	res.Accuracy = AccuracyFor(t.Device, backend, sc.MidDepth, in.Calibrated, res.Memory.Level == FitOffload)
	if in.Model.RecurrentLayers > 0 && !res.Accuracy.NoClaim {
		res.Accuracy.DecodePct += 15
		res.Accuracy.PrefillPct += 15
		res.Accuracy.Basis += "; hybrid linear-attention model: little benchmark data for its recurrent layers"
	}
	res.Supported = res.Memory.Level != FitNo

	// Warnings.
	if !res.Scenario.FitsInContext {
		var what string
		switch s.Overflow {
		case runtime.OverflowError:
			what = "requests will be rejected with a context error"
		case runtime.OverflowTruncateMiddle:
			what = "the middle of the conversation is silently cut"
		default:
			what = "the prompt is silently truncated and the agent will misbehave"
		}
		if sc.BasePromptTokens+sc.OutputTokensPerTurn < s.ContextTokens {
			res.Warnings = append(res.Warnings, fmt.Sprintf("the %d-token context (%s) leaves almost no room after %s's ~%dk-token prompt: the conversation overflows within a few turns",
				s.ContextTokens, s.ContextNote, HarnessTitle(sc.Harness), sc.BasePromptTokens/1000))
		} else {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s's ~%dk-token prompt does not fit the %d-token context (%s): %s",
				HarnessTitle(sc.Harness), sc.BasePromptTokens/1000, s.ContextTokens, s.ContextNote, what))
		}
	}
	if !in.Model.Tools && sc.Harness != "aider" {
		res.Warnings = append(res.Warnings, in.Model.Name+" has no native tool calling in local runtimes: coding agents that call tools will fail")
	}
	switch res.Memory.Level {
	case FitTight:
		res.Warnings = append(res.Warnings, "memory is tight: other apps or a longer context may push it over")
	case FitOffload:
		res.Warnings = append(res.Warnings, "does not fit in GPU memory: layers spill to system RAM, typically 3–10× slower (not predictable)")
	case FitNo:
		w := fmt.Sprintf("does not fit: needs %.1f GB, %.1f GB available", res.Memory.TotalBytes/gib, res.Memory.BudgetBytes/gib)
		if res.Memory.Fix != "" {
			w += "; " + res.Memory.Fix
		}
		res.Warnings = append(res.Warnings, w)
	}
	if in.Model.TrialStandIn != "" {
		res.Warnings = append(res.Warnings, fmt.Sprintf("trial stand-in: no cloud provider hosts this exact model, so trials use %s. Speed is simulated from this file exactly; the answers, including any fine-tuning or style, are the stand-in's", in.Model.TrialStandIn))
	} else if lowBit(q.Name) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("quality caveat: %s is a ~3-bit file chosen so this model fits and runs fast here; expect clearly more mistakes than the cloud's %s weights the trial uses (often a few points on coding benchmarks, more tool-call errors). A 4-bit file of the same model, or a smaller model at 4-bit, is the safer choice when quality matters more than speed", q.Name, in.Model.CloudPrecision))
	} else if !in.Model.QuantMatchesCloud {
		res.Warnings = append(res.Warnings, fmt.Sprintf("quality caveat: the trial uses the cloud's %s weights; local %s is typically 0–3 points worse on coding tasks and makes more tool-call mistakes", in.Model.CloudPrecision, q.Name))
	}
	applyCalibration(&res, in)
	return res
}

// lowBit reports quants below ~4 bits per weight (Q3_*, IQ3_*, Q2_*, IQ2_*).
func lowBit(name string) bool {
	n := strings.ToUpper(name)
	return strings.HasPrefix(n, "Q3") || strings.HasPrefix(n, "IQ3") || strings.HasPrefix(n, "Q2") || strings.HasPrefix(n, "IQ2") || strings.Contains(n, "3BIT")
}

func nz(v float64) float64 {
	if v <= 0 {
		return 1
	}
	return v
}

// MinContext is the smallest context in which the harness can work: its
// base prompt plus room for a few turns of conversation.
func MinContext(sc Scenario) int {
	if sc.BasePromptTokens == 0 {
		sc = DefaultScenario()
	}
	if sc.MinContext > 0 {
		return sc.MinContext
	}
	return sc.BasePromptTokens + 8192
}

// RecommendedContext returns a context size suited to the harness: room for
// the base prompt plus a working conversation, capped by the model and by
// memory (maxFit; 0 = unknown).
func RecommendedContext(sc Scenario, native, maxFit int) int {
	if sc.BasePromptTokens == 0 {
		sc = DefaultScenario()
	}
	want := sc.BasePromptTokens*2 + 16384
	if m := MinContext(sc); m > want {
		want = m
	}
	ctx := ((want + 8191) / 8192) * 8192
	if ctx < 32768 {
		ctx = 32768
	}
	if native > 0 && ctx > native {
		ctx = native
	}
	if maxFit > 0 && ctx > maxFit {
		ctx = (maxFit / 4096) * 4096
		if ctx < 4096 {
			ctx = 4096
		}
	}
	return ctx
}

// HarnessTitle returns a display name for a harness id.
func HarnessTitle(id string) string {
	switch id {
	case "claude-code":
		return "Claude Code"
	case "codex":
		return "Codex CLI"
	case "opencode":
		return "OpenCode"
	case "cline":
		return "Cline"
	case "aider":
		return "Aider"
	case "continue":
		return "Continue"
	case "zed":
		return "Zed"
	case "":
		return "The harness"
	}
	return id
}
