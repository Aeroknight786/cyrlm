package catalog

import (
	"encoding/json"
	"os"
	"testing"
)

func TestFileTag(t *testing.T) {
	for in, want := range map[string]string{
		"Qwen3.6-35B-A3B-UD-Q4_K_M.gguf":                          "UD-Q4_K_M",
		"gemma-4-26B-A4B-it-MXFP4_MOE.gguf":                       "MXFP4_MOE",
		"gpt-oss-20b-MXFP4.gguf":                                  "MXFP4",
		"Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q4_K.gguf": "Q4_K",
		"Qwen3.8-27B-Q8_0.gguf":                                   "Q8_0",
		"model-IQ4_XS.gguf":                                       "IQ4_XS",
		"folder-Q8_0/model-bf16-00001-of-00003.gguf":              "BF16",
		"model-TQ1_0.gguf":                                        "TQ1_0",
		"mmproj-model-Q8_0.gguf":                                  "",
		"mtp-model-Q8_0.gguf":                                     "",
		"dflash-model-Q8_0.gguf":                                  "",
		"eagle-model-Q8_0.gguf":                                   "",
		"imatrix-model-Q8_0.gguf":                                 "",
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

func TestOpenRouterIDsInSnapshot(t *testing.T) {
	data, err := os.ReadFile("testdata/openrouter_ids.json")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		t.Fatal(err)
	}
	valid := make(map[string]bool, len(ids))
	for _, id := range ids {
		valid[id] = true
	}
	for _, m := range All() {
		if m.OpenRouterID != "" && !valid[m.OpenRouterID] {
			t.Errorf("%s: unknown OpenRouter ID %q", m.ID, m.OpenRouterID)
		}
	}
}
