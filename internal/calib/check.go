package calib

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/gguf"
)

// Validation is out-of-sample evidence. It never enters the synthetic fit.
type Validation struct {
	At           time.Time       `json:"at"`
	Model        string          `json:"model"`
	Quant        string          `json:"quant"`
	File         string          `json:"file"`
	FileBytes    int64           `json:"file_bytes"`
	FileModified time.Time       `json:"file_modified"`
	Args         []string        `json:"args"`
	LoadMode     string          `json:"load_mode"`
	Depth        int             `json:"depth"`
	Prompt       int             `json:"prompt"`
	Gen          int             `json:"gen"`
	MeasuredTPS  float64         `json:"measured_tps"`
	SpreadTPS    float64         `json:"spread_tps"`
	PredictedTPS float64         `json:"predicted_tps"`
	BandPct      int             `json:"band_pct"`
	ErrorPct     float64         `json:"error_pct"` // 100 * (prediction/measured - 1)
	InsideBand   bool            `json:"inside_band"`
	Basis        string          `json:"basis"`
	Raw          json.RawMessage `json:"raw"`
}

type CheckPrediction struct {
	TPS     float64
	BandPct int
	Basis   string
}

type CheckOptions struct {
	Home, Engine, Path, Model, Quant string
	Fingerprint                      Fingerprint
	Predict                          func(BenchRow) (CheckPrediction, error)
}

// MatchFile reads only a local header and requires a unique catalog geometry.
// Exact file bytes and active tensor bytes replace the catalog's quant sizes.
func MatchFile(path string) (catalog.Model, catalog.Quant, error) {
	var empty catalog.Model
	if strings.Contains(path, "://") {
		return empty, catalog.Quant{}, errors.New("--check needs a local GGUF; no downloads")
	}
	st, err := os.Stat(path)
	if err != nil {
		return empty, catalog.Quant{}, err
	}
	if !st.Mode().IsRegular() {
		return empty, catalog.Quant{}, errors.New("--check needs a regular GGUF file")
	}
	f, err := gguf.Open(path)
	if err != nil {
		return empty, catalog.Quant{}, err
	}
	if n, _ := f.Uint("split.count"); n > 1 {
		return empty, catalog.Quant{}, errors.New("--check currently needs an unsplit GGUF so prediction and benchmark use the same complete file")
	}
	s := f.Summary()
	if len(f.Warnings) > 0 {
		return empty, catalog.Quant{}, fmt.Errorf("GGUF is incomplete or invalid: %s", strings.Join(f.Warnings, "; "))
	}
	var matches []catalog.Model
	for _, m := range catalog.All() {
		if m.Family != s.Architecture || m.Layers != int(s.BlockCount) || m.QHeads != int(s.HeadCount) || m.KVHeads != int(s.HeadCountKV) || m.HeadDim != int(s.KeyLength) || m.ExpertsTotal != int(s.ExpertCount) || m.ExpertsActive != int(s.ExpertUsedCount) {
			continue
		}
		if s.ParameterCount == 0 || m.TotalParamsB <= 0 || math.Abs(float64(s.ParameterCount)/1e9/m.TotalParamsB-1) > .05 {
			continue
		}
		matches = append(matches, m)
	}
	if len(matches) != 1 {
		return empty, catalog.Quant{}, fmt.Errorf("--check needs a unique catalog model matching GGUF architecture, layers, heads, experts and parameter count; found %d", len(matches))
	}
	m := matches[0]
	tag := catalog.FileTag(path)
	label := strings.TrimSuffix(s.FileTypeLabel, " (guessed)")
	if tag == "" {
		tag = label
	}
	// UD/mixed variants use a filename tag, while the header verifies its
	// underlying quant. A misleading filename must not pick another kernel.
	if strings.TrimSuffix(strings.TrimPrefix(tag, "UD-"), "_MOE") != label {
		return empty, catalog.Quant{}, fmt.Errorf("GGUF quant %s disagrees with filename tag %s", label, tag)
	}
	var quant catalog.Quant
	for _, q := range m.Quants {
		qt := q.FileTag
		if qt == "" {
			qt = q.Name
		}
		if q.Format == "gguf" && (strings.EqualFold(qt, tag) || strings.EqualFold(q.Name, tag)) {
			quant = q
			break
		}
	}
	if quant.Name == "" {
		return empty, quant, fmt.Errorf("%s has no catalog file for quant %s", m.ID, tag)
	}
	quant.FileBytes = st.Size()
	quant.ReadBytesPerToken = int64(s.ActiveBytes)
	quant.Estimated = false
	m.Quants = []catalog.Quant{quant}
	return m, quant, nil
}

