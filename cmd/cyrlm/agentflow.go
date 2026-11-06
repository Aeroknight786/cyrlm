package main

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/aeroknight786/cyrlm/internal/account"
	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/harness"
	"github.com/aeroknight786/cyrlm/internal/hw"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/sim"
	"github.com/aeroknight786/cyrlm/internal/verify"
)

//go:embed agent_prompt.md
var agentPrompt string

// ---------------------------------------------------------------- prompt

func cmdPrompt(args []string) error {
	fs := flag.NewFlagSet("prompt", flag.ExitOnError)
	h := fs.String("harness", "", "harness the message is for (fills in --harness)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	p := agentPrompt
	if *h != "" {
		hh, err := harness.Get(*h)
		if err != nil {
			return err
		}
		p = strings.ReplaceAll(p, "<HARNESS>", hh.ID)
	} else {
		p = strings.ReplaceAll(p, "You are running in <HARNESS> — use that as `--harness`", "Work out which coding tool you are running in and use it wherever <your-tool> appears as `--harness`")
		p = strings.ReplaceAll(p, "<HARNESS>", "<your-tool>")
	}
	fmt.Print(p)
	return nil
}

// ---------------------------------------------------------------- recommend

type recRow struct {
	r       predict.Result
	verdict string
	rank    int
}

// verdictFor classifies how a setup feels in an agent session.
func verdictFor(r predict.Result) (string, int) {
	first := r.Scenario.ColdFirstReply.Seconds()
	turn := r.Scenario.FollowUpTurn.Seconds()
	switch {
	case turn <= 20 && first <= 120:
		return "comfortable", 0
	case turn <= 60 && first <= 300:
		return "usable", 1
	}
	return "slow", 2
}

func cmdRecommend(args []string) error {
	fs := flag.NewFlagSet("recommend", flag.ExitOnError)
	var c common
	c.register(fs, "all")
	top := fs.Int("top", 5, "how many options to show")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if len(pos) > 0 {
		return errors.New("usage: cyrlm recommend [--harness h] [--top n] [--json]")
	}
	sc, hh, err := scenarioFor(c.harness)
	if err != nil {
		return err
	}
	t, _, err := c.target()
	if err != nil {
		return err
	}
	in := detectInstalled(t)
	var rows []recRow
	for _, m := range catalog.All() {
		if !m.Tools && hh.ID != "aider" {
			continue
		}
		row, ok, err := bestFileFor(c, m, t, sc, in, hh)
		if err != nil {
			return err
		}
		if ok {
			rows = append(rows, row)
		}
	}
	// Within a speed tier, larger models first (size is the capability proxy).
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].rank != rows[j].rank {
			return rows[i].rank < rows[j].rank
		}
		return rows[i].r.Model.TotalParamsB > rows[j].r.Model.TotalParamsB
	})
	if *top > 0 && len(rows) > *top {
		rows = rows[:*top]
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
			out = append(out, jr{Rank: i + 1, Model: row.r.Model.OllamaTag, ModelName: row.r.Model.Name, Verdict: row.verdict,
				Quant: row.r.Quant.Name, Runtime: row.r.Runtime.ID, TryCmd: tryCmd(row.r, hh.ID),
				Installed: in.has(row.r.Runtime.ID), Result: jsonResults([]predict.Result{row.r})[0]})
		}
		return printJSON(map[string]any{"machine": t.Name, "harness": hh.ID, "ranking": "speed tier (comfortable/usable/slow), then model size as a capability proxy — not a coding-quality score", "options": out})
	}
	fmt.Printf("\nBest local models for %s on %s\n\n", hh.Name, t.Name)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "#\tMODEL\tRUNTIME\tFILE\tFIRST REPLY\tFOLLOW-UP\tTASK*\tFEEL")
	for i, row := range rows {
		r := row.r
		rt := r.Runtime.Name
		if in.has(r.Runtime.ID) {
			rt += " ●"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s %s\t%s\t%s\t%s\t%s\n", i+1, r.Model.OllamaTag, rt, r.Quant.Name, gb(float64(r.Quant.FileBytes)),
			dur(r.Scenario.ColdFirstReply), dur(r.Scenario.FollowUpTurn), dur(r.Scenario.Session), row.verdict)
	}
	w.Flush()
	if len(rows) == 0 {
		fmt.Println("No catalog model gives a working agent setup on this machine.")
		return nil
	}
	allSlow := true
	for _, r := range rows {
		if r.verdict != "slow" {
			allSlow = false
		}
	}
	if allSlow {
		fmt.Println("\n⚠ None of these will feel usable in a coding agent on this machine: every first reply takes minutes. A machine with a GPU or Apple Silicon makes a large difference.")
	}
	fmt.Printf("\n* TASK = a %d-call agent task (cloud: ~%s). Tuned settings. ● = runtime installed. More options: --top 20\n", sc.Turns, dur(rows[0].r.Scenario.CloudSession))
	fmt.Println("Ranked by speed tier, then model size (a rough capability proxy — cyrlm does not score coding quality; try the top picks).")
	fmt.Println("Each model shows the file that runs best here: its usual 4-bit file when that is fast, else a smaller 3-bit file (more mistakes; see the quality caveat in `cyrlm predict`).")
	fmt.Printf("Try #1: %s\n\n", tryCmd(rows[0].r, hh.ID))
	return nil
}

