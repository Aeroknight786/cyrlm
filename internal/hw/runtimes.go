package hw

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"runtime"
	"strings"
	"sync"
)

// Default loopback endpoints of runtime servers.
const (
	ollamaURL      = "http://127.0.0.1:11434"
	lmStudioURL    = "http://127.0.0.1:1234"
	llamaCppURL    = "http://127.0.0.1:8080" // older default and triaLM's setup port
	llamaCppNewURL = "http://127.0.0.1:9931" // default of recent llama-server builds
	vllmURL        = "http://127.0.0.1:8000"
	lemonadeURL    = "http://127.0.0.1:8000"
	janURL         = "http://127.0.0.1:1337"
	dockerModelURL = "http://127.0.0.1:12434"
)

// DetectRuntimes finds installed local-LLM runtimes (Ollama, LM Studio,
// llama.cpp, mlx-lm, vLLM, Jan, Lemonade, Docker Model Runner) by looking on
// PATH and in default install locations, asks for versions with read-only
// commands (`--version`, Python dist-info metadata, app Info.plist), and
// probes default loopback ports to see whether a server is running. It never
// starts, installs or modifies anything, and returns within about 5 s.
// Results are in a fixed order (the order above); the slice is never nil.
func DetectRuntimes(ctx context.Context) []InstalledRuntime {
	return detectRuntimes(ctx, osRunner{}, runtime.GOOS)
}

type rtFinder struct {
	r            runner
	goos         string
	home         string
	localAppData string
}

func detectRuntimes(ctx context.Context, r runner, goos string) []InstalledRuntime {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, detectBudget)
	defer cancel()

	f := rtFinder{r: r, goos: goos}
	if goos == "windows" {
		f.home = r.Getenv("USERPROFILE")
		f.localAppData = r.Getenv("LOCALAPPDATA")
		if f.localAppData == "" {
			f.localAppData = f.under(f.home, "AppData", "Local")
		}
	} else {
		f.home = r.Getenv("HOME")
	}

	probes := []func(context.Context) *InstalledRuntime{
		f.ollama, f.lmStudio, f.llamaCpp, f.mlxLM, f.vllm, f.jan, f.lemonade, f.dockerModelRunner,
	}
	found := make([]*InstalledRuntime, len(probes))
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			found[i] = p(ctx)
		}()
	}
	wg.Wait()

	out := []InstalledRuntime{}
	for _, rt := range found {
		if rt != nil {
			out = append(out, *rt)
		}
	}
	return out
}

// ---- per-runtime probes ---------------------------------------------------

func (f rtFinder) ollama(ctx context.Context) *InstalledRuntime {
	var cands []string
	app := ""
	switch f.goos {
	case "darwin":
		app = f.firstExisting("/Applications/Ollama.app", f.under(f.home, "Applications", "Ollama.app"))
		cands = []string{"/usr/local/bin/ollama", "/opt/homebrew/bin/ollama", f.under(app, "Contents", "Resources", "ollama")}
	case "windows":
		cands = []string{f.under(f.localAppData, "Programs", "Ollama", "ollama.exe")}
	default:
		cands = []string{"/usr/local/bin/ollama", "/usr/bin/ollama"}
	}
	exe, src := f.find([]string{"ollama"}, cands...)
	var cliVer, srvVer string
	parallel(
		func() {
			if exe != "" {
				out, _ := f.r.Run(ctx, exe, "--version")
				cliVer = parseOllamaVersion(string(out))
			}
		},
		func() {
			if st, body, err := f.r.HTTPGet(ctx, ollamaURL+"/api/version"); err == nil && st == 200 {
				srvVer = jsonStringField(body, "version")
			}
		},
	)
	rt := &InstalledRuntime{ID: RuntimeOllama, Path: exe, Source: src, Version: firstNonEmpty(srvVer, cliVer)}
	if rt.Path == "" && app != "" {
		rt.Path, rt.Source = app, "app bundle"
	}
	if srvVer != "" {
		rt.Running, rt.Endpoint = true, ollamaURL
	}
	return f.result(rt)
}

