package app

import (
	"strconv"
	"strings"

	tea "pitago/src/tui/ev"
)

// NativeMenus enables tview's overlay widgets for the interactive model.
var NativeMenus bool

// menuTagCtx tags a right-click menu; the suffix is the chat block under
// the pointer (-1 for none), which copy:<kind> items act on.
const menuTagCtx = "ctx:"

func openMenuCmd(tag string, x, y int, items []tea.MenuItem) tea.Cmd {
	return func() tea.Msg { return tea.OpenMenuMsg{Tag: tag, X: x, Y: y, Items: items} }
}

// blockMenu is the ttt-style right-click menu for a chat block: the same
// semantic copy actions as the block-actions dialog.
func (m Model) blockMenu(idx int) []tea.MenuItem {
	if idx < 0 || idx >= len(m.blocks) {
		return nil
	}
	opts, payload := buildBlockOptions(collectBlockContent(m.blocks[idx]))
	items := make([]tea.MenuItem, 0, len(opts))
	for i, o := range opts {
		items = append(items, tea.MenuItem{ID: payload[i], Label: o})
	}
	return items
}

// inputAt reports whether screen x,y is on the input box: below the chat
// frame, in the main column, and inside the editor's own rows (not a task
// widget or plugin panel stacked between chat and input).
func (m Model) inputAt(x, y int) bool {
	if x < 0 || x >= min(m.winW, m.mainW()) {
		return false
	}
	top, bottom := m.inputRows()
	return y >= top && y < bottom
}

func (m Model) inputRows() (top, bottom int) {
	teamPanel := m.renderTeamWidget()
	extBudget := m.extPanelBudget(panelHeight(teamPanel))
	extAbove := m.renderExtWidgets("aboveEditor", extBudget)
	below := panelHeight(m.renderExtWidgets("belowEditor", max(0, extBudget-panelHeight(extAbove))))
	if teamPanel != "" && m.TeamWidgetPlacement == "belowEditor" {
		below += panelHeight(teamPanel)
	}
	bottom = m.winH - below // first row after the input box
	return bottom - m.inputHeight(), bottom
}

// rightClickMenu opens the context menu for a right press anywhere on the
// frame (chat, input, sidebar). Only used when NativeMenus is set.
func (m *Model) rightClickMenu(msg tea.MouseMsg) (tea.Cmd, bool) {
	if !NativeMenus || len(m.Dialogs) > 0 {
		return nil, false
	}
	ctx := -1
	if !m.overSide(msg.X) && msg.Y >= 1 && msg.Y <= m.chatViewport().Height {
		ctx = m.chatRowToBlock(msg.Y)
	}
	return openMenuCmd(menuTagCtx+strconv.Itoa(ctx), msg.X, msg.Y, m.contextMenu(msg.X, msg.Y)), true
}

// menuResult runs the item picked from the menu bar or a context menu.
func (m Model) menuResult(r tea.MenuResultMsg) (tea.Model, tea.Cmd) {
	switch {
	case r.Tag == tea.MenuBarTag:
		return m.runAction(r.ID, -1)
	case strings.HasPrefix(r.Tag, menuTagCtx):
		ctx, err := strconv.Atoi(strings.TrimPrefix(r.Tag, menuTagCtx))
		if err != nil {
			ctx = -1
		}
		return m.runAction(r.ID, ctx)
	}
	return m, nil
}
