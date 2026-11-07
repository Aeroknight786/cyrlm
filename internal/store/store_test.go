package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	goruntime "runtime"
	"testing"
)

func TestCredentialsAndPreferences(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")
	s := Store{Dir: t.TempDir()}
	if err := s.Write("config.json", map[string]string{"openrouter_key": "legacy", "other": "kept"}); err != nil {
		t.Fatal(err)
	}
	if s.Key() != "legacy" {
		t.Fatal("legacy key lost")
	}
	if err := s.SaveKey("new"); err != nil {
		t.Fatal(err)
	}
	if s.Key() != "new" {
		t.Fatal("new key not preferred")
	}
	if err := s.SaveKey("rotated"); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(filepath.Join(s.Dir, "credentials"))
	if goruntime.GOOS != "windows" && info.Mode().Perm() != 0600 { // Windows has no Unix modes
		t.Fatal(info.Mode())
	}
	t.Setenv("OPENROUTER_API_KEY", "env")
	if s.Key() != "env" {
		t.Fatal("env not preferred")
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := s.RemoveKey(); err != nil {
		t.Fatal(err)
	}
	if s.Key() != "" {
		t.Fatal("legacy key survives remove")
	}
	var old map[string]string
	_ = s.Read("config.json", &old)
	if old["other"] != "kept" {
		t.Fatal(old)
	}
	p, err := s.Preferences()
	if err != nil || p.BudgetUSD != 2 || p.Use != "agent" {
		t.Fatal(p, err)
	}
	_ = s.Write("prefs.json", map[string]any{"calibration_offer": "skipped"})
	p.Tool = "codex"
	p.Context = 65536
	if err = s.SavePreferences(p); err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	_ = s.Read("prefs.json", &raw)
	if string(raw["calibration_offer"]) != `"skipped"` {
		t.Fatal(raw)
	}
	got, err := s.Preferences()
	if err != nil || got.Tool != "codex" || got.Context != 65536 {
		t.Fatal(got, err)
	}
}
