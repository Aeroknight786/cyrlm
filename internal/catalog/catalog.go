// Package catalog is the seed model catalog: popular open-weight models used
// for local coding, with the architecture data the predictor needs (attention
// layout, KV heads, sliding windows, active parameters for MoE) and known
// quantized file sizes.
//
// Geometry comes from each model's published config. File sizes are those of
// the common GGUF/MLX builds; where a size is computed from bits-per-weight
// the Quant is marked Estimated. Upstream (cloud) model IDs are best-effort
// and can be overridden with --upstream-model.
//
// For models not listed here, `trialm inspect <gguf file or URL>` reads exact
// metadata from the GGUF header without downloading the weights.
package catalog

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// generatedJSON holds entries produced by tools/catalog-gen from the
// official GGUF headers on Hugging Face (exact geometry and file sizes).
//
//go:embed models_gguf.json
var generatedJSON []byte

// Quant is one downloadable quantization of a model.
type Quant struct {
	// Name, e.g. "Q4_K_M", "Q8_0", "MXFP4", "MLX-4bit".
	Name string
	// Format is "gguf" or "mlx".
	Format string
	// FileBytes is the download size (and resident weight size).
	FileBytes int64
	// ReadBytesPerToken overrides the derived bytes read per decoded token
	// (measured for gpt-oss). 0 = derive from parameters.
	ReadBytesPerToken int64
	// Estimated is true when FileBytes was computed from bits-per-weight.
	Estimated bool
	// FileTag selects this file in its Hugging Face repo when it differs
	// from Name (e.g. "UD-Q4_K_M", "MXFP4_MOE", "Q4_K"); used as
	// hf.co/<repo>:<tag> by Ollama, llama.cpp and LM Studio.
	FileTag string `json:",omitempty"`
}

// HFTag returns the tag that selects the quant's file on Hugging Face.
func (q Quant) HFTag() string {
	if q.FileTag != "" {
		return q.FileTag
	}
	return q.Name
}

var fileTagRe = regexp.MustCompile(`(?i)((?:UD-)?(?:TQ\d_\d|I?Q\d[_A-Z0-9]*|MXFP4(?:_MOE)?|BF16|F16|F32))$`)

var shardSuffixRe = regexp.MustCompile(`-\d{5}-OF-\d{5}$`)

// FileTag derives the quant tag from a GGUF file name such as
// "Qwen3.6-35B-A3B-UD-Q4_K_M.gguf" → "UD-Q4_K_M" ("" if none).
func FileTag(filename string) string {
	base := filename
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	base = strings.TrimSuffix(strings.ToUpper(base), ".GGUF")
	for _, prefix := range []string{"MMPROJ-", "MTP-", "DFLASH-", "EAGLE", "IMATRIX"} {
		if strings.HasPrefix(base, prefix) {
			return ""
		}
	}
	base = shardSuffixRe.ReplaceAllString(base, "")
	if m := fileTagRe.FindStringSubmatch(base); m != nil {
		return m[1]
	}
	return ""
}

