package window

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
)

func TestMain(m *testing.M) {
	if os.Getenv("TRIALM_WINDOW_HELPER") == "1" {
		b, _ := json.Marshal(map[string]any{"args": os.Args[1:], "claude": os.Getenv("CLAUDECODE"), "entry": os.Getenv("CLAUDE_CODE_ENTRYPOINT"), "home": os.Getenv("TRIALM_HOME")})
		_ = os.WriteFile(os.Getenv("TRIALM_WINDOW_RESULT"), b, 0600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}
func TestOpeners(t *testing.T) {
	for _, tc := range []struct {
		os   string
		env  map[string]string
		want []string
	}{
		{"darwin", nil, []string{"open", "/state/run/id.command"}},
		{"windows", nil, []string{"wt", "-w", "0", "nt", "-d", "/work", "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "/state/run/id.sh"}},
		{"linux", map[string]string{"TMUX": "yes"}, []string{"tmux", "new-window", "-c", "/work", "'sh' '/state/run/id.sh'"}},
		{"linux", map[string]string{"TERMINAL": "kitty"}, []string{"kitty", "-e", "sh", "/state/run/id.sh"}},
		{"linux", nil, []string{"/bin/x-terminal-emulator", "-e", "sh", "/state/run/id.sh"}},
	} {
		t.Run(tc.os+strings.Join(tc.want, ""), func(t *testing.T) {
			got, e := Opener(tc.os, "/state/run/id.sh", "/work", func(k string) string { return tc.env[k] }, func(k string) (string, error) { return "/bin/" + k, nil })
			if e != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatal(got, e)
			}
		})
	}
	_, e := Opener("linux", "s", "d", func(string) string { return "" }, func(string) (string, error) { return "", errors.New("unavailable") })
	if e == nil {
		t.Fatal("no fallback error")
	}
}
func TestScriptQuotingPermissionsAndScrub(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX execution; PowerShell generation is tested separately")
	}
	s := store.Store{Dir: filepath.Join(t.TempDir(), "state with spaces and 'quotes'")}
	exe, _ := os.Executable()
	dir := t.TempDir()
	marker := filepath.Join(dir, "injected")
	args := []string{"model's name", "--quant", "Q4_K_M", "--model", "$(touch " + marker + ") `touch " + marker + "`\n'quote'"}
	id, path, e := Generate(s, "darwin", exe, dir, args)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range []string{path, strings.TrimSuffix(path, ".sh") + ".command"} {
		st, e := os.Stat(p)
		if e != nil || st.Mode().Perm() != 0700 {
			t.Fatal(st, e)
		}
	}
	result := filepath.Join(s.Dir, "result.json")
	cmd := exec.Command("sh", path)
	cmd.Env = append(os.Environ(), "TRIALM_WINDOW_HELPER=1", "TRIALM_WINDOW_RESULT="+result, "CLAUDECODE=yes", "CLAUDE_CODE_ENTRYPOINT=yes")
	if b, e := cmd.CombinedOutput(); e != nil {
		t.Fatal(e, string(b))
	}
	b, _ := os.ReadFile(result)
	var v struct {
		Args                []string
		Claude, Entry, Home string
	}
	if e = json.Unmarshal(b, &v); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(v.Args, append([]string{"try", "--plain", "--here"}, args...)) || v.Claude != "" || v.Entry != "" || v.Home != s.Dir {
		t.Fatal(v)
	}
	if _, e = os.Stat(marker); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("shell injection", e)
	}
	b, e = os.ReadFile(filepath.Join(s.Dir, "run", id+".exit"))
	if e != nil || string(b) != "0" {
		t.Fatal(string(b), e)
	}
}
func TestPowerShellScript(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	_, path, e := Generate(s, "windows", "C:\\triaLM's app\\trialm.exe", "C:\\my repo", []string{"hello'world", "--ctx", "65536"})
	if e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(path)
	for _, want := range []string{"'C:\\triaLM''s app\\trialm.exe'", "'hello''world'", "$LASTEXITCODE", "TRIALM_HOME", "CLAUDE_CODE_ENTRYPOINT"} {
		if !strings.Contains(string(b), want) {
			t.Fatal(want, string(b))
		}
	}
}
func TestAutomatic(t *testing.T) {
	for _, k := range []string{"CLAUDECODE", "CODEX_SANDBOX", "TRIALM_AGENT"} {
		if !Automatic(func(name string) string {
			if name == k {
				return "1"
			}
			return ""
		}, true) {
			t.Fatal(k)
		}
	}
	if !Automatic(func(string) string { return "" }, false) || Automatic(func(string) string { return "" }, true) {
		t.Fatal("TTY selection")
	}
	_ = context.Background()
}

func TestLauncherFailureAndAcknowledgement(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	l := Launcher{Platform: "linux", Getenv: func(string) string { return "" }, Lookup: func(string) (string, error) { return "", errors.New("absent") }}
	e := l.Run(context.Background(), s, []string{"qwen3:8b"}, nil)
	var need *NeedsTerminal
	if !errors.As(e, &need) || !strings.Contains(need.Run, "--here") {
		t.Fatal(e)
	}
	l.Platform = "darwin"
	l.AckTimeout = 10 * time.Millisecond
	l.Open = func([]string) error { return nil }
	e = l.Run(context.Background(), s, []string{"qwen3:8b"}, nil)
	if !errors.As(e, &need) || need.Cause == nil {
		t.Fatal(e)
	}
	l.Open = func(argv []string) error {
		script := argv[len(argv)-1]
		id := strings.TrimSuffix(filepath.Base(script), ".command")
		return os.WriteFile(filepath.Join(s.Dir, "run", id+".exit"), []byte("0"), 0600)
	}
	if e = l.Run(context.Background(), s, []string{"qwen3:8b"}, io.Discard); e != nil {
		t.Fatal(e)
	}
}

func TestClosedWindowReturnsCheckpoint(t *testing.T) {
	s := store.Store{Dir: t.TempDir()}
	l := Launcher{Platform: "darwin", AckTimeout: time.Second, Open: func(a []string) error {
		id := strings.TrimSuffix(filepath.Base(a[len(a)-1]), ".command")
		if e := s.SaveTrial(store.TrialEntry{ID: id, Incomplete: true}); e != nil {
			return e
		}
		return os.WriteFile(filepath.Join(s.Dir, "run", id+".started"), []byte("99999999"), 0600)
	}}
	if e := l.Run(context.Background(), s, []string{"fake"}, nil); e != nil {
		t.Fatal(e)
	}
}
