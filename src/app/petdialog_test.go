package app

import (
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/pet"
)

// petDialog builds the /pet grid the way PickerMsg does: every entry in
// Options, the live entry in Current, cursor on the live entry.
func petDialog(m Model, current string) *Dialog {
	d := &Dialog{Kind: "pet", Title: "Select pet", Options: pet.Entries(), Current: current}
	for i, o := range d.Options {
		if o == current {
			d.Cursor = i
		}
	}
	d.Reindex()
	return d
}

// The /pet dialog is the /model shape: left = entry list, right = demo of the
// entry under the cursor. Every entry must be reachable and the preview must
// follow the cursor.
func TestPetDialogTwoPanes(t *testing.T) {
	m := Model{winW: 120, winH: 40, ready: true}
	d := petDialog(m, pet.DefaultName)
	m.Dialogs = []*Dialog{d}
	out := stripANSI(m.renderPetDialog(d))
	if !strings.Contains(out, "PETS") || !strings.Contains(out, "PREVIEW") {
		t.Fatalf("two-pane headers missing:\n%s", out)
	}
	// The cursor row (cat) is previewed with its art, not just its name.
	for i, ln := range pet.Resolve("cat").FrameFor(0, false) {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		if !strings.Contains(out, strings.TrimSpace(ln)) {
			t.Fatalf("preview missing the cursor entry's art row %d (%q):\n%s", i, ln, out)
		}
	}
	// Walking down moves the preview with the cursor.
	nm, _ := m.updatePetDialog(tea.KeyMsg{Type: tea.KeyDown}, d)
	m = nm.(Model)
	out = stripANSI(m.renderPetDialog(d))
	dragonArt := strings.TrimSpace(pet.Resolve("dragon").FrameFor(0, false)[petNameRow(pet.Resolve("dragon").FrameFor(0, false))])
	if !strings.Contains(out, dragonArt) {
		t.Fatalf("preview must follow the cursor (dragon):\n%s", out)
	}
	if strings.Contains(out, "cat"+strings.Repeat(" ", 2)) && !strings.Contains(out, "dragon") {
		t.Fatal("stale preview")
	}
	// A short list still shows its scroll hints; every entry is reachable.
	m2 := Model{winW: 100, winH: 16, ready: true}
	d2 := petDialog(m2, pet.Entries()[0])
	m2.Dialogs = []*Dialog{d2}
	short := stripANSI(m2.renderPetDialog(d2))
	if !strings.Contains(short, "…(+") {
		t.Fatalf("a 20-row list on a short terminal must hint at the rest:\n%s", short)
	}
	// Walking to the end reaches every entry (the list is the only pane that
	// takes input, and it wraps).
	for d.Cursor > 0 {
		d.Cursor--
	}
	nm, _ = m.updatePetDialog(tea.KeyMsg{Type: tea.KeyUp}, d)
	m = nm.(Model)
	if d.Cursor != len(d.FIdx)-1 {
		t.Fatalf("↑ from the top must wrap to the last entry, got %d", d.Cursor)
	}
}

// The ● marks the entry in use, and only that one.
func TestPetDialogCurrentMarker(t *testing.T) {
	m := Model{winW: 120, winH: 40, ready: true}
	d := petDialog(m, "owl")
	m.Dialogs = []*Dialog{d}
	if !petLineHas(stripANSI(m.renderPetDialog(d)), "owl", "●") {
		t.Fatalf("● must sit on the entry in use:\n%s", stripANSI(m.renderPetDialog(d)))
	}
	for _, n := range pet.Entries() {
		if n == "owl" {
			continue
		}
		if strings.Contains(stripANSI(m.renderPetDialog(d)), "● "+n) {
			t.Fatalf("● leaked onto %q", n)
		}
	}
}

// Keys mirror the generic list picker: ↑↓ with wrap, PgUp/PgDn, filter,
// Backspace, Enter, Esc/^C. There is no pane focus, so ←/→ are no-ops.
func TestPetDialogKeys(t *testing.T) {
	m := Model{winW: 120, winH: 24, ready: true}
	d := petDialog(m, pet.Names()[0])
	n := len(d.FIdx)
	step := func(km tea.KeyMsg) {
		t.Helper()
		nm, _ := m.updatePetDialog(km, d)
		m = nm.(Model)
	}
	d.Cursor = 0
	step(tea.KeyMsg{Type: tea.KeyUp})
	if d.Cursor != n-1 {
		t.Fatalf("↑ must wrap to the end, got %d", d.Cursor)
	}
	step(tea.KeyMsg{Type: tea.KeyDown})
	if d.Cursor != 0 {
		t.Fatalf("↓ must wrap to the top, got %d", d.Cursor)
	}
	// ←/→ are no-ops: the right pane is a passive preview.
	step(tea.KeyMsg{Type: tea.KeyRight})
	step(tea.KeyMsg{Type: tea.KeyLeft})
	if d.Cursor != 0 {
		t.Fatalf("←/→ must not move the list cursor, got %d", d.Cursor)
	}
	step(tea.KeyMsg{Type: tea.KeyPgDown})
	if d.Cursor != m.winH-11 {
		t.Fatalf("PgDn = %d, want %d", d.Cursor, m.winH-11)
	}
	step(tea.KeyMsg{Type: tea.KeyPgUp})
	if d.Cursor != 0 {
		t.Fatalf("PgUp must clamp at the top, got %d", d.Cursor)
	}
	// Filtering narrows the list; Backspace widens it again.
	nm, _ := m.updatePetDialog(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}}, d)
	m = nm.(Model)
	if len(d.FIdx) >= n || len(d.FIdx) == 0 {
		t.Fatalf("filter left %d of %d rows", len(d.FIdx), n)
	}
	for _, ri := range d.FIdx {
		if !strings.Contains(strings.ToLower(pet.Entries()[ri]), "c") {
			t.Fatalf("filter kept %q", pet.Entries()[ri])
		}
	}
	nm, _ = m.updatePetDialog(tea.KeyMsg{Type: tea.KeyBackspace}, d)
	m = nm.(Model)
	if d.Filter != "" || len(d.FIdx) != n {
		t.Fatalf("backspace filter = %q, rows = %d", d.Filter, len(d.FIdx))
	}
}

