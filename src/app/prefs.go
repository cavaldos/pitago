package app

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Pitago-local TUI prefs (~/.config/pitago/prefs.json, 0600 like theme.json):
// display toggles pi stores in its own TUI but pitago renders itself.
// HideThinking mirrors pi's "Hide thinking"; AutocompleteMax mirrors pi's
// "Autocomplete max items" (applied to the / popup window).

// Prefs is the persisted pitago-local display prefs (zero = pi defaults).
type Prefs struct {
	HideThinking    bool              `json:"hideThinking,omitempty"`
	AutocompleteMax int               `json:"autocompleteMax,omitempty"`
	Side            map[string]bool   `json:"side,omitempty"` // sidebar section key → visible (missing = default)
	CmdShortcuts    map[string]string `json:"cmdShortcuts,omitempty"` // /command name → "alt+x" (hub-assigned, Alt+key fires it)
}

// PrefsPath is ~/.config/pitago/prefs.json ("" when unresolvable).
func PrefsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "pitago", "prefs.json")
}

// PrefsPath returns this Model's prefs file path ("" = don't persist).
func (m *Model) PrefsPath() string { return m.prefsPath }

// LoadPrefs reads prefs (zero Prefs when missing/unparseable).
func LoadPrefs(path string) Prefs {
	var p Prefs
	if path == "" {
		return p
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return p
	}
	_ = json.Unmarshal(raw, &p)
	return p
}

// SavePrefs writes prefs (nil-op on empty path; 0600 like theme.json).
func SavePrefs(path string, p Prefs) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, _ := json.Marshal(p)
	return os.WriteFile(path, raw, 0o600)
}

// DefaultSideVisible is the sidebar visibility for sections the user never
// toggled: MCP + Plugins + Commands start hidden, everything else shows.
func DefaultSideVisible(key string) bool {
	switch key {
	case SidePlugins, SideMCP, SideCommands:
		return false
	}
	return true
}

// SideVisible reports one sidebar section's persisted visibility.
func (p Prefs) SideVisible(key string) bool {
	if p.Side != nil {
		if v, ok := p.Side[key]; ok {
			return v
		}
	}
	return DefaultSideVisible(key)
}

// AutocompleteMax returns the effective popup max (pi default 5, pitago
// default 10 when unset; pi allows 3-20).
func (p Prefs) EffectiveAutocompleteMax() int {
	if p.AutocompleteMax < 3 || p.AutocompleteMax > 20 {
		return 10
	}
	return p.AutocompleteMax
}
