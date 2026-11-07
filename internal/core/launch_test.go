package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/verify"
)

func TestLaunchEngineHelper(t *testing.T) {
	if os.Getenv("TRIALM_LAUNCH_TEST_ENGINE") != "1" {
		return
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			fmt.Fprint(w, `{"status":"ok"}`)
			return
		}
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[{"id":"local-model"}]}`)
			return
		}
		var request map[string]any
		_ = json.NewDecoder(r.Body).Decode(&request)
		if request["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"OK\"}}]}\n\ndata: [DONE]\n\n")
			return
		}
		fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"OK"}}],"usage":{"prompt_tokens":10,"completion_tokens":1}}`)
	})
	if err := http.ListenAndServe(os.Getenv("TRIALM_LAUNCH_TEST_ADDR"), nil); err != nil {
		t.Fatal(err)
	}
}
func launchJournal(t *testing.T) (Session, *setup.Executor, setup.Journal) {
	t.Helper()
	root := t.TempDir()
	t.Setenv("TRIALM_HOME", root)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := Session{}
	e, err := s.SetupExecutor()
	if err != nil {
		t.Fatal(err)
	}
	exe, _ := os.Executable()
	binary := filepath.Join(root, "engines", "llama-server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	_ = os.MkdirAll(filepath.Dir(binary), 0700)
	b, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(binary, b, 0700); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	endpoint := "http://" + addr
	p := setup.Plan{Slug: "local", Model: "local-model", ModelName: "Local model", Runtime: "llamacpp", RuntimeName: "llama.cpp", OS: runtime.GOOS, Arch: runtime.GOARCH, Harness: "codex", LocalModelName: "local-model", Endpoint: endpoint, ContextTokens: 2048,
		Steps: []setup.Step{{ID: "server", Kind: setup.Start, Argv: []string{binary, "-test.run=^TestLaunchEngineHelper$"}, Env: []string{"TRIALM_LAUNCH_TEST_ENGINE=1", "TRIALM_LAUNCH_TEST_ADDR=" + addr}, Health: endpoint + "/health"}, {ID: "ready", Kind: setup.Check}}}
	p.Seal()
	pid := filepath.Join(root, "run", "local-server.pid")
	j := setup.Journal{Plan: p, State: "ready", Report: &verify.Report{Ready: true}, Steps: []setup.Record{{State: "done", Inverse: setup.Inverse{PIDFile: pid}}, {State: "done"}}}
	path := filepath.Join(root, "setups", p.Slug, "journal.json")
	_ = os.MkdirAll(filepath.Dir(path), 0700)
	raw, _ := json.Marshal(j)
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Stop(pid) })
	return s, e, j
}
func TestLaunchPrintStartReuseBridgeStop(t *testing.T) {
	s, e, j := launchJournal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	printed, err := s.Launch(ctx, LaunchOptions{Tool: "codex", Print: true})
	if err != nil || printed.Bridge != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(j.Steps[0].Inverse.PIDFile); !os.IsNotExist(err) {
		t.Fatal("print started engine")
	}
	if !strings.Contains(strings.Join(printed.Wiring.Command, " "), "wire_api=responses") {
		t.Fatal(printed.Wiring.Command)
	}
	started, err := s.Launch(ctx, LaunchOptions{Tool: "codex", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer started.Bridge.Shutdown(context.Background())
	record, ok := proc.Status(ctx, j.Steps[0].Inverse.PIDFile)
	if !ok {
		t.Fatal("not healthy")
	}
	resp, err := http.Post(started.Endpoint+"/v1/responses", "application/json", strings.NewReader(`{"model":"local-model","input":"hi","stream":false}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "OK") {
		t.Fatalf("%s", body)
	}
	reused, err := s.Launch(ctx, LaunchOptions{Tool: "opencode", Log: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if reused.Bridge != nil {
		t.Fatal("unneeded bridge")
	}
	after, ok := proc.Status(ctx, j.Steps[0].Inverse.PIDFile)
	if !ok || after.PID != record.PID {
		t.Fatal("engine restarted")
	}
	if _, err = s.Launch(ctx, LaunchOptions{Stop: true}); err != nil {
		t.Fatal(err)
	}
	if e.Running(ctx, j)["server"] {
		t.Fatal("still running")
	}
}
func TestIdleStopGenerationAndConcurrentLease(t *testing.T) {
	s, e, j := launchJournal(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	a, err := e.LaunchLease(j.Plan.Slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Launch(ctx, LaunchOptions{Tool: "opencode", Log: io.Discard}); err != nil {
		t.Fatal(err)
	}
	old, err := e.LaunchLease(j.Plan.Slug, a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.LaunchLease(j.Plan.Slug, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = e.IdleStop(ctx, j.Plan.Slug, old, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !e.Running(ctx, j)["server"] {
		t.Fatal("old timer stopped new launch")
	}
	c, err := e.LaunchLease(j.Plan.Slug, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err := e.LaunchLease(j.Plan.Slug, b)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.IdleStop(ctx, j.Plan.Slug, token, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if !e.Running(ctx, j)["server"] {
		t.Fatal("timer stopped concurrent launch")
	}
	token, err = e.LaunchLease(j.Plan.Slug, c)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.IdleStop(ctx, j.Plan.Slug, token, 10*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if e.Running(ctx, j)["server"] {
		t.Fatal("idle engine did not stop")
	}
}
func TestNoReady(t *testing.T) {
	t.Setenv("TRIALM_HOME", t.TempDir())
	_, err := (Session{}).Launch(context.Background(), LaunchOptions{Print: true})
	if err != ErrNoReady {
		t.Fatal(err)
	}
}

func TestPrintCountsWiredPromptWithoutExecutingIt(t *testing.T) {
	s, _, j := launchJournal(t)
	step := setup.Step{ID: "wired-limit", Kind: setup.Admin, Argv: []string{"sudo", "sysctl", "iogpu.wired_limit_mb=20480"}, RebootReset: true}
	j.Plan.Steps = append([]setup.Step{step}, j.Plan.Steps...)
	j.Steps = append([]setup.Record{{State: "done"}}, j.Steps...)
	j.Plan.Seal()
	path := filepath.Join(os.Getenv("TRIALM_HOME"), "setups", j.Plan.Slug, "journal.json")
	b, _ := json.Marshal(j)
	_ = os.WriteFile(path, b, 0600)
	l, err := s.Launch(context.Background(), LaunchOptions{Print: true, Tool: "codex", Admin: func(context.Context, []string) error { t.Fatal("admin ran during print"); return nil }})
	if err != nil || l.Journal.Plan.Prompts.Passwords != 1 || l.Journal.Plan.Prompts.AfterRestart != "1 more after each restart" {
		t.Fatalf("%+v %v", l.Journal.Plan.Prompts, err)
	}
}

func TestIdleStopsNativeAdapter(t *testing.T) {
	_, e, j := launchJournal(t)
	marker := filepath.Join(e.Root, "native-stopped")
	j.Plan.Steps[0].Inverse = []string{j.Plan.Steps[0].Argv[0], "-test.run=^TestNativeStopHelper$"}
	j.Plan.Steps[0].Env = []string{"TRIALM_NATIVE_STOP=" + marker}
	j.Plan.Seal()
	j.Steps[0].Inverse.PIDFile = ""
	b, _ := json.Marshal(j)
	_ = os.WriteFile(filepath.Join(e.Root, "setups", j.Plan.Slug, "journal.json"), b, 0600)
	token, err := e.LaunchLease(j.Plan.Slug, "")
	if err != nil {
		t.Fatal(err)
	}
	token, err = e.LaunchLease(j.Plan.Slug, token)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.IdleStop(context.Background(), j.Plan.Slug, token, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(marker); err != nil {
		t.Fatal("native stop inverse did not run", err)
	}
}
func TestNativeStopHelper(t *testing.T) {
	if path := os.Getenv("TRIALM_NATIVE_STOP"); path != "" {
		if err := os.WriteFile(path, []byte("stopped"), 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPrintPreparesOwnedCLIConfigWithoutStarting(t *testing.T) {
	s, _, j := launchJournal(t)
	l, err := s.Launch(context.Background(), LaunchOptions{Print: true, Tool: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if len(l.Wiring.Files) != 1 {
		t.Fatal(l.Wiring.Files)
	}
	for path, want := range l.Wiring.Files {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("print command config unavailable: %v", err)
		}
		if !strings.Contains(path, filepath.Join("harness", j.Plan.Slug, "continue")) {
			t.Fatal(path)
		}
	}
	if _, err = os.Stat(j.Steps[0].Inverse.PIDFile); !os.IsNotExist(err) {
		t.Fatal("print started the engine")
	}
	if l.Bridge != nil {
		t.Fatal("print started a bridge")
	}
}
