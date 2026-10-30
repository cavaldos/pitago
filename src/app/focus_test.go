package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

const focusDemo = "package demo\n\nfunc A() int {\n\treturn 1\n}\n\nfunc B() int {\n\treturn 2\n}\n"

// focusModel builds a workspace (not a git repo, so the tree reads disk):
//
//	pkg/util.go
//	demo.go
func focusModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "demo.go"), []byte(focusDemo), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pkg", "util.go"), []byte("package pkg\n\nfunc U() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New(nil, dir)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return tm.(Model)
}

func focusKeys(m Model, keys ...tea.KeyMsg) Model {
	for _, k := range keys {
		tm, _ := m.Update(k)
		m = tm.(Model)
	}
	return m
}

func focusMouse(m Model, mm tea.MouseMsg) Model {
	tm, _ := m.Update(mm)
	return tm.(Model)
}

func focusOf(m Model) *focusState { return m.Dialogs[0].focus }

var (
	kEnter = tea.KeyMsg{Type: tea.KeyEnter}
	kEsc   = tea.KeyMsg{Type: tea.KeyEsc}
	kTab   = tea.KeyMsg{Type: tea.KeyTab}
)

// The neo-tree flow: open on the tree, expand pkg/, open util.go, jump back
// to the tree, open demo.go — and each file keeps its own cursor.
func TestFocusTreeSwitchesFilesAndKeepsCursor(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("")
	fs := focusOf(m)
	if fs.pane != paneTree || fs.file != nil || !fs.showTree {
		t.Fatal("/focus with no file should start on the tree")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "▸ pkg/") || !strings.Contains(view, "demo.go") {
		t.Fatalf("tree not drawn:\n%s", view)
	}
	m = focusKeys(m, kEnter) // pkg/ (dirs first) opens
	if !strings.Contains(ansi.Strip(m.View()), "▾ pkg/") {
		t.Fatal("Enter on a dir should expand it")
	}
	m = focusKeys(m, runes("j"), kEnter) // util.go
	fs = focusOf(m)
	if fs.file == nil || fs.file.Path != "pkg/util.go" || fs.pane != paneCode {
		t.Fatalf("util.go not opened into the code pane: %+v", fs.file)
	}
	m = focusKeys(m, runes("G")) // cursor to util.go's last line
	m = focusKeys(m, runes("h"), runes("j"), kEnter)
	fs = focusOf(m)
	if fs.file.Path != "demo.go" {
		t.Fatalf("h → tree, j, Enter should open demo.go, got %s", fs.file.Path)
	}
	m = focusKeys(m, runes("j"), runes("j"), runes("j")) // demo.go line 4
	m = focusKeys(m, kTab)                               // back to the tree, cursor on demo.go
	if focusOf(m).pane != paneTree {
		t.Fatal("Tab should move to the tree")
	}
	m = focusKeys(m, runes("k"), kEnter) // util.go again
	if fs := focusOf(m); fs.file.Path != "pkg/util.go" || fs.cur != 2 {
		t.Fatalf("util.go should reopen at its last line (2), got %s:%d", fs.file.Path, fs.cur)
	}
	m = focusKeys(m, kTab, runes("j"), kEnter)
	if fs := focusOf(m); fs.file.Path != "demo.go" || fs.cur != 3 {
		t.Fatalf("demo.go should reopen at line index 3, got %d", fs.cur)
	}
}

