package app

import (
	"strings"

	tea "pitago/src/tui/ev"
)

// Menu bar (File · Edit · Selection · View · Options · Help) and the
// right-click menu, ttt-style. Both are data: item IDs are actions run by
// runAction, so a menu entry can never drift from the key or command it
// mirrors.
//
// Action IDs:
//
//	key:<name>   replay a key the app already binds ("ctrl+n")
//	cmd:/<cmd>   run a slash command (pitago builtin or pi command)
//	act:<name>   a menu-only action (see runAction)
//	copy:<kind>  block copy for the block under the right-click (md/code/…)

// MenuBar implements ev.MenuBarProvider (tviewrt only; see NativeMenus).
func (m Model) MenuBar() []tea.Menu {
	if !NativeMenus {
		return nil
	}
	return []tea.Menu{
		{Title: "File", Items: []tea.MenuItem{
			{ID: "key:ctrl+n", Label: "New Session", Shortcut: "Ctrl+N"},
			{ID: "cmd:/resume", Label: "Resume Session…"},
			{ID: "cmd:/tree", Label: "Session Tree…"},
			{ID: "cmd:/fork", Label: "Fork Session…"},
			{Separator: true},
			{ID: "cmd:/name", Label: "Session Info / Name"},
			{ID: "cmd:/export", Label: "Export as HTML"},
			{Separator: true},
			{ID: "act:quit", Label: "Quit", Shortcut: "Ctrl+C ×2"},
		}},
		{Title: "Edit", Items: []tea.MenuItem{
			{ID: "act:paste", Label: "Paste", Shortcut: "Ctrl+V"},
			{ID: "act:copy-input", Label: "Copy Input", Disabled: m.inputEmpty()},
			{ID: "act:clear-input", Label: "Clear Input", Disabled: m.inputEmpty()},
			{Separator: true},
			{ID: "key:ctrl+y", Label: "Copy Last Answer", Shortcut: "Ctrl+Y"},
			{ID: "key:ctrl+o", Label: "Copy From History…", Shortcut: "Ctrl+O"},
		}},
		{Title: "Selection", Items: []tea.MenuItem{
			{ID: "act:copy-visible", Label: "Copy Visible Chat"},
			{ID: "act:copy-all", Label: "Copy Entire Transcript"},
			{Separator: true},
			{ID: "cmd:/copy-md", Label: "Copy Last Answer as Markdown"},
			{ID: "cmd:/copy-code", Label: "Copy Code Blocks"},
			{ID: "cmd:/copy-tables", Label: "Copy Tables"},
			{Separator: true},
			{ID: "act:jump-latest", Label: "Jump to Latest", Shortcut: "End"},
			{ID: "cmd:/focus", Label: "Focus on File Lines…"},
		}},
		{Title: "View", Items: []tea.MenuItem{
			{ID: "key:ctrl+e", Label: check(m.showSide()) + "Sidebar", Shortcut: "Ctrl+E"},
			{ID: "act:reset-sidebar", Label: "  Reset Sidebar Width", Disabled: m.SideWidth == 0},
			{ID: "key:ctrl+g", Label: check(m.expandTools) + "Expand Tool Output", Shortcut: "Ctrl+G"},
			{ID: "act:tidy", Label: check(m.Tidy) + "Tidy Mode"},
			{Separator: true},
			{ID: "cmd:/theme", Label: "  Theme…"},
			{ID: "cmd:/trajectory", Label: "  Run Trace…"},
			{ID: "cmd:/team", Label: "  Agent Team…"},
			{ID: "cmd:/notification", Label: "  Notifications…"},
		}},
		{Title: "Options", Items: []tea.MenuItem{
			{ID: "cmd:/model", Label: "Model…"},
			{ID: "key:ctrl+r", Label: "Recent Models…", Shortcut: "Ctrl+R"},
			{Label: "Thinking Level", Submenu: m.thinkingItems()},
			{Separator: true},
			{ID: "cmd:/compact", Label: "Compact Context"},
			{ID: "cmd:/settings", Label: "Agent Settings…"},
			{ID: "cmd:/pitago-setting", Label: "Pitago Settings…"},
			{ID: "cmd:/mcp", Label: "MCP Servers…"},
			{ID: "cmd:/login", Label: "Login…"},
		}},
		{Title: "Help", Items: []tea.MenuItem{
			{ID: "act:commands", Label: "All Commands", Shortcut: "/"},
			{ID: "cmd:/shortcuts", Label: "Keyboard Shortcuts"},
			{ID: "cmd:/session", Label: "Session Stats"},
			{Separator: true},
			{ID: "cmd:/update", Label: "Check for Updates"},
		}},
	}
}

func check(on bool) string {
	if on {
		return "✓ "
	}
	return "  "
}

func (m Model) inputEmpty() bool { return strings.TrimSpace(m.ta.Value()) == "" }

// thinkingItems lists the current model's thinking levels (pi's own list),
// with the active one checked.
func (m Model) thinkingItems() []tea.MenuItem {
	levels := m.thinkLevels
	if len(levels) == 0 {
		return []tea.MenuItem{{Label: "Not supported by this model", Disabled: true}}
	}
	out := make([]tea.MenuItem, 0, len(levels))
	for _, l := range levels {
		out = append(out, tea.MenuItem{ID: "cmd:/thinking " + l, Label: check(l == m.thinkLvl) + l})
	}
	return out
}

