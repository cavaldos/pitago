package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const sampleMD = "## Result\n\n| Name | Status |\n|---|---|\n| Alice | OK |\n| Bob | Fail |\n\n" +
	"```ts\nconst x: number = 1;\n```\n\n" +
	"Done. **bold** and *italic* and `inline`.\n"

func TestExtractTables(t *testing.T) {
	tables := extractTables(sampleMD)
	if len(tables) != 1 {
		t.Fatalf("want 1 table, got %d:\n%v", len(tables), tables)
	}
	got := tables[0]
	for _, want := range []string{
		"| Name | Status |",
		"|---|---|",
		"| Alice | OK |",
		"| Bob | Fail |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("table missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "┌") || strings.Contains(got, "│") {
		t.Fatal("extracted table must be raw markdown pipes, not rendered frame")
	}
}

func TestExtractCodeBlocks(t *testing.T) {
	blocks := extractCodeBlocks(sampleMD)
	if len(blocks) != 1 {
		t.Fatalf("want 1 code block, got %d", len(blocks))
	}
	want := "```ts\nconst x: number = 1;\n```"
	if blocks[0] != want {
		t.Fatalf("code block mismatch:\ngot  %q\nwant %q", blocks[0], want)
	}
}

func TestToPlainText(t *testing.T) {
	got := toPlainText(sampleMD)
	for _, absent := range []string{"##", "**", "```", "│"} {
		if strings.Contains(got, absent) {
			t.Fatalf("plain text must not contain %q:\n%s", absent, got)
		}
	}
	// Cell separators (" | ") are fine (TS contract); leading-pipe table
	// syntax is not.
	for _, ln := range strings.Split(got, "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "|") {
			t.Fatalf("plain text must not keep leading-pipe rows:\n%s", got)
		}
	}
	for _, present := range []string{"Name", "Alice", "const x", "Done", "inline"} {
		if !strings.Contains(got, present) {
			t.Fatalf("plain text missing %q:\n%s", present, got)
		}
	}
}

func TestCollectBlockContentAssistant(t *testing.T) {
	c := collectBlockContent(Block{Kind: "assistant", Text: sampleMD})
	if c.Markdown != sampleMD {
		t.Fatal("markdown must be the raw block text")
	}
	if len(c.Tables) != 1 || len(c.CodeBlocks) != 1 {
		t.Fatalf("expected 1 table + 1 code block, got %d/%d", len(c.Tables), len(c.CodeBlocks))
	}
}

func TestCollectBlockContentToolUsesToolResult(t *testing.T) {
	c := collectBlockContent(Block{Kind: "tool", ToolName: "bash", Text: "", ToolResult: "| a | b |\n|---|---|\n| 1 | 2 |"})
	if len(c.Tables) != 1 {
		t.Fatalf("tool block must extract tables from ToolResult, got %d", len(c.Tables))
	}
}

func TestBuildBlockOptionsPresence(t *testing.T) {
	c := BlockContent{
		Markdown:   "hi",
		CodeBlocks: []string{"```go\nx\n```"},
		Tables:     []string{"| a |\n|---|\n| 1 |"},
		Plain:      "hi",
	}
	opts, payload := buildBlockOptions(c)
	// Four copy actions: markdown, code, tables, plain. There is no "launch it in
	// an external viewer" action — that needed an external binary and opened a
	// window outside the app.
	want := []string{"md", "code", "tables", "plain"}
	if len(opts) != len(want) || len(payload) != len(want) {
		t.Fatalf("want %d options/payloads, got %d/%d: %v", len(want), len(opts), len(payload), opts)
	}
	for i := range want {
		if payload[i] != want[i] {
			t.Fatalf("payload order wrong: got %v want %v", payload, want)
		}
	}
	for _, o := range opts {
		if strings.Contains(strings.ToLower(o), "preview") {
			t.Fatalf("the external preview action is gone, found %q", o)
		}
	}
}

// A block with no content has nothing to copy, so there is no menu to open.
// Before the preview action was removed, an empty block still offered
// "Preview as markdown" and that was the only way to get a dialog at all.
func TestBuildBlockOptionsEmptyBlock(t *testing.T) {
	opts, payload := buildBlockOptions(BlockContent{})
	if len(opts) != 0 || len(payload) != 0 {
		t.Fatalf("an empty block has nothing to copy, got %v", opts)
	}
}

func TestNewBlockActionsDialog(t *testing.T) {
	blocks := []Block{{Kind: "assistant", Text: sampleMD}}
	d := newBlockActionsDialog(blocks, 0)
	if d == nil {
		t.Fatal("dialog must build for assistant")
	}
	if d.Kind != "blockactions" || d.BlockIdx != 0 {
		t.Fatalf("bad dialog identity: %+v", d)
	}
	want := []string{"Copy markdown", "Copy 1 code block", "Copy 1 table", "Copy plain text"}
	if len(d.Options) != len(want) {
		t.Fatalf("options=%v want=%v", d.Options, want)
	}
	for i := range want {
		if d.Options[i] != want[i] {
			t.Fatalf("option %d=%q want %q", i, d.Options[i], want[i])
		}
	}
	if newBlockActionsDialog(blocks, 5) != nil {
		t.Fatal("out-of-range index must return nil")
	}
}

func TestChatRowToBlock(t *testing.T) {
	m := &Model{blockRows: []int{0, 3, 9}}
	m.vp.YOffset = 2
	if got := m.chatRowToBlock(1); got != 0 {
		t.Fatalf("screen y=1 abs=%d → block %d, want 0", 2+0, got)
	}
	if got := m.chatRowToBlock(8); got != 2 {
		t.Fatalf("screen y=8 abs=%d → block %d, want 2", 2+7, got)
	}
}

func TestRightClickOpensBlockActions(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.blocks = []Block{{Kind: "assistant", Text: "# hello"}}
	m.blockRows = []int{0}
	m.vp.Height = 5 // right-click requires an actual viewport row
	tm, _ := m.Update(tea.MouseMsg{
		X: 1, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight,
	})
	got := tm.(Model)
	if len(got.Dialogs) != 1 || got.Dialogs[0].Kind != "blockactions" {
		t.Fatalf("right-click must open blockactions dialog, got %+v", got.Dialogs)
	}
}
