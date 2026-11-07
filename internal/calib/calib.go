// Package calib owns optional, local synthetic-GGUF machine calibration.
package calib

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/hw"
)

// FindEngine never installs software or downloads models.
func FindEngine(home string) (string, error) {
	if p, e := exec.LookPath("llama-bench"); e == nil {
		return p, nil
	}
	for _, p := range []string{"/opt/homebrew/bin/llama-bench", "/usr/local/bin/llama-bench"} {
		if executable(p) {
			return p, nil
		}
	}
	found := ""
	_ = filepath.WalkDir(filepath.Join(home, "engines"), func(p string, d fs.DirEntry, e error) error {
		if e != nil {
			return nil
		}
		if !d.IsDir() && (d.Name() == "llama-bench" || d.Name() == "llama-bench.exe") && executable(p) {
			found = p
			return fs.SkipAll
		}
		return nil
	})
	if found != "" {
		return found, nil
	}
	return "", errors.New("llama-bench was not found in PATH, Homebrew or $TRIALM_HOME/engines. Install llama.cpp (brew install llama.cpp on macOS). P2 setup will install a pinned llama.cpp release")
}

func executable(p string) bool {
	st, e := os.Stat(p)
	return e == nil && st.Mode().IsRegular() && (st.Mode()&0111 != 0 || runtime.GOOS == "windows" && strings.HasSuffix(strings.ToLower(p), ".exe"))
}

type Fingerprint struct {
	Chip  string   `json:"chip"`
	GPUs  []string `json:"gpus"`
	RAM   uint64   `json:"ram_bytes"`
	OS    string   `json:"os"`
	Build string   `json:"llama_cpp_build"`
	Hash  string   `json:"hash"`
}

func MachineFingerprint(ctx context.Context, engine string) (Fingerprint, error) {
	sys, e := hw.Detect(ctx)
	if e != nil {
		return Fingerprint{}, e
	}
	out, e := exec.CommandContext(ctx, engine, "--version").CombinedOutput()
	if e != nil {
		return Fingerprint{}, fmt.Errorf("llama-bench --version: %w", e)
	}
	var build []string
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "version:") || strings.HasPrefix(l, "built with") {
			build = append(build, l)
		}
	}
	if len(build) == 0 {
		return Fingerprint{}, errors.New("llama-bench did not report a build")
	}
	return fingerprintForSystem(sys, strings.Join(build, "\n"))
}

func fingerprintForSystem(sys *hw.System, build string) (Fingerprint, error) {
	if sys.RAMBytes == 0 {
		return Fingerprint{}, errors.New("cannot fingerprint this machine: RAM detection is unavailable. Run trialm calibrate in a normal terminal")
	}
	chip := sys.CPUName
	if chip == "" {
		chip = sys.AppleChip
	}
	if chip == "" && len(sys.GPUs) > 0 {
		chip = sys.GPUs[0].Name
	}
	f := Fingerprint{Chip: chip, RAM: sys.RAMBytes, OS: sys.OS + "/" + sys.Arch + "/" + sys.OSVersion, Build: build}
	for _, g := range sys.GPUs {
		f.GPUs = append(f.GPUs, fmt.Sprintf("%s/%s/%d/%s", g.Vendor, g.Name, g.Cores, g.Driver))
	}
	sort.Strings(f.GPUs)
	b, _ := json.Marshal(f)
	h := sha256.Sum256(b)
	f.Hash = hex.EncodeToString(h[:])
	return f, nil
}

// Row retains the engine's unmodified JSON as well as the predictor baseline.
type Row struct {
	LoadMode    string          `json:"load_mode,omitempty"`
	Spread      float64         `json:"repetition_spread_seconds,omitempty"`
	Repetitions int             `json:"repetitions,omitempty"`
	Probe       string          `json:"probe"`
	Class       string          `json:"family_class"`
	Quant       string          `json:"quant_class"`
	Layers      int             `json:"layers"`
	Check       bool            `json:"check"`
	Depth       int             `json:"depth"`
	Prompt      int             `json:"prompt"`
	Gen         int             `json:"gen"`
	Seconds     float64         `json:"seconds_per_token"`
	Baseline    float64         `json:"baseline_seconds_per_token"`
	Raw         json.RawMessage `json:"raw"`
}

