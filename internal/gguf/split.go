package gguf

import (
	"fmt"
	"strconv"
	"strings"
)

// llama.cpp split models (llama-gguf-split) are sharded into
// "<name>-00001-of-0000N.gguf" parts. The first part carries all metadata
// plus split.no / split.count / split.tensors.count; later parts carry only
// the split keys and their tensors. Summary of a single part has complete
// architecture data but tensor totals for that part only; open every part
// (SplitPartNames) and combine them with MergeSplits for whole-model totals.

// SplitInfo describes a llama.cpp split (sharded) model part.
type SplitInfo struct {
	// No is the 0-based part index (split.no).
	No int `json:"no"`
	// Count is the number of parts (split.count).
	Count int `json:"count"`
	// TensorsCount is the tensor count of the whole model
	// (split.tensors.count).
	TensorsCount int `json:"tensors_count"`
}

// Split returns the split.* keys of a llama.cpp split part; false when the
// file is not split.
func (f *File) Split() (SplitInfo, bool) {
	c, ok := f.Uint("split.count")
	if !ok || c == 0 {
		return SplitInfo{}, false
	}
	no, _ := f.Uint("split.no")
	tc, _ := f.Uint("split.tensors.count")
	return SplitInfo{No: int(min(no, 1<<20)), Count: int(min(c, 1<<20)), TensorsCount: int(min(tc, MaxTensors*100))}, true
}

// SplitPartNames returns the names of every part of a llama.cpp split model
// given any part's path or URL ("…-00001-of-00003.gguf", the naming of
// llama-gguf-split). false when name does not follow that pattern.
func SplitPartNames(name string) ([]string, bool) {
	base, query, _ := strings.Cut(name, "?")
	if query != "" {
		query = "?" + query
	}
	if !strings.HasSuffix(base, ".gguf") {
		return nil, false
	}
	stem := strings.TrimSuffix(base, ".gguf")
	// stem ends with -NNNNN-of-MMMMM
	const tail = len("-00001-of-00003")
	if len(stem) < tail {
		return nil, false
	}
	t := stem[len(stem)-tail:]
	if t[0] != '-' || t[6:10] != "-of-" {
		return nil, false
	}
	no, err1 := strconv.Atoi(t[1:6])
	count, err2 := strconv.Atoi(t[10:])
	if err1 != nil || err2 != nil || no < 1 || count < 1 || no > count {
		return nil, false
	}
	prefix := stem[:len(stem)-tail]
	out := make([]string, count)
	for i := range out {
		out[i] = fmt.Sprintf("%s-%05d-of-%05d.gguf%s", prefix, i+1, count, query)
	}
	return out, true
}

// MergeSplits combines the parts of a split model (in any order) into one
// File: metadata from part 0 (llama-gguf-split writes all metadata there),
// tensors from every part (TensorInfo.Split records the part), FileSize
// and HeaderBytesRead summed. Summary of the result covers the whole model.
func MergeSplits(parts []*File) (*File, error) {
	if len(parts) == 0 {
		return nil, fmt.Errorf("gguf: MergeSplits: no parts")
	}
	ordered := make([]*File, len(parts))
	for _, p := range parts {
		si, ok := p.Split()
		if !ok {
			if len(parts) == 1 {
				return p, nil
			}
			return nil, fmt.Errorf("gguf: MergeSplits: %s has no split.count", p.Source)
		}
		if si.Count != len(parts) {
			return nil, fmt.Errorf("gguf: MergeSplits: %s says split.count=%d, got %d parts", p.Source, si.Count, len(parts))
		}
		if si.No < 0 || si.No >= len(parts) || ordered[si.No] != nil {
			return nil, fmt.Errorf("gguf: MergeSplits: duplicate or invalid split.no %d (%s)", si.No, p.Source)
		}
		ordered[si.No] = p
	}
	first := ordered[0]
	m := &File{
		Source:     first.Source,
		Version:    first.Version,
		Alignment:  first.Alignment,
		KVs:        first.KVs,
		DataOffset: first.DataOffset,
		index:      first.index,
	}
	if m.index == nil {
		m.buildIndex()
	}
	names := map[string]int{}
	for i, p := range ordered {
		if m.FileSize >= 0 && p.FileSize >= 0 {
			m.FileSize += p.FileSize
		} else {
			m.FileSize = -1
		}
		m.HeaderBytesRead += p.HeaderBytesRead
		for _, w := range p.Warnings {
			m.Warnings = append(m.Warnings, fmt.Sprintf("part %d: %s", i+1, w))
		}
		for _, t := range p.Tensors {
			if j, dup := names[t.Name]; dup {
				return nil, fmt.Errorf("gguf: MergeSplits: tensor %q in parts %d and %d", t.Name, j+1, i+1)
			}
			names[t.Name] = i
			t.Split = i
			m.Tensors = append(m.Tensors, t)
		}
	}
	if si, _ := first.Split(); si.TensorsCount > 0 && si.TensorsCount != len(m.Tensors) {
		m.Warnings = append(m.Warnings, fmt.Sprintf("split.tensors.count is %d but the parts hold %d tensors", si.TensorsCount, len(m.Tensors)))
	}
	return m, nil
}
