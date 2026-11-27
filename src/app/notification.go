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
		// Payload is the DETAIL column and the Ctrl+Y source. Options is
		// flattened and capped at 180 cells for the row, so it cannot double
		// as the copy source: an error toast routinely runs longer.
		d.Payload = append(d.Payload, t.Text)
	}
	d.Filter = strings.TrimSpace(arg)
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
}

// notificationCols splits the box into the two columns: left = titles,
// right = the selected notification's full detail. boxW is clamped into
// [40,120] and then into winW-2, so the box plus border never exceeds the
// terminal (the trace windows floor at 70 columns; /notification must also
// fit a 40-column screen). cw is the content width: boxW minus dlgStyle's
// horizontal padding (3×2).
func notificationCols(winW int) (boxW, cw, leftW, rightW int) {
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
	// Even split with a 3-cell divider, like /trajectory.
	leftW = (cw - 3) / 2
	rightW = cw - 3 - leftW
	return boxW, cw, leftW, rightW
}

// notificationWin is the visible row count and the blank-line budget from the
// terminal height. The box costs:
//
//	placeH = winH - 2
//	frame  = 4   (border 2 + dlgStyle vertical padding 2)
//	chrome = 5   (title, meta, filter, column header, footer)
//	blanks = 2   (after filter, before footer; dropped on short terminals)
//	win    = placeH - frame - chrome - blanks   (list rows, capped at 20)
//
// The list region is padded to exactly win rows, so the box height is
// constant regardless of match count or cursor position.
func notificationWin(winH int) (win, blanks int) {
	placeH := winH - 2
	blanks = 2
	if placeH < 15 {
		blanks = 1
	}
	if placeH < 12 {
		blanks = 0
	}
	win = placeH - 4 - 5 - blanks
	if win < 1 {
		win = 1
	}
	if win > 20 { // same list cap /trajectory and the generic picker use
		win = 20
	}
	return win, blanks
}

// notificationGeom is the whole layout: columns from the width, rows from the
// height. The renderer and the wheel/page/click handlers share it so
// hit-testing never drifts from what is drawn.
func notificationGeom(winW, winH int) (boxW, cw, leftW, rightW, win, blanks int) {
	boxW, cw, leftW, rightW = notificationCols(winW)
	win, blanks = notificationWin(winH)
	return boxW, cw, leftW, rightW, win, blanks
}

// notificationRow is one pre-fitted left-column row. Segments are styled only
// after fitting, so no ANSI sequence is ever truncated or wrapped.
type notificationRow struct {
	marker string // "● INFO" / "× ERROR"
	err    bool
	body   string // title fitted to the body budget
	gap    int    // cells between marker and title
}

// plain composes the already-fitted segments; with the caller's 2-cell
// indent the result is exactly leftW.
func (r notificationRow) plain() string {
	return r.marker + strings.Repeat(" ", r.gap) + r.body
}

// buildNotificationRows fits every row to exactly leftW cells:
// 2 (indent/cursor) + markerW + gap + bodyW == leftW.
func buildNotificationRows(d *Dialog, fidx []int, leftW int) []notificationRow {
	rows := make([]notificationRow, 0, len(fidx))
	for _, ri := range fidx {
		if ri < 0 || ri >= len(d.Options) {
			continue
		}
		row := notificationRow{marker: "● INFO"}
		if ri < len(d.Descs) && strings.Contains(d.Descs[ri], "ERROR") {
			row.err = true
			row.marker = "× ERROR"
		}
		// A narrow terminal shrinks the gap before the title does.
		row.gap = 2
		bodyW := leftW - 2 - lipgloss.Width(row.marker) - row.gap
		if bodyW < 1 {
			row.gap = 1
			bodyW = leftW - 2 - lipgloss.Width(row.marker) - row.gap
		}
		if bodyW < 1 {
			bodyW = 1
		}
		row.body = Fit(d.Options[ri], bodyW)
		rows = append(rows, row)
	}
	return rows
}

// notificationDetailRows is the DETAIL column: the timestamp/kind meta line
// followed by the selected notification's full text, wrapped to w cells.
func notificationDetailRows(d *Dialog, w int) []string {
	meta := ""
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		meta = DescOf(d, d.FIdx[d.Cursor])
	}
	return append([]string{meta}, trajDetailLines(trajSelected(d), w)...)
}

