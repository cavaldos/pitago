// Package chatview is the native tview chat surface: a virtualized,
// scrollable list of pre-rendered blocks.
//
// The app keeps rendering. Each Block is an ANSI string the app produced at
// a given width (same strings renderBlocks builds today); ChatView parses a
// block into cells only when one of its lines is first drawn, caches the
// result per (ID, Width, Text), and paints only the visible rows. Content
// coordinates match src/app's: an absolute line is 0-based within the whole
// transcript (blocks stacked in order), a column is a 0-based cell.
//
// Scrolling mirrors the bubbles viewport API the app uses (YOffset,
// SetYOffset, GotoTop/Bottom, AtTop/Bottom, ScrollUp/Down, page and
// half-page moves, TotalLineCount, ScrollPercent), plus Refresh's
// "follow the tail only when already at the bottom" rule built in: every
// content mutation and every height change keeps a pinned view pinned and
// leaves a reader scrolled into history where they were.
//
// Hooks for the app: HitTest (screen cell → block/line/col), a selection
// range painter, a hovered-block highlight, the jump-to-latest chip (paint
// and hit test from one geometry), a generic overlay callback, and
// PendingPassthrough for kitty/iTerm2 graphics sequences found in block text
// — the runtime writes those to the tty after Screen.Show; ChatView never
// touches the tty.
//
// All methods are safe to call from any goroutine; tview calls Draw on its
// own.
package chatview

import (
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
	"pitago/src/components/cellwidth"
)

// Block is one rendered transcript unit.
//
// Text is the app's rendered ANSI string for the block at Width columns.
// It occupies one row per line, with a single trailing "\n" not starting a
// row of its own (the same count as app's ly()): "a\n\n" is two rows ("a"
// and a blank spacer), "" is zero rows (a hidden/folded block).
//
// ID must be unique among the blocks. A block whose ID, Width and Text
// match the cached entry keeps its parsed cells. Text is always compared
// (a version counter alone could let a stale parse survive an app bug);
// passing the app's cached string makes that an O(1) pointer-equal check.
// Version is carried for the app's own bookkeeping (e.g. blockKey) and is
// never trusted on its own.
type Block struct {
	ID      string
	Version uint64
	Width   int
	Text    string
}

// Point is an absolute content coordinate (src/app's Point).
type Point struct {
	Line int
	Col  int
}

// Hit is what HitTest finds under a screen cell.
type Hit struct {
	BlockID string
	Block   int // index in the block list
	Line    int // row within the block
	AbsLine int // absolute content line
	Col     int // cell column within the line (0 = left edge of the view)
}

// Selection is a drag selection over absolute content points. Anchor and
// Focus may be in any order; the range is [start, end) in columns, spanning
// whole lines in between (src/app's overlaySelection). MinCol, when set,
// returns the first selectable column of a line (the gutter clamp: app's
// gutterAt) so chrome is never painted as selected.
type Selection struct {
	Active        bool
	Anchor, Focus Point
	MinCol        func(absLine int) int
}

type entry struct {
	Block
	rows   int
	lines  []cells.Line // nil until first drawn
	blank  []bool       // per row: no visible text (hover skips these)
	parsed bool
}

// ChatView is the chat transcript primitive.
type ChatView struct {
	*tview.Box

	mu      sync.Mutex
	blocks  []*entry
	index   map[string]int
	starts  []int // starts[i] = absolute line of blocks[i]; len = len(blocks)+1
	yOffset int
	pinned  bool

	sel        Selection
	selStyle   func(tcell.Style) tcell.Style
	hoverID    string
	hoverStyle func(tcell.Style) tcell.Style

	chipText  string
	chipStyle tcell.Style
	chipOn    bool

	overlay func(scr tcell.Screen, x, y, w, h int)
	pending []string
	wheel   int
}

