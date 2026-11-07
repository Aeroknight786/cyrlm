package cli

import (
	"errors"
	"flag"
	"fmt"
	"path"

	"github.com/aeroknight786/trialm/internal/core"
)

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		return errors.New("usage: trialm inspect <file.gguf | https://…/model.gguf>")
	}
	f, err := core.OpenGGUF(pos[0])
	if err != nil {
		return err
	}
	s := f.Summary()
	if *jsonOut {
		return printJSON(s)
	}
	fmt.Fprintf(out(), "%s (%s) — %s\n", nonEmpty(s.Name, path.Base(pos[0])), s.Architecture, s.FileTypeLabel)
	fmt.Fprintf(out(), "  file           %s (%d tensors)\n", gb(float64(s.FileSize)), s.TensorCount)
	fmt.Fprintf(out(), "  parameters     %.2fB total", float64(s.ParameterCount)/1e9)
	if s.ExpertCount > 0 {
		fmt.Fprintf(out(), ", %d experts (%d active) → %.2fB read per token", s.ExpertCount, s.ExpertUsedCount, float64(s.ActiveParams)/1e9)
	}
	fmt.Fprintln(out())
	fmt.Fprintf(out(), "  per token      %s of weights read while generating\n", gb(float64(s.ActiveBytes)))
	fmt.Fprintf(out(), "  layers         %d (%d full attention, %d sliding-window", s.BlockCount, s.FullAttentionLayers, s.SlidingLayers)
	if s.RecurrentLayers > 0 {
		fmt.Fprintf(out(), ", %d recurrent", s.RecurrentLayers)
	}
	fmt.Fprintln(out(), ")")
	fmt.Fprintf(out(), "  attention      %d heads, %d KV heads, head dim %d", s.HeadCount, s.HeadCountKV, s.KeyLength)
	if s.SlidingWindow > 0 {
		fmt.Fprintf(out(), ", window %d", s.SlidingWindow)
	}
	fmt.Fprintln(out())
	fmt.Fprintf(out(), "  KV cache       %.0f KiB/token (f16), %.0f KiB/token (q8_0)\n", float64(s.KVBytesPerTokenF16)/1024, float64(s.KVBytesPerTokenQ8_0)/1024)
	for _, c := range []int{8192, 32768, 131072} {
		if uint64(c) <= s.ContextLength || s.ContextLength == 0 {
			fmt.Fprintf(out(), "                 %3dk context → %s (f16)\n", c/1024, gb(float64(s.KVBytesPerTokenF16)*float64(c)+float64(s.SlidingKVBytesF16)))
		}
	}
	fmt.Fprintf(out(), "  context        %d tokens trained\n", s.ContextLength)
	fmt.Fprintf(out(), "  chat template  %v\n", s.HasChatTemplate)
	if si := core.FindStandIn(pos[0], f, s); si.Upstream != "" {
		fmt.Fprintf(out(), "  trial          no cloud provider hosts this exact file: trials use its base %s as a stand-in (cloud id %s)\n", si.Describe(), si.Upstream)
		fmt.Fprintln(out(), "                 speed is simulated from this file exactly; behaviour is the base model's")
	}
	for _, n := range s.Notes {
		fmt.Fprintf(out(), "  note: %s\n", n)
	}
	fmt.Fprintf(out(), "\nPredict it: trialm predict %s\n", pos[0])
	return nil
}

func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
