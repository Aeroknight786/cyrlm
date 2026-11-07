package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/upstream"
)

// runtimeEndpoints are the usual local addresses of each runtime's
// OpenAI-compatible server, tried in order when --endpoint is not given.
var runtimeEndpoints = []struct{ id, name, url string }{
	{"llamacpp", "llama.cpp", "http://127.0.0.1:8080"},
	{"llamacpp", "llama.cpp", "http://127.0.0.1:8081"},
	{"ollama", "Ollama", "http://127.0.0.1:11434"},
	{"lmstudio", "LM Studio", "http://127.0.0.1:1234"},
	{"mlx-lm", "mlx-lm", "http://127.0.0.1:8080"},
}

// cmdBridge connects a harness to a real local runtime through triaLM's
// protocol layer: the harness speaks Responses / Anthropic / Ollama, the
// runtime only needs plain Chat Completions (its most mature API). It
// also reshapes conversations for strict chat templates and turns
// deterministic runtime failures into errors the harness shows instead of
// retrying. No timing is simulated.
func cmdBridge(args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	rt := fs.String("runtime", "", "runtime to connect to: llamacpp | ollama | lmstudio | mlx-lm (default: the first one answering)")
	endpoint := fs.String("endpoint", "", "runtime base URL, e.g. http://127.0.0.1:8080")
	model := fs.String("model", "", "model name as the runtime knows it (default: the runtime's first model)")
	hname := fs.String("harness", "", "print connection steps for this coding tool")
	ctxTokens := fs.Int("ctx", 0, "the runtime's context window, so the harness compacts in time (default: read from llama.cpp)")
	port := fs.Int("port", 11535, "listen port (falls back to the next free port)")
	logFile := fs.String("log", "", "write request logs to this file (also works in the foreground)")
	keyEnv := fs.String("key-env", "TRIALM_UPSTREAM_API_KEY", "env var with an API key for the runtime, if it needs one")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return errors.New("usage: trialm bridge [--runtime r | --endpoint URL] [--model m] [--harness h] [--port n] [--background]")
	}
	var hh harness.Harness
	if *hname != "" {
		if hh, err = harness.Get(*hname); err != nil {
			return err
		}
	}
	base, rtName, models, err := findRuntime(*rt, *endpoint)
	if err != nil {
		return err
	}
	if *model == "" {
		if len(models) == 0 {
			return fmt.Errorf("%s at %s lists no models: pass --model <name>", rtName, base)
		}
		*model = models[0]
	}
	if *ctxTokens == 0 {
		*ctxTokens = llamaCppContext(base)
	}
	key := paths.LookupEnv(*keyEnv)
	if key == "" {
		key = "local"
	}
	logW := out()
	if *logFile != "" {
		if err := os.MkdirAll(filepath.Dir(*logFile), 0o700); err != nil {
			return err
		}
		f, err := os.OpenFile(*logFile, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		logW = f
	}
	client := &upstream.OpenAICompat{BaseURL: base + "/v1", APIKey: key, Model: *model, IdleTimeout: 20 * time.Minute}
	srv := server.New(server.Config{
		Bridge: true, Upstream: client, RuntimeName: rtName, Log: logW,
		Model: api.ModelInfo{Name: *model, ContextLength: *ctxTokens, ModifiedAt: time.Now(), Capabilities: []string{"completion", "tools"}},
	})
	bound, err := srv.Listen("127.0.0.1", *port, true)
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1:%d: %w", *port, err)
	}
	fmt.Fprintf(out(), "\ntrialm bridge → %s at %s, model %s", rtName, base, *model)
	if *ctxTokens > 0 {
		fmt.Fprintf(out(), ", context %d tokens", *ctxTokens)
	}
	fmt.Fprintln(out())
	fmt.Fprintf(out(), "  Endpoints on http://127.0.0.1:%d — OpenAI (/v1/chat/completions, /v1/responses), Anthropic (/v1/messages), Ollama (/api/*)\n", bound)
	fmt.Fprintln(out(), "  Real local inference, no simulation. Requests are reshaped for the model's chat template; runtime errors are shown, not retried.")
	fmt.Fprintln(out())
	if hh.ID != "" {
		setup := hh.SetupFor(harness.Options{Port: bound, Model: *model, Tuned: true, ContextTokens: *ctxTokens, Protocol: "openai-chat", Bridge: true})
		if err := setup.WriteFiles(); err != nil {
			_ = srv.Shutdown(context.Background())
			return err
		}
		fmt.Fprintf(out(), "── Connect %s ─────────────────────────────────────────\n\n%s\n", hh.Name, setup.Render())
	}
	printRunning("")
	return serveUntilSignal(srv)
}

