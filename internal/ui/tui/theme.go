// Package tui implements the single inline terminal program. Core stays stdlib.
package tui

import (
	"os"
	"runtime"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/huh/v2"
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
	return palette{style("6"), style("2"), style("3"), style("1"), style("5"), style("8"), noColor, ascii || noColor}
}
func (p palette) wordmark() string {
	if p.plain || p.ascii {
		return "^ triaLM"
	}
	colors := lipgloss.Blend1D(6, lipgloss.Color("#34d9ce"), lipgloss.Color("#d881ff"))
	word := "▲ "
	for i, r := range "triaLM" {
		word += lipgloss.NewStyle().Bold(true).Foreground(colors[i]).Render(string(r))
	}
	return word
}
func (p palette) glyph(s string) string {
	if !p.ascii {
		return s
	}
	return strings.NewReplacer("✦", "*", "✧", "*", "◐", "+", "○", "o", "▲", "^", "⏎", "enter", "—", "-", "╭", "+", "╮", "+", "╰", "+", "╯", "+", "─", "-", "│", "|", "≥", ">=", "≤", "<=", "±", "+/-", "…", "...", "–", "-", "✓", "v", "✗", "x", "●", "*", "·", "-", "❯", ">", "‹", "<", "›", ">", "━", "=", "╷", "|", "█", "=", "░", "-", "≈", "~", "×", "x", "→", "->", "↑", "^", "↓", "v", "←", "<", "→", ">").Replace(s)
}
func programOptions() []tea.ProgramOption {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return []tea.ProgramOption{tea.WithColorProfile(colorprofile.Ascii)}
	}
	return nil
}

// Fields get a fixed theme; no component is asked to query the background.
func (m *Root) formTheme() huh.Theme {
	return huh.ThemeFunc(func(bool) *huh.Styles {
		s := huh.ThemeBase(true)
		s.Focused.Base = lipgloss.NewStyle()
		s.Blurred.Base = lipgloss.NewStyle()
		s.Focused.SelectedOption = m.theme.selected
		s.Focused.NextIndicator = m.theme.selected.SetString(" ›")
		s.Focused.PrevIndicator = m.theme.selected.SetString("‹ ")
		s.Blurred = s.Focused
		return s
	})
}

func (p palette) rich() palette {
	if p.plain {
		return p
	}
	p.selected = lipgloss.NewStyle().Foreground(lipgloss.Color("#008b99"))
	p.good = lipgloss.NewStyle().Foreground(lipgloss.Color("#22863a"))
	p.caveat = lipgloss.NewStyle().Foreground(lipgloss.Color("#b08900"))
	p.blocked = lipgloss.NewStyle().Foreground(lipgloss.Color("#d73a49"))
	p.cloud = lipgloss.NewStyle().Foreground(lipgloss.Color("#a855aa"))
	p.dim = lipgloss.NewStyle().Foreground(lipgloss.Color("#808080"))
	return p
}

func asciiTerminal(goos string, getenv func(string) string) bool {
	return getenv("TERM") == "dumb" || (goos == "windows" && getenv("WT_SESSION") == "" && getenv("TERM_PROGRAM") != "vscode")
}

var nativeOS = runtime.GOOS
