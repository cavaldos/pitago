// Package cellwidth measures and cuts terminal text in display cells.
//
// It is the single width authority for pitago's render layer: grapheme
// clusters are segmented and measured with rivo/uniseg, the same library
// tcell/tview use to place cells, so a width computed here matches the
// columns a string occupies on screen. ANSI escape sequences (CSI, OSC,
// DCS/APC/PM/SOS strings and two-byte ESC sequences) are zero-width and are
// carried through Truncate untouched. Stdlib + uniseg only.
package cellwidth

import (
	"strings"

	"github.com/rivo/uniseg"
)

const esc = 0x1b

// EscapeLen reports the byte length of the escape sequence at the start of
// s, or 0 when s does not start with ESC. An unterminated sequence runs to
// the end of s, so a cut-off escape is never measured as visible text.
func EscapeLen(s string) int {
	if len(s) == 0 || s[0] != esc {
		return 0
	}
	if len(s) == 1 {
		return 1
	}
	switch s[1] {
	case '[': // CSI: parameter/intermediate bytes, then one final byte 0x40–0x7E
		for i := 2; i < len(s); i++ {
			if c := s[i]; c >= 0x40 && c <= 0x7e {
				return i + 1
			}
		}
		return len(s)
	case ']', 'P', '_', '^', 'X': // OSC / DCS / APC / PM / SOS: ends at ST (ESC \) or BEL
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == esc && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return len(s)
	}
	// nF / Fp / Fe / Fs: optional intermediates 0x20–0x2F, then a final byte.
	i := 1
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) {
		return i + 1
	}
	return len(s)
}

// Strip removes every escape sequence from s.
func Strip(s string) string {
	if strings.IndexByte(s, esc) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for len(s) > 0 {
		if n := EscapeLen(s); n > 0 {
			s = s[n:]
			continue
		}
		i := strings.IndexByte(s, esc)
		if i < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		s = s[i:]
	}
	return b.String()
}

// StringWidth is the display width of s in cells, escapes ignored. Control
// characters (including newlines) are zero-width, so for multi-line text
// this is the sum of all lines; see Width for the widest line.
func StringWidth(s string) int {
	return uniseg.StringWidth(Strip(s))
}

// Width is the width of the widest line of s, escapes ignored. It is a
// drop-in for lipgloss.Width.
func Width(s string) int {
	w := 0
	for _, ln := range strings.Split(s, "\n") {
		if lw := StringWidth(ln); lw > w {
			w = lw
		}
	}
	return w
}

// GraphemeWidth is the width of a single grapheme cluster.
func GraphemeWidth(g string) int {
	return uniseg.StringWidth(g)
}

// Graphemes calls fn for each grapheme cluster of s (escape-free text) with
// its display width; fn returns false to stop.
func Graphemes(s string, fn func(g string, w int) bool) {
	state := -1
	var g string
	var w int
	for len(s) > 0 {
		g, s, w, state = uniseg.FirstGraphemeClusterInString(s, state)
		if !fn(g, w) {
			return
		}
	}
}

// Truncate cuts s to at most n display cells. When s is wider than n, the
// result is the longest grapheme-aligned prefix that leaves room for tail,
// followed by tail; tail counts toward n. A wide grapheme that would
// straddle the limit is dropped, never split. Escapes before the cut are
// kept, and so are escapes after it (with no text), so a closing SGR reset
// or OSC 8 terminator still lands. Strings that already fit are returned
// unchanged with no tail.
func Truncate(s string, n int, tail string) string {
	if n <= 0 {
		return ""
	}
	if StringWidth(s) <= n {
		return s
	}
	limit := n - StringWidth(tail)
	if limit < 0 {
		// Not even the tail fits: cut the tail itself.
		return Truncate(tail, n, "")
	}
	var b strings.Builder
	cells := 0
	cut := false
	for len(s) > 0 {
		if k := EscapeLen(s); k > 0 {
			b.WriteString(s[:k])
			s = s[k:]
			continue
		}
		i := strings.IndexByte(s, esc)
		text := s
		if i >= 0 {
			text = s[:i]
		}
		s = s[len(text):]
		if cut {
			continue
		}
		Graphemes(text, func(g string, w int) bool {
			if cells+w > limit {
				cut = true
				return false
			}
			cells += w
			b.WriteString(g)
			return true
		})
		if cut {
			b.WriteString(tail)
		}
	}
	// s was wider than n and limit <= n, so the loop always cut.
	return b.String()
}
