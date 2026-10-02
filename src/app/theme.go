package app

import (
	"strings"

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

// SetBackground picks the app surface (hub → Theme → background column)
// and persists the slug to the global prefs. "" (Transparent) leaves the
// terminal background alone: pitago's default look.
func (m *Model) SetBackground(name string) {
	m.setBackground(name, true)
}

// setBackground is SetBackground with the persist step split off, so the
// live preview while browsing the column does not write prefs on every
// cursor move.
func (m *Model) setBackground(name string, persist bool) {
	if name != "" {
		name = strings.ToLower(strings.TrimSpace(name))
	}
	if m.Background == name {
		return
	}
	m.Background = name
	if persist {
		prefs := LoadPrefs(m.prefsPath)
		prefs.Background = name
		_ = SavePrefs(m.prefsPath, prefs)
	}
	m.Refresh()
}

// bgColor is the surface paintBG fills the frame with, or "" when the
// terminal background shows through. The light/dark variant follows the
// active palette, so a light theme never gets a dark surface.
func (m Model) bgColor() string {
	return theme.BackgroundHex(m.Background, theme.Get(m.ThemeName).Light)
}

// previewRow live-applies whatever the hub's Theme section highlights:
// a palette row swaps the whole palette, a background row repaints the
// frame (not persisted until Enter). No-op for any other section or row.
func (m *Model) previewRow(d *Dialog) {
	if d.Kind != "pconfig" || d.CurPsec() != PsecTheme || len(d.FIdx) == 0 {
		return
	}
	if d.Cursor < 0 || d.Cursor >= len(d.FIdx) {
		return
	}
	ri := d.FIdx[d.Cursor]
	if ri < 0 || ri >= len(d.Payload) {
		return
	}
	switch p := d.Payload[ri]; {
	case strings.HasPrefix(p, psecActBg):
		m.setBackground(strings.TrimPrefix(p, psecActBg), false)
	case strings.HasPrefix(p, "theme:"):
		if name := strings.TrimPrefix(p, "theme:"); name != m.currentTheme() {
			m.applyTheme(name)
			m.Refresh()
		}
	}
}

// currentTheme is the active palette name, always resolved ("" means the
// row list has nothing to mark as current).
func (m Model) currentTheme() string {
	return m.ThemeName
}

// currentBackground is the picked surface ("" = the terminal background).
func (m Model) currentBackground() string {
	return m.Background
}

// OpenThemeSettings opens the settings hub on the Theme section. The
// standalone theme picker is gone — /theme lands here.
func (m *Model) OpenThemeSettings() {
	m.OpenHubSection(PsecTheme)
}
