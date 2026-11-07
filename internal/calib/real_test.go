package calib

import (
	"math"
	"testing"
)

func TestRealIntervalsRoundTrip(t *testing.T) {
	c := Calibration{Version: GeneratorVersion, FittingVersion: FittingVersion, Fingerprint: Fingerprint{Hash: "machine"}}
	samples := []RealSample{
		{Mode: "decode", Depth: 16384, Prompt: 16384, Generated: 32, Speed: 20},
		{Mode: "decode", Depth: 65536, Prompt: 65536, Generated: 31, Speed: 8.96, TokensEstimated: true},
		{Mode: "prefill", Depth: 128, Prompt: 383, Generated: 16, Speed: 368},
	}
	c.appendReal(RealMeasurement{Model: "model", Quant: "Q4", Engine: "engine", Samples: samples})
	dir := t.TempDir()
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != len(samples) {
		t.Fatal(got.Rows)
	}
	for i, row := range got.Rows {
		sample := samples[i]
		if row.Depth != sample.Depth || row.Prompt != sample.Prompt || row.Gen != sample.Generated || row.TokensEstimated != sample.TokensEstimated || row.Seconds != 1/sample.Speed {
			t.Fatalf("row=%+v sample=%+v", row, sample)
		}
	}
	// Different depths give the same residual only after evaluating each baseline.
	f, ok := got.RealFactor("model", "Q4", "engine", "decode", func(row Row) float64 { return 2 / row.Seconds })
	if !ok || math.Abs(f.Multiplier-.5) > 1e-12 {
		t.Fatal(f, ok)
	}
	for _, mode := range []string{"decode", "prefill"} {
		if _, ok := got.RealFactor("another", "Q4", "engine", mode, func(Row) float64 { t.Fatal("unmatched row evaluated"); return 0 }); ok {
			t.Fatal("matched another model")
		}
	}
	legacy := Calibration{}
	legacy.appendReal(RealMeasurement{Model: "old", Quant: "Q4", Engine: "engine", Decode: map[int]float64{65536: 8.96}, Prefill: 368})
	if len(legacy.Rows) != 2 || !legacy.Rows[0].TokensEstimated || !legacy.Rows[1].TokensEstimated || legacy.Rows[1].Prompt != 512 {
		t.Fatal(legacy.Rows)
	}
}
