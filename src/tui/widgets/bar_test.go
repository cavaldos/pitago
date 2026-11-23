package widgets

import (
	"strings"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// barFixture: a status bar on row 0 over a recording body, inside an
// Overlay with a fake-clock Hover.
type barFixture struct {
	h      *harness
	ov     *Overlay
	bar    *ClickBar
	body   *recorder
	hover  *Hover
	clock  *fakeClock
	clicks []string
}

func newBarFixture(t *testing.T, w, hgt int, segs ...Segment) *barFixture {
	f := &barFixture{body: newRecorder(), clock: &fakeClock{}}
	f.bar = NewClickBar(segs...)
	flex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(f.bar, 1, 0, false).AddItem(f.body, 0, 1, true)
	f.ov = NewOverlay(flex)
	f.h = newHarness(t, f.ov, w, hgt)
	f.hover = NewHover(f.ov, func(fn func()) { f.h.app.QueueUpdateDraw(fn) })
	f.hover.AfterFunc = f.clock.AfterFunc
	f.bar.Hover = f.hover
	f.bar.OnClick = func(id string, x, y int) { f.clicks = append(f.clicks, "L:"+id) }
	f.bar.OnRightClick = func(id string, x, y int) { f.clicks = append(f.clicks, "R:"+id) }
	return f
}

func statusSegs() []Segment {
	plain := tcell.StyleDefault
	return []Segment{
		{ID: "model", Text: " opus ", Style: plain, Tooltip: "Current model. Click to switch."},
		{ID: "mode", Text: " plan ", Style: plain},
		{ID: "off", Text: " n/a ", Style: plain, Disabled: true},
		{ID: "cost", Text: " $0.12 ", Style: plain, Right: true, Tooltip: "Session cost"},
		{ID: "ctx", Text: " 42% ", Style: plain, Right: true},
	}
}

func TestClickBarLayoutAndHitTest(t *testing.T) {
	f := newBarFixture(t, 40, 6, statusSegs()...)
	// left: " opus "(0-5) " plan "(6-11) " n/a "(12-16); right: " $0.12 "(33-39) " 42% "(28-32)
	if got := f.h.row(0); got != " opus  plan  n/a "+strings.Repeat(" ", 11)+" 42%  $0.12" {
		t.Fatalf("bar = %q", got)
	}
	cases := map[int]string{0: "model", 5: "model", 6: "mode", 12: "off", 20: "", 28: "ctx", 33: "cost", 39: "cost"}
	f.h.do(func() {
		for x, want := range cases {
			if got := f.bar.HitTest(x); got != want {
				t.Errorf("HitTest(%d) = %q, want %q", x, got, want)
			}
		}
	})
	r, ok := f.bar.SegmentRect("cost")
	if !ok || r != (Rect{33, 0, 7, 1}) {
		t.Errorf("SegmentRect(cost) = %+v %v", r, ok)
	}
}

func TestClickBarHoverAndClicks(t *testing.T) {
	f := newBarFixture(t, 40, 6, statusSegs()...)
	f.h.move(8, 0)
	if f.bar.Hovered() != "mode" {
		t.Fatalf("hovered %q", f.bar.Hovered())
	}
	if _, _, a := f.h.style(8, 0).Decompose(); a&tcell.AttrReverse == 0 {
		t.Error("hovered segment not highlighted (default HoverStyle = reverse)")
	}
	f.h.move(13, 0) // disabled: no hover
	if f.bar.Hovered() != "" {
		t.Errorf("disabled segment hovered")
	}
	f.h.click(8, 0)
	f.h.click(13, 0)
	f.h.rclick(35, 0)
	if strings.Join(f.clicks, ",") != "L:mode,R:cost" {
		t.Errorf("clicks %v", f.clicks)
	}
	f.h.move(10, 3) // into the body
	if f.bar.Hovered() != "" {
		t.Error("hover not cleared when leaving the bar")
	}
}

func TestTooltipDelayShowHide(t *testing.T) {
	f := newBarFixture(t, 40, 8, statusSegs()...)
	f.h.move(2, 0) // "model" has a tooltip
	if f.hover.Visible() || f.clock.live() != 1 {
		t.Fatalf("visible=%v timers=%d: must wait for the delay", f.hover.Visible(), f.clock.live())
	}
	f.h.move(3, 0) // still the same segment: no restart
	if f.clock.live() != 1 {
		t.Fatalf("timers=%d after moving within the segment", f.clock.live())
	}
	f.clock.fire()
	f.h.sync()
	if !f.hover.Visible() {
		t.Fatal("tooltip not shown after the delay")
	}
	tr := rectOf(f.hover.Tooltip())
	if tr.Y != 1 || tr.X != 0 { // below the anchor segment
		t.Errorf("tooltip rect %+v", tr)
	}
	if !strings.Contains(f.h.row(2), "Current model. Click to") {
		t.Errorf("tooltip text missing:\n%s", f.h.screen())
	}

	// Moving onto another segment hides it and restarts the delay.
	f.h.move(33, 0)
	if f.hover.Visible() || f.hover.Key() != "clickbar:cost" || f.clock.live() != 1 {
		t.Fatalf("visible=%v key=%q timers=%d", f.hover.Visible(), f.hover.Key(), f.clock.live())
	}
	f.clock.fire()
	f.h.sync()
	if tr := rectOf(f.hover.Tooltip()); tr.X+tr.W != 40 {
		t.Errorf("tooltip at the right edge not shifted left: %+v", tr)
	}

	// A press anywhere hides it, even over the tooltip's own anchor.
	f.h.press(34, 0)
	f.h.release(34, 0)
	if f.hover.Visible() {
		t.Error("press did not hide the tooltip")
	}

	// A segment without a tooltip cancels a pending one.
	f.h.move(2, 0)
	f.h.move(8, 0)
	if f.clock.fire() != 0 || f.hover.Visible() {
		t.Error("stale timer fired after moving to a segment without a tooltip")
	}

	// Moving into the body (a widget that never calls Out) still hides it:
	// the Overlay feeds the Hover every event.
	f.h.move(2, 0)
	f.clock.fire()
	f.h.sync()
	f.h.move(20, 5)
	if f.hover.Visible() {
		t.Error("tooltip survived the pointer leaving its anchor")
	}
}

func TestTooltipIsPassThrough(t *testing.T) {
	f := newBarFixture(t, 40, 8, statusSegs()...)
	f.h.move(2, 0)
	f.clock.fire()
	f.h.sync()
	tr := rectOf(f.hover.Tooltip())
	f.body.take()
	f.h.click(tr.X+2, tr.Y+1) // on the tooltip, over the body
	if f.body.count(actLeftClick) != 1 {
		t.Error("click on a tooltip did not reach the body below")
	}
}

func TestTooltipWrapANSIAndFlip(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	_ = sim.Init()
	sim.SetSize(30, 10)
	tip := NewTooltip("\x1b[31mred\x1b[0m words that need wrapping here", Rect{2, 9, 4, 1})
	tip.MaxWidth = 10
	tip.Place(Rect{0, 0, 30, 10})
	r := rectOf(tip)
	if r.Y+r.H != 9 { // no room below row 9: flipped above
		t.Errorf("tooltip not above its anchor: %+v", r)
	}
	if r.W != 13 { // shrinks to the widest wrapped line, "red words" (9) + border/padding 4
		t.Errorf("width %d, want 13", r.W)
	}
	tip.Draw(sim)
	sim.Show()
	var rows []string
	for y := r.Y + 1; y < r.Y+r.H-1; y++ {
		var b strings.Builder
		for x := r.X + 2; x < r.X+r.W-2; x++ {
			m, _, _, _ := sim.GetContent(x, y)
			b.WriteRune(m)
		}
		rows = append(rows, strings.TrimRight(b.String(), " "))
	}
	want := []string{"red words", "that need", "wrapping", "here"}
	if strings.Join(rows, "|") != strings.Join(want, "|") {
		t.Errorf("wrapped %q, want %q", rows, want)
	}
	_, _, st, _ := sim.GetContent(r.X+2, r.Y+1)
	if fg, _, _ := st.Decompose(); fg != tcell.ColorMaroon {
		t.Errorf("ANSI red lost: fg=%v", fg)
	}
	_, _, st, _ = sim.GetContent(r.X+6, r.Y+1)
	if fg, _, _ := st.Decompose(); fg == tcell.ColorMaroon {
		t.Error("SGR reset not honored")
	}
}

func TestHoverRealTimer(t *testing.T) {
	f := newBarFixture(t, 40, 6, statusSegs()...)
	f.hover.AfterFunc = nil
	f.hover.Delay = 20 * time.Millisecond
	f.h.move(2, 0)
	deadline := time.Now().Add(2 * time.Second)
	for !f.hover.Visible() && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !f.hover.Visible() {
		t.Fatal("tooltip never appeared with the real timer")
	}
}

func TestRightClickMenuDragRelease(t *testing.T) {
	f := newBarFixture(t, 40, 10, statusSegs()...)
	var picked []string
	m := NewContextMenu([]MenuItem{{ID: "switch", Label: "Switch model"}, {ID: "info", Label: "Info"}})
	m.OnSelect = func(id string) { picked = append(picked, id) }
	f.bar.OnRightClick = func(id string, x, y int) { m.OpenAt(f.ov, x, y) }

	f.h.rpress(2, 0) // menu opens on press, at the pointer
	if !m.Open() {
		t.Fatal("right press did not open the menu")
	}
	f.h.mouse(5, 2, tcell.ButtonSecondary) // drag with the button held onto "Info"
	f.h.release(5, 2)
	if strings.Join(picked, ",") != "info" || m.Open() {
		t.Fatalf("picked %v open=%v", picked, m.Open())
	}

	// A plain right click (press+release in place) leaves it open.
	f.h.rclick(2, 0)
	if !m.Open() {
		t.Fatal("right click in place closed the menu")
	}
}

func TestMenuButton(t *testing.T) {
	body := newRecorder()
	var ov *Overlay
	opens := 0
	var picked []string
	btn := NewMenuButton(nil, func() []MenuItem {
		opens++
		return []MenuItem{{ID: "new", Label: "New session"}, {ID: "x", Label: "Close"}}
	}, func(id string) { picked = append(picked, id) })
	top := tview.NewFlex().AddItem(tview.NewBox(), 0, 1, false).AddItem(btn, btn.Width(), 0, false)
	root := tview.NewFlex().SetDirection(tview.FlexRow).AddItem(top, 1, 0, false).AddItem(body, 0, 1, true)
	ov = NewOverlay(root)
	btn.Host = ov
	h := newHarness(t, ov, 30, 8)
	if got := h.row(0); !strings.HasSuffix(got, " ⋯") {
		t.Fatalf("button not drawn: %q", got)
	}
	h.click(28, 0)
	m := btn.Menu()
	if m == nil || opens != 1 {
		t.Fatalf("menu=%v opens=%d", m, opens)
	}
	r := rectOf(m)
	if r.Y != 1 || r.X+r.W != 30 { // below the button, right-aligned at the edge
		t.Errorf("menu rect %+v", r)
	}
	if m.Selected() != 0 {
		t.Errorf("menu from a button should preselect the first row")
	}
	h.click(28, 0) // the button again: closes, and does not reopen
	if btn.Menu() != nil || opens != 1 {
		t.Fatalf("second click: menu=%v opens=%d", btn.Menu(), opens)
	}
	h.click(28, 0)
	h.move(r.X+3, 3)
	h.click(r.X+3, 3)
	if strings.Join(picked, ",") != "x" || opens != 2 {
		t.Fatalf("picked %v opens %d", picked, opens)
	}
}

func TestOverlayKeysAndPaste(t *testing.T) {
	base := newRecorder()
	ov := NewOverlay(base)
	h := newHarness(t, ov, 30, 8)
	h.key(tcell.KeyDown)
	m := NewContextMenu([]MenuItem{{ID: "a", Label: "A"}})
	h.do(func() { m.OpenAt(ov, 1, 1) })
	h.key(tcell.KeyDown)
	h.key(tcell.KeyEsc)
	h.key(tcell.KeyUp)
	if len(base.keys) != 2 || base.keys[0] != tcell.KeyDown || base.keys[1] != tcell.KeyUp {
		t.Errorf("base keys %v: keys while the menu was open must not reach it", base.keys)
	}
}
