package ui

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
	"io"
	"net/url"
	"strings"
	"time"
)

type KeyFlow struct {
	Store    store.Store
	Client   openrouter.Client
	AuthURL  string
	Open     func(string) error
	Secret   func(string) (string, error)
	Headless bool

	Output io.Writer
	Emit   func(string, any, ...core.Next) error
	Needs  func(string, string) error
}

func (f KeyFlow) Run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("key", flag.ContinueOnError)
	paste := fs.Bool("paste", false, "paste a key into a masked terminal input")
	remove := fs.Bool("remove", false, "remove stored keys")
	headless := fs.Bool("headless", f.Headless, "copy an authorization code in a terminal")
	fs.Bool("json", false, "NDJSON output")
	err := fs.Parse(args)
	pos := fs.Args()
	if err != nil {
		return err
	}
	if len(pos) > 0 {
		return errors.New("usage: trialm key [--paste|--headless|--remove]")
	}
	if *paste && *remove {
		return errors.New("usage: choose --paste or --remove")
	}
	if *remove {
		if err := f.Store.RemoveKey(); err != nil {
			return err
		}
		fmt.Fprintln(f.Output, "Stored OpenRouter key removed. OPENROUTER_API_KEY still takes precedence when set.")
		return f.Emit("result", map[string]any{"removed": true}, core.Next{Cmd: "trialm key", Why: "sign in again"})
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
			return f.Needs("--paste", "no key was entered")
		}
	} else if key == "" {
		pkce, err := openrouter.NewPKCE()
		if err != nil {
			return err
		}
		callback, err := openrouter.ListenCallback()
		if err != nil {
			return f.Needs("--paste", "cannot bind a localhost OAuth callback")
		}
		defer callback.Close()
		auth := pkce.URL(f.AuthURL, callback.URL)
		// This URL contains the public challenge, never a key, code or verifier.
		if err := f.Emit("needs_user", map[string]any{"needs": "authorization", "url": auth, "run": "trialm key --headless"}); err != nil {
			return err
		}
		var code string
		if *headless {
			fmt.Fprintln(f.Output, "Open this URL on a browser, authorize triaLM, then copy the code from the redirect URL:", auth)
			code, err = f.Secret("Authorization code: ")
			if err != nil {
				return err
			}
			if u, e := url.Parse(code); e == nil && u.Query().Get("code") != "" {
				code = u.Query().Get("code")
			}
		} else {
			if err = f.Open(auth); err != nil {
				return f.Needs("--headless or --paste", "cannot open a browser")
			}
			fmt.Fprintln(f.Output, "Authorize triaLM in your browser.")
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
		return f.Needs("--paste", "no OpenRouter key")
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
	fmt.Fprintln(f.Output, "OpenRouter ready ·", credit.String(), "credit")
	return f.Emit("result", map[string]any{"key_configured": true, "balance": credit}, core.Next{Cmd: "trialm find --json", Why: "find a model"})
}
