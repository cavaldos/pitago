package app

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const maxNotificationHistory = 200

// OpenNotifications opens the in-memory notification log newest-first. The
// optional argument pre-fills the generic picker filter.
func (m *Model) OpenNotifications(arg string) {
	d := &Dialog{
		Kind:  "notification",
		Title: "Notification history",
	}
	for i := len(m.notificationHistory) - 1; i >= 0; i-- {
		t := m.notificationHistory[i]
		kind := "INFO"
		if t.Err {
			kind = "ERROR"
		}
		d.Options = append(d.Options, Short(strings.TrimSpace(t.Text), 180))
		d.Descs = append(d.Descs, t.At.Format("01-02 15:04:05")+" · "+kind)
	}
	d.Filter = strings.TrimSpace(arg)
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
}

// notificationGeom derives the box and list geometry from the terminal size.
//
// dlgStyle is Border + Padding(1,3) and lipgloss Width() covers the padding
// but not the border, so the box costs:
//   - width:  boxW + 2 (border); content width cw = boxW - 6 (padding 3+3)
//   - height: lines + 4 (border 2 + vertical padding 2)
//
// Width: boxW is clamped into [40,120] and then into winW-2, so the box plus
// border never exceeds the terminal (a 40-col screen gets a 38-col box
// instead of the old 70-col floor that rendered 72 wide). Every emitted line
// is fitted to cw, so lipgloss never wraps and the height stays predictable.
//
// Height budget (box must fit placeH = winH-2):
//
//	placeH = winH - 2
//	frame  = 4   (border 2 + dlgStyle vertical padding 2)
//	chrome = 4   (title + meta + filter + footer)
//	blanks = 2   (after filter, before footer; dropped on short terminals)
//	win    = placeH - frame - chrome - blanks   (list rows, markers included)
//	height = win + blanks + chrome + frame = placeH
//
// fixedWin spends at most two of the win rows on the "…(+N above/below)"
// markers, and the list region is padded to exactly win rows, so the box
// height is constant regardless of match count or cursor position.
func notificationGeom(winW, winH int) (boxW, cw, rowW, win, blanks int) {
	boxW = winW - 10
	if boxW > 120 {
		boxW = 120
	}
	if boxW < 40 {
		boxW = 40
	}
	if boxW > winW-2 { // border adds 2: never overflow a narrow terminal
		boxW = winW - 2
	}
	if boxW < 12 {
		boxW = 12
	}
	cw = boxW - 6
	if cw < 8 {
		cw = 8
	}
	rowW = cw - 2
	if rowW < 6 {
		rowW = 6
	}
	placeH := winH - 2
	blanks = 2
	if placeH < 14 {
		blanks = 1
	}
	if placeH < 11 {
		blanks = 0
	}
	win = placeH - 4 - 4 - blanks
	if win < 1 {
		win = 1
	}
	if win > 20 { // same list cap /trajectory and the generic picker use
		win = 20
	}
	return boxW, cw, rowW, win, blanks
}

// notificationWindow bounds rendered rows independently of retained items.
func notificationWindow(winH int) int {
	_, _, _, win, _ := notificationGeom(100, winH)
	return win
}

// notificationRow is one pre-fitted plain row. Segments are styled only
// after fitting, so no ANSI sequence is ever truncated or wrapped.
type notificationRow struct {
	marker string // "● INFO" / "× ERROR"
	err    bool
	body   string // fitted to the body budget
	desc   string // timestamp + kind
	gap    int    // cells between marker/body/desc (2, or 1 when narrow)
}

// plain composes the already-fitted segments; with the caller's 2-cell
// indent the result is exactly rowW. The selected row is fitted as one plain
// string so no ANSI sequence is ever cut in half.
func (r notificationRow) plain() string {
	return r.marker + strings.Repeat(" ", r.gap) + r.body +
		strings.Repeat(" ", r.gap) + r.desc
}

