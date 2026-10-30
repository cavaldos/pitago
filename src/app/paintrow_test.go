package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
)

// Regression: a selected row whose content carries its own styled span
// (the ● status dot) lost its background right after that span, so only
// the dot looked selected. paintRow must re-open the row style after
// every inner reset.
func TestPaintRowKeepsBackgroundAfterInnerSpans(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })

	row := lipgloss.NewStyle().Background(lipgloss.Color("#3C3836")).Foreground(lipgloss.Color("#EBDBB2"))
	dot := lipgloss.NewStyle().Foreground(lipgloss.Color("#B8BB26")).Render("● ")
	out := paintRow(row, dot+"OpenAI      2 keys", 30)

	pre := sgrPrefix(row)
	if pre == "" || !strings.Contains(pre, "48;2;") {
		t.Fatalf("no background in row prefix: %q", pre)
	}
	i := strings.Index(out, "OpenAI")
	if i < 0 {
		t.Fatalf("row text missing: %q", out)
	}
	before := out[:i]
	last := strings.LastIndex(before, "\x1b[0m")
	if last < 0 || !strings.Contains(before[last:], "48;2;") {
		t.Fatalf("background not re-opened after the dot: %q", out)
	}
	if w := lipgloss.Width(out); w != 30 {
		t.Fatalf("width = %d, want 30", w)
	}
}
