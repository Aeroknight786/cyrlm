// Package gguf reads the header of a GGUF model file (the metadata
// key/values and the tensor descriptors) without reading any tensor data.
//
// It works on local files (Open) and on HTTP(S) URLs through Range requests
// (OpenURL), so the exact architecture and per-tensor byte sizes of a
// multi-gigabyte model are known after fetching only its first few
// megabytes. Summary condenses the metadata into the quantities a memory and
// speed predictor needs: parameter counts, weight bytes read per token
// (active experts only for MoE), KV-cache bytes per token for full-attention
// and sliding-window layers, and recurrent state for SSM/hybrid models.
//
// Format reference: https://github.com/ggml-org/ggml/blob/master/docs/gguf.md
// (versions 2 and 3, little-endian). Key names follow llama.cpp's
// src/llama-arch.cpp; tensor type sizes follow ggml/src/ggml.c type_traits.
//
// The parser is defensive: every count and length read from the file is
// checked against the limits below before anything is allocated, large
// arrays (tokenizer vocabularies, merges, scores) are skipped rather than
// decoded, and the number of header bytes read is capped, so a malformed or
// hostile file cannot cause large allocations or unbounded downloads.
package gguf

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Limits applied while parsing. A file exceeding one is rejected with an
// error rather than partially parsed.
const (
	// MaxStringLen caps any single string value (chat templates are the
	// largest legitimate strings, typically < 64 KiB).
	MaxStringLen = 16 << 20
	// MaxKeyLen caps metadata key length (the GGUF spec says keys are at
	// most 2^16-1 bytes).
	MaxKeyLen = 1<<16 - 1
	// MaxTensorNameLen caps tensor names (ggml itself uses 64).
	MaxTensorNameLen = 1<<16 - 1
	// MaxTensors caps the tensor count.
	MaxTensors = 1_000_000
	// MaxKVs caps the metadata key/value count.
	MaxKVs = 100_000
	// MaxDims is GGML_MAX_DIMS.
	MaxDims = 4
	// MaxArrayLen caps the declared length of any metadata array.
	MaxArrayLen = 1 << 28
	// MaxArrayDepth caps nesting of arrays of arrays.
	MaxArrayDepth = 8
	// MaxMaterializedArrayLen is the longest array whose elements are
	// decoded and kept; longer arrays (and every tokenizer.ggml.* array)
	// only have their element type and length recorded.
	MaxMaterializedArrayLen = 4096
	// maxMaterializedTotal caps decoded array elements across the file.
	maxMaterializedTotal = 1 << 18
	// DefaultMaxHeaderBytes caps the bytes read to parse a header (real
	// headers are a few MiB, dominated by the tokenizer).
	DefaultMaxHeaderBytes = 64 << 20
	// DefaultAlignment is used when general.alignment is absent.
	DefaultAlignment = 32
)

// Errors returned (wrapped) by the parser and readers. Use errors.Is.
var (
	ErrNotGGUF            = errors.New("gguf: not a GGUF file")
	ErrBigEndian          = errors.New("gguf: big-endian GGUF files are not supported")
	ErrUnsupportedVersion = errors.New("gguf: unsupported GGUF version")
	ErrTruncated          = errors.New("gguf: unexpected end of file")
	ErrLimit              = errors.New("gguf: limit exceeded")
	ErrMalformed          = errors.New("gguf: malformed header")
	ErrHeaderTooLarge     = errors.New("gguf: header read limit exceeded")
	ErrRangeNotSupported  = errors.New("gguf: server ignored the HTTP Range request")
)

// ValueType is the type tag of a metadata value (gguf_type).
type ValueType uint32

// Metadata value types.
const (
	ValueUint8   ValueType = 0
	ValueInt8    ValueType = 1
	ValueUint16  ValueType = 2
	ValueInt16   ValueType = 3
	ValueUint32  ValueType = 4
	ValueInt32   ValueType = 5
	ValueFloat32 ValueType = 6
	ValueBool    ValueType = 7
	ValueString  ValueType = 8
	ValueArray   ValueType = 9
	ValueUint64  ValueType = 10
	ValueInt64   ValueType = 11
	ValueFloat64 ValueType = 12
)

