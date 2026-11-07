package calib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BenchRow is the numeric portion of llama-bench -o json. Raw rows are kept.
type BenchRow struct {
	Prompt    int       `json:"n_prompt"`
	Gen       int       `json:"n_gen"`
	Depth     int       `json:"n_depth"`
	AvgNS     float64   `json:"avg_ns"`
	Samples   []float64 `json:"samples_ns"`
	GPU       string    `json:"gpu_info"`
	GPULayers int       `json:"n_gpu_layers"`
}

type Event struct {
	Version  int      `json:"v"`
	Type     string   `json:"type"`
	Step     int      `json:"step,omitempty"`
	Total    int      `json:"total,omitempty"`
	Probe    string   `json:"probe,omitempty"`
	Message  string   `json:"message,omitempty"`
	Elapsed  float64  `json:"elapsed_s"`
	ETA      float64  `json:"eta_s,omitempty"`
	Rows     int      `json:"rows,omitempty"`
	Coverage []string `json:"coverage,omitempty"`
	Complete bool     `json:"complete,omitempty"`
}

// RunOptions keeps prediction math outside calib (predict imports calib).
type RunOptions struct {
	Backend        string
	Home, Engine   string
	Fingerprint    Fingerprint
	Quick, CPUOnly bool
	Baseline       func(Probe, BenchRow) float64
	Event          func(Event)
}

// Run saves every finished row and resumes completed groups. The plan is a
// runtime estimate; cancellation belongs to the caller, with no hard deadline.
// MinFreeBytes is the free disk a run needs: its largest temporary probe
// file (about 4.5 GB) plus margin.
const MinFreeBytes = 6 << 30

