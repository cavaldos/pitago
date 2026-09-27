package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
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

// The Commands tab is the local registry only: pi builtins + pitago
// commands, never the extension/prompt/skill rows from the pi catalog.
func TestPconfigCommandsSection(t *testing.T) {
	m := testPconfigModel()
	m.UseBuiltins([]Builtin{
		{Name: "model", Desc: "Select model", Origin: "pi"},
		{Name: "theme", Desc: "Switch theme", Origin: "pitago"},
		{Name: BuiltinLoginDialog, Desc: "login continuation", Hidden: true},
	}, nil)
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecCmd {
			d.ProvCursor = i
		}
	}
	d.ProvFocus = false
	m.LoadPsecRows(d)

	if n := psecCount(m, PsecCmd); n != 2 {
		t.Errorf("badge should count the 2 visible builtins, got %d", n)
	}
	// pi rows sort before pitago rows, hidden continuations never show.
	want := []string{"/model", "/theme"}
	if len(d.Options) != len(want) {
		t.Fatalf("commands rows = %v, want %v", d.Options, want)
	}
	for i, o := range want {
		if d.Options[i] != o {
			t.Errorf("row %d = %q, want %q", i, d.Options[i], o)
		}
	}
	if d.Payload[0] != "model" || d.Payload[1] != "theme" {
		t.Errorf("payload should carry the runnable names, got %v", d.Payload)
	}
	if !strings.Contains(d.Descs[0], "[pi]") || !strings.Contains(d.Descs[1], "[pitago]") {
		t.Errorf("origin tags missing: %q / %q", d.Descs[0], d.Descs[1])
	}
	// Third-party catalog rows stay in their own sections.
	joined := strings.Join(d.Options, " ")
	for _, name := range []string{"/mcp", "/council", "/skill:archify", "/" + BuiltinLoginDialog} {
		if strings.Contains(joined, name) {
			t.Errorf("%s must not appear in the Commands tab: %v", name, d.Options)
		}
	}
}

// The Shortcuts section is the /shortcuts reference: every built-in key
// plus the hub-assigned Alt rows, which Ctrl+S can re-open for editing.
func TestPconfigShortcutsSection(t *testing.T) {
	m := testPconfigModel()
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.UseBuiltins([]Builtin{{Name: "model", Desc: "Select model", Origin: "pi"}}, nil)
	m.setCmdShortcut("model", "alt+e")
	m.OpenPconfig()
	d := m.Dialogs[0]
	d.ProvCursor = secIndex(d, PsecKeys)
	d.ProvFocus = false
	m.LoadPsecRows(d)

	// Every built-in key is there, and the badge counts rows not just DB.
	want := len(shortcutsDb) + 1 // + the one assigned Alt row
	if len(d.Options) != want {
		t.Fatalf("shortcut rows = %d, want %d", len(d.Options), want)
	}
	if n := psecCount(m, PsecKeys); n != want {
		t.Errorf("badge = %d, want %d", n, want)
	}
	// The custom row is last, carries its /command as payload, and shows
	// the assigned label + category tag.
	last := len(d.Options) - 1
	if d.Options[last] != "⌥E" {
		t.Errorf("custom row key = %q, want ⌥E", d.Options[last])
	}
	if d.Payload[last] != "model" {
		t.Errorf("custom row payload = %q, want model (so Ctrl+S can edit it)", d.Payload[last])
	}
	if !strings.Contains(d.Descs[last], "[Custom]") {
		t.Errorf("custom row should be tagged [Custom], got %q", d.Descs[last])
	}
	// Built-in keys are info-only: no payload, so Ctrl+S is a no-op there.
	for i := 0; i < len(shortcutsDb); i++ {
		if d.Payload[i] != "" {
			t.Fatalf("built-in row %d should have no payload, got %q", i, d.Payload[i])
		}
		if d.Options[i] == "" || d.Descs[i] == "" {
			t.Fatalf("built-in row %d is empty: %q / %q", i, d.Options[i], d.Descs[i])
		}
	}
	// Ctrl+S on the custom row re-opens the capture dialog. The cursor
	// must be ON that row (LoadPsecRows leaves it at the top).
	d.Cursor = len(d.FIdx) - 1
	mm, _ := m.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyCtrlS}, m.Dialogs[0])
	m2 := mm.(Model)
	if len(m2.Dialogs) != 2 || m2.Dialogs[0].Kind != shortcutKind {
		t.Fatalf("Ctrl+S on a custom row should push capture, got %d dialogs", len(m2.Dialogs))
	}
	if m2.Dialogs[0].ShortcutCmd != "model" {
		t.Errorf("capture target = %q, want model", m2.Dialogs[0].ShortcutCmd)
	}
	// Ctrl+S on a built-in row stays inert.
	m2.Dialogs = m2.Dialogs[1:]
	m2.Dialogs[0].Cursor = 0
	um, _ := m2.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyCtrlS}, m2.Dialogs[0])
	if n := len(um.(Model).Dialogs); n != 1 {
		t.Errorf("Ctrl+S on a reference row must not open capture, got %d dialogs", n)
	}
}

