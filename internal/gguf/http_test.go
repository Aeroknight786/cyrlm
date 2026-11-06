package gguf

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func serveContent(data []byte, hits *atomic.Int32, ua *atomic.Value) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if hits != nil {
			hits.Add(1)
		}
		if ua != nil {
			ua.Store(r.Header.Get("User-Agent"))
		}
		http.ServeContent(w, r, "model.gguf", time.Time{}, bytes.NewReader(data))
	}
}

func TestOpenURLRange(t *testing.T) {
	b := gptOSSBuilder()
	for i := range b.kvs {
		if b.kvs[i].key == "tokenizer.ggml.tokens" {
			b.kvs[i].val = tokens(5000) // ~65 KB header
		}
	}
	data := b.bytes(t)
	var hits atomic.Int32
	var ua atomic.Value
	srv := httptest.NewServer(serveContent(data, &hits, &ua))
	defer srv.Close()

	f, err := openURL(context.Background(), srv.URL+"/m.gguf", srv.Client(), readOptions{chunk: 4096, maxChunk: 16384, maxRead: DefaultMaxHeaderBytes})
	if err != nil {
		t.Fatal(err)
	}
	if f.FileSize != int64(len(data)) || f.Source != srv.URL+"/m.gguf" {
		t.Errorf("size %d source %s", f.FileSize, f.Source)
	}
	if hits.Load() < 3 {
		t.Errorf("only %d requests: chunking not exercised", hits.Load())
	}
	if f.HeaderBytesRead >= int64(len(data)) {
		t.Errorf("read %d of %d bytes: tensor data was downloaded", f.HeaderBytesRead, len(data))
	}
	if got := ua.Load().(string); !strings.HasPrefix(got, "cyrlm") {
		t.Errorf("User-Agent %q", got)
	}
	local, err := Parse(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	rs, ls := f.Summary(), local.Summary()
	if rs.ActiveBytes != ls.ActiveBytes || rs.KVBytesPerTokenF16 != ls.KVBytesPerTokenF16 || rs.VocabSize != 5000 || len(f.Tensors) != len(local.Tensors) {
		t.Errorf("remote summary differs from local: %+v vs %+v", rs, ls)
	}

	// Default options: one request covers this small file.
	hits.Store(0)
	if _, err := OpenURL(context.Background(), srv.URL, nil); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 1 {
		t.Errorf("%d requests with 1 MiB chunks", hits.Load())
	}
}

func TestOpenURLRedirect(t *testing.T) {
	data := llamaBuilder().bytes(t)
	var resolveHits, cdnHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/org/repo/resolve/main/m.gguf", func(w http.ResponseWriter, r *http.Request) {
		resolveHits.Add(1)
		http.Redirect(w, r, "/cdn/m.gguf?X-Signature=secret", http.StatusFound)
	})
	mux.HandleFunc("/cdn/m.gguf", serveContent(data, &cdnHits, nil))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	f, err := openURL(context.Background(), srv.URL+"/org/repo/resolve/main/m.gguf", srv.Client(), readOptions{chunk: 512, maxChunk: 512, maxRead: DefaultMaxHeaderBytes})
	if err != nil {
		t.Fatal(err)
	}
	if resolveHits.Load() != 1 || cdnHits.Load() < 3 {
		t.Errorf("resolve hits %d, cdn hits %d: later requests should go straight to the CDN", resolveHits.Load(), cdnHits.Load())
	}
	if f.Summary().TensorBytes != 939264 {
		t.Errorf("tensor bytes %d", f.Summary().TensorBytes)
	}
}

