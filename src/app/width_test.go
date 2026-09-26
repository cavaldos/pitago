package app

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// displayWidth replaces ansi.StringWidth on the frame hot path, so it has to
// return the identical number for every input. These cases are the shapes a
// real frame contains.
func TestDisplayWidthMatchesAnsi(t *testing.T) {
	cases := []string{
		"",
		"plain ascii",
		"with  spaces   inside",
		"\x1b[1mbold\x1b[0m",
		"\x1b[38;5;208mtruecolor\x1b[0m",
		"\x1b[1;4;38;2;120;200;255mfancy\x1b[0m reset",
		"● gutter bullet",
		"\x1b[38;2;120;200;255m●\x1b[0m styled bullet",
		"─│┌┐└┘├┤┬┴┼ box drawing",
		"✓ ✿ ❯ ▸ ◐",
		"日本語のテキスト",
		"中文宽度测试",
		"emoji 👨‍👩‍👧 family zwj",
		"flag 🇻🇳 regional indicators",
		"combining é vs é",
		"accent é",
		"tab\there",
		"cr\rlf",
		"\x1b]0;title\x07after osc",
		"\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\",
		"\x1b(B ascii charset designation",
		"\x1b7\x1b8 save restore",
		"\x1b[?25l hide cursor",
		"\x1b[<64;10;5M sgr mouse",
		"mixed ● ascii ─ 和 日本 \x1b[1mstyled\x1b[0m tail",
		"\x1b",
		"\x1b[",
		"\x1b[1",
	}
	for _, c := range cases {
		if got, want := displayWidth(c), ansi.StringWidth(c); got != want {
			t.Errorf("displayWidth(%q) = %d, ansi.StringWidth = %d", c, got, want)
		}
	}
}

// Randomised equivalence over escape-heavy ASCII, plus mixed non-ASCII runs:
// this is the property the fast path actually rests on.
func TestDisplayWidthMatchesAnsiRandom(t *testing.T) {
	pieces := []string{
		"a", "Z", "0", " ", "~", "!", "?", ":", ";", "m", "K", "M",
		"\x1b[", "\x1b[0m", "\x1b[1;31m", "\x1b[38;5;", "H", "\x1b]0;x\x07",
		"\x1b]8;;u\x1b\\", "\x1b(B", "\x1b7", "\x1b", "\x1b[?", "l", "h",
		"\x07", "\r", "\t", "\x7f",
		"●", "─", "✓", "日", "本", "語", "é", "é", "👨", "👩", "‍",
		"🇻", "🇳", " ", "́",
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 40000; i++ {
		var sb strings.Builder
		for n := rng.Intn(8); n > 0; n-- {
			sb.WriteString(pieces[rng.Intn(len(pieces))])
		}
		s := sb.String()
		if got, want := displayWidth(s), ansi.StringWidth(s); got != want {
			t.Fatalf("displayWidth(%q) = %d, ansi.StringWidth = %d", s, got, want)
		}
	}
}

// The joins replace lipgloss's in the frame assembly, so they must produce
// byte-identical output for every block arrangement the app can build.
func TestJoinsMatchLipgloss(t *testing.T) {
	blocks := [][]string{
		{"", ""},
		{"a", "b"},
		{"a\nbb\nccc", "d\ne"},
		{"x", "y\nz\nw"},
		{"long line here\nshort", "s\nlooooooonger line\nm"},
		{"\x1b[1mbold\x1b[0m\nplain", "● bullet\n日本語"},
		{"\x1b[38;5;208mcolored\x1b[0m", ""},
		{"", "only second"},
		{"only first", ""},
		{strings.Repeat("wide ", 30), "a\nb\nc\nd"},
		{"\x1b]8;;https://x\x1b\\url\x1b]8;;\x1b\\", "trailing"},
		{"trailing", "\x1b]8;;https://x\x1b\\url\x1b]8;;\x1b\\"},
	}
	for i, group := range blocks {
		vertical := joinVerticalLeft(group...)
		if want := lipgloss.JoinVertical(lipgloss.Left, group...); vertical != want {
			t.Errorf("case %d joinVerticalLeft:\n got %q\nwant %q", i, vertical, want)
		}
		horizontal := joinHorizontalTop(group...)
		if want := lipgloss.JoinHorizontal(lipgloss.Top, group...); horizontal != want {
			t.Errorf("case %d joinHorizontalTop:\n got %q\nwant %q", i, horizontal, want)
		}
	}
	// A single block short-circuits in both implementations.
	if got, want := joinVerticalLeft("only"), lipgloss.JoinVertical(lipgloss.Left, "only"); got != want {
		t.Errorf("single block vertical: got %q want %q", got, want)
	}
	if got, want := joinHorizontalTop("only"), lipgloss.JoinHorizontal(lipgloss.Top, "only"); got != want {
		t.Errorf("single block horizontal: got %q want %q", got, want)
	}
	if joinVerticalLeft() != lipgloss.JoinVertical(lipgloss.Left) {
		t.Error("no blocks must agree")
	}
}

// Randomised block arrangements, including ragged heights and non-ASCII, to
// catch any shape the hand-written cases missed.
func TestJoinsMatchLipglossRandom(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	line := func() string {
		n := rng.Intn(12)
		alphabet := []rune("abc ●─✓日本語é\x1b[1m\x1b[0m ")
		out := make([]rune, n)
		for i := range out {
			out[i] = alphabet[rng.Intn(len(alphabet))]
		}
		return string(out)
	}
	for i := 0; i < 3000; i++ {
		group := make([]string, 1+rng.Intn(4))
		for g := range group {
			rows := rng.Intn(5)
			parts := make([]string, rows)
			for r := range parts {
				parts[r] = line()
			}
			group[g] = strings.Join(parts, "\n")
		}
		if got, want := joinVerticalLeft(group...), lipgloss.JoinVertical(lipgloss.Left, group...); got != want {
			t.Fatalf("case %d vertical %q:\n got %q\nwant %q", i, group, got, want)
		}
		if got, want := joinHorizontalTop(group...), lipgloss.JoinHorizontal(lipgloss.Top, group...); got != want {
			t.Fatalf("case %d horizontal %q:\n got %q\nwant %q", i, group, got, want)
		}
	}
}

// escapeLen must never claim a sequence is unrecognised when the state
// machine would treat the bytes differently: a wrong skip would shift every
// cell count after it.
func TestEscapeLenNeverUnderruns(t *testing.T) {
	seqs := []string{
		"\x1b[0m", "\x1b[38;2;1;2;3m", "\x1b[?1049h", "\x1b[1;2;3;4;5;6;7;8;9;10;11m",
		"\x1b[4:3m", "\x1b[ q", "\x1b[0 q", "\x1b(B", "\x1b)0", "\x1b#8", "\x1b%G",
		"\x1b]0;title\x07", "\x1b]0;title\x1b\\", "\x1bP1;2q\x1b\\", "\x1b_x\x1b\\",
		"\x1b7", "\x1b8", "\x1bc", "\x1bD", "\x1bM", "\x1bE", "\x1b=", "\x1b>",
	}
	for _, s := range seqs {
		n := escapeLen(s)
		if n <= 0 || n > len(s) {
			t.Errorf("escapeLen(%q) = %d, want 1..%d", s, n, len(s))
		}
	}
}
