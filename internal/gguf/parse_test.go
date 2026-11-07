package gguf

import (
	"bytes"
	"errors"
	"math"
	"runtime"
	"strings"
	"testing"
)

// Block layouts from ggml-common.h, computed by hand from the struct
// definitions (fp16 = 2 bytes, QK_K = 256).
func TestTypeTable(t *testing.T) {
	cases := []struct {
		typ         GGMLType
		id          uint32
		name        string
		block, size uint64
	}{
		{TypeF32, 0, "F32", 1, 4},
		{TypeF16, 1, "F16", 1, 2},
		{TypeQ4_0, 2, "Q4_0", 32, 2 + 16},
		{TypeQ4_1, 3, "Q4_1", 32, 2 + 2 + 16},
		{TypeQ5_0, 6, "Q5_0", 32, 2 + 4 + 16},
		{TypeQ5_1, 7, "Q5_1", 32, 2 + 2 + 4 + 16},
		{TypeQ8_0, 8, "Q8_0", 32, 2 + 32},
		{TypeQ8_1, 9, "Q8_1", 32, 2 + 2 + 32},
		{TypeQ2_K, 10, "Q2_K", 256, 16 + 64 + 2 + 2},
		{TypeQ3_K, 11, "Q3_K", 256, 32 + 64 + 12 + 2},
		{TypeQ4_K, 12, "Q4_K", 256, 2 + 2 + 12 + 128},
		{TypeQ5_K, 13, "Q5_K", 256, 2 + 2 + 12 + 32 + 128},
		{TypeQ6_K, 14, "Q6_K", 256, 128 + 64 + 16 + 2},
		{TypeQ8_K, 15, "Q8_K", 256, 4 + 256 + 32},
		{TypeIQ2_XXS, 16, "IQ2_XXS", 256, 2 + 64},
		{TypeIQ2_XS, 17, "IQ2_XS", 256, 2 + 64 + 8},
		{TypeIQ3_XXS, 18, "IQ3_XXS", 256, 2 + 96},
		{TypeIQ1_S, 19, "IQ1_S", 256, 2 + 32 + 16},
		{TypeIQ4_NL, 20, "IQ4_NL", 32, 2 + 16},
		{TypeIQ3_S, 21, "IQ3_S", 256, 2 + 64 + 8 + 32 + 4},
		{TypeIQ2_S, 22, "IQ2_S", 256, 2 + 64 + 8 + 8},
		{TypeIQ4_XS, 23, "IQ4_XS", 256, 2 + 2 + 4 + 128},
		{TypeI8, 24, "I8", 1, 1},
		{TypeI16, 25, "I16", 1, 2},
		{TypeI32, 26, "I32", 1, 4},
		{TypeI64, 27, "I64", 1, 8},
		{TypeF64, 28, "F64", 1, 8},
		{TypeIQ1_M, 29, "IQ1_M", 256, 32 + 16 + 8},
		{TypeBF16, 30, "BF16", 1, 2},
		{TypeTQ1_0, 34, "TQ1_0", 256, 48 + 4 + 2},
		{TypeTQ2_0, 35, "TQ2_0", 256, 64 + 2},
		{TypeMXFP4, 39, "MXFP4", 32, 1 + 16},
	}
	for _, c := range cases {
		if uint32(c.typ) != c.id {
			t.Errorf("%s: id %d, want %d", c.name, c.typ, c.id)
		}
		tr, ok := c.typ.Traits()
		if !ok || tr.Name != c.name || tr.BlockSize != c.block || tr.TypeSize != c.size || tr.Removed {
			t.Errorf("%s: traits %+v ok=%v, want block %d size %d", c.name, tr, ok, c.block, c.size)
		}
		if c.typ.String() != c.name {
			t.Errorf("String() = %q, want %q", c.typ.String(), c.name)
		}
	}
	// The spot checks the predictor relies on.
	for _, c := range []struct {
		typ   GGMLType
		n     uint64
		bytes uint64
	}{
		{TypeQ4_K, 256, 144}, {TypeQ8_0, 32, 34}, {TypeMXFP4, 32, 17}, {TypeQ6_K, 256, 210},
		{TypeQ4_K, 4096 * 4096, 4096 * 4096 / 256 * 144}, {TypeF16, 10, 20}, {TypeBF16, 3, 6},
	} {
		got, err := RowBytes(c.typ, c.n)
		if err != nil || got != c.bytes {
			t.Errorf("RowBytes(%s, %d) = %d, %v; want %d", c.typ, c.n, got, err, c.bytes)
		}
	}
	if _, err := RowBytes(TypeQ4_K, 100); err == nil {
		t.Error("RowBytes(Q4_K, 100) should fail: not a whole block")
	}
	if _, err := RowBytes(GGMLType(4), 32); err == nil || GGMLType(4).Known() {
		t.Error("type 4 (removed Q4_2) should be unknown")
	}
	if tr, ok := GGMLType(31).Traits(); !ok || !tr.Removed || tr.Name != "Q4_0_4_4" {
		t.Errorf("type 31 = %+v", tr)
	}
	if got := GGMLType(99).String(); got != "type(99)" {
		t.Errorf("unknown type String() = %q", got)
	}
	if bpw := typeTraits[TypeQ4_K].BitsPerWeight(); bpw != 4.5 {
		t.Errorf("Q4_K bpw = %v", bpw)
	}
}

