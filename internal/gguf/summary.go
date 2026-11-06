package gguf

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// AttentionKind is how a layer attends over the context.
type AttentionKind string

// Attention kinds.
const (
	// AttnFull layers cache K/V for every token of the context.
	AttnFull AttentionKind = "full"
	// AttnSliding layers cache K/V only for the last SlidingWindow tokens
	// (or the current chunk, for chunked attention).
	AttnSliding AttentionKind = "sliding"
	// AttnNone layers have no attention KV cache (recurrent/SSM layers,
	// attention-free layers, or layers reusing another layer's cache).
	AttnNone AttentionKind = "none"
)

// Layer is the attention/recurrence layout of one block.
type Layer struct {
	Attention AttentionKind `json:"attention"`
	// Recurrent is true when the layer keeps a fixed-size recurrent state
	// (SSM, linear attention, RWKV, short convolution).
	Recurrent   bool   `json:"recurrent,omitempty"`
	HeadCount   uint64 `json:"head_count"`
	HeadCountKV uint64 `json:"head_count_kv"`
	// KElems and VElems are the cache elements one token occupies in this
	// layer's K and V caches (0 when Attention is AttnNone).
	KElems uint64 `json:"k_elems"`
	VElems uint64 `json:"v_elems"`
	// StateElems is the recurrent state size in elements (per sequence).
	StateElems uint64 `json:"state_elems,omitempty"`
}

