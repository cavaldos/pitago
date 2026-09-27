package builtin

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/components/pet"
)

// /pet is a pitago builtin: no arg opens the picker, a name applies
// directly, an unknown name must report instead of silently doing nothing.
func TestPetCommand(t *testing.T) {
	var command app.Builtin
	for _, c := range All() {
		if c.Name == "pet" {
			command = c
			break
		}
	}
	if command.Name == "" {
		t.Fatal("builtin /pet missing")
	}
	if command.Origin != OriginPitago {
		t.Fatalf("/pet origin = %q", command.Origin)
	}

	// No arg → picker with the whole art table, preselected on the current pet.
	m := &app.Model{}
	m.PetName = pet.DefaultName
	cmd := command.Run(m, "")
	if cmd == nil {
		t.Fatal("/pet with no arg must open the picker")
	}
	pm, ok := cmd().(app.PickerMsg)
	if !ok {
		t.Fatalf("expected PickerMsg, got %T", cmd())
	}
	if pm.Kind != "pet" {
		t.Fatalf("picker kind = %q", pm.Kind)
	}
	if len(pm.Options) != len(pet.Entries()) {
		t.Fatalf("picker options = %d, want %d", len(pm.Options), len(pet.Entries()))
	}
	if pm.Current != pet.DefaultName {
		t.Fatalf("picker current = %q", pm.Current)
	}

	// Named arg → applied directly, no picker.
	m2 := &app.Model{}
	if cmd := command.Run(m2, "owl"); cmd != nil {
		t.Fatalf("/pet owl must not open the picker: %v", cmd)
	}
	if m2.PetName != "owl" {
		t.Fatalf("/pet owl → PetName = %q", m2.PetName)
	}

	// Unknown name → reported as a notice, Model untouched (the notice
	// itself is unexported toast state, so only the refusal is assertable).
	m3 := &app.Model{}
	if cmd := command.Run(m3, "unicorn"); cmd != nil {
		t.Fatalf("unknown pet must not schedule work: %v", cmd)
	}
	if m3.PetName != "" {
		t.Fatalf("unknown pet must not set PetName: %q", m3.PetName)
	}
	if command.Usage == "" {
		t.Error("/pet needs Usage text")
	}
}

// The picker kind must be wired to an Enter handler.
func TestConfirmPetRegistered(t *testing.T) {
	if Confirmers()["pet"] == nil {
		t.Fatal("pet picker has no confirm handler")
	}
	m := &app.Model{}
	m.Dialogs = []*app.Dialog{{Kind: "pet", Options: pet.Names()}}
	ri := -1
	for i, n := range pet.Names() {
		if n == "goose" {
			ri = i
		}
	}
	if ri < 0 {
		t.Fatal("goose missing from the art table")
	}
	if _, cmd := Confirmers()["pet"](m, m.Dialogs[0], ri); cmd != nil {
		t.Fatal("confirming a pet must not schedule work")
	}
	if len(m.Dialogs) != 0 {
		t.Fatal("pet dialog should be popped")
	}
	if m.PetName != "goose" {
		t.Fatalf("confirmPet → PetName = %q", m.PetName)
	}
}

// End-to-end: the /pet picker opened through the real Update path, walked
// with real key messages, and applied with the real confirmPet. This is the
// list picker's whole loop (picker message → list cursor → Enter → sidebar).
func TestPetDialogEndToEnd(t *testing.T) {
	m := app.New(nil, t.TempDir())
	m.UseBuiltins(All(), Confirmers())
	// A real terminal size: the dialog's page/window math reads winH.
	wm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = wm.(app.Model)
	m.PetName = pet.DefaultName
	pm, _ := app.Model.Update(m, app.PickerMsg{
		Kind:    "pet",
		Options: pet.Entries(),
		Current: pet.DefaultName,
	})
	m = pm.(app.Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != "pet" {
		t.Fatalf("picker must open the pet dialog, got %+v", m.Dialogs)
	}
	if m.Dialogs[0].Current != pet.DefaultName {
		t.Fatalf("dialog lost the current-pet marker: %q", m.Dialogs[0].Current)
	}
	start := m.Dialogs[0].Cursor
	if start < 0 || start >= len(m.Dialogs[0].FIdx) {
		t.Fatalf("cursor must preselect the current entry, got %d", start)
	}
	// ↓↓ lands two rows down the one-column list.
	var nm tea.Model
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(app.Model)
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = nm.(app.Model)
	if got, want := m.Dialogs[0].Cursor, start+2; got != want {
		t.Fatalf("list cursor = %d, want %d", got, want)
	}
	nm, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	applied, ok := nm.(*app.Model) // confirmPet answers with *Model
	if !ok {
		t.Fatalf("Enter must hand back *Model, got %T", nm)
	}
	m = *applied
	if len(m.Dialogs) != 0 {
		t.Fatal("Enter must close the pet dialog")
	}
	if want := pet.Entries()[start+2]; m.PetName != want {
		t.Fatalf("Enter applied %q, want %q", m.PetName, want)
	}
	// The look entries work as direct args too, and keep the anchor.
	var petCmd app.Builtin
	for _, c := range All() {
		if c.Name == "pet" {
			petCmd = c
		}
	}
	m2 := app.New(nil, t.TempDir())
	m2.PetName = pet.DefaultName
	if cmd := petCmd.Run(&m2, "classic"); cmd != nil {
		t.Fatalf("/pet classic must not open the picker: %v", cmd)
	}
	if m2.PetName != pet.DefaultName {
		t.Fatalf("classic must keep the anchor, got %q", m2.PetName)
	}
	msg := m2.OpenPet()().(app.PickerMsg)
	if msg.Current != pet.StyleClassic {
		t.Fatalf("in classic style the ● must mark %q, got %q", pet.StyleClassic, msg.Current)
	}
}
