package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/gguf"
)

func isGGUFRef(s string) bool {
	l := strings.ToLower(s)
	return strings.HasSuffix(l, ".gguf") || strings.Contains(l, ".gguf?") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func openGGUF(ref string) (*gguf.File, error) {
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

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: cyrlm inspect <file.gguf | https://…/model.gguf>")
	}
	f, err := openGGUF(pos[0])
	if err != nil {
		return err
	}
	s := f.Summary()
	if *jsonOut {
		return printJSON(s)
	}
	fmt.Printf("%s (%s) — %s\n", nonEmpty(s.Name, path.Base(pos[0])), s.Architecture, s.FileTypeLabel)
	fmt.Printf("  file           %s (%d tensors)\n", gb(float64(s.FileSize)), s.TensorCount)
	fmt.Printf("  parameters     %.2fB total", float64(s.ParameterCount)/1e9)
	if s.ExpertCount > 0 {
		fmt.Printf(", %d experts (%d active) → %.2fB read per token", s.ExpertCount, s.ExpertUsedCount, float64(s.ActiveParams)/1e9)
	}
	fmt.Println()
	fmt.Printf("  per token      %s of weights read while generating\n", gb(float64(s.ActiveBytes)))
	fmt.Printf("  layers         %d (%d full attention, %d sliding-window", s.BlockCount, s.FullAttentionLayers, s.SlidingLayers)
	if s.RecurrentLayers > 0 {
		fmt.Printf(", %d recurrent", s.RecurrentLayers)
	}
	fmt.Println(")")
	fmt.Printf("  attention      %d heads, %d KV heads, head dim %d", s.HeadCount, s.HeadCountKV, s.KeyLength)
	if s.SlidingWindow > 0 {
		fmt.Printf(", window %d", s.SlidingWindow)
	}
	fmt.Println()
	fmt.Printf("  KV cache       %.0f KiB/token (f16), %.0f KiB/token (q8_0)\n", float64(s.KVBytesPerTokenF16)/1024, float64(s.KVBytesPerTokenQ8_0)/1024)
	for _, c := range []int{8192, 32768, 131072} {
		if uint64(c) <= s.ContextLength || s.ContextLength == 0 {
			fmt.Printf("                 %3dk context → %s (f16)\n", c/1024, gb(float64(s.KVBytesPerTokenF16)*float64(c)+float64(s.SlidingKVBytesF16)))
		}
	}
	fmt.Printf("  context        %d tokens trained\n", s.ContextLength)
	fmt.Printf("  chat template  %v\n", s.HasChatTemplate)
	for _, n := range s.Notes {
		fmt.Printf("  note: %s\n", n)
	}
	fmt.Printf("\nPredict it: cyrlm predict %s\n", pos[0])
	return nil
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// modelFromGGUF converts a GGUF summary into a one-quant catalog model.
func modelFromGGUF(ref string) (catalog.Model, error) {
	f, err := openGGUF(ref)
	if err != nil {
		return catalog.Model{}, fmt.Errorf("reading GGUF header: %w", err)
	}
	s := f.Summary()
	if s.ParameterCount == 0 || s.BlockCount == 0 {
		return catalog.Model{}, errors.New("GGUF has no usable architecture metadata")
	}
	base := path.Base(ref)
	if u, err := url.Parse(ref); err == nil && u.Path != "" {
		base = path.Base(u.Path)
	}
	name := strings.TrimSuffix(base, path.Ext(base))
	size := s.FileSize
	if size <= 0 {
		size = int64(s.TensorBytes)
	}
	full := s.FullAttentionLayers
	if full == 0 && s.SlidingLayers == 0 && s.RecurrentLayers == 0 {
		full = int(s.BlockCount)
	}
	m := catalog.Model{
		ID: name, Name: nonEmpty(s.Name, name), OllamaTag: strings.ToLower(name), Family: s.Architecture,
		TotalParamsB:  float64(s.ParameterCount) / 1e9,
		ReadParamsB:   float64(s.ActiveParams) / 1e9,
		MatmulParamsB: float64(s.ActiveParams) / 1e9,
		MoE:           s.ExpertCount > 0, ExpertsTotal: int(s.ExpertCount), ExpertsActive: int(s.ExpertUsedCount),
		Layers: int(s.BlockCount), FullAttnLayers: full, SWALayers: s.SlidingLayers, SWAWindow: int(s.SlidingWindow),
		KVHeads: int(s.HeadCountKV), HeadDim: int(s.KeyLength), QHeads: int(s.HeadCount),
		NativeCtx: int(s.ContextLength), Tools: true, Thinking: false,
		Quants:         []catalog.Quant{{Name: nonEmpty(s.FileTypeLabel, s.DominantType), Format: "gguf", FileBytes: size, ReadBytesPerToken: int64(s.ActiveBytes)}},
		CloudPrecision: "provider-dependent",
		Notes:          []string{"from GGUF header: tool-calling support assumed"},
	}
	if m.ReadParamsB <= 0 {
		m.ReadParamsB, m.MatmulParamsB = m.TotalParamsB, m.TotalParamsB
	}
	return m, nil
}
