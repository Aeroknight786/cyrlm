package gguf

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/bits"
	"strings"
)

type parser struct {
	r            *windowReader
	off          int64
	materialized int
}

func (p *parser) bytes(n int) ([]byte, error) {
	b, err := p.r.slice(p.off, n)
	if err != nil {
		return nil, err
	}
	p.off += int64(n)
	return b, nil
}

// skip advances past n bytes without reading them.
func (p *parser) skip(n uint64) error {
	if n > uint64(math.MaxInt64-p.off) || (p.r.size >= 0 && int64(n) > p.r.size-p.off) {
		return fmt.Errorf("%w: skipping %d bytes at offset %d goes past the end of the file", ErrTruncated, n, p.off)
	}
	p.off += int64(n)
	return nil
}

func (p *parser) u32() (uint32, error) {
	b, err := p.bytes(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(b), nil
}

func (p *parser) u64() (uint64, error) {
	b, err := p.bytes(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(b), nil
}

func (p *parser) strLen(limit int, what string) (int, error) {
	n, err := p.u64()
	if err != nil {
		return 0, err
	}
	if n > uint64(limit) {
		return 0, fmt.Errorf("%w: %s length %d exceeds %d", ErrLimit, what, n, limit)
	}
	return int(n), nil
}

func (p *parser) str(limit int, what string) (string, error) {
	n, err := p.strLen(limit, what)
	if err != nil {
		return "", err
	}
	b, err := p.bytes(n)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parse(w *windowReader) (*File, error) {
	p := &parser{r: w}
	magic, err := p.bytes(4)
	if err != nil {
		return nil, fmt.Errorf("%w: file shorter than 4 bytes", ErrNotGGUF)
	}
	switch m := string(magic); {
	case m == "GGUF":
	case m == "FUGG":
		return nil, fmt.Errorf("%w (byte-swapped magic)", ErrBigEndian)
	case m[0] == '<':
		return nil, fmt.Errorf("%w: got HTML (for Hugging Face use a .../resolve/<rev>/<file>.gguf URL, not /blob/)", ErrNotGGUF)
	default:
		return nil, fmt.Errorf("%w: magic %q", ErrNotGGUF, m)
	}

	version, err := p.u32()
	if err != nil {
		return nil, fmt.Errorf("reading version: %w", err)
	}
	switch {
	case version == 2 || version == 3:
	case version == 1:
		return nil, fmt.Errorf("%w: version 1 (2023 pre-release format; re-convert the model)", ErrUnsupportedVersion)
	case version&0xffff == 0 && bits.ReverseBytes32(version) >= 1 && bits.ReverseBytes32(version) <= 3:
		return nil, fmt.Errorf("%w (version %d stored big-endian)", ErrBigEndian, bits.ReverseBytes32(version))
	default:
		return nil, fmt.Errorf("%w: version %d", ErrUnsupportedVersion, version)
	}

	nTensors, err := p.u64()
	if err != nil {
		return nil, fmt.Errorf("reading tensor count: %w", err)
	}
	nKV, err := p.u64()
	if err != nil {
		return nil, fmt.Errorf("reading kv count: %w", err)
	}
	if nTensors > MaxTensors {
		return nil, fmt.Errorf("%w: tensor count %d exceeds %d", ErrLimit, nTensors, MaxTensors)
	}
	if nKV > MaxKVs {
		return nil, fmt.Errorf("%w: kv count %d exceeds %d", ErrLimit, nKV, MaxKVs)
	}

	f := &File{Version: version, Alignment: DefaultAlignment, FileSize: w.size}
	f.KVs = make([]KV, 0, min(nKV, 1024))
	f.index = make(map[string]int, min(nKV, 1024))
	for i := uint64(0); i < nKV; i++ {
		key, err := p.str(MaxKeyLen, "key")
		if err != nil {
			return nil, fmt.Errorf("kv %d: reading key: %w", i, err)
		}
		if _, dup := f.index[key]; dup {
			return nil, fmt.Errorf("%w: duplicate key %q", ErrMalformed, key)
		}
		t, err := p.u32()
		if err != nil {
			return nil, fmt.Errorf("kv %d (%s): reading type: %w", i, key, err)
		}
		// Tokenizer arrays (tokens, scores, token_type, merges) are huge and
		// irrelevant for prediction: record their length only.
		keep := !strings.HasPrefix(key, "tokenizer.ggml.")
		v, err := p.value(ValueType(t), keep)
		if err != nil {
			return nil, fmt.Errorf("kv %d (%s): %w", i, key, err)
		}
		f.index[key] = len(f.KVs)
		f.KVs = append(f.KVs, KV{Key: key, Value: v})
	}

	if v, ok := f.Get("general.alignment"); ok {
		a, ok := v.Uint()
		if !ok || a == 0 || a&(a-1) != 0 || a > 1<<30 {
			return nil, fmt.Errorf("%w: general.alignment %v is not a power of two", ErrMalformed, v)
		}
		f.Alignment = a
	}

	f.Tensors = make([]TensorInfo, 0, min(nTensors, 4096))
	names := make(map[string]struct{}, min(nTensors, 4096))
	for i := uint64(0); i < nTensors; i++ {
		t, err := p.tensorInfo()
		if err != nil {
			return nil, fmt.Errorf("tensor %d: %w", i, err)
		}
		if _, dup := names[t.Name]; dup {
			return nil, fmt.Errorf("%w: duplicate tensor %q", ErrMalformed, t.Name)
		}
		names[t.Name] = struct{}{}
		f.Tensors = append(f.Tensors, t)
	}

	// With an unknown size, a skip may have jumped past the end unnoticed:
	// confirm the last header byte exists.
	if w.size < 0 && p.off > 0 {
		if _, err := w.slice(p.off-1, 1); err != nil {
			return nil, fmt.Errorf("end of header: %w", err)
		}
	}
	f.DataOffset = alignUp(p.off, int64(f.Alignment))
	f.HeaderBytesRead = w.read
	f.checkLayout()
	return f, nil
}

func alignUp(off, a int64) int64 {
	if r := off % a; r != 0 {
		return off + a - r
	}
	return off
}

func (p *parser) tensorInfo() (TensorInfo, error) {
	var t TensorInfo
	name, err := p.str(MaxTensorNameLen, "tensor name")
	if err != nil {
		return t, err
	}
	t.Name = name
	nd, err := p.u32()
	if err != nil {
		return t, fmt.Errorf("%s: %w", name, err)
	}
	if nd > MaxDims {
		return t, fmt.Errorf("%w: tensor %s has %d dimensions (max %d)", ErrMalformed, name, nd, MaxDims)
	}
	t.Dims = make([]uint64, nd)
	t.Elements = 1
	for j := range t.Dims {
		d, err := p.u64()
		if err != nil {
			return t, fmt.Errorf("%s: %w", name, err)
		}
		if d > math.MaxInt64 || (d != 0 && t.Elements > math.MaxInt64/d) {
			return t, fmt.Errorf("%w: tensor %s element count overflows", ErrMalformed, name)
		}
		t.Dims[j] = d
		t.Elements *= d
	}
	typ, err := p.u32()
	if err != nil {
		return t, fmt.Errorf("%s: %w", name, err)
	}
	t.Type = GGMLType(typ)
	if t.Offset, err = p.u64(); err != nil {
		return t, fmt.Errorf("%s: %w", name, err)
	}
	t.computeSize()
	return t, nil
}

func (t *TensorInfo) computeSize() {
	tr, ok := t.Type.Traits()
	if !ok {
		t.Err = fmt.Sprintf("unknown ggml type %d", uint32(t.Type))
		return
	}
	if len(t.Dims) == 0 || t.Dims[0]%tr.BlockSize != 0 {
		t.Err = fmt.Sprintf("row length %v is not a multiple of the %s block size %d", t.Dims, tr.Name, tr.BlockSize)
		return
	}
	blocks := t.Elements / tr.BlockSize
	if blocks > math.MaxInt64/tr.TypeSize {
		t.Err = "tensor size overflows"
		return
	}
	t.Bytes = blocks * tr.TypeSize
}

// checkLayout records warnings for tensor data that is misaligned or lies
// beyond the end of the file (a truncated or partial download).
func (f *File) checkLayout() {
	var misaligned, pastEnd, unknown int
	var firstPastEnd string
	for _, t := range f.Tensors {
		if t.Err != "" {
			unknown++
			continue
		}
		if t.Offset%f.Alignment != 0 {
			misaligned++
		}
		if f.FileSize >= 0 {
			end := uint64(f.DataOffset) + t.Offset
			if end < t.Offset || end+t.Bytes < end || end+t.Bytes > uint64(f.FileSize) {
				if pastEnd == 0 {
					firstPastEnd = t.Name
				}
				pastEnd++
			}
		}
	}
	if unknown > 0 {
		f.Warnings = append(f.Warnings, fmt.Sprintf("%d tensors have an unknown type or invalid shape; their sizes are excluded (see TensorInfo.Err)", unknown))
	}
	if misaligned > 0 {
		f.Warnings = append(f.Warnings, fmt.Sprintf("%d tensor offsets are not multiples of the %d-byte alignment", misaligned, f.Alignment))
	}
	if pastEnd > 0 {
		f.Warnings = append(f.Warnings, fmt.Sprintf("%d tensors (first: %s) extend past the end of the %d-byte file: truncated or partial download?", pastEnd, firstPastEnd, f.FileSize))
	}
}

// value reads a value of type t. keep=false skips array contents.
func (p *parser) value(t ValueType, keep bool) (Value, error) {
	switch t {
	case ValueString:
		s, err := p.str(MaxStringLen, "string")
		return Value{Type: t, v: s}, err
	case ValueArray:
		a, err := p.array(1, keep)
		return Value{Type: t, v: a}, err
	}
	if !t.valid() {
		return Value{}, fmt.Errorf("%w: unknown value type %d", ErrMalformed, uint32(t))
	}
	b, err := p.bytes(t.size())
	if err != nil {
		return Value{}, err
	}
	return decodeScalar(t, b), nil
}

func decodeScalar(t ValueType, b []byte) Value {
	le := binary.LittleEndian
	var v any
	switch t {
	case ValueUint8:
		v = b[0]
	case ValueInt8:
		v = int8(b[0])
	case ValueUint16:
		v = le.Uint16(b)
	case ValueInt16:
		v = int16(le.Uint16(b))
	case ValueUint32:
		v = le.Uint32(b)
	case ValueInt32:
		v = int32(le.Uint32(b))
	case ValueFloat32:
		v = math.Float32frombits(le.Uint32(b))
	case ValueBool:
		v = b[0] != 0
	case ValueUint64:
		v = le.Uint64(b)
	case ValueInt64:
		v = int64(le.Uint64(b))
	case ValueFloat64:
		v = math.Float64frombits(le.Uint64(b))
	}
	return Value{Type: t, v: v}
}

func (p *parser) array(depth int, keep bool) (*Array, error) {
	if depth > MaxArrayDepth {
		return nil, fmt.Errorf("%w: arrays nested deeper than %d", ErrLimit, MaxArrayDepth)
	}
	et32, err := p.u32()
	if err != nil {
		return nil, err
	}
	et := ValueType(et32)
	if !et.valid() {
		return nil, fmt.Errorf("%w: unknown array element type %d", ErrMalformed, et32)
	}
	n, err := p.u64()
	if err != nil {
		return nil, err
	}
	if n > MaxArrayLen {
		return nil, fmt.Errorf("%w: array length %d exceeds %d", ErrLimit, n, MaxArrayLen)
	}
	a := &Array{ElemType: et, Len: n}
	keep = keep && n <= MaxMaterializedArrayLen && p.materialized+int(n) <= maxMaterializedTotal
	a.Skipped = !keep
	if keep {
		p.materialized += int(n)
	}

	if sz := et.size(); sz > 0 {
		if !keep {
			return a, p.skip(n * uint64(sz))
		}
		b, err := p.bytes(int(n) * sz)
		if err != nil {
			return nil, err
		}
		a.Values = make([]Value, n)
		for i := range a.Values {
			a.Values[i] = decodeScalar(et, b[i*sz:])
		}
		return a, nil
	}

	if keep {
		a.Values = make([]Value, 0, n)
	}
	for i := uint64(0); i < n; i++ {
		if et == ValueString {
			l, err := p.strLen(MaxStringLen, "string")
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			if !keep {
				if err := p.skip(uint64(l)); err != nil {
					return nil, fmt.Errorf("element %d: %w", i, err)
				}
				continue
			}
			b, err := p.bytes(l)
			if err != nil {
				return nil, fmt.Errorf("element %d: %w", i, err)
			}
			a.Values = append(a.Values, Value{Type: ValueString, v: string(b)})
			continue
		}
		sub, err := p.array(depth+1, keep)
		if err != nil {
			return nil, fmt.Errorf("element %d: %w", i, err)
		}
		if keep {
			a.Values = append(a.Values, Value{Type: ValueArray, v: sub})
		}
	}
	return a, nil
}
