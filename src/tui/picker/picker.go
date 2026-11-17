// Package picker is the native, mouse-first list picker that replaces the
// app's one-pane dialogs (palette, recent, yank, sessions, select/ui,
// thinking, settings lists, …).
//
// A Picker shows a title, an optional message, an optional filter line,
// a scrolling list of rows (section headers, disabled rows, right-aligned
// or inline hints, ANSI labels drawn through components/cells), a
// scrollbar when the list overflows, and a footer hint line.
//
// It is a tview.Primitive and a widgets.Popup: Open(host) floats it as a
// modal box over a widgets.Overlay (sized like today's dialogs: 62–100
// cells wide, 12–20 rows of list), or put it in a page/Flex and it fills
// whatever rect it is given.
//
// Mouse: moving over a row highlights it, a single click activates it, the
// wheel scrolls, the scrollbar can be clicked or dragged, a right press on
// a row calls OnRowMenu (so the app can open a ContextMenu), and — as an
// Overlay popup — a press outside dismisses it (OnCancel).
//
// Keys (app updateListDialog parity): ↑/↓ and Ctrl+P/Ctrl+N move with
// wrap-around, PgUp/PgDn page, Home/End jump, Enter activates, Esc/Ctrl+C
// cancel, typing (incl. space) filters and Backspace deletes a rune. OnKey
// sees every key first, for the per-dialog extras (Tab scope, Del delete,
// Ctrl+Y copy, y/n confirm).
//
// Like tview's own primitives it is not locked: mutate it on the UI
// goroutine (Application.QueueUpdateDraw).
package picker

import (
	"fmt"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
	"pitago/src/components/cellwidth"
	"pitago/src/components/tstyle"
	"pitago/src/tui/widgets"
)

// Item is one row of data.
type Item struct {
	ID       string
	Label    string // may carry ANSI styling; first line only is shown
	Hint     string // right-aligned (or inline) secondary text
	Section  string // rows sharing a Section get one header above them
	Disabled bool   // drawn dim, never highlighted, never activated
	Keywords string // extra filter haystack (provider, specs, …), not shown
}

// HintMode places Item.Hint.
type HintMode int

const (
	// HintRight right-aligns the hint in the row (mouse-first look).
	HintRight HintMode = iota
	// HintInline puts "— hint" after the label, like today's dialogs.
	HintInline
)

// FilterMode chooses who filters.
type FilterMode int

const (
	// FilterLocal matches the filter against Label, Hint and Keywords
	// (case-insensitive substring, the app's Reindex rule).
	FilterLocal FilterMode = iota
	// FilterExternal shows Items as given: the app filters (or queries,
	// e.g. the npm marketplace) in OnFilter and calls SetItems.
	FilterExternal
)

// Styles styles a Picker.
type Styles struct {
	Box, Border, Title, Message, Filter, Row, Selected, Disabled,
	Hint, Section, Footer, Empty, Track, Thumb tcell.Style
}

// DefaultStyles derives styles from tview.Styles.
func DefaultStyles() Styles {
	base := tcell.StyleDefault.Background(tview.Styles.PrimitiveBackgroundColor).
		Foreground(tview.Styles.PrimaryTextColor)
	muted := base.Foreground(tview.Styles.SecondaryTextColor)
	return Styles{
		Box: base, Border: base.Foreground(tview.Styles.BorderColor),
		Title: base.Bold(true), Message: muted, Filter: muted, Row: muted,
		Selected: base.Background(tview.Styles.ContrastBackgroundColor).Bold(true),
		Disabled: muted.Dim(true), Hint: muted, Section: base.Bold(true),
		Footer: muted, Empty: muted, Track: base.Foreground(tview.Styles.BorderColor),
		Thumb: muted,
	}
}

// StylesFromPalette matches today's dialog look for a theme: rounded
// border in Border, bold Text title, muted rows/hints/footer (statusBar /
// toolStyle), HiBg/HiFg bold highlight (rowHiStyle), bold section headers
// (sideTitleStyle). bg is the surface ("" = terminal background).
func StylesFromPalette(p tstyle.Palette, bg string) Styles {
	base := tcell.StyleDefault.Background(tstyle.Color(bg)).Foreground(p.Text)
	muted := base.Foreground(p.Muted)
	return Styles{
		Box: base, Border: base.Foreground(p.Border),
		Title: base.Bold(true), Message: muted, Filter: muted, Row: muted,
		Selected: base.Background(p.HiBg).Foreground(p.HiFg).Bold(true),
		Disabled: muted.Dim(true), Hint: muted, Section: base.Bold(true),
		Footer: muted, Empty: muted, Track: base.Foreground(p.Border), Thumb: muted,
	}
}

