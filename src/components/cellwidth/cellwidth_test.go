package cellwidth

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestEscapeLen(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 0},
		{"\x1b", 1},
		{"\x1b[0mX", 4},
		{"\x1b[38;2;1;2;3mX", 13},
		{"\x1b[4:3mX", 6},
		{"\x1b[1A", 4},
		{"\x1b]8;;https://x.y\x1b\\X", 18},
		{"\x1b]8;;https://x.y\aX", 17},
		{"\x1b_Ga=p,i=1\x1b\\X", 12},
		{"\x1b(BX", 3},
		{"\x1b7X", 2},
		{"\x1b[38;5", 6}, // unterminated CSI: to end
		{"\x1b]8;;http", 9},
	}
	for _, c := range cases {
		if got := EscapeLen(c.in); got != c.want {
			t.Errorf("EscapeLen(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestStrip(t *testing.T) {
	in := "\x1b[1;31mred\x1b[0m \x1b]8;;http://a\x1b\\link\x1b]8;;\x1b\\ [x]"
	if got := Strip(in); got != "red link [x]" {
		t.Fatalf("Strip = %q", got)
	}
}

func TestStringWidth(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"", 0},
		{"abc", 3},
		{"\x1b[31mabc\x1b[0m", 3},
		{"日本", 4},
		{"é", 1},           // combining acute
		{"👍", 2},            // emoji presentation
		{"👩‍💻", 2},          // ZWJ sequence = one cluster
		{"🇻🇳", 2},           // regional-indicator flag
		{"❤️", 2},           // VS16 forces emoji presentation
		{"\U0010EEEE̅̍", 1}, // kitty placeholder + diacritics
		{"│─┼", 3},
		{"…", 1},
	}
	for _, c := range cases {
		if got := StringWidth(c.in); got != c.want {
			t.Errorf("StringWidth(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestWidthIsWidestLine(t *testing.T) {
	if got := Width("ab\n\x1b[1mabcd\x1b[0m\nx"); got != 4 {
		t.Fatalf("Width = %d, want 4", got)
	}
}

// TestLipglossParity pins agreement with lipgloss.Width (the helper this
// package replaces) on the text pitago actually renders: ASCII, box drawing,
// CJK, styled runs, links. Emoji/ZWJ edge cases are covered above with
// uniseg's (tcell's) answer, which is the one that matches the screen.
func TestLipglossParity(t *testing.T) {
	corpus := []string{
		"", "hello world", "  indented\t", "│ a │ b │", "┌──┬──┐",
		"日本語テキスト", "Tiếng Việt có dấu", "\x1b[1;38;5;203merror\x1b[0m",
		"\x1b[38;2;97;175;239m#61AFEF\x1b[0m", "\x1b]8;;file:///tmp/a.go\x1b\\a.go\x1b]8;;\x1b\\",
		"line1\nlonger line2\n", "…⏎ ✓ ✗ ● ○ ▸ ▾", "█████░░░░░",
	}
	for _, s := range corpus {
		if got, want := Width(s), lipgloss.Width(s); got != want {
			t.Errorf("Width(%q) = %d, lipgloss = %d", s, got, want)
		}
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		tail string
		want string
	}{
		{"hello", 10, "…", "hello"},
		{"hello", 5, "…", "hello"},
		{"hello world", 5, "…", "hell…"},
		{"hello world", 5, "", "hello"},
		{"日本語", 5, "…", "日本…"},
		{"日本語", 4, "…", "日…"}, // 日=2, 本 would straddle the limit (3)
		{"日本語", 3, "", "日"},   // never split a wide cell
		{"a👩‍💻b", 2, "", "a"}, // ZWJ cluster dropped, never split
		{"a👩‍💻b", 3, "", "a👩‍💻"},
		{"ééé", 2, "", "éé"},
		{"abc", 0, "…", ""},
		{"abcdef", 1, "…", "…"},
		{"abcdef", 2, "...", ".."}, // tail wider than n is itself cut
		// Escapes survive: the reset after the cut still lands.
		{"\x1b[31mhello world\x1b[0m", 6, "…", "\x1b[31mhello…\x1b[0m"},
		// OSC 8 terminator is preserved after the cut.
		{"\x1b]8;;http://a\x1b\\abcdef\x1b]8;;\x1b\\", 3, "", "\x1b]8;;http://a\x1b\\abc\x1b]8;;\x1b\\"},
	}
	for _, c := range cases {
		got := Truncate(c.in, c.n, c.tail)
		if got != c.want {
			t.Errorf("Truncate(%q, %d, %q) = %q, want %q", c.in, c.n, c.tail, got, c.want)
		}
		if c.n > 0 && StringWidth(got) > c.n {
			t.Errorf("Truncate(%q, %d) width %d > n", c.in, c.n, StringWidth(got))
		}
	}
}

func TestGraphemes(t *testing.T) {
	var gs []string
	var ws []int
	Graphemes("a日👩‍💻é", func(g string, w int) bool {
		gs = append(gs, g)
		ws = append(ws, w)
		return true
	})
	if strings.Join(gs, "|") != "a|日|👩‍💻|é" {
		t.Fatalf("graphemes = %q", gs)
	}
	if want := []int{1, 2, 2, 1}; !equalInts(ws, want) {
		t.Fatalf("widths = %v, want %v", ws, want)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
