package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/aeroknight786/trialm/internal/calibrationcli"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/paths"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/terminal"
	"github.com/aeroknight786/trialm/internal/ui"
)

type Mode string

const (
	Plain       Mode = "plain"
	JSON        Mode = "json"
	Interactive Mode = "interactive"
)

func SelectMode(args []string, stdinTTY, stdoutTTY bool, getenv func(string) string) Mode {
	for _, a := range args {
		if a == "--json" || a == "--json=true" {
			return JSON
		}
	}
	for _, a := range args {
		if a == "--plain" || a == "--plain=true" {
			return Plain
		}
	}
	if !stdinTTY || !stdoutTTY || getenv("CI") != "" || getenv("TRIALM_NO_INPUT") != "" {
		return Plain
	}
	return Interactive
}

type exitError struct {
	Code    int
	Message string
}

func (e *exitError) Error() string { return e.Message }
func needs(flag, why string) error { return &exitError{3, why + "; use " + flag} }
func out() io.Writer {
	if jsonMode {
		return os.Stderr
	}
	return os.Stdout
}

var jsonMode bool
var selectedMode Mode
var currentVerb string
var events *ui.Renderer
var commandContext = context.Background()

func emit(kind string, data any, next ...core.Next) error {
	if events != nil {
		events.Handle(core.Event{Type: kind, Data: data, Next: next})
		return events.Err
	}
	return nil
}

// Main is the process boundary. Legacy aliases keep their characterization
// output for one release; the new verbs expose the versioned event contract.
func Main(ctx context.Context, argv []string, v string) int {
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()
	commandContext = ctx
	version = v
	jsonMode = false
	events = nil
	currentVerb = ""
	selectedMode = SelectMode(argv[1:], terminal.IsTTY(os.Stdin), terminal.IsTTY(os.Stdout), os.Getenv)
	args := append([]string(nil), argv[1:]...)
	args, _, _ = takeFlag(args, "plain", false)
	if len(args) > 1 && (args[0] == "--json" || args[0] == "--json=true") {
		args = append(args[1:], "--json")
	}
	if len(args) == 0 || strings.HasPrefix(args[0], "--json") {
		currentVerb = "home"
	} else {
		currentVerb = args[0]
		args = args[1:]
	}
	newVerb := map[string]bool{"home": true, "find": true, "try": true, "setup": true, "launch": true, "key": true, "update": true}
	if newVerb[currentVerb] {
		jsonMode = selectedMode == JSON
		events = &ui.Renderer{Out: os.Stdout, Human: os.Stderr, JSON: jsonMode}
	}
	migrateHome()
	if selectedMode == Interactive && (currentVerb == "home" || currentVerb == "find") {
		return finish(runTUI(ctx, currentVerb, args))
	}
	if selectedMode == Interactive && currentVerb != "try" && currentVerb != "setup" && !jsonMode && terminal.IsTTY(os.Stdin) && terminal.IsTTY(os.Stdout) && os.Getenv("CI") == "" && os.Getenv("TRIALM_NO_INPUT") == "" {
		if err := calibrationcli.MaybeOffer(argv[1:]); err != nil {
			return finish(err)
		}
	}
	var err error
	if backgroundable[currentVerb] && paths.Getenv("DETACHED") == "" {
		if rest, _, bg := takeFlag(args, "background", false); bg {
			rest, logPath, _ := takeFlag(rest, "log", true)
			err = startBackground(currentVerb, rest, logPath)
			return finish(err)
		}
	}
	switch currentVerb {
	case "home":
		if jsonMode {
			d, e := (core.Session{}).Detect(ctx, core.Options{})
			if e != nil {
				err = e
				break
			}
			history, _ := store.Default().History()
			if err = emit("detect", d); err == nil {
				err = emit("result", map[string]any{"version": v, "key_configured": store.Default().Key() != "", "trials": ui.TrialCards(history)}, homeNext()...)
			}
		} else {
			fmt.Fprint(out(), usage)
			homeUpdateNotice(ctx)
		}
	case "find":
		err = cmdRecommend(args)
	case "setup":
		err = cmdPlan(args)
	case "launch":
		args, _, _ = takeFlag(args, "json", false)
		text := "triaLM launch arrives in v0.5: start the saved engine and bridge, then open your coding tool."
		fmt.Fprintln(out(), text)
		if jsonMode {
			err = emit("result", map[string]any{"ready": false, "message": text}, core.Next{Cmd: "trialm setup --dry-run", Why: "review a setup plan"})
		}
	case "key":
		err = cmdKey(ctx, args)
	case "update":
		err = cmdUpdate(ctx, args)
	case "data":
		err = &exitError{4, "catalog data management arrives in v0.7; use trialm models"}
	case "calibrate":
		err = calibrationcli.Run(args)
	case "detect":
		err = cmdDetect(args)
	case "models":
		err = cmdModels(args)
	case "runtimes":
		err = cmdRuntimes(args)
	case "predict":
		err = cmdPredict(args)
	case "serve":
		err = cmdServe(args, false)
	case "try":
		err = cmdServe(args, true)
	case "bridge":
		err = cmdBridge(args)
	case "bg":
		err = proc.Background(ctx, args, out())
	case "inspect":
		err = cmdInspect(args)
	case "recommend":
		err = cmdRecommend(args)
	case "verify":
		err = cmdVerify(args)
	case "plan":
		err = cmdPlan(args)
	case "prompt":
		err = cmdPrompt(args)
	case "login":
		err = cmdKey(ctx, args)
	case "logout":
		err = store.Default().RemoveKey()
	case "share":
		err = cmdShare(args)
	case "config":
		err = cmdConfig(args)
	case "version", "--version", "-v":
		fmt.Fprintln(out(), "trialm", version)
	case "help", "--help", "-h":
		if len(args) > 0 && (args[0] == "advanced" || args[0] == "--all" || args[0] == "all") {
			fmt.Fprint(out(), usageAll)
		} else {
			fmt.Fprint(out(), usage)
		}
	default:
		err = &exitError{2, fmt.Sprintf("unknown command %q; use trialm help", currentVerb)}
	}
	return finish(err)
}
func homeNext() []core.Next {
	if store.Default().Key() == "" {
		return []core.Next{{Cmd: "trialm key", Why: "no OpenRouter key"}, {Cmd: "trialm find --json", Why: "find models without a key"}}
	}
	return []core.Next{{Cmd: "trialm find --json", Why: "rank local models"}}
}
func finish(err error) int {
	if err == nil {
		if commandContext.Err() != nil {
			return 130
		}
		if events != nil && events.Err != nil {
			return 1
		}
		return 0
	}
	code := 1
	var e *exitError
	if errors.As(err, &e) {
		code = e.Code
	}
	if errors.Is(err, flag.ErrHelp) {
		return 0
	}
	if errors.Is(err, context.Canceled) {
		code = 130
	}
	if strings.HasPrefix(err.Error(), "usage:") {
		code = 2
	}
	if code == 130 && !jsonMode {
		fmt.Fprintln(os.Stderr, "Stopped.")
		return code
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	if jsonMode {
		kind := "error"
		if code == 3 {
			kind = "needs_user"
		}
		_ = emit(kind, map[string]any{"message": err.Error()})
	}
	return code
}
