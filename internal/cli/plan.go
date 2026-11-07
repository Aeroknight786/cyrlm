package cli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/ui"
	"net"
	"os"
	"path/filepath"

	"time"

	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/aeroknight786/trialm/internal/setup"
)

// freePort returns from, or the next port up to from+20 nothing listens on.
func freePort(from int) int {
	for p := from; p <= from+20; p++ {
		c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 300*time.Millisecond)
		if err != nil {
			return p
		}
		c.Close()
	}
	return from
}

// cmdPlan prints the exact steps to set up a model locally. It never runs
// them: a person or their coding agent executes them, one at a time.
func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	var c common
	c.register(fs, "")
	dry := fs.Bool("dry-run", false, "print the plan and its approval hash")
	var yes approval
	fs.Var(&yes, "yes", "approve this plan_id (execution arrives in v0.5)")
	force := fs.Bool("force", false, "plan even if the model does not fit")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if currentVerb == "setup" {
		if err := c.validateRequirements(); err != nil {
			return err
		}
	}
	if len(pos) != 1 && currentVerb != "setup" {
		return errors.New("usage: trialm plan <model> [--runtime r] [--defaults] [--harness h] [--quant q] [--ctx n] [--json]")
	}
	modelName, err := resolveRecipe(pos, &c)
	if err != nil {
		return err
	}
	m, err := lookupModel(modelName)
	if err != nil {
		return err
	}
	sc, hh, err := scenarioFor(c.harness)
	if err != nil {
		return err
	}
	t, _, err := c.target()
	if err != nil {
		return err
	}
	prof, why, err := chooseRuntime(c, t, m, sc, hh)
	if err != nil {
		return err
	}
	res := predict.Predict(predict.Input{Model: m, QuantName: c.quant, Runtime: prof, Target: t, Tuned: c.tuned,
		CtxOverride: c.ctx, KVCacheType: c.kv, Scenario: sc, BackendOverride: c.backend})
	if res.Unsupported != "" {
		return errors.New(res.Unsupported)
	}
	if res.Memory.Level == predict.FitNo && !*force {
		return fmt.Errorf("%s does not fit with %s here (needs %.1f GB, %.1f GB available); try a smaller --quant or --ctx, or --force",
			m.Name, prof.Name, res.Memory.TotalBytes/(1<<30), res.Memory.BudgetBytes/(1<<30))
	}
	in := detectInstalled(t)
	var hp *harness.Harness
	if c.harness != "" {
		hp = &hh
	}
	input := setup.Input{Model: m, Quant: res.Quant, Profile: prof, Settings: res.Settings, Backend: res.Backend, Harness: hp, OS: t.OS, Arch: t.Arch}
	if !t.Synthetic {
		input.OwnedFileExists = ownedFileExists
	}
	for _, ir := range in.ByProfile[prof.ID] {
		input.Installed, input.InstalledVersion = true, ir.Version
		input.Running = input.Running || ir.Running
	}
	// trialm starts llama-server / mlx_lm.server itself: if the usual port is
	// taken by something that is not that runtime, plan on the next free one.
	portNote := ""
	if !t.Synthetic && !input.Running && (prof.ID == "llamacpp" || prof.ID == "mlx-lm") {
		def := setup.DefaultPort(prof)
		if p := freePort(def); p != def {
			input.Port = p
			portNote = fmt.Sprintf("Port %d is already used by another program on this machine, so this plan uses %d everywhere (server, health check, bridge, verify).", def, p)
		}
	}
	plan, err := setup.Build(input)
	if err != nil {
		return err
	}
	if portNote != "" {
		plan.Warnings = append(plan.Warnings, portNote)
	}
	if currentVerb == "setup" {
		b, err := json.Marshal(plan)
		if err != nil {
			return err
		}
		id := fmt.Sprintf("%x", sha256.Sum256(b))
		if yes != "" && yes != "true" && string(yes) != id {
			return &exitError{4, "setup plan changed; review trialm setup --dry-run and approve its new --yes=<plan_id>"}
		}
		next := core.Next{Cmd: "trialm setup " + modelName + " --yes=" + id + " --json", Why: "approve this reviewed plan; execution arrives in v0.5"}
		if c.jsonOut {
			if err = emit("plan", map[string]any{"plan_id": id, "plan": plan}); err != nil {
				return err
			}
			return emit("result", map[string]any{"plan_id": id, "ready": false, "execution": "arrives in v0.5"}, next)
		}
		ui.PrintPlan(out(), plan, why, res)
		fmt.Fprintln(out(), "plan_id:", id)
		if !*dry {
			fmt.Fprintln(out(), "Execution arrives in v0.5. Nothing has been run.")
		}
		return nil
	}
	if c.jsonOut {
		return printJSON(plan)
	}
	ui.PrintPlan(out(), plan, why, res)
	return nil
}

// ownedFileExists reports whether ~/<rel> exists (rel starts with .trialm/).
func ownedFileExists(rel string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, filepath.FromSlash(rel)))
	return err == nil
}

type approval string

func (a *approval) String() string     { return string(*a) }
func (a *approval) Set(v string) error { *a = approval(v); return nil }
func (a *approval) IsBoolFlag() bool   { return true }
