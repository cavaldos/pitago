package chatview

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/components/cells"
	"pitago/src/tui/ev"
)

// Window is the native transcript for an app that already owns scrolling,
// wrapping and absolute hit tests. It paints only the given visible window.
type Window struct {
	*tview.Box
	View       ev.TranscriptView
	Base, Jump tcell.Style
	Graphics   func(string)
	cache      *cells.Cache
}

func NewWindow(cache *cells.Cache, graphics func(string)) *Window {
	return &Window{Box: tview.NewBox(), cache: cache, Graphics: graphics}
}

func (v *Window) Draw(scr tcell.Screen) {
	v.Box.DrawForSubclass(scr, v)
	x, y, w, h := v.GetInnerRect()
	view := v.View
	rows := view.Rows[:min(len(view.Rows), max(0, h))]
	for r, line := range v.cache.ParseRows(rows, v.Base) {
		if v.Graphics != nil && (strings.Contains(rows[r], "\x1b_G") || strings.Contains(rows[r], "\x1b]1337;")) {
			for _, payload := range cells.Passthrough(rows[r]) {
				v.Graphics(payload)
			}
		}
		abs := r + view.Offset
		start, end := selectedColumns(view.Selection, abs, r, line)
		hover := view.Selection == nil && abs >= view.HoverStart && abs < view.HoverEnd && strings.TrimSpace(rows[r]) != ""
		drawTranscriptLine(scr, x, y+r, w, line, func(col int, st tcell.Style) tcell.Style {
			if col >= start && col < end {
				return st.Reverse(true)
			}
			if hover {
				return st.Dim(false).Bold(true)
			}
			return st
		})
	}
	if view.JumpLabel != "" && view.JumpY < h && view.JumpX < w {
		chip, _ := cells.ParseLine(view.JumpLabel, cells.NewState(v.Jump))
		cells.DrawLine(scr, x+view.JumpX, y+view.JumpY, w-view.JumpX, chip)
	}
}

func selectedColumns(sel *ev.TextSelection, abs, row int, line cells.Line) (start, end int) {
	if sel == nil || abs < sel.Start.Line || abs > sel.End.Line {
		return 0, 0
	}
	end = line.Width()
	if abs == sel.Start.Line {
		start = sel.Start.Column
	}
	if abs == sel.End.Line {
		end = sel.End.Column
	}
	if row < len(sel.Gutters) {
		start = max(start, sel.Gutters[row])
	}
	return start, end
}

func drawTranscriptLine(scr tcell.Screen, x, y, w int, line cells.Line, style func(int, tcell.Style) tcell.Style) {
	col := 0
	for _, c := range line {
		if col+c.Width > w {
			break
		}
		p, comb := split(c.Grapheme)
		scr.SetContent(x+col, y, p, comb, style(col, c.Style))
		col += c.Width
	}
}
