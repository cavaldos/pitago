package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/clipboard"
)

func TestSelectionTextSingleLine(t *testing.T) {
	lines := []string{"hello world", "second line"}
	got := selectionText(lines, nil, Point{Line: 0, Col: 0}, Point{Line: 0, Col: 5})
	if got != "hello" {
		t.Fatalf("got %q, want \"hello\"", got)
	}
}

func TestSelectionTextMultiLine(t *testing.T) {
	lines := []string{"aaaa", "bbbb", "cccc"}
	got := selectionText(lines, nil, Point{Line: 0, Col: 1}, Point{Line: 2, Col: 2})
	want := "aaa\nbbbb\ncc"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestSelectionTextReversedAnchor(t *testing.T) {
	lines := []string{"xxxxxx"}
	got := selectionText(lines, nil, Point{Line: 0, Col: 5}, Point{Line: 0, Col: 2})
	if got != "xxx" {
		t.Fatalf("reversed drag must normalize, got %q", got)
	}
}

func TestSelectionTextStripsANSI(t *testing.T) {
	lines := []string{"\x1b[38;2;1;2;3mred\x1b[0m \x1b]8;;https://x\x07link\x1b]8;;\x07 rest"}
	got := selectionText(lines, nil, Point{Line: 0, Col: 0}, Point{Line: 0, Col: 100})
	if got != "red link rest" {
		t.Fatalf("ANSI/OSC not stripped: %q", got)
	}
}

func TestSelectionTextStripsSTHyperlinksWithoutConsumingText(t *testing.T) {
	lines := []string{"\x1b]8;;https://one\x1b\\one\x1b]8;;\x1b\\ after \x1b]8;;https://two\x1b\\two\x1b]8;;\x1b\\ tail"}
	got := selectionText(lines, nil, Point{Line: 0, Col: 0}, Point{Line: 0, Col: 100})
	if got != "one after two tail" {
		t.Fatalf("ST OSC stripping consumed visible text: %q", got)
	}
}

func TestSelectionTextClampsLines(t *testing.T) {
	got := selectionText([]string{"a"}, nil, Point{Line: 0, Col: 0}, Point{Line: 5, Col: 2})
	if !strings.Contains(got, "a") {
		t.Fatalf("out-of-range lines must clamp cleanly, got %q", got)
	}
}

func TestSelectionTextSkipsGutter(t *testing.T) {
	lines := []string{"● hello", "world", "  nope"}
	// A drag spanning the whole middle line must not copy the leading
	// gutter glyph even when the selection starts at column zero.
	got := selectionText(lines, []int{2, 0, 2}, Point{Line: 0, Col: 0}, Point{Line: 2, Col: 7})
	if got != "hello\nworld\nnope" {
		t.Fatalf("gutter not skipped: %q", got)
	}
}

func TestMousePointClampsPastGutter(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.chatLines = []string{"● hello world"}
	m.gutterCols = []int{2}
	m.vp.Width, m.vp.Height = 30, 5
	// Press on the gutter glyph (col 0/1) and on the text (col 4):
	// both must anchor at the content start or later, never inside the
	// glyph. Screen row 1 is content line 0.
	if p := m.mousePoint(0, 1); p.Col != 2 {
		t.Fatalf("press at col 0 must clamp to gutter width: %+v", p)
	}
	if p := m.mousePoint(1, 1); p.Col != 2 {
		t.Fatalf("press at col 1 must clamp to gutter width: %+v", p)
	}
	if p := m.mousePoint(5, 1); p.Col != 5 {
		t.Fatalf("press past the gutter must stay: %+v", p)
	}
}

func TestDoubleClickAnchorsPastGutter(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.chatLines = []string{"● hello world"}
	m.gutterCols = []int{2}
	m.vp.Width, m.vp.Height = 30, 5
	m.LastPressAt = time.Now().Add(-time.Second)
	m.LastPressLine = 0
	// Simulate the drag-release path: m.updateSelection's press handler
	// uses mousePoint internally, so run a full press then a release and
	// confirm the copied text has no glyph. Directly inspect the anchor
	// gate instead: double-click selection must start at col 2.
	got, _, handled := m.updateSelection(tea.MouseMsg{X: 3, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !handled {
		t.Fatal("press must start selection")
	}
	// Second press inside the double-click window on the same line.
	m.LastPressAt = time.Now()
	got2, _, _ := got.updateSelection(tea.MouseMsg{X: 3, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !got2.sel.DoubleClick {
		t.Fatal("second press must be a double click")
	}
	if got2.sel.Anchor.Col != 2 {
		t.Fatalf("double-click anchor must sit past the gutter: %+v", got2.sel.Anchor)
	}
}

func TestSliceColumnsGraphemes(t *testing.T) {
	s := "héllo 🌍 w"
	if got := sliceColumns(s, 0, 2); got != "hé" {
		t.Fatalf("combined e+acute split: %q", got)
	}
	if got := sliceColumns(s, 6, 7); got != "🌍" {
		t.Fatalf("wide emoji must not split: %q", got)
	}
}

func TestHighlightLine(t *testing.T) {
	line := "abcde"
	got := highlightLine(line, 1, 3)
	if !strings.Contains(got, "\x1b[7mbc\x1b[27m") {
		t.Fatalf("reverse video range missing: %q", got)
	}
	if got != "a"+"\x1b[7mbc\x1b[27m"+"de" {
		t.Fatalf("highlight composition wrong: %q", got)
	}
}

func TestMousePointClampsToChat(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW = 50 // below the 80-col sidebar breakpoint: chat is full width
	m.vp.Height = 20
	p := m.mousePoint(999, 0)
	if p.Col != m.mainW()-1 {
		t.Fatalf("right edge not clamped to mainW-1: %+v (mainW=%d)", p, m.mainW())
	}
	p = m.mousePoint(-5, 999)
	if p.Col != 0 {
		t.Fatalf("left edge not clamped: %+v", p)
	}
	if p.Line != m.vp.YOffset+19 {
		t.Fatalf("bottom edge not clamped: %+v", p)
	}
}

func TestLeftPressStartsSelection(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.blocks = []Block{{Kind: "assistant", Text: "hello"}}
	m.vp.Width = 60
	m.vp.Height = 10
	m.sel = Selection{}
	got, cmd, handled := m.updateSelection(tea.MouseMsg{
		X: 3, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if !handled || !got.sel.Active {
		t.Fatal("left press must start a selection")
	}
	if cmd != nil {
		t.Fatalf("press must not schedule edge scroll: %v", cmd)
	}
}

func TestLeftPressOutsideViewportDoesNotStartSelection(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	if _, _, handled := m.updateSelection(tea.MouseMsg{X: 1, Y: 0, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}); handled {
		t.Fatal("header press must not start a selection")
	}
	// The painted chat frame occupies screen rows 1..Height inclusive: the
	// header is row 0, so the bottom-most chat row is Height itself. The first
	// row past it belongs to whatever is below the chat.
	chatH := m.chatViewport().Height
	if _, _, handled := m.updateSelection(tea.MouseMsg{X: 1, Y: chatH + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}); handled {
		t.Fatalf("press below the chat frame (row %d, height %d) must not start a selection",
			chatH+1, chatH)
	}
	// The bottom-most chat row is a real chat row and must be selectable.
	if _, _, handled := m.updateSelection(tea.MouseMsg{X: 1, Y: chatH, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}); !handled {
		t.Fatalf("press on the bottom-most chat row (row %d) must start a selection", chatH)
	}
}

func TestActiveDragFinishesInsideSidebar(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.vp.Width, m.vp.Height = 40, 10
	m.chatLines = []string{"alpha", "beta"}
	m.sel = Selection{Active: true, Anchor: Point{0, 0}, Focus: Point{0, 1}, HadDrag: true}
	// X=100 is over the sidebar; an active drag must still receive release.
	got, _, handled := m.updateSelection(tea.MouseMsg{X: 100, Y: 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	if !handled || got.sel.Active {
		t.Fatal("active selection must finish when release crosses sidebar")
	}
}

func TestMotionExtendsAndReleaseCopies(t *testing.T) {
	// Capture the copy instead of performing it: YankText reaches the system
	// clipboard (or emits OSC 52 to this terminal), so an unstubbed release
	// replaces whatever the developer had copied.
	var copied string
	defer stubClipboard(func(text string) clipboard.Status {
		copied = text
		return clipboard.Status{Channel: clipboard.Atoto, Bytes: len(text), Chars: len([]rune(text))}
	})()

	m := New(nil, t.TempDir())
	m.Mouse = true
	m.chatLines = []string{"alpha", "beta", "gamma"}
	m.vp.Width = 30
	m.vp.Height = 5
	m.sel = Selection{Active: true, Anchor: Point{Line: 0, Col: 0}, Focus: Point{Line: 0, Col: 0}}
	got, _, _ := m.updateSelection(tea.MouseMsg{X: 5, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	if !got.sel.Active || !got.sel.HadDrag {
		t.Fatal("motion must keep selection dragging")
	}
	got, _, _ = got.updateSelection(tea.MouseMsg{X: 5, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	if got.sel.Active {
		t.Fatal("release must clear the selection")
	}
	// The drag runs from line 0 col 0 to line 2 col 5 (row 3 of the chat
	// frame), so the release must copy all three lines.
	if want := "alpha\nbeta\ngamma"; copied != want {
		t.Fatalf("release copied %q, want %q", copied, want)
	}
}

// stubClipboard redirects the clipboard transport for the duration of a test
// and returns a restore func. No test in this package may write to the real
// system clipboard.
func stubClipboard(fn func(string) clipboard.Status) func() {
	prev := clipboard.Transport
	clipboard.Transport = fn
	return func() { clipboard.Transport = prev }
}

func TestDoubleClickSelectsLine(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.chatLines = []string{"alpha beta", "second line"}
	m.vp.Width = 30
	m.vp.Height = 5
	m.LastPressLine = 1                                     // Y=2 → viewport row 1 → content line 1 ("second line")
	m.LastPressAt = time.Now().Add(-100 * time.Millisecond) // inside double-click window
	got, _, _ := m.updateSelection(tea.MouseMsg{X: 3, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !got.sel.DoubleClick || got.sel.Focus.Col == got.sel.Anchor.Col {
		t.Fatalf("double-click must select whole line: %+v", got.sel)
	}
}

func TestEdgeScrollPersistsOffset(t *testing.T) {
	m := New(nil, t.TempDir())
	lines := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		lines = append(lines, "line")
	}
	m.vp.Width = 30
	m.vp.Height = 5
	m.vp.SetContent(strings.Join(lines, "\n"))
	// Focus on the last visible row (rel 4 >= visible-2) with room below.
	m.sel = Selection{Active: true, Anchor: Point{Line: 0, Col: 0}, Focus: Point{Line: 4, Col: 0}}
	if cmd := m.edgeScrollCmd(m.sel.Focus); cmd == nil {
		t.Fatal("bottom-edge drag must schedule a scroll tick")
	}
	if m.vp.YOffset != 1 {
		t.Fatalf("edge scroll must persist on the model: YOffset=%d, want 1", m.vp.YOffset)
	}
	if m.sel.Focus.Line != 5 {
		t.Fatalf("focus must follow the scroll: Focus.Line=%d, want 5", m.sel.Focus.Line)
	}
}

// gutterCols must be built in renderBlocks: block-first lines carry the
// 2-cell gutter, boxed continuations carry a blank 2-cell gutter, and
// framed tool/bash blocks are flush-left (no gutter).
func TestRenderBlocksBuildsGutterCols(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 60, 40
	m.blocks = []Block{
		{Kind: "user", Text: "hi"},
		{Kind: "assistant", Text: "hello world"},
	}
	m.renderBlocks()
	if len(m.gutterCols) != len(m.chatLines) {
		t.Fatalf("gutterCols %d != chatLines %d", len(m.gutterCols), len(m.chatLines))
	}
	// Line 0 is the user block's first line (gutter); line 1 is the
	// assistant block's first line (gutter).
	if m.gutterCols[0] != 2 {
		t.Fatalf("user block first line should have gutter: %+v", m.gutterCols)
	}
	// Find the assistant block start and check it too.
	found := false
	for i := 1; i < len(m.chatLines); i++ {
		if strings.Contains(stripSelectionANSI(m.chatLines[i]), "hello world") {
			if m.gutterCols[i] != 2 {
				t.Fatalf("assistant first line should have gutter at %d: %+v", i, m.gutterCols[i])
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("assistant content not rendered: %q", m.chatLines)
	}
}

// A press must resolve to the line View() actually paints under the cursor.
// With a task widget or plugin panel up, the painted chat frame is a shrunk
// copy of m.vp, so mapping against m.vp shifted every row by the panel height
// and let a press on the panel itself select an unrelated chat line.
func TestMousePointMatchesPaintedChatFrame(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 24
	// Real content that overflows the frame, following the tail: this is the
	// configuration where the painted copy is re-pinned and its YOffset drifts
	// away from m.vp's, so a mousePoint built on m.vp addresses the wrong line.
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.vp.Width, m.vp.Height = 40, 10
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.vp.GotoBottom()
	m.chatLines = lines
	m.Todos = []TodoItem{
		{ID: "1", Content: "Finish design", Status: TodoInProgress},
		{ID: "2", Content: "Ship it", Status: TodoPending},
	}
	if got := m.renderTaskWidget(); got == "" {
		t.Fatal("precondition: task widget must render for this test to mean anything")
	}

	painted := m.chatViewport()
	if painted.Height >= m.vp.Height {
		t.Fatalf("precondition: panel must shrink the chat frame; painted=%d vp=%d",
			painted.Height, m.vp.Height)
	}
	if painted.YOffset == m.vp.YOffset {
		t.Fatalf("precondition: painted offset must differ from m.vp (%d) for this "+
			"test to discriminate; re-pin did not happen", painted.YOffset)
	}

	// Every visible row must map to the transcript line painted at that row.
	for row := 1; row <= painted.Height; row++ {
		got := m.mousePoint(1, row)
		want := painted.YOffset + row - 1
		if got.Line != want {
			t.Fatalf("screen row %d maps to line %d, but View() paints line %d there",
				row, got.Line, want)
		}
	}
}

// A press that lands on the panel below the chat frame is not a chat press and
// must not open a selection.
func TestPressOnPanelDoesNotStartSelection(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	m.chatLines = []string{"alpha", "beta"}
	m.Todos = []TodoItem{{ID: "1", Content: "Finish design", Status: TodoInProgress}}

	painted := m.chatViewport()
	panelRow := painted.Height + 1
	if panelRow >= m.vp.Height {
		t.Fatalf("precondition: need a row below the chat frame but inside m.vp; painted=%d vp=%d",
			painted.Height, m.vp.Height)
	}
	got, _, handled := m.updateSelection(tea.MouseMsg{
		X: 1, Y: panelRow, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft,
	})
	if handled {
		t.Error("a press on the task panel must not start a chat selection")
	}
	if got.sel.Active {
		t.Error("selection opened from a press outside the chat frame")
	}
}

// Dragging across a syntax-highlighted line must not drain the colour out of
// the unselected columns. The old highlight rebuilt the whole line from
// stripped plain text, so every SGR span outside the selection was lost.
func TestHighlightKeepsStylingOutsideSelection(t *testing.T) {
	const (
		red   = "\x1b[31m"
		blue  = "\x1b[34m"
		reset = "\x1b[0m"
	)
	line := red + "aaa" + blue + "bbb" + reset

	got := highlightLine(line, 4, 6)

	if !strings.Contains(got, red) {
		t.Errorf("colour of the columns before the selection was dropped: %q", got)
	}
	if !strings.Contains(got, blue) {
		t.Errorf("colour of the selected range was dropped: %q", got)
	}
	if !strings.Contains(got, "\x1b[7m") || !strings.Contains(got, "\x1b[27m") {
		t.Errorf("selected range is not reverse-video: %q", got)
	}
	// The visible text must survive unchanged, escapes aside.
	if plain := stripSelectionANSI(got); plain != "aaabbb" {
		t.Errorf("visible text changed: got %q, want %q", plain, "aaabbb")
	}
}

// Escape sequences are zero-width: they must not shift the column at which the
// highlight lands, or the selection lands one word early.
func TestHighlightIgnoresEscapeWidth(t *testing.T) {
	// A hyperlink OSC 8 sequence wraps the first word but occupies no columns.
	line := "\x1b]8;;https://example.com\x07one\x1b]8;;\x07 two three"
	got := highlightLine(line, 4, 7)
	if plain := stripSelectionANSI(got); plain != "one two three" {
		t.Fatalf("visible text changed: %q", plain)
	}
	// Column 4..7 is "two"; the OSC-8 wrappers must sit outside the inverse span.
	between := got[strings.Index(got, "\x1b[7m")+len("\x1b[7m"):]
	selected := between[:strings.Index(between, "\x1b[27m")]
	if selected != "two" {
		t.Fatalf("reverse video covers %q, want %q", selected, "two")
	}
}

// gutterCols is render-derived, so it can be empty or short. When it has no
// entry for a line, selection must still not put the "● " glyph on the
// clipboard.
func TestSelectionSkipsGutterWhenTableIsStale(t *testing.T) {
	lines := []string{"● first block", "plain continuation", "● second block"}

	// Table missing entirely (no Refresh yet).
	got := selectionText(lines, nil, Point{Line: 0, Col: 0}, Point{Line: 2, Col: 8})
	if strings.Contains(got, "●") {
		t.Errorf("gutter glyph copied when the gutter table is absent: %q", got)
	}
	if want := "first block\nplain continuation\nsecond"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	// Table present but too short to cover every line.
	short := []int{2}
	got = selectionText(lines, short, Point{Line: 0, Col: 0}, Point{Line: 2, Col: 8})
	if strings.Contains(got, "●") {
		t.Errorf("gutter glyph copied when the gutter table is short: %q", got)
	}

	// The table records block gutters; a line outside the block loop has no
	// entry, and the glyph it carries is what gives it one.
	withZero := []int{0, 0, 0}
	got = selectionText(lines, withZero, Point{Line: 1, Col: 0}, Point{Line: 1, Col: 5})
	if got != "plain" {
		t.Errorf("a line with no gutter glyph must not be clipped: got %q", got)
	}
}

// Every line drawn through gutter() carries a 2-cell gutter, not just the
// ones inside the block loop. The connection error, the status line and the
// welcome logo all live outside it, so their glyphs ("×", "○", "●") used to
// land on the clipboard — the leak this pins shut. Index arithmetic was tried
// first and was wrong twice: the jump mark is emitted before blockRows[i], and
// a chat with no blocks has no blockRows at all.
func TestGutterCoversPreambleStatusAndLogo(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
		icon  string
	}{
		{"conn-error", func(m *Model) {
			m.connErr = "connection lost"
			m.blocks = []Block{{Kind: "assistant", Text: "hello world"}}
		}, "×"},
		{"conn-error with no blocks", func(m *Model) {
			m.connErr = "connection lost"
		}, "×"},
		{"conn-error behind a jump mark", func(m *Model) {
			m.connErr = "connection lost"
			m.jumpBlock = 0
			m.blocks = []Block{{Kind: "assistant", Text: "hello world"}}
		}, "×"},
		{"status line", func(m *Model) {
			m.thinking = true
			m.Status = "thinking…"
			m.blocks = []Block{{Kind: "assistant", Text: "hello world"}}
		}, "○"},
		{"welcome logo", func(m *Model) {}, "●"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, t.TempDir())
			m.vp.Width, m.vp.Height = 60, 40
			tc.setup(&m)
			m.renderBlocks()

			found := -1
			for i, ln := range m.chatLines {
				if strings.HasPrefix(strings.TrimLeft(stripSelectionANSI(ln), " \t"), tc.icon+" ") {
					found = i
					break
				}
			}
			if found < 0 {
				t.Fatalf("precondition: no %q gutter line was rendered: %q", tc.icon, m.chatLines)
			}
			got := selectionText(m.chatLines, m.gutterCols, Point{found, 0}, Point{found, 3})
			if strings.Contains(got, tc.icon) {
				t.Errorf("%q glyph copied: %q", tc.icon, got)
			}
		})
	}
}

// Edge auto-scroll must stop at the real bottom of the transcript. The painted
// frame is shorter than m.vp when a panel is up, so a bound derived from the
// painted height never reaches m.vp's maximum: the tick loop then spins
// forever and walks the selection's focus past the transcript, which drops the
// newest lines from the copy — exactly what dragging to the bottom is for.
func TestEdgeScrollStopsAtTranscriptEnd(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.chatLines = lines
	m.Todos = []TodoItem{{ID: "1", Content: "Finish design", Status: TodoInProgress}}
	m.vp.GotoBottom()
	m.sel = Selection{Active: true, HadDrag: true}

	painted := m.chatViewport()
	if painted.Height >= m.vp.Height {
		t.Fatalf("precondition: panel must shrink the frame; painted=%d vp=%d",
			painted.Height, m.vp.Height)
	}

	// Hold the pointer on the bottom visible row and pulse until it settles.
	focus := Point{Line: painted.YOffset + painted.Height - 1, Col: 0}
	m.sel.Focus = focus
	for i := 0; i < 200; i++ {
		if cmd := m.edgeScrollCmd(m.sel.Focus); cmd == nil {
			break
		}
		if i == 199 {
			t.Fatal("edge scroll never settled: the 33ms tick loop would run forever")
		}
	}

	if m.sel.Focus.Line > len(m.chatLines) {
		t.Errorf("selection focus walked past the transcript: line %d of %d",
			m.sel.Focus.Line, len(m.chatLines))
	}
	if got := m.vp.YOffset; got != m.vp.TotalLineCount()-m.vp.Height {
		t.Errorf("did not settle at the real bottom: offset=%d want=%d",
			got, m.vp.TotalLineCount()-m.vp.Height)
	}
}

func TestDoubleClickWindowSurvivesRelease(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	m.chatLines = []string{"● the quick brown fox"}

	press := tea.MouseMsg{X: 8, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	release := tea.MouseMsg{X: 8, Y: 2, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone}

	m, _, _ = m.updateSelection(press)
	if m.LastPressAt.IsZero() {
		t.Fatal("first press must arm the double-click window")
	}
	m, _, _ = m.updateSelection(release)
	if m.LastPressAt.IsZero() {
		t.Fatal("the release inside a double-click must not disarm the window")
	}
}

func TestClickAfterDragIsNotDoubleClick(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	m.chatLines = []string{"● alpha", "beta"}

	press := tea.MouseMsg{X: 8, Y: 2, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	m, _, _ = m.updateSelection(press)
	m, _, _ = m.updateSelection(tea.MouseMsg{X: 12, Y: 3, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m, _, _ = m.updateSelection(tea.MouseMsg{X: 12, Y: 3, Action: tea.MouseActionRelease, Button: tea.MouseButtonNone})
	if !m.LastPressAt.IsZero() {
		t.Fatal("a drag must disarm the double-click window")
	}
	m.LastPressAt = time.Now()
	m, _, _ = m.updateSelection(press)
	if m.sel.DoubleClick {
		t.Fatal("a click right after a drag must not read as a double-click")
	}
}

// A full reset (ESC[0m) inside the selected range clears the reverse video the
// highlight just enabled, so the highlight used to stop mid-row at the next
// reset; an explicit colour could likewise override the inverted foreground.
// The selected range therefore carries no escapes of its own.
func TestHighlightSpanCarriesNoEscapes(t *testing.T) {
	line := "plain " + "\x1b[0m" + "grey " + "\x1b[38;5;252m" + "more\x1b[0m" + " tail"

	got := highlightLine(line, 0, visibleWidth(line))
	k, j := strings.Index(got, "\x1b[7m"), strings.Index(got, "\x1b[27m")
	if k < 0 || j < 0 {
		t.Fatalf("no inverse span emitted: %q", got)
	}
	if inside := got[k+4 : j]; strings.Contains(inside, "\x1b") {
		t.Errorf("selected span carries escapes that can end or alter the highlight: %q", inside)
	}
	if plain := stripSelectionANSI(got); plain != "plain grey more tail" {
		t.Errorf("visible text changed: %q", plain)
	}
}

// The same escape is still preserved when it falls outside the selected
// range — the unselected columns of a line keep their styling.
func TestHighlightKeepsEscapesOutsideSelectedRange(t *testing.T) {
	line := "\x1b[38;5;252mhead\x1b[0m selected text tail"
	start := visibleWidth("head ")

	got := highlightLine(line, start, start+visibleWidth("selected text"))
	if !strings.Contains(got, "\x1b[38;5;252m") {
		t.Errorf("styling outside the selection was destroyed: %q", got)
	}
	if plain := stripSelectionANSI(got); plain != "head selected text tail" {
		t.Errorf("visible text changed: %q", plain)
	}
	// The highlight must still cover exactly the selected words.
	k, j := strings.Index(got, "\x1b[7m"), strings.Index(got, "\x1b[27m")
	if k < 0 || j < 0 {
		t.Fatalf("no inverse span emitted: %q", got)
	}
	if inside := strings.TrimSpace(stripSelectionANSI(got[k+4 : j])); inside != "selected text" {
		t.Errorf("inverse span covers %q, want %q", inside, "selected text")
	}
}
