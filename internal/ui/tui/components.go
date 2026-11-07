package tui

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/table"
	"charm.land/lipgloss/v2"
	"github.com/aeroknight786/trialm/internal/figure"
)

func (m *Root) helpView() string { return m.theme.dim.Render(m.footer()) }
func (m *Root) tableView() string {
	mw, fw := m.width*2/5, m.width-m.width*2/5-12
	cols := []table.Column{{Title: "MODEL", Width: mw}, {Title: "FILE · ENGINE", Width: fw}, {Title: fmt.Sprintf("%10s", "TURN"), Width: 10}}
	if m.width >= 90 {

		cols = []table.Column{{Title: "MODEL", Width: 30}, {Title: "FILE · ENGINE", Width: 27}, {Title: fmt.Sprintf("%12s", "WRITES"), Width: 12}, {Title: fmt.Sprintf("%10s", "TURN"), Width: 10}, {Title: fmt.Sprintf("%10s", "FIRST"), Width: 10}}
	}
	if m.detection.Target.OS == "darwin" {
		cols[1].Width -= 4
		cols[len(cols)-1] = table.Column{Title: "LATER REPLIES", Width: 14}
		if m.width >= 90 {
			cols[2].Title, cols[3].Title, cols[4].Title = "FIRST REPLY", "LATER REPLIES", "WRITING"
			cols[0].Width, cols[1].Width, cols[2].Width, cols[3].Width, cols[4].Width = 26, 27, 12, 14, 10
		}
	}
	rows := []table.Row{}
	for i, row := range m.listing.Rows {
		r := row.Result
		file := r.Quant.Name + " " + figure.Exact(float64(r.Quant.FileBytes)/1e9, "GB", figure.Simulated, "catalog:file").String() + " · " + r.Runtime.ID
		turn := estimated(metric(r, r.Scenario.FollowUpTurn.Seconds(), "s", false), m.detection.Target.OS == "darwin")
		cells := table.Row{fmt.Sprintf("%s%d %s", selection(i, m.selected), i+1, r.Model.Name), file, fmt.Sprintf("%10s", turn)}
		if m.width >= 90 {
			cells = table.Row{fmt.Sprintf("%s%d %s", selection(i, m.selected), i+1, r.Model.Name), file, fmt.Sprintf("%12s", estimated(metric(r, r.DecodeTPSDeep, "t/s", false), m.detection.Target.OS == "darwin")), fmt.Sprintf("%10s", turn), fmt.Sprintf("%10s", estimated(metric(r, r.Scenario.ColdFirstReply.Seconds(), "s", true), m.detection.Target.OS == "darwin"))}
		}
		if m.detection.Target.OS == "darwin" && m.width >= 90 {
			cells[2], cells[3], cells[4] = fmt.Sprintf("%12s", strings.TrimSpace(cells[4])), fmt.Sprintf("%14s", turn), fmt.Sprintf("%10s", strings.TrimSpace(cells[2]))
		}
		rows = append(rows, cells)
	}
	tb := table.New(table.WithColumns(cols), table.WithRows(rows), table.WithWidth(m.width), table.WithHeight(len(rows)+1), table.WithFocused(true), table.WithStyles(table.Styles{Header: m.theme.dim.Bold(true), Cell: lipgloss.NewStyle(), Selected: m.theme.selected}))
	tb.SetCursor(max(0, m.selected))
	return tb.View()
}

func selection(i, selected int) string {
	if i == selected {
		return "❯ "
	}
	return "  "
}
