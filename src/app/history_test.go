package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func newHistModel(hist []string) Model {
	m := Model{histIdx: -1, Mouse: true} // production default is --mouse=true
	m.ta = textarea.New()
	m.ta.Focus()
	m.ta.SetValue("")
	m.hist = append([]string(nil), hist...)
	return m
}

func TestHistPrevNext(t *testing.T) {
	m := newHistModel([]string{"m1", "m2", "m3"})

	if !m.tryHistPrev() || m.ta.Value() != "m3" {
		t.Fatalf("Up from empty should recall m3, got %q", m.ta.Value())
	}
	if !m.tryHistPrev() || m.ta.Value() != "m2" {
		t.Fatalf("Up should recall m2, got %q", m.ta.Value())
	}
	if !m.tryHistPrev() || m.ta.Value() != "m1" {
		t.Fatalf("Up should recall m1, got %q", m.ta.Value())
	}
	// at oldest: stays, still consumed
	if !m.tryHistPrev() || m.ta.Value() != "m1" {
		t.Fatalf("Up at oldest should stay m1, got %q", m.ta.Value())
	}
	if !m.tryHistNext() || m.ta.Value() != "m2" {
		t.Fatalf("Down should go m2, got %q", m.ta.Value())
	}
	if !m.tryHistNext() || m.ta.Value() != "m3" {
		t.Fatalf("Down should go m3, got %q", m.ta.Value())
	}
	if !m.tryHistNext() || m.ta.Value() != "" || m.histBrowsing() {
		t.Fatalf("Down past newest should return to empty, got %q browsing=%v", m.ta.Value(), m.histBrowsing())
	}
}

func TestHistDownFromEmpty(t *testing.T) {
	m := newHistModel([]string{"m1", "m2"})
	if !m.tryHistNext() || m.ta.Value() != "m2" {
		t.Fatalf("Down from empty should recall newest, got %q", m.ta.Value())
	}
}

func TestHistBlockedWhenTyping(t *testing.T) {
	m := newHistModel([]string{"m1"})
	m.ta.SetValue("hello")
	if m.tryHistPrev() {
		t.Fatal("Up with non-empty input must not recall (chat scrolls)")
	}
	m.ta.SetValue("a\nb")
	if m.tryHistPrev() {
		t.Fatal("Up with multiline input must not recall")
	}
}

func TestHistEmptyNoCrash(t *testing.T) {
	m := newHistModel(nil)
	if m.tryHistPrev() || m.tryHistNext() {
		t.Fatal("empty history must not consume Up/Down")
	}
}

func TestHistPushDedupe(t *testing.T) {
	m := newHistModel(nil)
	m.pushHist("hi")
	m.pushHist("hi")
	if len(m.hist) != 1 {
		t.Fatalf("consecutive dupes should dedupe, got %v", m.hist)
	}
	m.pushHist("  ")
	if len(m.hist) != 1 {
		t.Fatalf("blank should not push, got %v", m.hist)
	}
	m.pushHist("/model")
	m.pushHist("  /login  ")
	if len(m.hist) != 1 {
		t.Fatalf("slash commands must not enter history, got %v", m.hist)
	}
}

func TestHistUpViaUpdate(t *testing.T) {
	m := newHistModel([]string{"m1", "m2"})
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := tm.(Model)
	if got.ta.Value() != "m2" {
		t.Fatalf("Update Up should recall m2, got %q", got.ta.Value())
	}
	tm2, _ := got.Update(tea.KeyMsg{Type: tea.KeyDown})
	got2 := tm2.(Model)
	if got2.ta.Value() != "" {
		t.Fatalf("Update Down past newest should clear, got %q", got2.ta.Value())
	}
}

func TestHistEditExitsBrowse(t *testing.T) {
	m := newHistModel([]string{"m1", "m2"})
	m.tryHistPrev() // browsing m2
	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")})
	got := tm.(Model)
	if got.histBrowsing() {
		t.Fatal("typing after recall must exit browse mode")
	}
}

