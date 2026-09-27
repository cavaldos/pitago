package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// treeActionDialog builds the menu src/builtin pushes for one tree row.
func treeActionDialog(rows ...string) *Dialog {
	d := &Dialog{Kind: "treeAction", Title: "Tree action",
		Message: "• user: hi", Options: rows, Descs: make([]string, len(rows)),
		Payload: []string{"full entry detail"}, Paths: []string{"entry-1"},
		TreeJump: []int{0}}
	for i := range d.Descs {
		d.Descs[i] = "desc " + rows[i]
	}
	d.Reindex()
	return d
}

func TestRenderTreeActionDialogShowsPiMenu(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := treeActionDialog("Copy entry", "Jump to message", "Close tree")
	got := stripANSI(m.renderTreeActionDialog(d))
	for _, want := range []string{"Tree action", "• user: hi", "Copy entry", "Jump to message", "Close tree"} {
		if !strings.Contains(got, want) {
			t.Errorf("menu missing %q in:\n%s", want, got)
		}
	}
	// The cursor row is the only one marked, so the user always knows which
	// action Enter will run.
	if n := strings.Count(got, "→ "); n != 1 {
		t.Errorf("want exactly one cursor marker, got %d in:\n%s", n, got)
	}
	if !strings.Contains(got, "→ Copy entry") {
		t.Errorf("cursor marker is not on the selected row:\n%s", got)
	}
	if !strings.Contains(got, treeActionFoot) {
		t.Errorf("menu footer missing pi's %q in:\n%s", treeActionFoot, got)
	}
	d.Cursor = 1
	got = stripANSI(m.renderTreeActionDialog(d))
	if !strings.Contains(got, "→ Jump to message") || strings.Count(got, "→ ") != 1 {
		t.Errorf("cursor move did not move the marker:\n%s", got)
	}
}

func TestRenderTreeActionDialogShortensLongRows(t *testing.T) {
	m := Model{winW: 120, winH: 40}
	d := treeActionDialog(strings.Repeat("x", 400))
	box, _, _, _ := m.treeActionLayout(d)
	lines := strings.Split(stripANSI(strings.TrimRight(box, "\n")), "\n")
	// Nothing may wrap: every row has to stay inside the box, border included.
	for i, ln := range lines {
		if w := lipgloss.Width(ln); w > lipgloss.Width(box) {
			t.Fatalf("row %d is %d cells, wider than the box: %q", i, w, ln)
		}
	}
	if !strings.Contains(strings.Join(lines, "\n"), "…") {
		t.Errorf("an overlong label was dropped instead of shortened:\n%s", strings.Join(lines, "\n"))
	}
}

func TestTreeActionDialogIsNotFilterable(t *testing.T) {
	d := treeActionDialog("Copy entry", "Close tree")
	if isFilterKind("treeAction") || filterableDialog(d) {
		t.Fatalf("treeAction must not be filterable: typing is not a feature pi has here")
	}
	m := Model{winW: 120, winH: 40, Dialogs: []*Dialog{d}}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mm := um.(Model)
	if mm.Dialogs[0].Filter != "" {
		t.Fatalf("rune typed into the filter: %q", mm.Dialogs[0].Filter)
	}
	if len(mm.Dialogs[0].FIdx) != 2 {
		t.Fatalf("rune filtered the action list: %v", mm.Dialogs[0].FIdx)
	}
}

func TestEscFromTreeActionReturnsToTree(t *testing.T) {
	tree := &Dialog{Kind: "tree", Options: []string{"a", "b"}, Descs: []string{"x", "y"}}
	tree.Reindex()
	tree.Cursor = 1
	m := Model{winW: 120, winH: 40, Dialogs: []*Dialog{treeActionDialog("Copy entry"), tree}}
	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	mm := um.(Model)
	if len(mm.Dialogs) != 1 || mm.Dialogs[0].Kind != "tree" {
		t.Fatalf("esc must pop only the menu, stack = %d", len(mm.Dialogs))
	}
	if mm.Dialogs[0].Cursor != 1 {
		t.Fatalf("tree selection lost: cursor=%d", mm.Dialogs[0].Cursor)
	}
}

// --- jump to entry ----------------------------------------------------

