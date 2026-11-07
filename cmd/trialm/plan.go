package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
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
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	var c common
	c.register(fs, "")
	force := fs.Bool("force", false, "plan even if the model does not fit")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if len(pos) != 1 {
		return errors.New("usage: trialm plan <model> [--runtime r] [--defaults] [--harness h] [--quant q] [--ctx n] [--json]")
	}
	m, err := lookupModel(pos[0])
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
	for _, ir := range in.byProfile[prof.ID] {
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
	if c.jsonOut {
		return printJSON(plan)
	}
	printPlan(plan, why, res)
	return nil
}

func printPlan(p setup.Plan, why string, res predict.Result) {
	mode := "default settings"
	if p.Tuned {
		mode = "tuned settings"
	}
	fmt.Printf("\nSetup plan: %s (%s, %s) with %s, %s\n", p.ModelName, p.Quant, gb(float64(p.DownloadBytes)), p.RuntimeName, mode)
	fmt.Printf("  runtime chosen: %s\n", why)
	fmt.Printf("  context %d tokens · %s · serves at %s as %q\n", p.ContextTokens, cacheLabel(p.KVCacheType), p.Endpoint, p.LocalModelName)
	fmt.Printf("  expected: first reply %s, then ~%s per turn, writing at %.0f tok/s\n",
		dur(res.Scenario.ColdFirstReply), dur(res.Scenario.FollowUpTurn), res.DecodeTPSDeep)
	if p.Harness != "" {
		mark := "✓"
		if !p.HarnessCompatible {
			mark = "✗"
		}
		fmt.Printf("  %s %s\n", mark, p.HarnessNote)
	}
	fmt.Println("\nNothing has been run. Run each step yourself (or let your coding agent run them, with your approval):")
	for i, s := range p.Steps {
		flags := []string{}
		if s.Optional {
			flags = append(flags, "optional")
		}
		if s.NeedsAdmin {
			flags = append(flags, "needs admin")
		}
		if s.DownloadBytes > 0 {
			flags = append(flags, "downloads "+gb(float64(s.DownloadBytes)))
		}
		if s.Confidence == "low" {
			flags = append(flags, "check the docs link: command not verified for every version")
		}
		extra := ""
		if len(flags) > 0 {
			extra = "  [" + strings.Join(flags, ", ") + "]"
		}
		fmt.Printf("\n%d. %s%s\n", i+1, s.Title, extra)
		if s.Why != "" {
			fmt.Printf("   %s\n", s.Why)
		}
		for _, cmd := range s.Commands {
			for _, line := range strings.Split(strings.TrimRight(cmd.Run, "\n"), "\n") {
				fmt.Printf("     %s\n", line)
			}
		}
		for _, f := range s.Files {
			fmt.Printf("   %s %s:\n", map[string]string{"create": "Create", "merge": "Merge into", "append": "Append to"}[f.Merge], f.Path)
			for _, line := range strings.Split(strings.TrimRight(f.Content, "\n"), "\n") {
				fmt.Printf("     %s\n", line)
			}
		}
		if s.Restart != "" {
			fmt.Printf("   then restart: %s\n", s.Restart)
		}
	}
	for _, w := range p.Warnings {
		fmt.Printf("\n⚠ %s", w)
	}
	fmt.Println()
	fmt.Println()
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