type entry struct {
	Item
	hay    string     // lowercased filter haystack
	label  cells.Line // parsed lazily
	parsed bool
}

type row struct {
	item   int    // index into items, -1 for a header
	header string // section header text
}

// Picker is the list picker primitive.
type Picker struct {
	*tview.Box

	Title, Message, Footer string
	Filterable             bool
	FilterPrompt           string // default "filter: "
	FilterMode             FilterMode
	HintMode               HintMode
	SearchHints            bool   // match the filter against hints too (default true)
	ShowPosition           bool   // append " (i/n)" to the footer
	EmptyText              string // default "— no match —"
	CloseOnSelect          bool   // default true (false: settings-style lists)
	Styles                 Styles
	WheelStep              int // rows per wheel notch (default 3)

	// OnSelect gets the activated row's ID. When the picker is an open
	// Overlay popup and CloseOnSelect is set, it is already closed.
	OnSelect func(id string)
	// OnCancel runs once when the picker is dismissed: Esc, Ctrl+C, a
	// press outside it (popup), or Close.
	OnCancel func()
	// OnFilter runs after every filter edit with the new text.
	OnFilter func(text string)
	// OnHighlight runs when the highlighted row changes ("" = none), for
	// detail panes that follow the cursor.
	OnHighlight func(id string)
	// OnRowMenu runs on a right press over a row (enabled or not) with the
	// screen position, so the app can open a ContextMenu there.
	OnRowMenu func(id string, x, y int)
	// OnKey sees every key first; return true to consume it.
	OnKey func(e *tcell.EventKey) bool

	items []entry
	vis   []int // filtered item indices, in order
	pos   []int // item index → vis position, -1 when filtered out
	rows  []row
	rowOf []int // vis position → row index
	sel   int   // vis position, -1 none
	top   int   // first list row on screen
	text  string

	fullscreen bool
	host       *widgets.Overlay
	closed     bool
	dragging   bool

	// list geometry from the last Draw (screen coords)
	lx, ly, lw, lh int
	sbX            int // scrollbar column, -1 none
}

// New returns an empty picker with the app's defaults.
func New(title string) *Picker {
	return &Picker{
		Box: tview.NewBox(), Title: title, FilterPrompt: "filter: ",
		SearchHints: true, EmptyText: "— no match —", CloseOnSelect: true,
		Styles: DefaultStyles(), WheelStep: 3, sel: -1, sbX: -1,
	}
}

// --- data -------------------------------------------------------------------

// SetItems replaces the rows, keeping the highlight on the same ID when it
// survives (else the first enabled row).
func (p *Picker) SetItems(items []Item) {
	keep := p.SelectedID()
	p.items = make([]entry, len(items))
	for i, it := range items {
		p.items[i] = entry{Item: it, hay: haystack(it)}
	}
	p.reindex()
	if !p.selectID(keep) {
		p.setSel(p.firstEnabled(0, 1))
	}
	p.top = 0
	p.ensureVisible()
}

func haystack(it Item) string {
	return strings.ToLower(cellwidth.Strip(it.Label) + "\x00" + it.Hint + "\x00" + it.Keywords)
}

// Items returns the current items.
func (p *Picker) Items() []Item {
	out := make([]Item, len(p.items))
	for i, e := range p.items {
		out[i] = e.Item
	}
	return out
}

// SetSelected highlights the row with id; false when it is not visible
// or disabled.
func (p *Picker) SetSelected(id string) bool {
	if !p.selectID(id) {
		return false
	}
	p.ensureVisible()
	return true
}

// SelectedID is the highlighted row's ID ("" for none).
func (p *Picker) SelectedID() string {
	if it, ok := p.Selected(); ok {
		return it.ID
	}
	return ""
}

