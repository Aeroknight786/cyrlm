package cli

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aeroknight786/trialm/internal/account"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/ui"
	"github.com/aeroknight786/trialm/internal/verify"
)

//go:embed agent_prompt.md
var agentPrompt string

// ---------------------------------------------------------------- prompt

func cmdPrompt(args []string) error {
	fs := flag.NewFlagSet("prompt", flag.ContinueOnError)
	h := fs.String("harness", "", "harness the message is for (fills in --harness)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	return ui.PrintPrompt(out(), agentPrompt, *h)
}

// ---------------------------------------------------------------- recommend

func cmdRecommend(args []string) error {
	fs := flag.NewFlagSet("recommend", flag.ContinueOnError)
	var c common
	c.register(fs, "all")
	top := fs.Int("top", 5, "how many options to show")
	claims := fs.Bool("claims", false, "published claims gallery")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if *claims {
		return printClaims(c.jsonOut)
	}
	if len(pos) > 0 && (strings.Contains(strings.Join(pos, " "), " -") || strings.Contains(strings.Join(pos, " "), "https://")) {
		fmt.Fprintln(out(), "claim parsing arrives in v0.6")
		return emit("result", map[string]any{"message": "claim parsing arrives in v0.6"}, core.Next{Cmd: "trialm find --claims", Why: "browse sourced claims"})
	}
	if err := c.validateRequirements(); err != nil {
		return err
	}
	if len(pos) > 0 && currentVerb != "find" {
		return errors.New("usage: trialm recommend [--harness h] [--top n] [--json]")
	}
	sc, hh, err := scenarioFor(c.harness)
	if err != nil {
		return err
	}
	t, _, err := c.target()
	if err != nil {
		return err
	}
	query := strings.Join(pos, " ")
	if query == "" && c.prefs.Model != "any" {
		query = c.prefs.Model
	}
	listing, err := (core.Session{}).Find(commandContext, c.options(), t, sc, hh, query, *top)
	if err != nil {
		return err
	}
	rows, in := listing.Rows, listing.Installed
	if currentVerb == "find" {
		if err := saveListing(rows, hh.ID); err != nil {
			return err
		}
	}
	if c.jsonOut {
		type jr struct {
			Rank      int        `json:"rank"`
			Model     string     `json:"model"`
			ModelName string     `json:"model_name"`
			Quant     string     `json:"quant"`
			Runtime   string     `json:"runtime"`
			TryCmd    string     `json:"try_command"`
			Verdict   string     `json:"verdict"`
			Installed bool       `json:"runtime_installed"`
			Result    jsonResult `json:"prediction"`
		}
		var out []jr
		for i, row := range rows {
			out = append(out, jr{Rank: i + 1, Model: row.Result.Model.OllamaTag, ModelName: row.Result.Model.Name, Verdict: row.Verdict,
				Quant: row.Result.Quant.Name, Runtime: row.Result.Runtime.ID, TryCmd: tryCmd(row.Result, hh.ID),
				Installed: in.Has(row.Result.Runtime.ID), Result: jsonResults([]predict.Result{row.Result})[0]})
		}
		return printJSON(map[string]any{"machine": t.Name, "harness": hh.ID, "ranking": "speed tier (comfortable/usable/slow), then model size as a capability proxy — not a coding-quality score", "options": out})
	}
	fmt.Fprintf(out(), "\nBest local models for %s on %s\n\n", hh.Name, t.Name)
	w := tabwriter.NewWriter(out(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tMODEL\tRUNTIME\tFILE\tFIRST REPLY\tFOLLOW-UP\tTASK*\tFEEL")
	for i, row := range rows {
		r := row.Result
		rt := r.Runtime.Name
		if r.Kind == "calibrated" {
			rt += " calibrated"
		}
		if in.Has(r.Runtime.ID) {
			rt += " ●"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s %s\t%s\t%s\t%s\t%s\n", i+1, r.Model.OllamaTag, rt, r.Quant.Name, gb(float64(r.Quant.FileBytes)),
			dur(r.Scenario.ColdFirstReply), dur(r.Scenario.FollowUpTurn), dur(r.Scenario.Session), row.Verdict)
	}
	w.Flush()
	if len(rows) == 0 {
		fmt.Fprintln(out(), "No catalog model gives a working agent setup on this machine.")
		return nil
	}
	allSlow := true
	for _, r := range rows {
		if r.Verdict != "slow" {
			allSlow = false
		}
	}
	if allSlow {
		fmt.Fprintln(out(), "\n⚠ None of these will feel usable in a coding agent on this machine: every first reply takes minutes. A machine with a GPU or Apple Silicon makes a large difference.")
	}
	fmt.Fprintf(out(), "\n* TASK = a %d-call agent task (cloud: ~%s). Tuned settings. ● = runtime installed. More options: --top 20\n", sc.Turns, dur(rows[0].Result.Scenario.CloudSession))
	fmt.Fprintln(out(), "Ranked by speed tier, then model size (a rough capability proxy — triaLM does not score coding quality; try the top picks).")
	fmt.Fprintln(out(), "Each model shows the file that runs best here: its usual 4-bit file when that is fast, else a smaller 3-bit file (more mistakes; see the quality caveat in `trialm predict`).")
	fmt.Fprintf(out(), "Try #1: %s\n\n", tryCmd(rows[0].Result, hh.ID))
	return nil
}

// tryCmd is the trial command for a recommended row (with its file).
func tryCmd(r predict.Result, harnessID string) string {
	q := ""
	if len(r.Model.Quants) > 0 && r.Quant.Name != r.Model.Quants[0].Name {
		q = " --quant " + r.Quant.Name
	}
	return fmt.Sprintf("trialm try %s --runtime %s%s --harness %s", r.Model.OllamaTag, r.Runtime.ID, q, harnessID)
}

// ---------------------------------------------------------------- verify

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	var c common
	c.register(fs, "")
	endpoint := fs.String("endpoint", "", "runtime URL (default: the runtime's usual local port)")
	model := fs.String("model", "", "model name as the runtime knows it")
	catalogModel := fs.String("catalog-model", "", "catalog model it corresponds to (default: --model)")
	promptTokens := fs.Int("prompt-tokens", 8000, "size of the uncached test prompt")
	follow := fs.Int("follow-up-tokens", 1000, "new tokens in the follow-up turn")
	output := fs.Int("output-tokens", 256, "tokens to generate per request")
	share := fs.Bool("share", false, "share the result without asking (needs `trialm login`)")
	skipHarness := fs.Bool("skip-harness-check", false, "measure speed only; skip the tool-call round trip in the --harness request shape")
	apiKey := fs.String("api-key", "", "runtime API key, if it needs one")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	c.finish(fs)
	if c.runtime == "" || c.runtime == "all" {
		return errors.New("usage: trialm verify --runtime <ollama|llamacpp|lmstudio|lmstudio-mlx|mlx-lm> --model <name> [--catalog-model <id>] [--quant q] [--ctx n]")
	}
	prof, err := runtime.Get(c.runtime)
	if err != nil {
		return err
	}
	if *model == "" {
		return errors.New("--model is required (the name your runtime uses, e.g. qwen3-coder:30b)")
	}
	if *endpoint == "" {
		*endpoint = fmt.Sprintf("http://127.0.0.1:%d", prof.Port)
	}
	t, sys, err := c.target()
	if err != nil {
		return err
	}
	// Prediction for comparison (optional: unknown models are measured only).
	cm := *catalogModel
	if cm == "" {
		cm = *model
	}
	var pred *predict.Result
	if m, err := lookupModel(cm); err == nil {
		sc, _, _ := scenarioFor(c.harness)
		r := predict.Predict(predict.Input{Model: m, QuantName: c.quant, Runtime: prof, Target: t, Tuned: c.tuned, CtxOverride: c.ctx, KVCacheType: c.kv, Scenario: sc, BackendOverride: c.backend})
		if r.Unsupported == "" {
			pred = &r
		}
	} else if !c.jsonOut {
		fmt.Fprintf(out(), "note: %q is not in the catalog; measuring without a prediction (pass --catalog-model to compare)\n", cm)
	}

	if !c.jsonOut {
		fmt.Fprintf(out(), "\nMeasuring %s on %s at %s\n", *model, prof.Name, *endpoint)
	}
	progress := os.Stdout
	if c.jsonOut {
		progress = os.Stderr
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := verify.Run(ctx, verify.Options{Endpoint: *endpoint, Model: *model, APIKey: *apiKey, PromptTokens: *promptTokens, FollowUpTokens: *follow, OutputTokens: *output, Progress: progress})
	if err != nil {
		return err
	}
	var hcheck *verify.HarnessCheck
	if c.harness != "" && !*skipHarness {
		hh, err := harness.Get(c.harness)
		if err != nil {
			return err
		}
		if !c.jsonOut {
			fmt.Fprintf(out(), "Checking %s's requests (greeting, tool call, tool result) against %s…\n", hh.Name, prof.Name)
		}
		r := verify.HarnessCheckRun(ctx, *endpoint, *model, *apiKey, hh, prof.APIs)
		hcheck = &r
	}
	rep := buildReport(res, pred, prof, t, sys, *promptTokens, *follow, *output)
	if pred != nil {
		rep.Model, rep.Quant = pred.Model.ID, pred.Quant.Name
	} else {
		rep.Model = cm
	}
	path, saveErr := account.SaveReport(rep)
	if c.jsonOut {
		out := map[string]any{"speed": rep, "saved_to": path, "notes": res.Notes}
		if hcheck != nil {
			out["harness_check"] = hcheck
			out["ready"] = hcheck.Passed
		}
		return printJSON(out)
	}
	printVerify(rep, res, pred != nil)
	if hcheck != nil {
		printHarnessCheck(*hcheck)
	}
	if saveErr != nil {
		fmt.Fprintf(out(), "could not save the result: %v\n", saveErr)
		return nil
	}
	fmt.Fprintf(out(), "Saved: %s\n", path)
	cfg := account.Load()
	switch {
	case *share || cfg.Share == account.ShareAlways:
		return doShare(cfg, path, rep, true)
	case cfg.Share == account.ShareAsk && cfg.CanShare():
		fmt.Fprintln(out(), "Help improve predictions for everyone: `trialm share --last` shows exactly what would be sent and asks first.")
	}
	return nil
}

func buildReport(res *verify.Result, pred *predict.Result, prof runtime.Profile, t predict.Target, sys *hw.System, pt, ft, ot int) account.Report {
	rep := account.Report{
		Schema: 1, ID: account.NewID(), TrialmVersion: version, CreatedAt: time.Now().UTC(), Runtime: prof.ID,
		PromptTokens: res.Cold.PromptTokens, FollowUpTokens: res.FollowUp.PromptTokens - res.Cold.PromptTokens, OutputTokens: ot,
		CacheReused: res.CacheReused, CacheCheck: res.CacheCheck, TokensEstimated: res.Cold.TokensEstimated,
	}
	if rep.FollowUpTokens <= 0 {
		rep.FollowUpTokens = ft
	}
	rep.Machine = account.Machine{OS: t.OS, Arch: t.Arch, CPU: "", RAMGB: int(t.RAMBytes/(1<<30) + 0.5),
		Unified: t.Unified, Laptop: t.Laptop, DeviceSpec: t.Device.Name, Segment: string(t.Device.Segment)}
	if sys != nil {
		rep.Machine.CPU, rep.Machine.CPUCores = sys.CPUName, sys.CPUCores
		for _, g := range sys.GPUs {
			rep.Machine.GPU, rep.Machine.GPUCores, rep.Machine.VRAMGB = g.Name, g.Cores, int(float64(g.VRAMBytes)/(1<<30)+0.5)
			break
		}
	}
	if ir := detectInstalled(t); ir.Detected {
		for _, r := range ir.ByProfile[prof.ID] {
			rep.RuntimeVer = r.Version
			break
		}
	}
	probeLoad := res.Probe.TTFTSec
	var pLoad, pPrompt, pDecode, pFollow float64
	if pred != nil {
		tm := pred.Timing
		rep.Settings = account.Settings{ContextTokens: pred.Settings.ContextTokens, KVCacheType: pred.Settings.KVCacheType, FlashAttention: pred.Settings.FlashAttention, Backend: pred.Backend}
		pLoad = tm.LoadColdS
		pPrompt = tm.PrefillSec(0, res.Cold.PromptTokens)
		n := max(1, res.Cold.CompletionTokens)
		sum := 0.0
		for i := 0; i < n; i++ {
			sum += tm.DecodeSec(res.Cold.PromptTokens + i)
		}
		pDecode = float64(n) / sum
		start := res.Cold.PromptTokens + res.Cold.CompletionTokens
		pFollow = tm.PrefillSec(start, max(start+1, res.FollowUp.PromptTokens))
	}
	// The probe only measures a load when the model was not already loaded.
	if probeLoad > 1.5 {
		rep.LoadSec = account.NewMetric(probeLoad, pLoad)
	} else {
		rep.LoadSec = account.NewMetric(0, 0)
	}
	rep.PromptSec = account.NewMetric(res.Cold.TTFTSec, pPrompt)
	rep.DecodeTPS = account.NewMetric(res.Cold.DecodeTPS, pDecode)
	rep.FollowUpSec = account.NewMetric(res.FollowUp.TTFTSec, pFollow)
	return rep
}

func printHarnessCheck(hc verify.HarnessCheck) {
	mark := func(s string) string {
		switch s {
		case "pass":
			return "✓"
		case "fail":
			return "✗"
		}
		return "–"
	}
	fmt.Fprintf(out(), "\n%s compatibility (%s):\n", hc.Harness, hc.Path)
	fmt.Fprintf(out(), "  %s greeting   %s tool call   %s follow-up after the tool result\n", mark(hc.Greeting), mark(hc.ToolCall), mark(hc.FollowUp))
	if hc.Passed {
		fmt.Fprintln(out(), "  Ready: real requests in this tool's format work end to end.")
		return
	}
	fmt.Fprintf(out(), "  NOT READY: %s\n", hc.Error)
	fmt.Fprintln(out(), "  The speed numbers above are valid, but the coding tool will fail until this passes.")
}

func printVerify(rep account.Report, res *verify.Result, havePred bool) {
	fmt.Fprintln(out())
	w := tabwriter.NewWriter(out(), 0, 0, 2, ' ', 0)
	if havePred {
		fmt.Fprintln(w, "\tMEASURED\tPREDICTED\tDIFFERENCE")
	} else {
		fmt.Fprintln(w, "\tMEASURED\t\t")
	}
	row := func(label string, m account.Metric, f func(float64) string) {
		if m.Measured == 0 {
			return
		}
		p, e := "", ""
		if m.Predicted > 0 {
			p, e = f(m.Predicted), fmt.Sprintf("%+.0f%%", -100*(1-m.Measured/m.Predicted))
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", label, f(m.Measured), p, e)
	}
	secs := func(v float64) string { return sim.FormatDur(v) }
	tps := func(v float64) string { return fmt.Sprintf("%.0f tok/s", v) }
	row("Model load", rep.LoadSec, secs)
	row(fmt.Sprintf("Read a %s-token prompt", sim.Kilo(rep.PromptTokens)), rep.PromptSec, secs)
	row("Writing speed", rep.DecodeTPS, tps)
	row(fmt.Sprintf("Follow-up (%s new tokens)", sim.Kilo(rep.FollowUpTokens)), rep.FollowUpSec, secs)
	w.Flush()
	if rep.LoadSec.Measured == 0 {
		fmt.Fprintln(out(), "(model was already loaded: load time not measured)")
	}
	switch res.CacheCheck {
	case verify.CacheReused:
		fmt.Fprintln(out(), "Prompt cache: reused on the follow-up ✓")
	case verify.CacheNotReused:
		fmt.Fprintln(out(), "Prompt cache: ✗ NOT reused — every turn re-reads the whole conversation. Check the runtime's cache settings.")
	default:
		fmt.Fprintln(out(), "Prompt cache: inconclusive at this prompt size (rerun with --prompt-tokens 16000)")
	}
	for _, n := range res.Notes {
		if !strings.Contains(n, "prompt cache") && !strings.Contains(n, "too fast") {
			fmt.Fprintf(out(), "note: %s\n", n)
		}
	}
	fmt.Fprintln(out())
}

// ---------------------------------------------------------------- account

func cmdConfig(args []string) error {
	cfg := account.Load()
	if len(args) == 0 || args[0] == "get" {
		key := "(not logged in)"
		if cfg.LoggedIn() {
			key = "configured"
		}
		api := cfg.APIURL
		if api == "" {
			api = "(not configured)"
		}
		dir, _ := account.Dir()
		or := "(none)"
		if cfg.OpenRouterKey != "" {
			or = "configured"
		}
		fmt.Fprintf(out(), "key         %s\nopenrouter  %s\napi-url     %s\nshare       %s   (ask | always | never)\nfiles       %s\n", key, or, api, cfg.Share, dir)
		return nil
	}
	if args[0] != "set" || len(args) != 3 {
		return errors.New("usage: trialm config [get] | trialm config set share <ask|always|never> | trialm config set api-url <url>")
	}
	switch args[1] {
	case "share":
		switch args[2] {
		case account.ShareAsk, account.ShareAlways, account.ShareNever:
			cfg.Share = args[2]
		default:
			return errors.New("share must be ask, always or never")
		}
	case "api-url":
		cfg.APIURL = args[2]
	default:
		return fmt.Errorf("unknown setting %q", args[1])
	}
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Fprintf(out(), "%s = %s\n", args[1], args[2])
	return nil
}

func cmdShare(args []string) error {
	fs := flag.NewFlagSet("share", flag.ContinueOnError)
	last := fs.Bool("last", false, "share the most recent verification")
	yes := fs.Bool("yes", false, "don't ask (you have already reviewed the payload)")
	dry := fs.Bool("dry-run", false, "only show what would be sent")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	var path string
	var rep account.Report
	switch {
	case len(pos) == 1:
		path = pos[0]
		rep, err = account.ReadReport(path)
	case *last || len(pos) == 0:
		path, rep, err = account.LastReport()
	}
	if err != nil {
		return err
	}
	b, _ := json.MarshalIndent(rep, "", "  ")
	fmt.Fprintf(out(), "This is everything that would be sent (no prompts, file paths, hostnames or usernames):\n\n%s\n\n", b)
	if *dry {
		return nil
	}
	cfg := account.Load()
	if cfg.Share == account.ShareNever && !*yes {
		return errors.New("sharing is turned off (`trialm config set share ask` to enable)")
	}
	return doShare(cfg, path, rep, *yes)
}

func doShare(cfg account.Config, path string, rep account.Report, confirmed bool) error {
	if rep.SharedID != "" {
		fmt.Fprintf(out(), "Already shared (id %s).\n", rep.SharedID)
		return nil
	}
	if !cfg.CanShare() {
		if !cfg.LoggedIn() {
			fmt.Fprintln(out(), "Not shared: sharing is not available in this version yet (the result is saved on this machine).")
		} else {
			fmt.Fprintln(out(), "Not shared: sharing is not available in this version yet (the result is saved on this machine).")
		}
		return nil
	}
	if !confirmed {
		fmt.Fprint(out(), "Share this result? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(out(), "Not shared.")
			return nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	id, err := cfg.Upload(ctx, rep)
	if err != nil {
		return err
	}
	_ = account.MarkShared(path, id)
	fmt.Fprintf(out(), "Shared — thank you (id %s).\n", id)
	return nil
}
