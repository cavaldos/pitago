package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// frameCols returns the display-cell positions of every table frame glyph.
func frameCols(r string) []int {
	rs := []rune(r)
	var out []int
	for i := range rs {
		if strings.ContainsRune("│┼┬┴├┤┌┐└┘", rs[i]) {
			out = append(out, lipgloss.Width(string(rs[:i])))
		}
	}
	return out
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

// stripAnsiForTest drops SGR escapes so tests compare visible cells.
func stripAnsiForTest(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// Tables get the old pi-style outer frame (Glamour only draws inner │/─/┼).
func TestFrameTablesBoxed(t *testing.T) {
	src := "| Họ tên | Tuổi |\n|---|---|\n| An | 28 |\n| Bình | 7 |"
	out := Render(src, 80)
	for _, want := range []string{"┌", "┬", "┐", "├", "┤", "└", "┴", "┘"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing frame %q in:\n%s", want, out)
		}
	}
	var want []int
	n := 0
	for _, ln := range strings.Split(out, "\n") {
		set := frameCols(ln)
		if len(set) == 0 {
			continue
		}
		n++
		if want == nil {
			want = set
		} else if !equalInts(set, want) {
			t.Fatalf("frame misaligned in %q vs %v:\n%s", ln, want, out)
		}
	}
	if n < 5 {
		t.Fatalf("framed %d rows, want >=5:\n%s", n, out)
	}
}

// Fit to content: a narrow column (No) stays narrow beside a wide one.
func TestFrameTablesFitContent(t *testing.T) {
	src := "| No | Nội dung chi tiết |\n|---|---|\n| 1 | Hello world |\n| 22 | X |"
	var top string
	for _, ln := range strings.Split(stripAnsiForTest(Render(src, 80)), "\n") {
		if strings.Contains(ln, "┌") {
			top = ln
		}
	}
	if top == "" {
		t.Fatalf("no framed top border:\n%s", Render(src, 80))
	}
	segs := strings.Split(strings.Trim(top, "┌┐"), "┬")
	if len(segs) != 2 {
		t.Fatalf("want 2 columns, top=%q", top)
	}
	if w := len([]rune(segs[0])); w > 4 {
		t.Fatalf("No column not narrow: %q", top)
	}
	if w := len([]rune(top)); w >= 40 {
		t.Fatalf("table not fit to content: %q", top)
	}
}

// Right-aligned columns keep right alignment after fit.
func TestFrameTablesRightAlign(t *testing.T) {
	src := "| No | Giá |\n|---:|---:|\n| 1 | 100 |\n| 22 | 7 |"
	plain := stripAnsiForTest(Render(src, 80))
	for _, want := range []string{"│  1 │ 100 │", "│ 22 │   7 │"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in:\n%s", want, plain)
		}
	}
}
func TestFrameTablesSkipsFramed(t *testing.T) {
	src := "┌─────┬─────┐\n│ A   │ B   │\n├───┼───┤\n│ 1   │ 2   │\n└─────┴─────┘"
	if got := frameTables(src, 80); got != src {
		t.Fatalf("double-framed:\n%s", got)
	}
}

func TestFrameTablesPlainUntouched(t *testing.T) {
	for _, src := range []string{"hello", "a\nb\nc", "- one\n- two\n", "> quote\n> more\n"} {
		if got := frameTables(src, 80); got != src {
			t.Fatalf("changed %q to %q", src, got)
		}
	}
}

func TestRenderFencePiBorder(t *testing.T) {
	src := "Here is code:\n\n```go\nfunc hello() {}\n```\n"
	out := Render(src, 80)
	if !strings.Contains(out, "hello") {
		t.Fatalf("render lost code content: %q", out)
	}
	// Glamour strips the ```go fence and highlights in-place (dark style).
	if !strings.Contains(out, "\x1b[") {
		t.Fatalf("render lost highlight: %q", out)
	}
}

func TestRenderPlainUnchanged(t *testing.T) {
	for _, src := range []string{"", "Hey there!", "plain reply without markers"} {
		if out := Render(src, 80); out != src {
			t.Fatalf("plain %q changed to %q", src, out)
		}
	}
}

func TestRenderBadWidth(t *testing.T) {
	src := "# Title\n\nsome **bold** text\n"
	if out := Render(src, 0); !strings.Contains(out, "Title") {
		t.Fatalf("bad width lost content: %q", out)
	}
}

func TestRenderNoTrailPad(t *testing.T) {
	src := "# Hello\n\nSome **bold** text with `code`:\n\n```go\nfunc hello() {}\n```\n\n- one\n- two\n"
	out := Render(src, 60)
	strip := func(s string) string {
		for {
			start := -1
			for i := 0; i < len(s)-1; i++ {
				if s[i] == 0x1b && s[i+1] == '[' {
					start = i
					break
				}
			}
			if start < 0 {
				return s
			}
			end := start + 2
			for end < len(s) && !((s[end] >= 'a' && s[end] <= 'z') || (s[end] >= 'A' && s[end] <= 'Z')) {
				end++
			}
			end++
			if end > len(s) {
				end = len(s)
			}
			s = s[:start] + s[end:]
		}
	}
	for _, ln := range strings.Split(out, "\n") {
		vis := strip(ln)
		if vis != strings.TrimRight(vis, " \t") {
			t.Fatalf("padded line %q in %q", ln, out)
		}
	}
}

func TestHighlightGo(t *testing.T) {
	out := Highlight("go", "package main\nfunc main() {}\n")
	// ANSI splits tokens, so check words separately.
	for _, w := range []string{"package", "main", "func"} {
		if !strings.Contains(out, w) {
			t.Fatalf("highlight lost %q: %q", w, out)
		}
	}
}

func TestHighlightFallback(t *testing.T) {
	// unknown lang + garbage: must return something containing the input
	out := Highlight("nosuchlang123", "hello world")
	if !strings.Contains(out, "hello world") {
		t.Fatalf("fallback lost code: %q", out)
	}
	if out := Highlight("go", ""); out != "" {
		t.Fatalf("empty in = %q, want empty", out)
	}
	if out := Highlight("", "echo hi"); !strings.Contains(out, "echo hi") {
		t.Fatalf("auto-detect lost code: %q", out)
	}
}