var valueTypeNames = [...]string{"uint8", "int8", "uint16", "int16", "uint32", "int32", "float32", "bool", "string", "array", "uint64", "int64", "float64"}

func (t ValueType) String() string {
	if int(t) < len(valueTypeNames) {
		return valueTypeNames[t]
	}
	return "type(" + strconv.FormatUint(uint64(t), 10) + ")"
}

func (t ValueType) valid() bool { return t <= ValueFloat64 }

// size is the encoded size of a fixed-size type, 0 for string and array.
func (t ValueType) size() int {
	switch t {
	case ValueUint8, ValueInt8, ValueBool:
		return 1
	case ValueUint16, ValueInt16:
		return 2
	case ValueUint32, ValueInt32, ValueFloat32:
		return 4
	case ValueUint64, ValueInt64, ValueFloat64:
		return 8
	}
	return 0
}

// Value is one metadata value. Scalars hold the Go value of the matching
// width (uint8 … float64, bool, string); arrays hold an *Array.
type Value struct {
	Type ValueType
	v    any
}

// Array is a metadata array. Elements are decoded only for short arrays
// that are not tokenizer data; otherwise Skipped is true, Values is nil and
// only ElemType and Len are known.
type Array struct {
	ElemType ValueType
	Len      uint64
	Values   []Value
	Skipped  bool
}

// Raw returns the underlying Go value (*Array for arrays).
func (v Value) Raw() any { return v.v }

// Uint returns an integer value as uint64; false for negative values or
// non-integer types.
func (v Value) Uint() (uint64, bool) {
	switch x := v.v.(type) {
	case uint8:
		return uint64(x), true
	case uint16:
		return uint64(x), true
	case uint32:
		return uint64(x), true
	case uint64:
		return x, true
	case int8, int16, int32, int64:
		i, _ := v.Int()
		if i < 0 {
			return 0, false
		}
		return uint64(i), true
	}
	return 0, false
}

// Int returns an integer value as int64; false for non-integer types or
// uint64 values above MaxInt64.
func (v Value) Int() (int64, bool) {
	switch x := v.v.(type) {
	case int8:
		return int64(x), true
	case int16:
		return int64(x), true
	case int32:
		return int64(x), true
	case int64:
		return x, true
	case uint8, uint16, uint32:
		u, _ := v.Uint()
		return int64(u), true
	case uint64:
		if x > math.MaxInt64 {
			return 0, false
		}
		return int64(x), true
	}
	return 0, false
}

// Float returns any numeric value as float64.
func (v Value) Float() (float64, bool) {
	switch x := v.v.(type) {
	case float32:
		return float64(x), true
	case float64:
		return x, true
	}
	if i, ok := v.Int(); ok {
		return float64(i), true
	}
	if u, ok := v.Uint(); ok {
		return float64(u), true
	}
	return 0, false
}

// Bool returns a bool value.
func (v Value) Bool() (bool, bool) {
	b, ok := v.v.(bool)
	return b, ok
}

// Str returns a string value.
func (v Value) Str() (string, bool) {
	s, ok := v.v.(string)
	return s, ok
}

// Array returns an array value.
func (v Value) Array() (*Array, bool) {
	a, ok := v.v.(*Array)
	return a, ok && a != nil
}

