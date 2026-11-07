package tui

import (
	"context"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
)

// Authorize never sends a secret to the model or terminal renderer.
func Authorize(ctx context.Context, s store.Store, c openrouter.Client, authURL string, open func(string) error, pasted string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	key := strings.TrimSpace(pasted)
	if key == "" {
		pkce, err := openrouter.NewPKCE()
		if err != nil {
			return err
		}
		cb, err := openrouter.ListenCallback()
		if err != nil {
			return err
		}
		defer cb.Close()
		if err = open(pkce.URL(authURL, cb.URL)); err != nil {
			return err
		}
		code, err := cb.Wait(ctx)
		if err != nil {
			return err
		}
		key, err = c.Exchange(ctx, code, pkce.Verifier)
		if err != nil {
			return err
		}
	}
	c.Key = key
	if _, err := c.Credits(ctx); err != nil {
		return err
	}
	return s.SaveKey(key)
}

func OpenBrowser(u string) error {
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

type headlessLogin struct {
	store    store.Store
	client   openrouter.Client
	verifier string
	URL      string
	close    func()
}

func beginHeadless(s store.Store, c openrouter.Client, authURL string) (*headlessLogin, error) {
	p, e := openrouter.NewPKCE()
	if e != nil {
		return nil, e
	}
	cb, e := openrouter.ListenCallback()
	if e != nil {
		return nil, e
	}
	return &headlessLogin{store: s, client: c, verifier: p.Verifier, URL: p.URL(authURL, cb.URL), close: func() { cb.Close() }}, nil
}
func (h *headlessLogin) finish(ctx context.Context, code string) error {
	defer h.close()
	if u, e := url.Parse(code); e == nil && u.Query().Get("code") != "" {
		code = u.Query().Get("code")
	}
	key, e := h.client.Exchange(ctx, strings.TrimSpace(code), h.verifier)
	if e != nil {
		return e
	}
	c := h.client
	c.Key = key
	if _, e = c.Credits(ctx); e != nil {
		return e
	}
	return h.store.SaveKey(key)
}
