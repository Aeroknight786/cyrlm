package setup

import (
	"encoding/json"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/aeroknight786/cyrlm/internal/catalog"
	"github.com/aeroknight786/cyrlm/internal/harness"
	"github.com/aeroknight786/cyrlm/internal/runtime"
)

// ---------------------------------------------------------------- fixtures

func model(t *testing.T, id string) catalog.Model {
	t.Helper()
	m, ok := catalog.Lookup(id)
	if !ok {
		t.Fatalf("catalog has no %s", id)
	}
	return m
}

func profile(t *testing.T, id string) runtime.Profile {
	t.Helper()
	p, err := runtime.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func harnessPtr(t *testing.T, id string) *harness.Harness {
	t.Helper()
	if id == "" {
		return nil
	}
	h, err := harness.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return &h
}

type spec struct {
	model, quant, runtime string
	goos, goarch, backend string
	tuned                 bool
	kv                    string // override KV cache type ("" = runtime's)
	ctx                   int    // override context (0 = resolved)
	harness               string
	installed, running    bool
}

func (s spec) input(t *testing.T) Input {
	t.Helper()
	m := model(t, s.model)
	p := profile(t, s.runtime)
	q, err := m.Quant(s.quant)
	if err != nil {
		t.Fatal(err)
	}
	set := p.Resolve(runtime.ResolveInput{GPUMemGiB: 64, NativeCtx: m.NativeCtx, Tuned: s.tuned, CtxOverride: s.ctx})
	if s.kv != "" {
		set.KVCacheType = s.kv
	}
	return Input{
		Model: m, Quant: q, Profile: p, Settings: set, Backend: s.backend,
		Harness: harnessPtr(t, s.harness), OS: s.goos, Arch: s.goarch,
		Installed: s.installed, InstalledVersion: map[bool]string{true: "1.2.3"}[s.installed], Running: s.running,
	}
}

func build(t *testing.T, in Input) Plan {
	t.Helper()
	p, err := Build(in)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	checkInvariants(t, in, p)
	return p
}

// ---------------------------------------------------------------- helpers

func stepByID(p Plan, id string) (Step, bool) {
	for _, s := range p.Steps {
		if s.ID == id {
			return s, true
		}
	}
	return Step{}, false
}

func mustStep(t *testing.T, p Plan, id string) Step {
	t.Helper()
	s, ok := stepByID(p, id)
	if !ok {
		t.Fatalf("no step %q in %v", id, stepIDs(p))
	}
	return s
}

func stepIDs(p Plan) []string {
	var ids []string
	for _, s := range p.Steps {
		ids = append(ids, s.ID)
	}
	return ids
}

func stepsOfKind(p Plan, kind string) []Step {
	var out []Step
	for _, s := range p.Steps {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	return out
}

func runs(s Step) string {
	var b strings.Builder
	for _, c := range s.Commands {
		b.WriteString(c.Run)
		b.WriteString("\n")
	}
	return b.String()
}

func allRuns(p Plan) string {
	var b strings.Builder
	for _, s := range p.Steps {
		b.WriteString(runs(s))
		for _, f := range s.Files {
			b.WriteString(f.Content)
		}
	}
	return b.String()
}

func file(t *testing.T, s Step, path string) FileEdit {
	t.Helper()
	for _, f := range s.Files {
		if f.Path == path {
			return f
		}
	}
	t.Fatalf("step %s has no file %s", s.ID, path)
	return FileEdit{}
}

func contains(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Errorf("%s does not contain %q:\n%s", what, sub, s)
		}
	}
}

func lacks(t *testing.T, what, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			t.Errorf("%s unexpectedly contains %q:\n%s", what, sub, s)
		}
	}
}

