package widgets

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type tviewAction = tview.MouseAction

const (
	actMove        = tview.MouseMove
	actLeftDown    = tview.MouseLeftDown
	actLeftUp      = tview.MouseLeftUp
	actLeftClick   = tview.MouseLeftClick
	actLeftDouble  = tview.MouseLeftDoubleClick
	actRightDown   = tview.MouseRightDown
	actRightUp     = tview.MouseRightUp
	actRightClick  = tview.MouseRightClick
	actRightDouble = tview.MouseRightDoubleClick
	actMiddleDown  = tview.MouseMiddleDown
	actMiddleUp    = tview.MouseMiddleUp
	actMiddleClick = tview.MouseMiddleClick
)

// Popup is a primitive the Overlay can host. Place is called before every
// draw with the overlay's rectangle so the popup can position (and flip or
// clamp) itself against the screen edges.
type Popup interface {
	tview.Primitive
	Place(bounds Rect)
}

// Dismisser is implemented by popups that want to know when the overlay
// removes them (click outside, Esc in a parent, CloseModals).
type Dismisser interface{ Dismissed() }

type layer struct {
	p     Popup
	modal bool
}

// Overlay draws Base and then a stack of popups above it.
//
// Mouse routing, top-most first:
//   - a modal popup (menu) under the pointer gets the event;
//   - pointer outside every modal popup while one is open: a press closes
//     all modal popups and is swallowed together with its release and
//     click, so the base never sees half a click; motion is swallowed too
//     (no hover on the base behind an open menu);
//   - non-modal popups (tooltips) never take the mouse: events fall
//     through to Base.
//
// Keys go to the top-most modal popup when there is one, else to Base.
// Hover, when set, is told about every mouse event so tooltips hide when
// the pointer leaves their anchor or anything is pressed.
type Overlay struct {
	*tview.Box
	Base  tview.Primitive
	Hover *Hover

	// Sticky primitives (a menu bar) keep getting the mouse while a modal
	// popup is open, instead of the press-outside-closes rule: hovering
	// another bar title switches menus, pressing the open title closes it.
	Sticky []tview.Primitive

	layers  []layer
	swallow swallowState
}

type swallowState int

const (
	swallowNone    swallowState = iota
	swallowUntilUp              // outside press dismissed a menu: eat until release
	swallowClick                // release seen: eat the Click/DoubleClick tview adds
)

// NewOverlay hosts base.
func NewOverlay(base tview.Primitive) *Overlay {
	o := &Overlay{Box: tview.NewBox(), Base: base}
	return o
}

// Push opens p on top. modal popups take the mouse and keyboard; others
// are display-only.
func (o *Overlay) Push(p Popup, modal bool) {
	o.Remove(p)
	o.layers = append(o.layers, layer{p, modal})
}

// Remove closes p and every popup above it (a submenu dies with its
// parent). Removed popups that implement Dismisser are told, top first.
func (o *Overlay) Remove(p Popup) {
	for i, l := range o.layers {
		if l.p == p {
			closed := o.layers[i:]
			o.layers = append([]layer(nil), o.layers[:i]...)
			for j := len(closed) - 1; j >= 0; j-- {
				if d, ok := closed[j].p.(Dismisser); ok {
					d.Dismissed()
				}
			}
			return
		}
	}
}

// CloseModals closes every modal popup (and anything above it).
func (o *Overlay) CloseModals() {
	for i, l := range o.layers {
		if l.modal {
			o.Remove(o.layers[i].p)
			return
		}
	}
}

// Has reports whether p is open.
func (o *Overlay) Has(p Popup) bool {
	for _, l := range o.layers {
		if l.p == p {
			return true
		}
	}
	return false
}

// Popups returns the open popups, bottom first.
func (o *Overlay) Popups() []Popup {
	out := make([]Popup, len(o.layers))
	for i, l := range o.layers {
		out[i] = l.p
	}
	return out
}

