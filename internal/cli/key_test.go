package cli

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/openrouter"
	"github.com/aeroknight786/trialm/internal/store"
)

func TestKeyFlowsAgainstFake(t *testing.T) {
	for _, mode := range []string{"browser", "headless", "paste", "env", "stored"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("OPENROUTER_API_KEY", "")
			jsonMode = false
			events = nil
			const secret = "sk-test-secret-never-output"
			var challenge string
			var calls []string
			fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls = append(calls, r.URL.Path)
				if r.Header.Get("X-Title") != "triaLM" || r.Header.Get("HTTP-Referer") != "https://github.com/aeroknight786/trialm" {
					t.Error("missing attribution")
				}
				switch r.URL.Path {
				case "/api/v1/auth/keys":
					var body map[string]string
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					h := sha256.Sum256([]byte(body["code_verifier"]))
					actual := base64.RawURLEncoding.EncodeToString(h[:])
					if body["code"] != "auth-code" || body["code_challenge_method"] != "S256" || len(body["code_verifier"]) < 43 {
						t.Error("invalid PKCE exchange")
					}
					if challenge != "" && actual != challenge {
						t.Error("S256 challenge mismatch")
					}
					_ = json.NewEncoder(w).Encode(map[string]string{"key": secret})
				case "/api/v1/credits":
					if r.Header.Get("Authorization") != "Bearer "+secret {
						t.Error("wrong key")
					}
					_, _ = w.Write([]byte(`{"data":{"total_credits":10,"total_usage":0.29}}`))
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer fake.Close()
			s := store.Store{Dir: t.TempDir()}
			if mode == "env" {
				t.Setenv("OPENROUTER_API_KEY", secret)
			}
			if mode == "stored" {
				if err := s.SaveKey(secret); err != nil {
					t.Fatal(err)
				}
			}
			opened := false
			f := keyFlow{Store: s, Client: openrouter.Client{BaseURL: fake.URL + "/api/v1", HTTP: fake.Client()}, AuthURL: fake.URL + "/auth", Open: func(auth string) error {
				opened = true
				u, err := url.Parse(auth)
				if err != nil {
					t.Fatal(err)
				}
				challenge = u.Query().Get("code_challenge")
				if u.Query().Get("code_challenge_method") != "S256" {
					t.Error("wrong challenge method")
				}
				callback, _ := url.Parse(u.Query().Get("callback_url"))
				callback.Host = strings.Replace(callback.Host, "localhost", "127.0.0.1", 1)
				q := callback.Query()
				q.Set("code", "auth-code")
				callback.RawQuery = q.Encode()
				resp, err := http.Get(callback.String())
				if err == nil {
					resp.Body.Close()
				}
				return err
			}, Secret: func(prompt string) (string, error) {
				if mode == "paste" {
					return secret, nil
				}
				if mode == "headless" {
					return "auth-code", nil
				}
				t.Fatal("unexpected secret prompt")
				return "", nil
			}}
			args := []string{}
			if mode == "paste" {
				args = []string{"--paste"}
			}
			if mode == "headless" {
				args = []string{"--headless"}
			}
			if err := f.run(context.Background(), args); err != nil {
				t.Fatal(err)
			}
			if opened != (mode == "browser") {
				t.Fatal("browser side effect", mode, opened)
			}
			if s.Key() != secret {
				t.Fatal("key not retained")
			}
			info, err := os.Stat(filepath.Join(s.Dir, "credentials"))
			if mode == "env" {
				if !os.IsNotExist(err) {
					t.Fatal("env key was persisted")
				}
			} else if err != nil || info.Mode().Perm() != 0600 {
				t.Fatal("credentials must be 0600", info, err)
			}
			if calls[len(calls)-1] != "/api/v1/credits" {
				t.Fatal(calls)
			}
			if err := f.run(context.Background(), []string{"--remove"}); err != nil {
				t.Fatal(err)
			}
			if mode != "env" && s.Key() != "" {
				t.Fatal("remove kept a key")
			}
		})
	}
}
func TestAuthErrorsNeverEchoSecrets(t *testing.T) {
	const secret = "do-not-print-this-secret"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(401); _, _ = w.Write([]byte(secret)) }))
	defer fake.Close()
	c := openrouter.Client{BaseURL: fake.URL, Key: secret}
	_, err := c.Credits(context.Background())
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
	_, err = c.Exchange(context.Background(), secret, secret)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatal(err)
	}
}