func TestOpenURLExpiredRedirectRetries(t *testing.T) {
	data := llamaBuilder().bytes(t)
	var cdnHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/resolve/m.gguf", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/cdn/m.gguf", http.StatusFound)
	})
	mux.HandleFunc("/cdn/m.gguf", func(w http.ResponseWriter, r *http.Request) {
		// Every second direct request is "expired".
		if cdnHits.Add(1)%2 == 0 && r.Header.Get("Referer") == "" {
			http.Error(w, "expired", http.StatusForbidden)
			return
		}
		http.ServeContent(w, r, "m.gguf", time.Time{}, bytes.NewReader(data))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := openURL(context.Background(), srv.URL+"/resolve/m.gguf", srv.Client(), readOptions{chunk: 512, maxChunk: 512, maxRead: DefaultMaxHeaderBytes}); err != nil {
		t.Fatal(err)
	}
}

// A server that ignores Range must not make us download the file.
func TestOpenURLIgnoresRange(t *testing.T) {
	const total = 1 << 30
	var written atomic.Int64
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(done)
		w.Header().Set("Content-Length", strconv.Itoa(total))
		w.WriteHeader(http.StatusOK)
		chunk := make([]byte, 64<<10)
		copy(chunk, "GGUF\x03\x00\x00\x00")
		for written.Load() < total {
			n, err := w.Write(chunk)
			written.Add(int64(n))
			if err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	start := time.Now()
	_, err := OpenURL(context.Background(), srv.URL+"/m.gguf", srv.Client())
	if !errors.Is(err, ErrRangeNotSupported) {
		t.Fatalf("err = %v, want ErrRangeNotSupported", err)
	}
	if !strings.Contains(err.Error(), "1073741824 bytes") {
		t.Errorf("error should report the size: %v", err)
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("server still writing 10s after the client gave up")
	}
	if w := written.Load(); w > 64<<20 {
		t.Errorf("server wrote %d bytes before the client hung up", w)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

// A tiny file served whole (Range ignored) is fine: it is all we asked for.
func TestOpenURLSmallFileWithoutRange(t *testing.T) {
	data := llamaBuilder().bytes(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	f, err := OpenURL(context.Background(), srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if f.FileSize != int64(len(data)) || len(f.Tensors) != 21 {
		t.Errorf("size %d tensors %d", f.FileSize, len(f.Tensors))
	}
}

func TestOpenURLErrors(t *testing.T) {
	data := llamaBuilder().bytes(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/missing.gguf", http.NotFound)
	mux.HandleFunc("/gated.gguf", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) })
	mux.HandleFunc("/page", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "x.html", time.Time{}, strings.NewReader("<!DOCTYPE html><html>"+strings.Repeat(" ", 5000)))
	})
	mux.HandleFunc("/bad-range", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 100-199/1000")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(make([]byte, 100))
	})
	mux.HandleFunc("/m.gguf", serveContent(data, nil, nil))
	srv := httptest.NewServer(mux)
	defer srv.Close()
	ctx := context.Background()

	var se *StatusError
	if _, err := OpenURL(ctx, srv.URL+"/missing.gguf", srv.Client()); !errors.As(err, &se) || se.Code != 404 {
		t.Errorf("404: %v", err)
	}
	if _, err := OpenURL(ctx, srv.URL+"/gated.gguf?token=abc", srv.Client()); !errors.As(err, &se) || se.Code != 401 || !strings.Contains(err.Error(), "gated") || strings.Contains(err.Error(), "abc") {
		t.Errorf("401: %v", err)
	}
	if _, err := OpenURL(ctx, srv.URL+"/page", srv.Client()); !errors.Is(err, ErrNotGGUF) {
		t.Errorf("html: %v", err)
	}
	if _, err := OpenURL(ctx, srv.URL+"/bad-range", srv.Client()); err == nil || !strings.Contains(err.Error(), "asked for bytes from 0") {
		t.Errorf("bad range: %v", err)
	}
	if _, err := OpenURL(ctx, "ftp://example.com/m.gguf", nil); err == nil {
		t.Error("ftp URL accepted")
	}
	// Header cap applies to downloads.
	if _, err := openURL(ctx, srv.URL+"/m.gguf", srv.Client(), readOptions{chunk: 256, maxChunk: 256, maxRead: 1024}); !errors.Is(err, ErrHeaderTooLarge) {
		t.Errorf("cap: %v", err)
	}
	// Cancelled context.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := OpenURL(cctx, srv.URL+"/m.gguf", srv.Client()); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled: %v", err)
	}
	// Truncated remote file: header only, tensor data missing.
	b := llamaBuilder()
	b.noData = true
	short := b.bytes(t)
	srv2 := httptest.NewServer(serveContent(short, nil, nil))
	defer srv2.Close()
	f, err := OpenURL(ctx, srv2.URL, srv2.Client())
	if err != nil || len(f.Warnings) == 0 {
		t.Errorf("truncated remote: %v %v", err, f)
	}
	// Remote file cut inside the header.
	srv3 := httptest.NewServer(serveContent(data[:200], nil, nil))
	defer srv3.Close()
	if _, err := openURL(ctx, srv3.URL, srv3.Client(), readOptions{chunk: 64, maxChunk: 64, maxRead: 1 << 20}); !errors.Is(err, ErrTruncated) {
		t.Errorf("cut header: %v", err)
	}
}

func TestParseContentRange(t *testing.T) {
	for _, c := range []struct {
		in                 string
		first, last, total int64
		ok                 bool
	}{
		{"bytes 0-1023/146515", 0, 1023, 146515, true},
		{"bytes 100-199/*", 100, 199, -1, true},
		{"bytes */5000", -1, -1, 5000, true},
		{"bytes 0-10/5", 0, 0, 0, false},
		{"bytes 10-5/100", 0, 0, 0, false},
		{"items 0-1/2", 0, 0, 0, false},
		{"bytes x-1/2", 0, 0, 0, false},
		{"", 0, 0, 0, false},
	} {
		f, l, tot, err := parseContentRange(c.in)
		if (err == nil) != c.ok || (c.ok && (f != c.first || l != c.last || tot != c.total)) {
			t.Errorf("parseContentRange(%q) = %d %d %d %v", c.in, f, l, tot, err)
		}
	}
}

// httpReaderAt must stop at the size it learned.
func TestHTTPReaderAtEOF(t *testing.T) {
	data := []byte("GGUF0123456789")
	srv := httptest.NewServer(serveContent(data, nil, nil))
	defer srv.Close()
	h := &httpReaderAt{ctx: context.Background(), client: srv.Client(), url: srv.URL, size: -1}
	buf := make([]byte, 100)
	n, err := h.ReadAt(buf, 0)
	if n != len(data) || err != io.EOF || h.size != int64(len(data)) {
		t.Errorf("ReadAt = %d %v size %d", n, err, h.size)
	}
	if n, err := h.ReadAt(buf, 50); n != 0 || err != io.EOF {
		t.Errorf("past EOF: %d %v", n, err)
	}
	h2 := &httpReaderAt{ctx: context.Background(), client: srv.Client(), url: srv.URL, size: -1}
	if n, err := h2.ReadAt(buf, 50); n != 0 || err != io.EOF {
		t.Errorf("416: %d %v", n, err)
	}
}
