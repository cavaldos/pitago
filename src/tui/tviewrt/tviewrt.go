// Package tviewrt runs an ev.Model on rivo/tview.
//
// Models can supply native workspace and dialog snapshots. Styled text from
// Markdown/extensions is parsed into tcell cells only in the region that
// displays it. The string painter is retained for unmigrated views.
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
	Filter func(ev.Model, ev.Msg) ev.Msg // optional event filter
	RawOut io.Writer                     // where raw sequences go; nil = the screen tty (or stdout)
}

// Runtime is a running program.
type Runtime struct {
	app  *tview.Application
	opts Options
	root *frame
	ov   *widgets.Overlay // root primitive: popups (menus, tooltips) above the frame
	bar  *widgets.MenuBar // menu bar row above the frame (nil without ev.MenuBarProvider)
	top  int              // rows above the frame: 1 with a menu bar, else 0

	pickers       map[string]*picker.Picker // open native pickers by tag
	settings      *settingsPanel
	workspace     *workspace
	nativeStyles  widgets.MenuBarStyles
	nativePalette ev.NativePalette

	divHover, divDrag string // ev.Divider ID under the pointer / being dragged
	hover             *widgets.Hover
	frameLeftDown     bool

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
	r := &Runtime{app: tview.NewApplication(), opts: opts, model: m, nativeStyles: widgets.DefaultMenuBarStyles()}
	r.root = &frame{Box: tview.NewBox(), r: r}
	r.ov = widgets.NewOverlay(r.root)
	if p, ok := m.(ev.MenuBarProvider); ok && len(p.MenuBar()) > 0 {
		// The model's frame starts one row lower; WindowSizeMsg, mouse Y
		// and menu positions are translated, so the model never knows.
		r.top = 1
		r.bar = widgets.NewMenuBar(r.ov, r.barMenus, func(_, id string) {
			r.dispatch(ev.MenuResultMsg{Tag: ev.MenuBarTag, ID: id})
		})
		r.ov.Base = tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(r.bar, 1, 0, false).
			AddItem(r.root, 0, 1, true)
	}
	r.hover = widgets.NewHover(r.ov, func(f func()) { r.app.QueueUpdateDraw(f) })
	if r.bar != nil {
		r.bar.Hover = r.hover
	}
	if _, ok := m.(ev.SettingsProvider); ok {
		r.settings = newSettingsPanel(r)
	}
	if _, ok := m.(ev.WorkspaceProvider); ok {
		r.workspace = newWorkspace(r.Raw())
	}
	r.app.SetRoot(r.ov, true)
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
	// With a supplied screen, tview only applies these modes once the
	// screen is installed; Run doesn't apply options set before SetScreen.
	r.app.EnableMouse(r.opts.Mouse).EnablePaste(true)
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
	r.syncNativeTheme()
	return false
}

func (r *Runtime) onKey(e *tcell.EventKey) *tcell.EventKey {
	// An open menu owns the keyboard (tview routes to the overlay's top
	// modal popup).
	// The menu bar owns F10, Alt+mnemonics and, while one of its menus is
	// open, ←/→/Esc.
	if r.bar != nil && r.bar.HandleKey(e) {
		return nil
	}
	if r.ov.Modal() {
		// tview stops the whole application on an unmodified Ctrl+C that
		// leaves the capture as the original event. Inside a popup Ctrl+C
		// means "cancel this popup", like Esc.
		if e.Key() == tcell.KeyCtrlC {
			return tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone)
		}
		return e
	}
	if p := r.settings; p != nil && p.active && p.input(e) {
		return nil
	}
	if r.opts.Key == nil {
		return e
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
	if r.w == 0 || r.ov.Modal() {
		return e, a
	}
	if p := r.settings; p != nil && p.active {
		r.frameLeftDown, r.divHover, r.divDrag = false, "", ""
		return e, a
	}
	if r.opts.MouseE == nil {
		return e, a
	}
	x, y := e.Position()
	// A drag that started in the frame must reach its original owner even
	// when the pointer crosses the menu bar, which owns ordinary clicks.
	frameDrag := r.frameLeftDown
	if a == tview.MouseLeftDown {
		r.frameLeftDown = y >= r.top
	}
	if a == tview.MouseLeftUp {
		r.frameLeftDown = false
		r.divDrag = ""
		if !frameDrag {
			return e, a
		}
	}
	if y < r.top {
		r.divHover = ""
		if !frameDrag || (a != tview.MouseMove && a != tview.MouseLeftUp) {
			return e, a
		}
	} else {
		r.trackDivider(x, y-r.top, a)
		if a == tview.MouseMove {
			r.updateTooltip(x, y-r.top)
		}
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
	return func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		if p := f.r.settings; p != nil && p.active {
			p.mouse(a, e)
		}
		return true, nil
	}
}

// frame hosts the workspace or the active full-screen dialog.
type frame struct {
	*tview.Box
	r *Runtime
}

