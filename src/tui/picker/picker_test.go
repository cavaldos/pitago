package picker

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/tui/widgets"
)

// harness: a real tview.Application on a SimulationScreen (the
// widgets/harness_test.go pattern). sync() injects a sentinel key and waits
// for it, so every event before it has been handled and painted.
type harness struct {
	t        *testing.T
	sim      tcell.SimulationScreen
	app      *tview.Application
	sentinel chan struct{}
	done     chan struct{}
}

const sentinelKey = tcell.KeyF64

func newHarness(t *testing.T, root tview.Primitive, w, h int) *harness {
	t.Helper()
	old := tview.DoubleClickInterval
	tview.DoubleClickInterval = 0
	t.Cleanup(func() { tview.DoubleClickInterval = old })
	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	hs := &harness{t: t, sim: sim, sentinel: make(chan struct{}, 1), done: make(chan struct{})}
	hs.app = tview.NewApplication().SetScreen(sim).EnableMouse(true).SetRoot(root, true)
	sim.SetSize(w, h)
	hs.app.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
		if e.Key() == sentinelKey {
			hs.sentinel <- struct{}{}
			return nil
		}
		return e
	})
	go func() { _ = hs.app.Run(); close(hs.done) }()
	t.Cleanup(func() { hs.app.Stop(); <-hs.done })
	hs.sync()
	return hs
}

func (h *harness) sync() {
	h.t.Helper()
	h.sim.InjectKey(sentinelKey, 0, tcell.ModNone)
	select {
	case <-h.sentinel:
	case <-time.After(2 * time.Second):
		h.t.Fatal("event loop did not drain")
	}
	done := make(chan struct{})
	h.app.QueueUpdateDraw(func() { close(done) })
	<-done
}

func (h *harness) mouse(x, y int, b tcell.ButtonMask) {
	h.sim.InjectMouse(x, y, b, tcell.ModNone)
	h.sync()
}
func (h *harness) move(x, y int) { h.mouse(x, y, tcell.ButtonNone) }
func (h *harness) click(x, y int) {
	h.mouse(x, y, tcell.ButtonPrimary)
	h.mouse(x, y, tcell.ButtonNone)
}
func (h *harness) rclick(x, y int) {
	h.mouse(x, y, tcell.ButtonSecondary)
	h.mouse(x, y, tcell.ButtonNone)
}
func (h *harness) wheel(x, y int) { h.mouse(x, y, tcell.WheelDown) }
func (h *harness) wheelUp(x, y int) {
	h.mouse(x, y, tcell.WheelUp)
}
func (h *harness) key(k tcell.Key) { h.sim.InjectKey(k, 0, tcell.ModNone); h.sync() }
func (h *harness) typ(s string) {
	for _, r := range s {
		h.sim.InjectKey(tcell.KeyRune, r, tcell.ModNone)
	}
	h.sync()
}

// do runs f on the UI goroutine and waits.
func (h *harness) do(f func()) {
	done := make(chan struct{})
	h.app.QueueUpdateDraw(func() { f(); close(done) })
	<-done
	h.sync()
}

func (h *harness) row(y int) string {
	cs, w, _ := h.sim.GetContents()
	var b strings.Builder
	for x := 0; x < w; x++ {
		c := cs[y*w+x]
		if len(c.Runes) == 0 {
			b.WriteByte(' ')
			continue
		}
		b.WriteString(string(c.Runes))
	}
	return strings.TrimRight(b.String(), " ")
}

func (h *harness) screen() string {
	_, _, hh := h.sim.GetContents()
	rows := make([]string, hh)
	for y := range rows {
		rows[y] = h.row(y)
	}
	return strings.Join(rows, "\n")
}

// find returns the screen position of the first occurrence of s.
func (h *harness) find(s string) (int, int) {
	h.t.Helper()
	_, w, hh := h.sim.GetContents()
	for y := 0; y < hh; y++ {
		if i := strings.Index(h.row(y), s); i >= 0 {
			// convert byte offset to cell column (rows here are ASCII-left)
			return len([]rune(h.row(y)[:i])), y
		}
	}
	h.t.Fatalf("%q not on screen:\n%s", s, h.screen())
	_ = w
	return 0, 0
}

