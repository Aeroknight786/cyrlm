package tui

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/setup/download"
	"github.com/aeroknight786/trialm/internal/verify"
)

func setupFrame(m *Root) {
	p := setup.Plan{ID: "fixture-plan", Slug: "qwen-local", ModelName: "Qwen3.6 35B-A3B", Quant: "IQ3_XXS", RuntimeName: "llama.cpp", Harness: "codex", ContextTokens: 40960, DownloadBytes: 13200000000, Prompts: setup.Prompts{Approvals: 1}, Untouched: []string{"Codex config", "shell profile", "other models"}, Predicted: verify.Predicted{DecodeTPS0: 44, DecodeTPSTarget: 36, PrefillTPS: 800}, DecodeErrorPct: 60, PrefillErrorPct: 70,
		Steps: []setup.Step{{Kind: setup.Install, Title: "llama.cpp b12182", DownloadBytes: 31000000}, {Kind: setup.Fetch, Title: "Qwen3.6-35B-A3B.gguf"}, {Kind: setup.Write, Title: "Save owned launcher"}, {Kind: setup.Start, Title: "llama-server :8080 · in-process bridge"}, {Kind: setup.Check, Title: "speed · greeting · tool call · follow-up"}}}
	report := &verify.Report{Ready: true, DecodeTPS: map[int]float64{0: 39.8, 40960: 31.2}, PrefillTPS: 655, Checks: []verify.Check{{Name: "Codex greeting", OK: true}, {Name: "tool call", OK: true}, {Name: "follow-up", OK: true}}}
	progress := download.Progress{Done: 8100000000, Total: 13200000000, BytesPerSec: 47000000, ETA: 109 * time.Second}
	j := setup.Journal{State: "ready", Plan: p, Report: report, Steps: []setup.Record{{State: "done"}, {State: "done"}, {Progress: progress}, {}, {}}}
	m.setup = setupView{plan: p, journal: j, report: report, states: map[int]string{1: "done", 2: "done"}, update: setup.Update{Index: 3, Total: 5, Progress: &progress}, calibrated: true, logs: []string{"sha256 checked", "archive extracted", "download resumed", "range accepted", "8.1 GB received"}}
	m.ready = []setup.Journal{j}
}
func TestSetupEventsAndPause(t *testing.T) {
	m := fixture(t)
	m.stack = []Screen{Setup}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.cfg.Apply = func(ctx context.Context, p setup.Plan, sink core.Sink) (setup.Journal, error) {
		sink(core.Event{Type: "setup_progress", Data: setup.Update{Index: 3, Total: 5, Progress: &download.Progress{Done: 61, Total: 100}}})
		<-ctx.Done()
		j := setup.Journal{State: "paused", Plan: p, Steps: []setup.Record{{State: "done"}, {State: "done"}, {Progress: download.Progress{Done: 61, Total: 100}}, {}, {}}}
		sink(core.Event{Type: "setup_paused", Data: j})
		return j, ctx.Err()
	}
	m.cfg.Context = ctx
	next := m.applySetup()
	v := next().(setupEvent)
	next = m.setupEvent(v)
	if !strings.Contains(m.View().Content, "61%") || !strings.Contains(m.footer(), "pauses") {
		t.Fatal(m.View().Content)
	}
	m.Update(key("ctrl+c"))
	for next != nil {
		v = next().(setupEvent)
		next = m.setupEvent(v)
	}
	if m.setup.journal.State != "paused" || m.busy {
		t.Fatal(m.setup.journal.State)
	}
	if !strings.Contains(m.View().Content, "2 of 5 done, download 61%") {
		t.Fatal(m.View().Content)
	}
}
