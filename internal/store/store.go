// Package store owns triaLM's local state.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aeroknight786/trialm/internal/paths"
)

type Store struct{ Dir string }

func Default() Store { d, _ := paths.Home(); return Store{Dir: d} }
func (s Store) Write(name string, v any) error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, "."+name+"-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(b)
	}
	if e := f.Close(); err == nil {
		err = e
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.Dir, name))
}
func (s Store) Read(name string, v any) error {
	b, err := os.ReadFile(filepath.Join(s.Dir, name))
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}
func (s Store) Key() string {
	if k := os.Getenv("OPENROUTER_API_KEY"); k != "" {
		return k
	}
	var c struct {
		Key string `json:"openrouter_key"`
	}
	if s.Read("credentials", &c) == nil {
		return c.Key
	}
	// Keep pre-v0.3 keys working until the user signs in again.
	_ = s.Read("config.json", &c)
	return c.Key
}
func (s Store) SaveKey(key string) error {
	if strings.TrimSpace(key) == "" {
		return errors.New("empty OpenRouter key")
	}
	return s.Write("credentials", map[string]string{"openrouter_key": strings.TrimSpace(key)})
}
func (s Store) RemoveKey() error {
	err := os.Remove(filepath.Join(s.Dir, "credentials"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var old map[string]json.RawMessage
	if s.Read("config.json", &old) == nil {
		delete(old, "openrouter_key")
		return s.Write("config.json", old)
	}
	return nil
}

type Preferences struct {
	Tool          string  `json:"tool"`
	Use           string  `json:"use"`
	Model         string  `json:"model"`
	Priority      string  `json:"priority"`
	Context       int     `json:"context"`
	ContextVia    string  `json:"context_via"`
	QualityFloor  string  `json:"quality_floor"`
	MaxDownloadGB float64 `json:"max_download_gb"`
	Engines       string  `json:"engines"`
	Boosts        string  `json:"boosts"`
	Privacy       string  `json:"privacy"`
	BudgetUSD     float64 `json:"budget_usd"`
	Hardware      string  `json:"hardware"`
}

func (s Store) Preferences() (Preferences, error) {
	p := Preferences{Use: "agent", Model: "any", Priority: "balanced", ContextVia: "native", Engines: "stable+new", Boosts: "auto", Privacy: "repo", BudgetUSD: 2, MaxDownloadGB: 100}
	err := s.Read("prefs.json", &p)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return p, err
}

// SavePreferences preserves independently owned fields such as calibration_offer.
func (s Store) SavePreferences(p Preferences) error {
	m := map[string]json.RawMessage{}
	_ = s.Read("prefs.json", &m)
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(b, &fields)
	for k, v := range fields {
		m[k] = v
	}
	return s.Write("prefs.json", m)
}