func jumpTestModel(blocks ...Block) Model {
	m := Model{winW: 120, winH: 40, ready: true, jumpBlock: -1, blocks: blocks}
	m.vp = viewport.New(m.mainW(), 20)
	return m
}

func TestJumpToEntryScrollsAndMarks(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "one"},
		Block{Kind: "tool", ToolName: "bash", ToolStatus: "done", ToolArgs: "ls", ToolResult: "a\nb"},
		Block{Kind: "assistant", Text: "two"},
		Block{Kind: "user", Text: "three"},
	)
	want := []int{0, 2, 3}
	for ordinal, idx := range want {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: JumpToEntry reported no block", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d", ordinal, m.jumpBlock, idx)
		}
		// The block lands about a third from the top, clamped so the last
		// line of the transcript stays reachable.
		off := m.blockLine[idx] - m.vp.Height/3
		if maxOff := m.vp.TotalLineCount() - m.vp.Height; off > maxOff {
			off = maxOff
		}
		if off < 0 {
			off = 0
		}
		if m.vp.YOffset != off {
			t.Fatalf("ordinal %d: YOffset = %d, want %d", ordinal, m.vp.YOffset, off)
		}
		lines := strings.Split(m.renderBlocks(), "\n")
		if m.blockLine[idx] < 1 || !strings.Contains(stripANSI(lines[m.blockLine[idx]-1]), jumpMark) {
			t.Fatalf("ordinal %d: no %q mark above the block:\n%s", ordinal, jumpMark, m.renderBlocks())
		}
	}
}

func TestJumpToEntryRejectsBadOrdinalAndUnpaintedModel(t *testing.T) {
	m := jumpTestModel(Block{Kind: "user", Text: "one"}, Block{Kind: "assistant", Text: "two"})
	for _, bad := range []int{-1, 2, 99} {
		if m.JumpToEntry(bad) {
			t.Fatalf("ordinal %d must not resolve", bad)
		}
	}
	if m.jumpBlock != -1 {
		t.Fatalf("a failed jump armed the mark: %d", m.jumpBlock)
	}
	// Nothing is painted before the first WindowSizeMsg: there is no line
	// table and no viewport, so the caller must fall back to the detail.
	cold := Model{jumpBlock: -1}
	if cold.JumpToEntry(0) {
		t.Fatalf("an unpainted model must report failure")
	}
}

// An aborted turn leaves an EMPTY assistant block behind (text_start opens
// one, the deltas never arrive) while the tree side never numbered that
// message, so an empty block must not consume an ordinal: counting it would
// shift every later jump one message early.
func TestJumpToEntrySkipsEmptyLeftoverBlocks(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "assistant", Text: ""}, // aborted turn, never filled
		Block{Kind: "user", Text: "one"},
		Block{Kind: "assistant", Text: "two"},
	)
	for ordinal, idx := range map[int]int{0: 1, 1: 2} {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: JumpToEntry reported no block", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d (was the empty block counted?)", ordinal, m.jumpBlock, idx)
		}
	}
	if m.JumpToEntry(2) {
		t.Error("an ordinal past the last non-empty block must not resolve")
	}
}

// A user message that carries only images still becomes a transcript block
// ("📷 1 image attached", built from the image count), so it must keep its
// ordinal: this pins the count src/builtin's numbering has to agree with.
func TestJumpToEntryCountsImageOnlyUserBlock(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "📷 1 image attached"},
		Block{Kind: "assistant", Text: "one"},
	)
	for ordinal, idx := range []int{0, 1} {
		if !m.JumpToEntry(ordinal) {
			t.Fatalf("ordinal %d: image-only user block was skipped", ordinal)
		}
		if m.jumpBlock != idx {
			t.Fatalf("ordinal %d: marked block %d, want %d", ordinal, m.jumpBlock, idx)
		}
	}
}

func TestBlockLineTracksEveryBlock(t *testing.T) {
	m := jumpTestModel(
		Block{Kind: "user", Text: "one"},
		Block{Kind: "assistant", Text: ""}, // skipped: must not drift the table
		Block{Kind: "assistant", Text: "two"},
	)
	m.renderBlocks()
	if len(m.blockLine) != len(m.blocks) {
		t.Fatalf("blockLine has %d entries for %d blocks", len(m.blockLine), len(m.blocks))
	}
	if m.blockLine[2] <= m.blockLine[0] || m.blockLine[1] != m.blockLine[2] {
		t.Fatalf("skipped block drifted the table: %v", m.blockLine)
	}
}