// Selected is the highlighted item.
func (p *Picker) Selected() (Item, bool) {
	if p.sel < 0 || p.sel >= len(p.vis) {
		return Item{}, false
	}
	return p.items[p.vis[p.sel]].Item, true
}

// Position is the 1-based highlighted position among visible items and
// their count (the settings footer's "(6/33)").
func (p *Picker) Position() (int, int) { return p.sel + 1, len(p.vis) }

// Filter returns the filter text.
func (p *Picker) Filter() string { return p.text }

// SetFilter replaces the filter text (no OnFilter call).
func (p *Picker) SetFilter(s string) {
	p.text = s
	p.refilter()
}

// VisibleIDs lists the IDs that pass the filter, in order.
func (p *Picker) VisibleIDs() []string {
	out := make([]string, len(p.vis))
	for i, ix := range p.vis {
		out[i] = p.items[ix].ID
	}
	return out
}

func (p *Picker) selectID(id string) bool {
	if id == "" {
		return false
	}
	for i, ix := range p.vis {
		if p.items[ix].ID == id && !p.items[ix].Disabled {
			p.setSel(i)
			return true
		}
	}
	return false
}

func (p *Picker) setSel(i int) {
	if i == p.sel {
		return
	}
	p.sel = i
	if p.OnHighlight != nil {
		p.OnHighlight(p.SelectedID())
	}
}

// reindex rebuilds vis/rows from items and the filter.
func (p *Picker) reindex() {
	p.vis = p.vis[:0]
	if cap(p.pos) < len(p.items) {
		p.pos = make([]int, len(p.items))
	}
	p.pos = p.pos[:len(p.items)]
	f := strings.ToLower(p.text)
	for i, e := range p.items {
		p.pos[i] = -1
		if p.FilterMode == FilterLocal && f != "" && !p.matches(e, f) {
			continue
		}
		p.pos[i] = len(p.vis)
		p.vis = append(p.vis, i)
	}
	p.rows = p.rows[:0]
	p.rowOf = p.rowOf[:0]
	last := ""
	for _, ix := range p.vis {
		if s := p.items[ix].Section; s != "" && s != last {
			p.rows = append(p.rows, row{item: -1, header: s})
		}
		last = p.items[ix].Section
		p.rowOf = append(p.rowOf, len(p.rows))
		p.rows = append(p.rows, row{item: ix})
	}
}

func (p *Picker) matches(e entry, f string) bool {
	if p.SearchHints {
		return strings.Contains(e.hay, f)
	}
	label := strings.ToLower(cellwidth.Strip(e.Label))
	return strings.Contains(label, f) || strings.Contains(strings.ToLower(e.Keywords), f)
}

// refilter re-applies the filter; the highlight resets to the first
// enabled row (app: Reindex clamps the cursor to 0) and the list to top.
func (p *Picker) refilter() {
	p.reindex()
	p.sel = -1
	p.setSel(p.firstEnabled(0, 1))
	if p.sel < 0 && p.OnHighlight != nil {
		p.OnHighlight("")
	}
	p.top = 0
	p.ensureVisible()
}

func (p *Picker) editFilter(s string) {
	p.text = s
	p.refilter()
	if p.OnFilter != nil {
		p.OnFilter(s)
	}
}

// --- navigation -------------------------------------------------------------

// firstEnabled scans vis from i in direction d (no wrap), -1 if none.
func (p *Picker) firstEnabled(i, d int) int {
	for ; i >= 0 && i < len(p.vis); i += d {
		if !p.items[p.vis[i]].Disabled {
			return i
		}
	}
	return -1
}

// step moves the highlight by one enabled row with wrap-around.
func (p *Picker) step(d int) {
	n := len(p.vis)
	if n == 0 {
		return
	}
	i := p.sel
	for k := 0; k < n; k++ {
		i = ((i+d)%n + n) % n
		if !p.items[p.vis[i]].Disabled {
			p.setSel(i)
			p.ensureVisible()
			return
		}
	}
}

// page moves by about one list height, clamped (no wrap).
func (p *Picker) page(d int) {
	if len(p.vis) == 0 {
		return
	}
	target := max(0, min(len(p.vis)-1, max(p.sel, 0)+d*max(1, p.listRows()-1)))
	i := p.firstEnabled(target, d)
	if i < 0 {
		i = p.firstEnabled(target, -d)
	}
	if i >= 0 {
		p.setSel(i)
		p.ensureVisible()
	}
}