// Enter applies the highlighted entry; Esc and ^C close without applying.
func TestPetDialogEnterAndDismiss(t *testing.T) {
	dir := t.TempDir()
	m := New(nil, dir)
	m.prefsPath = filepath.Join(dir, "prefs.json")
	m.winW, m.winH, m.ready = 120, 24, true
	m.confirm = map[string]ConfirmFunc{
		"pet": func(mm *Model, dd *Dialog, ri int) (tea.Model, tea.Cmd) {
			mm.Dialogs = mm.Dialogs[1:]
			mm.SetPet(dd.Options[ri])
			return mm, nil
		},
	}
	d := petDialog(m, pet.DefaultName)
	m.Dialogs = []*Dialog{d}
	// Move to the "classic" look (last entry) and apply it.
	d.Cursor = len(d.FIdx) - 1
	if d.Options[d.FIdx[d.Cursor]] != pet.StyleClassic {
		t.Fatalf("last entry = %q, want classic", d.Options[d.FIdx[d.Cursor]])
	}
	nm, _ := m.updateDialog(tea.KeyMsg{Type: tea.KeyEnter})
	applied, ok := nm.(*Model)
	if !ok {
		t.Fatalf("confirm must hand back *Model, got %T", nm)
	}
	m = *applied
	if len(m.Dialogs) != 0 {
		t.Fatal("Enter must close the pet dialog")
	}
	if m.PetStyle != pet.StyleClassic {
		t.Fatalf("Enter applied style %q, want classic", m.PetStyle)
	}
	if got := LoadPrefs(m.prefsPath).PetStyle; got != pet.StyleClassic {
		t.Fatalf("persisted petStyle = %q", got)
	}
	// Esc and ^C close without applying anything.
	for _, k := range []tea.KeyType{tea.KeyEsc, tea.KeyCtrlC} {
		mm := New(nil, dir)
		mm.winW, mm.winH, mm.ready = 120, 24, true
		mm.PetStyle = pet.StyleASCII
		mm.PetName = pet.DefaultName
		dd := petDialog(mm, pet.DefaultName)
		mm.Dialogs = []*Dialog{dd}
		dd.Cursor = 3 // any other entry
		out, _ := mm.updateDialog(tea.KeyMsg{Type: k})
		m2 := out.(Model)
		if len(m2.Dialogs) != 0 {
			t.Fatalf("key %v must close the pet dialog", k)
		}
		if m2.PetName != pet.DefaultName || m2.PetStyle != pet.StyleASCII {
			t.Fatalf("key %v must not apply: pet=%q style=%q", k, m2.PetName, m2.PetStyle)
		}
	}
}

// The box must fit the window in both dimensions at the sizes the generic
// dialogs support.
func TestPetDialogFitsWindow(t *testing.T) {
	for _, c := range []struct{ winW, winH int }{
		{120, 12}, {120, 24}, {120, 40}, {80, 24}, {70, 16},
	} {
		m := Model{winW: c.winW, winH: c.winH, ready: true}
		d := petDialog(m, pet.DefaultName)
		d.Cursor = len(d.FIdx) - 1 // scroll window at the end of the list
		m.Dialogs = []*Dialog{d}
		out := m.renderPetDialog(d)
		if h := len(strings.Split(strings.TrimRight(out, "\n"), "\n")); h > c.winH {
			t.Fatalf("%dx%d: dialog is %d rows tall", c.winW, c.winH, h)
		}
		// Width: the shared chrome adds 8 cells (border + padding) on top of
		// the box width, and the box has a 70-cell floor — same as /model.
		if w := widestLine(out); w > max(c.winW, m.petDialogBoxW()+8) {
			t.Fatalf("%dx%d: dialog is %d cells wide", c.winW, c.winH, w)
		}
	}
	// The ascii entry has no art of its own: the preview must fall back to
	// the pinned animal rather than come up blank.
	m := Model{winW: 120, winH: 30, ready: true}
	m.PetName = "turtle"
	d := petDialog(m, "turtle")
	m.Dialogs = []*Dialog{d}
	for fi, ri := range d.FIdx {
		if d.Options[ri] != pet.StyleASCII {
			continue
		}
		d.Cursor = fi
		out := stripANSI(m.renderPetDialog(d))
		if !strings.Contains(out, "ascii") {
			t.Fatalf("ascii preview missing its label:\n%s", out)
		}
		art := strings.TrimSpace(pet.Resolve("turtle").FrameFor(0, false)[petNameRow(pet.Resolve("turtle").FrameFor(0, false))])
		if !strings.Contains(out, art) {
			t.Fatalf("ascii preview must show the pinned animal:\n%s", out)
		}
	}
}

// petLineHas reports whether the cell drawing name also carries marker.
func petLineHas(out, name, marker string) bool {
	for _, ln := range strings.Split(out, "\n") {
		if strings.Contains(ln, name) && strings.Contains(ln, marker) {
			return true
		}
	}
	return false
}

func widestLine(s string) int {
	w := 0
	for _, ln := range strings.Split(s, "\n") {
		if n := len([]rune(stripANSI(ln))); n > w {
			w = n
		}
	}
	return w
}
