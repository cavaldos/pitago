package widgets

import (
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type barMenuFixture struct {
	h      *harness
	ov     *Overlay
	bar    *MenuBar
	body   *recorder
	clock  *fakeClock
	picked []string
}

func editorMenus() []BarMenu {
	return []BarMenu{
		{Title: "File", Tooltip: "Files and sessions", Items: []MenuItem{
			{ID: "new", Label: "New", Shortcut: "^N"}, {ID: "open", Label: "Open…"},
			{Separator: true}, {ID: "quit", Label: "Quit", Shortcut: "^Q"}}},
		{Title: "Edit", Items: []MenuItem{{ID: "copy", Label: "Copy"}, {ID: "more", Label: "More", Submenu: []MenuItem{{ID: "deep", Label: "Deep"}}}}},
		{Title: "Session", Items: []MenuItem{{ID: "fork", Label: "Fork"}}},
		{Title: "View", Items: []MenuItem{{ID: "side", Label: "Sidebar"}}},
		{Title: "Options", Items: []MenuItem{{ID: "prefs", Label: "Preferences"}}},
		{Title: "Help", Items: []MenuItem{{ID: "about", Label: "About"}}},
		{Title: "Extras", Items: []MenuItem{{ID: "x", Label: "X"}}}, // 'e' taken by Edit → 'x'
	}
}

func newBarMenuFixture(t *testing.T) *barMenuFixture {
	f := &barMenuFixture{body: newRecorder(), clock: &fakeClock{}}
	flex := tview.NewFlex().SetDirection(tview.FlexRow)
	f.ov = NewOverlay(flex)
	f.bar = NewMenuBar(f.ov, editorMenus, func(title, id string) { f.picked = append(f.picked, title+"/"+id) })
	flex.AddItem(f.bar, 1, 0, false).AddItem(f.body, 0, 1, true)
	f.h = newHarness(t, f.ov, 70, 14)
	hv := NewHover(f.ov, func(fn func()) { f.h.app.QueueUpdateDraw(fn) })
	hv.AfterFunc = f.clock.AfterFunc
	f.bar.Hover = hv
	f.h.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == sentinelKey {
			f.h.sentinel <- struct{}{}
			return nil
		}
		if f.bar.HandleKey(e) {
			return nil
		}
		return e
	})
	return f
}

func (f *barMenuFixture) altKey(r rune) {
	f.h.sim.InjectKey(tcell.KeyRune, r, tcell.ModAlt)
	f.h.sync()
}

// title x positions: " File "0-5 " Edit "6-11 " Session "12-20 " View "21-26 " Options "27-35 " Help "36-41 " Extras "42-49

func TestMenuBarDrawsTitlesAndMnemonics(t *testing.T) {
	f := newBarMenuFixture(t)
	if got := f.h.row(0); got != " File  Edit  Session  View  Options  Help  Extras" {
		t.Fatalf("bar = %q", got)
	}
	want := map[int]rune{0: 'f', 1: 'e', 2: 's', 3: 'v', 4: 'o', 5: 'h', 6: 'x'}
	for i, r := range want {
		if got := f.bar.Mnemonic(i); got != r {
			t.Errorf("mnemonic %d = %q, want %q", i, got, r)
		}
	}
	if _, _, a := f.h.style(1, 0).Decompose(); a&tcell.AttrUnderline == 0 {
		t.Error("F of File not underlined")
	}
	if _, _, a := f.h.style(2, 0).Decompose(); a&tcell.AttrUnderline != 0 {
		t.Error("i of File underlined")
	}
	if _, _, a := f.h.style(44, 0).Decompose(); a&tcell.AttrUnderline == 0 { // " Extras" starts at 42: E=43, x=44
		t.Error("x of Extras not underlined")
	}
}

func TestMenuBarClickOpenHoverSwitchClickClose(t *testing.T) {
	f := newBarMenuFixture(t)
	f.h.click(2, 0)
	if f.bar.OpenIndex() != 0 {
		t.Fatalf("open = %d", f.bar.OpenIndex())
	}
	if f.bar.Menu().Selected() != -1 {
		t.Error("a mouse open should not preselect a row")
	}
	if _, _, a := f.h.style(2, 0).Decompose(); a&tcell.AttrReverse == 0 {
		t.Error("open title not highlighted")
	}
	if r := rectOf(f.bar.Menu()); r.X != 0 || r.Y != 1 {
		t.Errorf("menu rect %+v, want under File", r)
	}
	f.h.move(8, 0) // hover Edit: switches
	if f.bar.OpenIndex() != 1 || len(f.ov.Popups()) != 1 {
		t.Fatalf("hover switch: open=%d popups=%d", f.bar.OpenIndex(), len(f.ov.Popups()))
	}
	if !strings.Contains(f.h.row(2), "Copy") {
		t.Errorf("Edit menu not drawn: %q", f.h.row(2))
	}
	if _, _, a := f.h.style(8, 0).Decompose(); a&tcell.AttrReverse == 0 {
		t.Error("switched title not highlighted")
	}
	f.h.move(8, 0)
	f.h.click(8, 0) // the open title again: closes
	if f.bar.OpenIndex() != -1 || f.ov.Modal() {
		t.Fatalf("second click: open=%d modal=%v", f.bar.OpenIndex(), f.ov.Modal())
	}
	if n := len(f.body.take()); n != 0 {
		t.Errorf("body saw %d actions from bar clicks", n)
	}
	// With nothing open, hovering titles only highlights.
	f.h.move(15, 0)
	if f.bar.OpenIndex() != -1 {
		t.Error("hover opened a menu while none was open")
	}
	if _, _, a := f.h.style(15, 0).Decompose(); a&tcell.AttrBold == 0 {
		t.Error("hovered title not bold")
	}
}

