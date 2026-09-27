package app

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// copyHintTTL is how long a dialog footer keeps showing the result of a
// copy. Long enough to read, short enough not to linger.
const copyHintTTL = 2 * time.Second

// clearCopyHintMsg retires a copyHint. gen makes a second copy immune to
// the first one's timer: without it, copying twice quickly leaves the hint
// up for roughly no time at all.
type clearCopyHintMsg struct{ gen int }

// dialogCopyable reports whether a dialog kind exposes a row to copy.
//
// Only the browsing dialogs qualify. Every unmodified rune is consumed
// by their type-to-filter path, so the copy action cannot be a bare letter
// anyway — Ctrl+Y is used, matching the app's existing yank vocabulary.
// The tree joins them for pi's sake: its selector carries onCopy, and its
// footer promises Ctrl+Y, so the key has to answer there too.
// The other copyable kind, "yank", already copies on Enter and has no
// Ctrl+Y of its own.
func dialogCopyable(d *Dialog) bool {
	return d.Kind == "trajectory" || d.Kind == "notification" || d.Kind == "tree"
}

// copyDialogSelection copies the selected row's full text and returns the
// command that clears the confirmation.
//
// It deliberately does not route through confirmDialog: unlike Enter, a
// copy is a pure clipboard action that must leave the dialog open so the
// user can keep browsing.
func (m *Model) copyDialogSelection(d *Dialog) tea.Cmd {
	text := trajSelected(d) // same resolver the detail pane renders from
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := clipWrite(text); err != nil {
		m.copyGen++
		m.copyHint = errStyle.Render("copy failed: " + err.Error())
	} else {
		m.copyGen++
		m.copyHint = okStyle.Render(fmt.Sprintf("✓ copied %d chars to clipboard", len([]rune(text))))
	}
	m.applyPopupH()
	m.Refresh()
	gen := m.copyGen
	return tea.Tick(copyHintTTL, func(time.Time) tea.Msg {
		return clearCopyHintMsg{gen: gen}
	})
}

// CopyEntryText writes caller-supplied text to the clipboard and raises the
// same footer confirmation copyDialogSelection does. The tree action menu
// owns the copy (it knows the full entry text, which no row of the tree
// dialog carries) and must not have to fake a selection to reuse the copy
// path.
func (m *Model) CopyEntryText(text string) tea.Cmd {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	if err := clipWrite(text); err != nil {
		m.copyGen++
		m.copyHint = errStyle.Render("copy failed: " + err.Error())
	} else {
		m.copyGen++
		m.copyHint = okStyle.Render(fmt.Sprintf("✓ copied %d chars to clipboard", len([]rune(text))))
	}
	m.applyPopupH()
	m.Refresh()
	gen := m.copyGen
	return tea.Tick(copyHintTTL, func(time.Time) tea.Msg {
		return clearCopyHintMsg{gen: gen}
	})
}

// dialogFoot renders an open dialog's footer: the confirmation while it is
// live, otherwise the caller's key hints. Callers pass their own hint text
// so the two dialogs keep their existing wording.
func (m Model) dialogFoot(hints string) string {
	if m.copyHint != "" {
		return m.copyHint
	}
	return hints
}
