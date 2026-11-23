// Adapted from github.com/charmbracelet/bubbles v1.0.0 viewport (MIT License,
// Copyright (c) 2020-2025 Charmbracelet, Inc; see NOTICE).

package textarea

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	tea "pitago/src/tui/ev"
)

// viewport is the subset of bubbles v1 viewport.Model the textarea drives:
// vertical scrolling over its wrapped lines plus the mouse wheel. The
// textarea always cleared the viewport's KeyMap, so no key handling is
// carried over. Every method keeps the bubbles arithmetic, so View() output
// is byte-identical.
type viewport struct {
	Width   int
	Height  int
	YOffset int

	MouseWheelEnabled bool
	MouseWheelDelta   int

	// Style is always the zero style here; kept so the frame-size math and
	// View's final Render pass match bubbles exactly.
	Style lipgloss.Style

	xOffset          int
	lines            []string
	longestLineWidth int
}

func newViewport(width, height int) viewport {
	return viewport{Width: width, Height: height, MouseWheelEnabled: true, MouseWheelDelta: 3}
}

func (m viewport) AtTop() bool { return m.YOffset <= 0 }

func (m viewport) AtBottom() bool { return m.YOffset >= m.maxYOffset() }

// SetContent sets the pager's text content.
func (m *viewport) SetContent(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n") // normalize line endings
	m.lines = strings.Split(s, "\n")
	m.longestLineWidth = findLongestLineWidth(m.lines)

	if m.YOffset > len(m.lines)-1 {
		m.GotoBottom()
	}
}

func (m viewport) maxYOffset() int {
	return max(0, len(m.lines)-m.Height+m.Style.GetVerticalFrameSize())
}

func (m viewport) visibleLines() (lines []string) {
	h := m.Height - m.Style.GetVerticalFrameSize()
	w := m.Width - m.Style.GetHorizontalFrameSize()

	if len(m.lines) > 0 {
		top := max(0, m.YOffset)
		bottom := clamp(m.YOffset+h, top, len(m.lines))
		lines = m.lines[top:bottom]
	}

	if (m.xOffset == 0 && m.longestLineWidth <= w) || w == 0 {
		return lines
	}

	cutLines := make([]string, len(lines))
	for i := range lines {
		cutLines[i] = ansi.Cut(lines[i], m.xOffset, m.xOffset+w)
	}
	return cutLines
}

func (m *viewport) SetYOffset(n int) {
	m.YOffset = clamp(n, 0, m.maxYOffset())
}

func (m *viewport) ScrollDown(n int) {
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return
	}
	m.SetYOffset(m.YOffset + n)
}

func (m *viewport) ScrollUp(n int) {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return
	}
	m.SetYOffset(m.YOffset - n)
}

func (m *viewport) GotoTop() {
	if m.AtTop() {
		return
	}
	m.SetYOffset(0)
}

func (m *viewport) GotoBottom() {
	m.SetYOffset(m.maxYOffset())
}

// Update handles the mouse wheel (the only input the textarea's viewport
// reacted to: its KeyMap was empty). Horizontal scrolling was disabled
// (horizontalStep 0), so shift+wheel and horizontal wheels are no-ops.
func (m viewport) Update(msg tea.Msg) (viewport, tea.Cmd) {
	mm, ok := msg.(tea.MouseMsg)
	if !ok || !m.MouseWheelEnabled || mm.Action != tea.MouseActionPress || mm.Shift {
		return m, nil
	}
	switch mm.Button { //nolint:exhaustive
	case tea.MouseButtonWheelUp:
		m.ScrollUp(m.MouseWheelDelta)
	case tea.MouseButtonWheelDown:
		m.ScrollDown(m.MouseWheelDelta)
	}
	return m, nil
}

// View renders the viewport into a string.
func (m viewport) View() string {
	w, h := m.Width, m.Height
	if sw := m.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := m.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}
	contentWidth := w - m.Style.GetHorizontalFrameSize()
	contentHeight := h - m.Style.GetVerticalFrameSize()
	contents := lipgloss.NewStyle().
		Width(contentWidth).      // pad to width.
		Height(contentHeight).    // pad to height.
		MaxHeight(contentHeight). // truncate height if taller.
		MaxWidth(contentWidth).   // truncate width if wider.
		Render(strings.Join(m.visibleLines(), "\n"))
	return m.Style.
		UnsetWidth().UnsetHeight(). // Style size already applied in contents.
		Render(contents)
}

func findLongestLineWidth(lines []string) int {
	w := 0
	for _, l := range lines {
		if ww := ansi.StringWidth(l); ww > w {
			w = ww
		}
	}
	return w
}
