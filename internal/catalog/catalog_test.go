package catalog

import "testing"

func TestFileTag(t *testing.T) {
	for in, want := range map[string]string{
		"Qwen3.6-35B-A3B-UD-Q4_K_M.gguf":                          "UD-Q4_K_M",
		"gemma-4-26B-A4B-it-MXFP4_MOE.gguf":                       "MXFP4_MOE",
		"gpt-oss-20b-MXFP4.gguf":                                  "MXFP4",
		"Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q4_K.gguf": "Q4_K",
		"Qwen3.8-27B-Q8_0.gguf":                                   "Q8_0",
		"model-IQ4_XS.gguf":                                       "IQ4_XS",
		"weird-name.gguf":                                         "",
	} {
		if got := FileTag(in); got != want {
			t.Errorf("FileTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratedCatalogLoads(t *testing.T) {
	m, ok := Lookup("qwen3.6:35b-a3b")
	if !ok || !m.Generated || m.KVPerTokenF16 <= 0 {
		t.Fatalf("generated entry missing or incomplete: %+v", m)
	}
	q, _ := m.Quant("")
	if q.HFTag() != "UD-Q4_K_M" {
		t.Fatalf("unsloth UD file tag = %q, want UD-Q4_K_M", q.HFTag())
	}
}
