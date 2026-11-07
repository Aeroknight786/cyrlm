package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/selfupdate"
	"github.com/aeroknight786/trialm/internal/store"
)

func homeUpdateNotice(ctx context.Context) {
	s := store.Default()
	n := selfupdate.Cached(s)
	if selfupdate.Newer(n.Version, version) {
		fmt.Fprintln(out(), n.Version, "available · trialm update")
	}
	if os.Getenv("TRIALM_OFFLINE") == "1" || os.Getenv("CI") != "" || !selfupdate.Claim(s, time.Now()) {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "update", "--check")
	detachAttrs(cmd)
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return
	}
	defer null.Close()
	cmd.Stdin = null
	cmd.Stdout = null
	cmd.Stderr = null
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}
func cmdUpdate(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "refresh the cached update notice")
	fs.Bool("json", false, "NDJSON output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return &exitError{2, "usage: trialm update [--json]"}
	}
	c := selfupdate.Client{}
	if *check {
		return c.Refresh(ctx, store.Default())
	}
	r, err := c.Check(ctx)
	if err != nil {
		return err
	}
	if !selfupdate.Newer(r.Version, version) {
		fmt.Fprintln(out(), "triaLM is up to date.")
		return emit("result", map[string]any{"version": version, "updated": false}, core.Next{Cmd: "trialm find", Why: "find a model"})
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if err = c.Install(ctx, r, exe); err != nil {
		return err
	}
	fmt.Fprintln(out(), "Updated triaLM to", r.Version)
	return emit("result", map[string]any{"version": r.Version, "updated": true}, core.Next{Cmd: "trialm", Why: "open the updated version"})
}
