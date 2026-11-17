package cells

import (
	"fmt"
	"strings"
	"testing"

	"github.com/gdamore/tcell/v2"

	"pitago/src/components/cellwidth"
	"pitago/src/components/terminal_image"
	"pitago/src/gomark"
)

var def = tcell.StyleDefault

func one(t *testing.T, s string) Line {
	t.Helper()
	ls := Parse(s)
	if len(ls) != 1 {
		t.Fatalf("Parse(%q) gave %d lines", s, len(ls))
	}
	return ls[0]
}

func TestPlainAndLiteralBrackets(t *testing.T) {
	in := "[red]not a tag[-] [x] [::b] [#ff0000]"
	ln := one(t, in)
	if ln.String() != in {
		t.Fatalf("text = %q", ln.String())
	}
	for _, c := range ln {
		if c.Style != def || c.Width != 1 {
			t.Fatalf("cell %+v styled", c)
		}
	}
}

func TestSGRBasics(t *testing.T) {
	cases := []struct {
		in   string
		want tcell.Style
	}{
		{"\x1b[1mX", def.Bold(true)},
		{"\x1b[2mX", def.Dim(true)},
		{"\x1b[3mX", def.Italic(true)},
		{"\x1b[4mX", def.Underline(true)},
		{"\x1b[4:3mX", def.Underline(tcell.UnderlineStyleCurly)},
		{"\x1b[21mX", def.Underline(tcell.UnderlineStyleDouble)},
		{"\x1b[5mX", def.Blink(true)},
		{"\x1b[7mX", def.Reverse(true)},
		{"\x1b[9mX", def.StrikeThrough(true)},
		{"\x1b[1;2m\x1b[22mX", def},
		{"\x1b[3;4;7;9m\x1b[23;24;27;29mX", def},
		{"\x1b[31mX", def.Foreground(tcell.PaletteColor(1))},
		{"\x1b[97mX", def.Foreground(tcell.PaletteColor(15))},
		{"\x1b[44mX", def.Background(tcell.PaletteColor(4))},
		{"\x1b[103mX", def.Background(tcell.PaletteColor(11))},
		{"\x1b[38;5;203mX", def.Foreground(tcell.PaletteColor(203))},
		{"\x1b[48;5;238mX", def.Background(tcell.PaletteColor(238))},
		{"\x1b[38:5:203mX", def.Foreground(tcell.PaletteColor(203))},
		{"\x1b[38;2;97;175;239mX", def.Foreground(tcell.NewRGBColor(97, 175, 239))},
		{"\x1b[38:2::97:175:239mX", def.Foreground(tcell.NewRGBColor(97, 175, 239))},
		{"\x1b[38:2:97:175:239mX", def.Foreground(tcell.NewRGBColor(97, 175, 239))},
		{"\x1b[48;2;1;2;3;1mX", def.Background(tcell.NewRGBColor(1, 2, 3)).Bold(true)},
		{"\x1b[1;38;5;10;48;5;20mX", def.Bold(true).Foreground(tcell.PaletteColor(10)).Background(tcell.PaletteColor(20))},
		{"\x1b[58;5;9mX", def.Underline(tcell.PaletteColor(9))},
		{"\x1b[31m\x1b[39mX", def},
		{"\x1b[41m\x1b[49mX", def},
		{"\x1b[1;31m\x1b[mX", def},
		{"\x1b[1;31m\x1b[0mX", def},
		{"\x1b[38;5;999mX", def},         // out of range: ignored
		{"\x1b[38;2;1mX", def},           // truncated: ignored
		{"\x1b[2J\x1b[1;1H\x1b[KX", def}, // non-SGR CSI: no effect
		{"\x1b[>4;1mX", def},             // private "m": not SGR
	}
	for _, c := range cases {
		ln := one(t, c.in)
		if len(ln) != 1 || ln[0].Grapheme != "X" {
			t.Errorf("%q: cells = %+v", c.in, ln)
			continue
		}
		if ln[0].Style != c.want {
			t.Errorf("%q: style = %+v, want %+v", c.in, ln[0].Style, c.want)
		}
	}
}

