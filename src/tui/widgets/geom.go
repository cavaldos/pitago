// Package widgets is a mouse-first widget kit for tview: context menus,
// draggable splitters, hover tooltips, clickable bars, menu buttons, and the
// Overlay that hosts popups above a base primitive and routes the mouse to
// the top-most one first.
//
// Everything here is a plain tview.Primitive, holds no application state
// beyond what it draws, and talks back through callbacks. Popups need an
// Overlay somewhere above them (tviewrt makes it the root).
package widgets

import (
	"github.com/gdamore/tcell/v2"

	"pitago/src/components/cells"
	"pitago/src/components/cellwidth"
)

// Rect is a screen rectangle.
type Rect struct{ X, Y, W, H int }

// Contains reports whether (x, y) is inside r.
func (r Rect) Contains(x, y int) bool {
	return x >= r.X && y >= r.Y && x < r.X+r.W && y < r.Y+r.H
}

// rectOf returns a primitive's rectangle.
func rectOf(p interface{ GetRect() (int, int, int, int) }) Rect {
	x, y, w, h := p.GetRect()
	return Rect{x, y, w, h}
}

// fit places a w×h box with its top-left at (x, y) inside bounds, moving
// it rather than shrinking it, unless it is bigger than bounds.
func fit(x, y, w, h int, bounds Rect) Rect {
	w = min(w, bounds.W)
	h = min(h, bounds.H)
	x = max(bounds.X, min(x, bounds.X+bounds.W-w))
	y = max(bounds.Y, min(y, bounds.Y+bounds.H-h))
	return Rect{x, y, w, h}
}

// placeAtPoint opens a w×h box at the pointer: down-right of (x, y), flipped
// left and/or up when it would cross the right/bottom edge.
func placeAtPoint(x, y, w, h int, bounds Rect) Rect {
	if x+w > bounds.X+bounds.W && x-w >= bounds.X {
		x -= w - 1
	}
	if y+h > bounds.Y+bounds.H && y-h >= bounds.Y {
		y -= h - 1
	}
	return fit(x, y, w, h, bounds)
}

// placeBelow opens a w×h box under anchor, left-aligned with it; above it
// when there is no room below; right-aligned with it when it would cross
// the right edge.
func placeBelow(anchor Rect, w, h int, bounds Rect) Rect {
	x, y := anchor.X, anchor.Y+anchor.H
	if y+h > bounds.Y+bounds.H && anchor.Y-h >= bounds.Y {
		y = anchor.Y - h
	}
	if x+w > bounds.X+bounds.W {
		x = anchor.X + anchor.W - w
	}
	return fit(x, y, w, h, bounds)
}

// placeBeside opens a w×h box to the right of anchor, top-aligned (a
// submenu); to the left when there is no room on the right.
func placeBeside(anchor Rect, w, h int, bounds Rect) Rect {
	x, y := anchor.X+anchor.W, anchor.Y
	if x+w > bounds.X+bounds.W && anchor.X-w >= bounds.X {
		x = anchor.X - w
	}
	return fit(x, y, w, h, bounds)
}

// printText writes plain text at (x, y), clipped to w columns, and returns
// the columns used. Wide graphemes that would straddle the edge are left out.
func printText(scr tcell.Screen, x, y, w int, s string, st tcell.Style) int {
	col := 0
	cellwidth.Graphemes(s, func(g string, gw int) bool {
		if gw == 0 {
			return true
		}
		if col+gw > w {
			return false
		}
		rs := []rune(g)
		scr.SetContent(x+col, y, rs[0], rs[1:], st)
		col += gw
		return true
	})
	return col
}

// fill paints r with spaces in st.
func fill(scr tcell.Screen, r Rect, st tcell.Style) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			scr.SetContent(x, y, ' ', nil, st)
		}
	}
}

// wrap word-wraps a parsed line to at most w columns. Breaks go after a
// space; a word longer than w is split. Spaces at a break are dropped.
func wrap(ln cells.Line, w int) []cells.Line {
	if w <= 0 || ln.Width() <= w {
		return []cells.Line{ln}
	}
	var out []cells.Line
	for len(ln) > 0 {
		// Skip leading spaces on continuation lines.
		if len(out) > 0 {
			for len(ln) > 0 && ln[0].Grapheme == " " {
				ln = ln[1:]
			}
			if len(ln) == 0 {
				break
			}
		}
		if ln.Width() <= w {
			out = append(out, ln)
			break
		}
		col, cut, lastSpace := 0, 0, -1
		for i, c := range ln {
			if col+c.Width > w {
				break
			}
			col += c.Width
			cut = i + 1
			if c.Grapheme == " " {
				lastSpace = i
			}
		}
		if cut == 0 { // a single cell wider than w
			cut = 1
		}
		if lastSpace > 0 && cut < len(ln) && ln[cut].Grapheme != " " {
			cut = lastSpace + 1
		}
		piece := ln[:cut]
		for len(piece) > 0 && piece[len(piece)-1].Grapheme == " " {
			piece = piece[:len(piece)-1]
		}
		out = append(out, piece)
		ln = ln[cut:]
	}
	return out
}

func isPress(a tviewAction) bool {
	return a == actLeftDown || a == actRightDown || a == actMiddleDown
}