// findRuntime returns the base URL, display name and model ids of the
// runtime to bridge to.
func findRuntime(id, endpoint string) (string, string, []string, error) {
	if endpoint != "" {
		endpoint = strings.TrimRight(strings.TrimSuffix(strings.TrimRight(endpoint, "/"), "/v1"), "/")
		models, err := listModels(endpoint)
		if err != nil {
			return "", "", nil, fmt.Errorf("no OpenAI-compatible runtime answers at %s (%v): start it first", endpoint, err)
		}
		name := "local runtime"
		for _, r := range runtimeEndpoints {
			if r.id == id {
				name = r.name
			}
		}
		return endpoint, name, models, nil
	}
	var tried []string
	for _, r := range runtimeEndpoints {
		if id != "" && r.id != id {
			continue
		}
		tried = append(tried, r.url)
		if models, err := listModels(r.url); err == nil {
			return r.url, r.name, models, nil
		}
	}
	if len(tried) == 0 {
		return "", "", nil, fmt.Errorf("unknown --runtime %q (llamacpp, ollama, lmstudio, mlx-lm)", id)
	}
	return "", "", nil, fmt.Errorf("no local runtime answers at %s: start it first, or pass --endpoint", strings.Join(tried, ", "))
}

func listModels(base string) ([]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/models", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET /v1/models: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("GET /v1/models: %v", err)
	}
	var ids []string
	for _, d := range body.Data {
		ids = append(ids, d.ID)
	}
	return ids, nil
}

// llamaCppContext reads llama-server's per-slot context from /props (0 when
// the runtime is not llama.cpp).
func llamaCppContext(base string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/props", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var p struct {
		Settings struct {
			NCtx int `json:"n_ctx"`
		} `json:"default_generation_settings"`
	}
	if json.NewDecoder(resp.Body).Decode(&p) != nil {
		return 0
	}
	return p.Settings.NCtx
}

func printRunning(extra string) {
	if os.Getenv("TRIALM_DETACHED") == "1" || os.Getenv("CYRLM_DETACHED") == "1" {
		return
	}
	stop := fmt.Sprintf("kill %d", os.Getpid())
	if goruntime.GOOS == "windows" {
		stop = fmt.Sprintf("Stop-Process -Id %d", os.Getpid())
	}
	fmt.Fprintf(out(), "Running (pid %d). Stop with Ctrl-C, or from another terminal: %s%s\n\n", os.Getpid(), stop, extra)
}

func serveUntilSignal(srv *server.Server) error {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()
	parentDone := make(chan struct{})
	if os.Getenv("TRIALM_PARENT_PIPE") == "1" {
		go func() { _, _ = io.Copy(io.Discard, os.Stdin); close(parentDone) }()
	}
	interrupted := false
	select {
	case <-parentDone:
	case sig := <-sigs:
		interrupted = sig == os.Interrupt
	case <-commandContext.Done():
		interrupted = true
	case err := <-errc:
		if err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	fmt.Fprintln(out())
	fmt.Fprint(out(), srv.Summary())
	if interrupted {
		return context.Canceled
	}
	return nil
}

// ---------------------------------------------------------------- background

// backgroundable commands accept --background: trialm re-runs itself fully
// detached (own session / process group, no terminal), logs to a file, and
// returns only after the server answers — a printed pid or a shell exit is
// not proof that it is running (agent command runners kill the process
// group of `nohup … &` jobs when the command returns).
var backgroundable = map[string]bool{"try": true, "serve": true, "bridge": true}

// takeFlag removes a boolean flag or a flag with a value from args.
func takeFlag(args []string, name string, hasValue bool) ([]string, string, bool) {
	var out []string
	val, found := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, p := range []string{"--" + name, "-" + name} {
			switch {
			case a == p:
				found = true
				if hasValue && i+1 < len(args) {
					val = args[i+1]
					i++
				}
				goto next
			case strings.HasPrefix(a, p+"="):
				found, val = true, a[len(p)+1:]
				goto next
			}
		}
		out = append(out, a)
	next:
	}
	return out, val, found
}

