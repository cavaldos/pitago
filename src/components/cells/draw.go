package cells

import (
	"strings"
	"sync"

	"github.com/gdamore/tcell/v2"
)

// Draw paints lines[offset:] into the w×h rectangle at (x, y) of scr and
// returns the number of rows painted. Each row is clipped at w columns; a
// wide cell that would straddle the right edge is left out rather than
// split. Nothing is padded: cells past a line's end keep whatever the
// caller (e.g. tview's Box.DrawForSubclass) filled them with.
func Draw(scr tcell.Screen, x, y, w, h int, lines []Line, offset int) int {
	if w <= 0 || h <= 0 {
		return 0
	}
	if offset < 0 {
		offset = 0
	}
	rows := 0
	for r := 0; r < h && offset+r < len(lines); r++ {
		DrawLine(scr, x, y+r, w, lines[offset+r])
		rows++
	}
	return rows
}

// DrawLine paints one line at (x, y), clipped to w columns, and returns
// the columns used.
func DrawLine(scr tcell.Screen, x, y, w int, ln Line) int {
	col := 0
	for _, c := range ln {
		if col+c.Width > w {
			break
		}
		primary, combining := splitGrapheme(c.Grapheme)
		scr.SetContent(x+col, y, primary, combining, c.Style)
		col += c.Width
	}
	return col
}

// splitGrapheme returns the first rune and the rest (combining marks,
// variation selectors, ZWJ continuations) as tcell's SetContent wants them.
func splitGrapheme(g string) (rune, []rune) {
	for i, r := range g {
		if i == 0 {
			if len(g) == len(string(r)) {
				return r, nil
			}
			return r, []rune(g[len(string(r)):])
		}
	}
	return ' ', nil
}

// Painter returns a frame painter for a tview runtime: it parses frame
// through c (nil → a fresh private cache) and draws it at the top of the
// w×h rectangle. The returned func's type matches tviewrt.Painter without
// importing it, keeping components free of runtime dependencies.
//
// Kitty/iTerm2 graphics in frame are dropped from the grid; a runtime that
// shows images must write Passthrough(frame) to the tty itself.
func Painter(c *Cache) func(scr tcell.Screen, x, y, w, h int, frame string) {
	if c == nil {
		c = NewCache(0)
	}
	return func(scr tcell.Screen, x, y, w, h int, frame string) {
		Draw(scr, x, y, w, h, c.Parse(frame), 0)
	}
}

// Cache memoizes ParseLine per (incoming state, line text), so a frame
// redrawn on every message only parses the lines that changed. It keeps
// two generations of at most max entries each: when the current one fills
// it becomes the previous one, and anything not seen since is dropped.
// Lines it returns are shared between calls and must be treated as
// read-only. Safe for concurrent use.
type Cache struct {
	mu        sync.Mutex
	max       int
	cur, prev map[cacheKey]cacheVal
}

type cacheKey struct {
	in   State
	line string
}

type cacheVal struct {
	line Line
	out  State
}

// NewCache returns a cache holding up to 2×max parsed lines. max <= 0
// picks a default sized for a few full frames of a large terminal.
func NewCache(max int) *Cache {
	if max <= 0 {
		max = 4096
	}
	return &Cache{max: max, cur: map[cacheKey]cacheVal{}, prev: map[cacheKey]cacheVal{}}
}

// Parse is cells.Parse through the cache.
func (c *Cache) Parse(s string) []Line {
	return c.ParseStyle(s, tcell.StyleDefault)
}

// ParseStyle is cells.ParseStyle through the cache.
func (c *Cache) ParseStyle(s string, base tcell.Style) []Line {
	return c.ParseRows(strings.Split(s, "\n"), base)
}

// ParseRows preserves the ANSI state across already split content rows.
func (c *Cache) ParseRows(raw []string, base tcell.Style) []Line {
	out := make([]Line, len(raw))
	st := NewState(base)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, ln := range raw {
		k := cacheKey{in: st, line: ln}
		v, ok := c.cur[k]
		if !ok {
			if v, ok = c.prev[k]; !ok {
				v.line, v.out = ParseLine(ln, st)
			}
			c.put(k, v)
		}
		out[i], st = v.line, v.out
	}
	return out
}

func (c *Cache) put(k cacheKey, v cacheVal) {
	if len(c.cur) >= c.max {
		c.prev, c.cur = c.cur, make(map[cacheKey]cacheVal, c.max)
	}
	c.cur[k] = v
}

// Len reports the number of cached lines (both generations).
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.cur) + len(c.prev)
}
