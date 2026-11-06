package gguf

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// UserAgent is sent with every HTTP request.
const UserAgent = "cyrlm (gguf header reader)"

// StatusError is returned when the server answers with an unexpected HTTP
// status.
type StatusError struct {
	URL  string // without query string
	Code int
	Hint string
}

func (e *StatusError) Error() string {
	s := fmt.Sprintf("gguf: GET %s: HTTP %d %s", e.URL, e.Code, http.StatusText(e.Code))
	if e.Hint != "" {
		s += " (" + e.Hint + ")"
	}
	return s
}

// OpenURL parses the GGUF header of a remote file using HTTP Range requests,
// downloading only the header (typically a few MiB; at most
// DefaultMaxHeaderBytes). Redirects are followed (Hugging Face
// .../resolve/main/x.gguf links redirect to a CDN) and later requests go
// straight to the final URL. If the server ignores Range and starts sending
// the whole file, OpenURL stops and returns ErrRangeNotSupported. client may
// be nil (http.DefaultClient, which has no timeout: bound ctx instead); pass
// one with an authorizing transport for gated repositories. File.FileSize
// comes from Content-Range.
func OpenURL(ctx context.Context, rawURL string, client *http.Client) (*File, error) {
	return openURL(ctx, rawURL, client, defaultOptions)
}

func openURL(ctx context.Context, rawURL string, client *http.Client, o readOptions) (*File, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("gguf: bad URL: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("gguf: unsupported URL scheme %q", u.Scheme)
	}
	if client == nil {
		client = http.DefaultClient
	}
	h := &httpReaderAt{ctx: ctx, client: client, url: rawURL, size: -1}
	if o.chunk <= 0 {
		o.chunk = defaultOptions.chunk
	}
	// The first request establishes the file size (Content-Range).
	first := make([]byte, min(int64(o.chunk), max(o.maxRead, 1)))
	n, err := h.ReadAt(first, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	w := newWindowReader(h, h.size, o)
	w.prime(first[:n])
	f, err := parse(w)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", redact(rawURL), err)
	}
	f.Source = rawURL
	return f, nil
}

// httpReaderAt implements io.ReaderAt with one Range request per call.
type httpReaderAt struct {
	ctx    context.Context
	client *http.Client
	url    string
	final  string // URL after redirects, used for later requests
	size   int64  // -1 until known
}

func (h *httpReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if h.size >= 0 && off >= h.size {
		return 0, io.EOF
	}
	target := h.url
	if h.final != "" {
		target = h.final
	}
	n, err := h.get(target, p, off)
	var se *StatusError
	if target != h.url && errors.As(err, &se) && se.Code >= 400 && se.Code < 500 {
		// A signed CDN URL may have expired: go through the original again.
		h.final = ""
		n, err = h.get(h.url, p, off)
	}
	return n, err
}

func (h *httpReaderAt) get(target string, p []byte, off int64) (int, error) {
	req, err := http.NewRequestWithContext(h.ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, fmt.Errorf("gguf: %w", err)
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", off, off+int64(len(p))-1))
	req.Header.Set("User-Agent", UserAgent)
	// Compressed responses would make byte ranges meaningless.
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := h.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("gguf: GET %s: %w", redact(target), err)
	}
	// Closing without draining abandons the connection, so an unwanted
	// full-file body is never downloaded.
	defer resp.Body.Close()
	if resp.Request != nil && resp.Request.URL != nil {
		h.final = resp.Request.URL.String()
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		start, last, total, err := parseContentRange(resp.Header.Get("Content-Range"))
		if err != nil {
			return 0, fmt.Errorf("gguf: GET %s: %w", redact(target), err)
		}
		if start != off || last < start {
			return 0, fmt.Errorf("gguf: GET %s: asked for bytes from %d, server sent %d-%d", redact(target), off, start, last)
		}
		if total >= 0 {
			h.size = total
		}
		want := min(int64(len(p)), last-start+1)
		n, err := io.ReadFull(resp.Body, p[:want])
		if err != nil {
			return n, fmt.Errorf("gguf: GET %s: reading body: %w", redact(target), err)
		}
		if n < len(p) {
			return n, io.EOF
		}
		return n, nil
	case http.StatusOK:
		// Range ignored. Accept it only when the whole file fits in what was
		// asked for (tiny file); otherwise stop instead of downloading it.
		if off == 0 && resp.ContentLength >= 0 && resp.ContentLength <= int64(len(p)) {
			n, err := io.ReadFull(resp.Body, p[:resp.ContentLength])
			if err != nil {
				return n, fmt.Errorf("gguf: GET %s: reading body: %w", redact(target), err)
			}
			h.size = resp.ContentLength
			if n < len(p) {
				return n, io.EOF
			}
			return n, nil
		}
		size := "unknown size"
		if resp.ContentLength >= 0 {
			size = strconv.FormatInt(resp.ContentLength, 10) + " bytes"
		}
		return 0, fmt.Errorf("%w: GET %s returned 200 with the full file (%s); not downloading it", ErrRangeNotSupported, redact(target), size)
	case http.StatusRequestedRangeNotSatisfiable:
		if _, _, total, err := parseContentRange(resp.Header.Get("Content-Range")); err == nil && total >= 0 {
			h.size = total
		}
		return 0, io.EOF
	}
	se := &StatusError{URL: redact(target), Code: resp.StatusCode}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		se.Hint = "gated or private repository? pass an http.Client that sends an Authorization header"
	case http.StatusNotFound:
		se.Hint = "check the repository, revision and file name"
	}
	return 0, se
}

// parseContentRange parses "bytes first-last/total"; total is -1 for "*".
// "bytes */total" (from a 416) yields first = last = -1.
func parseContentRange(s string) (first, last, total int64, err error) {
	bad := fmt.Errorf("bad Content-Range %q", s)
	rest, ok := strings.CutPrefix(strings.TrimSpace(s), "bytes ")
	if !ok {
		return 0, 0, 0, bad
	}
	rng, tot, ok := strings.Cut(rest, "/")
	if !ok {
		return 0, 0, 0, bad
	}
	total = -1
	if tot != "*" {
		if total, err = strconv.ParseInt(tot, 10, 64); err != nil || total < 0 {
			return 0, 0, 0, bad
		}
	}
	if rng == "*" {
		return -1, -1, total, nil
	}
	a, b, ok := strings.Cut(rng, "-")
	if !ok {
		return 0, 0, 0, bad
	}
	if first, err = strconv.ParseInt(a, 10, 64); err != nil || first < 0 {
		return 0, 0, 0, bad
	}
	if last, err = strconv.ParseInt(b, 10, 64); err != nil || last < first {
		return 0, 0, 0, bad
	}
	if total >= 0 && last >= total {
		return 0, 0, 0, bad
	}
	return first, last, total, nil
}

// redact drops the query string (CDN URLs carry signatures) and userinfo.
func redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "<url>"
	}
	u.RawQuery, u.Fragment, u.User = "", "", nil
	return u.String()
}
