package widgets

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Orientation is the direction a Splitter lays its panes out in.
type Orientation int

const (
	// Columns: panes side by side, a vertical divider between them.
	Columns Orientation = iota
	// Rows: panes stacked, a horizontal divider between them.
	Rows
)

// SplitterStyles styles the divider in its three states.
type SplitterStyles struct {
	Normal, Hover, Drag tcell.Style
}

// DefaultSplitterStyles derives divider styles from tview.Styles.
func DefaultSplitterStyles() SplitterStyles {
	n := tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor).
		Foreground(tview.Styles.BorderColor)
	return SplitterStyles{
		Normal: n,
		Hover:  n.Foreground(tview.Styles.SecondaryTextColor).Bold(true),
		Drag:   n.Foreground(tview.Styles.TitleColor).Bold(true),
	}
}

// Splitter lays out two panes with a one-cell divider the mouse can drag.
//
// Pos is the size in cells of the sized pane — First, or Second when
// SizeSecond is set (a right-hand sidebar keeps its width when the window
// grows). Pos is clamped to [Min, Max] and to what the rectangle allows
// (Max <= 0 means no upper bound). Double-clicking the divider restores
// Default. OnMove reports every change during a drag (and the reset),
// OnDone the final value on release.
//
// Handle-only mode: for a frame that paints its own panes (tviewrt's single
// frame), leave First/Second nil and set Under to the frame. Under gets the
// whole rectangle, every event the divider does not take, and focus; the
// divider is drawn over it, and OnMove tells the app to re-layout.
type Splitter struct {
	*tview.Box
	First, Second tview.Primitive
	Under         tview.Primitive
	Orientation   Orientation
	SizeSecond    bool
	Pos           int
	Min, Max      int
	Default       int
	Styles        SplitterStyles
	OnMove        func(pos int)
	OnDone        func(pos int)

	hover, dragging bool
	grab            int // pointer offset from the divider when the drag started
	focusSecond     bool
}

// NewSplitter builds a splitter with pos as both the starting and the
// default size of the sized pane.
func NewSplitter(first, second tview.Primitive, o Orientation, pos int) *Splitter {
	return &Splitter{Box: tview.NewBox(), First: first, Second: second, Orientation: o,
		Pos: pos, Default: pos, Min: 1, Styles: DefaultSplitterStyles()}
}

// Dragging reports whether a drag is in progress.
func (s *Splitter) Dragging() bool { return s.dragging }

// Hovered reports whether the pointer is over the divider.
func (s *Splitter) Hovered() bool { return s.hover }

// span is the length along the split axis.
func (s *Splitter) span() int {
	_, _, w, h := s.GetInnerRect()
	if s.Orientation == Rows {
		return h
	}
	return w
}

// clamp keeps pos inside [Min, Max] and leaves room for the divider and
// at least Min cells of the other pane.
func (s *Splitter) clamp(pos int) int {
	limit := s.span() - 1 - s.Min
	if s.Max > 0 {
		limit = min(limit, s.Max)
	}
	return max(s.Min, min(pos, limit))
}

// divider is the divider's offset from the start of the axis.
func (s *Splitter) divider() int {
	p := s.clamp(s.Pos)
	if s.SizeSecond {
		return s.span() - 1 - p
	}
	return p
}

// DividerRect is the divider's screen rectangle.
func (s *Splitter) DividerRect() Rect {
	x, y, w, h := s.GetInnerRect()
	d := s.divider()
	if s.Orientation == Rows {
		return Rect{x, y + d, w, 1}
	}
	return Rect{x + d, y, 1, h}
}

// panes returns the First and Second rectangles.
func (s *Splitter) panes() (Rect, Rect) {
	x, y, w, h := s.GetInnerRect()
	d := s.divider()
	if s.Orientation == Rows {
		return Rect{x, y, w, d}, Rect{x, y + d + 1, w, max(0, h-d-1)}
	}
	return Rect{x, y, d, h}, Rect{x + d + 1, y, max(0, w-d-1), h}
}