func TestWheelNeverTouchesHistory(t *testing.T) {
	m := newHistModel([]string{"m1", "m2"})
	wheel := func(down bool) tea.MouseMsg {
		b := tea.MouseButtonWheelUp
		if down {
			b = tea.MouseButtonWheelDown
		}
		return tea.MouseMsg{X: 5, Y: 5, Action: tea.MouseActionPress, Button: b}
	}
	// Empty input: wheel must not recall (arrows do).
	tm, _ := m.Update(wheel(false))
	if got := tm.(Model); got.ta.Value() != "" || got.histBrowsing() {
		t.Fatalf("wheel from empty must not recall, got %q browsing=%v", got.ta.Value(), got.histBrowsing())
	}
	// While browsing: wheel keeps the recalled input.
	m.tryHistPrev() // browsing m2
	tm, _ = m.Update(wheel(true))
	if got := tm.(Model); got.ta.Value() != "m2" || !got.histBrowsing() {
		t.Fatalf("wheel while browsing must keep m2, got %q browsing=%v", got.ta.Value(), got.histBrowsing())
	}
}

// With mouse off the terminal turns wheel scrolls into plain ↑↓: they must
// scroll the chat, never rewrite the input — in both empty and browsing
// states. Shift+↑↓ stays the explicit wheel-proof recall.
func TestMouseOffArrowsScrollNeverRecall(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = false
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	m.pushHist("m1")
	m.pushHist("m2")
	for i := 0; i < 30; i++ {
		m.AddBlock(Block{Kind: "user", Text: "line " + strings.Repeat("x", 40)})
	}
	m.RefreshFollow()
	top := m.vp.YOffset // at bottom after follow
	if top <= 0 {
		t.Fatal("need tall content for the scroll check")
	}
	// Empty input: plain ↑ scrolls, does not recall.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	got := tm.(Model)
	if got.ta.Value() != "" || got.histBrowsing() {
		t.Fatalf("mouse-off Up must not recall, got %q browsing=%v", got.ta.Value(), got.histBrowsing())
	}
	if got.vp.YOffset >= top {
		t.Fatal("mouse-off Up should scroll the chat")
	}
	m = got
	// Empty input: plain ↓ scrolls, does not recall newest either.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	got = tm.(Model)
	if got.ta.Value() != "" || got.histBrowsing() {
		t.Fatalf("mouse-off Down must not recall, got %q browsing=%v", got.ta.Value(), got.histBrowsing())
	}
	// While browsing (via Shift+↑): plain ↑↓ keeps the recalled input.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyShiftUp})
	got = tm.(Model)
	if got.ta.Value() != "m2" || !got.histBrowsing() {
		t.Fatalf("Shift+Up with mouse off should recall m2, got %q browsing=%v", got.ta.Value(), got.histBrowsing())
	}
	tm, _ = got.Update(tea.KeyMsg{Type: tea.KeyUp})
	if kept := tm.(Model); kept.ta.Value() != "m2" || !kept.histBrowsing() {
		t.Fatalf("mouse-off Up while browsing must keep m2, got %q browsing=%v", kept.ta.Value(), kept.histBrowsing())
	}
	tm, _ = got.Update(tea.KeyMsg{Type: tea.KeyDown})
	if kept := tm.(Model); kept.ta.Value() != "m2" || !kept.histBrowsing() {
		t.Fatalf("mouse-off Down while browsing must keep m2, got %q browsing=%v", kept.ta.Value(), kept.histBrowsing())
	}
	// Shift+↓ navigates newer even with mouse off.
	tm, _ = got.Update(tea.KeyMsg{Type: tea.KeyShiftDown})
	if nav := tm.(Model); nav.ta.Value() != "" || nav.histBrowsing() {
		t.Fatalf("Shift+Down past newest should clear, got %q browsing=%v", nav.ta.Value(), nav.histBrowsing())
	}
}

// Mouse on keeps the shell-like behavior: plain ↑↓ recalls when empty.
func TestMouseOnArrowsRecall(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Mouse = true
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	m.pushHist("m1")
	m.pushHist("m2")
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	if got := tm.(Model); got.ta.Value() != "m2" {
		t.Fatalf("mouse-on Up should recall m2, got %q", got.ta.Value())
	}
}