// checkInvariants holds for every plan.
func checkInvariants(t *testing.T, in Input, p Plan) {
	t.Helper()
	if !p.Ordered() {
		t.Errorf("steps out of order: %v", kinds(p))
	}
	if len(p.Steps) < 3 || p.Steps[0].Kind != KindCheck || p.Steps[len(p.Steps)-1].Kind != KindVerify {
		t.Errorf("plan must start with check and end with verify: %v", kinds(p))
	}
	shell := "sh"
	if in.OS == "windows" {
		shell = "powershell"
	}
	seen := map[string]bool{}
	var downloads []Step
	for _, s := range p.Steps {
		if seen[s.ID] {
			t.Errorf("duplicate step id %s", s.ID)
		}
		seen[s.ID] = true
		if s.Title == "" || s.ID == "" {
			t.Errorf("step without id/title: %+v", s)
		}
		if kindRank(s.Kind) < 0 {
			t.Errorf("step %s: unknown kind %q", s.ID, s.Kind)
		}
		switch s.Confidence {
		case High, Medium, Low:
		default:
			t.Errorf("step %s: confidence %q", s.ID, s.Confidence)
		}
		if len(s.Commands) == 0 && len(s.Files) == 0 && s.Why == "" {
			t.Errorf("step %s has nothing to do or say", s.ID)
		}
		for _, c := range append(append([]Command{}, s.Commands...), s.Undo...) {
			if c.OS != in.OS && c.OS != "any" {
				t.Errorf("step %s: command for %s in a %s plan: %s", s.ID, c.OS, in.OS, c.Run)
			}
			if c.Shell != shell {
				t.Errorf("step %s: shell %s in a %s plan", s.ID, c.Shell, in.OS)
			}
			if strings.TrimSpace(c.Run) == "" {
				t.Errorf("step %s: empty command", s.ID)
			}
		}
		for _, f := range s.Files {
			if !strings.HasPrefix(f.Path, "~/") {
				t.Errorf("step %s: file path %q should start with ~/", s.ID, f.Path)
			}
			switch f.Merge {
			case "create", "merge", "append":
			default:
				t.Errorf("step %s: merge %q", s.ID, f.Merge)
			}
			switch f.Format {
			case "toml", "json", "yaml", "modelfile", "text":
			default:
				t.Errorf("step %s: format %q", s.ID, f.Format)
			}
			if f.Format == "json" && !json.Valid([]byte(f.Content)) {
				t.Errorf("step %s: invalid JSON in %s:\n%s", s.ID, f.Path, f.Content)
			}
		}
		if s.Kind == KindDownload {
			downloads = append(downloads, s)
		}
	}
	if len(downloads) != 1 {
		t.Fatalf("want 1 download step, got %d", len(downloads))
	}
	q := in.Quant
	if q.Name == "" { // Build picked the default quant
		q, _ = in.Model.Quant(p.Quant)
	}
	if q.FileBytes == 0 || downloads[0].DownloadBytes != q.FileBytes || p.DownloadBytes != q.FileBytes {
		t.Errorf("download bytes: step %d plan %d, want %d", downloads[0].DownloadBytes, p.DownloadBytes, q.FileBytes)
	}
	v := runs(p.Steps[len(p.Steps)-1])
	contains(t, "verify command", v, "cyrlm verify", "--runtime "+p.Runtime, "--endpoint "+p.Endpoint,
		"--catalog-model "+p.Model, "--quant "+p.Quant, "--ctx "+itoa(p.ContextTokens))
	if !strings.Contains(v, "--model "+p.LocalModelName) && !strings.Contains(v, "--model '"+p.LocalModelName+"'") {
		t.Errorf("verify command lacks --model %s: %s", p.LocalModelName, v)
	}
	if p.Runtime != "mlx-lm" && strings.Contains(p.LocalModelName, "/") {
		t.Errorf("LocalModelName %q has a slash", p.LocalModelName)
	}
	if in.Harness == nil && len(stepsOfKind(p, KindHarness)) > 0 {
		t.Errorf("harness step without a harness")
	}
	if in.Harness != nil && p.HarnessCompatible != (len(stepsOfKind(p, KindHarness)) > 0) {
		t.Errorf("HarnessCompatible=%v but %d harness steps", p.HarnessCompatible, len(stepsOfKind(p, KindHarness)))
	}
	// JSON round trip.
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Plan
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p, back) {
		t.Errorf("JSON round trip changed the plan:\n%+v\n%+v", p, back)
	}
}

