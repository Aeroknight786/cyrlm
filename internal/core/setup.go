package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/verify"
)

type PlanOptions struct {
	Options
	Model              catalog.Model
	Target             predict.Target
	Scenario           predict.Scenario
	Harness            harness.Harness
	WireHarness, Force bool
}

func (s Session) Plan(ctx context.Context, o PlanOptions) (setup.Plan, error) {
	p, _, err := ChooseRuntime(o.Options, o.Target, o.Model, o.Scenario, o.Harness)
	if err != nil {
		return setup.Plan{}, err
	}
	res := predict.Predict(predict.Input{
		Model:           o.Model,
		QuantName:       o.Quant,
		Runtime:         p,
		Target:          o.Target,
		Tuned:           o.Tuned,
		CtxOverride:     o.Context,
		KVCacheType:     o.KV,
		Scenario:        o.Scenario,
		BackendOverride: o.Backend,
	})
	if res.Unsupported != "" {
		return setup.Plan{}, errors.New(res.Unsupported)
	}
	wired := o.Target.Unified && o.Target.OS == "darwin" && res.Memory.TotalBytes > o.Target.GPUMemBytes && res.Memory.TotalBytes <= o.Target.RAMBytes-4*(1<<30)
	if res.Memory.Level == predict.FitNo && !o.Force && !wired {
		return setup.Plan{}, fmt.Errorf("%s does not fit with %s here (needs %.1f GB, %.1f GB available); try a smaller --quant or --ctx, or --force", o.Model.Name, p.Name, res.Memory.TotalBytes/(1<<30), res.Memory.BudgetBytes/(1<<30))
	}
	input := setup.Input{
		DecodeErrorPct:  res.Accuracy.DecodePct,
		PrefillErrorPct: res.Accuracy.PrefillPct,
		Model:           o.Model,
		Quant:           res.Quant,
		Profile:         p,
		Settings:        res.Settings,
		Backend:         res.Backend,
		OS:              o.Target.OS,
		Arch:            o.Target.Arch,
		RAMBytes:        o.Target.RAMBytes,
		DefaultGPUBytes: o.Target.GPUMemBytes,
		Predicted:       verify.Predicted{DecodeTPS0: res.DecodeTPS0, DecodeTPSTarget: res.Timing.DecodeTPS(res.Settings.ContextTokens), PrefillTPS: res.PrefillTPS2k, BandPct: res.Accuracy.DecodePct},
	}
	if wired {
		input.RequiredGPUBytes = res.Memory.TotalBytes
	}
	if o.WireHarness {
		input.Harness = &o.Harness
	}
	installed := DetectInstalled(o.Target)
	for _, ir := range installed.ByProfile[p.ID] {
		input.Installed = true
		if p.ID == "llamacpp" || p.ID == "ollama" || p.ID == "mlx-lm" {
			input.Binary = ir.Path
		}
		if p.ID == "llamacpp" && (ir.Path == "" || !strings.Contains(filepath.Base(ir.Path), "llama-server")) {
			input.Installed = false
			input.Binary = ""
		}
		input.InstalledVersion = ir.Version
		input.Running = input.Running || ir.Running
	}
	if !o.Target.Synthetic {
		root, _ := paths.Home()
		input.OwnedFileExists = func(rel string) bool {
			_, err := os.Stat(filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(rel, ".trialm/"))))
			return err == nil
		}
		input, err = setup.Resolve(ctx, input, os.Getenv("HF_TOKEN"))
		if err != nil {
			return setup.Plan{}, err
		}
	}
	plan, err := setup.Build(input)
	if err == nil {
		s.Emit("setup_plan", map[string]any{"plan_id": plan.ID, "plan": plan}, Next{Cmd: "trialm setup " + o.Model.ID + " --yes=" + plan.ID + " --json", Why: "apply this reviewed plan"})
	}
	return plan, err
}
func (s Session) SetupExecutor() (*setup.Executor, error) {
	root, err := paths.Home()
	if err != nil {
		return nil, err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	return &setup.Executor{Root: root, Home: home, Event: func(kind string, data any) {
		s.Emit(kind, data, Next{Cmd: "trialm setup --json", Why: "read setup status or resume"})
	}}, nil
}
func (s Session) Apply(ctx context.Context, p setup.Plan, policy setup.Policy) (setup.Journal, error) {
	e, err := s.SetupExecutor()
	if err != nil {
		return setup.Journal{}, err
	}
	return e.Apply(ctx, p, policy)
}
func (s Session) Status(ctx context.Context, slug string) (setup.Journal, error) {
	e, err := s.SetupExecutor()
	if err != nil {
		return setup.Journal{}, err
	}
	if err = ctx.Err(); err != nil {
		return setup.Journal{}, err
	}
	j, err := e.Status(slug)
	if err == nil {
		j.Running = e.Running(ctx, j)
		s.Emit("setup_status", j, Next{Cmd: "trialm setup --json", Why: "resume saved setup"})
	}
	return j, err
}

// SetupResult closes the agent contract after the journal reaches Ready.
func (s Session) SetupResult(ctx context.Context, j setup.Journal) {
	if j.State != "ready" || j.Report == nil || !j.Report.Ready {
		return
	}
	p := j.Plan
	enginePath := ""
	for _, step := range p.Steps {
		if step.Kind == setup.Start && step.ID != "bridge" && len(step.Argv) > 0 {
			enginePath = step.Argv[0]
			break
		}
	}
	if err := calib.RecordReal(ctx, store.Default().Dir, enginePath, calib.RealMeasurement{Model: p.Model, Quant: p.Quant, Engine: p.Runtime, Decode: j.Report.DecodeTPS, Prefill: j.Report.PrefillTPS}); err == nil {
		s.Emit("setup_calibrated", true)
	} else {
		s.Emit("setup_log", setup.Update{Lines: []string{"Calibration was not saved · " + err.Error()}})
	}
	s.Emit("result", map[string]any{"ready": true}, Next{Cmd: "trialm launch " + p.Harness, Why: "open this Ready setup"})
}