// rec records callbacks.
type rec struct {
	mu                            sync.Mutex
	sel, cancel, filter, hl, menu []string
}

func (r *rec) add(dst *[]string, s string) { r.mu.Lock(); *dst = append(*dst, s); r.mu.Unlock() }
func (r *rec) get(dst *[]string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), *dst...)
}

func wire(p *Picker) *rec {
	r := &rec{}
	p.OnSelect = func(id string) { r.add(&r.sel, id) }
	p.OnCancel = func() { r.add(&r.cancel, "x") }
	p.OnFilter = func(s string) { r.add(&r.filter, s) }
	p.OnHighlight = func(id string) { r.add(&r.hl, id) }
	p.OnRowMenu = func(id string, x, y int) { r.add(&r.menu, fmt.Sprintf("%s@%d,%d", id, x, y)) }
	return r
}

func items(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{ID: fmt.Sprint("id", i), Label: fmt.Sprintf("item %02d", i), Hint: fmt.Sprint("h", i)}
	}
	return out
}

// popup opens p over a blank base at w×h and returns the harness + overlay.
func popup(t *testing.T, p *Picker, w, h int) (*harness, *widgets.Overlay) {
	t.Helper()
	ov := widgets.NewOverlay(tview.NewBox())
	hs := newHarness(t, ov, w, h)
	hs.do(func() { p.Open(ov) })
	return hs, ov
}

func TestPlacementAndLook(t *testing.T) {
	p := New("Pick a thing")
	p.Filterable = true
	p.Footer = "type to filter · ↑↓ select · Enter confirm · Esc cancel"
	p.SetItems(items(3))
	hs, _ := popup(t, p, 120, 40)
	x, y, w, h := p.GetRect()
	// 120-10 = 110 → capped at 100; 3 items → list of 3 rows + 9 chrome
	// (border 2, padding 2, title, filter, blank, blank, footer).
	if w != 100 || h != 12 || x != 10 || y != (40-12)/2 {
		t.Fatalf("rect = %d,%d %dx%d", x, y, w, h)
	}
	scr := hs.screen()
	for _, want := range []string{"╭", "╯", "Pick a thing", "filter: ▌", "▸ item 00", "  item 01", "h2", "type to filter"} {
		if !strings.Contains(scr, want) {
			t.Fatalf("missing %q:\n%s", want, scr)
		}
	}
	// Hint is right-aligned against the list's right edge.
	hx, hy := hs.find("h1")
	if hx+2 != x+w-1-padX || hs.row(hy)[0] == 'x' {
		t.Fatalf("hint at col %d, list ends at %d", hx, x+w-1-padX)
	}
	// Narrow terminal: never wider than the screen; min 62 otherwise.
	_, _, w2, _ := placed(p, widgets.Rect{X: 0, Y: 0, W: 70, H: 30})
	_, _, w3, _ := placed(p, widgets.Rect{X: 0, Y: 0, W: 50, H: 30})
	if w2 != 62 || w3 != 50 {
		t.Fatalf("widths %d %d", w2, w3)
	}
}

func placed(p *Picker, b widgets.Rect) (int, int, int, int) {
	p.Place(b)
	return p.GetRect()
}

