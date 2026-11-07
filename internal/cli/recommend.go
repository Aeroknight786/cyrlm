package cli

import (
	"fmt"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/predict"
)

type installed = core.Installed

var detectInstalled = core.DetectInstalled
var recommend = core.Recommend
var choose = core.Choose
var profileIDsFor = core.ProfileIDsFor
var installedNames = core.InstalledNames
var runtimeTitles = core.RuntimeTitles
var installHints = core.InstallHints

func modeLabel(r *predict.Result) string {
	if r.Settings.Tuned {
		return "tuned settings"
	}
	return "default settings"
}

func printRecommendation(rs []predict.Result, in installed, h harness.Harness) {
	best, mine := recommend(rs, in, h)
	if best == nil {
		fmt.Printf("Recommendation: no runtime gives a working %s setup for this model here — try a smaller quant (--quant), a smaller model, or --ctx.\n", h.Name)
		return
	}
	fmt.Printf("Best setup: %s with %s — task ≈ %s, first reply %s, then ~%s per turn.\n",
		best.Runtime.Name, modeLabel(best), dur(best.Scenario.Session), dur(best.Scenario.ColdFirstReply), dur(best.Scenario.FollowUpTurn))
	switch {
	case !in.Detected:
		fmt.Printf("  (what-if machine: installed runtimes not checked) Install: %s\n", installHints[best.Runtime.ID])
	case mine == nil && len(in.All) == 0:
		fmt.Printf("  No local runtime is installed. Install: %s\n", installHints[best.Runtime.ID])
	case mine == nil:
		fmt.Printf("  None of your installed runtimes (%s) gives a working setup for this model. Install: %s\n", installedNames(in), installHints[best.Runtime.ID])
	case mine.Runtime.ID == best.Runtime.ID && mine.Settings.Tuned == best.Settings.Tuned:
		fmt.Printf("  You already have it: %s.\n", in.Describe(mine.Runtime.ID))
	default:
		slower := 100 * (mine.Scenario.Session.Seconds()/best.Scenario.Session.Seconds() - 1)
		fmt.Printf("  With what you have — %s — use %s (%s): task ≈ %s", in.Describe(mine.Runtime.ID), mine.Runtime.Name, modeLabel(mine), dur(mine.Scenario.Session))
		if slower >= 5 {
			fmt.Printf(", %.0f%% slower than the best; to get the best install: %s", slower, installHints[best.Runtime.ID])
		} else {
			fmt.Print(", about as fast as the best")
		}
		fmt.Println(".")
	}
	if pick := choose(best, mine); pick.Settings.Tuned && len(pick.TuneSteps) > 0 {
		fmt.Printf("  Settings for %s:\n", pick.Runtime.Name)
		for _, s := range pick.TuneSteps {
			fmt.Printf("    - %s\n", s)
		}
	}
	for _, ir := range in.All {
		if _, note := profileIDsFor(ir); note != "" {
			fmt.Printf("  · %s: %s\n", runtimeTitles[ir.ID], note)
		}
	}
}
