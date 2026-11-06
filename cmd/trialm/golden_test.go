package main

// Characterisation ("golden") tests: the CLI's plain and --json output on six
// what-if machines. They pin today's behaviour so the refactor can move logic
// out of package main safely. Regenerate after an intended change with
//
//	go test ./cmd/... -run TestGolden -update
//
// and review the diff of testdata/golden before committing it.

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/harness"
)

var update = flag.Bool("update", false, "rewrite the golden files")

const goldenChildEnv = "GOLDEN_CHILD_ARGS"

func TestMain(m *testing.M) {
	// Child mode: run the real main() with the given arguments, so the golden
	// test exercises exactly what users run, without building a binary.
	if raw := os.Getenv(goldenChildEnv); raw != "" {
		var args []string
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			panic(err)
		}
		os.Args = append([]string{"trialm"}, args...)
		// Output must not depend on which coding tools this machine has.
		harness.KnownBinaries = nil
		main()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

type goldenFixture struct {
	name   string
	device string
	ram    string
}

// The six machine fixtures from docs/PLAN.md §9.
var goldenFixtures = []goldenFixture{
	{"m5-24", "Apple M5 10c", "24"},
	{"3090-128", "RTX 3090", "128"},
	{"4090-64", "RTX 4090", "64"},
	{"7900xt-64", "RX 7900 XT", "64"},
	{"5070-64", "RTX 5070", "64"},
	{"cpu-32", "cpu", "32"},
}

type goldenCmd struct {
	name string
	args []string
}

var goldenCmds = []goldenCmd{
	{"detect.json", []string{"detect", "--json"}},
	{"recommend.json", []string{"recommend", "--json"}},
	{"recommend.txt", []string{"recommend"}},
	{"recommend-codex.json", []string{"recommend", "--harness", "codex", "--json"}},
	{"predict.json", []string{"predict", "qwen3.6:35b-a3b", "--json"}},
	{"predict.txt", []string{"predict", "qwen3.6:35b-a3b"}},
	{"plan.json", []string{"plan", "qwen3.6:35b-a3b", "--harness", "codex", "--json"}},
	{"plan.txt", []string{"plan", "qwen3.6:35b-a3b", "--harness", "codex"}},
}

func TestGolden(t *testing.T) {
	if goruntime.GOOS == "windows" {
		t.Skip("goldens are recorded with POSIX paths and harness snippets")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, fx := range goldenFixtures {
		for _, gc := range goldenCmds {
			t.Run(fx.name+"/"+gc.name, func(t *testing.T) {
				t.Parallel()
				home := t.TempDir()
				args := append(append([]string{}, gc.args...), "--device", fx.device, "--ram", fx.ram)
				raw, _ := json.Marshal(args)
				cmd := exec.Command(exe)
				cmd.Env = goldenEnv(home, t.TempDir(), string(raw))
				var stdout, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &stdout, &stderr
				runErr := cmd.Run()
				got := stdout.String()
				if runErr != nil {
					got += "\n[exit: " + runErr.Error() + "]\n[stderr]\n" + stderr.String()
				}
				got = strings.ReplaceAll(got, home, "$HOME")
				path := filepath.Join("testdata", "golden", fx.name, gc.name)
				if *update {
					if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
						t.Fatal(err)
					}
					return
				}
				want, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("missing golden %s (run with -update): %v", path, err)
				}
				if got != string(want) {
					t.Errorf("%s differs from the golden file; rerun with -update and review the diff.\n--- got ---\n%s", path, got)
				}
			})
		}
	}
}

// goldenEnv is a minimal, deterministic environment: a fresh HOME, an empty
// PATH (so no installed tool is found), no keys, no colour, no parent agent.
func goldenEnv(home, emptyPath, args string) []string {
	env := []string{
		"HOME=" + home,
		"USERPROFILE=" + home,
		"PATH=" + emptyPath,
		"NO_COLOR=1",
		"TERM=dumb",
		goldenChildEnv + "=" + args,
	}
	if tmp := os.Getenv("TMPDIR"); tmp != "" {
		env = append(env, "TMPDIR="+tmp)
	}
	return env
}
