package calib

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"math"
	"os"
	"slices"
)

// RealSample preserves the interval and token provenance of one timed request.
type RealSample struct {
	Mode            string  `json:"mode"`
	Depth           int     `json:"depth"`
	Prompt          int     `json:"prompt"`
	Generated       int     `json:"generated"`
	TokensEstimated bool    `json:"tokens_estimated,omitempty"`
	Speed           float64 `json:"speed"`
	TTFTSec         float64 `json:"ttft_s"`
}

// RealMeasurement contains the speed samples from one Ready report. Model,
// quant and engine must be the identifiers used by the predictor.
type RealMeasurement struct {
	Model, Quant, Engine string
	Decode               map[int]float64
	Prefill              float64
	Samples              []RealSample
}

// RecordReal appends a run, creating a fingerprinted calibration if needed. A stale
// store is rejected rather than combining evidence from different machines.
func RecordReal(ctx context.Context, home, enginePath string, m RealMeasurement) error {
	if m.Model == "" || m.Quant == "" || m.Engine == "" {
		return errors.New("calib: model, quant and engine required")
	}
	f, err := MachineFingerprint(ctx, enginePath)
	if err != nil {
		return err
	}
	c, err := Load(home)
	if errors.Is(err, os.ErrNotExist) {
		c = &Calibration{Version: GeneratorVersion, FittingVersion: FittingVersion, Fingerprint: f}
	} else if err != nil {
		return err
	}
	if c.Fingerprint.Hash != f.Hash {
		return errors.New("calib: machine fingerprint changed")
	}
	c.appendReal(m)
	return c.Save(home)
}

func (c *Calibration) appendReal(m RealMeasurement) {
	samples := m.Samples
	// Older callers have no token counts. Retain their speeds with flagged counts.
	if len(samples) == 0 {
		for _, d := range slices.Sorted(maps.Keys(m.Decode)) {
			samples = append(samples, RealSample{Mode: "decode", Depth: d, Speed: m.Decode[d], TokensEstimated: true})
		}
		samples = append(samples, RealSample{Mode: "prefill", Prompt: 512, Speed: m.Prefill, TokensEstimated: true})
	}
	for _, s := range samples {
		if s.Speed <= 0 || !finite(s.Speed) {
			continue
		}
		raw, _ := json.Marshal(struct{ Model, Engine, Mode string }{m.Model, m.Engine, s.Mode})
		c.Rows = append(c.Rows, Row{Probe: "real", Quant: m.Quant, Depth: s.Depth, Prompt: s.Prompt, Gen: s.Generated, TokensEstimated: s.TokensEstimated, Seconds: 1 / s.Speed, Raw: raw})
	}
}

// RealFactor uses the latest measured interval, pooling only its repetitions.
// The caller supplies uncalibrated speeds and retains its prior band.
func (c *Calibration) RealFactor(model, quant, engine, mode string, baseline func(Row) float64) (FactorResult, bool) {
	samples := map[[2]int][]float64{}
	var interval [2]int
	for _, r := range c.Rows {
		if r.Probe != "real" || r.Quant != quant || r.Seconds <= 0 || !finite(r.Seconds) {
			continue
		}
		var tag struct{ Model, Engine, Mode string }
		if json.Unmarshal(r.Raw, &tag) == nil && tag.Model == model && tag.Engine == engine && tag.Mode == mode {
			if predicted := baseline(r); predicted > 0 && finite(predicted) {
				interval = [2]int{r.Depth, r.Prompt}
				samples[interval] = append(samples[interval], 1/r.Seconds/predicted)
			}
		}
	}
	ratios := samples[interval]
	if len(ratios) == 0 {
		return FactorResult{}, false
	}
	scale := median(ratios)
	return FactorResult{Multiplier: scale, BandPct: int(math.Ceil(100 * spread(ratios) / scale)), Basis: "real-model interval correction. Extrapolation retains the default band"}, true
}
