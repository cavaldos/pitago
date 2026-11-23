// Package cells turns ANSI-styled terminal text into a grid of styled
// grapheme cells that can be painted straight onto a tcell.Screen.
//
// It is the bridge between pitago's string renderers (glamour/gomark,
// chroma, pimark, lipgloss-era View() frames, kitty placeholder rows) and
// tview: no tview [tag] parsing is involved anywhere, so literal "[red]" or
// "[x]" text survives byte for byte.
//
// Understood input:
//   - SGR: reset, bold, dim, italic, underline (incl. 4:n styles and 21),
//     blink, reverse, strikethrough and their resets; 16-color, 256-color
//     (38;5 / 48;5) and truecolor (38;2 / 48;2, ';' or ':' separated)
//     foreground/background; 58/59 underline color.
//   - OSC 8 hyperlinks → tcell.Style.Url / UrlId.
//   - Grapheme clusters (combining marks, ZWJ emoji, flags, VS16) via
//     uniseg, so each Cell is exactly one on-screen glyph with its width.
//   - Tabs expand to the next multiple of TabStop columns (terminal
//     semantics, since frames were written for a terminal).
//
// Everything else — cursor movement, erase, other OSC strings, DCS, APC
// (kitty graphics) — is dropped from the grid. Passthrough returns the
// graphics sequences so a runtime can still write them to the tty.
package cells

import (
	"strconv"
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/uniseg"

	"pitago/src/components/cellwidth"
)

// TabStop is the tab-stop interval used when expanding '\t'.
const TabStop = 8

// Cell is one on-screen glyph: a full grapheme cluster (base rune plus any
// combining marks), the columns it occupies (1 or 2) and its style.
type Cell struct {
	Grapheme string
	Width    int
	Style    tcell.Style
}

// Line is one row of cells.
type Line []Cell

// Width is the number of columns l occupies.
func (l Line) Width() int {
	w := 0
	for _, c := range l {
		w += c.Width
	}
	return w
}

// String is the plain text of l (styles dropped).
func (l Line) String() string {
	var b strings.Builder
	for _, c := range l {
		b.WriteString(c.Grapheme)
	}
	return b.String()
}

// Parse converts s into lines (split on '\n') starting from
// tcell.StyleDefault. SGR and hyperlink state carries across lines, the
// way a terminal would carry it.
func Parse(s string) []Line {
	return ParseStyle(s, tcell.StyleDefault)
}

// ParseStyle is Parse with a base style: SGR 0 / 39 / 49 return to base's
// attributes / foreground / background instead of the terminal default, so
// text can sit on a themed surface.
func ParseStyle(s string, base tcell.Style) []Line {
	raw := strings.Split(s, "\n")
	out := make([]Line, len(raw))
	st := NewState(base)
	for i, ln := range raw {
		out[i], st = ParseLine(ln, st)
	}
	return out
}

// State is the parser state carried from one line to the next: the SGR
// style, the open OSC 8 hyperlink (tcell.Style has no URL getter, so the
// link is tracked beside it) and the base the resets return to. It is
// comparable, so it can be part of a cache key.
type State struct {
	SGR        tcell.Style
	URL, URLID string
	Base       tcell.Style
}

// NewState is the state at the start of a frame drawn over base.
func NewState(base tcell.Style) State { return State{SGR: base, Base: base} }

// Style is the cell style for the current state.
func (s State) Style() tcell.Style {
	st := s.SGR
	if s.URL != "" {
		st = st.Url(s.URL)
		if s.URLID != "" {
			st = st.UrlId(s.URLID)
		}
	}
	return st
}

// ParseLine parses one line (s must not contain '\n') starting in state
// in, and returns the cells plus the state at the end of the line, ready
// to start the next one.
func ParseLine(s string, in State) (Line, State) {
	var line Line
	if len(s) == 0 {
		return line, in
	}
	ps := in
	st := ps.Style()
	line = make(Line, 0, len(s))
	col := 0
	for len(s) > 0 {
		if s[0] == 0x1b {
			n := cellwidth.EscapeLen(s)
			ps = applyEscape(s[:n], ps)
			st = ps.Style()
			s = s[n:]
			continue
		}
		// Text run up to the next ESC.
		end := strings.IndexByte(s, 0x1b)
		if end < 0 {
			end = len(s)
		}
		text := s[:end]
		s = s[end:]
		state := -1
		for len(text) > 0 {
			var g string
			var w int
			g, text, w, state = uniseg.FirstGraphemeClusterInString(text, state)
			switch {
			case g == "\t":
				pad := TabStop - col%TabStop
				for k := 0; k < pad; k++ {
					line = append(line, Cell{Grapheme: " ", Width: 1, Style: st})
				}
				col += pad
				continue
			case isControl(g):
				continue
			}
			if w == 0 {
				// A combining mark or joiner split from its base by an
				// escape (or a stray one at line start): fold it into the
				// previous cell so the glyph stays whole.
				if k := len(line) - 1; k >= 0 {
					line[k].Grapheme += g
				}
				continue
			}
			line = append(line, Cell{Grapheme: g, Width: w, Style: st})
			col += w
		}
	}
	return line, ps
}

