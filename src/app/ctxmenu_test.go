package app

import (
	"strings"
	"testing"

	"pitago/src/components/clipboard"
	tea "pitago/src/tui/ev"
)

func withNativeMenus(t *testing.T) {
	t.Helper()
	NativeMenus = true
	t.Cleanup(func() { NativeMenus = false })
}

func blockMenuModel(t *testing.T) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.Mouse = true
	m.winW, m.winH = 100, 40
	m.vp.Width, m.vp.Height = 40, 5
	m.blocks = []Block{{Kind: "assistant", Text: "# hello\n\n```go\nx := 1\n```"}}
	m.renderBlocks()
	m.vp.SetContent(strings.Join(m.chatLines, "\n"))
	return m
}

func menuIDs(items []tea.MenuItem) string {
	var ids []string
	for _, it := range items {
		if !it.Separator {
			ids = append(ids, it.ID)
		}
	}
	return strings.Join(ids, ",")
}

// With native menus, right-click on an assistant block asks the runtime for
// the context menu at the pointer: the block's copy actions first, then the
// common groups. No full-screen dialog opens.
func TestRightClickRequestsNativeMenu(t *testing.T) {
	withNativeMenus(t)
	m := blockMenuModel(t)
	tm, cmd := m.Update(tea.MouseMsg{X: 7, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if got := tm.(Model); len(got.Dialogs) != 0 {
		t.Fatalf("dialog opened: %+v", got.Dialogs)
	}
	om, ok := cmd().(tea.OpenMenuMsg)
	if !ok || om.Tag != "ctx:0" || om.X != 7 || om.Y != 1 {
		t.Fatalf("menu msg = %#v", om)
	}
	if ids := menuIDs(om.Items); !strings.HasPrefix(ids, "copy:md,copy:code,copy:plain,key:ctrl+y") {
		t.Fatalf("items = %s", ids)
	}
}

// Right-click on an empty chat row still opens the common menu (ttt: the
// menu is available anywhere), without block copy items.
func TestRightClickAnywhere(t *testing.T) {
	withNativeMenus(t)
	m := blockMenuModel(t)
	_, cmd := m.Update(tea.MouseMsg{X: 7, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	om := cmd().(tea.OpenMenuMsg)
	if ids := menuIDs(om.Items); strings.Contains(ids, "copy:") || !strings.Contains(ids, "cmd:/model") {
		t.Fatalf("tag %s items = %s", om.Tag, ids)
	}
}

// Picking a copy item runs the same copy as the dialog would, on the block
// the menu was opened over.
func TestMenuResultCopiesBlock(t *testing.T) {
	var copied string
	defer stubClipboard(func(text string) clipboard.Status {
		copied = text
		return clipboard.Status{Channel: clipboard.Atoto, Bytes: len(text), Chars: len([]rune(text))}
	})()
	m := blockMenuModel(t)
	m.Update(tea.MenuResultMsg{Tag: "ctx:0", ID: "copy:code"})
	if copied != "```go\nx := 1\n```" { // same as the dialog: fences kept
		t.Fatalf("copied %q", copied)
	}
	// A stale index (block gone) must not panic.
	m.Update(tea.MenuResultMsg{Tag: "ctx:9", ID: "copy:md"})
}

// Over the input the menu leads with the editor's own actions; clear empties
// the input.
func TestInputContextMenu(t *testing.T) {
	withNativeMenus(t)
	m := New(nil, t.TempDir())
	m.Mouse = true
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	y := m.winH - 3 // inside the input box
	if !m.inputAt(5, y) || m.inputAt(5, 2) || m.inputAt(m.winW-2, y) {
		t.Fatalf("inputAt geometry wrong (y=%d)", y)
	}
	items := m.contextMenu(5, y)
	if items[1].ID != "act:copy-input" || !items[1].Disabled {
		t.Fatalf("empty input: %+v", items[:3])
	}
	m.ta.SetValue("draft")
	if m.contextMenu(5, y)[2].Disabled {
		t.Fatal("clear must be enabled with text")
	}
	tm, _ = m.Update(tea.MenuResultMsg{Tag: "ctx:-1", ID: "act:clear-input"})
	if v := tm.(Model).ta.Value(); v != "" {
		t.Fatalf("clear left %q", v)
	}
}

// Menu bar: hidden without native menus; key: items replay the bound key;
// toggles show their state.
func TestMenuBar(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	if m.MenuBar() != nil {
		t.Fatal("no menu bar without native menus")
	}
	withNativeMenus(t)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	bar := m.MenuBar()
	var titles []string
	for _, mm := range bar {
		titles = append(titles, mm.Title)
	}
	if strings.Join(titles, " ") != "File Edit Selection View Options Help" {
		t.Fatalf("titles = %v", titles)
	}
	side := func(m Model) string {
		for _, it := range m.MenuBar()[3].Items {
			if it.ID == "key:ctrl+e" {
				return it.Label
			}
		}
		return ""
	}
	if side(m) != "✓ Sidebar" {
		t.Fatalf("sidebar label %q", side(m))
	}
	tm, _ = m.Update(tea.MenuResultMsg{Tag: tea.MenuBarTag, ID: "key:ctrl+e"})
	m = tm.(Model)
	if m.showSide() || side(m) != "  Sidebar" {
		t.Fatal("View → Sidebar must toggle it like Ctrl+E")
	}
	if _, cmd := m.Update(tea.MenuResultMsg{Tag: tea.MenuBarTag, ID: "act:quit"}); cmd == nil {
		t.Fatal("quit")
	} else if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("File → Quit must quit")
	}
}

// Every key: item in both menus maps to a key the app binds.
func TestMenuKeysResolve(t *testing.T) {
	withNativeMenus(t)
	m := blockMenuModel(t)
	var walk func([]tea.MenuItem)
	walk = func(items []tea.MenuItem) {
		for _, it := range items {
			if k, ok := strings.CutPrefix(it.ID, "key:"); ok {
				if _, ok := keyMsgFor(k); !ok {
					t.Errorf("unmapped key item %q", it.ID)
				}
			}
			walk(it.Submenu)
		}
	}
	for _, mm := range m.MenuBar() {
		walk(mm.Items)
	}
	walk(m.contextMenu(7, 1))
}
