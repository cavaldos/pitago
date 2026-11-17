package app

import (
	"strconv"
	"strings"

	tea "pitago/src/tui/ev"
)

// NativeMenus is set by a runtime that can draw context menus at the
// pointer (tviewrt). Without it, right-click falls back to the full-screen
// dialogs (Bubble Tea).
var NativeMenus bool

const (
	menuTagBlock = "block:" // + block index
	menuTagInput = "input"
)

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

// inputMenu is the right-click menu of the input editor.
func (m Model) inputMenu() []tea.MenuItem {
	empty := strings.TrimSpace(m.ta.Value()) == ""
	return []tea.MenuItem{
		{ID: "paste", Label: "Paste", Shortcut: "^V"},
		{ID: "copy", Label: "Copy input", Disabled: empty},
		{Separator: true},
		{ID: "clear", Label: "Clear input", Disabled: empty},
	}
}

// inputAt reports whether screen x,y is on the input box: below the chat
// frame, in the main column, and inside the editor's own rows (not a task
// widget or plugin panel stacked between chat and input).
func (m Model) inputAt(x, y int) bool {
	if m.overSide(x) || y <= m.chatViewport().Height {
		return false
	}
	teamPanel := m.renderTeamWidget()
	extBudget := m.extPanelBudget(panelHeight(teamPanel))
	extAbove := m.renderExtWidgets("aboveEditor", extBudget)
	below := panelHeight(m.renderExtWidgets("belowEditor", max(0, extBudget-panelHeight(extAbove))))
	if teamPanel != "" && m.TeamWidgetPlacement == "belowEditor" {
		below += panelHeight(teamPanel)
	}
	bottom := m.winH - below // first row after the input box
	top := bottom - panelHeight(m.renderInput())
	return y >= top && y < bottom
}

// rightClickMenu opens the context menu for a right press at msg, if any
// target is under the pointer. Only used when NativeMenus is set.
func (m *Model) rightClickMenu(msg tea.MouseMsg) (tea.Cmd, bool) {
	if !NativeMenus || len(m.Dialogs) > 0 {
		return nil, false
	}
	if !m.overSide(msg.X) && msg.Y >= 1 && msg.Y <= m.chatViewport().Height {
		if idx := m.chatRowToBlock(msg.Y); idx >= 0 && m.blocks[idx].Kind == "assistant" {
			if items := m.blockMenu(idx); len(items) > 0 {
				return openMenuCmd(menuTagBlock+strconv.Itoa(idx), msg.X, msg.Y, items), true
			}
		}
		return nil, false
	}
	if m.inputAt(msg.X, msg.Y) {
		return openMenuCmd(menuTagInput, msg.X, msg.Y, m.inputMenu()), true
	}
	return nil, false
}

// menuResult runs the item picked from a context menu.
func (m Model) menuResult(r tea.MenuResultMsg) (tea.Model, tea.Cmd) {
	switch {
	case strings.HasPrefix(r.Tag, menuTagBlock):
		idx, err := strconv.Atoi(strings.TrimPrefix(r.Tag, menuTagBlock))
		if err != nil {
			return m, nil
		}
		d := &Dialog{BlockIdx: idx, Payload: []string{r.ID}}
		cmd := m.RunBlockAction(d, 0)
		return m, cmd
	case r.Tag == menuTagInput:
		switch r.ID {
		case "paste":
			m.histIdx = -1
			return m, m.pasteCmd(false)
		case "copy":
			m.YankText(m.ta.Value())
		case "clear":
			m.ta.Reset()
			m.histIdx = -1
			m.Refresh()
		}
	}
	return m, nil
}
