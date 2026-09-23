package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestSidebarHelpers(t *testing.T) {
	if got := fmtDur(157 * time.Second); got != "2m37s" {
		t.Fatalf("fmtDur 157s = %q", got)
	}
	if got := fmtDur(5 * time.Second); got != "5s" {
		t.Fatalf("fmtDur 5s = %q", got)
	}
	if got := ctxBar(50, 10); got != "█████░░░░░" {
		t.Fatalf("ctxBar 50%% = %q", got)
	}
	row := twoCol("time 2m37s", "in 17k", 30)
	if w := lipgloss.Width(row); w != 17+len("in 17k") {
		t.Fatalf("twoCol width = %d: %q", w, row)
	}
}

// tallModel builds a sidebar whose content overflows a short terminal:
// 5 recents, 2 MCP servers, 12 todos (capped view), 5 workspace files.
func tallModel(t *testing.T) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "a"
	for _, id := range []string{"a", "b", "c", "d", "e"} {
		m.recentModels = append(m.recentModels, RecentModel{ID: id})
	}
	m.MCP = []McpServer{{Name: "alpha", Connected: true, Direct: 1, Total: 2}, {Name: "beta"}}
	for i := 0; i < 12; i++ {
		m.Todos = append(m.Todos, TodoItem{ID: string(rune('a' + i)), Content: "task", Status: TodoPending})
	}
	m.ws = wsData{ok: true, branch: "main", files: []wsFile{
		{"a.go", 1, 0}, {"b.go", 2, 1}, {"c.go", 3, 0}, {"d.go", 0, 4}, {"e.go", 5, 5},
	}, more: 30, untracked: 2}
	// MCP + Plugins + Commands hide by default: opt the fixture in so the tall
	// sidebar exercises every section.
	m.Side = map[string]bool{SidePlugins: true, SideMCP: true, SideCommands: true}
	m.Plugins = []Plugin{
		{Spec: "npm:pi-lens", Name: "pi-lens"},
		{Spec: "npm:pi-foo", Name: "pi-foo"},
		{Spec: "git:github.com/x/y", Name: "github.com/x/y"},
	}
	return m
}

// Regression (screenshot): a tall sidebar must clip to its box — the whole
// frame stays exactly winH rows so it never pushes the chat input up.
func TestSidebarFitsHeight(t *testing.T) {
	m := tallModel(t)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 24 {
		t.Fatalf("frame is %d rows, want 24", len(lines))
	}
	box := strings.Split(stripANSI(m.renderSidebar()), "\n")
	if len(box) != m.sideH() {
		t.Fatalf("sidebar box is %d rows, want %d", len(box), m.sideH())
	}
}

func wheelAt(x, y int, down bool) tea.MouseMsg {
	b := tea.MouseButtonWheelUp
	if down {
		b = tea.MouseButtonWheelDown
	}
	return tea.MouseMsg{X: x, Y: y, Action: tea.MouseActionPress, Button: b}
}

// Ctrl+↓ scrolls the sidebar without a mouse (Alt+↓ keeps working);
// neither may move the chat viewport.
func TestSidebarCtrlScroll(t *testing.T) {
	m := tallModel(t)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlDown})
	m = tm.(Model)
	if m.sideVp.YOffset <= 0 {
		t.Fatal("Ctrl+Down did not scroll the sidebar")
	}
	if m.vp.YOffset != 0 {
		t.Fatal("sidebar Ctrl+Down must not scroll the chat")
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlUp})
	m = tm.(Model)
	if m.sideVp.YOffset != 0 {
		t.Fatalf("Ctrl+Up did not scroll back, offset=%d", m.sideVp.YOffset)
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown, Alt: true})
	m = tm.(Model)
	if m.sideVp.YOffset <= 0 {
		t.Fatal("Alt+Down did not scroll the sidebar")
	}
}