// Model is one catalog entry.
type Model struct {
	// ID is the canonical id, e.g. "gpt-oss-20b".
	ID string
	// Name is the display name.
	Name string
	// OllamaTag is the Ollama library tag, also the name the simulated
	// server exposes (so Ollama-style client configs work unchanged).
	OllamaTag string
	// Aliases are other accepted names.
	Aliases []string
	// Family is the GGUF architecture family (Ollama "family").
	Family string

	// TotalParamsB is total parameters (billions).
	TotalParamsB float64
	// ReadParamsB is the parameters whose weights are read per decoded
	// token (active set for MoE; excludes an untied input-embedding table).
	ReadParamsB float64
	// MatmulParamsB is the parameters doing matmuls per token (prefill
	// FLOPs = 2 × MatmulParamsB + attention).
	MatmulParamsB float64
	MoE           bool
	ExpertsTotal  int
	ExpertsActive int

	Layers         int
	FullAttnLayers int // layers attending over the whole context
	SWALayers      int // sliding-window layers
	SWAWindow      int // sliding window in tokens
	KVHeads        int
	HeadDim        int
	QHeads         int
	// RecurrentLayers are linear-attention/SSM layers with a fixed-size
	// state instead of a growing KV cache (hybrid models).
	RecurrentLayers int
	// KVPerTokenF16 / SWAKVPerTokenF16, when set, are exact f16 KV bytes per
	// token for full-attention and sliding layers (from the GGUF header);
	// they override the heads × head-dim formula (needed for MLA etc.).
	KVPerTokenF16    float64
	SWAKVPerTokenF16 float64
	// RecurrentStateBytes is the fixed per-sequence recurrent state.
	RecurrentStateBytes float64
	// Generated is true for entries read from GGUF headers.
	Generated bool
	// UpstreamUnverified is true when OpenRouterID is a best guess.
	UpstreamUnverified bool
	// TrialStandIn is set when no provider hosts this exact model and the
	// trial uses its base model instead (description for users).
	TrialStandIn string

	// DecodeEff scales predicted decode speed for architectures whose
	// kernels run below the bandwidth model (1 = no adjustment). Qwen3 MoE
	// (128 small experts) measured ~0.7 on Strix Halo (ROCm) and Jetson
	// Thor (CUDA).
	DecodeEff float64

	// NativeCtx is the model's trained context length.
	NativeCtx int
	Tools     bool
	Thinking  bool

	// Quants, default first.
	Quants []Quant

	// OpenRouterID is the best-known OpenRouter model id.
	OpenRouterID string
	// CloudPrecision describes what cloud providers typically serve.
	CloudPrecision string
	// QuantMatchesCloud is true when the local default quant is the native
	// release precision (gpt-oss MXFP4), so cloud output quality matches.
	QuantMatchesCloud bool

	HFGGUFRepo string
	HFMLXRepo  string
	Notes      []string
}

const gb = 1_000_000_000

// bits-per-weight of common quantization types, as observed on whole files.
var bpw = map[string]float64{
	"Q4_K_M": 4.89, "Q5_K_M": 5.70, "Q6_K": 6.56, "Q8_0": 8.50, "F16": 16, "BF16": 16,
	"MLX-4bit": 4.50, "MLX-8bit": 8.50,
}

func gq(name string, gbytes float64) Quant {
	return Quant{Name: name, Format: "gguf", FileBytes: int64(gbytes * gb)}
}

// gqt is a GGUF quant whose file tag differs from its display name (e.g.
// unsloth's "UD-Q3_K_XL" files, shown as Q3_K_XL).
func gqt(name, tag string, gbytes float64) Quant {
	q := gq(name, gbytes)
	q.FileTag = tag
	return q
}

func est(name string, paramsB float64) Quant {
	format := "gguf"
	if strings.HasPrefix(name, "MLX") {
		format = "mlx"
	}
	return Quant{Name: name, Format: format, FileBytes: int64(paramsB * bpw[name] / 8 * gb), Estimated: true}
}

