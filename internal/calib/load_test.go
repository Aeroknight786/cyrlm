package calib

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

func TestEngineSmoke(t *testing.T) {
	if os.Getenv("TRIALM_CALIB_ENGINE_TEST") == "" {
		t.Skip("opt-in local llama-bench CPU smoke; generates synthetic files only")
	}
	home := os.Getenv("TRIALM_HOME")
	if home == "" {
		t.Fatal("scratch TRIALM_HOME required")
	}
	engine, e := FindEngine(home)
	if e != nil {
		t.Fatal(e)
	}
	for _, p := range Grid(false) {
		if p.Check || p.Layers != p.Shape.Counts[0] {
			continue
		}
		t.Run(p.ID(), func(t *testing.T) {
			path, e := Generate(context.Background(), home, p)
			if e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(engine, "-m", path, "-p", "1", "-n", "1", "-d", "0", "-r", "1", "-o", "json", "-ngl", "0", "-dev", "none", "-nopo", "1")
			b, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatalf("%v\n%s", e, b)
			}
			t.Logf("loaded and executed %s", path)
		})
	}
}
