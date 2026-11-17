package app

import tea "pitago/src/tui/ev"

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
