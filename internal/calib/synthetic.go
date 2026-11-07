package calib

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/gguf"
)

// GeneratorVersion changes whenever tensor layouts or payloads change.
const GeneratorVersion = 3

// Shape preserves the geometry of a published architecture, including its head.
type Shape struct {
	ID, Architecture, Class                                                  string
	Hidden, Heads, KVHeads, HeadDim, FFN, Experts, Active, Vocab, RealLayers int
	Counts                                                                   []int
}

// Shapes are text-backbone shapes from the official model config.json files.
// Qwen hybrid counts contain complete four-layer attention/recurrent cycles.
var Shapes = []Shape{
	{"qwen3-8b", "qwen3", "dense", 4096, 32, 8, 128, 12288, 0, 0, 151936, 36, []int{2, 6}},
	{"qwen3-30b-a3b", "qwen3moe", "moe", 2048, 32, 4, 128, 768, 128, 8, 151936, 48, []int{2, 6}},
	{"qwen3.6-35b-a3b", "qwen35moe", "hybrid", 2048, 16, 2, 256, 512, 256, 8, 248320, 40, []int{4, 8}},
	{"gpt-oss-20b", "gpt-oss", "sliding", 2880, 64, 8, 64, 2880, 32, 4, 201088, 24, []int{2, 6}},
	{"gemma4-26b-a4b", "gemma4", "gemma", 2816, 16, 8, 256, 704, 128, 8, 262144, 30, []int{6, 12}},
}

// Probe is one synthetic GGUF, with the quant-kernel class it measures.
type Probe struct {
	Shape  Shape
	Quant  string
	Layers int
	Check  bool
}

func (p Probe) ID() string {
	return fmt.Sprintf("v%d-%s-%s-%d", GeneratorVersion, p.Shape.ID, p.Quant, p.Layers)
}

// Grid separates the quant sweep from architecture probes. Quick covers the
// two default MoE picks; full also covers Gemma and dense kernels.
func Grid(quick bool) []Probe {
	var ps []Probe
	add := func(s Shape, q string, counts []int, check bool) {
		for _, n := range counts {
			ps = append(ps, Probe{s, q, n, check})
		}
	}
	add(Shapes[2], "IQ3_XXS", Shapes[2].Counts, false)
	add(Shapes[3], "MXFP4", Shapes[3].Counts, false)
	add(Shapes[2], "IQ3_XXS", []int{12}, true)
	add(Shapes[3], "MXFP4", []int{4}, true)
	if quick {
		return ps
	}
	add(Shapes[4], "Q4_K_M", Shapes[4].Counts, false)
	add(Shapes[4], "Q4_K_M", []int{18}, true)
	for _, q := range []string{"Q4_K_M", "IQ3_XXS", "IQ4_XS", "MXFP4", "Q8_0"} {
		add(Shapes[0], q, Shapes[0].Counts, false)
		add(Shapes[0], q, []int{4}, true)
	}
	add(Shapes[1], "Q4_K_M", Shapes[1].Counts, false)
	add(Shapes[1], "Q4_K_M", []int{4}, true)
	return ps
}

func quantType(q string) gguf.GGMLType {
	switch q {
	case "Q4_K_M":
		return gguf.TypeQ4_K
	case "IQ3_XXS":
		return gguf.TypeIQ3_XXS
	case "IQ4_XS":
		return gguf.TypeIQ4_XS
	case "MXFP4":
		return gguf.TypeMXFP4
	default:
		return gguf.TypeQ8_0
	}
}

