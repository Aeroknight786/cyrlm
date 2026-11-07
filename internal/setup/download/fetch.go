package download

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type metadata struct{ URL, Validator string }

// defaultClient has no overall timeout (files are many GB) but gives up on a
// server that stops answering; a cancelled or failed fetch resumes later.
var defaultClient = http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 60 * time.Second,
	IdleConnTimeout:       90 * time.Second,
}}

// Fetch retrieves and verifies one file. Partial bytes and their validator are
// kept beside the destination so interrupted transfers can be resumed.
func (c Client) Fetch(ctx context.Context, r Request) (Result, error) {
	result := Result{Path: r.Dest}
	digest, err := hex.DecodeString(r.SHA256)
	if err != nil || len(digest) != sha256.Size || r.SHA256 != strings.ToLower(r.SHA256) {
		return result, fmt.Errorf("download: invalid sha256")
	}
	if r.Dest == "" {
		return result, fmt.Errorf("download: destination is empty")
	}
	if err := os.MkdirAll(filepath.Dir(r.Dest), 0755); err != nil {
		return result, err
	}
	for _, path := range r.Reuse {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if matches(ctx, path, r.Size, r.SHA256) {
			if err := reuse(ctx, path, r.Dest); err != nil {
				return result, err
			}
			result.Reused = path
			if c.Progress != nil {
				info, _ := os.Stat(r.Dest)
				c.Progress(Progress{Done: info.Size(), Total: info.Size(), Reused: path})
			}
			return result, nil
		}
	}
	part, meta := r.Dest+".part", r.Dest+".part.json"
	for attempt := 0; attempt < 2; attempt++ {
		n, resumed, sum, err := c.transfer(ctx, r, part, meta)
		result.Bytes += n
		result.Resumed = result.Resumed || resumed
		if err != nil {
			return result, err
		}
		// transfer verifies the digest as it writes.
		if sum == r.SHA256 {
			if err := os.Rename(part, r.Dest); err != nil {
				return result, err
			}
			_ = os.Remove(meta)
			return result, nil
		}
		_ = os.Remove(part)
		_ = os.Remove(meta)
	}
	return result, ErrDigest
}

func (c Client) transfer(ctx context.Context, r Request, part, meta string) (int64, bool, string, error) {
	var m metadata
	if b, err := os.ReadFile(meta); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	f, err := os.OpenFile(part, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return 0, false, "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, false, "", err
	}
	offset := st.Size()
	if m.URL != r.URL || m.Validator == "" || (r.Size > 0 && offset > r.Size) {
		offset = 0
	}
	for {
		remaining := r.Size - offset
		if remaining < 0 {
			remaining = 0
		}
		if free := freeBytes(filepath.Dir(part)); free >= 0 && (remaining > free || free-remaining < 1_000_000_000) {
			return 0, false, "", fmt.Errorf("download: not enough disk space")
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL, nil)
		if err != nil {
			return 0, false, "", err
		}
		if strings.EqualFold(req.URL.Hostname(), "huggingface.co") && r.Token != "" {
			req.Header.Set("Authorization", "Bearer "+r.Token)
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
			req.Header.Set("If-Range", m.Validator)
		}
		client := defaultClient
		if c.HTTP != nil {
			client = *c.HTTP
		}
		originalRedirect := client.CheckRedirect
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			// Never forward credentials to a redirect, even another HF URL.
			req.Header.Del("Authorization")
			if originalRedirect != nil {
				err := originalRedirect(req, via)
				req.Header.Del("Authorization")
				return err
			}
			if len(via) >= 10 {
				return fmt.Errorf("download: too many redirects")
			}
			return nil
		}
		resp, err := client.Do(req)
		if err != nil {
			return 0, false, "", err
		}
		validator := resp.Header.Get("ETag")
		if strings.HasPrefix(validator, "W/") {
			validator = ""
		}
		if validator == "" {
			validator = resp.Header.Get("Last-Modified")
		}
		if offset > 0 && (resp.StatusCode != http.StatusPartialContent || validator != m.Validator || !validRange(resp.Header.Get("Content-Range"), offset)) {
			resp.Body.Close()
			offset = 0
			continue
		}
		if resp.StatusCode != http.StatusOK && !(offset > 0 && resp.StatusCode == http.StatusPartialContent) {
			resp.Body.Close()
			return 0, false, "", fmt.Errorf("download: HTTP %s", resp.Status)
		}
		if r.Size == 0 && resp.ContentLength >= 0 {
			if free := freeBytes(filepath.Dir(part)); free >= 0 && (resp.ContentLength > free || free-resp.ContentLength < 1_000_000_000) {
				resp.Body.Close()
				return 0, false, "", fmt.Errorf("download: not enough disk space")
			}
		}
		if offset == 0 {
			if err = f.Truncate(0); err != nil {
				resp.Body.Close()
				return 0, false, "", err
			}
		}
		h := sha256.New()
		if _, err = f.Seek(0, io.SeekStart); err == nil && offset > 0 {
			_, err = io.CopyN(h, f, offset)
		}
		if err != nil {
			resp.Body.Close()
			return 0, false, "", err
		}
		m = metadata{r.URL, validator}
		b, _ := json.Marshal(m)
		if err = os.WriteFile(meta, b, 0600); err != nil {
			resp.Body.Close()
			return 0, false, "", err
		}
		total := r.Size
		if total == 0 && resp.ContentLength >= 0 {
			total = offset + resp.ContentLength
		}
		pw := &progressWriter{h: h, f: f, callback: c.Progress, done: offset, total: total, start: time.Now()}
		n, err := io.Copy(pw, resp.Body)
		closeErr := resp.Body.Close()
		if err == nil {
			err = closeErr
		}
		if err == nil {
			err = f.Sync()
		}
		if err != nil {
			return n, offset > 0, "", err
		}
		return n, offset > 0, hex.EncodeToString(h.Sum(nil)), nil
	}
}

func validRange(value string, offset int64) bool {
	if !strings.HasPrefix(value, "bytes ") {
		return false
	}
	first, _, ok := strings.Cut(strings.TrimPrefix(value, "bytes "), "-")
	n, err := strconv.ParseInt(first, 10, 64)
	return ok && err == nil && n == offset
}

type progressWriter struct {
	h           hash.Hash
	f           *os.File
	callback    func(Progress)
	done, total int64
	start, last time.Time
	lastBytes   int64
	rate        float64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	_, _ = w.h.Write(p[:n])
	w.done += int64(n)
	now := time.Now()
	if w.callback != nil && now.Sub(w.last) >= 250*time.Millisecond {
		elapsed := now.Sub(w.last).Seconds()
		if w.last.IsZero() {
			elapsed = now.Sub(w.start).Seconds()
		}
		if elapsed > 0 {
			rate := float64(w.done-w.lastBytes) / elapsed
			if w.last.IsZero() {
				rate = float64(n) / elapsed
			}
			if w.rate == 0 {
				w.rate = rate
			} else {
				w.rate = .25*rate + .75*w.rate
			}
		}
		var eta time.Duration
		if w.total > w.done && w.rate > 0 {
			eta = time.Duration(float64(w.total-w.done) / w.rate * float64(time.Second))
		}
		w.callback(Progress{Done: w.done, Total: w.total, BytesPerSec: w.rate, ETA: eta})
		w.last = now
		w.lastBytes = w.done
	}
	return n, err
}