// New returns an empty chat view pinned to the bottom.
func New() *ChatView {
	return &ChatView{
		Box:        tview.NewBox(),
		index:      map[string]int{},
		starts:     []int{0},
		pinned:     true,
		selStyle:   func(s tcell.Style) tcell.Style { return s.Reverse(true) },
		hoverStyle: func(s tcell.Style) tcell.Style { return s.Dim(false).Bold(true) },
		wheel:      3,
	}
}

// rowsOf counts the rows Text occupies (app's ly()).
func rowsOf(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if strings.HasSuffix(text, "\n") {
		return n
	}
	return n + 1
}

func sameRender(a, b Block) bool {
	return a.Width == b.Width && a.Text == b.Text
}

// newEntry builds the entry for b, reusing old's parse when unchanged.
func (v *ChatView) newEntry(b Block, old *entry) *entry {
	if old != nil && sameRender(old.Block, b) {
		old.Block = b
		return old
	}
	e := &entry{Block: b, rows: rowsOf(b.Text)}
	if strings.Contains(b.Text, "\x1b_G") || strings.Contains(b.Text, "\x1b]1337;") {
		v.pending = append(v.pending, cells.Passthrough(b.Text)...)
	}
	return e
}

// --- content mutation ----------------------------------------------------

// SetBlocks replaces the whole transcript (viewport.SetContent). Blocks
// whose ID/Version/Width are unchanged keep their parsed cells.
func (v *ChatView) SetBlocks(bs []Block) {
	v.mu.Lock()
	defer v.mu.Unlock()
	follow := v.atBottom()
	old := v.index
	oldBlocks := v.blocks
	v.blocks = make([]*entry, len(bs))
	v.index = make(map[string]int, len(bs))
	for i, b := range bs {
		var prev *entry
		if j, ok := old[b.ID]; ok {
			prev = oldBlocks[j]
		}
		v.blocks[i] = v.newEntry(b, prev)
		v.index[b.ID] = i
	}
	v.reindex(0)
	v.afterMutation(follow)
}

// AppendBlock adds b at the end. If a block with b.ID exists it is updated
// in place instead.
func (v *ChatView) AppendBlock(b Block) {
	v.mu.Lock()
	defer v.mu.Unlock()
	follow := v.atBottom()
	if i, ok := v.index[b.ID]; ok {
		v.blocks[i] = v.newEntry(b, v.blocks[i])
		v.reindex(i)
	} else {
		v.blocks = append(v.blocks, v.newEntry(b, nil))
		v.index[b.ID] = len(v.blocks) - 1
		v.starts = append(v.starts, v.starts[len(v.starts)-1]+v.blocks[len(v.blocks)-1].rows)
	}
	v.afterMutation(follow)
}

// UpdateBlock replaces the block with b.ID and reports whether it existed.
// Streaming text updates the last block this way every flush.
func (v *ChatView) UpdateBlock(b Block) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, ok := v.index[b.ID]
	if !ok {
		return false
	}
	follow := v.atBottom()
	v.blocks[i] = v.newEntry(b, v.blocks[i])
	v.reindex(i)
	v.afterMutation(follow)
	return true
}

// RemoveBlock drops the block with id and reports whether it existed.
func (v *ChatView) RemoveBlock(id string) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, ok := v.index[id]
	if !ok {
		return false
	}
	follow := v.atBottom()
	v.blocks = append(v.blocks[:i], v.blocks[i+1:]...)
	delete(v.index, id)
	for j := i; j < len(v.blocks); j++ {
		v.index[v.blocks[j].ID] = j
	}
	v.starts = v.starts[:len(v.blocks)+1]
	v.reindex(i)
	v.afterMutation(follow)
	return true
}

// reindex recomputes starts from block i on.
func (v *ChatView) reindex(i int) {
	if cap(v.starts) < len(v.blocks)+1 {
		ns := make([]int, len(v.blocks)+1, 2*len(v.blocks)+1)
		copy(ns, v.starts)
		v.starts = ns
	}
	v.starts = v.starts[:len(v.blocks)+1]
	v.starts[0] = 0
	if i < 0 {
		i = 0
	}
	for j := i; j < len(v.blocks); j++ {
		v.starts[j+1] = v.starts[j] + v.blocks[j].rows
	}
}

