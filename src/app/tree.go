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

// treeHeaderLines is the fixed chrome above the row list in the tree
// overlay: separator, title, controls, search, separator. The renderer and
// the mouse hit test both measure from it, so a click can never name a
// different row than the one on screen.
const treeHeaderLines = 5

// treeControlsHint lists only the keys the tree actually binds. Pi's own
// footer advertises branch/labelling/filter chords; pitago implements
// none of them (the copy key is Ctrl+Y, filtering is plain typing), and a
// hint for a key that does nothing is worse than no hint.
const treeControlsHint = "↑↓ move · ←/→ page · pgup/pgdn page · type filters · enter actions · ctrl+y copy · esc close"

// treeWindow is the ONE scroll-window computation shared by the tree
// renderer and its mouse hit test: the first and last visible row, and the
// content line the list starts on.
func (m Model) treeWindow(d *Dialog) (start, end int) {
	win := treePageSize(m.winH)
	total := len(d.FIdx)
	start = 0
	if d.Cursor >= win {
		start = d.Cursor - win + 1
	}
	end = start + win
	if end > total {
		end = total
	}
	if end-start < win {
		start = end - win
	}
	if start < 0 {
		start = 0
	}
	return start, end
}

// treeWidth is the overlay's inner width; the overlay is placed top-left, so
// its content line 0 is terminal row 0.
func treeWidth(winW int) int {
	if winW < 40 {
		return 40
	}
	return winW
}

