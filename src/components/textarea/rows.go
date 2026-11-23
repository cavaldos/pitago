package textarea

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/rivo/uniseg"

	"pitago/src/tui/ev"
)

// VisibleRows presents the editor's existing word wrap and caret to the native
// tview widget. It uses the same offset as SetCursorAt, so clicking a glyph
// after a wide rune or on a scrolled continuation still moves to that glyph.
func (m Model) VisibleRows() []ev.EditorRow {
	rows := make([]ev.EditorRow, m.height)
	if m.Value() == "" && m.Placeholder != "" {
		wrap := ansi.Hardwrap(ansi.Wordwrap(m.Placeholder, m.width, ""), m.width, true)
		for i, line := range strings.Split(strings.TrimSpace(wrap), "\n") {
			if i >= len(rows) {
				break
			}
			rows[i].Text, rows[i].Placeholder = line, true
		}
		m.viewport.YOffset = 0
		for i := range rows {
			rows[i].Prompt, rows[i].Caret = m.getPromptString(i), -1
		}
		if len(rows) > 0 && m.focus {
			rows[0].Caret = 0
		}
		return rows
	}
	info := m.LineInfo()
	caret := m.cursorLineNumber()
	offset := clamp(m.viewport.YOffset, max(0, caret-m.height+1), caret)
	m.viewport.YOffset = offset
	for i := range rows {
		rows[i].Prompt, rows[i].Caret = m.getPromptString(i+offset), -1
	}
	visual := 0
	for i, line := range m.value {
		for j, segment := range m.memoizedWrap(line, m.width) {
			r := visual - offset
			if r >= len(rows) {
				return rows
			}
			if r >= 0 {
				row := &rows[r]
				if m.ShowLineNumbers {
					if j == 0 {
						row.Prompt += m.formatLineNumber(i + 1)
					} else {
						row.Prompt += m.formatLineNumber(" ")
					}
				}
				row.Text = string(segment)
				if uniseg.StringWidth(row.Text) > m.width {
					row.Text = strings.TrimSuffix(row.Text, " ")
				}
				if r+offset == caret && m.focus {
					row.Caret = min(info.CharOffset, m.width)
				}
			}
			visual++
		}
	}
	return rows
}
