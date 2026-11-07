package gguf

import (
	"fmt"
	"strconv"
)

// GGMLType is a tensor storage type (enum ggml_type in ggml/include/ggml.h).
type GGMLType uint32

// ggml tensor types. Ids 4, 5 (Q4_2, Q4_3), 31–33 (Q4_0_4_4, Q4_0_4_8,
// Q4_0_8_8) and 36–38 (IQ4_NL_4_4, IQ4_NL_4_8, IQ4_NL_8_8) were removed from
// ggml; the latter six still have known sizes (see TypeTraits.Removed).
const (
	TypeF32     GGMLType = 0
	TypeF16     GGMLType = 1
	TypeQ4_0    GGMLType = 2
	TypeQ4_1    GGMLType = 3
	TypeQ5_0    GGMLType = 6
	TypeQ5_1    GGMLType = 7
	TypeQ8_0    GGMLType = 8
	TypeQ8_1    GGMLType = 9
	TypeQ2_K    GGMLType = 10
	TypeQ3_K    GGMLType = 11
	TypeQ4_K    GGMLType = 12
	TypeQ5_K    GGMLType = 13
	TypeQ6_K    GGMLType = 14
	TypeQ8_K    GGMLType = 15
	TypeIQ2_XXS GGMLType = 16
	TypeIQ2_XS  GGMLType = 17
	TypeIQ3_XXS GGMLType = 18
	TypeIQ1_S   GGMLType = 19
	TypeIQ4_NL  GGMLType = 20
	TypeIQ3_S   GGMLType = 21
	TypeIQ2_S   GGMLType = 22
	TypeIQ4_XS  GGMLType = 23
	TypeI8      GGMLType = 24
	TypeI16     GGMLType = 25
	TypeI32     GGMLType = 26
	TypeI64     GGMLType = 27
	TypeF64     GGMLType = 28
	TypeIQ1_M   GGMLType = 29
	TypeBF16    GGMLType = 30
	TypeTQ1_0   GGMLType = 34
	TypeTQ2_0   GGMLType = 35
	TypeMXFP4   GGMLType = 39
)

// TypeTraits gives the storage layout of a ggml type: BlockSize elements
// are stored in TypeSize bytes.
type TypeTraits struct {
	Name      string
	BlockSize uint64
	TypeSize  uint64
	// Removed marks types dropped from current ggml (repacked Q4_0/IQ4_NL
	// variants): their size is known but current llama.cpp will not load
	// them.
	Removed bool
}

// BitsPerWeight is the storage cost per element.
func (t TypeTraits) BitsPerWeight() float64 {
	return float64(t.TypeSize*8) / float64(t.BlockSize)
}

// Block layouts from ggml/src/ggml-common.h (QK_K = 256); the byte counts
// are the sizeof() of each block_* struct, e.g. block_q4_K = 2×fp16 +
// 12 scale bytes + 128 nibble bytes = 144.
var typeTraits = map[GGMLType]TypeTraits{
	TypeF32:     {"F32", 1, 4, false},
	TypeF16:     {"F16", 1, 2, false},
	TypeQ4_0:    {"Q4_0", 32, 18, false},
	TypeQ4_1:    {"Q4_1", 32, 20, false},
	TypeQ5_0:    {"Q5_0", 32, 22, false},
	TypeQ5_1:    {"Q5_1", 32, 24, false},
	TypeQ8_0:    {"Q8_0", 32, 34, false},
	TypeQ8_1:    {"Q8_1", 32, 36, false},
	TypeQ2_K:    {"Q2_K", 256, 84, false},
	TypeQ3_K:    {"Q3_K", 256, 110, false},
	TypeQ4_K:    {"Q4_K", 256, 144, false},
	TypeQ5_K:    {"Q5_K", 256, 176, false},
	TypeQ6_K:    {"Q6_K", 256, 210, false},
	TypeQ8_K:    {"Q8_K", 256, 292, false},
	TypeIQ2_XXS: {"IQ2_XXS", 256, 66, false},
	TypeIQ2_XS:  {"IQ2_XS", 256, 74, false},
	TypeIQ3_XXS: {"IQ3_XXS", 256, 98, false},
	TypeIQ1_S:   {"IQ1_S", 256, 50, false},
	TypeIQ4_NL:  {"IQ4_NL", 32, 18, false},
	TypeIQ3_S:   {"IQ3_S", 256, 110, false},
	TypeIQ2_S:   {"IQ2_S", 256, 82, false},
	TypeIQ4_XS:  {"IQ4_XS", 256, 136, false},
	TypeI8:      {"I8", 1, 1, false},
	TypeI16:     {"I16", 1, 2, false},
	TypeI32:     {"I32", 1, 4, false},
	TypeI64:     {"I64", 1, 8, false},
	TypeF64:     {"F64", 1, 8, false},
	TypeIQ1_M:   {"IQ1_M", 256, 56, false},
	TypeBF16:    {"BF16", 1, 2, false},
	31:          {"Q4_0_4_4", 32, 18, true},
	32:          {"Q4_0_4_8", 32, 18, true},
	33:          {"Q4_0_8_8", 32, 18, true},
	TypeTQ1_0:   {"TQ1_0", 256, 54, false},
	TypeTQ2_0:   {"TQ2_0", 256, 66, false},
	36:          {"IQ4_NL_4_4", 32, 18, true},
	37:          {"IQ4_NL_4_8", 32, 18, true},
	38:          {"IQ4_NL_8_8", 32, 18, true},
	TypeMXFP4:   {"MXFP4", 32, 17, false},
}