// renderTreeDialog reproduces Pi's native Session Tree screen: a full-width
// overlay, a compact command hint, a search line, horizontal separators, and
// a flat chronological list. The selected active row is highlighted in place.
func (m Model) renderTreeDialog(d *Dialog) string {
	width := treeWidth(m.winW)
	win := treePageSize(m.winH)
	var b strings.Builder
	line := sepStyle.Render(strings.Repeat("─", width))
	b.WriteString(line + "\n")
	b.WriteString(sideTitleStyle.Render("Session Tree") + "\n")
	// dialogFoot: a Ctrl+Y (or CopyEntryText) confirmation replaces the key
	// hint while it is up, so the copy is visible with the tree still open —
	// otherwise the key looks like it did nothing.
	b.WriteString(statusBarStyle.Render(Fit(m.dialogFoot(treeControlsHint), width)) + "\n")
	b.WriteString(statusBarStyle.Render("Type to search:") + " " + d.Filter + "▌\n")
	b.WriteString(line + "\n")

	start, end := m.treeWindow(d)
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
	if total := len(d.FIdx); total > 0 {
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

// --- tree action menu -------------------------------------------------
//
// Pi's /tree Enter does NOT print the entry into the chat: it closes the
// list and opens a second, tiny selector over it, and Esc goes back to the
// tree with the same row selected (pi 0.87.1 interactive-mode.js
// showTreeSelector). Pi's real follow-up is session.navigateTree(), which
// its RPC mode does not expose (no navigate_tree case in rpc-mode.js), so
// pitago cannot navigate the session: the local action menu is the agreed
// replacement. Everything below is src/app's half of that contract — the
// rows themselves are built by src/builtin.

// treeActionFoot is pi's footer, verbatim: it is the only place the user
// learns Esc backs out to the tree.
const treeActionFoot = "↑↓ navigate · enter select · escape/ctrl+c cancel"

// treeActionGeom derives the box and row geometry from the terminal size.
//
// dlgStyle is Border + Padding(1,3) and lipgloss Width() covers the padding
// but not the border, so the box costs boxW+2 wide and lines+4 high. boxW 62
// is the generic picker's narrow box: a tree entry offers 2-4 actions, and
// the entry line above them must fit on one row.
func treeActionGeom(winW, winH, n int) (boxW, cw, rowW, win int) {
	boxW = 62
	if boxW > winW-2 {
		boxW = winW - 2
	}
	if boxW < 24 {
		boxW = 24
	}
	cw = boxW - 6
	rowW = cw - 2
	if rowW < 8 {
		rowW = 8
	}
	win = winH - 9 // border 2 + padding 2 + title/entry/blank/footer 5
	if win > 4 {
		win = 4
	}
	if win > n {
		win = n
	}
	if win < 1 {
		win = 1
	}
	return boxW, cw, rowW, win
}

// treeActionBoxTop is the content line the first action row sits on, counted
// from the box's top border: border 1 + top padding 1 + title 1 + entry 1 +
// blank 1. The entry line is conditional, so the origin is derived from the
// same condition the renderer used rather than pinned to a constant.
func treeActionBoxTop(d *Dialog) int {
	top := 4 // border + padding + title + blank
	if d.Message != "" {
		top++
	}
	return top
}

// treeActionWindow is the row window of the menu: which of d.FIdx the box
// shows. The renderer and the mouse hit test both read it, so a click can
// never resolve to a different row than the one on screen.
func (m Model) treeActionWindow(d *Dialog) (start, end int) {
	_, _, _, win := treeActionGeom(m.winW, m.winH, len(d.FIdx))
	start = d.Cursor - win + 1
	if start < 0 {
		start = 0
	}
	end = start + win
	if end > len(d.FIdx) {
		end = len(d.FIdx)
	}
	return start, end
}

// treeActionLayout builds the centered box plus the screen origin the mouse
// hit test needs. lipgloss.Place is a no-op once the content overflows the
// placement area, hence the clamps: the origin must match what Place did.
func (m Model) treeActionLayout(d *Dialog) (box string, x0, y0, bh int) {
	boxW, cw, rowW, win := treeActionGeom(m.winW, m.winH, len(d.FIdx))
	start, end := m.treeActionWindow(d)
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Fit(d.Title, cw)) + "\n")
	// The dim line names the entry the actions belong to: the menu floats
	// over the tree, so the row it refers to is not visible behind it.
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(Fit(Short(d.Message, cw), cw)) + "\n")
	}
	b.WriteString("\n")
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		label := ""
		if ri >= 0 && ri < len(d.Options) {
			// rowW-2: every row is "marker (2 cells) + label", so the
			// cursor mark can never widen the box and force a wrap.
			label = Short(d.Options[ri], rowW-2)
		}
		if fi == d.Cursor {
			b.WriteString(rowHiStyle.Width(rowW).Render("→ " + label))
		} else {
			b.WriteString("  " + label)
		}
		b.WriteString("\n")
	}
	for i := end - start; i < win; i++ {
		b.WriteString(strings.Repeat(" ", rowW) + "\n")
	}
	b.WriteString("\n" + toolStyle.Render(Fit(m.dialogFoot(treeActionFoot), cw)))
	box = dlgStyle.Width(boxW).Render(b.String())
	bh = lipgloss.Height(box)
	x0, y0 = 0, 0
	bw := lipgloss.Width(box)
	if m.winW > bw {
		x0 = (m.winW - bw) / 2
	}
	if placeH := maxInt(m.winH-2, 1); placeH > bh {
		y0 = (placeH - bh) / 2
	}
	return box, x0, y0, bh
}

// renderTreeActionDialog draws pi's small centered action menu over the tree.
func (m Model) renderTreeActionDialog(d *Dialog) string {
	box, _, _, _ := m.treeActionLayout(d)
	return lipgloss.Place(m.winW, maxInt(m.winH-2, 1), lipgloss.Center, lipgloss.Center, box)
}

// treeActionRowAt maps a click to an index into d.FIdx, or -1 when the click
// missed the box. A miss must stay a miss: the chat behind a dialog never
// reacts to a click that landed on the menu.
func (m Model) treeActionRowAt(d *Dialog, x, y int) int {
	_, x0, y0, bh := m.treeActionLayout(d)
	// tea mouse coordinates are 1-based, the layout is 0-based.
	ry, rx := y-1-y0, x-1-x0
	if ry < 0 || ry >= bh || rx < 0 {
		return -1
	}
	start, end := m.treeActionWindow(d)
	if rx >= treeActionGeomBoxW(m) {
		return -1
	}
	fi := ry - treeActionBoxTop(d)
	if fi < start || fi >= end {
		return -1
	}
	return fi
}

