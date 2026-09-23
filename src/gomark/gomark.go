// Package gomark renders markdown tables to bordered boxes with stdlib +
// lipgloss only (no node/pi). Trial version: tables get pi-like borders,
// headings/bold/inline-code get minimal styling, everything else passes
// through. Used as fallback when pimark bridge fails, or forced via
// PITAGO_RENDER=go.
package gomark

import (
	"regexp"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	boldRe = regexp.MustCompile(`\*\*(.+?)\*\*`)
	codeRe = regexp.MustCompile("`([^`]+?)`")
	delimRe = regexp.MustCompile(`^:?-{1,}:?$`)
)

var (
	boldStyle = lipgloss.NewStyle().Bold(true)
	codeStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#9CDCFE"))
	headStyle = lipgloss.NewStyle().Bold(true)
)

func stripInline(s string) string {
	s = boldRe.ReplaceAllString(s, "$1")
	s = codeRe.ReplaceAllString(s, "$1")
	return s
}

func inline(s string) string {
	s = boldRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := boldRe.FindStringSubmatch(m)[1]
		return boldStyle.Render(inner)
	})
	s = codeRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := codeRe.FindStringSubmatch(m)[1]
		return codeStyle.Render(inner)
	})
	return s
}

func splitRow(ln string) []string {
	t := strings.TrimSpace(ln)
	t = strings.TrimPrefix(t, "|")
	t = strings.TrimSuffix(t, "|")
	parts := strings.Split(t, "|")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

func isDelimRow(ln string) bool {
	cells := splitRow(ln)
	if len(cells) == 0 {
		return false
	}
	for _, c := range cells {
		if !delimRe.MatchString(strings.TrimSpace(c)) {
			return false
		}
	}
	return true
}

func alignOf(cell string) string {
	c := strings.TrimSpace(cell)
	l := strings.HasPrefix(c, ":")
	r := strings.HasSuffix(c, ":")
	switch {
	case l && r:
		return "center"
	case r:
		return "right"
	default:
		return "left"
	}
}

func padCell(s string, w int, align string) string {
	d := w - lipgloss.Width(stripInline(s))
	if d < 0 {
		d = 0
	}
	styled := inline(s)
	switch align {
	case "right":
		return strings.Repeat(" ", d) + styled
	case "center":
		l := d / 2
		return strings.Repeat(" ", l) + styled + strings.Repeat(" ", d-l)
	default:
		return styled + strings.Repeat(" ", d)
	}
}

// renderTable builds one bordered table. Header bold, body plain.
// ponytail: no wrap/truncate — table wider than width renders full, wrap if it matters.
func renderTable(header []string, aligns []string, rows [][]string) string {
	n := len(header)
	widths := make([]int, n)
	for i, h := range header {
		widths[i] = lipgloss.Width(stripInline(h))
	}
	for _, r := range rows {
		for i := 0; i < n && i < len(r); i++ {
			if w := lipgloss.Width(stripInline(r[i])); w > widths[i] {
				widths[i] = w
			}
		}
	}
	bar := func(l, m, r string) string {
		var b strings.Builder
		b.WriteString(l)
		for i, w := range widths {
			if i > 0 {
				b.WriteString(m)
			}
			b.WriteString(strings.Repeat("─", w+2))
		}
		b.WriteString(r)
		return b.String()
	}
	row := func(cells []string, bold bool) string {
		var b strings.Builder
		b.WriteString("│")
		for i := 0; i < n; i++ {
			cell := ""
			if i < len(cells) {
				cell = cells[i]
			}
			p := padCell(cell, widths[i], aligns[i])
			if bold {
				// header: pad on plain then bold whole padded string
				plain := cell + strings.Repeat(" ", widths[i]-lipgloss.Width(stripInline(cell)))
				// re-pad with alignment
				switch aligns[i] {
				case "right":
					plain = strings.Repeat(" ", widths[i]-lipgloss.Width(stripInline(cell))) + cell
				case "center":
					d := widths[i] - lipgloss.Width(stripInline(cell))
					l := d / 2
					plain = strings.Repeat(" ", l) + cell + strings.Repeat(" ", d-l)
				}
				p = headStyle.Render(stripInline(plain))
				// inline styles inside header already covered by plain bold
			}
			b.WriteString(" " + p + " │")
		}
		return b.String()
	}
	var out []string
	out = append(out, bar("┌", "┬", "┐"))
	out = append(out, row(header, true))
	out = append(out, bar("├", "┼", "┤"))
	for _, r := range rows {
		out = append(out, row(r, false))
	}
	out = append(out, bar("└", "┴", "┘"))
	return strings.Join(out, "\n")
}

// Render converts markdown to ANSI. Tables become boxes, "# " becomes bold,
// other lines keep list markers with inline bold/code styling.
func Render(src string, width int) string {
	lines := strings.Split(src, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		// table block: |...| lines, 2nd must be delim row
		if strings.HasPrefix(strings.TrimSpace(lines[i]), "|") &&
			i+1 < len(lines) && isDelimRow(lines[i+1]) {
			j := i + 2
			for j < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[j]), "|") {
				j++
			}
			header := splitRow(lines[i])
			delims := splitRow(lines[i+1])
			n := len(header)
			aligns := make([]string, n)
			for k := 0; k < n; k++ {
				if k < len(delims) {
					aligns[k] = alignOf(delims[k])
				} else {
					aligns[k] = "left"
				}
			}
			var rows [][]string
			for k := i + 2; k < j; k++ {
				r := splitRow(lines[k])
				for len(r) < n {
					r = append(r, "")
				}
				rows = append(rows, r)
			}
			out = append(out, renderTable(header, aligns, rows))
			i = j
			continue
		}
		t := lines[i]
		trim := strings.TrimSpace(t)
		if strings.HasPrefix(trim, "#") {
			trim = strings.TrimLeft(trim, "# ")
			out = append(out, headStyle.Render(stripInline(trim)))
		} else {
			out = append(out, inline(t))
		}
		i++
	}
	return strings.Join(out, "\n")
}
