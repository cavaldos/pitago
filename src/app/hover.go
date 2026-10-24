package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// sideClickableAt reports whether (x, y) is a sidebar row that reacts to a
// click. It reuses the click hit-tests, so hover can never light up a row
// that a click would ignore.
func (m Model) sideClickableAt(x, y int) bool {
	if !m.ready || !m.Mouse {
		return false
	}
	if _, ok := m.recentAt(x, y); ok {
		return true
	}
	return m.pluginToggleAt(x, y) || m.sideFoldAt(x, y) != "" ||
		m.subagentsMoreAt(x, y) || m.subagentRowAt(x, y) != ""
}

// chatClickableAt returns the tool/thinking block under (x, y), the blocks a
// plain click folds/unfolds, or -1. It reads m.vp directly (not the full
// chatViewport geometry) because it runs per motion report; the chat
// viewport always starts at screen row 1.
func (m Model) chatClickableAt(x, y int) int {
	if !m.ready || !m.Mouse || len(m.Dialogs) > 0 || m.overSide(x) ||
		y < 1 || y > m.vp.Height {
		return -1
	}
	abs := m.vp.YOffset + y - 1
	if strings.TrimSpace(m.chatLine(abs)) == "" {
		return -1
	}
	idx := -1
	for i, start := range m.blockRows {
		if start > abs {
			break
		}
		idx = i
	}
	if idx < 0 || idx >= len(m.blocks) {
		return -1
	}
	if k := m.blocks[idx].Kind; k != "tool" && k != "thinking" {
		return -1
	}
	return idx
}

// hoverChanged reports whether a motion report at (x, y) changes what the
// hover highlight shows, so the filter forwards only those.
func (m Model) hoverChanged(x, y int) bool {
	on := m.sideClickableAt(x, y)
	if on != m.hoverOn || (on && y != m.hoverY) {
		return true
	}
	return m.chatClickableAt(x, y)+1 != m.hoverBlock
}

// setHover records the pointer's clickable row or block (or clears them).
func (m *Model) setHover(msg tea.MouseMsg) {
	m.hoverOn = m.sideClickableAt(msg.X, msg.Y)
	m.hoverY = msg.Y
	m.hoverBlock = m.chatClickableAt(msg.X, msg.Y) + 1
}

// brighten wraps a rendered line in bold, re-asserting it after every reset
// the line already carries. Bold also cancels faint (SGR 22), so dimmed
// thinking text lights up too.
func brighten(ln string) string {
	const on = "\x1b[22;1m"
	ln = strings.ReplaceAll(ln, "\x1b[0m", "\x1b[0m"+on)
	ln = strings.ReplaceAll(ln, "\x1b[m", "\x1b[m"+on)
	return on + ln + "\x1b[0m"
}

// applyChatHover brightens the visible rows of the hovered tool/thinking
// block. view is the chat viewport slice starting at content line yoff.
func (m Model) applyChatHover(view string, yoff int) string {
	b := m.hoverBlock - 1
	if b < 0 || b >= len(m.blockRows) || b >= len(m.blocks) || m.sel.Active {
		return view
	}
	start, end := m.blockRows[b], 1<<30
	if b+1 < len(m.blockRows) {
		end = m.blockRows[b+1]
	}
	lines := strings.Split(view, "\n")
	for i := range lines {
		if abs := yoff + i; abs >= start && abs < end && strings.TrimSpace(stripANSI(lines[i])) != "" {
			lines[i] = brighten(lines[i])
		}
	}
	return strings.Join(lines, "\n")
}

// hoverStyle brightens the hovered row: bold accent text, so the row reads
// as pressable without a backdrop.
var hoverStyle = func() lipgloss.Style {
	return lipgloss.NewStyle().Bold(true).Foreground(cAccent)
}

// applyHover restyles the sidebar viewport line under the pointer.
func (m Model) applyHover(body string) string {
	if !m.hoverOn {
		return body
	}
	lines := strings.Split(body, "\n")
	i := m.hoverY - 1
	if i < 0 || i >= len(lines) {
		return body
	}
	plain := strings.TrimRight(stripANSI(lines[i]), " ")
	if strings.TrimSpace(plain) == "" {
		return body
	}
	lines[i] = hoverStyle().Render(plain)
	return strings.Join(lines, "\n")
}