func (f rtFinder) lmStudio(ctx context.Context) *InstalledRuntime {
	var apps []string
	switch f.goos {
	case "darwin":
		apps = []string{"/Applications/LM Studio.app", f.under(f.home, "Applications", "LM Studio.app")}
	case "windows":
		apps = []string{
			f.under(f.localAppData, "Programs", "LM Studio", "LM Studio.exe"),
			f.under(f.localAppData, "Programs", "lm-studio", "LM Studio.exe"),
		}
	default:
		if m, _ := f.r.Glob(f.under(f.home, "Applications", "LM-Studio*.AppImage")); len(m) > 0 && f.home != "" {
			apps = m[:1]
		}
	}
	app := f.firstExisting(apps...)
	lms, src := f.find([]string{"lms"},
		f.under(f.home, ".lmstudio", "bin", f.exe("lms")),
		f.under(f.home, ".cache", "lm-studio", "bin", f.exe("lms")))
	if app == "" && lms == "" {
		return nil
	}
	rt := &InstalledRuntime{ID: RuntimeLMStudio, Path: lms, Source: src}
	if app != "" {
		rt.Path, rt.Source = app, "app"
		rt.Version = f.appVersion(app)
	}
	if st, body, err := f.r.HTTPGet(ctx, lmStudioURL+"/v1/models"); err == nil && st == 200 && jsonHasKey(body, "data") {
		rt.Running, rt.Endpoint = true, lmStudioURL
	}
	return rt
}

func (f rtFinder) llamaCpp(ctx context.Context) *InstalledRuntime {
	cands := []string{
		"/opt/homebrew/bin/llama-server", "/usr/local/bin/llama-server",
		"/home/linuxbrew/.linuxbrew/bin/llama-server",
		f.under(f.home, "llama.cpp", "build", "bin", f.exe("llama-server")),
	}
	if f.goos == "windows" {
		cands = []string{
			f.under(f.localAppData, "Microsoft", "WinGet", "Links", "llama-server.exe"),
			f.under(f.home, "llama.cpp", "build", "bin", "Release", "llama-server.exe"),
		}
	}
	exe, src := f.find([]string{"llama-server", "llama-cli"}, cands...)
	var ver, backend string
	healthOK, endpoint := false, ""
	parallel(
		func() {
			if exe == "" {
				return
			}
			out, _ := f.r.Run(ctx, exe, "--version")
			ver, backend = parseLlamaCppVersion(string(out))
			if backend == "" {
				backend = f.llamaLibBackend(exe)
			}
		},
		func() {
			for _, u := range []string{llamaCppURL, llamaCppNewURL} {
				st, body, err := f.r.HTTPGet(ctx, u+"/health")
				if err == nil && (st == 200 || st == 503) && (jsonHasKey(body, "status") || jsonHasKey(body, "error")) {
					healthOK, endpoint = true, u
					return
				}
			}
		},
	)
	confirmed := false
	if healthOK {
		if st, body, err := f.r.HTTPGet(ctx, endpoint+"/props"); err == nil && st == 200 {
			confirmed = jsonHasKey(body, "default_generation_settings") || jsonHasKey(body, "build_info")
			if ver == "" {
				ver = llamaBuildInfoVersion(jsonStringField(body, "build_info"))
			}
		}
	}
	rt := &InstalledRuntime{ID: RuntimeLlamaCpp, Path: exe, Source: src, Version: ver, Backend: backend}
	if healthOK && (confirmed || exe != "") {
		rt.Running, rt.Endpoint = true, endpoint
	}
	return f.result(rt)
}

func (f rtFinder) mlxLM(ctx context.Context) *InstalledRuntime {
	exe, src := f.find([]string{"mlx_lm.server", "mlx_lm.generate", "mlx_lm"},
		f.under(f.home, ".local", "bin", "mlx_lm.server"), "/opt/homebrew/bin/mlx_lm.server")
	if exe == "" {
		return nil
	}
	// mlx_lm.server's default port (8080) collides with llama.cpp, so no
	// Running probe; importing mlx just for a version is too slow, so read
	// the installed package metadata instead.
	rt := &InstalledRuntime{ID: RuntimeMLXLM, Path: exe, Source: src, Version: f.pythonDistVersion(exe, "mlx_lm")}
	if f.goos == "darwin" {
		rt.Backend = "metal"
	}
	return rt
}

var vllmOwnedRe = regexp.MustCompile(`"owned_by"\s*:\s*"vllm"`)

