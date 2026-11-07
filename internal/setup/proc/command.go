package proc

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/paths"
)

func Detach(ctx context.Context, output io.Writer, argv []string, logPath, readyURL string, timeout time.Duration, self bool) error {
	env := []string{}
	if self {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		argv = append([]string{exe}, argv...)
		env = []string{"TRIALM_DETACHED=1", "CYRLM_DETACHED=1"}
	}
	root, err := paths.Home()
	if err != nil {
		return err
	}
	r, err := Start(ctx, Options{Argv: argv, Env: env, PIDFile: filepath.Join(root, "run", filepath.Base(logPath)+".pid"), Log: logPath, Health: readyURL, Timeout: timeout, DiscoverHealth: self})
	if err != nil {
		return err
	}
	if self {
		b, _ := os.ReadFile(logPath)
		fmt.Fprint(output, string(b))
	}
	fmt.Fprintf(output, "✓ Started in the background and answering at %s (pid %d). Log: %s\n", strings.TrimSuffix(r.Health, "/health"), r.PID, logPath)
	return nil
}

// Background runs any long-lived program (e.g. llama-server) the same way:
// detached, logged, and confirmed ready by a URL before returning.
//
//	trialm bg --log ~/.trialm/logs/llama.log --ready http://127.0.0.1:8080/health -- llama-server …
func Background(ctx context.Context, args []string, output io.Writer) error {
	fs := flag.NewFlagSet("bg", flag.ContinueOnError)
	logPath := fs.String("log", "", "log file (required)")
	ready := fs.String("ready", "", "URL that answers 2xx once the program is ready (required)")
	timeout := fs.Duration("timeout", 10*time.Minute, "how long to wait for --ready (model loads can be slow)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	argv := fs.Args()
	if *logPath == "" || *ready == "" || len(argv) == 0 {
		return errors.New("usage: trialm bg --log FILE --ready URL [--timeout 10m] -- program args…")
	}
	if strings.HasPrefix(*logPath, "~/") {
		home, _ := os.UserHomeDir()
		*logPath = filepath.Join(home, (*logPath)[2:])
	}
	_ = os.MkdirAll(filepath.Dir(*logPath), 0o755)
	if Health(ctx, *ready) {
		return fmt.Errorf("something already answers at %s: stop it first or use another port", *ready)
	}
	return Detach(ctx, output, argv, *logPath, *ready, *timeout, false)
}
