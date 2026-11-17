// Package tviewrt runs an ev.Model on rivo/tview.
//
// Stage 1 of the migration ("whole-frame bridge"): the model's View() string
// is painted into a single full-screen primitive through the ANSI→cell
// painter of src/components/cells, and tcell input is translated to ev messages. Regions of the UI
// are then replaced one by one with native tview primitives (see
// docs/tview-migration.md) without the event plumbing changing again.
//
// Threading: every Update runs on tview's event goroutine — input callbacks
// directly, async results via QueueUpdateDraw. Cmds run on their own
// goroutines, exactly like Bubble Tea.
package tviewrt

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
	"pitago/src/tui/picker"
	"pitago/src/tui/widgets"

	"pitago/src/tui/ev"
)

// Painter draws one rendered frame (a View() string) onto the screen region.
type Painter func(scr tcell.Screen, x, y, w, h int, frame string)

// Options wires the runtime. Key and Mouse convert tcell input to ev
// messages; returning ok=false / nil drops the event.
type Options struct {
	Screen tcell.Screen // nil = real terminal (tests pass a SimulationScreen)
	Mouse  bool         // start with all-motion mouse reporting on
	Paint  Painter      // nil = cells.Painter with a fresh cache
	Key    func(*tcell.EventKey) (ev.KeyMsg, bool)
	MouseE func(*tcell.EventMouse, tview.MouseAction) []ev.MouseMsg
	Filter func(ev.Model, ev.Msg) ev.Msg // optional, like tea.WithFilter
	RawOut io.Writer                     // where raw sequences go; nil = the screen tty (or stdout)
}

// Runtime is a running program.
type Runtime struct {
	app  *tview.Application
	opts Options
	root *frame
	ov   *widgets.Overlay  // root primitive: popups (menus, tooltips) above the frame
	bar  *widgets.ClickBar // menu bar row above the frame (nil without ev.MenuBarProvider)
	top  int               // rows above the frame: 1 with a menu bar, else 0

	pickers map[string]*picker.Picker // open native pickers by tag

	mu    sync.Mutex // guards model against Model() readers off-loop
	model ev.Model
	w, h  int
	quit  bool

	rawMu sync.Mutex
	raw   bytes.Buffer // terminal-native sequences (kitty APC) flushed before each Show

	exactPaste bool // the screen is wrapped in keys.PasteScreen (see paste.go)
}

// New prepares a runtime for m; call Run to start it.
func New(m ev.Model, opts Options) *Runtime {
	if opts.Paint == nil {
		opts.Paint = cells.Painter(cells.NewCache(4096))
	}
	r := &Runtime{app: tview.NewApplication(), opts: opts, model: m}
	r.root = &frame{Box: tview.NewBox(), r: r}
	var base tview.Primitive = r.root
	if p, ok := m.(ev.MenuBarProvider); ok && len(p.MenuBar()) > 0 {
		// The model's frame starts one row lower; WindowSizeMsg, mouse Y
		// and menu positions are translated, so the model never knows.
		r.top = 1
		r.bar = widgets.NewClickBar()
		r.bar.OnClick = func(id string, _, _ int) { r.openBarMenu(id) }
		r.syncBar()
		base = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(r.bar, 1, 0, false).
			AddItem(r.root, 0, 1, true)
	}
	r.ov = widgets.NewOverlay(base)
	r.app.SetRoot(r.ov, true).EnableMouse(opts.Mouse).EnablePaste(true)
	if opts.Screen != nil {
		r.app.SetScreen(r.wrapPaste(opts.Screen))
	}
	r.app.SetInputCapture(r.onKey)
	r.app.SetMouseCapture(r.onMouse)
	r.app.SetBeforeDrawFunc(r.beforeDraw)
	r.app.SetAfterDrawFunc(r.afterDraw)
	return r
}

