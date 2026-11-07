package cli

import (
	"testing"

	"github.com/aeroknight786/trialm/internal/store"
)

// A recipe number from the last listing supplies its harness, runtime and
// quant unless the user gave them; an auto-detected harness does not count.
func TestResolveRecipeUsesListing(t *testing.T) {
	t.Setenv("TRIALM_HOME", t.TempDir())
	if err := store.Default().Write("last.json", []savedRecipe{{Model: "qwen3.6:35b-a3b", Quant: "IQ3_XXS", Runtime: "llamacpp", Harness: "codex"}}); err != nil {
		t.Fatal(err)
	}
	c := common{harness: "claude-code", harnessAuto: true}
	m, err := resolveRecipe([]string{"#1"}, &c)
	if err != nil {
		t.Fatal(err)
	}
	if m != "qwen3.6:35b-a3b" || c.harness != "codex" || c.runtime != "llamacpp" || c.quant != "IQ3_XXS" || c.recipeRef != "#1" {
		t.Fatalf("got model %q, %+v", m, c)
	}
	c = common{harness: "opencode"}
	if _, err := resolveRecipe([]string{"1"}, &c); err != nil || c.harness != "opencode" {
		t.Fatalf("an explicit harness must win: %q %v", c.harness, err)
	}
}