// Ctrl+E hides the tree (code takes the full width) and brings it back.
func TestFocusCtrlETogglesTree(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("demo.go")
	if !focusOf(m).showTree {
		t.Fatal("tree should show on a wide terminal")
	}
	m = focusKeys(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if focusOf(m).showTree || strings.Contains(ansi.Strip(m.View()), "pkg/") {
		t.Fatal("Ctrl+E should hide the tree")
	}
	m = focusKeys(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if !focusOf(m).showTree {
		t.Fatal("second Ctrl+E should show it again")
	}
}

// Search, select lines 7-9, ask, send. With pi offline the prompt lands in
// the editor, so its text is checkable: reference + those lines only.
func TestFocusSearchSelectAskSendsOnlyTheRange(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("")
	m = focusKeys(m, runes("/"), runes("demo"), kEnter)
	fs := focusOf(m)
	if fs.file == nil || fs.file.Path != "demo.go" {
		t.Fatal("search + Enter did not open demo.go")
	}
	m = focusKeys(m, runes("G"), runes("v"), runes("k"), runes("k"))
	if r := focusOf(m).focusRange(); r.Start != 7 || r.End != 9 {
		t.Fatalf("selection = %+v, want 7-9", r)
	}
	m = focusKeys(m, kEnter, runes("rename B to Two"), kEnter)
	if len(m.Dialogs) != 0 {
		t.Fatal("send must close the window")
	}
	got := m.ta.Value()
	for _, want := range []string{"rename B to Two", "`demo.go:7-9`", "```go\nfunc B() int {\n", "return 2\n}\n```"} {
		if !strings.Contains(got, want) {
			t.Errorf("prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "func A") {
		t.Errorf("prompt leaked lines outside the selection:\n%s", got)
	}
}

// /focus path:start-end opens straight on the range; i drops the excerpt
// into the editor; a bare /focus reopens the same file and selection.
func TestFocusTargetInsertAndReopen(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("demo.go:3-5")
	fs := focusOf(m)
	if fs.pane != paneCode {
		t.Fatal("a path argument should open the code pane")
	}
	if r := fs.focusRange(); r.Start != 3 || r.End != 5 {
		t.Fatalf("range = %+v, want 3-5", r)
	}
	m = focusKeys(m, runes("i"))
	if len(m.Dialogs) != 0 {
		t.Fatal("i must close the window")
	}
	if v := m.ta.Value(); !strings.Contains(v, "`demo.go:3-5`") || !strings.Contains(v, "func A() int {") {
		t.Fatalf("editor = %q", v)
	}
	m.OpenFocus("")
	if fs := focusOf(m); fs.file == nil || fs.focusRange().Start != 3 || fs.focusRange().End != 5 {
		t.Fatal("bare /focus should reopen the last file and selection")
	}
}

// Esc unwinds: search → back; ask → code; selection cleared; then close.
func TestFocusEscUnwinds(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("demo.go")
	m = focusKeys(m, runes("/"), kEsc)
	if fs := focusOf(m); fs.stage != focusCode || fs.file == nil {
		t.Fatal("Esc in search should return to the open file")
	}
	m = focusKeys(m, runes("v"), kEnter, kEsc)
	if focusOf(m).stage != focusCode {
		t.Fatal("Esc in the ask box should return to the code")
	}
	m = focusKeys(m, kEsc)
	if len(m.Dialogs) != 1 || focusOf(m).anchor != -1 {
		t.Fatal("Esc with a selection should only clear it")
	}
	m = focusKeys(m, kEsc)
	if len(m.Dialogs) != 0 {
		t.Fatal("Esc in the code pane should close the window")
	}
}

// screenPos finds text on the rendered screen: (column, row) in cells.
func screenPos(t *testing.T, m Model, text string) (int, int) {
	t.Helper()
	for y, ln := range strings.Split(ansi.Strip(m.View()), "\n") {
		if i := strings.Index(ln, text); i >= 0 {
			return ansi.StringWidth(ln[:i]), y
		}
	}
	t.Fatalf("%q not on screen:\n%s", text, ansi.Strip(m.View()))
	return 0, 0
}

// Mouse, hit-tested against what is actually drawn: a click on a tree file
// opens it; a drag across code lines selects them.
func TestFocusMouseClickTreeAndDragSelect(t *testing.T) {
	m := focusModel(t)
	m.OpenFocus("")
	x, y := screenPos(t, m, "demo.go")
	m = focusMouse(m, tea.MouseMsg{X: x + 1, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if fs := focusOf(m); fs.file == nil || fs.file.Path != "demo.go" {
		t.Fatal("clicking demo.go in the tree should open it")
	}
	x3, y3 := screenPos(t, m, "func A() int {")
	_, y5 := screenPos(t, m, "5 │ }")
	m = focusMouse(m, tea.MouseMsg{X: x3, Y: y3, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	m = focusMouse(m, tea.MouseMsg{X: x3, Y: y5, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft})
	m = focusMouse(m, tea.MouseMsg{X: x3, Y: y5, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft})
	if r := focusOf(m).focusRange(); r.Start != 3 || r.End != 5 {
		t.Fatalf("drag from line 3 to 5 selected %+v", r)
	}
}
