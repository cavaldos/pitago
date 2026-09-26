package app

import (
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/rivo/uniseg"
)

// Point is an absolute chat-content coordinate: Line is 0-based within the
// rendered chat content; Col is a 0-based terminal cell column.
type Point struct {
	Line int
	Col  int
}

// Selection is the pure state of a chat-column drag selection.
type Selection struct {
	Active      bool
	Anchor      Point
	Focus       Point
	HadDrag     bool
	DoubleClick bool
}

const doubleClickWindow = 450 * time.Millisecond

// selectionTickMsg drives continuous edge auto-scroll while a drag is held.
type selectionTickMsg struct{}

// mousePoint maps a terminal row to an absolute chat-content point. The
// viewport begins on screen row 1 (row 0 is the header). x is clamped to the
// chat column, never the sidebar.
func (m Model) mousePoint(x, y int) Point {
	w := m.mainW()
	if w < 1 {
		w = 1
	}
	col := x
	if col < 0 {
		col = 0
	}
	if col >= w {
		col = w - 1
	}
	h := m.vp.Height
	if h < 1 {
		h = 1
	}
	row := y - 1
	if row < 0 {
		row = 0
	}
	if row >= h {
		row = h - 1
	}
	line := m.vp.YOffset + row
	// The gutter (2 cells) is not content: a press on it, or a drag that
	// crosses it, must not include the glyph in the selection.
	if g := m.gutterFor(line); col < g {
		col = g
	}
	return Point{Line: line, Col: col}
}

// gutterFor reports the leading gutter width for a rendered chat line (0
// or 2), so selection can clamp past it. Lines before the first block and
// out-of-range lines have no gutter.
func (m Model) gutterFor(line int) int {
	if line < 0 || line >= len(m.gutterCols) {
		return 0
	}
	return m.gutterCols[line]
}

// updateSelection handles left press/motion/release in the chat column.
// Returns handled=true when the event was consumed by the selector.
func (m Model) updateSelection(msg tea.MouseMsg) (Model, tea.Cmd, bool) {
	if !m.Mouse || len(m.Dialogs) > 0 {
		return m, nil, false
	}
	now := time.Now()
	p := m.mousePoint(msg.X, msg.Y)
	switch msg.Action {
	case tea.MouseActionPress:
		// New selection presses must start inside the chat viewport. Once
		// dragging, motion/release continue to be handled even if the pointer
		// crosses into the sidebar, so the release cannot activate a sidebar
		// control while completing a chat selection.
		if msg.Button != tea.MouseButtonLeft || m.overSide(msg.X) || msg.Y < 1 || msg.Y >= m.vp.Height {
			return m, nil, false
		}
		m.sel = Selection{Active: true, Anchor: p, Focus: p}
		if now.Sub(m.LastPressAt) < doubleClickWindow && m.LastPressLine == p.Line {
			// Double-click selects the complete rendered line, past the
			// gutter: the glyph is chrome, not content.
			line := m.chatLine(p.Line)
			m.sel.Anchor = Point{Line: p.Line, Col: m.gutterFor(p.Line)}
			m.sel.Focus = Point{Line: p.Line, Col: visibleWidth(line)}
			m.sel.DoubleClick = true
			m.sel.HadDrag = true
			m.LastPressAt = time.Time{}
		} else {
			m.LastPressAt = now
			m.LastPressLine = p.Line
		}
		return m, nil, true
	case tea.MouseActionMotion:
		if !m.sel.Active {
			return m, nil, false
		}
		if p != m.sel.Focus {
			m.sel.HadDrag = true
		}
		if !m.sel.DoubleClick {
			m.sel.Focus = p
		}
		return m, m.edgeScrollCmd(p), true
	case tea.MouseActionRelease:
		if !m.sel.Active {
			return m, nil, false
		}
		if !m.sel.DoubleClick {
			m.sel.Focus = p
		}
		if m.sel.HadDrag {
			text := selectionText(m.chatLines, m.gutterCols, m.sel.Anchor, m.sel.Focus)
			if strings.TrimSpace(text) != "" {
				m.YankText(text)
			}
		}
		m.sel = Selection{}
		return m, nil, true
	}
	return m, nil, false
}

// edgeScrollCmd scrolls the chat while the drag is within two visible rows
// of the top/bottom edge and schedules the next pulse. Handled inline so a
// single 33 ms tick keeps scrolling while the mouse is held still at the edge.
func (m *Model) edgeScrollCmd(p Point) tea.Cmd {
	visible := m.vp.Height
	if visible <= 0 {
		return nil
	}
	rel := p.Line - m.vp.YOffset
	maxOff := m.vp.TotalLineCount() - visible
	if maxOff < 0 {
		maxOff = 0
	}
	var moved bool
	if rel <= 1 && m.vp.YOffset > 0 {
		m.vp.SetYOffset(m.vp.YOffset - 1)
		m.sel.Focus.Line--
		moved = true
	} else if rel >= visible-2 && m.vp.YOffset < maxOff {
		m.vp.SetYOffset(m.vp.YOffset + 1)
		m.sel.Focus.Line++
		moved = true
	}
	if !moved {
		return nil
	}
	return tea.Tick(33*time.Millisecond, func(time.Time) tea.Msg { return selectionTickMsg{} })
}