// FitPoint stores uncertainty in seconds, before extrapolation.
type FitPoint struct {
	Layers int     `json:"layers"`
	Spread float64 `json:"spread_seconds"`
	Error  float64 `json:"error_seconds,omitempty"`
}

const FittingVersion = 2

type Fit struct {
	DepthLayerError float64    `json:"depth_layer_error_seconds,omitempty"`
	DepthSource     string     `json:"depth_source,omitempty"`
	Noise           []FitPoint `json:"training_spread,omitempty"`
	Checks          []FitPoint `json:"held_out,omitempty"`
	Rejected        string     `json:"rejected,omitempty"`

	MeanLayers   float64   `json:"mean_layers"`
	LayerSS      float64   `json:"layer_sum_squares"`
	TrainSamples int       `json:"train_samples"`
	Class        string    `json:"family_class"`
	Quant        string    `json:"quant_class"`
	Depth        int       `json:"depth_bucket"`
	Mode         string    `json:"mode"`
	Fixed        float64   `json:"fixed_seconds"`
	PerLayer     float64   `json:"layer_seconds"`
	BaseFixed    float64   `json:"base_fixed_seconds"`
	BaseLayer    float64   `json:"base_layer_seconds"`
	Multiplier   float64   `json:"multiplier"`
	BandPct      int       `json:"band_pct"`
	Residuals    []float64 `json:"relative_residuals"`
	Samples      int       `json:"samples"`
}

type Calibration struct {
	FittingVersion int         `json:"fitting_version"`
	Backend        string      `json:"backend"`
	Version        int         `json:"version"`
	Fingerprint    Fingerprint `json:"fingerprint"`
	Started        time.Time   `json:"started"`
	WallSeconds    float64     `json:"wall_seconds"`
	Complete       bool        `json:"complete"`
	Quick          bool        `json:"quick"`
	Rows           []Row       `json:"rows"`
	Fits           []Fit       `json:"fits"`
	Coverage       []string    `json:"coverage"`
	Notes          []string    `json:"notes"`
}

func Load(home string) (*Calibration, error) {
	b, e := os.ReadFile(filepath.Join(home, "calib.json"))
	if e != nil {
		return nil, e
	}
	var c Calibration
	e = json.Unmarshal(b, &c)
	if e != nil {
		return nil, e
	}
	if c.Version != GeneratorVersion {
		return nil, errors.New("calibration generator version changed")
	}
	if c.FittingVersion != FittingVersion {
		c.FitRows()
	}
	return &c, nil
}
func (c *Calibration) Save(home string) error {
	if e := os.MkdirAll(home, 0700); e != nil {
		return e
	}
	b, e := json.MarshalIndent(c, "", "  ")
	if e != nil {
		return e
	}
	f, e := os.CreateTemp(home, ".calib-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		_, e = f.Write(b)
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(home, "calib.json"))
}

func DepthBucket(d int) int {
	if d >= 16384 {
		return 32768
	}
	if d >= 4096 {
		return 8192
	}
	return 0
}
func mode(r Row) string {
	if r.Gen > 0 {
		return "decode"
	}
	if r.Prompt <= 4 {
		return fmt.Sprintf("verify%d", r.Prompt)
	}
	return "prefill"
}
func fitKey(class, q string, d int, m string) string {
	return fmt.Sprintf("%s/%s/%d/%s", class, q, d, m)
}

