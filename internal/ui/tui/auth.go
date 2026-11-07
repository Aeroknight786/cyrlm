package tui

import (
	"context"
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