func Run(ctx context.Context, o RunOptions) (*Calibration, error) {
	started := time.Now()
	c := &Calibration{Backend: o.Backend, Version: GeneratorVersion, Fingerprint: o.Fingerprint, Started: started, Quick: o.Quick, Notes: []string{
		"Synthetic weights and full heads. No model downloads. MLX and MLA are not covered.",
		"Bands combine held-out layer error and repetition spread, extrapolated to real layers. They are sensitivity estimates, not validated confidence intervals.",
		"Deep context cost transfers per layer from one reference probe per family, with an extra uncertainty allowance of 15% of the context penalty.",
		"Ollama and LM Studio transfer assumes matching llama.cpp kernels and settings.",
		"Verify rows measure the small-prompt path; serving all candidate logits can add head work.",
	}}
	if old, e := Load(o.Home); e == nil && old.Fingerprint.Hash == o.Fingerprint.Hash && old.Backend == o.Backend {
		c.Rows = old.Rows
		c.Validations = old.Validations
		for _, note := range old.Notes {
			if strings.HasPrefix(note, "mlock failed for ") && !contains(c.Notes, note) {
				c.Notes = append(c.Notes, note)
			}
		}
	}
	steps := Plan(o.Quick)
	remaining := func(from int) float64 {
		total := 0.
		for _, s := range steps[from:] {
			if !s.done(c.Rows) {
				total += s.EstimatedSeconds
			}
		}
		return total
	}
	emit := func(typ string, i int, p Probe, msg string) {
		if o.Event == nil {
			return
		}
		id := ""
		if p.Shape.ID != "" {
			id = p.ID()
		}
		from := i
		if from > 0 {
			from--
		}
		if from > len(steps) {
			from = len(steps)
		}
		o.Event(Event{Version: 1, Type: typ, Step: i, Total: len(steps), Probe: id, Message: msg, Elapsed: time.Since(started).Seconds(), ETA: remaining(from), Rows: len(c.Rows), Coverage: c.Coverage, Complete: c.Complete})
	}
	save := func() error { c.FitRows(); c.WallSeconds = time.Since(started).Seconds(); return c.Save(o.Home) }
	emit("start", 0, Probe{}, fmt.Sprintf("Optional machine calibration; estimated %.0f seconds on the M5 (use --plan for details). Finished rows resume after interruption.", remaining(0)))
	// Each synthetic file is used by consecutive steps only; delete it after
	// its last one, so the run needs one probe's worth of disk at a time
	// (at most about 5 GB) instead of every probe at once (about 70 GB).
	lastUse := map[string]int{}
	for i, s := range steps {
		lastUse[s.Probe.ID()] = i
	}
	cacheDir := filepath.Join(o.Home, "cache", "calib")
	for i, s := range steps {
		p := s.Probe
		if e := ctx.Err(); e != nil {
			_ = save()
			emit("interrupted", i+1, p, e.Error())
			return c, e
		}
		if s.done(c.Rows) {
			emit("step", i+1, p, "Using finished rows")
			continue
		}
		emit("step", i+1, p, "Generating or loading synthetic GGUF")
		if free := freeBytes(o.Home); free >= 0 && free < MinFreeBytes {
			_ = save()
			return c, fmt.Errorf("calibration needs about %d GB of free disk for its temporary files; %.1f GB is free", MinFreeBytes>>30, float64(free)/(1<<30))
		}
		path, e := Generate(ctx, o.Home, p)
		if e != nil {
			_ = save()
			return c, e
		}
		args := s.Args(path, o.CPUOnly)
		loadMode := "mlock"
		measure := func() error {
			emit("step", i+1, p, "Benchmarking "+strings.Join(args[2:], " "))
			return runBench(ctx, o.Engine, args, func(raw json.RawMessage, b BenchRow) error {
				if !o.CPUOnly && (b.GPULayers == 0 || strings.TrimSpace(b.GPU) == "") {
					return errors.New("llama-bench did not use a GPU; no GPU calibration was saved")
				}
				n := b.Prompt + b.Gen
				if n <= 0 {
					return errors.New("llama-bench returned a zero-token row")
				}
				baseline := o.Baseline(p, b)
				samples := b.Samples
				if len(samples) == 0 {
					samples = []float64{b.AvgNS}
				}
				seconds := make([]float64, len(samples))
				for j, ns := range samples {
					if ns <= 0 || !finite(ns) || baseline <= 0 || !finite(baseline) {
						return errors.New("llama-bench returned invalid timings")
					}
					seconds[j] = ns / 1e9 / float64(n)
				}
				retained := c.Rows[:0]
				for _, r := range c.Rows {
					if r.Probe != p.ID() || r.Depth != b.Depth || mode(r) != mode(Row{Prompt: b.Prompt, Gen: b.Gen}) {
						retained = append(retained, r)
					}
				}
				c.Rows = append(retained, Row{Probe: p.ID(), Class: p.Shape.Class, Quant: p.Quant, Layers: p.Layers, Check: p.Check, Depth: b.Depth, Prompt: b.Prompt, Gen: b.Gen, Seconds: median(seconds), Spread: spread(seconds), Repetitions: len(seconds), LoadMode: loadMode, Baseline: baseline, Raw: raw})
				if e := save(); e != nil {
					return e
				}
				emit("progress", i+1, p, fmt.Sprintf("Saved depth %d, prompt %d, decode %d (%s)", b.Depth, b.Prompt, b.Gen, loadMode))
				return nil
			})
		}
		previousRows := append([]Row(nil), c.Rows...)
		e = measure()
		if errors.Is(e, errMlockUnavailable) && ctx.Err() == nil {
			// Warnings can arrive after JSON rows. Discard that attempt before
			// retrying so an interrupted fallback never leaves falsely pinned rows.
			c.Rows = previousRows
			loadMode = "mmap"
			// CPU-only flags may follow -lm; replace its value by name.
			for j := 0; j < len(args)-1; j++ {
				if args[j] == "-lm" {
					args[j+1] = "mmap"
				}
			}
			note := "mlock failed for " + p.ID() + "; retried with mmap. Check repetition spread before trusting the fit."
			if !contains(c.Notes, note) {
				c.Notes = append(c.Notes, note)
			}
			if e = save(); e != nil {
				return c, e
			}
			emit("step", i+1, p, note)
			e = measure()
		}
		if e != nil {
			_ = save()
			emit("interrupted", i+1, p, e.Error())
			return c, e
		}
		if !s.done(c.Rows) {
			_ = save()
			return c, errors.New("llama-bench returned an incomplete measurement group; finished rows kept")
		}
		if lastUse[p.ID()] == i {
			_ = os.Remove(path)
		}
	}
	_ = os.Remove(cacheDir) // only succeeds when empty
	c.FitRows()
	c.Complete = true
	if e := save(); e != nil {
		return c, e
	}
	if !c.Usable() {
		return c, errors.New("no usable held-out layer fit; raw rows were kept. Run trialm calibrate --refit to inspect fits, or remove calib.json and rerun on a quiet machine")
	}
	emit("result", len(steps), Probe{}, fmt.Sprintf("Saved calibration in %.0f seconds", c.WallSeconds))
	return c, nil
}