func (f rtFinder) vllm(ctx context.Context) *InstalledRuntime {
	exe, src := f.find([]string{"vllm"})
	var ver, srvVer string
	running := false
	parallel(
		func() {
			if exe != "" {
				ver = f.pythonDistVersion(exe, "vllm")
			}
		},
		func() {
			st, body, err := f.r.HTTPGet(ctx, vllmURL+"/v1/models")
			ownModels := err == nil && st == 200 && vllmOwnedRe.Match(body)
			// With --api-key, /v1 answers 401 but /version stays public.
			keyed := err == nil && st == 401 && exe != ""
			if !ownModels && !keyed {
				return
			}
			if st, body, err := f.r.HTTPGet(ctx, vllmURL+"/version"); err == nil && st == 200 {
				srvVer = jsonStringField(body, "version")
			}
			running = ownModels || srvVer != ""
		},
	)
	rt := &InstalledRuntime{ID: RuntimeVLLM, Path: exe, Source: src, Version: firstNonEmpty(srvVer, ver)}
	if running {
		rt.Running, rt.Endpoint = true, vllmURL
	}
	return f.result(rt)
}

func (f rtFinder) jan(ctx context.Context) *InstalledRuntime {
	var app, src string
	switch f.goos {
	case "darwin":
		app = f.firstExisting("/Applications/Jan.app", f.under(f.home, "Applications", "Jan.app"))
		src = "app"
	case "windows":
		app = f.firstExisting(
			f.under(f.localAppData, "Programs", "jan", "Jan.exe"),
			f.under(f.localAppData, "Programs", "Jan", "Jan.exe"))
		src = "install path"
	default:
		app, src = f.find([]string{"jan", "Jan"}, "/opt/Jan/jan", "/usr/bin/jan")
	}
	if app == "" {
		return nil
	}
	rt := &InstalledRuntime{ID: RuntimeJan, Path: app, Source: src}
	if f.goos == "darwin" {
		rt.Version = f.appVersion(app)
	}
	// Recent Jan versions require an API key (401 without one).
	if st, body, err := f.r.HTTPGet(ctx, janURL+"/v1/models"); err == nil && (st == 401 || st == 200 && jsonHasKey(body, "data")) {
		rt.Running, rt.Endpoint = true, janURL
	}
	return rt
}

var versionTokenRe = regexp.MustCompile(`\bv?(\d+\.\d+(?:\.\d+)?(?:[.-][0-9A-Za-z]+)*)\b`)

func (f rtFinder) lemonade(ctx context.Context) *InstalledRuntime {
	var cands []string
	if f.goos == "windows" {
		cands = []string{f.under(f.localAppData, "lemonade_server", "bin", "lemonade-server.exe")}
	}
	exe, src := f.find([]string{"lemonade-server", "lemonade-server-dev"}, cands...)
	var ver string
	running, strong := false, false
	parallel(
		func() {
			if exe == "" {
				return
			}
			if ver = f.pythonDistVersion(exe, "lemonade_sdk"); ver == "" {
				out, _ := f.r.Run(ctx, exe, "--version")
				if m := versionTokenRe.FindStringSubmatch(string(out)); m != nil {
					ver = m[1]
				}
			}
		},
		func() {
			st, body, err := f.r.HTTPGet(ctx, lemonadeURL+"/api/v1/health")
			running = err == nil && st == 200 && jsonHasKey(body, "status")
			strong = running && jsonHasKey(body, "model_loaded")
		},
	)
	rt := &InstalledRuntime{ID: RuntimeLemonade, Path: exe, Source: src, Version: ver}
	if running && (exe != "" || strong) {
		rt.Running, rt.Endpoint = true, lemonadeURL
	}
	return f.result(rt)
}

var dockerModelVersionRe = regexp.MustCompile(`(?i)model runner version\s+v?(\S+)`)

func (f rtFinder) dockerModelRunner(ctx context.Context) *InstalledRuntime {
	var cands []string
	switch f.goos {
	case "darwin":
		cands = []string{"/usr/local/bin/docker", "/opt/homebrew/bin/docker", "/Applications/Docker.app/Contents/Resources/bin/docker"}
	case "windows":
		cands = []string{`C:\Program Files\Docker\Docker\resources\bin\docker.exe`}
	}
	exe, src := f.find([]string{"docker"}, cands...)
	if exe == "" {
		return nil
	}
	var out []byte
	var err error
	running := false
	parallel(
		// `docker model version` is client-side only; `status` and friends
		// may install the runner on Docker Engine, so they are not used.
		func() { out, err = f.r.Run(ctx, exe, "model", "version") },
		func() {
			st, _, herr := f.r.HTTPGet(ctx, dockerModelURL+"/engines/llama.cpp/v1/models")
			running = herr == nil && st == 200
		},
	)
	m := dockerModelVersionRe.FindStringSubmatch(string(out))
	if err != nil || m == nil {
		return nil
	}
	rt := &InstalledRuntime{ID: RuntimeDocker, Path: exe, Source: src, Version: m[1]}
	if running {
		rt.Running, rt.Endpoint = true, dockerModelURL
	}
	if f.goos == "darwin" {
		rt.Backend = "metal"
	}
	return rt
}