// isControl reports a C0/C1 control or DEL cluster (\r, \a, \b, …), which
// has no glyph in the grid. Tabs are handled before this.
func isControl(g string) bool {
	if len(g) == 0 {
		return true
	}
	c := g[0]
	if c < 0x20 || c == 0x7f {
		return true
	}
	// C1 controls U+0080–U+009F (UTF-8 C2 80–C2 9F).
	return len(g) == 2 && c == 0xc2 && g[1] >= 0x80 && g[1] <= 0x9f
}

// applyEscape folds one escape sequence into st.
func applyEscape(seq string, st State) State {
	if len(seq) < 2 {
		return st
	}
	switch seq[1] {
	case '[':
		if seq[len(seq)-1] != 'm' {
			return st // cursor movement, erase, … : no grid effect
		}
		params := seq[2 : len(seq)-1]
		if params != "" && (params[0] < '0' || params[0] > '9') && params[0] != ';' && params[0] != ':' {
			return st // private-mode "m" (e.g. CSI > 4;1 m): not SGR
		}
		st.SGR = applySGR(params, st.SGR, st.Base)
		return st
	case ']':
		return applyOSC(seq, st)
	}
	return st
}

// oscBody strips "ESC ]" and the terminator (BEL or ESC \).
func oscBody(seq string) string {
	body := seq[2:]
	switch {
	case strings.HasSuffix(body, "\x1b\\"):
		body = body[:len(body)-2]
	case strings.HasSuffix(body, "\a"):
		body = body[:len(body)-1]
	}
	return body
}

// applyOSC handles OSC 8 hyperlinks: "8;params;URI". An empty URI ends
// the link. Other OSC commands (titles, colors, iTerm2 images) are ignored.
func applyOSC(seq string, st State) State {
	body := oscBody(seq)
	if !strings.HasPrefix(body, "8;") {
		return st
	}
	rest := body[2:]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 {
		return st
	}
	params, uri := rest[:semi], rest[semi+1:]
	st.URL, st.URLID = uri, ""
	if uri == "" {
		return st
	}
	for _, kv := range strings.Split(params, ":") {
		if id, ok := strings.CutPrefix(kv, "id="); ok && id != "" {
			st.URLID = id
		}
	}
	return st
}

// sgrParam is one ';'-separated SGR parameter with its ':' sub-params.
type sgrParam struct {
	n   int
	sub []string // raw ':' sub-parameters after n (may contain "")
}

func splitSGR(params string) []sgrParam {
	if params == "" {
		return []sgrParam{{n: 0}}
	}
	parts := strings.Split(params, ";")
	out := make([]sgrParam, 0, len(parts))
	for _, p := range parts {
		fields := strings.Split(p, ":")
		n, _ := strconv.Atoi(fields[0]) // "" → 0, per spec
		out = append(out, sgrParam{n: n, sub: fields[1:]})
	}
	return out
}

