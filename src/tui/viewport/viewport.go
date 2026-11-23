// Package viewport is a value-type scrolling text view over pitago's own
// event types (src/tui/ev).
//
// It is a port of github.com/charmbracelet/bubbles v1.0.0 viewport (MIT
// License, Copyright (c) 2020-2025 Charmbracelet, Inc), reduced to what
// src/app uses and with identical behaviour: same scroll arithmetic, same
// default key bindings, same wheel delta and the same View() output.
package viewport

import (
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"pitago/src/tui/ev"
)

// New returns a viewport of the given size.
func New(width, height int) (m Model) {
	m.Width = width
	m.Height = height
	m.setInitialValues()
	return m
}

// Model is the viewport state.
type Model struct {
	Width  int
	Height int

	MouseWheelEnabled bool
	MouseWheelDelta   int

	// YOffset is the vertical scroll position.
	YOffset int
	xOffset int

	horizontalStep int

	// Style is applied around the content (frame sizes reduce the content).
	Style lipgloss.Style

	initialized      bool
	lines            []string
	longestLineWidth int
}

func (m *Model) setInitialValues() {
	m.MouseWheelEnabled = true
	m.MouseWheelDelta = 3
	m.initialized = true
}

// AtTop reports whether the view is scrolled to the top.
func (m Model) AtTop() bool { return m.YOffset <= 0 }

// AtBottom reports whether the view is scrolled to (or past) the bottom.
func (m Model) AtBottom() bool { return m.YOffset >= m.maxYOffset() }

// PastBottom reports whether the view is scrolled past the bottom.
func (m Model) PastBottom() bool { return m.YOffset > m.maxYOffset() }

// ScrollPercent is the vertical scroll position in [0,1].
func (m Model) ScrollPercent() float64 {
	if m.Height >= len(m.lines) {
		return 1.0
	}
	y := float64(m.YOffset)
	h := float64(m.Height)
	t := float64(len(m.lines))
	v := y / (t - h)
	return math.Max(0.0, math.Min(1.0, v))
}

// SetContent replaces the content. It only re-pins to the bottom when the
// current offset overruns the new line count.
func (m *Model) SetContent(s string) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	m.lines = strings.Split(s, "\n")
	m.longestLineWidth = findLongestLineWidth(m.lines)
	if m.YOffset > len(m.lines)-1 {
		m.GotoBottom()
	}
}

func (m Model) maxYOffset() int {
	return max(0, len(m.lines)-m.Height+m.Style.GetVerticalFrameSize())
}

// VisibleLines returns the clipped viewport text, without frame or padding.
// The returned slice is read-only; it may share the model's content buffer.
func (m Model) VisibleLines() []string { return m.visibleLines() }

func (m Model) visibleLines() (lines []string) {
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
	cut := make([]string, len(lines))
	for i := range lines {
		cut[i] = ansi.Cut(lines[i], m.xOffset, m.xOffset+w)
	}
	return cut
}

// SetYOffset sets the offset, clamped to the content.
func (m *Model) SetYOffset(n int) { m.YOffset = clamp(n, 0, m.maxYOffset()) }

// PageDown scrolls one page down.
func (m *Model) PageDown() []string {
	if m.AtBottom() {
		return nil
	}
	return m.ScrollDown(m.Height)
}

// PageUp scrolls one page up.
func (m *Model) PageUp() []string {
	if m.AtTop() {
		return nil
	}
	return m.ScrollUp(m.Height)
}

// HalfPageDown scrolls half a page down.
func (m *Model) HalfPageDown() []string {
	if m.AtBottom() {
		return nil
	}
	return m.ScrollDown(m.Height / 2)
}

// HalfPageUp scrolls half a page up.
func (m *Model) HalfPageUp() []string {
	if m.AtTop() {
		return nil
	}
	return m.ScrollUp(m.Height / 2)
}

// LineDown scrolls n lines down.
func (m *Model) LineDown(n int) []string { return m.ScrollDown(n) }

// ScrollDown scrolls n lines down and returns the lines that came into view.
func (m *Model) ScrollDown(n int) []string {
	if m.AtBottom() || n == 0 || len(m.lines) == 0 {
		return nil
	}
	m.SetYOffset(m.YOffset + n)
	bottom := clamp(m.YOffset+m.Height, 0, len(m.lines))
	top := clamp(m.YOffset+m.Height-n, 0, bottom)
	return m.lines[top:bottom]
}

