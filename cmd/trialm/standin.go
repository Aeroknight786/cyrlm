package main

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/gguf"
	"github.com/aeroknight786/trialm/internal/hub"
)

// standIn is the cloud model used for a trial of a model no provider hosts
// (fine-tunes, abliterated or community models): its base model. Speed is
// still simulated from the exact file; behaviour is the base model's.
type standIn struct {
	Base     string // e.g. "Qwen/Qwen3.6-35B-A3B"
	Upstream string // OpenRouter id
	Source   string // where the base came from
	Known    bool   // Upstream comes from the catalog (not a guessed id)
}

func (s standIn) describe() string {
	if s.Upstream == "" {
		return ""
	}
	return fmt.Sprintf("%s (found in %s)", s.Base, s.Source)
}

// findStandIn looks for the base model: GGUF general.base_model.* keys,
// then the Hugging Face model card chain, then the closest catalog model of
// the same architecture and size.
func findStandIn(ref string, f *gguf.File, s *gguf.Summary) standIn {
	var cands []string
	source := ""
	if n, ok := f.Uint("general.base_model.count"); ok {
		for i := uint64(0); i < n && i < 4; i++ {
			k := fmt.Sprintf("general.base_model.%d.", i)
			if u, ok := f.String(k + "repo_url"); ok {
				if repo, ok := hub.RepoFromURL(u); ok {
					cands = append(cands, repo)
					continue
				}
			}
			org, _ := f.String(k + "organization")
			name, _ := f.String(k + "name")
			if org != "" && name != "" {
				cands = append(cands, strings.ReplaceAll(org, " ", "-")+"/"+strings.ReplaceAll(name, " ", "-"))
			}
		}
		if len(cands) > 0 {
			source = "the GGUF file's metadata"
		}
	}
	if len(cands) == 0 {
		if repo, ok := hub.RepoFromURL(ref); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			cands = hub.BaseChain(ctx, repo, 4)
			cancel()
			if len(cands) > 0 {
				source = "the Hugging Face model card"
			}
		}
	}
	// Nearest ancestor that the catalog knows (verified cloud id).
	for _, c := range cands {
		if m, ok := catalog.Lookup(c); ok && m.OpenRouterID != "" {
			return standIn{Base: c, Upstream: m.OpenRouterID, Source: source, Known: !m.UpstreamUnverified}
		}
	}
	if len(cands) > 0 {
		root := cands[len(cands)-1]
		if strings.HasSuffix(strings.ToLower(root), "-base") && len(cands) > 1 {
			root = cands[len(cands)-2] // prefer the instruct model over a pretrained base
		}
		return standIn{Base: root, Upstream: strings.ToLower(root), Source: source}
	}
	// Same architecture and roughly the same size.
	params := float64(s.ParameterCount) / 1e9
	var best *catalog.Model
	bestDiff := math.Inf(1)
	for _, m := range catalog.All() {
		if m.Family != s.Architecture || m.OpenRouterID == "" {
			continue
		}
		d := math.Abs(m.TotalParamsB-params) / params
		if d < 0.15 && d < bestDiff {
			mm := m
			best, bestDiff = &mm, d
		}
	}
	if best != nil {
		return standIn{Base: best.Name, Upstream: best.OpenRouterID, Source: "the catalog: same architecture (" + s.Architecture + ") and size — a guess", Known: !best.UpstreamUnverified}
	}
	return standIn{}
}

// ggufModelName derives a display name from a file or URL.
func ggufModelName(ref string) string {
	base := ref
	if u, err := url.Parse(ref); err == nil && u.Path != "" {
		base = u.Path
	}
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return strings.TrimSuffix(base, ".gguf")
}
