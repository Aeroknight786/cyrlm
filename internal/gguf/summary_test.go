package gguf

import (
	"bytes"
	"fmt"
	"reflect"
	"testing"
)

const llamaTemplate = "{% for m in messages %}{{ m.content }}{% endfor %}"

// (a) A tiny llama-style dense model: 2 blocks, 4 heads / 2 KV heads (GQA),
// Q4_K / Q6_K / Q8_0 / F16 / F32 tensors.
func llamaBuilder() builder {
	b := builder{kvs: []kv{
		{"general.architecture", "llama"},
		{"general.name", "Tiny Llama"},
		{"general.file_type", uint32(15)},
		{"general.quantization_version", uint32(2)},
		{"llama.context_length", uint32(4096)},
		{"llama.embedding_length", uint32(256)},
		{"llama.block_count", uint32(2)},
		{"llama.feed_forward_length", uint32(512)},
		{"llama.attention.head_count", uint32(4)},
		{"llama.attention.head_count_kv", uint32(2)},
		{"llama.rope.freq_base", float32(500000)},
		{"llama.rope.dimension_count", uint32(64)},
		{"tokenizer.ggml.model", "gpt2"},
		{"tokenizer.ggml.tokens", tokens(128)},
		{"tokenizer.chat_template", llamaTemplate},
	}}
	b.tensors = []tensorSpec{
		{"token_embd.weight", []uint64{256, 128}, TypeQ4_K},
		{"output_norm.weight", []uint64{256}, TypeF32},
		{"output.weight", []uint64{256, 128}, TypeQ6_K},
	}
	for l := 0; l < 2; l++ {
		p := fmt.Sprintf("blk.%d.", l)
		b.tensors = append(b.tensors,
			tensorSpec{p + "attn_norm.weight", []uint64{256}, TypeF32},
			tensorSpec{p + "attn_q.weight", []uint64{256, 256}, TypeQ4_K},
			tensorSpec{p + "attn_k.weight", []uint64{256, 128}, TypeQ4_K},
			tensorSpec{p + "attn_v.weight", []uint64{256, 128}, TypeF16},
			tensorSpec{p + "attn_output.weight", []uint64{256, 256}, TypeQ4_K},
			tensorSpec{p + "ffn_norm.weight", []uint64{256}, TypeF32},
			tensorSpec{p + "ffn_gate.weight", []uint64{256, 512}, TypeQ4_K},
			tensorSpec{p + "ffn_up.weight", []uint64{256, 512}, TypeQ4_K},
			tensorSpec{p + "ffn_down.weight", []uint64{512, 256}, TypeQ8_0},
		)
	}
	return b
}

// (b) A gpt-oss-style MoE: 4 blocks, 4 experts (2 active) in MXFP4,
// sliding window 128 alternating with full attention.
func gptOSSBuilder(extra ...kv) builder {
	b := builder{kvs: append([]kv{
		{"general.architecture", "gpt-oss"},
		{"general.file_type", uint32(38)},
		{"gpt-oss.context_length", uint32(131072)},
		{"gpt-oss.embedding_length", uint32(64)},
		{"gpt-oss.block_count", uint32(4)},
		{"gpt-oss.feed_forward_length", uint32(128)},
		{"gpt-oss.expert_feed_forward_length", uint32(128)},
		{"gpt-oss.attention.head_count", uint32(4)},
		{"gpt-oss.attention.head_count_kv", uint32(2)},
		{"gpt-oss.attention.key_length", uint32(16)},
		{"gpt-oss.attention.value_length", uint32(16)},
		{"gpt-oss.attention.sliding_window", uint32(128)},
		{"gpt-oss.expert_count", uint32(4)},
		{"gpt-oss.expert_used_count", uint32(2)},
		{"gpt-oss.rope.freq_base", float32(150000)},
		{"gpt-oss.rope.scaling.type", "yarn"},
		{"gpt-oss.rope.scaling.factor", float32(32)},
		{"gpt-oss.rope.scaling.original_context_length", uint32(4096)},
		{"tokenizer.ggml.tokens", tokens(100)},
	}, extra...)}
	b.tensors = []tensorSpec{
		{"token_embd.weight", []uint64{64, 100}, TypeQ8_0}, // 6800
		{"output_norm.weight", []uint64{64}, TypeF32},      // 256
		{"output.weight", []uint64{64, 100}, TypeQ8_0},     // 6800
	}
	for l := 0; l < 4; l++ {
		p := fmt.Sprintf("blk.%d.", l)
		b.tensors = append(b.tensors,
			tensorSpec{p + "attn_norm.weight", []uint64{64}, TypeF32},               // 256
			tensorSpec{p + "attn_q.weight", []uint64{64, 64}, TypeQ8_0},             // 4352
			tensorSpec{p + "attn_k.weight", []uint64{64, 32}, TypeQ8_0},             // 2176
			tensorSpec{p + "attn_v.weight", []uint64{64, 32}, TypeQ8_0},             // 2176
			tensorSpec{p + "attn_output.weight", []uint64{64, 64}, TypeQ8_0},        // 4352
			tensorSpec{p + "attn_sinks.weight", []uint64{4}, TypeF32},               // 16
			tensorSpec{p + "post_attention_norm.weight", []uint64{64}, TypeF32},     // 256
			tensorSpec{p + "ffn_gate_inp.weight", []uint64{64, 4}, TypeF32},         // 1024
			tensorSpec{p + "ffn_gate_exps.weight", []uint64{64, 128, 4}, TypeMXFP4}, // 17408
			tensorSpec{p + "ffn_up_exps.weight", []uint64{64, 128, 4}, TypeMXFP4},   // 17408
			tensorSpec{p + "ffn_down_exps.weight", []uint64{128, 64, 4}, TypeMXFP4}, // 17408
			tensorSpec{p + "ffn_gate_exps.bias", []uint64{128, 4}, TypeF32},         // 2048
		)
	}
	return b
}