// contextMenu is the right-click menu: what is under the pointer first
// (the block's copy actions, the input's edit actions), then the common
// groups, like an editor's context menu.
func (m Model) contextMenu(x, y int) []tea.MenuItem {
	var items []tea.MenuItem
	if !m.overSide(x) && y >= 1 && y <= m.chatViewport().Height {
		if idx := m.chatRowToBlock(y); idx >= 0 && m.blocks[idx].Kind == "assistant" {
			if sub := m.blockMenu(idx); len(sub) > 0 {
				for i := range sub {
					sub[i].ID = "copy:" + sub[i].ID
				}
				items = append(items, sub...)
				items = append(items, tea.MenuItem{Separator: true})
			}
		}
	}
	if m.inputAt(x, y) {
		items = append(items,
			tea.MenuItem{ID: "act:paste", Label: "Paste", Shortcut: "Ctrl+V"},
			tea.MenuItem{ID: "act:copy-input", Label: "Copy Input", Disabled: m.inputEmpty()},
			tea.MenuItem{ID: "act:clear-input", Label: "Clear Input", Disabled: m.inputEmpty()},
			tea.MenuItem{Separator: true},
		)
	} else {
		items = append(items,
			tea.MenuItem{ID: "key:ctrl+y", Label: "Copy Last Answer", Shortcut: "Ctrl+Y"},
			tea.MenuItem{ID: "act:copy-visible", Label: "Copy Visible Chat"},
			tea.MenuItem{ID: "act:paste", Label: "Paste into Input", Shortcut: "Ctrl+V"},
			tea.MenuItem{Separator: true},
		)
	}
	items = append(items,
		tea.MenuItem{ID: "act:jump-latest", Label: "Jump to Latest", Shortcut: "End"},
		tea.MenuItem{ID: "key:ctrl+g", Label: check(m.expandTools) + "Expand Tool Output", Shortcut: "Ctrl+G"},
		tea.MenuItem{ID: "key:ctrl+e", Label: check(m.showSide()) + "Sidebar", Shortcut: "Ctrl+E"},
		tea.MenuItem{Separator: true},
		tea.MenuItem{ID: "cmd:/model", Label: "Model…"},
		tea.MenuItem{Label: "Thinking Level", Submenu: m.thinkingItems()},
		tea.MenuItem{ID: "cmd:/compact", Label: "Compact Context"},
		tea.MenuItem{Separator: true},
		tea.MenuItem{ID: "key:ctrl+n", Label: "New Session", Shortcut: "Ctrl+N"},
		tea.MenuItem{ID: "act:commands", Label: "All Commands", Shortcut: "/"},
	)
	return items
}

// runAction executes one menu item ID. ctx is the block index a
// right-click menu was opened on (-1 for the menu bar).
func (m Model) runAction(id string, ctx int) (tea.Model, tea.Cmd) {
	kind, arg, _ := strings.Cut(id, ":")
	switch kind {
	case "key":
		if k, ok := keyMsgFor(arg); ok {
			return m.Update(k)
		}
	case "cmd":
		return m, m.runSlash(arg)
	case "copy":
		cmd := m.RunBlockAction(&Dialog{BlockIdx: ctx, Payload: []string{arg}}, 0)
		return m, cmd
	case "act":
		switch arg {
		case "quit":
			return m, tea.Quit
		case "paste":
			m.histIdx = -1
			return m, m.pasteCmd(false)
		case "copy-input":
			m.YankText(m.ta.Value())
		case "clear-input":
			m.ta.Reset()
			m.histIdx = -1
			m.refreshCmds()
			m.Refresh()
		case "copy-all":
			m.copyChatRange(0, len(m.chatLines)-1)
		case "copy-visible":
			vp := m.chatViewport()
			m.copyChatRange(vp.YOffset, vp.YOffset+vp.Height-1)
		case "jump-latest":
			m.JumpToLatest()
		case "reset-sidebar":
			m.ResetSideWidth()
		case "tidy":
			m.SetTidy(!m.Tidy)
			m.Refresh()
		case "commands":
			if m.inputEmpty() {
				return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			}
		}
	}
	return m, nil
}

// copyChatRange copies transcript lines from..to (inclusive), gutters
// stripped, like a drag selection over them.
func (m *Model) copyChatRange(from, to int) {
	if len(m.chatLines) == 0 {
		return
	}
	from = max(0, from)
	to = min(len(m.chatLines)-1, to)
	if to < from {
		return
	}
	text := selectionText(m.chatLines, m.gutterCols,
		Point{Line: from, Col: 0}, Point{Line: to, Col: visibleWidth(m.chatLines[to])})
	if strings.TrimSpace(text) != "" {
		m.YankText(text)
	}
}

// runSlash runs "/name args" the way submitting it would, without touching
// the input.
func (m *Model) runSlash(text string) tea.Cmd {
	if b, arg, ok := m.FindBuiltin(text); ok {
		m.noteCmdUse(b.Name)
		m.Refresh()
		return b.Run(m, arg)
	}
	if name, ok := m.FindPiCommand(text); ok {
		m.noteCmdUse(name)
		m.Refresh()
		return m.ForwardExtensionCommand(text)
	}
	return nil
}

// keyMsgFor builds the KeyMsg for a "ctrl+x" name (the only form menus use).
func keyMsgFor(name string) (tea.KeyMsg, bool) {
	if l, ok := strings.CutPrefix(name, "ctrl+"); ok && len(l) == 1 && l[0] >= 'a' && l[0] <= 'z' {
		return tea.KeyMsg{Type: tea.KeyCtrlA + tea.KeyType(l[0]-'a')}, true
	}
	return tea.KeyMsg{}, false
}
