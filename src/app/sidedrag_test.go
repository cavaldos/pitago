package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"

	tea "pitago/src/tui/ev"
)

func dragModel(t *testing.T) Model {
	t.Helper()
	m := tallModel(t)
	m.Mouse = true
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	return tm.(Model)
}

func mouse(m Model, x, y int, a tea.MouseAction, b tea.MouseButton) Model {
	tm, _ := m.Update(tea.MouseMsg{X: x, Y: y, Action: a, Button: b})
	return tm.(Model)
}

// Dragging the sidebar's left border resizes it live, the frame stays exactly
// winW wide on every row, and the width is persisted on release.
func TestSideDividerDragResizes(t *testing.T) {
	m := dragModel(t)
	div := m.mainW()
	if div != 120-defaultSideW {
		t.Fatalf("divider at %d", div)
	}
	m = mouse(m, div, 5, tea.MouseActionPress, tea.MouseButtonLeft)
	if !m.sideDrag {
		t.Fatal("press on the divider must start a drag")
	}
	m = mouse(m, 70, 6, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.sideW() != 50 || m.mainW() != 70 || m.vp.Width != 70 {
		t.Fatalf("mid-drag side=%d main=%d vp=%d", m.sideW(), m.mainW(), m.vp.Width)
	}
	for i, ln := range strings.Split(m.View(), "\n") {
		if w := lipgloss.Width(ln); w != 120 {
			t.Fatalf("row %d is %d cells after resize", i, w)
		}
	}
	m = mouse(m, 66, 6, tea.MouseActionRelease, tea.MouseButtonNone)
	if m.sideDrag || m.sideW() != 54 {
		t.Fatalf("after release drag=%v side=%d", m.sideDrag, m.sideW())
	}
	if got := LoadPrefs(m.prefsPath).SideWidth; got != 54 {
		t.Fatalf("persisted width = %d", got)
	}
}

// The width is clamped so the chat keeps minChatW and the sidebar minSideW,
// and a narrower terminal re-clamps a saved width.
func TestSideDividerClamps(t *testing.T) {
	m := dragModel(t)
	m = mouse(m, m.mainW(), 3, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouse(m, 2, 3, tea.MouseActionMotion, tea.MouseButtonLeft)
	if m.mainW() != minChatW {
		t.Fatalf("chat shrank to %d", m.mainW())
	}
	m = mouse(m, 119, 3, tea.MouseActionRelease, tea.MouseButtonNone)
	if m.sideW() != minSideW {
		t.Fatalf("sidebar shrank to %d", m.sideW())
	}
	m.SideWidth = 70
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m = tm.(Model)
	if m.sideW() != 90-minChatW {
		t.Fatalf("saved width not re-clamped: %d", m.sideW())
	}
}

// A press anywhere else is not a drag; a press on the divider while a
// dialog is open belongs to the dialog.
func TestSideDividerOnlyOnBorder(t *testing.T) {
	m := dragModel(t)
	if m2 := mouse(m, m.mainW()-1, 5, tea.MouseActionPress, tea.MouseButtonLeft); m2.sideDrag {
		t.Fatal("chat press started a drag")
	}
	if m2 := mouse(m, m.mainW(), 0, tea.MouseActionPress, tea.MouseButtonLeft); m2.sideDrag {
		t.Fatal("header row is not the divider")
	}
	m.Dialogs = []*Dialog{{Kind: "confirm"}}
	if m.onSideDivider(m.mainW(), 5) {
		t.Fatal("divider must yield to an open dialog")
	}
	m.Dialogs = nil
	m.Mouse = false
	if m.onSideDivider(m.mainW(), 5) {
		t.Fatal("--mouse=false disables the divider")
	}
}

// The scroll filter must forward motion while dragging, or the sidebar would
// only jump on release.
func TestSideDragMotionPassesFilter(t *testing.T) {
	m := dragModel(t)
	m.sideDrag = true
	if ScrollEventFilter(m, tea.MouseMsg{X: 50, Y: 4, Action: tea.MouseActionMotion, Button: tea.MouseButtonLeft}) == nil {
		t.Fatal("filter dropped drag motion")
	}
}

func TestSideDividerDoubleClickResets(t *testing.T) {
	m := dragModel(t)
	m = mouse(m, m.mainW(), 5, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouse(m, 60, 5, tea.MouseActionRelease, tea.MouseButtonNone)
	if m.sideW() != 60 {
		t.Fatalf("drag: %d", m.sideW())
	}
	m.sideDividerPress = time.Time{}
	m = mouse(m, m.mainW(), 5, tea.MouseActionPress, tea.MouseButtonLeft)
	m = mouse(m, m.mainW(), 5, tea.MouseActionRelease, tea.MouseButtonNone)
	m = mouse(m, m.mainW(), 5, tea.MouseActionPress, tea.MouseButtonLeft)
	if m.sideW() != defaultSideW || m.sideDrag {
		t.Fatalf("double-click: side=%d drag=%v", m.sideW(), m.sideDrag)
	}
	if d := m.Dividers(); len(d) != 1 || d[0].X != m.mainW() {
		t.Fatalf("dividers %+v", d)
	}
	if k, _, _ := m.Tooltip(m.mainW(), 5); k != "divider" {
		t.Fatal("divider tooltip")
	}
}
