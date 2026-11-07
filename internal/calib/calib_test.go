package calib

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/gguf"
	"github.com/aeroknight786/trialm/internal/hw"
)

func TestProbeGeometryAndMixedKernels(t *testing.T) {
	for _, p := range Grid(false) {
		t.Run(p.ID(), func(t *testing.T) {
			meta, ts, m, q := p.Layout()
			if len(meta["tokenizer.ggml.tokens"].([]string)) != p.Shape.Vocab || len(meta["tokenizer.ggml.token_type"].([]int32)) != p.Shape.Vocab {
				t.Fatal("vocabulary shrank")
			}
			if q.FileBytes < 256<<20 {
				t.Fatal("file could sit in GPU cache")
			}
			if m.FullAttnLayers+m.SWALayers+m.RecurrentLayers != p.Layers {
				t.Fatal("lost layer geometry")
			}
			seen := map[string]bool{}
			for _, ts := range ts {
				if seen[ts.Name] {
					t.Fatal("duplicate", ts.Name)
				}
				seen[ts.Name] = true
				tr, _ := ts.Type.Traits()
				if ts.Dimensions[0]%tr.BlockSize != 0 {
					t.Fatal("invalid row", ts)
				}
				if ts.Name == "output.weight" && (ts.Dimensions[0] != uint64(p.Shape.Hidden) || ts.Dimensions[1] != uint64(p.Shape.Vocab)) {
					t.Fatal("output head shrank")
				}
				if p.Shape.Class == "sliding" && ts.Name == "output.weight" && ts.Type != gguf.TypeQ8_0 {
					t.Fatal("gpt-oss output quant differs from source GGUF")
				}
				if p.Shape.Class == "hybrid" && ts.Name == "blk.0.ffn_gate_exps.weight" && ts.Type != gguf.TypeIQ2_S {
					t.Fatal("UD mixed quant lost")
				}
			}
			if !seen["output.weight"] || !seen["token_embd.weight"] {
				t.Fatal("missing fixed cost")
			}
		})
	}
}
func TestPayloadDeterministicFiniteAndNonrepeating(t *testing.T) {
	for _, typ := range []gguf.GGMLType{gguf.TypeF32, gguf.TypeF16, gguf.TypeBF16, gguf.TypeQ4_K, gguf.TypeQ6_K, gguf.TypeIQ2_S, gguf.TypeIQ3_XXS, gguf.TypeIQ4_XS, gguf.TypeMXFP4, gguf.TypeQ8_0} {
		tr, _ := typ.Traits()
		n := tr.TypeSize * 8192
		var a, b bytes.Buffer
		if e := payload(typ, "output.weight", false)(context.Background(), &a, n); e != nil {
			t.Fatal(e)
		}
		_ = payload(typ, "output.weight", false)(context.Background(), &b, n)
		if !bytes.Equal(a.Bytes(), b.Bytes()) {
			t.Fatal("not deterministic", typ)
		}
		if bytes.Equal(a.Bytes()[:n/2], a.Bytes()[n/2:]) {
			t.Fatal("payload repeats across chunks", typ)
		}
		for i := uint64(0); i < n; i += tr.TypeSize {
			block := a.Bytes()[i : i+tr.TypeSize]
			if typ == gguf.TypeF32 {
				f := math.Float32frombits(binary.LittleEndian.Uint32(block))
				if math.IsInf(float64(f), 0) || math.IsNaN(float64(f)) {
					t.Fatal("bad F32")
				}
				continue
			}
			if typ == gguf.TypeMXFP4 {
				if block[0] == 255 {
					t.Fatal("NaN MXFP4 exponent")
				}
				continue
			}
			off := 0
			if typ == gguf.TypeQ6_K {
				off = 208
			}
			v := binary.LittleEndian.Uint16(block[off:])
			mask := uint16(0x7c00)
			if typ == gguf.TypeBF16 {
				mask = 0x7f80
			}
			if v&mask == mask {
				t.Fatal("non-finite scale", typ)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := payload(gguf.TypeQ8_0, "x", false)(ctx, io.Discard, 34); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
func TestGenerationCancellationIsAtomic(t *testing.T) {
	home := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := Generate(ctx, home, Grid(true)[0]); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	entries, e := os.ReadDir(filepath.Join(home, "cache", "calib"))
	if e != nil || len(entries) > 0 {
		t.Fatalf("partial file survived: %v %v", entries, e)
	}
}

func fitRows() []Row {
	var rows []Row
	for _, l := range []int{2, 6} {
		for _, noise := range []float64{0.99, 1.01} {
			rows = append(rows, Row{Class: "dense", Quant: "Q4_K_M", Layers: l, Gen: 32, Seconds: (.01 + .002*float64(l)) * noise, Baseline: .02 + .003*float64(l)})
		}
	}
	rows = append(rows, Row{Class: "dense", Quant: "Q4_K_M", Layers: 4, Check: true, Gen: 32, Seconds: .019, Baseline: .032})
	return rows
}
func TestAffineFitResidualBandAndExtrapolation(t *testing.T) {
	c := Calibration{Rows: fitRows()}
	c.FitRows()
	if len(c.Fits) != 1 {
		t.Fatalf("%+v", c.Fits)
	}
	f := c.Fits[0]
	if math.Abs(f.Fixed-.01) > 1e-9 || math.Abs(f.PerLayer-.002) > 1e-9 {
		t.Fatal(f)
	}
	v, b, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode")
	if !ok || math.Abs(v-(.128/.082)) > 1e-8 || b <= 0 {
		t.Fatal(v, b, ok)
	}
	_, near, _ := c.Factor("dense", "Q4_K_M", 0, 4, "decode")
	if near >= b {
		t.Fatal("band ignored extrapolation leverage")
	}
	if len(f.Residuals) != 5 || f.LogSigma <= 0 {
		t.Fatal("check did not enter residuals")
	}
	if _, _, ok = c.Factor("dense", "Q4_K_M", 32768, 36, "decode"); ok {
		t.Fatal("unmeasured depth claimed")
	}
}
func TestUnidentifiableFitDoesNotCalibrate(t *testing.T) {
	c := Calibration{Rows: []Row{fitRows()[0], fitRows()[2]}}
	c.FitRows()
	if _, _, ok := c.Factor("dense", "Q4_K_M", 0, 36, "decode"); ok {
		t.Fatal("two points supplied an invented band")
	}
	c.Rows = append(c.Rows, Row{Class: "dense", Quant: "Q4_K_M", Layers: 4, Gen: 32, Seconds: math.NaN(), Baseline: 1})
	c.FitRows()
	if len(c.Fits) != 1 {
		t.Fatal("invalid sample entered fit")
	}
}
func TestQuantCompositionAndMissingMLA(t *testing.T) {
	c := Calibration{Fits: []Fit{{Class: "hybrid", Quant: "IQ3_XXS", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 2, BaseLayer: 2, BandPct: 3}, {Class: "dense", Quant: "IQ3_XXS", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 3, BaseLayer: 3, BandPct: 4}, {Class: "dense", Quant: "Q8_0", Mode: "decode", Fixed: 1, PerLayer: 1, BaseFixed: 6, BaseLayer: 6, BandPct: 5}}}
	v, b, ok := c.Factor("hybrid", "Q8_0", 0, 40, "decode")
	if !ok || v != 4 || b < 12 {
		t.Fatal(v, b, ok)
	}
	if _, _, ok = c.Factor("mla", "Q8_0", 0, 40, "decode"); ok {
		t.Fatal("unmeasured MLA calibrated")
	}
}
func TestAtomicStoreVersionAndDepthBuckets(t *testing.T) {
	home := t.TempDir()
	c := Calibration{Version: GeneratorVersion, Rows: fitRows(), Fingerprint: Fingerprint{Hash: "test"}}
	c.FitRows()
	if e := c.Save(home); e != nil {
		t.Fatal(e)
	}
	loaded, e := Load(home)
	if e != nil || loaded.Fingerprint.Hash != "test" || len(loaded.Rows) != 5 {
		t.Fatal(loaded, e)
	}
	st, _ := os.Stat(filepath.Join(home, "calib.json"))
	if runtime.GOOS != "windows" && st.Mode().Perm() != 0600 {
		t.Fatal("world-readable calibration")
	}
	c.Version--
	_ = c.Save(home)
	if _, e = Load(home); e == nil {
		t.Fatal("old generator accepted")
	}
	for d, want := range map[int]int{0: 0, 4095: 0, 8192: 8192, 16384: 32768, 45000: 32768} {
		if DepthBucket(d) != want {
			t.Fatal(d)
		}
	}
}

func TestCalibBenchHelper(t *testing.T) {
	if os.Getenv("TRIALM_CALIB_HELPER") != "1" {
		return
	}
	fmt.Print(`[{"n_prompt":512,"n_gen":0,"n_depth":0,"avg_ns":512000000,"samples_ns":[511000000,513000000],"gpu_info":"test GPU","n_gpu_layers":99}`)
	fmt.Fprintln(os.Stderr, "intentional engine failure after a finished row")
	os.Exit(1)
}
func TestRunnerKeepsFinishedRowsOnFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell adapter for test executable")
	}
	t.Setenv("TRIALM_CALIB_HELPER", "1")
	home := t.TempDir()
	p := Grid(true)[0]
	dir := filepath.Join(home, "cache", "calib")
	_ = os.MkdirAll(dir, 0700)
	f, e := os.Create(filepath.Join(dir, p.ID()+".gguf"))
	if e != nil {
		t.Fatal(e)
	}
	_ = f.Truncate(256 << 20)
	_ = f.Close()
	engine := filepath.Join(t.TempDir(), "bench")
	exe, _ := os.Executable()
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=TestCalibBenchHelper\n"
	if e = os.WriteFile(engine, []byte(script), 0700); e != nil {
		t.Fatal(e)
	}
	var events []Event
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		c, e := Run(ctx, RunOptions{Home: home, Engine: engine, Quick: true, Backend: "metal", Fingerprint: Fingerprint{Hash: "test"}, Baseline: func(Probe, BenchRow) float64 { return .002 }, Event: func(e Event) { events = append(events, e) }})
		if e == nil || c.Complete || len(c.Rows) != 2 {
			t.Fatal(c, e)
		}
		saved, se := Load(home)
		if se != nil || len(saved.Rows) != 2 || !json.Valid(saved.Rows[0].Raw) {
			t.Fatal(saved, se)
		}
	}
	old, _ := Load(home)
	old.Rows = append(old.Rows, Row{Probe: "old-machine-row", Seconds: 1})
	_ = old.Save(home)
	changed, e := Run(ctx, RunOptions{Home: home, Engine: engine, Quick: true, Backend: "metal", Fingerprint: Fingerprint{Hash: "changed"}, Baseline: func(Probe, BenchRow) float64 { return .002 }})
	if e == nil || len(changed.Rows) != 2 || changed.Fingerprint.Hash != "changed" {
		t.Fatal("stale fingerprint rows reused", changed, e)
	}
	if len(events) == 0 {
		t.Fatal("no NDJSON event data")
	}
}

func TestRunBenchCancellationKeepsDeliveredRow(t *testing.T) {
	t.Setenv("TRIALM_CALIB_HELPER", "1")
	exe, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rows := 0
	e = runBench(ctx, exe, []string{"-test.run=TestCalibBenchHelper"}, func(raw json.RawMessage, b BenchRow) error { rows++; cancel(); return nil })
	if !errors.Is(e, context.Canceled) || rows != 1 {
		t.Fatal("finished row lost on cancellation", rows, e)
	}
}

func TestFingerprintIdentityAndStaleness(t *testing.T) {
	base := hw.System{OS: "darwin", Arch: "arm64", OSVersion: "26.5", AppleChip: "Apple M5", RAMBytes: 24 << 30, GPUs: []hw.GPU{{Vendor: "apple", Name: "Apple M5", Cores: 10}}}
	first, e := fingerprintForSystem(&base, "b11960")
	if e != nil {
		t.Fatal(e)
	}
	again, _ := fingerprintForSystem(&base, "b11960")
	if first.Hash != again.Hash {
		t.Fatal("unstable fingerprint")
	}
	for _, which := range []string{"chip", "ram", "os", "gpu", "build"} {
		sys := base
		sys.GPUs = append([]hw.GPU(nil), base.GPUs...)
		build := "b11960"
		switch which {
		case "chip":
			sys.AppleChip = "Apple M6"
		case "ram":
			sys.RAMBytes = 32 << 30
		case "os":
			sys.OSVersion = "27"
		case "gpu":
			sys.GPUs[0].Cores = 12
		case "build":
			build = "b12000"
		}
		changed, e := fingerprintForSystem(&sys, build)
		if e != nil || changed.Hash == first.Hash {
			t.Fatal("identity change did not invalidate", which, e)
		}
	}
	base.RAMBytes = 0
	if _, e = fingerprintForSystem(&base, "b11960"); e == nil {
		t.Fatal("missing RAM supplied a usable fingerprint")
	}
}