func (p *Picker) listRows() int {
	if p.lh > 0 {
		return p.lh
	}
	return 12
}

// ensureVisible scrolls minimally so the highlighted row (and its section
// header, when it is the first item under one) is on screen.
func (p *Picker) ensureVisible() {
	h := p.listRows()
	maxTop := max(0, len(p.rows)-h)
	if p.sel >= 0 && p.sel < len(p.rowOf) {
		r := p.rowOf[p.sel]
		first := r
		if r > 0 && p.rows[r-1].item < 0 {
			first = r - 1 // keep the header with its first item
		}
		if first < p.top {
			p.top = first
		}
		if r >= p.top+h {
			p.top = r - h + 1
		}
	}
	p.top = max(0, min(p.top, maxTop))
}

func (p *Picker) scrollBy(n int) {
	p.top = max(0, min(p.top+n, max(0, len(p.rows)-p.listRows())))
}

func (p *Picker) activate() {
	it, ok := p.Selected()
	if !ok || it.Disabled {
		return
	}
	if p.CloseOnSelect && p.host != nil {
		p.closed = true // a selection is not a cancel
		p.host.Remove(p)
	}
	if p.OnSelect != nil {
		p.OnSelect(it.ID)
	}
}

func (p *Picker) cancel() {
	if p.host != nil && p.host.Has(p) {
		p.host.Remove(p) // Dismissed fires OnCancel
		return
	}
	p.fireCancel()
}

func (p *Picker) fireCancel() {
	if p.closed {
		return
	}
	p.closed = true
	if p.OnCancel != nil {
		p.OnCancel()
	}
}

// --- popup lifecycle ----------------------------------------------------------

// Open floats p over host as a modal popup.
func (p *Picker) Open(host *widgets.Overlay) {
	p.host, p.closed, p.dragging = host, false, false
	host.Push(p, true)
}

// Close dismisses p (OnCancel runs if it was not closed by a selection).
func (p *Picker) Close() { p.cancel() }

// IsOpen reports whether p is on its overlay.
func (p *Picker) IsOpen() bool { return p.host != nil && p.host.Has(p) }

// Dismissed implements widgets.Dismisser (press outside, CloseModals).
func (p *Picker) Dismissed() { p.fireCancel() }

// SetFullscreen makes Place fill the overlay instead of centering a box.
func (p *Picker) SetFullscreen(on bool) { p.fullscreen = on }

// Place implements widgets.Popup: centered box sized like today's dialogs.
func (p *Picker) Place(b widgets.Rect) {
	if p.fullscreen {
		p.SetRect(b.X, b.Y, b.W, b.H)
		return
	}
	w := min(max(b.W-10, 62), 100, b.W)
	win := min(max(b.H-10, 12), 20)
	// The list area never grows past what the full (unfiltered) list
	// needs, so short pickers stay compact and the box does not jump
	// while filtering.
	need := len(p.items) + p.sectionCount()
	if need < 1 {
		need = 1
	}
	listH := min(win, need)
	h := min(p.chromeRows()+listH, b.H)
	p.SetRect(b.X+(b.W-w)/2, b.Y+max(0, (b.H-h)/2), w, h)
}

func (p *Picker) sectionCount() int {
	n, last := 0, ""
	for _, e := range p.items {
		if e.Section != "" && e.Section != last {
			n++
		}
		last = e.Section
	}
	return n
}

// chromeRows: border 2 + padding 2 + title + message? + filter? + blank
// + blank + footer.
func (p *Picker) chromeRows() int {
	n := 4 + 1 + 1 + 1 + 1
	if p.Message != "" {
		n++
	}
	if p.Filterable {
		n++
	}
	return n
}

// --- drawing ---------------------------------------------------------------------

const (
	padX = 3 // dlgStyle Padding(1, 3)
	padY = 1
)