func TestBaseStyle(t *testing.T) {
	base := def.Foreground(tcell.ColorWhite).Background(tcell.NewHexColor(0x282a36))
	ls := ParseStyle("a\x1b[31mb\x1b[39mc\x1b[44md\x1b[49me\x1b[1mf\x1b[0mg", base)
	got := ls[0]
	want := []tcell.Style{
		base, base.Foreground(tcell.PaletteColor(1)), base,
		base.Background(tcell.PaletteColor(4)), base, base.Bold(true), base,
	}
	for i, w := range want {
		if got[i].Style != w {
			t.Errorf("cell %d (%s) = %+v, want %+v", i, got[i].Grapheme, got[i].Style, w)
		}
	}
}

func TestStateCarriesAcrossLines(t *testing.T) {
	ls := Parse("\x1b[1;32mone\ntwo\x1b[0m\nthree")
	if len(ls) != 3 {
		t.Fatalf("lines = %d", len(ls))
	}
	g := def.Bold(true).Foreground(tcell.PaletteColor(2))
	if ls[0][0].Style != g || ls[1][0].Style != g || ls[2][0].Style != def {
		t.Fatalf("styles = %+v / %+v / %+v", ls[0][0].Style, ls[1][0].Style, ls[2][0].Style)
	}
}

func TestOSC8(t *testing.T) {
	link := def.Url("https://example.com/a")
	cases := []string{
		"\x1b]8;;https://example.com/a\x1b\\L\x1b]8;;\x1b\\n",
		"\x1b]8;;https://example.com/a\aL\x1b]8;;\an",
	}
	for _, in := range cases {
		ln := one(t, in)
		if ln.String() != "Ln" || ln[0].Style != link || ln[1].Style != def {
			t.Errorf("%q: %+v", in, ln)
		}
	}
	// id param, link kept across SGR reset, closed after.
	ln := one(t, "\x1b]8;id=7;file:///tmp/x.go\x1b\\\x1b[1mA\x1b[0mB\x1b]8;;\x1b\\C")
	withID := def.Url("file:///tmp/x.go").UrlId("7")
	if ln[0].Style != withID.Bold(true) || ln[1].Style != withID || ln[2].Style != def {
		t.Fatalf("osc8 id: %+v", ln)
	}
	// Other OSC (title) ignored.
	if l := one(t, "\x1b]0;title\aZ"); l.String() != "Z" || l[0].Style != def {
		t.Fatalf("title osc: %+v", l)
	}
}

func TestGraphemes(t *testing.T) {
	ln := one(t, "a日👩‍💻é🇻🇳❤️")
	var gs []string
	var ws []int
	for _, c := range ln {
		gs = append(gs, c.Grapheme)
		ws = append(ws, c.Width)
	}
	if strings.Join(gs, "|") != "a|日|👩‍💻|é|🇻🇳|❤️" {
		t.Fatalf("graphemes = %q", gs)
	}
	if fmt.Sprint(ws) != "[1 2 2 1 2 2]" {
		t.Fatalf("widths = %v", ws)
	}
	// A combining mark split from its base by an escape folds back in.
	ln = one(t, "e\x1b[31ḿx")
	if len(ln) != 2 || ln[0].Grapheme != "é" || ln[0].Style != def {
		t.Fatalf("split combining: %+v", ln)
	}
}

func TestControlsAndTabs(t *testing.T) {
	ln := one(t, "a\tb\r\a\x7fc")
	if ln.String() != "a       bc" || ln.Width() != 10 {
		t.Fatalf("tabs/controls: %q (%d)", ln.String(), ln.Width())
	}
	ln = one(t, "12345678\tx")
	if ln.Width() != 17 {
		t.Fatalf("tab at stop: width %d", ln.Width())
	}
}