func TestBlockCountSeesPostedBlocks(t *testing.T) {
	m := Model{jumpBlock: -1}
	if m.BlockCount() != 0 {
		t.Fatalf("fresh model has %d blocks", m.BlockCount())
	}
	m.AddBlock(Block{Kind: "user", Text: "posted by a tree action"})
	if m.BlockCount() != 1 {
		t.Fatalf("BlockCount = %d after AddBlock", m.BlockCount())
	}
}

// --- mouse ------------------------------------------------------------

func clickModel(d *Dialog) Model {
	m := Model{winW: 120, winH: 40, Dialogs: []*Dialog{d}, confirm: map[string]ConfirmFunc{}}
	return m
}

func leftClick(x, y int) tea.MouseMsg {
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
}

// One click on a tree row runs it, which is how the action menu opens.
func TestTreeRowClickRunsConfirmer(t *testing.T) {
	d := &Dialog{Kind: "tree", Options: []string{"a", "b", "c"}, Descs: []string{"x", "x", "x"}}
	d.Reindex()
	var got []int
	m := clickModel(d)
	m.confirm["tree"] = func(mm *Model, dd *Dialog, ri int) (tea.Model, tea.Cmd) {
		got = append(got, ri)
		return *mm, nil
	}
	// Row 2 sits on the third list line, i.e. header + 2 rows.
	nm, _, ok := m.clickTreeDialog(d, leftClick(4, treeHeaderLines+1+2))
	if !ok {
		t.Fatalf("click inside the row list was not handled")
	}
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("confirmer ran with %v, want [2]", got)
	}
	if nm.(Model).Dialogs[0].Cursor != 2 {
		t.Fatalf("click did not select the row: cursor=%d", nm.(Model).Dialogs[0].Cursor)
	}
}

func TestTreeClickOutsideRowListIsInert(t *testing.T) {
	d := &Dialog{Kind: "tree", Options: []string{"a", "b"}, Descs: []string{"x", "x"}}
	d.Reindex()
	m := clickModel(d)
	m.confirm["tree"] = func(mm *Model, dd *Dialog, ri int) (tea.Model, tea.Cmd) {
		t.Fatalf("a click on the header must not run an action")
		return *mm, nil
	}
	for _, at := range []tea.MouseMsg{leftClick(4, 1), leftClick(4, 200), leftClick(500, treeHeaderLines+2)} {
		if _, _, ok := m.clickTreeDialog(d, at); !ok {
			t.Fatalf("click %+v must still be swallowed by the dialog", at)
		}
	}
	if d.Cursor != 0 {
		t.Fatalf("an inert click moved the cursor to %d", d.Cursor)
	}
}

func TestTreeActionClickSelectsThenRuns(t *testing.T) {
	d := treeActionDialog("Copy entry", "Close tree")
	var got []int
	m := clickModel(d)
	m.confirm["treeAction"] = func(mm *Model, dd *Dialog, ri int) (tea.Model, tea.Cmd) {
		got = append(got, ri)
		return *mm, nil
	}
	// Derive the click point from the renderer's own geometry: row 1 is
	// treeActionBoxTop lines below the box's top border, and tea mouse rows
	// are 1-based.
	_, x0, y0, _ := m.treeActionLayout(d)
	x, y := x0+4, y0+treeActionBoxTop(d)+1+1
	nm, _, ok := m.clickTreeAction(d, leftClick(x, y))
	if !ok {
		t.Fatalf("click inside the box was not handled")
	}
	if len(got) != 0 {
		t.Fatalf("the first click ran the action: %v", got)
	}
	if nm.(Model).Dialogs[0].Cursor != 1 {
		t.Fatalf("click did not select row 1: cursor=%d", nm.(Model).Dialogs[0].Cursor)
	}
	// A second click on the selected row runs it.
	nm, _, _ = m.clickTreeAction(d, leftClick(x, y))
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("second click ran %v, want [1]", got)
	}
	_ = nm
}

