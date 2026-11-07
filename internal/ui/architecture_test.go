package ui

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCharmOnlyInUI(t *testing.T) {
	root := ".."
	e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, e := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if e != nil {
			return e
		}
		for _, imp := range f.Imports {
			name, _ := strconv.Unquote(imp.Path.Value)
			thirdParty := strings.Contains(strings.Split(name, "/")[0], ".") && !strings.HasPrefix(name, "github.com/aeroknight786/trialm/")
			if thirdParty && !strings.HasPrefix(filepath.ToSlash(path), "../ui/") {
				t.Errorf("third-party import outside ui: %s: %s", path, name)
			}
		}
		if strings.HasPrefix(filepath.ToSlash(path), "../ui/") {
			b, _ := os.ReadFile(path)
			for _, bad := range []string{"HasDarkBackground(", "RequestBackgroundColor", "WithAltScreen", ".WithTheme(huh.ThemeCharm"} {
				if strings.Contains(string(b), bad) {
					t.Errorf("%s: forbidden terminal behavior %s", path, bad)
				}
			}
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile("tui/chat.go")
	if e != nil || strings.Count(string(b), "\n") > 250 {
		t.Fatal("chat screen exceeds 250 lines", e)
	}
}