func kinds(p Plan) []string {
	var k []string
	for _, s := range p.Steps {
		k = append(k, s.Kind)
	}
	return k
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// assertKindSequence checks that want appears, in order, among the plan's
// step kinds.
func assertKindSequence(t *testing.T, p Plan, want ...string) {
	t.Helper()
	i := 0
	for _, k := range kinds(p) {
		if i < len(want) && k == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Errorf("kinds %v do not contain the sequence %v", kinds(p), want)
	}
}

// ---------------------------------------------------------------- tests

func TestOllamaDarwinTunedQ8ClaudeCode(t *testing.T) {
	in := spec{model: "qwen3-coder-30b-a3b", quant: "Q4_K_M", runtime: "ollama", goos: "darwin", goarch: "arm64",
		backend: "metal", tuned: true, kv: "q8_0", harness: "claude-code"}.input(t)
	p := build(t, in)

	assertKindSequence(t, p, KindCheck, KindInstall, KindDownload, KindConfigure, KindService, KindStart, KindHarness, KindVerify)
	if p.Endpoint != "http://127.0.0.1:11434" || p.LocalModelName != "qwen3-coder:30b-cyrlm" {
		t.Errorf("endpoint %s name %s", p.Endpoint, p.LocalModelName)
	}
	if !p.Tuned || p.ContextTokens != 65536 || p.KVCacheType != "q8_0" || p.KeepAliveS != -1 || !p.FlashAttention {
		t.Errorf("settings not carried into plan: %+v", p)
	}
	if mustStep(t, p, "install-ollama").Optional {
		t.Error("install should not be optional when Ollama is missing")
	}
	contains(t, "install", runs(mustStep(t, p, "install-ollama")), "curl -fsSL https://ollama.com/install.sh | sh", "open -a Ollama")
	contains(t, "download", runs(mustStep(t, p, "download-model")), "ollama pull hf.co/unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q4_K_M")

	cfg := mustStep(t, p, "configure-modelfile")
	mf := file(t, cfg, "~/.cyrlm/ollama/qwen3-coder-30b-cyrlm.Modelfile")
	if mf.Format != "modelfile" || mf.Merge != "create" {
		t.Errorf("modelfile edit: %+v", mf)
	}
	contains(t, "Modelfile", mf.Content, "FROM hf.co/unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q4_K_M\n", "PARAMETER num_ctx 65536\n")
	contains(t, "create", runs(cfg), `ollama create qwen3-coder:30b-cyrlm -f "$HOME/.cyrlm/ollama/qwen3-coder-30b-cyrlm.Modelfile"`)

	env := mustStep(t, p, "service-ollama-env")
	r := runs(env)
	contains(t, "env step", r, "launchctl setenv OLLAMA_KV_CACHE_TYPE q8_0", "launchctl setenv OLLAMA_KEEP_ALIVE -1", "open -a Ollama")
	// Flash attention is on by default in Ollama: no line for it.
	lacks(t, "env step", r, "OLLAMA_FLASH_ATTENTION", "OLLAMA_CONTEXT_LENGTH")
	if env.Restart == "" || len(env.Undo) == 0 {
		t.Errorf("env step needs restart and undo: %+v", env)
	}
	persist := mustStep(t, p, "service-ollama-env-persist")
	if !persist.Optional {
		t.Error("LaunchAgent step should be optional")
	}

	cc := mustStep(t, p, "harness-claude-code")
	f := file(t, cc, "~/.cyrlm/harness/claude-code-ollama.json")
	var settings struct {
		Env map[string]string `json:"env"`
	}
	if err := json.Unmarshal([]byte(f.Content), &settings); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"ANTHROPIC_BASE_URL":             "http://127.0.0.1:11434",
		"ANTHROPIC_MODEL":                "qwen3-coder:30b-cyrlm",
		"ANTHROPIC_DEFAULT_SONNET_MODEL": "qwen3-coder:30b-cyrlm",
		"CLAUDE_CODE_ATTRIBUTION_HEADER": "0",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS": "65536",
	}
	for k, v := range want {
		if settings.Env[k] != v {
			t.Errorf("settings env %s = %q, want %q", k, settings.Env[k], v)
		}
	}
	if _, ok := settings.Env["ANTHROPIC_API_KEY"]; ok {
		t.Error("empty ANTHROPIC_API_KEY must not be written to settings")
	}
	contains(t, "restart", cc.Restart, "claude --settings")
	def := mustStep(t, p, "harness-claude-code-default")
	if !def.Optional || file(t, def, "~/.claude/settings.json").Merge != "merge" {
		t.Errorf("global Claude Code settings step should be an optional merge: %+v", def)
	}
	if !p.HarnessCompatible || !strings.Contains(p.HarnessNote, "Anthropic") {
		t.Errorf("harness: %v %q", p.HarnessCompatible, p.HarnessNote)
	}
}

func TestOllamaEnvOnlyForNonDefaults(t *testing.T) {
	// Untuned: everything at Ollama's defaults, so no server settings step.
	p := build(t, spec{model: "qwen3-8b", runtime: "ollama", goos: "linux", goarch: "amd64", backend: "cuda"}.input(t))
	if len(stepsOfKind(p, KindService)) != 0 {
		t.Errorf("untuned plan should not touch the server environment: %v", stepIDs(p))
	}
	lacks(t, "untuned plan", allRuns(p), "OLLAMA_KV_CACHE_TYPE", "OLLAMA_KEEP_ALIVE", "OLLAMA_FLASH_ATTENTION")
	// The Modelfile is still written with the resolved context.
	contains(t, "Modelfile", file(t, mustStep(t, p, "configure-modelfile"), "~/.cyrlm/ollama/qwen3-8b-cyrlm.Modelfile").Content,
		"PARAMETER num_ctx "+itoa(p.ContextTokens))

	// Tuned with an f16 cache: only keep-alive differs (flash attention is
	// already on by default).
	p = build(t, spec{model: "qwen3-8b", runtime: "ollama", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true}.input(t))
	env := mustStep(t, p, "service-ollama-env")
	r := runs(env)
	contains(t, "systemd drop-in", r, `Environment="OLLAMA_KEEP_ALIVE=-1"`, "/etc/systemd/system/ollama.service.d/cyrlm.conf", "sudo systemctl daemon-reload", "sudo systemctl restart ollama")
	lacks(t, "systemd drop-in", r, "OLLAMA_KV_CACHE_TYPE", "OLLAMA_FLASH_ATTENTION")
	if !env.NeedsAdmin {
		t.Error("systemd drop-in needs admin")
	}

	// Flash attention turned off explicitly is a non-default.
	in := spec{model: "qwen3-8b", runtime: "ollama", goos: "linux", goarch: "amd64", backend: "cuda"}.input(t)
	in.Settings.FlashAttention = false
	p = build(t, in)
	contains(t, "env", runs(mustStep(t, p, "service-ollama-env")), `Environment="OLLAMA_FLASH_ATTENTION=0"`)
}

