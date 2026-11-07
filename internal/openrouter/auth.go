package openrouter

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

type PKCE struct{ Verifier, Challenge string }

func NewPKCE() (PKCE, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return PKCE{}, err
	}
	v := base64.RawURLEncoding.EncodeToString(b)
	h := sha256.Sum256([]byte(v))
	return PKCE{v, base64.RawURLEncoding.EncodeToString(h[:])}, nil
}
func (p PKCE) URL(authURL, callback string) string {
	if authURL == "" {
		authURL = "https://openrouter.ai/auth"
	}
	v := url.Values{"callback_url": {callback}, "code_challenge": {p.Challenge}, "code_challenge_method": {"S256"}}
	return authURL + "?" + v.Encode()
}

// Callback has an unpredictable path as well as a random loopback port. Only
// the browser that received the authorization URL knows this path.
type Callback struct {
	URL      string
	codes    chan string
	srv      *http.Server
	listener net.Listener
}

func ListenCallback() (*Callback, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 24)
	if _, err = rand.Read(b); err != nil {
		l.Close()
		return nil, err
	}
	path := "/callback/" + base64.RawURLEncoding.EncodeToString(b)
	c := &Callback{URL: "http://localhost:" + fmt.Sprint(l.Addr().(*net.TCPAddr).Port) + path, codes: make(chan string, 1), listener: l}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) {
		code := r.URL.Query().Get("code")
		if code == "" {
			http.Error(w, "No authorization code.", 400)
			return
		}
		select {
		case c.codes <- code:
			fmt.Fprintln(w, "triaLM is signed in. You can close this tab.")
		default:
			http.Error(w, "Already received.", 409)
		}
	})
	c.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = c.srv.Serve(l) }()
	return c, nil
}
func (c *Callback) Close() { _ = c.srv.Close() }
func (c *Callback) Wait(ctx context.Context) (string, error) {
	select {
	case code := <-c.codes:
		return code, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