func TestFileTypeLabel(t *testing.T) {
	for ft, want := range map[int64]string{0: "F32", 1: "F16", 7: "Q8_0", 15: "Q4_K_M", 18: "Q6_K", 30: "IQ4_XS", 32: "BF16", 38: "MXFP4", 1024 | 15: "Q4_K_M (guessed)"} {
		if got, ok := FileTypeLabel(ft); !ok || got != want {
			t.Errorf("FileTypeLabel(%d) = %q, %v; want %q", ft, got, ok, want)
		}
	}
	if _, ok := FileTypeLabel(5); ok {
		t.Error("ftype 5 (removed) should be unknown")
	}
}

func TestDenseLlamaLocal(t *testing.T) {
	data := llamaBuilder().bytes(t)
	f, err := Open(writeTemp(t, "tiny-llama-Q4_K_M.gguf", data))
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 3 || f.Alignment != 32 || len(f.Tensors) != 21 || f.FileSize != int64(len(data)) {
		t.Fatalf("version %d alignment %d tensors %d size %d", f.Version, f.Alignment, len(f.Tensors), f.FileSize)
	}
	if f.DataOffset%32 != 0 || len(f.Warnings) != 0 {
		t.Errorf("data offset %d, warnings %v", f.DataOffset, f.Warnings)
	}
	if f.HeaderBytesRead <= 0 || f.HeaderBytesRead > int64(len(data)) {
		t.Errorf("HeaderBytesRead = %d", f.HeaderBytesRead)
	}

	// Metadata accessors.
	if a := f.Architecture(); a != "llama" {
		t.Errorf("arch %q", a)
	}
	if v, ok := f.Uint("llama.block_count"); !ok || v != 2 {
		t.Errorf("block_count %d %v", v, ok)
	}
	if v, ok := f.Float("llama.rope.freq_base"); !ok || v != 500000 {
		t.Errorf("freq_base %v %v", v, ok)
	}
	if _, ok := f.String("llama.block_count"); ok {
		t.Error("String on an integer key should fail")
	}
	toks, ok := f.Array("tokenizer.ggml.tokens")
	if !ok || !toks.Skipped || toks.Len != 128 || toks.Values != nil || toks.ElemType != ValueString {
		t.Errorf("tokens array %+v", toks)
	}
	if v, _ := f.Get("tokenizer.ggml.tokens"); v.String() != "[128 x string]" {
		t.Errorf("tokens String() = %q", v.String())
	}

	// Tensor sizes, hand-computed.
	want := map[string]struct {
		elems, bytes uint64
	}{
		"token_embd.weight":        {256 * 128, 256 * 128 / 256 * 144}, // Q4_K: 18432
		"output_norm.weight":       {256, 256 * 4},                     // F32
		"output.weight":            {256 * 128, 128 * 210},             // Q6_K: 26880
		"blk.0.attn_q.weight":      {256 * 256, 256 * 144},             // Q4_K: 36864
		"blk.0.attn_v.weight":      {256 * 128, 256 * 128 * 2},         // F16: 65536
		"blk.1.ffn_down.weight":    {512 * 256, 512 * 256 / 32 * 34},   // Q8_0: 139264
		"blk.1.ffn_gate.weight":    {256 * 512, 512 * 144},             // Q4_K: 73728
		"blk.1.attn_output.weight": {256 * 256, 36864},
	}
	for name, w := range want {
		ti, ok := f.Tensor(name)
		if !ok {
			t.Errorf("missing tensor %s", name)
			continue
		}
		if ti.Elements != w.elems || ti.Bytes != w.bytes || ti.Err != "" {
			t.Errorf("%s: elems %d bytes %d err %q; want %d, %d", name, ti.Elements, ti.Bytes, ti.Err, w.elems, w.bytes)
		}
		if ti.Offset%32 != 0 {
			t.Errorf("%s offset %d not aligned", name, ti.Offset)
		}
	}

	s := f.Summary()
	const perLayerBytes = 1024 + 36864 + 18432 + 65536 + 36864 + 1024 + 73728 + 73728 + 139264 // 446464
	const totalBytes = 18432 + 1024 + 26880 + 2*perLayerBytes                                  // 939264
	const perLayerParams = 256 + 65536 + 32768 + 32768 + 65536 + 256 + 3*131072                // 590336
	const totalParams = 32768 + 256 + 32768 + 2*perLayerParams                                 // 1246464
	checks := []struct {
		name      string
		got, want any
	}{
		{"arch", s.Architecture, "llama"},
		{"name", s.Name, "Tiny Llama"},
		{"file type", s.FileType, int64(15)},
		{"file type label", s.FileTypeLabel, "Q4_K_M"},
		{"dominant", s.DominantType, "Q4_K"},
		{"quant version", s.QuantizationVersion, uint64(2)},
		{"ctx", s.ContextLength, uint64(4096)},
		{"embd", s.EmbeddingLength, uint64(256)},
		{"blocks", s.BlockCount, uint64(2)},
		{"ff", s.FeedForwardLength, uint64(512)},
		{"heads", s.HeadCount, uint64(4)},
		{"kv heads", s.HeadCountKV, uint64(2)},
		{"kv per layer (scalar key)", len(s.HeadCountKVPerLayer), 0},
		{"key len", s.KeyLength, uint64(64)},
		{"value len", s.ValueLength, uint64(64)},
		{"rope dims", s.RopeDimensionCount, uint64(64)},
		{"rope base", s.RopeFreqBase, 500000.0},
		{"vocab", s.VocabSize, uint64(128)},
		{"tokenizer", s.TokenizerModel, "gpt2"},
		{"chat template", s.HasChatTemplate, true},
		{"chat template len", s.ChatTemplateLength, len(llamaTemplate)},
		{"tensors", s.TensorCount, 21},
		{"params", s.ParameterCount, uint64(totalParams)},
		{"bytes", s.TensorBytes, uint64(totalBytes)},
		{"Q4_K bytes", s.TypeBytes["Q4_K"], uint64(18432 + 2*(36864+18432+36864+73728+73728))},
		{"expert bytes", s.ExpertBytes, uint64(0)},
		{"non-expert bytes", s.NonExpertBytes, uint64(totalBytes)},
		{"embedding bytes", s.EmbeddingBytes, uint64(18432)},
		{"embedding params", s.EmbeddingParams, uint64(32768)},
		{"tied", s.TiedEmbeddings, false},
		{"active bytes", s.ActiveBytes, uint64(totalBytes - 18432)},
		{"active params", s.ActiveParams, uint64(totalParams - 32768)},
		{"full layers", s.FullAttentionLayers, 2},
		{"sliding layers", s.SlidingLayers, 0},
		{"recurrent layers", s.RecurrentLayers, 0},
		// 2 layers × 2 KV heads × (64 + 64) dims × 2 bytes.
		{"kv f16", s.KVBytesPerTokenF16, uint64(2 * 2 * 128 * 2)},
		// Each 128-element row is 4 Q8_0 blocks of 34 bytes; K + V; 2 layers.
		{"kv q8_0", s.KVBytesPerTokenQ8_0, uint64(2 * 2 * 4 * 34)},
		{"sliding kv", s.SlidingKVBytesF16, uint64(0)},
		{"kv cache 4096 f16", s.KVCacheBytes(4096, TypeF16), uint64(4096 * 1024)},
		{"kv cache 100 q4_0", s.KVCacheBytes(100, TypeQ4_0), uint64(100 * 2 * 2 * 4 * 18)},
		{"file size", s.FileSize, int64(len(data))},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	if top := s.SortedTypeBytes(); len(top) == 0 || top[0].Type != "Q4_K" {
		t.Errorf("SortedTypeBytes = %v", top)
	}
}

func TestVersion2AndAlignment(t *testing.T) {
	b := llamaBuilder()
	b.version = 2
	b.alignment = 64
	f, err := Parse(bytes.NewReader(b.bytes(t)), -1)
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != 2 || f.Alignment != 64 || f.DataOffset%64 != 0 || f.FileSize != -1 {
		t.Errorf("version %d alignment %d data offset %d size %d", f.Version, f.Alignment, f.DataOffset, f.FileSize)
	}
	for _, ti := range f.Tensors {
		if ti.Offset%64 != 0 {
			t.Errorf("%s offset %d", ti.Name, ti.Offset)
		}
	}
	if s := f.Summary(); s.TensorBytes != 939264 {
		t.Errorf("tensor bytes %d", s.TensorBytes)
	}
}

func TestMetadataValueTypes(t *testing.T) {
	b := builder{kvs: []kv{
		{"general.architecture", "test"},
		{"u8", uint8(200)}, {"i8", int8(-5)}, {"u16", uint16(60000)}, {"i16", int16(-300)},
		{"u32", uint32(4e9)}, {"i32", int32(-7)}, {"f32", float32(1.5)}, {"b", true},
		{"s", "hello"}, {"u64", uint64(1 << 40)}, {"i64", int64(-1 << 40)}, {"f64", 2.25},
		{"arr.u32", []uint32{1, 2, 3}},
		{"arr.str", []string{"a", "bc"}},
		{"arr.nested", nested{et: ValueArray, elems: []any{[]uint32{1, 2}, []string{"x"}, nested{et: ValueArray, elems: []any{[]bool{true}}}}}},
		{"arr.big", generated{et: ValueUint32, n: MaxMaterializedArrayLen + 1, gen: func(i int) any { return uint32(i) }}},
		{"arr.bignested", generated{et: ValueArray, n: 5000, gen: func(i int) any { return []string{"q"} }}},
	}}
	f, err := Parse(bytes.NewReader(b.bytes(t)), -1)
	if err != nil {
		t.Fatal(err)
	}
	get := func(k string) Value {
		v, ok := f.Get(k)
		if !ok {
			t.Fatalf("missing %s", k)
		}
		return v
	}
	if v, ok := get("u8").Uint(); !ok || v != 200 {
		t.Error("u8")
	}
	if v, ok := get("i8").Int(); !ok || v != -5 {
		t.Error("i8")
	}
	if _, ok := get("i8").Uint(); ok {
		t.Error("negative i8 as Uint should fail")
	}
	if v, ok := get("u16").Uint(); !ok || v != 60000 {
		t.Error("u16")
	}
	if v, ok := get("i16").Int(); !ok || v != -300 {
		t.Error("i16")
	}
	if v, ok := get("u32").Uint(); !ok || v != 4e9 {
		t.Error("u32")
	}
	if v, ok := get("i32").Float(); !ok || v != -7 {
		t.Error("i32 as float")
	}
	if v, ok := get("f32").Float(); !ok || v != 1.5 {
		t.Error("f32")
	}
	if v, ok := get("b").Bool(); !ok || !v {
		t.Error("bool")
	}
	if v, ok := get("s").Str(); !ok || v != "hello" {
		t.Error("string")
	}
	if v, ok := get("u64").Uint(); !ok || v != 1<<40 {
		t.Error("u64")
	}
	if v, ok := get("i64").Int(); !ok || v != -1<<40 {
		t.Error("i64")
	}
	if v, ok := get("f64").Float(); !ok || v != 2.25 {
		t.Error("f64")
	}
	if a, ok := get("arr.u32").Array(); !ok || a.Skipped || len(a.Values) != 3 {
		t.Errorf("arr.u32 %+v", a)
	} else if x, _ := a.Values[2].Uint(); x != 3 {
		t.Errorf("arr.u32[2] = %d", x)
	}
	if s := get("arr.str").String(); s != `["a", "bc"]` {
		t.Errorf("arr.str = %s", s)
	}
	nest, _ := get("arr.nested").Array()
	if nest == nil || len(nest.Values) != 3 {
		t.Fatalf("nested %+v", nest)
	}
	inner, _ := nest.Values[2].Array()
	deep, _ := inner.Values[0].Array()
	if b, _ := deep.Values[0].Bool(); !b {
		t.Error("nested bool")
	}
	if a, _ := get("arr.big").Array(); a == nil || !a.Skipped || a.Len != MaxMaterializedArrayLen+1 || a.Values != nil {
		t.Errorf("big array should be skipped: %+v", a)
	}
	if a, _ := get("arr.bignested").Array(); a == nil || !a.Skipped || a.Len != 5000 {
		t.Errorf("big nested array should be skipped: %+v", a)
	}
	if _, ok := f.Get("nope"); ok {
		t.Error("missing key found")
	}
}

// (d) A 50k-token vocabulary (plus scores, token types and merges) is
// skipped: only lengths are kept, fixed-size arrays are not even read.
func TestLargeTokenizerSkipped(t *testing.T) {
	const vocab = 50_000
	b := llamaBuilder()
	b.kvs = append(b.kvs[:len(b.kvs):len(b.kvs)],
		kv{"tokenizer.ggml.scores", generated{et: ValueFloat32, n: vocab, gen: func(i int) any { return float32(i) }}},
		kv{"tokenizer.ggml.token_type", generated{et: ValueInt32, n: vocab, gen: func(i int) any { return int32(1) }}},
		kv{"tokenizer.ggml.merges", generated{et: ValueString, n: 20_000, gen: func(i int) any { return "a b" }}},
	)
	for i := range b.kvs {
		if b.kvs[i].key == "tokenizer.ggml.tokens" {
			b.kvs[i].val = tokens(vocab)
		}
	}
	data := b.bytes(t)
	path := writeTemp(t, "vocab.gguf", data)

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	f, err := openFile(path, readOptions{chunk: 64 << 10, maxChunk: 64 << 10, maxRead: DefaultMaxHeaderBytes})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	for k, n := range map[string]uint64{"tokenizer.ggml.tokens": vocab, "tokenizer.ggml.scores": vocab, "tokenizer.ggml.token_type": vocab, "tokenizer.ggml.merges": 20_000} {
		a, ok := f.Array(k)
		if !ok || !a.Skipped || a.Values != nil || a.Len != n {
			t.Errorf("%s: %+v", k, a)
		}
	}
	if s := f.Summary(); s.VocabSize != vocab {
		t.Errorf("vocab %d", s.VocabSize)
	}
	// scores + token_type (400 KB) are skipped by offset, never read.
	if f.HeaderBytesRead > int64(len(data))-2*4*vocab+3*(64<<10) {
		t.Errorf("read %d of %d bytes: fixed-size tokenizer arrays were read", f.HeaderBytesRead, len(data))
	}
	// Materialising 70k strings would allocate several MB.
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 1<<20 {
		t.Errorf("parsing allocated %d bytes", alloc)
	}
}

func TestUnknownTensorTypeKeepsGoing(t *testing.T) {
	b := builder{
		kvs: []kv{{"general.architecture", "llama"}, {"llama.block_count", uint32(1)}},
		tensors: []tensorSpec{
			{"a.weight", []uint64{64, 2}, GGMLType(99)},
			{"b.weight", []uint64{100, 2}, TypeQ4_K}, // 100 is not a multiple of 256
			{"c.weight", []uint64{64, 2}, TypeF32},
			{"d.weight", []uint64{64}, GGMLType(31)}, // removed Q4_0_4_4
		},
	}
	f, err := Parse(bytes.NewReader(b.bytes(t)), -1)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Tensors) != 4 {
		t.Fatalf("tensors %d", len(f.Tensors))
	}
	if f.Tensors[0].Err == "" || f.Tensors[1].Err == "" || f.Tensors[2].Err != "" || f.Tensors[2].Bytes != 512 || f.Tensors[3].Bytes != 36 {
		t.Errorf("tensors %+v", f.Tensors)
	}
	s := f.Summary()
	if s.UnknownTensors != 2 || s.TensorBytes != 512+36 || s.ParameterCount != 128+200+128+64 {
		t.Errorf("unknown %d bytes %d params %d", s.UnknownTensors, s.TensorBytes, s.ParameterCount)
	}
	if !hasNote(s.Notes, "unknown ggml type") || !hasNote(s.Notes, "removed from current llama.cpp") {
		t.Errorf("notes %q", s.Notes)
	}
	if len(f.Warnings) == 0 {
		t.Error("expected a warning about unknown tensors")
	}
}

