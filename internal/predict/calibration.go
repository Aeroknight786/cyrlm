package predict

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/paths"
)

type timingCalibration struct{ decode, prefill [3]float64 }

// Resolve the fitted multipliers once, before the scenario's per-token loop.
func (t *Timing) calibrationFactor(mode string, depth int) float64 {
	if t.calibration == nil {
		return 1
	}
	i := 0
	switch calib.DepthBucket(depth) {
	case 8192:
		i = 1
	case 32768:
		i = 2
	}
	if mode == "decode" {
		return t.calibration.decode[i]
	}
	return t.calibration.prefill[i]
}

// WithCalibration predicts using supplied local evidence. The caller must verify
// the machine/build fingerprint first; this also supports offline validation.
func WithCalibration(in Input, c *calib.Calibration) Result {
	baseline := in
	baseline.Target.Synthetic = true // bypass automatic machine evidence lookup
	r := Predict(baseline)
	r.Target = in.Target
	applyMachineCalibration(&r, in, c)
	return r
}

// Cache by home, calibration mtime and engine mtime. Fingerprint checks run
// once per process/cache change, rather than once per catalog prediction.
var calibrationCache struct {
	sync.Mutex
	key  string
	data *calib.Calibration
}

func machineCalibration() *calib.Calibration {
	home, e := paths.Home()
	if e != nil {
		return nil
	}
	st, e := os.Stat(filepath.Join(home, "calib.json"))
	if e != nil {
		return nil
	}
	engine, e := calib.FindEngine(home)
	if e != nil {
		return nil
	}
	es, e := os.Stat(engine)
	if e != nil {
		return nil
	}
	key := fmt.Sprintf("%s/%d/%d/%s/%d", home, st.ModTime().UnixNano(), st.Size(), engine, es.ModTime().UnixNano())
	calibrationCache.Lock()
	defer calibrationCache.Unlock()
	if calibrationCache.key == key {
		return calibrationCache.data
	}
	calibrationCache.key = key
	calibrationCache.data = nil
	c, e := calib.Load(home)
	if e != nil || !c.Usable() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	f, e := calib.MachineFingerprint(ctx, engine)
	if e != nil || c.Fingerprint.Hash != f.Hash {
		return nil
	}
	calibrationCache.data = c
	return c
}
func calibrationClass(m catalog.Model) string {
	if strings.HasPrefix(m.Family, "deepseek") || strings.HasPrefix(m.Family, "glm4moe") {
		return "mla"
	}
	if m.Family == "gemma4" {
		return "gemma"
	}
	if m.RecurrentLayers > 0 {
		return "hybrid"
	}
	if m.SWALayers > 0 {
		return "sliding"
	}
	if m.MoE {
		return "moe"
	}
	return "dense"
}
func calibrationQuant(q string) string {
	q = strings.TrimPrefix(strings.ToUpper(q), "UD-")
	switch q {
	case "Q4_K", "Q4_K_S":
		return "Q4_K_M"
	case "MXFP4_MOE":
		return "MXFP4"
	}
	return q
}

// applyCalibration is the sole integration hook. Unsupported kernels, spilled
// models and --device targets keep the original timings and bands.
func applyCalibration(r *Result, in Input) {
	r.Kind = "simulated"
	if in.Runtime.Engine == "mlx" {
		r.Warnings = append(r.Warnings, "MLX is not calibrated by llama-bench; these timings remain simulated")
	}
	if !calibrationAllowed(r, in) {
		return
	}
	applyMachineCalibration(r, in, machineCalibration())
}

func calibrationAllowed(r *Result, in Input) bool {
	return !in.Target.Synthetic && !in.Target.CPUOnly && in.Runtime.Engine == "llamacpp" && r.Timing != nil && r.Memory.Level != FitOffload && r.Memory.Level != FitNo
}
func applyMachineCalibration(r *Result, in Input, c *calib.Calibration) {
	if !calibrationAllowed(r, in) || c == nil || c.Backend != r.Backend {
		return
	}
	class, q := calibrationClass(in.Model), calibrationQuant(r.Quant.Name)
	sc := in.Scenario
	if sc.BasePromptTokens == 0 {
		sc = DefaultScenario()
	}
	factors := &timingCalibration{}
	var db, pb int
	var paths []string
	for i, depth := range []int{0, 8192, 32768} {
		// Timing can cross every bucket. Report the widest used band for each
		// operation so every headline and validation depth has honest coverage.
		for _, mode := range []string{"decode", "prefill"} {
			f, ok := c.FactorEvidence(class, q, depth, in.Model.Layers, mode)
			if !ok {
				return
			}
			band := f.BandPct
			if f.Fallback {
				prior := r.Accuracy.DecodePct
				if mode == "prefill" {
					prior = r.Accuracy.PrefillPct
				}
				if band < prior {
					band = prior
				}
			}
			if mode == "decode" {
				factors.decode[i] = f.Multiplier
				if band > db {
					db = band
				}
			} else {
				factors.prefill[i] = f.Multiplier
				if band > pb {
					pb = band
				}
			}
			paths = append(paths, mode+": "+f.Basis)
		}
	}
	r.Timing.calibration = factors
	r.Kind = "calibrated"
	r.Accuracy.DecodePct = db
	r.Accuracy.PrefillPct = pb
	r.Accuracy.PrefillMeasureOnly = false
	r.Accuracy.Basis = "calibrated on this machine; sensitivity band from synthetic checks and transfer allowances; context costs transfer per layer; real-model coverage is not established; " + strings.Join(paths, "; ")
	r.DecodeTPS0 = r.Timing.DecodeTPS(512)
	r.PrefillTPS2k = r.Timing.PrefillTPS(0, 2048)
	r.PrefillTPSP0 = r.Timing.PrefillTPS(0, sc.BasePromptTokens)
	r.Scenario = RunScenario(r.Timing, sc, r.Settings.ContextTokens)
	r.DecodeTPSDeep = r.Scenario.FollowUpDecodeT
	r.Timing.VerifySeconds = map[int]float64{}
	for _, f := range c.Fits {
		if f.Class == class && f.Quant == q && f.Depth == 0 && f.Rejected == "" && f.BandPct >= 0 && strings.HasPrefix(f.Mode, "verify") {
			n := 0
			_, _ = fmt.Sscanf(f.Mode, "verify%d", &n)
			if n > 0 {
				r.Timing.VerifySeconds[n] = float64(n) * (f.Fixed + f.PerLayer*float64(in.Model.Layers))
			}
		}
	}
	if in.Runtime.ID != "llamacpp" {
		r.Warnings = append(r.Warnings, "calibrated using llama-bench; assumes this runtime's llama.cpp engine uses matching kernels and settings")
	}
}
