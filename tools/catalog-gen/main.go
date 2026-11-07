// Command catalog-gen regenerates internal/catalog/models_gguf.json by
// reading the headers of official GGUF files on Hugging Face (HTTP range
// requests; weights are not downloaded). Every geometry number and file
// size in the generated catalog comes from the files themselves.
//
//	go run ./tools/catalog-gen > internal/catalog/models_gguf.json
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/gguf"
)

type file struct{ quant, name string }

type source struct {
	id, name, tag, openrouter, repo string
	files                           []file // first = default quant
	notes                           []string
}

// trialStandIns describe sources whose OpenRouter id is a stand-in (no
// provider hosts the exact model); checked against /api/v1/models on
// 2026-11-07.
var trialStandIns = map[string]string{
	"qwen3.5-4b":           "Qwen3.5 9B (same family, larger): no provider hosts the 4B, so trial answers are a little better than the local model",
	"gemma4-12b":           "Gemma 4 26B-A4B (same family, larger): no provider hosts the 12B, so trial answers are a little better than the local model",
	"devstral-small-2-24b": "Devstral 2 (same family, larger): no provider hosts Devstral Small 2, so trial answers are better than the local model",
	"granite-4.2-30b":      "Granite 4.2 8B (same family, smaller): no provider hosts the 30B, so trial answers are weaker than the local model",
}

// Sources: popular local-size models from major labs (Hugging Face
// download rankings, November 2026). OpenRouter ids are best guesses.
var sources = []source{
	{"qwen3.8-27b", "Qwen3.8 27B", "qwen3.8:27b", "qwen/qwen3.8-27b", "ggml-org/Qwen3.8-27B-GGUF",
		[]file{{"Q4_K_M", "Qwen3.8-27B-Q4_K_M.gguf"}, {"Q8_0", "Qwen3.8-27B-Q8_0.gguf"}}, nil},
	{"qwen3.6-35b-a3b", "Qwen3.6 35B-A3B", "qwen3.6:35b-a3b", "qwen/qwen3.6-35b-a3b", "unsloth/Qwen3.6-35B-A3B-GGUF",
		[]file{{"Q4_K_M", "Qwen3.6-35B-A3B-UD-Q4_K_M.gguf"}, {"MXFP4", "Qwen3.6-35B-A3B-MXFP4_MOE.gguf"}, {"Q8_0", "Qwen3.6-35B-A3B-Q8_0.gguf"},
			{"IQ4_XS", "Qwen3.6-35B-A3B-UD-IQ4_XS.gguf"}, {"Q3_K_XL", "Qwen3.6-35B-A3B-UD-Q3_K_XL.gguf"}, {"IQ3_XXS", "Qwen3.6-35B-A3B-UD-IQ3_XXS.gguf"}}, nil},
	{"qwen3.5-9b", "Qwen3.5 9B", "qwen3.5:9b", "qwen/qwen3.5-9b", "unsloth/Qwen3.5-9B-GGUF",
		[]file{{"Q4_K_M", "Qwen3.5-9B-Q4_K_M.gguf"}, {"Q8_0", "Qwen3.5-9B-Q8_0.gguf"}}, nil},
	{"qwen3.5-4b", "Qwen3.5 4B", "qwen3.5:4b", "qwen/qwen3.5-9b", "unsloth/Qwen3.5-4B-GGUF",
		[]file{{"Q4_K_M", "Qwen3.5-4B-Q4_K_M.gguf"}, {"Q8_0", "Qwen3.5-4B-Q8_0.gguf"}}, nil},
	{"gemma4-12b", "Gemma 4 12B", "gemma4:12b", "google/gemma-4-26b-a4b-it", "unsloth/gemma-4-12b-it-GGUF",
		[]file{{"Q4_K_M", "gemma-4-12b-it-Q4_K_M.gguf"}, {"Q8_0", "gemma-4-12b-it-Q8_0.gguf"}}, nil},
	{"gemma4-26b-a4b", "Gemma 4 26B-A4B", "gemma4:26b-a4b", "google/gemma-4-26b-a4b-it", "unsloth/gemma-4-26B-A4B-it-GGUF",
		[]file{{"Q4_K_M", "gemma-4-26B-A4B-it-UD-Q4_K_M.gguf"}, {"MXFP4", "gemma-4-26B-A4B-it-MXFP4_MOE.gguf"}, {"Q8_0", "gemma-4-26B-A4B-it-Q8_0.gguf"},
			{"IQ4_XS", "gemma-4-26B-A4B-it-UD-IQ4_XS.gguf"}, {"Q3_K_XL", "gemma-4-26B-A4B-it-UD-Q3_K_XL.gguf"}, {"IQ3_XXS", "gemma-4-26B-A4B-it-UD-IQ3_XXS.gguf"}}, nil},
	{"devstral-small-2-24b", "Devstral Small 2 24B", "devstral-small-2:24b", "mistralai/devstral-2512", "unsloth/Devstral-Small-2-24B-Instruct-2512-GGUF",
		[]file{{"Q4_K_M", "Devstral-Small-2-24B-Instruct-2512-Q4_K_M.gguf"}, {"Q8_0", "Devstral-Small-2-24B-Instruct-2512-Q8_0.gguf"},
			{"IQ4_XS", "Devstral-Small-2-24B-Instruct-2512-IQ4_XS.gguf"}, {"Q3_K_XL", "Devstral-Small-2-24B-Instruct-2512-UD-Q3_K_XL.gguf"}}, nil},
	{"glm-4.7-flash", "GLM-4.7-Flash", "glm-4.7-flash", "z-ai/glm-4.7-flash", "unsloth/GLM-4.7-Flash-GGUF",
		[]file{{"Q4_K_M", "GLM-4.7-Flash-Q4_K_M.gguf"}, {"MXFP4", "GLM-4.7-Flash-MXFP4_MOE.gguf"}, {"Q8_0", "GLM-4.7-Flash-Q8_0.gguf"},
			{"IQ4_XS", "GLM-4.7-Flash-IQ4_XS.gguf"}, {"Q3_K_XL", "GLM-4.7-Flash-UD-Q3_K_XL.gguf"}, {"IQ3_XXS", "GLM-4.7-Flash-UD-IQ3_XXS.gguf"}}, nil},
	{"nemotron-3.5-lightning-30b-a3b", "Nemotron 3.5 Lightning 30B-A3B", "nemotron-3.5-lightning:30b", "nvidia/nemotron-3.5-lightning", "ggml-org/NVIDIA-Nemotron-3.5-Lightning-30B-A3B-GGUF",
		[]file{{"Q8_0", "NVIDIA-Nemotron-3.5-Lightning-30B-A3B-Q8_0.gguf"}}, nil},
	{"granite-4.2-30b", "Granite 4.2 30B", "granite4.2:30b", "ibm-granite/granite-4.2-8b", "ibm-granite/granite-4.2-30b-GGUF",
		[]file{{"Q4_K_M", "granite-4.2-30b-Q4_K_M.gguf"}, {"Q8_0", "granite-4.2-30b-Q8_0.gguf"}}, nil},
	{"granite-4.2-8b", "Granite 4.2 8B", "granite4.2:8b", "ibm-granite/granite-4.2-8b", "ibm-granite/granite-4.2-8b-GGUF",
		[]file{{"Q4_K_M", "granite-4.2-8b-Q4_K_M.gguf"}, {"Q8_0", "granite-4.2-8b-Q8_0.gguf"}}, nil},
}

