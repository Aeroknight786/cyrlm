package tui

import (
	"context"
	"fmt"
	"math"
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/aeroknight786/trialm/internal/catalog"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/engine"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/harness"
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
	plan       setup.Plan
	journal    setup.Journal
	report     *verify.Report
	update     setup.Update
	states     map[int]string
	logs       []string
	events     chan setupEvent
	cancel     context.CancelFunc
	calibrated bool
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
	m.push(Setup)
	m.busy = true
	m.setup = setupView{states: map[int]string{}}
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
		if v.err != nil {
			m.message = v.err.Error()
		} else if v.journal.State == "ready" {
			m.replace(Ready)
			if m.setup.calibrated {
				return m.find()
			}
		}
		return nil
	}
	if v.err != nil {
		m.busy = false
		m.message = v.err.Error()
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
		if u.Index > 0 {
			m.setup.update = u
			m.setup.states[u.Index] = u.State
		}
		m.setup.logs = append(m.setup.logs, u.Lines...)
		if len(m.setup.logs) > 5 {
			m.setup.logs = m.setup.logs[len(m.setup.logs)-5:]
		}
	case "setup_ready":
		m.setup.report, _ = v.event.Data.(*verify.Report)
	case "setup_calibrated":
		m.setup.calibrated = true
	}
	if m.setup.events != nil {
		return m.nextSetup()
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
func (m *Root) setupBody() string {
	p := m.setup.plan
	if p.ID == "" {
		return "Planning local setup…"
	}
	b := fmt.Sprintf("Set up %s · %s · %s\n%d approval · %d OS password prompts · %s\n", p.ModelName, p.Quant, p.RuntimeName, p.Prompts.Approvals, p.Prompts.Passwords, p.DownloadSummary())
	if p.DownloadUnknown {
		b += "Plus installer downloads of unknown size\n"
	}
	if p.Prompts.AfterRestart != "" {
		b += p.Prompts.AfterRestart + "\n"
	}
	for i, s := range p.Steps {
		mark := " "
		if m.setup.states[i+1] == "done" {
			mark = "✓"
		}
		b += fmt.Sprintf("%s %-8s %s", mark, s.Kind, s.Title)
		bytes := s.DownloadBytes
		if s.Request != nil {
			bytes += s.Request.Size
		}
		if bytes > 0 {
			b += " · " + figure.Bytes(bytes, "setup:resolved-files").String() + " · sha256 pinned"
		}
		b += "\n"
	}
	b += "Not touched · " + strings.Join(p.Untouched, ", ") + "\n"
	for _, warning := range p.Warnings {
		b += warning + "\n"
	}
	if len(m.setup.journal.Steps) > 0 {
		b += m.setup.journal.Summary() + "\n"
	}
	if u := m.setup.update; u.Progress != nil && u.Progress.Total > 0 {
		progress := u.Progress
		b += "Downloading " + m.progressBar(float64(progress.Done)/float64(progress.Total)) + "\n"
		b += fmt.Sprintf("%d%% · %s / %s · %s · ETA %s\n", progress.Done*100/progress.Total, figure.Bytes(progress.Done, "download").String(), figure.Bytes(progress.Total, "download").String(), figure.Exact(progress.BytesPerSec/1e6, "MB/s", figure.Measured, "download").String(), figure.Exact(math.Round(progress.ETA.Seconds()), "s", figure.Measured, "download").String())
	}
	if len(m.setup.logs) > 0 {
		log := viewport.New(viewport.WithWidth(max(20, m.width-2)), viewport.WithHeight(len(m.setup.logs)))
		log.SetContent(strings.Join(m.setup.logs, "\n"))
		b += log.View() + "\n"
	}
	if m.busy {
		return strings.TrimSuffix(b, "\n") // the footer says ctrl-c pauses
	}
	return b + "Go? [Y/n]"
}
func (m *Root) readyBody() string {
	p, r := m.setup.plan, m.setup.report
	b := "✓ Ready · " + m.detection.Target.Name + "\n                         predicted    measured    miss\n"
	if r == nil {
		return b
	}
	row := func(label string, predicted, actual float64, band int) {
		prediction := figure.Band(predicted, band, "t/s", figure.Simulated, "setup:prediction").String()
		measured := figure.Band(actual, 20, "t/s", figure.Measured, "verify:ready").String()
		miss := "—"
		if predicted > 0 {
			miss = fmt.Sprintf("%+d%%", int(math.Round(100*(actual/predicted-1))))
		}
		b += fmt.Sprintf("%-24s %12s %12s %9s\n", label, prediction, measured, miss)
	}
	row("writing @ start", p.Predicted.DecodeTPS0, r.DecodeTPS[0], p.DecodeErrorPct)
	row(fmt.Sprintf("writing @ %dK", p.ContextTokens/1024), p.Predicted.DecodeTPSTarget, r.DecodeTPS[p.ContextTokens], p.DecodeErrorPct)
	row("reading", p.Predicted.PrefillTPS, r.PrefillTPS, p.PrefillErrorPct)
	for _, c := range r.Checks {
		mark := "✗"
		if c.OK {
			mark = "✓"
		}
		b += strings.TrimSuffix(mark+" "+c.Name+" · "+c.Detail, " · ") + "\n"
	}
	if m.setup.calibrated {
		machine := "this machine"
		if m.detection.Target.OS == "darwin" {
			machine = "this Mac"
		}
		b += "calibrated " + machine + " · re-ranked\n"
	}
	return b + "enter open " + harness.DisplayName(m.cfg.Preferences.Tool) + " now  later: trialm launch"
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
	cfg.Balance = nil
	return Run(cfg)
}