func TestKeysParity(t *testing.T) {
	p := New("t")
	p.SetItems([]Item{{ID: "a", Label: "a"}, {ID: "b", Label: "b", Disabled: true}, {ID: "c", Label: "c"}})
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	if p.SelectedID() != "a" {
		t.Fatal("first enabled not highlighted")
	}
	hs.key(tcell.KeyDown) // skips disabled b
	if p.SelectedID() != "c" {
		t.Fatalf("down → %s", p.SelectedID())
	}
	hs.key(tcell.KeyDown) // wraps
	if p.SelectedID() != "a" {
		t.Fatalf("wrap down → %s", p.SelectedID())
	}
	hs.key(tcell.KeyUp) // wraps up
	if p.SelectedID() != "c" {
		t.Fatalf("wrap up → %s", p.SelectedID())
	}
	hs.key(tcell.KeyCtrlP)
	hs.key(tcell.KeyCtrlN)
	if p.SelectedID() != "c" {
		t.Fatalf("ctrl+p/n → %s", p.SelectedID())
	}
	hs.key(tcell.KeyHome)
	if p.SelectedID() != "a" {
		t.Fatal("Home")
	}
	hs.key(tcell.KeyEnd)
	if p.SelectedID() != "c" {
		t.Fatal("End")
	}
	hs.key(tcell.KeyEnter)
	if got := r.get(&r.sel); len(got) != 1 || got[0] != "c" || p.IsOpen() {
		t.Fatalf("Enter: sel=%v open=%v", got, p.IsOpen())
	}
	if len(r.get(&r.cancel)) != 0 {
		t.Fatal("selection fired OnCancel")
	}
}

func TestEscAndCtrlCCancel(t *testing.T) {
	p := New("t")
	p.SetItems(items(2))
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	hs.key(tcell.KeyEsc)
	if len(r.get(&r.cancel)) != 1 || p.IsOpen() || len(r.get(&r.sel)) != 0 {
		t.Fatalf("Esc: cancel=%v open=%v", r.get(&r.cancel), p.IsOpen())
	}
	// Ctrl+C: tview.Application stops itself on Ctrl+C before any
	// primitive sees it (unless the runtime's input capture handles it),
	// so drive the handler directly.
	p2 := New("t")
	p2.SetItems(items(2))
	r2 := wire(p2)
	p2.InputHandler()(tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModCtrl), func(tview.Primitive) {})
	if len(r2.get(&r2.cancel)) != 1 {
		t.Fatal("Ctrl+C did not cancel")
	}
}

func TestFilterTypingBackspaceAndNoMatch(t *testing.T) {
	p := New("t")
	p.Filterable = true
	p.SetItems([]Item{
		{ID: "1", Label: "Claude Opus", Keywords: "anthropic"},
		{ID: "2", Label: "\x1b[1mGPT\x1b[0m five", Hint: "openai"},
		{ID: "3", Label: "Tiếng Việt"},
	})
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	hs.typ("ANTH") // case-insensitive, Keywords match
	if ids := p.VisibleIDs(); len(ids) != 1 || ids[0] != "1" {
		t.Fatalf("anth → %v", ids)
	}
	if !strings.Contains(hs.screen(), "filter: ANTH▌") {
		t.Fatal("filter line not drawn")
	}
	for range 4 {
		hs.key(tcell.KeyBackspace2)
	}
	hs.typ("open") // hint match (SearchHints default)
	if ids := p.VisibleIDs(); len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("open → %v", ids)
	}
	hs.do(func() { p.SetFilter("") })
	hs.typ("gpt five") // label match through ANSI, with a space
	if ids := p.VisibleIDs(); len(ids) != 1 || ids[0] != "2" {
		t.Fatalf("gpt five → %v", ids)
	}
	hs.do(func() { p.SetFilter("") })
	hs.typ("việt")
	hs.key(tcell.KeyBackspace2) // rune-wise
	if p.Filter() != "việ" {
		t.Fatalf("backspace → %q", p.Filter())
	}
	hs.typ("zzz")
	if !strings.Contains(hs.screen(), "— no match —") || p.SelectedID() != "" {
		t.Fatalf("no match:\n%s", hs.screen())
	}
	hs.key(tcell.KeyEnter) // nothing to activate
	if len(r.get(&r.sel)) != 0 {
		t.Fatal("Enter on empty list selected")
	}
	if f := r.get(&r.filter); len(f) == 0 || f[len(f)-1] != "việzzz" {
		t.Fatalf("OnFilter = %v", f)
	}
}

func TestFilterExternal(t *testing.T) {
	p := New("market")
	p.Filterable = true
	p.FilterMode = FilterExternal
	p.SetItems(items(3))
	var asked []string
	p.OnFilter = func(s string) { asked = append(asked, s) }
	hs, _ := popup(t, p, 100, 30)
	hs.typ("zz")
	if len(p.VisibleIDs()) != 3 {
		t.Fatal("external mode filtered locally")
	}
	hs.do(func() { p.SetItems([]Item{{ID: "r", Label: "result"}}) })
	if !strings.Contains(hs.screen(), "▸ result") || strings.Join(asked, ",") != "z,zz" {
		t.Fatalf("asked=%v\n%s", asked, hs.screen())
	}
}

