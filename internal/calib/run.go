package calib

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
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

// Run saves every completed JSON row, including rows emitted before Ctrl-C.
// Each invocation is bounded by the overall budget. A later run resumes probes
// whose measurement groups are complete under the same fingerprint.
func Run(ctx context.Context, o RunOptions) (*Calibration, error) {
	started := time.Now()
	budget := 8 * time.Minute
	if o.Quick {
		budget = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	c := &Calibration{Backend: o.Backend, Version: GeneratorVersion, Fingerprint: o.Fingerprint, Started: started, Quick: o.Quick, Notes: []string{
		"Synthetic weights; exact text-backbone geometry and full output head. No model downloads.",
		"Bands are 75% log-residual fit intervals conditional on synthetic shapes; real-model extrapolation is not validated.",
		"MLX is not covered. Ollama and LM Studio transfer assumes matching llama.cpp kernels and settings.",
		"MLA is not covered in this generator version. Gemma fit covers Gemma 4 26B-A4B geometry only.",
		"Verify rows are llama-bench small-prompt costs; serving all candidate logits can add output-head work.",
	}}
	if old, e := Load(o.Home); e == nil && old.Fingerprint.Hash == o.Fingerprint.Hash && old.Backend == o.Backend {
		c.Rows = old.Rows
	}
	ps := Grid(o.Quick)
	steps := len(ps) * 2
	emit := func(typ string, step int, p Probe, msg string) {
		if o.Event != nil {
			elapsed := time.Since(started).Seconds()
			eta := budget.Seconds() - elapsed
			if step > 1 {
				eta = elapsed / float64(step-1) * float64(steps-step+1)
			}
			if eta < 0 {
				eta = 0
			}
			probeID := ""
			if p.Shape.ID != "" {
				probeID = p.ID()
			}
			o.Event(Event{Version: 1, Type: typ, Step: step, Total: steps, Probe: probeID, Message: msg, Elapsed: elapsed, ETA: eta, Rows: len(c.Rows), Coverage: c.Coverage, Complete: c.Complete})
		}
	}
	save := func() error { c.FitRows(); c.WallSeconds = time.Since(started).Seconds(); return c.Save(o.Home) }
	emit("start", 0, Probe{}, "Optional machine calibration; 5–8 min, about 2 min with --quick. Writes synthetic GGUFs under cache/calib and calib.json.")
	for i, p := range ps {
		if e := ctx.Err(); e != nil {
			_ = save()
			return c, e
		}
		for group := 0; group < 2; group++ {
			step := i*2 + group + 1
			if groupDone(c.Rows, p, o.Quick, group) {
				emit("step", step, p, "Using finished rows")
				continue
			}
			emit("step", step, p, "Generating or loading synthetic GGUF")
			path, e := Generate(ctx, o.Home, p)
			if e != nil {
				_ = save()
				return c, e
			}
			args := BenchArgs(path, o.Quick, group, o.CPUOnly)
			emit("step", step, p, "Benchmarking "+strings.Join(args[2:], " "))
			e = runBench(ctx, o.Engine, args, func(raw json.RawMessage, b BenchRow) error {
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
				retained := c.Rows[:0]
				for _, r := range c.Rows {
					if r.Probe == p.ID() && r.Depth == b.Depth && r.Prompt == b.Prompt && r.Gen == b.Gen {
						continue
					}
					retained = append(retained, r)
				}
				c.Rows = retained
				for _, ns := range samples {
					if ns <= 0 || !finite(ns) || baseline <= 0 || !finite(baseline) {
						return errors.New("llama-bench returned invalid timings")
					}
					c.Rows = append(c.Rows, Row{Probe: p.ID(), Class: p.Shape.Class, Quant: p.Quant, Layers: p.Layers, Check: p.Check, Depth: b.Depth, Prompt: b.Prompt, Gen: b.Gen, Seconds: ns / 1e9 / float64(n), Baseline: baseline, Raw: raw})
				}
				if e := save(); e != nil {
					return e
				}
				emit("progress", step, p, fmt.Sprintf("Saved depth %d, prompt %d, decode %d", b.Depth, b.Prompt, b.Gen))
				return nil
			})
			if e != nil {
				_ = save()
				emit("interrupted", step, p, e.Error())
				return c, e
			}
		}
	}
	c.FitRows()
	if len(c.Fits) == 0 {
		_ = save()
		return c, errors.New("no usable layer fit; raw benchmark rows were kept in calib.json")
	}
	c.Complete = true
	if e := save(); e != nil {
		return c, e
	}
	emit("result", steps, Probe{}, fmt.Sprintf("Saved calibration in %.0f seconds", c.WallSeconds))
	return c, nil
}

// BenchArgs limits verify to depth zero; full decode and prefill use all three
// depths. llama-bench caches depth preparation across tests in one invocation.
func BenchArgs(path string, quick bool, group int, cpu bool) []string {
	depth := "0,8192,32768"
	args := []string{"-m", path, "-p", "512", "-n", "32", "-d", depth, "-r", "2", "-o", "json", "-ngl", "99", "-fa", "on"}
	if group == 1 {
		args = []string{"-m", path, "-p", "1,2,3,4", "-n", "0", "-d", "0", "-r", "2", "-o", "json", "-ngl", "99", "-fa", "on"}
	}
	if cpu {
		args = append(args, "-ngl", "0", "-dev", "none", "-nopo", "1")
	}
	return args
}
func groupDone(rows []Row, p Probe, quick bool, group int) bool {
	depths := []int{0, 8192, 32768}
	if group == 1 {
		depths = []int{0}
	}
	for _, d := range depths {
		if group == 1 {
			for n := 1; n <= 4; n++ {
				if !rowExists(rows, p.ID(), d, n, 0) {
					return false
				}
			}
		} else if !rowExists(rows, p.ID(), d, 512, 0) || !rowExists(rows, p.ID(), d, 0, 32) {
			return false
		}
	}
	return true
}
func rowExists(rows []Row, id string, d, p, n int) bool {
	for _, r := range rows {
		if r.Probe == id && r.Depth == d && r.Prompt == p && r.Gen == n {
			return true
		}
	}
	return false
}

// A capped log preserves the useful final diagnostics without unbounded memory.
type logTail struct{ bytes.Buffer }

func (b *logTail) Write(p []byte) (int, error) {
	n := len(p)
	if n > 8192 {
		p = p[n-8192:]
		b.Reset()
	}
	if b.Len()+len(p) > 8192 {
		keep := append([]byte(nil), b.Bytes()[b.Len()+len(p)-8192:]...)
		b.Reset()
		b.Buffer.Write(keep)
	}
	b.Buffer.Write(p)
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
	if decodeErr != nil || waitErr != nil {
		return fmt.Errorf("llama-bench failed (%v; %v): %s", decodeErr, waitErr, strings.TrimSpace(log.String()))
	}
	return nil
}