func TestOllamaWindows(t *testing.T) {
	p := build(t, spec{model: "gpt-oss-20b", runtime: "ollama", goos: "windows", goarch: "amd64", backend: "cuda",
		tuned: true, kv: "q8_0", harness: "codex", installed: true}.input(t))
	inst := mustStep(t, p, "install-ollama")
	if !inst.Optional || !strings.HasPrefix(inst.Title, "Already installed") {
		t.Errorf("install step should be marked when installed: %+v", inst)
	}
	contains(t, "install", runs(inst), "irm https://ollama.com/install.ps1 | iex")
	// Installed but not running: the download step starts the tray app first.
	contains(t, "download", runs(mustStep(t, p, "download-model")), "ollama app.exe", "ollama pull hf.co/ggml-org/gpt-oss-20b-GGUF:MXFP4")
	r := runs(mustStep(t, p, "service-ollama-env"))
	contains(t, "windows env", r,
		"[Environment]::SetEnvironmentVariable('OLLAMA_KV_CACHE_TYPE', 'q8_0', 'User')",
		"[Environment]::SetEnvironmentVariable('OLLAMA_KEEP_ALIVE', '-1', 'User')",
		"Stop-Process", "Start-Process")
	contains(t, "modelfile command", runs(mustStep(t, p, "configure-modelfile")), `"$HOME\.cyrlm\ollama\gpt-oss-20b-cyrlm.Modelfile"`)
	codex := file(t, mustStep(t, p, "harness-codex"), "~/.codex/config.toml")
	contains(t, "codex config", codex.Content, "[model_providers.cyrlm-ollama]", `base_url = "http://127.0.0.1:11434/v1"`,
		`wire_api = "responses"`, "[profiles.cyrlm-ollama]", `model = "gpt-oss:20b-cyrlm"`)
	if codex.Merge != "merge" {
		t.Errorf("codex config must be merged: %s", codex.Merge)
	}
	found := false
	for _, w := range p.Warnings {
		found = found || strings.Contains(w, "system tray")
	}
	if !found {
		t.Errorf("missing Windows restart warning: %v", p.Warnings)
	}
}

func TestLlamaCppLinuxCUDACodexIncompatible(t *testing.T) {
	in := spec{model: "qwen3-coder-30b-a3b", quant: "Q4_K_M", runtime: "llamacpp", goos: "linux", goarch: "amd64",
		backend: "cuda", tuned: true, harness: "codex"}.input(t)
	// Pin the APIs this test is about (the profile data may gain the
	// Responses API later; compatibility follows Profile.APIs).
	in.Profile.APIs = []string{"openai-chat", "anthropic"}
	p := build(t, in)
	if p.HarnessCompatible {
		t.Fatal("Codex (Responses API) must be incompatible with llama-server's Chat/Anthropic APIs")
	}
	contains(t, "harness note", p.HarnessNote, "Codex", "Responses API", "Ollama", "cyrlm serve")
	if len(stepsOfKind(p, KindHarness)) != 0 {
		t.Errorf("no harness step expected: %v", stepIDs(p))
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), p.HarnessNote) {
		t.Error("harness note should also be a warning")
	}
	assertKindSequence(t, p, KindCheck, KindInstall, KindDownload, KindConfigure, KindStart, KindVerify)
	if p.Endpoint != "http://127.0.0.1:8080" {
		t.Errorf("endpoint %s", p.Endpoint)
	}

	inst := runs(mustStep(t, p, "install-llamacpp"))
	contains(t, "linux install", inst, "api.github.com/repos/ggml-org/llama.cpp/releases", "bin-ubuntu-cuda-", "cudart-", "-x64")
	contains(t, "download", runs(mustStep(t, p, "download-model")), "llama-cli -hf unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q4_K_M")

	script := file(t, mustStep(t, p, "configure-launch-script"), "~/.cyrlm/llamacpp/qwen3-coder-30b-cyrlm.sh").Content
	contains(t, "launch script", script, "llama-server -hf unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF:Q4_K_M", "-c 65536", "-fa on", "--jinja",
		"-ngl 999", "--alias qwen3-coder:30b-cyrlm", "--port 8080")
	lacks(t, "launch script (f16 cache)", script, "-ctk", "-ctv")
	contains(t, "start", runs(mustStep(t, p, "start-server")), "nohup sh", "/health")

	// q8_0 KV cache adds the cache-type flags.
	in.Settings.KVCacheType = "q8_0"
	p = build(t, in)
	contains(t, "launch script (q8_0)", file(t, mustStep(t, p, "configure-launch-script"), "~/.cyrlm/llamacpp/qwen3-coder-30b-cyrlm.sh").Content, "-ctk q8_0 -ctv q8_0")

	// With the Responses API the same plan gets a Codex step on :8080.
	in.Profile.APIs = []string{"openai-chat", "anthropic", "responses"}
	p = build(t, in)
	contains(t, "codex config", file(t, mustStep(t, p, "harness-codex"), "~/.codex/config.toml").Content, `base_url = "http://127.0.0.1:8080/v1"`)
}

