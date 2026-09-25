package builtin

import (
	"testing"

	"pitago/src/app"
)

// End-to-end: the wired confirmer pops the picker, persists current, toasts.
func TestConfirmSubagentsSelects(t *testing.T) {
	fn, ok := Confirmers()["subagents"]
	if !ok {
		t.Fatal("subagents missing from Confirmers")
	}
	m := &app.Model{}
	m.Dialogs = []*app.Dialog{{
		Kind: "subagents", Title: "Subagents",
		Options: []string{app.SubagentsNone, "scout", "reviewer"},
		Descs:   []string{"none", "recon", "review"},
	}}
	m.Dialogs[0].Reindex()
	m.Dialogs[0].Cursor = 1 // scout
	nm, _ := fn(m, m.Dialogs[0], m.Dialogs[0].FIdx[m.Dialogs[0].Cursor])
	if len(m.Dialogs) != 0 {
		t.Fatal("picker should pop on Enter")
	}
	if m.CurrentSubagent() != "scout" {
		t.Fatalf("want scout current, got %q", m.CurrentSubagent())
	}
	_ = nm
}

func TestConfirmSubagentsNoneClears(t *testing.T) {
	fn := Confirmers()["subagents"]
	m := &app.Model{}
	m.Dialogs = []*app.Dialog{{
		Kind: "subagents", Title: "Subagents",
		Options: []string{app.SubagentsNone, "scout"},
		Descs:   []string{"none", "recon"},
	}}
	m.Dialogs[0].Reindex()
	m.SetCurrentSubagent("scout")
	_, _ = fn(m, m.Dialogs[0], 0)
	if m.CurrentSubagent() != "" {
		t.Fatalf("none row must clear, got %q", m.CurrentSubagent())
	}
}

// The registry entry must exist so /subagents routes to the native picker
// instead of falling through to pi (whose ui.custom is a no-op in RPC).
func TestSubagentsRegistered(t *testing.T) {
	found := false
	for _, c := range All() {
		if c.Name == "subagents" {
			found = true
			if c.Usage == "" {
				t.Error("subagents needs Usage")
			}
		}
	}
	if !found {
		t.Fatal("builtin /subagents missing")
	}
}
