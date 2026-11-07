// Package tui implements the single inline terminal program. Core stays stdlib.
package tui

import (
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

type palette struct {
	selected, good, caveat, blocked, cloud, dim lipgloss.Style
	plain, ascii                                bool
}

func newPalette(noColor, ascii bool) palette {
	style := func(c string) lipgloss.Style {
		s := lipgloss.NewStyle()
		if !noColor {
			s = s.Foreground(lipgloss.Color(c))
		}
		return s
	}
	return palette{style("6"), style("2"), style("3"), style("1"), style("5"), style("8"), noColor, ascii}
}
func (p palette) wordmark() string {
	if p.plain {
		return "triaLM"
	}
	return p.selected.Render("tria") + lipgloss.NewStyle().Bold(true).Render("LM")
}
func (p palette) glyph(s string) string {
	if !p.ascii {
		return s
	}
	return strings.NewReplacer("✓", "v", "✗", "x", "●", "*", "·", "-", "❯", ">", "━", "=", "█", "=", "░", "-", "≈", "~", "×", "x", "→", "->", "↑", "^", "↓", "v", "←", "<", "→", ">").Replace(s)
}
func programOptions() []tea.ProgramOption {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return []tea.ProgramOption{tea.WithColorProfile(colorprofile.Ascii)}
	}
	return nil
}
