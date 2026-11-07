package predict

import (
	"math"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/catalog"
	runtime "github.com/aeroknight786/trialm/internal/engine"
)

// Reference measurements from public llama.cpp benchmarks (research/
// bench_data.csv; source URLs there). The efficiency constants were fitted
// on this dataset, so this is a regression guard against accidental formula
// changes, not an accuracy proof — the held-out (leave-one-device-out)
// errors are in docs/RESEARCH.md.
var refs = []struct {
	device string
	ramGB  float64
	model  string
	quant  string
	metric string // "tg" or "pp"
	n      int    // pp prompt length (from depth 0)
	meas   float64
	tol    float64 // allowed |pred/meas-1|
}{
	{"Apple M4 Pro 20c", 64, "gpt-oss-20b", "MXFP4", "tg", 128, 63.34, 0.25},
	{"Apple M4 Pro 20c", 64, "gpt-oss-20b", "MXFP4", "pp", 2048, 700.78, 0.30},
	{"Apple M4 Pro 20c", 64, "gpt-oss-20b", "MXFP4", "pp", 32768, 419.74, 0.35},
	{"Apple M3 Max 40c", 128, "gpt-oss-20b", "MXFP4", "tg", 128, 64.26, 0.30},
	{"Apple M3 Max 40c", 128, "gpt-oss-20b", "MXFP4", "pp", 32768, 530.52, 0.35},
	{"Apple M3 Max 40c", 64, "llama3.1-8b", "Q4_K_M", "pp", 512, 693.32, 0.30},
	{"Apple M2 Ultra 76c", 192, "gpt-oss-120b", "MXFP4", "tg", 128, 79.68, 0.30},
	{"RTX 4090", 64, "gpt-oss-20b", "MXFP4", "tg", 128, 221.95, 0.25},
	{"RTX 4090", 64, "gpt-oss-20b", "MXFP4", "pp", 2048, 8022.33, 0.50},  // CUDA MoE prefill: known +40% outlier
	{"RTX 4090", 64, "gpt-oss-20b", "MXFP4", "pp", 32768, 5112.35, 0.75}, // long-prompt CUDA attention cost underestimated (+65%)
	{"RTX 3090", 64, "gpt-oss-20b", "MXFP4", "tg", 128, 161.77, 0.35},    // Ampere vs Ada cache effects: +31%
	{"RTX 4090", 64, "llama3.1-8b", "Q4_K_M", "pp", 512, 7138.99, 0.55},  // 2024 build; newer builds are ~2× faster
	{"RTX 3090", 64, "llama3.1-8b", "Q4_K_M", "pp", 512, 4030.40, 0.40},
	{"Radeon 8060S", 128, "gpt-oss-120b", "MXFP4", "tg", 32, 47.49, 0.30},
	{"Radeon 8060S", 128, "qwen3-coder-30b-a3b", "Q4_K_M", "tg", 128, 57.02, 0.25},
	{"Jetson AGX Thor", 128, "qwen3-coder-30b-a3b", "Q8_0", "tg", 32, 44.26, 0.25},
}

func TestReferenceMeasurements(t *testing.T) {
	prof, err := runtime.Get("llamacpp")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range refs {
		m, ok := catalog.Lookup(r.model)
		if !ok {
			t.Fatalf("model %s not in catalog", r.model)
		}
		sys, err := SyntheticSystem(r.device, r.ramGB, 0)
		if err != nil {
			t.Fatal(err)
		}
		tg := TargetFromSystem(sys, 0)
		backend := ""
		if tg.Device.Segment == "amd_strix_halo" {
			backend = "rocm"
		}
		res := Predict(Input{Model: m, QuantName: r.quant, Runtime: prof, Target: tg, CtxOverride: 65536, BackendOverride: backend})
		if res.Unsupported != "" {
			t.Fatalf("%s %s: %s", r.device, r.model, res.Unsupported)
		}
		var pred float64
		if r.metric == "tg" {
			pred = res.Timing.DecodeTPS(r.n / 2)
		} else {
			pred = res.Timing.PrefillTPS(0, r.n)
		}
		e := pred/r.meas - 1
		t.Logf("%-20s %-20s %s%-6d meas %8.1f pred %8.1f (%+.0f%%)", r.device, r.model, r.metric, r.n, r.meas, pred, 100*e)
		if math.Abs(e) > r.tol {
			t.Errorf("%s %s %s%d: predicted %.1f vs measured %.1f (%+.0f%%, tolerance ±%.0f%%)", r.device, r.model, r.metric, r.n, pred, r.meas, 100*e, 100*r.tol)
		}
	}
}

