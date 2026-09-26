package app

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// Selecting a key stays on /login (not main) and pushes to pi.
func TestLoginSelectStaysAndPushesPi(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-second-22222222")

	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = 0 // inactive key
	m.Dialogs = []*Dialog{d}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "login" {
		t.Fatalf("must stay on /login, dialogs=%v", m.Dialogs)
	}
	if cmd == nil {
		t.Fatal("select must defer the keystore write off the event loop")
	}

	// Activation + the push to pi are blocking file I/O: they run in the
	// Cmd and come back as LoginSwitchMsg, which then respawns pi.
	um, respawn := m.Update(cmd())
	m = um.(Model)
	if respawn == nil {
		t.Fatal("select must respawn pi")
	}
	keys, active := pirpc.ListKeys(m.KeyPath, "GROQ_API_KEY")
	if active != 0 || keys[active] != "sk-first-11111111" {
		t.Fatalf("keys=%v active=%d", keys, active)
	}
	if got := pirpc.PiApiKey("groq"); got != "sk-first-11111111" {
		t.Fatalf("pi key = %q", got)
	}
}

// Deleting the ACTIVE key stays on /login too.
func TestLoginDeleteActiveStays(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-second-22222222")

	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = 1 // active
	m.Dialogs = []*Dialog{d}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyBackspace})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "login" {
		t.Fatalf("active delete must stay on /login")
	}
	if cmd == nil {
		t.Fatal("active delete must defer the keystore write off the event loop")
	}

	// Delete + push + re-read share one Cmd (the notice's re-read must never
	// race the write); LoginDeleteMsg then respawns pi.
	um, respawn := m.Update(cmd())
	m = um.(Model)
	if respawn == nil {
		t.Fatal("active delete must respawn pi")
	}
	keys, _ := pirpc.ListKeys(m.KeyPath, "GROQ_API_KEY")
	if len(keys) != 1 {
		t.Fatalf("keys=%v", keys)
	}
}

// s toggles show/hide on the right pane; r opens rename on top of /login.
func TestLoginShowRenameShortcuts(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = 0
	m.Dialogs = []*Dialog{d}

	um, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = um.(Model)
	if !m.Dialogs[0].ShowKeys {
		t.Fatal("s must show keys")
	}
	if !strings.Contains(m.Dialogs[0].Options[0], "sk-first-11111111") {
		t.Fatalf("shown row must reveal key: %q", m.Dialogs[0].Options[0])
	}
	um, _ = m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = um.(Model)
	if m.Dialogs[0].ShowKeys {
		t.Fatal("second s must hide again")
	}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("r must defer the keystore read off the event loop")
	}

	// The keystore read runs in the Cmd; the prompt is pushed on arrival.
	um, _ = m.Update(cmd())
	m = um.(Model)
	if len(m.Dialogs) != 2 || m.Dialogs[0].Kind != "rename" {
		t.Fatalf("r must open rename on top, got %v", m.Dialogs)
	}
	if m.Dialogs[1].Kind != "login" {
		t.Fatalf("login must stay underneath rename")
	}
}

// Rename saves via msg and stays on /login.
func TestLoginRenameStays(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	_ = pirpc.SaveKey(m.KeyPath, "GROQ_API_KEY", "sk-first-11111111")
	d := loginTestDialog()
	d.ProvCursor = 2
	m.RefreshLoginKeys(d)
	m.Dialogs = []*Dialog{
		{Kind: "rename", Filter: "work", LoginProvider: "groq", LoginEnv: "GROQ_API_KEY", RenameIdx: 0},
		d,
	}
	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("rename Enter must produce msg")
	}
	msg := cmd()
	rn, ok := msg.(RenameKeyMsg)
	if !ok || rn.Name != "work" {
		t.Fatalf("msg = %#v", msg)
	}
	// apply like Update does
	um2, _ := m.Update(rn)
	m = um2.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "login" {
		t.Fatalf("must return to /login, got %v", m.Dialogs)
	}
	items, _ := pirpc.ListKeyItems(m.KeyPath, "GROQ_API_KEY")
	if items[0].Name != "work" {
		t.Fatalf("name = %q", items[0].Name)
	}
}

