package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/ext"
)

// SubagentsNone — canonical value lives in ext (pi-extension domain).
const SubagentsNone = ext.SubagentsNone

// SubagentInfo — canonical type lives in ext; alias keeps call sites green.
type SubagentInfo = ext.SubagentInfo

// DiscoverSubagents lists effective subagents: project > user > package >
// DiscoverSubagents — canonical discovery lives in ext (pi-extension
// domain); this keeps the (cwd) signature used by UI/tests.
func DiscoverSubagents(cwd string) []SubagentInfo {
	return ext.DiscoverSubagents(cwd, piAgentDir())
}

// scanAgentDir — canonical impl in ext.
func scanAgentDir(dir, source string) []SubagentInfo { return ext.ScanAgentDir(dir, source) }

// parseAgentFrontmatter — canonical impl in ext.
func parseAgentFrontmatter(raw string) (name, desc, model string) {
	return ext.ParseAgentFrontmatter(raw)
}

// CurrentSubagent is the last-picked subagent (persisted in prefs.json).
func (m *Model) CurrentSubagent() string {
	if m.CurAgent != "" {
		return m.CurAgent
	}
	return LoadPrefs(m.prefsPath).CurrentSubagent
}

// SetCurrentSubagent persists the picked subagent.
func (m *Model) SetCurrentSubagent(name string) {
	m.CurAgent = name
	prefs := LoadPrefs(m.prefsPath)
	prefs.CurrentSubagent = name
	_ = SavePrefs(m.prefsPath, prefs)
}

// subagentDesc — canonical impl in ext.
func subagentDesc(a SubagentInfo, current string) string { return ext.Desc(a, current) }

// OpenSubagents shows the native subagent picker (/subagents). RPC mode
// can't render pi-subagents' custom UI (ui.custom returns undefined), so
// the extension command silently does nothing — this replaces it. Enter
// selects (see confirmSubagents in src/builtin); /subagents <name> picks
// directly.
func (m *Model) OpenSubagents(arg string) tea.Cmd {
	agents := DiscoverSubagents(m.cwd)
	if len(agents) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no subagents found — pi install npm:pi-subagents"})
		m.Refresh()
		return nil
	}
	cur := m.CurrentSubagent()
	if a := strings.TrimSpace(arg); a != "" {
		switch strings.ToLower(a) {
		case "off", "none", "clear", "--none", "exit", "quit":
			m.SetCurrentSubagent("")
			m.AddBlock(Block{Kind: "notice", Text: "subagent off → back to initial state"})
			m.Refresh()
			return nil
		}
		for _, ag := range agents {
			if strings.EqualFold(ag.Name, a) {
				m.SetCurrentSubagent(ag.Name)
				m.AddBlock(Block{Kind: "notice", Text: "subagent → " + ag.Name + " [" + ag.Source + "]"})
				m.Refresh()
				return nil
			}
		}
		m.AddBlock(Block{Kind: "notice", Text: "subagent '" + a + "' not found", Err: true})
		m.Refresh()
		return nil
	}
	// First row clears the selection (no agent picked).
	noneDesc := "turn off subagent · back to initial state"
	if cur == "" {
		noneDesc = "● current · " + noneDesc
	}
	opts := make([]string, 0, len(agents)+1)
	descs := make([]string, 0, len(agents)+1)
	opts = append(opts, SubagentsNone)
	descs = append(descs, noneDesc)
	for _, ag := range agents {
		opts = append(opts, ag.Name)
		descs = append(descs, subagentDesc(ag, cur))
	}
	d := &Dialog{Kind: "subagents", Title: "Subagents",
		Message: "↑↓ pick · Enter select · type to filter · Esc close · ● = current",
		Options: opts, Descs: descs}
	d.Reindex()
	// Preselect the current agent so it's visible on open.
	for i, ri := range d.FIdx {
		if d.Options[ri] == cur {
			d.Cursor = i
			break
		}
	}
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}
