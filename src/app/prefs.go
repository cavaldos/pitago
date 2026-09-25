package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// Pitago-local TUI prefs (~/.config/pitago/prefs.json, 0600 like theme.json):
// display toggles pi stores in its own TUI but pitago renders itself.
// HideThinking mirrors pi's "Hide thinking"; AutocompleteMax mirrors pi's
// "Autocomplete max items" (applied to the / popup window).

// Prefs is the persisted pitago-local display prefs (zero = pi defaults).
type Prefs struct {
	HideThinking    bool              `json:"hideThinking,omitempty"`
	AutocompleteMax int               `json:"autocompleteMax,omitempty"`
	CurrentSubagent string            `json:"currentSubagent,omitempty"` // last-picked /subagents entry (● marker)
	ModelProvider   string            `json:"modelProvider,omitempty"` // last explicitly chosen model: restored after /new (pi resets to default)
	ModelID         string            `json:"modelID,omitempty"`       // model id paired with ModelProvider ("" = never switched)
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

// rememberModel persists the user's explicit model choice so /new can
// restore it (pi resets to its own default on NewSession). Blank provider/id
// resolve via the recent list (the cycle path only reports a label).
func (m *Model) rememberModel(provider, id, label string) {
	if provider == "" || id == "" {
		for _, r := range m.recentModels {
			if r.ID == label || r.DispLabel() == label || (id != "" && r.ID == id) {
				if provider == "" {
					provider = r.Provider
				}
				if id == "" {
					id = r.ID
				}
			}
		}
	}
	if strings.TrimSpace(id) == "" {
		return
	}
	p := LoadPrefs(m.prefsPath)
	p.ModelProvider, p.ModelID = provider, id
	_ = SavePrefs(m.prefsPath, p)
}

// savedModel returns the persisted model choice ("", "" = never switched).
func (m *Model) savedModel() (provider, id string) {
	p := LoadPrefs(m.prefsPath)
	return p.ModelProvider, p.ModelID
}

// resolveSavedModel fills a blank provider via GetModels (the SwitchToRecent
// rule). Nil Pi or lookup failure returns the input unchanged.
func (m *Model) resolveSavedModel(prov, id string) (string, string) {
	if prov != "" || m.Pi == nil {
		return prov, id
	}
	if models, err := m.Pi.GetModels(); err == nil {
		for _, mi := range models {
			if mi.ID == id || mi.Name == id {
				prov = mi.Provider
				if mi.ID != "" {
					id = mi.ID
				} else {
					id = mi.Name
				}
				break
			}
		}
	}
	return prov, id
}

// ApplySavedModel re-applies the persisted model choice at startup. Call once
// after Configure and before the program starts; explicit --provider/--model
// flags win (the caller skips when flags are set). Returns the applied label
// ("" = nothing saved or restore failed; startup stays silent either way).
func (m *Model) ApplySavedModel() string {
	prov, id := m.savedModel()
	if strings.TrimSpace(id) == "" || m.Pi == nil {
		return ""
	}
	prov, id = m.resolveSavedModel(prov, id)
	label, err := m.Pi.SetModelByID(prov, id)
	if err != nil {
		return ""
	}
	if label == "" {
		label = id
	}
	m.ModelLbl = label
	m.pushRecent(prov, id, label)
	return label
}

// restoreModelCmd re-applies the persisted choice after /new. Unresolvable
// entries stay silent so a removed model never blocks the fresh session.
func (m *Model) restoreModelCmd(provider, id string) tea.Cmd {
	return func() tea.Msg {
		prov, id := m.resolveSavedModel(provider, id)
		if strings.TrimSpace(id) == "" {
			return nil
		}
		label, err := m.Pi.SetModelByID(prov, id)
		return ModelCycleMsg{Label: label, Provider: prov, ID: id, Restored: true, Err: err}
	}
}
