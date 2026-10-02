package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"pitago/src/components/theme"
)

func TestPaintBG(t *testing.T) {
	defer ApplyTheme(theme.Get("default")) // globals: don't leak into other tests
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(termenv.Ascii) // global: other tests expect the default
	m := New(nil, t.TempDir())
	m.winW, m.winH = 20, 4
	frame := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Render("hi") + " tail"

	if got := m.paintBG(frame); got != frame {
		t.Errorf("off by default: got %q", got)
	}

	m.Background = "opencode" // #0a0a0a in a dark palette
	got := m.paintBG(frame)
	rows := strings.Split(got, "\n")
	if len(rows) != 4 {
		t.Fatalf("want 4 rows, got %d", len(rows))
	}
	for i, row := range rows {
		if displayWidth(row) != 20 {
			t.Errorf("row %d is %d cells, want 20", i, displayWidth(row))
		}
	}
	// The tail after the inner reset must be filled too: an inner style's
	// reset clears the background, so a single pass over the row would
	// leave it transparent.
	if !strings.Contains(rows[0], "\x1b[48;2;10;10;10m tail") {
		t.Errorf("text past the inner reset is unpainted: %q", rows[0])
	}
}

// The background set is opencode's: every choice except transparent
// resolves to a surface, and a light palette takes the light variant.
func TestBackgrounds(t *testing.T) {
	names := theme.Backgrounds()
	if names[0] != theme.Transparent {
		t.Fatalf("first background = %q, want transparent first", names[0])
	}
	for _, n := range names {
		dark := theme.BackgroundHex(n, false)
		if n == theme.Transparent {
			if dark != "" {
				t.Errorf("transparent must paint nothing, got %q", dark)
			}
			continue
		}
		if dark == "" || theme.BackgroundHex(n, true) == "" {
			t.Errorf("background %q has no surface", n)
		}
	}
	if got := theme.BackgroundHex("nope", false); got != "" {
		t.Errorf("unknown background should paint nothing, got %q", got)
	}
	// A light pitago palette (catppuccin-latte) must not get catppuccin's
	// dark surface.
	m := New(nil, t.TempDir())
	m.Background = "catppuccin"
	m.ThemeName = "catppuccin-latte"
	if bg := m.bgColor(); bg != "#eff1f5" {
		t.Fatalf("light palette background = %q, want #eff1f5", bg)
	}
}

func TestSetTheme(t *testing.T) {
	defer ApplyTheme(theme.Get("default")) // globals: don't leak into other tests
	m := New(nil, t.TempDir())
	m.SetTheme("gruvbox")
	if m.ThemeName != "gruvbox" {
		t.Fatalf("want gruvbox, got %q", m.ThemeName)
	}
	if cText != lipgloss.Color("#EBDBB2") {
		t.Fatalf("cText not swapped: %q", string(cText))
	}
	m.SetTheme("nope-unknown")
	if m.ThemeName != "default" {
		t.Fatalf("unknown should fall back to default, got %q", m.ThemeName)
	}
}

// The standalone theme picker is gone: /theme opens the hub's Theme
// section, right pane focused and listing every palette.
func TestOpenThemeLandsInHub(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ThemeName = "dracula"
	m.OpenThemeSettings()
	d := m.Dialogs[0]
	if d.Kind != "pconfig" {
		t.Fatalf("want the settings hub, got %q", d.Kind)
	}
	if d.CurPsec() != PsecTheme {
		t.Fatalf("want the Theme section, got %q", d.CurPsec())
	}
	if d.ProvFocus {
		t.Error("themes should be browsable immediately (right pane focused)")
	}
	// The right pane lists every palette, then every background: two
	// columns of one row list.
	if len(d.Options) != len(theme.Names())+len(theme.Backgrounds()) {
		t.Fatalf("section lists %d rows, want %d palettes + %d backgrounds",
			len(d.Options), len(theme.Names()), len(theme.Backgrounds()))
	}
	found := false
	for i, n := range d.Options {
		switch {
		case i < len(theme.Names()):
			if n == "dracula" {
				found = true
				if !strings.Contains(d.Descs[i], "✓ current") {
					t.Errorf("active theme row should be marked: %q", d.Descs[i])
				}
			}
			if d.Payload[i] != "theme:"+n {
				t.Errorf("row %q payload = %q, want theme:%s", n, d.Payload[i], n)
			}
		default:
			if d.Payload[i] != "bg:"+n {
				t.Errorf("background row %q payload = %q, want bg:%s", n, d.Payload[i], n)
			}
		}
	}
	if !found {
		t.Fatal("the active theme should be in its own list")
	}
}