var models = []Model{
	{
		ID: "gpt-oss-20b", Name: "gpt-oss 20B", OllamaTag: "gpt-oss:20b", Aliases: []string{"openai/gpt-oss-20b", "gpt-oss"},
		Family: "gptoss", TotalParamsB: 20.91, ReadParamsB: 3.61, MatmulParamsB: 3.61, MoE: true, ExpertsTotal: 32, ExpertsActive: 4,
		Layers: 24, FullAttnLayers: 12, SWALayers: 12, SWAWindow: 128, KVHeads: 8, HeadDim: 64, QHeads: 64,
		NativeCtx: 131072, Tools: true, Thinking: true,
		Quants: []Quant{
			{Name: "MXFP4", Format: "gguf", FileBytes: 12_101_000_000, ReadBytesPerToken: 2_570_000_000},
			{Name: "MLX-MXFP4", Format: "mlx", FileBytes: 12_100_000_000, ReadBytesPerToken: 2_570_000_000, Estimated: true},
		},
		OpenRouterID: "openai/gpt-oss-20b", CloudPrecision: "MXFP4 (native release format)", QuantMatchesCloud: true,
		HFGGUFRepo: "ggml-org/gpt-oss-20b-GGUF",
		Notes:      []string{"Alternating sliding-window (128) and full attention keeps the KV cache small."},
	},
	{
		ID: "gpt-oss-120b", Name: "gpt-oss 120B", OllamaTag: "gpt-oss:120b", Aliases: []string{"openai/gpt-oss-120b"},
		Family: "gptoss", TotalParamsB: 116.83, ReadParamsB: 5.12, MatmulParamsB: 5.12, MoE: true, ExpertsTotal: 128, ExpertsActive: 4,
		Layers: 36, FullAttnLayers: 18, SWALayers: 18, SWAWindow: 128, KVHeads: 8, HeadDim: 64, QHeads: 64,
		NativeCtx: 131072, Tools: true, Thinking: true,
		Quants: []Quant{
			{Name: "MXFP4", Format: "gguf", FileBytes: 63_372_000_000, ReadBytesPerToken: 3_530_000_000},
		},
		OpenRouterID: "openai/gpt-oss-120b", CloudPrecision: "MXFP4 (native release format)", QuantMatchesCloud: true,
		HFGGUFRepo: "ggml-org/gpt-oss-120b-GGUF",
	},
	{
		ID: "qwen3-coder-30b-a3b", Name: "Qwen3-Coder 30B-A3B", OllamaTag: "qwen3-coder:30b",
		Aliases: []string{"qwen/qwen3-coder-30b-a3b-instruct", "qwen3-coder", "qwen3-coder-30b"},
		Family:  "qwen3moe", TotalParamsB: 30.53, ReadParamsB: 3.04, MatmulParamsB: 3.04, MoE: true, ExpertsTotal: 128, ExpertsActive: 8,
		Layers: 48, FullAttnLayers: 48, KVHeads: 4, HeadDim: 128, QHeads: 32, DecodeEff: 0.72,
		NativeCtx: 262144, Tools: true,
		// Low-bit sizes from the Hugging Face file listing of the repo below.
		Quants: []Quant{gq("Q4_K_M", 18.6), gq("Q6_K", 25.1), gq("Q8_0", 32.5), est("MLX-4bit", 30.53),
			gq("IQ4_XS", 16.38), gqt("Q3_K_XL", "UD-Q3_K_XL", 13.81), gqt("IQ3_XXS", "UD-IQ3_XXS", 12.85)},
		OpenRouterID: "qwen/qwen3-coder-30b-a3b-instruct", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF", HFMLXRepo: "mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit",
	},
	{
		ID: "qwen3-32b", Name: "Qwen3 32B", OllamaTag: "qwen3:32b", Aliases: []string{"qwen/qwen3-32b"},
		Family: "qwen3", TotalParamsB: 32.76, ReadParamsB: 31.98, MatmulParamsB: 31.98,
		Layers: 64, FullAttnLayers: 64, KVHeads: 8, HeadDim: 128, QHeads: 64,
		NativeCtx: 40960, Tools: true, Thinking: true,
		Quants:       []Quant{gq("Q4_K_M", 19.8), gq("Q8_0", 34.8), est("MLX-4bit", 32.76)},
		OpenRouterID: "qwen/qwen3-32b", CloudPrecision: "BF16/FP8 (provider-dependent)", HFGGUFRepo: "Qwen/Qwen3-32B-GGUF",
	},
	{
		ID: "qwen3-14b", Name: "Qwen3 14B", OllamaTag: "qwen3:14b", Aliases: []string{"qwen/qwen3-14b"},
		Family: "qwen3", TotalParamsB: 14.77, ReadParamsB: 13.99, MatmulParamsB: 13.99,
		Layers: 40, FullAttnLayers: 40, KVHeads: 8, HeadDim: 128, QHeads: 40,
		NativeCtx: 40960, Tools: true, Thinking: true,
		Quants:       []Quant{gq("Q4_K_M", 9.0), gq("Q8_0", 15.7), est("MLX-4bit", 14.77)},
		OpenRouterID: "qwen/qwen3-14b", CloudPrecision: "BF16/FP8 (provider-dependent)", HFGGUFRepo: "Qwen/Qwen3-14B-GGUF",
	},
	{
		ID: "qwen3-8b", Name: "Qwen3 8B", OllamaTag: "qwen3:8b", Aliases: []string{"qwen/qwen3-8b"},
		Family: "qwen3", TotalParamsB: 8.19, ReadParamsB: 7.57, MatmulParamsB: 7.57,
		Layers: 36, FullAttnLayers: 36, KVHeads: 8, HeadDim: 128, QHeads: 32,
		NativeCtx: 40960, Tools: true, Thinking: true,
		Quants:       []Quant{gq("Q4_K_M", 5.03), gq("Q8_0", 8.71), est("MLX-4bit", 8.19)},
		OpenRouterID: "qwen/qwen3-14b", TrialStandIn: "Qwen3 14B, the same family one size up, so trial answers are a little better than the local model", CloudPrecision: "BF16/FP8 (provider-dependent)", HFGGUFRepo: "Qwen/Qwen3-8B-GGUF",
	},
	{
		ID: "llama3.1-8b", Name: "Llama 3.1 8B Instruct", OllamaTag: "llama3.1:8b", Aliases: []string{"meta-llama/llama-3.1-8b-instruct", "llama3.1"},
		Family: "llama", TotalParamsB: 8.03, ReadParamsB: 7.50, MatmulParamsB: 7.50,
		Layers: 32, FullAttnLayers: 32, KVHeads: 8, HeadDim: 128, QHeads: 32,
		NativeCtx: 131072, Tools: true,
		Quants:       []Quant{gq("Q4_K_M", 4.92), gq("Q8_0", 8.54), est("MLX-4bit", 8.03)},
		OpenRouterID: "meta-llama/llama-3.1-8b-instruct", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "bartowski/Meta-Llama-3.1-8B-Instruct-GGUF",
	},
	{
		ID: "llama3.3-70b", Name: "Llama 3.3 70B Instruct", OllamaTag: "llama3.3:70b", Aliases: []string{"meta-llama/llama-3.3-70b-instruct", "llama3.3"},
		Family: "llama", TotalParamsB: 70.55, ReadParamsB: 69.5, MatmulParamsB: 69.5,
		Layers: 80, FullAttnLayers: 80, KVHeads: 8, HeadDim: 128, QHeads: 64,
		NativeCtx: 131072, Tools: true,
		Quants:       []Quant{gq("Q4_K_M", 42.5), gq("Q8_0", 75.0), est("MLX-4bit", 70.55)},
		OpenRouterID: "meta-llama/llama-3.3-70b-instruct", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "bartowski/Llama-3.3-70B-Instruct-GGUF",
	},
	{
		ID: "gemma3-27b", Name: "Gemma 3 27B", OllamaTag: "gemma3:27b", Aliases: []string{"google/gemma-3-27b-it"},
		Family: "gemma3", TotalParamsB: 27.0, ReadParamsB: 27.0, MatmulParamsB: 27.0,
		Layers: 62, FullAttnLayers: 10, SWALayers: 52, SWAWindow: 1024, KVHeads: 16, HeadDim: 128, QHeads: 32,
		NativeCtx: 131072, Tools: false,
		Quants:       []Quant{gq("Q4_K_M", 16.5), gq("Q8_0", 28.7), est("MLX-4bit", 27.0)},
		OpenRouterID: "google/gemma-3-27b-it", CloudPrecision: "BF16 (provider-dependent)", HFGGUFRepo: "ggml-org/gemma-3-27b-it-GGUF",
		Notes: []string{"No native tool calling in common local runtimes: coding agents that need tools will fail."},
	},
	{
		ID: "gemma3-12b", Name: "Gemma 3 12B", OllamaTag: "gemma3:12b", Aliases: []string{"google/gemma-3-12b-it"},
		Family: "gemma3", TotalParamsB: 11.8, ReadParamsB: 11.8, MatmulParamsB: 11.8,
		Layers: 48, FullAttnLayers: 8, SWALayers: 40, SWAWindow: 1024, KVHeads: 8, HeadDim: 256, QHeads: 16,
		NativeCtx: 131072, Tools: false,
		Quants:       []Quant{gq("Q4_K_M", 7.3), gq("Q8_0", 12.5), est("MLX-4bit", 11.8)},
		OpenRouterID: "google/gemma-3-12b-it", CloudPrecision: "BF16 (provider-dependent)", HFGGUFRepo: "ggml-org/gemma-3-12b-it-GGUF",
		Notes: []string{"No native tool calling in common local runtimes: coding agents that need tools will fail."},
	},
	{
		ID: "devstral-small-24b", Name: "Devstral Small 24B", OllamaTag: "devstral:24b", Aliases: []string{"mistralai/devstral-small", "devstral"},
		Family: "llama", TotalParamsB: 23.57, ReadParamsB: 22.9, MatmulParamsB: 22.9,
		Layers: 40, FullAttnLayers: 40, KVHeads: 8, HeadDim: 128, QHeads: 32,
		NativeCtx: 131072, Tools: true,
		Quants:       []Quant{gq("Q4_K_M", 14.3), gq("Q8_0", 25.1), est("MLX-4bit", 23.57)},
		OpenRouterID: "mistralai/devstral-2512", TrialStandIn: "Devstral 2, the larger model of the same family, so trial answers are better than the local model", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "unsloth/Devstral-Small-2507-GGUF",
	},
	{
		ID: "qwen2.5-coder-32b", Name: "Qwen2.5-Coder 32B", OllamaTag: "qwen2.5-coder:32b", Aliases: []string{"qwen/qwen-2.5-coder-32b-instruct"},
		Family: "qwen2", TotalParamsB: 32.76, ReadParamsB: 31.98, MatmulParamsB: 31.98,
		Layers: 64, FullAttnLayers: 64, KVHeads: 8, HeadDim: 128, QHeads: 40,
		NativeCtx: 32768, Tools: true,
		Quants:       []Quant{gq("Q4_K_M", 19.9), gq("Q8_0", 34.8), est("MLX-4bit", 32.76)},
		OpenRouterID: "qwen/qwen-2.5-coder-32b-instruct", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "Qwen/Qwen2.5-Coder-32B-Instruct-GGUF",
	},
	{
		ID: "qwen2.5-coder-7b", Name: "Qwen2.5-Coder 7B", OllamaTag: "qwen2.5-coder:7b", Aliases: []string{"qwen/qwen2.5-coder-7b-instruct"},
		Family: "qwen2", TotalParamsB: 7.62, ReadParamsB: 7.07, MatmulParamsB: 7.07,
		Layers: 28, FullAttnLayers: 28, KVHeads: 4, HeadDim: 128, QHeads: 28,
		NativeCtx: 32768, Tools: true,
		Quants:       []Quant{gq("Q4_K_M", 4.68), gq("Q8_0", 8.1), est("MLX-4bit", 7.62)},
		OpenRouterID: "qwen/qwen3-coder-30b-a3b-instruct", TrialStandIn: "Qwen3 Coder 30B-A3B, a newer and larger coding model, so trial answers are better than the local model", CloudPrecision: "BF16/FP8 (provider-dependent)",
		HFGGUFRepo: "Qwen/Qwen2.5-Coder-7B-Instruct-GGUF",
	},
}