// Draw implements tview.Primitive.
func (p *Picker) Draw(scr tcell.Screen) {
	x, y, w, h := p.GetRect()
	if w <= 2 || h <= 2 {
		return
	}
	st := p.Styles
	for yy := y; yy < y+h; yy++ {
		for xx := x; xx < x+w; xx++ {
			scr.SetContent(xx, yy, ' ', nil, st.Box)
		}
	}
	p.drawBorder(scr, x, y, w, h)

	cx, cw := x+1+padX, w-2-2*padX
	cy, bottom := y+1+padY, y+h-1-padY // content rows [cy, bottom)
	if cw < 4 || bottom-cy < 3 {
		return
	}
	put(scr, cx, cy, cw, p.Title, st.Title)
	cy++
	if p.Message != "" {
		put(scr, cx, cy, cw, p.Message, st.Message)
		cy++
	}
	if p.Filterable {
		put(scr, cx, cy, cw, p.FilterPrompt+p.text+"▌", st.Filter)
		cy++
	}
	cy++ // blank
	footY := bottom - 1
	listBottom := footY - 1 // blank above the footer
	lh := max(0, listBottom-cy)
	p.lx, p.ly, p.lw = cx, cy, cw
	if lh != p.lh {
		// New list height (first draw, resize): bring the highlight into
		// view. Otherwise only clamp — a wheel or scrollbar scroll must
		// not snap back to the highlighted row on the next frame.
		p.lh = lh
		p.ensureVisible()
	} else {
		p.scrollBy(0)
	}
	p.drawList(scr)
	put(scr, cx, footY, cw, p.footer(), st.Footer)
}

func (p *Picker) footer() string {
	f := p.Footer
	if p.ShowPosition && len(p.vis) > 0 {
		i, n := p.Position()
		f += fmt.Sprintf(" (%d/%d)", max(i, 1), n)
	}
	return f
}

func (p *Picker) drawBorder(scr tcell.Screen, x, y, w, h int) {
	b := p.Styles.Border
	for xx := x + 1; xx < x+w-1; xx++ {
		scr.SetContent(xx, y, '─', nil, b)
		scr.SetContent(xx, y+h-1, '─', nil, b)
	}
	for yy := y + 1; yy < y+h-1; yy++ {
		scr.SetContent(x, yy, '│', nil, b)
		scr.SetContent(x+w-1, yy, '│', nil, b)
	}
	scr.SetContent(x, y, '╭', nil, b)
	scr.SetContent(x+w-1, y, '╮', nil, b)
	scr.SetContent(x, y+h-1, '╰', nil, b)
	scr.SetContent(x+w-1, y+h-1, '╯', nil, b)
}

func (p *Picker) drawList(scr tcell.Screen) {
	st := p.Styles
	if p.lh <= 0 {
		return
	}
	if len(p.vis) == 0 {
		put(scr, p.lx, p.ly, p.lw, p.EmptyText, st.Empty)
		p.sbX = -1
		return
	}
	w := p.lw
	p.sbX = -1
	if len(p.rows) > p.lh {
		p.sbX = p.lx + p.lw - 1
		w-- // scrollbar column
	}
	for r := 0; r < p.lh && p.top+r < len(p.rows); r++ {
		rw := p.rows[p.top+r]
		yy := p.ly + r
		if rw.item < 0 {
			put(scr, p.lx, yy, w, "  "+strings.ToUpper(rw.header), st.Section)
			continue
		}
		p.drawItem(scr, yy, w, rw.item)
	}
	if p.sbX >= 0 {
		p.drawScrollbar(scr)
	}
}

