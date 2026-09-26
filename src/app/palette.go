package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/palette"
	"pitago/src/ext"
)

// Matching + window size live in components/palette; this file keeps the
// popup wiring on Model.
func (m *Model) cmdPrefix() (string, bool) {
	v := m.ta.Value()
	if !strings.HasPrefix(v, "/") || strings.ContainsAny(v, " \n") {
		return "", false
	}
	return v[1:], true
}

func (m *Model) refreshCmds() {
	m.cmdItems = m.cmdItems[:0]
	if p, ok := m.cmdPrefix(); ok {
		names := make([]string, len(m.Cmds))
		for i := range m.Cmds {
			// name + source + extension tag + description, so "/pi-subagents"
			// also matches that extension's commands (like pi), and typing
			// "pitago"/"builtin" narrows to that origin
			names[i] = m.Cmds[i].Name + " " + m.Cmds[i].Source + " " + m.Cmds[i].SourceTag() + " " + m.Cmds[i].Description
		}
		m.cmdItems = append(m.cmdItems, palette.Match(p, names)...)
	}
	m.cmdOpen = len(m.cmdItems) > 0
	if m.cmdCursor >= len(m.cmdItems) {
		m.cmdCursor = 0
	}
	if m.cmdCursor < 0 {
		m.cmdCursor = 0
	}
	m.ensureCmdVisible()
	m.applyPopupH()
}

// cmdWin caps the visible popup rows so the whole frame fits winH on
// short terminals: header(1) + chat min(3) + popup box + input(6+chips)
// must stay <= winH. Tall screens keep palette.Win; the box never grows
// past it (see components/palette).
func (m Model) cmdWin() int {
	boxMax := m.winH - 1 - 3 - (6 + m.chipH()) - m.atPopupH() - m.uiPopupH() - m.inputPopupH()
	win := boxMax - 2 - 1 - 2 // border + footer + both scroll hints
	if win > palette.Win {
		win = palette.Win
	}
	if win < 1 {
		win = 1
	}
	return win
}

func (m *Model) ensureCmdVisible() {
	win := m.cmdWin()
	if m.cmdCursor < m.cmdOffset {
		m.cmdOffset = m.cmdCursor
	}
	if m.cmdCursor >= m.cmdOffset+win {
		m.cmdOffset = m.cmdCursor - win + 1
	}
	if m.cmdOffset < 0 {
		m.cmdOffset = 0
	}
}

func (m *Model) popupH() int {
	if !m.cmdOpen {
		return 0
	}
	win := m.cmdWin()
	n := len(m.cmdItems)
	if n > win {
		n = win
	}
	extra := 0 // scroll hints above/below the window
	if m.cmdOffset > 0 {
		extra++
	}
	if m.cmdOffset+win < len(m.cmdItems) {
		extra++
	}
	return n + extra + 3 // rows + hints + footer + border
}

// chatVpHeight is the one height formula for the chat viewport:
// base budget (winH-7) minus the tray, every popup, and the live team
// panel. Folding the team panel in here is what makes the compact panel
// behave like a /command popup — it pushes the chat up by exactly its real
// row count instead of being subtracted again at paint time (which is how
// the dashboard used to eat the whole frame). The three-row floor keeps the
// transcript readable no matter how many panels are open.
func (m *Model) chatVpHeight() int {
	h := m.baseVpH - m.popupH() - m.atPopupH() - m.uiPopupH() - m.inputPopupH() - m.chipH() - m.teamPanelH()
	if h < 3 {
		h = 3
	}
	return h
}

func (m *Model) applyPopupH() {
	if !m.ready || m.vp.Width == 0 {
		return
	}
	if h := m.chatVpHeight(); h != m.vp.Height {
		m.vp.Height = h
		m.vp.GotoBottom()
	}
}

// applyTeamPanelH re-syncs the chat viewport after a team widget update.
// View() has a value receiver and cannot persist m.vp, so the rows the
// panel occupies must be reserved from an *Model method — the same place
// applyPopupH reserves the popups. Both go through chatVpHeight, so a panel
// and a popup can never each claim the same rows.
func (m *Model) applyTeamPanelH() { m.applyPopupH() }

func (m *Model) exactCmdMatch() bool {
	p, ok := m.cmdPrefix()
	if !ok {
		return false
	}
	for _, c := range m.Cmds {
		if strings.EqualFold(c.Name, p) {
			return true
		}
	}
	return false
}

// handleCmdKey handles keys while the command popup is open. true = key consumed.

