package ui

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/core"
	runtime "github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/sim"
)

func gb(b float64) string {
	if b >= 100e9 {
		return fmt.Sprintf("%.0f GB", b/1e9)
	}
	return fmt.Sprintf("%.1f GB", b/1e9)
}

// gbMem formats memory capacities in binary GB, the way operating systems
// and vendors label RAM and VRAM ("24 GB" = 24 GiB).
func gbMem(b float64) string {
	return fmt.Sprintf("%.1f GB", b/(1<<30))
}

func dur(d interface{ Seconds() float64 }) string { return sim.FormatDur(d.Seconds()) }

// readingRatio compares generation speed with reading speed (~5 tok/s).
func readingRatio(tps float64) string {
	r := tps / 5
	switch {
	case r >= 2:
		return fmt.Sprintf("~%.0f× reading speed", r)
	case r >= 1:
		return "about reading speed"
	}
	return "slower than reading"
}

func PrintDetect(output io.Writer, sys *hw.System, t predict.Target, rts []hw.InstalledRuntime) {
	fmt.Fprintf(output, "Machine: %s\n", t.Name)
	if sys != nil {
		fmt.Fprintf(output, "  OS        %s/%s %s\n", sys.OS, sys.Arch, sys.OSVersion)
		fmt.Fprintf(output, "  CPU       %s (%d cores, %d threads)\n", sys.CPUName, sys.CPUCores, sys.CPUThreads)
		fmt.Fprintf(output, "  RAM       %s\n", gbMem(float64(sys.RAMBytes)))
		for _, g := range sys.GPUs {
			kind := "dedicated"
			if g.Unified {
				kind = "unified/shared"
			}
			fmt.Fprintf(output, "  GPU       %s — %s %s", g.Name, gbMem(float64(g.VRAMBytes)), kind)
			if len(g.Backends) > 0 {
				fmt.Fprintf(output, " [%s]", strings.Join(g.Backends, ", "))
			}
			fmt.Fprintln(output)
		}
		if len(sys.GPUs) == 0 {
			fmt.Fprintln(output, "  GPU       none detected (CPU-only predictions)")
		}
	}
	fmt.Fprintf(output, "\nPrediction basis: %s — %.0f GB/s memory bandwidth, %.1f TFLOPS (spec%s)\n",
		t.Device.Name, t.Device.BandwidthGBs, t.Device.FP16TFLOPS, map[bool]string{true: "", false: "; partly assumed"}[t.Device.Confident])
	fmt.Fprintf(output, "Memory the model can use: %s\n", gbMem(t.GPUMemBytes))
	for _, n := range t.Notes {
		fmt.Fprintf(output, "  note: %s\n", n)
	}
	if sys != nil {
		for _, n := range sys.Notes {
			fmt.Fprintf(output, "  note: %s\n", n)
		}
	}
	fmt.Fprintln(output, "\nInstalled runtimes:")
	if len(rts) == 0 {
		fmt.Fprintln(output, "  none found (Ollama, LM Studio, llama.cpp, mlx-lm, vLLM, Jan, Lemonade, Docker Model Runner)")
	}
	for _, r := range rts {
		state := ""
		if r.Running {
			state = " · running at " + r.Endpoint
		}
		fmt.Fprintf(output, "  %-20s %s%s\n", core.RuntimeTitles[r.ID], r.Version, state)
		if ids, note := core.ProfileIDsFor(r); note != "" {
			fmt.Fprintf(output, "  %-20s ↳ %s\n", "", note)
		} else if len(ids) > 0 {
			fmt.Fprintf(output, "  %-20s ↳ simulated as: %s\n", "", strings.Join(ids, ", "))
		}
	}
}

