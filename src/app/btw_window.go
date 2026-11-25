package app

// btw_window.go — the BTW side-conversation window (pi-btw plugin).
//
// The plugin runs its side thread in a hidden child session and pushes the
// transcript here through the ordinary setWidget channel under the reserved
// "btw" key. This file turns that stream into a real window: a modal dialog
// in the ask-user style (framed, centered) with the thread lines and its own
// input. Esc hides the window but keeps the content — the plugin keeps
// pushing updates while a turn runs, and only an explicit /btw* command
// reopens it, so hiding is a real "get this out of my face", not a pause.
// The window replaces the generic ext-widget panel for this key: a thread
// transcript above the editor was exactly the chat-area noise it was meant
// to avoid.

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tea "pitago/src/tui/ev"
)

// btwWindowKey is the widget key the btw extension uses for its transcript.
// Reserved pitago-side: any other plugin using the same key feeds this window.
const btwWindowKey = "btw"

// btwTailRows caps the transcript rows the floating box shows at once; ↑/↓
// scroll the window through the full pushed history.
const btwTailRows = 10

// btwOpen reports the BTW window is on the dialog stack.
func (m Model) btwOpen() bool {
	for _, d := range m.Dialogs {
		if d.Kind == "btw" {
			return true
		}
	}
	return false
}

// setBtwWindow routes one btw widget update: lines are the thread
// transcript, no lines mean the plugin cleared the thread (/btw:clear,
// /btw:inject) — close and forget. While the window is hidden by the user
// (btwDismissed), updates refresh the content without reopening it.
func (m *Model) setBtwWindow(lines []string) {
	if len(lines) == 0 {
		m.BtwLines = nil
		m.btwDismissed = false
		m.closeBtwWindow()
		return
	}
	m.BtwLines = append([]string(nil), lines...)
	if d := m.btwOnStack(); d != nil {
		// Content is append-only, so a scrolled offset stays anchored to the
		// tail and keeps reading the same region as new lines land.
		d.BtwOff = min(d.BtwOff, m.btwMaxOff())
	}
	if !m.btwDismissed && !m.btwOpen() {
		m.Dialogs = append(m.Dialogs, btwDialog())
	}
	// Line pushes change the floating box's height; give the chat its rows
	// back (or reserve new ones) like every other popup does.
	m.applyPopupH()
	m.Refresh()
}

// closeBtwWindow removes the BTW dialog from the stack, wherever it sits.
func (m *Model) closeBtwWindow() {
	for i, d := range m.Dialogs {
		if d.Kind == "btw" {
			m.Dialogs = append(m.Dialogs[:i], m.Dialogs[i+1:]...)
			break
		}
	}
	m.applyPopupH()
	m.Refresh()
}

// btwDialog is the fresh BTW window dialog.
func btwDialog() *Dialog {
	return &Dialog{Kind: "btw", Title: "BTW", Placeholder: "Type a side question…"}
}

// btwOnStack finds the BTW window dialog wherever it sits: another queued
// dialog can be on top of the stack while this window stays open.
func (m Model) btwOnStack() *Dialog {
	for _, d := range m.Dialogs {
		if d.Kind == "btw" {
			return d
		}
	}
	return nil
}

// btwMaxOff caps the scroll offset so the window always keeps btwTailRows
// lines on screen: fully scrolled up it shows the oldest kept lines.
func (m Model) btwMaxOff() int {
	return max(0, len(m.BtwLines)-btwTailRows)
}

// reopenBtwWindow clears the hidden latch and brings the window back with
// the latest pushed content. Called when a /btw* command is forwarded.
func (m *Model) reopenBtwWindow() {
	m.btwDismissed = false
	if len(m.BtwLines) > 0 && !m.btwOpen() {
		m.Dialogs = append(m.Dialogs, btwDialog())
	}
	m.applyPopupH()
	m.Refresh()
}

// btwSubmitText resolves the window's Enter: a leading "/" is forwarded as
// its own command (/btw:clear, /side …); anything else is a side question
// and rides the /btw command the plugin already routes.
func btwSubmitText(d *Dialog) string {
	text := strings.TrimSpace(d.Filter)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "/") {
		return text
	}
	return "/btw " + text
}