func summarize(t *testing.T, b builder) *Summary {
	t.Helper()
	f, err := Parse(bytes.NewReader(b.bytes(t)), -1)
	if err != nil {
		t.Fatal(err)
	}
	return f.Summary()
}

func kinds(s *Summary) string {
	out := make([]byte, len(s.Layers))
	for i, l := range s.Layers {
		switch {
		case l.Recurrent && l.Attention == AttnNone:
			out[i] = 'R'
		case l.Recurrent:
			out[i] = 'H' // attention + recurrent in the same layer
		case l.Attention == AttnFull:
			out[i] = 'F'
		case l.Attention == AttnSliding:
			out[i] = 'S'
		default:
			out[i] = '-'
		}
	}
	return string(out)
}

func TestGPTOSSMoE(t *testing.T) {
	s := summarize(t, gptOSSBuilder())
	const nonExpert = 6800 + 256 + 6800 + 4*(256+4352+2176+2176+4352+16+256+1024) // 72288
	const expert = 4 * (3*17408 + 2048)                                           // 217088
	const params = 6400 + 64 + 6400 + 4*(64+4096+2048+2048+4096+4+64+256) + 4*(3*32768+512)
	checks := []struct {
		name      string
		got, want any
	}{
		{"label", s.FileTypeLabel, "MXFP4"},
		{"dominant", s.DominantType, "MXFP4"},
		{"MXFP4 bytes", s.TypeBytes["MXFP4"], uint64(12 * 17408)},
		{"experts", s.ExpertCount, uint64(4)},
		{"used", s.ExpertUsedCount, uint64(2)},
		{"expert ff", s.ExpertFeedForwardLength, uint64(128)},
		{"bytes", s.TensorBytes, uint64(nonExpert + expert)},
		{"expert bytes", s.ExpertBytes, uint64(expert)},
		{"non-expert bytes", s.NonExpertBytes, uint64(nonExpert)},
		{"embedding bytes", s.EmbeddingBytes, uint64(6800)},
		// non-expert minus the embedding lookup, plus half the experts.
		{"active bytes", s.ActiveBytes, uint64(nonExpert - 6800 + expert*2/4)},
		{"params", s.ParameterCount, uint64(params)},
		{"active params", s.ActiveParams, uint64(params - 6400 - 4*(3*32768+512)/2)},
		{"key len", s.KeyLength, uint64(16)},
		{"window", s.SlidingWindow, uint64(128)},
		{"pattern source", s.SlidingPatternSource, "convention:gpt-oss"},
		{"layout", kinds(s), "SFSF"},
		{"full", s.FullAttentionLayers, 2},
		{"sliding", s.SlidingLayers, 2},
		// 2 full layers × 2 KV heads × (16+16) × 2 bytes.
		{"kv f16", s.KVBytesPerTokenF16, uint64(256)},
		// 32-element rows: one 34-byte Q8_0 block each for K and V.
		{"kv q8_0", s.KVBytesPerTokenQ8_0, uint64(2 * 2 * 34)},
		{"sliding per token", s.SlidingKVBytesPerTokenF16, uint64(256)},
		{"sliding window f16", s.SlidingKVBytesF16, uint64(128 * 256)},
		{"sliding window q8_0", s.SlidingKVBytesQ8_0, uint64(128 * 136)},
		{"kv cache 1000", s.KVCacheBytes(1000, TypeF16), uint64(1000*256 + 128*256)},
		{"kv cache 64", s.KVCacheBytes(64, TypeF16), uint64(64*256 + 64*256)},
		{"rope type", s.RopeScalingType, "yarn"},
		{"rope factor", s.RopeScalingFactor, 32.0},
		{"rope orig ctx", s.RopeOriginalContextLength, uint64(4096)},
		{"vocab", s.VocabSize, uint64(100)},
		{"ctx", s.ContextLength, uint64(131072)},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if !hasNote(s.Notes, "MoE") || !hasNote(s.Notes, "convention") {
		t.Errorf("notes %q", s.Notes)
	}

	// An explicit per-layer pattern overrides the convention.
	s = summarize(t, gptOSSBuilder(kv{"gpt-oss.attention.sliding_window_pattern", []bool{false, true, true, false}}))
	if kinds(s) != "FSSF" || s.SlidingPatternSource != "gpt-oss.attention.sliding_window_pattern" {
		t.Errorf("explicit pattern: %s from %q", kinds(s), s.SlidingPatternSource)
	}
}

func TestSlidingConventions(t *testing.T) {
	attn := func(arch string, n int, extra ...kv) builder {
		return builder{kvs: append([]kv{
			{"general.architecture", arch},
			{arch + ".block_count", uint32(n)},
			{arch + ".embedding_length", uint32(128)},
			{arch + ".attention.head_count", uint32(4)},
			{arch + ".attention.head_count_kv", uint32(2)},
		}, extra...)}
	}
	cases := []struct {
		name   string
		b      builder
		layout string
		window uint64
		note   string
	}{
		{"gemma3 5:1", attn("gemma3", 12, kv{"gemma3.attention.sliding_window", uint32(512)}), "SSSSSFSSSSSF", 512, "convention"},
		{"gemma2 alternating", attn("gemma2", 4, kv{"gemma2.attention.sliding_window", uint32(4096)}), "SFSF", 4096, ""},
		{"cohere2 3:1", attn("cohere2", 8, kv{"cohere2.attention.sliding_window", uint32(4096)}), "SSSFSSSF", 4096, ""},
		{"llama4 chunked", attn("llama4", 8), "SSSFSSSF", 8192, "chunked"},
		{"llama4 swa disabled", attn("llama4", 4, kv{"llama4.attention.sliding_window", uint32(0)}), "FFFF", 0, ""},
		{"gemma3 without window", attn("gemma3", 6), "FFFFFF", 0, ""},
		{"unknown arch with window", attn("phi3", 4, kv{"phi3.attention.sliding_window", uint32(2048)}), "FFFF", 2048, "upper bound"},
		{"pattern as period", attn("gemma3", 6, kv{"gemma3.attention.sliding_window", uint32(512)}, kv{"gemma3.attention.sliding_window_pattern", uint32(3)}), "SSFSSF", 512, "period"},
		{"pattern as int array", attn("exaone4", 4, kv{"exaone4.attention.sliding_window", uint32(4096)}, kv{"exaone4.attention.sliding_window_pattern", []uint32{1, 1, 1, 0}}), "SSSF", 4096, ""},
		{"pattern bool scalar", attn("foo", 3, kv{"foo.attention.sliding_window", uint32(64)}, kv{"foo.attention.sliding_window_pattern", true}), "SSS", 64, ""},
		{"pattern without window", attn("foo", 2, kv{"foo.attention.sliding_window_pattern", []bool{true, false}}), "FF", 0, "sliding_window is 0"},
		{"short pattern padded", attn("foo", 3, kv{"foo.attention.sliding_window", uint32(64)}, kv{"foo.attention.sliding_window_pattern", []bool{false, true}}), "FSS", 64, "padded"},
		{"gemma3n shared kv", attn("gemma3n", 4, kv{"gemma3n.attention.sliding_window", uint32(512)}, kv{"gemma3n.attention.sliding_window_pattern", []bool{true, false, true, false}}, kv{"gemma3n.attention.shared_kv_layers", uint32(2)}), "SF--", 512, "reuse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := summarize(t, c.b)
			if kinds(s) != c.layout || s.SlidingWindow != c.window {
				t.Errorf("layout %s window %d, want %s %d (notes %q)", kinds(s), s.SlidingWindow, c.layout, c.window, s.Notes)
			}
			if c.note != "" && !hasNote(s.Notes, c.note) {
				t.Errorf("notes %q lack %q", s.Notes, c.note)
			}
			// 2 KV heads × head dim 32 (128/4) × K+V × 2 bytes per layer.
			if want := uint64(s.FullAttentionLayers * 2 * 32 * 2 * 2); s.KVBytesPerTokenF16 != want {
				t.Errorf("kv f16 %d, want %d", s.KVBytesPerTokenF16, want)
			}
		})
	}
	if s := summarize(t, cases[3].b); !s.SlidingChunked {
		t.Error("llama4 should be chunked")
	}
}

