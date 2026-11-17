package widgets

import (
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type splitFixture struct {
	h     *harness
	s     *Splitter
	a, b  *recorder
	moves []int
	dones []int
}

func newSplitFixture(t *testing.T, o Orientation, sizeSecond bool, pos, w, hgt int) *splitFixture {
	f := &splitFixture{a: newRecorder(), b: newRecorder()}
	f.s = NewSplitter(f.a, f.b, o, pos)
	f.s.SizeSecond = sizeSecond
	f.s.Min = 3
	f.s.OnMove = func(p int) { f.moves = append(f.moves, p) }
	f.s.OnDone = func(p int) { f.dones = append(f.dones, p) }
	f.h = newHarness(t, NewOverlay(f.s), w, hgt)
	return f
}

func (f *splitFixture) divider() Rect {
	var r Rect
	f.h.do(func() { r = f.s.DividerRect() })
	return r
}

func TestSplitterDragColumns(t *testing.T) {
	f := newSplitFixture(t, Columns, false, 10, 40, 8)
	if d := f.divider(); d != (Rect{10, 0, 1, 8}) {
		t.Fatalf("divider %+v", d)
	}
	if r := []rune(f.h.row(3)); r[10] != '│' {
		t.Fatalf("divider not drawn: %q", f.h.row(3))
	}

	f.h.move(10, 3)
	if !f.s.Hovered() || []rune(f.h.row(3))[10] != '┃' {
		t.Fatalf("hover: %v %q", f.s.Hovered(), f.h.row(3))
	}
	f.h.move(5, 3)
	if f.s.Hovered() {
		t.Fatal("hover stuck after leaving the divider")
	}
	f.a.take() // the move to (5,3) was the pane's hover; only the drag must not leak

	f.h.press(10, 3)
	if !f.s.Dragging() {
		t.Fatal("press on divider did not start a drag")
	}
	f.h.drag(14, 3)
	f.h.drag(20, 5)
	f.h.release(20, 5)
	if f.s.Pos != 20 || f.s.Dragging() {
		t.Fatalf("pos=%d dragging=%v", f.s.Pos, f.s.Dragging())
	}
	if len(f.moves) != 2 || f.moves[0] != 14 || f.moves[1] != 20 {
		t.Errorf("OnMove %v, want [14 20]", f.moves)
	}
	if len(f.dones) != 1 || f.dones[0] != 20 {
		t.Errorf("OnDone %v", f.dones)
	}
	if r := []rune(f.h.row(0)); r[20] != '┃' || r[10] == '│' {
		t.Errorf("divider not redrawn at 20 (hovered): %q", f.h.row(0))
	}
	// The panes were laid out around the new position.
	var ar, br Rect
	f.h.do(func() { ar, br = rectOf(f.a), rectOf(f.b) })
	if ar != (Rect{0, 0, 20, 8}) || br != (Rect{21, 0, 19, 8}) {
		t.Errorf("panes %+v %+v", ar, br)
	}
	if n := len(f.a.take()) + len(f.b.take()); n != 0 {
		t.Errorf("panes saw %d actions during the drag", n)
	}
}

func TestSplitterClampsAndCapturesOutside(t *testing.T) {
	f := newSplitFixture(t, Columns, false, 10, 40, 8)
	f.h.do(func() { f.s.Max = 25 })
	f.h.press(10, 2)
	f.h.drag(0, 2) // below Min
	if f.s.Pos != 3 {
		t.Errorf("pos %d, want Min 3", f.s.Pos)
	}
	f.h.drag(39, 7) // past Max, at the screen edge
	if f.s.Pos != 25 {
		t.Errorf("pos %d, want Max 25", f.s.Pos)
	}
	f.h.do(func() { f.s.Max = 0 })
	f.h.drag(38, 7) // no Max: leaves Min for the other pane (40-1-3)
	if f.s.Pos != 36 {
		t.Errorf("pos %d, want 36", f.s.Pos)
	}
	f.h.release(39, 7)
	if f.s.Dragging() {
		t.Error("release outside the divider did not end the drag")
	}
}

func TestSplitterGrabOffset(t *testing.T) {
	// A drag keeps the pointer's offset from the divider (here 0), and a
	// press next to the divider goes to the pane, not the drag.
	f := newSplitFixture(t, Columns, false, 10, 40, 8)
	f.h.press(11, 2)
	if f.s.Dragging() {
		t.Fatal("press beside the divider started a drag")
	}
	f.h.release(11, 2)
	if f.b.count(actLeftDown) != 1 {
		t.Error("second pane did not get its press")
	}
}

func TestSplitterSizeSecondFollowsResize(t *testing.T) {
	f := newSplitFixture(t, Columns, true, 12, 40, 6)
	if d := f.divider(); d.X != 40-1-12 {
		t.Fatalf("divider at %d, want %d", d.X, 40-1-12)
	}
	f.h.press(27, 1)
	f.h.drag(19, 1) // dragging left widens the right-hand pane
	f.h.release(19, 1)
	if f.s.Pos != 20 {
		t.Fatalf("pos %d, want 20", f.s.Pos)
	}
	f.h.sim.SetSize(60, 6)
	f.h.sync()
	if d := f.divider(); d.X != 60-1-20 {
		t.Errorf("after resize divider at %d, want %d (sidebar keeps its width)", d.X, 60-1-20)
	}
}

func TestSplitterRows(t *testing.T) {
	f := newSplitFixture(t, Rows, false, 4, 20, 12)
	if d := f.divider(); d != (Rect{0, 4, 20, 1}) {
		t.Fatalf("divider %+v", d)
	}
	if f.h.row(4) != "────────────────────" {
		t.Fatalf("row 4 = %q", f.h.row(4))
	}
	f.h.press(7, 4)
	f.h.drag(7, 8)
	f.h.release(7, 8)
	if f.s.Pos != 8 || len(f.dones) != 1 {
		t.Fatalf("pos=%d dones=%v", f.s.Pos, f.dones)
	}
}

func TestSplitterDoubleClickResets(t *testing.T) {
	f := newSplitFixture(t, Columns, false, 10, 40, 8)
	f.h.press(10, 2)
	f.h.drag(22, 2)
	f.h.release(22, 2)
	if f.s.Pos != 22 {
		t.Fatalf("pos %d", f.s.Pos)
	}
	tview.DoubleClickInterval = time.Second
	f.h.click(22, 2)
	f.h.click(22, 2)
	if f.s.Pos != 10 {
		t.Errorf("double-click: pos %d, want Default 10", f.s.Pos)
	}
	if last := f.dones[len(f.dones)-1]; last != 10 {
		t.Errorf("OnDone after reset = %d", last)
	}
}

func TestSplitterHandleOverFrame(t *testing.T) {
	// tviewrt shape: one self-painting frame, the splitter is only the drag
	// handle drawn over it; the frame gets everything else.
	frame := newRecorder()
	s := NewSplitter(nil, nil, Columns, 8)
	s.SizeSecond = true // a right-hand sidebar 8 wide
	s.Under = frame
	var moves []int
	s.OnMove = func(p int) { moves = append(moves, p) }
	h := newHarness(t, NewOverlay(s), 30, 4)
	var fr Rect
	h.do(func() { fr = rectOf(frame) })
	if fr != (Rect{0, 0, 30, 4}) {
		t.Fatalf("frame rect %+v: Under must get the whole area", fr)
	}
	if []rune(h.row(1))[21] != '│' {
		t.Fatalf("divider not drawn over the frame at x=21: %q", h.row(1))
	}
	h.click(5, 1)
	h.click(25, 1)
	if n := frame.count(actLeftClick); n != 2 {
		t.Errorf("frame got %d clicks, want 2 (both sides of the divider)", n)
	}
	h.press(21, 1)
	h.drag(15, 1)
	h.release(15, 1)
	if s.Pos != 14 || len(moves) != 1 || moves[0] != 14 {
		t.Errorf("pos=%d moves=%v, want sidebar width 14", s.Pos, moves)
	}
	if n := len(frame.take()); n != 0 {
		t.Errorf("frame saw %d actions of the drag", n)
	}
	h.key(tcell.KeyEnter)
	if len(frame.keys) != 1 {
		t.Errorf("keys did not reach the frame: %v", frame.keys)
	}
}