// updateSelectionTick re-applies edge scrolling if the drag is still active
// and the mouse is still at the edge (this message fires every 33 ms).
func (m Model) updateSelectionTick() (Model, tea.Cmd) {
	if !m.sel.Active {
		return m, nil
	}
	return m, m.edgeScrollCmd(m.sel.Focus)
}

func (m Model) chatLine(line int) string {
	if line < 0 || line >= len(m.chatLines) {
		return ""
	}
	return m.chatLines[line]
}

var (
	selectionCSI = regexp.MustCompile("\x1b\\[[0-9;?]*[ -/]*[@-~]")
	// OSC bodies cannot contain BEL or ESC. This matches both BEL and ST
	// terminators without greedily consuming visible text after an ST.
	selectionOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
)

func stripSelectionANSI(s string) string {
	s = selectionOSC.ReplaceAllString(s, "")
	return selectionCSI.ReplaceAllString(s, "")
}

func visibleWidth(s string) int {
	return uniseg.StringWidth(stripSelectionANSI(s))
}

// sliceColumns slices plain text by display cells, snapping at grapheme
// boundaries (never splits a wide rune or combining sequence).
func sliceColumns(s string, start, end int) string {
	if end <= start {
		return ""
	}
	col := 0
	var b strings.Builder
	g := uniseg.NewGraphemes(s)
	for g.Next() {
		seg := g.Str()
		w := uniseg.StringWidth(seg)
		if col >= start && col < end {
			b.WriteString(seg)
		}
		col += w
		if col >= end {
			break
		}
	}
	return b.String()
}

func normalizePoints(a, b Point) (Point, Point) {
	if a.Line < b.Line || (a.Line == b.Line && a.Col <= b.Col) {
		return a, b
	}
	return b, a
}

// selectionText extracts ANSI/OSC-free visible text for a range of rendered
// lines. Lines outside the content are treated as empty.
// selectionText extracts ANSI/OSC-free visible text for a range of rendered
// lines, skipping each line's gutter (passed in as gutters, parallel to
// lines) so a drag that crosses block starts never copies the glyph.
func selectionText(lines []string, gutters []int, anchor, focus Point) string {
	a, b := normalizePoints(anchor, focus)
	if a == b {
		return ""
	}
	var out []string
	for i := a.Line; i <= b.Line; i++ {
		line := ""
		if i >= 0 && i < len(lines) {
			line = stripSelectionANSI(lines[i])
		}
		start, end := 0, visibleWidth(line)
		if i == a.Line {
			start = a.Col
		}
		if i == b.Line {
			end = b.Col
		}
		if g := gutterAt(gutters, i); start < g {
			start = g
		}
		out = append(out, strings.TrimRight(sliceColumns(line, start, end), " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// gutterAt reads a gutter width from a parallel gutters slice (nil-safe).
func gutterAt(gutters []int, line int) int {
	if line < 0 || line >= len(gutters) {
		return 0
	}
	return gutters[line]
}

// highlightLine applies reverse video to the selected visible columns. The
// active drag is short-lived, so selected text is rendered plain+inverse
// rather than trying to preserve every underlying SGR span.
func highlightLine(line string, start, end int) string {
	if end <= start {
		return line
	}
	plain := stripSelectionANSI(line)
	return sliceColumns(plain, 0, start) + "\x1b[7m" + sliceColumns(plain, start, end) + "\x1b[27m" + sliceColumns(plain, end, visibleWidth(plain))
}

// overlaySelection highlights the active selection on already-rendered viewport
// lines. It takes the rendered string and the offset rather than reading m.vp so
// it composes with whatever produces the viewport: the panel layout renders a
// local copy of the viewport with a reserved height, so the selection has to
// land on that output instead of on a second, independent render.
func overlaySelection(view string, yOffset int, sel Selection, gutters []int) string {
	if !sel.Active {
		return view
	}
	lines := strings.Split(view, "\n")
	a, b := normalizePoints(sel.Anchor, sel.Focus)
	for rel := range lines {
		abs := yOffset + rel
		if abs < a.Line || abs > b.Line {
			continue
		}
		start, end := 0, visibleWidth(lines[rel])
		if abs == a.Line {
			start = a.Col
		}
		if abs == b.Line {
			end = b.Col
		}
		// Never paint the reverse-video over the gutter: it is chrome,
		// not content, and the copied text already excludes it.
		if g := gutterAt(gutters, abs); start < g {
			start = g
		}
		lines[rel] = highlightLine(lines[rel], start, end)
	}
	return strings.Join(lines, "\n")
}