// LineUp scrolls n lines up.
func (m *Model) LineUp(n int) []string { return m.ScrollUp(n) }

// ScrollUp scrolls n lines up and returns the lines that came into view.
func (m *Model) ScrollUp(n int) []string {
	if m.AtTop() || n == 0 || len(m.lines) == 0 {
		return nil
	}
	m.SetYOffset(m.YOffset - n)
	top := max(0, m.YOffset)
	bottom := clamp(m.YOffset+n, 0, m.maxYOffset())
	return m.lines[top:bottom]
}

// SetHorizontalStep sets the column step for left/right scrolling.
func (m *Model) SetHorizontalStep(n int) { m.horizontalStep = max(n, 0) }

// SetXOffset sets the horizontal offset, clamped to the content.
func (m *Model) SetXOffset(n int) { m.xOffset = clamp(n, 0, m.longestLineWidth-m.Width) }

// ScrollLeft scrolls n columns left.
func (m *Model) ScrollLeft(n int) { m.SetXOffset(m.xOffset - n) }

// ScrollRight scrolls n columns right.
func (m *Model) ScrollRight(n int) { m.SetXOffset(m.xOffset + n) }

// TotalLineCount is the number of content lines.
func (m Model) TotalLineCount() int { return len(m.lines) }

// VisibleLineCount is the number of lines currently shown.
func (m Model) VisibleLineCount() int { return len(m.visibleLines()) }

// GotoTop scrolls to the top.
func (m *Model) GotoTop() []string {
	if m.AtTop() {
		return nil
	}
	m.SetYOffset(0)
	return m.visibleLines()
}

// GotoBottom scrolls to the bottom.
func (m *Model) GotoBottom() []string {
	m.SetYOffset(m.maxYOffset())
	return m.visibleLines()
}

// Update handles the default key bindings and the mouse wheel.
func (m Model) Update(msg ev.Msg) (Model, ev.Cmd) {
	if !m.initialized {
		m.setInitialValues()
	}
	switch msg := msg.(type) {
	case ev.KeyMsg:
		switch msg.String() {
		case "pgdown", " ", "f":
			m.PageDown()
		case "pgup", "b":
			m.PageUp()
		case "u", "ctrl+u":
			m.HalfPageUp()
		case "d", "ctrl+d":
			m.HalfPageDown()
		case "down", "j":
			m.ScrollDown(1)
		case "up", "k":
			m.ScrollUp(1)
		case "left", "h":
			m.ScrollLeft(m.horizontalStep)
		case "right", "l":
			m.ScrollRight(m.horizontalStep)
		}
	case ev.MouseMsg:
		if !m.MouseWheelEnabled || msg.Action != ev.MouseActionPress {
			break
		}
		switch msg.Button {
		case ev.MouseButtonWheelUp:
			if msg.Shift {
				m.ScrollLeft(m.horizontalStep)
			} else {
				m.ScrollUp(m.MouseWheelDelta)
			}
		case ev.MouseButtonWheelDown:
			if msg.Shift {
				m.ScrollRight(m.horizontalStep)
			} else {
				m.ScrollDown(m.MouseWheelDelta)
			}
		case ev.MouseButtonWheelLeft:
			m.ScrollLeft(m.horizontalStep)
		case ev.MouseButtonWheelRight:
			m.ScrollRight(m.horizontalStep)
		}
	}
	return m, nil
}

// View renders the visible lines padded/truncated to Width×Height.
func (m Model) View() string {
	w, h := m.Width, m.Height
	if sw := m.Style.GetWidth(); sw != 0 {
		w = min(w, sw)
	}
	if sh := m.Style.GetHeight(); sh != 0 {
		h = min(h, sh)
	}
	cw := w - m.Style.GetHorizontalFrameSize()
	ch := h - m.Style.GetVerticalFrameSize()
	contents := lipgloss.NewStyle().
		Width(cw).
		Height(ch).
		MaxHeight(ch).
		MaxWidth(cw).
		Render(strings.Join(m.visibleLines(), "\n"))
	return m.Style.UnsetWidth().UnsetHeight().Render(contents)
}

func clamp(v, low, high int) int {
	if high < low {
		low, high = high, low
	}
	return min(high, max(low, v))
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