// Clicking the PLUGINS header collapses/expands the list; the row above
// (COMMANDS counts) and below (separator) must not toggle. The mapping
// stays correct when the sidebar is scrolled.
func TestPluginHeaderToggleClick(t *testing.T) {
	m := tallModel(t)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m = tm.(Model)
	sx := m.mainW() + 5
	y := 1 + m.pluginHeaderRow()
	if !m.pluginToggleAt(sx, y) {
		t.Fatalf("header row %d not hit at y=%d", m.pluginHeaderRow(), y)
	}
	if m.pluginToggleAt(sx, y-1) {
		t.Fatal("row above the header must not toggle")
	}
	if m.pluginToggleAt(sx, y+1) {
		t.Fatal("row below the header must not toggle")
	}
	if m.pluginToggleAt(10, y) {
		t.Fatal("chat column must not toggle")
	}
	// scrolled: same content row maps to a smaller screen y
	m.sideVp.SetYOffset(2)
	if !m.pluginToggleAt(sx, y-2) {
		t.Fatal("header not hit after scrolling")
	}
	// end-to-end through the mouse handler. Real terminals report release
	// with Button None (SGR `m` / X10 code 3 carry no button), so the
	// handler must not require Left — that was the dead-click bug.
	m2 := tallModel(t)
	tm, _ = m2.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	m2 = tm.(Model)
	rel := tea.MouseMsg{X: sx, Y: 1 + m2.pluginHeaderRow(), Action: tea.MouseActionRelease, Button: tea.MouseButtonNone}
	tm, _ = m2.Update(rel)
	m2 = tm.(Model)
	if m2.showPlugins {
		t.Fatal("clicking the header must collapse the list")
	}
	tm, _ = m2.Update(rel)
	m2 = tm.(Model)
	if !m2.showPlugins {
		t.Fatal("clicking the header again must expand the list")
	}
	// press alone (no release) must not toggle
	press := tea.MouseMsg{X: sx, Y: 1 + m2.pluginHeaderRow(), Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	tm, _ = m2.Update(press)
	m2 = tm.(Model)
	if !m2.showPlugins {
		t.Fatal("press without release must not toggle the list")
	}
}

// /mouse flips capture at runtime: the flag follows, and a toast popup
// explains the new mode (not ready here, so no tea command is returned).
func TestToggleMouse(t *testing.T) {
	m := New(nil, t.TempDir())
	if m.Mouse {
		t.Fatal("mouse must start off in tests (flag default is applied in main)")
	}
	if cmd := m.ToggleMouse(""); cmd != nil || !m.Mouse {
		t.Fatal("bare /mouse must turn capture on")
	}
	if cmd := m.ToggleMouse("off"); cmd != nil || m.Mouse {
		t.Fatal("/mouse off must turn capture off")
	}
	if cmd := m.ToggleMouse("on"); cmd != nil || !m.Mouse {
		t.Fatal("/mouse on must turn capture on")
	}
	if len(m.toasts) != 3 {
		t.Fatalf("each toggle must log a toast, got %d toasts", len(m.toasts))
	}
}

// Wheel over the sidebar scrolls the sidebar, not the chat; wheel over the
// chat leaves the sidebar alone.
func TestSidebarWheelScroll(t *testing.T) {
	m := tallModel(t)
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	if m.sideVp.YOffset != 0 {
		t.Fatalf("start offset = %d, want 0", m.sideVp.YOffset)
	}
	sx := m.mainW() + 5
	tm, _ = m.Update(wheelAt(sx, 10, true))
	m = tm.(Model)
	if m.sideVp.YOffset <= 0 {
		t.Fatal("wheel over sidebar did not scroll it")
	}
	if m.vp.YOffset != 0 {
		t.Fatal("sidebar wheel must not scroll the chat")
	}
	off := m.sideVp.YOffset
	tm, _ = m.Update(wheelAt(10, 10, true)) // over the chat
	m = tm.(Model)
	if m.sideVp.YOffset != off {
		t.Fatal("chat wheel must not scroll the sidebar")
	}
}
