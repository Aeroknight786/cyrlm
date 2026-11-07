package calib

import (
	"encoding/json"
	"testing"
)

func TestRealSpeedRoundTrip(t *testing.T) {
	c := Calibration{Version: GeneratorVersion, FittingVersion: FittingVersion, Fingerprint: Fingerprint{Hash: "machine"}}
	tag, _ := json.Marshal(struct{ Model, Engine, Mode string }{"model", "engine", "decode"})
	c.Rows = []Row{{Probe: "real", Quant: "Q4", Depth: 0, Seconds: 0.1, Raw: tag}, {Probe: "real", Quant: "Q4", Depth: 0, Seconds: 0.05, Raw: tag}}
	dir := t.TempDir()
	if err := c.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	speed, band, ok := got.RealSpeed("model", "Q4", "engine", "decode", 0)
	if !ok || speed != 15 || band <= 0 {
		t.Fatalf("%v %v %v", speed, band, ok)
	}
	if _, _, ok = got.RealSpeed("another", "Q4", "engine", "decode", 0); ok {
		t.Fatal("matched another model")
	}
	if got.Fingerprint.Hash == "different" {
		t.Fatal("fingerprint should invalidate")
	}
}