// Layout returns metadata, tensors, and a predictor model with exact active
// tensor bytes. It never allocates weights in memory.
func (p Probe) Layout() (map[string]any, []gguf.WriteTensor, catalog.Model, catalog.Quant) {
	s := p.Shape
	a := s.Architecture
	meta := map[string]any{"general.architecture": a, "general.name": "triaLM synthetic calibration", "general.file_type": uint32(15),
		a + ".context_length": uint32(131072), a + ".embedding_length": uint32(s.Hidden), a + ".block_count": uint32(p.Layers), a + ".feed_forward_length": uint32(s.FFN),
		a + ".attention.head_count": uint32(s.Heads), a + ".attention.head_count_kv": uint32(s.KVHeads), a + ".attention.key_length": uint32(s.HeadDim), a + ".attention.value_length": uint32(s.HeadDim),
		a + ".attention.layer_norm_rms_epsilon": float32(1e-6), a + ".rope.dimension_count": uint32(s.HeadDim), a + ".rope.freq_base": float32(1000000),
		"tokenizer.ggml.model": "llama", "tokenizer.ggml.bos_token_id": uint32(1), "tokenizer.ggml.eos_token_id": uint32(2), "tokenizer.ggml.unknown_token_id": uint32(0), "tokenizer.ggml.padding_token_id": uint32(3), "tokenizer.ggml.add_bos_token": true}
	meta["general.file_type"] = map[string]uint32{"Q4_K_M": 15, "IQ3_XXS": 23, "IQ4_XS": 30, "MXFP4": 38, "Q8_0": 7}[p.Quant]
	meta["general.quantization_version"] = uint32(2)
	toks := make([]string, s.Vocab)
	scores := make([]float32, s.Vocab)
	types := make([]int32, s.Vocab)
	for i := range toks {
		toks[i] = fmt.Sprintf("▁t%d", i)
		types[i] = 1
	}
	toks[0] = "<unk>"
	types[0] = 2
	toks[1] = "<s>"
	types[1] = 3
	toks[2] = "</s>"
	types[2] = 3
	toks[3] = "<pad>"
	types[3] = 3
	for i := 0; i < 256; i++ {
		toks[i+4] = fmt.Sprintf("<0x%02X>", i)
		types[i+4] = 6
	}
	meta["tokenizer.ggml.tokens"] = toks
	meta["tokenizer.ggml.scores"] = scores
	meta["tokenizer.ggml.token_type"] = types
	if s.Experts > 0 {
		meta[a+".expert_count"] = uint32(s.Experts)
		meta[a+".expert_used_count"] = uint32(s.Active)
		meta[a+".expert_feed_forward_length"] = uint32(s.FFN)
	}
	var ts []gguf.WriteTensor
	var total, active, params, totalParams, readParams float64
	add := func(name string, typ gguf.GGMLType, dims ...int) {
		ds := make([]uint64, len(dims))
		n := uint64(1)
		for i, d := range dims {
			ds[i] = uint64(d)
			n *= uint64(d)
		}
		// Preserve the mixed default-pick kernel layouts audited from GGUF headers.
		if a == "qwen35moe" && p.Quant == "IQ3_XXS" && len(ds) > 1 && typ != gguf.TypeF32 {
			typ = gguf.TypeQ6_K
			if strings.Contains(name, "ffn_gate_exps") || strings.Contains(name, "ffn_up_exps") {
				typ = gguf.TypeIQ2_S
			}
			if strings.Contains(name, "ffn_down_exps") {
				typ = gguf.TypeIQ3_XXS
			}
		}
		// K-quants require 256-wide rows; Gemma's 704-wide down projections use Q8.
		tr, _ := typ.Traits()
		if ds[0]%tr.BlockSize != 0 {
			typ = gguf.TypeQ8_0
		}
		sz, _ := gguf.RowBytes(typ, n)
		frac := 1.0
		if strings.Contains(name, "_exps.") {
			frac = float64(s.Active) / float64(s.Experts)
		}
		total += float64(sz)
		totalParams += float64(n)
		if name != "token_embd.weight" {
			active += float64(sz) * frac
			readParams += float64(n) * frac
			if len(ds) > 1 {
				params += float64(n) * frac
			}
		}
		ts = append(ts, gguf.WriteTensor{Name: name, Dimensions: ds, Type: typ, Fill: payload(typ, name, len(ds) == 1)})
	}
	qt := quantType(p.Quant)
	head := gguf.TypeQ6_K
	if qt == gguf.TypeQ8_0 {
		head = qt
	}
	if a == "gpt-oss" {
		head = gguf.TypeQ8_0
	}
	add("token_embd.weight", head, s.Hidden, s.Vocab)
	add("output.weight", head, s.Hidden, s.Vocab)
	add("output_norm.weight", gguf.TypeF32, s.Hidden)
	full, swa, recur := p.Layers, 0, 0
	if a == "qwen35moe" {
		full = p.Layers / 4
		recur = p.Layers - full
		meta[a+".full_attention_interval"] = uint32(4)
		meta[a+".rope.freq_base"] = float32(10000000)
		meta[a+".rope.dimension_count"] = uint32(64)
		meta[a+".rope.dimension_sections"] = []uint32{11, 11, 10, 0}
		meta[a+".ssm.conv_kernel"] = uint32(4)
		meta[a+".ssm.inner_size"] = uint32(4096)
		meta[a+".ssm.state_size"] = uint32(128)
		meta[a+".ssm.time_step_rank"] = uint32(32)
		meta[a+".ssm.group_count"] = uint32(16)
		meta[a+".expert_shared_feed_forward_length"] = uint32(512)
	}
	if a == "gpt-oss" {
		full = p.Layers / 2
		swa = p.Layers - full
		meta[a+".rope.scaling.type"] = "yarn"
		meta[a+".rope.scaling.factor"] = float32(32)
		meta[a+".rope.scaling.original_context_length"] = uint32(4096)
		meta[a+".rope.freq_base"] = float32(150000)
		meta[a+".attention.layer_norm_rms_epsilon"] = float32(1e-5)
		meta[a+".attention.sliding_window"] = uint32(128)
		meta[a+".attention.sliding_window_pattern"] = uint32(2)
	}
	if a == "gemma4" {
		meta[a+".feed_forward_length"] = uint32(2112)
		full = p.Layers / 6
		swa = p.Layers - full
		pattern := make([]bool, p.Layers)
		kh := make([]uint32, p.Layers)
		for i := range pattern {
			pattern[i] = (i+1)%6 != 0
			kh[i] = 8
			if !pattern[i] {
				kh[i] = 2
			}
		}
		meta[a+".attention.sliding_window_pattern"] = pattern
		meta[a+".attention.head_count_kv"] = kh
		meta[a+".attention.sliding_window"] = uint32(1024)
		meta[a+".embedding_length_per_layer_input"] = uint32(0)
		meta[a+".attention.key_length"] = uint32(512)
		meta[a+".attention.value_length"] = uint32(512)
		meta[a+".attention.key_length_swa"] = uint32(256)
		meta[a+".attention.value_length_swa"] = uint32(256)
		meta[a+".rope.dimension_count"] = uint32(128)
	}
	for i := 0; i < p.Layers; i++ {
		prefix := fmt.Sprintf("blk.%d.", i)
		put := func(n string, t gguf.GGMLType, ds ...int) { add(prefix+n, t, ds...) }
		put("attn_norm.weight", gguf.TypeF32, s.Hidden)
		at := qt
		if a == "gpt-oss" {
			at = gguf.TypeQ8_0
		}
		if a == "qwen35moe" && (i+1)%4 != 0 {
			put("attn_qkv.weight", qt, s.Hidden, 8192)
			put("attn_gate.weight", qt, s.Hidden, 4096)
			put("ssm_conv1d.weight", gguf.TypeF32, 4, 8192)
			put("ssm_dt.bias", gguf.TypeF32, 32)
			put("ssm_a", gguf.TypeF32, 32)
			put("ssm_beta.weight", gguf.TypeF32, s.Hidden, 32)
			put("ssm_alpha.weight", gguf.TypeF32, s.Hidden, 32)
			put("ssm_norm.weight", gguf.TypeF32, 128)
			put("ssm_out.weight", qt, 4096, s.Hidden)
		} else {
			hd, kv := s.HeadDim, s.KVHeads
			if a == "gemma4" && (i+1)%6 == 0 {
				hd = 512
				kv = 2
			}
			qdim := hd * s.Heads
			if a == "qwen35moe" {
				qdim *= 2
			}
			put("attn_q.weight", at, s.Hidden, qdim)
			put("attn_k.weight", at, s.Hidden, hd*kv)
			if a != "gemma4" {
				put("attn_v.weight", at, s.Hidden, hd*kv)
			}
			put("attn_output.weight", at, hd*s.Heads, s.Hidden)
			if a == "gpt-oss" {
				put("attn_q.bias", gguf.TypeF32, qdim)
				put("attn_k.bias", gguf.TypeF32, hd*kv)
				put("attn_v.bias", gguf.TypeF32, hd*kv)
				put("attn_output.bias", gguf.TypeF32, s.Hidden)
				put("attn_sinks.weight", gguf.TypeF32, s.Heads)
			} else {
				put("attn_q_norm.weight", gguf.TypeF32, hd)
				put("attn_k_norm.weight", gguf.TypeF32, hd)
			}
			if a == "gemma4" && i == 5 {
				add("rope_freqs.weight", gguf.TypeF32, hd/2)
			}
		}
		norm := "ffn_norm.weight"
		if a == "qwen35moe" || a == "gpt-oss" {
			norm = "post_attention_norm.weight"
		}
		put(norm, gguf.TypeF32, s.Hidden)
		if s.Experts > 0 {
			put("ffn_gate_inp.weight", gguf.TypeF32, s.Hidden, s.Experts)
			put("ffn_gate_exps.weight", qt, s.Hidden, s.FFN, s.Experts)
			put("ffn_up_exps.weight", qt, s.Hidden, s.FFN, s.Experts)
			put("ffn_down_exps.weight", qt, s.FFN, s.Hidden, s.Experts)
			if a == "gpt-oss" {
				put("ffn_gate_inp.bias", gguf.TypeF32, s.Experts)
				put("ffn_gate_exps.bias", gguf.TypeF32, s.FFN, s.Experts)
				put("ffn_up_exps.bias", gguf.TypeF32, s.FFN, s.Experts)
				put("ffn_down_exps.bias", gguf.TypeF32, s.Hidden, s.Experts)
			}
			if a == "qwen35moe" {
				put("ffn_gate_inp_shexp.weight", gguf.TypeF32, s.Hidden)
				put("ffn_gate_shexp.weight", qt, s.Hidden, 512)
				put("ffn_up_shexp.weight", qt, s.Hidden, 512)
				put("ffn_down_shexp.weight", qt, 512, s.Hidden)
			}
		} else {
			put("ffn_gate.weight", qt, s.Hidden, s.FFN)
			put("ffn_up.weight", qt, s.Hidden, s.FFN)
			put("ffn_down.weight", qt, s.FFN, s.Hidden)
		}
		if a == "gemma4" {
			put("post_attention_norm.weight", gguf.TypeF32, s.Hidden)
			put("post_ffw_norm.weight", gguf.TypeF32, s.Hidden)
			put("ffn_gate_inp.scale", gguf.TypeF32, s.Hidden)
			for _, n := range []string{"pre_ffw_norm_2.weight", "post_ffw_norm_1.weight", "post_ffw_norm_2.weight"} {
				put(n, gguf.TypeF32, s.Hidden)
			}
			put("ffn_gate.weight", qt, s.Hidden, 2112)
			put("ffn_up.weight", qt, s.Hidden, 2112)
			put("ffn_down.weight", qt, 2112, s.Hidden)
		}
	}
	m := catalog.Model{ID: p.ID(), Family: a, Name: p.ID(), Layers: p.Layers, FullAttnLayers: full, SWALayers: swa, RecurrentLayers: recur, KVHeads: s.KVHeads, HeadDim: s.HeadDim, QHeads: s.Heads, MoE: s.Experts > 0, ExpertsTotal: s.Experts, ExpertsActive: s.Active, TotalParamsB: totalParams / 1e9, ReadParamsB: readParams / 1e9, MatmulParamsB: params / 1e9, NativeCtx: 131072, Tools: true}
	if a == "gpt-oss" {
		m.SWAWindow = 128
	}
	if a == "gemma4" {
		m.SWAWindow = 1024
		m.KVPerTokenF16 = float64(full * 2 * 2 * 512 * 2)
		m.SWAKVPerTokenF16 = float64(swa * 2 * 8 * 256 * 2)
	}
	if recur > 0 {
		m.RecurrentStateBytes = float64(recur) * 2195456
	}
	q := catalog.Quant{Name: p.Quant, Format: "gguf", FileBytes: int64(total), ReadBytesPerToken: int64(active)}
	if original, ok := catalog.Lookup(s.ID); ok {
		m.DecodeEff = original.DecodeEff
	}
	m.Quants = []catalog.Quant{q}
	return meta, ts, m, q
}

