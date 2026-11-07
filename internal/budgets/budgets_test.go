// Package budgets enforces PLAN §9's ceilings from P1a onward. Source lines
// are non-test lines containing Go tokens (comments and blank lines excluded).
package budgets

import (
	"go/scanner"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func sourceLines(t *testing.T, path string) int {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fs := token.NewFileSet()
	f := fs.AddFile(path, -1, len(b))
	var sc scanner.Scanner
	sc.Init(f, b, nil, 0)
	lines := map[int]bool{}
	for {
		p, k, lit := sc.Scan()
		if k == token.EOF {
			break
		}
		if k == token.SEMICOLON && lit == "\n" {
			continue
		}
		start := fs.Position(p).Line
		end := start + strings.Count(lit, "\n")
		for n := start; n <= end; n++ {
			lines[n] = true
		}
	}
	return len(lines)
}
func TestSourceBudgets(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	packages := map[string]int{}
	total, commandPhysical := 0, 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" || d.Name() == "bin" || d.Name() == "dist" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if !strings.HasPrefix(rel, "internal"+string(filepath.Separator)) && !strings.HasPrefix(rel, "cmd"+string(filepath.Separator)) {
			return nil
		}
		n := sourceLines(t, path)
		total += n
		dir := filepath.ToSlash(filepath.Dir(rel))
		packages[dir] += n
		if dir == "cmd/trialm" {
			b, _ := os.ReadFile(path)
			commandPhysical += strings.Count(string(b), "\n")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// v0.3 has no separate global number in the plan; adopt the stricter v0.4
	// ceiling now. The frozen wire, GGUF and hw exceptions match §9.
	if total > 29000 { // v0.5 gate, PLAN §9 (raised from 28.5k on 2026-11-07)
		t.Errorf("source lines %d > 29000", total)
	}
	if commandPhysical > 30 {
		t.Errorf("cmd/trialm %d physical lines > 30", commandPhysical)
	}
	cliUI := packages["internal/cli"]
	for pkg, n := range packages {
		if pkg == "internal/ui" || strings.HasPrefix(pkg, "internal/ui/") {
			cliUI += n
		}
	}
	if cliUI > 5500 {
		t.Errorf("cli+ui %d > 5500", cliUI)
	}
	wire := 0
	for pkg, n := range packages {
		if strings.HasPrefix(pkg, "internal/api/") {
			wire += n
		}
	}
	if wire > 4400 {
		t.Errorf("api wire lines %d > 4400", wire)
	}
	for pkg, n := range packages {
		limit := 2500
		if strings.HasPrefix(pkg, "internal/api/") {
			continue
		}
		if pkg == "internal/gguf" {
			limit = 2500
		}
		if pkg == "internal/hw" {
			limit = 2600
		}
		if n > limit {
			t.Errorf("%s: %d > %d lines", pkg, n, limit)
		}
	}
	t.Logf("product source %d/29000; cmd/trialm %d/30 physical; cli+ui %d/5500", total, commandPhysical, cliUI)
}
func TestStrippedBinaryBudget(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	target := filepath.Join(t.TempDir(), "trialm")
	cmd := exec.Command("go", "build", "-trimpath", "-ldflags=-s -w", "-o", target, "./cmd/trialm")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 14000000 {
		t.Errorf("stripped binary %d > 14000000 bytes", info.Size())
	}
	t.Logf("stripped binary %d/14000000 bytes", info.Size())
}
