package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/harness"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/store"
)

func TestMacReplyFiguresAndToolNames(t *testing.T) {
	m := fixture(t)
	m.width = 100
	m.detecting, m.offer = false, false
	m.cfg.Preferences.Tool = "claude-code"
	if b := strip(m.recipes()); !strings.Contains(b, "(~20K tokens)") {
		t.Fatal("stale tool prompt in header", b)
	}
	m.listing.Harness, _ = harness.Get("claude-code")
	m.listing.Scenario = m.listing.Harness.Scenario
	m.stack = []Screen{Home}
	m.models = m.models[:1]
	r := m.models[0].Row.Result
	r.Kind = "simulated"
	m.models[0].Row.Result = r
	m.models[0].Reply = metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false)
	m.models[0].Journal = nil
	reply := estimated(m.models[0].Reply, true)
	if b := strip(m.launcher()); !strings.Contains(b, reply+" per reply") || strings.Contains(b, "tok/s") {
		t.Fatal(b)
	}
	m.listing.Rows = []core.Row{m.models[0].Row}
	if b := strip(m.recipes()); !strings.Contains(b, "Recipes for Claude Code on this Mac · balanced") || !strings.Contains(b, "(~20K tokens)") || !strings.Contains(b, reply) || strings.Contains(b, "48k native") || strings.Contains(b, "claim splash") {
		t.Fatal(b)
	}
	for _, label := range []string{"FIRST REPLY", "LATER REPLIES", "WRITING"} {
		if !strings.Contains(strip(m.tableView()), label) {
			t.Fatal(m.tableView())
		}
	}
	r.Kind = "calibrated"
	m.models[0].Row.Result = r
	m.models[0].Reply = metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false)
	m.models[0].Journal = &setup.Journal{}
	m.listing.Rows[0] = m.models[0].Row
	exact := strings.TrimPrefix(m.models[0].Reply.String(), "~")
	if b := strip(m.launcher()); !strings.Contains(b, exact+" per reply") || strings.Contains(b, "~"+exact) {
		t.Fatal(b)
	}
	if b := strip(m.tableView()); !strings.Contains(b, exact) || strings.Contains(b, "~"+exact) {
		t.Fatal(b)
	}
	m.stack = []Screen{Launching}
	if b := m.body(); b != "Opening Claude Code on the local model…" {
		t.Fatal(b)
	}
	m.setup.plan.Harness = "claude-code"
	m.cfg.Preferences.Tool = "claude-code"
	m.setup.update = setup.Update{}
	m.busy = false
	m.stack = []Screen{Setup}
	m.setup.plan.Steps[0].Argv = []string{"/private/engine", "--internal"}
	b := strip(m.setupBody())
	for _, want := range []string{"Install llama.cpp b12182 (16.7 MB)", "Start the engine", "Connect Claude Code", "Check speed and tools", "your Claude Code settings,"} {
		if !strings.Contains(b, want) {
			t.Fatal(b)
		}
	}
	if strings.Contains(b, "/private/engine") || strings.Contains(b, "claude-code") {
		t.Fatal(b)
	}
	m.busy = true
	m.setup.update.Kind = setup.Check
	m.setup.logs = []string{"/private/verification.log: internal check"}
	if b := strip(m.setupBody()); strings.Contains(b, "/private/") {
		t.Fatal("compact progress exposed technical logs", b)
	}
	m.busy = false
	m.expanded = true
	if b := strip(m.setupBody()); !strings.Contains(b, "/private/engine") || !strings.Contains(b, "--internal") {
		t.Fatal(b)
	}
	m.listing.Scenario.BasePromptTokens = 44000
	if b := strip(m.recipes()); !strings.Contains(b, "(~44K tokens)") {
		t.Fatal(b)
	}
	m.detection.Target.OS = "linux"
	if b := strip(m.recipes()); !strings.Contains(b, "Simulated or calibrated") || strings.Contains(b, "on this Mac") {
		t.Fatal(b)
	}
	m.detection.Target.OS = "darwin"
	m.makeFields()
	m.stack = []Screen{Needs}
	if b := strip(m.body()); !strings.Contains(b, "Claude Code") || strings.Contains(b, "claude-code") {
		t.Fatal(b)
	}
}

func TestAllToolDisplayNames(t *testing.T) {
	m := fixture(t)
	m.width = 100
	for _, h := range harness.All() {
		m.cfg.Preferences.Tool = h.ID
		m.stack = []Screen{Launching}
		if b := m.body(); !strings.Contains(b, toolName(h.ID)) {
			t.Fatal(b)
		}
		m.listing.Scenario = h.Scenario
		m.listing.Harness = h
		if b := strip(m.recipes()); !strings.Contains(b, "Recipes for "+toolName(h.ID)+" on this Mac") {
			t.Fatal(b)
		}
	}
}

func TestMacToolCycleRefreshesReply(t *testing.T) {
	m := fixture(t)
	m.detecting, m.offer = false, false
	m.stack = []Screen{Home}
	m.cfg.Models = func(_ context.Context, _ core.Detection, p store.Preferences) ([]core.ModelChoice, error) {
		if p.Tool != "claude-code" {
			t.Fatalf("refresh used %q", p.Tool)
		}
		choices := append([]core.ModelChoice(nil), m.models...)
		choices[0].Reply = figure.Band(42, 60, "s", figure.Simulated, "selected:claude-code")
		return choices, nil
	}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if cmd == nil || m.cfg.Preferences.Tool != "claude-code" || !m.detecting {
		t.Fatal("tool cycle did not refresh")
	}
	msg := cmd().(modelsMsg)
	m.Update(modelsMsg{tool: "codex", rows: nil})
	if !m.detecting {
		t.Fatal("stale result completed refresh")
	}
	m.Update(msg)
	if m.detecting || m.models[0].Reply.V != 42 {
		t.Fatal(m.models)
	}
}