// Summary is the prediction-relevant digest of a GGUF header. Zero means
// "absent" for metadata fields. Assumptions are explained in Notes.
type Summary struct {
	Architecture string `json:"architecture"`
	Name         string `json:"name,omitempty"`
	SizeLabel    string `json:"size_label,omitempty"`
	// FileType is general.file_type (llama_ftype), -1 when absent.
	FileType int64 `json:"file_type"`
	// FileTypeLabel is e.g. "Q4_K_M" or "MXFP4"; when general.file_type is
	// absent it is DominantType + " (guessed)".
	FileTypeLabel string `json:"file_type_label,omitempty"`
	// DominantType is the ggml type holding the most tensor bytes.
	DominantType        string            `json:"dominant_type,omitempty"`
	QuantizationVersion uint64            `json:"quantization_version,omitempty"`
	TypeBytes           map[string]uint64 `json:"type_bytes,omitempty"`

	ContextLength   uint64 `json:"context_length"`
	EmbeddingLength uint64 `json:"embedding_length"`
	BlockCount      uint64 `json:"block_count"`
	// FeedForwardLength, HeadCount and HeadCountKV are the maximum over
	// layers when the file stores per-layer arrays (kept in *PerLayer).
	FeedForwardLength         uint64   `json:"feed_forward_length"`
	FeedForwardLengthPerLayer []uint64 `json:"feed_forward_length_per_layer,omitempty"`
	HeadCount                 uint64   `json:"head_count"`
	HeadCountPerLayer         []uint64 `json:"head_count_per_layer,omitempty"`
	HeadCountKV               uint64   `json:"head_count_kv"`
	HeadCountKVPerLayer       []uint64 `json:"head_count_kv_per_layer,omitempty"`
	// KeyLength/ValueLength are per-head dimensions (default
	// embedding_length / head_count).
	KeyLength   uint64 `json:"key_length"`
	ValueLength uint64 `json:"value_length"`

	SlidingWindow uint64 `json:"sliding_window,omitempty"`
	// SlidingChunked is true for chunked local attention (llama4).
	SlidingChunked bool `json:"sliding_chunked,omitempty"`
	// SlidingPatternSource says how per-layer sliding was decided: a
	// metadata key, or "convention:<arch>".
	SlidingPatternSource string `json:"sliding_pattern_source,omitempty"`

	ExpertCount             uint64 `json:"expert_count,omitempty"`
	ExpertUsedCount         uint64 `json:"expert_used_count,omitempty"`
	ExpertSharedCount       uint64 `json:"expert_shared_count,omitempty"`
	ExpertFeedForwardLength uint64 `json:"expert_feed_forward_length,omitempty"`
	LeadingDenseBlockCount  uint64 `json:"leading_dense_block_count,omitempty"`
	// NextNPredictLayers is the number of trailing multi-token-prediction
	// blocks (GLM-4.5, DeepSeek); they are excluded from active bytes.
	NextNPredictLayers uint64 `json:"nextn_predict_layers,omitempty"`

	// MLA (deepseek2-style multi-head latent attention).
	QLoraRank      uint64 `json:"q_lora_rank,omitempty"`
	KVLoraRank     uint64 `json:"kv_lora_rank,omitempty"`
	KeyLengthMLA   uint64 `json:"key_length_mla,omitempty"`
	ValueLengthMLA uint64 `json:"value_length_mla,omitempty"`

	// SSM / recurrent / hybrid.
	SSMConvKernel         uint64 `json:"ssm_conv_kernel,omitempty"`
	SSMInnerSize          uint64 `json:"ssm_inner_size,omitempty"`
	SSMStateSize          uint64 `json:"ssm_state_size,omitempty"`
	SSMTimeStepRank       uint64 `json:"ssm_time_step_rank,omitempty"`
	SSMGroupCount         uint64 `json:"ssm_group_count,omitempty"`
	FullAttentionInterval uint64 `json:"full_attention_interval,omitempty"`

	RopeDimensionCount        uint64  `json:"rope_dimension_count,omitempty"`
	RopeFreqBase              float64 `json:"rope_freq_base,omitempty"`
	RopeScalingType           string  `json:"rope_scaling_type,omitempty"`
	RopeScalingFactor         float64 `json:"rope_scaling_factor,omitempty"`
	RopeOriginalContextLength uint64  `json:"rope_original_context_length,omitempty"`

	VocabSize          uint64 `json:"vocab_size"`
	TokenizerModel     string `json:"tokenizer_model,omitempty"`
	HasChatTemplate    bool   `json:"has_chat_template"`
	ChatTemplateLength int    `json:"chat_template_length,omitempty"`

	// Split is set for llama.cpp split parts; tensor totals then cover only
	// the parts that were read (see MergeSplits).
	Split *SplitInfo `json:"split,omitempty"`

	// Derived from the tensor table.
	TensorCount int `json:"tensor_count"`
	// UnknownTensors counts tensors whose size is unknown (excluded from
	// byte totals; their elements are still counted).
	UnknownTensors  int    `json:"unknown_tensors,omitempty"`
	ParameterCount  uint64 `json:"parameter_count"`
	TensorBytes     uint64 `json:"tensor_bytes"`
	ExpertParams    uint64 `json:"expert_params,omitempty"`
	ExpertBytes     uint64 `json:"expert_bytes,omitempty"`
	NonExpertParams uint64 `json:"non_expert_params"`
	NonExpertBytes  uint64 `json:"non_expert_bytes"`
	// EmbeddingParams/Bytes are token_embd.weight: a row lookup per token,
	// not a full read, so excluded from ActiveBytes unless TiedEmbeddings.
	EmbeddingParams uint64 `json:"embedding_params"`
	EmbeddingBytes  uint64 `json:"embedding_bytes"`
	// TiedEmbeddings is true when there is no output.weight, so the
	// embedding matrix doubles as the output head (read every token).
	TiedEmbeddings bool `json:"tied_embeddings,omitempty"`
	// ActiveParams/ActiveBytes estimate the weights read per decoded token:
	// non-expert (minus the embedding lookup) + expert × used/count.
	ActiveParams uint64 `json:"active_params"`
	ActiveBytes  uint64 `json:"active_bytes"`

	Layers              []Layer `json:"layers,omitempty"`
	FullAttentionLayers int     `json:"full_attention_layers"`
	SlidingLayers       int     `json:"sliding_layers"`
	RecurrentLayers     int     `json:"recurrent_layers,omitempty"`
	NoAttentionLayers   int     `json:"no_attention_layers,omitempty"`

	// KV cache bytes per context token across full-attention layers.
	KVBytesPerTokenF16  uint64 `json:"kv_bytes_per_token_f16"`
	KVBytesPerTokenQ8_0 uint64 `json:"kv_bytes_per_token_q8_0"`
	// Per-token KV bytes across sliding layers (they hold at most
	// SlidingWindow tokens) and the fixed cost of a full window.
	SlidingKVBytesPerTokenF16  uint64 `json:"sliding_kv_bytes_per_token_f16,omitempty"`
	SlidingKVBytesPerTokenQ8_0 uint64 `json:"sliding_kv_bytes_per_token_q8_0,omitempty"`
	SlidingKVBytesF16          uint64 `json:"sliding_kv_bytes_f16,omitempty"`
	SlidingKVBytesQ8_0         uint64 `json:"sliding_kv_bytes_q8_0,omitempty"`
	// RecurrentStateBytes is the fixed recurrent state per sequence (F32).
	RecurrentStateBytes uint64 `json:"recurrent_state_bytes,omitempty"`

	FileSize int64    `json:"file_size"`
	Notes    []string `json:"notes,omitempty"`
}