func (m *Model) handleCmdKey(km tea.KeyMsg) bool {
	switch km.Type {
	case tea.KeyUp:
		if m.cmdCursor > 0 {
			m.cmdCursor--
		} else {
			m.cmdCursor = len(m.cmdItems) - 1
		}
		m.ensureCmdVisible()
		return true
	case tea.KeyDown:
		if m.cmdCursor < len(m.cmdItems)-1 {
			m.cmdCursor++
		} else {
			m.cmdCursor = 0
		}
		m.ensureCmdVisible()
		return true
	case tea.KeyTab:
		m.completeCmd()
		return true
	case tea.KeyEsc:
		m.cmdOpen = false
		m.applyPopupH()
		m.Refresh()
		return true
	case tea.KeyEnter:
		if m.exactCmdMatch() {
			return false // exact match sends immediately
		}
		m.completeCmd()
		return true
	}
	return false
}

func (m *Model) completeCmd() {
	if !m.cmdOpen || len(m.cmdItems) == 0 {
		return
	}
	c := m.Cmds[m.cmdItems[m.cmdCursor]]
	m.ta.SetValue("/" + c.Name + " ")
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
}

func (m Model) renderCmdPopup() string {
	mainW := m.mainW()
	win := m.cmdWin()
	end := m.cmdOffset + win
	if end > len(m.cmdItems) {
		end = len(m.cmdItems)
	}
	// plain (unstyled) row text so width math stays ANSI-free
	plain := func(i int) (name, rest string) {
		c := m.Cmds[m.cmdItems[i]]
		name = "/" + c.Name
		if tag := c.SourceTag(); tag != "" {
			// extension command: "[u:npm:pi-subagents] desc", like pi
			rest = " — [" + tag + "]"
			if c.Description != "" {
				rest += " " + c.Description
			}
		} else {
			if c.Description != "" {
				rest = " — " + c.Description
			}
			rest += " [" + c.Source + "]"
		}
		return name, rest
	}
	foot := fmt.Sprintf("(%d/%d) Tab complete · Enter send · Esc close", m.cmdCursor+1, len(m.cmdItems))
	// The dropdown hugs its content instead of spanning the chat width:
	// box = widest line over ALL matches (scrolling never jitters it)
	// plus padding, capped at mainW.
	contentW := lipgloss.Width(foot)
	if m.cmdOffset > 0 {
		if w := lipgloss.Width(fmt.Sprintf("…(+%d above)", m.cmdOffset)); w > contentW {
			contentW = w
		}
	}
	if below := len(m.cmdItems) - end; below > 0 {
		if w := lipgloss.Width(fmt.Sprintf("…(+%d below)", below)); w > contentW {
			contentW = w
		}
	}
	for pos := range m.cmdItems {
		name, rest := plain(pos)
		if w := 2 + lipgloss.Width(Short(name+rest, mainW)); w > contentW {
			contentW = w
		}
	}
	boxW := contentW + 2 // horizontal padding (lipgloss adds the 2 border cols outside Width)
	if boxW > mainW-2 {
		boxW = mainW - 2
	}
	textW := boxW - 2 - 2 // padding + "▸ " marker
	if textW < 1 {
		textW = 1
	}
	var b strings.Builder
	if m.cmdOffset > 0 {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d above)", m.cmdOffset)) + "\n")
	}
	for i := m.cmdOffset; i < end; i++ {
		name, rest := plain(i)
		row := Short(name+rest, textW)
		// command name cyan, annotation keeps the row color
		nl := len(name)
		if nl > len(row) {
			nl = len(row)
		}
		if i == m.cmdCursor {
			b.WriteString("▸ " + cmdNameHiStyle.Render(row[:nl]) + cmdHiStyle.Render(row[nl:]) + "\n")
		} else {
			b.WriteString("  " + cmdNameStyle.Render(row[:nl]) + statusBarStyle.Render(row[nl:]) + "\n")
		}
	}
	if end < len(m.cmdItems) {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d below)", len(m.cmdItems)-end)) + "\n")
	}
	b.WriteString(toolStyle.Render(foot))
	return cmdPopStyle.Width(boxW).Render(strings.TrimRight(b.String(), "\n"))
}

// isInlineUI reports an extension select/confirm dialog (e.g. plan-mode
// menu) that renders as a small popup above the input — like /commands —
// instead of the fullscreen centered modal.
func (m Model) isInlineUI() bool {
	return len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "ui"
}

// containsPlan matches plan-mode text (menu title/options, setStatus).
// Canonical impl lives in ext (pi-extension domain); kept here for compat.
func containsPlan(s string) bool { return ext.ContainsPlan(s) }

