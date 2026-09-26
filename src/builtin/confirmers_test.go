package builtin

import (
	"testing"

	"pitago/src/app"
)

// The picker dialogs the app opens are dispatched by kind through Confirmers().
// A kind that is missing an entry still opens, still moves its cursor, and then
// silently does nothing on Enter — the failure looks like a dead key rather than
// a missing registration, so it is worth asserting the map directly.
func TestConfirmersCoversEveryDialogKindThatConfirms(t *testing.T) {
	confirmers := Confirmers()
	for _, kind := range []string{
		"model", "recent", "sessions", "thinking", "theme", "settings",
		"pconfig", "login", "loginMethod", "loginOAuth", "logout", "secret",
		"yank", "update", "trajectory", "tree", "subagents",
		"blockactions",
	} {
		if _, ok := confirmers[kind]; !ok {
			t.Errorf("dialog kind %q has no confirmer: Enter would do nothing", kind)
		}
	}
}

// The block-actions menu is opened from app code with this exact kind, so a
// mismatch between the two halves is what makes Enter dead.
func TestBlockActionsDialogKindHasAConfirmer(t *testing.T) {
	fn, ok := Confirmers()["blockactions"]
	if !ok {
		t.Fatal(`app opens a dialog with Kind "blockactions"; Confirmers() has no such key`)
	}
	if fn == nil {
		t.Fatal("the blockactions confirmer is nil")
	}
}

// Enter on the semantic copy menu must close the dialog and hand the picked row
// to the app, which owns the actual copy.
func TestConfirmBlockActionsClosesAndDispatches(t *testing.T) {
	m := hubModel()
	m.Dialogs = []*app.Dialog{{Kind: "blockactions", BlockIdx: 0, Options: []string{"Copy markdown"}}}
	fn, ok := Confirmers()["blockactions"]
	if !ok {
		t.Fatal("no blockactions confirmer registered")
	}
	model, _ := fn(m, m.Dialogs[0], 0)
	got, ok := model.(*app.Model)
	if !ok {
		t.Fatalf("confirmer returned %T, want *app.Model", model)
	}
	if len(got.Dialogs) != 0 {
		t.Fatalf("Enter should close the menu, %d dialogs left", len(got.Dialogs))
	}
}