func TestThemeLivePreviewOnCursorMove(t *testing.T) {
	defer ApplyTheme(theme.Get("default")) // globals: don't leak into other tests
	m := New(nil, t.TempDir())
	m.ThemeName = "default"
	m.OpenThemeSettings()
	got := m
	got.Dialogs[0].Cursor = 0 // first palette row
	nm, _ := got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, got.Dialogs[0])
	got = nm.(Model)
	if got.ThemeName != "one-dark" {
		t.Fatalf("Down should live-apply one-dark, got %q", got.ThemeName)
	}
	if cText != lipgloss.Color("#ABB2BF") {
		t.Fatalf("cText not swapped: %q", string(cText))
	}
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyUp}, got.Dialogs[0])
	if nm.(Model).ThemeName != "default" {
		t.Fatalf("Up should wrap back to default, got %q", nm.(Model).ThemeName)
	}
	// Stepping down from the last palette must land on a background, not
	// keep walking the palette column: → focuses the background column.
	got = nm.(Model)
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyRight}, got.Dialogs[0])
	got = nm.(Model)
	if !got.Dialogs[0].BgFocus {
		t.Fatal("→ should focus the background column")
	}
	bgBefore := got.Background
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, got.Dialogs[0])
	got = nm.(Model)
	if !got.Dialogs[0].BgFocus {
		t.Error("↓ must keep the cursor in the background column")
	}
	ri := got.Dialogs[0].FIdx[got.Dialogs[0].Cursor]
	if !strings.HasPrefix(got.Dialogs[0].Payload[ri], "bg:") {
		t.Fatalf("cursor landed on %q, want a background row", got.Dialogs[0].Options[ri])
	}
	if got.Background == bgBefore {
		t.Error("browsing the background column should preview it live")
	}
	// Browsing another section must not preview.
	got = nm.(Model)
	got.Dialogs[0].Cursor = 0
	got.Dialogs[0].Filter = "zzz" // filter the Theme rows out
	got.Dialogs[0].Reindex()
	before := got.ThemeName
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, got.Dialogs[0])
	if nm.(Model).ThemeName != before {
		t.Errorf("empty list must not change the theme, got %q", nm.(Model).ThemeName)
	}
}

// Each Theme column keeps its own row: → then ↓ in the backgrounds must
// not lose the palette you were on, and ← lands back on it.
func TestThemeColumnsRememberTheirRow(t *testing.T) {
	defer ApplyTheme(theme.Get("default"))
	m := New(nil, t.TempDir())
	m.OpenThemeSettings()
	got := m
	d := got.Dialogs[0]
	for range 3 { // walk down to a palette row deep enough to matter
		nm, _ := got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, got.Dialogs[0])
		got = nm.(Model)
	}
	d = got.Dialogs[0]
	if d.Cursor != 3 {
		t.Fatalf("setup: cursor at row %d, want 3", d.Cursor)
	}
	nm, _ := got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyRight}, d)
	got = nm.(Model)
	for range 4 { // wander down the background column
		nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyDown}, got.Dialogs[0])
		got = nm.(Model)
	}
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyLeft}, got.Dialogs[0])
	got = nm.(Model)
	if got.Dialogs[0].BgFocus {
		t.Fatal("← should leave the background column")
	}
	if ri := got.Dialogs[0].FIdx[got.Dialogs[0].Cursor]; ri != 3 {
		t.Fatalf("← landed on row %d, want the palette row 3 it was left on", ri)
	}
	// → returns to the background row, not back to its top.
	nm, _ = got.updatePconfigDialog(tea.KeyMsg{Type: tea.KeyRight}, got.Dialogs[0])
	got = nm.(Model)
	if ri := got.Dialogs[0].FIdx[got.Dialogs[0].Cursor]; !strings.HasPrefix(got.Dialogs[0].Payload[ri], "bg:") {
		t.Fatalf("→ landed on %q, want a background row", got.Dialogs[0].Options[ri])
	}
}