var endpointLine = regexp.MustCompile(`Endpoints on (http://127\.0\.0\.1:\d+)`)

// startBackground runs `trialm <cmd> <args>` detached and waits until it
// serves, then prints its output so far.
func startBackground(cmd string, args []string, logPath string) error {
	if logPath == "" {
		dir := filepath.Join(trialmHome(), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		logPath = filepath.Join(dir, cmd+".log")
	}
	return detach(append([]string{cmd}, args...), logPath, "", 90*time.Second, true)
}

// detach starts argv (argv[0] = a trialm subcommand when self, else a
// program) with output to logPath, then waits for readiness: readyURL
// answering 2xx, or (self) the server's own "Endpoints on" line plus its
// /health. It returns an error, with the log tail, when the process exits
// or is not ready in time.
func detach(argv []string, logPath, readyURL string, timeout time.Duration, self bool) error {
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	defer logf.Close()
	var c *exec.Cmd
	if self {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		c = exec.Command(exe, argv...)
		c.Env = append(os.Environ(), "TRIALM_DETACHED=1", "CYRLM_DETACHED=1")
	} else {
		path, err := exec.LookPath(argv[0])
		if err != nil {
			return fmt.Errorf("%s: not found", argv[0])
		}
		c = exec.Command(path, argv[1:]...)
	}
	c.Stdin, c.Stdout, c.Stderr = nil, logf, logf
	detachAttrs(c)
	if err := c.Start(); err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-exited:
			return fmt.Errorf("it stopped right away (%v). Log %s:\n%s", err, logPath, tail(logPath, 30))
		case <-time.After(300 * time.Millisecond):
		}
		url := readyURL
		if self {
			m := endpointLine.FindStringSubmatch(readFile(logPath))
			if m == nil {
				continue
			}
			url = m[1] + "/health"
		}
		if ok(url) {
			if self {
				fmt.Fprint(out(), readFile(logPath))
			}
			stop := fmt.Sprintf("kill %d", c.Process.Pid)
			if goruntime.GOOS == "windows" {
				stop = fmt.Sprintf("Stop-Process -Id %d", c.Process.Pid)
			}
			fmt.Fprintf(out(), "✓ Started in the background and answering at %s (pid %d). Log: %s. Stop: %s\n", strings.TrimSuffix(url, "/health"), c.Process.Pid, logPath, stop)
			return nil
		}
	}
	return fmt.Errorf("not ready after %s (pid %d still running). Log %s:\n%s", timeout, c.Process.Pid, logPath, tail(logPath, 30))
}

func ok(url string) bool {
	if url == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func readFile(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func tail(p string, n int) string {
	var lines []string
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return strings.Join(lines, "\n")
}

// cmdBg runs any long-lived program (e.g. llama-server) the same way:
// detached, logged, and confirmed ready by a URL before returning.
//
//	trialm bg --log ~/.trialm/logs/llama.log --ready http://127.0.0.1:8080/health -- llama-server …
func cmdBg(args []string) error {
	fs := flag.NewFlagSet("bg", flag.ContinueOnError)
	logPath := fs.String("log", "", "log file (required)")
	ready := fs.String("ready", "", "URL that answers 2xx once the program is ready (required)")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for --ready (model loads can be slow)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if *logPath == "" || *ready == "" || len(argv) == 0 {
		return errors.New("usage: trialm bg --log FILE --ready URL [--timeout 10m] -- program args…")
	}
	if strings.HasPrefix(*logPath, "~/") {
		home, _ := os.UserHomeDir()
		*logPath = filepath.Join(home, (*logPath)[2:])
	}
	_ = os.MkdirAll(filepath.Dir(*logPath), 0o755)
	if ok(*ready) {
		return fmt.Errorf("something already answers at %s: stop it first or use another port", *ready)
	}
	return detach(argv, *logPath, *ready, *timeout, false)
}
