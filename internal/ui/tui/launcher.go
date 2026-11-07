package tui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/calib"
	"github.com/aeroknight786/trialm/internal/figure"
	"github.com/aeroknight786/trialm/internal/harness"
	"os"
)

func toolName(id string) string { return strings.TrimSuffix(harness.DisplayName(id), " CLI") }
func (m *Root) card(text string) string {
	border := lipgloss.RoundedBorder()
	if m.theme.ascii {
		border = lipgloss.ASCIIBorder()
	}
	return lipgloss.NewStyle().Border(border).BorderForeground(m.theme.selected.GetForeground()).Padding(0, 1).Width(max(16, min(m.width, 80)-2)).Render(wrap(text, max(10, min(m.width, 80)-6)))
}
func (m *Root) waiting() string {
	if m.theme.plain || m.theme.ascii {
		return "..."
	}
	return m.theme.selected.Render(m.spin.View())
}
func (m *Root) launcher() string {
	tool := toolName(m.cfg.Preferences.Tool)
	machine, _, _ := strings.Cut(m.detection.Target.Device.Name, " (")
	if m.detection.Target.Synthetic {
		machine = "what-if " + machine
	}
	ram := "RAM unknown"
	if m.detection.Target.RAMBytes > 0 {
		ram = figure.Exact(m.detection.Target.RAMBytes/(1<<30), "GB", figure.Measured, "hardware:ram").String()
	}
	header := m.theme.wordmark() + "    " + machine + " · " + ram + " · " + tool + " ✓"
	if m.detecting {
		header = m.theme.wordmark() + "    Let's get your models running"
	}
	b := m.card(header) + "\n"
	if m.detection.System != nil && m.detection.System.Power.LowPower {
		b += m.theme.caveat.Render("  Low Power Mode is on: local models run about 3× slower. Turn it off in System Settings → Battery.") + "\n"
	}
	if m.detecting {
		lines := []string{"machine", "coding tools", "engines", "models on disk"}
		for i, line := range lines {
			mark := m.waiting()
			if (i < 3 && m.detected) || i == 1 {
				mark = m.theme.good.Render("✓")
			}
			b += "\n  " + mark + " " + line
		}
		return b
	}
	if m.calibration != nil {
		elapsed := time.Since(m.started).Round(time.Second).String() // "6s", "2m10s"
		b += "\n" + m.card(m.waiting()+" Calibrating this machine\n"+m.progressBar(m.calibration.fraction)+"\nElapsed "+elapsed+" · ETA "+m.calibration.eta) + "\n"
	} else if m.offer {
		label := "✦ Calibrate this machine once · recommended"
		if m.detection.Target.OS == "darwin" {
			label = "✦ Calibrate this Mac once · recommended"
		}
		b += "\n" + m.card(m.theme.selected.Bold(true).Render(label)+"\n"+calib.Duration(calib.Plan(false, os.Getenv("TRIALM_CALIBRATE_SPEC") != "0"))+" · no downloads · enter start · s skip") + "\n"
	}
	b += "\n  " + m.theme.selected.Bold(true).Render("Your models") + "\n"
	for i, choice := range m.models {
		mark, state, style := "○", choice.Download.String()+" · not on disk", m.theme.dim
		speed := choice.Speed.String()
		if choice.Journal != nil {
			mark, state, style = "●", "ready", m.theme.good
		} else {
			speed = "~" + strings.TrimPrefix(speed, "~")
			if choice.Row.LocalPath != "" {
				mark, state, style = "◐", "on disk · not set up", m.theme.selected
			}
		}
		cursor := "  "
		if i == m.modelIndex {
			cursor = "❯ "
		}
		nameWidth := max(12, min(25, m.width-39))
		line := fmt.Sprintf("%s%s %-*s %10s   %s", cursor, mark, nameWidth, clip(choice.Row.Result.Model.Name, nameWidth), speed, state)
		if i == m.modelIndex && !m.theme.plain {
			style = style.Background(lipgloss.Color("#182b30"))
		}
		b += "  " + style.Render(line) + "\n"
	}
	cursor := "  "
	if m.modelIndex == len(m.models) {
		cursor = "❯ "
	}
	b += "\n  " + m.theme.cloud.Render(cursor+"✦ Find something faster or smarter")
	if m.notice != "" {
		b += "\n\n  " + m.theme.dim.Render(m.notice)
	}
	return b
}