// tryCmd is the trial command for a recommended row (with its file).
func tryCmd(r predict.Result, harnessID string) string {
	q := ""
	if len(r.Model.Quants) > 0 && r.Quant.Name != r.Model.Quants[0].Name {
		q = " --quant " + r.Quant.Name
	}
	return fmt.Sprintf("cyrlm try %s --runtime %s%s --harness %s", r.Model.OllamaTag, r.Runtime.ID, q, harnessID)
}

// bestFileFor picks the model file (quantization) and runtime to show for
// m. Every file is tried; the model's default file wins when it reaches the
// best speed tier any file reaches, otherwise the largest file (closest to
// the default's quality) that does — so a 3-bit file is chosen only when
// it is what lets the model fit or feel fast here. --quant pins the file.
func bestFileFor(c common, m catalog.Model, t predict.Target, sc predict.Scenario, in installed, hh harness.Harness) (recRow, bool, error) {
	quants := []string{c.quant}
	if c.quant == "" {
		quants = quants[:0]
		for _, q := range m.Quants {
			quants = append(quants, q.Name)
		}
	}
	var cands []recRow
	for _, qn := range quants {
		cq := c
		cq.quant = qn
		rs, err := predictAll(cq, m, t, sc, []bool{true})
		if err != nil {
			continue // this file does not exist for any runtime here
		}
		best, mine := recommend(rs, in, hh)
		pick := best
		if mine != nil && best != nil && mine.Scenario.Session.Seconds() <= best.Scenario.Session.Seconds()*1.05 {
			pick = mine
		}
		if pick == nil {
			continue
		}
		v, rank := verdictFor(*pick)
		cands = append(cands, recRow{r: *pick, verdict: v, rank: rank})
	}
	if len(cands) == 0 {
		return recRow{}, false, nil
	}
	top := cands[0].rank
	for _, r := range cands {
		if r.rank < top {
			top = r.rank
		}
	}
	def := m.Quants[0]
	var pick *recRow
	for i := range cands {
		r := &cands[i]
		if r.rank != top {
			continue
		}
		if r.r.Quant.Name == def.Name || (def.Format == "gguf" && r.r.Quant.Format == "mlx" && r.r.Quant.FileBytes <= def.FileBytes*11/10) {
			// The default file (or its MLX equivalent) reaches the top tier.
			if pick == nil || pick.r.Quant.FileBytes < def.FileBytes*9/10 || r.r.Scenario.Session < pick.r.Scenario.Session {
				pick = r
			}
			continue
		}
		if r.r.Quant.FileBytes > def.FileBytes {
			continue // bigger than the default: slower for a small quality gain
		}
		if pick == nil || (pick.r.Quant.FileBytes < def.FileBytes*9/10 && r.r.Quant.FileBytes > pick.r.Quant.FileBytes) {
			pick = r
		}
	}
	if pick == nil {
		for i := range cands {
			if cands[i].rank == top && (pick == nil || cands[i].r.Quant.FileBytes < pick.r.Quant.FileBytes) {
				pick = &cands[i]
			}
		}
	}
	return *pick, true, nil
}

// ---------------------------------------------------------------- verify

