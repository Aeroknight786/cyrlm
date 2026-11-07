package core

import (
	"context"
	"strings"
	"testing"

	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/harness"
)

func TestSessionSeamAndRequirements(t *testing.T) {
	var events []Event
	s := Session{Sink: func(e Event) { events = append(events, e) }}
	o := Options{Device: "RTX 4090", RAM: 64, Runtime: "all", Tuned: true}
	d, err := s.Detect(context.Background(), o)
	if err != nil || !d.Target.Synthetic || len(events) != 1 || events[0].Type != "detect" {
		t.Fatal(d, events, err)
	}
	h, _ := harness.Get("codex")
	listing, err := s.Find(context.Background(), o, d.Target, h.Scenario, h, "qwen3.6:35b-a3b", 5)
	if err != nil || len(listing.Rows) != 1 {
		t.Fatal(listing, err)
	}
	r := listing.Rows[0].Result
	if r.Model.OllamaTag != "qwen3.6:35b-a3b" || events[len(events)-1].Type != "recipes" {
		t.Fatal(r.Model, events)
	}
	o.Runtime = r.Runtime.ID
	o.Quant = r.Quant.Name
	prepared, err := s.Try(context.Background(), o, r.Model, d.Target, h.Scenario, h, false)
	if err != nil || prepared.Result.DecodeTPSDeep != r.DecodeTPSDeep {
		t.Fatal(prepared, err)
	}
	o.Runtime = "all"
	o.Quant = ""
	o.Engines = "installed"
	listing, err = s.Find(context.Background(), o, d.Target, h.Scenario, h, "", 5)
	if err != nil || len(listing.Rows) != 0 {
		t.Fatal("what-if must have no installed runtime", listing, err)
	}
	o.Engines = ""
	o.MaxDownloadGB = .001
	listing, err = s.Find(context.Background(), o, d.Target, h.Scenario, h, "", 5)
	if err != nil || len(listing.Rows) != 0 {
		t.Fatal("download cap ignored", listing, err)
	}
	o.MaxDownloadGB = 100
	o.Priority = "speed"
	listing, err = s.Find(context.Background(), o, d.Target, h.Scenario, h, "", 5)
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < len(listing.Rows); i++ {
		if listing.Rows[i-1].Result.Scenario.Session > listing.Rows[i].Result.Scenario.Session {
			t.Fatal("speed priority ignored")
		}
	}
	o.QualityFloor = "4bit+"
	o.Boosts = "off"
	listing, err = s.Find(context.Background(), o, d.Target, h.Scenario, h, "qwen", 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range listing.Rows {
		if quantBits(row.Result.Quant.Name) < 4 || row.Result.Settings.Tuned {
			t.Fatal("quality floor or boosts ignored", row)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.Find(ctx, o, d.Target, h.Scenario, h, "", 5); err != context.Canceled {
		t.Fatal(err)
	}
}
func TestTryFitAndQueryAliases(t *testing.T) {
	m, _ := catalog.Lookup("qwen3.6:35b-a3b")
	if !queryMatches(m, m.ID) || !queryMatches(m, "qwen3.6") {
		t.Fatal(m.ID)
	}
	d, err := (Session{}).Detect(context.Background(), Options{Device: "cpu", RAM: 1})
	if err != nil {
		t.Fatal(err)
	}
	h, _ := harness.Get("codex")
	o := Options{Runtime: "llamacpp", Quant: "Q4_K_M", Tuned: true, Context: 131072}
	_, err = (Session{}).Try(context.Background(), o, m, d.Target, h.Scenario, h, false)
	if err == nil || !strings.Contains(err.Error(), "does not fit") {
		t.Fatal(err)
	}
	if _, err = (Session{}).Try(context.Background(), o, m, d.Target, h.Scenario, h, true); err != nil {
		t.Fatal(err)
	}
}

func TestEightBitRequirement(t *testing.T) {
	s := Session{}
	o := Options{Device: "RTX 4090", RAM: 64, Runtime: "all", Tuned: true, QualityFloor: "8bit"}
	d, e := s.Detect(context.Background(), o)
	if e != nil {
		t.Fatal(e)
	}
	h, _ := harness.Get("codex")
	l, e := s.Find(context.Background(), o, d.Target, h.Scenario, h, "", 5)
	if e != nil || len(l.Rows) == 0 {
		t.Fatal(l, e)
	}
	for _, r := range l.Rows {
		if quantBits(r.Result.Quant.Name) < 8 {
			t.Fatal(r.Result.Quant)
		}
	}
}
