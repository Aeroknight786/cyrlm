package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/hw"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/sim"
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

func printDetect(sys *hw.System, t predict.Target, rts []hw.InstalledRuntime) {
	fmt.Printf("Machine: %s\n", t.Name)
	if sys != nil {
		fmt.Printf("  OS        %s/%s %s\n", sys.OS, sys.Arch, sys.OSVersion)
		fmt.Printf("  CPU       %s (%d cores, %d threads)\n", sys.CPUName, sys.CPUCores, sys.CPUThreads)
		fmt.Printf("  RAM       %s\n", gbMem(float64(sys.RAMBytes)))
		for _, g := range sys.GPUs {
			kind := "dedicated"
			if g.Unified {
				kind = "unified/shared"
			}
			fmt.Printf("  GPU       %s — %s %s", g.Name, gbMem(float64(g.VRAMBytes)), kind)
			if len(g.Backends) > 0 {
				fmt.Printf(" [%s]", strings.Join(g.Backends, ", "))
			}
			fmt.Println()
		}
		if len(sys.GPUs) == 0 {
			fmt.Println("  GPU       none detected (CPU-only predictions)")
		}
	}
	fmt.Printf("\nPrediction basis: %s — %.0f GB/s memory bandwidth, %.1f TFLOPS (spec%s)\n",
		t.Device.Name, t.Device.BandwidthGBs, t.Device.FP16TFLOPS, map[bool]string{true: "", false: "; partly assumed"}[t.Device.Confident])
	fmt.Printf("Memory the model can use: %s\n", gbMem(t.GPUMemBytes))
	for _, n := range t.Notes {
		fmt.Printf("  note: %s\n", n)
	}
	if sys != nil {
		for _, n := range sys.Notes {
			fmt.Printf("  note: %s\n", n)
		}
	}
	fmt.Println("\nInstalled runtimes:")
	if len(rts) == 0 {
		fmt.Println("  none found (Ollama, LM Studio, llama.cpp, mlx-lm, vLLM, Jan, Lemonade, Docker Model Runner)")
	}
	for _, r := range rts {
		state := ""
		if r.Running {
			state = " · running at " + r.Endpoint
		}
		fmt.Printf("  %-20s %s%s\n", runtimeTitles[r.ID], r.Version, state)
		if ids, note := profileIDsFor(r); note != "" {
			fmt.Printf("  %-20s ↳ %s\n", "", note)
		} else if len(ids) > 0 {
			fmt.Printf("  %-20s ↳ simulated as: %s\n", "", strings.Join(ids, ", "))
		}
	}
}

func printModels(ms []catalog.Model) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "MODEL\tPARAMS\tACTIVE\tDEFAULT QUANT\tDOWNLOAD\tCONTEXT\tTOOLS\tCLOUD QUALITY MATCH")
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
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%dk\t%s\t%s\n", m.OllamaTag, m.ParamLabel(), active, q.Name, gb(float64(q.FileBytes)), m.NativeCtx/1024, tools, match)
	}
	w.Flush()
	fmt.Println("\nUse the name in the first column. Other models: `cyrlm inspect <gguf file or URL>`.")
}

