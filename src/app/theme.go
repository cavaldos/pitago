package app

import (
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/theme"
)

// applyTheme swaps the palette + persists + refreshes input styles.
// Quiet: no chat notice (used for live preview while browsing the picker).
func (m *Model) applyTheme(name string) {
	t := theme.Get(name)
	ApplyTheme(t)
	m.ThemeName = t.Name
	_ = theme.Save(m.themePath, t.Name)
	m.ta.FocusedStyle.Prompt = lipgloss.NewStyle().Foreground(cInput)
	m.ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(cMuted)
	m.ta.BlurredStyle.Prompt = lipgloss.NewStyle().Foreground(cMuted)
}

// SetTheme applies a palette by name, persists it to theme.json, refreshes
// the input styles (built once in New) and posts a toast popup.
func (m *Model) SetTheme(name string) {
	m.applyTheme(name)
	m.AddBlock(Block{Kind: "notice", Text: "theme → " + m.ThemeName})
	m.Refresh()
}

// previewTheme live-applies the highlighted row of the hub's Theme
// section (no-op for any other section, or when already on that theme).
func (m *Model) previewTheme(d *Dialog) {
	if d.Kind != "pconfig" || d.CurPsec() != PsecTheme || len(d.FIdx) == 0 {
		return
	}
	if d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return
	}
	ri := d.FIdx[d.Cursor]
	if ri < 0 || ri >= len(d.Options) || d.Options[ri] == m.currentTheme() {
		return
	}
	m.applyTheme(d.Options[ri])
	m.Refresh()
}

// currentTheme is the active palette name, always resolved ("" means the
// row list has nothing to mark as current).
func (m Model) currentTheme() string {
	return m.ThemeName
}

// OpenThemeSettings opens the settings hub on the Theme section. The
// standalone theme picker is gone — /theme lands here.
func (m *Model) OpenThemeSettings() {
	m.OpenHubSection(PsecTheme)
}