// (c) head_count_kv stored per layer.
func TestPerLayerKVHeads(t *testing.T) {
	s := summarize(t, builder{kvs: []kv{
		{"general.architecture", "llama"},
		{"llama.block_count", uint32(4)},
		{"llama.embedding_length", uint32(512)},
		{"llama.attention.head_count", uint32(8)},
		{"llama.attention.head_count_kv", []uint32{8, 8, 4, 2}},
		{"llama.feed_forward_length", []int32{1024, 2048, 2048, 1024}},
	}})
	if s.HeadCountKV != 8 || !reflect.DeepEqual(s.HeadCountKVPerLayer, []uint64{8, 8, 4, 2}) {
		t.Errorf("kv heads %d %v", s.HeadCountKV, s.HeadCountKVPerLayer)
	}
	if s.FeedForwardLength != 2048 || len(s.FeedForwardLengthPerLayer) != 4 {
		t.Errorf("ff %d %v", s.FeedForwardLength, s.FeedForwardLengthPerLayer)
	}
	// (8+8+4+2) heads × (64+64) × 2 bytes.
	if s.KVBytesPerTokenF16 != 22*128*2 || s.KeyLength != 64 {
		t.Errorf("kv f16 %d key len %d", s.KVBytesPerTokenF16, s.KeyLength)
	}
	if s.Layers[3].HeadCountKV != 2 || s.Layers[3].KElems != 2*64 {
		t.Errorf("layer 3 %+v", s.Layers[3])
	}
}

