package app

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Trajectory window: left = step names, right = the selected step's full
// detail. The box is FIXED size: every content line is fitted to exactly
// cw (boxW minus dlgStyle's padding+border) so long words, long filters or
// long footers can never stretch it. Wheel over the detail column scrolls
// it (TrajOff); wheel over the steps moves the selection; PgUp/PgDn page
// the detail. ↑↓/filter/Enter/Esc stay on the generic updateDialog path.

// trajTagRe matches tool tags like [read: a.go] for highlighting.
var trajTagRe = regexp.MustCompile(`\[[^\[\]]*\]`)

// trajGeom is the two-column layout from the terminal width. The renderer
// and the wheel/page handlers share it so column hit-testing never drifts
// from what is drawn.
func trajGeom(winW int) (boxW, leftW, rightW int) {
	boxW = winW - 10
	if boxW < 70 {
		boxW = 70
	}
	if boxW > 150 {
		boxW = 150
	}
	leftW = 42
	if boxW < 120 {
		leftW = 36
	}
	if boxW < 100 {
		leftW = 28
	}
	rightW = boxW - 6 - leftW - 3
	if rightW < 30 {
		rightW = 30
	}
	return boxW, leftW, rightW
}

// trajWin is the visible row count from the terminal height.
func trajWin(winH int) int {
	win := winH - 14
	if win < 12 {
		win = 12
	}
	if win > 24 {
		win = 24
	}
	return win
}

// trajContentW is the exact content width: boxW minus dlgStyle's horizontal
// padding (3×2). lipgloss Width excludes the border (it adds 2 on top), so
// the border is NOT subtracted — every content line is fitted/padded to
// this, and the box never resizes.
func trajContentW(winW int) int {
	boxW, _, _ := trajGeom(winW)
	return boxW - 6
}

// trajDetailAt reports whether screen column x is over the DETAIL column.
// Box total = boxW + border 2, centered; content starts after border(1) +
// padding(3); the │ divider sits at leftW+1 inside the content.
func trajDetailAt(winW, x int) bool {
	boxW, leftW, _ := trajGeom(winW)
	contentX := (winW-(boxW+2))/2 + 4
	return x >= contentX+leftW+1
}

// trajDescKind reads the step kind from its desc ("10:02:11 · user").
func trajDescKind(d *Dialog, ri int) string {
	if ri < 0 || ri >= len(d.Descs) {
		return ""
	}
	parts := strings.Split(d.Descs[ri], "·")
	return strings.TrimSpace(parts[len(parts)-1])
}

// trajKindStyle maps a step kind to its highlight color.
func trajKindStyle(kind string) lipgloss.Style {
	switch kind {
	case "user":
		return lipgloss.NewStyle().Foreground(cCyan)
	case "assistant":
		return lipgloss.NewStyle().Bold(true).Foreground(cPlan)
	case "tool":
		return lipgloss.NewStyle().Foreground(cYellow)
	case "bash":
		return lipgloss.NewStyle().Foreground(cGreen)
	default:
		return lipgloss.NewStyle().Foreground(cMuted)
	}
}

// trajColorRow colors the step number dim, the role kind and every [tag]
// span. Each segment carries its own style (never nested) and styles add
// no cells, so a fitted row stays fitted. row must be plain fitted text:
// fitting styled text could cut an ANSI sequence in half.
func trajColorRow(row, kind string) string {
	base := lipgloss.NewStyle().Foreground(cText)
	tagSt := lipgloss.NewStyle().Foreground(cYellow)
	num, rest, ok := strings.Cut(row, " ")
	var b strings.Builder
	if ok {
		b.WriteString(toolStyle.Render(num))
		b.WriteString(base.Render(" "))
	} else {
		rest = row
	}
	kst := trajKindStyle(kind)
	token := kind + ":"
	colored := false
	flush := func(s string) {
		if !colored && kind != "" {
			if i := strings.Index(s, token); i >= 0 {
				b.WriteString(base.Render(s[:i]))
				b.WriteString(kst.Render(token))
				b.WriteString(base.Render(s[i+len(token):]))
				colored = true
				return
			}
		}
		b.WriteString(base.Render(s))
	}
	locs := trajTagRe.FindAllStringIndex(rest, -1)
	prev := 0
	for _, l := range locs {
		flush(rest[prev:l[0]])
		b.WriteString(tagSt.Render(rest[l[0]:l[1]]))
		prev = l[1]
	}
	flush(rest[prev:])
	return b.String()
}

