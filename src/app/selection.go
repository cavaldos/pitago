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
//
// The row-to-line mapping must run against the same viewport View() paints
// from, not m.vp: the painted chat frame is a shrunk, possibly re-pinned copy
// of m.vp whenever the task widget or a plugin panel is up. Mapping against
// m.vp would both shift every line by the panel offset and let a press that
// lands on a panel resolve to a chat line the user never aimed at.
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
	chatVp := m.chatViewport()
	h := chatVp.Height
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
	line := chatVp.YOffset + row
	// The gutter (2 cells) is not content: a press on it, or a drag that
	// crosses it, must not include the glyph in the selection.
	if g := m.gutterFor(line); col < g {
		col = g
	}
	return Point{Line: line, Col: col}
}

// gutterFor reports the leading gutter width for a rendered chat line (0
// or 2), so selection can clamp past it. See gutterAt for why a zero table
// entry defers to the line itself.
func (m Model) gutterFor(line int) int {
	if line < 0 {
		return 0
	}
	if line < len(m.gutterCols) && m.gutterCols[line] != 0 {
		return m.gutterCols[line]
	}
	return gutterFromLine(m.chatLine(line))
}

// updateSelection handles left press/motion/release in the chat column.
// Returns handled=true when the event was consumed by the selector.
func (m Model) updateSelection(msg tea.MouseMsg) (Model, tea.Cmd, bool) {
	if !m.Mouse || len(m.Dialogs) > 0 {
		return m, nil, false
	}
	now := time.Now()
	p := m.mousePoint(msg.X, msg.Y)
	// Same geometry the highlight is painted against: a press below the chat
	// frame lands on a panel, not on a transcript line.
	chatH := m.chatViewport().Height
	switch msg.Action {
	case tea.MouseActionPress:
		// New selection presses must start inside the chat viewport. Once
		// dragging, motion/release continue to be handled even if the pointer
		// crosses into the sidebar, so the release cannot activate a sidebar
		// control while completing a chat selection.
		if msg.Button != tea.MouseButtonLeft || m.overSide(msg.X) || msg.Y < 1 || msg.Y > chatH {
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
			m.LastPressLine = -1
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
		// Drop the double-click window after a drag, so a click that follows
		// one inside the window is not read as a double-click. It must NOT be
		// dropped on every release: a real double-click is press, release,
		// press, and disarming on that release means the second press can
		// never be detected.
		if m.sel.HadDrag {
			m.LastPressAt = time.Time{}
			m.LastPressLine = -1
		}
		m.sel = Selection{}
		return m, nil, true
	}
	return m, nil, false
}

// edgeScrollCmd scrolls the chat while the drag is within two visible rows
// of the top/bottom edge and schedules the next pulse. Handled inline so a
// single 33 ms tick keeps scrolling while the mouse is held still at the edge.
//
// The edge is measured against the painted chat frame, but the bound and the
// write are both m.vp's: SetYOffset clamps against m.vp.Height, so a maxOff
// derived from the painted height would still report "moved" after the real
// bottom is reached — a permanent 33 ms tick loop that also walks
// m.sel.Focus.Line past the transcript, so a drag held at the bottom edge
// copies empty tail lines and drops exactly the newest messages.
func (m *Model) edgeScrollCmd(p Point) tea.Cmd {
	chatVp := m.chatViewport()
	visible := chatVp.Height
	if visible <= 0 || m.vp.Height <= 0 {
		return nil
	}
	rel := p.Line - chatVp.YOffset
	maxOff := m.vp.TotalLineCount() - m.vp.Height
	if maxOff < 0 {
		maxOff = 0
	}
	var moved bool
	if rel <= 1 && m.vp.YOffset > 0 {
		before := m.vp.YOffset
		m.vp.SetYOffset(before - 1)
		if m.vp.YOffset != before {
			m.sel.Focus.Line--
			moved = true
		}
	} else if rel >= visible-2 && m.vp.YOffset < maxOff {
		before := m.vp.YOffset
		m.vp.SetYOffset(before + 1)
		if m.vp.YOffset != before {
			m.sel.Focus.Line++
			moved = true
		}
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
	selectionCSI = regexp.MustCompile("\x1b\\[[0-9;:?]*[ -/]*[@-~]")
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
		if g := gutterAt(gutters, i, line); start < g {
			start = g
		}
		out = append(out, strings.TrimRight(sliceColumns(line, start, end), " \t"))
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}

// gutterIcons are the glyphs gutter() prefixes onto the first non-blank row of
// the lines it renders: the block bullet, the status ring, the error cross.
var gutterIcons = []string{"● ", "○ ", "× "}

// gutterFromLine reports the gutter a rendered line advertises about itself.
// Everything drawn through gutter() outside the block loop — the connection
// error, the status line, the welcome logo — is covered by this; only a
// block's blank continuation gutter is invisible here, and the render table
// already records that one.
func gutterFromLine(rendered string) int {
	plain := strings.TrimLeft(stripSelectionANSI(rendered), " \t")
	for _, icon := range gutterIcons {
		if strings.HasPrefix(plain, icon) {
			return 2
		}
	}
	return 0
}

// gutterAt reports the gutter width of a rendered line.
//
// A non-zero table entry is authoritative: it comes from renderBlocks, which
// knows a block's blank continuation gutter that no glyph check can see.
// Otherwise the line decides for itself, because a zero entry also means "not
// a block line" — the table is sized to every chat line but only the block
// loop fills it, and treating those zeros as "no gutter" is what put "● " and
// "× " on the clipboard.
func gutterAt(gutters []int, line int, rendered string) int {
	if line >= 0 && line < len(gutters) && gutters[line] != 0 {
		return gutters[line]
	}
	if line < 0 {
		return 0
	}
	return gutterFromLine(rendered)
}

// escapeAt reports the length of the ANSI/OSC escape sequence starting at
// index i, or 0 when there is none. Zero-width by definition: these must be
// attributed to the segment that follows them, never counted as columns.
func escapeAt(s string, i int) (int, string) {
	if i+1 >= len(s) || s[i] != 0x1b {
		return 0, ""
	}
	switch s[i+1] {
	case '[':
		// CSI: parameter/intermediate bytes are all below 0x40, so the first
		// byte in the final-byte range closes the sequence.
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j - i + 1, s[i : j+1]
			}
		}
	case ']':
		// OSC: runs to BEL, or to ST (ESC \).
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j - i + 1, s[i : j+1]
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j - i + 2, s[i : j+2]
			}
		}
	default:
		// Two-character escape, except charset selectors (ESC ( B and
		// friends), where the byte after ESC is an intermediate in 0x20-0x2f
		// and one more byte belongs to the sequence. Getting this wrong
		// leaves a stray byte to be walked as visible text inside the
		// reverse-video span.
		if s[i+1] >= 0x20 && s[i+1] <= 0x2f && i+2 < len(s) {
			return 3, s[i : i+3]
		}
		return 2, s[i : i+2]
	}
	return 0, ""
}