func TestTruncatedDataWarning(t *testing.T) {
	b := llamaBuilder()
	b.noData = true
	data := b.bytes(t)
	f, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Warnings) != 1 || !strings.Contains(f.Warnings[0], "truncated") {
		t.Errorf("warnings %q", f.Warnings)
	}
}

// Every prefix of a valid file must fail cleanly (or, once the tensor
// table is complete, parse with a truncation warning), never panic.
func TestTruncatedPrefixes(t *testing.T) {
	data := llamaBuilder().bytes(t)
	full, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for l := 0; l < int(full.DataOffset)+64; l++ {
		p := data[:l]
		small := readOptions{chunk: 4096, maxChunk: 4096, maxRead: DefaultMaxHeaderBytes}
		f, err := parse(newWindowReader(bytes.NewReader(p), int64(l), small))
		f2, err2 := parse(newWindowReader(bytes.NewReader(p), -1, small))
		if (err == nil) != (err2 == nil) {
			t.Fatalf("len %d: known-size err %v, unknown-size err %v", l, err, err2)
		}
		if l <= int(full.DataOffset)-32 {
			if err == nil {
				t.Fatalf("len %d: parsed a truncated header", l)
			}
			if !errors.Is(err, ErrTruncated) && !errors.Is(err, ErrNotGGUF) {
				t.Fatalf("len %d: unexpected error %v", l, err)
			}
			continue
		}
		if err == nil && (len(f.Warnings) == 0 || f2 == nil) {
			t.Fatalf("len %d: no truncation warning", l)
		}
	}
}

