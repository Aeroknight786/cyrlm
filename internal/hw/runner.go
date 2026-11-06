package hw

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	// probeTimeout bounds every external command.
	probeTimeout = 3 * time.Second
	// httpTimeout bounds loopback HTTP probes of runtime servers.
	httpTimeout = 1500 * time.Millisecond
	// detectBudget bounds Detect and DetectRuntimes as a whole.
	detectBudget = 4500 * time.Millisecond
	// maxCmdOutput caps how much command output is kept.
	maxCmdOutput = 8 << 20
)

// runner is the side-effect boundary of the package: everything the
// orchestrators learn about the machine comes through it, so tests can
// substitute fixtures. Implementations must be safe for concurrent use.
type runner interface {
	// Run executes name with args and returns its standard output, or its
	// standard error when stdout is empty (some tools print versions there).
	// Output is returned even when the command exits non-zero.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	ReadFile(path string) ([]byte, error)
	Glob(pattern string) ([]string, error)
	LookPath(file string) (string, error)
	Exists(path string) bool
	Getenv(key string) string
	// HTTPGet fetches a loopback URL and returns the status and (capped) body.
	HTTPGet(ctx context.Context, url string) (status int, body []byte, err error)
}

// osRunner is the real runner.
type osRunner struct{}

func (osRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	stdout := &cappedBuffer{max: maxCmdOutput}
	stderr := &cappedBuffer{max: 256 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	// Don't hang on grandchildren that keep the pipes open.
	cmd.WaitDelay = 500 * time.Millisecond
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("%s: timed out", filepath.Base(name))
		} else {
			err = fmt.Errorf("%s: %w", filepath.Base(name), ctx.Err())
		}
	}
	out := stdout.Bytes()
	if len(bytes.TrimSpace(out)) == 0 {
		out = stderr.Bytes()
	}
	return out, err
}

func (osRunner) ReadFile(path string) ([]byte, error)  { return os.ReadFile(path) }
func (osRunner) Glob(pattern string) ([]string, error) { return filepath.Glob(pattern) }
func (osRunner) LookPath(file string) (string, error)  { return exec.LookPath(file) }
func (osRunner) Getenv(key string) string              { return os.Getenv(key) }
func (osRunner) Exists(path string) bool               { _, err := os.Stat(path); return err == nil }
func (osRunner) HTTPGet(ctx context.Context, u string) (int, []byte, error) {
	return loopbackGet(ctx, u)
}

var loopbackClient = &http.Client{
	Timeout: httpTimeout,
	Transport: &http.Transport{
		Proxy:                 nil, // loopback only; never via a proxy
		DialContext:           (&net.Dialer{Timeout: httpTimeout}).DialContext,
		DisableKeepAlives:     true,
		ResponseHeaderTimeout: httpTimeout,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func loopbackGet(ctx context.Context, rawURL string) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, err
	}
	if !isLoopbackHost(u.Hostname()) {
		return 0, nil, fmt.Errorf("refusing to probe non-loopback URL %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, err
	}
	resp, err := loopbackClient.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	return resp.StatusCode, body, err
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// cappedBuffer keeps at most max bytes and silently discards the rest, so a
// chatty child never blocks or fails on a full pipe.
type cappedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.buf.Len(); room > 0 {
		if len(p) > room {
			c.buf.Write(p[:room])
		} else {
			c.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *cappedBuffer) Bytes() []byte {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Bytes()
}

// parallel runs fns concurrently and waits for all of them.
func parallel(fns ...func()) {
	var wg sync.WaitGroup
	wg.Add(len(fns))
	for _, f := range fns {
		go func() {
			defer wg.Done()
			f()
		}()
	}
	wg.Wait()
}
