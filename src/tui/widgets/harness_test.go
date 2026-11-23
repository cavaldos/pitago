package widgets

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// harness runs a real tview.Application on a SimulationScreen. Mouse and
// key events go through tcell's queue and tview's own action derivation
// (Down/Up/Click/DoubleClick/Move), exactly as on a terminal. sync() makes
// every step deterministic: it injects a sentinel key behind the event and
// waits for the app's input capture to see it, so everything before it has
// been handled.
type harness struct {
	t   *testing.T
	sim tcell.SimulationScreen
	app *tview.Application

	sentinel chan struct{}
	done     chan struct{}
}

const sentinelKey = tcell.KeyF64

func newHarness(t *testing.T, root tview.Primitive, w, h int) *harness {
	t.Helper()
	old := tview.DoubleClickInterval
	tview.DoubleClickInterval = 0 // every click is a single click unless a test says otherwise
	t.Cleanup(func() { tview.DoubleClickInterval = old })

	sim := tcell.NewSimulationScreen("UTF-8")
	if err := sim.Init(); err != nil {
		t.Fatal(err)
	}
	hs := &harness{t: t, sim: sim, sentinel: make(chan struct{}, 1), done: make(chan struct{})}
	hs.app = tview.NewApplication().SetScreen(sim).EnableMouse(true).SetRoot(root, true)
	sim.SetSize(w, h) // after SetScreen: it calls Init, which resets a sim screen to 80x25
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
	h.app.QueueUpdateDraw(func() {}) // and paint
}

func (h *harness) mouse(x, y int, b tcell.ButtonMask) {
	h.sim.InjectMouse(x, y, b, tcell.ModNone)
	h.sync()
}

func (h *harness) move(x, y int)    { h.mouse(x, y, tcell.ButtonNone) }
func (h *harness) press(x, y int)   { h.mouse(x, y, tcell.ButtonPrimary) }
func (h *harness) release(x, y int) { h.mouse(x, y, tcell.ButtonNone) }
func (h *harness) click(x, y int)   { h.press(x, y); h.release(x, y) }
func (h *harness) rpress(x, y int)  { h.mouse(x, y, tcell.ButtonSecondary) }
func (h *harness) rclick(x, y int)  { h.rpress(x, y); h.release(x, y) }
func (h *harness) wheel(x, y int)   { h.mouse(x, y, tcell.WheelDown) }
func (h *harness) drag(x, y int)    { h.mouse(x, y, tcell.ButtonPrimary) }
func (h *harness) key(k tcell.Key)  { h.sim.InjectKey(k, 0, tcell.ModNone); h.sync() }
func (h *harness) do(f func())      { h.app.QueueUpdateDraw(f) }

// row is screen row y as text (wide cells' tails skipped).
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

func (h *harness) style(x, y int) tcell.Style {
	_, _, st, _ := h.sim.GetContent(x, y)
	return st
}

// recorder is a base primitive that logs the mouse actions it receives.
type recorder struct {
	*tview.Box
	mu   sync.Mutex
	acts []tview.MouseAction
	keys []tcell.Key
}

func newRecorder() *recorder { return &recorder{Box: tview.NewBox()} }

func (r *recorder) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		if !r.InRect(e.Position()) {
			return false, nil
		}
		r.mu.Lock()
		r.acts = append(r.acts, a)
		r.mu.Unlock()
		return true, nil
	}
}

func (r *recorder) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return func(e *tcell.EventKey, _ func(tview.Primitive)) {
		r.mu.Lock()
		r.keys = append(r.keys, e.Key())
		r.mu.Unlock()
	}
}

func (r *recorder) take() []tview.MouseAction {
	r.mu.Lock()
	defer r.mu.Unlock()
	a := r.acts
	r.acts = nil
	return a
}

func (r *recorder) count(a tview.MouseAction) int {
	n := 0
	for _, x := range r.take() {
		if x == a {
			n++
		}
	}
	return n
}

// fakeClock is a Hover.AfterFunc that fires only when told to.
type fakeClock struct {
	mu      sync.Mutex
	pending []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) AfterFunc(d time.Duration, f func()) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	c.pending = append(c.pending, t)
	return func() { c.mu.Lock(); t.stopped = true; c.mu.Unlock() }
}

// fire runs every live timer from a foreign goroutine, as time.AfterFunc
// would, and waits for it.
func (c *fakeClock) fire() int {
	c.mu.Lock()
	ts := c.pending
	c.pending = nil
	c.mu.Unlock()
	n := 0
	for _, t := range ts {
		c.mu.Lock()
		stopped := t.stopped
		c.mu.Unlock()
		if stopped {
			continue
		}
		n++
		done := make(chan struct{})
		go func() { t.f(); close(done) }()
		<-done
	}
	return n
}

func (c *fakeClock) live() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, t := range c.pending {
		if !t.stopped {
			n++
		}
	}
	return n
}
