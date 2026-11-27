package widgets

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cellwidth"
)

// MenuItem is one row of a ContextMenu.
type MenuItem struct {
	ID        string
	Label     string
	Shortcut  string // right-aligned hint, e.g. "⌥X"
	Hint      string // dim description after the label (only when no Shortcut)
	Toggle    bool   // picking keeps the menu open (✓ toggles re-read via OnRefresh)
	Disabled  bool
	Separator bool       // a rule; every other field is ignored
	Submenu   []MenuItem // opens to the side on hover, → or Enter
}

func (it MenuItem) selectable() bool { return !it.Separator && !it.Disabled }

// MenuStyles styles a ContextMenu.
type MenuStyles struct {
	Normal, Selected, Disabled, Shortcut, Border tcell.Style
}

// DefaultMenuStyles derives menu styles from tview.Styles.
func DefaultMenuStyles() MenuStyles {
	base := tcell.StyleDefault.Background(tview.Styles.ContrastBackgroundColor).
		Foreground(tview.Styles.PrimaryTextColor)
	return MenuStyles{
		Normal:   base,
		Selected: base.Reverse(true),
		Disabled: base.Foreground(tview.Styles.TertiaryTextColor).Dim(true),
		Shortcut: base.Foreground(tview.Styles.SecondaryTextColor),
		Border:   base.Foreground(tview.Styles.BorderColor),
	}
}

type anchorKind int

const (
	anchorPoint anchorKind = iota
	anchorBelow
	anchorBeside
)

// ContextMenu is a bordered popup list. Open it with Overlay-hosted
// OpenAt (pointer), OpenBelow (button) — submenus open themselves beside
// their row. Mouse: hover highlights and opens submenus, a release over an
// enabled row selects (so press-drag-release works from a right-press),
// a press outside closes. Keys: ↑/↓ (skip separators and disabled rows),
// Home/End, Enter/→ (select / open submenu), ←/Esc (close one level).
type ContextMenu struct {
	*tview.Box
	Items  []MenuItem
	Styles MenuStyles

	// OnSelect gets the chosen item's ID; the menu chain is already closed
	// (and OnClose has run) by then, so OnSelect may open another popup.
	OnSelect func(id string)
	// OnClose runs once when the menu closes for any reason: selection,
	// Esc, a press outside, or Overlay.CloseModals.
	OnClose func()
	// OnCross, when set on the root menu, makes ← (on the root menu) and
	// → (on a row without a submenu, at any depth) ask for the neighbouring
	// menu: dir is -1 or +1. A MenuBar uses it to walk its titles.
	OnCross func(dir int)
	// OnRefresh, when set on the root menu, re-reads the items after a
	// Toggle pick stays open (a MenuBar re-reads its Source).
	OnRefresh func()

	host       *Overlay
	parent     *ContextMenu
	sub        *ContextMenu
	subIndex   int
	sel        int
	anchor     Rect
	anchorKind anchorKind
	armed      bool // a press or motion happened inside: releases may select
	closed     bool
	offset     int
}

// NewContextMenu builds a menu; styles default to DefaultMenuStyles.
func NewContextMenu(items []MenuItem) *ContextMenu {
	m := &ContextMenu{Box: tview.NewBox(), Items: items, Styles: DefaultMenuStyles(), sel: -1, subIndex: -1}
	return m
}

// OpenAt shows m on host with its top-left at the pointer (x, y), flipped
// left/up at the screen edges.
func (m *ContextMenu) OpenAt(host *Overlay, x, y int) {
	m.open(host, Rect{x, y, 1, 1}, anchorPoint)
}

// OpenBelow shows m under anchor (a button), above it when there is no
// room, right-aligned with it at the right edge.
func (m *ContextMenu) OpenBelow(host *Overlay, anchor Rect) {
	m.open(host, anchor, anchorBelow)
}

func (m *ContextMenu) open(host *Overlay, anchor Rect, kind anchorKind) {
	m.host, m.anchor, m.anchorKind = host, anchor, kind
	m.closed, m.armed, m.sub, m.subIndex = false, false, nil, -1
	m.sel = -1
	if kind == anchorBelow { // keyboard users expect a highlighted row
		m.sel = m.step(-1, 1)
	}
	host.Push(m, true)
}

// Selected is the highlighted row index, -1 for none.
func (m *ContextMenu) Selected() int { return m.sel }

// Open reports whether m is on screen.
func (m *ContextMenu) Open() bool { return m.host != nil && m.host.Has(m) }

// Close closes m, its submenus and — for a submenu — nothing above it.
func (m *ContextMenu) Close() {
	if m.host != nil {
		m.host.Remove(m)
	}
}

// Dismissed implements Dismisser.
func (m *ContextMenu) Dismissed() {
	if m.closed {
		return
	}
	m.closed = true
	if m.parent != nil {
		m.parent.sub, m.parent.subIndex = nil, -1
	}
	if m.parent == nil && m.OnClose != nil {
		m.OnClose()
	}
}