// Raw returns a writer for terminal-native escape sequences that cannot live
// in the cell grid (kitty image upload/placement). Bytes are buffered and
// written to the tty just before the next frame is shown (tview runs
// root.Draw → afterDraw → Show), so a payload precedes the frame that uses it. Safe from any
// goroutine; this is what app.ImageOut is set to under tview.
func (r *Runtime) Raw() io.Writer { return rawWriter{r} }

type rawWriter struct{ r *Runtime }

func (w rawWriter) Write(p []byte) (int, error) {
	w.r.rawMu.Lock()
	defer w.r.rawMu.Unlock()
	return w.r.raw.Write(p)
}

// afterDraw flushes buffered raw sequences before tcell shows the frame (tview
// calls the after-draw hook ahead of Screen.Show; keep it that way).
// Kitty placements use C=1 (cursor does not move) and unicode placeholders,
// so where the cursor sits does not matter.
func (r *Runtime) afterDraw(scr tcell.Screen) {
	r.rawMu.Lock()
	if r.raw.Len() == 0 {
		r.rawMu.Unlock()
		return
	}
	b := append([]byte(nil), r.raw.Bytes()...)
	r.raw.Reset()
	r.rawMu.Unlock()
	if r.opts.RawOut != nil {
		_, _ = r.opts.RawOut.Write(b)
		return
	}
	if tty, ok := scr.Tty(); ok {
		_, _ = tty.Write(b)
		return
	}
	if r.opts.Screen == nil {
		_, _ = os.Stdout.Write(b)
	}
}

// Send delivers msg from any goroutine (the app.Post hook).
func (r *Runtime) Send(msg ev.Msg) {
	r.app.QueueUpdateDraw(func() { r.dispatch(msg) })
}

// Run blocks until the model quits. It returns the final model.
func (r *Runtime) Run() (ev.Model, error) {
	if r.opts.Screen == nil {
		if err := r.openTerminal(); err != nil {
			return r.Model(), err
		}
	}
	if c := r.model.Init(); c != nil {
		r.exec(c)
	}
	err := r.app.Run()
	return r.Model(), err
}

// Stop ends Run from any goroutine.
func (r *Runtime) Stop() { r.app.Stop() }

// Model returns the current model (safe from any goroutine).
func (r *Runtime) Model() ev.Model {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.model
}

// App exposes the tview application for native primitives added later.
func (r *Runtime) App() *tview.Application { return r.app }

// dispatch runs one Update. Must be called on the tview event goroutine.
func (r *Runtime) dispatch(msg ev.Msg) {
	if msg == nil || r.quit {
		return
	}
	switch m := msg.(type) {
	case ev.QuitMsg:
		r.quit = true
		r.app.Stop()
		return
	case ev.MouseModeMsg:
		r.app.EnableMouse(m.All)
		return
	case ev.OpenMenuMsg:
		r.openMenu(m)
		return
	case ev.OpenPickerMsg:
		r.openPicker(m)
		return
	case ev.ClosePickerMsg:
		if p := r.pickers[m.Tag]; p != nil {
			p.Close()
		}
		return
	case ev.BatchMsg:
		for _, c := range m {
			r.exec(c)
		}
		return
	case ev.SequenceMsg:
		r.execSeq(m)
		return
	}
	if r.opts.Filter != nil {
		if msg = r.opts.Filter(r.model, msg); msg == nil {
			return
		}
	}
	r.mu.Lock()
	nm, cmd := r.model.Update(msg)
	r.model = nm
	r.mu.Unlock()
	r.exec(cmd)
}

// exec runs c off the event loop and feeds its result back.
func (r *Runtime) exec(c ev.Cmd) {
	if c == nil {
		return
	}
	go func() {
		if msg := c(); msg != nil {
			r.Send(msg)
		}
	}()
}

