// Command trialm predicts and simulates how open-weight models would run on
// your own machine, and serves a paced local endpoint so you can try a model
// inside your coding harness before downloading it.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/account"
	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/runtime"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/sim"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/terminal"
	"github.com/aeroknight786/trialm/internal/tok"
	"github.com/aeroknight786/trialm/internal/trial"
	"github.com/aeroknight786/trialm/internal/ui/tui"
	windowui "github.com/aeroknight786/trialm/internal/ui/window"
	"github.com/aeroknight786/trialm/internal/upstream"
)

var version = "0.2.1-dev"

const usage = `triaLM — try a local coding model before you download it

  trialm find [model]        ranked models for this machine
  trialm try [model]         a private, paced cloud trial
  trialm setup <model>       review a local setup plan (execution in v0.5)
  trialm launch [tool]       daily launch (arrives in v0.5)
  trialm key                 OpenRouter sign-in

Flags: --plain --json. More: trialm help advanced
`
const usageAll = `trialm — try a local model before you download it

Usage:
  trialm update                                install the latest sha256-checked release
  trialm data                                  catalog data (arrives in v0.7)
  trialm calibrate [--quick|--status] [--json]  optional machine calibration
  trialm detect   [--probe] [--json]           what's in this machine and which runtimes are installed
  trialm models                                 models in the catalog
  trialm runtimes                               runtime profiles (Ollama, llama.cpp, LM Studio, MLX)
  trialm predict  <model> [flags]               will it fit, how fast, on every runtime
  trialm serve    <model> [flags]               run the simulated local runtime (Ollama-compatible)
  trialm try      <model> [--harness h] [flags] predict + serve + connect your harness
  trialm inspect  <file.gguf | URL>             read a GGUF header (no full download)
  trialm recommend [--harness h]                best models + runtimes for this machine
  trialm plan     <model> --runtime r [flags]   exact setup steps (for you or your coding agent)
  trialm verify   --runtime r --model m         measure your real setup against the prediction (+ --harness: tool-call check)
  trialm bridge   [--endpoint URL] --harness h  translate a coding tool's API to a local runtime's Chat Completions
  trialm bg --log F --ready URL -- cmd …        start a server detached and wait until it answers
  trialm prompt   [--harness h]                 the message to paste into your coding agent
  trialm login | logout | config               compatibility aliases (use trialm key)
  trialm share                                  opt-in sharing of results (needs an account server)
  trialm version

Let your coding agent do it all:  trialm prompt --harness claude-code   (paste the output into it)

Common flags:
  --runtime <id>     ollama | llamacpp | lmstudio | lmstudio-mlx | mlx-lm (predict: default all)
  --defaults         simulate the runtime's out-of-the-box settings (default: triaLM's tuned settings)
  --ctx <n>          context window override
  --quant <q>        quantization (e.g. Q4_K_M, Q8_0)
  --harness <h>      claude-code | codex | opencode | aider | cline | continue | zed | qwen-code
  --device <name>    what-if: simulate another machine, e.g. "RTX 4090", "Apple M4 Pro 20c"
  --ram <GB> --vram <GB>   memory for --device

Serve/try flags:
  --upstream <u>     auto (your triaLM account if logged in, else OpenRouter) | trialm | openrouter | mock | URL
  --upstream-model   cloud model id (default: catalog's OpenRouter id)
  --key-env <VAR>    env var holding the upstream API key (default OPENROUTER_API_KEY)
  --provider <slug>  OpenRouter: pin provider(s), comma-separated
  --port <n>         listen port (default 11434, falls back to 11435+ if busy)
  --launch           (try) start the harness in this terminal; logs go to a file
  --background       (try/serve/bridge) run detached, return once it answers; log in ~/.trialm/logs

Examples:
  trialm predict gpt-oss:20b
  trialm predict qwen3-coder:30b --device "Apple M4 Pro 20c" --ram 48
  trialm try gpt-oss:20b --harness claude-code --runtime ollama
  trialm serve qwen3-coder:30b --runtime llamacpp --upstream mock
`

