package gomark

import (
	"strings"
	"testing"
)

func TestRenderTable(t *testing.T) {
	src := "| Họ tên | Tuổi |\n|---|---:|\n| An | 28 |\n| Bình | 7 |"
	out := Render(src, 80)
	plain := stripInline(out)
	for _, want := range []string{"Họ tên", "Tuổi", "An", "28", "┌", "┬", "┐", "└", "│"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
}

func TestPassthrough(t *testing.T) {
	if got := Render("hello", 80); got != "hello" {
		t.Fatalf("plain changed: %q", got)
	}
}
