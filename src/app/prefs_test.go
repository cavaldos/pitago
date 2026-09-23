package app

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPrefsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "prefs.json")
	if err := SavePrefs(path, Prefs{HideThinking: true, AutocompleteMax: 5,
		Side: map[string]bool{"mcp": true, "plugins": false}}); err != nil {
		t.Fatalf("save: %v", err)
	}
	p := LoadPrefs(path)
	if !p.HideThinking || p.AutocompleteMax != 5 {
		t.Errorf("round trip failed: %+v", p)
	}
	if !p.SideVisible("mcp") || p.SideVisible("plugins") {
		t.Errorf("side round trip failed: %+v", p.Side)
	}
	if got := (Prefs{}).EffectiveAutocompleteMax(); got != 10 {
		t.Errorf("unset max should be pitago default 10, got %d", got)
	}
	if got := (Prefs{AutocompleteMax: 99}).EffectiveAutocompleteMax(); got != 10 {
		t.Errorf("out-of-range max should clamp to 10, got %d", got)
	}
}

func TestSideDefaults(t *testing.T) {
	var p Prefs // nothing ever toggled: MCP + Plugins + Commands hide, the rest show
	for _, k := range []string{"mcp", "plugins", "commands"} {
		if p.SideVisible(k) {
			t.Errorf("%s should hide by default", k)
		}
	}
	for _, k := range []string{"pet", "session", "model", "stats", "cost", "recent", "todos", "workspace"} {
		if !p.SideVisible(k) {
			t.Errorf("%s should show by default", k)
		}
	}
}

func TestHideThinkingSkipsBlocks(t *testing.T) {
	m := &Model{}
	m.AddBlock(Block{Kind: "thinking", Text: "hmm"})
	m.AddBlock(Block{Kind: "assistant", Text: "done"})
	if got := m.renderBlocks(); !strings.Contains(got, "hmm") {
		t.Fatal("thinking should render by default")
	}
	m.HideThinking = true
	got := m.renderBlocks()
	if strings.Contains(got, "hmm") {
		t.Error("thinking should be hidden")
	}
	if !strings.Contains(got, "done") {
		t.Error("assistant text must still render")
	}
}

func TestSettingsMsgPassesDialog(t *testing.T) {
	m := &Model{}
	m.Dialogs = []*Dialog{{Kind: "settings", Title: "Agent settings",
		Options: []string{"Steering: one-at-a-time"}, Descs: []string{"x"}}}
	mm, _ := m.Update(SettingsMsg{Opts: []string{"Steering: all"}, Descs: []string{"y"}})
	if got := mm.(Model).Dialogs[0].Options[0]; got != "Steering: all" {
		t.Errorf("settings rows should refresh behind the dialog, got %q", got)
	}
}