func TestMemoryFitLevels(t *testing.T) {
	m, _ := catalog.Lookup("gpt-oss-120b")
	q, _ := m.Quant("")
	sys, _ := SyntheticSystem("RTX 4090", 64, 24)
	tg := TargetFromSystem(sys, 0)
	mem := EstimateMemory(m, q, 32768, 2, true, "cuda", tg)
	if mem.Level != FitOffload {
		t.Errorf("gpt-oss-120b on a 24 GB card with 64 GB RAM should be offload, got %s", mem.Level)
	}
	small, _ := catalog.Lookup("qwen3-8b")
	sq, _ := small.Quant("")
	if l := EstimateMemory(small, sq, 32768, 2, true, "cuda", tg).Level; l != FitOK {
		t.Errorf("qwen3:8b on 24 GB should fit, got %s", l)
	}
	// Mac wired limit: 24 GB Mac exposes ~16 GB to the GPU.
	mac, _ := SyntheticSystem("Apple M4 Pro 16c", 24, 0)
	mt := TargetFromSystem(mac, 0)
	coder, _ := catalog.Lookup("qwen3-coder:30b")
	cq, _ := coder.Quant("")
	mm := EstimateMemory(coder, cq, 32768, 2, true, "metal", mt)
	if mm.Level == FitOK || mm.Level == FitTight {
		t.Errorf("qwen3-coder 30B Q4_K_M at 32k on a 24 GB Mac must not fit the ~16 GB GPU budget, got %s (%.1f/%.1f GB)", mm.Level, mm.TotalBytes/1e9, mm.BudgetBytes/1e9)
	}
}

func TestDecodeSlowsWithDepth(t *testing.T) {
	m, _ := catalog.Lookup("llama3.1-8b")
	prof, _ := runtime.Get("llamacpp")
	sys, _ := SyntheticSystem("RTX 4090", 64, 0)
	res := Predict(Input{Model: m, Runtime: prof, Target: TargetFromSystem(sys, 0), CtxOverride: 65536})
	d0, d32 := res.Timing.DecodeTPS(0), res.Timing.DecodeTPS(32768)
	if !(d32 < d0*0.9) {
		t.Errorf("decode should slow with depth for a full-attention model: %.0f → %.0f", d0, d32)
	}
	p0 := res.Timing.PrefillTPS(0, 2048)
	p32 := res.Timing.PrefillTPS(30720, 32768)
	if !(p32 < p0) {
		t.Errorf("prefill should slow with depth: %.0f → %.0f", p0, p32)
	}
}

func TestOllamaDefaultContextTiers(t *testing.T) {
	prof, _ := runtime.Get("ollama")
	m, _ := catalog.Lookup("gpt-oss:20b")
	for _, c := range []struct {
		device string
		vram   float64
		want   int
	}{{"RTX 4060 Ti", 16, 4096}, {"RTX 4090", 24, 32768}, {"RTX PRO 6000", 96, 131072}} {
		sys, _ := SyntheticSystem(c.device, 64, c.vram)
		res := Predict(Input{Model: m, Runtime: prof, Target: TargetFromSystem(sys, 0)})
		if res.Settings.ContextTokens != c.want {
			t.Errorf("%s %v GB: context %d, want %d (capped at native 131072)", c.device, c.vram, res.Settings.ContextTokens, c.want)
		}
	}
}

