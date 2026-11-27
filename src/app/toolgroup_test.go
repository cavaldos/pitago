package app

import (
	"strings"
	"testing"

	tea "pitago/src/tui/ev"
)

func TestConsecutiveActivityFoldsIntoSummary(t *testing.T) {
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
	if !strings.Contains(out, "2 reads, 1 command") {
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
	for _, want := range []string{"2 reads, 1 command", "README.md", "pyproject.toml", "find src"} {
		if !strings.Contains(out, want) {
			t.Fatalf("open group missing %q:\n%s", want, out)
		}
	}
}

func TestConsecutiveCompactToolRowsHaveNoGap(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 120, 20
	m.blocks = []Block{
		{Kind: "tool", ToolName: "bash", ToolArgs: "go test", ToolStatus: "done", GroupOpen: true},
		{Kind: "assistant", Text: "\n"},
		{Kind: "tool", ToolName: "ctx_execute", ToolArgs: "echo hi", ToolStatus: "done"},
		{Kind: "tool", ToolName: "edit", ToolArgs: "main.go", ToolStatus: "done"},
		{Kind: "thinking", Text: "checking"},
	}
	for range 2 { // cold render and cache hit
		out := stripANSI(m.renderBlocks())
		if !strings.Contains(out, "Bash(go test)\n● Ctx_execute(echo hi)\n● Edit(main.go)\n\n○ checking") {
			t.Fatalf("expected adjacent tool rows with a gap before thinking:\n%s", out)
		}
		for i, want := range map[int]string{2: "Ctx_execute", 3: "Edit", 4: "checking"} {
			row := m.blockRows[i]
			if m.blockLine[i] != row || !strings.Contains(stripANSI(m.chatLines[row]), want) {
				t.Fatalf("wrong row for block %d: %d", i, row)
			}
		}
	}
}

func TestCollapseToolGroupFromFooter(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready, m.Mouse, m.hideSide = true, true, true
	m.winW, m.winH = 120, 32
	m.vp.Width, m.vp.Height = 120, 5
	m.blocks = []Block{
		{Kind: "tool", ToolName: "bash", ToolArgs: "go test", ToolStatus: "done", GroupOpen: true, Expanded: true, ToolResult: strings.Repeat("test output\n", 12)},
		{Kind: "tool", ToolName: "bash", ToolArgs: "git status", ToolStatus: "done"},
	}
	for range 8 {
		m.blocks = append(m.blocks, Block{Kind: "assistant", Text: "later message"})
	}
	m.Refresh()
	footer := -1
	for row, text := range m.chatLines {
		if stripANSI(text) == "[Collapse]" {
			footer = row
			break
		}
	}
	if footer <= m.blockRows[1] || footer >= m.blockRows[2] {
		t.Fatalf("collapse button should be after the last tool and before the next message; row=%d", footer)
	}
	m.vp.SetYOffset(footer - 2)
	painted := m.chatViewport()
	if painted.YOffset <= m.blockRows[0] {
		t.Fatal("precondition: group header is scrolled offscreen")
	}
	y := footer - painted.YOffset + 1
	msg := tea.MouseMsg{X: 2, Y: y, Button: tea.MouseButtonLeft, Action: tea.MouseActionPress}
	updated, _ := m.Update(msg)
	msg.Action = tea.MouseActionRelease
	updated, _ = updated.Update(msg)
	m = updated.(Model)
	out := stripANSI(m.chatContent)
	if m.blocks[0].GroupOpen || strings.Contains(out, "[Collapse]") || strings.Contains(out, "Bash(git status)") {
		t.Fatalf("the button did not collapse the group:\n%s", out)
	}
	if m.chatViewport().YOffset > m.blockRows[0] {
		t.Fatal("collapsed summary is still scrolled offscreen")
	}
}

func TestThinkingAndCommandsFoldTogether(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 120, 20
	m.blocks = []Block{
		{Kind: "bash", Text: "$ echo one"},
		{Kind: "thinking", Text: "considering"},
		{Kind: "tool", ToolName: "read", ToolStatus: "done"},
		{Kind: "tool", ToolName: "write", ToolStatus: "done"},
	}
	out := stripANSI(m.renderBlocks())
	if !strings.Contains(out, "1 command, 1 thought, 1 read, 1 tool") {
		t.Fatalf("missing activity summary:\n%s", out)
	}
	for _, hidden := range []string{"$ echo one", "considering"} {
		if strings.Contains(out, hidden) {
			t.Fatalf("folded activity %q is still rendered:\n%s", hidden, out)
		}
	}

	m.blocks[0].GroupOpen = true
	out = stripANSI(m.renderBlocks())
	for _, visible := range []string{"$ echo one", "considering"} {
		if !strings.Contains(out, visible) {
			t.Fatalf("expanded group missing %q:\n%s", visible, out)
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
	if !strings.Contains(out, "2 tools…") {
		t.Fatalf("running summary wrong:\n%s", out)
	}
}

func TestFinishedGroupStopsBlinking(t *testing.T) {
	m := New(nil, t.TempDir())
	m.vp.Width, m.vp.Height = 120, 20
	// Thinking and shell-echo blocks carry no status; they must not pin
	// the group's dot on "running" after every real call has finished.
	m.blocks = []Block{
		{Kind: "tool", ToolName: "read", ToolStatus: "done"},
		{Kind: "bash", Text: "$ echo hi\nhi"},
		{Kind: "thinking", Text: "considering"},
		{Kind: "tool", ToolName: "bash", ToolStatus: "done", ToolResult: "line one\nline two\nline three"},
	}
	if m.groupRunning(0) {
		t.Fatal("finished group still reports running")
	}
	out := stripANSI(m.renderBlocks())
	if strings.Contains(out, "…") {
		t.Fatalf("finished group still shows running ellipsis:\n%s", out)
	}
	// Collapsed summary shows the last two lines of the latest command.
	for _, want := range []string{"line two", "line three"} {
		if !strings.Contains(out, want) {
			t.Fatalf("summary missing output tail %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "line one") {
		t.Fatalf("summary shows more than the tail:\n%s", out)
	}

	// Expanding the group hides the tail (the full call is right below).
	m.blocks[0].GroupOpen = true
	out = stripANSI(m.renderBlocks())
	if strings.Contains(out, "line three") && strings.Count(out, "line three") != 1 {
		t.Fatalf("expanded group duplicates the tail:\n%s", out)
	}
	if !strings.Contains(out, "echo hi") {
		t.Fatalf("expanded group lost the call itself:\n%s", out)
	}
}