func cmdVerify(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ExitOnError)
	var c common
	c.register(fs, "")
	endpoint := fs.String("endpoint", "", "runtime URL (default: the runtime's usual local port)")
	model := fs.String("model", "", "model name as the runtime knows it")
	catalogModel := fs.String("catalog-model", "", "catalog model it corresponds to (default: --model)")
	promptTokens := fs.Int("prompt-tokens", 8000, "size of the uncached test prompt")
	follow := fs.Int("follow-up-tokens", 1000, "new tokens in the follow-up turn")
	output := fs.Int("output-tokens", 256, "tokens to generate per request")
	share := fs.Bool("share", false, "share the result without asking (needs `cyrlm login`)")
	skipHarness := fs.Bool("skip-harness-check", false, "measure speed only; skip the tool-call round trip in the --harness request shape")
	apiKey := fs.String("api-key", "", "runtime API key, if it needs one")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	c.finish(fs)
	if c.runtime == "" || c.runtime == "all" {
		return errors.New("usage: cyrlm verify --runtime <ollama|llamacpp|lmstudio|lmstudio-mlx|mlx-lm> --model <name> [--catalog-model <id>] [--quant q] [--ctx n]")
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
		fmt.Printf("note: %q is not in the catalog; measuring without a prediction (pass --catalog-model to compare)\n", cm)
	}

	if !c.jsonOut {
		fmt.Printf("\nMeasuring %s on %s at %s\n", *model, prof.Name, *endpoint)
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
	var hcheck *HarnessCheck
	if c.harness != "" && !*skipHarness {
		hh, err := harness.Get(c.harness)
		if err != nil {
			return err
		}
		if !c.jsonOut {
			fmt.Printf("Checking %s's requests (greeting, tool call, tool result) against %s…\n", hh.Name, prof.Name)
		}
		r := harnessCheck(ctx, *endpoint, *model, *apiKey, hh, prof.APIs)
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
		fmt.Printf("could not save the result: %v\n", saveErr)
		return nil
	}
	fmt.Printf("Saved: %s\n", path)
	cfg := account.Load()
	switch {
	case *share || cfg.Share == account.ShareAlways:
		return doShare(cfg, path, rep, true)
	case cfg.Share == account.ShareAsk && cfg.CanShare():
		fmt.Println("Help improve predictions for everyone: `cyrlm share --last` shows exactly what would be sent and asks first.")
	}
	return nil
}

func buildReport(res *verify.Result, pred *predict.Result, prof runtime.Profile, t predict.Target, sys *hw.System, pt, ft, ot int) account.Report {
	rep := account.Report{
		Schema: 1, ID: account.NewID(), CyrlmVersion: version, CreatedAt: time.Now().UTC(), Runtime: prof.ID,
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
	if ir := detectInstalled(t); ir.detected {
		for _, r := range ir.byProfile[prof.ID] {
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

func printHarnessCheck(hc HarnessCheck) {
	mark := func(s string) string {
		switch s {
		case checkPass:
			return "✓"
		case checkFail:
			return "✗"
		}
		return "–"
	}
	fmt.Printf("\n%s compatibility (%s):\n", hc.Harness, hc.Path)
	fmt.Printf("  %s greeting   %s tool call   %s follow-up after the tool result\n", mark(hc.Greeting), mark(hc.ToolCall), mark(hc.FollowUp))
	if hc.Passed {
		fmt.Println("  Ready: real requests in this tool's format work end to end.")
		return
	}
	fmt.Printf("  NOT READY: %s\n", hc.Error)
	fmt.Println("  The speed numbers above are valid, but the coding tool will fail until this passes.")
}

func printVerify(rep account.Report, res *verify.Result, havePred bool) {
	fmt.Println()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
		fmt.Println("(model was already loaded: load time not measured)")
	}
	switch res.CacheCheck {
	case verify.CacheReused:
		fmt.Println("Prompt cache: reused on the follow-up ✓")
	case verify.CacheNotReused:
		fmt.Println("Prompt cache: ✗ NOT reused — every turn re-reads the whole conversation. Check the runtime's cache settings.")
	default:
		fmt.Println("Prompt cache: inconclusive at this prompt size (rerun with --prompt-tokens 16000)")
	}
	for _, n := range res.Notes {
		if !strings.Contains(n, "prompt cache") && !strings.Contains(n, "too fast") {
			fmt.Printf("note: %s\n", n)
		}
	}
	fmt.Println()
}

// ---------------------------------------------------------------- account

func cmdLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	key := fs.String("key", "", "account key (default: read from stdin)")
	apiURL := fs.String("api", "", "account server URL")
	fs.Bool("openrouter", true, "store your OpenRouter key (default)")
	acctKey := fs.Bool("account", false, "store a cyrlm account key instead (needs an account server)")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	cfg := account.Load()
	if !*acctKey {
		k := strings.TrimSpace(*key)
		if k == "" {
			fmt.Print("Paste your OpenRouter key (https://openrouter.ai/keys; input is visible, it is stored only on this machine): ")
			line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
			k = strings.TrimSpace(line)
		}
		if k == "" {
			return errors.New("no key entered")
		}
		cfg.OpenRouterKey = k
		if err := cfg.Save(); err != nil {
			return err
		}
		dir, _ := account.Dir()
		fmt.Printf("OpenRouter key saved (%s) in %s — trials will use it.\n", account.Mask(k), dir)
		return nil
	}
	if *apiURL != "" {
		cfg.APIURL = *apiURL
	}
	k := strings.TrimSpace(*key)
	if k == "" {
		fmt.Print("Paste your cyrlm key (from your account page; input is visible): ")
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return errors.New("no key entered")
		}
		k = strings.TrimSpace(line)
	}
	if k == "" {
		return errors.New("no key entered")
	}
	cfg.Key = k
	if cfg.APIURL != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		me, err := cfg.CheckKey(ctx)
		if err != nil {
			return fmt.Errorf("key not accepted: %w", err)
		}
		fmt.Printf("Logged in as %s", me.Account)
		if me.CreditsUSD > 0 {
			fmt.Printf(" · trial credits $%.2f", me.CreditsUSD)
		}
		fmt.Println()
	} else {
		fmt.Println("Key saved. (No account server configured yet: set one with `cyrlm config set api-url <url>`.)")
	}
	return cfg.Save()
}