// styledSegments splits a rendered line at the visible-column boundaries start
// and end, returning the text before, inside and after the range. Every escape
// sequence is preserved and attributed to the segment it precedes, so the
// syntax colouring of a code fence or a table survives the split.
func styledSegments(line string, start, end int) (before, selected, after string) {
	var b, s, a strings.Builder
	col := 0
	emit := func(colAt int, text string) {
		switch {
		case colAt < start:
			b.WriteString(text)
		case colAt < end:
			s.WriteString(text)
		default:
			a.WriteString(text)
		}
	}
	for i := 0; i < len(line); {
		if n, seq := escapeAt(line, i); n > 0 {
			emit(col, seq)
			i += n
			continue
		}
		cluster, rest, _, _ := uniseg.FirstGraphemeClusterInString(line[i:], -1)
		if cluster == "" {
			break
		}
		emit(col, cluster)
		col += uniseg.StringWidth(cluster)
		if rest == "" {
			// This cluster consumed the remainder; rest is empty rather
			// than "still more", and there is nothing left to index into.
			break
		}
		i += len(line[i:]) - len(rest)
	}
	return b.String(), s.String(), a.String()
}

// highlightLine applies reverse video to the selected visible columns. Only
// the selected range is re-wrapped: the unselected columns of the same line
// keep their original styling, so dragging across a syntax-highlighted fence
// or a coloured table does not drain the colour out of the whole line.
func highlightLine(line string, start, end int) string {
	if end <= start {
		return line
	}
	before, selected, after := styledSegments(line, start, end)
	if selected == "" {
		return line
	}
	return before + "\x1b[7m" + selected + "\x1b[27m" + after
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
		if g := gutterAt(gutters, abs, lines[rel]); start < g {
			start = g
		}
		lines[rel] = highlightLine(lines[rel], start, end)
	}
	return strings.Join(lines, "\n")
}