func TestHybridAndRecurrent(t *testing.T) {
	ssm := []kv{
		{"h.ssm.conv_kernel", uint32(4)}, {"h.ssm.inner_size", uint32(128)},
		{"h.ssm.state_size", uint32(16)}, {"h.ssm.group_count", uint32(1)},
	}
	// Jamba/granite-hybrid style: KV heads 0 on SSM layers.
	s := summarize(t, builder{kvs: append([]kv{
		{"general.architecture", "h"},
		{"h.block_count", uint32(4)},
		{"h.embedding_length", uint32(64)},
		{"h.attention.head_count", uint32(4)},
		{"h.attention.head_count_kv", []uint32{0, 4, 0, 2}},
	}, ssm...)})
	if kinds(s) != "RFRF" || s.RecurrentLayers != 2 || s.FullAttentionLayers != 2 {
		t.Errorf("layout %s", kinds(s))
	}
	// (4+2) KV heads × (16+16) × 2 bytes.
	if s.KVBytesPerTokenF16 != 6*32*2 {
		t.Errorf("kv f16 %d", s.KVBytesPerTokenF16)
	}
	// conv (4-1)×(128+2·1·16) + ssm 16×128 = 2528 F32 per layer.
	if s.RecurrentStateBytes != 2*2528*4 || s.Layers[0].StateElems != 2528 {
		t.Errorf("state %d (%d)", s.RecurrentStateBytes, s.Layers[0].StateElems)
	}

	// qwen3next: every full_attention_interval-th layer is attention.
	s = summarize(t, builder{kvs: []kv{
		{"general.architecture", "qwen3next"},
		{"qwen3next.block_count", uint32(8)},
		{"qwen3next.embedding_length", uint32(64)},
		{"qwen3next.attention.head_count", uint32(4)},
		{"qwen3next.attention.head_count_kv", uint32(2)},
		{"qwen3next.attention.key_length", uint32(32)},
		{"qwen3next.attention.value_length", uint32(32)},
		{"qwen3next.full_attention_interval", uint32(4)},
		{"qwen3next.ssm.conv_kernel", uint32(4)}, {"qwen3next.ssm.inner_size", uint32(128)},
		{"qwen3next.ssm.state_size", uint32(16)}, {"qwen3next.ssm.group_count", uint32(2)},
		{"qwen3next.ssm.time_step_rank", uint32(4)},
	}})
	if kinds(s) != "RRRFRRRF" || s.FullAttentionInterval != 4 || s.SSMTimeStepRank != 4 {
		t.Errorf("layout %s", kinds(s))
	}
	if s.KVBytesPerTokenF16 != 2*2*64*2 {
		t.Errorf("kv f16 %d", s.KVBytesPerTokenF16)
	}
	// conv 3×(128+2·2·16)=576 + ssm 16×128=2048 → 2624 F32 × 6 layers.
	if s.RecurrentStateBytes != 6*2624*4 {
		t.Errorf("state %d", s.RecurrentStateBytes)
	}

	// Pure mamba: no attention at all.
	s = summarize(t, builder{kvs: []kv{
		{"general.architecture", "mamba"}, {"mamba.block_count", uint32(2)}, {"mamba.embedding_length", uint32(64)},
		{"mamba.ssm.conv_kernel", uint32(4)}, {"mamba.ssm.inner_size", uint32(128)}, {"mamba.ssm.state_size", uint32(16)},
	}})
	if kinds(s) != "RR" || s.KVBytesPerTokenF16 != 0 || s.RecurrentStateBytes != 2*(3*128+16*128)*4 {
		t.Errorf("mamba layout %s kv %d state %d", kinds(s), s.KVBytesPerTokenF16, s.RecurrentStateBytes)
	}

	// falcon-h1: attention and SSM in every layer.
	s = summarize(t, builder{kvs: []kv{
		{"general.architecture", "falcon-h1"}, {"falcon-h1.block_count", uint32(2)}, {"falcon-h1.embedding_length", uint32(64)},
		{"falcon-h1.attention.head_count", uint32(4)}, {"falcon-h1.attention.head_count_kv", uint32(2)},
		{"falcon-h1.ssm.conv_kernel", uint32(4)}, {"falcon-h1.ssm.inner_size", uint32(128)}, {"falcon-h1.ssm.state_size", uint32(16)},
	}})
	if kinds(s) != "HH" || s.FullAttentionLayers != 2 || s.RecurrentLayers != 2 {
		t.Errorf("falcon-h1 layout %s", kinds(s))
	}

	// RWKV: recurrent only.
	s = summarize(t, builder{kvs: []kv{
		{"general.architecture", "rwkv7"}, {"rwkv7.block_count", uint32(3)}, {"rwkv7.embedding_length", uint32(64)},
		{"rwkv7.wkv.head_size", uint32(16)},
	}})
	if kinds(s) != "RRR" || s.RecurrentStateBytes != 3*(2*64+64*16)*4 {
		t.Errorf("rwkv layout %s state %d", kinds(s), s.RecurrentStateBytes)
	}
}