func TestKittyPlaceholderRow(t *testing.T) {
	for _, id := range []uint64{1, 9, 42, 255} {
		row, ok := terminal_image.PlaceholderRow(id, 3, 4)
		if !ok {
			t.Fatal("PlaceholderRow failed")
		}
		ln := one(t, row)
		if len(ln) != 4 || ln.Width() != 4 {
			t.Fatalf("id %d: %d cells, width %d", id, len(ln), ln.Width())
		}
		d, _ := terminal_image.Diacritic(3)
		if ln[0].Grapheme != terminal_image.PlaceholderChar+string(d) {
			t.Fatalf("first cell %q lost its row diacritic", ln[0].Grapheme)
		}
		fg := def.Foreground(tcell.PaletteColor(int(id)))
		for i, c := range ln {
			if i > 0 && c.Grapheme != terminal_image.PlaceholderChar {
				t.Fatalf("cell %d = %q", i, c.Grapheme)
			}
			if c.Style != fg {
				t.Fatalf("id %d cell %d style %+v", id, i, c.Style)
			}
		}
		// Byte-faithful: the grid text is the row minus its SGR wrappers.
		if ln.String() != cellwidth.Strip(row) {
			t.Fatalf("text %q != %q", ln.String(), cellwidth.Strip(row))
		}
	}
	// 24-bit id form (row+col diacritics on every cell, 38;2).
	r, _ := terminal_image.Diacritic(0)
	c1, _ := terminal_image.Diacritic(1)
	row := "\x1b[38;2;0;1;44m" + terminal_image.PlaceholderChar + string(r) + string(r) +
		terminal_image.PlaceholderChar + string(r) + string(c1) + "\x1b[39m"
	ln := one(t, row)
	if len(ln) != 2 || ln[1].Grapheme != terminal_image.PlaceholderChar+string(r)+string(c1) ||
		ln[0].Style != def.Foreground(tcell.NewRGBColor(0, 1, 44)) {
		t.Fatalf("24-bit placeholder: %+v", ln)
	}
}

func TestPassthrough(t *testing.T) {
	up := terminal_image.Upload("iVBORw0KGgo=", 5)
	place := terminal_image.PlaceUnicode(5, 3, 2)
	row, _ := terminal_image.PlaceholderRow(5, 0, 3)
	iterm := "\x1b]1337;File=inline=1;size=3;width=2;height=auto;preserveAspectRatio=1:QUJD\a"
	frame := "text\n" + up + place + row + "\x1b]8;;http://x\x1b\\l\x1b]8;;\x1b\\\n" + iterm
	got := Passthrough(frame)
	want := []string{up, place, iterm}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("Passthrough = %q, want %q", got, want)
	}
	ls := Parse(frame)
	if ls[1].String() != cellwidth.Strip(row)+"l" {
		t.Fatalf("graphics leaked into grid: %q", ls[1].String())
	}
}

// TestGlamourFixture runs real glamour (gomark) output through the parser:
// the grid text must equal the escape-stripped output line for line, the
// width must match cellwidth, and some cells must carry color/attributes.
func TestGlamourFixture(t *testing.T) {
	md := "# Title\n\nSome **bold** and *italic* and `code` text [x] [link](https://example.com).\n\n" +
		"| a | b |\n|---|---|\n| 日本 | 👍 |\n\n- item one\n- item two\n\n```go\nfunc main() { fmt.Println(\"hi\") }\n```\n"
	out := gomark.Render(md, 60)
	if !strings.Contains(out, "\x1b[") {
		t.Skip("glamour produced no ANSI (no color profile?)")
	}
	checkFixture(t, out)
}

func TestChromaFixture(t *testing.T) {
	code := "package main\n\nimport \"fmt\"\n\nfunc main() {\n    fmt.Println(\"[x] hi\") // c\n}"
	out := gomark.Highlight(code, "go")
	if out == code {
		t.Skip("chroma produced no highlighting")
	}
	ls := checkFixture(t, out)
	var plain []string
	for _, l := range ls {
		plain = append(plain, l.String())
	}
	if strings.Join(plain, "\n") != code {
		t.Fatalf("chroma text changed:\n%s", strings.Join(plain, "\n"))
	}
}

func checkFixture(t *testing.T, out string) []Line {
	t.Helper()
	ls := Parse(out)
	raw := strings.Split(out, "\n")
	if len(ls) != len(raw) {
		t.Fatalf("lines %d != %d", len(ls), len(raw))
	}
	styled := 0
	for i, l := range ls {
		want := cellwidth.Strip(raw[i])
		if l.String() != want {
			t.Errorf("line %d text %q != %q", i, l.String(), want)
		}
		if l.Width() != cellwidth.StringWidth(raw[i]) {
			t.Errorf("line %d width %d != %d", i, l.Width(), cellwidth.StringWidth(raw[i]))
		}
		for _, c := range l {
			if c.Style != def {
				styled++
			}
		}
	}
	if styled == 0 {
		t.Fatal("no styled cells in fixture")
	}
	return ls
}

