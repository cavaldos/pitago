package app

import (
	"time"

	tea "pitago/src/tui/ev"
)

// onSideDivider reports whether screen x,y is on the sidebar's left border,
// the column the user grabs to resize it. Row 0 is the header line, which
// spans the whole width, so the divider starts at row 1.
func (m Model) onSideDivider(x, y int) bool {
	return m.Mouse && m.showSide() && len(m.Dialogs) == 0 && x == m.mainW() && y >= 1
}

// updateSideDrag runs the sidebar resize gesture. It returns handled=true
// for every report that belongs to the gesture.
func (m Model) updateSideDrag(msg tea.MouseMsg) (Model, bool) {
	if !m.sideDrag {
		if msg.Action == tea.MouseActionPress && msg.Button == tea.MouseButtonLeft && m.onSideDivider(msg.X, msg.Y) {
			// Double-click on the divider restores the default width.
			if time.Since(m.sideDividerPress) < doubleClickWindow {
				m.sideDividerPress = time.Time{}
				m.ResetSideWidth()
				return m, true
			}
			m.sideDividerPress = time.Now()
			m.sideDrag = true
			return m, true
		}
		return m, false
	}
	switch msg.Action {
	case tea.MouseActionMotion:
		m.setSideWidth(m.winW-msg.X, false)
	case tea.MouseActionRelease:
		m.sideDrag = false
		m.setSideWidth(m.winW-msg.X, true)
	default:
		// A wheel or another button mid-drag: tview reports a wheel during
		// a held drag as release+press, so only a release ends the gesture.
	}
	return m, true
}

// setSideWidth applies a new sidebar width (clamped) and reflows the chat,
// input and sidebar like a terminal resize. persist writes it to prefs.
func (m *Model) setSideWidth(w int, persist bool) {
	w = clampSideW(w, m.winW)
	if w != m.sideW() || m.SideWidth == 0 {
		m.SideWidth = w
		m.relayoutWidths()
	}
	if persist && m.prefsPath != "" {
		p := LoadPrefs(m.prefsPath)
		p.SideWidth = m.SideWidth
		_ = SavePrefs(m.prefsPath, p)
	}
}

// ResetSideWidth restores the default sidebar width (and forgets the pref).
func (m *Model) ResetSideWidth() {
	m.SideWidth = 0
	m.relayoutWidths()
	if m.prefsPath != "" {
		p := LoadPrefs(m.prefsPath)
		p.SideWidth = 0
		_ = SavePrefs(m.prefsPath, p)
	}
}

// relayoutWidths is the width half of the WindowSize handler.
func (m *Model) relayoutWidths() {
	if !m.ready {
		return
	}
	w := m.mainW()
	m.vp.Width = w
	m.sideVp.Width = m.sideInnerW()
	m.ta.SetWidth(w - 6)
	m.Refresh()
}

// Dividers implements ev.DividerProvider: the sidebar's left border, so the
// tview runtime can highlight it under the pointer and while dragging.
func (m Model) Dividers() []tea.Divider {
	if !m.Mouse || !m.showSide() || len(m.Dialogs) > 0 || m.winH <= 1 {
		return nil
	}
	return []tea.Divider{{ID: "sidebar", X: m.mainW(), Y: 1, H: m.winH - 1}}
}

// Tooltip implements ev.TooltipProvider (tview runtime): hints for the
// sidebar divider and the input's status line.
func (m Model) Tooltip(x, y int) (string, tea.Rect, string) {
	if !m.ready || len(m.Dialogs) > 0 {
		return "", tea.Rect{}, ""
	}
	if m.onSideDivider(x, y) {
		return "divider", tea.Rect{X: x, Y: y, W: 1, H: 1}, "Drag to resize the sidebar · double-click to reset"
	}
	if NativeMenus && y == 0 && x < m.mainW() {
		return "header", tea.Rect{X: 0, Y: 0, W: m.mainW(), H: 1}, "Click for session info · right-click for more"
	}
	if m.inputAt(x, y) && y == m.inputFooterY() {
		return "stats", tea.Rect{X: 0, Y: y, W: m.mainW(), H: 1}, m.statsTooltip()
	}
	return "", tea.Rect{}, ""
}

// inputFooterY is the row of the input box's status line (just above its
// bottom border).
func (m Model) inputFooterY() int {
	for y := m.winH - 1; y >= 0; y-- {
		if m.inputAt(0, y) {
			return y - 1
		}
	}
	return -1
}

func (m Model) statsTooltip() string {
	s := "Model: " + m.ModelLbl
	if m.thinkLvl != "" {
		s += "\nThinking: " + m.thinkLvl
	}
	if m.Stats.ContextPct > 0 {
		s += "\nContext: " + FmtNum(m.Stats.TokensTotal) + " tokens"
	}
	return s + "\nClick to change model · right-click for thinking and compact"
}

// barClick makes the header and the input's status line clickable (only
// where the runtime draws native menus, i.e. tview). Acts on release so a
// press that turns into a drag never fires.
func (m *Model) barClick(msg tea.MouseMsg) (tea.Cmd, bool) {
	if !NativeMenus || !m.Mouse || !m.ready || len(m.Dialogs) > 0 || m.sel.Active ||
		msg.Action != tea.MouseActionRelease {
		return nil, false
	}
	switch {
	case msg.Y == 0 && msg.X < m.mainW():
		return m.runSlash("/name"), true
	case m.inputAt(msg.X, msg.Y) && msg.Y == m.inputFooterY():
		return m.runSlash("/model"), true
	}
	return nil, false
}