// Draw implements tview.Primitive.
func (s *Splitter) Draw(scr tcell.Screen) {
	if s.Under != nil {
		ix, iy, iw, ih := s.GetInnerRect()
		s.Under.SetRect(ix, iy, iw, ih)
		s.Under.Draw(scr)
	} else {
		s.Box.DrawForSubclass(scr, s)
	}
	a, b := s.panes()
	if s.First != nil {
		s.First.SetRect(a.X, a.Y, a.W, a.H)
		s.First.Draw(scr)
	}
	if s.Second != nil {
		s.Second.SetRect(b.X, b.Y, b.W, b.H)
		s.Second.Draw(scr)
	}
	st := s.Styles.Normal
	switch {
	case s.dragging:
		st = s.Styles.Drag
	case s.hover:
		st = s.Styles.Hover
	}
	ch := tview.Borders.Vertical
	if s.Orientation == Rows {
		ch = tview.Borders.Horizontal
	}
	if s.hover || s.dragging { // a heavier rule while it is grabbable
		ch = '┃'
		if s.Orientation == Rows {
			ch = '━'
		}
	}
	d := s.DividerRect()
	for y := d.Y; y < d.Y+d.H; y++ {
		for x := d.X; x < d.X+d.W; x++ {
			scr.SetContent(x, y, ch, nil, st)
		}
	}
}

// posAt converts a pointer position to a Pos value, keeping the offset
// the pointer had from the divider when the drag began.
func (s *Splitter) posAt(x, y int) int {
	ix, iy, _, _ := s.GetInnerRect()
	off := x - ix
	if s.Orientation == Rows {
		off = y - iy
	}
	off -= s.grab
	if s.SizeSecond {
		return s.span() - 1 - off
	}
	return off
}

func (s *Splitter) set(pos int) {
	pos = s.clamp(pos)
	if pos == s.Pos {
		return
	}
	s.Pos = pos
	if s.OnMove != nil {
		s.OnMove(pos)
	}
}

// MouseHandler implements tview.Primitive. A press on the divider captures
// the mouse, so the drag keeps tracking outside the splitter's rectangle.
func (s *Splitter) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return s.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		d := s.DividerRect()
		on := d.Contains(x, y)

		if s.dragging {
			switch a {
			case actMove:
				s.set(s.posAt(x, y))
				return true, s
			case actLeftUp:
				s.dragging = false
				s.hover = on
				if s.OnDone != nil {
					s.OnDone(s.Pos)
				}
				return true, nil
			}
			return true, s
		}

		if !s.InRect(x, y) {
			if s.hover {
				s.hover = false
				return true, nil // repaint without the highlight
			}
			return false, nil
		}
		if a == actMove && on != s.hover {
			s.hover = on
			// fall through: the pane under the pointer still sees the move
			if on {
				return true, nil
			}
		}
		if on {
			switch a {
			case actLeftDown:
				s.dragging = true
				s.grab = x - d.X
				if s.Orientation == Rows {
					s.grab = y - d.Y
				}
				return true, s
			case actLeftDouble:
				s.set(s.Default)
				if s.OnDone != nil {
					s.OnDone(s.Pos)
				}
				return true, nil
			}
			return true, nil
		}

		a1, b1 := s.panes()
		for _, p := range []struct {
			r      Rect
			p      tview.Primitive
			second bool
		}{{a1, s.First, false}, {b1, s.Second, true}} {
			if p.p == nil || !p.r.Contains(x, y) {
				continue
			}
			if a == actLeftDown {
				s.focusSecond = p.second
			}
			if h := p.p.MouseHandler(); h != nil {
				return h(a, e, setFocus)
			}
		}
		if s.Under != nil {
			if h := s.Under.MouseHandler(); h != nil {
				return h(a, e, setFocus)
			}
		}
		return false, nil
	})
}

// Focus gives focus to the pane last clicked (First initially).
func (s *Splitter) Focus(delegate func(p tview.Primitive)) {
	if s.focusSecond && s.Second != nil {
		delegate(s.Second)
		return
	}
	if s.First != nil {
		delegate(s.First)
		return
	}
	if s.Under != nil {
		delegate(s.Under)
		return
	}
	s.Box.Focus(delegate)
}

// HasFocus implements tview.Primitive.
func (s *Splitter) HasFocus() bool {
	for _, p := range []tview.Primitive{s.First, s.Second, s.Under} {
		if p != nil && p.HasFocus() {
			return true
		}
	}
	return s.Box.HasFocus()
}

// InputHandler forwards keys to whichever pane has focus.
func (s *Splitter) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return s.WrapInputHandler(func(e *tcell.EventKey, setFocus func(tview.Primitive)) {
		for _, p := range []tview.Primitive{s.First, s.Second, s.Under} {
			if p != nil && p.HasFocus() {
				if h := p.InputHandler(); h != nil {
					h(e, setFocus)
				}
				return
			}
		}
	})
}
