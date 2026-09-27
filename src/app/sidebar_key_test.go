package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// Ctrl+E toggles the sidebar. Ctrl+B used to, but herdr (a terminal
// multiplexer) owns Ctrl+B, so the binding moved: stealing the textarea's
// "line end" key is the same trade "character backward" was before it.
func TestSidebarToggleKeyIsCtrlE(t *testing.T) {
	m := &Model{ta: textarea.New()}
	m.winW, m.winH = 120, 40
	if !m.showSide() {
		t.Fatal("sidebar should start visible at 120 cols")
	}

	mm, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m2 := mm.(Model)
	if m2.showSide() {
		t.Error("Ctrl+E should hide the sidebar")
	}
	mm, _ = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlE})
	m3 := mm.(Model)
	if !m3.showSide() {
		t.Error("Ctrl+E should toggle the sidebar back on")
	}

	// Ctrl+B is no longer the sidebar key: it falls through to the
	// textarea (bubbles binds it to CharacterBackward) and must not toggle.
	mm, _ = m3.Update(tea.KeyMsg{Type: tea.KeyCtrlB})
	if m4 := mm.(Model); !m4.showSide() {
		t.Error("Ctrl+B must no longer toggle the sidebar (herdr owns it)")
	}
}

// The published key reference must not advertise the dead binding.
func TestSidebarKeyDocMatchesBinding(t *testing.T) {
	var found bool
	for _, s := range shortcutsDb {
		// The hide/show row only — the sidebar *scroll* keys (Ctrl+↑↓,
		// wheel, …) are unrelated and must keep their own bindings.
		if !strings.Contains(s.Description, "Hide/show sidebar") {
			continue
		}
		found = true
		if s.Key != "Ctrl+E" {
			t.Errorf("sidebar shortcut documented as %q, want Ctrl+E (%s)", s.Key, s.Description)
		}
	}
	if !found {
		t.Fatal("no hide/show sidebar entry in shortcutsDb — the /shortcuts reference lost it")
	}
}