func TestMouseHoverClickRightClick(t *testing.T) {
	p := New("t")
	p.SetItems([]Item{{ID: "a", Label: "alpha"}, {ID: "b", Label: "bravo", Disabled: true}, {ID: "c", Label: "charlie"}})
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	cx, cy := hs.find("charlie")
	hs.move(cx, cy)
	if p.SelectedID() != "c" || !strings.Contains(hs.row(cy), "▸ charlie") {
		t.Fatalf("hover: %s\n%s", p.SelectedID(), hs.row(cy))
	}
	bx, by := hs.find("bravo")
	hs.move(bx, by) // disabled: highlight stays
	hs.click(bx, by)
	if p.SelectedID() != "c" || len(r.get(&r.sel)) != 0 {
		t.Fatal("disabled row reacted")
	}
	hs.rclick(bx, by) // right-click works on any row (menus may explain why disabled)
	if m := r.get(&r.menu); len(m) != 1 || m[0] != fmt.Sprintf("b@%d,%d", bx, by) {
		t.Fatalf("OnRowMenu = %v", m)
	}
	if !p.IsOpen() {
		t.Fatal("right-click closed the picker")
	}
	ax, ay := hs.find("alpha")
	hs.click(ax, ay) // single click activates
	if s := r.get(&r.sel); len(s) != 1 || s[0] != "a" || p.IsOpen() {
		t.Fatalf("click: sel=%v open=%v", s, p.IsOpen())
	}
}

func TestClickOutsideCancels(t *testing.T) {
	p := New("t")
	p.SetItems(items(3))
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	hs.click(0, 0)
	if p.IsOpen() || len(r.get(&r.cancel)) != 1 {
		t.Fatalf("outside click: open=%v cancel=%v", p.IsOpen(), r.get(&r.cancel))
	}
}

func TestWheelScrollbarAndKeepVisible(t *testing.T) {
	p := New("long")
	p.SetItems(items(100))
	hs, _ := popup(t, p, 100, 30) // win = min(max(30-10,12),20) = 20
	if p.lh != 20 {
		t.Fatalf("list rows = %d", p.lh)
	}
	scr := hs.screen()
	if !strings.Contains(scr, "┃") || !strings.Contains(scr, "item 19") || strings.Contains(scr, "item 20") {
		t.Fatalf("first page / scrollbar:\n%s", scr)
	}
	x, y, _, _ := p.GetRect()
	hs.wheel(x+10, y+8)
	if p.top != 3 || !strings.Contains(hs.screen(), "item 22") {
		t.Fatalf("wheel top=%d", p.top)
	}
	hs.wheelUp(x+10, y+8)
	if p.top != 0 {
		t.Fatalf("wheel up top=%d", p.top)
	}
	// The wheel reports carried a position, so the pointer hovered a row
	// there; Home resets before paging.
	hs.key(tcell.KeyHome)
	hs.key(tcell.KeyPgDn)
	if p.SelectedID() != "id19" {
		t.Fatalf("PgDn → %s", p.SelectedID())
	}
	hs.key(tcell.KeyEnd)
	if p.top != 80 || !strings.Contains(hs.screen(), "▸ item 99") {
		t.Fatalf("End top=%d", p.top)
	}
	hs.key(tcell.KeyDown) // wrap to top scrolls back
	if p.top != 0 || p.SelectedID() != "id0" {
		t.Fatalf("wrap top=%d sel=%s", p.top, p.SelectedID())
	}
	// Scrollbar: click the bottom of the track → last page.
	hs.click(p.sbX, p.ly+p.lh-1)
	if p.top != 80 {
		t.Fatalf("track click top=%d", p.top)
	}
	// Drag the thumb back to the top.
	hs.mouse(p.sbX, p.ly+p.lh-1, tcell.ButtonPrimary)
	hs.mouse(p.sbX, p.ly, tcell.ButtonPrimary)
	hs.mouse(p.sbX, p.ly, tcell.ButtonNone)
	if p.top != 0 {
		t.Fatalf("drag top=%d", p.top)
	}
}

