package ui

import (
	"fmt"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/predict"
	"io"
)

func modeLabel(r *predict.Result) string {
	if r.Settings.Tuned {
		return "tuned settings"
	}
	return "default settings"
}

func PrintRecommendation(w io.Writer, rs []predict.Result, in core.Installed, h harness.Harness) {
	best, mine := core.Recommend(rs, in, h)
	if best == nil {
		fmt.Fprintf(w, "Recommendation: no runtime gives a working %s setup for this model here — try a smaller quant (--quant), a smaller model, or --ctx.\n", h.Name)
		return
	}
	fmt.Fprintf(w, "Best setup: %s with %s — task ≈ %s, first reply %s, then ~%s per turn.\n",
		best.Runtime.Name, modeLabel(best), dur(best.Scenario.Session), dur(best.Scenario.ColdFirstReply), dur(best.Scenario.FollowUpTurn))
	switch {
	case !in.Detected:
		fmt.Fprintf(w, "  (what-if machine: installed runtimes not checked) Install: %s\n", core.InstallHints[best.Runtime.ID])
	case mine == nil && len(in.All) == 0:
		fmt.Fprintf(w, "  No local runtime is installed. Install: %s\n", core.InstallHints[best.Runtime.ID])
	case mine == nil:
		fmt.Fprintf(w, "  None of your installed runtimes (%s) gives a working setup for this model. Install: %s\n", core.InstalledNames(in), core.InstallHints[best.Runtime.ID])
	case mine.Runtime.ID == best.Runtime.ID && mine.Settings.Tuned == best.Settings.Tuned:
		fmt.Fprintf(w, "  You already have it: %s.\n", in.Describe(mine.Runtime.ID))
	default:
		slower := 100 * (mine.Scenario.Session.Seconds()/best.Scenario.Session.Seconds() - 1)
		fmt.Fprintf(w, "  With what you have — %s — use %s (%s): task ≈ %s", in.Describe(mine.Runtime.ID), mine.Runtime.Name, modeLabel(mine), dur(mine.Scenario.Session))
		if slower >= 5 {
			fmt.Fprintf(w, ", %.0f%% slower than the best; to get the best install: %s", slower, core.InstallHints[best.Runtime.ID])
		} else {
			fmt.Fprint(w, ", about as fast as the best")
		}
		fmt.Fprintln(w, ".")
	}
	if pick := core.Choose(best, mine); pick.Settings.Tuned && len(pick.TuneSteps) > 0 {
		fmt.Fprintf(w, "  Settings for %s:\n", pick.Runtime.Name)
		for _, s := range pick.TuneSteps {
			fmt.Fprintf(w, "    - %s\n", s)
		}
	}
	for _, ir := range in.All {
		if _, note := core.ProfileIDsFor(ir); note != "" {
			fmt.Fprintf(w, "  · %s: %s\n", core.RuntimeTitles[ir.ID], note)
		}
	}
}
