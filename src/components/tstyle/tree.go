package tstyle

import (
	"github.com/gdamore/tcell/v2"

	"pitago/src/components/cells"
	"pitago/src/components/cellwidth"
	"pitago/src/components/sessiontree"
)

// TokenStyle maps a sessiontree (pi theme) token onto p over base. It is the
// tcell twin of src/app's treeStyle (app/tree.go): pitago has no separate
// "dim" color, so dim is the muted color drawn faint; unknown tokens and
// StyleText take the theme's text color.
func TokenStyle(base tcell.Style, token string, p Palette) tcell.Style {
	switch token {
	case sessiontree.StyleAccent:
		return base.Foreground(p.Accent)
	case sessiontree.StyleSuccess:
		return base.Foreground(p.Green)
	case sessiontree.StyleMuted:
		return base.Foreground(p.Muted)
	case sessiontree.StyleDim:
		return base.Foreground(p.Muted).Dim(true)
	case sessiontree.StyleWarning:
		return base.Foreground(p.Yellow)
	case sessiontree.StyleError:
		return base.Foreground(p.Red)
	case sessiontree.StyleBorderAccent:
		return base.Foreground(p.Cyan)
	case sessiontree.StyleCustomLabel:
		return base.Foreground(p.Plan)
	}
	return base.Foreground(p.Text)
}

// SpanStyle is TokenStyle plus the span's own flags: Bold, Italic, and
// Selected (pi's selectedBg → the theme's highlight background), as in
// src/app's renderTreeLine.
func SpanStyle(base tcell.Style, sp sessiontree.Span, p Palette) tcell.Style {
	st := TokenStyle(base, sp.Style, p)
	if sp.Bold {
		st = st.Bold(true)
	}
	if sp.Italic {
		st = st.Italic(true)
	}
	if sp.Selected {
		st = st.Background(p.HiBg)
	}
	return st
}

// TreeLine converts a rendered session-tree row into cells ready for
// cells.Draw, one grapheme per cell, with no ANSI round trip.
func TreeLine(base tcell.Style, ln sessiontree.Line, p Palette) cells.Line {
	out := make(cells.Line, 0, sessiontree.Width(ln))
	for _, sp := range ln {
		st := SpanStyle(base, sp, p)
		cellwidth.Graphemes(sp.Text, func(g string, w int) bool {
			if w == 0 {
				// Zero-width cluster (stray combining mark) joins the
				// previous glyph, like cells.ParseLine.
				if k := len(out) - 1; k >= 0 {
					out[k].Grapheme += g
				}
				return true
			}
			out = append(out, cells.Cell{Grapheme: g, Width: w, Style: st})
			return true
		})
	}
	return out
}