// isPlanMode reports whether the plan indicator should show (live only).
// The extension exposes no plan flag in get_state, so this is a heuristic:
// latched Start choice, open plan menu, or plan in extension status.
// Menu text match delegates to ext (pi-extension domain).
func (m Model) isPlanMode() bool {
	if m.planOn {
		return true
	}
	if len(m.Dialogs) > 0 && m.Dialogs[0].Kind == "ui" {
		d := m.Dialogs[0]
		return ext.MenuHasPlan(d.Title, d.Message, d.Options)
	}
	return containsPlan(m.extStat)
}

// uiPopupH reserves viewport rows for the inline extension popup so the
// frame stays exactly winH (header + chat + popup + input).
func (m Model) uiPopupH() int {
	if !m.isInlineUI() {
		return 0
	}
	return lipgloss.Height(m.renderUIDialogPopup())
}

// inputPopupH reserves viewport rows for the floating free-text dialog so
// the frame stays exactly winH (header + chat + popup + input). Measured
// live so long input that wraps to more lines still fits.
func (m Model) inputPopupH() int {
	if !m.inputOpen() {
		return 0
	}
	return lipgloss.Height(m.renderInputBox())
}

// uiWin caps visible option rows so title + message + options + footer +
// input still fit winH. Small menus (plan-mode: 4 rows) show fully.
func (m Model) uiWin(titleLines, msgLines int) int {
	// header(1) + chat min(3) + input(6+chips) + title + msg + blank + footer + border(2)
	reserved := 1 + 3 + (6 + m.chipH()) + titleLines + msgLines + 1 + 1 + 2
	win := m.winH - reserved - 2 // both scroll hints
	if win > 10 {
		win = 10
	}
	if win < 1 {
		win = 1
	}
	return win
}

func (m Model) renderUIDialogPopup() string {
	d := m.Dialogs[0]
	mainW := m.mainW()
	title := d.Title
	if title == "" {
		title = "Select"
	}
	var msgs []string
	if strings.TrimSpace(d.Message) != "" {
		msgs = strings.Split(strings.TrimSpace(d.Message), "\n")
	}
	foot := "↑↓ select · Enter confirm · Esc cancel"
	total := len(d.FIdx)
	win := m.uiWin(1, len(msgs))
	start := 0
	if total > win {
		start = d.Cursor - 2
		if start < 0 {
			start = 0
		}
		if start+win > total {
			start = total - win
		}
	}
	end := start + win
	if end > total {
		end = total
	}
	// Hug content like /commands: widest plain line + padding, capped.
	contentW := lipgloss.Width(title)
	for _, ln := range msgs {
		if w := lipgloss.Width(Short(strings.TrimSpace(ln), mainW)); w > contentW {
			contentW = w
		}
	}
	if w := lipgloss.Width(foot); w > contentW {
		contentW = w
	}
	if start > 0 {
		if w := lipgloss.Width(fmt.Sprintf("…(+%d above)", start)); w > contentW {
			contentW = w
		}
	}
	if end < total {
		if w := lipgloss.Width(fmt.Sprintf("…(+%d below)", total-end)); w > contentW {
			contentW = w
		}
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		row := ""
		if ri >= 0 && ri < len(d.Options) {
			row = d.Options[ri]
		}
		if w := 2 + lipgloss.Width(Short(row, mainW)); w > contentW {
			contentW = w
		}
	}
	boxW := contentW + 2
	if boxW > mainW-2 {
		boxW = mainW - 2
	}
	textW := boxW - 2 - 2
	if textW < 1 {
		textW = 1
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(Short(title, textW)) + "\n")
	for _, ln := range msgs {
		b.WriteString(statusBarStyle.Render(Short(strings.TrimSpace(ln), textW)) + "\n")
	}
	b.WriteString("\n")
	if start > 0 {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
	}
	for fi := start; fi < end; fi++ {
		ri := d.FIdx[fi]
		row := ""
		if ri >= 0 && ri < len(d.Options) {
			row = d.Options[ri]
		}
		row = Short(row, textW)
		if fi == d.Cursor {
			b.WriteString("▸ " + rowHiStyle.Render(row) + "\n")
		} else {
			b.WriteString("  " + statusBarStyle.Render(row) + "\n")
		}
	}
	if end < total {
		b.WriteString("  " + toolStyle.Render(fmt.Sprintf("…(+%d below)", total-end)) + "\n")
	}
	if total == 0 {
		b.WriteString("  " + toolStyle.Render("— no match —") + "\n")
	}
	b.WriteString(toolStyle.Render(foot))
	return cmdPopStyle.Width(boxW).Render(strings.TrimRight(b.String(), "\n"))
}
