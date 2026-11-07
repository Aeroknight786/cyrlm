package calib

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// A fake engine exercises Run without allocating weights or using a GPU.
func TestSuccessfulBenchHelper(t *testing.T) {
	if os.Getenv("TRIALM_SUCCESSFUL_BENCH_HELPER") == "" {
		return
	}
	values := map[string]string{}
	for i, a := range os.Args {
		if i+1 < len(os.Args) && strings.HasPrefix(a, "-") {
			values[a] = os.Args[i+1]
		}
	}
	if mode := os.Getenv("TRIALM_CHECK_TEST_MODE"); mode != "" {
		for k, v := range map[string]string{"-p": "512", "-n": "8", "-d": "0,32768", "-r": "3", "-o": "json", "-ngl": "99", "-fa": "on"} {
			if values[k] != v {
				fmt.Fprintln(os.Stderr, "incorrect check flags", k, values[k])
				os.Exit(2)
			}
		}
		if mode == "bad-json" {
			fmt.Println("bad JSON")
			os.Exit(0)
		}
	}
	if file := os.Getenv("TRIALM_BENCH_CALLS"); file != "" {
		f, e := os.OpenFile(file, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(2)
		}
		fmt.Fprintln(f, values["-lm"])
		_ = f.Close()
	}
	if values["-lm"] == "mlock" && os.Getenv("TRIALM_TEST_MLOCK_WARNING") == "1" {
		fmt.Fprintln(os.Stderr, "warning: failed to mlock; falling back internally")
		// A successful exit with rows is still an mlock failure.
	}
	stem := strings.TrimSuffix(filepath.Base(values["-m"]), ".gguf")
	parts := strings.Split(stem, "-")
	l, _ := strconv.Atoi(parts[len(parts)-1])
	var rows []BenchRow
	for _, ds := range strings.Split(values["-d"], ",") {
		d, _ := strconv.Atoi(ds)
		cost := .01 + .002*float64(l) + float64(d)*float64(l)*1e-8
		for _, ps := range strings.Split(values["-p"], ",") {
			p, _ := strconv.Atoi(ps)
			if p == 0 {
				continue
			}
			ns := cost * float64(p) * 1e9
			rows = append(rows, BenchRow{Prompt: p, Depth: d, Samples: []float64{ns * .99, ns, ns * 1.01}, GPU: "fake GPU", GPULayers: 99})
		}
		g, _ := strconv.Atoi(values["-n"])
		if g > 0 {
			ns := cost * float64(g) * 1e9
			rows = append(rows, BenchRow{Gen: g, Depth: d, Samples: []float64{ns * .99, ns, ns * 1.01}, GPU: "fake GPU", GPULayers: 99})
		}
	}
	switch os.Getenv("TRIALM_CHECK_TEST_MODE") {
	case "incomplete":
		rows = rows[:1]
	case "invalid":
		rows[0].Samples[0] = -1
	case "short-reps":
		rows[0].Samples = rows[0].Samples[:2]
	case "changed":
		f, e := os.OpenFile(values["-m"], os.O_APPEND|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(2)
		}
		_, _ = f.WriteString("changed")
		_ = f.Close()
	case "cpu":
		rows[0].GPULayers = 0
	case "duplicate":
		rows = append(rows, rows[0])
	}
	_ = json.NewEncoder(os.Stdout).Encode(rows)
	os.Exit(0)
}
func TestRunMedianFallbackAndResume(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("shell adapter")
	}
	for _, fallback := range []bool{false, true} {
		t.Run(fmt.Sprint(fallback), func(t *testing.T) {
			t.Setenv("TRIALM_SUCCESSFUL_BENCH_HELPER", "1")
			t.Setenv("GORACE", "atexit_sleep_ms=0")
			if fallback {
				t.Setenv("TRIALM_TEST_MLOCK_WARNING", "1")
			}
			home := t.TempDir()
			calls := filepath.Join(home, "calls")
			t.Setenv("TRIALM_BENCH_CALLS", calls)
			for _, p := range Grid(true) {
				dir := filepath.Join(home, "cache", "calib")
				if e := os.MkdirAll(dir, 0700); e != nil {
					t.Fatal(e)
				}
				f, e := os.Create(filepath.Join(dir, p.ID()+".gguf"))
				if e != nil {
					t.Fatal(e)
				}
				_ = f.Truncate(256 << 20)
				_ = f.Close()
			}
			exe, _ := os.Executable()
			engine := filepath.Join(home, "bench")
			script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=TestSuccessfulBenchHelper -- \"$@\"\n"
			if e := os.WriteFile(engine, []byte(script), 0700); e != nil {
				t.Fatal(e)
			}
			options := RunOptions{Home: home, Engine: engine, Quick: true, Backend: "metal", Fingerprint: Fingerprint{Hash: "fake"}, Baseline: func(p Probe, b BenchRow) float64 {
				return .02 + .003*float64(p.Layers) + float64(b.Depth)*float64(p.Layers)*2e-8
			}}
			c, e := Run(context.Background(), options)
			if e != nil {
				t.Fatal(e)
			}
			if !c.Complete || !c.Usable() || len(c.Coverage) != 2 || len(c.Rows) != 44 {
				t.Fatal("incomplete grid", c.Complete, len(c.Rows))
			}
			wantMode := "mlock"
			if fallback {
				wantMode = "mmap"
			}
			for _, r := range c.Rows {
				if r.Repetitions != 3 || r.LoadMode != wantMode || !json.Valid(r.Raw) || r.Spread <= 0 {
					t.Fatal(r)
				}
			}
			for _, class := range []string{"hybrid", "sliding"} {
				for _, d := range []int{0, 8192, 32768} {
					for _, m := range []string{"decode", "prefill"} {
						_, b, ok := c.Factor(class, referenceQuant(class), d, realLayers(class), m)
						if !ok || b < 10 || b > 30 {
							t.Fatal("clean full path did not yield useful fit", class, d, m, b, ok)
						}
					}
				}
			}
			c.Validations = []Validation{{Model: "real-check", MeasuredTPS: 10}}
			if e = c.Save(home); e != nil {
				t.Fatal(e)
			}
			before, _ := os.ReadFile(calls)
			// Resume must skip all groups, including those completed with mmap.
			var events []Event
			options.Event = func(e Event) { events = append(events, e) }
			c, e = Run(context.Background(), options)
			if e != nil || !c.Complete {
				t.Fatal(e)
			}
			if len(c.Validations) != 1 || c.Validations[0].Model != "real-check" {
				t.Fatal("resume lost external validation")
			}
			after, _ := os.ReadFile(calls)
			if string(before) != string(after) {
				t.Fatal("finished groups reran")
			}
			for _, e := range events {
				if e.ETA != 0 {
					t.Fatal("resume ETA counted skipped work", e)
				}
			}
			// Legacy default-mmap rows do not satisfy the new robust grid.
			r := c.Rows[0]
			r.LoadMode = ""
			if rowExists([]Row{r}, r.Probe, r.Depth, r.Prompt, r.Gen) {
				t.Fatal("legacy rows marked robust")
			}
			if fallback && !strings.Contains(strings.Join(c.Notes, " "), "retried with mmap") {
				t.Fatal("fallback note lost on resume")
			}
		})
	}
}
func TestLogTailRetainsMlockFailure(t *testing.T) {
	var tail logTail
	_, _ = tail.Write([]byte("warning: failed to ml"))
	_, _ = tail.Write([]byte("ock memory\n"))
	_, _ = tail.Write([]byte(strings.Repeat("x", 16000)))
	if !tail.mlockUnavailable() || len(tail.String()) > 8192 {
		t.Fatal("lost lock failure or unbounded tail")
	}
}