func cmdLogout(args []string) error {
	cfg := account.Load()
	cfg.Key, cfg.OpenRouterKey = "", ""
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Println("Logged out: keys removed from this machine.")
	return nil
}

func cmdConfig(args []string) error {
	cfg := account.Load()
	if len(args) == 0 || args[0] == "get" {
		key := "(not logged in)"
		if cfg.LoggedIn() {
			key = cfg.MaskedKey()
		}
		api := cfg.APIURL
		if api == "" {
			api = "(not configured)"
		}
		dir, _ := account.Dir()
		or := "(none)"
		if cfg.OpenRouterKey != "" {
			or = account.Mask(cfg.OpenRouterKey)
		}
		fmt.Printf("key         %s\nopenrouter  %s\napi-url     %s\nshare       %s   (ask | always | never)\nfiles       %s\n", key, or, api, cfg.Share, dir)
		return nil
	}
	if args[0] != "set" || len(args) != 3 {
		return errors.New("usage: cyrlm config [get] | cyrlm config set share <ask|always|never> | cyrlm config set api-url <url>")
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
	fmt.Printf("%s = %s\n", args[1], args[2])
	return nil
}

func cmdShare(args []string) error {
	fs := flag.NewFlagSet("share", flag.ExitOnError)
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
	fmt.Printf("This is everything that would be sent (no prompts, file paths, hostnames or usernames):\n\n%s\n\n", b)
	if *dry {
		return nil
	}
	cfg := account.Load()
	if cfg.Share == account.ShareNever && !*yes {
		return errors.New("sharing is turned off (`cyrlm config set share ask` to enable)")
	}
	return doShare(cfg, path, rep, *yes)
}

func doShare(cfg account.Config, path string, rep account.Report, confirmed bool) error {
	if rep.SharedID != "" {
		fmt.Printf("Already shared (id %s).\n", rep.SharedID)
		return nil
	}
	if !cfg.CanShare() {
		if !cfg.LoggedIn() {
			fmt.Println("Not shared: sharing is not available in this version yet (the result is saved on this machine).")
		} else {
			fmt.Println("Not shared: sharing is not available in this version yet (the result is saved on this machine).")
		}
		return nil
	}
	if !confirmed {
		fmt.Print("Share this result? [y/N] ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Not shared.")
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
	fmt.Printf("Shared — thank you (id %s).\n", id)
	return nil
}