// afterMutation applies Refresh's follow rule: stick to the tail only when
// the view was already there; otherwise keep the offset (clamped).
func (v *ChatView) afterMutation(follow bool) {
	if follow {
		v.yOffset = v.maxYOffset()
		v.pinned = true
		return
	}
	v.setYOffset(v.yOffset)
}

// --- geometry ------------------------------------------------------------

func (v *ChatView) total() int { return v.starts[len(v.starts)-1] }

func (v *ChatView) height() int {
	_, _, _, h := v.GetInnerRect()
	if h < 0 {
		return 0
	}
	return h
}

func (v *ChatView) maxYOffset() int {
	return max(0, v.total()-v.height())
}

func (v *ChatView) atBottom() bool { return v.yOffset >= v.maxYOffset() }

func (v *ChatView) setYOffset(n int) {
	v.yOffset = min(max(n, 0), v.maxYOffset())
	v.pinned = v.atBottom()
}

// SetRect keeps a pinned view pinned when the height changes (app's
// chatFrameGeometry re-pin), and clamps a reader's offset otherwise.
func (v *ChatView) SetRect(x, y, w, h int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	pinned := v.pinned
	v.Box.SetRect(x, y, w, h)
	if pinned {
		v.yOffset = v.maxYOffset()
		v.pinned = true
	} else {
		v.setYOffset(v.yOffset)
	}
}

// --- scroll API (bubbles viewport parity) --------------------------------

// YOffset is the absolute line drawn on the top row.
func (v *ChatView) YOffset() int { v.mu.Lock(); defer v.mu.Unlock(); return v.yOffset }

// SetYOffset scrolls to n, clamped so the last line stays reachable.
func (v *ChatView) SetYOffset(n int) { v.mu.Lock(); defer v.mu.Unlock(); v.setYOffset(n) }

// Height is the number of content rows (inner rect height).
func (v *ChatView) Height() int { v.mu.Lock(); defer v.mu.Unlock(); return v.height() }

// TotalLineCount is the number of content lines.
func (v *ChatView) TotalLineCount() int { v.mu.Lock(); defer v.mu.Unlock(); return v.total() }

// AtTop reports whether the first line is on the top row.
func (v *ChatView) AtTop() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.yOffset <= 0 }

// AtBottom reports whether the last line is on screen (or content fits).
func (v *ChatView) AtBottom() bool { v.mu.Lock(); defer v.mu.Unlock(); return v.atBottom() }

// GotoTop scrolls to the first line.
func (v *ChatView) GotoTop() { v.SetYOffset(0) }

// GotoBottom scrolls to the last line and re-pins the tail.
func (v *ChatView) GotoBottom() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.yOffset = v.maxYOffset()
	v.pinned = true
}

// ScrollUp moves the view n lines toward the top (viewport LineUp).
func (v *ChatView) ScrollUp(n int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setYOffset(v.yOffset - n)
}

// ScrollDown moves the view n lines toward the bottom (viewport LineDown).
func (v *ChatView) ScrollDown(n int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.setYOffset(v.yOffset + n)
}

// LineUp is ScrollUp (bubbles v1 name).
func (v *ChatView) LineUp(n int) { v.ScrollUp(n) }

// LineDown is ScrollDown (bubbles v1 name).
func (v *ChatView) LineDown(n int) { v.ScrollDown(n) }

// PageUp moves one view height up (viewport ViewUp).
func (v *ChatView) PageUp() { v.ScrollUp(max(1, v.Height())) }

// PageDown moves one view height down (viewport ViewDown).
func (v *ChatView) PageDown() { v.ScrollDown(max(1, v.Height())) }

// HalfPageUp moves half a view up.
func (v *ChatView) HalfPageUp() { v.ScrollUp(max(1, v.Height()/2)) }

// HalfPageDown moves half a view down.
func (v *ChatView) HalfPageDown() { v.ScrollDown(max(1, v.Height()/2)) }