func applySGR(params string, st, base tcell.Style) tcell.Style {
	ps := splitSGR(params)
	baseFg, baseBg, _ := base.Decompose()
	for i := 0; i < len(ps); i++ {
		p := ps[i]
		switch n := p.n; {
		case n == 0:
			// The open OSC 8 link is not SGR state and stays open, as in
			// a terminal; it lives in State, not here.
			st = base
		case n == 1:
			st = st.Bold(true)
		case n == 2:
			st = st.Dim(true)
		case n == 3:
			st = st.Italic(true)
		case n == 4:
			st = st.Underline(underlineStyle(p.sub))
		case n == 5 || n == 6:
			st = st.Blink(true)
		case n == 7:
			st = st.Reverse(true)
		case n == 9:
			st = st.StrikeThrough(true)
		case n == 21:
			st = st.Underline(tcell.UnderlineStyleDouble)
		case n == 22:
			st = st.Bold(false).Dim(false)
		case n == 23:
			st = st.Italic(false)
		case n == 24:
			st = st.Underline(false)
		case n == 25:
			st = st.Blink(false)
		case n == 27:
			st = st.Reverse(false)
		case n == 29:
			st = st.StrikeThrough(false)
		case n >= 30 && n <= 37:
			st = st.Foreground(tcell.PaletteColor(n - 30))
		case n == 38 || n == 48 || n == 58:
			c, used, ok := extColor(p, ps[i+1:])
			i += used
			if !ok {
				continue
			}
			switch n {
			case 38:
				st = st.Foreground(c)
			case 48:
				st = st.Background(c)
			default:
				st = st.Underline(c)
			}
		case n == 39:
			st = st.Foreground(baseFg)
		case n >= 40 && n <= 47:
			st = st.Background(tcell.PaletteColor(n - 40))
		case n == 49:
			st = st.Background(baseBg)
		case n == 59:
			st = st.Underline(tcell.ColorDefault)
		case n >= 90 && n <= 97:
			st = st.Foreground(tcell.PaletteColor(n - 90 + 8))
		case n >= 100 && n <= 107:
			st = st.Background(tcell.PaletteColor(n - 100 + 8))
		}
	}
	return st
}

// underlineStyle maps "4" / "4:n" to a tcell underline style.
func underlineStyle(sub []string) tcell.UnderlineStyle {
	if len(sub) == 0 {
		return tcell.UnderlineStyleSolid
	}
	switch sub[0] {
	case "0":
		return tcell.UnderlineStyleNone
	case "2":
		return tcell.UnderlineStyleDouble
	case "3":
		return tcell.UnderlineStyleCurly
	case "4":
		return tcell.UnderlineStyleDotted
	case "5":
		return tcell.UnderlineStyleDashed
	}
	return tcell.UnderlineStyleSolid
}

// extColor decodes an extended color (38/48/58) from either the colon
// form in p.sub ("38:5:n", "38:2::r:g:b", "38:2:r:g:b") or the legacy
// semicolon form in the following params ("38;5;n", "38;2;r;g;b"). used is
// how many following params were consumed.
func extColor(p sgrParam, rest []sgrParam) (c tcell.Color, used int, ok bool) {
	if len(p.sub) > 0 {
		atoi := func(s string) int { v, _ := strconv.Atoi(s); return v }
		switch p.sub[0] {
		case "5":
			if len(p.sub) >= 2 {
				return palette(atoi(p.sub[1]))
			}
		case "2":
			v := p.sub[1:]
			if len(v) >= 4 { // colorspace id present (may be empty)
				v = v[1:]
			}
			if len(v) >= 3 {
				return rgb(atoi(v[0]), atoi(v[1]), atoi(v[2]))
			}
		}
		return 0, 0, false
	}
	if len(rest) == 0 {
		return 0, 0, false
	}
	switch rest[0].n {
	case 5:
		if len(rest) >= 2 {
			c, _, ok := palette(rest[1].n)
			return c, 2, ok
		}
		return 0, len(rest), false
	case 2:
		if len(rest) >= 4 {
			c, _, ok := rgb(rest[1].n, rest[2].n, rest[3].n)
			return c, 4, ok
		}
		return 0, len(rest), false
	}
	return 0, 1, false
}

func palette(n int) (tcell.Color, int, bool) {
	if n < 0 || n > 255 {
		return 0, 0, false
	}
	return tcell.PaletteColor(n), 0, true
}

func rgb(r, g, b int) (tcell.Color, int, bool) {
	if r < 0 || r > 255 || g < 0 || g > 255 || b < 0 || b > 255 {
		return 0, 0, false
	}
	return tcell.NewRGBColor(int32(r), int32(g), int32(b)), 0, true
}

// Passthrough returns, in order, the escape sequences in s that draw
// pixels outside the cell grid and therefore cannot go through tcell:
// kitty graphics APC strings (ESC _ G … ST: uploads and U=1 placements)
// and iTerm2 inline images (OSC 1337 File=…). A tview runtime writes these
// to the tty itself (after Screen.Show) — Parse drops them from the grid.
func Passthrough(s string) []string {
	var out []string
	for {
		i := strings.IndexByte(s, 0x1b)
		if i < 0 {
			return out
		}
		s = s[i:]
		n := cellwidth.EscapeLen(s)
		seq := s[:n]
		if strings.HasPrefix(seq, "\x1b_G") || strings.HasPrefix(seq, "\x1b]1337;File=") {
			out = append(out, seq)
		}
		s = s[n:]
	}
}
