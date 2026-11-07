// Package setup turns a prediction (model, quant, runtime, settings,
// harness) into an ordered, reviewable setup plan for the user's machine:
// install the runtime, download the exact quantization, apply the tuned
// settings, start the server, connect the harness and verify.
//
// The package only describes steps; it never executes anything. A coding
// agent runs `trialm plan ... --json`, shows each step to the user and runs
// it after approval, then runs the final `trialm verify` step.
//
// Commands are written for the plan's OS and shell (POSIX sh on macOS and
// Linux, PowerShell on Windows). Each Command runs on its own, in order;
// a multi-line Run is one script. Files are edits the agent applies with
// its file tools ("~" is the user's home). Steps without commands or files
// are manual: their Why says what to do. Confidence says how sure we are
// that the commands match current runtime versions; Docs link the sources.
package setup

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/runtime"
)

// Command is one shell command.
type Command struct {
	OS    string `json:"os"`    // "darwin" | "linux" | "windows" | "any"
	Shell string `json:"shell"` // "sh" | "powershell"
	Run   string `json:"run"`
}

// FileEdit is a file to create or merge into.
type FileEdit struct {
	Path    string `json:"path"`    // with ~ for home, e.g. "~/.codex/config.toml"
	Format  string `json:"format"`  // "toml" | "json" | "yaml" | "modelfile" | "text"
	Content string `json:"content"` // snippet to add/merge
	Merge   string `json:"merge"`   // "create" (new file) | "merge" (merge keys into existing; back up first) | "append"
	Note    string `json:"note,omitempty"`
}

// Step is one reviewable unit of work.
type Step struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind"` // check | install | download | configure | service | start | harness | verify
	Title         string     `json:"title"`
	Why           string     `json:"why,omitempty"`
	Commands      []Command  `json:"commands,omitempty"`
	Files         []FileEdit `json:"files,omitempty"`
	DownloadBytes int64      `json:"download_bytes,omitempty"`
	NeedsAdmin    bool       `json:"needs_admin,omitempty"`
	Restart       string     `json:"restart,omitempty"` // what must be restarted afterwards
	Optional      bool       `json:"optional,omitempty"`
	Undo          []Command  `json:"undo,omitempty"`
	Confidence    string     `json:"confidence"` // high|medium|low: how sure we are the command is right for current versions
	Docs          []string   `json:"docs,omitempty"`
}