func TestSectionsHeadersAndInlineHints(t *testing.T) {
	p := New("settings")
	p.HintMode = HintInline
	p.ShowPosition = true
	p.Footer = "Enter change"
	p.CloseOnSelect = false
	p.SetItems([]Item{
		{ID: "s1", Label: "Steering", Hint: "one-at-a-time", Section: "agent"},
		{ID: "s2", Label: "Follow-up", Hint: "all", Section: "agent"},
		{ID: "t1", Label: "Theme", Hint: "dracula", Section: "look"},
	})
	r := wire(p)
	hs, _ := popup(t, p, 100, 30)
	scr := hs.screen()
	for _, want := range []string{"  AGENT", "  LOOK", "▸ Steering  — one-at-a-time", "Enter change (1/3)"} {
		if !strings.Contains(scr, want) {
			t.Fatalf("missing %q:\n%s", want, scr)
		}
	}
	hs.key(tcell.KeyEnd)
	hs.key(tcell.KeyEnter) // settings lists stay open
	if s := r.get(&r.sel); len(s) != 1 || s[0] != "t1" || !p.IsOpen() {
		t.Fatalf("stay open: sel=%v open=%v", s, p.IsOpen())
	}
	if !strings.Contains(hs.screen(), "(3/3)") {
		t.Fatal("position not updated")
	}
	// Clicking a header does nothing.
	hx, hy := hs.find("AGENT")
	hs.click(hx, hy)
	if len(r.get(&r.sel)) != 1 {
		t.Fatal("header click selected")
	}
}

func TestANSILabelOnHighlightAndLiteralBrackets(t *testing.T) {
	p := New("t")
	p.Styles.Selected = tcell.StyleDefault.Background(tcell.ColorBlue).Bold(true)
	p.SetItems([]Item{{ID: "a", Label: "\x1b[31mred\x1b[0m [x] plain"}})
	hs, _ := popup(t, p, 100, 30)
	x, y := hs.find("red")
	_, _, st, _ := hs.sim.GetContent(x, y)
	fg, bg, at := st.Decompose()
	if fg != tcell.PaletteColor(1) || bg != tcell.ColorBlue || at&tcell.AttrBold == 0 {
		t.Fatalf("label cell fg=%v bg=%v attr=%v", fg, bg, at)
	}
	px, _ := hs.find("plain")
	_, _, st, _ = hs.sim.GetContent(px, y)
	if _, bg, _ := st.Decompose(); bg != tcell.ColorBlue {
		t.Fatal("plain part lost the highlight")
	}
	if !strings.Contains(hs.row(y), "red [x] plain") {
		t.Fatalf("row = %q", hs.row(y))
	}
}

func TestTruncationWithEllipsis(t *testing.T) {
	p := New("t")
	p.SetItems([]Item{{ID: "a", Label: strings.Repeat("日本", 60), Hint: "hint"}})
	hs, _ := popup(t, p, 80, 30)
	_, y := hs.find("▸")
	row := hs.row(y)
	if !strings.Contains(row, "…") || !strings.Contains(row, "hint") {
		t.Fatalf("row = %q", row)
	}
}

func TestOnKeyFirstAndPaste(t *testing.T) {
	p := New("sessions")
	p.Filterable = true
	p.SetItems(items(3))
	var tabs int
	p.OnKey = func(e *tcell.EventKey) bool {
		if e.Key() == tcell.KeyTab {
			tabs++
			return true
		}
		return false
	}
	hs, _ := popup(t, p, 100, 30)
	hs.key(tcell.KeyTab)
	if tabs != 1 {
		t.Fatal("OnKey not called")
	}
	hs.do(func() { p.PasteHandler()("item 01\nignored", func(tview.Primitive) {}) })
	if p.Filter() != "item 01" || len(p.VisibleIDs()) != 1 {
		t.Fatalf("paste → %q %v", p.Filter(), p.VisibleIDs())
	}
}

