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

// With native menus, right-click on an assistant block asks the runtime for
// a menu at the pointer instead of opening the full-screen dialog.
func TestRightClickRequestsNativeMenu(t *testing.T) {
	withNativeMenus(t)
	m := blockMenuModel(t)
	tm, cmd := m.Update(tea.MouseMsg{X: 7, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if got := tm.(Model); len(got.Dialogs) != 0 {
		t.Fatalf("dialog opened: %+v", got.Dialogs)
	}
	if cmd == nil {
		t.Fatal("no menu cmd")
	}
	om, ok := cmd().(tea.OpenMenuMsg)
	if !ok || om.Tag != "block:0" || om.X != 7 || om.Y != 1 {
		t.Fatalf("menu msg = %#v", om)
	}
	var ids []string
	for _, it := range om.Items {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "md,code,plain" {
		t.Fatalf("items = %v", ids)
	}
}

// Picking an item runs the same copy as the dialog would.
func TestMenuResultCopiesBlock(t *testing.T) {
	var copied string
	defer stubClipboard(func(text string) clipboard.Status {
		copied = text
		return clipboard.Status{Channel: clipboard.Atoto, Bytes: len(text), Chars: len([]rune(text))}
	})()
	m := blockMenuModel(t)
	m.Update(tea.MenuResultMsg{Tag: "block:0", ID: "code"})
	if copied != "```go\nx := 1\n```" { // same as the dialog: fences kept
		t.Fatalf("copied %q", copied)
	}
	// A stale index (block gone) must not panic.
	m.Update(tea.MenuResultMsg{Tag: "block:9", ID: "md"})
}

// The input's own menu: copy/clear are disabled on an empty input, and
// clear empties it.
func TestInputMenu(t *testing.T) {
	withNativeMenus(t)
	m := New(nil, t.TempDir())
	m.Mouse = true
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 30})
	m = tm.(Model)
	items := m.inputMenu()
	if !items[1].Disabled || !items[3].Disabled {
		t.Fatal("copy/clear must be disabled on an empty input")
	}
	m.ta.SetValue("draft")
	y := m.winH - 3 // inside the input box
	if !m.inputAt(5, y) || m.inputAt(5, 2) || m.inputAt(m.winW-2, y) {
		t.Fatalf("inputAt geometry wrong (y=%d)", y)
	}
	_, cmd := m.Update(tea.MouseMsg{X: 5, Y: y, Action: tea.MouseActionPress, Button: tea.MouseButtonRight})
	if om, ok := cmd().(tea.OpenMenuMsg); !ok || om.Tag != "input" {
		t.Fatalf("want input menu, got %#v", cmd())
	}
	tm, _ = m.Update(tea.MenuResultMsg{Tag: "input", ID: "clear"})
	if v := tm.(Model).ta.Value(); v != "" {
		t.Fatalf("clear left %q", v)
	}
}