// Plan is the full setup plan.
type Plan struct {
	Model          string `json:"model"`
	ModelName      string `json:"model_name"`
	Quant          string `json:"quant"`
	Runtime        string `json:"runtime"`
	RuntimeName    string `json:"runtime_name"`
	Backend        string `json:"backend"`
	Harness        string `json:"harness,omitempty"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	Tuned          bool   `json:"tuned"`
	ContextTokens  int    `json:"context_tokens"`
	KVCacheType    string `json:"kv_cache_type"`
	FlashAttention bool   `json:"flash_attention"`
	KeepAliveS     int    `json:"keep_alive_s"` // -1 never
	// Endpoint is where the runtime will serve, e.g. http://127.0.0.1:11434.
	Endpoint string `json:"endpoint"`
	// LocalModelName is the name the harness should use with the real runtime.
	LocalModelName    string   `json:"local_model_name"`
	DownloadBytes     int64    `json:"download_bytes"`
	HarnessCompatible bool     `json:"harness_compatible"`
	HarnessNote       string   `json:"harness_note,omitempty"`
	Steps             []Step   `json:"steps"`
	Warnings          []string `json:"warnings,omitempty"`
}

// Input is what Build needs. Settings are the resolved runtime settings
// (predict.Result.Settings); a zero Settings means the runtime's defaults.
type Input struct {
	Model    catalog.Model
	Quant    catalog.Quant
	Profile  runtime.Profile
	Settings runtime.Settings
	Backend  string           // cuda|metal|vulkan|rocm|sycl|cpu|mlx
	Harness  *harness.Harness // nil = no harness step
	OS, Arch string
	// Installed is true when the runtime is already installed.
	Installed        bool
	InstalledVersion string
	Running          bool
	// Port overrides the runtime's usual port (the caller picks a free
	// one when something else already uses it). 0 = the usual port.
	Port int
	// OwnedFileExists reports whether a file exists under the user's home
	// (rel is home-relative, e.g. ".trialm/ollama/x.Modelfile"). When a
	// setup made by cyrlm exists for this model and runtime, the plan keeps
	// its "-cyrlm" model name, so the runtime does not create a second copy.
	// nil = no such check.
	OwnedFileExists func(rel string) bool
}

// Step kinds, in plan order.
const (
	KindCheck     = "check"
	KindInstall   = "install"
	KindDownload  = "download"
	KindConfigure = "configure"
	KindService   = "service"
	KindStart     = "start"
	KindHarness   = "harness"
	KindVerify    = "verify"
)

// KindOrder lists step kinds in the order they appear in a plan.
var KindOrder = []string{KindCheck, KindInstall, KindDownload, KindConfigure, KindService, KindStart, KindHarness, KindVerify}

// Confidence levels.
const (
	High   = "high"
	Medium = "medium"
	Low    = "low"
)

// Build produces the setup plan. It fails when the runtime cannot run on
// the target OS/arch or cannot load the requested quantization.
func Build(in Input) (Plan, error) {
	p := in.Profile
	if p.ID == "" {
		return Plan{}, errors.New("setup: no runtime profile")
	}
	if in.OS == "" {
		return Plan{}, errors.New("setup: target OS is required")
	}
	if in.Model.ID == "" {
		return Plan{}, errors.New("setup: no model")
	}
	if len(p.Backends(in.OS, in.Arch)) == 0 {
		return Plan{}, fmt.Errorf("%s does not run on %s/%s", p.Name, in.OS, in.Arch)
	}
	if in.Quant.Name == "" {
		q, ok := quantFor(in.Model, p.Formats)
		if !ok {
			return Plan{}, fmt.Errorf("%s has no %s quantization for %s", in.Model.Name, strings.Join(p.Formats, "/"), p.Name)
		}
		in.Quant = q
	}
	if !has(p.Formats, in.Quant.Format) {
		return Plan{}, fmt.Errorf("%s loads %s models, but %s %s is %s", p.Name, strings.Join(p.Formats, "/"), in.Model.Name, in.Quant.Name, in.Quant.Format)
	}
	if in.Settings.ContextTokens <= 0 {
		s := p.Resolve(runtime.ResolveInput{NativeCtx: in.Model.NativeCtx, Tuned: in.Settings.Tuned})
		s.Tuned = in.Settings.Tuned
		in.Settings = s
	}
	b := newBuilder(in)
	var err error
	switch p.ID {
	case "ollama":
		err = b.ollama()
	case "llamacpp":
		err = b.llamacpp()
	case "lmstudio", "lmstudio-mlx":
		err = b.lmstudio()
	case "mlx-lm":
		err = b.mlxlm()
	default:
		err = fmt.Errorf("setup: no setup recipe for runtime %q", p.ID)
	}
	if err != nil {
		return Plan{}, err
	}
	b.harnessSteps()
	b.verifyStep()
	b.finishWarnings()
	return *b.plan, nil
}

type builder struct {
	in    Input
	p     runtime.Profile
	plan  *Plan
	win   bool
	shell string
	port  int
	// fileName is the local model name ("<tag>-trialm") made safe for file
	// names; used for Modelfiles, launch scripts and logs.
	fileName string
}

func newBuilder(in Input) *builder {
	p := in.Profile
	win := in.OS == "windows"
	shell := "sh"
	if win {
		shell = "powershell"
	}
	port := in.Port
	if port == 0 {
		port = p.Port
	}
	if port == 0 {
		port = defaultPorts[p.ID]
	}
	backend := in.Backend
	plat := p.Backends(in.OS, in.Arch)
	if backend == "" {
		backend = plat[0]
		for _, pref := range p.BackendPreference {
			if has(plat, pref) {
				backend = pref
				break
			}
		}
	}
	kv := strings.ToLower(in.Settings.KVCacheType)
	if kv == "" {
		kv = "f16"
	}
	plan := &Plan{
		Model:          in.Model.ID,
		ModelName:      in.Model.Name,
		Quant:          in.Quant.Name,
		Runtime:        p.ID,
		RuntimeName:    p.Name,
		Backend:        backend,
		OS:             in.OS,
		Arch:           in.Arch,
		Tuned:          in.Settings.Tuned,
		ContextTokens:  in.Settings.ContextTokens,
		KVCacheType:    kv,
		FlashAttention: in.Settings.FlashAttention,
		KeepAliveS:     keepAliveSeconds(in.Settings.KeepAlive),
		Endpoint:       fmt.Sprintf("http://127.0.0.1:%d", port),
		DownloadBytes:  in.Quant.FileBytes,
		Steps:          []Step{},
	}
	b := &builder{in: in, p: p, plan: plan, win: win, shell: shell, port: port}
	if !has(plat, backend) {
		b.warn(fmt.Sprintf("Backend %s is not one %s supports on %s (supported: %s); the commands assume the runtime picks a working backend.", backend, p.Name, runtime.PlatformKey(in.OS, in.Arch), strings.Join(plat, ", ")))
	}
	// mlx_lm.server resolves the request's model name as a repo path, so
	// it must be the repo id; the other runtimes take any name.
	if p.ID == "mlx-lm" {
		plan.LocalModelName = mlxRepo(in.Model, in.Quant)
	} else {
		plan.LocalModelName = b.localName()
	}
	b.fileName = safeFile(b.localName())
	return b
}

// legacySuffix is the local model suffix setups made by cyrlm used.
const legacySuffix = "-cyrlm"

// localName is localName(model, quant), except that an existing cyrlm
// setup for this model and runtime keeps its "-cyrlm" name.
func (b *builder) localName() string {
	name := localName(b.in.Model, b.in.Quant)
	if b.in.OwnedFileExists == nil {
		return name
	}
	legacy := strings.TrimSuffix(name, localSuffix) + legacySuffix
	f := safeFile(legacy)
	for _, rel := range []string{"ollama/" + f + ".Modelfile", "llamacpp/" + f + ".sh", "llamacpp/" + f + ".ps1"} {
		if b.in.OwnedFileExists(".trialm/" + rel) {
			return legacy
		}
	}
	return name
}

// DefaultPort is the port a plan uses for runtime id unless told otherwise.
func DefaultPort(p runtime.Profile) int {
	if p.Port != 0 {
		return p.Port
	}
	return defaultPorts[p.ID]
}

// bgStart is the command that starts a launch script detached, logged and
// checked: `trialm bg` returns only once healthURL answers, so the server
// survives the agent's command runner and a printed pid is not mistaken
// for a running server.
func (b *builder) bgStart(scriptRel, logRel, healthURL string) Command {
	if b.win {
		return b.os("trialm bg --log " + b.home(logRel) + " --ready " + healthURL + " -- powershell -NoProfile -ExecutionPolicy Bypass -File " + b.home(scriptRel))
	}
	return b.os("trialm bg --log " + b.home(logRel) + " --ready " + healthURL + " -- sh " + b.home(scriptRel))
}

var defaultPorts = map[string]int{"ollama": 11434, "llamacpp": 8080, "lmstudio": 1234, "lmstudio-mlx": 1234, "mlx-lm": 8080}

func quantFor(m catalog.Model, formats []string) (catalog.Quant, bool) {
	for _, f := range formats {
		if q, ok := m.QuantFor(f); ok {
			return q, true
		}
	}
	return catalog.Quant{}, false
}

// localName is the model name the runtime serves and the harness requests:
// the Ollama tag (or catalog id) plus the quant when it is not the
// default, suffixed "-trialm", e.g. "qwen3-coder:30b-trialm" or
// "qwen3-coder:30b-q8_0-trialm". It has no slashes.
func localName(m catalog.Model, q catalog.Quant) string {
	base := m.OllamaTag
	if base == "" {
		base = m.ID
	}
	if len(m.Quants) > 0 && !strings.EqualFold(m.Quants[0].Name, q.Name) {
		base += "-" + strings.ToLower(q.Name)
	}
	var sb strings.Builder
	for _, r := range strings.ToLower(base) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-', r == ':':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	return sb.String() + localSuffix
}

// localSuffix marks model names that triaLM's setups created.
const localSuffix = "-trialm"

func safeFile(s string) string {
	return strings.NewReplacer(":", "-", "/", "-", "\\", "-", " ", "-").Replace(s)
}

func keepAliveSeconds(d time.Duration) int {
	if d < 0 {
		return -1
	}
	return int(d / time.Second)
}

// ---------------------------------------------------------------- helpers

func (b *builder) add(s Step) {
	if s.Confidence == "" {
		s.Confidence = Medium
	}
	b.plan.Steps = append(b.plan.Steps, s)
}

func (b *builder) warn(w string) {
	for _, x := range b.plan.Warnings {
		if x == w {
			return
		}
	}
	b.plan.Warnings = append(b.plan.Warnings, w)
}

// os returns a command for the plan's OS.
func (b *builder) os(run string) Command {
	return Command{OS: b.in.OS, Shell: b.shell, Run: run}
}

// any returns an OS-independent command (same text on every OS) in the
// plan's shell.
func (b *builder) any(run string) Command {
	return Command{OS: "any", Shell: b.shell, Run: run}
}

// home returns a quoted path under the home directory for use in commands.
func (b *builder) home(rel string) string {
	if b.win {
		return `"$HOME\` + strings.ReplaceAll(rel, "/", `\`) + `"`
	}
	return `"$HOME/` + rel + `"`
}

// q quotes an argument for the plan's shell.
func (b *builder) q(s string) string {
	if b.win {
		return psQuote(s)
	}
	return shQuote(s)
}

// args joins arguments, quoting each for the plan's shell.
func (b *builder) args(a ...string) string {
	out := make([]string, len(a))
	for i, s := range a {
		out[i] = b.q(s)
	}
	return strings.Join(out, " ")
}

// curl is the curl invocation that works in the plan's shell (PowerShell 5
// aliases curl to Invoke-WebRequest).
func (b *builder) curl() string {
	if b.win {
		return "curl.exe"
	}
	return "curl"
}

func shQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:@%+=,-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func psQuote(s string) string {
	if s == "" {
		return "''"
	}
	safe := true
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("_./:\\=-", r)) {
			safe = false
			break
		}
	}
	if safe {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// shortName is a runtime's name without the engine note, e.g. "LM Studio".
func shortName(p runtime.Profile) string {
	if i := strings.Index(p.Name, " ("); i > 0 {
		return p.Name[:i]
	}
	return p.Name
}

func has(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func gbString(n int64) string {
	return fmt.Sprintf("%.1f GB", float64(n)/1e9)
}

// winArch is the architecture name used by Windows/Linux release assets.
func winArch(goarch string) string {
	if goarch == "arm64" {
		return "arm64"
	}
	return "x64"
}

// outputTokens is a generous per-response limit for harness/server configs.
func outputTokens(ctx int) int {
	n := ctx / 4
	if n < 4096 {
		n = 4096
	}
	if n > 32768 {
		n = 32768
	}
	return n
}

// installedNote describes an existing install for check/install steps.
func (b *builder) installedNote() string {
	switch {
	case b.in.Installed && b.in.InstalledVersion != "":
		return fmt.Sprintf("triaLM found %s %s installed", b.p.Name, b.in.InstalledVersion)
	case b.in.Installed:
		return fmt.Sprintf("triaLM found %s installed", b.p.Name)
	}
	return fmt.Sprintf("triaLM did not find %s", b.p.Name)
}

// checkStep is the first step: what is installed and running, disk space.
func (b *builder) checkStep(cmds []Command, docs ...string) {
	if h := b.in.Harness; h != nil && h.Binary != "" {
		cmds = append(cmds, b.any(h.Binary+" --version"))
	}
	if b.win {
		cmds = append(cmds, b.os("Get-PSDrive -Name ($HOME.Substring(0,1)) | Select-Object Used,Free"))
	} else {
		cmds = append(cmds, b.os(`df -h "$HOME"`))
	}
	why := b.installedNote()
	if b.in.Running {
		why += fmt.Sprintf(" and its server running on port %d", b.port)
	}
	why += ". These read-only commands confirm what is installed and running, and that there is room for the " + gbString(b.in.Quant.FileBytes) + " download. A failing version command means the runtime is not installed or not on PATH."
	b.add(Step{
		ID: "check", Kind: KindCheck,
		Title:      "Check what is already installed",
		Why:        why,
		Commands:   cmds,
		Confidence: High,
		Docs:       docs,
	})
}

// markInstalled turns an install step into an optional one when the
// runtime is already present.
func (b *builder) markInstalled(s *Step) {
	if !b.in.Installed {
		return
	}
	s.Optional = true
	s.Title = "Already installed: " + s.Title
	s.Why = b.installedNote() + "; skip this step, or run it to upgrade. " + s.Why
}

// verifyStep is always last.
func (b *builder) verifyStep() {
	pl := b.plan
	args := []string{"--runtime", pl.Runtime, "--endpoint", pl.Endpoint, "--model", pl.LocalModelName,
		"--catalog-model", pl.Model, "--quant", pl.Quant, "--ctx", fmt.Sprint(pl.ContextTokens)}
	if pl.KVCacheType != "" && pl.KVCacheType != "f16" {
		args = append(args, "--kv", pl.KVCacheType)
	}
	if pl.Harness != "" {
		args = append(args, "--harness", pl.Harness)
	}
	run := "trialm verify " + b.args(args...)
	b.add(Step{
		ID: "verify", Kind: KindVerify,
		Title:      "Verify the setup",
		Why:        verifyWhy(pl),
		Commands:   []Command{b.any(run)},
		Confidence: High,
	})
}

func (b *builder) finishWarnings() {
	pl := b.plan
	var admin []string
	for _, s := range pl.Steps {
		if s.NeedsAdmin {
			admin = append(admin, s.Title)
		}
	}
	if len(admin) > 0 {
		b.warn("These steps need administrator rights (sudo): " + strings.Join(admin, "; ") + ".")
	}
	if pl.DownloadBytes > 0 {
		w := fmt.Sprintf("The model download is %s; keep at least %s of disk space free.", gbString(pl.DownloadBytes), gbString(pl.DownloadBytes*11/10+2e9))
		if b.in.Quant.Estimated {
			w += " (Size estimated from bits per weight.)"
		}
		b.warn(w)
	}
	if b.in.Harness != nil && !b.in.Model.Tools {
		b.warn(b.in.Model.Name + " has no native tool calling in common local runtimes; coding agents that need tools may fail.")
	}
	if pl.KVCacheType != "f16" && !pl.FlashAttention && b.p.Engine == "llamacpp" {
		b.warn("A quantized KV cache (" + pl.KVCacheType + ") needs flash attention, which these settings leave off.")
	}
	out := pl.Warnings[:0]
	for _, w := range pl.Warnings {
		if strings.TrimSpace(w) != "" {
			out = append(out, w)
		}
	}
	pl.Warnings = out
}

// kindRank returns the position of a kind in KindOrder (-1 if unknown).
func kindRank(k string) int {
	for i, x := range KindOrder {
		if x == k {
			return i
		}
	}
	return -1
}

// Ordered reports whether a plan's steps follow KindOrder.
func (p Plan) Ordered() bool {
	last := 0
	for _, s := range p.Steps {
		r := kindRank(s.Kind)
		if r < last {
			return false
		}
		last = r
	}
	return true
}

// pkill stops processes whose command line contains pattern.
func (b *builder) pkill(pattern string) string {
	if b.win {
		return "Get-CimInstance Win32_Process | Where-Object { $_.CommandLine -like '*" + pattern + "*' } | ForEach-Object { Stop-Process -Id $_.ProcessId }"
	}
	return "pkill -f " + shQuote(pattern)
}

func verifyWhy(pl *Plan) string {
	w := fmt.Sprintf("Checks that %s answers at %s with %s, that the context window is %d tokens and compares real speed with triaLM's prediction.", pl.RuntimeName, pl.Endpoint, pl.LocalModelName, pl.ContextTokens)
	if pl.Harness != "" {
		w += " It then sends requests in " + pl.Harness + "'s own format (a greeting, a tool call and the tool result) and reports \"Ready\" only if all three work: a fast runtime can still reject a coding tool's requests. The setup is not done until this says Ready."
	}
	return w
}