// KVCacheBytes returns the KV cache for ctx tokens of one sequence with
// cache type typ (e.g. TypeF16, TypeQ8_0, TypeQ4_0): full-attention layers
// hold ctx tokens, sliding layers min(ctx, SlidingWindow). It excludes
// llama.cpp's padding (sliding caches keep one extra ubatch) and
// RecurrentStateBytes.
func (s *Summary) KVCacheBytes(ctx uint64, typ GGMLType) uint64 {
	var total uint64
	win := min(ctx, s.SlidingWindow)
	for _, l := range s.Layers {
		per := cacheBytes(typ, l.KElems) + cacheBytes(typ, l.VElems)
		switch l.Attention {
		case AttnFull:
			total += ctx * per
		case AttnSliding:
			total += win * per
		}
	}
	return total
}

// maxLayers bounds per-layer slices built from block_count.
const maxLayers = 1 << 16

// slidingConventions gives set_swa_pattern periods llama.cpp hard-codes
// for architectures whose files carry a sliding window but no per-layer
// pattern: layer il is sliding when il % period < period-1.
var slidingConventions = map[string]uint64{
	"gpt-oss": 2, // alternating, starting with sliding
	"gemma2":  2,
	"gemma3":  6, // 5 local : 1 global
	"cohere2": 4, // 3 sliding : 1 full
}