func (m *ContextMenu) root() *ContextMenu {
	r := m
	for r.parent != nil {
		r = r.parent
	}
	return r
}

func (m *ContextMenu) choose(i int) {
	it := m.Items[i]
	if !it.selectable() {
		return
	}
	if len(it.Submenu) > 0 {
		m.openSub(i, true)
		return
	}
	r := m.root()
	if it.Toggle {
		if r.OnSelect != nil {
			r.OnSelect(it.ID)
		}
		if r.OnRefresh != nil {
			r.OnRefresh()
		}
		return
	}
	r.Close()
	if r.OnSelect != nil {
		r.OnSelect(it.ID)
	}
}

func (m *ContextMenu) openSub(i int, viaKey bool) {
	if m.subIndex == i && m.sub != nil {
		return
	}
	if m.sub != nil {
		m.sub.Close()
	}
	x, y, w, _ := m.GetRect()
	row := Rect{x, y + 1 + i, w, 1}
	s := NewContextMenu(m.Items[i].Submenu)
	s.Styles, s.parent = m.Styles, m
	m.sub, m.subIndex = s, i
	s.open(m.host, row, anchorBeside)
	if viaKey {
		s.sel = s.step(-1, 1)
	}
}

// size is the outer size: a border, one space of padding each side, label,
// two spaces, shortcut, and a submenu arrow column when any row has one.
func (m *ContextMenu) size() (int, int) {
	label, short, arrow := 0, 0, 0
	for _, it := range m.Items {
		label = max(label, cellwidth.StringWidth(it.Label))
		short = max(short, cellwidth.StringWidth(it.Shortcut))
		if len(it.Submenu) > 0 {
			arrow = 2
		}
	}
	for _, it := range m.Items {
		if it.Hint != "" && it.Shortcut == "" {
			label = max(label, cellwidth.StringWidth(it.Label)+2+cellwidth.StringWidth(it.Hint))
		}
	}
	w := 1 + label + arrow + 1
	if short > 0 {
		w += 2 + short
	}
	return w + 2, len(m.Items) + 2
}

// Place implements Popup.
func (m *ContextMenu) Place(bounds Rect) {
	w, h := m.size()
	var r Rect
	switch m.anchorKind {
	case anchorBelow:
		r = placeBelow(m.anchor, w, h, bounds)
	case anchorBeside:
		r = placeBeside(m.anchor, w, h, bounds)
		r.Y = max(bounds.Y, r.Y-1) // align the first row, not the border, with the parent row
		r = fit(r.X, r.Y, r.W, r.H, bounds)
	default:
		r = placeAtPoint(m.anchor.X, m.anchor.Y, w, h, bounds)
	}
	m.SetRect(r.X, r.Y, r.W, r.H)
	m.keepVisible(m.sel)
}

// Draw implements tview.Primitive.
func (m *ContextMenu) Draw(scr tcell.Screen) {
	x, y, w, h := m.GetRect()
	if w < 3 || h < 3 {
		return
	}
	st := m.Styles
	fill(scr, Rect{x, y, w, h}, st.Normal)
	for cx := x + 1; cx < x+w-1; cx++ {
		scr.SetContent(cx, y, tview.Borders.Horizontal, nil, st.Border)
		scr.SetContent(cx, y+h-1, tview.Borders.Horizontal, nil, st.Border)
	}
	for cy := y + 1; cy < y+h-1; cy++ {
		scr.SetContent(x, cy, tview.Borders.Vertical, nil, st.Border)
		scr.SetContent(x+w-1, cy, tview.Borders.Vertical, nil, st.Border)
	}
	scr.SetContent(x, y, tview.Borders.TopLeft, nil, st.Border)
	scr.SetContent(x+w-1, y, tview.Borders.TopRight, nil, st.Border)
	scr.SetContent(x, y+h-1, tview.Borders.BottomLeft, nil, st.Border)
	scr.SetContent(x+w-1, y+h-1, tview.Borders.BottomRight, nil, st.Border)

	inner := w - 2
	visible := h - 2
	if m.offset > 0 {
		scr.SetContent(x+w/2, y, '▲', nil, st.Border)
	}
	if m.offset+visible < len(m.Items) {
		scr.SetContent(x+w/2, y+h-1, '▼', nil, st.Border)
	}
	for row := 0; row < visible; row++ {
		i := m.offset + row
		if i >= len(m.Items) {
			break
		}
		it := m.Items[i]
		ry := y + 1 + row
		if it.Separator {
			scr.SetContent(x, ry, tview.Borders.LeftT, nil, st.Border)
			scr.SetContent(x+w-1, ry, tview.Borders.RightT, nil, st.Border)
			for cx := x + 1; cx < x+w-1; cx++ {
				scr.SetContent(cx, ry, tview.Borders.Horizontal, nil, st.Border)
			}
			continue
		}
		row, short := st.Normal, st.Shortcut
		switch {
		case it.Disabled:
			row, short = st.Disabled, st.Disabled
		case i == m.sel || i == m.subIndex:
			row, short = st.Selected, st.Selected
		}
		fill(scr, Rect{x + 1, ry, inner, 1}, row)
		printText(scr, x+2, ry, inner-2, it.Label, row)
		if it.Hint != "" && it.Shortcut == "" && !it.Disabled {
			// dim description after the label; selected rows dim their own
			// reverse-video style instead of the muted disabled one
			hint := st.Disabled
			if i == m.sel || i == m.subIndex {
				hint = st.Selected.Dim(true)
			}
			hx := x + 2 + cellwidth.StringWidth(it.Label) + 2
			printText(scr, hx, ry, max(0, x+w-2-hx), it.Hint, hint)
		}
		right := x + w - 2 // last inner column is padding
		if len(it.Submenu) > 0 {
			scr.SetContent(right-1, ry, '›', nil, row)
			right -= 2
		}
		if it.Shortcut != "" {
			sw := cellwidth.StringWidth(it.Shortcut)
			printText(scr, right-sw, ry, sw, it.Shortcut, short)
		}
	}
}

