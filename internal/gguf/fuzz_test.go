package gguf

import (
	"bytes"
	"testing"
)

// FuzzParse checks that arbitrary input never panics or hangs and that a
// successful parse yields a usable Summary. Run with
// go test -fuzz=FuzzParse ./internal/gguf/
func FuzzParse(f *testing.F) {
	for _, b := range []builder{llamaBuilder(), gptOSSBuilder()} {
		b.noData = true
		f.Add(b.bytes(f))
	}
	f.Add([]byte("GGUF\x03\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00"))
	f.Fuzz(func(t *testing.T, data []byte) {
		file, err := Parse(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return
		}
		s := file.Summary()
		_ = s.KVCacheBytes(4096, TypeQ8_0)
		for _, kv := range file.KVs {
			_ = kv.Value.String()
		}
	})
}