func (f *frame) Draw(scr tcell.Screen) {
	_, bg, _ := f.r.nativeStyles.Normal.Decompose()
	f.SetBackgroundColor(bg)
	f.DrawForSubclass(scr, f)
	// Every frame starts with the cursor hidden; whatever owns the caret this
	// frame (the composer, a picker filter) shows it at its own position.
	scr.HideCursor()
	x, y, w, h := f.GetInnerRect()
	if p := f.r.settings; p != nil {
		if provider, ok := f.r.model.(ev.SettingsProvider); ok {
			if v, active := provider.NativeSettings(); active {
				p.draw(scr, x, y, w, h, v, f.r.nativeStyles)
				return
			}
		}
		p.active = false
	}
	// A painted dialog (the old bubble-tea renderer) owns the whole frame:
	// model.View composes the workspace behind it and the dialog on top.
	if pd, ok := f.r.model.(ev.PaintedDialogProvider); ok && pd.PaintedDialog() {
		f.r.paintView(scr, x, y, w, h)
		return
	}
	if p, ok := f.r.model.(ev.WorkspaceProvider); f.r.workspace != nil && ok {
		f.r.workspace.draw(scr, x, y, w, h, p.NativeWorkspace(), f.r.nativeStyles, f.r.nativePalette)
		f.r.paintDividers(scr, x, y, h)
		return
	}
	f.r.paintView(scr, x, y, w, h)
}

// paintView draws the model's full painted view (workspace + dialog overlay
// composed as text), the rendering the Bubble Tea runtime used.
func (r *Runtime) paintView(scr tcell.Screen, x, y, w, h int) {
	r.mu.Lock()
	view := r.model.View()
	r.mu.Unlock()
	if strings.Contains(view, "\x1b_G") || strings.Contains(view, "\x1b]1337;") {
		for _, s := range cells.Passthrough(view) {
			_, _ = io.WriteString(r.Raw(), s)
		}
	}
	r.opts.Paint(scr, x, y, w, h, view)
	r.paintDividers(scr, x, y, h)
}

// PasteHandler turns a bracketed paste into one KeyRunes/Paste message,
// matching Bubble Tea v1.
func (f *frame) PasteHandler() func(string, func(tview.Primitive)) {
	return func(text string, _ func(tview.Primitive)) {
		msg := f.r.pasteMsg(text)
		if p := f.r.settings; p != nil && p.active {
			p.paste(string(msg.Runes))
			return
		}
		f.r.dispatch(msg)
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

// barMenus maps the model's menu bar for widgets.MenuBar (read at draw
// and open time, so ✓ toggles stay current).
func (r *Runtime) barMenus() []widgets.BarMenu {
	p, ok := r.model.(ev.MenuBarProvider)
	if !ok {
		return nil
	}
	var out []widgets.BarMenu
	for _, mm := range p.MenuBar() {
		out = append(out, widgets.BarMenu{Title: mm.Title, Tooltip: mm.Tooltip, Items: toWidgetItems(mm.Items)})
	}
	return out
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

// trackDivider records which model divider (ev.DividerProvider) the pointer
// hovers or drags, for visual feedback only: the drag itself is the
// model's (it sees the same mouse messages).
func (r *Runtime) trackDivider(x, y int, a tview.MouseAction) {
	r.divHover = ""
	p, ok := r.model.(ev.DividerProvider)
	if !ok {
		return
	}
	for _, d := range p.Dividers() {
		if x == d.X && y >= d.Y && y < d.Y+d.H {
			r.divHover = d.ID
			if a == tview.MouseLeftDown {
				r.divDrag = d.ID
			}
		}
	}
}

// paintDividers restyles the hovered/dragged divider column: │ → ┃.
func (r *Runtime) paintDividers(scr tcell.Screen, ox, oy, h int) {
	if r.divHover == "" && r.divDrag == "" {
		return
	}
	p, ok := r.model.(ev.DividerProvider)
	if !ok {
		return
	}
	st := widgets.DefaultSplitterStyles()
	for _, d := range p.Dividers() {
		style := st.Hover
		switch d.ID {
		case r.divDrag:
			style = st.Drag
		case r.divHover:
		default:
			continue
		}
		for y := d.Y; y < d.Y+d.H && y < h; y++ {
			c, comb, _, _ := scr.GetContent(ox+d.X, oy+y)
			switch c {
			case '│':
				c = '┃'
			case '╭', '╮':
				c = '┏'
			case '╰', '╯':
				c = '┗'
			}
			scr.SetContent(ox+d.X, oy+y, c, comb, style)
		}
	}
}

// updateTooltip asks the model (ev.TooltipProvider) what is under the
// pointer and shows it after the hover delay.
func (r *Runtime) updateTooltip(x, y int) {
	p, ok := r.model.(ev.TooltipProvider)
	if !ok || r.hover == nil {
		return
	}
	key, rc, text := p.Tooltip(x, y)
	if key == "" || text == "" {
		r.hover.Out()
		return
	}
	r.hover.Over("frame:"+key, widgets.Rect{X: rc.X, Y: rc.Y + r.top, W: rc.W, H: rc.H}, text)
}
