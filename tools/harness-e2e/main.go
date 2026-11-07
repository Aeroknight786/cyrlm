// Command harness-e2e exercises the real headless CLIs through both triaLM paths.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type spec struct{ id, pkg, version, binary, tool, extra string }

var specs = []spec{
	{"claude-code", "@anthropic-ai/claude-code", "2.1.319", "claude", "Bash", `-p "$TASK" --allowedTools Bash --permission-mode acceptEdits`},
	{"codex", "@openai/codex", "0.167.0", "codex", "shell", `exec --skip-git-repo-check --sandbox workspace-write -c approval_policy='"never"' "$TASK"`},
	{"opencode", "opencode-ai", "1.19.5", "opencode", "bash", `run "$TASK"`},
	{"aider", "aider-chat", "0.86.2", "aider", "aider", `--message "$TASK" --yes-always --no-git --no-check-update --no-show-release-notes --no-show-model-warnings --analytics-disable --auto-test --test-cmd 'test -f .ran || { touch .ran; echo "FAIL: first run"; false; }' tool.txt`},
	{"qwen-code", "@qwen-code/qwen-code", "0.26.9", "qwen", "run_shell_command", `--approval-mode auto-edit --allowed-tools run_shell_command -p "$TASK"`},
	{"cline", "cline", "3.0.74", "cline", "run_commands", `--json -c "$PWD" "$TASK"`},
	{"continue", "@continuedev/cli", "1.5.47", "cn", "Write", `-p "$TASK" --allow Write`},
}

type result struct {
	Harness string `json:"harness"`
	Path    string `json:"path"`
	Pass    bool   `json:"pass"`
	Reason  string `json:"reason"`
}

var commandRE = regexp.MustCompile(`(?m)^    (.+)$`)
var pidRE = regexp.MustCompile(`(?:Stop: kill |\(pid )(\d+)`)

const greetingTask = "Reply to HARNESS_E2E_GREETING"

func toolFailure(id string, requests []byte) string {
	if id == "codex" && strings.Contains(string(requests), "sandbox-exec: sandbox_apply: Operation not permitted") {
		return "Codex shell blocked: sandbox-exec: sandbox_apply: Operation not permitted (nested macOS Seatbelt; rerun outside the outer sandbox)"
	}
	return "tool.txt does not contain tool-ok"
}

