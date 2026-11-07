// Package selfupdate downloads a release, verifies its checksum and atomically
// replaces a caller-supplied executable. Tests only supply temporary files.
package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
)

const Latest = "https://api.github.com/repos/Aeroknight786/trialm/releases/latest"

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
}
type Release struct {
	Version string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}
type Client struct {
	LatestURL string
	HTTP      *http.Client
	OS, Arch  string
}

func (c Client) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "triaLM updater")
	req.Header.Set("Accept", "application/vnd.github+json")
	hc := c.HTTP
	if hc == nil {
		hc = &http.Client{Timeout: 45 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("release download: HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("release asset exceeds size limit")
	}
	return b, err
}
func (c Client) Check(ctx context.Context) (Release, error) {
	u := c.LatestURL
	if u == "" {
		u = Latest
	}
	b, err := c.get(ctx, u, 1<<20)
	var r Release
	if err != nil {
		return r, err
	}
	err = json.Unmarshal(b, &r)
	if err == nil && r.Version == "" {
		err = fmt.Errorf("release has no version")
	}
	return r, err
}
func Newer(remote, current string) bool {
	parse := func(s string) ([3]int, bool) {
		var n [3]int
		s = strings.TrimPrefix(s, "v")
		if strings.ContainsAny(s, "-+") {
			return n, false
		}
		p := strings.Split(s, ".")
		if len(p) != 3 {
			return n, false
		}
		for i, v := range p {
			var err error
			n[i], err = strconv.Atoi(v)
			if err != nil || n[i] < 0 {
				return n, false
			}
		}
		return n, true
	}
	a, ok := parse(remote)
	if !ok {
		return false
	}
	b, ok := parse(current)
	if !ok {
		return true
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
func (c Client) Install(ctx context.Context, r Release, target string) error {
	goos, arch := c.OS, c.Arch
	if goos == "" {
		goos = runtime.GOOS
	}
	if arch == "" {
		arch = runtime.GOARCH
	}
	ext := ".tar.gz"
	binary := "trialm"
	if goos == "windows" {
		ext = ".zip"
		binary += ".exe"
	}
	stem := "trialm_" + goos + "_" + arch
	name := stem + ext
	var archiveURL, sumsURL string
	for _, a := range r.Assets {
		if a.Name == name {
			archiveURL = a.URL
		}
		if a.Name == "checksums.txt" {
			sumsURL = a.URL
		}
	}
	if archiveURL == "" || sumsURL == "" {
		return fmt.Errorf("release lacks %s or checksums.txt", name)
	}
	sums, err := c.get(ctx, sumsURL, 1<<20)
	if err != nil {
		return err
	}
	var want string
	for _, line := range strings.Split(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if want != "" {
				return fmt.Errorf("duplicate release checksum")
			}
			want = strings.ToLower(fields[0])
		}
	}
	if raw, err := hex.DecodeString(want); err != nil || len(raw) != sha256.Size {
		return fmt.Errorf("release checksum is missing or invalid")
	}
	archive, err := c.get(ctx, archiveURL, 64<<20)
	if err != nil {
		return err
	}
	got := fmt.Sprintf("%x", sha256.Sum256(archive))
	if got != want {
		return fmt.Errorf("sha256 mismatch for %s", name)
	}
	payload, err := extract(archive, stem+"/"+binary, ext)
	if err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(target)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(resolved), ".trialm-update-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(info.Mode().Perm()); err == nil {
		_, err = f.Write(payload)
	}
	if err == nil {
		err = f.Sync()
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return replace(f.Name(), resolved)
}
func extract(b []byte, name, ext string) ([]byte, error) {
	const limit = 32 << 20
	read := func(r io.Reader, n int64) ([]byte, error) {
		if n <= 0 || n > limit {
			return nil, fmt.Errorf("invalid release binary size")
		}
		b, err := io.ReadAll(io.LimitReader(r, n+1))
		if int64(len(b)) != n {
			return nil, fmt.Errorf("truncated release binary")
		}
		return b, err
	}
	if ext == ".zip" {
		z, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
		if err != nil {
			return nil, err
		}
		for _, f := range z.File {
			if f.Name == name && f.Mode().IsRegular() {
				r, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer r.Close()
				return read(r, int64(f.UncompressedSize64))
			}
		}
	} else {
		g, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		defer g.Close()
		t := tar.NewReader(g)
		for {
			h, err := t.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, err
			}
			if h.Name == name && h.Typeflag == tar.TypeReg {
				return read(t, h.Size)
			}
		}
	}
	return nil, fmt.Errorf("release archive has no %s", name)
}

type Notice struct {
	CheckedAt time.Time `json:"checked_at"`
	Version   string    `json:"version"`
}

func Cached(s store.Store) Notice { var n Notice; _ = s.Read("update.json", &n); return n }

// Claim uses O_EXCL across processes, and records failed attempts too. No
// home command waits for a network request. A stale claim expires daily.
func Claim(s store.Store, now time.Time) bool {
	if n := Cached(s); now.Sub(n.CheckedAt) < 24*time.Hour {
		return false
	}
	if os.MkdirAll(s.Dir, 0700) != nil {
		return false
	}
	lock := filepath.Join(s.Dir, "update-check.lock")
	if st, err := os.Stat(lock); err == nil && now.Sub(st.ModTime()) >= 24*time.Hour {
		_ = os.Remove(lock)
	}
	f, err := os.OpenFile(lock, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return false
	}
	_ = f.Close()
	n := Cached(s)
	n.CheckedAt = now
	if s.Write("update.json", n) != nil {
		_ = os.Remove(lock)
		return false
	}
	return true
}
func (c Client) Refresh(ctx context.Context, s store.Store) error {
	r, err := c.Check(ctx)
	n := Cached(s)
	if err == nil {
		n.Version = r.Version
	}
	n.CheckedAt = time.Now()
	writeErr := s.Write("update.json", n)
	_ = os.Remove(filepath.Join(s.Dir, "update-check.lock"))
	if err != nil {
		return err
	}
	return writeErr
}
