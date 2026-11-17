package tviewrt

import (
	"strings"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// PaintTranslate is the interim painter: each frame line goes through
// tview.TranslateANSI and tview.Print. Literal text is tag-escaped first so
// "[x]" in chat/code is not read as a style tag. OSC sequences (OSC 8
// links, kitty image commands) are dropped. It is replaced by the
// cell-level painter from src/components/cells.
func PaintTranslate(scr tcell.Screen, x, y, w, h int, frame string) {
	for i, line := range strings.Split(frame, "\n") {
		if i >= h {
			break
		}
		tview.Print(scr, tview.TranslateANSI(escapeText(line)), x, y+i, w, tview.AlignLeft, tcell.ColorDefault)
	}
}

// escapeText applies tview.Escape to the runs of plain text between
// escape sequences, leaving the sequences themselves intact.
func escapeText(s string) string {
	if !strings.Contains(s, "[") {
		return s
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] != 0x1b {
			j := strings.IndexByte(s[i:], 0x1b)
			if j < 0 {
				j = len(s) - i
			}
			b.WriteString(tview.Escape(s[i : i+j]))
			i += j
			continue
		}
		j := seqEnd(s, i)
		b.WriteString(s[i:j])
		i = j
	}
	return b.String()
}

// seqEnd returns the index just past the escape sequence starting at i.
func seqEnd(s string, i int) int {
	if i+1 >= len(s) {
		return len(s)
	}
	switch s[i+1] {
	case '[': // CSI: parameters/intermediates then a final byte 0x40–0x7e
		for j := i + 2; j < len(s); j++ {
			if s[j] >= 0x40 && s[j] <= 0x7e {
				return j + 1
			}
		}
	case ']', 'P', '_', '^', 'X': // string sequences end at BEL or ST
		for j := i + 2; j < len(s); j++ {
			if s[j] == 0x07 {
				return j + 1
			}
			if s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\' {
				return j + 2
			}
		}
	default:
		return i + 2
	}
	return len(s)
}
