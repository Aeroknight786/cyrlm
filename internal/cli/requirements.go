package cli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/store"
)

type savedRecipe = store.Recipe

func saveListing(rows []core.Row, h string) error {
	list := []savedRecipe{}
	for _, r := range rows {
		list = append(list, savedRecipe{Model: r.Result.Model.OllamaTag, Quant: r.Result.Quant.Name, Runtime: r.Result.Runtime.ID, Harness: h})
	}
	return store.Default().Write("last.json", list)
}
func resolveRecipe(pos []string, c *common) (string, error) {
	if len(pos) > 1 {
		return "", &exitError{2, "supply one model or recipe number"}
	}
	name := "1"
	if len(pos) == 1 {
		name = pos[0]
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(name, "#")); err == nil {
		var rows []savedRecipe
		if err = store.Default().Read("last.json", &rows); err != nil || n < 1 || n > len(rows) {
			return "", needs("trialm find", "recipe number is not in the last listing")
		}
		r := rows[n-1]
		if c.runtime == "" || c.runtime == "all" {
			c.runtime = r.Runtime
			c.recipeRef = "#" + strconv.Itoa(n)
		}
		if c.quant == "" {
			c.quant = r.Quant
		}
		if (c.harness == "" || c.harnessAuto) && r.Harness != "" {
			c.harness, c.harnessAuto = r.Harness, false
		}
		return r.Model, nil
	}
	if i := strings.Index(name, "/"); i >= 0 && !strings.Contains(name, "://") && !strings.HasSuffix(name, ".gguf") {
		// A recipe slug has an explicit @engine suffix. HF model ids remain ids.
		if at := strings.LastIndex(name, "@"); at > i {
			if c.quant == "" {
				c.quant = name[i+1 : at]
			}
			if c.runtime == "" || c.runtime == "all" {
				c.runtime = name[at+1:]
			}
			name = name[:i]
		}
	}
	return name, nil
}
func (c *common) validateRequirements() error {
	if c.prefsErr != nil {
		return fmt.Errorf("read prefs.json: %w", c.prefsErr)
	}
	for _, v := range []struct {
		name, value string
		allowed     []string
	}{
		{"--use", c.prefs.Use, []string{"agent", "chat"}},
		{"--want", c.prefs.Priority, []string{"balanced", "speed", "quality", "long"}},
		{"--engines", c.prefs.Engines, []string{"stable+new", "installed", "stable"}},
		{"--boosts", c.prefs.Boosts, []string{"off", "auto", "everything"}},
		{"--privacy", c.prefs.Privacy, []string{"repo", "playground", "predict-only"}},
	} {
		ok := false
		for _, a := range v.allowed {
			ok = ok || a == v.value
		}
		if !ok {
			return &exitError{2, fmt.Sprintf("%s must be %s", v.name, strings.Join(v.allowed, "|"))}
		}
	}
	if c.prefs.ContextVia != "native" {
		return &exitError{4, "opt-in YaRN arrives in v0.6; use --context-via native"}
	}
	if c.prefs.QualityFloor != "" && c.prefs.QualityFloor != "3bit-ok" && c.prefs.QualityFloor != "4bit+" {
		return &exitError{2, "--quality-floor must be 3bit-ok|4bit+"}
	}
	return nil
}

func privacyNotice() error {
	s := store.Default()
	var state struct {
		Shown bool `json:"shown"`
	}
	if s.Read("trial-privacy.json", &state) == nil && state.Shown {
		return nil
	}
	fmt.Fprintln(out(), "Trials send this repo to providers that do not store it. Use --playground for a scratch task or --zdr for zero data retention.")
	return s.Write("trial-privacy.json", map[string]bool{"shown": true})
}
