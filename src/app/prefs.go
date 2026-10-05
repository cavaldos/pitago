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
	Pet             string            `json:"pet,omitempty"`             // sidebar ASCII pet name (missing = default "cat")
	PetStyle        string            `json:"petStyle,omitempty"`        // "ascii" (default) | "classic" kaomoji look
	CurrentModel    *ModelRef         `json:"currentModel,omitempty"`    // last-picked model, restored at spawn only
	Side            map[string]bool   `json:"side,omitempty"`            // sidebar section key → visible (missing = default)
	CmdShortcuts    map[string]string `json:"cmdShortcuts,omitempty"`    // /command name → "alt+x" (hub-assigned, Alt+key fires it)
	RecentCmds      []string          `json:"recentCmds,omitempty"`      // last-run /command names, most recent first (floated to the top of the "/" popup)
	SuggestPlugins  []string          `json:"suggestPlugins,omitempty"`  // extra packages suggested in the hub Plugins tab (Ctrl+F), bare npm names
	Tidy            bool              `json:"tidy,omitempty"`            // tidy mode: tool blocks collapse to their header (no args detail, no result/diff/output)
	Background      string            `json:"background,omitempty"`      // app surface: an opencode background slug, "" = the terminal's own
	TaskWidgetOff   bool              `json:"taskWidgetOff,omitempty"`   // hide the above-editor task widget
	ShellBinary     string            `json:"shellBinary,omitempty"`     // shell mode program: "$SHELL" (default) or an absolute path; "" = follow $SHELL
}

// TaskWidgetVisible reports whether the above-editor task widget should paint.
// Default is on: that is upstream behaviour, and the widget carries live
// elapsed time and token counts per task that the sidebar Todos panel does
// not. Off is opt-in because the same todos are already mirrored into the
// sidebar, and the widget takes chat rows right above the input.
func (p Prefs) TaskWidgetVisible() bool { return !p.TaskWidgetOff }

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
// toggled: MCP + Plugins + Commands start hidden, everything else shows
// (including LSP, so diagnostics are visible without hunting for a toggle).
func DefaultSideVisible(key string) bool {
	switch key {
	case SidePlugins, SideMCP, SideCommands, SideSubagents:
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
