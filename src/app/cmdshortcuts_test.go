package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func altKey(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: true}
}

func TestShortcutLabelOf(t *testing.T) {
	if l, ok := shortcutLabelOf(altKey('E')); !ok || l != "alt+e" {
		t.Errorf("Alt+E should map to alt+e, got %q %v", l, ok)
	}
	if l, ok := shortcutLabelOf(altKey('3')); !ok || l != "alt+3" {
		t.Errorf("Alt+3 should map to alt+3, got %q %v", l, ok)
	}
	if _, ok := shortcutLabelOf(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'e'}}); ok {
		t.Error("non-Alt key must not map")
	}
	if _, ok := shortcutLabelOf(tea.KeyMsg{Type: tea.KeyEnter}); ok {
		t.Error("Enter must not map")
	}
}

func TestShortcutReserved(t *testing.T) {
	for _, l := range []string{"alt+m", "alt+1", "alt+5"} {
		if !shortcutReserved(l) {
			t.Errorf("%s should be reserved", l)
		}
	}
	if shortcutReserved("alt+e") || shortcutReserved("alt+9") {
		t.Error("alt+e / alt+9 should be assignable")
	}
}

func TestSetStealsLabel(t *testing.T) {
	m := &Model{}
	m.setCmdShortcut("mcp", "alt+e")
	m.setCmdShortcut("council", "alt+e") // steal
	if _, ok := m.CmdShortcuts["mcp"]; ok {
		t.Error("steal should remove the previous owner")
	}
	if got := m.CmdShortcuts["council"]; got != "alt+e" {
		t.Errorf("council should own alt+e, got %q", got)
	}
	if cmd, ok := m.findCmdShortcut("alt+e"); !ok || cmd != "council" {
		t.Errorf("reverse lookup failed, got %q %v", cmd, ok)
	}
	m.clearCmdShortcut("council")
	if len(m.CmdShortcuts) != 0 {
		t.Error("clear should drop the entry")
	}
}

// Ctrl+S on an extension row opens capture; Alt+E assigns, pops back to
// the hub, and the row shows ⌥E.
func TestPconfigShortcutFlow(t *testing.T) {
	m := testPconfigModel()
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecExt {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)

	mm, _ := m.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyCtrlS}, m.Dialogs[0])
	m2 := mm.(Model)
	if len(m2.Dialogs) != 2 || m2.Dialogs[0].Kind != shortcutKind {
		t.Fatalf("Ctrl+S should push capture, got %+v", m2.Dialogs)
	}
	if m2.Dialogs[0].ShortcutCmd != "mcp" {
		t.Fatalf("capture target should be mcp, got %q", m2.Dialogs[0].ShortcutCmd)
	}

	um, _ := m2.updateDialog(altKey('E'))
	m3 := um.(Model)
	if got := m3.CmdShortcuts["mcp"]; got != "alt+e" {
		t.Errorf("mcp should own alt+e, got %q", got)
	}
	if len(m3.Dialogs) != 1 || m3.Dialogs[0].Kind != "pconfig" {
		t.Fatalf("assign should pop back to the hub, got %+v", m3.Dialogs)
	}
	if !strings.Contains(m3.Dialogs[0].Descs[0], "⌥E") {
		t.Errorf("row should show ⌥E, got %q", m3.Dialogs[0].Descs[0])
	}

	// ⌫ in capture clears.
	m3.openShortcutCapture("mcp")
	um, _ = m3.updateDialog(tea.KeyMsg{Type: tea.KeyBackspace})
	m4 := um.(Model)
	if len(m4.CmdShortcuts) != 0 {
		t.Errorf("backspace should clear, got %v", m4.CmdShortcuts)
	}

	// Reserved Alt+M stays open with a toast, no assignment.
	m4.openShortcutCapture("mcp")
	um, _ = m4.updateDialog(altKey('m'))
	m5 := um.(Model)
	if len(m5.Dialogs) != 2 {
		t.Error("reserved key should keep capture open")
	}
	if len(m5.CmdShortcuts) != 0 {
		t.Error("reserved key must not assign")
	}
}