// updateBtwDialog handles keys while the BTW window is on top. Esc hides
// (content kept, /btw reopens); Enter forwards and clears the draft.
func (m Model) updateBtwDialog(km tea.KeyMsg, d *Dialog) (tea.Model, tea.Cmd) {
	switch km.Type {
	case tea.KeyCtrlC:
		return m.dismissBtwDialog(d)
	case tea.KeyEsc:
		return m.dismissBtwDialog(d)
	case tea.KeyUp:
		if d.BtwOff < m.btwMaxOff() {
			d.BtwOff++
		}
		return m, nil
	case tea.KeyDown:
		if d.BtwOff > 0 {
			d.BtwOff--
		}
		return m, nil
	case tea.KeyPgUp:
		d.BtwOff = min(d.BtwOff+5, m.btwMaxOff())
		return m, nil
	case tea.KeyPgDown:
		d.BtwOff = max(0, d.BtwOff-5)
		return m, nil
	case tea.KeyBackspace:
		if r := []rune(d.Filter); len(r) > 0 {
			d.Filter = string(r[:len(r)-1])
		}
		return m, nil
	case tea.KeyCtrlV:
		return m, m.pasteCmd(true)
	case tea.KeyEnter:
		text := btwSubmitText(d)
		if text == "" {
			return m, nil
		}
		d.Filter = ""
		d.BtwOff = 0 // follow the incoming turn
		return m, m.ForwardExtensionCommand(text)
	}
	if km.Type == tea.KeySpace {
		d.Filter += " "
		return m, nil
	}
	if km.Type == tea.KeyRunes {
		d.Filter += km.String()
		return m, nil
	}
	return m, nil
}

// dismissBtwDialog hides the window and latches btwDismissed so live turn
// updates refresh content without popping it back open. BtwLines survive.
func (m Model) dismissBtwDialog(d *Dialog) (tea.Model, tea.Cmd) {
	m.btwDismissed = true
	m.closeBtwWindow()
	return m, nil
}

// btwBoxBody renders the floating window content: title, transcript window
// (↑↓ scrollable, newest last), the draft input, and hints.
func (m Model) btwBoxBody() (string, int) {
	d := m.btwOnStack()
	if d == nil {
		return "", 0
	}
	var b strings.Builder
	b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(cText).Render(d.Title) + "\n")
	total := len(m.BtwLines)
	off := min(d.BtwOff, m.btwMaxOff())
	start := max(0, total-btwTailRows-off)
	end := total - off
	if start > 0 {
		b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d above)", start)) + "\n")
	}
	for _, line := range m.BtwLines[start:end] {
		b.WriteString(btwLine(line) + "\n")
	}
	if off > 0 {
		b.WriteString(toolStyle.Render(fmt.Sprintf("…(+%d below · ↓)", off)) + "\n")
	}
	b.WriteString("\n")
	// pi's ui.input placeholder convention: a dim hint while the draft is
	// empty, so the offered action is visible instead of a bare cursor.
	if d.Filter == "" && d.Placeholder != "" {
		b.WriteString(toolStyle.Render("> "+d.Placeholder) + cmdHiStyle.Render("▌") + "\n")
	} else {
		b.WriteString(cmdHiStyle.Render("> "+d.Filter+"▌") + "\n")
	}
	b.WriteString("\n" + toolStyle.Render("↑↓ scroll · Enter send · Esc hide (thread kept) · /btw reopen"))
	boxW := 70
	if mw := m.mainW() - 4; mw < boxW && mw > 20 {
		boxW = mw
	}
	return b.String(), boxW
}

// btwLine paints one transcript line: the speaker label ("You:"/"BTW:")
// gets a bright bold run so the thread reads like a chat, the content and
// every other line (header, tools, status) stay dim.
func btwLine(line string) string {
	if label, rest, ok := strings.Cut(line, ": "); ok {
		switch label {
		case "You", "BTW":
			return lipgloss.NewStyle().Bold(true).Foreground(cText).Render(label+": ") + statusBarStyle.Render(rest)
		}
	}
	return statusBarStyle.Render(line)
}

// renderBtwBox draws the floating BTW window, mirroring renderInputBox: it
// floats above the input while the chat and sidebar stay visible behind it.
func (m Model) renderBtwBox() string {
	body, w := m.btwBoxBody()
	if w == 0 {
		return "" // no btw dialog on the stack: nothing to draw
	}
	return dlgStyle.Width(w).Render(body)
}

// overlayBtwBox floats the window over the bottom rows of the chat viewport
// (overlayToasts parity, toast.go): the chat keeps its full height and the
// frame's row arithmetic never shifts. The window sits at the left edge; the
// covered rows keep their chat text on the remaining columns.
func (m Model) overlayBtwBox(chat, box string) string {
	if box == "" || chat == "" {
		return chat
	}
	rows := strings.Split(chat, "\n")
	brows := strings.Split(box, "\n")
	start := len(rows) - len(brows)
	if start < 0 {
		return chat // viewport shorter than the window: skip, never shift
	}
	keepW := max(0, m.mainW()-lipgloss.Width(brows[0]))
	for i, br := range brows {
		rows[start+i] = truncANSI(rows[start+i], keepW) + br
	}
	return strings.Join(rows, "\n")
}