func TestSetItemsKeepsSelectionAndFullscreen(t *testing.T) {
	p := New("t")
	p.SetItems(items(10))
	if !p.SetSelected("id7") || p.SetSelected("nope") {
		t.Fatal("SetSelected")
	}
	p.SetItems(items(12))
	if p.SelectedID() != "id7" {
		t.Fatalf("kept = %s", p.SelectedID())
	}
	p.SetFullscreen(true)
	if _, _, w, h := placed(p, widgets.Rect{X: 0, Y: 0, W: 90, H: 40}); w != 90 || h != 40 {
		t.Fatalf("fullscreen %dx%d", w, h)
	}
	// As a plain page (no overlay): fills its rect, Esc still cancels.
	p2 := New("page")
	p2.SetItems(items(5))
	r := wire(p2)
	hs := newHarness(t, p2, 70, 20)
	if !strings.Contains(hs.screen(), "item 04") {
		t.Fatalf("page:\n%s", hs.screen())
	}
	hs.key(tcell.KeyEsc)
	if len(r.get(&r.cancel)) != 1 {
		t.Fatal("page Esc did not cancel")
	}
	hs.click(5, 6) // row "▸ item 01"? — click activates whatever row is there
	if len(r.get(&r.sel)) != 1 {
		t.Fatalf("page click: %v\n%s", r.get(&r.sel), hs.screen())
	}
}

// --- perf: 5k items ------------------------------------------------------------

func bigItems(n int) []Item {
	out := make([]Item, n)
	for i := range out {
		out[i] = Item{
			ID: fmt.Sprint(i), Label: fmt.Sprintf("\x1b[1mprovider-%d\x1b[0m/model-%05d", i%40, i),
			Hint: fmt.Sprintf("%dk ctx", 8+i%200), Section: fmt.Sprint("group ", i/500),
			Keywords: "anthropic openai google",
		}
	}
	return out
}

func TestPerf5k(t *testing.T) {
	p := New("models")
	p.Filterable = true
	scr := tcell.NewSimulationScreen("UTF-8")
	if err := scr.Init(); err != nil {
		t.Fatal(err)
	}
	defer scr.Fini()
	scr.SetSize(120, 40)
	p.Place(widgets.Rect{X: 0, Y: 0, W: 120, H: 40})
	start := time.Now()
	p.SetItems(bigItems(5000))
	setItems := time.Since(start)
	start = time.Now()
	for _, r := range "model-04" { // 8 keystrokes, each a full refilter
		p.editFilter(p.text + string(r))
		p.Draw(scr)
	}
	typing := time.Since(start) / 8
	start = time.Now()
	for i := 0; i < 100; i++ {
		p.SetFilter("")
		p.step(1)
		p.Draw(scr)
	}
	frame := time.Since(start) / 100
	t.Logf("5k: SetItems %v, keystroke+draw %v, refilter+draw %v", setItems, typing, frame)
	if typing > 16*time.Millisecond || frame > 16*time.Millisecond {
		t.Fatalf("over the 60 fps budget: keystroke %v, frame %v", typing, frame)
	}
}

func BenchmarkKeystroke5k(b *testing.B) {
	p := New("models")
	p.Filterable = true
	p.SetItems(bigItems(5000))
	scr := tcell.NewSimulationScreen("UTF-8")
	_ = scr.Init()
	scr.SetSize(120, 40)
	p.Place(widgets.Rect{X: 0, Y: 0, W: 120, H: 40})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.SetFilter([]string{"m", "mo", "model-0", ""}[i%4])
		p.Draw(scr)
	}
}

func BenchmarkScrollDraw5k(b *testing.B) {
	p := New("models")
	p.SetItems(bigItems(5000))
	scr := tcell.NewSimulationScreen("UTF-8")
	_ = scr.Init()
	scr.SetSize(120, 40)
	p.Place(widgets.Rect{X: 0, Y: 0, W: 120, H: 40})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		p.page(1)
		p.Draw(scr)
	}
}
