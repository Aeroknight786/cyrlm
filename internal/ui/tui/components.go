package tui

import (
	"fmt"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/figure"
)

func (m *Root) helpView() string { return m.theme.dim.Render(m.footer()) }
func (m *Root) tableView() string {
	mw, fw := m.width*2/5, m.width-m.width*2/5-12
	cols := []table.Column{{Title: "MODEL", Width: mw}, {Title: "FILE · ENGINE", Width: fw}, {Title: fmt.Sprintf("%10s", "TURN"), Width: 10}}
	if m.width >= 90 {
		mw, fw = 30, 27
		cols = []table.Column{{Title: "MODEL", Width: mw}, {Title: "FILE · ENGINE", Width: fw}, {Title: fmt.Sprintf("%12s", "WRITES"), Width: 12}, {Title: fmt.Sprintf("%10s", "TURN"), Width: 10}, {Title: fmt.Sprintf("%10s", "FIRST"), Width: 10}}
	}
	rows := []table.Row{}
	for i, row := range m.listing.Rows {
		r := row.Result
		mark := "  "
		if i == m.selected {
			mark = "❯ "
		}
		model := fmt.Sprintf("%s%d %s", mark, i+1, r.Model.Name)
		file := r.Quant.Name + " " + figure.Exact(float64(r.Quant.FileBytes)/1e9, "GB", figure.Simulated, "catalog:file").String() + " · " + r.Runtime.ID
		turn := metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false).String()
		cells := table.Row{model, file, fmt.Sprintf("%10s", turn)}
		if m.width >= 90 {
			cells = table.Row{model, file, fmt.Sprintf("%12s", metric(r, r.DecodeTPSDeep, "t/s", false).String()), fmt.Sprintf("%10s", turn), fmt.Sprintf("%10s", metric(r, r.Scenario.ColdFirstReply.Seconds(), "s", true).String())}
		}
		rows = append(rows, cells)
	}
	tb := table.New(table.WithColumns(cols), table.WithRows(rows), table.WithWidth(m.width), table.WithHeight(len(rows)+1), table.WithFocused(true), table.WithStyles(table.Styles{Header: m.theme.dim.Bold(true), Cell: lipgloss.NewStyle(), Selected: m.theme.selected}))
	tb.SetCursor(max(0, m.selected))
	return tb.View()
}