func payload(t gguf.GGMLType, name string, vector bool) func(context.Context, io.Writer, uint64) error {
	return func(ctx context.Context, w io.Writer, n uint64) error {
		tr, _ := t.Traits()
		buf := make([]byte, int(tr.TypeSize)*4096)
		seed := uint64(0x9e3779b97f4a7c15)
		for _, ch := range name {
			seed = (seed ^ uint64(ch)) * 1099511628211
		}
		for n > 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
			// Fresh deterministic PRNG bytes in each chunk, with no repeated
			// expert matrices or identical routing weights between layers.
			for i := 0; i+8 <= len(buf); i += 8 {
				seed ^= seed << 13
				seed ^= seed >> 7
				seed ^= seed << 17
				binary.LittleEndian.PutUint64(buf[i:], seed)
			}
			for off := 0; off < len(buf); off += int(tr.TypeSize) {
				b := buf[off:]
				switch t {
				case gguf.TypeF32:
					v := float32(int8(b[0])) / 128000
					if vector && (strings.Contains(name, "norm") || strings.Contains(name, "scale") || strings.Contains(name, "rope_freqs")) {
						v = 1
					}
					if strings.HasSuffix(name, "ssm_a") {
						v = -1
					}
					binary.LittleEndian.PutUint32(b, math.Float32bits(v))
				case gguf.TypeF16:
					binary.LittleEndian.PutUint16(b, 0x1400|uint16(b[0]&0x80)<<8)
				case gguf.TypeBF16:
					binary.LittleEndian.PutUint16(b, 0x3a80|uint16(b[0]&0x80)<<8)
				case gguf.TypeQ4_K:
					binary.LittleEndian.PutUint16(b, 0x0800)
					binary.LittleEndian.PutUint16(b[2:], 0x0800)
				case gguf.TypeQ6_K:
					binary.LittleEndian.PutUint16(b[208:], 0x0800)
				case gguf.TypeMXFP4:
					b[0] = 112
				default:
					binary.LittleEndian.PutUint16(b, 0x1000)
				}
			}
			k := uint64(len(buf))
			if n < k {
				k = n
			}
			written, err := w.Write(buf[:k])
			if err != nil {
				return err
			}
			if written != int(k) {
				return io.ErrShortWrite
			}
			n -= k
		}
		return nil
	}
}

// Generate caches a complete file atomically; interrupted generation leaves no
// apparently valid cache entry. The version is part of the deterministic name.
func Generate(ctx context.Context, home string, p Probe) (string, error) {
	dir := filepath.Join(home, "cache", "calib")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, p.ID()+".gguf")
	if st, err := os.Stat(path); err == nil && st.Size() >= 256<<20 {
		return path, nil
	}
	f, err := os.CreateTemp(dir, ".generate-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	meta, ts, _, _ := p.Layout()
	err = gguf.Write(ctx, f, meta, ts)
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return "", err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return "", err
	}
	return path, nil
}