func printRuntimes(ps []runtime.Profile, installed []hw.InstalledRuntime) {
	inst := map[string]hw.InstalledRuntime{}
	for _, r := range installed {
		inst[r.ID] = r
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUNTIME\tVERSIONS\tENGINE\tAPIS\tINSTALLED")
	for _, p := range ps {
		i := "-"
		if r, ok := inst[p.ID]; ok {
			i = strings.TrimSpace("yes " + r.Version)
		} else if p.ID == "lmstudio-mlx" {
			if r, ok := inst["lmstudio"]; ok {
				i = strings.TrimSpace("yes " + r.Version)
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", p.ID, p.Versions, p.Engine, strings.Join(p.APIs, ", "), i)
	}
	w.Flush()
	fmt.Printf("\nProfiles updated %s. cyrlm itself serves every API, so any harness works with any profile.\n", runtime.Updated())
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

func printPrediction(m catalog.Model, t predict.Target, sc predict.Scenario, rs []predict.Result, inst installed) {
	fmt.Printf("\n%s on %s\n", m.Name, t.Name)
	fmt.Printf("Scenario: %s — %dk-token system prompt + tools, then ~%s new tokens and ~%d output tokens per turn\n\n",
		predict.HarnessTitle(sc.Harness), sc.BasePromptTokens/1000, sim.Kilo(sc.NewTokensPerTurn), sc.OutputTokensPerTurn)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "RUNTIME\tFILE\tCONTEXT\tMEMORY\tFIRST REPLY\tFOLLOW-UP\tWRITES AT\tTASK*")
	var notes []string
	var acc *predict.Accuracy
	cloud := ""
	for _, r := range rs {
		if r.Unsupported != "" {
			fmt.Fprintf(w, "%s\t—\t—\t—\tnot available: %s\t\t\t\n", rowLabel(r), r.Unsupported)
			continue
		}
		first := dur(r.Scenario.ColdFirstReply)
		follow := dur(r.Scenario.FollowUpTurn)
		writes := fmt.Sprintf("%.0f tok/s", r.DecodeTPSDeep)
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
		if inst.has(r.Runtime.ID) {
			label += " ●"
		}
		fmt.Fprintf(w, "%s\t%s %s\t%s\t%s\t%s\t%s\t%s\t%s\n", label, r.Quant.Name, gb(float64(r.Quant.FileBytes)), ctx, fitCell(r), first, follow, writes, task)
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
	w.Flush()
	fmt.Printf("\nFIRST REPLY = cold start (model load + the whole prompt). FOLLOW-UP = a typical later turn (new tokens only + the reply).\n")
	if inst.detected && len(inst.byProfile) > 0 {
		fmt.Println("● = installed on this machine.")
	}
	fmt.Printf("* TASK = a %d-call agent task; at datacenter speed it would take about %s. \"✗ breaks\" = the harness prompt does not fit the context.\n", sc.Turns, cloud)
	if acc != nil {
		if acc.NoClaim {
			fmt.Printf("Accuracy: no claim — %s.\n", acc.Basis)
		} else {
			pre := fmt.Sprintf("±%d%%", acc.PrefillPct)
			if acc.PrefillMeasureOnly {
				pre = "measure only (" + pre + ")"
			}
			fmt.Printf("Typical error (about 3 in 4 predictions): generation ±%d%%, prompt processing %s. %s.\n", acc.DecodePct, pre, acc.Basis)
		}
		if acc.CalibrateAdvice != "" {
			fmt.Printf("Tip: %s.\n", acc.CalibrateAdvice)
		}
	}
	if len(t.Notes) > 0 {
		fmt.Println()
		for _, n := range t.Notes {
			fmt.Printf("  · %s\n", n)
		}
	}
	if len(notes) > 0 {
		fmt.Println()
		for _, n := range notes {
			fmt.Printf("  ⚠ %s\n", n)
		}
	}
	fmt.Println()
}

func contains(a []string, s string) bool {
	for _, x := range a {
		if x == s {
			return true
		}
	}
	return false
}

func printServeBanner(m catalog.Model, t predict.Target, p runtime.Profile, why string, r predict.Result, upDesc string, port, requested int) {
	s := r.Settings
	fmt.Printf("\ncyrlm is simulating %s %s on %s\n", m.Name, r.Quant.Name, t.Name)
	mode := "defaults"
	if s.Tuned {
		mode = "tuned settings"
	}
	fmt.Printf("  runtime      %s, %s (%s)\n", p.Name, mode, why)
	fmt.Printf("  backend      %s · context %d tokens (%s) · KV cache %s", r.Backend, s.ContextTokens, s.ContextNote, s.KVCacheType)
	if s.KeepAlive > 0 {
		fmt.Printf(" · unloads after %s idle", s.KeepAlive)
	}
	fmt.Println()
	fmt.Printf("  speed        %.0f tok/s writing, %.0f tok/s reading the prompt (%s)\n", r.DecodeTPS0, r.PrefillTPS2k, readingRatio(r.DecodeTPS0))
	fmt.Printf("  model output %s\n", upDesc)
	if port != requested {
		fmt.Printf("  note         port %d was busy (is Ollama running?), using %d\n", requested, port)
	}
	fmt.Printf("\n  Endpoints on http://127.0.0.1:%d — Ollama (/api/*), OpenAI (/v1/chat/completions, /v1/responses), Anthropic (/v1/messages)\n", port)
	if !strings.HasPrefix(upDesc, "offline") {
		fmt.Println("  Privacy: prompts your harness sends (including code and command output) go to the upstream above.")
		fmt.Println("           Files and tools stay on this machine; only model requests leave it.")
	}
	if len(s.Assumptions) > 0 {
		fmt.Println("  Assumptions (unverified runtime behaviour):")
		for _, a := range s.Assumptions {
			fmt.Printf("    - %s\n", a)
		}
	}
	for _, wn := range r.Warnings {
		fmt.Printf("  ⚠ %s\n", wn)
	}
	fmt.Println()
}
