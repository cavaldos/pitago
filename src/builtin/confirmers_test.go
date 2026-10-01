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

// Enter on a marketplace row while another plugin op is running must not
// fire a second `pi install` — and must not even arm the confirm gate.
func TestPluginBusyGate(t *testing.T) {
	m := hubModel()
	m.Market = []app.MarketEntry{{Name: "pi-new", Version: "1.0.0", Desc: "fresh"}}
	d := m.Dialogs[0]
	selectPsec(m, d, app.PsecMarket)

	// an install is already running
	if cmd := m.StartPluginOp("install", "npm:pi-other"); cmd == nil {
		t.Fatal("StartPluginOp should return a cmd")
	}
	mm, cmd := confirmPconfig(m, d, d.FIdx[d.Cursor])
	m2 := mm.(*app.Model)
	if cmd != nil {
		t.Error("Enter while an op is running must not issue a second install")
	}
	act, spec := m2.PluginBusy()
	if act != "install" || spec != "npm:pi-other" {
		t.Errorf("the running op must be left alone, got %q/%q", act, spec)
	}
	if got := m2.Status; !strings.Contains(got, "wait for it") {
		t.Errorf("the status should tell the user to wait, got %q", got)
	}
	if len(m2.Dialogs) != 1 {
		t.Error("the hub should stay open")
	}
	// once the op lands, Enter arms the confirm gate as usual
	um, _ := m2.Update(app.PluginChangeMsg{Action: "install", Spec: "npm:pi-other"})
	m3 := um.(app.Model)
	if _, spec := m3.PluginBusy(); spec != "" {
		t.Fatalf("the op should be done, got %q", spec)
	}
	mm, cmd = confirmPconfig(&m3, m3.Dialogs[0], m3.Dialogs[0].FIdx[0])
	m4 := mm.(*app.Model)
	if cmd != nil {
		t.Error("the first Enter after the op should only arm the gate")
	}
	if !strings.Contains(m4.Status, "confirm install npm:pi-new") {
		t.Errorf("the confirm gate should arm again, got %q", m4.Status)
	}
}
