package main

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/trial"
)

func TestHomeReturnsTrialCard(t *testing.T) {
	home := t.TempDir()
	s := store.Store{Dir: filepath.Join(home, ".trialm")}
	if e := s.SaveTrial(store.TrialEntry{ID: "window-test", At: time.Now(), Model: "qwen", Rating: 2, Band: 60, Report: trial.Report{Requests: 2, SpendKnown: true, SpendUSD: .01}}); e != nil {
		t.Fatal(e)
	}
	out, stderr, code := runCLI(t, home, "--json")
	if code != 0 {
		t.Fatal(code, string(stderr))
	}
	events := decodeEvents(t, out)
	data := events[len(events)-1]["data"].(map[string]any)
	cards := data["trials"].([]any)
	if len(cards) != 1 {
		t.Fatal(data)
	}
	card := cards[0].(map[string]any)
	if card["id"] != "window-test" || card["rating"].(map[string]any)["v"] != float64(2) {
		t.Fatal(card)
	}
	validateNumbers(t, data)
}