func init() {
	var gen []Model
	if err := json.Unmarshal(generatedJSON, &gen); err != nil {
		panic("catalog: invalid models_gguf.json: " + err.Error())
	}
	have := map[string]bool{}
	for _, m := range models {
		have[m.ID] = true
	}
	for _, m := range gen {
		if !have[m.ID] {
			models = append(models, m)
		}
	}
}

// All returns catalog models sorted by total size.
func All() []Model {
	out := append([]Model(nil), models...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].TotalParamsB < out[j].TotalParamsB })
	return out
}

// Lookup finds a model by id, Ollama tag, alias, or OpenRouter id
// (case-insensitive). It also accepts a "name:quant" suffix such as
// "qwen3-coder:30b-q8_0" by ignoring the quant part.
func Lookup(name string) (Model, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	for _, m := range models {
		if matches(m, n) {
			return m, true
		}
	}
	// Ollama tags with quant suffixes: "qwen3:8b-q8_0".
	if i := strings.LastIndex(n, "-"); i > 0 && strings.Contains(n, ":") {
		base := n[:i]
		for _, m := range models {
			if matches(m, base) {
				return m, true
			}
		}
	}
	return Model{}, false
}

func matches(m Model, n string) bool {
	if n == strings.ToLower(m.ID) || n == strings.ToLower(m.OllamaTag) || (m.OpenRouterID != "" && n == strings.ToLower(m.OpenRouterID)) {
		return true
	}
	for _, a := range m.Aliases {
		if n == strings.ToLower(a) {
			return true
		}
	}
	return false
}