// notificationPage pages the detail column when it overflows, and the title
// list when it does not (PgUp/PgDn must never be dead keys here).
func (m Model) notificationPage(d *Dialog, down bool) {
	_, _, _, rightW, win, _ := notificationGeom(m.winW, m.winH)
	lines := len(notificationDetailRows(d, rightW-4))
	if len(d.FIdx) > 0 && lines > win {
		if down {
			d.TrajOff = trajScroll(d.TrajOff+win, lines, win)
		} else {
			d.TrajOff = trajScroll(d.TrajOff-win, lines, win)
		}
		m.Refresh()
		return
	}
	n := len(d.FIdx)
	if n == 0 {
		return
	}
	if down {
		d.Cursor = min(d.Cursor+win, n-1)
	} else {
		d.Cursor = max(d.Cursor-win, 0)
	}
	d.TrajOff = 0
	m.Refresh()
}

// notificationLeftLines renders the title column: fixed rows so the box
// never resizes, with the …(+N) markers spending at most two rows.
func notificationLeftLines(d *Dialog, rows []notificationRow, win, leftW int) []string {
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	var lines []string
	if above {
		lines = append(lines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d above)", start), leftW-2)))
	}
	for fi := start; fi < end && fi < len(rows); fi++ {
		r := rows[fi]
		if fi == d.Cursor {
			// Selected: highlight the whole row; the marker word keeps the
			// info/error distinction readable without inner colors (an inner
			// reset would kill the highlight background past the token).
			lines = append(lines, "▸ "+rowHiStyle.Width(leftW-2).Render(Fit(r.plain(), leftW-2)))
			continue
		}
		st := okStyle
		if r.err {
			st = errStyle
		}
		lines = append(lines, "  "+st.Render(r.marker)+
			strings.Repeat(" ", r.gap)+statusBarStyle.Render(r.body))
	}
	if below {
		lines = append(lines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d below)", total-end), leftW-2)))
	}
	if total == 0 {
		lines = append(lines, "  "+toolStyle.Render(Fit("— no match —", leftW-2)))
	}
	for len(lines) > win { // defensive: markers must not grow the box
		lines = lines[:len(lines)-1]
	}
	for len(lines) < win {
		lines = append(lines, strings.Repeat(" ", leftW))
	}
	return lines
}

// notificationRightLines renders the detail column: the meta line, then the
// wrapped full text scrolled by TrajOff. The meta line leads so the timestamp
// stays readable even for a very long message.
func notificationRightLines(d *Dialog, win, rightW int) []string {
	var detail []string
	if len(d.FIdx) == 0 {
		msg := "— no notifications —"
		if d.Filter != "" {
			msg = "— no matching notifications —"
		}
		detail = trajDetailLines(msg, rightW-4)
	} else {
		detail = notificationDetailRows(d, rightW-4)
	}
	off := trajScroll(d.TrajOff, len(detail), win)
	shown := append([]string(nil), detail[off:]...)
	if len(shown) > win {
		shown = shown[:win]
	}
	lines := make([]string, 0, win)
	for i, ln := range shown {
		st := lipgloss.NewStyle().Foreground(cText)
		if len(d.FIdx) == 0 || off+i == 0 {
			st = toolStyle
		}
		lines = append(lines, "  "+trajPad(st.Render(Fit(ln, rightW-4)), rightW-2))
	}
	for len(lines) < win {
		lines = append(lines, "  "+trajPad("", rightW-2))
	}
	return lines
}

// renderNotificationDialog draws the fixed-size two-column view: left = one
// title per notification, right = the selected notification's full text.
// Timestamps and explicit INFO/ERROR labels remain readable without relying
// on color alone. Every content line is fitted to cw, so the box never
// resizes.
func (m Model) renderNotificationDialog(d *Dialog) string {
	boxW, cw, leftW, rightW, win, blanks := notificationGeom(m.winW, m.winH)
	rows := buildNotificationRows(d, d.FIdx, leftW)
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

	leftLines := notificationLeftLines(d, rows, win, leftW)
	rightLines := notificationRightLines(d, win, rightW)

	b.WriteString(trajPad("  "+sideTitleStyle.Render(Fit("TITLES", leftW-2))+" "+
		sepStyle.Render("│")+" "+"  "+sideTitleStyle.Render(Fit("DETAIL", rightW-2)), cw) + "\n")
	sep := sepStyle.Render("│")
	for i := 0; i < win; i++ {
		b.WriteString(trajPad(leftLines[i]+" "+sep+" "+rightLines[i], cw) + "\n")
	}

	foot := "type to filter · ↑↓ select · wheel/PgDn detail · Ctrl+Y copy · Esc close"
	if len(m.Dialogs) > 1 {
		foot += fmt.Sprintf(" · (%d pending)", len(m.Dialogs)-1)
	}
	if blanks > 1 {
		b.WriteString("\n")
	}
	b.WriteString(toolStyle.Render(Fit(m.dialogFoot(foot), cw)))
	box := dlgStyle.Width(boxW).Render(b.String())
	return lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box)
}
