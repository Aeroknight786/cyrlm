package gguf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestStreamingWriterRoundTrip(t *testing.T) {
	var b bytes.Buffer
	metadata := map[string]any{"general.architecture": "llama", "llama.block_count": uint32(2), "llama.embedding_length": uint32(256), "epsilon": float32(1e-6), "enabled": true, "tokenizer.ggml.tokens": []string{"a", "b"}, "tokenizer.ggml.scores": []float32{0, 1}, "tokenizer.ggml.token_type": []int32{1, 2}, "counts": []uint32{1, 2}, "pattern": []bool{false, true}}
	ts := []WriteTensor{{Name: "output.weight", Dimensions: []uint64{256, 2}, Type: TypeQ4_K, Fill: func(ctx context.Context, w io.Writer, n uint64) error {
		_, e := io.CopyN(w, bytes.NewReader(make([]byte, n)), int64(n))
		return e
	}}}
	if e := Write(context.Background(), &b, metadata, ts); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(t.TempDir(), "model.gguf")
	if e := os.WriteFile(path, b.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	f, e := Open(path)
	if e != nil {
		t.Fatal(e)
	}
	if f.DataOffset%32 != 0 || len(f.Tensors) != 1 || f.Tensors[0].Bytes != 288 || f.Tensors[0].Dims[1] != 2 {
		t.Fatalf("bad header: %+v", f)
	}
	var again bytes.Buffer
	if e = Write(context.Background(), &again, metadata, ts); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(b.Bytes(), again.Bytes()) {
		t.Fatal("metadata map order changed deterministic output")
	}
}
func TestStreamingWriterRejectsInvalidPayload(t *testing.T) {
	fill := func(context.Context, io.Writer, uint64) error { return nil }
	for _, ts := range [][]WriteTensor{
		{{Name: "bad", Dimensions: []uint64{128, 2}, Type: TypeQ4_K, Fill: fill}},
		{{Name: "bad", Dimensions: []uint64{0}, Type: TypeF32, Fill: fill}},
		{{Name: "short", Dimensions: []uint64{1}, Type: TypeF32, Fill: fill}},
		{{Name: "no-fill", Dimensions: []uint64{1}, Type: TypeF32}},
		{{Name: "duplicate", Dimensions: []uint64{1}, Type: TypeF32, Fill: fill}, {Name: "duplicate", Dimensions: []uint64{1}, Type: TypeF32, Fill: fill}},
	} {
		if e := Write(context.Background(), io.Discard, nil, ts); e == nil {
			t.Fatalf("accepted %+v", ts)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := Write(ctx, io.Discard, nil, []WriteTensor{{Name: "cancel", Dimensions: []uint64{1}, Type: TypeF32, Fill: fill}})
	if !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if e = Write(context.Background(), io.Discard, map[string]any{"bad": complex(1, 2)}, nil); e == nil {
		t.Fatal("unsupported metadata accepted")
	}
}
