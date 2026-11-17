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

	mu    sync.Mutex // guards model against Model() readers off-loop
	model ev.Model
	w, h  int
	quit  bool

	rawMu sync.Mutex
	raw   bytes.Buffer // terminal-native sequences (kitty APC) flushed after each Show
}

// New prepares a runtime for m; call Run to start it.
func New(m ev.Model, opts Options) *Runtime {
	if opts.Paint == nil {
		opts.Paint = cells.Painter(cells.NewCache(4096))
	}
	r := &Runtime{app: tview.NewApplication(), opts: opts, model: m}
	r.root = &frame{Box: tview.NewBox(), r: r}
	r.app.SetRoot(r.root, true).EnableMouse(opts.Mouse).EnablePaste(true)
	if opts.Screen != nil {
		r.app.SetScreen(opts.Screen)
	}
	r.app.SetInputCapture(r.onKey)
	r.app.SetMouseCapture(r.onMouse)
	r.app.SetBeforeDrawFunc(r.beforeDraw)
	r.app.SetAfterDrawFunc(r.afterDraw)
	return r
}

// Raw returns a writer for terminal-native escape sequences that cannot live
// in the cell grid (kitty image upload/placement). Bytes are buffered and
// written to the tty right after the next frame is shown. Safe from any
// goroutine; this is what app.ImageOut is set to under tview.
func (r *Runtime) Raw() io.Writer { return rawWriter{r} }

type rawWriter struct{ r *Runtime }

func (w rawWriter) Write(p []byte) (int, error) {
	w.r.rawMu.Lock()
	defer w.r.rawMu.Unlock()
	return w.r.raw.Write(p)
}

// afterDraw flushes buffered raw sequences once tcell has written the frame.
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
		r.dispatch(ev.WindowSizeMsg{Width: w, Height: h})
	}
	return false
}

func (r *Runtime) onKey(e *tcell.EventKey) *tcell.EventKey {
	if r.opts.Key == nil {
		return e
	}
	if k, ok := r.opts.Key(e); ok {
		r.dispatch(k)
	}
	return nil
}

func (r *Runtime) onMouse(e *tcell.EventMouse, a tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
	if r.opts.MouseE == nil {
		return nil, 0
	}
	for _, m := range r.opts.MouseE(e, a) {
		r.dispatch(m)
	}
	return nil, 0
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
		f.r.dispatch(ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune(text), Paste: true})
	}
}