// /shortcuts opens the hub's Shortcuts section: the section must be a
// faithful projection of shortcutRows() (key, action, category, payload).
func TestShortcutRowsFeedHubSection(t *testing.T) {
	m := &Model{CmdShortcuts: map[string]string{"beta": "alt+b", "alpha": "alt+a"}}
	rows := m.shortcutRows()

	m.OpenKeysSettings()
	d := m.Dialogs[0]
	if d.Kind != "pconfig" {
		t.Fatalf("OpenKeysSettings should push the hub, got %q", d.Kind)
	}
	if d.CurPsec() != PsecKeys {
		t.Fatalf("/shortcuts should land on the Shortcuts section, got %q", d.CurPsec())
	}
	if d.ProvFocus {
		t.Error("hub should open with the right pane focused (keys are the point)")
	}
	if len(d.Options) != len(rows) {
		t.Fatalf("section rows = %d, shortcutRows = %d", len(d.Options), len(rows))
	}
	for i, r := range rows {
		if d.Options[i] != r.key {
			t.Errorf("row %d key = %q, want %q", i, d.Options[i], r.key)
		}
		if d.Payload[i] != r.cmd {
			t.Errorf("row %d payload = %q, want %q", i, d.Payload[i], r.cmd)
		}
		// The category rides along as a trailing tag on the description.
		if !strings.HasSuffix(d.Descs[i], "["+r.cat+"]") {
			t.Errorf("row %d desc %q should end with [%s]", i, d.Descs[i], r.cat)
		}
		if !strings.HasPrefix(d.Descs[i], shortDesc(r.desc, 40)) {
			t.Errorf("row %d desc %q should start with %q", i, d.Descs[i], shortDesc(r.desc, 40))
		}
	}
	// Custom rows are sorted by command name for a stable list.
	if got := rows[len(rows)-2]; got.key != "⌥A" || got.desc != "/alpha" {
		t.Errorf("first custom row = %+v, want ⌥A → /alpha", got)
	}
	if got := rows[len(rows)-1]; got.key != "⌥B" || got.desc != "/beta" {
		t.Errorf("second custom row = %+v, want ⌥B → /beta", got)
	}
	for _, r := range rows[len(rows)-2:] {
		if r.cat != "Custom" || r.cmd == "" {
			t.Errorf("custom row missing cat/cmd: %+v", r)
		}
	}
	// Built-in rows are not reassignable: no command behind them.
	for _, r := range rows[:len(rows)-2] {
		if r.cmd != "" {
			t.Errorf("built-in row carries a command: %+v", r)
		}
	}
}

// The standalone shortcuts dialog is gone for good: nothing may reopen it.
func TestNoShortcutsDialogRemains(t *testing.T) {
	if isFilterKind("shortcuts") {
		t.Error("the shortcuts dialog was removed; drop it from isFilterKind")
	}
	m := &Model{}
	m.OpenKeysSettings()
	if got := m.Dialogs[0].Kind; got == "shortcuts" {
		t.Fatal("/shortcuts must not open a separate dialog any more")
	}
}

// Ctrl+S on a Commands row assigns an Alt-shortcut, the row shows it, and
// pressing that Alt+key stages the builtin in the input.
func TestPconfigCmdShortcutAssignAndFire(t *testing.T) {
	m := testPconfigModel()
	m.UseBuiltins([]Builtin{{Name: "model", Desc: "Select model", Origin: "pi"}}, nil)
	m.ta = textarea.New()
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.OpenPconfig()
	d := m.Dialogs[0]
	for i, id := range d.PsecIDs {
		if id == PsecCmd {
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
	if m2.Dialogs[0].ShortcutCmd != "model" {
		t.Fatalf("capture target should be model, got %q", m2.Dialogs[0].ShortcutCmd)
	}

	um, _ := m2.updateDialog(altKey('C'))
	m3 := um.(Model)
	if got := m3.CmdShortcuts["model"]; got != "alt+c" {
		t.Errorf("model should own alt+c, got %q", got)
	}
	if len(m3.Dialogs) != 1 || m3.Dialogs[0].Kind != "pconfig" {
		t.Fatalf("assign should pop back to the hub, got %+v", m3.Dialogs)
	}
	if !strings.Contains(m3.Dialogs[0].Descs[0], "⌥C") {
		t.Errorf("row should show ⌥C, got %q", m3.Dialogs[0].Descs[0])
	}

	// A builtin is not in the pi catalog — hasCmd must still accept it, or
	// the shortcut would toast "not installed" instead of staging.
	if !m3.hasCmd("model") {
		t.Fatal("hasCmd should accept a registered builtin")
	}
	// The hub is still open here: an Alt rune would land in its filter box,
	// so Esc first — that is the real flow (assign, close, then fire).
	em, _ := m3.updateDialog(tea.KeyMsg{Type: tea.KeyEsc})
	m4 := em.(Model)
	if len(m4.Dialogs) != 0 {
		t.Fatalf("Esc should close the hub, got %+v", m4.Dialogs)
	}
	fm, _ := m4.Update(altKey('C'))
	m5 := fm.(Model)
	if got := m5.ta.Value(); got != "/model " {
		t.Errorf("Alt+C should stage /model, got %q", got)
	}

	// A shortcut left behind by a since-uninstalled command still toasts.
	// AddBlock("notice") routes to the toast stack, not the transcript.
	m5.CmdShortcuts["ghost"] = "alt+g"
	m5.saveCmdShortcuts()
	gm, _ := m5.Update(altKey('G'))
	m6 := gm.(Model)
	if m6.ta.Value() != "/model " {
		t.Errorf("stale shortcut must not stage, got %q", m6.ta.Value())
	}
	if len(m6.toasts) == 0 {
		t.Fatal("stale shortcut should raise a toast")
	}
	last := m6.toasts[len(m6.toasts)-1]
	if !strings.Contains(last.Text, "not installed") {
		t.Errorf("stale shortcut should explain itself, got %q", last.Text)
	}
}
