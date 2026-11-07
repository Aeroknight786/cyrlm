//go:build unix

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/server"
	localsetup "github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/store"
)

var localModel string

func generateLocal(ctx context.Context, scratch string) error {
	var err error
	localModel, err = calib.Generate(ctx, filepath.Join(scratch, "synthetic"), calib.Probe{Shape: calib.Shapes[0], Quant: "Q4_K_M", Layers: 2})
	return err
}

// localRoute makes every scripted request prove that the detached CPU engine
// is healthy and can produce a real completion. Only protocol text/tool calls
// come from the script. Random synthetic weights have no language quality.
func localRoute(fake, endpoint string, hits *atomic.Int64) *httptest.Server {
	target, _ := url.Parse(fake)
	proxy := httputil.NewSingleHostReverseProxy(target)
	client := &http.Client{Timeout: 30 * time.Second}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			if !proc.Health(r.Context(), endpoint+"/health") {
				http.Error(w, "CPU engine is not healthy", 503)
				return
			}
			body := bytes.NewBufferString(`{"messages":[{"role":"user","content":"OK"}],"max_tokens":1,"stream":false,"temperature":0}`)
			request, _ := http.NewRequestWithContext(r.Context(), "POST", endpoint+"/v1/chat/completions", body)
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				http.Error(w, err.Error(), 502)
				return
			}
			b, _ := io.ReadAll(response.Body)
			response.Body.Close()
			var completion struct {
				Choices []json.RawMessage `json:"choices"`
				Timings map[string]any    `json:"timings"`
			}
			if response.StatusCode != 200 || json.Unmarshal(b, &completion) != nil || len(completion.Choices) == 0 || completion.Timings == nil {
				http.Error(w, "CPU engine completion missing", 502)
				return
			}
			hits.Add(1)
		}
		proxy.ServeHTTP(w, r)
	}))
}

