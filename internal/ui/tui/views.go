package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/charmbracelet/x/ansi"
)

func strip(s string) string       { return ansi.Strip(s) }
func clip(s string, w int) string { return ansi.Truncate(s, max(1, w), "") }
func metric(r predict.Result, v float64, unit string, reading bool) figure.Figure {
	band := r.Accuracy.DecodePct
	if reading {
		band = r.Accuracy.PrefillPct
	}
	return figure.Band(v, band, unit, figure.Kind(r.Kind), "prediction:"+r.Model.ID+"/"+r.Runtime.ID)
}
func (m *Root) body() string {
	title := m.theme.wordmark()
	switch m.screen() {
	case First:
		machine := "Detecting this machine…"
		if m.detection.Target.Name != "" {
			machine = m.detection.Target.Name
		}
		engine := "none yet (setup arrives in v0.5)"
		if len(m.detection.Runtimes) > 0 {
			engine = fmt.Sprintf("%d installed", len(m.detection.Runtimes))
		}
		return title + "  feel a local model before you download it\n✓ " + machine + "\n✓ Coding tool   " + m.cfg.Preferences.Tool + "\n· Engines       " + engine + "\n\nConnect OpenRouter: real cloud answers, paced to this machine.\nTrials use providers that do not store your repo. Cap $2."
	case Paste:
		if m.password == nil {
			return "OpenRouter key"
		}
		return title + "\n" + m.password.View()
	case Calibration:
		return title + "\n\nCalibrate this machine · recommended\n≈3–7 min · no model downloads\nSynthetic files need about 6 GB of temporary disk.\nUses an installed llama-bench. Skip and browse if unavailable."
	case Calibrating:
		elapsed := time.Since(m.started).Seconds()
		fraction := 0.0
		eta := "estimating"
		if m.calibration != nil {
			fraction = m.calibration.fraction
			eta = m.calibration.eta
		}
		return "Calibrating this machine\n" + m.progressBar(fraction) + "\nElapsed " + figure.Exact(elapsed, "s", figure.Measured, "clock").String() + " · ETA " + eta + "\nFinished measurements are kept if stopped."
	case Needs:
		lines := []string{"What do you need? · Enter accepts every default"}
		for i, f := range m.fields {
			mark := "  "
			if i == m.field {
				mark = m.theme.selected.Render("❯ ")
			}
			lines = append(lines, mark+strings.ReplaceAll(f.View(), "\n", " "))
		}
		lines = append(lines, "Native context only · YaRN choices arrive in v0.6", "Trial: no-logging providers · cap "+figure.Exact(m.cfg.Preferences.BudgetUSD, "USD", figure.Measured, "prefs:budget").String())
		return strings.Join(lines, "\n")
	case Recipes:
		return m.recipes()
	case Details:
		return m.details()
	case Summary:
		if m.last.ID == "" {
			return "No trials yet."
		}
		r := m.last.Report
		split := m.split(r.ReadingSeconds, r.WritingSeconds, r.LoadSeconds+r.BetweenTurnsSeconds)
		return "Trial over · " + m.last.Model + " · " + m.last.Quant + " · " + m.last.Runtime + "\n" + r.Plain(m.last.Band) + split + "\nHow did it feel?"
	case Home:
		b := title + "  " + m.detection.Target.Name + "\n"
		// P2 owns verified ready setups; a trial is never labelled ready.
		b += "Ready setups: none yet · setup arrives in v0.5\n❯ Find or try another\n\nTrials history\n"
		for i := len(m.history) - 1; i >= max(0, len(m.history)-5); i-- {
			r := m.history[i]
			b += fmt.Sprintf("  %s · %s · rating %d/3\n", r.At.Format("Jan 2"), r.Model, r.Rating)
		}
		b += "Popular configs: c claims gallery\n"
		if m.notice != "" {
			b += m.theme.dim.Render(m.notice)
		}
		return b
	case Gallery:
		b := "Published claims · unverified by triaLM · never ranked\n"
		for i, c := range Claims() {
			mark := "  "
			if i == m.selected {
				mark = "❯ "
			}
			b += mark + c.ID + " [" + c.Grade + "]\n  " + c.Hardware + "\n  " + c.Figures[0].String() + "\n"
		}
		return b
	case ClaimCard:
		if m.message == "claim parsing arrives in v0.6" {
			return "Claim\nclaim parsing arrives in v0.6"
		}
		cs := Claims()
		c := cs[min(max(0, m.selected), len(cs)-1)]
		b := "Claim · " + c.ID + " [" + c.Grade + "]\n" + c.Hardware + "\n" + c.Recipe + "\n"
		for _, f := range c.Figures {
			b += f.String() + "\n"
		}
		return b + "Source: " + c.SourceName + "\n" + c.SourceURL + "\nRecorded " + c.Date + "\nNot simulated yet. Adapter arrives in a later release."
	case Search:
		return m.search.View()
	case Why:
		if row, ok := m.row(); ok {
			r := row.Result
			f := metric(r, r.DecodeTPSDeep, "tok/s", false)
			return fmt.Sprintf("Why these numbers?\n%s · %s\n%s\nRange %s to %s\nSource: %s\nWriting ±%d%% · reading ±%d%%\n%s", r.Model.Name, r.Runtime.Name, f.String(), figure.BandNumber(f.Lo, 26), figure.BandNumber(f.Hi, 26), f.Src, r.Accuracy.DecodePct, r.Accuracy.PrefillPct, strings.Join(r.Warnings, "\n"))
		}
		return "Claims are source-reported figures, not triaLM measurements.\n" + Claims()[0].SourceURL
	case Setup:
		return "Set up locally\nSetup execution arrives in v0.5.\nReview the plan: trialm setup --dry-run\nNo engine or model has been installed."
	case Trial:
		return "Trial in another terminal\nRequest · waiting for harness\nCache · pending\nWait · local pacing\nSpend · tracked in the trial card"
	case Chat:
		return m.chat.view()
	}
	return title
}
func (m *Root) recipes() string {
	b := fmt.Sprintf("Recipes for %s · %s · %dk native\nSimulated or calibrated · uncertainty shown on ?\n", m.cfg.Preferences.Tool, m.cfg.Preferences.Priority, m.cfg.Preferences.Context/1024)
	if len(m.listing.Rows) == 0 {
		return b + "No working recipe for these needs. Press r to change them."
	}
	if m.width >= 90 {
		b += "    MODEL                         FILE · ENGINE                  WRITES       TURN       FIRST\n"
	} else {
		b += "    MODEL                         FILE · ENGINE                  TURN\n"
	}
	for i, row := range m.listing.Rows {
		r := row.Result
		mark := "  "
		if i == m.selected {
			mark = "❯ "
		}
		model := fmt.Sprintf("%s%d %s", mark, i+1, r.Model.Name)
		file := r.Quant.Name + " · " + r.Runtime.Name
		turn := metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false).String()
		if m.width < 60 {
			b += model + "\n  " + file + "\n  turn " + turn + "\n"
		} else if m.width < 90 {
			b += fmt.Sprintf("%-33s %-26s %9s\n", clip(model, 32), clip(file, 25), turn)
		} else {
			b += fmt.Sprintf("%-33s %-26s %11s %9s %9s\n", clip(model, 32), clip(file, 25), metric(r, r.DecodeTPSDeep, "t/s", false).String(), turn, metric(r, r.Scenario.ColdFirstReply.Seconds(), "s", true).String())
		}
		if strings.Contains(r.Quant.Name, "3_") || strings.Contains(r.Quant.Name, "IQ3") {
			b += "  " + m.theme.caveat.Render("3-bit · quality caveat") + "\n"
		}
	}
	b += "\nClaims we can't simulate yet\n"
	// Claims remain a separate lane and never enter the core ranker.
	for _, c := range ClaimsFor(m.detection.Target.Device.Name, m.detection.Target.OS) {
		b += "  claim " + c.ID + " · " + c.Figures[0].String() + " [" + c.Grade + "]\n"
	}
	b += m.theme.cloud.Render("Trial: real cloud model · no-logging providers · cap " + figure.Exact(m.cfg.Preferences.BudgetUSD, "USD", figure.Measured, "prefs:budget").String())
	b += "\n" + m.theme.dim.Render("calibrate for tighter numbers: trialm calibrate")
	return b
}
func (m *Root) details() string {
	row, ok := m.row()
	if !ok {
		return "No recipe selected."
	}
	r := row.Result
	mem := r.Memory
	b := r.Model.Name + " · " + r.Quant.Name + " · " + r.Runtime.Name + "\n"
	b += "Memory " + m.progressBar(mem.TotalBytes/max(1, mem.BudgetBytes)) + "\n" + figure.Exact(mem.TotalBytes/(1<<30), "GB", figure.Simulated, "memory:plan").String() + " of " + figure.Exact(mem.BudgetBytes/(1<<30), "GB", figure.Measured, "hardware:budget").String() + " · " + mem.Level + "\n"
	if r.Timing != nil {
		read := r.Timing.PrefillSec(r.Scenario.DecodeDepth, r.Scenario.DecodeDepth+m.listing.Scenario.NewTokensPerTurn)
		write := r.Timing.DecodeSec(r.Scenario.DecodeDepth) * float64(m.listing.Scenario.OutputTokensPerTurn)
		b += "Turn " + metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false).String() + "\n" + m.split(read, write, 0)
	}
	flash := "off"
	if r.Settings.FlashAttention {
		flash = "on: keeps prompt reading efficient"
	}
	b += "\n✓ flash attention · " + flash + "\n· KV cache · " + r.Settings.KVCacheType + " · " + mem.Level + " at this context\n· YaRN · native context; opt-in arrives in v0.6\n· spec · not simulated yet (v0.6)\n"
	b += strings.Join(r.TuneSteps, "\n")
	return b
}
func (m *Root) progressBar(v float64) string {
	v = max(0, min(1, v))
	w := max(8, min(36, m.width-8))
	if m.theme.plain || m.theme.ascii {
		n := int(float64(w) * v)
		return "[" + strings.Repeat("=", n) + strings.Repeat("-", w-n) + "]"
	}
	return m.bar.ViewAs(v)
}
func (m *Root) split(read, write, fixed float64) string {
	total := read + write + fixed
	if total <= 0 {
		return "Time split: no requests yet.\n"
	}
	a, b := int(100*read/total), int(100*write/total)
	label := fmt.Sprintf("reading %d%% · writing %d%% · fixed/tools %d%%", a, b, 100-a-b)
	if m.theme.plain || m.theme.ascii {
		return m.progressBar(read/total) + "\n" + label + "\n"
	}
	w := max(8, min(36, m.width-8))
	n := int(float64(w) * read / total)
	k := int(float64(w) * write / total)
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Render(strings.Repeat("━", n)) + lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Render(strings.Repeat("━", k)) + m.theme.dim.Render(strings.Repeat("━", max(0, w-n-k)))
	return bar + "\n" + label + "\n"
}
