package server

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/aeroknight786/trialm/internal/api"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/paths"
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
func CommandBridge(commandContext context.Context, output io.Writer, args []string) error {
	fs := flag.NewFlagSet("bridge", flag.ContinueOnError)
	rt := fs.String("runtime", "", "runtime to connect to: llamacpp | ollama | lmstudio | mlx-lm (default: the first one answering)")
	endpoint := fs.String("endpoint", "", "runtime base URL, e.g. http://127.0.0.1:8080")
	model := fs.String("model", "", "model name as the runtime knows it (default: the runtime's first model)")
	hname := fs.String("harness", "", "print connection steps for this coding tool")
	ctxTokens := fs.Int("ctx", 0, "the runtime's context window, so the harness compacts in time (default: read from llama.cpp)")
	port := fs.Int("port", 11535, "listen port (falls back to the next free port)")
	logFile := fs.String("log", "", "write request logs to this file (also works in the foreground)")
	keyEnv := fs.String("key-env", "TRIALM_UPSTREAM_API_KEY", "env var with an API key for the runtime, if it needs one")
	err := fs.Parse(args)
	if err != nil {
		return err
	}
	pos := fs.Args()
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
	logW := output
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
	srv := New(Config{
		Bridge: true, Harness: hh.ID, Upstream: client, RuntimeName: rtName, Log: logW,
		Model: api.ModelInfo{Name: *model, ContextLength: *ctxTokens, ModifiedAt: time.Now(), Capabilities: []string{"completion", "tools"}},
	})
	bound, err := srv.Listen("127.0.0.1", *port, true)
	if err != nil {
		return fmt.Errorf("cannot listen on 127.0.0.1:%d: %w", *port, err)
	}
	fmt.Fprintf(output, "\ntrialm bridge → %s at %s, model %s", rtName, base, *model)
	if *ctxTokens > 0 {
		fmt.Fprintf(output, ", context %d tokens", *ctxTokens)
	}
	fmt.Fprintln(output)
	fmt.Fprintf(output, "  Endpoints on http://127.0.0.1:%d — OpenAI (/v1/chat/completions, /v1/responses), Anthropic (/v1/messages), Ollama (/api/*)\n", bound)
	fmt.Fprintln(output, "  Real local inference, no simulation. Requests are reshaped for the model's chat template; runtime errors are shown, not retried.")
	fmt.Fprintln(output)
	if hh.ID != "" {
		setup := hh.SetupFor(harness.Options{Port: bound, Model: *model, Tuned: true, ContextTokens: *ctxTokens, Protocol: "openai-chat", Bridge: true})
		if err := setup.WriteFiles(); err != nil {
			_ = srv.Shutdown(context.Background())
			return err
		}
		fmt.Fprintf(output, "── Connect %s ─────────────────────────────────────────\n\n%s\n", hh.Name, setup.Render())
	}
	PrintRunning(output, "")
	return Wait(commandContext, output, srv)
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

func PrintRunning(output io.Writer, extra string) {
	if os.Getenv("TRIALM_DETACHED") == "1" || os.Getenv("CYRLM_DETACHED") == "1" {
		return
	}
	stop := fmt.Sprintf("kill %d", os.Getpid())
	if goruntime.GOOS == "windows" {
		stop = fmt.Sprintf("Stop-Process -Id %d", os.Getpid())
	}
	fmt.Fprintf(output, "Running (pid %d). Stop with Ctrl-C, or from another terminal: %s%s\n\n", os.Getpid(), stop, extra)
}

func Wait(commandContext context.Context, output io.Writer, srv *Server) error {
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
	fmt.Fprintln(output)
	fmt.Fprint(output, srv.Summary())
	if interrupted {
		return context.Canceled
	}
	return nil
}
