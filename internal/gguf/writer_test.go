package gguf

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// A minimal GGUF writer for building synthetic test files.

// nested is an explicitly typed array value (needed for arrays of arrays
// and for element types the Go slice type does not imply).
type nested struct {
	et    ValueType
	elems []any
}

// generated is an array whose elements are produced on the fly, for large
// arrays (tokenizer vocabularies) that should not be built up front.
type generated struct {
	et  ValueType
	n   int
	gen func(i int) any
}

type kv struct {
	key string
	val any
}

type tensorSpec struct {
	name string
	dims []uint64
	typ  GGMLType
}

type builder struct {
	version   uint32 // 0 = 3
	alignment uint64 // 0 = 32; written as general.alignment when != 32
	kvs       []kv
	tensors   []tensorSpec
	noData    bool // stop after the header padding (truncated download)
}

type enc struct{ bytes.Buffer }

func (e *enc) u32(v uint32) { _ = binary.Write(&e.Buffer, binary.LittleEndian, v) }
func (e *enc) u64(v uint64) { _ = binary.Write(&e.Buffer, binary.LittleEndian, v) }
func (e *enc) str(s string) { e.u64(uint64(len(s))); e.WriteString(s) }

func typeOf(v any) ValueType {
	switch x := v.(type) {
	case uint8:
		return ValueUint8
	case int8:
		return ValueInt8
	case uint16:
		return ValueUint16
	case int16:
		return ValueInt16
	case uint32:
		return ValueUint32
	case int32:
		return ValueInt32
	case float32:
		return ValueFloat32
	case bool:
		return ValueBool
	case string:
		return ValueString
	case uint64:
		return ValueUint64
	case int64:
		return ValueInt64
	case float64:
		return ValueFloat64
	case []string, []uint32, []int32, []float32, []bool, []uint64, nested, generated:
		return ValueArray
	default:
		panic(fmt.Sprintf("typeOf: unsupported %T", x))
	}
}

// tagged writes the type tag then the value.
func (e *enc) tagged(v any) {
	e.u32(uint32(typeOf(v)))
	e.raw(v)
}

func (e *enc) raw(v any) {
	switch x := v.(type) {
	case uint8:
		e.WriteByte(x)
	case int8:
		e.WriteByte(byte(x))
	case uint16, int16, uint32, int32, uint64, int64:
		_ = binary.Write(&e.Buffer, binary.LittleEndian, x)
	case float32:
		e.u32(math.Float32bits(x))
	case float64:
		e.u64(math.Float64bits(x))
	case bool:
		if x {
			e.WriteByte(1)
		} else {
			e.WriteByte(0)
		}
	case string:
		e.str(x)
	case []string:
		e.arr(ValueString, len(x), func(i int) any { return x[i] })
	case []uint32:
		e.arr(ValueUint32, len(x), func(i int) any { return x[i] })
	case []int32:
		e.arr(ValueInt32, len(x), func(i int) any { return x[i] })
	case []float32:
		e.arr(ValueFloat32, len(x), func(i int) any { return x[i] })
	case []bool:
		e.arr(ValueBool, len(x), func(i int) any { return x[i] })
	case []uint64:
		e.arr(ValueUint64, len(x), func(i int) any { return x[i] })
	case nested:
		e.arr(x.et, len(x.elems), func(i int) any { return x.elems[i] })
	case generated:
		e.arr(x.et, x.n, x.gen)
	default:
		panic(fmt.Sprintf("raw: unsupported %T", x))
	}
}

func (e *enc) arr(et ValueType, n int, at func(int) any) {
	e.u32(uint32(et))
	e.u64(uint64(n))
	for i := 0; i < n; i++ {
		e.raw(at(i))
	}
}

func (b builder) bytes(t testing.TB) []byte {
	t.Helper()
	version := b.version
	if version == 0 {
		version = 3
	}
	align := b.alignment
	if align == 0 {
		align = 32
	}
	kvs := b.kvs
	if align != 32 {
		kvs = append([]kv{{"general.alignment", uint32(align)}}, kvs...)
	}
	var e enc
	e.WriteString("GGUF")
	e.u32(version)
	e.u64(uint64(len(b.tensors)))
	e.u64(uint64(len(kvs)))
	for _, p := range kvs {
		e.str(p.key)
		e.tagged(p.val)
	}
	var off uint64
	sizes := make([]uint64, len(b.tensors))
	for i, ts := range b.tensors {
		n := uint64(1)
		for _, d := range ts.dims {
			n *= d
		}
		sz, err := RowBytes(ts.typ, n)
		if err != nil {
			sz = 0 // unknown type in a negative test: no data
		}
		sizes[i] = sz
		e.str(ts.name)
		e.u32(uint32(len(ts.dims)))
		for _, d := range ts.dims {
			e.u64(d)
		}
		e.u32(uint32(ts.typ))
		e.u64(off)
		off += sz
		off = (off + align - 1) / align * align
	}
	for uint64(e.Len())%align != 0 {
		e.WriteByte(0)
	}
	if b.noData {
		return e.Bytes()
	}
	data := make([]byte, off)
	e.Write(data)
	return e.Bytes()
}

func writeTemp(t testing.TB, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func tokens(n int) generated {
	return generated{et: ValueString, n: n, gen: func(i int) any { return fmt.Sprintf("tok%d", i) }}
}
