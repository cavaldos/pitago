package app

import (
	"sort"
)

// ShortcutItem describes one keyboard shortcut, as listed by the settings
// hub's Shortcuts section.
type ShortcutItem struct {
	Key         string // e.g. "Ctrl+C" / "Ctrl+Shift+/"
	Description string // what it does
	Category    string // e.g. "Navigation", "Editing", "Global"
}

var shortcutsDb = []ShortcutItem{
	// Global / System
	{"Ctrl+C", "Quit (double-press within 3s)", "Global"},
	{"Esc", "Cancel running turn (double-press within 3s)", "Global"},
	{"Ctrl+Shift+/", "Open the Shortcuts section (/shortcuts)", "Global"},
	{"/?", "Open command palette", "Global"},
	{"Ctrl+D", "Detach from external Pi session (back to owned session)", "Global"},
	{"Ctrl+Q", "Detach from external Pi session (back to owned session)", "Global"},

	// Navigation / Chat
	{"↑↓", "Scroll chat (recall history when empty, mouse on)", "Chat"},
	{"Shift+↑↓", "Recall sent message (both mouse modes)", "Chat"},
	{"PgUp / PgDn", "Scroll chat (page)", "Chat"},
	{"Home / End", "Jump to top/bottom of chat", "Chat"},
	{"Ctrl+↑↓", "Scroll sidebar (no mouse)", "Sidebar"},
	{"Ctrl+PgUp / PgDn", "Scroll sidebar page", "Sidebar"},
	{"Ctrl+Home / End", "Jump sidebar top/bottom", "Sidebar"},

	// Commands / Palette
	{"/", "Open command palette", "Commands"},
	{"Tab", "Complete command in palette", "Commands"},
	{"↑↓", "Navigate command palette", "Commands"},
	{"Enter", "Send command / confirm", "Commands"},
	{"Esc", "Close palette / dialog", "Commands"},

	// Sidebar toggle / extras
	{"Ctrl+E", "Hide/show sidebar (Ctrl+B is taken by herdr)", "UI"},
	{"Ctrl+R", "Open recent models picker", "UI"},
	{"Ctrl+N", "New session", "UI"},
	{"Ctrl+P", "Cycle model", "UI"},
	{"Ctrl+Y", "Yank last assistant answer to clipboard", "UI"},
	{"Ctrl+Y", "Copy selected step / notification (in trajectory, /notification)", "UI"},
	{"Ctrl+V", "Paste (multi-backend)", "UI"},
	{"Ctrl+O", "Open yank picker", "UI"},

	// Tools / Thinking / Settings
	{"Ctrl+G", "Expand/collapse all tool blocks", "UI"},
	{"Ctrl+T", "Cycle thinking level", "UI"},
	{"Ctrl+F", "Star/unstar model (in picker)", "UI"},

	// Input / Tray
	{"Alt+Enter", "Queue message as follow-up (waits for the turn to finish)", "Input"},
	{"!cmd", "Run a shell command through pi (!!cmd keeps output out of context)", "Input"},
	{"↓ (last input line)", "Move into image tray", "Input"},
	{"←→ (tray)", "Select image chip", "Input"},
	{"⌫ (tray empty)", "Delete last image chip", "Input"},
	{"Esc (tray)", "Exit tray / restore input", "Input"},

	// Dialog / Picker keys
	{"↑↓ (dialog)", "Navigate options", "Dialog"},
	{"←→ / Tab (dialog)", "Switch pane (model/login)", "Dialog"},
	{"Enter (dialog)", "Confirm / select", "Dialog"},
	{"⌫ (dialog)", "Delete item / clear filter", "Dialog"},
	{"s (dialog)", "Show/hide keys", "Dialog"},
	{"r (dialog)", "Rename", "Dialog"},

	// Mouse (optional)
	{"Alt+M", "Toggle mouse on/off (same as /mouse)", "Mouse"},
	{"Mouse click (sidebar)", "Switch recent / toggle plugins", "Mouse"},
	{"Mouse wheel (sidebar)", "Scroll sidebar", "Mouse"},
	{"Hold Option/Shift", "Select text (mouse on)", "Mouse"},
}

// shortcutOrder controls category display order (empty groups skipped).
var shortcutOrder = []string{"Global", "Navigation", "Chat", "Sidebar", "Commands", "Custom", "UI", "Input", "Dialog", "Mouse"}

// shortcutRow is one flattened shortcut-help row: the key, its action,
// the display category (for grouping), and the /command a hub-assigned
// Alt row re-assigns ("" for the built-in keys).
type shortcutRow struct {
	key, desc, cat, cmd string
}

// shortcutRows flattens shortcutsDb into display order (shortcutOrder),
// with the hub-assigned Alt shortcuts appended as the "Custom" group.
// Sole source of truth for the settings hub's Shortcuts section — the
// standalone shortcuts dialog is gone, /shortcuts lands here.
func (m Model) shortcutRows() []shortcutRow {
	byCat := make(map[string][]ShortcutItem)
	for _, s := range shortcutsDb {
		byCat[s.Category] = append(byCat[s.Category], s)
	}
	var out []shortcutRow
	for _, cat := range shortcutOrder {
		if cat == "Custom" {
			continue // no built-in items: filled from prefs below
		}
		for _, it := range byCat[cat] {
			out = append(out, shortcutRow{key: it.Key, desc: it.Description, cat: it.Category})
		}
	}
	// Hub-assigned Alt shortcuts (Custom): Alt+key stages the /command.
	custom := make([]string, 0, len(m.CmdShortcuts))
	for cmd := range m.CmdShortcuts {
		custom = append(custom, cmd)
	}
	sort.Strings(custom)
	for _, cmd := range custom {
		out = append(out, shortcutRow{
			key:  shortcutDisplay(m.CmdShortcuts[cmd]),
			desc: "/" + cmd,
			cat:  "Custom", cmd: cmd,
		})
	}
	return out
}