func TestMLAAndMTP(t *testing.T) {
	b := builder{kvs: []kv{
		{"general.architecture", "deepseek2"},
		{"deepseek2.block_count", uint32(3)},
		{"deepseek2.embedding_length", uint32(64)},
		{"deepseek2.attention.head_count", uint32(16)},
		{"deepseek2.attention.head_count_kv", uint32(1)},
		{"deepseek2.attention.key_length", uint32(576)},
		{"deepseek2.attention.value_length", uint32(512)},
		{"deepseek2.attention.key_length_mla", uint32(192)},
		{"deepseek2.attention.value_length_mla", uint32(128)},
		{"deepseek2.attention.kv_lora_rank", uint32(512)},
		{"deepseek2.attention.q_lora_rank", uint32(1536)},
		{"deepseek2.rope.dimension_count", uint32(64)},
		{"deepseek2.expert_count", uint32(8)},
		{"deepseek2.expert_used_count", uint32(2)},
		{"deepseek2.expert_shared_count", uint32(1)},
		{"deepseek2.leading_dense_block_count", uint32(1)},
		{"deepseek2.nextn_predict_layers", uint32(1)},
	}, tensors: []tensorSpec{
		{"token_embd.weight", []uint64{64, 10}, TypeF32},            // 2560
		{"output.weight", []uint64{64, 10}, TypeF32},                // 2560
		{"blk.0.ffn_up.weight", []uint64{64, 32}, TypeF32},          // 8192 dense
		{"blk.1.ffn_up_shexp.weight", []uint64{64, 32}, TypeF32},    // 8192 shared expert
		{"blk.1.ffn_up_exps.weight", []uint64{64, 32, 8}, TypeQ8_0}, // 17408
		{"blk.2.ffn_up_exps.weight", []uint64{64, 32, 8}, TypeQ8_0}, // 17408 (MTP)
		{"blk.2.eh_proj.weight", []uint64{128, 64}, TypeF32},        // 32768 (MTP)
		{"blk.1.ffn_gate.3.weight", []uint64{32, 2}, TypeF32},       // legacy per-expert: 256
	}}
	s := summarize(t, b)
	if s.KVLoraRank != 512 || s.KeyLengthMLA != 192 || s.ValueLengthMLA != 128 || s.QLoraRank != 1536 {
		t.Errorf("mla keys %+v", s)
	}
	// Latent cache: 1 KV head × (576 + 512) × 2 bytes × 3 layers.
	if s.KVBytesPerTokenF16 != 3*1088*2 || !hasNote(s.Notes, "MLA") {
		t.Errorf("kv f16 %d notes %q", s.KVBytesPerTokenF16, s.Notes)
	}
	const expert = 17408 + 17408 + 256
	const total = 2560 + 2560 + 8192 + 8192 + 32768 + expert
	if s.TensorBytes != total || s.ExpertBytes != expert {
		t.Errorf("bytes %d expert %d", s.TensorBytes, s.ExpertBytes)
	}
	// Non-expert minus embedding minus MTP block, plus 2/8 of non-MTP experts.
	want := uint64(total-expert-2560-32768) + uint64(17408+256)*2/8
	if s.ActiveBytes != want || !hasNote(s.Notes, "multi-token-prediction") {
		t.Errorf("active %d, want %d (notes %q)", s.ActiveBytes, want, s.Notes)
	}

	// Old DeepSeek2 conversion without MLA keys: full per-head cache.
	b.kvs = []kv{
		{"general.architecture", "deepseek2"}, {"deepseek2.block_count", uint32(1)},
		{"deepseek2.attention.head_count", uint32(16)}, {"deepseek2.attention.head_count_kv", uint32(16)},
		{"deepseek2.attention.key_length", uint32(192)}, {"deepseek2.attention.value_length", uint32(128)},
		{"deepseek2.attention.kv_lora_rank", uint32(512)},
	}
	if s := summarize(t, b); s.KVBytesPerTokenF16 != 16*320*2 || !hasNote(s.Notes, "without key_length_mla") {
		t.Errorf("old mla: kv %d notes %q", s.KVBytesPerTokenF16, s.Notes)
	}
}