// Summary digests the header. It never fails; missing or inconsistent
// metadata is reported in Notes.
func (f *File) Summary() *Summary {
	s := &Summary{FileType: -1, FileSize: f.FileSize, TypeBytes: map[string]uint64{}}
	note := func(format string, a ...any) { s.Notes = append(s.Notes, fmt.Sprintf(format, a...)) }
	arch := f.Architecture()
	s.Architecture = arch
	key := func(suffix string) string { return arch + "." + suffix }
	u := func(k string) uint64 { v, _ := f.Uint(k); return v }
	if arch == "" {
		note("general.architecture is missing; architecture keys could not be read")
	}

	s.Name, _ = f.String("general.name")
	s.SizeLabel, _ = f.String("general.size_label")
	if ft, ok := f.Int("general.file_type"); ok {
		s.FileType = ft
		if l, ok := FileTypeLabel(ft); ok {
			s.FileTypeLabel = l
		} else {
			s.FileTypeLabel = "ftype " + strconv.FormatInt(ft, 10)
		}
	}
	s.QuantizationVersion = u("general.quantization_version")

	s.ContextLength = u(key("context_length"))
	s.EmbeddingLength = u(key("embedding_length"))
	s.BlockCount = u(key("block_count"))
	n := s.BlockCount
	if n > maxLayers {
		note("block_count %d is implausible; per-layer layout not computed", n)
		n = 0
	}
	if s.ContextLength == 0 && arch != "" {
		note("%s is missing", key("context_length"))
	}

	ff, ffArr := f.perLayer(key("feed_forward_length"), n, note)
	s.FeedForwardLength = maxOf(ff)
	if ffArr {
		s.FeedForwardLengthPerLayer = ff
	}
	if ff == nil {
		s.FeedForwardLength = u(key("feed_forward_length"))
	}
	heads, headsArr := f.perLayer(key("attention.head_count"), n, note)
	if headsArr {
		s.HeadCountPerLayer = heads
	}
	kvHeads, kvArr := f.perLayer(key("attention.head_count_kv"), n, note)
	if kvArr {
		s.HeadCountKVPerLayer = kvHeads
	}
	if kvHeads == nil && heads != nil {
		kvHeads = append([]uint64(nil), heads...) // llama.cpp: defaults to head_count
	}
	s.HeadCount = maxOf(heads)
	s.HeadCountKV = maxOf(kvHeads)
	if n == 0 {
		s.HeadCount = u(key("attention.head_count"))
		s.HeadCountKV = u(key("attention.head_count_kv"))
		if s.HeadCountKV == 0 {
			s.HeadCountKV = s.HeadCount
		}
	}
	firstHeads := s.HeadCount
	for _, h := range heads {
		if h > 0 {
			firstHeads = h // llama.cpp uses n_head(0) for the default head size
			break
		}
	}
	s.KeyLength = u(key("attention.key_length"))
	s.ValueLength = u(key("attention.value_length"))
	if firstHeads > 0 {
		if s.KeyLength == 0 {
			s.KeyLength = s.EmbeddingLength / firstHeads
		}
		if s.ValueLength == 0 {
			s.ValueLength = s.EmbeddingLength / firstHeads
		}
	}

	s.ExpertCount = u(key("expert_count"))
	s.ExpertUsedCount = u(key("expert_used_count"))
	s.ExpertSharedCount = u(key("expert_shared_count"))
	s.ExpertFeedForwardLength = u(key("expert_feed_forward_length"))
	s.LeadingDenseBlockCount = u(key("leading_dense_block_count"))
	s.NextNPredictLayers = u(key("nextn_predict_layers"))

	s.QLoraRank = u(key("attention.q_lora_rank"))
	s.KVLoraRank = u(key("attention.kv_lora_rank"))
	s.KeyLengthMLA = u(key("attention.key_length_mla"))
	s.ValueLengthMLA = u(key("attention.value_length_mla"))

	s.SSMConvKernel = u(key("ssm.conv_kernel"))
	s.SSMInnerSize = u(key("ssm.inner_size"))
	s.SSMStateSize = u(key("ssm.state_size"))
	s.SSMTimeStepRank = u(key("ssm.time_step_rank"))
	s.SSMGroupCount = u(key("ssm.group_count"))
	s.FullAttentionInterval = u(key("full_attention_interval"))

	s.RopeDimensionCount = u(key("rope.dimension_count"))
	s.RopeFreqBase, _ = f.Float(key("rope.freq_base"))
	s.RopeScalingType, _ = f.String(key("rope.scaling.type"))
	s.RopeScalingFactor, _ = f.Float(key("rope.scaling.factor"))
	s.RopeOriginalContextLength = u(key("rope.scaling.original_context_length"))

	s.TokenizerModel, _ = f.String("tokenizer.ggml.model")
	if a, ok := f.Array("tokenizer.ggml.tokens"); ok {
		s.VocabSize = a.Len
	} else if v := u(key("vocab_size")); v > 0 {
		s.VocabSize = v
	} else if t, ok := f.Tensor("token_embd.weight"); ok && len(t.Dims) == 2 {
		s.VocabSize = t.Dims[1]
		note("vocab size taken from token_embd.weight (no tokenizer.ggml.tokens)")
	}
	if tmpl, ok := f.String("tokenizer.chat_template"); ok {
		s.HasChatTemplate = true
		s.ChatTemplateLength = len(tmpl)
	}

	if si, ok := f.Split(); ok {
		s.Split = &si
	}

	f.summarizeTensors(s, note)
	f.summarizeLayers(s, n, heads, kvHeads, note)

	if c, ok := f.Bool(key("attention.causal")); ok && !c {
		note("non-causal (embedding) model: llama.cpp keeps no KV cache across requests")
	}
	return s
}