func (p *Picker) drawItem(scr tcell.Screen, y, w, ix int) {
	st := p.Styles
	e := &p.items[ix]
	selected := p.sel >= 0 && p.vis[p.sel] == ix
	rowSt, hintSt := st.Row, st.Hint
	switch {
	case e.Disabled:
		rowSt, hintSt = st.Disabled, st.Disabled
	case selected:
		rowSt = st.Selected
		hintSt = st.Selected
		for xx := p.lx; xx < p.lx+w; xx++ { // full-width highlight (paintRow)
			scr.SetContent(xx, y, ' ', nil, rowSt)
		}
	}
	mark := "  "
	if selected {
		mark = "▸ "
	}
	col := p.lx + put(scr, p.lx, y, w, mark, rowSt)
	room := p.lx + w - col
	if room <= 0 {
		return
	}
	if !e.parsed {
		e.parsed = true
		label := e.Label
		if i := strings.IndexByte(label, '\n'); i >= 0 {
			label = label[:i]
		}
		if ls := cells.Parse(label); len(ls) > 0 {
			e.label = ls[0]
		}
	}
	hint := e.Hint
	hintW := cellwidth.StringWidth(hint)
	labelRoom := room
	switch {
	case hint == "":
	case p.HintMode == HintRight && hintW+2 < room-4:
		labelRoom = room - hintW - 2
	case p.HintMode == HintInline:
	default:
		hint = "" // no room: label wins
	}
	used := drawCells(scr, col, y, labelRoom, e.label, rowSt)
	col += used
	if hint == "" {
		return
	}
	if p.HintMode == HintRight {
		put(scr, p.lx+w-hintW, y, hintW, hint, hintSt)
		return
	}
	if rest := p.lx + w - col; rest > 6 { // inline: "  — hint"
		put(scr, col, y, rest, "  — "+hint, hintSt)
	}
}

// drawCells paints a label line clipped to w with "…", layering the row
// style under the label's own SGR: cells with default fg/bg take the
// row's colors and every cell gets the row's attributes, so a colored
// label keeps its color on the highlight (app paintRow).
func drawCells(scr tcell.Screen, x, y, w int, ln cells.Line, row tcell.Style) int {
	if w <= 0 {
		return 0
	}
	cut := ln.Width() > w
	limit := w
	if cut {
		limit = w - 1
	}
	col := 0
	for _, c := range ln {
		if col+c.Width > limit {
			break
		}
		r, comb := split(c.Grapheme)
		scr.SetContent(x+col, y, r, comb, layer(c.Style, row))
		col += c.Width
	}
	if cut {
		scr.SetContent(x+col, y, '…', nil, row)
		col++
	}
	return col
}

func layer(cell, row tcell.Style) tcell.Style {
	cf, cb, ca := cell.Decompose()
	rf, rb, ra := row.Decompose()
	out := row
	if cf != tcell.ColorDefault {
		out = out.Foreground(cf)
	} else {
		out = out.Foreground(rf)
	}
	if cb != tcell.ColorDefault {
		out = out.Background(cb)
	} else {
		out = out.Background(rb)
	}
	return out.Attributes(ca | ra)
}

func (p *Picker) drawScrollbar(scr tcell.Screen) {
	st := p.Styles
	total, h := len(p.rows), p.lh
	thumbH := max(1, h*h/total)
	maxTop := total - h
	thumbY := 0
	if maxTop > 0 {
		thumbY = (h - thumbH) * p.top / maxTop
	}
	for r := 0; r < h; r++ {
		ch, s := '│', st.Track
		if r >= thumbY && r < thumbY+thumbH {
			ch, s = '┃', st.Thumb
		}
		scr.SetContent(p.sbX, p.ly+r, ch, nil, s)
	}
}

// put prints plain text clipped to w with "…"; returns columns used.
func put(scr tcell.Screen, x, y, w int, s string, st tcell.Style) int {
	if w <= 0 {
		return 0
	}
	if cellwidth.StringWidth(s) > w {
		s = cellwidth.Truncate(s, w, "…")
	}
	col := 0
	cellwidth.Graphemes(s, func(g string, gw int) bool {
		if col+gw > w {
			return false
		}
		r, comb := split(g)
		scr.SetContent(x+col, y, r, comb, st)
		col += gw
		return true
	})
	return col
}

func split(g string) (rune, []rune) {
	rs := []rune(g)
	if len(rs) == 0 {
		return ' ', nil
	}
	return rs[0], rs[1:]
}

// --- input -----------------------------------------------------------------------

// rowAt maps a screen point to a list row index (-1 outside the rows).
func (p *Picker) rowAt(x, y int) int {
	if x < p.lx || x >= p.lx+p.lw || y < p.ly || y >= p.ly+p.lh || x == p.sbX {
		return -1
	}
	r := p.top + y - p.ly
	if r >= len(p.rows) {
		return -1
	}
	return r
}

// visOfRow is the vis position of list row r (-1 for headers).
func (p *Picker) visOfRow(r int) int {
	if r < 0 || p.rows[r].item < 0 {
		return -1
	}
	return p.pos[p.rows[r].item]
}

