package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func filterKeeps(t *testing.T, m Model, msg tea.Msg) bool {
	t.Helper()
	return ScrollEventFilter(m, msg) != nil
}

// sideModel is scrollModel with the sidebar visible, so overSide() routes on
// column the way it does in a real session.
func sideModel(t testing.TB) Model {
	t.Helper()
	m := scrollModel(t, 400)
	m.hideSide = false
	m.Refresh()
	return m
}

// Every drop the filter makes must leave the model bit-identical, because
// Bubble Tea skips Update entirely when a filter returns nil.
func TestFilterDropsAreExactNoOps(t *testing.T) {
	t.Run("chat wheel up at the top", func(t *testing.T) {
		m := scrollModel(t, 400)
		m.vp.GotoTop()
		ev := wheelAt(10, 10, false)
		if filterKeeps(t, m, ev) {
			t.Fatal("wheel up at the top should be dropped")
		}
		tm, _ := m.Update(ev)
		if got := tm.(Model).vp.YOffset; got != 0 {
			t.Fatalf("Update moved a dropped wheel: %d, want 0", got)
		}
	})

	t.Run("chat wheel down at the bottom", func(t *testing.T) {
		m := scrollModel(t, 400)
		m.vp.GotoBottom()
		ev := wheelAt(10, 10, true)
		if filterKeeps(t, m, ev) {
			t.Fatal("wheel down at the bottom should be dropped")
		}
		tm, _ := m.Update(ev)
		if got := tm.(Model).vp.YOffset; got != m.vp.YOffset {
			t.Fatalf("Update moved a dropped wheel: %d -> %d", m.vp.YOffset, got)
		}
	})

	t.Run("motion", func(t *testing.T) {
		m := scrollModel(t, 400)
		ev := tea.MouseMsg{X: 10, Y: 10, Action: tea.MouseActionMotion}
		if filterKeeps(t, m, ev) {
			t.Fatal("motion should be dropped")
		}
	})
	t.Run("sidebar wheel at its edge", func(t *testing.T) {
		m := sideModel(t)
		m.sideVp.SetYOffset(0)
		x := m.mainW() + 5
		up := wheelAt(x, 10, false)
		if !m.overSide(x) {
			t.Fatalf("x=%d does not hover the sidebar", x)
		}
		if filterKeeps(t, m, up) {
			t.Fatal("sidebar wheel up at its top should be dropped")
		}
		m.sideVp.GotoBottom()
		down := wheelAt(x, 10, true)
		if filterKeeps(t, m, down) {
			t.Fatal("sidebar wheel down at its bottom should be dropped")
		}
		// The chat must be untouched by the sidebar's edge state.
		chatOff := m.vp.YOffset
		tm, _ := m.Update(down)
		if got := tm.(Model).vp.YOffset; got != chatOff {
			t.Fatalf("sidebar wheel moved the chat offset: %d -> %d", chatOff, got)
		}
	})
}

// Mid-scroll and shifted/horizontal wheels must survive the filter: those
// genuinely move, or mean something other than a vertical scroll.
func TestFilterKeepsLiveScroll(t *testing.T) {
	m := scrollModel(t, 400)
	m.vp.GotoTop()
	if !filterKeeps(t, m, wheelAt(10, 10, true)) {
		t.Fatal("wheel down mid-scroll must pass the filter")
	}
	m.vp.SetYOffset(m.vp.YOffset + 10)
	if !filterKeeps(t, m, wheelAt(10, 10, false)) {
		t.Fatal("wheel up mid-scroll must pass the filter")
	}

	m.vp.GotoTop()
	shift := wheelAt(10, 10, false)
	shift.Shift = true
	if !filterKeeps(t, m, shift) {
		t.Fatal("shift+wheel must pass the filter even at the top")
	}

	side := sideModel(t)
	if !filterKeeps(t, side, wheelAt(side.mainW()+5, 10, true)) {
		t.Fatal("sidebar wheel must pass the filter")
	}
}

// A dialog captures the mouse before the viewports see it, so nothing may be
// dropped while one is open — even when the chat is pinned at an edge.
func TestFilterKeepsEverythingBehindDialog(t *testing.T) {
	m := scrollModel(t, 400)
	m.vp.GotoTop()
	m.Dialogs = []*Dialog{{Kind: "trajectory"}}
	if !filterKeeps(t, m, wheelAt(10, 10, false)) {
		t.Fatal("wheel behind a dialog must reach the dialog, not be dropped")
	}
	if !filterKeeps(t, m, tea.MouseMsg{X: 10, Y: 10, Action: tea.MouseActionMotion}) {
		t.Fatal("motion must pass while a dialog is open")
	}
}

// Motion is never bound, so it is always dropped; every other message type
// must reach Update untouched.
func TestFilterPassesOtherMessages(t *testing.T) {
	m := scrollModel(t, 400)
	m.vp.GotoTop()
	for _, msg := range []tea.Msg{
		tea.KeyMsg{Type: tea.KeyEnter},
		tea.WindowSizeMsg{Width: 120, Height: 30},
		// A click release is the sidebar's click target.
		tea.MouseMsg{X: 10, Y: 5, Action: tea.MouseActionRelease, Button: tea.MouseButtonLeft},
	} {
		if !filterKeeps(t, m, msg) {
			t.Fatalf("%T must pass the filter", msg)
		}
	}
}

// A chat drag selection reads motion (press → motion* → release), so motion
// must pass while one is active — otherwise drags can never extend.
func TestFilterKeepsMotionDuringDrag(t *testing.T) {
	m := scrollModel(t, 400)
	m.vp.GotoTop()
	ev := tea.MouseMsg{X: 10, Y: 10, Action: tea.MouseActionMotion}
	if filterKeeps(t, m, ev) {
		t.Fatal("idle motion should still be dropped")
	}
	m.sel = Selection{Active: true, Anchor: Point{Line: 0, Col: 0}, Focus: Point{Line: 0, Col: 0}}
	if !filterKeeps(t, m, ev) {
		t.Fatal("motion must pass while a drag selection is active")
	}
}