// OAuth provider shows disconnect + reload (no add row) and stays on /login.
func TestLoginOAuthOnlyProvider(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	os.WriteFile(agent+"/auth.json", []byte(`{"openai-codex":{"type":"oauth","access":"a","refresh":"r","expires":1790857264866,"accountId":"acct-9"}}`), 0o600)
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	d := &Dialog{
		Kind: "login", Title: "Provider login",
		Provs: []string{"openai-codex", "groq"}, ProvConn: map[string]bool{},
		ProvFocus: true,
	}
	d.Reindex()
	m.RefreshLoginKeys(d)
	if d.LoginProvider != "openai-codex" {
		t.Fatalf("sel = %q", d.LoginProvider)
	}
	if !d.LoginOAuth || d.LoginOAuthAcct != "acct-9" {
		t.Fatalf("oauth = %v %q", d.LoginOAuth, d.LoginOAuthAcct)
	}
	if len(d.LoginActions) != 2 || d.LoginActions[0] != "disconnect" || d.LoginActions[1] != "reload" {
		t.Fatalf("actions = %v", d.LoginActions)
	}
	if loginKeysLen(d) != 0 {
		t.Fatalf("keys = %d, want 0", loginKeysLen(d))
	}
}

// Enter on Disconnect removes pi OAuth, forgets mirror, stays on /login.
func TestLoginDisconnectOAuthStays(t *testing.T) {
	agent := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agent)
	os.WriteFile(agent+"/auth.json", []byte(`{"openai-codex":{"type":"oauth","access":"a","refresh":"r","expires":1790857264866}}`), 0o600)
	m := Model{KeyPath: t.TempDir() + "/keys.json", AuthPath: t.TempDir() + "/pi_auth.json"}
	pirpc.SyncAuthStateFromPi(m.AuthPath)
	d := &Dialog{
		Kind: "login", Title: "Provider login",
		Provs: []string{"openai-codex"}, ProvConn: map[string]bool{},
		ProvFocus: true,
	}
	d.Reindex()
	m.RefreshLoginKeys(d)
	d.ProvFocus = false
	d.KeyCursor = 0 // disconnect row (no keys, actions=[disconnect,reload])
	m.Dialogs = []*Dialog{d}
	if d.LoginActions[0] != "disconnect" {
		t.Fatalf("actions = %v", d.LoginActions)
	}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "login" {
		t.Fatalf("must stay on /login")
	}
	if cmd == nil {
		t.Fatal("disconnect must defer the auth teardown off the event loop")
	}

	// The teardown is blocking file I/O: it runs in the Cmd and comes back
	// as OAuthGoneMsg, which refreshes the picker and then respawns pi.
	um, respawn := m.Update(cmd())
	m = um.(Model)
	if respawn == nil {
		t.Fatal("disconnect must respawn pi")
	}
	if _, _, ok := pirpc.PiOAuth("openai-codex"); ok {
		t.Fatal("pi oauth should be gone")
	}
	if _, ok := pirpc.LoadAuthState(m.AuthPath)["openai-codex"]; ok {
		t.Fatal("mirror should forget")
	}
	if m.Dialogs[0].LoginOAuth {
		t.Fatal("picker must show logged-out state")
	}
}

// Ctrl+P on /login jumps to the model picker (dialog pops, loader runs).
func TestLoginCtrlPGoesToModel(t *testing.T) {
	m := Model{KeyPath: t.TempDir() + "/keys.json"}
	m.UseBuiltins([]Builtin{{Name: "model", Run: func(mm *Model, arg string) tea.Cmd {
		return func() tea.Msg { return PickerMsg{Kind: "model"} }
	}}}, nil)
	d := loginTestDialog()
	m.Dialogs = []*Dialog{d}

	um, cmd := m.updateDialog(tea.KeyMsg{Type: tea.KeyCtrlP})
	m = um.(Model)
	if len(m.Dialogs) != 0 {
		t.Fatalf("login must close, dialogs=%v", m.Dialogs)
	}
	if cmd == nil {
		t.Fatal("must run model loader")
	}
}
