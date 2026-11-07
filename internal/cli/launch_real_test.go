package cli

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/verify"
)

func TestRealLocalLaunch(t *testing.T) {
	if os.Getenv("TRIALM_REAL_LAUNCH") != "1" {
		t.Skip("set TRIALM_REAL_LAUNCH=1 for a real CPU synthetic launch")
	}
	binary, err := exec.LookPath("llama-server")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("TRIALM_HOME", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	model, err := calib.Generate(ctx, filepath.Join(root, "synthetic"), calib.Probe{Shape: calib.Shapes[0], Quant: "Q4_K_M", Layers: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(model)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	version, _ := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	profile, _ := engine.Get("llamacpp")
	plan, err := setup.Build(setup.Input{Model: catalog.Model{ID: "synthetic-local", Name: "CPU synthetic", OllamaTag: "synthetic"}, Quant: catalog.Quant{Name: "Q4_K_M", Format: "gguf"}, Profile: profile, Settings: engine.Settings{ContextTokens: 2048, KVCacheType: "f16"}, Backend: "cpu", OS: runtime.GOOS, Arch: runtime.GOARCH, LocalModel: model, Port: port, Installed: true, Binary: binary, InstalledVersion: string(version)})
	if err != nil {
		t.Fatal(err)
	}
	// Bound random-language compatibility replies at the engine as well as
	// each verification request, keeping this opt-in CPU smoke short.
	for i := range plan.Steps {
		if plan.Steps[i].Kind == setup.Start {
			plan.Steps[i].Argv = append(plan.Steps[i].Argv, "--n-predict", "32")
		}
	}
	plan.Seal()
	executor := setup.Executor{Root: root, Home: filepath.Join(root, "home"), Ready: func(ctx context.Context, o verify.ReadyOptions) (verify.Report, error) {
		o.Harness = nil
		return verify.Ready(ctx, o)
	}}
	journal, err := executor.Apply(ctx, plan, setup.Policy{PlanID: plan.ID})
	if err != nil || journal.State != "ready" {
		t.Fatalf("%s %v", journal.State, err)
	}
	t.Cleanup(func() {
		for _, r := range journal.Steps {
			if r.Inverse.PIDFile != "" {
				_ = proc.Stop(r.Inverse.PIDFile)
			}
		}
	})
	out, code := launchChild(t, root, "launch", "continue", "--print")
	if code != 0 || !strings.Contains(out, "cn --config") {
		t.Fatalf("print %d %s", code, out)
	}
	out, code = launchChild(t, root, "launch", "--stop")
	if code != 0 || proc.Health(ctx, plan.Endpoint+"/health") {
		t.Fatalf("stop %d %s", code, out)
	}
	// Synthetic weights produce random language. Check must report the failing
	// compatibility smoke after real speed measurements, never fabricate Ready.
	out, code = launchChild(t, root, "launch", "continue", "--check")
	if code != 4 || !strings.Contains(out, "warmup") || !strings.Contains(out, "harness") {
		t.Fatalf("check %d %s", code, out)
	}
	t.Logf("real CPU --check returned 4 with the expected synthetic-language harness failure\n%s", out)
	exe, _ := os.Executable()
	raw, _ := json.Marshal([]string{"launch", "--serve", "continue"})
	cmd := exec.CommandContext(ctx, exe)
	cmd.Env = append(os.Environ(), goldenChildEnv+"="+string(raw))
	logPath := filepath.Join(root, "serve.log")
	log, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout = log
	cmd.Stderr = log
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	ready := false
	for i := 0; i < 100; i++ {
		b, _ := os.ReadFile(logPath)
		if strings.Contains(string(b), "Local route") {
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = cmd.Process.Signal(os.Interrupt)
	<-done
	if !ready {
		b, _ := os.ReadFile(logPath)
		t.Fatalf("serve never ready: %s", b)
	}
	b, err := os.ReadFile(filepath.Join(root, "harness", "continue", "ide.config.yaml"))
	var snippet map[string]any
	if err != nil || json.Unmarshal(b, &snippet) != nil {
		t.Fatalf("snippet %s %v", b, err)
	}
	if !proc.Health(ctx, plan.Endpoint+"/health") {
		t.Fatal("engine did not stay warm")
	}
	out, code = launchChild(t, root, "launch", "--stop")
	if code != 0 {
		t.Fatalf("final stop %d %s", code, out)
	}
	t.Log("--print, --stop, --check, --serve continue, warm retention and final stop used a real CPU llama-server")
}
