package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// treePageSize is the native Pi tree page step: one terminal page of rows.
func treePageSize(winH int) int {
	win := winH - 8
	if win < 8 {
		win = 8
	}
	if win > 30 {
		win = 30
	}
	return win
}

func (m Model) treePage(d *Dialog, down bool) {
	n := len(d.FIdx)
	if n == 0 {
		return
	}
	page := treePageSize(m.winH)
	if down {
		d.Cursor += page
	} else {
		d.Cursor -= page
	}
	if d.Cursor < 0 {
		d.Cursor = 0
	}
	if d.Cursor >= n {
		d.Cursor = n - 1
	}
	m.Refresh()
}

// updateTreeWheel matches native Pi: the wheel moves the selected row. The
// old trajectory wheel logic is intentionally not reused because /tree has
// no detail pane.
func (m Model) updateTreeWheel(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	n := len(d.FIdx)
	if n == 0 {
		return m, nil
	}
	if mm.Button == tea.MouseButtonWheelDown {
		d.Cursor = (d.Cursor + 1) % n
	} else {
		d.Cursor = (d.Cursor - 1 + n) % n
	}
	m.Refresh()
	return m, nil
}

// renderTreeDialog reproduces Pi's native Session Tree screen: a full-width
// overlay, a compact command hint, a search line, horizontal separators, and
// a flat chronological list. The selected active row is highlighted in place.
func (m Model) renderTreeDialog(d *Dialog) string {
	width := m.winW
	if width < 40 {
		width = 40
	}
	win := treePageSize(m.winH)
	var b strings.Builder
	line := sepStyle.Render(strings.Repeat("─", width))
	b.WriteString(line + "\n")
	b.WriteString(sideTitleStyle.Render("Session Tree") + "\n")
	controls := "↑/↓ move · ←/→ page · option+←/→ branch · ctrl+x copy · shift+l label · shift+t label time · filters ctrl+d/t/u/l/a · cycle ctrl+o/shift+ctrl+o"
	b.WriteString(statusBarStyle.Render(Fit(controls, width)) + "\n")
	b.WriteString(statusBarStyle.Render("Type to search:") + " " + d.Filter + "▌\n")
	b.WriteString(line + "\n")

	total := len(d.FIdx)
	start := 0
	if d.Cursor >= win {
		start = d.Cursor - win + 1
	}
	end := start + win
	if end > total {
		end = total
	}
	if end-start < win {
		start = end - win
	}
	if start < 0 {
		start = 0
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		row := ""
		if ri >= 0 && ri < len(d.Options) {
			row = Fit(d.Options[ri], width)
		}
		if fi == d.Cursor {
			b.WriteString(rowHiStyle.Render(Fit(row, width)) + "\n")
		} else {
			b.WriteString(trajColorRow(row, trajDescKind(d, ri)) + "\n")
		}
	}
	for i := end - start; i < win; i++ {
		b.WriteString(strings.Repeat(" ", width) + "\n")
	}
	if total > 0 {
		b.WriteString(toolStyle.Render(Fit(fmt.Sprintf("(%d/%d)", d.Cursor+1, total), width)))
	} else {
		b.WriteString(toolStyle.Render(Fit("(0/0)", width)))
	}

	return lipgloss.Place(m.winW, maxInt(m.winH-2, 1), lipgloss.Left, lipgloss.Top, strings.TrimRight(b.String(), "\n"))
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
