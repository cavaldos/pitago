package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/image"
	"pitago/src/components/mention"
)

// @ file mentions (pi parity) -------------------------------------------------
//
// Lookup lives in components/mention (pure, no TUI state); this file keeps
// the popup wiring on Model: refresh/complete/navigate/render.
//
// Pi's TUI completes @path with a fuzzy file finder (fd, respects
// .gitignore) and sends the raw "@path" text to the agent — the model reads
// the file with its tools. Only CLI startup @args are expanded locally into
// <file> blocks. So pitago only needs the autocomplete half: typing @ opens
// this popup, Tab/Enter completes, the prompt text reaches pi untouched.
//

// cursorPos returns the textarea cursor as (row, rune-col), clamped.
func (m *Model) cursorPos() (row, col int) {
	row = m.ta.Line()
	li := m.ta.LineInfo()
	col = li.StartColumn + li.ColumnOffset
	lines := strings.Split(m.ta.Value(), "\n")
	if row < 0 {
		row = 0
	}
	if row >= len(lines) {
		row = len(lines) - 1
	}
	if row < 0 {
		return 0, 0
	}
	if n := len([]rune(lines[row])); col < 0 {
		col = 0
	} else if col > n {
		col = n
	}
	return row, col
}

// atCandidates lists suggestions for the raw text after @ under m.cwd.
func (m *Model) atCandidates(raw string, quoted bool) []mention.Item {
	return mention.Candidates(m.cwd, raw, quoted)
}

// refreshAt recomputes the @ popup from the token before the cursor.
func (m *Model) refreshAt() {
	// Shell mode owns the editor: "@" is a shell word (or a GitHub
	// shorthand), not a file mention, so the popup must never open — see
	// cmdPrefix for the "/" twin.
	if m.shellOn {
		m.closeAt()
		return
	}
	row, col := m.cursorPos()
	lines := strings.Split(m.ta.Value(), "\n")
	var line []rune
	if row >= 0 && row < len(lines) {
		line = []rune(lines[row])
	}
	prefix, start, ok := mention.Token(line, col)
	if !ok {
		m.closeAt()
		return
	}
	raw := strings.TrimPrefix(prefix, "@")
	quoted := false
	if strings.HasPrefix(raw, "\"") {
		quoted = true
		raw = raw[1:]
	}
	items := m.atCandidates(raw, quoted)
	if len(items) == 0 {
		m.closeAt()
		return
	}
	if prefix != m.atPrefix {
		m.atCursor, m.atOffset = 0, 0
	}
	m.atOpen = true
	m.atPrefix = prefix
	m.atStart = start
	m.atRow = row
	m.atItems = items
	if m.atCursor >= len(items) {
		m.atCursor = 0
	}
	m.ensureAtVisible()
	m.applyPopupH()
}

// closeAt hides the @ popup (no-op when already closed).
func (m *Model) closeAt() {
	if m.atOpen {
		m.atOpen = false
		m.applyPopupH()
	}
}

// ensureAtVisible keeps atCursor inside the scroll window.
func (m *Model) ensureAtVisible() {
	if m.atCursor < m.atOffset {
		m.atOffset = m.atCursor
	}
	if m.atCursor >= m.atOffset+mention.Win {
		m.atOffset = m.atCursor - mention.Win + 1
	}
	if m.atOffset < 0 {
		m.atOffset = 0
	}
}

// handleAtKey handles keys while the @ popup is open. true = consumed.
func (m *Model) handleAtKey(km tea.KeyMsg) bool {
	switch km.Type {
	case tea.KeyUp:
		if m.atCursor > 0 {
			m.atCursor--
		} else {
			m.atCursor = len(m.atItems) - 1
		}
		m.ensureAtVisible()
		return true
	case tea.KeyDown:
		if m.atCursor < len(m.atItems)-1 {
			m.atCursor++
		} else {
			m.atCursor = 0
		}
		m.ensureAtVisible()
		return true
	case tea.KeyTab:
		m.completeAt()
		return true
	case tea.KeyEnter:
		m.completeAt() // complete, don't send — Enter again sends
		return true
	case tea.KeyEsc:
		m.closeAt()
		m.Refresh()
		return true
	}
	return false
}

