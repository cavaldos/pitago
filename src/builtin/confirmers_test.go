package builtin

import (
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/components/clipboard"
)

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