// FitRows rebuilds fits from raw rows without hardware or engine access.
// Repetitions count once: each probe/depth/operation contributes its median.
func (c *Calibration) FitRows() {
	groups := map[string][]Row{}
	for _, r := range observations(c.Rows) {
		k := fitKey(r.Class, r.Quant, DepthBucket(r.Depth), mode(r))
		groups[k] = append(groups[k], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c.FittingVersion = FittingVersion
	for i, n := range c.Notes {
		if strings.Contains(n, "75%") {
			c.Notes[i] = "Bands combine held-out layer error and repetition spread, extrapolated to real layers; real-model coverage is not validated."
		}
	}
	c.Fits, c.Coverage = nil, nil
	for _, k := range keys {
		rs := groups[k]
		var train []Row
		for _, r := range rs {
			if !r.Check {
				train = append(train, r)
			}
		}
		a, b, ok := line(train, false)
		ba, bb, bok := line(train, true)
		if !ok || !bok || b <= 0 || bb <= 0 {
			continue
		}
		f := Fit{Class: rs[0].Class, Quant: rs[0].Quant, Depth: DepthBucket(rs[0].Depth), Mode: mode(rs[0]), Fixed: a, PerLayer: b, BaseFixed: ba, BaseLayer: bb, Samples: len(rs), TrainSamples: len(train), BandPct: -1}
		for _, r := range train {
			f.MeanLayers += float64(r.Layers)
		}
		f.MeanLayers /= float64(len(train))
		for _, r := range train {
			f.LayerSS += math.Pow(float64(r.Layers)-f.MeanLayers, 2)
			f.Noise = append(f.Noise, FitPoint{Layers: r.Layers, Spread: r.Spread})
			if r.Repetitions < 2 {
				f.Rejected = "repetition spread unavailable"
			}
		}
		for _, r := range rs {
			pred := a + b*float64(r.Layers)
			residual := r.Seconds/pred - 1
			f.Residuals = append(f.Residuals, residual)
			if r.Check {
				f.Checks = append(f.Checks, FitPoint{Layers: r.Layers, Spread: r.Spread, Error: math.Abs(r.Seconds - pred)})
				if math.Abs(residual) > .35 {
					f.Rejected = "held-out error exceeds 35%"
				}
				if r.Repetitions < 2 {
					f.Rejected = "held-out repetition spread unavailable"
				}
			}
		}
		if len(f.Checks) == 0 {
			f.Rejected = "no held-out layer check"
		}
		layers := realLayers(f.Class)
		if f.Rejected == "" {
			f.BandPct = f.band(layers)
		}
		f.Multiplier = (ba + bb*float64(layers)) / (a + b*float64(layers))
		c.Fits = append(c.Fits, f)
	}
	c.fitDepths(groups)
	// Coverage means a prediction can actually use both operations at all
	// timing buckets, rather than merely having a verify or depth-zero fit.
	for _, f := range c.Fits {
		coverage := f.Class + "/" + f.Quant
		if f.Depth == 0 && f.Mode == "decode" && !contains(c.Coverage, coverage) && c.hasTimings(f.Class, f.Quant, realLayers(f.Class)) {
			c.Coverage = append(c.Coverage, coverage)
		}
	}
}
func (c *Calibration) hasTimings(class, q string, layers int) bool {
	for _, d := range []int{0, 8192, 32768} {
		for _, m := range []string{"decode", "prefill"} {
			if _, _, ok := c.Factor(class, q, d, layers, m); !ok {
				return false
			}
		}
	}
	return true
}

// Usable reports whether at least one measured family/quant has full timing
// coverage. Raw completion alone does not imply a usable calibration.
func (c *Calibration) Usable() bool { return len(c.Coverage) > 0 }

// A single deep reference measures the incremental context cost per layer.
// Its depth-zero held-out check still governs acceptance. This transfer is
// explicit and carries extra uncertainty; it is not an independent deep check.
func (c *Calibration) fitDepths(groups map[string][]Row) {
	bases := append([]Fit(nil), c.Fits...)
	for _, f := range bases {
		if f.Depth != 0 || f.Rejected != "" || strings.HasPrefix(f.Mode, "verify") {
			continue
		}
		reference := referenceQuant(f.Class)
		referenceOK := false
		for _, base := range bases {
			if base.Class == f.Class && base.Quant == reference && base.Depth == 0 && base.Mode == f.Mode && base.Rejected == "" {
				referenceOK = true
				break
			}
		}
		if !referenceOK {
			continue
		}
		for _, d := range []int{8192, 32768} {
			// Preserve explicit deep evidence, including failed checks.
			if f.Quant != reference && len(groups[fitKey(f.Class, f.Quant, d, f.Mode)]) > 0 {
				continue
			}
			rs := groups[fitKey(f.Class, reference, d, f.Mode)]
			if len(rs) != 1 || rs[0].Check {
				continue
			}
			r := rs[0]
			var zero Row
			for _, z := range groups[fitKey(f.Class, reference, 0, f.Mode)] {
				if z.Probe == r.Probe && z.Layers == r.Layers {
					zero = z
					break
				}
			}
			if zero.Seconds <= 0 || r.Repetitions < 2 {
				continue
			}
			delta := r.Seconds - zero.Seconds
			// A negative context cost is a noise diagnostic, not a speedup.
			if delta < 0 {
				continue
			}
			deep := f
			deep.Depth = d
			deep.PerLayer += delta / float64(r.Layers)
			deep.BaseLayer += (r.Baseline - zero.Baseline) / float64(r.Layers)
			if deep.BaseLayer <= 0 {
				continue
			}
			deep.DepthLayerError = (r.Spread + zero.Spread + .15*delta) / float64(r.Layers)
			deep.DepthSource = r.Probe
			deep.BandPct = deep.band(realLayers(f.Class))
			deep.Multiplier = (deep.BaseFixed + deep.BaseLayer*float64(realLayers(f.Class))) / (deep.Fixed + deep.PerLayer*float64(realLayers(f.Class)))
			c.Fits = append(c.Fits, deep)
		}
	}
}

func realLayers(class string) int {
	for _, s := range Shapes {
		if s.Class == class {
			return s.RealLayers
		}
	}
	return 32
}

// observations accepts both the legacy one-row-per-repetition store and new
// median rows. Raw engine JSON remains the source of the repetition spread.
func observations(rows []Row) []Row {
	groups := map[string][]Row{}
	for _, r := range rows {
		if r.Layers <= 0 || r.Seconds <= 0 || r.Baseline <= 0 || !finite(r.Seconds) || !finite(r.Baseline) {
			continue
		}
		key := fmt.Sprintf("%s/%s/%s/%d/%t/%d/%s", r.Probe, r.Class, r.Quant, r.Layers, r.Check, r.Depth, mode(r))
		groups[key] = append(groups[key], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Row
	for _, k := range keys {
		rs := groups[k]
		r := rs[0]
		var samples, bases []float64
		for _, v := range rs {
			samples = append(samples, v.Seconds)
			bases = append(bases, v.Baseline)
		}
		// New rows already contain the median; recover repetitions from raw once.
		if len(rs) == 1 && len(r.Raw) > 0 {
			var b BenchRow
			if json.Unmarshal(r.Raw, &b) == nil && len(b.Samples) > 0 && b.Prompt+b.Gen > 0 {
				samples = nil
				for _, ns := range b.Samples {
					if ns > 0 && finite(ns) {
						samples = append(samples, ns/1e9/float64(b.Prompt+b.Gen))
					}
				}
			}
		}
		if len(samples) == 0 {
			continue
		}
		r.Seconds = median(samples)
		r.Baseline = median(bases)
		r.Spread = spread(samples)
		r.Repetitions = len(samples)
		out = append(out, r)
	}
	return out
}
func median(v []float64) float64 {
	v = append([]float64(nil), v...)
	sort.Float64s(v)
	n := len(v)
	if n == 0 {
		return 0
	}
	return v[(n-1)/2] + (v[n/2]-v[(n-1)/2])/2
}

// MAD retains robustness to an isolated slow repetition; two repetitions have
// no majority and therefore retain their full half-range as uncertainty.
func spread(v []float64) float64 {
	m := median(v)
	dev := make([]float64, len(v))
	for i, x := range v {
		dev[i] = math.Abs(x - m)
	}
	return 1.4826 * median(dev)
}

func line(rs []Row, base bool) (float64, float64, bool) {
	var n, x, y, xx, xy float64
	for _, r := range rs {
		if r.Check {
			continue
		}
		v := r.Seconds
		if base {
			v = r.Baseline
		}
		l := float64(r.Layers)
		n++
		x += l
		y += v
		xx += l * l
		xy += l * v
	}
	den := n*xx - x*x
	if den <= 0 {
		return 0, 0, false
	}
	b := (n*xy - x*y) / den
	a := (y - b*x) / n
	// Nonnegative least squares: a physical fixed cost cannot be negative.
	// Prefill batching often puts the unconstrained optimum below zero.
	if a < 0 {
		a = 0
		b = xy / xx
	}
	return a, b, finite(a) && finite(b)
}
func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
func contains(ss []string, s string) bool {
	for _, v := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// Factor returns the speed multiplier and a residual-derived band. An absent
// fit or an unidentifiable band leaves the original prediction in place.
func (c *Calibration) directFactor(class, q string, depth, layers int, m string) (float64, int, bool) {
	for _, f := range c.Fits {
		if f.Class == class && f.Quant == q && f.Depth == DepthBucket(depth) && f.Mode == m && f.BandPct >= 0 && f.Rejected == "" {
			den := f.Fixed + f.PerLayer*float64(layers)
			num := f.BaseFixed + f.BaseLayer*float64(layers)
			if den > 0 && num > 0 {
				return num / den, f.band(layers), true
			}
		}
	}
	return 1, 0, false
}

// Factor composes the measured dense quant-kernel ratio with an architecture
// fit when that exact architecture/quant pair was not in the time-limited grid.
// Adding log-error radii is conservative without assuming independent errors.
func (c *Calibration) Factor(class, q string, depth, layers int, m string) (float64, int, bool) {
	// An explicit failed check cannot be bypassed by composing other fits.
	for _, f := range c.Fits {
		if f.Class == class && f.Quant == q && f.Depth == DepthBucket(depth) && f.Mode == m && f.Rejected != "" {
			return 1, 0, false
		}
	}
	if v, b, ok := c.directFactor(class, q, depth, layers, m); ok {
		return v, b, true
	}
	if DepthBucket(depth) != 0 && !strings.HasPrefix(m, "verify") {
		reference := referenceQuant(class)
		shallow, sb, sok := c.Factor(class, q, 0, layers, m)
		deep, db, dok := c.directFactor(class, reference, depth, layers, m)
		base, bb, bok := c.directFactor(class, reference, 0, layers, m)
		if sok && dok && bok {
			return shallow * deep / base, composeBands(sb, db, bb), true
		}
		return 1, 0, false
	}
	if class == "dense" || class == "mla" || strings.HasPrefix(m, "verify") {
		return 1, 0, false
	}
	reference := referenceQuant(class)
	if reference == "" {
		return 1, 0, false
	}
	architecture, ab, aok := c.directFactor(class, reference, depth, layers, m)
	kernel, kb, kok := c.directFactor("dense", q, depth, 36, m)
	base, bb, bok := c.directFactor("dense", reference, depth, 36, m)
	if !aok || !kok || !bok {
		return 1, 0, false
	}
	return architecture * kernel / base, composeBands(ab, kb, bb), true
}

func referenceQuant(class string) string {
	return map[string]string{"dense": "Q4_K_M", "hybrid": "IQ3_XXS", "sliding": "MXFP4", "moe": "Q4_K_M", "gemma": "Q4_K_M"}[class]
}
func composeBands(bands ...int) int {
	radius := 0.
	for _, b := range bands {
		radius += math.Log1p(float64(b) / 100)
	}
	return int(math.Ceil(100 * math.Expm1(radius)))
}

// band is a sensitivity envelope, not a claimed statistical confidence level.
// Check error is treated as an unknown layer cost and carried to full depth.
func (f Fit) band(layers int) int {
	if len(f.Checks) == 0 {
		return f.BandPct
	}
	pred := f.Fixed + f.PerLayer*float64(layers)
	if pred <= 0 || f.LayerSS <= 0 {
		return -1
	}
	noise := float64(layers) * f.DepthLayerError
	for _, p := range f.Noise {
		weight := 1/float64(f.TrainSamples) + (float64(layers)-f.MeanLayers)*(float64(p.Layers)-f.MeanLayers)/f.LayerSS
		weight = math.Abs(weight)
		if f.Fixed == 0 {
			// On the nonnegative boundary, also cover the origin-constrained fit's
			// sensitivity, which differs from an unconstrained affine regression.
			originWeight := float64(layers) * float64(p.Layers) / (f.LayerSS + float64(f.TrainSamples)*f.MeanLayers*f.MeanLayers)
			weight = math.Max(weight, math.Abs(originWeight))
		}
		noise += weight * p.Spread
	}
	check := 0.
	for _, p := range f.Checks {
		check = math.Max(check, (p.Error+p.Spread)*math.Max(1, float64(layers)/float64(p.Layers)))
	}
	return int(math.Ceil(100 * (.10 + (noise+check)/pred)))
}