func exerciseLocal(root, logs, bin string, en []string, s spec, fakePort int) string {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	enginePort, err := port()
	if err != nil {
		return err.Error()
	}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d", enginePort)
	var hits atomic.Int64
	route := localRoute(fmt.Sprintf("http://127.0.0.1:%d", fakePort), endpoint, &hits)
	defer route.Close()
	bridge, routePort, err := server.Local(route.URL, "synthetic-local", "CPU synthetic fixture", 65536, 0, io.Discard)
	if err != nil {
		return err.Error()
	}
	go func() { _ = bridge.Serve() }()
	defer bridge.Shutdown(context.Background())
	profile, _ := engine.Get("llamacpp")
	input := localsetup.Input{Model: catalog.Model{ID: "synthetic-local", Name: "Synthetic 2-layer Qwen3", OllamaTag: "synthetic-local"}, Quant: catalog.Quant{Name: "Q4_K_M", Format: "gguf"}, Profile: profile, Settings: engine.Settings{ContextTokens: 2048, KVCacheType: "f16", Tuned: true}, Backend: "cpu", OS: runtime.GOOS, Arch: runtime.GOARCH, LocalModel: localModel, Port: enginePort}
	if binary, err := exec.LookPath("llama-server"); err == nil {
		version, _ := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
		input.Installed = true
		input.Binary = binary
		input.InstalledVersion = string(version)
	}
	plan, err := localsetup.Build(input)
	if err != nil {
		return err.Error()
	}
	// Register the synthetic local model and its reviewed resumable plan. The
	// fixture route remains a normal local API endpoint in the persisted recipe.
	plan.Harness = s.id
	plan.ContextTokens = 65536
	plan.Endpoint = fmt.Sprintf("http://127.0.0.1:%d", routePort)
	plan.CheckEndpoint = plan.Endpoint
	plan.APIs = []string{"openai-chat", "anthropic", "responses", "ollama"}
	plan.Seal()
	state := store.Store{Dir: filepath.Join(root, "trialm")}
	if err = state.Write("last.json", []store.Recipe{{Model: localModel, Quant: "Q4_K_M", Runtime: "llamacpp", Harness: s.id}}); err != nil {
		return err.Error()
	}
	journal := localsetup.Journal{Plan: plan, State: "paused", Prompts: plan.Prompts, Steps: make([]localsetup.Record, len(plan.Steps))}
	journalPath := filepath.Join(state.Dir, "setups", plan.Slug, "journal.json")
	_ = os.MkdirAll(filepath.Dir(journalPath), 0700)
	encoded, _ := json.Marshal(journal)
	if err = os.WriteFile(journalPath, encoded, 0600); err != nil {
		return err.Error()
	}
	work := filepath.Join(root, "work-local")
	_ = os.MkdirAll(work, 0700)
	runCLI := func(log string, args ...string) error {
		return run(ctx, work, en, filepath.Join(logs, log), bin, args...)
	}
	// Engines use a detached process group. Always stop them before scratch removal.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = run(cleanup, work, en, filepath.Join(logs, "local-cleanup.log"), bin, "launch", "--stop", "--setup", plan.Slug)
	}()
	if err = runCLI("local-setup.log", "setup", "--yes="+plan.ID, "--json"); err != nil {
		return "local setup: " + err.Error()
	}
	setupLog, _ := os.ReadFile(filepath.Join(logs, "local-setup.log"))
	lines := strings.Split(strings.TrimSpace(string(setupLog)), "\n")
	var result struct {
		Type  string `json:"type"`
		Ready bool   `json:"ready"`
		Next  []struct {
			Cmd string `json:"cmd"`
		} `json:"next"`
	}
	if json.Unmarshal([]byte(lines[len(lines)-1]), &result) != nil || result.Type != "result" || !result.Ready || len(result.Next) != 1 || result.Next[0].Cmd != "trialm launch "+s.id {
		return "setup agent result/next contract failed"
	}
	if err = runCLI("local-home.log", "--json"); err != nil {
		return "ready home: " + err.Error()
	}
	home, _ := os.ReadFile(filepath.Join(logs, "local-home.log"))
	if !strings.Contains(string(home), `"ready_setups"`) || !strings.Contains(string(home), "trialm launch "+s.id) {
		return "home did not list the Ready setup and next launch"
	}
	if !proc.Health(ctx, endpoint+"/health") || hits.Load() == 0 {
		return "setup did not reach the CPU engine"
	}
	if err = state.Read(filepath.Join("setups", plan.Slug, "journal.json"), &journal); err != nil || journal.State != "ready" || journal.Prompts.Approvals != 1 || journal.Prompts.Passwords != 0 {
		return "local setup did not reach Ready with one approval"
	}
	if err = runCLI("local-stop.log", "launch", "--stop", "--setup", plan.Slug); err != nil {
		return "stop: " + err.Error()
	}
	if proc.Health(ctx, endpoint+"/health") {
		return "engine did not stop before launch"
	}
	if err = runCLI("local-print.log", "launch", s.id, "--print", "--setup", plan.Slug); err != nil || proc.Health(ctx, endpoint+"/health") {
		return "print started the engine or failed"
	}
	// Shadow only the harness executable in this isolated PATH. Launch still
	// owns environment/config wiring and the TTY hand-off to the real CLI.
	actual := filepath.Join(root, "node_modules/.bin", s.binary)
	wrapperDir := filepath.Join(root, "launch-bin")
	_ = os.MkdirAll(wrapperDir, 0700)
	extra := s.extra
	prefix := ""
	if s.id == "opencode" {
		prefix = "run "
		extra = `"$TASK"`
	}
	wrapper := "#!/bin/sh\nexec " + shellQuote(actual) + " " + prefix + `"$@" ` + extra + "\n"
	if err = os.WriteFile(filepath.Join(wrapperDir, s.binary), []byte(wrapper), 0700); err != nil {
		return err.Error()
	}
	launchEnv := append([]string{}, en...)
	for i, v := range launchEnv {
		if strings.HasPrefix(v, "PATH=") {
			launchEnv[i] = "PATH=" + wrapperDir + ":" + strings.TrimPrefix(v, "PATH=")
		}
	}
	greetingEnv := append([]string{}, launchEnv...)
	for i, v := range greetingEnv {
		if strings.HasPrefix(v, "TASK=") {
			greetingEnv[i] = "TASK=" + greetingTask
		}
	}
	before := hits.Load()
	args := []string{"launch", s.id, "--setup", plan.Slug, "--here"}
	if err = run(ctx, work, greetingEnv, filepath.Join(logs, "local-greeting.log"), bin, args...); err != nil {
		return "launched greeting: " + err.Error()
	}
	b, _ := os.ReadFile(filepath.Join(logs, "local-greeting.log"))
	if !strings.Contains(string(b), "Hello from the fake upstream.") {
		return "launched greeting missing"
	}
	if !proc.Health(ctx, endpoint+"/health") || hits.Load() <= before {
		return "launch did not start and reach the CPU engine"
	}
	before = hits.Load()
	if err = run(ctx, work, launchEnv, filepath.Join(logs, "local-cli.log"), bin, args...); err != nil {
		return "launched CLI: " + err.Error()
	}
	b, _ = os.ReadFile(filepath.Join(work, "tool.txt"))
	if !strings.Contains(string(b), "tool-ok") {
		requests, _ := os.ReadFile(filepath.Join(logs, "local-requests.jsonl"))
		return toolFailure(s.id, requests)
	}
	b, _ = os.ReadFile(filepath.Join(logs, "local-cli.log"))
	if !strings.Contains(string(b), "Done: the tool ran.") || hits.Load() <= before {
		return "launched follow-up or real CPU inference missing"
	}
	if s.id == "continue" {
		if err = runCLI("local-check.log", "launch", "--check", "--setup", plan.Slug); err != nil {
			return "check: " + err.Error()
		}
		if err = serveSnippets(ctx, work, logs, bin, en, state); err != nil {
			return err.Error()
		}
	}
	evidence := fmt.Sprintf("engine started detached, healthy, stopped, restarted by launch; %d real CPU completions; scripted greeting/tool/follow-up passed\n", hits.Load())
	_ = os.WriteFile(filepath.Join(logs, "local-engine.txt"), []byte(evidence), 0600)
	return ""
}
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func serveSnippets(ctx context.Context, work, logs, bin string, en []string, state store.Store) error {
	for _, tool := range []string{"cline", "continue", "zed"} {
		logPath := filepath.Join(logs, "local-serve-"+tool+".log")
		log, err := os.Create(logPath)
		if err != nil {
			return err
		}
		cmd := exec.CommandContext(ctx, bin, "launch", "--serve", tool)
		cmd.Dir = work
		cmd.Env = en
		cmd.Stdout = log
		cmd.Stderr = log
		if err = cmd.Start(); err != nil {
			log.Close()
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		deadline := time.Now().Add(30 * time.Second)
		ready := false
		for time.Now().Before(deadline) {
			b, _ := os.ReadFile(logPath)
			if strings.Contains(string(b), "Local route") {
				ready = true
				break
			}
			select {
			case err = <-done:
				log.Close()
				return fmt.Errorf("serve %s exited early: %v", tool, err)
			default:
			}
			time.Sleep(50 * time.Millisecond)
		}
		var validationErr error
		if ready && tool == "continue" {
			validationErr = run(ctx, work, en, filepath.Join(logs, "local-ide-snippet.log"), filepath.Join(filepath.Dir(state.Dir), "node_modules/.bin/cn"), "--config", filepath.Join(state.Dir, "harness", "continue", "ide.config.yaml"), "-p", greetingTask)
		}
		_ = cmd.Process.Signal(os.Interrupt)
		<-done
		log.Close()
		if validationErr != nil {
			return fmt.Errorf("Continue IDE snippet: %w", validationErr)
		}
		if !ready {
			return fmt.Errorf("serve %s never printed its snippet", tool)
		}
		snippet := filepath.Join(state.Dir, "harness", tool, "extension.json")
		if tool == "continue" {
			snippet = filepath.Join(state.Dir, "harness", tool, "ide.config.yaml")
		}
		if tool == "zed" {
			snippet = filepath.Join(state.Dir, "harness", tool, "settings.json")
		}
		b, err := os.ReadFile(snippet)
		var cfg map[string]any
		if err != nil || json.Unmarshal(b, &cfg) != nil {
			return fmt.Errorf("serve %s invalid snippet", tool)
		}
	}
	return nil
}
