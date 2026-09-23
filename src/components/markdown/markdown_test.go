package markdown

import (
	"strings"
	"testing"
)

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
