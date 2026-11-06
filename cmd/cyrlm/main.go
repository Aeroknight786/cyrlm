// Command cyrlm predicts and simulates how open-weight models would run on
// your own machine, and serves a paced local endpoint so you can try a model
// inside your coding harness before downloading it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	goruntime "runtime"

	"github.com/aeroknight786/cyrlm/internal/account"
	"github.com/aeroknight786/cyrlm/internal/api"
	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/harness"
	"github.com/aeroknight786/cyrlm/internal/hw"
	"github.com/aeroknight786/cyrlm/internal/predict"
	"github.com/aeroknight786/cyrlm/internal/runtime"
	"github.com/aeroknight786/cyrlm/internal/server"
	"github.com/aeroknight786/cyrlm/internal/sim"
	"github.com/aeroknight786/cyrlm/internal/tok"
	"github.com/aeroknight786/cyrlm/internal/upstream"
)

var version = "0.1.0-dev"

const usage = `cyrlm — try a local coding model before you download it

Easiest: let your coding agent do everything.
  cyrlm prompt --harness claude-code     prints a message; paste it into Claude Code (or codex, opencode, …)

Commands:
  cyrlm detect                           your machine and installed runtimes
  cyrlm recommend [--harness h]          best models + runtimes for this machine
  cyrlm try <model> --harness h          live trial: the real model, at this machine's speed
  cyrlm plan <model> [--runtime r]       exact setup steps (nothing is run)
  cyrlm verify --runtime r --model m     measure your real setup against the prediction
  cyrlm login                            store your OpenRouter key (in your own terminal, never in a chat)
  cyrlm version

Flags: --harness claude-code|codex|opencode|aider|cline|continue|zed|qwen-code
       --runtime ollama|llamacpp|lmstudio|lmstudio-mlx|mlx-lm   --quant Q4_K_M   --ctx N   --json
More commands and flags: cyrlm help --all
`
const usageAll = `cyrlm — try a local model before you download it

Usage:
  cyrlm detect   [--probe] [--json]           what's in this machine and which runtimes are installed
  cyrlm models                                 models in the catalog
  cyrlm runtimes                               runtime profiles (Ollama, llama.cpp, LM Studio, MLX)
  cyrlm predict  <model> [flags]               will it fit, how fast, on every runtime
  cyrlm serve    <model> [flags]               run the simulated local runtime (Ollama-compatible)
  cyrlm try      <model> --harness <h> [flags] predict + serve + connect your harness
  cyrlm inspect  <file.gguf | URL>             read a GGUF header (no full download)
  cyrlm recommend [--harness h]                best models + runtimes for this machine
  cyrlm plan     <model> --runtime r [flags]   exact setup steps (for you or your coding agent)
  cyrlm verify   --runtime r --model m         measure your real setup against the prediction
  cyrlm prompt   [--harness h]                 the message to paste into your coding agent
  cyrlm login [--account] | logout | config    keys (OpenRouter by default; cyrlm account later)
  cyrlm share                                  opt-in sharing of results (needs an account server)
  cyrlm version

Let your coding agent do it all:  cyrlm prompt --harness claude-code   (paste the output into it)

Common flags:
  --runtime <id>     ollama | llamacpp | lmstudio | lmstudio-mlx | mlx-lm (predict: default all)
  --defaults         simulate the runtime's out-of-the-box settings (default: cyrlm's tuned settings)
  --ctx <n>          context window override
  --quant <q>        quantization (e.g. Q4_K_M, Q8_0)
  --harness <h>      claude-code | codex | opencode | aider | cline | continue | zed | qwen-code
  --device <name>    what-if: simulate another machine, e.g. "RTX 4090", "Apple M4 Pro 20c"
  --ram <GB> --vram <GB>   memory for --device

Serve/try flags:
  --upstream <u>     auto (your cyrlm account if logged in, else OpenRouter) | cyrlm | openrouter | mock | URL
  --upstream-model   cloud model id (default: catalog's OpenRouter id)
  --key-env <VAR>    env var holding the upstream API key (default OPENROUTER_API_KEY)
  --provider <slug>  OpenRouter: pin provider(s), comma-separated
  --port <n>         listen port (default 11434, falls back to 11435+ if busy)
  --launch           (try) start the harness in this terminal; logs go to a file

Examples:
  cyrlm predict gpt-oss:20b
  cyrlm predict qwen3-coder:30b --device "Apple M4 Pro 20c" --ram 48
  cyrlm try gpt-oss:20b --harness claude-code --runtime ollama
  cyrlm serve qwen3-coder:30b --runtime llamacpp --upstream mock
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	var err error
	args := os.Args[2:]
	switch os.Args[1] {
	case "detect":
		err = cmdDetect(args)
	case "models":
		err = cmdModels(args)
	case "runtimes":
		err = cmdRuntimes(args)
	case "predict":
		err = cmdPredict(args)
	case "serve":
		err = cmdServe(args, false)
	case "try":
		err = cmdServe(args, true)
	case "inspect":
		err = cmdInspect(args)
	case "recommend":
		err = cmdRecommend(args)
	case "verify":
		err = cmdVerify(args)
	case "plan":
		err = cmdPlan(args)
	case "prompt":
		err = cmdPrompt(args)
	case "login":
		err = cmdLogin(args)
	case "logout":
		err = cmdLogout(args)
	case "share":
		err = cmdShare(args)
	case "config":
		err = cmdConfig(args)
	case "version", "--version", "-v":
		fmt.Println("cyrlm", version)
	case "help", "--help", "-h":
		if len(args) > 0 && (args[0] == "--all" || args[0] == "all") {
			fmt.Print(usageAll)
		} else {
			fmt.Print(usage)
		}
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// parseInterspersed parses flags that may appear after positional args.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// common flags shared by predict/serve/try.
type common struct {
	runtime, quant, harness, device, kv, backend string
	ctx                                          int
	ram, vram                                    float64
	tuned, defaults, probe, jsonOut, verbose     bool
	tunedSet                                     bool
}

func (c *common) register(fs *flag.FlagSet, defaultRuntime string) {
	fs.StringVar(&c.runtime, "runtime", defaultRuntime, "runtime id or 'all'")
	fs.StringVar(&c.quant, "quant", "", "quantization")
	fs.StringVar(&c.harness, "harness", "", "harness id")
	fs.StringVar(&c.device, "device", "", "what-if device name")
	fs.StringVar(&c.kv, "kv", "", "KV cache type: f16 | q8_0 | q4_0")
	fs.StringVar(&c.backend, "backend", "", "force backend (cuda, metal, vulkan, rocm, sycl, cpu)")
	fs.IntVar(&c.ctx, "ctx", 0, "context window")
	fs.Float64Var(&c.ram, "ram", 0, "RAM in GB for --device")
	fs.Float64Var(&c.vram, "vram", 0, "VRAM in GB for --device")
	fs.BoolVar(&c.tuned, "tuned", true, "use cyrlm's recommended settings (default)")
	fs.BoolVar(&c.defaults, "defaults", false, "simulate the runtime's out-of-the-box settings instead")
	fs.BoolVar(&c.probe, "probe", false, "measure memory bandwidth (CPU-only accuracy)")
	fs.BoolVar(&c.jsonOut, "json", false, "JSON output")
	fs.BoolVar(&c.verbose, "verbose", false, "show low-level details and assumptions")
}

// finish applies flag interactions after parsing.
func (c *common) finish(fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "tuned" || f.Name == "defaults" {
			c.tunedSet = true
		}
	})
	if c.defaults {
		c.tuned = false
	}
}

func (c *common) target() (predict.Target, *hw.System, error) {
	if c.device != "" {
		sys, err := predict.SyntheticSystem(c.device, c.ram, c.vram)
		if err != nil {
			return predict.Target{}, nil, err
		}
		t := predict.TargetFromSystem(sys, 0)
		t.Synthetic = true
		t.Name = "what-if: " + t.Name
		return t, sys, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	sys, err := hw.Detect(ctx)
	if err != nil && sys == nil {
		return predict.Target{}, nil, err
	}
	bw := 0.0
	if c.probe {
		bw, _ = hw.MeasureMemoryBandwidth(context.Background(), 1500*time.Millisecond)
	}
	return predict.TargetFromSystem(sys, bw), sys, nil
}

func lookupModel(name string) (catalog.Model, error) {
	if isGGUFRef(name) {
		return modelFromGGUF(name)
	}
	m, ok := catalog.Lookup(name)
	if ok {
		return m, nil
	}
	var names []string
	for _, m := range catalog.All() {
		names = append(names, m.OllamaTag)
	}
	return catalog.Model{}, fmt.Errorf("%q is not in the catalog. Known models: %s\n(For other models, `cyrlm inspect <gguf url>` reads exact metadata; catalog additions welcome.)", name, strings.Join(names, ", "))
}

func scenarioFor(h string) (predict.Scenario, harness.Harness, error) {
	hh, err := harness.Get(h)
	if err != nil {
		return predict.Scenario{}, hh, err
	}
	if h == "" {
		hh, _ = harness.Get("claude-code")
	}
	return hh.Scenario, hh, nil
}

// ---------------------------------------------------------------- detect

func cmdDetect(args []string) error {
	fs := flag.NewFlagSet("detect", flag.ExitOnError)
	var c common
	c.register(fs, "")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	c.finish(fs)
	t, sys, err := c.target()
	if err != nil {
		return err
	}
	var rts []hw.InstalledRuntime
	if c.device == "" {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		rts = hw.DetectRuntimes(ctx)
		cancel()
	}
	if c.jsonOut {
		return printJSON(map[string]any{"system": sys, "target": t, "runtimes": rts})
	}
	printDetect(sys, t, rts)
	return nil
}

// ---------------------------------------------------------------- models

func cmdModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	ms := catalog.All()
	if *jsonOut {
		return printJSON(ms)
	}
	printModels(ms)
	return nil
}

func cmdRuntimes(args []string) error {
	fs := flag.NewFlagSet("runtimes", flag.ExitOnError)
	jsonOut := fs.Bool("json", false, "JSON output")
	if _, err := parseInterspersed(fs, args); err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(runtime.All())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	printRuntimes(runtime.All(), hw.DetectRuntimes(ctx))
	return nil
}

// ---------------------------------------------------------------- predict

func predictAll(c common, m catalog.Model, t predict.Target, sc predict.Scenario, tunedModes []bool) ([]predict.Result, error) {
	var profiles []runtime.Profile
	if c.runtime == "" || c.runtime == "all" {
		profiles = runtime.All()
	} else {
		p, err := runtime.Get(c.runtime)
		if err != nil {
			return nil, err
		}
		profiles = []runtime.Profile{p}
	}
	var out []predict.Result
	for _, p := range profiles {
		for _, tuned := range tunedModes {
			r := predict.Predict(predict.Input{
				Model: m, QuantName: c.quant, Runtime: p, Target: t, Tuned: tuned,
				CtxOverride: c.ctx, KVCacheType: c.kv, Scenario: sc, Calibrated: c.probe && t.CPUOnly,
				BackendOverride: c.backend,
			})
			if r.Unsupported != "" && (c.runtime == "" || c.runtime == "all") {
				continue // runtimes that can't run this model here are simply not listed
			}
			out = append(out, r)
		}
	}
	return out, nil
}

func cmdPredict(args []string) error {
	fs := flag.NewFlagSet("predict", flag.ExitOnError)
	var c common
	c.register(fs, "all")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if len(pos) != 1 {
		return errors.New("usage: cyrlm predict <model> [flags]  (see `cyrlm models`)")
	}
	m, err := lookupModel(pos[0])
	if err != nil {
		return err
	}
	sc, _, err := scenarioFor(c.harness)
	if err != nil {
		return err
	}
	t, _, err := c.target()
	if err != nil {
		return err
	}
	modes := []bool{false, true}
	if c.tunedSet {
		modes = []bool{c.tuned}
	}
	results, err := predictAll(c, m, t, sc, modes)
	if err != nil {
		return err
	}
	if c.jsonOut {
		return printJSON(jsonResults(results))
	}
	inst := detectInstalled(t)
	_, hh, _ := scenarioFor(c.harness)
	printPrediction(m, t, sc, results, inst)
	printRecommendation(results, inst, hh)
	fmt.Println()
	return nil
}

// ---------------------------------------------------------------- serve / try

func cmdServe(args []string, try bool) error {
	name := "serve"
	if try {
		name = "try"
	}
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	var c common
	c.register(fs, "")
	up := fs.String("upstream", "auto", "auto | cyrlm | openrouter | mock | base URL")
	upModel := fs.String("upstream-model", "", "upstream model id")
	keyEnv := fs.String("key-env", "", "env var with the upstream API key")
	provider := fs.String("provider", "", "OpenRouter provider slug(s) to pin")
	port := fs.Int("port", 11434, "listen port")
	host := fs.String("host", "127.0.0.1", "listen address")
	launch := fs.Bool("launch", false, "launch the harness (try)")
	allowOrigin := fs.String("allow-origin", "", "extra allowed browser origins (comma-separated)")
	force := fs.Bool("force", false, "serve even if the model does not fit")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})
	if len(pos) != 1 {
		return fmt.Errorf("usage: cyrlm %s <model> [flags]", name)
	}
	if try && c.harness == "" {
		return errors.New("try needs --harness (claude-code, codex, opencode, aider, cline, continue, zed, qwen-code)")
	}
	m, err := lookupModel(pos[0])
	if err != nil {
		return err
	}
	sc, hh, err := scenarioFor(c.harness)
	if err != nil {
		return err
	}
	t, _, err := c.target()
	if err != nil {
		return err
	}

	// Runtime: explicit, else an installed one that supports this machine,
	// else the first profile that does.
	prof, why, err := chooseRuntime(c, t, m, sc, hh)
	if err != nil {
		return err
	}
	res := predict.Predict(predict.Input{
		Model: m, QuantName: c.quant, Runtime: prof, Target: t, Tuned: c.tuned,
		CtxOverride: c.ctx, KVCacheType: c.kv, Scenario: sc, BackendOverride: c.backend,
	})
	if res.Unsupported != "" {
		return errors.New(res.Unsupported)
	}
	if res.Memory.Level == predict.FitNo && !*force {
		return fmt.Errorf("%s does not fit on %s with %s: needs %.1f GB, %.1f GB available%s\n(use --force to simulate anyway, a smaller --quant, or a smaller --ctx)",
			m.Name, t.Name, prof.Name, res.Memory.TotalBytes/(1<<30), res.Memory.BudgetBytes/(1<<30), suffix("; ", res.Memory.Fix))
	}
	// Upstream first, so a missing key is the first thing a user sees.
	client, upDesc, err := buildUpstream(*up, *upModel, *keyEnv, *provider, m)
	if err != nil {
		return err
	}
	if try {
		printPrediction(m, t, sc, []predict.Result{res}, installed{})
	}

	dev := sim.NewDevice(sim.Config{
		RuntimeName: prof.Name, RuntimeID: prof.ID, Settings: res.Settings, Timing: res.Timing, Model: m,
		Render: tok.RenderOptions{StripPastReasoning: true},
	})
	var origins []string
	if *allowOrigin != "" {
		origins = strings.Split(*allowOrigin, ",")
	}
	logW := os.Stdout
	var logPath string
	if *launch {
		dir := filepath.Join(os.TempDir(), "cyrlm")
		_ = os.MkdirAll(dir, 0o755)
		logPath = filepath.Join(dir, fmt.Sprintf("session-%s.log", time.Now().Format("20060102-150405")))
		f, err := os.Create(logPath)
		if err != nil {
			return err
		}
		defer f.Close()
		logW = f
	}
	srv := server.New(server.Config{
		Device: dev, Upstream: client, DeviceName: t.Name, RuntimeName: prof.Name, Log: logW, AllowOrigins: origins,
		Model: api.ModelInfo{
			Name: m.OllamaTag, Family: m.Family, ParameterSize: m.ParamLabel(), Quantization: res.Quant.Name,
			ContextLength: res.Settings.ContextTokens, SizeBytes: res.Quant.FileBytes, Capabilities: capabilities(m),
			ModifiedAt: time.Now(),
		},
	})
	bound, err := srv.Listen(*host, *port, !portSet)
	if err != nil {
		return fmt.Errorf("cannot listen on %s:%d: %w", *host, *port, err)
	}
	printServeBanner(m, t, prof, why, res, upDesc, bound, *port, c.verbose, !try)
	if c.harness != "" {
		setup := hh.SetupFor(harness.Options{Port: bound, Model: m.OllamaTag, Tuned: c.tuned, ContextTokens: res.Settings.ContextTokens})
		fmt.Printf("── Connect %s ─────────────────────────────────────────\n\n%s\n", hh.Name, setup.Render())
		if *launch {
			go func() { _ = srv.Serve() }()
			fmt.Printf("Launching %s… (simulation log: %s)\n\n", hh.Name, logPath)
			lerr := setup.Launch()
			_ = srv.Shutdown(context.Background())
			fmt.Println()
			fmt.Print(srv.Summary())
			return lerr
		}
	}
	stop := fmt.Sprintf("kill %d", os.Getpid())
	if goruntime.GOOS == "windows" {
		stop = fmt.Sprintf("Stop-Process -Id %d", os.Getpid())
	}
	fmt.Printf("Running (pid %d). Stop with Ctrl-C, or from another terminal: %s — the session summary is printed on exit.\n", os.Getpid(), stop)
	fmt.Println()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()
	select {
	case <-sigs:
	case err := <-errc:
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	fmt.Println()
	fmt.Print(srv.Summary())
	return nil
}

func nonEmptyStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func suffix(sep, s string) string {
	if s == "" {
		return ""
	}
	return sep + s
}

func capabilities(m catalog.Model) []string {
	c := []string{"completion"}
	if m.Tools {
		c = append(c, "tools")
	}
	if m.Thinking {
		c = append(c, "thinking")
	}
	return c
}

func buildUpstream(kind, model, keyEnv, provider string, m catalog.Model) (upstream.Client, string, error) {
	acct := account.Load()
	if kind == "auto" {
		switch {
		case acct.CanShare():
			kind = "cyrlm"
		case os.Getenv(nonEmptyStr(keyEnv, "OPENROUTER_API_KEY")) != "" || acct.OpenRouterKey != "":
			kind = "openrouter"
		default:
			return nil, "", errors.New("the trial runs the real model in the cloud and needs your OpenRouter key: run `cyrlm login` yourself in a terminal (never paste the key into a chat). Get a key at https://openrouter.ai/keys. For an offline timing demo, use --upstream mock")
		}
	}
	switch kind {
	case "mock":
		c := &upstream.Mock{}
		return c, c.Describe(), nil
	case "cyrlm":
		if !acct.CanShare() {
			return nil, "", errors.New("--upstream cyrlm needs `cyrlm login` and a configured account server")
		}
		if model == "" {
			model = nonEmptyStr(m.OpenRouterID, m.ID)
		}
		c := &upstream.OpenAICompat{BaseURL: strings.TrimRight(acct.APIURL, "/") + "/v1", APIKey: acct.Key, Model: model, Reasoning: m.Thinking}
		return c, "your cyrlm account (" + model + ")", nil
	}
	base := kind
	if kind == "openrouter" || kind == "" {
		base = upstream.OpenRouterBaseURL
	}
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		return nil, "", fmt.Errorf("--upstream must be openrouter, mock or an http(s) base URL, got %q", kind)
	}
	if model == "" {
		model = m.OpenRouterID
		if base != upstream.OpenRouterBaseURL {
			return nil, "", errors.New("--upstream-model is required with a custom upstream URL")
		}
		if model == "" {
			return nil, "", errors.New("no cloud model is known for this file: pass --upstream-model <openrouter id> (e.g. its base model)")
		}
	}
	if keyEnv == "" {
		keyEnv = "OPENROUTER_API_KEY"
		if base != upstream.OpenRouterBaseURL {
			keyEnv = "CYRLM_UPSTREAM_API_KEY"
		}
	}
	key := os.Getenv(keyEnv)
	if key == "" && base == upstream.OpenRouterBaseURL {
		key = acct.OpenRouterKey
	}
	if key == "" && base == upstream.OpenRouterBaseURL {
		return nil, "", fmt.Errorf("no OpenRouter key: run `cyrlm login` yourself in a terminal (or set %s), or use --upstream mock for an offline timing demo", keyEnv)
	}
	extra := map[string]any{}
	if base == upstream.OpenRouterBaseURL && provider != "" {
		extra["provider"] = map[string]any{"only": strings.Split(provider, ","), "allow_fallbacks": false}
	}
	c := &upstream.OpenAICompat{BaseURL: base, APIKey: key, Model: model, Extra: extra, Reasoning: m.Thinking}
	return c, c.Describe(), nil
}

// ---------------------------------------------------------------- helpers

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type jsonResult struct {
	Runtime          string   `json:"runtime"`
	Tuned            bool     `json:"tuned"`
	Backend          string   `json:"backend"`
	Quant            string   `json:"quant"`
	DownloadGB       float64  `json:"download_gb"`
	ContextTokens    int      `json:"context_tokens"`
	MemoryGB         float64  `json:"memory_gb"`
	BudgetGB         float64  `json:"budget_gb"`
	Fit              string   `json:"fit"`
	DecodeTPS        float64  `json:"decode_tps"`
	DecodeTPSDeep    float64  `json:"decode_tps_deep"`
	PrefillTPS       float64  `json:"prefill_tps"`
	ColdFirstReplyS  float64  `json:"cold_first_reply_s"`
	FollowUpTTFTS    float64  `json:"follow_up_ttft_s"`
	FollowUpTurnS    float64  `json:"follow_up_turn_s"`
	SessionS         float64  `json:"session_s"`
	CloudSessionS    float64  `json:"cloud_session_s"`
	DecodeAccuracy   int      `json:"decode_accuracy_pct"`
	PrefillAccuracy  int      `json:"prefill_accuracy_pct"`
	NoAccuracyClaim  bool     `json:"no_accuracy_claim"`
	Warnings         []string `json:"warnings"`
	Assumptions      []string `json:"assumptions"`
	Unsupported      string   `json:"unsupported,omitempty"`
	TuneSteps        []string `json:"tune_steps,omitempty"`
	ContextReasoning string   `json:"context_note"`
}

func jsonResults(rs []predict.Result) []jsonResult {
	out := make([]jsonResult, 0, len(rs))
	for _, r := range rs {
		j := jsonResult{Runtime: r.Runtime.ID, Tuned: r.Settings.Tuned, Backend: r.Backend, Unsupported: r.Unsupported}
		if r.Unsupported == "" {
			j.Quant, j.DownloadGB = r.Quant.Name, float64(r.Quant.FileBytes)/1e9
			j.ContextTokens, j.ContextReasoning = r.Settings.ContextTokens, r.Settings.ContextNote
			j.MemoryGB, j.BudgetGB, j.Fit = r.Memory.TotalBytes/1e9, r.Memory.BudgetBytes/1e9, r.Memory.Level
			j.DecodeTPS, j.DecodeTPSDeep, j.PrefillTPS = r.DecodeTPS0, r.DecodeTPSDeep, r.PrefillTPSP0
			j.ColdFirstReplyS = r.Scenario.ColdFirstReply.Seconds()
			j.FollowUpTTFTS = r.Scenario.FollowUpTTFT.Seconds()
			j.FollowUpTurnS = r.Scenario.FollowUpTurn.Seconds()
			j.SessionS, j.CloudSessionS = r.Scenario.Session.Seconds(), r.Scenario.CloudSession.Seconds()
			j.DecodeAccuracy, j.PrefillAccuracy, j.NoAccuracyClaim = r.Accuracy.DecodePct, r.Accuracy.PrefillPct, r.Accuracy.NoClaim
			j.Warnings, j.Assumptions, j.TuneSteps = r.Warnings, r.Settings.Assumptions, r.TuneSteps
		}
		out = append(out, j)
	}
	sort.SliceStable(out, func(i, k int) bool { return out[i].Runtime < out[k].Runtime })
	return out
}