// (e) Malformed and hostile headers.
func TestMalformed(t *testing.T) {
	head := func(version uint32, nTensors, nKV uint64) *enc {
		e := &enc{}
		e.WriteString("GGUF")
		e.u32(version)
		e.u64(nTensors)
		e.u64(nKV)
		return e
	}
	kvHead := func(nTensors, nKV uint64, key string, typ ValueType) *enc {
		e := head(3, nTensors, nKV)
		e.str(key)
		e.u32(uint32(typ))
		return e
	}
	tensorHead := func(name string, nd uint32, dims ...uint64) *enc {
		e := head(3, 1, 0)
		e.str(name)
		e.u32(nd)
		for _, d := range dims {
			e.u64(d)
		}
		e.u32(uint32(TypeF32))
		e.u64(0)
		return e
	}
	cases := []struct {
		name string
		data func() []byte
		want error
		msg  string
	}{
		{"empty", func() []byte { return nil }, ErrNotGGUF, ""},
		{"bad magic", func() []byte { return []byte("GGML\x01\x00\x00\x00") }, ErrNotGGUF, "GGML"},
		{"html", func() []byte { return []byte("<!DOCTYPE html><html>") }, ErrNotGGUF, "/resolve/"},
		{"v1", func() []byte { return head(1, 0, 0).Bytes() }, ErrUnsupportedVersion, "version 1"},
		{"v9", func() []byte { return head(9, 0, 0).Bytes() }, ErrUnsupportedVersion, "version 9"},
		{"big-endian", func() []byte { return []byte("GGUF\x00\x00\x00\x03" + strings.Repeat("\x00", 16)) }, ErrBigEndian, ""},
		{"byte-swapped magic", func() []byte { return []byte("FUGG\x03\x00\x00\x00") }, ErrBigEndian, ""},
		{"huge tensor count", func() []byte { return head(3, 1<<40, 0).Bytes() }, ErrLimit, "tensor count"},
		{"tensor count over limit", func() []byte { return head(3, MaxTensors+1, 0).Bytes() }, ErrLimit, "tensor count"},
		{"huge kv count", func() []byte { return head(3, 0, 1<<62).Bytes() }, ErrLimit, "kv count"},
		{"huge key length", func() []byte { e := head(3, 0, 1); e.u64(1 << 40); return e.Bytes() }, ErrLimit, "key length"},
		{"huge string value", func() []byte { e := kvHead(0, 1, "k", ValueString); e.u64(1 << 62); return e.Bytes() }, ErrLimit, "string length"},
		{"string just over cap", func() []byte { e := kvHead(0, 1, "k", ValueString); e.u64(MaxStringLen + 1); return e.Bytes() }, ErrLimit, "string length"},
		{"string past EOF", func() []byte {
			e := kvHead(0, 1, "k", ValueString)
			e.u64(1000)
			e.WriteString("abc")
			return e.Bytes()
		}, ErrTruncated, ""},
		{"truncated scalar", func() []byte { e := kvHead(0, 1, "k", ValueUint64); e.WriteString("ab"); return e.Bytes() }, ErrTruncated, ""},
		{"unknown value type", func() []byte { e := kvHead(0, 1, "k", ValueType(99)); return e.Bytes() }, ErrMalformed, "value type 99"},
		{"unknown array type", func() []byte { e := kvHead(0, 1, "k", ValueArray); e.u32(42); e.u64(1); return e.Bytes() }, ErrMalformed, "element type 42"},
		{"huge array", func() []byte {
			e := kvHead(0, 1, "k", ValueArray)
			e.u32(uint32(ValueUint8))
			e.u64(1 << 60)
			return e.Bytes()
		}, ErrLimit, "array length"},
		{"array past EOF", func() []byte {
			e := kvHead(0, 1, "k", ValueArray)
			e.u32(uint32(ValueUint64))
			e.u64(MaxArrayLen)
			return e.Bytes()
		}, ErrTruncated, ""},
		{"huge string in skipped array", func() []byte {
			e := kvHead(0, 1, "tokenizer.ggml.tokens", ValueArray)
			e.u32(uint32(ValueString))
			e.u64(2)
			e.str("ok")
			e.u64(1 << 50)
			return e.Bytes()
		}, ErrLimit, "string length"},
		{"nesting bomb", func() []byte {
			e := kvHead(0, 1, "k", ValueArray)
			for i := 0; i < 100; i++ {
				e.u32(uint32(ValueArray))
				e.u64(1)
			}
			return e.Bytes()
		}, ErrLimit, "nested"},
		{"duplicate key", func() []byte {
			e := head(3, 0, 2)
			for i := 0; i < 2; i++ {
				e.str("k")
				e.tagged(uint32(1))
			}
			return e.Bytes()
		}, ErrMalformed, "duplicate key"},
		{"bad alignment", func() []byte { e := head(3, 0, 1); e.str("general.alignment"); e.tagged(uint32(48)); return e.Bytes() }, ErrMalformed, "alignment"},
		{"zero alignment", func() []byte { e := head(3, 0, 1); e.str("general.alignment"); e.tagged(uint32(0)); return e.Bytes() }, ErrMalformed, "alignment"},
		{"string alignment", func() []byte { e := head(3, 0, 1); e.str("general.alignment"); e.tagged("32"); return e.Bytes() }, ErrMalformed, "alignment"},
		{"too many dims", func() []byte { return tensorHead("t", 5, 1, 1, 1, 1, 1).Bytes() }, ErrMalformed, "dimensions"},
		{"huge n_dims", func() []byte { return tensorHead("t", 1<<31).Bytes() }, ErrMalformed, "dimensions"},
		{"dims overflow", func() []byte { return tensorHead("t", 3, 1<<40, 1<<40, 1<<40).Bytes() }, ErrMalformed, "overflow"},
		{"negative dim", func() []byte { return tensorHead("t", 1, math.MaxUint64).Bytes() }, ErrMalformed, "overflow"},
		{"huge tensor name", func() []byte { e := head(3, 1, 0); e.u64(1 << 33); return e.Bytes() }, ErrLimit, "tensor name"},
		{"tensor count lies", func() []byte {
			e := head(3, 1000, 0)
			e.str("t")
			e.u32(1)
			e.u64(32)
			e.u32(0)
			e.u64(0)
			return e.Bytes()
		}, ErrTruncated, "tensor 1"},
		{"duplicate tensor", func() []byte {
			return builder{tensors: []tensorSpec{{"t", []uint64{32}, TypeF32}, {"t", []uint64{32}, TypeF32}}}.bytes(t)
		}, ErrMalformed, "duplicate tensor"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data := c.data()
			for _, size := range []int64{int64(len(data)), -1} {
				f, err := Parse(bytes.NewReader(data), size)
				if err == nil {
					t.Fatalf("size %d: parsed: %+v", size, f)
				}
				if !errors.Is(err, c.want) {
					t.Fatalf("size %d: error %v, want %v", size, err, c.want)
				}
				if c.msg != "" && !strings.Contains(err.Error(), c.msg) {
					t.Fatalf("size %d: error %q does not mention %q", size, err, c.msg)
				}
			}
		})
	}
}

func TestHeaderReadCap(t *testing.T) {
	b := llamaBuilder()
	for i := range b.kvs {
		if b.kvs[i].key == "tokenizer.ggml.tokens" {
			b.kvs[i].val = tokens(50_000)
		}
	}
	path := writeTemp(t, "big.gguf", b.bytes(t))
	_, err := openFile(path, readOptions{chunk: 4096, maxChunk: 4096, maxRead: 100_000})
	if !errors.Is(err, ErrHeaderTooLarge) {
		t.Fatalf("err = %v, want ErrHeaderTooLarge", err)
	}
	if _, err := Open(path + ".missing"); err == nil {
		t.Error("opening a missing file should fail")
	}
	if _, err := Open(t.TempDir()); err == nil {
		t.Error("opening a directory should fail")
	}
}

func hasNote(notes []string, sub string) bool {
	for _, n := range notes {
		if strings.Contains(n, sub) {
			return true
		}
	}
	return false
}
