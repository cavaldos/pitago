package app

import (
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Hub-assigned Alt shortcuts for /commands (Skills/Prompts/Extensions tabs).
// Alt+key is the only space: Ctrl+letters belong to the input (Emacs keys)
// or existing actions, while Alt is free except Alt+M (mouse) and Alt+1..5
// (recent models). Persisted in prefs.json (CmdShortcuts: name → "alt+x").

// shortcutKind is the capture dialog Kind (Dialog.ShortcutCmd = target).
const shortcutKind = "cmdshortcut"

// shortcutLabelOf maps Alt+single-letter/digit to its "alt+x" label.
func shortcutLabelOf(km tea.KeyMsg) (string, bool) {
	if !km.Alt || km.Type != tea.KeyRunes || len(km.Runes) != 1 {
		return "", false
	}
	r := km.Runes[0]
	if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
		return "", false
	}
	return "alt+" + string(unicode.ToLower(r)), true
}

// shortcutReserved reports labels the TUI already owns (never assignable).
func shortcutReserved(label string) bool {
	if label == "alt+m" {
		return true
	}
	if len(label) == 5 && strings.HasPrefix(label, "alt+") {
		if c := label[4]; c >= '1' && c <= '5' {
			return true
		}
	}
	return false
}

// shortcutDisplay renders "alt+x" as the row suffix (⌥X).
func shortcutDisplay(label string) string {
	if cut, ok := strings.CutPrefix(label, "alt+"); ok && cut != "" {
		return "⌥" + strings.ToUpper(cut)
	}
	return label
}

// shortcutForCmd reports the assigned label for a /command ("" = none).
func (m Model) shortcutForCmd(cmd string) string {
	return m.CmdShortcuts[cmd]
}

// findCmdShortcut reverse-looks-up a label to its /command.
func (m Model) findCmdShortcut(label string) (string, bool) {
	for cmd, l := range m.CmdShortcuts {
		if l == label {
			return cmd, true
		}
	}
	return "", false
}

// hasCmd reports a /command still runnable — a local builtin or a row in
// the pi catalog (uninstalled ones keep their shortcut row stale instead
// of firing into "unknown command").
func (m Model) hasCmd(cmd string) bool {
	for _, b := range m.builtins {
		if b.Name == cmd && !b.Hidden {
			return true
		}
	}
	for _, c := range m.Cmds {
		if c.Name == cmd {
			return true
		}
	}
	return false
}

// cmdHubRow is one Commands-tab row: the command name, its one-line
// description and the origin tag shown in brackets.
type cmdHubRow struct {
	name, desc, tag string
}

// builtinCmdRows lists the Commands section: pi's re-implemented builtins
// plus pitago's own commands, sorted by origin then name. Only the local
// registry (builtin.All) is read — extension/prompt/skill commands never
// appear here; they own the Skills/Prompts/Extensions sections.
func (m Model) builtinCmdRows() []cmdHubRow {
	out := make([]cmdHubRow, 0, len(m.builtins))
	for _, b := range m.builtins {
		if b.Hidden {
			continue // continuation entry, not a user command
		}
		tag := b.Origin
		if tag == "" {
			tag = "pi"
		}
		out = append(out, cmdHubRow{name: b.Name, desc: b.Desc, tag: tag})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].tag != out[j].tag {
			return out[i].tag < out[j].tag
		}
		return out[i].name < out[j].name
	})
	return out
}

// saveCmdShortcuts persists the map (LoadPrefs-mutate-SavePrefs parity).
func (m *Model) saveCmdShortcuts() {
	if m.CmdShortcuts == nil {
		m.CmdShortcuts = map[string]string{}
	}
	prefs := LoadPrefs(m.prefsPath)
	prefs.CmdShortcuts = m.CmdShortcuts
	_ = SavePrefs(m.prefsPath, prefs)
}

// setCmdShortcut assigns label to cmd, stealing it from any previous owner.
func (m *Model) setCmdShortcut(cmd, label string) {
	if m.CmdShortcuts == nil {
		m.CmdShortcuts = map[string]string{}
	}
	for other, l := range m.CmdShortcuts {
		if other != cmd && l == label {
			delete(m.CmdShortcuts, other)
		}
	}
	m.CmdShortcuts[cmd] = label
	m.saveCmdShortcuts()
}

// clearCmdShortcut drops cmd's assignment (nil-op when none).
func (m *Model) clearCmdShortcut(cmd string) {
	if _, ok := m.CmdShortcuts[cmd]; !ok {
		return
	}
	delete(m.CmdShortcuts, cmd)
	m.saveCmdShortcuts()
}

// openShortcutCapture pushes the assign dialog over the hub: press Alt+key
// to assign, ⌫ to clear, Esc to cancel.
func (m *Model) openShortcutCapture(cmd string) {
	cur := m.shortcutForCmd(cmd)
	msg := "Press Alt+letter/digit to assign · ⌫ clears · Esc cancels"
	if cur != "" {
		msg = "Now " + shortcutDisplay(cur) + " · " + msg
	}
	d := &Dialog{Kind: shortcutKind, Title: "Shortcut — /" + cmd,
		Message: msg, ShortcutCmd: cmd}
	m.Dialogs = append([]*Dialog{d}, m.Dialogs...)
	m.Refresh()
}

// updateShortcutDialog runs the capture dialog (no filter/confirm shape).
func (m Model) updateShortcutDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	cmd := d.ShortcutCmd
	pop := func() {
		m.Dialogs = m.Dialogs[1:]
		m.reloadHubRows("")
		m.refreshPiTasks()
		m.Refresh()
	}
	switch km.Type {
	case tea.KeyEsc:
		pop()
		return m, m.ReconcileTurnCmd()
	case tea.KeyBackspace, tea.KeyDelete:
		m.clearCmdShortcut(cmd)
		m.AddBlock(Block{Kind: "notice", Text: "/" + cmd + ": shortcut cleared"})
		pop()
		return m, m.ReconcileTurnCmd()
	}
	if label, ok := shortcutLabelOf(km); ok {
		if shortcutReserved(label) {
			m.AddBlock(Block{Kind: "notice", Text: shortcutDisplay(label) + " is taken (mouse / recent models)"})
			m.Refresh()
			return m, nil
		}
		m.setCmdShortcut(cmd, label)
		m.AddBlock(Block{Kind: "notice", Text: shortcutDisplay(label) + " → /" + cmd})
		pop()
		return m, m.ReconcileTurnCmd()
	}
	return m, nil // anything else: stay open, wait for Alt+key
}

// renderShortcutDialog draws the capture box (same centered style).
func (m Model) renderShortcutDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	cur := m.shortcutForCmd(d.ShortcutCmd)
	now := "— none —"
	if cur != "" {
		now = shortcutDisplay(cur)
	}
	b.WriteString("\n")
	b.WriteString("  " + toolStyle.Render("current: ") + lipgloss.NewStyle().Foreground(cText).Render(now) + "\n")
	b.WriteString("\n" + toolStyle.Render("Alt+key assign · ⌫ clear · Esc cancel"))
	box := dlgStyle.Width(62).Render(b.String())
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		"",
	)
}