func TestTiedEmbeddingsAndMissingMetadata(t *testing.T) {
	s := summarize(t, builder{tensors: []tensorSpec{
		{"token_embd.weight", []uint64{64, 10}, TypeF16},
		{"blk.0.attn_q.weight", []uint64{64, 64}, TypeF16},
	}})
	if !s.TiedEmbeddings || s.ActiveBytes != 1280+8192 || s.VocabSize != 10 {
		t.Errorf("tied %v active %d vocab %d", s.TiedEmbeddings, s.ActiveBytes, s.VocabSize)
	}
	if s.FileType != -1 || s.FileTypeLabel != "F16 (guessed)" || s.Architecture != "" {
		t.Errorf("file type %d %q", s.FileType, s.FileTypeLabel)
	}
	if !hasNote(s.Notes, "architecture") || !hasNote(s.Notes, "tied") || !hasNote(s.Notes, "guessed") {
		t.Errorf("notes %q", s.Notes)
	}
	// Experts without counts: everything counted active.
	s = summarize(t, builder{tensors: []tensorSpec{{"blk.0.ffn_up_exps.weight", []uint64{32, 4, 2}, TypeF32}}})
	if s.ActiveBytes != 1024 || !hasNote(s.Notes, "all experts") {
		t.Errorf("active %d notes %q", s.ActiveBytes, s.Notes)
	}
	// Implausible block count does not allocate per-layer slices.
	s = summarize(t, builder{kvs: []kv{{"general.architecture", "x"}, {"x.block_count", uint64(1 << 40)}, {"x.attention.head_count", uint32(1)}}})
	if len(s.Layers) != 0 || !hasNote(s.Notes, "implausible") {
		t.Errorf("layers %d notes %q", len(s.Layers), s.Notes)
	}
}

