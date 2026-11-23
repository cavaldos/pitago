package widgets

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
)

func sampleItems() []MenuItem {
	return []MenuItem{
		{ID: "copy", Label: "Copy", Shortcut: "⌥C"},
		{ID: "paste", Label: "Paste", Disabled: true},
		{Separator: true},
		{ID: "more", Label: "More", Submenu: []MenuItem{
			{ID: "a", Label: "Alpha"},
			{ID: "b", Label: "Beta"},
		}},
		{ID: "quit", Label: "Quit"},
	}
}

type menuFixture struct {
	h      *harness
	base   *recorder
	ov     *Overlay
	m      *ContextMenu
	picked []string
	closes int
}

func newMenuFixture(t *testing.T, w, hgt int) *menuFixture {
	f := &menuFixture{base: newRecorder()}
	f.ov = NewOverlay(f.base)
	f.h = newHarness(t, f.ov, w, hgt)
	f.m = NewContextMenu(sampleItems())
	f.m.OnSelect = func(id string) { f.picked = append(f.picked, id) }
	f.m.OnClose = func() { f.closes++ }
	return f
}

func (f *menuFixture) openAt(x, y int) {
	f.h.do(func() { f.m.OpenAt(f.ov, x, y) })
	f.h.sync()
}

func (f *menuFixture) rect() Rect {
	var r Rect
	f.h.do(func() { r = rectOf(f.m) })
	return r
}

func TestMenuDrawsAtPointer(t *testing.T) {
	f := newMenuFixture(t, 40, 12)
	f.openAt(5, 2)
	r := f.rect()
	// widest label "Paste"(5) + submenu arrow(2) + gap(2) + "⌥C"(2) + padding 2 + border 2
	if r != (Rect{5, 2, 15, 7}) {
		t.Fatalf("rect = %+v", r)
	}
	want := []string{
		"     ┌─────────────┐",
		"     │ Copy     ⌥C │",
		"     │ Paste       │",
		"     ├─────────────┤",
		"     │ More      › │",
		"     │ Quit        │",
		"     └─────────────┘",
	}
	for i, w := range want {
		if got := f.h.row(2 + i); got != w {
			t.Errorf("row %d\n got %q\nwant %q", 2+i, got, w)
		}
	}
}

func TestMenuFlipsAtEdges(t *testing.T) {
	f := newMenuFixture(t, 30, 10)
	f.openAt(28, 8)
	r := f.rect()
	if r.X+r.W > 30 || r.Y+r.H > 10 || r.X >= 28 || r.Y >= 8 {
		t.Fatalf("menu not flipped into the screen: %+v", r)
	}
	if r.X+r.W-1 != 28 || r.Y+r.H-1 != 8 { // flipped so its far corner sits at the pointer
		t.Errorf("flip should pin the corner to the pointer: %+v", r)
	}
	// Bigger than the screen: clamped to it.
	g := newMenuFixture(t, 12, 5)
	g.openAt(3, 3)
	if r := g.rect(); r.X < 0 || r.Y < 0 || r.X+r.W > 12 || r.Y+r.H > 5 {
		t.Errorf("not clamped: %+v", r)
	}
}

func TestMenuHoverAndClickSelects(t *testing.T) {
	f := newMenuFixture(t, 40, 12)
	f.openAt(5, 2)
	f.h.move(8, 3) // "Copy"
	if f.m.Selected() != 0 {
		t.Fatalf("hover selected %d", f.m.Selected())
	}
	if _, _, attr := f.h.style(8, 3).Decompose(); attr&tcell.AttrReverse == 0 {
		t.Error("hovered row not highlighted")
	}
	f.h.move(8, 4) // disabled "Paste": highlight stays on Copy
	if f.m.Selected() != 0 {
		t.Errorf("disabled row took the highlight")
	}
	f.h.click(8, 4) // clicking disabled does nothing
	if len(f.picked) != 0 || !f.m.Open() {
		t.Fatalf("disabled row selected: %v", f.picked)
	}
	f.h.click(8, 7) // "Quit"
	if strings.Join(f.picked, ",") != "quit" || f.m.Open() || f.closes != 1 {
		t.Fatalf("picked=%v open=%v closes=%d", f.picked, f.m.Open(), f.closes)
	}
	if n := len(f.base.take()); n != 0 {
		t.Errorf("base saw %d mouse actions through the menu", n)
	}
}

