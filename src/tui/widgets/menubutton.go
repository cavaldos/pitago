package widgets

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cellwidth"
)

// MenuButton is a small clickable label ("⋯" by default) that opens a
// ContextMenu below itself. Items is called on every open, so the menu
// reflects current state. Clicking the button while its menu is open
// closes it.
type MenuButton struct {
	*tview.Box
	Label      string
	Style      tcell.Style
	HoverStyle tcell.Style
	Host       *Overlay
	Items      func() []MenuItem
	OnSelect   func(id string)

	menu  *ContextMenu
	hover bool
}

// NewMenuButton builds a "⋯" button hosted by host.
func NewMenuButton(host *Overlay, items func() []MenuItem, onSelect func(id string)) *MenuButton {
	st := tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor).
		Foreground(tview.Styles.SecondaryTextColor)
	return &MenuButton{Box: tview.NewBox(), Label: "⋯", Style: st, HoverStyle: st.Reverse(true),
		Host: host, Items: items, OnSelect: onSelect}
}

// Width is the button's natural width (label plus one space each side).
func (b *MenuButton) Width() int { return cellwidth.StringWidth(b.Label) + 2 }

// Menu is the open menu, or nil.
func (b *MenuButton) Menu() *ContextMenu {
	if b.menu != nil && b.menu.Open() {
		return b.menu
	}
	return nil
}

// Draw implements tview.Primitive.
func (b *MenuButton) Draw(scr tcell.Screen) {
	x, y, w, h := b.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	st := b.Style
	if b.hover || b.Menu() != nil {
		st = b.HoverStyle
	}
	fill(scr, Rect{x, y, w, 1}, st)
	lw := cellwidth.StringWidth(b.Label)
	printText(scr, x+max(0, (w-lw)/2), y, w, b.Label, st)
}

// Toggle opens the menu, or closes it when open.
func (b *MenuButton) Toggle() {
	if m := b.Menu(); m != nil {
		m.Close()
		return
	}
	var items []MenuItem
	if b.Items != nil {
		items = b.Items()
	}
	b.menu = NewContextMenu(items)
	b.menu.OnSelect = b.OnSelect
	x, y, w, _ := b.GetInnerRect()
	b.menu.OpenBelow(b.Host, Rect{x, y, w, 1})
}

// MouseHandler implements tview.Primitive. It opens on press, like a
// menu bar, so press-drag-release onto an item works.
func (b *MenuButton) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return b.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		in := b.InRect(e.Position())
		if a == actMove {
			changed := in != b.hover
			b.hover = in
			return changed, nil
		}
		if !in {
			return false, nil
		}
		if a == actLeftDown {
			b.Toggle()
		}
		return true, nil
	})
}

// InputHandler opens the menu on Enter or Space when the button has focus.
func (b *MenuButton) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return b.WrapInputHandler(func(e *tcell.EventKey, _ func(tview.Primitive)) {
		if e.Key() == tcell.KeyEnter || (e.Key() == tcell.KeyRune && e.Rune() == ' ') {
			b.Toggle()
		}
	})
}