// completeAt replaces the @ token with the selected path. Dirs keep the
// popup open (no trailing space) so completion continues, like pi.
func (m *Model) completeAt() {
	if !m.atOpen || len(m.atItems) == 0 {
		return
	}
	if m.atCursor < 0 || m.atCursor >= len(m.atItems) {
		m.atCursor = 0
	}
	it := m.atItems[m.atCursor]
	// Tab-completed @image → tray chip, not long path text (the typed
	// @token is stripped so send doesn't deliver it twice).
	if ref := image.Dequote(strings.TrimPrefix(it.Value, "@")); !it.Dir && image.ExistsImage(m.cwd, ref) {
		row, col := m.cursorPos()
		if row != m.atRow {
			m.refreshAt() // cursor wandered off; resync, don't corrupt text
			return
		}
		lines := strings.Split(m.ta.Value(), "\n")
		if row >= 0 && row < len(lines) {
			line := []rune(lines[row])
			if m.atStart <= len(line) && col <= len(line) && m.atStart <= col {
				rest := strings.TrimPrefix(string(line[col:]), " ")
				lines[row] = string(line[:m.atStart]) + rest
				abs := m.atStart
				for i := 0; i < row; i++ {
					abs += len([]rune(lines[i])) + 1
				}
				m.setValueAt(strings.Join(lines, "\n"), abs)
			}
		}
		m.attachPaths([]string{ref})
		m.closeAt()
		m.Refresh()
		return
	}
	lines := strings.Split(m.ta.Value(), "\n")
	row, col := m.cursorPos()
	if row != m.atRow || row < 0 || row >= len(lines) {
		m.refreshAt() // cursor wandered off; resync, don't corrupt text
		return
	}
	line := []rune(lines[row])
	if m.atStart > len(line) || col < m.atStart || col > len(line) {
		m.refreshAt()
		return
	}
	ins := it.Value
	if !it.Dir {
		ins += " "
	}
	lines[row] = string(line[:m.atStart]) + ins + string(line[col:])
	m.ta.SetValue(strings.Join(lines, "\n"))
	if row == len(lines)-1 {
		m.ta.SetCursor(m.atStart + len([]rune(ins)))
	}
	m.refreshAt()
	m.Refresh()
}

func (m *Model) atPopupH() int {
	if !m.atOpen {
		return 0
	}
	n := len(m.atItems)
	if n > mention.Win {
		n = mention.Win
	}
	extra := 0
	if m.atOffset > 0 {
		extra++
	}
	if m.atOffset+mention.Win < len(m.atItems) {
		extra++
	}
	return n + extra + 3 // rows + hints + footer + border
}

func (m Model) renderAtPopup() string {
	mainW := m.mainW()
	var b strings.Builder
	end := m.atOffset + mention.Win
	if end > len(m.atItems) {
		end = len(m.atItems)
	}
	if m.atOffset > 0 {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d above)", m.atOffset)) + "\n")
	}
	for i := m.atOffset; i < end; i++ {
		it := m.atItems[i]
		row := it.Label
		if it.Desc != "" && it.Desc != it.Label {
			row += " — " + it.Desc
		}
		row = Short(row, mainW-8)
		if i == m.atCursor {
			b.WriteString("▸ " + cmdHiStyle.Render(row) + "\n")
		} else {
			b.WriteString("  " + statusBarStyle.Render(row) + "\n")
		}
	}
	if end < len(m.atItems) {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d below)", len(m.atItems)-end)) + "\n")
	}
	b.WriteString(toolStyle.Render(fmt.Sprintf("(%d/%d) Tab complete · Enter complete · Esc close", m.atCursor+1, len(m.atItems))))
	return cmdPopStyle.Width(mainW).Render(strings.TrimRight(b.String(), "\n"))
}
