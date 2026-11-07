package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/server"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/proc"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/ui/window"
)

func cmdLaunch(args []string) error {
	fs := flag.NewFlagSet("launch", flag.ContinueOnError)
	printOnly := fs.Bool("print", false, "print environment and command")
	serve := fs.Bool("serve", false, "serve an IDE route in the foreground")
	stop := fs.Bool("stop", false, "stop the saved engine")
	check := fs.Bool("check", false, "repeat Ready verification")
	slug := fs.String("setup", "", "saved setup slug")
	here := fs.Bool("here", false, "hand off in this terminal")
	win := fs.Bool("window", false, "open in a terminal window")
	idle := fs.String("idle-token", "", "private idle-stop generation")
	_ = fs.Bool("json", false, "NDJSON events")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 1 {
		return errors.New("usage: trialm launch [tool] [--setup slug] [--print|--serve|--stop|--check]")
	}
	session := core.Session{}
	executor, err := session.SetupExecutor()
	if err != nil {
		return err
	}
	if *idle != "" {
		return executor.IdleStop(commandContext, *slug, *idle, 15*time.Minute)
	}
	tool := ""
	if len(pos) > 0 {
		tool = pos[0]
	}
	if *serve && tool != "cline" && tool != "continue" && tool != "zed" {
		return &exitError{2, "--serve needs cline, continue or zed"}
	}
	rows, err := session.ReadySetups(commandContext)
	if err != nil {
		return err
	}
	if *slug == "" && len(rows) > 0 {
		*slug = rows[0].Plan.Slug
	}
	if len(rows) == 0 {
		_ = emit("needs_user", map[string]any{"needs": "setup", "run": "trialm setup"})
		return needs("trialm setup", "no Ready setup exists")
	}

	token := ""
	if !*printOnly && !*stop {
		token, err = executor.LaunchLease(*slug, "")
		if err != nil {
			return err
		}
		defer func() {
			generation, e := executor.LaunchLease(*slug, token)
			if e != nil {
				return
			}
			exe, e := os.Executable()
			if e != nil {
				return
			}
			c := proc.Options{Argv: []string{exe, "launch", "--setup", *slug, "--idle-token", generation}, Log: filepath.Join(executor.Root, "logs", *slug, "idle.log")}
			_ = proc.Timer(c)
		}()
	}
	logPath := filepath.Join(executor.Root, "logs", *slug, "launch.log")
	var log *os.File
	if !*printOnly {
		if err = os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
			return err
		}
		log, err = os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		defer log.Close()
	}
	l, err := session.Launch(commandContext, core.LaunchOptions{Slug: *slug, Tool: tool, Print: *printOnly, Stop: *stop, Check: *check, Serve: *serve, Log: log, Admin: setup.OSAdmin})
	if err != nil {
		if *check && l.Journal.Report != nil {
			fmt.Fprint(out(), l.Journal.Report.Plain())
		}
		if errors.Is(err, core.ErrNoReady) {
			return needs("trialm setup", err.Error())
		}
		return setupError(err)
	}
	if *stop {
		fmt.Fprintln(out(), "Engine stopped.")
		return emit("result", map[string]any{"stopped": true})
	}
	if l.Bridge != nil {
		defer l.Bridge.Shutdown(context.Background())
	}
	if *check {
		fmt.Fprint(out(), l.Journal.Report.Plain())
		return emit("result", l.Journal.Report)
	}
	if *printOnly {
		w := l.Wiring
		w.Config = ""
		w.Steps = nil
		w.Notes = nil
		fmt.Fprint(out(), w.Render())
		return emit("result", map[string]any{"env": l.Wiring.Env, "command": l.Wiring.Command})
	}
	if *serve {
		h, _ := harness.Get(tool)
		snippet, err := h.Snippet(harness.Options{Model: l.Journal.Plan.LocalModelName, ContextTokens: l.Journal.Plan.ContextTokens, Home: executor.Root}, l.Endpoint)
		if err != nil {
			return err
		}
		if err = snippet.WriteFiles(); err != nil {
			return err
		}
		fmt.Fprint(out(), snippet.Render())
		for _, content := range snippet.Files {
			fmt.Fprintln(out(), content)
		}
		fmt.Fprintln(out(), "Local route", l.Endpoint, "· ctrl-c stops the route · engine stays warm 15 min")
		return server.Meter(commandContext, out(), l.Bridge)
	}
	if !*printOnly && !*serve && !*stop && !*check && !*here && (*win || autoWindow()) {
		_ = emit("launch", map[string]any{"started": true, "engine": l.Journal.Plan.Runtime, "endpoint": l.Endpoint, "command": l.Wiring.Command, "env": l.Wiring.Env})
		err = (window.Launcher{Verb: "launch"}).Run(commandContext, store.Default(), window.Args(args), out())
		var need *window.NeedsTerminal
		if errors.As(err, &need) {
			_ = emit("needs_user", map[string]any{"needs": "terminal", "run": need.Run})
			return &exitError{3, need.Error()}
		}
		return err
	}
	_ = emit("launch", map[string]any{"started": true, "engine": l.Journal.Plan.Runtime, "endpoint": l.Endpoint, "command": l.Wiring.Command, "env": l.Wiring.Env})
	return l.Wiring.Launch()
}