// itemAt is the row under (x, y), or -1.
func (m *ContextMenu) itemAt(x, y int) int {
	rx, ry, rw, rh := m.GetRect()
	if x <= rx || x >= rx+rw-1 || y <= ry || y >= ry+rh-1 {
		return -1
	}
	i := m.offset + y - ry - 1
	if i >= len(m.Items) {
		return -1
	}
	return i
}

// MouseHandler implements tview.Primitive. The Overlay only routes events
// inside the menu's rectangle here.
func (m *ContextMenu) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return m.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		if a == tview.MouseScrollUp || a == tview.MouseScrollDown {
			if a == tview.MouseScrollUp {
				m.scroll(-3)
			} else {
				m.scroll(3)
			}
			return true, nil
		}
		i := m.itemAt(x, y)
		switch a {
		case actMove:
			m.armed = true
			if i >= 0 && m.Items[i].selectable() {
				m.sel = i
				if len(m.Items[i].Submenu) > 0 {
					m.openSub(i, false)
				} else if m.sub != nil {
					m.sub.Close()
				}
			}
		case actLeftDown, actRightDown:
			m.armed = true
			if i >= 0 && m.Items[i].selectable() {
				m.sel = i
			}
		case actLeftUp, actRightUp:
			if m.armed && i >= 0 && m.Items[i].selectable() {
				m.choose(i)
			}
		}
		return true, nil
	})
}

// InputHandler implements tview.Primitive; the Overlay sends keys to the
// top-most menu, which is the deepest open submenu.
func (m *ContextMenu) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return m.WrapInputHandler(func(e *tcell.EventKey, _ func(tview.Primitive)) {
		switch e.Key() {
		case tcell.KeyUp, tcell.KeyBacktab:
			m.sel = m.step(m.sel, -1)
			m.keepVisible(m.sel)
		case tcell.KeyDown, tcell.KeyTab:
			m.sel = m.step(m.sel, 1)
			m.keepVisible(m.sel)
		case tcell.KeyHome:
			m.sel = m.step(-1, 1)
			m.keepVisible(m.sel)
		case tcell.KeyEnd:
			m.sel = m.step(len(m.Items), -1)
			m.keepVisible(m.sel)
		case tcell.KeyEnter:
			if m.sel >= 0 {
				m.choose(m.sel)
			}
		case tcell.KeyRight:
			if m.sel >= 0 && len(m.Items[m.sel].Submenu) > 0 {
				m.openSub(m.sel, true)
			} else if r := m.root(); r.OnCross != nil {
				r.OnCross(+1)
			}
		case tcell.KeyLeft:
			if m.parent != nil {
				m.Close()
			} else if m.OnCross != nil {
				m.OnCross(-1)
			}
		case tcell.KeyEsc:
			m.Close()
		}
	})
}

// step moves from i in direction d to the next selectable row, wrapping;
// it returns i (or -1) when there is none.
func (m *ContextMenu) scroll(delta int) {
	_, _, _, h := m.GetRect()
	m.offset = max(0, min(m.offset+delta, len(m.Items)-max(1, h-2)))
}

func (m *ContextMenu) keepVisible(i int) {
	if i < 0 {
		return
	}
	_, _, _, h := m.GetRect()
	visible := max(1, h-2)
	if i < m.offset {
		m.offset = i
	} else if i >= m.offset+visible {
		m.offset = i - visible + 1
	}
	m.scroll(0) // clamp after the menu or screen size changes
}

func (m *ContextMenu) step(i, d int) int {
	n := len(m.Items)
	for k := 1; k <= n; k++ {
		j := ((i+d*k)%n + n) % n
		if m.Items[j].selectable() {
			return j
		}
	}
	return i
}