// execSeq runs cmds in order on one goroutine; a nested Batch/Sequence
// result is dispatched (and so started) before the next cmd runs.
func (r *Runtime) execSeq(cmds []ev.Cmd) {
	go func() {
		for _, c := range cmds {
			if c == nil {
				continue
			}
			msg := c()
			if msg == nil {
				continue
			}
			done := make(chan struct{})
			r.app.QueueUpdateDraw(func() { r.dispatch(msg); close(done) })
			<-done
			if _, q := msg.(ev.QuitMsg); q {
				return
			}
		}
	}()
}

// beforeDraw keeps the model's idea of the terminal size in sync. tview
// calls it on the event goroutine right before every draw, including the
// first one and the one after a resize.
func (r *Runtime) beforeDraw(scr tcell.Screen) bool {
	w, h := scr.Size()
	if w != r.w || h != r.h {
		r.w, r.h = w, h
		r.dispatch(ev.WindowSizeMsg{Width: w, Height: h - r.top})
	}
	r.syncBar()
	return false
}

func (r *Runtime) onKey(e *tcell.EventKey) *tcell.EventKey {
	// An open menu owns the keyboard (tview routes to the overlay's top
	// modal popup).
	if r.ov.Modal() {
		// tview stops the whole application on an unmodified Ctrl+C that
		// leaves the capture as the original event. Inside a popup Ctrl+C
		// means "cancel this popup", like Esc.
		if e.Key() == tcell.KeyCtrlC {
			return tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
		}
		return e
	}
	if r.opts.Key == nil {
		return e
	}
	// F10 opens the menu bar, as in most editors.
	if e.Key() == tcell.KeyF10 && r.bar != nil && len(r.bar.Segments) > 0 {
		r.openBarMenu(r.bar.Segments[0].ID)
		return nil
	}
	if k, ok := r.opts.Key(e); ok {
		r.dispatch(k)
	}
	return nil
}

// onMouse must hand the event back: tview reuses the capture's return value
// for every action of one report, so returning nil on the Move would blank
// the Down/Up that follows. The frame's MouseHandler then reports it
// consumed, which is what makes tview redraw.
func (r *Runtime) onMouse(e *tcell.EventMouse, a tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	// tcell clamps coordinates to 0,0 until its first resize lands.
	// An open menu owns the pointer; the overlay routes the event to it
	// (and closes it on a press outside, swallowing that click).
	if r.opts.MouseE == nil || r.w == 0 || r.ov.Modal() {
		return e, a
	}
	// The menu bar row belongs to the bar (tview routes it there).
	if _, y := e.Position(); y < r.top {
		return e, a
	}
	for _, m := range r.opts.MouseE(e, a) {
		m.Y -= r.top
		r.dispatch(m)
	}
	return e, a
}

// MouseHandler swallows every mouse action (the model already saw it via
// onMouse) and marks it consumed so the frame is redrawn.
func (f *frame) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
		return true, nil
	}
}

// frame is the single full-screen primitive of stage 1.
type frame struct {
	*tview.Box
	r *Runtime
}

func (f *frame) Draw(scr tcell.Screen) {
	f.DrawForSubclass(scr, f)
	x, y, w, h := f.GetInnerRect()
	f.r.mu.Lock()
	view := f.r.model.View()
	f.r.mu.Unlock()
	if strings.Contains(view, "\x1b_G") || strings.Contains(view, "\x1b]1337;") {
		for _, s := range cells.Passthrough(view) {
			_, _ = io.WriteString(f.r.Raw(), s)
		}
	}
	f.r.opts.Paint(scr, x, y, w, h, view)
}

// PasteHandler turns a bracketed paste into one KeyRunes/Paste message,
// matching Bubble Tea v1.
func (f *frame) PasteHandler() func(string, func(tview.Primitive)) {
	return func(text string, _ func(tview.Primitive)) {
		f.r.dispatch(f.r.pasteMsg(text))
	}
}