// Check benchmarks a real file at short and deep context, using the calibration
// flags and the same mlock-warning fallback. Only a complete run is appended.
func Check(ctx context.Context, o CheckOptions) ([]Validation, error) {
	c, err := Load(o.Home)
	if err != nil {
		return nil, err
	}
	if o.Fingerprint.Hash == "" || c.Fingerprint.Hash != o.Fingerprint.Hash {
		return nil, errors.New("calibration is stale; run trialm calibrate before --check")
	}
	if o.Predict == nil || o.Model == "" || o.Quant == "" {
		return nil, errors.New("--check needs a matched model and calibrated prediction")
	}
	path, err := filepath.Abs(o.Path)
	if err != nil {
		return nil, err
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() {
		return nil, errors.New("--check needs a regular GGUF file")
	}
	args := []string{"-m", path, "-p", "512", "-n", "8", "-d", "0,32768", "-r", "3", "-o", "json", "-ngl", "99", "-fa", "on", "-lm", "mlock"}
	loadMode := "mlock"
	var rows []Validation
	measure := func() error {
		return runBench(ctx, o.Engine, args, func(raw json.RawMessage, b BenchRow) error {
			if b.GPULayers == 0 || strings.TrimSpace(b.GPU) == "" {
				return errors.New("llama-bench did not use a GPU; validation was not saved")
			}
			if b.Depth != 0 && b.Depth != 32768 || !(b.Prompt == 512 && b.Gen == 0 || b.Prompt == 0 && b.Gen == 8) {
				return errors.New("unexpected validation measurement group")
			}
			if len(b.Samples) != 3 {
				return errors.New("validation needs three repetitions")
			}
			seconds := make([]float64, len(b.Samples))
			for i, ns := range b.Samples {
				if ns <= 0 || !finite(ns) {
					return errors.New("invalid validation timing")
				}
				seconds[i] = ns / 1e9 / float64(b.Prompt+b.Gen)
			}
			pred, e := o.Predict(b)
			if e != nil {
				return e
			}
			if pred.TPS <= 0 || !finite(pred.TPS) || pred.BandPct < 0 {
				return errors.New("invalid calibrated prediction")
			}
			measured := 1 / median(seconds)
			for _, r := range rows {
				if r.Depth == b.Depth && r.Gen == b.Gen && r.Prompt == b.Prompt {
					return errors.New("duplicate validation measurement group")
				}
			}
			radius := float64(pred.BandPct) / 100
			rows = append(rows, Validation{At: time.Now(), Model: o.Model, Quant: o.Quant, File: path, FileBytes: st.Size(), FileModified: st.ModTime(), Args: append([]string(nil), args...), LoadMode: loadMode, Depth: b.Depth, Prompt: b.Prompt, Gen: b.Gen, MeasuredTPS: measured, SpreadTPS: spread(seconds) / (median(seconds) * median(seconds)), PredictedTPS: pred.TPS, BandPct: pred.BandPct, ErrorPct: 100 * (pred.TPS/measured - 1), InsideBand: measured >= pred.TPS*(1-radius) && measured <= pred.TPS*(1+radius), Basis: pred.Basis, Raw: raw})
			return nil
		})
	}
	err = measure()
	if errors.Is(err, errMlockUnavailable) && ctx.Err() == nil {
		rows = nil
		loadMode = "mmap"
		args[len(args)-1] = loadMode
		err = measure()
	}
	if err != nil {
		return nil, err
	}
	if len(rows) != 4 {
		return nil, errors.New("incomplete validation grid; no validation was saved")
	}
	after, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if after.Size() != st.Size() || !after.ModTime().Equal(st.ModTime()) {
		return nil, errors.New("GGUF changed during validation; no validation was saved")
	}
	c.Validations = append(c.Validations, rows...)
	if err = c.Save(o.Home); err != nil {
		return nil, err
	}
	return rows, nil
}
