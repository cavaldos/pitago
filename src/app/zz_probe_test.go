package app

import (
	"strings"
	"testing"

	lp "github.com/charmbracelet/lipgloss"

	"pitago/src/components/format"
)

func ansiWidth(s string) int { return lp.Width(s) }

func TestProbeHeaderWidth(t *testing.T) {
	for _, w := range []int{10, 20, 30, 44, 60, 80, 120} {
		inner := blockInner(w)
		bl := Block{Kind: "tool", ToolName: "read", ToolArgs: "some/very/long/path/to/a/file.go", ToolStatus: "done"}
		row := toolHeaderRow(bl, toolHead(bl), inner)
		t.Logf("w=%d inner=%d rowwidth=%d %q", w, inner, ansiWidth(row), row)
	}
}

func TestProbeFramedBlock(t *testing.T) {
	rows := []string{"hello", "world", "x"}
	for _, w := range []int{10, 20, 44} {
		out := framedBlock(rows, w, blockThemeFor(format.StatusSuccess))
		for i, ln := range strings.Split(out, "\n") {
			t.Logf("w=%d line %d width=%d %q", w, i, ansiWidth(ln), ln)
		}
	}
}

func TestProbeLspSection(t *testing.T) {
	m := Model{}
	m.blocks = []Block{{
		Kind:       "tool",
		ToolName:   "lsp_diagnostics",
		ToolStatus: "done",
		ToolCallID: "c1",
		ToolResult: "gopls LSP diagnostics: 2 diagnostic(s) across 1 file(s).\n\nsrc/app/x.go:12:5: error staticcheck: unused variable\nsrc/app/x.go:3:1: warning govet S1000: should use x += 1\n",
	}}
	for _, inner := range []int{4, 6, 8, 10, 15, 20, 26, 30} {
		out := m.renderLspSection(inner)
		t.Logf("--- inner=%d", inner)
		for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			t.Logf("  w=%d %q", ansiWidth(ln), ln)
		}
	}
}

func TestProbeLspLongServerName(t *testing.T) {
	m := Model{}
	m.blocks = []Block{{
		Kind:       "tool",
		ToolName:   "lsp_diagnostics",
		ToolStatus: "done",
		ToolCallID: "c1",
		ToolResult: "typescript-language-server LSP diagnostics: 13 diagnostic(s) across 2 file(s).\n\nsrc/app/x.go:12:5: error staticcheck: unused variable\n",
	}}
	for _, inner := range []int{26, 30, 34} {
		out := m.renderLspSection(inner)
		t.Logf("--- inner=%d", inner)
		for _, ln := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
			t.Logf("  w=%d %q", ansiWidth(ln), ln)
		}
	}
}

func TestProbeToolDetailNeutral(t *testing.T) {
	for _, s := range []string{"", "running", "done", "error", "cancelled", "aborted", "timeout"} {
		t.Logf("status=%q class=%q detail=%q", s, format.ToolStatusClass(s), toolDetail(Block{ToolStatus: s}))
	}
}