func main() {
	var out []catalog.Model
	for _, src := range sources {
		m, err := build(src)
		if err != nil {
			fmt.Fprintf(os.Stderr, "skip %s: %v\n", src.id, err)
			continue
		}
		fmt.Fprintf(os.Stderr, "ok   %-32s %6.2fB total, %5.2fB active, %d full / %d sliding / %d recurrent layers, ctx %d, tools=%v\n",
			m.ID, m.TotalParamsB, m.ReadParamsB, m.FullAttnLayers, m.SWALayers, m.RecurrentLayers, m.NativeCtx, m.Tools)
		out = append(out, m)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func open(repo, name string) (*gguf.File, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return gguf.OpenURL(ctx, fmt.Sprintf("https://huggingface.co/%s/resolve/main/%s", repo, name), nil)
}

func build(src source) (catalog.Model, error) {
	m := catalog.Model{
		ID: src.id, Name: src.name, OllamaTag: src.tag, OpenRouterID: src.openrouter, TrialStandIn: trialStandIns[src.id], HFGGUFRepo: src.repo,
		CloudPrecision: "provider-dependent", Generated: true, UpstreamUnverified: true,
		Notes: append([]string{"geometry and file sizes read from the GGUF headers in " + src.repo}, src.notes...),
	}
	for i, fl := range src.files {
		f, err := open(src.repo, fl.name)
		if err != nil {
			return m, fmt.Errorf("%s: %w", fl.name, err)
		}
		s := f.Summary()
		if i == 0 {
			if s.ParameterCount == 0 || s.BlockCount == 0 {
				return m, fmt.Errorf("%s: no architecture metadata", fl.name)
			}
			m.Family = s.Architecture
			m.TotalParamsB = float64(s.ParameterCount) / 1e9
			m.ReadParamsB = float64(s.ActiveParams) / 1e9
			m.MatmulParamsB = m.ReadParamsB
			m.MoE = s.ExpertCount > 0
			m.ExpertsTotal, m.ExpertsActive = int(s.ExpertCount), int(s.ExpertUsedCount)
			m.Layers = int(s.BlockCount)
			m.FullAttnLayers, m.SWALayers, m.RecurrentLayers = s.FullAttentionLayers, s.SlidingLayers, s.RecurrentLayers
			m.SWAWindow = int(s.SlidingWindow)
			m.KVHeads, m.HeadDim, m.QHeads = int(s.HeadCountKV), int(s.KeyLength), int(s.HeadCount)
			m.KVPerTokenF16 = float64(s.KVBytesPerTokenF16)
			m.SWAKVPerTokenF16 = float64(s.SlidingKVBytesPerTokenF16)
			m.RecurrentStateBytes = float64(s.RecurrentStateBytes)
			m.NativeCtx = int(s.ContextLength)
			tmpl, _ := f.String("tokenizer.chat_template")
			lt := strings.ToLower(tmpl)
			m.Tools = strings.Contains(lt, "tools") || strings.Contains(lt, "tool_call")
			m.Thinking = strings.Contains(lt, "think") || strings.Contains(lt, "reasoning")
			if s.RecurrentLayers > 0 {
				m.Notes = append(m.Notes, fmt.Sprintf("hybrid model: %d of %d layers are linear-attention/recurrent with a fixed-size state, so the KV cache grows only on %d layers", s.RecurrentLayers, s.BlockCount, s.FullAttentionLayers))
			}
		}
		size := s.FileSize
		if size <= 0 {
			size = int64(s.TensorBytes)
		}
		q := catalog.Quant{Name: fl.quant, Format: "gguf", FileBytes: size, ReadBytesPerToken: int64(s.ActiveBytes)}
		if tag := catalog.FileTag(fl.name); tag != "" && tag != fl.quant {
			q.FileTag = tag
		}
		m.Quants = append(m.Quants, q)
	}
	return m, nil
}
