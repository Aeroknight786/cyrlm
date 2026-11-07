package cli

import (
	"context"
	"os"
	"os/exec"
	"runtime"

	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/ui"
)

type keyFlow ui.KeyFlow

func cmdKey(ctx context.Context, args []string) error {
	f := keyFlow{Store: store.Default(), Open: openBrowser, Secret: readSecret, Headless: os.Getenv("SSH_CONNECTION") != "" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "")}
	return f.run(ctx, args)
}
func (f keyFlow) run(ctx context.Context, args []string) error {
	flow := ui.KeyFlow(f)
	flow.Output = out()
	flow.Emit = emit
	flow.Needs = needs
	return flow.Run(ctx, args)
}

func openBrowser(u string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	return cmd.Run()
}
