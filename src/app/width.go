package app

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// displayWidth reports how many terminal cells s occupies.
//
// It is a drop-in for ansi.StringWidth — the same number, every time — but it
// skips the grapheme-segmentation machinery for the ASCII spans that make up
// nearly every byte of a terminal line. Frame assembly measured 46% of a
// frame inside ansi.StringWidth, and 70% of that was the join path walking
// every line of every block, so this is where the cost actually lives.
//
// ansi.stringWidth is a byte state machine: a printable byte costs one cell,
// an escape sequence costs nothing, and the first non-ASCII byte starts a
// grapheme cluster measured as a whole (0, 1 or 2 cells). Two rules make the
// fast path exact rather than approximate:
//
//   - Printable ASCII is always single-cell, so ASCII runs are counted by
//     bytes.
//   - No ASCII byte can be absorbed into a grapheme cluster that began with a
//     non-ASCII byte. The characters UAX #29 combines with a base — combining
//     marks, ZWJ, variation selectors, regional indicators — are all
//     non-ASCII, so a cluster boundary never falls inside an ASCII run. Width
//     is therefore additive across a split, and a non-ASCII run can be
//     measured on its own.
//
// Anything the escape grammar does not cover is handed to ansi.StringWidth
// rather than guessed, so an incomplete grammar costs speed, never accuracy.
func displayWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == 0x1b: // escape sequence: zero cells
			n := escapeLen(s[i:])
			if n == 0 {
				return ansi.StringWidth(s) // unclassified: measure the lot
			}
			i += n
		case c >= 0x80: // a maximal non-ASCII run, measured as a whole
			j := i
			for j < len(s) && s[j] >= 0x80 {
				j++
			}
			w += ansi.StringWidth(s[i:j])
			i = j
		case c >= 0x20 && c != 0x7f: // printable ASCII: exactly one cell
			w++
			i++
		default: // C0 control: the state machine prints nothing for these
			i++
		}
	}
	return w
}

// escapeLen returns the byte length of the escape sequence at the start of s,
// or 0 when it does not match a form recognised here. Returning 0 sends the
// caller to ansi.StringWidth, which is why an omission here is safe.
func escapeLen(s string) int {
	if len(s) < 2 {
		return 0
	}
	switch s[1] {
	case '[': // CSI: parameter bytes, intermediate bytes, one final byte
		i := 2
		for i < len(s) && s[i] >= 0x30 && s[i] <= 0x3f {
			i++
		}
		for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
			i++
		}
		if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
			return i + 1
		}
		return 0
	case ']', 'P', 'X', '^', '_': // string sequences, up to BEL or ST
		for i := 2; i < len(s); i++ {
			if s[i] == 0x07 {
				return i + 1
			}
			if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
				return i + 2
			}
		}
		return 0
	case '(', ')', '*', '+', '-', '.', '/': // designation, one more byte
		if len(s) >= 3 {
			return 3
		}
		return 0
	default:
		// ECMA-48 two-byte escape (RIS, IND, NEL, DECSC…). A byte outside
		// the two ranges the grammar allows is unrecognised.
		if s[1] >= 0x20 && s[1] <= 0x2f || s[1] >= 0x30 && s[1] <= 0x7e {
			return 2
		}
		return 0
	}
}

// joinVerticalLeft joins blocks top to bottom, every line padded on the right
// to the widest block, exactly as lipgloss.JoinVertical(lipgloss.Left, ...)
// does. The only differences are mechanical: each line is measured once
// instead of once in getLines and again for the pad, and the pad is skipped
// when it is empty.
func joinVerticalLeft(blocks ...string) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return blocks[0]
	}
	split := make([][]string, len(blocks))
	widths := make([][]int, len(blocks))
	maxWidth := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		widths[i] = measureLines(split[i])
		for _, w := range widths[i] {
			if w > maxWidth {
				maxWidth = w
			}
		}
	}
	var sb strings.Builder
	for i, block := range split {
		for j, line := range block {
			sb.WriteString(line)
			if pad := maxWidth - widths[i][j]; pad > 0 {
				sb.WriteString(spaces(pad))
			}
			if i != len(split)-1 || j != len(block)-1 {
				sb.WriteByte('\n')
			}
		}
	}
	return sb.String()
}

// joinHorizontalTop joins blocks left to right, aligning them on the top edge
// and padding each column to its own widest line, exactly as
// lipgloss.JoinHorizontal(lipgloss.Top, ...) does. Shorter blocks are grown
// with empty lines at the bottom first, matching lipgloss's Top branch.
func joinHorizontalTop(blocks ...string) string {
	if len(blocks) == 0 {
		return ""
	}
	if len(blocks) == 1 {
		return blocks[0]
	}
	split := make([][]string, len(blocks))
	widths := make([][]int, len(blocks))
	maxHeight := 0
	for i, b := range blocks {
		split[i] = strings.Split(b, "\n")
		widths[i] = measureLines(split[i])
		if len(split[i]) > maxHeight {
			maxHeight = len(split[i])
		}
	}
	var sb strings.Builder
	for row := 0; row < maxHeight; row++ {
		for i, block := range split {
			if row < len(block) {
				sb.WriteString(block[row])
				if pad := maxOf(widths[i]) - widths[i][row]; pad > 0 {
					sb.WriteString(spaces(pad))
				}
				continue
			}
			// Past this block's last line: emit its trailing pad only.
			if pad := maxOf(widths[i]); pad > 0 {
				sb.WriteString(spaces(pad))
			}
		}
		if row < maxHeight-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func measureLines(lines []string) []int {
	w := make([]int, len(lines))
	for i, l := range lines {
		w[i] = displayWidth(l)
	}
	return w
}

func maxOf(xs []int) int {
	m := 0
	for _, x := range xs {
		if x > m {
			m = x
		}
	}
	return m
}

// spacePad covers the padding runs a frame actually needs in one shot, so the
// common case is a slice of a shared string rather than an allocation.
const spacePad = "                                                                "

// spaces returns n spaces, falling back to a fresh string past the pad.
func spaces(n int) string {
	if n <= len(spacePad) {
		return spacePad[:n]
	}
	return strings.Repeat(" ", n)
}