func TestSplit(t *testing.T) {
	names, ok := SplitPartNames("/models/Qwen3-235B-Q4_K_M-00001-of-00003.gguf")
	want := []string{
		"/models/Qwen3-235B-Q4_K_M-00001-of-00003.gguf",
		"/models/Qwen3-235B-Q4_K_M-00002-of-00003.gguf",
		"/models/Qwen3-235B-Q4_K_M-00003-of-00003.gguf",
	}
	if !ok || !reflect.DeepEqual(names, want) {
		t.Errorf("SplitPartNames = %v %v", names, ok)
	}
	names, ok = SplitPartNames("https://huggingface.co/o/r/resolve/main/m-00002-of-00002.gguf?download=true")
	if !ok || names[0] != "https://huggingface.co/o/r/resolve/main/m-00001-of-00002.gguf?download=true" {
		t.Errorf("URL split names = %v", names)
	}
	for _, bad := range []string{"model.gguf", "m-00003-of-00002.gguf", "m-0001-of-0003.gguf", "m-00001-of-00003.bin", "x-00000-of-00002.gguf"} {
		if _, ok := SplitPartNames(bad); ok {
			t.Errorf("SplitPartNames(%q) should fail", bad)
		}
	}

	part := func(no int, kvs []kv, ts ...tensorSpec) *File {
		b := builder{kvs: append([]kv{{"split.no", uint16(no)}, {"split.count", uint16(2)}, {"split.tensors.count", int32(3)}}, kvs...), tensors: ts}
		data := b.bytes(t)
		f, err := Parse(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		f.Source = fmt.Sprintf("part%d", no+1)
		return f
	}
	p1 := part(0, []kv{{"general.architecture", "llama"}, {"llama.block_count", uint32(1)}},
		tensorSpec{"token_embd.weight", []uint64{32, 4}, TypeF32}, tensorSpec{"output.weight", []uint64{32, 4}, TypeF32})
	p2 := part(1, nil, tensorSpec{"blk.0.attn_q.weight", []uint64{32, 32}, TypeQ8_0})

	si, ok := p1.Split()
	if !ok || si != (SplitInfo{No: 0, Count: 2, TensorsCount: 3}) {
		t.Errorf("Split() = %+v %v", si, ok)
	}
	if _, ok := (&File{}).Split(); ok {
		t.Error("unsplit file reported as split")
	}
	s := p1.Summary()
	if s.Split == nil || s.Split.Count != 2 || !hasNote(s.Notes, "MergeSplits") {
		t.Errorf("first part summary split %+v notes %q", s.Split, s.Notes)
	}

	m, err := MergeSplits([]*File{p2, p1})
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Tensors) != 3 || m.Tensors[2].Split != 1 || m.FileSize != p1.FileSize+p2.FileSize || m.Architecture() != "llama" {
		t.Errorf("merged: %d tensors, size %d", len(m.Tensors), m.FileSize)
	}
	ms := m.Summary()
	if ms.TensorBytes != 512+512+32*34 || hasNote(ms.Notes, "MergeSplits") {
		t.Errorf("merged bytes %d notes %q", ms.TensorBytes, ms.Notes)
	}
	if _, err := MergeSplits([]*File{p1}); err == nil {
		t.Error("merging 1 of 2 parts should fail")
	}
	if _, err := MergeSplits([]*File{p1, p1}); err == nil {
		t.Error("merging a duplicate part should fail")
	}
}
