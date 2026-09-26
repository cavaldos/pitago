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
//
// CurrentModel is the last model the user explicitly picked (global, not
// per-project): one nested key so prefs.json never grows flat model keys.
type ModelRef struct {
	Provider string `json:"provider,omitempty"`
	ID       string `json:"id,omitempty"`
}

// The model copy is safe because it has exactly one writer (SetCurrentModel,
// only from a ModelCycleMsg, i.e. a user action) and exactly one reader
// (main at process start, which turns it into --provider/--model on the pi
// child). It is never read back mid-session to render the footer — the
// label always comes from get_state — so it can no longer disagree with
// what pi is actually running. The thinking level stays out of prefs:
// unlike the model, pitago has no way to pass it to the pi child at spawn.

// Prefs is the persisted pitago-local display prefs (zero = pi defaults).
type Prefs struct {
	HideThinking    bool              `json:"hideThinking,omitempty"`
	AutocompleteMax int               `json:"autocompleteMax,omitempty"`
	CurrentSubagent string            `json:"currentSubagent,omitempty"` // last-picked /subagents entry (● marker)
	CurrentModel    *ModelRef         `json:"currentModel,omitempty"`    // last-picked model, restored at spawn only
	Side            map[string]bool   `json:"side,omitempty"`            // sidebar section key → visible (missing = default)
	CmdShortcuts    map[string]string `json:"cmdShortcuts,omitempty"`    // /command name → "alt+x" (hub-assigned, Alt+key fires it)
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

// CurrentModelRef returns the last-picked model (nil = none saved).
func (m *Model) CurrentModelRef() *ModelRef {
	if m.savedModel != nil {
		return m.savedModel
	}
	return LoadPrefs(m.prefsPath).CurrentModel
}

// SetCurrentModel persists the model the user just picked, so the next
// process start (and every respawn) can pass it to the pi child. Same
// LoadPrefs → mutate → SavePrefs dance as SetCurrentSubagent.
func (m *Model) SetCurrentModel(provider, id string) {
	if provider == "" && id == "" {
		return
	}
	ref := &ModelRef{Provider: provider, ID: id}
	m.savedModel = ref
	prefs := LoadPrefs(m.prefsPath)
	prefs.CurrentModel = ref
	_ = SavePrefs(m.prefsPath, prefs)
}

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
