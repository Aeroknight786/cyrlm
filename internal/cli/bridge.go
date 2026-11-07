package cli

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/setup/proc"
)

// ---------------------------------------------------------------- background

// backgroundable commands accept --background: trialm re-runs itself fully
// detached (own session / process group, no terminal), logs to a file, and
// returns only after the server answers — a printed pid or a shell exit is
// not proof that it is running (agent command runners kill the process
// group of `nohup … &` jobs when the command returns).
var backgroundable = map[string]bool{"try": true, "serve": true, "bridge": true}

// takeFlag removes a boolean flag or a flag with a value from args.
func takeFlag(args []string, name string, hasValue bool) ([]string, string, bool) {
	var out []string
	val, found := "", false
	for i := 0; i < len(args); i++ {
		a := args[i]
		for _, p := range []string{"--" + name, "-" + name} {
			switch {
			case a == p:
				found = true
				if hasValue && i+1 < len(args) {
					val = args[i+1]
					i++
				}
				goto next
			case strings.HasPrefix(a, p+"="):
				found, val = true, a[len(p)+1:]
				goto next
			}
		}
		out = append(out, a)
	next:
	}
	return out, val, found
}

// startBackground runs `trialm <cmd> <args>` detached and waits until it
// serves, then prints its output so far.
func startBackground(cmd string, args []string, logPath string) error {
	if logPath == "" {
		dir := filepath.Join(trialmHome(), "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		logPath = filepath.Join(dir, cmd+".log")
	}
	return proc.Detach(commandContext, out(), append([]string{cmd}, args...), logPath, "", 90*time.Second, true)
}
