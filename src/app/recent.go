package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/recent"
)

// Sidebar section row budgets above the RECENT MODELS block: each visible
// block contributes a fixed number of content rows (header + lines +
// separator). recentAt/pluginToggleAt derive click rows from the same
// budgets, so hiding a section keeps clicks in sync with the render —
// guarded by TestRecentAtMatchesRender.
const (
	sideSessionRows = 5 // SESSION header + first + sess + file + sep
	sideModelRows   = 4 // model + ctx + toks + sep (before the level list)
	sideStatsRows   = 9 // header + 7 stat lines + sep
)

// sideModelRows is the SideModel row budget: the fixed four rows plus one
// row per thinking level rendered under the model row. It is derived, not
// fixed, because the level list arrives asynchronously (GetLevels) and is
// empty until it does — an empty list simply means no extra rows.
func (m Model) sideModelRows() int {
	return sideModelRows + len(m.thinkLevelLines(sideInnerW))
}

// sideRowsBeforeRecent counts the sidebar content rows above the RECENT
// MODELS header from the sections currently visible.
func (m Model) sideRowsBeforeRecent() int {
	n := 0
	if m.SideVisible(SidePet) {
		n += m.petRows() // style-dependent: ascii 5, classic 2
	}
	if m.SideVisible(SideSession) {
		n += sideSessionRows
	}
	if m.SideVisible(SideModel) {
		n += m.sideModelRows()
	}
	if m.SideVisible(SideStats) {
		n += sideStatsRows
	}
	if m.SideVisible(SideCost) {
		if r := m.sideCostRows(); len(r) > 0 {
			n += 2 + len(r) // COST header + rows + sep
		}
	}
	return n
}

// recentContentRow is the sidebar content row of the first recent model.
// Screen y = 1 (box border) + row; recentAt must match.
func (m Model) recentContentRow() int {
	return m.sideRowsBeforeRecent() + 1 // +1 for the RECENT MODELS header
}

// sideCostRows formats the sidebar COST breakdown: top-3 models by cost,
// only when the session spans more than one model (same rule as /session).
// Render and the click mapping both use it so the row math stays in sync.
func (m Model) sideCostRows() []string {
	if len(m.sessBreak) <= 1 {
		return nil
	}
	n := len(m.sessBreak)
	if n > 3 {
		n = 3
	}
	rows := make([]string, 0, n)
	for _, b := range m.sessBreak[:n] {
		rows = append(rows, Short(Short(b.Key, sideInnerW-8)+" "+fmt.Sprintf("$%.3f", b.Cost), sideInnerW))
	}
	return rows
}

// pushRecent moves (provider, id) to the front, dedupes, caps, persists.
// Ordering/cap live in components/recent; empty ids stay ignored (no write).

func (m *Model) pushRecent(provider, id, label string) {
	if strings.TrimSpace(id) == "" {
		return
	}
	m.recentModels = recent.Push(m.recentModels, provider, id, label)
	recent.Save(m.recentPath, m.recentModels)
}

// openRecents shows the recent-models picker (Ctrl+R / /recent).

func (m *Model) OpenRecents() tea.Cmd {
	if len(m.recentModels) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "no recent models yet — switch with /model first"})
		m.Refresh()
		return nil
	}
	opts := make([]string, 0, len(m.recentModels))
	descs := make([]string, 0, len(m.recentModels))
	for _, r := range m.recentModels {
		opts = append(opts, r.DispLabel())
		desc := r.Provider
		if r.ID == m.ModelLbl || r.DispLabel() == m.ModelLbl {
			desc = "current"
			if r.Provider != "" {
				desc += " · " + r.Provider
			}
		}
		descs = append(descs, desc)
	}
	d := &Dialog{Kind: "recent", Title: "Recent models", Options: opts, Descs: descs}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}

// switchToRecent switches to recentModels[idx] (resolving provider via
// GetModels when the entry only has a label). Result reuses ModelCycleMsg.

func (m *Model) SwitchToRecent(idx int) tea.Cmd {
	if idx < 0 || idx >= len(m.recentModels) {
		return nil
	}
	r := m.recentModels[idx]
	m.Status = "switching model…"
	m.Refresh()
	return func() tea.Msg {
		prov, id := r.Provider, r.ID
		if prov == "" {
			models, err := m.Pi.GetModels()
			if err != nil {
				return ModelCycleMsg{Err: err}
			}
			found := false
			for _, mi := range models {
				if mi.ID == r.ID || mi.Name == r.ID || mi.Name == r.DispLabel() {
					prov, id = mi.Provider, mi.ID
					if id == "" {
						id = mi.Name
					}
					found = true
					break
				}
			}
			if !found {
				return ModelCycleMsg{Err: fmt.Errorf("model %q not in pi model list", r.DispLabel())}
			}
		}
		label, err := m.Pi.SetModelByID(prov, id)
		return ModelCycleMsg{Label: label, Provider: prov, ID: id, Err: err}
	}
}

// firstUser returns the first user message text (session title line).

func (m Model) recentAt(x, y int) (int, bool) {
	if !m.ready || !m.showSide() || len(m.Dialogs) > 0 || len(m.recentModels) == 0 {
		return 0, false
	}
	if !m.SideVisible(SideRecent) {
		return 0, false
	}
	if x < m.mainW() || x > m.winW || y < 1+m.recentContentRow() {
		return 0, false
	}
	// visible rows start at YOffset when the sidebar is scrolled
	idx := y - (1 + m.recentContentRow()) + m.sideVp.YOffset
	if idx < 0 || idx >= len(m.recentModels) {
		return 0, false
	}
	return idx, true
}

// pluginHeaderRow is the sidebar content row of the PLUGINS toggle header,
// counted from the visible sections above it: the RECENT block (header +
// models + hint + sep) and the COMMANDS block (header + count rows) only
// contribute when visible. Everything after it (MCP/Todos/WORKSPACE)
// doesn't affect the row.
func (m Model) pluginHeaderRow() int {
	r := len(m.recentModels)
	if r == 0 {
		r = 1 // empty state renders one "—" row
	}
	c := 1 // COMMANDS counts (or "—")
	if len(m.queue.Steering)+len(m.queue.FollowUp) > 0 {
		c++ // queue line
	}
	n := m.sideRowsBeforeRecent()
	if m.SideVisible(SideRecent) {
		n += 1 + r + 2
	}
	if m.SideVisible(SideCommands) {
		n += 1 + c
	}
	return n
}

// pluginToggleAt reports a click on the PLUGINS header (collapses/expands
// the list). Same screen→content mapping as recentAt (box border 1,
// plus YOffset when scrolled).
func (m Model) pluginToggleAt(x, y int) bool {
	if !m.ready || !m.showSide() || len(m.Dialogs) > 0 {
		return false
	}
	if !m.SideVisible(SidePlugins) {
		return false
	}
	if x < m.mainW() || x > m.winW {
		return false
	}
	return y-1+m.sideVp.YOffset == m.pluginHeaderRow()
}

// recentHint advertises click-switch only when the terminal reports mouse
// events (--mouse); otherwise clicks never reach the app, so show keys.
// Sidebar scrolls with Ctrl+↑↓ (Alt+↑↓ also works, wheel with --mouse).
func (m Model) recentHint() string {
	if m.Mouse {
		return "click to switch · ^↑↓ scroll"
	}
	return "^R list · ^↑↓ scroll"
}

// command palette (/ autocomplete) --------------------------------------------

// cmdPrefix returns the text after / when the input is an unfinished command.
