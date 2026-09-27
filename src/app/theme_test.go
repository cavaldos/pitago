package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/theme"
)

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
	if len(d.Options) != len(theme.Names()) {
		t.Fatalf("section lists %d themes, want %d", len(d.Options), len(theme.Names()))
	}
	// The active theme is marked, and every row carries a runnable payload.
	found := false
	for i, n := range d.Options {
		if n == "dracula" {
			found = true
			if !strings.Contains(d.Descs[i], "✓ current") {
				t.Errorf("active theme row should be marked: %q", d.Descs[i])
			}
		}
		if d.Payload[i] != "theme:"+n {
			t.Errorf("row %q payload = %q, want theme:%s", n, d.Payload[i], n)
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
	got.Dialogs[0].Cursor = 0
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
