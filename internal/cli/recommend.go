package cli

import (
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
	"github.com/aeroknight786/trialm/internal/ui"
)

type installed = core.Installed

var detectInstalled = core.DetectInstalled
var gb = ui.GB
var gbMem = ui.GBMem
var dur = ui.Dur
var cacheLabel = ui.CacheLabel
var bandNumber = ui.BandNumber

func printDetect(s *hw.System, t predict.Target, r []hw.InstalledRuntime) {
	ui.PrintDetect(out(), s, t, r)
}
func printModels(ms []catalog.Model)                               { ui.PrintModels(out(), ms) }
func printRuntimes(ps []runtime.Profile, is []hw.InstalledRuntime) { ui.PrintRuntimes(out(), ps, is) }
func printPrediction(m catalog.Model, t predict.Target, sc predict.Scenario, rs []predict.Result, in installed) {
	ui.PrintPrediction(out(), m, t, sc, rs, in)
}
func printRecommendation(rs []predict.Result, in installed, h harness.Harness) {
	ui.PrintRecommendation(out(), rs, in, h)
}
func printServeBanner(m catalog.Model, t predict.Target, p runtime.Profile, why string, r predict.Result, up string, port, requested int, verbose, warnings bool) {
	ui.PrintServeBanner(out(), m, t, p, why, r, up, port, requested, verbose, warnings)
}
