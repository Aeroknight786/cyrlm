package cli

import (
	"errors"
	"flag"
	"os"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/terminal"
	"github.com/aeroknight786/trialm/internal/ui"
	"github.com/aeroknight786/trialm/internal/ui/tui"
)

func cmdPlan(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	var c common
	c.register(fs, "")
	dry := fs.Bool("dry-run", false, "print the plan and its approval hash")
	undo := fs.Bool("undo", false, "reverse a saved setup")
	models := fs.Bool("delete-models", false, "delete this setup's model files during undo")
	engines := fs.Bool("remove-engine", false, "remove this setup's engine during undo")
	var yes setup.Approval
	fs.Var(&yes, "yes", "approve this exact plan_id")
	force := fs.Bool("force", false, "plan even if the model does not fit")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	c.finish(fs)
	if err := c.validateRequirements(); err != nil {
		return err
	}
	session := core.Session{Sink: func(e core.Event) {
		if events != nil {
			events.Handle(e)
		}
	}}
	executor, err := session.SetupExecutor()
	if err != nil {
		return err
	}
	executor.Admin = setup.OSAdmin
	if *undo || len(pos) == 0 && !*dry {
		slug := ""
		if len(pos) > 0 {
			slug = pos[0]
		} else {
			slug, _ = setup.Latest(executor.Root)
		}
		if slug != "" {
			if *undo {
				_, err = executor.Undo(commandContext, slug, setup.UndoOptions{Models: *models, Engines: *engines})
				return err
			}
			approval := string(yes)
			if approval == "true" {
				approval = ""
			}
			j, e := executor.Resume(commandContext, slug, approval)
			err = e
			if err == nil {
				session.SetupResult(commandContext, j)
			}
			return setupError(err)
		}
		if *undo {
			return errors.New("no saved setup to undo")
		}
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
	target, _, err := c.target()
	if err != nil {
		return err
	}
	plan, err := session.Plan(commandContext, core.PlanOptions{
		Options:     c.options(),
		Model:       m,
		Target:      target,
		Scenario:    sc,
		Harness:     hh,
		WireHarness: c.harness != "",
		Force:       *force,
	})
	if err != nil {
		return err
	}
	if yes != "" && yes != "true" && string(yes) != plan.ID {
		return &exitError{4, "setup plan changed; review trialm setup --dry-run and approve its new --yes=<plan_id>"}
	}
	if !c.jsonOut && (selectedMode != Interactive || *dry || currentVerb == "plan") {
		ui.PrintPlan(out(), plan)
	}
	if currentVerb == "plan" && c.jsonOut {
		return printJSON(plan)
	}
	if *dry || currentVerb == "plan" {
		return emit("result", map[string]any{"plan_id": plan.ID, "ready": false})
	}
	if yes == "" && selectedMode == Interactive {
		return tui.RunSetup(commandContext, plan)
	}
	if yes == "" {
		if c.jsonOut || !terminal.IsTTY(os.Stdin) {
			return needs("--yes="+plan.ID, "approve the reviewed setup plan")
		}
		approved, err := ui.ConfirmSetup(out(), os.Stdin)
		if err != nil {
			return err
		}
		if !approved {
			return nil
		}
	}
	j, err := executor.Apply(commandContext, plan, setup.Policy{PlanID: plan.ID})
	if err == nil {
		session.SetupResult(commandContext, j)
	}
	return setupError(err)
}
func setupError(err error) error {
	var approval interface{ NeedsUser() bool }
	if errors.As(err, &approval) && approval.NeedsUser() {
		return &exitError{3, err.Error()}
	}
	var ready *setup.NotReady
	if errors.As(err, &ready) {
		return &exitError{4, err.Error()}
	}
	return err
}