// perLayer reads a key that is either a scalar (applied to every layer) or
// a per-layer array. It returns nil when the key is absent or unusable.
func (f *File) perLayer(k string, n uint64, note func(string, ...any)) (vals []uint64, isArray bool) {
	v, ok := f.Get(k)
	if !ok || n == 0 {
		return nil, false
	}
	if a, ok := v.Array(); ok {
		if a.Skipped || len(a.Values) == 0 {
			note("%s is an array of %d elements that was not decoded; ignored", k, a.Len)
			return nil, false
		}
		vals = make([]uint64, n)
		for i := range vals {
			j := min(i, len(a.Values)-1) // pad with the last value
			x, ok := a.Values[j].Uint()
			if !ok {
				note("%s has non-integer elements; ignored", k)
				return nil, false
			}
			vals[i] = x
		}
		if uint64(len(a.Values)) != n {
			note("%s has %d elements for %d layers; padded/truncated", k, len(a.Values), n)
		}
		return vals, true
	}
	x, ok := v.Uint()
	if !ok {
		note("%s is not an integer; ignored", k)
		return nil, false
	}
	vals = make([]uint64, n)
	for i := range vals {
		vals[i] = x
	}
	return vals, false
}

func maxOf(xs []uint64) uint64 {
	var m uint64
	for _, x := range xs {
		m = max(m, x)
	}
	return m
}

// isExpertTensor reports routed-expert weights: merged "*_exps*" tensors,
// or legacy per-expert "blk.N.ffn_{gate,up,down}.E.weight" (early Mixtral
// conversions). Shared experts ("_shexp") are always active.
func isExpertTensor(name string) bool {
	if strings.Contains(name, "_exps") {
		return true
	}
	parts := strings.Split(name, ".")
	if len(parts) == 5 && parts[0] == "blk" {
		switch parts[2] {
		case "ffn_gate", "ffn_up", "ffn_down":
			_, err := strconv.Atoi(parts[3])
			return err == nil
		}
	}
	return false
}

// layerIndex parses N from "blk.N.*".
func layerIndex(name string) (uint64, bool) {
	rest, ok := strings.CutPrefix(name, "blk.")
	if !ok {
		return 0, false
	}
	num, _, ok := strings.Cut(rest, ".")
	if !ok {
		return 0, false
	}
	i, err := strconv.ParseUint(num, 10, 32)
	return i, err == nil
}