func PrintModels(output io.Writer, ms []catalog.Model) {
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MODEL\tPARAMS\tACTIVE\tDEFAULT QUANT\tDOWNLOAD\tCONTEXT\tTOOLS\tCLOUD QUALITY MATCH")
	for _, m := range ms {
		q := m.Quants[0]
		active := "dense"
		if m.MoE {
			active = fmt.Sprintf("%.1fB", m.ReadParamsB)
		}
		if m.RecurrentLayers > 0 {
			active += " hybrid"
		}
		tools := "yes"
		if !m.Tools {
			tools = "no"
		}
		match := "approx."
		if m.QuantMatchesCloud {
			match = "exact"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%dk\t%s\t%s\n", m.OllamaTag, m.ParamLabel(), active, q.Name, gb(float64(q.FileBytes)), m.NativeCtx/1024, tools, match)
	}
	tw.Flush()
	fmt.Fprintln(output, "\nUse the name in the first column. Other models: `trialm inspect <gguf file or URL>`.")
}

func PrintRuntimes(output io.Writer, ps []runtime.Profile, installed []hw.InstalledRuntime) {
	inst := map[string]hw.InstalledRuntime{}
	for _, r := range installed {
		inst[r.ID] = r
	}
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUNTIME\tVERSIONS\tENGINE\tAPIS\tINSTALLED")
	for _, p := range ps {
		i := "-"
		if r, ok := inst[p.ID]; ok {
			i = strings.TrimSpace("yes " + r.Version)
		} else if p.ID == "lmstudio-mlx" {
			if r, ok := inst["lmstudio"]; ok {
				i = strings.TrimSpace("yes " + r.Version)
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Versions, p.Engine, strings.Join(p.APIs, ", "), i)
	}
	tw.Flush()
	fmt.Fprintf(output, "\nProfiles updated %s. triaLM itself serves every API, so any harness works with any profile.\n", runtime.Updated())
}

func rowLabel(r predict.Result) string {
	mode := "defaults"
	if r.Settings.Tuned {
		mode = "tuned"
	}
	return fmt.Sprintf("%s (%s)", r.Runtime.Name, mode)
}

func fitCell(r predict.Result) string {
	m := r.Memory
	sym := map[string]string{predict.FitOK: "✓", predict.FitTight: "~", predict.FitOffload: "⚠", predict.FitNo: "✗"}[m.Level]
	return fmt.Sprintf("%s %.1f/%.0f GB", sym, m.TotalBytes/(1<<30), m.BudgetBytes/(1<<30))
}

func PrintPrediction(output io.Writer, m catalog.Model, t predict.Target, sc predict.Scenario, rs []predict.Result, inst core.Installed) {
	fmt.Fprintf(output, "\n%s on %s\n", m.Name, t.Name)
	fmt.Fprintf(output, "Scenario: %s — %dk-token system prompt + tools, then ~%s new tokens and ~%d output tokens per turn\n\n",
		predict.HarnessTitle(sc.Harness), sc.BasePromptTokens/1000, sim.Kilo(sc.NewTokensPerTurn), sc.OutputTokensPerTurn)
	tw := tabwriter.NewWriter(output, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "RUNTIME\tFILE\tCONTEXT\tMEMORY\tFIRST REPLY\tFOLLOW-UP\tWRITES AT\tTASK*")
	var notes []string
	var acc *predict.Accuracy
	cloud := ""
	for _, r := range rs {
		if r.Unsupported != "" {
			fmt.Fprintf(tw, "%s\t—\t—\t—\tnot available: %s\t\t\t\n", rowLabel(r), r.Unsupported)
			continue
		}
		first := dur(r.Scenario.ColdFirstReply)
		follow := dur(r.Scenario.FollowUpTurn)
		writes := writingSpeed(r)
		task := dur(r.Scenario.Session)
		if !r.Scenario.FitsInContext {
			task = "✗ breaks"
		}
		if r.Memory.Level == predict.FitNo {
			first, follow, writes, task = "—", "—", "—", "—"
		} else if r.Accuracy.NoClaim {
			first, follow, writes, task = "?"+first, "?"+follow, "?"+writes, "?"+task
		}
		ctx := fmt.Sprintf("%dk", r.Settings.ContextTokens/1024)
		if !r.Scenario.FitsInContext {
			ctx += " ⚠"
		}
		label := rowLabel(r)
		if r.Kind == "calibrated" {
			label += " calibrated"
		}
		if inst.Has(r.Runtime.ID) {
			label += " ●"
		}
		fmt.Fprintf(tw, "%s\t%s %s\t%s\t%s\t%s\t%s\t%s\t%s\n", label, r.Quant.Name, gb(float64(r.Quant.FileBytes)), ctx, fitCell(r), first, follow, writes, task)
		for _, wn := range r.Warnings {
			if strings.HasPrefix(wn, "quality caveat") || strings.HasPrefix(wn, "trial stand-in") || strings.Contains(wn, "no native tool calling") {
				if !contains(notes, wn) {
					notes = append(notes, wn)
				}
				continue
			}
			notes = append(notes, rowLabel(r)+": "+wn)
		}
		if acc == nil {
			a := r.Accuracy
			acc = &a
		}
		cloud = dur(r.Scenario.CloudSession)
	}
	tw.Flush()
	fmt.Fprintf(output, "\nFIRST REPLY = cold start (model load + the whole prompt). FOLLOW-UP = a typical later turn (new tokens only + the reply).\n")
	if inst.Detected && len(inst.ByProfile) > 0 {
		fmt.Fprintln(output, "● = installed on this machine.")
	}
	fmt.Fprintf(output, "* TASK = a %d-call agent task; at datacenter speed it would take about %s. \"✗ breaks\" = the harness prompt does not fit the context.\n", sc.Turns, cloud)
	if acc != nil {
		if acc.NoClaim {
			fmt.Fprintf(output, "Accuracy: no claim — %s.\n", acc.Basis)
		} else {
			pre := fmt.Sprintf("±%d%%", acc.PrefillPct)
			if acc.PrefillMeasureOnly {
				pre = "measure only (" + pre + ")"
			}
			prefix := "Typical error (about 3 in 4 predictions)"
			if strings.HasPrefix(acc.Basis, "calibrated") {
				prefix = "Synthetic fit band (75% interval)"
			}
			fmt.Fprintf(output, "%s: generation ±%d%%, prompt processing %s. %s.\n", prefix, acc.DecodePct, pre, acc.Basis)
		}
		if acc.CalibrateAdvice != "" && !strings.Contains(acc.CalibrateAdvice, "trialm detect --probe") {
			fmt.Fprintf(output, "Tip: %s.\n", acc.CalibrateAdvice)
		}
	}
	if len(t.Notes) > 0 {
		fmt.Fprintln(output)
		for _, n := range t.Notes {
			if strings.Contains(n, "run with --probe") {
				continue
			}
			fmt.Fprintf(output, "  · %s\n", n)
		}
	}
	if len(notes) > 0 {
		fmt.Fprintln(output)
		for _, n := range notes {
			fmt.Fprintf(output, "  ⚠ %s\n", n)
		}
	}
	fmt.Fprintln(output)
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func cacheLabel(kv string) string {
	if kv == "q8_0" {
		return "8-bit context cache"
	}
	return "16-bit context cache"
}

func PrintServeBanner(output io.Writer, m catalog.Model, t predict.Target, p runtime.Profile, why string, r predict.Result, upDesc string, port, requested int, verbose, warnings bool) {
	s := r.Settings
	fmt.Fprintf(output, "\ntrialm is simulating %s %s on %s\n", m.Name, r.Quant.Name, t.Name)
	mode := "defaults"
	if s.Tuned {
		mode = "tuned settings"
	}
	fmt.Fprintf(output, "  runtime      %s, %s (%s)\n", p.Name, mode, why)
	fmt.Fprintf(output, "  context      %d tokens (%s) · %s · %s", s.ContextTokens, s.ContextNote, cacheLabel(s.KVCacheType), r.Backend)
	if s.KeepAlive > 0 {
		fmt.Fprintf(output, " · unloads after %s idle", s.KeepAlive)
	}
	fmt.Fprintln(output)
	fmt.Fprintf(output, "  speed        writes %s, reads prompts at %s tokens/s (%s)\n", writingSpeed(r), bandNumber(r.PrefillTPS2k, r.Accuracy.PrefillPct), readingRatio(r.DecodeTPSDeep))
	fmt.Fprintf(output, "  model output %s\n", upDesc)
	if port != requested {
		fmt.Fprintf(output, "  note         port %d was busy (is Ollama running?), using %d\n", requested, port)
	}
	fmt.Fprintf(output, "\n  Endpoints on http://127.0.0.1:%d — Ollama (/api/*), OpenAI (/v1/chat/completions, /v1/responses), Anthropic (/v1/messages)\n", port)
	if !strings.HasPrefix(upDesc, "offline") {
		fmt.Fprintln(output, "  Privacy: prompts your harness sends (including code and command output) go to the upstream above.")
		fmt.Fprintln(output, "           Files and tools stay on this machine; only model requests leave it.")
	}
	if verbose && len(s.Assumptions) > 0 {
		fmt.Fprintln(output, "  Assumptions (unverified runtime behaviour):")
		for _, a := range s.Assumptions {
			fmt.Fprintf(output, "    - %s\n", a)
		}
	}
	if warnings { // try already printed them with the prediction
		for _, wn := range r.Warnings {
			fmt.Fprintf(output, "  ⚠ %s\n", wn)
		}
	}
	fmt.Fprintln(output)
}

func writingSpeed(r predict.Result) string {
	return fmt.Sprintf("%s tok/s at %s", bandNumber(r.DecodeTPSDeep, r.Accuracy.DecodePct), sim.Kilo(r.Scenario.DecodeDepth))
}

var bandNumber = figure.BandNumber

func suffix(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

func GB(b float64) string                         { return gb(b) }
func GBMem(b float64) string                      { return gbMem(b) }
func Dur(d interface{ Seconds() float64 }) string { return dur(d) }
func CacheLabel(k string) string                  { return cacheLabel(k) }
func BandNumber(v float64, pct int) string        { return bandNumber(v, pct) }
func WritingSpeed(r predict.Result) string        { return writingSpeed(r) }