// trajLegend is the color key under the filter line (pre-sized parts, only
// padded — never truncated — so styled text stays intact).
func trajLegend(cw int) string {
	var b strings.Builder
	b.WriteString(toolStyle.Render("legend "))
	b.WriteString(lipgloss.NewStyle().Foreground(cCyan).Render("user"))
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cPlan).Render("assistant"))
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().Foreground(cYellow).Render("[tool]"))
	b.WriteString(" ")
	b.WriteString(lipgloss.NewStyle().Foreground(cGreen).Render("[bash]"))
	return trajPad(b.String(), cw)
}

// trajPad pads s to exactly w cells (ANSI-aware); never truncates, so it
// is safe on styled text — callers pre-fit the plain parts.
func trajPad(s string, w int) string {
	if d := w - lipgloss.Width(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

// trajSelected returns the selected step's detail text ("" when none).
func trajSelected(d *Dialog) string {
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		if ri := d.FIdx[d.Cursor]; ri >= 0 && ri < len(d.Payload) {
			return d.Payload[ri]
		}
	}
	return ""
}

// trajScroll clamps an offset into the scrollable range of lines.
func trajScroll(off, total, win int) int {
	max := total - win
	if max < 0 {
		max = 0
	}
	if off > max {
		off = max
	}
	if off < 0 {
		off = 0
	}
	return off
}

// updateTrajWheel routes the wheel by column: steps move the selection
// (detail offset resets, like ↑↓), detail scrolls 3 lines per notch.
// When the detail has no overflow the wheel is never dead: it moves the
// selection anywhere (otherwise hovering a short detail feels broken).
func (m Model) updateTrajWheel(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd) {
	down := mm.Button == tea.MouseButtonWheelDown
	_, _, rightW := trajGeom(m.winW)
	win := trajWin(m.winH)
	lines := trajDetailLines(trajSelected(d), rightW-4)
	if len(lines) <= win || !trajDetailAt(m.winW, mm.X) {
		t := tea.KeyDown
		if !down {
			t = tea.KeyUp
		}
		nm, cmd := m.updateDialog(tea.KeyMsg{Type: t})
		return nm, cmd
	}
	step := 3
	if down {
		d.TrajOff = trajScroll(d.TrajOff+step, len(lines), win)
	} else {
		d.TrajOff = trajScroll(d.TrajOff-step, len(lines), win)
	}
	m.Refresh()
	return m, nil
}

// trajPage pages the detail column (PgUp/PgDn while the window is open).
func (m Model) trajPage(d *Dialog, down bool) {
	_, _, rightW := trajGeom(m.winW)
	win := trajWin(m.winH)
	lines := trajDetailLines(trajSelected(d), rightW-4)
	if down {
		d.TrajOff = trajScroll(d.TrajOff+win, len(lines), win)
	} else {
		d.TrajOff = trajScroll(d.TrajOff-win, len(lines), win)
	}
	m.Refresh()
}

// renderTrajectoryDialog draws the /trajectory window: left = step names,
// right = the selected step's full detail scrolled by TrajOff. Every
// content line ends exactly cw cells wide, so the box never resizes.
func (m Model) renderTrajectoryDialog(d *Dialog) string {
	return m.renderTraceDialog(d, "trajectory")
}

func (m Model) renderTraceDialog(d *Dialog, mode string) string {
	var b strings.Builder
	boxW, leftW, rightW := trajGeom(m.winW)
	cw := trajContentW(m.winW)
	win := trajWin(m.winH)

	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(d.Title, cw)) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(Fit(d.Message, cw)) + "\n")
	}
	b.WriteString(statusBarStyle.Render(Fit("filter: "+d.Filter+"▌", cw)) + "\n")
	b.WriteString(trajLegend(cw) + "\n")
	b.WriteString("\n")

	// Left window (step names): fixed rows so the box never resizes.
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)
	var leftLines []string
	if above {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d above)", start), leftW-2)))
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		row := ""
		if ri >= 0 && ri < len(d.Options) {
			row = Fit(d.Options[ri], leftW-2)
		} else {
			row = Fit("", leftW-2)
		}
		if fi == d.Cursor {
			// selected: full-width highlight, no inner colors (an inner
			// reset would kill the highlight background past the token)
			leftLines = append(leftLines, statusBarStyle.Render("▸ ")+rowHiStyle.Render(Fit(row, leftW-2)))
		} else {
			leftLines = append(leftLines, statusBarStyle.Render("  ")+trajColorRow(row, trajDescKind(d, ri)))
		}
	}
	if below {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit(fmt.Sprintf("…(+%d below)", total-end), leftW-2)))
	}
	if total == 0 {
		leftLines = append(leftLines, "  "+toolStyle.Render(Fit("— no match —", leftW-2)))
	}
	for len(leftLines) < win {
		leftLines = append(leftLines, "  "+statusBarStyle.Render(Fit("", leftW-2)))
	}

	// Right window (selected step's detail, scrolled by TrajOff).
	kind := ""
	if len(d.FIdx) > 0 && d.Cursor >= 0 && d.Cursor < len(d.FIdx) {
		kind = trajDescKind(d, d.FIdx[d.Cursor])
	}
	rows := trajDetailRows(trajSelected(d), rightW-4)
	off := trajScroll(d.TrajOff, len(rows), win)
	shown := append([]trajRow(nil), rows[off:]...)
	if len(shown) > win {
		shown = shown[:win]
	}
	var rightLines []string
	for _, r := range shown {
		plain := Fit(r.text, rightW-4)
		switch {
		case r.title:
			rightLines = append(rightLines, "  "+trajPad(trajColorRow(plain, kind), rightW-2))
		case r.meta:
			rightLines = append(rightLines, "  "+trajPad(toolStyle.Render(plain), rightW-2))
		default:
			rightLines = append(rightLines, "  "+trajPad(trajDetailRowStyle(plain, r.diff).Render(plain), rightW-2))
		}
	}
	for len(rightLines) < win {
		rightLines = append(rightLines, "  "+trajPad("", rightW-2))
	}

	leftTitle, rightTitle := "STEPS", "DETAIL"
	b.WriteString(trajPad("  "+sideTitleStyle.Render(Fit(leftTitle, leftW-2))+" "+sepStyle.Render("│")+" "+"  "+sideTitleStyle.Render(Fit(rightTitle, rightW-2)), cw) + "\n")
	sep := sepStyle.Render("│")
	for i := 0; i < win; i++ {
		l, r := "", ""
		if i < len(leftLines) {
			l = leftLines[i]
		}
		if i < len(rightLines) {
			r = rightLines[i]
		}
		b.WriteString(trajPad(l+" "+sep+" "+r, cw) + "\n")
	}
	skipped := len(rows) - off - win
	if off > 0 && skipped > 0 {
		b.WriteString(toolStyle.Render(Fit(fmt.Sprintf("…(+%d lines above · +%d below: wheel/PgDn or Enter views full step)", off, skipped), cw)) + "\n")
	} else if off > 0 {
		b.WriteString(toolStyle.Render(Fit(fmt.Sprintf("…(+%d lines above)", off), cw)) + "\n")
	} else if skipped > 0 {
		b.WriteString(toolStyle.Render(Fit(fmt.Sprintf("…(+%d lines below: wheel/PgDn or Enter views full step)", skipped), cw)) + "\n")
	} else {
		b.WriteString(Fit("", cw) + "\n")
	}
	b.WriteString("\n")
	foot := "type to filter · ↑↓ steps · wheel detail · Enter view in chat · Ctrl+Y copy · Esc close"
	if n := len(d.FIdx); n > 0 {
		cur := d.Cursor + 1
		if cur > n {
			cur = n
		}
		foot += fmt.Sprintf(" (%d/%d · %s)", cur, n, d.Scope)
	}
	b.WriteString(toolStyle.Render(Fit(m.dialogFoot(foot), cw)))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}

