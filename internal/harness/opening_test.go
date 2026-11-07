package harness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/aeroknight786/trialm/internal/store"
)

func TestOpeningDefaultAndMedian(t *testing.T) {
	root := t.TempDir()
	t.Setenv("TRIALM_HOME", root)
	cc, _ := Get("claude-code")
	codex, _ := Get("codex")
	if cc.Scenario.BasePromptTokens != 20000 || codex.Scenario.BasePromptTokens != 9000 {
		t.Fatal(cc.Scenario, codex.Scenario)
	}
	for _, n := range []int{20200, 41000, 47000} {
		if e := RecordOpeningPrompt(cc.ID, n); e != nil {
			t.Fatal(e)
		}
	}
	measured := OpeningScenario(cc.Scenario, "darwin")
	if measured.BasePromptTokens != 41000 {
		t.Fatal(measured)
	}
	for _, goos := range []string{"linux", "windows"} {
		if got := OpeningScenario(cc.Scenario, goos); got != cc.Scenario {
			t.Fatal(goos, got)
		}
	}
	if got := OpeningScenario(codex.Scenario, "darwin"); got != codex.Scenario {
		t.Fatal(got)
	}
	if runtime.GOOS == "darwin" {
		alias, _ := Get("claude")
		if alias.Scenario != measured {
			t.Fatal("Get did not select evidence", alias.Scenario)
		}
	}
	for n := 1; n <= 12; n++ {
		if e := RecordOpeningPrompt(cc.ID, n*1000); e != nil {
			t.Fatal(e)
		}
	}
	var samples map[string][]int
	if e := (store.Store{Dir: root}).Read(openingFile, &samples); e != nil {
		t.Fatal(e)
	}
	if len(samples[cc.ID]) != 10 || samples[cc.ID][0] != 3000 || OpeningScenario(cc.Scenario, "darwin").BasePromptTokens != 7500 {
		t.Fatal(samples)
	}
	for _, invalid := range []string{`invalid`, `null`, `{"claude-code":[0,-1]}`} {
		if e := os.WriteFile(filepath.Join(root, openingFile), []byte(invalid), 0600); e != nil {
			t.Fatal(e)
		}
		if got := OpeningScenario(cc.Scenario, "darwin"); got != cc.Scenario {
			t.Fatal(got)
		}
	}
}

func TestOpeningConcurrentCounts(t *testing.T) {
	t.Setenv("TRIALM_HOME", t.TempDir())
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			if e := RecordOpeningPrompt("codex", 9000); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	var samples map[string][]int
	if e := store.Default().Read(openingFile, &samples); e != nil {
		t.Fatal(e)
	}
	if len(samples["codex"]) != 10 {
		t.Fatal(samples)
	}
}

func TestOpeningProcessHelper(t *testing.T) {
	id := os.Getenv("TRIALM_OPENING_HELPER")
	if id == "" {
		return
	}
	for range 10 {
		if err := RecordOpeningPrompt(id, 20000); err != nil {
			t.Fatal(err)
		}
	}
}
func TestOpeningConcurrentBridges(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Mac bridge observation only")
	}
	t.Setenv("TRIALM_HOME", t.TempDir())
	commands := []*exec.Cmd{}
	for i := range 4 {
		cmd := exec.Command(os.Args[0], "-test.run=^TestOpeningProcessHelper$", "-test.count=1")
		cmd.Env = append(os.Environ(), fmt.Sprintf("TRIALM_OPENING_HELPER=bridge-%d", i))
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal(err)
		}
	}
	var counts map[string][]int
	if err := store.Default().Read(openingFile, &counts); err != nil {
		t.Fatal(err)
	}
	if len(counts) != 4 {
		t.Fatal(counts)
	}
	for _, ns := range counts {
		if len(ns) != 10 {
			t.Fatal(counts)
		}
	}
}