func port() (int, error) {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return 0, e
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
func env(root, bin string) []string {
	return []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + root + "/home", "XDG_CONFIG_HOME=" + root + "/config", "XDG_DATA_HOME=" + root + "/data", "XDG_CACHE_HOME=" + root + "/cache", "XDG_STATE_HOME=" + root + "/state", "CLAUDE_CONFIG_DIR=" + root + "/claude", "CODEX_HOME=" + root + "/codex", "QWEN_HOME=" + root + "/qwen", "CONTINUE_GLOBAL_DIR=" + root + "/continue", "CLINE_DIR=" + root + "/cline", "TRIALM_HOME=" + root + "/trialm", "TMPDIR=" + root + "/tmp", "TERM=xterm", "OPENCODE_DISABLE_MODELS_FETCH=1", "CONTINUE_CLI_ENABLE_TELEMETRY=0", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "UV_TOOL_DIR=" + root + "/uv-tools", "UV_TOOL_BIN_DIR=" + bin, "UV_PYTHON_INSTALL_DIR=" + root + "/uv-python", "TASK=create tool.txt containing tool-ok using your shell tool"}
}
func run(ctx context.Context, dir string, environment []string, log string, name string, args ...string) error {
	c := exec.CommandContext(ctx, name, args...)
	c.Dir = dir
	c.Env = environment
	c.Stdin = strings.NewReader("")
	f, e := os.Create(log)
	if e != nil {
		return e
	}
	defer f.Close()
	c.Stdout = f
	c.Stderr = f
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error { return syscall.Kill(-c.Process.Pid, syscall.SIGKILL) }
	c.WaitDelay = 3 * time.Second
	err := c.Run()
	if c.Process != nil {
		_ = syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	return err
}
func setup(root string) error {
	for _, d := range []string{"home", "config", "data", "cache", "state", "claude", "codex", "qwen", "continue", "cline", "trialm", "tmp", "work", "bin"} {
		if e := os.MkdirAll(filepath.Join(root, d), 0700); e != nil {
			return e
		}
	}
	return os.WriteFile(filepath.Join(root, "codex/config.toml"), []byte("[notice]\nhide_full_access_warning = true\n"), 0600)
}
func extract(out string) (string, string) {
	i := strings.Index(out, "── Connect ")
	if i < 0 {
		return "", ""
	}
	s := out[i:]
	cmd := ""
	if n := strings.Index(s, "Run in a new terminal window:"); n >= 0 {
		if m := commandRE.FindStringSubmatch(s[n:]); len(m) > 1 {
			cmd = m[1]
		}
	}
	config := ""
	if n := strings.Index(s, "Add to "); n >= 0 {
		p := strings.Index(s[n:], "\n\n")
		if p >= 0 {
			lines := strings.Split(s[n+p+2:], "\n")
			var b []string
			for _, l := range lines {
				if !strings.HasPrefix(l, "    ") {
					break
				}
				b = append(b, strings.TrimPrefix(l, "    "))
			}
			config = strings.Join(b, "\n")
		}
	}
	return cmd, config
}
func main() { os.Exit(realMain()) }

func realMain() int {
	only := flag.String("only", "", "run one harness")
	keep := flag.Bool("keep", false, "keep scratch directories")
	latest := flag.Bool("latest", false, "install latest versions")
	output := flag.String("output", "harness-e2e-logs", "summary and logs directory")
	flag.Parse()
	out, _ := filepath.Abs(*output)
	if e := os.MkdirAll(out, 0700); e != nil {
		panic(e)
	}
	scratch, e := os.MkdirTemp("", "trialm-harness-e2e-")
	if e != nil {
		panic(e)
	}
	if !*keep {
		defer os.RemoveAll(scratch)
	} else {
		fmt.Fprintln(os.Stderr, "Scratch:", scratch)
	}
	repo, e := os.Getwd()
	if e != nil {
		panic(e)
	}
	bin := filepath.Join(scratch, "trialm-bin")
	if e = run(context.Background(), repo, os.Environ(), filepath.Join(out, "build.log"), "go", "build", "-o", bin, "./cmd/trialm"); e != nil {
		fmt.Fprintln(os.Stderr, "build failed:", e)
		return 1
	}
	var results []result
	found := false
	for _, s := range specs {
		if *only != "" && *only != s.id {
			continue
		}
		found = true
		root := filepath.Join(scratch, s.id)
		_ = setup(root)
		logs := filepath.Join(out, s.id)
		_ = os.MkdirAll(logs, 0700)
		en := env(root, filepath.Join(root, "node_modules/.bin"))
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		version := s.version
		if *latest {
			version = "latest"
		}
		var err error
		if s.id == "aider" {
			requirement := s.pkg
			if !*latest {
				requirement += "==" + version
			}
			err = run(ctx, root, en, filepath.Join(logs, "install.log"), "uv", "tool", "install", "--python", "3.12", requirement)
		} else {
			err = run(ctx, root, en, filepath.Join(logs, "install.log"), "npm", "install", "--no-audit", "--no-fund", "--prefix", root, s.pkg+"@"+version)
		}
		cancel()
		if s.id == "cline" {
			_ = os.Symlink("../cline/bin/cline", filepath.Join(root, "node_modules/.bin/cline"))
		}
		if err == nil {
			helpCtx, helpCancel := context.WithTimeout(context.Background(), time.Minute)
			cli := filepath.Join(root, "node_modules/.bin", s.binary)
			err = run(helpCtx, root, en, filepath.Join(logs, "help.log"), cli, "--help")
			if err == nil && s.id == "codex" {
				err = run(helpCtx, root, en, filepath.Join(logs, "exec-help.log"), cli, "exec", "--help")
			}
			helpCancel()
		}
		for _, path := range []string{"trial", "bridge"} {
			r := result{Harness: s.id, Path: path}
			if err != nil {
				r.Reason = "CLI installation failed: " + err.Error()
			} else {
				r.Reason = exercise(repo, root, logs, bin, en, s, path)
				r.Pass = r.Reason == ""
				if r.Pass {
					r.Reason = "tool edit and follow-up received"
				}
			}
			results = append(results, r)
			status := "FAIL"
			if r.Pass {
				status = "PASS"
			}
			fmt.Printf("%s %s %s: %s\n", status, s.id, path, r.Reason)
		}
	}
	if !found {
		fmt.Fprintln(os.Stderr, "unknown harness:", *only)
		return 2
	}
	b, _ := json.MarshalIndent(results, "", "  ")
	_ = os.WriteFile(filepath.Join(out, "summary.json"), append(b, '\n'), 0600)
	for _, r := range results {
		if !r.Pass {
			return 1
		}
	}
	return 0
}
func exercise(repo, root, logs, bin string, en []string, s spec, path string) string {
	fp, e := port()
	if e != nil {
		return e.Error()
	}
	p, e := port()
	if e != nil {
		return e.Error()
	}
	if fp == p {
		return "port allocation collision"
	}
	if s.id == "cline" {
		hub, err := port()
		if err != nil {
			return err.Error()
		}
		en = append(append([]string{}, en...), "CLINE_HUB_PORT="+strconv.Itoa(hub))
		defer func() {
			// The Cline hub detaches from the CLI; its private port identifies it.
			data, err := exec.Command("lsof", "-tiTCP:"+strconv.Itoa(hub), "-sTCP:LISTEN").Output()
			if err == nil {
				for _, word := range strings.Fields(string(data)) {
					if pid, err := strconv.Atoi(word); err == nil {
						_ = syscall.Kill(pid, syscall.SIGTERM)
					}
				}
			}
		}()
	}
	work := filepath.Join(root, "work-"+path)
	_ = os.MkdirAll(work, 0700)
	fake := exec.Command("python3", filepath.Join(repo, "tools/harness-e2e/fake_upstream.py"), strconv.Itoa(fp), filepath.Join(logs, path+"-requests.jsonl"))
	fake.Env = append(en, "FAKE_TOOL="+s.tool)
	f, _ := os.Create(filepath.Join(logs, path+"-fake.log"))
	defer f.Close()
	fake.Stdout = f
	fake.Stderr = f
	if e = fake.Start(); e != nil {
		return "fake upstream: " + e.Error()
	}
	defer func() { _ = fake.Process.Kill(); _ = fake.Wait() }()
	for i := 0; i < 100; i++ {
		c, e := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", fp), 100*time.Millisecond)
		if e == nil {
			c.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	args := []string{"bridge", "--endpoint", fmt.Sprintf("http://127.0.0.1:%d", fp), "--harness", s.id, "--background", "--port", strconv.Itoa(p)}
	if path == "trial" {
		args = []string{"try", "qwen3.6:35b-a3b", "--harness", s.id, "--upstream", fmt.Sprintf("http://127.0.0.1:%d/v1", fp), "--upstream-model", "fake", "--device", "RTX 4090", "--ram", "64", "--background", "--port", strconv.Itoa(p)}
	}
	args = append(args, "--log", filepath.Join(logs, path+"-server.log"))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	logfile := filepath.Join(logs, path+"-connect.log")
	e = run(ctx, work, en, logfile, bin, args...)
	b, _ := os.ReadFile(logfile)
	out := string(b)
	if m := pidRE.FindStringSubmatch(out); len(m) > 1 {
		pid, _ := strconv.Atoi(m[1])
		defer syscall.Kill(pid, syscall.SIGTERM)
	}
	if e != nil {
		return "trialm startup: " + e.Error()
	}
	cmd, config := extract(out)
	if config != "" && s.id == "opencode" {
		_ = os.WriteFile(filepath.Join(work, "opencode.json"), []byte(config), 0600)
	}
	if config != "" && s.id == "continue" {
		_ = os.WriteFile(filepath.Join(root, "continue/config.yaml"), []byte("name: triaLM\nversion: 0.0.1\nschema: v1\n"+config+"\n"), 0600)
	}
	if cmd == "" {
		return "trialm provides no machine-readable connect output or runnable CLI command"
	}
	if s.id == "opencode" {
		cmd = strings.Replace(cmd, "opencode ", "opencode run ", 1)
		s.extra = `"$TASK"`
	}
	greetingEnv := append([]string{}, en...)
	for i, v := range greetingEnv {
		if strings.HasPrefix(v, "TASK=") {
			// Cline treats a single word as a command name, even when quoted.
			greetingEnv[i] = "TASK=" + greetingTask
		}
	}
	greetingCtx, greetingCancel := context.WithTimeout(context.Background(), 3*time.Minute)
	greetingLog := filepath.Join(logs, path+"-greeting.log")
	greetingErr := run(greetingCtx, work, greetingEnv, greetingLog, "sh", "-c", cmd+" "+s.extra)
	greetingCancel()
	greeting, _ := os.ReadFile(greetingLog)
	if greetingErr != nil || !strings.Contains(string(greeting), "Hello from the fake upstream.") {
		return "greeting failed; see greeting log"
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel2()
	e = run(ctx2, work, en, filepath.Join(logs, path+"-cli.log"), "sh", "-c", cmd+" "+s.extra)
	if e != nil {
		return "CLI failed: " + e.Error()
	}
	file, _ := os.ReadFile(filepath.Join(work, "tool.txt"))
	if !strings.Contains(string(file), "tool-ok") {
		requests, _ := os.ReadFile(filepath.Join(logs, path+"-requests.jsonl"))
		return toolFailure(s.id, requests)
	}
	output, _ := os.ReadFile(filepath.Join(logs, path+"-cli.log"))
	if !strings.Contains(string(output), "Done: the tool ran.") {
		return "follow-up reply missing"
	}
	return ""
}
