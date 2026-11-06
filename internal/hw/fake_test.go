package hw

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type fakeOut struct {
	out string
	err error
}

type fakeHTTP struct {
	status int
	body   string
}

// fakeRunner serves fixtures. Commands are matched first exactly against
// "name arg1 arg2...", then by the longest key that is a substring of it.
type fakeRunner struct {
	cmds   map[string]fakeOut
	files  map[string]string
	paths  map[string]string // LookPath name -> path
	exists map[string]bool
	env    map[string]string
	http   map[string]fakeHTTP
	// realFS falls back to the OS for ReadFile/Glob (sysfs trees in t.TempDir()).
	realFS bool

	mu    sync.Mutex
	calls []string
}

func (f *fakeRunner) record(s string) {
	f.mu.Lock()
	f.calls = append(f.calls, s)
	f.mu.Unlock()
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	line := strings.TrimSpace(name + " " + strings.Join(args, " "))
	f.record("run " + line)
	if o, ok := f.cmds[line]; ok {
		return []byte(o.out), o.err
	}
	best := ""
	for k := range f.cmds {
		if strings.Contains(line, k) && len(k) > len(best) {
			best = k
		}
	}
	if best != "" {
		return []byte(f.cmds[best].out), f.cmds[best].err
	}
	return nil, &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (f *fakeRunner) ReadFile(p string) ([]byte, error) {
	f.record("read " + p)
	if s, ok := f.files[p]; ok {
		return []byte(s), nil
	}
	if f.realFS {
		return os.ReadFile(p)
	}
	return nil, os.ErrNotExist
}

func (f *fakeRunner) Glob(pattern string) ([]string, error) {
	cands := map[string]bool{}
	add := func(p string) {
		for p != "" && p != "/" && p != "." {
			cands[p] = true
			np := dirOf(p)
			if np == p {
				break
			}
			p = np
		}
	}
	for p := range f.files {
		add(p)
	}
	for p, ok := range f.exists {
		if ok {
			add(p)
		}
	}
	slash := func(s string) string { return strings.ReplaceAll(s, `\`, "/") }
	var out []string
	for c := range cands {
		if ok, _ := path.Match(slash(pattern), slash(c)); ok {
			out = append(out, c)
		}
	}
	if f.realFS {
		m, _ := filepath.Glob(pattern)
		out = append(out, m...)
	}
	sort.Strings(out)
	return out, nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if p, ok := f.paths[name]; ok {
		return p, nil
	}
	return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
}

func (f *fakeRunner) Exists(p string) bool {
	if f.exists[p] {
		return true
	}
	if _, ok := f.files[p]; ok {
		return true
	}
	if f.realFS {
		_, err := os.Stat(p)
		return err == nil
	}
	return false
}

func (f *fakeRunner) Getenv(k string) string { return f.env[k] }

func (f *fakeRunner) HTTPGet(_ context.Context, u string) (int, []byte, error) {
	f.record("http " + u)
	if r, ok := f.http[u]; ok {
		return r.status, []byte(r.body), nil
	}
	return 0, nil, errors.New("connection refused")
}

func (f *fakeRunner) called(prefix string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// fixture reads testdata/name.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// writeTree creates files (relative path -> content) under root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func hasNote(s *System, substr string) bool {
	for _, n := range s.Notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

const giB = uint64(1) << 30
const miB = uint64(1) << 20
