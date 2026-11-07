package openrouter

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestCallbackScopeAndCancellation(t *testing.T) {
	c, err := ListenCallback()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	u, _ := url.Parse(c.URL)
	u.Host = strings.Replace(u.Host, "localhost", "127.0.0.1", 1)
	bad := *u
	bad.Path = "/callback/guess"
	r, err := http.Get(bad.String() + "?code=guess")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 404 {
		t.Fatal(r.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if _, err = c.Wait(ctx); err == nil {
		t.Fatal("wait ignored cancellation")
	}
	r, err = http.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	if r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
	r, err = http.Get(u.String() + "?code=ok")
	if err != nil {
		t.Fatal(err)
	}
	r.Body.Close()
	code, err := c.Wait(context.Background())
	if err != nil || code != "ok" {
		t.Fatal(code, err)
	}
}