// migrateHome moves a cyrlm installation (~/.cyrlm and the old config
// directory) into ~/.trialm once, and says so on stderr.
func migrateHome() {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return
	}
	oldConfig := ""
	if base, err := os.UserConfigDir(); err == nil {
		oldConfig = filepath.Join(base, "cyrlm")
	}
	m, err := paths.Migrate(userHome, oldConfig)
	if err != nil {
		fmt.Fprintln(os.Stderr, "warning: could not move your cyrlm files to ~/.trialm:", err)
		return
	}
	if m.Did() {
		fmt.Fprintln(os.Stderr, "cyrlm is now triaLM: your key, setups and results moved to ~/.trialm (~/.cyrlm still points there).")
	}
}

// trialmHome is ~/.trialm (or $TRIALM_HOME).
func trialmHome() string {
	h, err := paths.Home()
	if err != nil {
		return filepath.Join(os.TempDir(), "trialm")
	}
	return h
}

// parseInterspersed parses flags that may appear after positional args.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil, err
			}
			return nil, &exitError{2, err.Error()}
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
	prefs                                        store.Preferences
	prefsErr                                     error
	minTPS, maxTurn, minBits                     float64
	runtime, quant, harness, device, kv, backend string
	ctx                                          int
	ram, vram                                    float64
	tuned, defaults, probe, jsonOut, verbose     bool
	tunedSet                                     bool
	// harnessAuto is set when the harness was auto-detected rather than
	// given; a recipe number from the last listing then supplies it.
	harnessAuto bool
	// recipeRef names the listing entry ("#1") that set runtime and quant.
	recipeRef string
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
	fs.BoolVar(&c.tuned, "tuned", true, "use triaLM's recommended settings (default)")
	fs.BoolVar(&c.defaults, "defaults", false, "simulate the runtime's out-of-the-box settings instead")
	fs.BoolVar(&c.probe, "probe", false, "measure memory bandwidth (CPU-only accuracy)")
	fs.BoolVar(&c.jsonOut, "json", false, "JSON output")
	fs.BoolVar(&c.verbose, "verbose", false, "show low-level details and assumptions")
	// New requirement flags are aliases of the same values used by the old CLI.
	c.prefs, c.prefsErr = store.Default().Preferences()
	if currentVerb == "find" || currentVerb == "try" || currentVerb == "setup" {
		c.harness = c.prefs.Tool
		c.ctx = c.prefs.Context
		c.device = c.prefs.Hardware
	}
	fs.StringVar(&c.harness, "for", c.harness, "coding tool")
	fs.StringVar(&c.device, "as", c.device, "what-if device")
	fs.StringVar(&c.prefs.Model, "model", c.prefs.Model, "model family filter")
	fs.StringVar(&c.prefs.Use, "use", c.prefs.Use, "agent | chat")
	fs.StringVar(&c.prefs.Priority, "want", c.prefs.Priority, "balanced | speed | quality | long")
	fs.StringVar(&c.prefs.Priority, "priority", c.prefs.Priority, "ranking priority")
	fs.StringVar(&c.prefs.ContextVia, "context-via", c.prefs.ContextVia, "native | yarn")
	fs.StringVar(&c.prefs.ContextVia, "yarn", c.prefs.ContextVia, "opt-in YaRN (arrives in v0.6)")
	fs.StringVar(&c.prefs.QualityFloor, "quality-floor", c.prefs.QualityFloor, "3bit-ok | 4bit+")
	fs.Float64Var(&c.prefs.MaxDownloadGB, "max-gb", c.prefs.MaxDownloadGB, "download cap in GB")
	fs.StringVar(&c.prefs.Engines, "engines", c.prefs.Engines, "stable+new | installed | stable")
	fs.StringVar(&c.prefs.Boosts, "boosts", c.prefs.Boosts, "off | auto | everything")
	fs.StringVar(&c.prefs.Privacy, "privacy", c.prefs.Privacy, "repo | playground | predict-only")
	fs.Float64Var(&c.minTPS, "min-tps", 0, "minimum writing speed")
	fs.Float64Var(&c.maxTurn, "max-turn", 0, "maximum follow-up seconds")
	fs.Float64Var(&c.minBits, "min-bits", 0, "minimum file bits per weight")

}

