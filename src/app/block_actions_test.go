package app

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/clipboard"
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
	// A constructed model, not a bare literal: chatRowToBlock now resolves
	// against the painted frame, which renders the header and input.
	m := New(nil, t.TempDir())
	// chatLines must match blockRows: a blank row resolves to no block, so a
	// fixture whose lines came from a welcome view would not describe the
	// block layout it claims to test.
	lines := make([]string, 10)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.chatLines = lines
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.blockRows = []int{0, 3, 9}
	m.winW, m.winH = 100, 40
	// A frame deep enough for row 8 yet shallow enough that the header and
	// input leave it intact, so the painted viewport is m.vp and this stays a
	// pure offset-mapping test; TestChatRowToBlockUsesPaintedFrame covers the
	// shrunken case.
	m.vp.Width, m.vp.Height = 40, 10
	m.vp.YOffset = 2
	if got := m.chatRowToBlock(1); got != 0 {
		t.Fatalf("screen y=1 abs=%d → block %d, want 0", 2+0, got)
	}
	if got := m.chatRowToBlock(8); got != 2 {
		t.Fatalf("screen y=8 abs=%d → block %d, want 2", 2+7, got)
	}
}

// Right-clicking resolves a block against the painted chat frame. With a task
// widget up the frame is shrunk and re-pinned, so resolving against m.vp sent
// the copy menu to a different block than the one under the pointer — and let a
// right-click on the panel itself open a menu for an unrelated block.
func TestChatRowToBlockUsesPaintedFrame(t *testing.T) {
	m := New(nil, t.TempDir())
	m.winW, m.winH = 100, 24
	m.vp.Width, m.vp.Height = 40, 10
	lines := make([]string, 60)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %02d", i)
	}
	m.chatLines = lines
	m.vp.SetContent(strings.Join(lines, "\n"))
	m.vp.GotoBottom()
	m.Todos = []TodoItem{{ID: "1", Content: "Finish design", Status: TodoInProgress}}

	painted := m.chatViewport()
	if painted.Height >= m.vp.Height {
		t.Fatalf("precondition: panel must shrink the frame; painted=%d vp=%d",
			painted.Height, m.vp.Height)
	}

	// Blocks spaced 5 lines apart, so an offset error shows up as a miss.
	m.blockRows = []int{0, 5, 10, 15, 20, 25, 30, 35, 40, 45, 50, 55}

	for row := 1; row <= painted.Height; row++ {
		abs := painted.YOffset + row - 1
		want := -1
		for i, start := range m.blockRows {
			if start > abs {
				break
			}
			want = i
		}
		if got := m.chatRowToBlock(row); got != want {
			t.Fatalf("row %d: chatRowToBlock=%d, but the painted frame has block %d there",
				row, got, want)
		}
	}

	// Every row below the chat frame is the task panel, not a block.
	for row := painted.Height + 1; row < m.vp.Height; row++ {
		if got := m.chatRowToBlock(row); got != -1 {
			t.Fatalf("row %d is below the chat frame (painted=%d vp=%d) but resolved to block %d",
				row, painted.Height, m.vp.Height, got)
		}
	}
}

func TestRightClickOpensBlockActions(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 40
	m.vp.Width, m.vp.Height = 40, 5 // right-click requires an actual viewport row
	m.blocks = []Block{{Kind: "assistant", Text: "# hello"}}
	// Render for real: chatRowToBlock resolves blank rows to no block, so a
	// fixture with hand-set blockRows but no chatLines describes nothing.
	m.renderBlocks()
	m.vp.SetContent(strings.Join(m.chatLines, "\n"))
	if strings.TrimSpace(stripSelectionANSI(m.chatLine(0))) == "" {
		t.Fatalf("precondition: row 0 must hold the block, chatLines=%q", m.chatLines)
	}
	tm, _ := m.Update(tea.MouseMsg{
		X: 1, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight,
	})
	got := tm.(Model)
	if len(got.Dialogs) != 1 || got.Dialogs[0].Kind != "blockactions" {
		t.Fatalf("right-click must open blockactions dialog, got %+v", got.Dialogs)
	}
}

// Enter on a menu row must actually run the copy, not just pop the dialog. The
// builtin test owns the dialog-pops wiring; what lands on the clipboard is this
// package's contract, and a dialog with no Payload would return early here.
func TestRunBlockActionCopiesMarkdown(t *testing.T) {
	var copied string
	defer stubClipboard(func(text string) clipboard.Status {
		copied = text
		return clipboard.Status{Channel: clipboard.Atoto, Bytes: len(text), Chars: len([]rune(text))}
	})()

	m := New(nil, t.TempDir())
	idx := m.AddBlock(Block{Kind: "assistant", Text: "## Result\n\nthe quick brown fox\n"})
	if !m.OpenBlockActions(idx) {
		t.Fatal("an assistant block with text must open a copy menu")
	}
	d := m.Dialogs[0]

	var mdRow = -1
	for i, kind := range d.Payload {
		if kind == "md" {
			mdRow = i
		}
	}
	if mdRow < 0 {
		t.Fatalf("no markdown action offered: payload=%v", d.Payload)
	}

	m.RunBlockAction(d, mdRow)
	if !strings.Contains(copied, "quick brown fox") {
		t.Fatalf("markdown copy missing the block text: %q", copied)
	}
}