// treeActionGeomBoxW is the box's outer width, used to reject clicks to its
// left or right (the box is centered, so the columns around it are chat).
func treeActionGeomBoxW(m Model) int {
	boxW, _, _, _ := treeActionGeom(m.winW, m.winH, 0)
	return boxW + 2 // border
}

// clickTreeDialog handles a left click over the tree overlay: it selects the
// row under the cursor and immediately runs it, so one click opens the
// action menu. Clicks off the row list are swallowed, not forwarded.
func (m Model) clickTreeDialog(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if mm.Action != tea.MouseActionPress || mm.Button != tea.MouseButtonLeft {
		return m, nil, false
	}
	if mm.X < 0 || mm.X >= treeWidth(m.winW) {
		return m, nil, true
	}
	start, end := m.treeWindow(d)
	rel := (mm.Y - 1) - treeHeaderLines
	if rel < 0 || rel >= end-start {
		return m, nil, true
	}
	d.Cursor = start + rel
	m.Refresh()
	nm, cmd := m.confirmDialog(d)
	return nm, cmd, true
}

// clickTreeAction handles a left click over the action menu: the first click
// on a row selects it, a second click on the selected row runs it — the same
// two-step pi's keyboard does with ↑↓/Enter.
func (m Model) clickTreeAction(d *Dialog, mm tea.MouseMsg) (tea.Model, tea.Cmd, bool) {
	if mm.Action != tea.MouseActionPress || mm.Button != tea.MouseButtonLeft {
		return m, nil, false
	}
	fi := m.treeActionRowAt(d, mm.X, mm.Y)
	if fi < 0 {
		return m, nil, true // miss: swallowed, never forwarded to the chat
	}
	if d.Cursor != fi {
		d.Cursor = fi
		m.Refresh()
		return m, nil, true
	}
	nm, cmd := m.confirmDialog(d)
	return nm, cmd, true
}

// --- jump to a tree entry's message -----------------------------------
//
// Positional matching is the only bridge available: the transcript is built
// from get_messages, which carries no session entry ids, while get_tree
// entries do. So src/builtin counts the user/assistant messages it saw in
// order and ships the ordinal (Dialog.TreeJump); the app resolves that
// ordinal back to a block index here. It is right whenever both views come
// from the same session, which is the only case /tree can be opened in.

// jumpMark is the dim marker drawn immediately above the block a jump landed
// on. Without it a scrolled transcript gives no evidence of where the jump
// went, since the tree entry itself is not in the chat.
const jumpMark = "▌ jumped here"

// JumpToEntry scrolls the transcript to the ordinal-th user/assistant block
// and marks it. It reports false when there is no such block — including
// before the first paint, when no line table exists; src/builtin uses that
// to fall back to showing the entry detail in the chat.
func (m *Model) JumpToEntry(ordinal int) bool {
	if !m.ready || ordinal < 0 {
		return false
	}
	// Ordinal resolution: count the transcript's user/assistant blocks in
	// order and take the ordinal-th, exactly as the tree rows counted them.
	// A block with no text is not counted: an aborted turn can leave an
	// empty assistant block behind (text_start opens one, the deltas never
	// arrive), and the tree side never numbered it. Counting it here would
	// shift every later ordinal by one and land the jump on the wrong
	// message.
	idx := -1
	n := 0
	for i, bl := range m.blocks {
		if bl.Kind != "user" && bl.Kind != "assistant" {
			continue
		}
		if strings.TrimSpace(bl.Text) == "" {
			continue
		}
		if n == ordinal {
			idx = i
			break
		}
		n++
	}
	if idx < 0 {
		return false
	}
	m.Refresh() // painted transcript ⇒ a block line table
	if len(m.blockLine) != len(m.blocks) {
		m.renderBlocks()
	}
	if idx >= len(m.blockLine) {
		return false
	}
	m.jumpBlock = idx
	m.Refresh() // the mark is in the content, so the line table is final
	// Land the block about a third from the top: the mark sits one line
	// above it and the tool/assistant lines that follow stay readable.
	off := m.blockLine[idx] - m.vp.Height/3
	if off < 0 {
		off = 0
	}
	m.vp.SetYOffset(off) // clamps so the last line stays reachable
	return true
}