func (s Step) done(rows []Row) bool {
	for _, v := range strings.Split(s.Depths, ",") {
		d, _ := strconv.Atoi(v)
		if s.Group == 1 {
			for n := 1; n <= 4; n++ {
				if !rowExists(rows, s.Probe.ID(), d, n, 0) {
					return false
				}
			}
		} else if !rowExists(rows, s.Probe.ID(), d, 512, 0) || !rowExists(rows, s.Probe.ID(), d, 0, 8) {
			return false
		}
	}
	return true
}
func rowExists(rows []Row, id string, d, p, n int) bool {
	for _, r := range rows {
		if r.Probe == id && r.Depth == d && r.Prompt == p && r.Gen == n && (r.LoadMode == "mlock" || r.LoadMode == "mmap") && r.Repetitions >= 3 {
			return true
		}
	}
	return false
}

var errMlockUnavailable = errors.New("mlock unavailable")

func mlockFailed(log string) bool {
	for _, line := range strings.Split(strings.ToLower(log), "\n") {
		if (strings.Contains(line, "mlock") || strings.Contains(line, "virtual lock") || strings.Contains(line, "virtuallock")) && (strings.Contains(line, "fail") || strings.Contains(line, "cannot") || strings.Contains(line, "not permitted") || strings.Contains(line, "not supported") || strings.Contains(line, "unsupported") || strings.Contains(line, "unable") || strings.Contains(line, "not available")) {
			return true
		}
	}
	return false
}
func wantsMlock(args []string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == "-lm" && args[i+1] == "mlock" {
			return true
		}
	}
	return false
}

// A capped log preserves the useful final diagnostics without unbounded memory.
type logTail struct {
	buffer     bytes.Buffer
	mu         sync.Mutex
	lockFailed bool
}

func (b *logTail) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buffer.String() }

func (b *logTail) mlockUnavailable() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lockFailed
}

func (b *logTail) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	// Remember warnings even when later engine diagnostics displace the tail.
	if mlockFailed(b.buffer.String() + string(p)) {
		b.lockFailed = true
	}
	n := len(p)
	if n > 8192 {
		p = p[n-8192:]
		b.buffer.Reset()
	}
	if b.buffer.Len()+len(p) > 8192 {
		keep := append([]byte(nil), b.buffer.Bytes()[b.buffer.Len()+len(p)-8192:]...)
		b.buffer.Reset()
		b.buffer.Write(keep)
	}
	b.buffer.Write(p)
	return n, nil
}

func runBench(ctx context.Context, engine string, args []string, onRow func(json.RawMessage, BenchRow) error) error {
	cmd := exec.CommandContext(ctx, engine, args...)
	var log logTail
	cmd.Stderr = &log
	out, e := cmd.StdoutPipe()
	if e != nil {
		return e
	}
	if e = cmd.Start(); e != nil {
		return e
	}
	dec := json.NewDecoder(out)
	tok, decodeErr := dec.Token()
	if decodeErr == nil && tok != json.Delim('[') {
		decodeErr = errors.New("expected llama-bench JSON array")
	}
	if decodeErr == nil {
		for dec.More() {
			var raw json.RawMessage
			if decodeErr = dec.Decode(&raw); decodeErr != nil {
				break
			}
			var b BenchRow
			if decodeErr = json.Unmarshal(raw, &b); decodeErr != nil {
				break
			}
			if wantsMlock(args) && log.mlockUnavailable() {
				decodeErr = errMlockUnavailable
				break
			}
			if decodeErr = onRow(raw, b); decodeErr != nil {
				break
			}
		}
		if decodeErr == nil {
			_, decodeErr = dec.Token()
		}
	}
	if decodeErr != nil {
		_ = cmd.Process.Kill()
		_, _ = io.Copy(io.Discard, out)
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if wantsMlock(args) && log.mlockUnavailable() {
		return fmt.Errorf("%w: %s", errMlockUnavailable, strings.TrimSpace(log.String()))
	}
	if decodeErr != nil || waitErr != nil {
		return fmt.Errorf("llama-bench failed (%v; %v): %s", decodeErr, waitErr, strings.TrimSpace(log.String()))
	}
	return nil
}