// buildNotificationRows fits every row to exactly rowW cells:
// 2 (indent/cursor) + markerW + bodyW + 2 (gap) + descW == rowW.
func buildNotificationRows(d *Dialog, fidx []int, rowW int) []notificationRow {
	rows := make([]notificationRow, 0, len(fidx))
	for _, ri := range fidx {
		if ri < 0 || ri >= len(d.Options) {
			continue
		}
		desc := DescOf(d, ri)
		row := notificationRow{marker: "● INFO", desc: desc, err: strings.Contains(desc, "ERROR")}
		if row.err {
			row.marker = "× ERROR"
		}
		markerW := lipgloss.Width(row.marker)
		descW := lipgloss.Width(row.desc)
		// Reserve the 2-cell indent, the marker, the desc and both gaps
		// before the body; the remainder is the body budget. A gap drops
		// to 1 before the body does, so a narrow terminal shrinks text
		// instead of overflowing the box.
		row.gap = 2
		if rowW-markerW-descW-2*row.gap-2 < 1 {
			row.gap = 1
		}
		bodyW := rowW - markerW - descW - 2*row.gap - 2
		if bodyW < 1 {
			bodyW = 1
		}
		row.marker = Fit(row.marker, markerW)
		row.desc = Fit(row.desc, descW)
		row.body = Fit(d.Options[ri], bodyW)
		rows = append(rows, row)
	}
	return rows
}

// renderNotificationDialog draws a fixed-height list. Timestamps and explicit
// INFO/ERROR labels remain readable without relying on color alone.
func (m Model) renderNotificationDialog(d *Dialog) string {
	boxW, cw, rowW, win, blanks := notificationGeom(m.winW, m.winH)
	rows := buildNotificationRows(d, d.FIdx, rowW)
	var b strings.Builder

	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(d.Title, cw)) + "\n")
	// The counter describes the snapshot this dialog was built from
	// (d.Options), not the live history: notifications arriving while the
	// window is open must not change the header under the user.
	b.WriteString(statusBarStyle.Render(Fit(fmt.Sprintf("Latest %d of %d · RAM only",
		min(len(d.Options), maxNotificationHistory), len(d.Options)), cw)) + "\n")
	b.WriteString(statusBarStyle.Render(Fit("filter: "+d.Filter+"▌", cw)) + "\n")
	if blanks > 0 {
		b.WriteString("\n")
	}

	// List region: markers + rows, padded to exactly win rows.
	var list []string
	start, end, above, below := fixedWin(d.Cursor, len(rows), win)
	if above {
		list = append(list, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d above)", start), rowW)))
	}
	if len(rows) == 0 {
		empty := "— no notifications —"
		if d.Filter != "" {
			empty = "— no matching notifications —"
		}
		list = append(list, "  "+toolStyle.Render(Fit(empty, rowW)))
	}
	for fi := start; fi < end; fi++ {
		r := rows[fi]
		if fi == d.Cursor {
			// Selected: highlight the whole row; the marker word keeps the
			// info/error distinction readable without inner colors (an inner
			// reset would kill the highlight background past the token).
			list = append(list, "▸ "+rowHiStyle.Width(rowW).Render(Fit(r.plain(), rowW)))
		} else {
			st := okStyle
			if r.err {
				st = errStyle
			}
			gap := strings.Repeat(" ", r.gap)
			list = append(list, "  "+st.Render(r.marker)+gap+statusBarStyle.Render(r.body)+toolStyle.Render(gap+r.desc))
		}
	}
	if below {
		list = append(list, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d below)", len(rows)-end), rowW)))
	}
	for len(list) > win { // defensive: never exceed the list budget
		list = list[:len(list)-1]
	}
	for len(list) < win {
		list = append(list, strings.Repeat(" ", rowW))
	}
	for _, ln := range list {
		b.WriteString(ln + "\n")
	}

	foot := "type to filter · ↑↓ select · PgUp/PgDn page · Esc close"
	if len(m.Dialogs) > 1 {
		foot += fmt.Sprintf(" · (%d pending)", len(m.Dialogs)-1)
	}
	if blanks > 1 {
		b.WriteString("\n")
	}
	b.WriteString(toolStyle.Render(Fit(foot, cw)))
	box := dlgStyle.Width(boxW).Render(b.String())
	return lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box)
}