// Modal reports whether a modal popup is open.
func (o *Overlay) Modal() bool { return o.topModal() != nil }

func (o *Overlay) topModal() Popup {
	for i := len(o.layers) - 1; i >= 0; i-- {
		if o.layers[i].modal {
			return o.layers[i].p
		}
	}
	return nil
}

// Draw implements tview.Primitive.
func (o *Overlay) Draw(scr tcell.Screen) {
	x, y, w, h := o.GetRect()
	if o.Base != nil {
		o.Base.SetRect(x, y, w, h)
		o.Base.Draw(scr)
	}
	bounds := Rect{x, y, w, h}
	for _, l := range o.layers {
		l.p.Place(bounds)
		l.p.Draw(scr)
	}
}

// Focus gives focus to the base; menus take keys through InputHandler
// without owning tview focus, so closing one never strands focus.
func (o *Overlay) Focus(delegate func(p tview.Primitive)) {
	if o.Base != nil {
		delegate(o.Base)
		return
	}
	o.Box.Focus(delegate)
}

// HasFocus implements tview.Primitive.
func (o *Overlay) HasFocus() bool {
	return o.Box.HasFocus() || (o.Base != nil && o.Base.HasFocus())
}

// InputHandler routes keys to the top modal popup, else to the base.
func (o *Overlay) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return o.WrapInputHandler(func(e *tcell.EventKey, setFocus func(tview.Primitive)) {
		if p := o.topModal(); p != nil {
			if h := p.InputHandler(); h != nil {
				h(e, setFocus)
			}
			return
		}
		if o.Base != nil {
			if h := o.Base.InputHandler(); h != nil {
				h(e, setFocus)
			}
		}
	})
}

// PasteHandler forwards pastes to the base (menus don't take text).
func (o *Overlay) PasteHandler() func(string, func(tview.Primitive)) {
	return o.WrapPasteHandler(func(text string, setFocus func(tview.Primitive)) {
		if o.Base != nil && o.topModal() == nil {
			if h := o.Base.PasteHandler(); h != nil {
				h(text, setFocus)
			}
		}
	})
}

// MouseHandler implements the routing described on Overlay.
func (o *Overlay) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return o.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		if o.Hover != nil {
			o.Hover.observe(a, x, y)
		}

		switch o.swallow {
		case swallowUntilUp:
			if a == actLeftUp || a == actRightUp || a == actMiddleUp {
				o.swallow = swallowClick
			}
			return true, nil
		case swallowClick:
			o.swallow = swallowNone
			switch a {
			case actLeftClick, actLeftDouble, actRightClick, actRightDouble, actMiddleClick:
				return true, nil
			}
		}

		for i := len(o.layers) - 1; i >= 0; i-- {
			l := o.layers[i]
			if !l.modal || !rectOf(l.p).Contains(x, y) {
				continue
			}
			var capture tview.Primitive
			if h := l.p.MouseHandler(); h != nil {
				_, capture = h(a, e, setFocus)
			}
			// A release that closed the last menu (a selection) is followed
			// by tview's synthetic Click in the same report: eat it, or the
			// base would act on a click that was really a menu choice.
			if (a == actLeftUp || a == actRightUp || a == actMiddleUp) && o.topModal() == nil {
				o.swallow = swallowClick
			}
			return true, capture
		}
		if o.topModal() != nil {
			for _, p := range o.Sticky {
				if p == nil || !rectOf(p).Contains(x, y) {
					continue
				}
				if h := p.MouseHandler(); h != nil {
					_, capture := h(a, e, setFocus)
					return true, capture
				}
				return true, nil
			}
			if isPress(a) {
				o.CloseModals()
				o.swallow = swallowUntilUp
			}
			return true, nil // nothing behind an open menu reacts
		}
		if o.Base != nil {
			if h := o.Base.MouseHandler(); h != nil {
				return h(a, e, setFocus)
			}
		}
		return false, nil
	})
}
