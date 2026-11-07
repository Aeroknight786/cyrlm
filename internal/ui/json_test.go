package ui

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
)

func TestFiguresPreserveBandAndNext(t *testing.T) {
	var b bytes.Buffer
	r := Renderer{Out: &b, JSON: true}
	r.Handle(core.Event{Type: "result", Data: map[string]any{"kind": "calibrated", "decode_tps": 40, "decode_error_range_pct": 60, "prefill_error_range_pct": 70, "follow_up_turn_s": 10}})
	if r.Err != nil {
		t.Fatal(r.Err)
	}
	var e map[string]any
	if err := json.Unmarshal(b.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if n, ok := e["next"].([]any); !ok || len(n) != 0 {
		t.Fatal(e)
	}
	d := e["data"].(map[string]any)
	f := d["decode_tps"].(map[string]any)
	if f["lo"] != float64(16) || f["hi"] != float64(64) || f["kind"] != "calibrated" || f["unit"] != "tok/s" {
		t.Fatal(f)
	}
	typed := figure.Band(40, 60, "tok/s", figure.Simulated, "bands:test")
	got := Numbers(typed).(map[string]any)
	if got["src"] != "bands:test" {
		t.Fatal(got)
	}
}

// UI formatting of speeds and times must go through figure. Existing plain
// layouts use string cells; new raw float/unit formats fail this guard.
func TestNoRawSpeedOrTimeFloatFormats(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	bad := regexp.MustCompile(`%[+\-#0-9.]*[fgeEG][ \t]*(tok/s|tokens/s|seconds|s\b|ms\b)`)
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		fs := token.NewFileSet()
		f, err := parser.ParseFile(fs, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok || id.Name != "fmt" {
				return true
			}
			for _, arg := range call.Args {
				lit, ok := arg.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				s, err := strconv.Unquote(lit.Value)
				if err == nil && bad.MatchString(s) {
					t.Errorf("%s: raw float speed/time; format a figure.Figure", fs.Position(lit.Pos()))
				}
			}
			return true
		})
	}
}
func TestRendererPlainNoEscapes(t *testing.T) {
	var b bytes.Buffer
	r := Renderer{Out: &b}
	r.Handle(core.Event{Type: "result", Data: "triaLM ready"})
	if r.Err != nil || bytes.ContainsRune(b.Bytes(), '\x1b') {
		t.Fatal(b.String(), r.Err)
	}
}
