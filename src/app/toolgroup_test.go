package app

import (
	"strings"
	"testing"
)

func TestConsecutiveToolCallsFoldIntoSummary(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 120, 20
	m.blocks = []Block{
		{Kind: "tool", ToolName: "read", ToolArgs: "/a/README.md", ToolStatus: "done"},
		{Kind: "assistant", Text: "\n"}, // empty leftover must not split the run
		{Kind: "tool", ToolName: "read", ToolArgs: "/a/pyproject.toml", ToolStatus: "done"},
		{Kind: "tool", ToolName: "bash", ToolArgs: "find src", ToolStatus: "done"},
		{Kind: "assistant", Text: "done reading"},
		{Kind: "tool", ToolName: "read", ToolArgs: "/a/solo.md", ToolStatus: "done"},
	}
	out := stripANSI(m.renderBlocks())
	if !strings.Contains(out, "Read 2 files, ran 1 shell command") {
		t.Fatalf("missing summary row:\n%s", out)
	}
	if strings.Contains(out, "README.md") || strings.Contains(out, "find src") {
		t.Fatalf("folded calls still rendered:\n%s", out)
	}
	if !strings.Contains(out, "solo.md") {
		t.Fatalf("a lone call must render on its own:\n%s", out)
	}

	m.blocks[0].GroupOpen = true
	out = stripANSI(m.renderBlocks())
	for _, want := range []string{"Read 2 files", "README.md", "pyproject.toml", "find src"} {
		if !strings.Contains(out, want) {
			t.Fatalf("open group missing %q:\n%s", want, out)
		}
	}
}

func TestToolGroupSummaryRunning(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 120, 20
	m.blocks = []Block{
		{Kind: "tool", ToolName: "grep", ToolStatus: "done"},
		{Kind: "tool", ToolName: "glob", ToolStatus: "running"},
	}
	out := stripANSI(m.renderBlocks())
	if !strings.Contains(out, "Searching for 2 patterns…") {
		t.Fatalf("running summary wrong:\n%s", out)
	}
}
