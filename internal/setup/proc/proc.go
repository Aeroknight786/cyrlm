// Package proc owns detached engine processes and their health checks.
package proc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"
)

type Options struct {
	Argv, Env            []string
	PIDFile, Log, Health string
	Timeout              time.Duration
	DiscoverHealth       bool
	Lines                func([]string)
}
type Record struct {
	Log    string   `json:"log"`
	PID    int      `json:"pid"`
	Argv   []string `json:"argv"`
	Env    []string `json:"env,omitempty"`
	Health string   `json:"health"`
}

func Health(ctx context.Context, url string) bool {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
func Status(ctx context.Context, path string) (Record, bool) {
	var r Record
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &r) != nil {
		return r, false
	}
	return r, Health(ctx, r.Health)
}

var endpointLine = regexp.MustCompile(`Endpoints on (http://127\.0\.0\.1:\d+)`)

func Start(ctx context.Context, o Options) (Record, error) {
	if len(o.Argv) == 0 {
		return Record{}, errors.New("empty process argv")
	}
	if r, ok := Status(ctx, o.PIDFile); ok {
		if slices.Equal(r.Argv, o.Argv) && slices.Equal(r.Env, o.Env) {
			return r, nil
		}
		if err := Stop(o.PIDFile); err != nil {
			return r, err
		}
	}
	if o.Health != "" && Health(ctx, o.Health) {
		return Record{}, fmt.Errorf("port already occupied at %s", o.Health)
	}
	if o.Timeout == 0 {
		o.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	if err := os.MkdirAll(filepath.Dir(o.Log), 0700); err != nil {
		return Record{}, err
	}
	log, err := os.OpenFile(o.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return Record{}, err
	}
	defer log.Close()
	c := exec.Command(o.Argv[0], o.Argv[1:]...)
	c.Env = append(os.Environ(), o.Env...)
	c.Stdout, c.Stderr = log, log
	DetachAttrs(c)
	if err = c.Start(); err != nil {
		return Record{}, err
	}
	r := Record{PID: c.Process.Pid, Argv: o.Argv, Env: o.Env, Log: o.Log, Health: o.Health}
	persist := func() error {
		if o.PIDFile == "" {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(o.PIDFile), 0700); err != nil {
			return err
		}
		b, _ := json.Marshal(r)
		return os.WriteFile(o.PIDFile, b, 0600)
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	if err = persist(); err != nil {
		_ = Terminate(c.Process)
		return r, err
	}
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = Terminate(c.Process)
			<-exited
			_ = os.Remove(o.PIDFile)
			return r, ctx.Err()
		case err := <-exited:
			_ = os.Remove(o.PIDFile)
			return r, fmt.Errorf("engine exited (%v): %s", err, strings.Join(Tail(o.Log, 5), "\n"))
		case <-ticker.C:
			lines := Tail(o.Log, 5)
			if o.Lines != nil {
				o.Lines(lines)
			}
			if o.DiscoverHealth {
				if m := endpointLine.FindStringSubmatch(strings.Join(Tail(o.Log, 100), "\n")); m != nil {
					r.Health = m[1] + "/health"
				}
			}
			if Health(ctx, r.Health) {
				return r, persist()
			}
		}
	}
}
func Stop(path string) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var r Record
	if err = json.Unmarshal(b, &r); err != nil {
		return err
	}
	if r.PID <= 0 {
		return errors.New("invalid engine pid")
	}
	p, err := os.FindProcess(r.PID)
	if err == nil {
		err = Terminate(p)
		_ = p.Release()
	}
	if err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && Health(context.Background(), r.Health) {
		time.Sleep(20 * time.Millisecond)
	}
	return os.Remove(path)
}
func Tail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) > n {
			lines = lines[1:]
		}
	}
	return lines
}

func WaitHealth(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		if Health(ctx, url) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
