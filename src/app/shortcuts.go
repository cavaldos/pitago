package app

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ShortcutItem describes one keyboard shortcut for the shortcuts help page.
type ShortcutItem struct {
	Key         string // e.g. "Ctrl+C" / "Ctrl+Shift+/"
	Description string // what it does
	Category    string // e.g. "Navigation", "Editing", "Global"
}

var shortcutsDb = []ShortcutItem{
	// Global / System
	{"Ctrl+C", "Quit (double-press within 3s)", "Global"},
	{"Esc", "Cancel running turn (double-press within 3s)", "Global"},
	{"Ctrl+Shift+/", "Open shortcuts page", "Global"},
	{"/?", "Open command palette / shortcuts", "Global"},

	// Navigation / Chat
	{"↑↓", "Scroll chat", "Chat"},
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
	{"Ctrl+B", "Hide/show sidebar", "UI"},
	{"Ctrl+R", "Open recent models picker", "UI"},
	{"Ctrl+N", "New session", "UI"},
	{"Ctrl+P", "Cycle model", "UI"},
	{"Ctrl+Y", "Yank last assistant answer to clipboard", "UI"},
	{"Ctrl+V", "Paste (multi-backend)", "UI"},
	{"Ctrl+O", "Open yank picker", "UI"},

	// Tools / Thinking / Settings
	{"Ctrl+G", "Expand/collapse all tool blocks", "UI"},
	{"Ctrl+T", "Cycle thinking level", "UI"},
	{"Ctrl+F", "Star/unstar model (in picker)", "UI"},

	// Input / Tray
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

// OpenShortcuts opens the shortcut help page as a dialog/modal.
// Options = key, Descs = action, Providers = category (parallel): the
// list renderer groups the filtered rows, and typing filters key/action.
func (m *Model) OpenShortcuts() tea.Cmd {
	m.Status = "opening shortcuts…"
	m.Refresh()

	byCat := make(map[string][]ShortcutItem)
	for _, s := range shortcutsDb {
		byCat[s.Category] = append(byCat[s.Category], s)
	}

	var opts, descs, provs []string
	for _, cat := range shortcutOrder {
		for _, it := range byCat[cat] {
			opts = append(opts, it.Key)
			descs = append(descs, it.Description)
			provs = append(provs, cat)
		}
	}
	// Hub-assigned Alt shortcuts (Custom): Alt+key stages the /command.
	var custom []string
	for cmd := range m.CmdShortcuts {
		custom = append(custom, cmd)
	}
	sort.Strings(custom)
	for _, cmd := range custom {
		opts = append(opts, shortcutDisplay(m.CmdShortcuts[cmd]))
		descs = append(descs, "/"+cmd)
		provs = append(provs, "Custom")
	}

	d := &Dialog{
		Kind: "shortcuts", Title: "Keyboard Shortcuts",
		Message: "Alt+M toggles mouse · type to filter · ↑↓ navigate · Enter/Esc close.",
		Options: opts, Descs: descs, Providers: provs, Cursor: 0,
	}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}

// renderShortcuts builds the plain text shown in the dialog / page.
// Borderless two-column list (key + dimmed action), like the model picker.
func renderShortcuts() string {
	keyW := 12
	for _, s := range shortcutsDb {
		if w := lipgloss.Width(s.Key); w > keyW {
			keyW = w
		}
	}
	if keyW > 24 {
		keyW = 24
	}

	byCat := make(map[string][]ShortcutItem)
	for _, s := range shortcutsDb {
		byCat[s.Category] = append(byCat[s.Category], s)
	}

	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Render("Keyboard Shortcuts") + "\n\n")

	for _, cat := range shortcutOrder {
		items := byCat[cat]
		if len(items) == 0 {
			continue
		}
		b.WriteString(strings.ToUpper(cat) + "\n")
		for _, it := range items {
			b.WriteString("  " + Fit(it.Key, keyW) + "  — " + it.Description + "\n")
		}
		b.WriteString("\n")
	}

	b.WriteString("Open with /?  ·  Ctrl+Shift+/  ·  /shortcuts  ·  Alt+M toggles mouse\n")
	return b.String()
}

// renderShortcutsDialog draws the shortcuts help as a borderless two-column
// list (key + dimmed action, like the model picker): one row per shortcut,
// headers are display-only and never selectable. Filtering narrows FIdx;
// only the cursor window is drawn.
func (m Model) renderShortcutsDialog(d *Dialog) string {
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	if d.Message != "" {
		b.WriteString(statusBarStyle.Render(d.Message) + "\n")
	}
	b.WriteString(statusBarStyle.Render("filter: "+d.Filter+"▌") + "\n")
	b.WriteString("\n")

	boxW := m.winW - 10
	if boxW < 62 {
		boxW = 62
	}
	if boxW > 100 {
		boxW = 100
	}
	rowW := boxW - 10 // cursor mark + dialog padding + border

	// Key column fits the filtered rows (stable min, capped for desc room).
	keyW := 14
	for _, fi := range d.FIdx {
		if fi < len(d.Options) {
			if w := lipgloss.Width(d.Options[fi]); w > keyW {
				keyW = w
			}
		}
	}
	if keyW > 24 {
		keyW = 24
	}
	descW := rowW - keyW - 5 // "  " gap + "— " prefix
	if descW < 20 {
		descW = 20
		keyW = rowW - descW - 5
	}

	win := m.winH - 16
	if win < 8 {
		win = 8
	}
	if win > 18 {
		win = 18
	}
	total := len(d.FIdx)
	start, end, above, below := fixedWin(d.Cursor, total, win)

	if above {
		b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
	}
	if total == 0 {
		b.WriteString("  " + toolStyle.Render("— no match —") + "\n")
	} else {
		// Group only the visible window so the cursor row is always shown.
		lastCat := ""
		for fi := start; fi < end; fi++ {
			ri := d.FIdx[fi]
			cat := providerAt(d.Providers, ri)
			if cat == "" {
				cat = "Other"
			}
			if cat != lastCat {
				b.WriteString("  " + sideTitleStyle.Width(rowW-2).Render(strings.ToUpper(cat)) + "\n")
				lastCat = cat
			}
			mark := "  "
			style := statusBarStyle
			if fi == d.Cursor {
				mark = "▸ "
				style = rowHiStyle
			}
			row := Fit(Short(d.Options[ri], keyW), keyW) + "  "
			if desc := DescOf(d, ri); desc != "" {
				row += toolStyle.Render("— " + Short(desc, descW))
			}
			// Pad the unstyled tail so every row fills rowW (styled desc
			// already counted via Width-aware Fit/Short above).
			if fi == d.Cursor {
				b.WriteString(mark + style.Width(rowW).Render(row) + "\n")
			} else {
				b.WriteString(mark + style.Render(row) + "\n")
			}
		}
	}
	if below {
		b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)) + "\n")
	}

	b.WriteString("\n" + toolStyle.Render("type to filter · ↑↓ select · Enter/Esc close · Alt+M toggle mouse · /mouse on|off"))
	box := dlgStyle.Width(boxW).Render(b.String())
	hint := ""
	if len(m.Dialogs) > 1 {
		hint = statusBarStyle.Render(fmt.Sprintf("(%d more dialogs pending)", len(m.Dialogs)-1))
	}
	return lipgloss.JoinVertical(lipgloss.Center,
		lipgloss.Place(m.winW, m.winH-2, lipgloss.Center, lipgloss.Center, box),
		hint,
	)
}
