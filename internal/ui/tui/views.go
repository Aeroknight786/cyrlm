package tui

import (
	"fmt"
	"math"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/core"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/predict"
	"github.com/charmbracelet/x/ansi"
)

func wrap(s string, w int) string { return ansi.Wrap(s, max(1, w), "") }
func strip(s string) string       { return ansi.Strip(s) }
func clip(s string, w int) string { return ansi.Truncate(s, max(1, w), "") }
func estimated(f figure.Figure, local ...bool) string {
	if len(local) > 0 && local[0] && f.Kind == figure.Calibrated {
		return strings.TrimPrefix(f.String(), "~")
	}
	return "~" + strings.TrimPrefix(f.String(), "~")
}
func metric(r predict.Result, v float64, unit string, reading bool) figure.Figure {
	return core.PredictionMetric(r, v, unit, reading)
}
func (m *Root) body() string {
	title := m.theme.wordmark()
	switch m.screen() {
	case First:
		return title + "\n\nTry it in the cloud first\nConnect OpenRouter to start a private trial.\nTrial cap " + figure.Exact(m.cfg.Preferences.BudgetUSD, "USD", figure.Measured, "prefs:budget").String() + "."
	case Paste:
		if m.password == nil {
			return "OpenRouter key"
		}
		return title + "\n" + m.authURL + "\n" + m.password.View()
	case Needs:
		lines := []string{"What do you need? · Enter accepts every default"}
		for i, f := range m.fields {
			mark := m.theme.selected.Render(selection(i, m.field))
			lines = append(lines, mark+fmt.Sprintf("%-18s", m.labels[i])+strings.TrimSpace(strings.ReplaceAll(f.View(), "\n", " ")))
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
		partial := ""
		if m.last.Incomplete {
			partial = "Partial summary · window closed before the final report.\n"
		}
		split := m.split(r.ReadingSeconds, r.WritingSeconds, r.LoadSeconds+r.BetweenTurnsSeconds)
		return partial + "Trial over · " + m.last.Model + " · " + m.last.Quant + " · " + m.last.Runtime + "\n" + r.Plain(m.last.Band) + split + "\nHow did it feel?"
	case Home:
		return m.launcher()
	case Help:
		return "Every key, one place\n\nLauncher\n  ↑↓ / j k select · enter run · tab cycle installed tools\n  t cloud trial · f find faster or smarter · / search · c claims\n  enter calibrate once · s skip forever\n\nRecipes\n  ↑↓ / j k select · enter trial · w trial in a window\n  s set up · n / r needs · d details · / search · c claims\n  ? figures and sources · space toggle boosts in details\n\nSetup and Ready\n  enter / y approve · enter open tool · n / esc back\n  d steps and logs · ctrl-c pause\n  u undo from Ready · q later\n\nCloud sign-in\n  enter browser / connect · p paste · esc back\n\nTrial summary\n  1 too slow · 2 fine · 3 great · s set up · t another\n\nNeeds · ↑↓ / j k / tab row · ←→ / h l change · enter apply\nSearch · enter search · esc back\nClaims · ↑↓ / j k select · enter / ? open · ? source\nErrors · d raw details\nChat · enter send · esc finish\nEverywhere · esc back · q quit · ctrl-c stop"
	case Gallery:
		b := "Published claims · unverified by triaLM · never ranked\n"
		for i, c := range Claims() {
			b += selection(i, m.selected) + c.ID + " [" + c.Grade + "]\n  " + c.Hardware + "\n  " + c.Figures[0].String() + "\n"
		}
		return b
	case ClaimCard:
		if m.pendingClaim {
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
		if len(m.stack) > 1 && m.stack[len(m.stack)-2] == ClaimCard {
			cs := Claims()
			c := cs[min(max(0, m.selected), len(cs)-1)]
			return "Source · " + c.SourceName + "\n" + c.SourceURL + "\n" + c.Date + " · " + c.DateBasis + "\nGrade " + c.Grade + " · unverified by triaLM\n" + c.Hardware
		}
		if row, ok := m.row(); ok {
			r := row.Result
			f := metric(r, r.DecodeTPSDeep, "tok/s", false)
			return fmt.Sprintf("Why these numbers?\n%s · %s\n%s\nRange %s to %s\nSource: %s\nWriting ±%d%% · reading ±%d%%\n%s", r.Model.Name, r.Runtime.Name, estimated(f, m.detection.Target.OS == "darwin"), figure.BandNumber(f.Lo, 26), figure.BandNumber(f.Hi, 26), f.Src, r.Accuracy.DecodePct, r.Accuracy.PrefillPct, strings.Join(r.Warnings, "\n"))
		}
		return "Claims are source-reported figures, not triaLM measurements.\n" + Claims()[0].SourceURL
	case Setup:
		return m.setupBody()
	case Ready:
		return m.readyBody()
	case Launching:
		return "Opening " + toolName(m.cfg.Preferences.Tool) + " on the local model…"
	case Trial:
		return m.meter.String()
	case Chat:
		return m.chat.view()
	}
	return title
}
func (m *Root) recipes() string {
	b := fmt.Sprintf("Recipes for %s · %s · %dk native\nSimulated or calibrated · uncertainty shown on ?\n", m.cfg.Preferences.Tool, m.cfg.Preferences.Priority, m.cfg.Preferences.Context/1024)
	if m.detection.Target.OS == "darwin" {
		b = fmt.Sprintf("Recipes for %s on this Mac · %s\n", toolName(m.cfg.Preferences.Tool), m.cfg.Preferences.Priority)
		b += wrap(m.theme.dim.Render(fmt.Sprintf("First reply reads %s's opening prompt (~%sK tokens); later replies reuse it. ~ = estimate, ? = how sure.", toolName(m.cfg.Preferences.Tool), core.OpeningPromptFigure(m.listing.Scenario, m.cfg.Preferences.Tool).Number())), m.width) + "\n"
	}
	if len(m.listing.Rows) == 0 {
		if m.busy {
			return b // the spinner line says it is still ranking
		}
		return b + "No working recipe for these needs. Press n to change them."
	}
	if m.width >= 60 {
		b += m.tableView() + "\n"
	} else {
		for i, row := range m.listing.Rows {
			r := row.Result
			b += fmt.Sprintf("%s%d %s\n  %s · %s\n  turn %s\n", selection(i, m.selected), i+1, r.Model.Name, r.Quant.Name, r.Runtime.ID, estimated(metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false), m.detection.Target.OS == "darwin"))
		}
	}
	for _, row := range m.listing.Rows {
		if strings.Contains(row.Result.Quant.Name, "IQ3") {
			b += m.theme.caveat.Render("3-bit files: quality caveat") + "\n"
			break
		}
	}
	cs := ClaimsFor(m.detection.Target.Device.Name, m.detection.Target.OS)
	if m.detection.Target.OS == "darwin" {
		if len(cs) > 0 {
			b += m.theme.dim.Render("c published claims for similar hardware") + "\n"
		}
	} else {
		b += "\nClaims we can't simulate yet\n"
		for _, c := range cs {
			b += "  claim " + c.ID + " · " + c.Figures[0].String() + " [" + c.Grade + "]\n"
		}
	}
	b += wrap(m.theme.cloud.Render("Trial: real cloud model · no-logging providers · cap "+figure.Exact(m.cfg.Preferences.BudgetUSD, "USD", figure.Measured, "prefs:budget").String()), m.width)
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
	b += "Memory " + m.progressBar(mem.TotalBytes/max(1, mem.BudgetBytes)) + "\n" + estimated(figure.Exact(mem.TotalBytes/(1<<30), "GB", figure.Simulated, "memory:plan")) + " of " + figure.Exact(mem.BudgetBytes/(1<<30), "GB", figure.Measured, "hardware:budget").String() + " · " + mem.Level + "\n"
	if r.Timing != nil {
		read := r.Scenario.FollowUpTTFT.Seconds()
		write := max(0, r.Scenario.FollowUpTurn.Seconds()-read)
		b += "Turn " + estimated(metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false), m.detection.Target.OS == "darwin") + "\n" + m.split(read, write, 0)
	}
	flash := "off"
	if r.Settings.FlashAttention {
		flash = "on: keeps prompt reading efficient"
	}
	b += fmt.Sprintf("\n✓ flash attention, %d slot(s) · %s\n· KV cache · %s · %s at this context\n· YaRN · not needed: native %dk ≥ %dk\n· spec · not simulated yet (v0.6)\n", r.Settings.Slots, flash, r.Settings.KVCacheType, mem.Level, r.Model.NativeCtx/1024, r.Settings.ContextTokens/1024)
	b += strings.Join(r.TuneSteps, "\n")
	if mem.Level == predict.FitOK {
		b = m.theme.good.Render("✓ fits") + "\n" + b
	} else if mem.Level == predict.FitNo {
		b = m.theme.blocked.Render("✗ blocked") + "\n" + b
	} else {
		b = m.theme.caveat.Render("caveat: "+mem.Level) + "\n" + b
	}
	return b
}
func (m *Root) progressBar(v float64) string {
	v = max(0, min(1, v))
	w := max(8, min(36, m.width-8))
	if m.theme.plain || m.theme.ascii {
		n := int(float64(w) * v)
		return "[" + strings.Repeat("=", n) + strings.Repeat("-", w-n) + "]"
	}
	if m.busy {
		return m.bar.View()
	}
	return m.bar.ViewAs(v)
}
func (m *Root) split(read, write, fixed float64) string {
	total := read + write + fixed
	if total <= 0 {
		return "Time split: no requests yet.\n"
	}
	a := int(math.Round(100 * read / total))
	b := min(100-a, int(math.Round(100*write/total)))
	label := fmt.Sprintf("reading %d%% · writing %d%% · fixed/tools %d%%", a, b, 100-a-b)
	if m.theme.plain || m.theme.ascii {
		return m.progressBar(read/total) + "\n" + label + "\n"
	}
	w := max(8, min(36, m.width-8))
	n := int(float64(w) * read / total)
	k := int(float64(w) * write / total)
	bar := strings.Builder{}
	for i := 0; i < w; i++ {
		color := fmt.Sprintf("#%02x%02x%02x", 40+140*i/max(1, w-1), 150-70*i/max(1, w-1), 170+50*i/max(1, w-1))
		glyph := "━"
		if i == n || i == n+k {
			glyph = "╷"
		}
		bar.WriteString(lipgloss.NewStyle().Foreground(lipgloss.Color(color)).Render(glyph))
	}

	return bar.String() + "\n" + label + "\n"
}

// ratingWord names the after-trial answer instead of showing a score.