func TestLlamaCppInstallPerOS(t *testing.T) {
	cases := []struct {
		goos, goarch, backend string
		want                  []string
		conf                  string
	}{
		{"darwin", "arm64", "metal", []string{"brew install llama.cpp"}, High},
		{"windows", "amd64", "vulkan", []string{"winget install llama.cpp"}, High},
		{"windows", "amd64", "cuda", []string{"bin-win-cuda-", "cudart-", "Expand-Archive", "SetEnvironmentVariable('Path'"}, Medium},
		{"windows", "arm64", "cpu", []string{"winget install llama.cpp"}, High},
		{"linux", "amd64", "vulkan", []string{"bin-ubuntu-vulkan-x64"}, Medium},
		{"linux", "arm64", "cpu", []string{"bin-ubuntu-arm64"}, Medium},
	}
	for _, c := range cases {
		t.Run(c.goos+"/"+c.backend, func(t *testing.T) {
			p := build(t, spec{model: "qwen3-8b", runtime: "llamacpp", goos: c.goos, goarch: c.goarch, backend: c.backend, tuned: true}.input(t))
			s := mustStep(t, p, "install-llamacpp")
			contains(t, "install", runs(s), c.want...)
			if s.Confidence != c.conf {
				t.Errorf("confidence %s, want %s", s.Confidence, c.conf)
			}
			script := mustStep(t, p, "configure-launch-script").Files
			if c.goos == "windows" {
				contains(t, "ps1", script[0].Content, "& $exe -hf Qwen/Qwen3-8B-GGUF:Q4_K_M")
			}
			if c.backend == "cpu" {
				contains(t, "cpu launch", script[0].Content, "-ngl 0")
			}
		})
	}
}

func TestLMStudioWindowsOpenCode(t *testing.T) {
	in := spec{model: "qwen3-coder-30b-a3b", quant: "Q4_K_M", runtime: "lmstudio", goos: "windows", goarch: "amd64",
		backend: "cuda", tuned: true, kv: "q8_0", harness: "opencode"}.input(t)
	p := build(t, in)
	assertKindSequence(t, p, KindCheck, KindInstall, KindDownload, KindConfigure, KindService, KindStart, KindHarness, KindVerify)
	if p.Endpoint != "http://127.0.0.1:1234" {
		t.Errorf("endpoint %s", p.Endpoint)
	}
	contains(t, "install", runs(mustStep(t, p, "install-lmstudio")), "winget install --id ElementLabs.LMStudio")
	contains(t, "bootstrap", runs(mustStep(t, p, "install-lms-cli")), `& "$HOME\.lmstudio\bin\lms.exe" bootstrap`)
	contains(t, "download", runs(mustStep(t, p, "download-model")),
		"get 'https://huggingface.co/unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF@Q4_K_M' --gguf -y")
	manual := mustStep(t, p, "configure-load-settings")
	if len(manual.Commands) != 0 || manual.Confidence != Low {
		t.Errorf("load settings should be a manual low-confidence step: %+v", manual)
	}
	contains(t, "manual settings", manual.Why, "Flash Attention: On", "K Cache Quantization Type: Q8_0", "V Cache Quantization Type: Q8_0")
	contains(t, "server", runs(mustStep(t, p, "service-lms-server")), "server start --port 1234")
	load := runs(mustStep(t, p, "start-model"))
	contains(t, "load", load, "load qwen3-coder-30b-a3b-instruct --context-length 65536 --gpu max --identifier qwen3-coder:30b-cyrlm -y")
	lacks(t, "load", load, "--ttl")

	oc := file(t, mustStep(t, p, "harness-opencode"), "~/.config/opencode/opencode.json")
	var cfg struct {
		Provider map[string]struct {
			NPM     string `json:"npm"`
			Options struct {
				BaseURL string `json:"baseURL"`
			} `json:"options"`
			Models map[string]struct {
				Limit struct{ Context, Output int } `json:"limit"`
			} `json:"models"`
		} `json:"provider"`
	}
	if err := json.Unmarshal([]byte(oc.Content), &cfg); err != nil {
		t.Fatal(err)
	}
	prov := cfg.Provider["cyrlm-lmstudio"]
	if prov.Options.BaseURL != "http://127.0.0.1:1234/v1" || prov.NPM != "@ai-sdk/openai-compatible" {
		t.Errorf("opencode provider: %+v", prov)
	}
	if prov.Models["qwen3-coder:30b-cyrlm"].Limit.Context != 65536 {
		t.Errorf("opencode model limit: %+v", prov.Models)
	}

	// Untuned with an f16 cache: LM Studio's defaults need no manual step.
	p = build(t, spec{model: "qwen3-coder-30b-a3b", runtime: "lmstudio", goos: "windows", goarch: "amd64", backend: "cuda", harness: "opencode", installed: true}.input(t))
	if _, ok := stepByID(p, "configure-load-settings"); ok {
		t.Error("no manual load settings expected for defaults")
	}
	if _, ok := stepByID(p, "install-lms-cli"); ok {
		t.Error("lms bootstrap is not needed when LM Studio is installed")
	}
	contains(t, "installed lms", runs(mustStep(t, p, "download-model")), "lms get ")
}

