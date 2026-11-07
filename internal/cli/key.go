package cli

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
)

type keyFlow struct {
	Store    store.Store
	Client   openrouter.Client
	AuthURL  string
	Open     func(string) error
	Secret   func(string) (string, error)
	Headless bool
}

func cmdKey(ctx context.Context, args []string) error {
	f := keyFlow{Store: store.Default(), Open: openBrowser, Secret: readSecret, Headless: os.Getenv("SSH_CONNECTION") != "" || (runtime.GOOS != "darwin" && runtime.GOOS != "windows" && os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "")}
	return f.run(ctx, args)
}
func (f keyFlow) run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key", flag.ContinueOnError)
	paste := fs.Bool("paste", false, "paste a key into a masked terminal input")
	remove := fs.Bool("remove", false, "remove stored keys")
	headless := fs.Bool("headless", f.Headless, "copy an authorization code in a terminal")
	fs.Bool("json", false, "NDJSON output")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return &exitError{2, "usage: trialm key [--paste|--headless|--remove]"}
	}
	if *paste && *remove {
		return &exitError{2, "choose --paste or --remove"}
	}
	if *remove {
		if err := f.Store.RemoveKey(); err != nil {
			return err
		}
		fmt.Fprintln(out(), "Stored OpenRouter key removed. OPENROUTER_API_KEY still takes precedence when set.")
		return emit("result", map[string]any{"removed": true}, core.Next{Cmd: "trialm key", Why: "sign in again"})
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	key := f.Store.Key()
	if *paste {
		key, err = f.Secret("OpenRouter key: ")
		if err != nil {
			return err
		}
		if strings.TrimSpace(key) == "" {
			return needs("--paste", "no key was entered")
		}
	} else if key == "" {
		pkce, err := openrouter.NewPKCE()
		if err != nil {
			return err
		}
		callback, err := openrouter.ListenCallback()
		if err != nil {
			return needs("--paste", "cannot bind a localhost OAuth callback")
		}
		defer callback.Close()
		auth := pkce.URL(f.AuthURL, callback.URL)
		// This URL contains the public challenge, never a key, code or verifier.
		if err := emit("needs_user", map[string]any{"needs": "authorization", "url": auth, "run": "trialm key --headless"}); err != nil {
			return err
		}
		var code string
		if *headless {
			fmt.Fprintln(out(), "Open this URL on a browser, authorize triaLM, then copy the code from the redirect URL:", auth)
			code, err = f.Secret("Authorization code: ")
			if err != nil {
				return err
			}
			if u, e := url.Parse(code); e == nil && u.Query().Get("code") != "" {
				code = u.Query().Get("code")
			}
		} else {
			if err = f.Open(auth); err != nil {
				return needs("--headless or --paste", "cannot open a browser")
			}
			fmt.Fprintln(out(), "Authorize triaLM in your browser.")
			code, err = callback.Wait(ctx)
		}
		if err != nil {
			return err
		}
		key, err = f.Client.Exchange(ctx, strings.TrimSpace(code), pkce.Verifier)
		if err != nil {
			return err
		}
	}
	if strings.TrimSpace(key) == "" {
		return needs("--paste", "no OpenRouter key")
	}
	c := f.Client
	c.Key = key
	balance, err := c.Credits(ctx)
	if err != nil {
		return err
	}
	if *paste || f.Store.Key() == "" {
		if err = f.Store.SaveKey(key); err != nil {
			return err
		}
	}
	credit := figure.Exact(balance, "USD", figure.Measured, "openrouter:/credits")
	fmt.Fprintln(out(), "OpenRouter ready ·", credit.String(), "credit")
	return emit("result", map[string]any{"key_configured": true, "balance": credit}, core.Next{Cmd: "trialm find --json", Why: "find a model"})
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
