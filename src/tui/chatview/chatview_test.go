package chatview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cellwidth"
	"pitago/src/components/terminal_image"
)

func screen(t testing.TB, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return s
}

// rows returns the screen as plain text rows (never-drawn cells as ' ').
func rows(s tcell.SimulationScreen) []string {
	cs, w, h := s.GetContents()
	out := make([]string, h)
	for y := 0; y < h; y++ {
		var b strings.Builder
		for x := 0; x < w; x++ {
			c := cs[y*w+x]
			if len(c.Runes) == 0 {
				b.WriteByte(' ')
				continue
			}
			b.WriteString(string(c.Runes))
			x += max(0, cellwidth.StringWidth(string(c.Runes))-1) // skip wide-cell tail
		}
		out[y] = strings.TrimRight(b.String(), " ")
	}
	return out
}

func cell(s tcell.SimulationScreen, x, y int) tcell.SimCell {
	cs, w, _ := s.GetContents()
	return cs[y*w+x]
}

func draw(t testing.TB, v *ChatView, s tcell.SimulationScreen) []string {
	t.Helper()
	s.Clear()
	v.Draw(s)
	s.Show()
	return rows(s)
}

// numbered makes n blocks of k lines each: "b<i>l<j>", plus a blank spacer.
func numbered(n, k int) []Block {
	bs := make([]Block, n)
	for i := range bs {
		var b strings.Builder
		for j := 0; j < k; j++ {
			fmt.Fprintf(&b, "b%dl%d\n", i, j)
		}
		bs[i] = Block{ID: fmt.Sprint("b", i), Version: 1, Width: 40, Text: b.String()}
	}
	return bs
}

func newView(w, h int) *ChatView {
	v := New()
	v.SetRect(0, 0, w, h)
	return v
}