func TestCacheMatchesParse(t *testing.T) {
	frame := gomark.Render("# H\n\n**b** `c` [x]\n\n- a\n- b\n", 40) + "\n\x1b[1;31mopen\nstill red\x1b[0m\nplain"
	c := NewCache(4)
	for round := 0; round < 3; round++ {
		got, want := c.Parse(frame), Parse(frame)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("round %d: cache diverges", round)
		}
	}
	if c.Len() > 8 {
		t.Fatalf("cache exceeded 2×max: %d", c.Len())
	}
	// Same text under a different incoming state must not hit.
	c2 := NewCache(0)
	a := c2.Parse("\x1b[31mX\ny")
	b := c2.Parse("X\ny")
	if a[1][0].Style == b[1][0].Style {
		t.Fatal("cache ignored incoming state")
	}
}

func newScreen(t *testing.T, w, h int) tcell.SimulationScreen {
	t.Helper()
	s := tcell.NewSimulationScreen("UTF-8")
	if err := s.Init(); err != nil {
		t.Fatal(err)
	}
	s.SetSize(w, h)
	return s
}

func screenRow(s tcell.SimulationScreen, y int) []tcell.SimCell {
	cs, w, _ := s.GetContents()
	return cs[y*w : (y+1)*w]
}

func TestDraw(t *testing.T) {
	s := newScreen(t, 6, 3)
	defer s.Fini()
	ls := Parse("\x1b[31mab日cd\x1b[0m\nline2\nline3\nline4")
	if n := Draw(s, 1, 0, 4, 2, ls, 1); n != 2 {
		t.Fatalf("rows = %d", n)
	}
	Draw(s, 0, 2, 3, 1, ls, 0) // "ab" + 日 would straddle col 3: dropped
	s.Show()
	row := func(y int) string {
		var b strings.Builder
		for _, c := range screenRow(s, y) {
			if len(c.Runes) == 0 {
				b.WriteByte(' ') // never-drawn cell
				continue
			}
			b.WriteString(string(c.Runes))
		}
		return b.String()
	}
	if got := row(0); got != " line " {
		t.Fatalf("row0 = %q", got)
	}
	if got := row(1); got != " line " {
		t.Fatalf("row1 = %q", got)
	}
	r2 := screenRow(s, 2)
	if string(r2[0].Runes)+string(r2[1].Runes) != "ab" || len(r2[2].Runes) != 0 {
		t.Fatalf("row2 = %+v", r2[:3])
	}
	if fg, _, _ := r2[0].Style.Decompose(); fg != tcell.PaletteColor(1) {
		t.Fatalf("row2 fg = %v", fg)
	}
}

func TestDrawKeepsCombining(t *testing.T) {
	s := newScreen(t, 4, 1)
	defer s.Fini()
	row, _ := terminal_image.PlaceholderRow(42, 2, 2)
	Painter(nil)(s, 0, 0, 4, 1, row)
	s.Show()
	cs := screenRow(s, 0)
	d, _ := terminal_image.Diacritic(2)
	if string(cs[0].Runes) != terminal_image.PlaceholderChar+string(d) {
		t.Fatalf("cell0 runes %q", string(cs[0].Runes))
	}
	if fg, _, _ := cs[0].Style.Decompose(); fg != tcell.PaletteColor(42) {
		t.Fatalf("placeholder fg %v", fg)
	}
}

func BenchmarkParseFrame(b *testing.B) {
	frame := benchFrame()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		Parse(frame)
	}
}

func BenchmarkCacheFrame(b *testing.B) {
	frame := benchFrame()
	c := NewCache(0)
	c.Parse(frame)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c.Parse(frame)
	}
}

// benchFrame is ~60 lines × ~200 cols of styled text, like a full View().
func benchFrame() string {
	var b strings.Builder
	for i := 0; i < 60; i++ {
		for j := 0; j < 20; j++ {
			fmt.Fprintf(&b, "\x1b[38;2;%d;100;200mword%04d \x1b[0m", (i*j)%255, j)
		}
		b.WriteString("│ 日本 ✓\n")
	}
	return b.String()
}