// ScrollPercent is the viewport's 0..1 position (1 when content fits).
func (v *ChatView) ScrollPercent() float64 {
	v.mu.Lock()
	defer v.mu.Unlock()
	h, t := v.height(), v.total()
	if h >= t {
		return 1
	}
	p := float64(v.yOffset) / float64(t-h)
	return min(max(p, 0), 1)
}

// SetWheelStep sets the lines per wheel notch (default 3, as app's
// wheelTo). 0 disables wheel handling in MouseHandler.
func (v *ChatView) SetWheelStep(n int) { v.mu.Lock(); v.wheel = n; v.mu.Unlock() }

// --- lookups ---------------------------------------------------------------

// blockAt returns the index of the block holding absolute line abs.
func (v *ChatView) blockAt(abs int) int {
	// First block whose end (starts[i+1]) is beyond abs; empty blocks have
	// start == end and are skipped naturally.
	return sort.Search(len(v.blocks), func(i int) bool { return v.starts[i+1] > abs })
}

// BlockStart returns the absolute first line of block id (app's
// blockLine/blockRows) and whether the block exists.
func (v *ChatView) BlockStart(id string) (int, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, ok := v.index[id]
	if !ok {
		return 0, false
	}
	return v.starts[i], true
}

// BlockAtLine returns the ID of the block holding absolute line abs.
func (v *ChatView) BlockAtLine(abs int) (string, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if abs < 0 || abs >= v.total() {
		return "", false
	}
	return v.blocks[v.blockAt(abs)].ID, true
}

// parse fills e.lines on first use.
func (e *entry) parse() {
	if e.parsed {
		return
	}
	e.parsed = true
	if e.rows == 0 {
		return
	}
	text := strings.TrimSuffix(e.Text, "\n")
	e.lines = cells.Parse(text)
	e.blank = make([]bool, len(e.lines))
	for i, ln := range e.lines {
		e.blank[i] = strings.TrimSpace(ln.String()) == ""
	}
}

// line returns the parsed absolute line abs (nil past the end).
func (v *ChatView) line(abs int) (cells.Line, int, bool) {
	if abs < 0 || abs >= v.total() {
		return nil, -1, false
	}
	bi := v.blockAt(abs)
	e := v.blocks[bi]
	e.parse()
	r := abs - v.starts[bi]
	if r >= len(e.lines) {
		return nil, bi, true
	}
	return e.lines[r], bi, e.blank[r]
}

// LineText is the plain text of absolute line abs (selection copy source).
func (v *ChatView) LineText(abs int) string {
	v.mu.Lock()
	defer v.mu.Unlock()
	ln, _, _ := v.line(abs)
	return ln.String()
}

// LineWidth is the cell width of absolute line abs.
func (v *ChatView) LineWidth(abs int) int {
	v.mu.Lock()
	defer v.mu.Unlock()
	ln, _, _ := v.line(abs)
	return ln.Width()
}

// HitTest maps a screen cell to the content under it. ok is false outside
// the inner rect or below the last line.
func (v *ChatView) HitTest(x, y int) (Hit, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	ix, iy, w, h := v.GetInnerRect()
	if x < ix || x >= ix+w || y < iy || y >= iy+h {
		return Hit{}, false
	}
	abs := v.yOffset + (y - iy)
	if abs >= v.total() {
		return Hit{}, false
	}
	bi := v.blockAt(abs)
	return Hit{BlockID: v.blocks[bi].ID, Block: bi, Line: abs - v.starts[bi], AbsLine: abs, Col: x - ix}, true
}

// PointAt maps a screen cell to an absolute content point, clamped into
// the view like app's mousePoint (used while a drag leaves the frame).
func (v *ChatView) PointAt(x, y int) Point {
	v.mu.Lock()
	defer v.mu.Unlock()
	ix, iy, w, h := v.GetInnerRect()
	col := min(max(x-ix, 0), max(w-1, 0))
	row := min(max(y-iy, 0), max(h-1, 0))
	return Point{Line: v.yOffset + row, Col: col}
}