func TestRowsOf(t *testing.T) {
	for in, want := range map[string]int{"": 0, "a": 1, "a\n": 1, "a\n\n": 2, "a\nb": 2, "\n": 1} {
		if got := rowsOf(in); got != want {
			t.Errorf("rowsOf(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFitsAndPinnedTail(t *testing.T) {
	s := screen(t, 20, 4)
	defer s.Fini()
	v := newView(20, 4)
	v.SetBlocks(numbered(1, 2))
	if !v.AtBottom() || !v.AtTop() || v.YOffset() != 0 || v.TotalLineCount() != 2 {
		t.Fatalf("fits: bottom=%v top=%v y=%d total=%d", v.AtBottom(), v.AtTop(), v.YOffset(), v.TotalLineCount())
	}
	if v.ScrollPercent() != 1 {
		t.Fatalf("percent = %v", v.ScrollPercent())
	}
	got := draw(t, v, s)
	if got[0] != "b0l0" || got[1] != "b0l1" || got[2] != "" {
		t.Fatalf("rows = %q", got)
	}
	// Append while at bottom: follows the tail.
	v.AppendBlock(numbered(3, 2)[1])
	v.AppendBlock(numbered(3, 2)[2])
	if v.TotalLineCount() != 6 || v.YOffset() != 2 || !v.AtBottom() {
		t.Fatalf("follow: total=%d y=%d", v.TotalLineCount(), v.YOffset())
	}
	got = draw(t, v, s)
	if got[0] != "b1l0" || got[3] != "b2l1" {
		t.Fatalf("rows = %q", got)
	}
}

func TestReaderKeepsOffset(t *testing.T) {
	v := newView(20, 5)
	v.SetBlocks(numbered(10, 3)) // 30 lines
	if v.YOffset() != 25 {
		t.Fatalf("initial pin y=%d", v.YOffset())
	}
	v.ScrollUp(10)
	if v.YOffset() != 15 || v.AtBottom() {
		t.Fatalf("scrolled y=%d", v.YOffset())
	}
	// New content arrives and the streaming block grows: reader stays.
	v.AppendBlock(Block{ID: "new", Version: 1, Text: "x\ny\n"})
	v.UpdateBlock(Block{ID: "new", Version: 2, Text: "x\ny\nz\n"})
	if v.YOffset() != 15 || v.TotalLineCount() != 33 {
		t.Fatalf("reader moved: y=%d total=%d", v.YOffset(), v.TotalLineCount())
	}
	// Scrolling back to the bottom re-pins.
	v.ScrollDown(1000)
	if !v.AtBottom() || v.YOffset() != 28 {
		t.Fatalf("y=%d", v.YOffset())
	}
	v.AppendBlock(Block{ID: "n2", Text: "q\n"})
	if v.YOffset() != 29 {
		t.Fatalf("re-pinned follow y=%d", v.YOffset())
	}
	// Shrinking content under a reader clamps.
	v.ScrollUp(5)
	v.SetBlocks(numbered(2, 3))
	if v.YOffset() != 1 || !v.AtBottom() {
		t.Fatalf("clamp y=%d", v.YOffset())
	}
}

func TestScrollAPI(t *testing.T) {
	v := newView(20, 10)
	v.SetBlocks(numbered(10, 10)) // 100 lines
	v.GotoTop()
	if !v.AtTop() || v.ScrollPercent() != 0 {
		t.Fatal("GotoTop")
	}
	v.PageDown()
	v.HalfPageDown()
	v.LineDown(2)
	if v.YOffset() != 17 {
		t.Fatalf("y=%d", v.YOffset())
	}
	v.PageUp()
	v.HalfPageUp()
	v.LineUp(1)
	if v.YOffset() != 1 {
		t.Fatalf("y=%d", v.YOffset())
	}
	v.SetYOffset(-5)
	if v.YOffset() != 0 {
		t.Fatal("negative clamp")
	}
	v.SetYOffset(1000)
	if v.YOffset() != 90 || !v.AtBottom() || v.ScrollPercent() != 1 {
		t.Fatalf("overrun clamp y=%d", v.YOffset())
	}
	v.SetYOffset(45)
	if p := v.ScrollPercent(); p != 0.5 {
		t.Fatalf("percent %v", p)
	}
	v.GotoBottom()
	if v.YOffset() != 90 {
		t.Fatal("GotoBottom")
	}
}

func TestResizeRepinsOnlyWhenPinned(t *testing.T) {
	v := newView(20, 10)
	v.SetBlocks(numbered(5, 10)) // 50 lines
	v.SetRect(0, 0, 20, 4)       // shrink while pinned: newest line stays
	if v.YOffset() != 46 || !v.AtBottom() {
		t.Fatalf("pinned shrink y=%d", v.YOffset())
	}
	v.SetYOffset(10)
	v.SetRect(0, 0, 20, 8)
	if v.YOffset() != 10 {
		t.Fatalf("reader resize moved y=%d", v.YOffset())
	}
}

func TestVersionCache(t *testing.T) {
	s := screen(t, 20, 3)
	defer s.Fini()
	v := newView(20, 3)
	v.SetBlocks([]Block{{ID: "a", Version: 1, Width: 20, Text: "one\n"}})
	draw(t, v, s)
	e := v.blocks[0]
	if !e.parsed {
		t.Fatal("not parsed on draw")
	}
	// Same ID/Width/Text: entry (and its cells) reused, even via SetBlocks.
	v.SetBlocks([]Block{{ID: "a", Version: 1, Width: 20, Text: "one\n"}})
	if v.blocks[0] != e {
		t.Fatal("unchanged block re-created")
	}
	// Width change re-parses.
	v.UpdateBlock(Block{ID: "a", Version: 1, Width: 30, Text: "ONE\n"})
	if v.blocks[0] == e || draw(t, v, s)[0] != "ONE" {
		t.Fatal("width change not re-rendered")
	}
	// Text is always compared: a stale Version never keeps a stale parse.
	v.SetBlocks([]Block{{ID: "u", Version: 5, Text: "x\n"}})
	e = v.blocks[0]
	v.SetBlocks([]Block{{ID: "u", Version: 6, Text: "x\n"}})
	if v.blocks[0] != e {
		t.Fatal("identical text re-created")
	}
	v.SetBlocks([]Block{{ID: "u", Version: 6, Text: "y\nz\n"}})
	if v.blocks[0] == e || v.TotalLineCount() != 2 {
		t.Fatal("changed text under the same version reused")
	}
}

func TestOnlyVisibleBlocksParse(t *testing.T) {
	s := screen(t, 20, 5)
	defer s.Fini()
	v := newView(20, 5)
	v.SetBlocks(numbered(100, 3))
	draw(t, v, s)
	parsed := 0
	for _, e := range v.blocks {
		if e.parsed {
			parsed++
		}
	}
	if parsed > 3 {
		t.Fatalf("%d blocks parsed for a 5-row view", parsed)
	}
}

func TestStylesLiteralBracketsAndEmptyBlocks(t *testing.T) {
	s := screen(t, 30, 4)
	defer s.Fini()
	v := newView(30, 4)
	v.SetBlocks([]Block{
		{ID: "a", Text: "\x1b[1;31m● [x] err\x1b[0m\n\n"},
		{ID: "hidden", Text: ""},
		{ID: "b", Text: "日本 ok\n"},
	})
	got := draw(t, v, s)
	if got[0] != "● [x] err" || got[1] != "" || got[2] != "日本 ok" {
		t.Fatalf("rows = %q", got)
	}
	if fg, _, attr := cell(s, 0, 0).Style.Decompose(); fg != tcell.PaletteColor(1) || attr&tcell.AttrBold == 0 {
		t.Fatalf("style fg=%v attr=%v", fg, attr)
	}
	if id, _ := v.BlockAtLine(2); id != "b" {
		t.Fatalf("line 2 in %q (empty block must take no rows)", id)
	}
	if st, _ := v.BlockStart("hidden"); st != 2 {
		t.Fatalf("hidden start %d", st)
	}
}

func TestHitTestAndPointAt(t *testing.T) {
	v := New()
	v.SetRect(2, 1, 20, 4) // offset rect: screen coords must be translated
	v.SetBlocks(numbered(5, 3))
	v.SetYOffset(4)
	h, ok := v.HitTest(5, 2) // row 1 of view → abs 5
	if !ok || h.AbsLine != 5 || h.BlockID != "b1" || h.Line != 2 || h.Col != 3 || h.Block != 1 {
		t.Fatalf("hit = %+v ok=%v", h, ok)
	}
	if _, ok := v.HitTest(1, 2); ok {
		t.Fatal("hit left of rect")
	}
	if _, ok := v.HitTest(5, 5); ok {
		t.Fatal("hit below rect")
	}
	if p := v.PointAt(100, -3); p != (Point{Line: 4, Col: 19}) {
		t.Fatalf("PointAt clamp = %+v", p)
	}
	// Below the last line inside the rect: no hit.
	v.SetBlocks(numbered(1, 1))
	if _, ok := v.HitTest(5, 3); ok {
		t.Fatal("hit past content")
	}
}

func TestSelectionPainter(t *testing.T) {
	s := screen(t, 20, 4)
	defer s.Fini()
	v := newView(20, 4)
	v.SetBlocks([]Block{{ID: "a", Text: "● hello\n  world\n  third\n"}})
	// Reversed anchor/focus, gutter clamp of 2 cells on every line.
	v.SetSelection(Selection{
		Active: true, Anchor: Point{Line: 2, Col: 4}, Focus: Point{Line: 0, Col: 0},
		MinCol: func(int) int { return 2 },
	})
	draw(t, v, s)
	rev := func(x, y int) bool {
		_, _, a := cell(s, x, y).Style.Decompose()
		return a&tcell.AttrReverse != 0
	}
	if rev(0, 0) || !rev(2, 0) || !rev(6, 0) {
		t.Fatal("line 0: gutter must not be selected, text must")
	}
	if !rev(2, 1) || !rev(6, 1) {
		t.Fatal("middle line fully selected")
	}
	if !rev(3, 2) || rev(4, 2) {
		t.Fatal("last line: [start, 4) only")
	}
	v.ClearSelection()
	draw(t, v, s)
	if rev(2, 0) {
		t.Fatal("cleared selection still painted")
	}
}

func TestHover(t *testing.T) {
	s := screen(t, 20, 4)
	defer s.Fini()
	v := newView(20, 4)
	v.SetBlocks([]Block{{ID: "a", Text: "\x1b[2mfaint\x1b[0m\n\n"}, {ID: "b", Text: "other\n"}})
	v.SetHover("a")
	draw(t, v, s)
	if _, _, a := cell(s, 0, 0).Style.Decompose(); a&tcell.AttrBold == 0 || a&tcell.AttrDim != 0 {
		t.Fatalf("hovered row attrs %v", a)
	}
	if _, _, a := cell(s, 0, 2).Style.Decompose(); a&tcell.AttrBold != 0 {
		t.Fatal("other block hovered")
	}
	// Selection suppresses hover (app parity).
	v.SetSelection(Selection{Active: true, Anchor: Point{2, 0}, Focus: Point{2, 1}})
	draw(t, v, s)
	if _, _, a := cell(s, 0, 0).Style.Decompose(); a&tcell.AttrBold != 0 {
		t.Fatal("hover painted during selection")
	}
}

func TestJumpChip(t *testing.T) {
	s := screen(t, 40, 8)
	defer s.Fini()
	v := newView(40, 8)
	v.SetJumpChip("↓ Jump to latest · End", tcell.StyleDefault.Reverse(true))
	v.SetBlocks(numbered(10, 3))
	if v.ChipVisible() {
		t.Fatal("chip at bottom")
	}
	v.ScrollUp(3)
	if !v.ChipVisible() {
		t.Fatal("chip hidden while scrolled up")
	}
	got := draw(t, v, s)
	// h=8 → row 8-1-2 = 5; width 22 → start (40-22)/2 = 9.
	if !strings.Contains(got[5], "↓ Jump to latest · End") || strings.Index(got[5], "↓") != 9 {
		t.Fatalf("chip row = %q", got[5])
	}
	if !v.ChipAt(9, 5) || !v.ChipAt(30, 5) || v.ChipAt(31, 5) || v.ChipAt(8, 5) || v.ChipAt(10, 4) {
		t.Fatal("ChipAt disagrees with paint")
	}
	// Too short a frame: no chip.
	v.SetRect(0, 0, 40, 4)
	if v.ChipVisible() {
		t.Fatal("chip on 4-row frame")
	}
	v.SetJumpChip("", tcell.StyleDefault)
	v.SetRect(0, 0, 40, 8)
	if v.ChipVisible() {
		t.Fatal("disabled chip visible")
	}
}

func TestOverlay(t *testing.T) {
	s := screen(t, 10, 3)
	defer s.Fini()
	v := newView(10, 3)
	v.SetBlocks(numbered(1, 1))
	called := false
	v.SetOverlay(func(scr tcell.Screen, x, y, w, h int) {
		called = true
		if w != 10 || h != 3 {
			t.Errorf("overlay rect %d×%d", w, h)
		}
		scr.SetContent(x+9, y+2, '!', nil, tcell.StyleDefault)
	})
	got := draw(t, v, s)
	if !called || got[2] != "         !" {
		t.Fatalf("overlay: called=%v rows=%q", called, got)
	}
}

func TestPendingPassthrough(t *testing.T) {
	up := terminal_image.Upload("iVBORw0KGgo=", 7)
	place := terminal_image.PlaceUnicode(7, 2, 1)
	row, _ := terminal_image.PlaceholderRow(7, 0, 2)
	v := newView(20, 3)
	v.SetBlocks([]Block{{ID: "img", Version: 1, Text: up + place + row + "\n"}})
	got := v.PendingPassthrough()
	if len(got) != 2 || got[0] != up || got[1] != place {
		t.Fatalf("pending = %q", got)
	}
	if len(v.PendingPassthrough()) != 0 {
		t.Fatal("not drained")
	}
	// Same version again: not re-queued.
	v.SetBlocks([]Block{{ID: "img", Version: 1, Text: up + place + row + "\n"}})
	if len(v.PendingPassthrough()) != 0 {
		t.Fatal("re-queued an unchanged block")
	}
	// The grid still holds the placeholder cells with the diacritic.
	s := screen(t, 20, 3)
	defer s.Fini()
	draw(t, v, s)
	d, _ := terminal_image.Diacritic(0)
	if string(cell(s, 0, 0).Runes) != terminal_image.PlaceholderChar+string(d) {
		t.Fatalf("placeholder cell %q", string(cell(s, 0, 0).Runes))
	}
}

func TestRemoveBlock(t *testing.T) {
	v := newView(20, 10)
	v.SetBlocks(numbered(3, 2))
	if !v.RemoveBlock("b1") || v.RemoveBlock("nope") {
		t.Fatal("RemoveBlock result")
	}
	if v.TotalLineCount() != 4 {
		t.Fatalf("total %d", v.TotalLineCount())
	}
	if st, ok := v.BlockStart("b2"); !ok || st != 2 {
		t.Fatalf("b2 start %d", st)
	}
	if v.UpdateBlock(Block{ID: "b1"}) {
		t.Fatal("update of removed block")
	}
}

func TestMouseWheel(t *testing.T) {
	v := newView(20, 5)
	v.SetBlocks(numbered(10, 3))
	h := v.MouseHandler()
	ev := tcell.NewEventMouse(3, 2, tcell.WheelUp, 0)
	if ok, _ := h(tview.MouseScrollUp, ev, func(tview.Primitive) {}); !ok || v.YOffset() != 22 {
		t.Fatalf("wheel up: ok=%v y=%d", ok, v.YOffset())
	}
	out := tcell.NewEventMouse(50, 2, tcell.WheelUp, 0)
	if ok, _ := h(tview.MouseScrollUp, out, func(tview.Primitive) {}); ok {
		t.Fatal("wheel outside rect handled")
	}
}

func TestKeys(t *testing.T) {
	v := newView(20, 5)
	v.SetBlocks(numbered(10, 3))
	in := v.InputHandler()
	press := func(k tcell.Key) { in(tcell.NewEventKey(k, 0, tcell.ModNone), func(tview.Primitive) {}) }
	press(tcell.KeyHome)
	press(tcell.KeyPgDn)
	press(tcell.KeyDown)
	if v.YOffset() != 6 {
		t.Fatalf("y=%d", v.YOffset())
	}
	press(tcell.KeyEnd)
	if !v.AtBottom() {
		t.Fatal("End")
	}
}

// --- benchmarks: 10k-line log, 60 fps budget is 16.6 ms per frame --------

func bigLog() []Block {
	bs := make([]Block, 2000) // 2000 blocks × 5 lines = 10k lines
	for i := range bs {
		var b strings.Builder
		fmt.Fprintf(&b, "\x1b[1;38;2;97;175;239m● block %d\x1b[0m [tool] \x1b]8;;file:///a/b%d.go\x1b\\b%d.go\x1b]8;;\x1b\\\n", i, i, i)
		for j := 0; j < 3; j++ {
			fmt.Fprintf(&b, "  \x1b[38;5;250mline %d of block %d — 日本語 text with some width to it, padding padding padding\x1b[0m\n", j, i)
		}
		b.WriteString("\n")
		bs[i] = Block{ID: fmt.Sprint(i), Version: 1, Width: 200, Text: b.String()}
	}
	return bs
}

func BenchmarkScrollFrame(b *testing.B) {
	s := screen(b, 200, 50)
	defer s.Fini()
	v := newView(200, 50)
	v.SetBlocks(bigLog())
	if v.TotalLineCount() != 10000 {
		b.Fatalf("total %d", v.TotalLineCount())
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.SetYOffset((i * 37) % 9950) // jump around: mostly cold blocks
		v.Draw(s)
	}
}

func BenchmarkAppendFrame(b *testing.B) {
	s := screen(b, 200, 50)
	defer s.Fini()
	v := newView(200, 50)
	v.SetBlocks(bigLog())
	v.Draw(s)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One streaming flush: the tail block grows, view follows, repaint.
		v.UpdateBlock(Block{ID: "1999", Version: uint64(i + 2), Width: 200,
			Text: strings.Repeat("\x1b[38;5;252mstreamed token text \x1b[0m", 1+i%8) + "\n"})
		if i%16 == 0 {
			v.AppendBlock(Block{ID: fmt.Sprint("n", i), Version: 1, Width: 200, Text: "\x1b[1m● new\x1b[0m\n\n"})
		}
		v.Draw(s)
	}
}

func BenchmarkSetBlocksUnchanged(b *testing.B) {
	v := newView(200, 50)
	log := bigLog()
	v.SetBlocks(log)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v.SetBlocks(log) // app re-sending the full list each Refresh
	}
}