func (p *Picker) scrollTo(y int) {
	total, h := len(p.rows), p.lh
	if h <= 0 || total <= h {
		return
	}
	frac := float64(y-p.ly) / float64(max(1, h-1))
	p.top = max(0, min(total-h, int(frac*float64(total-h)+0.5)))
}

// MouseHandler implements tview.Primitive.
func (p *Picker) MouseHandler() func(tview.MouseAction, *tcell.EventMouse, func(tview.Primitive)) (bool, tview.Primitive) {
	return p.WrapMouseHandler(func(a tview.MouseAction, e *tcell.EventMouse, setFocus func(tview.Primitive)) (bool, tview.Primitive) {
		x, y := e.Position()
		if p.dragging {
			switch a {
			case tview.MouseMove:
				p.scrollTo(y)
				return true, p
			case tview.MouseLeftUp:
				p.dragging = false
				return true, nil
			}
		}
		if !p.InRect(x, y) {
			return false, nil
		}
		if p.sbX >= 0 && x == p.sbX && y >= p.ly && y < p.ly+p.lh {
			if a == tview.MouseLeftDown {
				p.dragging = true
				p.scrollTo(y)
				return true, p
			}
			return true, nil
		}
		switch a {
		case tview.MouseScrollUp:
			p.scrollBy(-max(1, p.WheelStep))
			return true, nil
		case tview.MouseScrollDown:
			p.scrollBy(max(1, p.WheelStep))
			return true, nil
		}
		r := p.rowAt(x, y)
		vi := p.visOfRow(r)
		switch a {
		case tview.MouseMove:
			if vi >= 0 && !p.items[p.vis[vi]].Disabled {
				p.setSel(vi)
			}
		case tview.MouseLeftDown:
			setFocus(p)
		case tview.MouseLeftClick, tview.MouseLeftDoubleClick:
			if vi >= 0 && !p.items[p.vis[vi]].Disabled {
				p.setSel(vi)
				p.activate()
			}
		case tview.MouseRightDown:
			if vi >= 0 && p.OnRowMenu != nil {
				p.OnRowMenu(p.items[p.vis[vi]].ID, x, y)
			}
		}
		return true, nil
	})
}

// InputHandler implements tview.Primitive.
func (p *Picker) InputHandler() func(*tcell.EventKey, func(tview.Primitive)) {
	return p.WrapInputHandler(func(e *tcell.EventKey, _ func(tview.Primitive)) {
		if p.OnKey != nil && p.OnKey(e) {
			return
		}
		switch e.Key() {
		case tcell.KeyUp, tcell.KeyCtrlP:
			p.step(-1)
		case tcell.KeyDown, tcell.KeyCtrlN:
			p.step(1)
		case tcell.KeyPgUp:
			p.page(-1)
		case tcell.KeyPgDn:
			p.page(1)
		case tcell.KeyHome:
			if i := p.firstEnabled(0, 1); i >= 0 {
				p.setSel(i)
				p.ensureVisible()
			}
		case tcell.KeyEnd:
			if i := p.firstEnabled(len(p.vis)-1, -1); i >= 0 {
				p.setSel(i)
				p.ensureVisible()
			}
		case tcell.KeyEnter:
			p.activate()
		case tcell.KeyEsc, tcell.KeyCtrlC:
			p.cancel()
		case tcell.KeyBackspace, tcell.KeyBackspace2:
			if p.Filterable && p.text != "" {
				r := []rune(p.text) // rune-wise: byte trim corrupts Vietnamese
				p.editFilter(string(r[:len(r)-1]))
			}
		case tcell.KeyRune:
			if p.Filterable && e.Modifiers()&(tcell.ModAlt|tcell.ModCtrl) == 0 {
				p.editFilter(p.text + string(e.Rune()))
			}
		}
	})
}

// PasteHandler appends pasted text (first line) to the filter.
func (p *Picker) PasteHandler() func(string, func(tview.Primitive)) {
	return p.WrapPasteHandler(func(text string, _ func(tview.Primitive)) {
		if !p.Filterable {
			return
		}
		if i := strings.IndexAny(text, "\r\n"); i >= 0 {
			text = text[:i]
		}
		if text != "" {
			p.editFilter(p.text + text)
		}
	})
}