// finish applies flag interactions after parsing.
func (c *common) finish(fs *flag.FlagSet) {
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "tuned" || f.Name == "defaults" {
			c.tunedSet = true
		}
	})
	if c.prefs.Boosts == "off" {
		c.tuned = false
	}
	if c.defaults {
		c.tuned = false
	}
	switch fs.Name() {
	case "try", "serve", "recommend", "find", "setup", "plan":
		if c.harness == "" {
			h, note := harness.DetectInstalled()
			c.harness, c.harnessAuto = h.ID, true
			if note != "" {
				fmt.Fprintln(os.Stderr, note)
			}
		}
	}
}

func (c *common) target() (predict.Target, *hw.System, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	d, err := (core.Session{}).Detect(ctx, c.options())
	return d.Target, d.System, err
}

func lookupModel(name string) (catalog.Model, error) {
	if core.IsGGUFRef(name) {
		return core.ModelFromGGUF(name)
	}
	m, ok := catalog.Lookup(name)
	if ok {
		return m, nil
	}
	var names []string
	for _, m := range catalog.All() {
		names = append(names, m.OllamaTag)
	}
	return catalog.Model{}, fmt.Errorf("%q is not in the catalog. Known models: %s\n(For other models, `trialm inspect <gguf url>` reads exact metadata; catalog additions welcome.)", name, strings.Join(names, ", "))
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
	fs := flag.NewFlagSet("detect", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
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
	fs := flag.NewFlagSet("runtimes", flag.ContinueOnError)
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

func predictAll(c common, m catalog.Model, t predict.Target, sc predict.Scenario, modes []bool) ([]predict.Result, error) {
	return core.PredictAll(c.options(), m, t, sc, modes)
}
func chooseRuntime(c common, t predict.Target, m catalog.Model, sc predict.Scenario, h harness.Harness) (runtime.Profile, string, error) {
	p, why, err := core.ChooseRuntime(c.options(), t, m, sc, h)
	if err == nil && c.recipeRef != "" {
		why = "recipe " + c.recipeRef + " from your last `trialm find`"
	}
	return p, why, err
}
func (c common) options() core.Options {
	return core.Options{Runtime: c.runtime, Quant: c.quant, Context: c.ctx, KV: c.kv, Backend: c.backend, Probe: c.probe, Tuned: c.tuned, Device: c.device, RAM: c.ram, VRAM: c.vram, Engines: c.prefs.Engines, Boosts: c.prefs.Boosts, QualityFloor: c.prefs.QualityFloor, Use: c.prefs.Use, Priority: c.prefs.Priority, MaxDownloadGB: c.prefs.MaxDownloadGB, MinTPS: c.minTPS, MaxTurn: c.maxTurn, MinBits: c.minBits}
}

func cmdPredict(args []string) error {
	fs := flag.NewFlagSet("predict", flag.ContinueOnError)
	var c common
	c.register(fs, "all")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if len(pos) != 1 {
		return errors.New("usage: trialm predict <model> [flags]  (see `trialm models`)")
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
	fmt.Fprintln(out())
	return nil
}

// ---------------------------------------------------------------- serve / try

func cmdServe(args []string, try bool) error {
	name := "serve"
	if try {
		name = "try"
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var c common
	c.register(fs, "")
	up := fs.String("upstream", "auto", "auto | trialm | openrouter | mock | base URL")
	upModel := fs.String("upstream-model", "", "upstream model id")
	keyEnv := fs.String("key-env", "", "env var with the upstream API key")
	provider := fs.String("provider", "", "OpenRouter provider slug(s) to pin")
	defaultPort := 11434
	if try {
		defaultPort = 11535
	}
	port := fs.Int("port", defaultPort, "listen port")
	host := fs.String("host", "127.0.0.1", "listen address")
	launch := fs.Bool("launch", false, "launch the harness (try)")
	logFile := fs.String("log", "", "write request logs to this file (also works in the foreground)")
	allowOrigin := fs.String("allow-origin", "", "extra allowed browser origins (comma-separated)")
	force := fs.Bool("force", false, "serve even if the model does not fit")
	zdr := fs.Bool("zdr", c.prefs.ZDR, "require zero data retention")
	budget := fs.Float64("budget", c.prefs.BudgetUSD, "per-trial USD cap")
	pace := fs.String("pace", "p50", "p50 | p90 local pacing")
	serve := fs.Bool("serve", false, "serve in the foreground without launching a tool")
	here := fs.Bool("here", false, "launch in this terminal")
	window := fs.Bool("window", false, "open a trial in another terminal")
	playground := fs.Bool("playground", false, "use a scratch working directory")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if *pace != "p50" && *pace != "p90" {
		return &exitError{2, "--pace must be p50|p90"}
	}
	if *budget <= 0 || math.IsNaN(*budget) || math.IsInf(*budget, 0) {
		return &exitError{2, "--budget must be a finite positive USD amount"}
	}

	explicitWindow := *window
	if try && !*here && !*serve && !*launch && paths.Getenv("DETACHED") == "" && autoWindow() {
		*window = true
	}
	if *here {
		*launch = true
	}
	chat := try && c.prefs.Use == "chat" && !*serve
	if chat && !terminal.IsTTY(os.Stdin) && (!*window || !explicitWindow && jsonMode) {
		return needs("--serve or --window", "chat needs a terminal")
	}
	if *serve && *launch {
		return &exitError{2, "choose --serve or --launch"}
	}
	if try && !*window && !*serve && !*launch && selectedMode == Interactive && terminal.IsTTY(os.Stdin) && terminal.IsTTY(os.Stdout) {
		*launch = true
	}
	portSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "port" {
			portSet = true
		}
	})
	if err := c.validateRequirements(); err != nil {
		return err
	}
	if len(pos) == 0 && (!try || currentVerb != "try") {
		return fmt.Errorf("usage: trialm %s <model> [flags]", name)
	}
	if try && len(pos) == 0 {
		sc, hh, e := scenarioFor(c.harness)
		if e != nil {
			return e
		}
		t, _, e := c.target()
		if e != nil {
			return e
		}
		listing, e := (core.Session{}).Find(commandContext, c.options(), t, sc, hh, c.prefs.Model, 1)
		if c.prefs.Model == "any" {
			listing, e = (core.Session{}).Find(commandContext, c.options(), t, sc, hh, "", 1)
		}
		if e != nil {
			return e
		}
		if len(listing.Rows) == 0 {
			return &exitError{4, "no working trial recipe; use trialm find"}
		}
		if e = saveListing(listing.Rows, hh.ID); e != nil {
			return e
		}
	}
	modelName, err := resolveRecipe(pos, &c)
	if err != nil {
		return err
	}
	m, err := lookupModel(modelName)
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

	prepared, err := (core.Session{}).Try(context.Background(), c.options(), m, t, sc, hh, *force)
	if err != nil {
		return err
	}
	res, why := prepared.Result, prepared.Why
	explicitLaunch := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "launch" {
			explicitLaunch = true
		}
	})
	if try && selectedMode == Interactive && !explicitLaunch && !*here && !*serve && paths.Getenv("DETACHED") == "" {
		return runTrialTUI(commandContext, c, res, args, *window)
	}
	prof := res.Runtime
	// Upstream first, so a missing key is the first thing a user sees.
	if c.prefs.Privacy == "predict-only" {
		printPrediction(m, t, sc, []predict.Result{res}, installed{})
		return emit("result", map[string]any{"prediction": jsonResults([]predict.Result{res}), "privacy": "predict-only"}, core.Next{Cmd: "trialm setup " + m.OllamaTag + " --dry-run --json", Why: "review local setup"})
	}
	client, upDesc, err := buildUpstream(*up, *upModel, *keyEnv, *provider, m)
	if err != nil {
		return err
	}
	fidelity := "offline timing demo · no model"
	var spending *trial.Budget
	if uc, ok := client.(*upstream.OpenAICompat); ok {
		fidelity = "custom endpoint · precision and privacy unverified"
		if *zdr && uc.BaseURL != upstream.OpenRouterBaseURL {
			return &exitError{4, "--zdr needs OpenRouter endpoint policy metadata; use --upstream openrouter"}
		}
		if uc.Model != m.OpenRouterID {
			m.UpstreamUnverified = true
			m.TrialStandIn = ""
		}
		if uc.BaseURL == upstream.OpenRouterBaseURL {
			endpoints, e := (openrouter.Client{Key: uc.APIKey}).PrivateEndpoints(commandContext, uc.Model, *zdr)
			if e != nil {
				return e
			}
			endpoint, e := trial.Select(endpoints, trial.Policy{ZDR: *zdr, Tools: c.prefs.Use != "chat", Context: res.Settings.ContextTokens, Provider: *provider, Quant: res.Quant.Name})
			if e != nil {
				return needs("--privacy predict-only", e.Error())
			}
			uc.Extra = map[string]any{"provider": trial.Provider(endpoint, *zdr)}
			rates, e := trial.Prices(endpoint.Pricing)
			if e != nil {
				return e
			}
			spending = trial.NewBudget(client, rates, *budget)
			client = spending
			fidelity = trial.Fidelity(m, res.Quant, endpoint)
			if e = privacyNotice(); e != nil {
				return e
			}
		}
	}
	fmt.Fprintln(out(), "Trial fidelity:", fidelity)
	if *pace == "p90" {
		res.Timing = trial.Pessimistic(res.Timing, res.Accuracy.DecodePct, res.Accuracy.PrefillPct)
		res.Scenario = predict.RunScenario(res.Timing, sc, res.Settings.ContextTokens)
		res.DecodeTPS0 = res.Timing.DecodeTPS(512)
		res.DecodeTPSDeep = res.Timing.DecodeTPS(res.Scenario.DecodeDepth)
		res.PrefillTPS2k = res.Timing.PrefillTPS(0, 2048)
		res.PrefillTPSP0 = res.Timing.PrefillTPS(0, sc.BasePromptTokens)
		fmt.Fprintln(out(), "Pacing: p90 · pessimistic band edge.")
	}
	if try && currentVerb == "try" && paths.Getenv("DETACHED") == "" && !*here && !*serve && !*launch && (*window || autoWindow()) {
		return runWindow(commandContext, windowArgs(args), out())
	}
	if *window && !*here && !*serve {
		return runWindow(commandContext, windowArgs(args), out())
	}
	if try && currentVerb == "try" && !*launch && !*serve && !chat && paths.Getenv("DETACHED") == "" {
		return needs("--serve or --here", "a trial needs a terminal or foreground server mode")
	}
	if *playground || c.prefs.Privacy == "playground" {
		dir := filepath.Join(trialmHome(), "playground")
		if err = os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		old, e := os.Getwd()
		if e != nil {
			return e
		}
		if e = os.Chdir(dir); e != nil {
			return e
		}
		defer os.Chdir(old)
	}
	if try {
		printPrediction(m, t, sc, []predict.Result{res}, installed{})
	}

	dev := sim.NewDevice(sim.Config{
		RuntimeName: prof.Name, RuntimeID: prof.ID, Settings: res.Settings, Timing: res.Timing, Model: m, Memory: res.Memory,
		Render: tok.RenderOptions{StripPastReasoning: true},
	})
	var origins []string
	if *allowOrigin != "" {
		origins = strings.Split(*allowOrigin, ",")
	}
	logW := out()
	var logPath string
	if *launch || *logFile != "" {
		logPath = launchLogPath(*logFile, time.Now())
		if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(logPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		logW = f
	}
	trialID := os.Getenv("TRIALM_RUN_ID")
	if trialID == "" {
		trialID = strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	trialAt := time.Now()
	srv := server.New(server.Config{
		TrialSummary: func(records []sim.Record) string {
			spend := -1.0
			if _, mock := client.(*upstream.Mock); mock {
				spend = 0
			}
			if spending != nil {
				spend = spending.Spent()
			}
			report := trial.Summarize(records, spend, fidelity)
			_ = store.Default().SaveTrial(store.TrialEntry{ID: trialID, At: trialAt, Model: m.OllamaTag, Quant: res.Quant.Name, Runtime: prof.ID, Band: max(res.Accuracy.DecodePct, res.Accuracy.PrefillPct), Report: report})
			if jsonMode {
				_ = emit("result", report.EventData(max(res.Accuracy.DecodePct, res.Accuracy.PrefillPct)), core.Next{Cmd: "trialm setup " + m.OllamaTag + " --dry-run --json", Why: "review local setup"})
			}
			return report.Plain(max(res.Accuracy.DecodePct, res.Accuracy.PrefillPct))
		},
		Device: dev, Upstream: client, DeviceName: t.Name, RuntimeName: prof.Name, Log: logW, AllowOrigins: origins,
		Model: api.ModelInfo{
			Name: m.OllamaTag + "-trialm", Family: m.Family, ParameterSize: m.ParamLabel(), Quantization: res.Quant.Name,
			ContextLength: res.Settings.ContextTokens, SizeBytes: res.Quant.FileBytes, Capabilities: capabilities(m),
			ModifiedAt: time.Now(),
		},
	})
	meterCtx, meterCancel := context.WithCancel(commandContext)
	defer meterCancel()
	windowui.Track(meterCtx, store.Default(), os.Getenv("TRIALM_RUN_ID"), max(res.Accuracy.DecodePct, res.Accuracy.PrefillPct), func() trial.Report {
		spend := -1.0
		if spending != nil {
			spend = spending.Spent()
		}
		return trial.Summarize(dev.Stats().Snapshot(), spend, fidelity)
	})
	bound, err := srv.Listen(*host, *port, !portSet)
	if err != nil {
		return fmt.Errorf("cannot listen on %s:%d: %w", *host, *port, err)
	}
	printServeBanner(m, t, prof, why, res, upDesc, bound, *port, c.verbose, !try)
	if jsonMode {
		if err = emit("progress", map[string]any{"ready": true, "endpoint": fmt.Sprintf("http://127.0.0.1:%d", bound), "fidelity": fidelity}); err != nil {
			return err
		}
	}
	if chat {
		go func() { _ = srv.Serve() }()
		err = tui.RunChat(commandContext, &upstream.OpenAICompat{BaseURL: fmt.Sprintf("http://127.0.0.1:%d/v1", bound), Model: m.OllamaTag + "-trialm"})
		_ = srv.Shutdown(context.Background())
		fmt.Fprintln(out(), srv.Summary())
		return err
	}
	if c.harness != "" {
		setup := hh.SetupFor(harness.Options{Port: bound, Model: m.OllamaTag + "-trialm", Tuned: c.tuned, ContextTokens: res.Settings.ContextTokens, Protocol: hh.ProtocolFor(prof.APIs)})
		if err := setup.WriteFiles(); err != nil {
			_ = srv.Shutdown(context.Background())
			return err
		}
		fmt.Fprintf(out(), "── Connect %s ─────────────────────────────────────────\n\n%s\n", hh.Name, setup.Render())
		if *launch {
			go func() { _ = srv.Serve() }()
			fmt.Fprintf(out(), "Launching %s… (simulation log: %s)\n\n", hh.Name, logPath)
			lerr := setup.Launch()
			_ = srv.Shutdown(context.Background())
			fmt.Fprintln(out())
			fmt.Fprintln(out(), srv.Summary())
			return lerr
		}
	}
	printRunning(" — the session summary is printed on exit.")
	return serveUntilSignal(srv)
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
		case os.Getenv(nonEmptyStr(keyEnv, "OPENROUTER_API_KEY")) != "" || store.Default().Key() != "":
			kind = "openrouter"
		default:
			return nil, "", needs("trialm key or --upstream mock", "the trial needs your OpenRouter key; never paste it into a chat")
		}
	}
	switch kind {
	case "mock":
		c := &upstream.Mock{}
		return c, c.Describe(), nil
	case "trialm":
		if !acct.CanShare() {
			return nil, "", errors.New("--upstream trialm needs `trialm login` and a configured account server")
		}
		if model == "" {
			model = nonEmptyStr(m.OpenRouterID, m.ID)
		}
		c := &upstream.OpenAICompat{BaseURL: strings.TrimRight(acct.APIURL, "/") + "/v1", APIKey: acct.Key, Model: model, Reasoning: m.Thinking}
		return c, "your triaLM account (" + model + ")", nil
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
			keyEnv = "TRIALM_UPSTREAM_API_KEY"
		}
	}
	key := paths.LookupEnv(keyEnv)
	if key == "" && base == upstream.OpenRouterBaseURL {
		key = store.Default().Key()
	}
	if key == "" && base == upstream.OpenRouterBaseURL {
		return nil, "", needs("trialm key or --key-env "+keyEnv, "no OpenRouter key")
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
	if jsonMode {
		kind := "result"
		switch currentVerb {
		case "find":
			kind = "recipes"
		case "setup":
			kind = "plan"
		}
		if err := emit(kind, v); err != nil {
			return err
		}
		next := []core.Next{{Cmd: "trialm find --json", Why: "find another model"}}
		if currentVerb == "find" {
			next = []core.Next{{Cmd: "trialm try 1", Why: "try the top recipe"}, {Cmd: "trialm setup 1 --dry-run --json", Why: "review local setup"}}
		}
		return emit("result", map[string]any{"ok": true}, next...)
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

type jsonResult struct {
	Kind             string          `json:"kind"`
	VerifySeconds    map[int]float64 `json:"verify_seconds,omitempty"`
	Runtime          string          `json:"runtime"`
	Tuned            bool            `json:"tuned"`
	Backend          string          `json:"backend"`
	Quant            string          `json:"quant"`
	DownloadGB       float64         `json:"download_gb"`
	ContextTokens    int             `json:"context_tokens"`
	MemoryGB         float64         `json:"memory_gb"`
	BudgetGB         float64         `json:"budget_gb"`
	Fit              string          `json:"fit"`
	DecodeTPS        float64         `json:"decode_tps"`
	DecodeTPSDeep    float64         `json:"decode_tps_deep"`
	PrefillTPS       float64         `json:"prefill_tps"`
	ColdFirstReplyS  float64         `json:"cold_first_reply_s"`
	FollowUpTTFTS    float64         `json:"follow_up_ttft_s"`
	FollowUpTurnS    float64         `json:"follow_up_turn_s"`
	SessionS         float64         `json:"session_s"`
	CloudSessionS    float64         `json:"cloud_session_s"`
	DecodeAccuracy   int             `json:"decode_error_range_pct"` // ± error covering ~3 in 4 predictions
	PrefillAccuracy  int             `json:"prefill_error_range_pct"`
	NoAccuracyClaim  bool            `json:"no_speed_claim"`
	Warnings         []string        `json:"warnings"`
	Assumptions      []string        `json:"assumptions"`
	Unsupported      string          `json:"unsupported,omitempty"`
	TuneSteps        []string        `json:"tune_steps,omitempty"`
	ContextReasoning string          `json:"context_note"`
}

func jsonResults(rs []predict.Result) []jsonResult {
	out := make([]jsonResult, 0, len(rs))
	for _, r := range rs {
		j := jsonResult{Kind: r.Kind, Runtime: r.Runtime.ID, Tuned: r.Settings.Tuned, Backend: r.Backend, Unsupported: r.Unsupported}
		if r.Unsupported == "" {
			j.VerifySeconds = r.Timing.VerifySeconds
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

// launchLogPath keeps launch logs with triaLM's other logs unless overridden.
func launchLogPath(explicit string, now time.Time) string {
	if explicit != "" {
		return explicit
	}
	return filepath.Join(trialmHome(), "logs", "session-"+now.Format("20060102-150405")+".log")
}