func (f *File) summarizeTensors(s *Summary, note func(string, ...any)) {
	var hasEmbd, hasOutput bool
	var removed int
	var mtpExpBytes, mtpExpParams, mtpBytes, mtpParams uint64
	mtpStart := uint64(1<<63 - 1)
	if s.NextNPredictLayers > 0 && s.NextNPredictLayers <= s.BlockCount {
		mtpStart = s.BlockCount - s.NextNPredictLayers
	}
	for _, t := range f.Tensors {
		s.TensorCount++
		s.ParameterCount += t.Elements
		if t.Err != "" {
			s.UnknownTensors++
			continue
		}
		s.TensorBytes += t.Bytes
		s.TypeBytes[t.Type.String()] += t.Bytes
		if tr, _ := t.Type.Traits(); tr.Removed {
			removed++
		}
		expert := isExpertTensor(t.Name)
		if expert {
			s.ExpertBytes += t.Bytes
			s.ExpertParams += t.Elements
		}
		switch t.Name {
		case "token_embd.weight":
			hasEmbd = true
			s.EmbeddingBytes, s.EmbeddingParams = t.Bytes, t.Elements
		case "output.weight":
			hasOutput = true
		}
		if l, ok := layerIndex(t.Name); ok && l >= mtpStart {
			if expert {
				mtpExpBytes += t.Bytes
				mtpExpParams += t.Elements
			} else {
				mtpBytes += t.Bytes
				mtpParams += t.Elements
			}
		}
	}
	// Unknown-size tensors still count towards parameters; keep the
	// non-expert byte split consistent with TensorBytes.
	s.NonExpertBytes = s.TensorBytes - s.ExpertBytes
	s.NonExpertParams = s.ParameterCount - s.ExpertParams

	var dominant uint64
	for name, b := range s.TypeBytes {
		if b > dominant || (b == dominant && name < s.DominantType) {
			s.DominantType, dominant = name, b
		}
	}
	if s.FileType < 0 && s.DominantType != "" {
		s.FileTypeLabel = s.DominantType + " (guessed)"
		note("general.file_type is missing; label guessed from the dominant tensor type")
	}
	if s.UnknownTensors > 0 {
		note("%d tensors have an unknown ggml type or invalid shape and are excluded from byte totals", s.UnknownTensors)
	}
	if removed > 0 {
		note("%d tensors use repacked types removed from current llama.cpp (re-download a current quant)", removed)
	}
	if s.Split != nil && s.Split.Count > 1 && s.TensorCount < s.Split.TensorsCount {
		note("split model part %d of %d: tensor totals cover %d of %d tensors; open every part and use MergeSplits for whole-model totals",
			s.Split.No+1, s.Split.Count, s.TensorCount, s.Split.TensorsCount)
	}

	s.TiedEmbeddings = hasEmbd && !hasOutput
	activeBytes := s.NonExpertBytes - mtpBytes
	activeParams := s.NonExpertParams - mtpParams
	if !s.TiedEmbeddings {
		activeBytes -= s.EmbeddingBytes
		activeParams -= s.EmbeddingParams
	} else {
		note("no output.weight: embeddings are tied, so token_embd is read every token as the output head and counted as active")
	}
	if mtpBytes+mtpExpBytes > 0 {
		note("%d trailing multi-token-prediction block(s) (%d bytes) are excluded from active bytes (unused in normal decoding)", s.NextNPredictLayers, mtpBytes+mtpExpBytes)
	}
	expBytes, expParams := s.ExpertBytes-mtpExpBytes, s.ExpertParams-mtpExpParams
	switch {
	case expBytes == 0:
	case s.ExpertCount > 0 && s.ExpertUsedCount > 0:
		used := min(s.ExpertUsedCount, s.ExpertCount)
		activeBytes += expBytes * used / s.ExpertCount
		activeParams += expParams * used / s.ExpertCount
		note("MoE: active = non-expert + %d/%d of routed-expert (_exps) tensors; assumes evenly sized experts, ignores routing locality across tokens", used, s.ExpertCount)
	default:
		activeBytes += expBytes
		activeParams += expParams
		note("expert tensors present but expert_count/expert_used_count missing: all experts counted as active")
	}
	s.ActiveBytes, s.ActiveParams = activeBytes, activeParams
}