// result drops runtimes that were neither found on disk nor answering.
func (f rtFinder) result(rt *InstalledRuntime) *InstalledRuntime {
	if rt.Path == "" && !rt.Running {
		return nil
	}
	if rt.Path == "" {
		rt.Source = "http"
	}
	return rt
}

// ---- helpers --------------------------------------------------------------

// under joins elems below base, or returns "" when base is unknown.
func (f rtFinder) under(base string, elem ...string) string {
	if base == "" {
		return ""
	}
	if f.goos == "windows" {
		parts := []string{strings.ReplaceAll(base, `\`, "/")}
		for _, e := range elem {
			parts = append(parts, strings.ReplaceAll(e, `\`, "/"))
		}
		return strings.ReplaceAll(path.Join(parts...), "/", `\`)
	}
	return path.Join(append([]string{base}, elem...)...)
}

func (f rtFinder) exe(name string) string {
	if f.goos == "windows" {
		return name + ".exe"
	}
	return name
}

// find returns the first of names on PATH, else the first existing candidate.
func (f rtFinder) find(names []string, candidates ...string) (string, string) {
	for _, n := range names {
		if p, err := f.r.LookPath(n); err == nil {
			return p, "PATH"
		}
	}
	if p := f.firstExisting(candidates...); p != "" {
		return p, "install path"
	}
	return "", ""
}

func (f rtFinder) firstExisting(paths ...string) string {
	for _, p := range paths {
		if p != "" && f.r.Exists(p) {
			return p
		}
	}
	return ""
}

// appVersion reads CFBundleShortVersionString from a macOS app bundle.
func (f rtFinder) appVersion(app string) string {
	if !strings.HasSuffix(app, ".app") {
		return ""
	}
	b, err := f.r.ReadFile(app + "/Contents/Info.plist")
	if err != nil {
		return ""
	}
	return parsePlistVersion(b)
}

// llamaLibBackend infers the backend from ggml backend libraries shipped
// next to the executable (release archives use dynamic backends).
func (f rtFinder) llamaLibBackend(exe string) string {
	dir := dirOf(exe)
	if dir == "" {
		return ""
	}
	sep := "/"
	if f.goos == "windows" {
		sep = `\`
	}
	for _, c := range []struct{ glob, backend string }{
		{"*ggml-cuda*", "cuda"}, {"*ggml-hip*", "rocm"}, {"*ggml-vulkan*", "vulkan"},
		{"*ggml-sycl*", "sycl"}, {"*ggml-metal*", "metal"},
	} {
		if m, _ := f.r.Glob(dir + sep + c.glob); len(m) > 0 {
			return c.backend
		}
	}
	return ""
}

// pythonDistVersion finds the version of an installed Python distribution
// for a console script by locating <prefix>/lib/python3*/site-packages/
// <dist>-<version>.dist-info, where prefix comes from the script's shebang
// interpreter or the script's own bin directory. Python is never run.
func (f rtFinder) pythonDistVersion(script, dist string) string {
	var prefixes []string
	if b, err := f.r.ReadFile(script); err == nil {
		if py := shebangInterpreter(b); py != "" {
			prefixes = append(prefixes, dirOf(dirOf(py)))
		}
	}
	prefixes = append(prefixes, dirOf(dirOf(script)))
	seen := map[string]bool{}
	for _, p := range prefixes {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		for _, pat := range []string{
			p + "/lib/python3*/site-packages/" + dist + "-*.dist-info",
			p + "/lib/python3*/dist-packages/" + dist + "-*.dist-info",
			p + `\Lib\site-packages\` + dist + "-*.dist-info",
		} {
			matches, _ := f.r.Glob(pat)
			for _, m := range matches {
				if v := distInfoVersion(m, dist); v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func dirOf(p string) string {
	i := strings.LastIndexAny(p, `/\`)
	if i <= 0 {
		return ""
	}
	return p[:i]
}

// shebangInterpreter returns the interpreter path of a script, handling
// pip's /bin/sh trampoline for long paths. "/usr/bin/env python" yields "".
func shebangInterpreter(b []byte) string {
	if len(b) > 4096 {
		b = b[:4096]
	}
	s := string(b)
	if !strings.HasPrefix(s, "#!") {
		return ""
	}
	lines := strings.Split(s, "\n")
	f := strings.Fields(strings.TrimPrefix(lines[0], "#!"))
	if len(f) == 0 {
		return ""
	}
	interp := f[0]
	if strings.HasSuffix(interp, "/sh") {
		for _, l := range lines[1:] {
			if rest, ok := strings.CutPrefix(l, "'''exec' "); ok {
				if ff := strings.Fields(rest); len(ff) > 0 {
					return strings.Trim(ff[0], `'"`)
				}
			}
		}
		return ""
	}
	if strings.HasSuffix(interp, "/env") || !strings.Contains(path.Base(interp), "python") {
		return ""
	}
	return interp
}

