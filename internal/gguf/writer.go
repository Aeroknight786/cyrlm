package gguf

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
)

// WriteTensor describes a tensor in GGML dimension order (fastest first).
// Fill must write exactly the requested number of bytes.
type WriteTensor struct {
	Name       string
	Dimensions []uint64
	Type       GGMLType
	Fill       func(context.Context, io.Writer, uint64) error
}

// Write writes a GGUF v3 stream with 32-byte aligned tensors. Metadata supports
// strings, uint32, float32, bool, and arrays of those types. Tensor payloads are
// streamed, so even a large synthetic model needs only a small working buffer.
func Write(ctx context.Context, w io.Writer, metadata map[string]any, tensors []WriteTensor) error {
	var h bytes.Buffer
	put := func(v any) { _ = binary.Write(&h, binary.LittleEndian, v) }
	str := func(s string) { put(uint64(len(s))); h.WriteString(s) }
	h.WriteString("GGUF")
	put(uint32(3))
	put(uint64(len(tensors)))
	put(uint64(len(metadata)))
	keys := make([]string, 0, len(metadata))
	for k := range metadata {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		str(k)
		switch v := metadata[k].(type) {
		case string:
			put(uint32(ValueString))
			str(v)
		case uint32:
			put(uint32(ValueUint32))
			put(v)
		case float32:
			put(uint32(ValueFloat32))
			put(v)
		case bool:
			put(uint32(ValueBool))
			put(v)
		case []string:
			put(uint32(ValueArray))
			put(uint32(ValueString))
			put(uint64(len(v)))
			for _, x := range v {
				str(x)
			}
		case []uint32:
			put(uint32(ValueArray))
			put(uint32(ValueUint32))
			put(uint64(len(v)))
			for _, x := range v {
				put(x)
			}
		case []int32:
			put(uint32(ValueArray))
			put(uint32(ValueInt32))
			put(uint64(len(v)))
			for _, x := range v {
				put(x)
			}
		case []float32:
			put(uint32(ValueArray))
			put(uint32(ValueFloat32))
			put(uint64(len(v)))
			for _, x := range v {
				put(x)
			}
		case []bool:
			put(uint32(ValueArray))
			put(uint32(ValueBool))
			put(uint64(len(v)))
			for _, x := range v {
				put(x)
			}
		default:
			return fmt.Errorf("GGUF metadata %s: unsupported type %T", k, v)
		}
	}
	sizes := make([]uint64, len(tensors))
	var off uint64
	seen := map[string]bool{}
	for i, t := range tensors {
		if seen[t.Name] || t.Name == "" || len(t.Dimensions) == 0 || len(t.Dimensions) > 4 {
			return fmt.Errorf("invalid tensor %q", t.Name)
		}
		seen[t.Name] = true
		n := uint64(1)
		for _, d := range t.Dimensions {
			if d == 0 || n > math.MaxUint64/d {
				return fmt.Errorf("tensor %s: invalid dimensions", t.Name)
			}
			n *= d
		}
		tr, ok := t.Type.Traits()
		if !ok || tr.Removed || t.Dimensions[0]%tr.BlockSize != 0 {
			return fmt.Errorf("tensor %s: invalid row type or alignment", t.Name)
		}
		sz, err := RowBytes(t.Type, n)
		if err != nil {
			return err
		}
		sizes[i] = sz
		str(t.Name)
		put(uint32(len(t.Dimensions)))
		for _, d := range t.Dimensions {
			put(d)
		}
		put(uint32(t.Type))
		put(off)
		if sz > math.MaxUint64-off-31 {
			return fmt.Errorf("GGUF tensor data too large")
		}
		off = (off + sz + 31) / 32 * 32
	}
	for h.Len()%32 != 0 {
		h.WriteByte(0)
	}
	if n, err := w.Write(h.Bytes()); err != nil {
		return err
	} else if n != h.Len() {
		return io.ErrShortWrite
	}
	for i, t := range tensors {
		if err := ctx.Err(); err != nil {
			return err
		}
		if t.Fill == nil {
			return fmt.Errorf("tensor %s: no payload writer", t.Name)
		}
		cw := &countWriter{w: w}
		if err := t.Fill(ctx, cw, sizes[i]); err != nil {
			return err
		}
		if cw.n != sizes[i] {
			return fmt.Errorf("tensor %s: wrote %d bytes, expected %d", t.Name, cw.n, sizes[i])
		}
		if pad := (32 - sizes[i]%32) % 32; pad > 0 {
			if _, err := w.Write(make([]byte, pad)); err != nil {
				return err
			}
		}
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n uint64
}

func (w *countWriter) Write(p []byte) (int, error) {
	n, e := w.w.Write(p)
	w.n += uint64(n)
	return n, e
}