// --- decorations -----------------------------------------------------------

// SetSelection sets (or, with Active false, clears) the painted selection.
func (v *ChatView) SetSelection(s Selection) { v.mu.Lock(); v.sel = s; v.mu.Unlock() }

// ClearSelection removes the selection highlight.
func (v *ChatView) ClearSelection() { v.SetSelection(Selection{}) }

// SetSelectionStyle overrides the selected-cell transform (default reverse).
func (v *ChatView) SetSelectionStyle(fn func(tcell.Style) tcell.Style) {
	v.mu.Lock()
	v.selStyle = fn
	v.mu.Unlock()
}

// SetHover highlights the non-blank rows of block id ("" clears), like
// app's applyChatHover. It is suppressed while a selection is active.
func (v *ChatView) SetHover(id string) { v.mu.Lock(); v.hoverID = id; v.mu.Unlock() }

// SetHoverStyle overrides the hover transform (default bold, not faint).
func (v *ChatView) SetHoverStyle(fn func(tcell.Style) tcell.Style) {
	v.mu.Lock()
	v.hoverStyle = fn
	v.mu.Unlock()
}

// SetJumpChip configures the jump-to-latest chip: text is plain (no
// ANSI), drawn in style. An empty text disables the chip.
func (v *ChatView) SetJumpChip(text string, style tcell.Style) {
	v.mu.Lock()
	v.chipText, v.chipStyle, v.chipOn = text, style, text != ""
	v.mu.Unlock()
}

// Chip geometry constants, from app/jump_latest.go.
const (
	jumpLatestRowFromBottom = 2
	jumpLatestMinRows       = 5
)

// chipRect is the chip's screen position, or ok=false when it is not
// warranted: at the bottom, frame too short, or text too wide.
func (v *ChatView) chipRect() (x, y, w int, ok bool) {
	if !v.chipOn {
		return 0, 0, 0, false
	}
	ix, iy, iw, ih := v.GetInnerRect()
	if ih < jumpLatestMinRows || v.atBottom() {
		return 0, 0, 0, false
	}
	tw := cellwidth.StringWidth(v.chipText)
	if tw >= iw {
		return 0, 0, 0, false
	}
	return ix + (iw-tw)/2, iy + max(0, ih-1-jumpLatestRowFromBottom), tw, true
}

// ChipVisible reports whether the jump chip would be drawn now.
func (v *ChatView) ChipVisible() bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	_, _, _, ok := v.chipRect()
	return ok
}

// ChipAt reports whether screen cell (x, y) is on the drawn chip — the
// same geometry Draw paints, so click and paint cannot disagree.
func (v *ChatView) ChipAt(x, y int) bool {
	v.mu.Lock()
	defer v.mu.Unlock()
	cx, cy, cw, ok := v.chipRect()
	return ok && y == cy && x >= cx && x < cx+cw
}

// SetOverlay installs fn, called at the end of Draw with the inner rect,
// for app-owned floating UI (toasts, etc.). nil removes it.
func (v *ChatView) SetOverlay(fn func(scr tcell.Screen, x, y, w, h int)) {
	v.mu.Lock()
	v.overlay = fn
	v.mu.Unlock()
}

// PendingPassthrough returns, and forgets, the kitty APC / iTerm2 OSC 1337
// sequences found in block text since the last call, in arrival order.
// Each distinct block render is scanned once, so a sequence is surfaced
// once per (ID, Width, Text) — the runtime writes them to the tty after Show.
func (v *ChatView) PendingPassthrough() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	out := v.pending
	v.pending = nil
	return out
}

// --- drawing ---------------------------------------------------------------