func TestLMStudioMLX(t *testing.T) {
	p := build(t, spec{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "lmstudio-mlx", goos: "darwin", goarch: "arm64",
		backend: "mlx", tuned: true, harness: "claude-code"}.input(t))
	contains(t, "install", runs(mustStep(t, p, "install-lmstudio")), "brew install --cask lm-studio")
	contains(t, "download", runs(mustStep(t, p, "download-model")), "get https://huggingface.co/mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit --mlx -y")
	load := runs(mustStep(t, p, "start-model"))
	contains(t, "load", load, "--context-length 65536", "--identifier qwen3-coder:30b-mlx-4bit-cyrlm")
	lacks(t, "mlx load", load, "--gpu")
	if p.LocalModelName != "qwen3-coder:30b-mlx-4bit-cyrlm" {
		t.Errorf("LocalModelName %s", p.LocalModelName)
	}
	if !p.HarnessCompatible {
		t.Error("LM Studio serves the Anthropic API")
	}
}

func TestMLXLM(t *testing.T) {
	base := spec{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "mlx-lm", goos: "darwin", goarch: "arm64", backend: "mlx", tuned: true, harness: "opencode"}
	p := build(t, base.input(t))
	if p.LocalModelName != "mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit" {
		t.Errorf("mlx-lm must serve the repo id, got %s", p.LocalModelName)
	}
	contains(t, "install", runs(mustStep(t, p, "install-mlx-lm")), "uv tool install mlx-lm")
	contains(t, "download", runs(mustStep(t, p, "download-model")), "mlx_lm.generate --model mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit")
	script := file(t, mustStep(t, p, "configure-launch-script"), "~/.cyrlm/mlx-lm/qwen3-coder-30b-mlx-4bit-cyrlm.sh").Content
	contains(t, "launch", script, "exec mlx_lm.server --model mlx-community/Qwen3-Coder-30B-A3B-Instruct-4bit --host 127.0.0.1 --port 8080")
	lacks(t, "launch (f16)", script, "--kv-bits")

	q8 := base
	q8.kv = "q8_0"
	p = build(t, q8.input(t))
	contains(t, "launch (q8)", file(t, mustStep(t, p, "configure-launch-script"), "~/.cyrlm/mlx-lm/qwen3-coder-30b-mlx-4bit-cyrlm.sh").Content, "--kv-bits 8")

	// Claude Code needs the Anthropic API, which mlx-lm does not serve.
	cc := base
	cc.harness = "claude-code"
	p = build(t, cc.input(t))
	if p.HarnessCompatible || !strings.Contains(p.HarnessNote, "Anthropic Messages API") {
		t.Errorf("claude-code on mlx-lm: %v %q", p.HarnessCompatible, p.HarnessNote)
	}
}