// Traits returns the storage layout of t; false for unknown types.
func (t GGMLType) Traits() (TypeTraits, bool) {
	tr, ok := typeTraits[t]
	return tr, ok
}

// Known reports whether t's storage size is known.
func (t GGMLType) Known() bool {
	_, ok := typeTraits[t]
	return ok
}

func (t GGMLType) String() string {
	if tr, ok := typeTraits[t]; ok {
		return tr.Name
	}
	return "type(" + strconv.FormatUint(uint64(t), 10) + ")"
}

// RowBytes returns the stored size of n elements of type t, which must be
// a whole number of blocks.
func RowBytes(t GGMLType, n uint64) (uint64, error) {
	tr, ok := typeTraits[t]
	if !ok {
		return 0, fmt.Errorf("unknown ggml type %d", uint32(t))
	}
	if n%tr.BlockSize != 0 {
		return 0, fmt.Errorf("%d elements is not a multiple of the %s block size %d", n, tr.Name, tr.BlockSize)
	}
	return n / tr.BlockSize * tr.TypeSize, nil
}

// cacheBytes is the size of n cache elements of type t, rounding partial
// blocks up (cache rows are always whole blocks in practice).
func cacheBytes(t GGMLType, n uint64) uint64 {
	tr, ok := typeTraits[t]
	if !ok {
		return 0
	}
	return (n + tr.BlockSize - 1) / tr.BlockSize * tr.TypeSize
}

// File types (enum llama_ftype in llama.cpp include/llama.h), the value of
// general.file_type. Labels are llama-quantize's names, except 38 which
// llama-quantize calls "MXFP4_MOE" and is labelled "MXFP4" here.
var fileTypeLabels = map[int64]string{
	0:  "F32",
	1:  "F16",
	2:  "Q4_0",
	3:  "Q4_1",
	4:  "Q4_1_SOME_F16",
	7:  "Q8_0",
	8:  "Q5_0",
	9:  "Q5_1",
	10: "Q2_K",
	11: "Q3_K_S",
	12: "Q3_K_M",
	13: "Q3_K_L",
	14: "Q4_K_S",
	15: "Q4_K_M",
	16: "Q5_K_S",
	17: "Q5_K_M",
	18: "Q6_K",
	19: "IQ2_XXS",
	20: "IQ2_XS",
	21: "Q2_K_S",
	22: "IQ3_XS",
	23: "IQ3_XXS",
	24: "IQ1_S",
	25: "IQ4_NL",
	26: "IQ3_S",
	27: "IQ3_M",
	28: "IQ2_S",
	29: "IQ2_M",
	30: "IQ4_XS",
	31: "IQ1_M",
	32: "BF16",
	33: "Q4_0_4_4",
	34: "Q4_0_4_8",
	35: "Q4_0_8_8",
	36: "TQ1_0",
	37: "TQ2_0",
	38: "MXFP4",
}

// llamaFtypeGuessed is OR-ed into general.file_type when llama.cpp guessed it.
const llamaFtypeGuessed = 1024

// FileTypeLabel returns the label of a general.file_type value, e.g. 15 →
// "Q4_K_M". Unknown values return "" and false.
func FileTypeLabel(ftype int64) (string, bool) {
	guessed := ftype >= llamaFtypeGuessed && ftype&llamaFtypeGuessed != 0
	if guessed {
		ftype &^= llamaFtypeGuessed
	}
	l, ok := fileTypeLabels[ftype]
	if ok && guessed {
		l += " (guessed)"
	}
	return l, ok
}
