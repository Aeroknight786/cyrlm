package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func matches(ctx context.Context, path string, size int64, digest string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || (size > 0 && size != st.Size()) {
		return false
	}
	h := sha256.New()
	_, err = io.Copy(h, &contextReader{ctx, f})
	return err == nil && hex.EncodeToString(h.Sum(nil)) == digest
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

func reuse(ctx context.Context, src, dst string) error {
	return reuseWithLink(ctx, src, dst, os.Link)
}

func reuseWithLink(ctx context.Context, src, dst string, link func(string, string) error) error {
	// A temporary destination keeps existing files intact on errors.
	f, err := os.CreateTemp(filepath.Dir(dst), ".download-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	f.Close()
	os.Remove(tmp)
	defer os.Remove(tmp)
	if err = link(src, tmp); err != nil {
		in, e := os.Open(src)
		if e != nil {
			return e
		}
		defer in.Close()
		out, e := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if e != nil {
			return e
		}
		_, err = io.Copy(out, &contextReader{ctx, in})
		if err == nil {
			err = out.Sync()
		}
		if e = out.Close(); err == nil {
			err = e
		}
		if err != nil {
			return err
		}
	}
	return os.Rename(tmp, dst)
}

// Candidates returns existing cache paths for a repository, filename and digest.
func Candidates(home, repo, file, sha string) []string {
	// Reject traversal and absolute paths in repository-controlled names.
	safe := func(s string) bool {
		s = strings.ReplaceAll(s, "\\", "/")
		if strings.HasPrefix(s, "/") || strings.Contains(s, ":") {
			return false
		}
		for _, p := range strings.Split(s, "/") {
			if p == ".." || p == "" {
				return false
			}
		}
		return true
	}
	if !safe(repo) || !safe(file) || len(sha) != 64 {
		return nil
	}
	if _, err := hex.DecodeString(sha); err != nil {
		return nil
	}
	hub := os.Getenv("HF_HUB_CACHE")
	if hub == "" {
		hub = os.Getenv("HF_HOME")
		if hub == "" {
			hub = filepath.Join(home, ".cache", "huggingface")
		}
		hub = filepath.Join(hub, "hub")
	}
	ollama := os.Getenv("OLLAMA_MODELS")
	if ollama == "" {
		ollama = filepath.Join(home, ".ollama", "models")
	}
	paths := []string{filepath.Join(hub, "models--"+strings.ReplaceAll(repo, "/", "--"), "blobs", sha), filepath.Join(home, ".lmstudio", "models", repo, file), filepath.Join(home, ".cache", "lm-studio", "models", repo, file), filepath.Join(ollama, "blobs", "sha256-"+sha)}
	var found []string
	for _, p := range paths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			found = append(found, p)
		}
	}
	return found
}