// Quant returns the named quant (case-insensitive) or the default when name
// is empty.
func (m Model) Quant(name string) (Quant, error) {
	if name == "" {
		return m.Quants[0], nil
	}
	for _, q := range m.Quants {
		if strings.EqualFold(q.Name, name) {
			return q, nil
		}
	}
	names := make([]string, len(m.Quants))
	for i, q := range m.Quants {
		names[i] = q.Name
	}
	return Quant{}, fmt.Errorf("model %s has no quant %q (available: %s)", m.ID, name, strings.Join(names, ", "))
}

// QuantFor returns the default quant in the given format ("gguf" or "mlx").
func (m Model) QuantFor(format string) (Quant, bool) {
	for _, q := range m.Quants {
		if q.Format == format {
			return q, true
		}
	}
	return Quant{}, false
}

// ReadBytes returns bytes read from weights per decoded token for q.
func (m Model) ReadBytes(q Quant) float64 {
	if q.ReadBytesPerToken > 0 {
		return float64(q.ReadBytesPerToken)
	}
	return float64(q.FileBytes) * m.ReadParamsB / m.TotalParamsB
}

// KVBytesPerToken is the KV-cache size of one token across full-attention
// layers for a cache element size (2 = f16, 1.0625 = q8_0, 0.5625 = q4_0).
func (m Model) KVBytesPerToken(elemBytes float64) float64 {
	if m.KVPerTokenF16 > 0 {
		return m.KVPerTokenF16 * elemBytes / 2
	}
	return 2 * float64(m.FullAttnLayers*m.KVHeads*m.HeadDim) * elemBytes
}