// "Copy plain text" must hand over what the reader sees. A markdown link's
// visible text is its label; leaving the [label](url) form in the result is
// exactly what the action promises not to do.
func TestToPlainTextStripsLinkSyntax(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"inline link", "see [pi docs](https://pi.dev) for more", "see pi docs for more"},
		{"link with title", `[a](https://x.dev "T")`, "a"},
		{"bold link", "**[bold link](https://x.dev)**", "bold link"},
		{"autolink", "ping <https://example.com> now", "ping https://example.com now"},
		{"mailto autolink", "mail <mailto:a@b.dev>", "mail mailto:a@b.dev"},
		{"link next to emphasis", "*em* and [lbl](https://x.dev)", "em and lbl"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := toPlainText(tc.in); got != tc.want {
				t.Errorf("toPlainText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// Right-clicking the empty space under a short transcript must not open the
// last block's copy menu: chatRowToBlock resolved any row past a block's start
// to that block, so clicking blank space offered to copy content the pointer
// was nowhere near.
func TestChatRowToBlockIgnoresBlankRows(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 100, 40
	m.vp.Width, m.vp.Height = 40, 20
	m.blocks = []Block{{Kind: "assistant", Text: "only one block"}}
	m.renderBlocks()
	m.vp.SetContent(strings.Join(m.chatLines, "\n"))
	m.vp.GotoBottom()

	// Find a blank chat row below the transcript's last block.
	blank := -1
	for i := len(m.chatLines) - 1; i >= 0; i-- {
		if strings.TrimSpace(stripSelectionANSI(m.chatLines[i])) == "" {
			blank = i
			break
		}
	}
	if blank < 0 {
		t.Skip("transcript has no blank row to test")
	}
	// chatRowToBlock takes a 1-based screen row: abs = YOffset + row - 1.
	rel := blank - m.vp.YOffset + 1
	if rel < 1 || rel > m.vp.Height {
		t.Skipf("blank row %d is outside the visible frame (offset %d, height %d)",
			blank, m.vp.YOffset, m.vp.Height)
	}
	if got := m.chatRowToBlock(rel); got != -1 {
		t.Errorf("blank row %d resolved to block %d, want -1", rel, got)
	}
}

// GFM allows a table with no outer pipes. It renders as a table in the chat,
// so the copy menu must offer it — requiring a leading "|" silently dropped it
// from the menu and made /copy-tables copy nothing.
func TestExtractTablesAcceptsBothPipeForms(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			"with outer pipes",
			"| Name | Status |\n|---|---|\n| Ada | ok |",
			"| Name | Status |\n|---|---|\n| Ada | ok |",
		},
		{
			"without outer pipes",
			"Name | Status\n--- | ---\nAda | ok",
			"Name | Status\n--- | ---\nAda | ok",
		},
		{
			"alignment colons without outer pipes",
			"Name | Status\n:--- | ---:\nAda | ok",
			"Name | Status\n:--- | ---:\nAda | ok",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := extractTables(tc.in)
			if len(got) != 1 {
				t.Fatalf("want 1 table, got %d: %q", len(got), got)
			}
			if got[0] != tc.want {
				t.Errorf("table = %q, want %q", got[0], tc.want)
			}
		})
	}
}

// A pipe in prose is not a table; the separator row is what makes it one.
func TestExtractTablesIgnoresProseWithPipes(t *testing.T) {
	md := "Use `foo | bar` in the shell.\n\nNo table here at all."
	if got := extractTables(md); len(got) != 0 {
		t.Errorf("prose containing a pipe must not parse as a table, got %q", got)
	}
}

// Two adjacent tables must not have the first swallow the second's header.
func TestExtractTablesSeparatesAdjacentTables(t *testing.T) {
	md := "A | B\n--|--\n1 | 2\n\nC | D\n--|--\n3 | 4"
	got := extractTables(md)
	if len(got) != 2 {
		t.Fatalf("want 2 tables, got %d: %q", len(got), got)
	}
	if got[0] != "A | B\n--|--\n1 | 2" {
		t.Errorf("first table = %q", got[0])
	}
	if got[1] != "C | D\n--|--\n3 | 4" {
		t.Errorf("second table = %q", got[1])
	}
}