func (f *File) summarizeLayers(s *Summary, n uint64, heads, kvHeads []uint64, note func(string, ...any)) {
	if n == 0 {
		return
	}
	arch := s.Architecture
	key := func(suffix string) string { return arch + "." + suffix }
	u := func(k string) uint64 { v, _ := f.Uint(k); return v }
	hasPrefix := func(p string) bool {
		for _, kv := range f.KVs {
			if strings.HasPrefix(kv.Key, p) {
				return true
			}
		}
		return false
	}

	layers := make([]Layer, n)
	for i := range layers {
		l := &layers[i]
		if heads != nil {
			l.HeadCount = heads[i]
		}
		if kvHeads != nil {
			l.HeadCountKV = kvHeads[i]
		}
		l.Attention = AttnFull
		if l.HeadCountKV == 0 {
			l.Attention = AttnNone
		}
	}

	// Recurrent layers.
	hasSSM := hasPrefix(key("ssm."))
	hasShortConv := hasPrefix(key("shortconv."))
	isRWKV := strings.HasPrefix(arch, "rwkv") || strings.HasPrefix(arch, "arwkv")
	switch {
	case isRWKV:
		for i := range layers {
			layers[i].Recurrent, layers[i].Attention = true, AttnNone
		}
	case s.FullAttentionInterval > 0 || arch == "qwen3next":
		iv := s.FullAttentionInterval
		if iv == 0 {
			iv = 4
			note("qwen3next without full_attention_interval: assumed every 4th layer is full attention")
		}
		for i := range layers {
			if uint64(i+1)%iv != 0 {
				layers[i].Recurrent, layers[i].Attention = true, AttnNone
			}
		}
	case arch == "falcon-h1":
		for i := range layers {
			layers[i].Recurrent = true // parallel attention + SSM in every layer
		}
	case hasSSM || hasShortConv:
		for i := range layers {
			if layers[i].HeadCountKV == 0 {
				layers[i].Recurrent = true
			}
		}
	}

	// Sliding-window layers.
	window := u(key("attention.sliding_window"))
	_, windowKey := f.Get(key("attention.sliding_window"))
	var sliding []bool
	patKey := key("attention.sliding_window_pattern")
	if v, ok := f.Get(patKey); ok {
		sliding = slidingFromPattern(v, n, patKey, note)
		if sliding != nil {
			s.SlidingPatternSource = patKey
		}
	}
	if sliding == nil {
		period, known := slidingConventions[arch]
		switch {
		case arch == "llama4" && !(windowKey && window == 0):
			window, period, known = 8192, 4, true
			s.SlidingChunked = true
			note("llama4: assumed chunked attention (8192-token chunks) on 3 of every 4 layers, as llama.cpp does")
		case known && window > 0:
			note("%s: no %s key; assumed llama.cpp's convention (every %d-th layer full attention, others sliding)", arch, patKey, period)
		case window > 0:
			note("%s has sliding_window=%d but no per-layer pattern is known for it; all attention layers counted as full (upper bound)", arch, window)
			known = false
		}
		if known && window > 0 {
			sliding = swaPattern(period, n)
			s.SlidingPatternSource = "convention:" + arch
		}
	}
	s.SlidingWindow = window
	if sliding != nil && window == 0 {
		note("%s marks sliding layers but sliding_window is 0; treated as full attention", patKey)
		sliding = nil
		s.SlidingPatternSource = ""
	}
	for i := range layers {
		if sliding != nil && sliding[i] && layers[i].Attention == AttnFull {
			layers[i].Attention = AttnSliding
		}
	}

	// Layers sharing an earlier layer's KV cache (gemma3n).
	if shared := u(key("attention.shared_kv_layers")); shared > 0 && shared < n {
		for i := n - shared; i < n; i++ {
			layers[i].Attention = AttnNone
		}
		note("last %d layers reuse earlier layers' KV cache and add none", shared)
	}

	// Per-token cache sizes.
	for i := range layers {
		l := &layers[i]
		if l.Attention != AttnNone {
			l.KElems = l.HeadCountKV * s.KeyLength
			l.VElems = l.HeadCountKV * s.ValueLength
		}
	}
	if s.KVLoraRank > 0 {
		if s.KeyLengthMLA > 0 {
			note("MLA: the cache holds the compressed latent (key_length %d + value_length %d per layer); runtimes that reuse K as V need less", s.KeyLength, s.ValueLength)
		} else {
			note("MLA model converted without key_length_mla: llama.cpp caches full per-head K/V; re-converted GGUFs need far less")
		}
	}

	// Recurrent state per layer (llama.cpp n_embd_r + n_embd_s, F32).
	var stateElems uint64
	var stateKnown bool
	switch {
	case isRWKV:
		shift := u(key("token_shift_count"))
		if shift == 0 {
			shift = 2
		}
		if hs := u(key("wkv.head_size")); hs > 0 {
			stateElems, stateKnown = shift*s.EmbeddingLength+s.EmbeddingLength*hs, true
		}
	case hasSSM:
		var conv uint64
		if s.SSMConvKernel > 0 {
			conv = (s.SSMConvKernel - 1) * (s.SSMInnerSize + 2*s.SSMGroupCount*s.SSMStateSize)
		}
		stateElems, stateKnown = conv+s.SSMStateSize*s.SSMInnerSize, true
	case hasShortConv:
		if lc := u(key("shortconv.l_cache")); lc > 0 {
			stateElems, stateKnown = s.EmbeddingLength*(lc-1), true
		}
	}

	for i := range layers {
		l := &layers[i]
		if l.Recurrent {
			s.RecurrentLayers++
			if stateKnown {
				l.StateElems = stateElems
				s.RecurrentStateBytes += stateElems * 4
			}
		}
		switch l.Attention {
		case AttnFull:
			s.FullAttentionLayers++
			s.KVBytesPerTokenF16 += cacheBytes(TypeF16, l.KElems) + cacheBytes(TypeF16, l.VElems)
			s.KVBytesPerTokenQ8_0 += cacheBytes(TypeQ8_0, l.KElems) + cacheBytes(TypeQ8_0, l.VElems)
		case AttnSliding:
			s.SlidingLayers++
			s.SlidingKVBytesPerTokenF16 += cacheBytes(TypeF16, l.KElems) + cacheBytes(TypeF16, l.VElems)
			s.SlidingKVBytesPerTokenQ8_0 += cacheBytes(TypeQ8_0, l.KElems) + cacheBytes(TypeQ8_0, l.VElems)
		case AttnNone:
			if !l.Recurrent {
				s.NoAttentionLayers++
			}
		}
	}
	s.SlidingKVBytesF16 = window * s.SlidingKVBytesPerTokenF16
	s.SlidingKVBytesQ8_0 = window * s.SlidingKVBytesPerTokenQ8_0
	if s.RecurrentLayers > 0 {
		if stateKnown {
			note("recurrent state: %d layers, %d bytes per sequence (F32), independent of context length", s.RecurrentLayers, s.RecurrentStateBytes)
		} else {
			note("%d recurrent layers; their state size is unknown (small, context-independent)", s.RecurrentLayers)
		}
	}
	if s.NoAttentionLayers > 0 && s.RecurrentLayers == 0 {
		note("%d layers have no attention (head_count_kv = 0)", s.NoAttentionLayers)
	}
	if s.SlidingLayers > 0 {
		note("sliding layers cache at most %d tokens; llama.cpp also keeps one extra ubatch (~512 tokens) per sliding layer", window)
	}
	s.Layers = layers
}

