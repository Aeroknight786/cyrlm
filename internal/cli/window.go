package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/terminal"
	"github.com/aeroknight786/trialm/internal/ui/tui"
	"github.com/aeroknight786/trialm/internal/ui/window"
)

func runWindow(ctx context.Context, args []string, w io.Writer) error {
	err := window.Run(ctx, store.Default(), args, w)
	var need *window.NeedsTerminal
	if errors.As(err, &need) {
		_ = emit("needs_user", map[string]any{"needs": "terminal", "run": need.Run}, core.Next{Cmd: need.Run, Why: "open this in your terminal"})
		return &exitError{3, need.Error()}
	}
	return err
}
func windowArgs(args []string) []string {
	a := append([]string(nil), args...)
	for _, flag := range []string{"window", "json", "launch", "plain", "here"} {
		a, _, _ = takeFlag(a, flag, false)
	}
	return a
}
func printClaims(jsonOut bool) error {
	cs := tui.Claims()
	if jsonOut {
		return emit("result", map[string]any{"claims": cs}, core.Next{Cmd: "trialm find", Why: "rank simulated recipes"})
	}
	fmt.Fprintln(out(), "Published claims · unverified by triaLM · never ranked")
	for _, c := range cs {
		fmt.Fprintf(out(), "\n%s [%s]\n%s\n%s\n", c.ID, c.Grade, c.Hardware, c.Recipe)
		for _, f := range c.Figures {
			fmt.Fprintln(out(), " ", f.String())
		}
		fmt.Fprintln(out(), c.SourceName, "·", c.Date, "·", c.DateBasis)
		fmt.Fprintln(out(), c.SourceURL)
	}
	return nil
}
func autoWindow() bool {
	return window.Automatic(os.Getenv, terminal.IsTTY(os.Stdin)) && os.Getenv("SSH_CONNECTION") == "" && !strings.Contains(strings.ToLower(os.Getenv("TERM_PROGRAM")), "vscode")
}