// distInfoVersion extracts "0.28.2" from ".../mlx_lm-0.28.2.dist-info".
func distInfoVersion(p, dist string) string {
	base := p[strings.LastIndexAny(p, `/\`)+1:]
	base = strings.TrimSuffix(base, ".dist-info")
	if len(base) <= len(dist)+1 || !strings.EqualFold(base[:len(dist)+1], dist+"-") {
		return ""
	}
	return base[len(dist)+1:]
}

var ollamaVersionRe = regexp.MustCompile(`(?i)version is\s+v?(\d[^\s]*)`)

// parseOllamaVersion parses `ollama --version` ("ollama version is 0.12.6",
// or "Warning: client version is 0.12.6" when no server is running).
func parseOllamaVersion(out string) string {
	if m := ollamaVersionRe.FindStringSubmatch(out); m != nil {
		return m[1]
	}
	return ""
}

var (
	llamaVersionRe = regexp.MustCompile(`(?m)^version:\s*(\d+)\s*\(([0-9a-fA-F]+)\)`)
	hipWordRe      = regexp.MustCompile(`(?i)\b(rocm|hip|ggml_hip)\b`)
	appleTargetRe  = regexp.MustCompile(`(?i)\b(arm64|aarch64)-apple-darwin`)
)

// parseLlamaCppVersion parses `llama-server --version` / `llama-cli
// --version` output: "version: 6765 (abc1234)" gives "b6765"; backend hints
// come from backend-loading lines (CUDA, ROCm/HIP, Vulkan, SYCL, Metal), or
// an Apple Silicon build target (Metal is on by default there).
func parseLlamaCppVersion(out string) (version, backend string) {
	if m := llamaVersionRe.FindStringSubmatch(out); m != nil {
		version = "b" + m[1]
	}
	low := strings.ToLower(out)
	switch {
	case strings.Contains(low, "cuda"):
		backend = "cuda"
	case hipWordRe.MatchString(out):
		backend = "rocm"
	case strings.Contains(low, "vulkan"):
		backend = "vulkan"
	case strings.Contains(low, "sycl"):
		backend = "sycl"
	case strings.Contains(low, "metal"), appleTargetRe.MatchString(out):
		backend = "metal"
	case strings.Contains(low, "loaded cpu backend"):
		backend = "cpu"
	}
	return version, backend
}

// llamaBuildInfoVersion turns /props build_info "b6765-abc1234" into "b6765".
func llamaBuildInfoVersion(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '-'); i > 0 {
		s = s[:i]
	}
	if s != "" && s[0] >= '0' && s[0] <= '9' {
		s = "b" + s
	}
	return s
}

var plistVersionRe = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

// parsePlistVersion reads CFBundleShortVersionString from an XML Info.plist.
func parsePlistVersion(b []byte) string {
	if m := plistVersionRe.FindSubmatch(b); m != nil {
		return strings.TrimSpace(string(m[1]))
	}
	return ""
}

func jsonStringField(body []byte, key string) string {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return ""
	}
	var s string
	if json.Unmarshal(m[key], &s) != nil {
		return ""
	}
	return s
}

func jsonHasKey(body []byte, key string) bool {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return false
	}
	_, ok := m[key]
	return ok
}
