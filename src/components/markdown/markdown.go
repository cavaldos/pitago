// Package markdown renders chat output in-process with Go (Glamour v2 +
// Chroma, dark theme). No node/pi needed, so tables render on every machine.
// PITAGO_RENDER=pi opts back into pi's own renderer for comparison.
package markdown

import (
	"os"
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"pitago/src/gomark"
	"pitago/src/pimark"
)

// ansiSeq matches one SGR escape; trailPad matches end-of-line padding:
// spaces/tabs padding pi adds to fill the wrap width.
var (
	ansiSeq  = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")
	trailPad = regexp.MustCompile(`(?:[ \t]|\x1b\[[0-9;]*[a-zA-Z])+$`)
)

// rtrimLine drops pi's wrap-width padding so mouse selection stays clean.
// Lines with nothing visible become "" (gutter keeps those empty).
func rtrimLine(ln string) string {
	if strings.TrimSpace(ansiSeq.ReplaceAllString(ln, "")) == "" {
		return ""
	}
	if !trailPad.MatchString(ln) {
		return ln
	}
	return trailPad.ReplaceAllString(ln, "\x1b[0m")
}

// rtrim applies rtrimLine per line.
func rtrim(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		lines[i] = rtrimLine(ln)
	}
	return strings.Join(lines, "\n")
}

// isMarkdown is a fast path so plain replies skip the bridge round-trip
// (and stream the same as before). Assistant text from the model is
// markdown, so this stays loose on purpose.
func isMarkdown(s string) bool {
	if strings.Contains(s, "```") {
		return true
	}
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "- ") ||
			strings.HasPrefix(t, "* ") || strings.HasPrefix(t, "+ ") ||
			strings.HasPrefix(t, "> ") || strings.HasPrefix(t, "|") ||
			strings.HasPrefix(t, "```") {
			return true
		}
	}
	return strings.ContainsAny(s, "`*_[") || strings.Contains(s, "**")
}

// framedChars only appear in an already-framed table (pi output, pasted
// box tables). Glamour only emits │ ─ ┼, never these.
const framedChars = "┌┐└┘┬┴├┤"

// isTableSep reports whether ln is a table separator row: only ─/┼ once
// ANSI escapes and padding are removed.
func isTableSep(ln string) bool {
	t := strings.TrimSpace(ansiSeq.ReplaceAllString(ln, ""))
	if t == "" {
		return false
	}
	for _, r := range t {
		if r != '─' && r != '┼' {
			return false
		}
	}
	return true
}

func isTableRow(ln string) bool {
	return strings.Contains(ln, "│") || isTableSep(ln)
}

// frameGroup draws a tight outer frame (┌┐└┘├┤) around one glamour-rendered
// table group so it looks like the old pi-style boxed table. Groups that
// already carry a frame, lack a separator row, or would overflow width are
// returned untouched.
// ponytail: no re-wrap — skip when w+2 > width instead of shrinking cells.
func frameGroup(group []string, width int) []string {
	if len(group) < 2 {
		return group
	}
	hasSep, framed := false, false
	for _, ln := range group {
		if isTableSep(ln) {
			hasSep = true
		}
		if strings.ContainsAny(ln, framedChars) {
			framed = true
		}
	}
	if !hasSep || framed {
		return group
	}
	// Strip the uniform left margin (glamour: 2) so the frame hugs content.
	minLead := -1
	for _, ln := range group {
		n := 0
		for n < len(ln) && ln[n] == ' ' {
			n++
		}
		if minLead < 0 || n < minLead {
			minLead = n
		}
	}
	if minLead > 2 {
		minLead = 2 // deeper (list/quote) indent stays outside the frame
	}
	rows := make([]string, 0, len(group))
	w := 0
	for _, ln := range group {
		t := rtrimLine(ln[minLead:])
		rows = append(rows, t)
		if ww := lipgloss.Width(t); ww > w {
			w = ww
		}
	}
	if w+2 > width {
		return group
	}
	padded := make([]string, 0, len(rows))
	for _, r := range rows {
		padded = append(padded, r+strings.Repeat(" ", w-lipgloss.Width(r)))
	}
	// Tick positions from the first separator row so ┬/┴ sit above/below ┼.
	var ticks []int
	for _, r := range padded {
		if !isTableSep(r) {
			continue
		}
		for i, ch := range []rune(ansiSeq.ReplaceAllString(r, "")) {
			if ch == '┼' {
				ticks = append(ticks, i)
			}
		}
		break
	}
	mkBorder := func(left string, tick, right rune) string {
		b := []rune(left + strings.Repeat("─", w) + string(right))
		for _, t := range ticks {
			if t+1 < len(b) {
				b[t+1] = tick
			}
		}
		return string(b)
	}
	out := []string{mkBorder("┌", '┬', '┐')}
	for _, r := range padded {
		if isTableSep(r) {
			out = append(out, "├"+r+"┤")
		} else {
			out = append(out, "│"+r+"│")
		}
	}
	return append(out, mkBorder("└", '┴', '┘'))
}

// frameTables draws old-style outer frames around glamour-rendered tables
// (inner │/─/┼ only). Everything else passes through unchanged.
func frameTables(s string, width int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	for i := 0; i < len(lines); {
		if !isTableRow(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		j := i
		for j < len(lines) && isTableRow(lines[j]) {
			j++
		}
		out = append(out, frameGroup(lines[i:j], width)...)
		i = j
	}
	return strings.Join(out, "\n")
}

// Render turns markdown into ANSI for the chat column, wrapped to width.
// Default is Go (Glamour); PITAGO_RENDER=pi opts into pi's bridge.
func Render(src string, width int) string {
	if strings.TrimSpace(src) == "" || !isMarkdown(src) {
		return src
	}
	if width < 20 {
		width = 80
	}
	if os.Getenv("PITAGO_RENDER") == "pi" {
		out, err := pimark.Render(src, width, pimark.Assistant)
		if err != nil || strings.TrimSpace(out) == "" {
			return strings.Trim(rtrim(frameTables(gomark.Render(src, width), width)), "\n")
		}
		return strings.Trim(rtrim(frameTables(out, width)), "\n")
	}
	return strings.Trim(rtrim(frameTables(gomark.Render(src, width), width)), "\n")
}

// Highlight colors code with Chroma (no auto-detect, like pi's rule).
// PITAGO_RENDER=pi opts into pi's highlightCode.
func Highlight(lang, code string) string {
	if code == "" {
		return code
	}
	if os.Getenv("PITAGO_RENDER") == "pi" {
		out, err := pimark.Highlight(code, lang)
		if err != nil || out == "" {
			if goOut := gomark.Highlight(code, lang); goOut != "" {
				return goOut
			}
			return code
		}
		return rtrim(out)
	}
	if out := gomark.Highlight(code, lang); out != "" {
		return out
	}
	return code
}
