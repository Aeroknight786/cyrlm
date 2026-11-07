package ui

import (
	"fmt"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/setup"
	"io"
	"strings"
)

func PrintPlan(output io.Writer, p setup.Plan, why string, res predict.Result) {
	mode := "default settings"
	if p.Tuned {
		mode = "tuned settings"
	}
	fmt.Fprintf(output, "\nSetup plan: %s (%s, %s) with %s, %s\n", p.ModelName, p.Quant, gb(float64(p.DownloadBytes)), p.RuntimeName, mode)
	fmt.Fprintf(output, "  runtime chosen: %s\n", why)
	fmt.Fprintf(output, "  context %d tokens · %s · serves at %s as %q\n", p.ContextTokens, cacheLabel(p.KVCacheType), p.Endpoint, p.LocalModelName)
	fmt.Fprintf(output, "  expected: first reply %s, then ~%s per turn, writing at %.0f tok/s\n",
		dur(res.Scenario.ColdFirstReply), dur(res.Scenario.FollowUpTurn), res.DecodeTPSDeep)
	if p.Harness != "" {
		mark := "✓"
		if !p.HarnessCompatible {
			mark = "✗"
		}
		fmt.Fprintf(output, "  %s %s\n", mark, p.HarnessNote)
	}
	fmt.Fprintln(output, "\nNothing has been run. Run each step yourself (or let your coding agent run them, with your approval):")
	for i, s := range p.Steps {
		flags := []string{}
		if s.Optional {
			flags = append(flags, "optional")
		}
		if s.NeedsAdmin {
			flags = append(flags, "needs admin")
		}
		if s.DownloadBytes > 0 {
			flags = append(flags, "downloads "+gb(float64(s.DownloadBytes)))
		}
		if s.Confidence == "low" {
			flags = append(flags, "check the docs link: command not verified for every version")
		}
		extra := ""
		if len(flags) > 0 {
			extra = "  [" + strings.Join(flags, ", ") + "]"
		}
		fmt.Fprintf(output, "\n%d. %s%s\n", i+1, s.Title, extra)
		if s.Why != "" {
			fmt.Fprintf(output, "   %s\n", s.Why)
		}
		for _, cmd := range s.Commands {
			for _, line := range strings.Split(strings.TrimRight(cmd.Run, "\n"), "\n") {
				fmt.Fprintf(output, "     %s\n", line)
			}
		}
		for _, f := range s.Files {
			fmt.Fprintf(output, "   %s %s:\n", map[string]string{"create": "Create", "merge": "Merge into", "append": "Append to"}[f.Merge], f.Path)
			for _, line := range strings.Split(strings.TrimRight(f.Content, "\n"), "\n") {
				fmt.Fprintf(output, "     %s\n", line)
			}
		}
		if s.Restart != "" {
			fmt.Fprintf(output, "   then restart: %s\n", s.Restart)
		}
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(output, "\n⚠ %s", w)
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output)
}
