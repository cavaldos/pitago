package builtin

import (
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/components/clipboard"
)

// The picker dialogs the app opens are dispatched by kind through Confirmers().
// A kind that is missing an entry still opens, still moves its cursor, and then
// silently does nothing on Enter — the failure looks like a dead key rather than
// a missing registration, so it is worth asserting the map directly.
func TestConfirmersCoversEveryDialogKindThatConfirms(t *testing.T) {
	confirmers := Confirmers()
	// "theme" is absent on purpose: upstream folded /theme into the
	// /pitago-setting hub (PsecTheme), so no theme dialog is opened any more.
	for _, kind := range []string{
		"model", "recent", "sessions", "thinking", "pet", "settings",
		"pconfig", "login", "loginMethod", "loginOAuth", "logout", "secret",
		"yank", "update", "trajectory", "tree", "treeAction", "subagents",
		"blockactions", "mcp", "mcpAction", "mcpExposure", "mcpTools",
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
// to the app. The dialog is a real one (built by newBlockActionsDialog from a
// real assistant block) rather than a hand-rolled literal, because a dialog
// with no Payload makes RunBlockAction return early and the assertion would
// pass even if dispatch were a no-op. What the copy itself contains is
// RunBlockAction's contract, covered in the app package.
func TestConfirmBlockActionsClosesAndDispatches(t *testing.T) {
	m := hubModel()
	// hubModel leaves a pconfig dialog open; the right-click path that owns
	// this menu only fires when no dialog is open, and the confirmer pops
	// Dialogs[1:], so clear it to match the real state.
	m.Dialogs = nil
	idx := m.AddBlock(app.Block{Kind: "assistant", Text: "the quick brown fox"})
	var copied string
	prev := clipboard.Transport
	clipboard.Transport = func(text string) clipboard.Status {
		copied = text
		return clipboard.Status{Channel: clipboard.Atoto, Bytes: len(text), Chars: len([]rune(text))}
	}
	defer func() { clipboard.Transport = prev }()

	if !m.OpenBlockActions(idx) {
		t.Fatal("an assistant block with text must open a copy menu")
	}
	// The menu is the only dialog, as on a real right-click.
	d := m.Dialogs[0]
	if d.Kind != "blockactions" || len(d.Payload) == 0 {
		t.Fatalf("the menu must be a blockactions dialog carrying a payload, or Enter "+
			"dispatches nothing: kind=%q payload=%v", d.Kind, d.Payload)
	}

	fn, ok := Confirmers()["blockactions"]
	if !ok {
		t.Fatal("no blockactions confirmer registered")
	}
	open := len(m.Dialogs)
	model, _ := fn(m, d, 0)
	got, ok := model.(*app.Model)
	if !ok {
		t.Fatalf("confirmer returned %T, want *app.Model", model)
	}
	if len(got.Dialogs) != open-1 {
		t.Fatalf("Enter should close the menu, %d dialogs left", len(got.Dialogs))
	}
	if !strings.Contains(copied, "quick brown fox") {
		t.Fatalf("Enter closed the menu without copying: %q", copied)
	}
}