func TestBuildErrors(t *testing.T) {
	mlxLinux := spec{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "mlx-lm", goos: "linux", goarch: "arm64", backend: "mlx"}.input(t)
	ggufOnMLX := spec{model: "qwen3-coder-30b-a3b", quant: "Q4_K_M", runtime: "mlx-lm", goos: "darwin", goarch: "arm64"}.input(t)
	mlxOnOllama := spec{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "ollama", goos: "darwin", goarch: "arm64"}.input(t)
	noMLXRepo := spec{model: "qwen3-32b", quant: "MLX-4bit", runtime: "mlx-lm", goos: "darwin", goarch: "arm64"}.input(t)
	noHFLlama := spec{model: "qwen3-8b", runtime: "llamacpp", goos: "linux", goarch: "amd64"}.input(t)
	noHFLlama.Model.HFGGUFRepo = ""
	lmsLinuxARM := spec{model: "qwen3-8b", runtime: "lmstudio", goos: "darwin", goarch: "amd64"}.input(t)
	noOS := spec{model: "qwen3-8b", runtime: "ollama", goos: "linux", goarch: "amd64"}.input(t)
	noOS.OS = ""
	cases := map[string]struct {
		in   Input
		want string
	}{
		"mlx-lm on linux":         {mlxLinux, "does not run on linux/arm64"},
		"gguf on mlx-lm":          {ggufOnMLX, "loads mlx models"},
		"mlx on ollama":           {mlxOnOllama, "loads gguf models"},
		"no MLX repo":             {noMLXRepo, "no Hugging Face MLX repo"},
		"llama.cpp without HF":    {noHFLlama, "no Hugging Face GGUF repo"},
		"lm studio on intel mac":  {lmsLinuxARM, "does not run on darwin/amd64"},
		"missing OS":              {noOS, "OS is required"},
		"missing runtime profile": {Input{Model: model(t, "qwen3-8b"), OS: "linux"}, "no runtime profile"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Build(c.in)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestCatalogModelSources(t *testing.T) {
	gen := model(t, "qwen3.6-35b-a3b")
	if !gen.Generated || gen.HFGGUFRepo == "" {
		t.Fatalf("fixture: want a generated entry with an HF repo, got %+v", gen)
	}
	hand := model(t, "gpt-oss-20b")
	if hand.Generated {
		t.Fatal("fixture: gpt-oss-20b should be hand-entered")
	}
	noHF := model(t, "qwen3-8b")
	noHF.HFGGUFRepo = ""

	cases := []struct {
		name  string
		m     catalog.Model
		quant string
		want  string
		conf  string
	}{
		{"generated", gen, "Q4_K_M", "ollama pull hf.co/unsloth/Qwen3.6-35B-A3B-GGUF:Q4_K_M", High},
		{"generated non-default quant", gen, "Q8_0", "ollama pull hf.co/unsloth/Qwen3.6-35B-A3B-GGUF:Q8_0", High},
		{"hand-entered", hand, "MXFP4", "ollama pull hf.co/ggml-org/gpt-oss-20b-GGUF:MXFP4", High},
		{"library tag fallback", noHF, "Q4_K_M", "ollama pull qwen3:8b\n", Medium},
		{"library tag with quant", noHF, "Q8_0", "ollama pull qwen3:8b-q8_0", Medium},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := profile(t, "ollama")
			q, err := c.m.Quant(c.quant)
			if err != nil {
				t.Fatal(err)
			}
			in := Input{Model: c.m, Quant: q, Profile: p, Settings: p.Resolve(runtime.ResolveInput{NativeCtx: c.m.NativeCtx, Tuned: true}),
				Backend: "cuda", OS: "linux", Arch: "amd64", Harness: harnessPtr(t, "aider")}
			pl := build(t, in)
			dl := mustStep(t, pl, "download-model")
			contains(t, "download", runs(dl), c.want)
			if dl.Confidence != c.conf {
				t.Errorf("confidence %s, want %s", dl.Confidence, c.conf)
			}
			mf := mustStep(t, pl, "configure-modelfile").Files[0].Content
			contains(t, "Modelfile", mf, "PARAMETER num_ctx "+itoa(pl.ContextTokens))
			if strings.Contains(c.want, "hf.co/") {
				contains(t, "Modelfile", mf, "FROM hf.co/")
			}
			if c.quant != c.m.Quants[0].Name && !strings.Contains(pl.LocalModelName, strings.ToLower(c.quant)) {
				t.Errorf("non-default quant should be in the local name: %s", pl.LocalModelName)
			}
			// Aider speaks the Ollama API: a manual launch step.
			contains(t, "aider", mustStep(t, pl, "harness-aider").Why, "OLLAMA_API_BASE=http://127.0.0.1:11434", "ollama_chat/"+pl.LocalModelName)
		})
	}
}

func TestContextOverrideAndDefaults(t *testing.T) {
	// Explicit context ends up in the Modelfile.
	p := build(t, spec{model: "qwen3-coder-30b-a3b", runtime: "ollama", goos: "darwin", goarch: "arm64", tuned: true, ctx: 32768}.input(t))
	contains(t, "Modelfile", mustStep(t, p, "configure-modelfile").Files[0].Content, "PARAMETER num_ctx 32768\n")

	// Zero settings and quant fall back to the runtime's defaults.
	m := model(t, "qwen3-coder-30b-a3b")
	p = build(t, Input{Model: m, Quant: catalog.Quant{}, Profile: profile(t, "mlx-lm"), OS: "darwin", Arch: "arm64"})
	if p.Quant != "MLX-4bit" || p.ContextTokens <= 0 || p.Backend != "mlx" {
		t.Errorf("defaults not applied: quant %s ctx %d backend %s", p.Quant, p.ContextTokens, p.Backend)
	}
	if p.Harness != "" || !p.HarnessCompatible || len(stepsOfKind(p, KindHarness)) != 0 {
		t.Errorf("no-harness plan: %+v", p)
	}
}

func TestOtherHarnesses(t *testing.T) {
	cases := []struct {
		harness, runtime, step string
		want                   []string
	}{
		{"continue", "ollama", "harness-continue", []string{"provider: ollama", "apiBase: http://127.0.0.1:11434"}},
		{"cline", "lmstudio", "harness-cline", []string{"http://127.0.0.1:1234"}},
		{"qwen-code", "llamacpp", "harness-qwen-code", []string{"OPENAI_BASE_URL=http://127.0.0.1:8080/v1"}},
		{"generic", "llamacpp", "harness-generic", []string{"http://127.0.0.1:8080/v1"}},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			p := build(t, spec{model: "qwen3-8b", runtime: c.runtime, goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, harness: c.harness}.input(t))
			s := mustStep(t, p, c.step)
			text := s.Why
			for _, f := range s.Files {
				text += f.Content
			}
			contains(t, c.step, text, c.want...)
		})
	}
	// Zed and Continue also speak OpenAI Chat Completions, so they work
	// with llama-server through its OpenAI endpoint.
	p := build(t, spec{model: "qwen3-8b", runtime: "llamacpp", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, harness: "continue"}.input(t))
	if !p.HarnessCompatible {
		t.Fatalf("continue should connect to llama-server over its OpenAI API: %s", p.HarnessNote)
	}
	s := mustStep(t, p, "harness-continue")
	text := s.Why
	for _, f := range s.Files {
		text += f.Content
	}
	contains(t, "harness-continue", text, "provider: openai", "apiBase: http://127.0.0.1:8080/v1")
	// Claude Code needs the Anthropic API: mlx-lm only serves Chat Completions.
	mp := build(t, spec{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "mlx-lm", goos: "darwin", goarch: "arm64", backend: "mlx", harness: "claude-code"}.input(t))
	if mp.HarnessCompatible {
		t.Error("claude-code must be incompatible with mlx-lm (no Anthropic API)")
	}
}