// Draw paints the visible rows, then decorations, chip and overlay.
func (v *ChatView) Draw(scr tcell.Screen) {
	v.Box.DrawForSubclass(scr, v)
	v.mu.Lock()
	x, y, w, h := v.GetInnerRect()
	if v.pinned {
		v.yOffset = v.maxYOffset()
	} else {
		v.setYOffset(v.yOffset)
	}
	var selA, selB Point
	if v.sel.Active {
		selA, selB = v.sel.Anchor, v.sel.Focus
		if selB.Line < selA.Line || (selA.Line == selB.Line && selB.Col < selA.Col) {
			selA, selB = selB, selA
		}
	}
	hoverBlock := -1
	if i, ok := v.index[v.hoverID]; ok && v.hoverID != "" && !v.sel.Active {
		hoverBlock = i
	}
	for r := 0; r < h; r++ {
		abs := v.yOffset + r
		ln, bi, blank := v.line(abs)
		if bi < 0 {
			break
		}
		start, end := 0, 0
		if v.sel.Active && abs >= selA.Line && abs <= selB.Line {
			end = ln.Width()
			if abs == selA.Line {
				start = selA.Col
			}
			if abs == selB.Line {
				end = selB.Col
			}
			if v.sel.MinCol != nil {
				start = max(start, v.sel.MinCol(abs))
			}
		}
		hover := bi == hoverBlock && !blank
		v.drawRow(scr, x, y+r, w, ln, start, end, hover)
	}
	if cx, cy, _, ok := v.chipRect(); ok {
		col := cx
		cellwidth.Graphemes(v.chipText, func(g string, gw int) bool {
			p, comb := split(g)
			scr.SetContent(col, cy, p, comb, v.chipStyle)
			col += gw
			return true
		})
	}
	overlay := v.overlay
	v.mu.Unlock()
	if overlay != nil {
		overlay(scr, x, y, w, h)
	}
}

func (v *ChatView) drawRow(scr tcell.Screen, x, y, w int, ln cells.Line, selStart, selEnd int, hover bool) {
	drawTranscriptLine(scr, x, y, w, ln, func(col int, st tcell.Style) tcell.Style {
		if hover && v.hoverStyle != nil {
			st = v.hoverStyle(st)
		}
		if col >= selStart && col < selEnd && v.selStyle != nil {
			st = v.selStyle(st)
		}
		return st
	})
}

// split returns a grapheme as SetContent wants it. The common case (one
// rune) allocates nothing.
func split(g string) (rune, []rune) {
	r, n := utf8.DecodeRuneInString(g)
	if n == 0 {
		return ' ', nil
	}
	if n == len(g) {
		return r, nil
	}
	return r, []rune(g[n:])
}

// --- input -----------------------------------------------------------------

// MouseHandler scrolls on the wheel (SetWheelStep lines per notch). Clicks,
// drags and hover are left to the app (Box.SetMouseCapture + HitTest).
func (v *ChatView) MouseHandler() func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
	return v.WrapMouseHandler(func(action tview.MouseAction, event *tcell.EventMouse, setFocus func(p tview.Primitive)) (bool, tview.Primitive) {
		if !v.InRect(event.Position()) {
			return false, nil
		}
		v.mu.Lock()
		step := v.wheel
		v.mu.Unlock()
		if step <= 0 {
			return false, nil
		}
		switch action {
		case tview.MouseScrollUp:
			v.ScrollUp(step)
			return true, nil
		case tview.MouseScrollDown:
			v.ScrollDown(step)
			return true, nil
		}
		return false, nil
	})
}

// InputHandler handles scroll keys when the view has focus: ↑/↓ one line,
// PgUp/PgDn one page, Home/End top/bottom.
func (v *ChatView) InputHandler() func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
	return v.WrapInputHandler(func(event *tcell.EventKey, setFocus func(p tview.Primitive)) {
		switch event.Key() {
		case tcell.KeyUp:
			v.ScrollUp(1)
		case tcell.KeyDown:
			v.ScrollDown(1)
		case tcell.KeyPgUp:
			v.PageUp()
		case tcell.KeyPgDn:
			v.PageDown()
		case tcell.KeyHome:
			v.GotoTop()
		case tcell.KeyEnd:
			v.GotoBottom()
		}
	})
}
