package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

func loginTestDialog() *Dialog {
	d := &Dialog{
		Kind: "login", Title: "Provider login",
		Provs:     []string{"anthropic", "openai", "groq"},
		ProvConn:  map[string]bool{},
		ProvFocus: true,
	}
	d.Reindex()
	return d
}

// Filter narrows the left (providers) pane by id, label, or env var.
func TestLoginReindexFiltersProviders(t *testing.T) {
	d := loginTestDialog()
	if len(d.PIdx) != 3 {
		t.Fatalf("PIdx = %v", d.PIdx)
	}
	d.Filter = "groq"
	d.Reindex()
	if len(d.PIdx) != 1 || d.Provs[d.PIdx[0]] != "groq" {
		t.Fatalf("groq PIdx = %v", d.PIdx)
	}
	d.Filter = "OPENAI_API_KEY"
	d.Reindex()
	if len(d.PIdx) != 1 || d.Provs[d.PIdx[0]] != "openai" {
		t.Fatalf("env PIdx = %v", d.PIdx)
	}
	d.Filter = "anthropic"
	d.Reindex()
	if len(d.PIdx) != 1 {
		t.Fatalf("label PIdx = %v", d.PIdx)
	}
	d.Filter = "no-such-provider"
	d.Reindex()
	if len(d.PIdx) != 0 {
		t.Fatalf("expected empty, got %v", d.PIdx)
	}
}

// Right pane: masked key rows + 3 action rows, never the raw secret.
func TestRefreshLoginKeysBuildsRows(t *testing.T) {
	dir := t.TempDir()
	m := Model{KeyPath: dir + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-second-22222222")

	d := loginTestDialog()
	d.ProvCursor = 2 // groq
	m.RefreshLoginKeys(d)
	if d.LoginProvider != "groq" || d.LoginEnv != "GROQ_API_KEY" {
		t.Fatalf("sel = %q %q", d.LoginProvider, d.LoginEnv)
	}
	if d.KeyActive != 1 {
		t.Fatalf("active = %d", d.KeyActive)
	}
	if len(d.Options) != 2+3 {
		t.Fatalf("rows = %d: %v", len(d.Options), d.Options)
	}
	for _, row := range d.Options {
		if strings.Contains(row, "sk-first-11111111") || strings.Contains(row, "sk-second-22222222") {
			t.Fatalf("row leaks full key: %q", row)
		}
	}
	if d.Payload[0] != "sk-first-11111111" || d.Payload[1] != "sk-second-22222222" {
		t.Fatalf("payload = %v", d.Payload)
	}
	if d.LoginCounts["groq"] != 2 || !d.ProvConn["groq"] {
		t.Fatalf("counts=%v conn=%v", d.LoginCounts, d.ProvConn)
	}
	if d.Options[2] != "＋ Add new key" {
		t.Fatalf("add row = %q", d.Options[2])
	}
}

// Enter on the left pane focuses keys; typing filters providers.
func TestLoginDialogEnterAndFilter(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	m.Dialogs = []*Dialog{loginTestDialog()}
	m.RefreshLoginKeys(m.Dialogs[0])

	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if m.Dialogs[0].ProvFocus {
		t.Fatal("Enter on providers must focus keys")
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyLeft})
	m = um.(Model)
	if !m.Dialogs[0].ProvFocus {
		t.Fatal("Left must focus providers")
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g', 'r', 'o', 'q'}})
	// runes arrive one KeyMsg at a time in the real loop; simulate serially
	_ = um
	m.Dialogs[0].Filter = ""
	for _, r := range "groq" {
		um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = um.(Model)
	}
	d := m.Dialogs[0]
	if d.Filter != "groq" || len(d.PIdx) != 1 || !d.ProvFocus {
		t.Fatalf("filter=%q PIdx=%v focus=%v", d.Filter, d.PIdx, d.ProvFocus)
	}
}

// ⌫ on an inactive key deletes it and stays in the dialog.
func TestLoginDeleteInactiveStays(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-second-22222222")

	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = 0 // inactive (active is 1)
	m.Dialogs = []*Dialog{d}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyBackspace})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("inactive delete must defer the keystore write off the event loop")
	}

	// The delete is blocking file I/O; LoginDeleteMsg reports back and, for
	// an inactive key, deliberately returns no respawn command.
	um, respawn := m.Update(cmd())
	m = um.(Model)
	if respawn != nil {
		t.Fatal("inactive delete must stay (no respawn cmd)")
	}
	if len(m.Dialogs) != 1 {
		t.Fatal("dialog must stay open")
	}
	keys, active := pirpc.ListKeys(m.KeyPath, "GROQ_API_KEY")
	if len(keys) != 1 || active != 0 || keys[0] != "sk-second-22222222" {
		t.Fatalf("keys=%v active=%d", keys, active)
	}
}

// Enter on "+ Add new key" opens the secret input for that provider.
func TestLoginAddOpensSecret(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = loginKeysLen(d) // Add row (no keys saved)
	m.Dialogs = []*Dialog{d}

	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	s := m.Dialogs[0]
	if s.Kind != "secret" || s.LoginProvider != "groq" || s.LoginEnv != "GROQ_API_KEY" {
		t.Fatalf("secret = %+v", s)
	}
}