// Menus reports that this runtime can show native context menus
// (ev.OpenMenuMsg). The Bubble Tea runtime cannot.
func (r *Runtime) Menus() bool { return true }

// openMenu shows an ev.OpenMenuMsg as a widgets.ContextMenu. The choice is
// dispatched back as ev.MenuResultMsg on the UI goroutine.
func (r *Runtime) openMenu(m ev.OpenMenuMsg) {
	if len(m.Items) == 0 {
		return
	}
	menu := widgets.NewContextMenu(toWidgetItems(m.Items))
	tag := m.Tag
	menu.OnSelect = func(id string) { r.dispatch(ev.MenuResultMsg{Tag: tag, ID: id}) }
	menu.OpenAt(r.ov, m.X, m.Y+r.top)
}

func toWidgetItems(in []ev.MenuItem) []widgets.MenuItem {
	if len(in) == 0 {
		return nil
	}
	out := make([]widgets.MenuItem, len(in))
	for i, it := range in {
		out[i] = widgets.MenuItem{
			ID: it.ID, Label: it.Label, Shortcut: it.Shortcut,
			Disabled: it.Disabled, Separator: it.Separator,
			Submenu: toWidgetItems(it.Submenu),
		}
	}
	return out
}

// syncBar refreshes the menu bar titles from the model.
func (r *Runtime) syncBar() {
	if r.bar == nil {
		return
	}
	p, _ := r.model.(ev.MenuBarProvider)
	var segs []widgets.Segment
	if p != nil {
		for _, mm := range p.MenuBar() {
			segs = append(segs, widgets.Segment{ID: mm.Title, Text: " " + mm.Title + " "})
		}
	}
	r.bar.SetSegments(segs...)
}

// openBarMenu drops down the menu bar entry titled id. Items are read from
// the model at open time, so toggle checkmarks are current.
func (r *Runtime) openBarMenu(id string) {
	p, ok := r.model.(ev.MenuBarProvider)
	if !ok {
		return
	}
	for _, mm := range p.MenuBar() {
		if mm.Title != id || len(mm.Items) == 0 {
			continue
		}
		rect, ok := r.bar.SegmentRect(id)
		if !ok {
			return
		}
		menu := widgets.NewContextMenu(toWidgetItems(mm.Items))
		menu.OnSelect = func(item string) { r.dispatch(ev.MenuResultMsg{Tag: ev.MenuBarTag, ID: item}) }
		menu.OpenBelow(r.ov, rect)
		return
	}
}

// openPicker shows an ev.OpenPickerMsg as a native picker.Picker. Its
// outcome is dispatched back as ev.PickerResultMsg on the UI goroutine.
func (r *Runtime) openPicker(m ev.OpenPickerMsg) {
	if r.pickers == nil {
		r.pickers = map[string]*picker.Picker{}
	}
	if old := r.pickers[m.Tag]; old != nil {
		old.Close()
	}
	p := picker.New(m.Title)
	p.Message, p.Footer, p.Filterable = m.Message, m.Footer, m.Filterable
	items := make([]picker.Item, len(m.Items))
	for i, it := range m.Items {
		items[i] = picker.Item{ID: it.ID, Label: it.Label, Hint: it.Hint, Section: it.Section, Disabled: it.Disabled}
	}
	p.SetItems(items)
	if m.Filter != "" {
		p.SetFilter(m.Filter)
	}
	if m.Selected != "" {
		p.SetSelected(m.Selected)
	}
	tag := m.Tag
	done := false
	finish := func(res ev.PickerResultMsg) {
		if done {
			return
		}
		done = true
		delete(r.pickers, tag)
		r.dispatch(res)
	}
	p.OnSelect = func(id string) { finish(ev.PickerResultMsg{Tag: tag, ID: id}) }
	p.OnCancel = func() { finish(ev.PickerResultMsg{Tag: tag, Cancelled: true}) }
	r.pickers[tag] = p
	p.Open(r.ov)
}