// swaPattern mirrors llama.cpp's set_swa_pattern(period): layer il is
// sliding when il % period < period-1 (period 0: all sliding).
func swaPattern(period, n uint64) []bool {
	out := make([]bool, n)
	for i := range out {
		out[i] = period == 0 || uint64(i)%period < period-1
	}
	return out
}

// slidingFromPattern decodes <arch>.attention.sliding_window_pattern: a
// per-layer bool/int array (true = sliding), a bool applying to every
// layer, or an integer period (>= 2) as in set_swa_pattern.
func slidingFromPattern(v Value, n uint64, k string, note func(string, ...any)) []bool {
	if a, ok := v.Array(); ok {
		if a.Skipped || len(a.Values) == 0 {
			note("%s array was not decoded; ignored", k)
			return nil
		}
		out := make([]bool, n)
		for i := range out {
			e := a.Values[min(i, len(a.Values)-1)]
			if b, ok := e.Bool(); ok {
				out[i] = b
			} else if x, ok := e.Uint(); ok {
				out[i] = x != 0
			} else {
				note("%s has unsupported elements; ignored", k)
				return nil
			}
		}
		if uint64(len(a.Values)) != n {
			note("%s has %d elements for %d layers; padded/truncated", k, len(a.Values), n)
		}
		return out
	}
	if b, ok := v.Bool(); ok {
		out := make([]bool, n)
		for i := range out {
			out[i] = b
		}
		return out
	}
	if p, ok := v.Uint(); ok {
		if p >= 2 {
			note("%s = %d read as a period: every %d-th layer full attention", k, p, p)
			return swaPattern(p, n)
		}
		out := make([]bool, n)
		for i := range out {
			out[i] = p != 0
		}
		return out
	}
	note("%s has an unsupported type; ignored", k)
	return nil
}

// SortedTypeBytes returns TypeBytes as (type, bytes) pairs, largest first.
func (s *Summary) SortedTypeBytes() []TypeShare {
	out := make([]TypeShare, 0, len(s.TypeBytes))
	for t, b := range s.TypeBytes {
		out = append(out, TypeShare{Type: t, Bytes: b})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Bytes != out[j].Bytes {
			return out[i].Bytes > out[j].Bytes
		}
		return out[i].Type < out[j].Type
	})
	return out
}

// TypeShare is one entry of Summary.SortedTypeBytes.
type TypeShare struct {
	Type  string `json:"type"`
	Bytes uint64 `json:"bytes"`
}
