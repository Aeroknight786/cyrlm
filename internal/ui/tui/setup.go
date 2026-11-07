package tui

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/setup"
	"github.com/aeroknight786/trialm/internal/store"
	"github.com/aeroknight786/trialm/internal/verify"
)

type adminRequest struct {
	ctx  context.Context
	argv []string
	done chan error
}

type setupEvent struct {
	event   core.Event
	journal setup.Journal
	err     error
	done    bool
}
type setupView struct {
	plan    setup.Plan
	journal setup.Journal
	report  *verify.Report
	update  setup.Update
	states  map[int]string
	logs    []string
	events  chan setupEvent
	cancel  context.CancelFunc
	onDisk  bool
	failed  bool // a finished run that did not reach Ready
}

func (m *Root) planSetup() tea.Cmd {
	row, ok := m.row()
	if m.screen() == Summary {
		if model, found := catalog.Lookup(m.last.Model); found {
			row.Result.Model = model
			row.Result.Quant.Name = m.last.Quant
			row.Result.Runtime, _ = engine.Get(m.last.Runtime)
		}
	}
	if !ok || m.cfg.Plan == nil {
		return nil
	}
	m.autoLaunch = m.screen() == Home
	m.push(Setup)
	m.busy = true
	m.setup = setupView{states: map[int]string{}, onDisk: row.LocalPath != ""}
	return func() tea.Msg {
		p, e := m.cfg.Plan(m.cfg.Context, row, m.cfg.Preferences)
		executor, _ := (core.Session{}).SetupExecutor()
		var j setup.Journal
		if executor != nil {
			j, _ = executor.Status(p.Slug)
		}
		return setupEvent{journal: j, event: core.Event{Type: "setup_plan", Data: map[string]any{"plan": p}}, err: e}
	}
}
func (m *Root) applySetup() tea.Cmd {
	ctx, cancel := context.WithCancel(m.cfg.Context)
	m.setup.cancel = cancel
	m.setup.events = make(chan setupEvent, 32)
	events := m.setup.events
	plan := m.setup.plan
	m.busy = true
	go func() {
		j, e := m.cfg.Apply(ctx, plan, func(ev core.Event) { events <- setupEvent{event: ev} })
		events <- setupEvent{journal: j, err: e, done: true}
		close(events)
		cancel()
	}()
	return m.nextSetup()
}
func (m *Root) nextSetup() tea.Cmd {
	ch := m.setup.events
	return func() tea.Msg { v := <-ch; return v }
}
func (m *Root) setupEvent(v setupEvent) tea.Cmd {
	if v.done {
		m.busy = false
		m.setup.cancel = nil
		m.setup.journal = v.journal
		if v.journal.Report != nil {
			m.setup.report = v.journal.Report
		}
		m.setup.failed = v.err != nil
		if v.err != nil {
			m.fail("Setup needs another try. Press enter to resume or d for details.", v.err)
		} else if v.journal.State == "ready" {
			m.replace(Ready)
			m.sparkle = 0
			return tea.Tick(150*time.Millisecond, func(time.Time) tea.Msg { return sparkleMsg(1) })
		}
		return nil
	}
	if v.err != nil {
		m.busy = false
		m.fail("Setup needs another try. Press enter to resume or d for details.", v.err)
		return nil
	}
	switch v.event.Type {
	case "setup_admin":
		request := v.event.Data.(adminRequest)
		cmd := exec.CommandContext(request.ctx, request.argv[0], request.argv[1:]...)
		return tea.ExecProcess(cmd, func(err error) tea.Msg { request.done <- err; return setupEvent{} })
	case "setup_plan":
		data, _ := v.event.Data.(map[string]any)
		m.setup.plan, _ = data["plan"].(setup.Plan)
		m.setup.journal = v.journal
		m.busy = false
	case "setup_status", "setup_paused":
		m.setup.journal, _ = v.event.Data.(setup.Journal)
	case "setup_step", "setup_progress", "setup_log":
		u, _ := v.event.Data.(setup.Update)
		if m.setup.states == nil {
			m.setup.states = map[int]string{}
		}
		if u.Index > 0 && v.event.Type != "setup_log" {
			m.setup.update = u
			m.setup.states[u.Index] = u.State
		}
		m.setup.logs = append(m.setup.logs, u.Lines...)
		if len(m.setup.logs) > 5 {
			m.setup.logs = m.setup.logs[len(m.setup.logs)-5:]
		}
	case "setup_ready":
		m.setup.report, _ = v.event.Data.(*verify.Report)
	}
	if m.setup.events != nil {
		return tea.Batch(m.nextSetup(), m.animateProgress())
	}
	return nil
}
func (m *Root) launchSetup(j setup.Journal) tea.Cmd {
	m.push(Launching)
	argv := []string{"launch", m.cfg.Preferences.Tool, "--setup", j.Plan.Slug, "--here", "--plain"}
	cmd := m.child(argv)
	m.busy = true
	return tea.ExecProcess(cmd, func(e error) tea.Msg { return childMsg{kind: Launching, err: e} })
}
func (m *Root) animateProgress() tea.Cmd {
	if m.theme.plain || m.theme.ascii {
		return nil
	}
	if u := m.setup.update; u.Progress != nil && u.Progress.Total > 0 {
		return m.bar.SetPercent(float64(u.Progress.Done) / float64(u.Progress.Total))
	}
	return nil
}
func (m *Root) setupBody() string {
	p := m.setup.plan
	if p.ID == "" {
		return m.card(m.waiting() + " Planning your setup")
	}
	summary := p.DownloadSummary()
	if m.setup.onDisk {
		summary = "already on disk · " + summary
	}
	password := "no password"
	if p.Prompts.Passwords > 0 {
		password = fmt.Sprintf("%d OS password prompt(s)", p.Prompts.Passwords)
	}
	b := m.theme.selected.Bold(true).Render("Set up "+p.ModelName+" for "+toolName(m.cfg.Preferences.Tool)) + "\n" + summary + fmt.Sprintf(" · %d approval · ", p.Prompts.Approvals) + password + "\n"
	b += "Not touched · " + strings.Join(p.Untouched, ", ") + "\n"
	if p.Prompts.AfterRestart != "" {
		b += m.theme.caveat.Render(p.Prompts.AfterRestart) + "\n"
	}
	for i, step := range p.Steps {
		state := m.setup.states[i+1]
		if !m.expanded && !m.busy {
			continue
		}
		if !m.expanded && state == "" {
			continue
		}
		mark := "·"
		if state == "done" {
			mark = m.theme.good.Render("✓")
		} else if state != "" {
			mark = m.waiting()
		}
		b += "\n" + mark + " " + step.Title
		if m.expanded && len(step.Argv) > 0 {
			b += "\n  " + fmt.Sprintf("%q", step.Argv)
		}
	}
	if u := m.setup.update; u.Progress != nil && u.Progress.Total > 0 {
		p := u.Progress
		b += "\n\n" + m.progressBar(float64(p.Done)/float64(p.Total)) + fmt.Sprintf(" %d%%", p.Done*100/p.Total)
		b += "\n" + figure.Exact(p.BytesPerSec/1e6, "MB/s", figure.Measured, "download").String() + " · ETA " + figure.Exact(math.Round(p.ETA.Seconds()), "s", figure.Measured, "download").String()
	}
	if m.setup.journal.State == "paused" {
		b += "\n" + m.setup.journal.Summary()
	}
	if m.expanded {
		b += "\n" + strings.Join(m.setup.logs, "\n")
	} else if m.busy && m.setup.update.Kind == setup.Check {
		for _, line := range m.setup.logs {
			name, _, _ := strings.Cut(line, ":")
			b += "\n" + clip(name, min(m.width, 80)-6)
		}
	}
	if m.expanded {
		b += "\n" + strings.Join(p.Warnings, "\n")
	}
	return m.card(strings.TrimSpace(b))
}
func (m *Root) readyBody() string {
	p, r := m.setup.plan, m.setup.report
	sparkle := []string{"✦ Ready", "✧ ✦ Ready ✧", "✦ Ready  ✧", "✦ Ready"}[min(3, m.sparkle)]
	b := m.theme.good.Bold(true).Render(sparkle) + " · " + p.ModelName + "\n\n                         measured       predicted\n"
	if r == nil {
		return m.card(b)
	}
	row := func(label string, actual, predicted float64, band int) {
		measured := "—"
		if actual > 0 {
			measured = figure.Band(actual, 20, "tok/s", figure.Measured, "verify:ready").String()
		}
		pred := "~" + strings.TrimPrefix(figure.Band(predicted, band, "tok/s", figure.Simulated, "setup:prediction").String(), "~")
		b += fmt.Sprintf("%-23s %12s %15s\n", label, measured, pred)
	}
	row("Writing at start", r.DecodeTPS[0], p.Predicted.DecodeTPS0, p.DecodeErrorPct)
	row(fmt.Sprintf("Writing at %dK", p.ContextTokens/1024), r.DecodeTPS[p.ContextTokens], p.Predicted.DecodeTPSTarget, p.DecodeErrorPct)
	row("Reading", r.PrefillTPS, p.Predicted.PrefillTPS, p.PrefillErrorPct)
	for _, c := range r.Checks {
		if c.OK {
			b += m.theme.good.Render("✓ "+c.Name) + "\n"
		}
	}
	if len(r.Misses) > 0 {
		b += "\n" + m.theme.dim.Render(clip(strings.Join(r.Misses, " · "), min(m.width, 80)-6)) + "\n"
	}
	if m.expanded {
		b += "\n" + strings.Join(m.setup.logs, "\n")
	}
	return m.card(strings.TrimSpace(b))
}

func RunSetup(ctx context.Context, p setup.Plan) error {
	s := store.Default()
	prefs, _ := s.Preferences()
	prefs.Context = p.ContextTokens
	if p.Harness != "" {
		prefs.Tool = p.Harness
	}
	cfg := SessionConfig(ctx, s, prefs, core.Options{}, 5, "")
	cfg.Start = Setup
	cfg.LocalPlan = &p
	cfg.UpdateNotice = nil
	return Run(cfg)
}
