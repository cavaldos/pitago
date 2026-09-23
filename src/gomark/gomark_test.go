package gomark

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*[a-zA-Z]")

func strip(s string) string { return ansiSeq.ReplaceAllString(s, "") }

func TestRenderTable(t *testing.T) {
	src := "| Họ tên | Tuổi |\n|---|---:|\n| An | 28 |\n| Bình | 7 |"
	out := strip(Render(src, 80))
	for _, want := range []string{"Họ tên", "Tuổi", "An", "28"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "─") && !strings.Contains(out, "│") && !strings.Contains(out, "|") {
		t.Fatalf("no table border in:\n%s", out)
	}
}

func TestPassthrough(t *testing.T) {
	if got := Render("", 80); got != "" {
		t.Fatalf("empty changed: %q", got)
	}
}