func TestAppleUnknownCoresName(t *testing.T) {
	sys, _ := SyntheticSystem("Apple M1", 8, 0)
	sys.GPUs[0].Cores = 0
	tg := TargetFromSystem(sys, 0)
	if strings.Contains(tg.Name, "0-core") || !strings.Contains(tg.Name, "GPU core count unknown") {
		t.Fatalf("name %q", tg.Name)
	}
}

func TestGeneratedCatalogModels(t *testing.T) {
	prof, _ := runtime.Get("llamacpp")
	sys, _ := SyntheticSystem("RTX 4090", 64, 0)
	tg := TargetFromSystem(sys, 0)
	for _, id := range []string{"qwen3.6:35b-a3b", "gemma4:26b-a4b", "glm-4.7-flash", "qwen3.8:27b"} {
		m, ok := catalog.Lookup(id)
		if !ok || !m.Generated {
			t.Fatalf("%s missing from the generated catalog", id)
		}
		res := Predict(Input{Model: m, Runtime: prof, Target: tg, Tuned: true})
		if res.Unsupported != "" || res.DecodeTPS0 <= 0 || res.Memory.TotalBytes <= 0 {
			t.Fatalf("%s: bad prediction %+v", id, res.Unsupported)
		}
	}
	// Hybrid models' KV cache grows only on their full-attention layers.
	q36, _ := catalog.Lookup("qwen3.6:35b-a3b")
	if kv := q36.KVCacheBytes(65536, 2); kv > 1.5e9 {
		t.Fatalf("qwen3.6 KV at 64k = %.2f GB, expected ~1.3 GB (10 full-attention layers)", kv/1e9)
	}
}

// TestAttentionAtDepth guards the KV-read term against the measured slowdown
// between a short and a long cache (bench_data.csv depth rows, and the
// owner's M5 runs of Qwen3.5 9B). Ratios cancel the weight-streaming fit,
// so they test only the attention term.
func TestAttentionAtDepth(t *testing.T) {
	prof, _ := runtime.Get("llamacpp")
	for _, c := range []struct {
		device, model, quant string
		ram                  float64
		short, long          int
		measured, tol        float64 // tok/s(short) / tok/s(long)
	}{
		{"Apple M3 Max 40c", "llama3.1-8b", "Q4_K_M", 64, 256, 4096, 48.97 / 36.12, 0.15},
		{"Apple M2 Ultra 76c", "llama3.1-8b", "Q4_K_M", 192, 256, 4096, 78.81 / 54.13, 0.15},
		{"Apple M1 Max 32c", "llama3.1-8b", "Q4_K_M", 64, 256, 4096, 35.73 / 26.84, 0.15},
		{"RTX 4090", "llama3.1-8b", "Q4_K_M", 64, 256, 4096, 130.58 / 110.66, 0.10},
		{"RTX 3090", "llama3.1-8b", "Q4_K_M", 64, 256, 4096, 115.42 / 87.49, 0.20}, // CUDA keeps the weight rate (fit-attn: no held-out gain); the 3090 is its worst row
		{"Apple M5 10c", "qwen3.5-9b", "Q4_K_M", 24, 0, 65536, 21.0 / 9.0, 0.30},   // busy-machine runs vary
	} {
		m, ok := catalog.Lookup(c.model)
		if !ok {
			t.Fatal(c.model)
		}
		sys, err := SyntheticSystem(c.device, c.ram, 0)
		if err != nil {
			t.Fatal(err)
		}
		res := Predict(Input{Model: m, QuantName: c.quant, Runtime: prof, Target: TargetFromSystem(sys, 0), CtxOverride: 65536})
		got := res.Timing.DecodeTPS(c.short) / res.Timing.DecodeTPS(c.long)
		t.Logf("%-18s %-12s %5d→%5d: slowdown predicted %.2f× measured %.2f×", c.device, c.model, c.short, c.long, got, c.measured)
		if math.Abs(got/c.measured-1) > c.tol {
			t.Errorf("%s %s: slowdown %.2f× vs measured %.2f×", c.device, c.model, got, c.measured)
		}
	}
}
