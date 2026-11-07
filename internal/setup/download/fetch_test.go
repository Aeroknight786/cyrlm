package download

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func digest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func TestResume(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefghij"), 10000)
	for _, mode := range []string{"range", "ignore", "changed"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			var received []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				received = append(received, r.Header.Get("Range"))
				tag := `"one"`
				if mode == "changed" && calls > 1 {
					tag = `"two"`
				}
				w.Header().Set("ETag", tag)
				if calls == 1 {
					w.Header().Set("Content-Length", strconv.Itoa(len(data)))
					w.WriteHeader(200)
					_, _ = w.Write(data[:12345])
					return
				}
				if mode == "range" && r.Header.Get("Range") != "" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 12345-%d/%d", len(data)-1, len(data)))
					w.WriteHeader(206)
					_, _ = w.Write(data[12345:])
					return
				}
				_, _ = w.Write(data)
			}))
			defer server.Close()
			r := Request{URL: server.URL, SHA256: digest(data), Size: int64(len(data)), Dest: filepath.Join(t.TempDir(), "asset")}
			if _, err := (Client{}).Fetch(context.Background(), r); err == nil {
				t.Fatal("expected interrupted transfer")
			}
			got, err := (Client{}).Fetch(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}
			b, err := os.ReadFile(r.Dest)
			if err != nil || !bytes.Equal(b, data) {
				t.Fatal("wrong bytes", err)
			}
			if received[1] != "bytes=12345-" {
				t.Fatal(received)
			}
			if mode == "range" && (!got.Resumed || got.Bytes != int64(len(data)-12345)) {
				t.Fatal(got)
			}
			if mode != "range" && calls != 3 {
				t.Fatal(calls)
			}
		})
	}
}
func TestDigestRetry(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte("bad")) }))
	defer server.Close()
	_, err := (Client{}).Fetch(context.Background(), Request{URL: server.URL, SHA256: digest([]byte("good")), Dest: filepath.Join(t.TempDir(), "x")})
	if !errors.Is(err, ErrDigest) || calls != 2 {
		t.Fatal(err, calls)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	data := bytes.Repeat([]byte("x"), 65536)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"a"`)
		w.Write(data)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "x")
	c := Client{Progress: func(Progress) { cancel() }}
	_, err := c.Fetch(ctx, Request{URL: server.URL, SHA256: digest(data), Dest: dest})
	if err == nil {
		t.Fatal("expected cancellation")
	}
	if st, err := os.Stat(dest + ".part"); err != nil || st.Size() == 0 {
		t.Fatal(st, err)
	}
}
func TestRedirectNoToken(t *testing.T) {
	final := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("token leaked")
		}
		w.Write([]byte("ok"))
	}))
	defer final.Close()
	start := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, final.URL, 302) }))
	defer start.Close()
	_, err := (Client{}).Fetch(context.Background(), Request{URL: start.URL, Token: "secret", SHA256: digest([]byte("ok")), Dest: filepath.Join(t.TempDir(), "x")})
	if err != nil {
		t.Fatal(err)
	}
}
func TestCandidatesAndReuse(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HF_HOME", "")
	t.Setenv("HF_HUB_CACHE", "")
	t.Setenv("OLLAMA_MODELS", "")
	b := []byte("model")
	sum := digest(b)
	paths := []string{filepath.Join(home, ".cache/huggingface/hub/models--org--model/blobs", sum), filepath.Join(home, ".lmstudio/models/org/model/file"), filepath.Join(home, ".cache/lm-studio/models/org/model/file"), filepath.Join(home, ".ollama/models/blobs/sha256-"+sum)}
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	got := Candidates(home, "org/model", "file", sum)
	if strings.Join(got, "|") != strings.Join(paths, "|") {
		t.Fatal(got)
	}
	for _, p := range got {
		dest := filepath.Join(t.TempDir(), "file")
		result, err := (Client{}).Fetch(context.Background(), Request{SHA256: sum, Size: int64(len(b)), Dest: dest, Reuse: []string{p}})
		if err != nil || result.Reused != p {
			t.Fatal(result, err)
		}
		a, _ := os.Stat(p)
		z, _ := os.Stat(dest)
		if !os.SameFile(a, z) {
			t.Fatal("expected hard link")
		}
	}
	if Candidates(home, "../bad", "file", sum) != nil || Candidates(home, "org/model", `..\secret`, sum) != nil {
		t.Fatal("unsafe path")
	}
}
func TestCutPoints(t *testing.T) {
	data := bytes.Repeat([]byte("0123456789"), 2048)
	for i := 1; i < 40; i++ {
		cut := 1 + (i*997)%(len(data)-1)
		t.Run(strconv.Itoa(cut), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"tag"`)
				if r.Header.Get("Range") != "" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", cut, len(data)-1, len(data)))
					w.WriteHeader(206)
					w.Write(data[cut:])
					return
				}
				w.Header().Set("Content-Length", strconv.Itoa(len(data)))
				w.Write(data[:cut])
			}))
			defer server.Close()
			req := Request{URL: server.URL, SHA256: digest(data), Dest: filepath.Join(t.TempDir(), "f")}
			_, _ = (Client{}).Fetch(context.Background(), req)
			if _, err := (Client{}).Fetch(context.Background(), req); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCopyFallback(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "source")
	dst := filepath.Join(dir, "destination")
	if err := os.WriteFile(src, []byte("contents"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := reuseWithLink(context.Background(), src, dst, func(string, string) error { return errors.New("cross-device") }); err != nil {
		t.Fatal(err)
	}
	a, _ := os.Stat(src)
	b, _ := os.Stat(dst)
	data, err := os.ReadFile(dst)
	if err != nil || string(data) != "contents" || os.SameFile(a, b) {
		t.Fatal(err)
	}
}