func TestTreeActionClickOutsideBoxIsInert(t *testing.T) {
	d := treeActionDialog("Copy entry", "Close tree")
	m := clickModel(d)
	m.confirm["treeAction"] = func(mm *Model, dd *Dialog, ri int) (tea.Model, tea.Cmd) {
		t.Fatalf("a click outside the menu must not run an action")
		return *mm, nil
	}
	_, x0, y0, bh := m.treeActionLayout(d)
	for _, at := range []tea.MouseMsg{
		leftClick(1, 1),                         // far above the box
		leftClick(60, y0+bh+3),                  // below it
		leftClick(x0+40, y0+2),                  // the title line, not a row
		leftClick(x0-6, y0+treeActionBoxTop(d)), // left of the box
	} {
		if _, _, ok := m.clickTreeAction(d, at); !ok {
			t.Fatalf("click %+v must still be swallowed by the dialog", at)
		}
	}
	if d.Cursor != 0 {
		t.Fatalf("an inert click moved the cursor to %d", d.Cursor)
	}
}

// The dialog stack must never leak a click through to the chat behind it.
func TestDialogMouseStaysCapturedForTreeAndMenu(t *testing.T) {
	d := treeActionDialog("Copy entry")
	m := Model{winW: 120, winH: 40, Dialogs: []*Dialog{d}}
	um, cmd := m.Update(leftClick(3, 3))
	if um.(Model).Dialogs[0] != d {
		t.Fatalf("dialog was replaced by a click")
	}
	if cmd != nil {
		t.Fatalf("a swallowed click produced a command")
	}
}

// --- dialog stack + copy ---------------------------------------------

func TestCloseAllDialogsEmptiesStack(t *testing.T) {
	tree := &Dialog{Kind: "tree", Options: []string{"a"}, Descs: []string{"x"}}
	tree.Reindex()
	m := Model{winW: 120, winH: 40, ready: true, jumpBlock: -1,
		Dialogs: []*Dialog{tree, treeActionDialog("Copy entry")}}
	m.CloseAllDialogs()
	if len(m.Dialogs) != 0 {
		t.Fatalf("stack not emptied: %d dialogs left", len(m.Dialogs))
	}
	// Esc on an empty stack must stay a no-op, not a panic.
	m.CloseAllDialogs()
	if len(m.Dialogs) != 0 {
		t.Fatalf("CloseAllDialogs on an empty stack invented dialogs")
	}
}

func TestCopyEntryTextWritesAndHints(t *testing.T) {
	got := stubClip(t, nil)
	m := Model{winW: 120, winH: 40, Dialogs: []*Dialog{treeActionDialog("Copy entry")}}
	cmd := m.CopyEntryText("the full entry detail")
	if len(*got) != 1 || (*got)[0] != "the full entry detail" {
		t.Fatalf("clipboard = %q", *got)
	}
	if !strings.Contains(m.copyHint, "copied") {
		t.Fatalf("copy hint = %q", m.copyHint)
	}
	if cmd == nil {
		t.Fatalf("want the hint retirement cmd")
	}
	if m.CopyEntryText("  ") != nil {
		t.Fatalf("empty text must be quiet")
	}
	if len(*got) != 1 {
		t.Fatalf("empty text wrote to the clipboard")
	}
}

// The tree footer must advertise only keys that are bound: pi's branch /
// label / filter chords are not implemented here and Ctrl+Y (not Ctrl+X) is
// the copy key.
func TestTreeControlsHintOnlyAdvertisesWorkingKeys(t *testing.T) {
	m := Model{winW: 140, winH: 40}
	d := &Dialog{Kind: "tree", Options: []string{"a"}, Descs: []string{"x"}}
	d.Reindex()
	got := stripANSI(m.renderTreeDialog(d))
	if !strings.Contains(got, treeControlsHint) {
		t.Errorf("controls hint missing from:\n%s", got)
	}
	for _, dead := range []string{"ctrl+x", "ctrl+d", "shift+l", "shift+t", "ctrl+o", "option+←"} {
		if strings.Contains(got, dead) {
			t.Errorf("hint still advertises the unimplemented %q:\n%s", dead, got)
		}
	}
	// A live copy confirmation replaces the hint while it is up.
	m.copyHint = "✓ copied 12 chars"
	if got := stripANSI(m.renderTreeDialog(d)); !strings.Contains(got, "copied 12 chars") {
		t.Errorf("copy confirmation not visible over the tree:\n%s", got)
	}
}