func TestMenuClickOutsideClosesAndIsSwallowed(t *testing.T) {
	f := newMenuFixture(t, 40, 12)
	f.openAt(5, 2)
	f.base.take()
	f.h.move(30, 10) // motion behind an open menu: not for the base
	f.h.click(30, 10)
	if f.m.Open() || f.closes != 1 || len(f.picked) != 0 {
		t.Fatalf("open=%v closes=%d picked=%v", f.m.Open(), f.closes, f.picked)
	}
	if acts := f.base.take(); len(acts) != 0 {
		t.Errorf("base saw the dismissing click: %v", acts)
	}
	f.h.move(31, 10)
	f.h.click(31, 10) // menu gone: the base gets clicks again
	if f.base.count(actLeftClick) != 1 {
		t.Error("base did not get the next click")
	}
}

func TestMenuKeyboard(t *testing.T) {
	f := newMenuFixture(t, 40, 12)
	f.openAt(5, 2)
	f.h.key(tcell.KeyDown) // → Copy
	f.h.key(tcell.KeyDown) // skips disabled + separator → More
	if f.m.Selected() != 3 {
		t.Fatalf("selected %d, want 3 (More)", f.m.Selected())
	}
	f.h.key(tcell.KeyUp)
	if f.m.Selected() != 0 {
		t.Fatalf("selected %d after up, want 0", f.m.Selected())
	}
	f.h.key(tcell.KeyUp) // wraps to Quit
	if f.m.Selected() != 4 {
		t.Fatalf("selected %d after wrap, want 4", f.m.Selected())
	}
	f.h.key(tcell.KeyEnter)
	if strings.Join(f.picked, ",") != "quit" {
		t.Fatalf("picked %v", f.picked)
	}
	if len(f.base.keys) != 0 {
		t.Errorf("keys leaked to base while the menu was open: %v", f.base.keys)
	}
	f.openAt(5, 2)
	f.h.key(tcell.KeyEsc)
	if f.m.Open() || f.closes != 2 {
		t.Fatalf("esc: open=%v closes=%d", f.m.Open(), f.closes)
	}
	f.h.key(tcell.KeyEsc) // menu closed: keys reach the base again
	if len(f.base.keys) != 1 {
		t.Errorf("base keys %v", f.base.keys)
	}
}

func TestSubmenuMouseAndKeys(t *testing.T) {
	f := newMenuFixture(t, 50, 14)
	f.openAt(5, 2)
	f.h.move(8, 6) // "More" opens its submenu beside the row
	pops := f.ov.Popups()
	if len(pops) != 2 {
		t.Fatalf("popups=%d, want menu+submenu", len(pops))
	}
	sub := pops[1].(*ContextMenu)
	sr := rectOf(sub)
	if sr.X != 20 || sr.Y != 5 { // right of the 15-wide menu; first row level with "More"
		t.Errorf("submenu rect %+v", sr)
	}
	if !strings.Contains(f.h.row(6), "More      ›") || !strings.Contains(f.h.row(6), "Alpha") {
		t.Errorf("row 6 = %q", f.h.row(6))
	}
	f.h.move(8, 3) // back on "Copy": submenu closes
	if len(f.ov.Popups()) != 1 {
		t.Fatalf("submenu survived hovering a sibling")
	}
	f.h.move(8, 6)
	f.h.move(22, 7) // into the submenu, "Beta"
	f.h.click(22, 7)
	if strings.Join(f.picked, ",") != "b" || len(f.ov.Popups()) != 0 || f.closes != 1 {
		t.Fatalf("picked=%v popups=%d closes=%d", f.picked, len(f.ov.Popups()), f.closes)
	}

	// Keyboard: → opens with the first row selected, ← closes one level.
	f.openAt(5, 2)
	f.h.key(tcell.KeyDown)
	f.h.key(tcell.KeyDown)
	f.h.key(tcell.KeyRight)
	if len(f.ov.Popups()) != 2 {
		t.Fatal("→ did not open the submenu")
	}
	f.h.key(tcell.KeyLeft)
	if len(f.ov.Popups()) != 1 || !f.m.Open() {
		t.Fatal("← should close only the submenu")
	}
	f.h.key(tcell.KeyEnter) // Enter on "More" opens it too
	f.h.key(tcell.KeyDown)
	f.h.key(tcell.KeyEnter) // Beta
	if strings.Join(f.picked, ",") != "b,b" {
		t.Fatalf("picked %v", f.picked)
	}
}

func TestSubmenuFlipsLeft(t *testing.T) {
	f := newMenuFixture(t, 30, 14)
	f.openAt(14, 2) // 15 wide: x 14..28, no room for the submenu on the right
	f.h.move(17, 6)
	pops := f.ov.Popups()
	if len(pops) != 2 {
		t.Fatal("no submenu")
	}
	sr := rectOf(pops[1].(*ContextMenu))
	if sr.X+sr.W != 14 {
		t.Errorf("submenu should sit left of the menu: %+v", sr)
	}
}