// String formats the value for display; long strings and arrays are
// abbreviated.
func (v Value) String() string {
	switch x := v.v.(type) {
	case string:
		if len(x) > 80 {
			return strconv.Quote(x[:77]+"...") + fmt.Sprintf(" (%d bytes)", len(x))
		}
		return strconv.Quote(x)
	case *Array:
		if x == nil {
			return "[]"
		}
		if x.Skipped {
			return fmt.Sprintf("[%d x %s]", x.Len, x.ElemType)
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, e := range x.Values {
			if i == 16 {
				fmt.Fprintf(&b, ", ... (%d total)", x.Len)
				break
			}
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(e.String())
		}
		b.WriteByte(']')
		return b.String()
	case nil:
		return "<nil>"
	}
	return fmt.Sprint(v.v)
}

// KV is one metadata key/value pair.
type KV struct {
	Key   string
	Value Value
}

// TensorInfo describes one tensor. Sizes are computed from the shape and
// the ggml type table; Err is set (and Bytes is 0) when that is impossible
// (unknown type, row length not a multiple of the type's block size).
type TensorInfo struct {
	Name string
	// Dims is the shape, innermost (row) dimension first, as in ggml.
	Dims []uint64
	Type GGMLType
	// Offset is relative to the start of the data section (File.DataOffset).
	Offset uint64
	// Elements is the product of Dims.
	Elements uint64
	// Bytes is the stored size of the tensor data.
	Bytes uint64
	// Split is the index of the split part holding the tensor (0 unless
	// the File combines split parts).
	Split int
	// Err explains why Bytes could not be computed.
	Err string
}

// File is a parsed GGUF header.
type File struct {
	// Source is the path or URL the header was read from.
	Source string
	// Version is the GGUF version (2 or 3).
	Version uint32
	// Alignment is general.alignment or DefaultAlignment.
	Alignment uint64
	// KVs holds the metadata in file order.
	KVs []KV
	// Tensors holds the tensor descriptors in file order.
	Tensors []TensorInfo
	// DataOffset is the absolute offset of the tensor data section.
	DataOffset int64
	// FileSize is the total file size in bytes, -1 when unknown (summed
	// over parts for a merged split model).
	FileSize int64
	// HeaderBytesRead is the number of bytes actually read or downloaded
	// to parse the header.
	HeaderBytesRead int64
	// Warnings lists non-fatal problems (e.g. tensor data extending past the
	// end of the file, which means a truncated download).
	Warnings []string

	index map[string]int
}

// Get returns the value of key. A File returned by this package is safe for
// concurrent reads.
func (f *File) Get(key string) (Value, bool) {
	if f.index == nil {
		for _, kv := range f.KVs {
			if kv.Key == key {
				return kv.Value, true
			}
		}
		return Value{}, false
	}
	i, ok := f.index[key]
	if !ok {
		return Value{}, false
	}
	return f.KVs[i].Value, true
}

// String returns a string value.
func (f *File) String(key string) (string, bool) {
	v, ok := f.Get(key)
	if !ok {
		return "", false
	}
	return v.Str()
}

// Uint returns a non-negative integer value.
func (f *File) Uint(key string) (uint64, bool) {
	v, ok := f.Get(key)
	if !ok {
		return 0, false
	}
	return v.Uint()
}

// Int returns an integer value.
func (f *File) Int(key string) (int64, bool) {
	v, ok := f.Get(key)
	if !ok {
		return 0, false
	}
	return v.Int()
}

// Float returns a numeric value.
func (f *File) Float(key string) (float64, bool) {
	v, ok := f.Get(key)
	if !ok {
		return 0, false
	}
	return v.Float()
}

// Bool returns a bool value.
func (f *File) Bool(key string) (bool, bool) {
	v, ok := f.Get(key)
	if !ok {
		return false, false
	}
	return v.Bool()
}

// Array returns an array value.
func (f *File) Array(key string) (*Array, bool) {
	v, ok := f.Get(key)
	if !ok {
		return nil, false
	}
	return v.Array()
}

// Architecture returns general.architecture ("" if absent).
func (f *File) Architecture() string {
	s, _ := f.String("general.architecture")
	return s
}

// Tensor returns the descriptor of the named tensor.
func (f *File) Tensor(name string) (TensorInfo, bool) {
	for _, t := range f.Tensors {
		if t.Name == name {
			return t, true
		}
	}
	return TensorInfo{}, false
}