func TestMenuBarQuickClicksAcrossTitles(t *testing.T) {
	// tview turns a second click within DoubleClickInterval anywhere into a
	// DoubleClick; the bar opens on press, so that can't swallow it.
	f := newBarMenuFixture(t)
	tview.DoubleClickInterval = 1e9
	f.h.click(2, 0)
	f.h.click(2, 0) // closes File
	f.h.click(8, 0) // opens Edit right away
	if f.bar.OpenIndex() != 1 {
		t.Fatalf("open = %d, want Edit", f.bar.OpenIndex())
	}
}

func TestMenuBarPressDragRelease(t *testing.T) {
	f := newBarMenuFixture(t)
	f.h.press(2, 0)
	f.h.drag(3, 3) // "Open…" with the button held
	f.h.release(3, 3)
	if strings.Join(f.picked, ",") != "File/open" || f.bar.OpenIndex() != -1 {
		t.Fatalf("picked %v open=%d", f.picked, f.bar.OpenIndex())
	}
	if n := f.body.count(actLeftClick); n != 0 {
		t.Error("the releasing click leaked to the body")
	}
}

func TestMenuBarKeyboard(t *testing.T) {
	f := newBarMenuFixture(t)
	f.altKey('e')
	if f.bar.OpenIndex() != 1 || f.bar.Menu().Selected() != 0 {
		t.Fatalf("alt+e: open=%d", f.bar.OpenIndex())
	}
	f.h.key(tcell.KeyRight) // Copy has no submenu → next title
	if f.bar.OpenIndex() != 2 {
		t.Fatalf("→: open=%d, want Session", f.bar.OpenIndex())
	}
	f.h.key(tcell.KeyLeft)
	f.h.key(tcell.KeyLeft)
	if f.bar.OpenIndex() != 0 {
		t.Fatalf("←←: open=%d, want File", f.bar.OpenIndex())
	}
	f.h.key(tcell.KeyLeft) // wraps
	if f.bar.OpenIndex() != 6 {
		t.Fatalf("← wrap: open=%d, want Extras", f.bar.OpenIndex())
	}
	// In Edit, → on "More" opens its submenu instead; ← closes the
	// submenu only; another ← walks the bar.
	f.altKey('E')
	f.h.key(tcell.KeyDown)
	f.h.key(tcell.KeyRight)
	if len(f.ov.Popups()) != 2 || f.bar.OpenIndex() != 1 {
		t.Fatalf("→ on submenu row: popups=%d open=%d", len(f.ov.Popups()), f.bar.OpenIndex())
	}
	f.h.key(tcell.KeyRight) // "Deep" has no submenu → next title, chain closed
	if f.bar.OpenIndex() != 2 || len(f.ov.Popups()) != 1 {
		t.Fatalf("→ in submenu: open=%d popups=%d", f.bar.OpenIndex(), len(f.ov.Popups()))
	}
	f.altKey('s') // the open one: closes
	if f.bar.OpenIndex() != -1 {
		t.Fatal("alt+mnemonic of the open menu should close it")
	}
	f.h.key(tcell.KeyF10)
	if f.bar.OpenIndex() != 0 {
		t.Fatal("F10 should open File")
	}
	f.h.key(tcell.KeyDown)
	f.h.key(tcell.KeyEnter)
	if strings.Join(f.picked, ",") != "File/open" {
		t.Fatalf("picked %v", f.picked)
	}
	f.h.key(tcell.KeyF10)
	f.h.key(tcell.KeyEsc)
	if f.bar.OpenIndex() != -1 || f.ov.Modal() {
		t.Fatal("Esc did not close")
	}
	f.altKey('z') // no title: not consumed, reaches the body
	f.h.key(tcell.KeyDown)
	if len(f.body.keys) != 2 {
		t.Errorf("body keys %v", f.body.keys)
	}
}

func TestMenuBarOutsideClickAndTooltip(t *testing.T) {
	f := newBarMenuFixture(t)
	f.h.click(2, 0)
	f.h.click(50, 8) // outside: closes, swallowed
	if f.bar.OpenIndex() != -1 || f.body.count(actLeftClick) != 0 {
		t.Fatal("outside click must close the menu and not reach the body")
	}
	f.h.move(2, 0)
	f.clock.fire()
	f.h.sync()
	if !f.bar.Hover.Visible() || !strings.Contains(f.h.screen(), "Files and sessions") {
		t.Fatalf("title tooltip missing:\n%s", f.h.screen())
	}
	f.h.click(2, 0) // opening the menu hides the tooltip
	if f.bar.Hover.Visible() {
		t.Error("tooltip stayed over the open menu")
	}
}
