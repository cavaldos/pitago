package app

import (
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

func newHistModel(hist []string) Model {
	m := Model{histIdx: -1}
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
