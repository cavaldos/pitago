package widgets

import (
	"unicode"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cellwidth"
)

// BarMenu is one title of a MenuBar.
type BarMenu struct {
	Title   string
	Tooltip string // shown on hover while no menu is open (needs MenuBar.Hover)
	Items   []MenuItem
}

// MenuBarStyles styles the bar.
type MenuBarStyles struct {
	Normal, Hover, Open tcell.Style
	Menu                MenuStyles
}

// DefaultMenuBarStyles derives bar styles from tview.Styles.
func DefaultMenuBarStyles() MenuBarStyles {
	n := tcell.StyleDefault.Background(tview.Styles.ContrastBackgroundColor).
		Foreground(tview.Styles.PrimaryTextColor)
	return MenuBarStyles{Normal: n, Hover: n.Bold(true), Open: n.Reverse(true), Menu: DefaultMenuStyles()}
}

// MenuBar is an editor-style menu bar: a row of titles, each dropping down
// a ContextMenu.
//
//   - press a title: open its menu; press the open title: close it;
//   - while a menu is open, hovering another title switches to its menu,
//     and press-drag-release from a title onto an item picks it;
//   - ←/→ in an open menu walk the titles (← closes a submenu first; →
//     on a row with a submenu opens it);
//   - Alt+<mnemonic> opens a title (mnemonic underlined), F10 toggles the
//     first menu, Esc closes — see HandleKey, which the app's key capture
//     must call before anything else.
//
// The bar registers itself as one of the host Overlay's Sticky primitives
// so it keeps the mouse while its menu (a modal popup) is open.
type MenuBar struct {
	*tview.Box
	Host   *Overlay
	Hover  *Hover // optional: title tooltips
	Styles MenuBarStyles

	// Source returns the titles and items; called on every draw and on
	// every open, so labels and checkmarks are current.
	Source func() []BarMenu
	// OnSelect reports a pick: the title it came from and the item ID.
	OnSelect func(title, id string)
	// Mnemonics enables Alt+<letter> in HandleKey (on by default). Each
	// title gets the first letter not used by an earlier title.
	Mnemonics bool

	menus []BarMenu
	spans []barSpan
	open  int
	hover int
	menu  *ContextMenu
}

type barSpan struct {
	x, w     int
	mnemonic int // rune index in the title, -1 for none
	key      rune
}

// NewMenuBar builds a bar hosted by host.
func NewMenuBar(host *Overlay, source func() []BarMenu, onSelect func(title, id string)) *MenuBar {
	b := &MenuBar{Box: tview.NewBox(), Host: host, Source: source, OnSelect: onSelect,
		Styles: DefaultMenuBarStyles(), Mnemonics: true, open: -1, hover: -1}
	host.Sticky = append(host.Sticky, b)
	return b
}

// OpenIndex is the open title's index, -1 when closed.
func (b *MenuBar) OpenIndex() int {
	if b.menu == nil || !b.menu.Open() {
		return -1
	}
	return b.open
}

// Menu is the open drop-down, or nil.
func (b *MenuBar) Menu() *ContextMenu {
	if b.OpenIndex() < 0 {
		return nil
	}
	return b.menu
}

// Titles are the current titles.
func (b *MenuBar) Titles() []string {
	b.refresh()
	out := make([]string, len(b.menus))
	for i, m := range b.menus {
		out[i] = m.Title
	}
	return out
}

// Mnemonic is the Alt key letter of title i (lower case), 0 for none.
func (b *MenuBar) Mnemonic(i int) rune {
	b.refresh()
	if i < 0 || i >= len(b.spans) {
		return 0
	}
	return b.spans[i].key
}

// TitleRect is title i's screen rectangle.
func (b *MenuBar) TitleRect(i int) (Rect, bool) {
	b.refresh()
	if i < 0 || i >= len(b.spans) {
		return Rect{}, false
	}
	_, y, _, _ := b.GetInnerRect()
	return Rect{b.spans[i].x, y, b.spans[i].w, 1}, true
}

// refresh re-reads Source and lays the titles out: " Title " each.
func (b *MenuBar) refresh() {
	if b.Source != nil {
		b.menus = b.Source()
	}
	x, _, _, _ := b.GetInnerRect()
	b.spans = b.spans[:0]
	used := map[rune]bool{}
	for _, m := range b.menus {
		sp := barSpan{x: x, w: cellwidth.StringWidth(m.Title) + 2, mnemonic: -1}
		for i, r := range []rune(m.Title) {
			lr := unicode.ToLower(r)
			if unicode.IsLetter(lr) && lr < 0x80 && !used[lr] {
				sp.mnemonic, sp.key = i, lr
				used[lr] = true
				break
			}
		}
		b.spans = append(b.spans, sp)
		x += sp.w
	}
}

// at is the title index at screen column x, or -1.
func (b *MenuBar) at(x int) int {
	for i, sp := range b.spans {
		if x >= sp.x && x < sp.x+sp.w {
			return i
		}
	}
	return -1
}