func TestPlanJSONShape(t *testing.T) {
	p := build(t, spec{model: "qwen3-8b", runtime: "ollama", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, harness: "claude-code"}.input(t))
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"model", "model_name", "quant", "runtime", "runtime_name", "backend", "harness", "os", "arch", "tuned",
		"context_tokens", "kv_cache_type", "flash_attention", "keep_alive_s", "endpoint", "local_model_name", "download_bytes",
		"harness_compatible", "steps"} {
		if _, ok := raw[k]; !ok {
			t.Errorf("plan JSON lacks %q", k)
		}
	}
	step := raw["steps"].([]any)[0].(map[string]any)
	for _, k := range []string{"id", "kind", "title", "commands", "confidence"} {
		if _, ok := step[k]; !ok {
			t.Errorf("step JSON lacks %q: %v", k, step)
		}
	}
	cmd := step["commands"].([]any)[0].(map[string]any)
	for _, k := range []string{"os", "shell", "run"} {
		if _, ok := cmd[k]; !ok {
			t.Errorf("command JSON lacks %q", k)
		}
	}
}

func TestLocalNameAndQuoting(t *testing.T) {
	m := catalog.Model{ID: "x", OllamaTag: "Foo/Bar:7B", Quants: []catalog.Quant{{Name: "Q4_K_M"}, {Name: "Q8_0"}}}
	if got := localName(m, m.Quants[0]); got != "foo-bar:7b-cyrlm" {
		t.Errorf("localName = %s", got)
	}
	if got := localName(m, m.Quants[1]); got != "foo-bar:7b-q8_0-cyrlm" {
		t.Errorf("localName = %s", got)
	}
	if got := shQuote("it's here"); got != `'it'\''s here'` {
		t.Errorf("shQuote = %s", got)
	}
	if got := shQuote("hf.co/a/b:Q4_K_M"); got != "hf.co/a/b:Q4_K_M" {
		t.Errorf("shQuote = %s", got)
	}
	if got := psQuote("it's"); got != "'it''s'" {
		t.Errorf("psQuote = %s", got)
	}
	mm := catalog.Model{HFMLXRepo: "mlx-community/Foo-4bit"}
	if got := mlxRepo(mm, catalog.Quant{Name: "MLX-8bit"}); got != "mlx-community/Foo-8bit" {
		t.Errorf("mlxRepo = %s", got)
	}
	if got := mlxRepo(mm, catalog.Quant{Name: "MLX-MXFP4"}); got != "mlx-community/Foo-4bit" {
		t.Errorf("mlxRepo = %s", got)
	}
}

// TestShellSyntax parses (sh -n, never executes) every POSIX command and
// shell script the plans contain.
func TestShellSyntax(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	specs := []spec{
		{model: "qwen3-coder-30b-a3b", runtime: "ollama", goos: "darwin", goarch: "arm64", tuned: true, kv: "q8_0", harness: "claude-code"},
		{model: "qwen3-coder-30b-a3b", runtime: "ollama", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, kv: "q8_0", harness: "aider", installed: true},
		{model: "qwen3-coder-30b-a3b", runtime: "llamacpp", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, kv: "q8_0", harness: "claude-code"},
		{model: "qwen3-coder-30b-a3b", runtime: "llamacpp", goos: "linux", goarch: "arm64", backend: "vulkan", tuned: true, harness: "qwen-code"},
		{model: "qwen3-coder-30b-a3b", runtime: "llamacpp", goos: "darwin", goarch: "arm64", backend: "metal", tuned: true, harness: "opencode"},
		{model: "qwen3-coder-30b-a3b", runtime: "lmstudio", goos: "linux", goarch: "amd64", backend: "cuda", tuned: true, kv: "q8_0", harness: "codex"},
		{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "lmstudio-mlx", goos: "darwin", goarch: "arm64", tuned: true, kv: "q8_0"},
		{model: "qwen3-coder-30b-a3b", quant: "MLX-4bit", runtime: "mlx-lm", goos: "darwin", goarch: "arm64", tuned: true, kv: "q8_0", harness: "generic"},
	}
	for _, sp := range specs {
		p := build(t, sp.input(t))
		var scripts []string
		for _, s := range p.Steps {
			for _, c := range append(append([]Command{}, s.Commands...), s.Undo...) {
				scripts = append(scripts, c.Run)
			}
			for _, f := range s.Files {
				if strings.HasSuffix(f.Path, ".sh") {
					scripts = append(scripts, f.Content)
				}
			}
		}
		for _, src := range scripts {
			cmd := exec.Command(sh, "-n")
			cmd.Stdin = strings.NewReader(src)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Errorf("%s/%s: sh -n failed: %v %s\n%s", sp.runtime, sp.goos, err, out, src)
			}
		}
	}
}
