package widgets

import (
	"strings"
	"sync"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
)

// Tooltip is a bordered, word-wrapped popup anchored to a rectangle: below
// it when there is room, else above, shifted left at the right edge.
// Text may carry ANSI SGR (parsed with src/components/cells) and newlines.
type Tooltip struct {
	*tview.Box
	Anchor   Rect
	MaxWidth int // content columns; 0 = 48
	Style    tcell.Style
	Border   tcell.Style

	text  string
	lines []cells.Line // parsed once; wrapped at Place
	shown []cells.Line
}

// NewTooltip builds a tooltip for text.
func NewTooltip(text string, anchor Rect) *Tooltip {
	st := tcell.StyleDefault.Background(tview.Styles.ContrastBackgroundColor).
		Foreground(tview.Styles.PrimaryTextColor)
	t := &Tooltip{Box: tview.NewBox(), Anchor: anchor, Style: st,
		Border: st.Foreground(tview.Styles.BorderColor)}
	t.SetText(text)
	return t
}

// SetText replaces the content.
func (t *Tooltip) SetText(text string) {
	t.text = text
	t.lines = cells.ParseStyle(strings.TrimRight(text, "\n"), t.Style)
}

// Text is the raw content.
func (t *Tooltip) Text() string { return t.text }

// Place implements Popup.
func (t *Tooltip) Place(bounds Rect) {
	maxW := t.MaxWidth
	if maxW <= 0 {
		maxW = 48
	}
	maxW = max(1, min(maxW, bounds.W-4))
	t.shown = t.shown[:0]
	cw := 0
	for _, ln := range t.lines {
		for _, w := range wrap(ln, maxW) {
			t.shown = append(t.shown, w)
			cw = max(cw, w.Width())
		}
	}
	r := placeBelow(t.Anchor, cw+4, len(t.shown)+2, bounds)
	t.SetRect(r.X, r.Y, r.W, r.H)
}

// Draw implements tview.Primitive.
func (t *Tooltip) Draw(scr tcell.Screen) {
	x, y, w, h := t.GetRect()
	if w < 3 || h < 3 {
		return
	}
	fill(scr, Rect{x, y, w, h}, t.Style)
	b := t.Border
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, tview.Borders.Horizontal, nil, b)
		scr.SetContent(cx, y+h-1, tview.Borders.Horizontal, nil, b)
	}
	for cy := y + 1; cy < y+h-1; cy++ {
		scr.SetContent(x, cy, tview.Borders.Vertical, nil, b)
		scr.SetContent(x+w-1, cy, tview.Borders.Vertical, nil, b)
	}
	scr.SetContent(x, y, tview.Borders.TopLeft, nil, b)
	scr.SetContent(x+w-1, y, tview.Borders.TopRight, nil, b)
	scr.SetContent(x, y+h-1, tview.Borders.BottomLeft, nil, b)
	scr.SetContent(x+w-1, y+h-1, tview.Borders.BottomRight, nil, b)
	cells.Draw(scr, x+2, y+1, w-4, h-2, t.shown, 0)
}

// Hover shows a Tooltip after the pointer has rested on a target for
// Delay. Widgets report what is under the pointer with Over/Out; the
// Overlay also feeds it every mouse event, so a tooltip hides as soon as
// the pointer leaves its anchor or anything is pressed — even when the
// widget below never says Out.
type Hover struct {
	Overlay  *Overlay
	Delay    time.Duration // 0 = 400ms
	MaxWidth int

	// AfterFunc schedules f after d and returns a stop func. Default:
	// time.AfterFunc. Tests substitute a fake clock.
	AfterFunc func(d time.Duration, f func()) (stop func())
	// Queue runs f on the UI goroutine and redraws: set it to
	// app.QueueUpdateDraw. Only the delay timer uses it — from its own
	// goroutine, because tview's QueueUpdate blocks until f has run and
	// would deadlock if called from the UI goroutine. Nil runs f inline.
	Queue func(f func())

	mu      sync.Mutex
	key     string
	anchor  Rect
	text    string
	gen     int
	stop    func()
	tip     *Tooltip
	visible bool
}

// NewHover builds a Hover hosted by o and installs it as o.Hover.
func NewHover(o *Overlay, queue func(func())) *Hover {
	h := &Hover{Overlay: o, Queue: queue}
	o.Hover = h
	return h
}

// Over, Out and the Overlay feed run on the UI goroutine (inside mouse
// handlers). Only the delay timer crosses goroutines, through Queue.

// Over says the pointer is on target key (any stable identity), whose
// screen rectangle is anchor and whose tooltip is text. Repeating the same
// key is free; a new key hides the current tooltip and restarts the delay.
// An empty key or text means Out.
func (h *Hover) Over(key string, anchor Rect, text string) {
	if key == "" || text == "" {
		h.Out()
		return
	}
	h.mu.Lock()
	if key == h.key {
		h.mu.Unlock()
		return
	}
	h.cancelLocked()
	h.key, h.anchor, h.text = key, anchor, text
	h.gen++
	gen := h.gen
	d := h.Delay
	if d <= 0 {
		d = 400 * time.Millisecond
	}
	after := h.AfterFunc
	h.mu.Unlock()
	if after == nil {
		after = func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		}
	}
	stop := after(d, func() { h.fire(gen) })
	h.mu.Lock()
	if h.gen == gen {
		h.stop = stop
	} else {
		stop()
	}
	h.mu.Unlock()
}

// Out hides the tooltip and cancels a pending one.
func (h *Hover) Out() {
	h.mu.Lock()
	had := h.key != "" || h.visible
	h.cancelLocked()
	h.key = ""
	h.gen++
	tip := h.tip
	vis := h.visible
	h.visible = false
	h.mu.Unlock()
	if had && vis && tip != nil {
		h.Overlay.Remove(tip) // already on the UI goroutine: never through Queue
	}
}

// Visible reports whether a tooltip is on screen; Key is its target.
func (h *Hover) Visible() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.visible }

// Key is the current target ("" for none).
func (h *Hover) Key() string { h.mu.Lock(); defer h.mu.Unlock(); return h.key }

// Tooltip is the tooltip primitive (nil before the first one showed).
func (h *Hover) Tooltip() *Tooltip { h.mu.Lock(); defer h.mu.Unlock(); return h.tip }

func (h *Hover) cancelLocked() {
	if h.stop != nil {
		h.stop()
		h.stop = nil
	}
}

func (h *Hover) run(f func()) {
	if h.Queue != nil {
		h.Queue(f)
		return
	}
	f()
}

func (h *Hover) fire(gen int) {
	h.run(func() {
		h.mu.Lock()
		if gen != h.gen || h.key == "" {
			h.mu.Unlock()
			return
		}
		if h.tip == nil {
			h.tip = NewTooltip("", Rect{})
		}
		tip := h.tip
		tip.Anchor, tip.MaxWidth = h.anchor, h.MaxWidth
		tip.SetText(h.text)
		h.visible = true
		h.stop = nil
		h.mu.Unlock()
		h.Overlay.Push(tip, false)
	})
}

// observe is the Overlay's feed: a press anywhere, or motion outside the
// anchor, ends the hover. Called on the UI goroutine.
func (h *Hover) observe(a tview.MouseAction, x, y int) {
	h.mu.Lock()
	active := h.key != ""
	inside := h.anchor.Contains(x, y)
	h.mu.Unlock()
	if !active {
		return
	}
	if isPress(a) || a == tview.MouseScrollUp || a == tview.MouseScrollDown || (a == actMove && !inside) {
		h.Out()
	}
}