// trajRow is one wrapped detail line; title/meta mark the payload's first
// two lines (title + "kind · id …" meta) for highlighting.
type trajRow struct {
	text        string
	title, meta bool
	diff        trajDiffKind
}

// trajDetailRows word-wraps detail to w cells (all rows, no cap).
func trajDetailRows(detail string, w int) []trajRow {
	if w < 10 {
		w = 10
	}
	wrap := lipgloss.NewStyle().Width(w)
	var out []trajRow
	orig := 0
	for _, ln := range strings.Split(detail, "\n") {
		mkTitle, mkMeta := orig == 0, orig == 1
		diff := trajDiffKindOf(ln)
		if strings.TrimSpace(ln) == "" {
			out = append(out, trajRow{text: "", diff: diff})
			orig++
			continue
		}
		for _, wln := range strings.Split(wrap.Render(ln), "\n") {
			out = append(out, trajRow{text: wln, title: mkTitle, meta: mkMeta, diff: diff})
			mkTitle, mkMeta = false, false
		}
		orig++
	}
	return out
}

type trajDiffKind uint8

const (
	trajDiffNone trajDiffKind = iota
	trajDiffRemove
	trajDiffAdd
	trajDiffHeader
)

// trajDiffKindOf classifies a source detail line before it is wrapped. This
// keeps continuation rows of a long diff line colored as part of the same
// change instead of reverting to normal text after the first wrap.
func trajDiffKindOf(line string) trajDiffKind {
	switch {
	case strings.HasPrefix(line, "- "), strings.HasPrefix(line, "---"):
		return trajDiffRemove
	case strings.HasPrefix(line, "+ "), strings.HasPrefix(line, "+++"):
		return trajDiffAdd
	case strings.HasPrefix(line, "diff:"), strings.HasPrefix(line, "@@"):
		return trajDiffHeader
	default:
		return trajDiffNone
	}
}

// trajDetailRowStyle gives edit diff rows Pi-like colors: removals are red,
// additions green, and diff/file headers yellow. Ordinary detail text keeps
// the normal foreground color.
func trajDetailRowStyle(line string, kind trajDiffKind) lipgloss.Style {
	if kind == trajDiffNone {
		kind = trajDiffKindOf(line)
	}
	switch kind {
	case trajDiffRemove:
		return lipgloss.NewStyle().Foreground(cRed)
	case trajDiffAdd:
		return lipgloss.NewStyle().Foreground(cGreen)
	case trajDiffHeader:
		return lipgloss.NewStyle().Foreground(cYellow)
	default:
		return lipgloss.NewStyle().Foreground(cText)
	}
}

// trajDetailLines word-wraps detail to w cells (all rows, no cap).
func trajDetailLines(s string, w int) []string {
	rows := trajDetailRows(s, w)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.text)
	}
	return out
}

// trajWrap word-wraps detail to w cells, capped at max rows.
func trajWrap(s string, w, max int) ([]string, int) {
	out := trajDetailLines(s, w)
	if len(out) > max {
		return out[:max], len(out) - max
	}
	return out, 0
}
