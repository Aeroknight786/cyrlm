package calibrationcli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/aeroknight786/trialm/internal/hw"
	"github.com/aeroknight786/trialm/internal/paths"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/calib"
	runtime "github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/terminal"
)

func Run(args []string) error {
	fs := flag.NewFlagSet("calibrate", flag.ContinueOnError)
	quick := fs.Bool("quick", false, "the two default hybrid and sliding MoE picks")
	spec := fs.Bool("spec", os.Getenv("TRIALM_CALIBRATE_SPEC") != "0", "include the speculative verify sweep (on by default; --spec=false or TRIALM_CALIBRATE_SPEC=0 skips it)")
	plan := fs.Bool("plan", false, "print the grid and estimated M5 seconds without running a benchmark")
	refit := fs.Bool("refit", false, "rebuild fits from saved raw rows without hardware or llama-bench")
	check := fs.String("check", "", "check an existing local GGUF against calibration (about 1–2 min; no downloads)")
	status := fs.Bool("status", false, "show calibration coverage and fingerprint status")
	jsonOut := fs.Bool("json", false, "emit NDJSON events")
	pos, e := parseInterspersed(fs, args)
	if e != nil {
		return e
	}
	checkSet := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "check" {
			checkSet = true
		}
	})
	if checkSet && *check == "" {
		return errors.New("--check needs a local GGUF path")
	}
	if len(pos) > 0 {
		return errors.New("usage: trialm calibrate [--quick] [--spec] [--status | --plan | --refit | --check model.gguf] [--json]")
	}
	selected := 0
	for _, v := range []bool{*status, *plan, *refit, *check != ""} {
		if v {
			selected++
		}
	}
	if selected > 1 {
		return errors.New("choose one of --status, --plan, --refit or --check")
	}
	if *quick && *check != "" {
		return errors.New("--quick does not apply to --check")
	}
	home := trialmHome()
	if *plan {
		steps := calib.Plan(*quick, *spec)
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"v": 1, "type": "calibration_plan", "estimated_seconds": calib.EstimatedSeconds(steps), "estimate_basis": "Apple M5, llama-bench 11960; includes cold synthetic generation; allow about 25% variation", "steps": steps})
		}
		fmt.Fprintf(out(), "Calibration plan: %d steps, about %.0f seconds (%.0f min) on the M5. Allow about 25%% variation.\n", len(steps), calib.EstimatedSeconds(steps), calib.EstimatedSeconds(steps)/60)
		for i, s := range steps {
			fmt.Fprintf(out(), "[%d/%d] %s · about %.0f s\n", i+1, len(steps), s.Description(), s.EstimatedSeconds)
		}
		fmt.Fprintln(out(), "Estimate from the owner's per-step timings, including generation and warm-up. No hard stop; Ctrl-C saves finished rows for resume.")
		return nil
	}
	if *refit {
		c, e := calib.Load(home)
		if e != nil {
			return e
		}
		if e = rebaseline(c); e != nil {
			return e
		}
		if e = c.Save(home); e != nil {
			return e
		}
		if *jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"v": 1, "type": "calibration_refit", "calibration": c})
		}
		fmt.Fprintf(out(), "Rebuilt %d fits from %d saved rows. Coverage: %s\n", len(c.Fits), len(c.Rows), strings.Join(c.Coverage, ", "))
		for _, f := range c.Fits {
			if f.Rejected != "" {
				fmt.Fprintf(out(), "%s/%s depth %d %s: rejected (%s)\n", f.Class, f.Quant, f.Depth, f.Mode, f.Rejected)
			} else {
				fmt.Fprintf(out(), "%s/%s depth %d %s: about %.2gx speed, ±%d%%\n", f.Class, f.Quant, f.Depth, f.Mode, f.Multiplier, f.BandPct)
			}
		}
		return nil
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	engine, engineErr := calib.FindEngine(home)
	if *status {
		return calibrationStatus(ctx, home, engine, engineErr, *jsonOut)
	}
	if *check != "" {
		if engineErr != nil {
			return engineErr
		}
		return calibrationCheck(ctx, home, engine, *check, *jsonOut)
	}
	emit := func(ev calib.Event) {
		if *jsonOut {
			_ = json.NewEncoder(os.Stdout).Encode(ev)
			return
		}
		switch ev.Type {
		case "start":
			fmt.Fprintln(os.Stderr, ev.Message)
		case "step":
			fmt.Fprintf(os.Stderr, "[%d/%d] %s · %s · ETA %s\n", ev.Step, ev.Total, ev.Probe, ev.Message, calibrationDuration(ev.ETA))
		case "result":
			fmt.Fprintf(os.Stderr, "%s. Coverage: %s. MLX is not calibrated.\n", ev.Message, strings.Join(ev.Coverage, ", "))
		case "interrupted":
			fmt.Fprintln(os.Stderr, "Stopped. Finished rows are in", filepath.Join(home, "calib.json"))
		}
	}
	fail := func(e error) error {
		if *jsonOut {
			emit(calib.Event{Version: 1, Type: "error", Message: e.Error()})
		}
		return e
	}
	if engineErr != nil {
		return fail(engineErr)
	}
	fp, e := calib.MachineFingerprint(ctx, engine)
	if e != nil {
		return fail(e)
	}
	t, _, e := calibrationTarget(ctx)
	if e != nil {
		return fail(e)
	}
	if t.CPUOnly {
		return fail(errors.New("synthetic GPU calibration needs a detected GPU; use trialm detect --probe for CPU bandwidth"))
	}
	baseline, backend := baselineFor(t)
	if hw.MacPower(ctx).LowPower {
		return fail(errors.New("Low Power Mode is on: it slows the GPU, so this calibration would record the wrong speed for this Mac. Turn it off in System Settings → Battery, then run trialm calibrate"))
	}
	_, e = calib.Run(ctx, calib.RunOptions{Backend: backend, Home: home, Engine: engine, Fingerprint: fp, Quick: *quick, Spec: *spec, Baseline: baseline, Event: emit})
	if errors.Is(e, context.Canceled) {
		return fail(fmt.Errorf("calibration interrupted; finished rows kept: %w", e))
	}
	if e != nil {
		return fail(e)
	}
	return nil
}
func calibrationDuration(s float64) string {
	if s < 60 {
		return fmt.Sprintf("%.0f s", s)
	}
	return fmt.Sprintf("about %.0f min", s/60)
}
func calibrationStatus(ctx context.Context, home, engine string, engineErr error, jsonOut bool) error {
	c, e := calib.Load(home)
	state := "missing"
	reason := "Run trialm calibrate for tighter numbers. MLX is not covered."
	if e == nil {
		state = "stale"
		reason = "Machine or llama.cpp build changed. Run trialm calibrate again."
		if engineErr != nil {
			reason = engineErr.Error()
		} else if fp, fe := calib.MachineFingerprint(ctx, engine); fe != nil {
			reason = fe.Error()
		} else if fp.Hash == c.Fingerprint.Hash {
			state = "current"
			reason = "Synthetic fit; real-model coverage is not established. Use trialm calibrate --check with a local GGUF."
			if len(c.Validations) > 0 {
				misses := 0
				for _, v := range c.Validations {
					if !v.InsideBand {
						misses++
					}
				}
				reason = fmt.Sprintf("Synthetic fit; %d real-model validation rows saved (%d outside band). These checks do not establish general coverage.", len(c.Validations), misses)
			}
			if !c.Usable() {
				state = "incomplete"
				reason = "No usable fit yet. Run trialm calibrate --refit to inspect the held-out checks."
			}
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		state = "invalid"
		reason = e.Error()
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"v": 1, "type": "calibration_status", "status": state, "message": reason, "calibration": c})
	}
	fmt.Fprintf(out(), "Calibration: %s. %s\n", state, reason)
	if c != nil {
		fmt.Fprintf(out(), "Run: %s · %.0f seconds · %d raw samples · complete: %t\nCoverage: %s\n", c.Started.Format("2006-01-02"), c.WallSeconds, len(c.Rows), c.Complete, strings.Join(c.Coverage, ", "))
		for _, n := range c.Notes {
			fmt.Fprintln(out(), n)
		}
	}
	return nil
}

