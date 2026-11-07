package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/gguf"
	"github.com/aeroknight786/trialm/internal/hub"
)

func IsGGUFRef(s string) bool {
	l := strings.ToLower(s)
	return strings.HasSuffix(l, ".gguf") || strings.Contains(l, ".gguf?") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func OpenGGUF(ref string) (*gguf.File, error) {
	if strings.HasPrefix(ref, "http://") || strings.HasPrefix(ref, "https://") {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		return gguf.OpenURL(ctx, hfResolve(ref), nil)
	}
	return gguf.Open(ref)
}

// hfResolve turns a Hugging Face "blob" page URL into a "resolve" file URL.
func hfResolve(u string) string {
	if strings.Contains(u, "huggingface.co/") && strings.Contains(u, "/blob/") {
		return strings.Replace(u, "/blob/", "/resolve/", 1)
	}
	return u
}

func hubRepo(ref string) (string, bool) { return hub.RepoFromURL(ref) }

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// modelFromGGUF converts a GGUF summary into a one-quant catalog model.
func ModelFromGGUF(ref string) (catalog.Model, error) {
	f, err := OpenGGUF(ref)
	if err != nil {
		return catalog.Model{}, fmt.Errorf("reading GGUF header: %w", err)
	}
	s := f.Summary()
	if s.ParameterCount == 0 || s.BlockCount == 0 {
		return catalog.Model{}, errors.New("GGUF has no usable architecture metadata")
	}
	name := ggufModelName(ref)
	size := s.FileSize
	if size <= 0 {
		size = int64(s.TensorBytes)
	}
	full := s.FullAttentionLayers
	if full == 0 && s.SlidingLayers == 0 && s.RecurrentLayers == 0 {
		full = int(s.BlockCount)
	}
	tmpl, _ := f.String("tokenizer.chat_template")
	lt := strings.ToLower(tmpl)
	m := catalog.Model{
		ID: name, Name: nonEmpty(s.Name, name), OllamaTag: strings.ToLower(name), Family: s.Architecture,
		TotalParamsB:  float64(s.ParameterCount) / 1e9,
		ReadParamsB:   float64(s.ActiveParams) / 1e9,
		MatmulParamsB: float64(s.ActiveParams) / 1e9,
		MoE:           s.ExpertCount > 0, ExpertsTotal: int(s.ExpertCount), ExpertsActive: int(s.ExpertUsedCount),
		Layers: int(s.BlockCount), FullAttnLayers: full, SWALayers: s.SlidingLayers, SWAWindow: int(s.SlidingWindow),
		RecurrentLayers: s.RecurrentLayers, KVPerTokenF16: float64(s.KVBytesPerTokenF16), SWAKVPerTokenF16: float64(s.SlidingKVBytesPerTokenF16),
		RecurrentStateBytes: float64(s.RecurrentStateBytes),
		KVHeads:             int(s.HeadCountKV), HeadDim: int(s.KeyLength), QHeads: int(s.HeadCount),
		NativeCtx: int(s.ContextLength), Tools: tmpl == "" || strings.Contains(lt, "tools") || strings.Contains(lt, "tool_call"),
		Thinking:       strings.Contains(lt, "think") || strings.Contains(lt, "reasoning"),
		Quants:         []catalog.Quant{{Name: nonEmpty(s.FileTypeLabel, s.DominantType), Format: "gguf", FileBytes: size, ReadBytesPerToken: int64(s.ActiveBytes), FileTag: catalog.FileTag(name)}},
		CloudPrecision: "provider-dependent", Generated: true, UpstreamUnverified: true,
		Notes: []string{"from the GGUF header"},
	}
	if repo, ok := hubRepo(ref); ok {
		m.HFGGUFRepo = repo
	}
	if m.ReadParamsB <= 0 {
		m.ReadParamsB, m.MatmulParamsB = m.TotalParamsB, m.TotalParamsB
	}
	if si := FindStandIn(ref, f, s); si.Upstream != "" {
		m.OpenRouterID = si.Upstream
		m.UpstreamUnverified = !si.Known
		m.TrialStandIn = si.Describe()
	} else {
		m.Notes = append(m.Notes, "no base model found: pass --upstream-model to choose the cloud model for a trial")
	}
	return m, nil
}
