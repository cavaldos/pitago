package builtin

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
)

// Pi's Esc from the follow-up selector re-opens the tree with the same row
// selected, so "Back to tree" pops the menu only — and the tree must survive
// with its cursor intact.
func TestTreeActionBackKeepsTreeOpen(t *testing.T) {
	m := &app.Model{}
	tree := &app.Dialog{Kind: "tree", Title: "Session Tree", Options: []string{"a", "b"}, Cursor: 1}
	tree.Reindex()
	act := &app.Dialog{Kind: "treeAction", Options: []string{TreeActBack, TreeActJump}}
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog: the menu sits in front of the tree.
	m.Dialogs = append(m.Dialogs, act, tree)
	if _, cmd := confirmTreeAction(m, m.Dialogs[0], 0); cmd != nil {
		t.Error("back to tree is synchronous")
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "tree" {
		t.Fatalf("only the menu may close, got %d dialogs", len(m.Dialogs))
	}
	if m.Dialogs[0].Cursor != 1 {
		t.Errorf("tree cursor = %d, want the selected row kept", m.Dialogs[0].Cursor)
	}
}

// Copy pops the menu only and returns the Cmd, so the confirmation hint
// lands in the still-open tree's footer (pi's onCopy behaviour).
func TestTreeActionCopyKeepsTreeOpen(t *testing.T) {
	labels := []string{TreeActJump, TreeActCopy, TreeActFork, TreeActBack}
	m := &app.Model{}
	tree := &app.Dialog{Kind: "tree", Options: []string{"• user: hi"}}
	tree.Reindex()
	act := &app.Dialog{Kind: "treeAction", Options: labels, Payload: []string{"hi · id e1 · 10:00\n\nhi"}}
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog: the menu sits in front of the tree.
	m.Dialogs = append(m.Dialogs, act, tree)
	before := m.BlockCount()
	_, cmd := confirmTreeAction(m, m.Dialogs[0], 1)
	if cmd == nil {
		t.Error("copy must return the clipboard cmd")
	}
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "tree" {
		t.Fatalf("only the menu may close, got %d dialogs", len(m.Dialogs))
	}
	if m.BlockCount() != before {
		t.Error("copy must not print the entry")
	}
}

// "View entry" is the local stand-in for pi's navigateTree: it leaves the
// tree and prints the entry detail.
func TestTreeActionViewPrintsEntry(t *testing.T) {
	detail := "[compaction: 12k tokens]\ncompaction · id e3 · 10:00\n\ntokens before compaction: 12000"
	labels := []string{TreeActView, TreeActCopy, TreeActBack}
	m := &app.Model{}
	tree := &app.Dialog{Kind: "tree", Options: []string{"[compaction: 12k tokens]"}}
	tree.Reindex()
	act := &app.Dialog{Kind: "treeAction", Options: labels, Payload: []string{detail}, Paths: []string{"e3"}}
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog: the menu sits in front of the tree.
	m.Dialogs = append(m.Dialogs, act, tree)
	if _, cmd := confirmTreeAction(m, m.Dialogs[0], 0); cmd != nil {
		t.Error("view entry is synchronous")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("both dialogs must close, got %d", len(m.Dialogs))
	}
	if m.BlockCount() != 1 {
		t.Fatalf("blocks = %d, want the entry printed", m.BlockCount())
	}
	if !strings.Contains(detail, "compaction") {
		t.Error("detail payload missing")
	}
}

// A row can carry an ordinal the loaded transcript does not have (/tree
// before the messages land). Scrolling nowhere must degrade to printing the
// entry, never to a dead end.
func TestTreeActionJumpFallsBackToView(t *testing.T) {
	labels := []string{TreeActJump, TreeActCopy, TreeActFork, TreeActBack}
	m := &app.Model{} // no transcript loaded: JumpToEntry reports false
	tree := &app.Dialog{Kind: "tree", Options: []string{"• user: hi"}}
	tree.Reindex()
	act := &app.Dialog{Kind: "treeAction", Options: labels,
		Payload: []string{"user · id e1 · 10:00\n\nhi"}, Paths: []string{"e1"}, TreeJump: []int{0}}
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog: the menu sits in front of the tree.
	m.Dialogs = append(m.Dialogs, act, tree)
	if _, cmd := confirmTreeAction(m, m.Dialogs[0], 0); cmd != nil {
		t.Error("the fallback is synchronous")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("both dialogs must close, got %d", len(m.Dialogs))
	}
	if m.BlockCount() != 1 {
		t.Errorf("blocks = %d, want the View-entry fallback block", m.BlockCount())
	}
}

// Fork goes through the same pipeline as /fork (one Pi.Fork round-trip, the
// branch text handed back) and closes both dialogs first.
func TestTreeActionForkUsesForkPipeline(t *testing.T) {
	m, log := opsModel(t, nil)
	labels := []string{TreeActJump, TreeActCopy, TreeActFork, TreeActBack}
	tree := &app.Dialog{Kind: "tree", Options: []string{"• user: hi"}}
	tree.Reindex()
	act := &app.Dialog{Kind: "treeAction", Options: labels,
		Payload: []string{"user · id e1 · 10:00\n\nhi"}, Paths: []string{"e1"}, TreeJump: []int{0}}
	act.Reindex()
	// Dialogs[0] is the ACTIVE dialog: the menu sits in front of the tree.
	m.Dialogs = append(m.Dialogs, act, tree)
	_, cmd := confirmTreeAction(m, m.Dialogs[0], 2)
	if cmd == nil {
		t.Fatal("fork must return a Cmd")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("both dialogs must close, got %d", len(m.Dialogs))
	}
	op, ok := cmd().(app.PiOpMsg)
	if !ok || op.Op != "fork" || op.Err != nil || !op.Reload {
		t.Fatalf("fork result = %#v", op)
	}
	if !strings.Contains(op.Text, "first question") {
		t.Errorf("fork must hand pi's branch text back, got %q", op.Text)
	}
	cmds := log()
	last := cmds[len(cmds)-1]
	if last["type"] != "fork" || last["entryId"] != "e1" {
		t.Errorf("last command = %v, want fork(e1)", last)
	}
}

// Out-of-range rows (stale cursor after the menu rebuilds) must leave the
// dialog stack exactly as it was.
func TestTreeActionIgnoresBadRow(t *testing.T) {
	for _, ri := range []int{-1, 3} {
		m := &app.Model{}
		act := &app.Dialog{Kind: "treeAction", Options: []string{TreeActBack}}
		act.Reindex()
		m.Dialogs = append(m.Dialogs, act)
		_, cmd := confirmTreeAction(m, m.Dialogs[0], ri)
		if cmd != nil {
			t.Errorf("row %d: want no cmd", ri)
		}
		if len(m.Dialogs) != 1 {
			t.Errorf("row %d: dialogs = %d, want 1", ri, len(m.Dialogs))
		}
	}
}

// The whole /tree → menu → back flow through the app's REAL Update/View
// path, not the confirmers directly. Dialogs[0] is the active dialog
// everywhere in the app (View, updateDialog, confirmDialog, dismissDialog),
// so a menu pushed at the end of the stack would render nothing, swallow no
// keys, and leak one dialog per Enter — all invisible to a test that only
// calls the confirmers by hand.
func TestTreeActionFlowThroughUpdateAndView(t *testing.T) {
	// A confirmer returns *app.Model, so Update can hand back either shape.
	asModel := func(nm tea.Model) app.Model {
		switch v := nm.(type) {
		case app.Model:
			return v
		case *app.Model:
			return *v
		}
		t.Fatalf("unexpected model type %T", nm)
		return app.Model{}
	}
	press := func(m app.Model, k tea.KeyType) app.Model {
		nm, _ := m.Update(tea.KeyMsg{Type: k})
		return asModel(nm)
	}
	m := app.New(nil, t.TempDir())
	m.UseBuiltins(nil, Confirmers())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = asModel(tm)
	tree := &app.Dialog{Kind: "tree", Title: "Tree (default)",
		Options: []string{"• user: hi", "• assistant: ok"},
		Descs:   []string{"user · 10:00", "assistant · 10:01"},
		Payload: []string{"hi", "ok"},
		Paths:   []string{"e1", "e2"}, TreeJump: []int{0, 1}, TreeRole: []string{"user", "assistant"}}
	tree.Reindex()
	m.Dialogs = append(m.Dialogs, tree)
	// pickRow puts the tree cursor on a row and presses Enter there.
	pickRow := func(m app.Model, row int) app.Model {
		m.Dialogs[0].Cursor = row
		return press(m, tea.KeyEnter)
	}
	// menuHas reports whether the rendered menu offers a label.
	menuHas := func(m app.Model, label string) bool {
		return strings.Contains(m.View(), label)
	}

	// 1. A user row: the menu lands IN FRONT of the tree and is on screen.
	m = pickRow(m, 0)
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "treeAction" || m.Dialogs[1].Kind != "tree" {
		t.Fatalf("Enter must put the menu in front, got %v", kinds(m))
	}
	for _, want := range []string{"Tree action", "Jump to message", "Copy entry"} {
		if !menuHas(m, want) {
			t.Fatalf("the menu is not on screen (%q missing):\n%s", want, m.View())
		}
	}
	// The entry id must travel tree → menu or the fork arm is unreachable:
	// a user row offers it, an assistant row cannot (pi's fork rejects it).
	if !menuHas(m, "Fork from here") {
		t.Fatalf("a user row must offer Fork from here (entry id lost):\n%s", m.View())
	}

	// 2. "Back to tree" (last row) closes the menu only, keeping the row.
	m.Dialogs[0].Cursor = len(m.Dialogs[0].FIdx) - 1
	m = press(m, tea.KeyEnter)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "tree" || m.Dialogs[0].Cursor != 0 {
		t.Fatalf("back to tree = %v, want one tree with row 0 selected", kinds(m))
	}

	// 3. An assistant row: same menu, no fork arm.
	m = pickRow(m, 1)
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "treeAction" {
		t.Fatalf("assistant row = %v, want the menu in front", kinds(m))
	}
	if menuHas(m, "Fork from here") {
		t.Fatalf("an assistant row must not offer Fork from here:\n%s", m.View())
	}

	// 4. Esc from the menu returns to the tree; Esc closes the tree. The
	//    stack must never grow: one Enter per row, one Esc per screen.
	m = press(m, tea.KeyEsc)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "tree" || m.Dialogs[0].Cursor != 1 {
		t.Fatalf("Esc from the menu = %v, want the tree with row 1 selected", kinds(m))
	}
	m = press(m, tea.KeyEsc)
	if len(m.Dialogs) != 0 {
		t.Fatalf("Esc on the tree must close it, got %v", kinds(m))
	}
}

func kinds(m app.Model) []string {
	out := make([]string, 0, len(m.Dialogs))
	for _, d := range m.Dialogs {
		out = append(out, d.Kind)
	}
	return out
}