func calibrationPromptEligible(args []string, stdinTTY, stdoutTTY bool, getenv func(string) string) bool {
	if !stdinTTY || !stdoutTTY || getenv("CI") != "" || getenv("TRIALM_NO_INPUT") != "" {
		return false
	}
	if len(args) > 0 && args[0] != "detect" && args[0] != "recommend" {
		return false
	}
	for _, a := range args {
		if a == "--json" || strings.HasPrefix(a, "--json=") || a == "--plain" || a == "--device" || strings.HasPrefix(a, "--device=") || a == "--help" || a == "-h" {
			return false
		}
	}
	return true
}

func calibrationPrefs(home string) (map[string]json.RawMessage, error) {
	m := map[string]json.RawMessage{}
	b, e := os.ReadFile(filepath.Join(home, "prefs.json"))
	if errors.Is(e, os.ErrNotExist) {
		return m, nil
	}
	if e != nil {
		return nil, e
	}
	e = json.Unmarshal(b, &m)
	return m, e
}
func saveCalibrationChoice(home, choice string) error {
	m, e := calibrationPrefs(home)
	if e != nil {
		return e
	}
	m["calibration_offer"], _ = json.Marshal(choice)
	b, e := json.MarshalIndent(m, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(home, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(home, ".prefs-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), filepath.Join(home, "prefs.json"))
}
func MaybeOffer(args []string) error {
	if len(args) > 0 && args[0] != "detect" && args[0] != "recommend" {
		return nil
	}
	if !calibrationReminderEligible(args) {
		return nil
	}
	home := trialmHome()
	prefs, e := calibrationPrefs(home)
	if e != nil {
		return e
	}
	if _, ok := prefs["calibration_offer"]; ok {
		if len(args) > 0 && args[0] == "recommend" && calibrationReminderEligible(args) && predictCalibrationMissing(home) {
			line := "calibrate this machine for tighter numbers: trialm calibrate"
			if terminal.IsTTY(os.Stderr) && os.Getenv("NO_COLOR") == "" {
				line = "\x1b[2m" + line + "\x1b[0m"
			}
			fmt.Fprintln(os.Stderr, line)
		}
		return nil
	}
	if !calibrationPromptEligible(args, terminal.IsTTY(os.Stdin), terminal.IsTTY(os.Stdout), os.Getenv) {
		return nil
	}

	if !predictCalibrationMissing(home) {
		return nil
	}
	fmt.Fprintf(os.Stderr, "Recommended: calibrate this machine once for tighter speed estimates (about %.0f min on the M5).\n", calib.EstimatedSeconds(calib.Plan(false, os.Getenv("TRIALM_CALIBRATE_SPEC") != "0"))/60)
	fmt.Fprintln(os.Stderr, "triaLM writes synthetic models (random weights, real shapes) and runs llama-bench on them. No model downloads. It needs about 6 GB of free disk for temporary files, deleted as it goes, and keeps only calib.json and prefs.json under", home+".")
	fmt.Fprint(os.Stderr, "Enter calibrates (recommended) · s or any other key skips: ")
	key, e := terminal.Key(os.Stdin)
	fmt.Fprintln(os.Stderr)
	if e != nil {
		return e
	}
	if key == 3 {
		return context.Canceled
	}
	choice := "skipped"
	if key == '\n' || key == '\r' {
		choice = "accepted"
	}
	if e = saveCalibrationChoice(home, choice); e != nil {
		return e
	}
	if choice == "accepted" {
		return Run(nil)
	}
	return nil
}
func predictCalibrationMissing(home string) bool {
	c, e := calib.Load(home)
	if e != nil || !c.Usable() {
		return true
	}
	engine, e := calib.FindEngine(home)
	if e != nil {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	fp, e := calib.MachineFingerprint(ctx, engine)
	return e != nil || fp.Hash != c.Fingerprint.Hash
}

func calibrationReminderEligible(args []string) bool {
	for _, a := range args {
		if a == "--json" || strings.HasPrefix(a, "--json=") || a == "--device" || strings.HasPrefix(a, "--device=") {
			return false
		}
	}
	return true
}

// baselineFor predicts a synthetic probe's seconds per token on target t with
// the current predictor; calibration multipliers are relative to it.
func baselineFor(t predict.Target) (func(calib.Probe, calib.BenchRow) float64, string) {
	profile, _ := runtime.Get("llamacpp")
	timings := map[string]*predict.Timing{}
	backend, _ := profile.ChooseBackend(t.OS, t.Arch, t.HWBackends)
	return func(p calib.Probe, b calib.BenchRow) float64 {
		tm := timings[p.ID()]
		if tm == nil {
			_, _, m, q := p.Layout()
			target := t
			target.Synthetic = true
			r := predict.Predict(predict.Input{Model: m, QuantName: q.Name, Runtime: profile, Target: target, Tuned: true, CtxOverride: 65536, KVCacheType: "f16"})
			tm = r.Timing
			timings[p.ID()] = tm
		}
		if b.Gen > 0 {
			sec := 0.0
			for i := 0; i < b.Gen; i++ {
				sec += tm.DecodeSec(b.Depth + i)
			}
			return sec / float64(b.Gen)
		}
		return tm.PrefillSec(b.Depth, b.Depth+b.Prompt) / float64(b.Prompt)
	}, backend
}

// rebaseline recomputes the stored baselines for this machine and refits.
func rebaseline(c *calib.Calibration) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	t, _, e := calibrationTarget(ctx)
	if e != nil {
		return e
	}
	f, _ := baselineFor(t)
	c.Rebaseline(f)
	return nil
}

// Upgrade refits a calibration saved by an older speed model, keeping its
// measurements; it runs once, quietly, after an update.
func Upgrade() {
	home, e := paths.Home()
	if e != nil {
		return
	}
	c, e := calib.Load(home)
	if e != nil || !c.NeedsRebaseline {
		return
	}
	if rebaseline(c) == nil {
		_ = c.Save(home)
	}
}
