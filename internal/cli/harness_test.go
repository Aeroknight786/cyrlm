package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"github.com/aeroknight786/trialm/internal/server"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestCommonHarnessDetection(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	for _, name := range []string{"try", "serve", "recommend", "plan"} {
		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		var c common
		c.register(fs, "")
		c.finish(fs)
		if c.harness != "claude-code" {
			t.Errorf("%s: %s", name, c.harness)
		}
		c.harness = "aider"
		c.finish(fs)
		if c.harness != "aider" {
			t.Errorf("%s replaced explicit flag", name)
		}
	}
	fs := flag.NewFlagSet("predict", flag.ContinueOnError)
	var c common
	c.register(fs, "")
	c.finish(fs)
	if c.harness != "" {
		t.Error("unrelated command auto-detected a harness")
	}
}

func TestLaunchLogPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	now := time.Date(2026, 11, 7, 12, 13, 14, 0, time.UTC)
	if got := launchLogPath("", now); got != filepath.Join(home, "logs", "session-20261107-121314.log") {
		t.Error(got)
	}
	if got := launchLogPath("custom.log", now); got != "custom.log" {
		t.Error(got)
	}
}

func TestForegroundLogFlag(t *testing.T) {
	for _, try := range []bool{false, true} {
		if err := cmdServe([]string{"--harness", "generic", "--log", filepath.Join(t.TempDir(), "session.log")}, try); err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("log flag: %v", err)
		}
	}
	if err := server.CommandBridge(commandContext, out(), []string{"extra-argument", "--log", filepath.Join(t.TempDir(), "bridge.log")}); err == nil || !strings.Contains(err.Error(), "usage:") {
		t.Errorf("bridge log flag: %v", err)
	}
}

func TestBridgeWritesOwnedFilesBeforeConnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("graceful child shutdown uses SIGTERM")
	}
	rt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/props" {
			_, _ = w.Write([]byte(`{"default_generation_settings":{"n_ctx":65536}}`))
		} else {
			_, _ = w.Write([]byte(`{"data":[{"id":"local-model"}]}`))
		}
	}))
	defer rt.Close()
	for _, tc := range []struct{ id, rel, env string }{
		{"aider", "aider/model.metadata.json", "OPENAI_API_BASE="},
		{"qwen-code", "qwen-code/settings.json", "QWEN_CODE_SYSTEM_DEFAULTS_PATH="},
		{"cline", "cline/providers.json", "CLINE_PROVIDER_SETTINGS_PATH="},
		{"continue", "continue/config.yaml", "cn --config "},
	} {
		t.Run(tc.id, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
			t.Setenv("TRIALM_HOME", home)
			logPath := filepath.Join(home, "requests.log")
			args, _ := json.Marshal([]string{"bridge", "--endpoint", rt.URL, "--harness", tc.id, "--port", "0", "--log", logPath})
			f, err := os.Create(filepath.Join(home, "output.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			cmd := exec.Command(os.Args[0])
			cmd.Env = append(os.Environ(), goldenChildEnv+"="+string(args))
			cmd.Stdout, cmd.Stderr = f, f
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
			client := &http.Client{Timeout: 100 * time.Millisecond}
			ready := false
			var output []byte
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
				output, _ = os.ReadFile(f.Name())
				if m := endpointLine.FindSubmatch(output); len(m) > 1 {
					resp, err := client.Get(string(m[1]) + "/health")
					if err == nil {
						resp.Body.Close()
						if resp.StatusCode == http.StatusOK {
							ready = true
							break
						}
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !ready {
				t.Fatalf("bridge did not become ready:\n%s", output)
			}
			// The endpoint line precedes Connect. Read again after health
			// answers, which proves Connect and its file writes completed.
			output, err = os.ReadFile(f.Name())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(home, "harness", filepath.FromSlash(tc.rel))
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(output), tc.env) || !strings.Contains(string(output), path) {
				t.Errorf("missing printed wiring:\n%s", output)
			}
			if tc.id == "aider" && (!strings.Contains(string(content), "openai/local-model") || strings.Contains(string(output), "OLLAMA_API_BASE=")) {
				t.Error("bridge used the Ollama route")
			}
			if tc.id == "cline" && (!strings.Contains(string(content), "openai-compatible") || !strings.Contains(string(content), "65536")) {
				t.Error(string(content))
			}
			if _, err := os.Stat(logPath); err != nil {
				t.Error(err)
			}
			if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
				t.Fatal(err)
			}
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTryLaunchAiderProtocolAndOwnedFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test harness uses a POSIX script")
	}
	home := t.TempDir()
	t.Setenv("TRIALM_HOME", home)
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, "config"))
	bin := filepath.Join(home, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	capture := filepath.Join(home, "capture")
	t.Setenv("TRIALM_WIRING_CAPTURE", capture)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("OLLAMA_API_BASE", "")
	script := "#!/bin/sh\nprintf '%s\\n' \"$OPENAI_API_BASE\" \"$OLLAMA_API_BASE\" \"$@\" > \"$TRIALM_WIRING_CAPTURE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "aider"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	for _, rt := range []string{"llamacpp", "ollama"} {
		logPath := filepath.Join(home, rt+".log")
		err := cmdServe([]string{"qwen3:8b", "--runtime", rt, "--harness", "aider", "--ctx", "40960", "--upstream", "mock", "--device", "RTX 4090", "--ram", "64", "--vram", "24", "--port", "0", "--launch", "--log", logPath}, true)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(capture)
		if err != nil {
			t.Fatal(err)
		}
		output := string(raw)
		suffix, flag, model := "model.metadata.json", "--model-metadata-file", "openai/qwen3:8b-trialm"
		if rt == "ollama" {
			suffix, flag, model = "model.settings.yml", "--model-settings-file", "ollama_chat/qwen3:8b-trialm"
		}
		if !strings.Contains(output, flag) || !strings.Contains(output, model) {
			t.Errorf("%s: %s", rt, output)
		}
		path := filepath.Join(home, "harness", "aider", suffix)
		if !strings.Contains(output, path) {
			t.Errorf("missing owned file flag: %s", output)
		}
		if _, err := os.Stat(path); err != nil {
			t.Error(err)
		}
		if _, err := os.Stat(logPath); err != nil {
			t.Error(err)
		}
	}
}

func TestTryInterruptExitCode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt delivery uses POSIX signals")
	}
	home := t.TempDir()
	raw, _ := json.Marshal([]string{"try", "qwen3:8b", "--device", "RTX 4090", "--ram", "64", "--runtime", "llamacpp", "--upstream", "mock", "--serve", "--port", "0", "--json"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0])
	cmd.Env = append(os.Environ(), goldenChildEnv+"="+string(raw), "HOME="+home, "TRIALM_HOME="+home, "TRIALM_OFFLINE=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()
	sc := bufio.NewScanner(stdout)
	ready := false
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"type":"progress"`) {
			ready = true
			break
		}
	}
	if !ready {
		t.Fatal("trial did not become ready", sc.Err())
	}
	if err = cmd.Process.Signal(os.Interrupt); err != nil {
		t.Fatal(err)
	}
	// Drain the summary so a full stdout pipe cannot block process shutdown.
	_, _ = io.Copy(io.Discard, stdout)
	err = cmd.Wait()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 130 {
		t.Fatal("interrupt exit", err)
	}
}

var endpointLine = regexp.MustCompile(`Endpoints on (http://127\.0\.0\.1:\d+)`)
