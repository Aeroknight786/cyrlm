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
	Probe    string          `json:"probe"`
	Class    string          `json:"family_class"`
	Quant    string          `json:"quant_class"`
	Layers   int             `json:"layers"`
	Check    bool            `json:"check"`
	Depth    int             `json:"depth"`
	Prompt   int             `json:"prompt"`
	Gen      int             `json:"gen"`
	Seconds  float64         `json:"seconds_per_token"`
	Baseline float64         `json:"baseline_seconds_per_token"`
	Raw      json.RawMessage `json:"raw"`
}

type Fit struct {
	LogSigma     float64   `json:"log_residual_sigma"`
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
	Backend     string      `json:"backend"`
	Version     int         `json:"version"`
	Fingerprint Fingerprint `json:"fingerprint"`
	Started     time.Time   `json:"started"`
	WallSeconds float64     `json:"wall_seconds"`
	Complete    bool        `json:"complete"`
	Quick       bool        `json:"quick"`
	Rows        []Row       `json:"rows"`
	Fits        []Fit       `json:"fits"`
	Coverage    []string    `json:"coverage"`
	Notes       []string    `json:"notes"`
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

// FitRows fits t(L)=a+bL on two layer counts, holding cross-check layer counts
// out. Relative residuals include independent engine repetitions and checks.
// The band uses the RMS log residual (n-2 degrees of freedom) times
// 1.15 (the central 75% normal interval) and regression prediction leverage:
// a roughly 75% log-error interval conditional on these synthetic shapes.
// It is a fit diagnostic, not a real-model validation interval.
func (c *Calibration) FitRows() {
	groups := map[string][]Row{}
	for _, r := range c.Rows {
		if r.Seconds <= 0 || r.Baseline <= 0 || !finite(r.Seconds) || !finite(r.Baseline) {
			continue
		}
		k := fitKey(r.Class, r.Quant, DepthBucket(r.Depth), mode(r))
		groups[k] = append(groups[k], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	c.Fits = nil
	c.Coverage = nil
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
		if !ok || !bok || a < 0 || b <= 0 || ba < 0 || bb <= 0 {
			continue
		}
		f := Fit{Class: rs[0].Class, Quant: rs[0].Quant, Depth: DepthBucket(rs[0].Depth), Mode: mode(rs[0]), Fixed: a, PerLayer: b, BaseFixed: ba, BaseLayer: bb, Samples: len(rs)}
		sum := 0.0
		for _, r := range rs {
			pred := a + b*float64(r.Layers)
			v := math.Log(r.Seconds / pred)
			sum += v * v
			f.Residuals = append(f.Residuals, math.Expm1(v))
		}
		f.TrainSamples = len(train)
		for _, r := range train {
			f.MeanLayers += float64(r.Layers)
		}
		f.MeanLayers /= float64(len(train))
		for _, r := range train {
			f.LayerSS += math.Pow(float64(r.Layers)-f.MeanLayers, 2)
		}
		if len(rs) > 2 {
			f.LogSigma = math.Sqrt(sum / float64(len(rs)-2))
		} else {
			f.BandPct = -1
		} // two points cannot estimate a residual band
		layers := 32
		for _, s := range Shapes {
			if s.Class == f.Class {
				layers = s.RealLayers
				break
			}
		}
		if len(rs) > 2 {
			f.BandPct = f.band(layers)
		}
		f.Multiplier = (ba + bb*float64(layers)) / (a + b*float64(layers))
		c.Fits = append(c.Fits, f)
		coverage := f.Class + "/" + f.Quant
		if !contains(c.Coverage, coverage) {
			c.Coverage = append(c.Coverage, coverage)
		}
	}
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
	return a, b, true
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
		if f.Class == class && f.Quant == q && f.Depth == DepthBucket(depth) && f.Mode == m && f.BandPct >= 0 {
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
	if v, b, ok := c.directFactor(class, q, depth, layers, m); ok {
		return v, b, true
	}
	if class == "dense" || class == "mla" || strings.HasPrefix(m, "verify") {
		return 1, 0, false
	}
	reference := map[string]string{"hybrid": "IQ3_XXS", "sliding": "MXFP4", "moe": "Q4_K_M", "gemma": "Q4_K_M"}[class]
	if reference == "" {
		return 1, 0, false
	}
	architecture, ab, aok := c.directFactor(class, reference, depth, layers, m)
	kernel, kb, kok := c.directFactor("dense", q, depth, 36, m)
	base, bb, bok := c.directFactor("dense", reference, depth, 36, m)
	if !aok || !kok || !bok {
		return 1, 0, false
	}
	radius := math.Log1p(float64(ab)/100) + math.Log1p(float64(kb)/100) + math.Log1p(float64(bb)/100)
	return architecture * kernel / base, int(math.Ceil(100 * math.Expm1(radius))), true
}

// Include extrapolation leverage: two shallow layer counts give less evidence
// about a much deeper model than about another shallow probe.
func (f Fit) band(layers int) int {
	if f.TrainSamples < 3 || f.LayerSS <= 0 {
		return f.BandPct
	}
	leverage := math.Sqrt(1 + 1/float64(f.TrainSamples) + math.Pow(float64(layers)-f.MeanLayers, 2)/f.LayerSS)
	return int(math.Ceil(100 * math.Expm1(1.15*f.LogSigma*leverage)))
}