// Open drops down title i (closing any other) with its first row
// selected, as a keyboard open does.
func (b *MenuBar) Open(i int) { b.open1(i, true) }

// open1 opens title i; a mouse open leaves the highlight to the pointer.
func (b *MenuBar) open1(i int, keyboard bool) {
	b.refresh()
	if i < 0 || i >= len(b.menus) {
		return
	}
	if b.menu != nil && b.menu.Open() {
		b.menu.Close()
	}
	if b.Hover != nil {
		b.Hover.Out()
	}
	title := b.menus[i].Title
	m := NewContextMenu(b.menus[i].Items)
	m.Styles = b.Styles.Menu
	m.OnSelect = func(id string) {
		if b.OnSelect != nil {
			b.OnSelect(title, id)
		}
	}
	// Toggle picks stay open: re-read Source so ✓ marks and any Hint
	// rows reflect the model's new state.
	m.OnRefresh = func() {
		b.refresh()
		if b.open >= 0 && b.open < len(b.menus) {
			m.Items = b.menus[b.open].Items
		}
	}
	m.OnClose = func() {
		if b.menu == m {
			b.open = -1
		}
	}
	m.OnCross = func(dir int) {
		n := len(b.menus)
		if n > 0 {
			b.Open(((b.open+dir)%n + n) % n)
		}
	}
	b.menu, b.open = m, i
	r, _ := b.TitleRect(i)
	m.OpenBelow(b.Host, r)
	if !keyboard {
		m.sel = -1
	}
}

// OpenTitle opens the menu with this title.
func (b *MenuBar) OpenTitle(title string) {
	for i, t := range b.Titles() {
		if t == title {
			b.Open(i)
			return
		}
	}
}

// Close closes the open menu, if any.
func (b *MenuBar) Close() {
	if m := b.Menu(); m != nil {
		m.Close()
	}
}

// HandleKey handles the bar's global keys and reports whether it used the
// event: F10 toggles the first menu, Alt+<mnemonic> opens (or, when it is
// the open one, closes) a menu. Call it from the application's input
// capture before routing keys elsewhere, including while a menu is open.
// Everything else (arrows, Enter, Esc inside a menu) reaches the menu
// through the Overlay.
func (b *MenuBar) HandleKey(e *tcell.EventKey) bool {
	b.refresh()
	if len(b.menus) == 0 {
		return false
	}
	if e.Key() == tcell.KeyF10 && e.Modifiers() == tcell.ModNone {
		if b.OpenIndex() >= 0 {
			b.Close()
		} else {
			b.Open(0)
		}
		return true
	}
	if !b.Mnemonics || e.Key() != tcell.KeyRune || e.Modifiers() != tcell.ModAlt {
		return false
	}
	k := unicode.ToLower(e.Rune())
	for i, sp := range b.spans {
		if sp.key != 0 && sp.key == k {
			if b.OpenIndex() == i {
				b.Close()
			} else {
				b.Open(i)
			}
			return true
		}
	}
	return false
}

// Draw implements tview.Primitive.
func (b *MenuBar) Draw(scr tcell.Screen) {
	x, y, w, h := b.GetInnerRect()
	if w <= 0 || h <= 0 {
		return
	}
	b.refresh()
	fill(scr, Rect{x, y, w, 1}, b.Styles.Normal)
	open := b.OpenIndex()
	for i, sp := range b.spans {
		if sp.x >= x+w {
			break
		}
		st := b.Styles.Normal
		switch {
		case i == open:
			st = b.Styles.Open
		case i == b.hover && open < 0:
			st = b.Styles.Hover
		}
		fill(scr, Rect{sp.x, y, min(sp.w, x+w-sp.x), 1}, st)
		col := sp.x + 1
		for ri, r := range []rune(b.menus[i].Title) {
			cs := st
			if b.Mnemonics && ri == sp.mnemonic {
				cs = st.Underline(true)
			}
			cw := cellwidth.StringWidth(string(r))
			if col+cw > x+w {
				break
			}
			scr.SetContent(col, y, r, nil, cs)
			col += cw
		}
	}
}

// MouseHandler implements tview.Primitive. It gets events both through
// the normal tree (no menu open) and as an Overlay Sticky (menu open).
func (b *MenuBar) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return b.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, _ func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		if !b.InRect(x, y) {
			if b.hover >= 0 {
				b.hover = -1
				return true, nil
			}
			return false, nil
		}
		b.refresh()
		i := b.at(x)
		open := b.OpenIndex()
		switch a {
		case actMove:
			b.hover = i
			if open >= 0 && i >= 0 && i != open {
				b.open1(i, false)
			}
			if b.Hover != nil {
				if open < 0 && i >= 0 && b.menus[i].Tooltip != "" {
					r, _ := b.TitleRect(i)
					b.Hover.Over("menubar:"+b.menus[i].Title, r, b.menus[i].Tooltip)
				} else {
					b.Hover.Out()
				}
			}
		case actLeftDown:
			switch {
			case i < 0:
				b.Close()
			case i == open:
				b.Close()
			default:
				b.open1(i, false)
			}
		}
		return true, nil
	})
}
