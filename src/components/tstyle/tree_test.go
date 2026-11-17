package tstyle

import (
	"testing"

	"github.com/gdamore/tcell/v2"

	"pitago/src/components/sessiontree"
	"pitago/src/components/theme"
)

func TestTokenStyle(t *testing.T) {
	th := theme.Get("one-dark")
	p := FromTheme(th)
	base := tcell.StyleDefault
	cases := []struct {
		token string
		want  tcell.Style
	}{
		{sessiontree.StyleText, base.Foreground(Color(th.Text))},
		{sessiontree.StyleAccent, base.Foreground(Color(th.Accent))},
		{sessiontree.StyleSuccess, base.Foreground(Color(th.Green))},
		{sessiontree.StyleMuted, base.Foreground(Color(th.Muted))},
		{sessiontree.StyleDim, base.Foreground(Color(th.Muted)).Dim(true)},
		{sessiontree.StyleWarning, base.Foreground(Color(th.Yellow))},
		{sessiontree.StyleError, base.Foreground(Color(th.Red))},
		{sessiontree.StyleBorderAccent, base.Foreground(Color(th.Cyan))},
		{sessiontree.StyleCustomLabel, base.Foreground(Color(th.Plan))},
		{"unknown-token", base.Foreground(Color(th.Text))},
	}
	for _, c := range cases {
		if got := TokenStyle(base, c.token, p); got != c.want {
			t.Errorf("TokenStyle(%q) = %+v, want %+v", c.token, got, c.want)
		}
	}
	// Base background survives (themed surface).
	bg := tcell.StyleDefault.Background(tcell.NewHexColor(0x282c34))
	if _, b, _ := TokenStyle(bg, sessiontree.StyleAccent, p).Decompose(); b != tcell.NewHexColor(0x282c34) {
		t.Fatalf("base bg lost: %v", b)
	}
}

func TestSpanStyle(t *testing.T) {
	p := FromTheme(theme.Get("dracula"))
	sp := sessiontree.Span{Text: "x", Style: sessiontree.StyleAccent, Bold: true, Italic: true, Selected: true}
	want := tcell.StyleDefault.Foreground(p.Accent).Bold(true).Italic(true).Background(p.HiBg)
	if got := SpanStyle(tcell.StyleDefault, sp, p); got != want {
		t.Fatalf("SpanStyle = %+v, want %+v", got, want)
	}
}

func TestTreeLine(t *testing.T) {
	p := FromTheme(theme.Get("nord"))
	ln := sessiontree.Line{
		{Text: "├─ ", Style: sessiontree.StyleDim},
		{Text: "user: 日本 [x] 👩‍💻", Style: sessiontree.StyleAccent, Selected: true},
	}
	got := TreeLine(tcell.StyleDefault, ln, p)
	if got.String() != "├─ user: 日本 [x] 👩‍💻" {
		t.Fatalf("text = %q", got.String())
	}
	if got.Width() != sessiontree.Width(ln) {
		t.Fatalf("width %d != sessiontree.Width %d", got.Width(), sessiontree.Width(ln))
	}
	if got[0].Style != SpanStyle(tcell.StyleDefault, ln[0], p) ||
		got[len(got)-1].Style != SpanStyle(tcell.StyleDefault, ln[1], p) {
		t.Fatalf("styles: %+v … %+v", got[0].Style, got[len(got)-1].Style)
	}
	if last := got[len(got)-1]; last.Width != 2 || last.Grapheme != "👩‍💻" {
		t.Fatalf("ZWJ cell = %+v", last)
	}
}