// KVCacheBytes is the KV cache allocated for ctx tokens: full-attention
// layers hold every token, sliding-window layers hold only the window.
func (m Model) KVCacheBytes(ctx int, elemBytes float64) float64 {
	win := m.SWAWindow
	if ctx < win {
		win = ctx
	}
	if m.KVPerTokenF16 > 0 || m.SWAKVPerTokenF16 > 0 {
		return (m.KVPerTokenF16*float64(ctx) + m.SWAKVPerTokenF16*float64(win)) * elemBytes / 2
	}
	perLayerTok := 2 * float64(m.KVHeads*m.HeadDim) * elemBytes
	return perLayerTok * (float64(m.FullAttnLayers)*float64(ctx) + float64(m.SWALayers)*float64(win))
}

// KVReadBytes is the KV bytes read to decode one token at depth d.
func (m Model) KVReadBytes(d int, elemBytes float64) float64 {
	return m.KVCacheBytes(d, elemBytes)
}

// AttnFLOPs is the attention FLOPs (scores + values) for one token
// attending to ctx earlier tokens.
func (m Model) AttnFLOPs(ctx int) float64 {
	win := m.SWAWindow
	if ctx < win {
		win = ctx
	}
	perLayer := 4 * float64(m.QHeads*m.HeadDim)
	return perLayer * (float64(m.FullAttnLayers)*float64(ctx) + float64(m.SWALayers)*float64(win))
}

// ParamLabel is a display size such as "20.9B".
func (m Model) ParamLabel() string {
	return fmt.Sprintf("%.1fB", m.TotalParamsB)
}
