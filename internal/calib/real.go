package calib

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
)

// RealMeasurement contains the speed samples from one Ready report. Model,
// quant and engine must be the identifiers used by the predictor.
type RealMeasurement struct {
	Model, Quant, Engine string
	Decode               map[int]float64
	Prefill              float64
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
	add := func(depth int, mode string, speed float64) {
		if speed <= 0 || !finite(speed) {
			return
		}
		raw, _ := json.Marshal(struct{ Model, Engine, Mode string }{m.Model, m.Engine, mode})
		c.Rows = append(c.Rows, Row{Probe: "real", Quant: m.Quant, Depth: depth, Seconds: 1 / speed, Raw: raw})
	}
	for d, speed := range m.Decode {
		add(d, "decode", speed)
	}
	add(0, "prefill", m.Prefill)
	return c.Save(home)
}

// RealSpeed returns the median speed and measured relative spread, if at least
// two runs exist. A single run has no independently measured band.
func (c *Calibration) RealSpeed(model, quant, engine, mode string, depth int) (float64, int, bool) {
	var samples []float64
	for _, r := range c.Rows {
		if r.Probe != "real" || r.Quant != quant || (mode == "decode" && DepthBucket(r.Depth) != DepthBucket(depth)) {
			continue
		}
		var tag struct{ Model, Engine, Mode string }
		if json.Unmarshal(r.Raw, &tag) == nil && tag.Model == model && tag.Engine == engine && tag.Mode == mode && r.Seconds > 0 {
			samples = append(samples, 1/r.Seconds)
		}
	}
	if len(samples) == 0 {
		return 0, 0, false
	}
	v := median(samples)
	band := -1
	if len(samples) >= 2 {
		band = int(math.Ceil(100 * spread(samples) / v))
	}
	return v, band, true
}
