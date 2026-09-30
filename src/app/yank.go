package app

import (
	"fmt"
	"strings"

	atottoclipboard "github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/components/clipboard"
	"pitago/src/components/yank"
)

// YankLast copies the last assistant answer to the system clipboard.
// Drag-select in the terminal always grabs the sidebar too (same rows),
// so this is the clean way to copy chat-only text.

func (m *Model) YankLast() tea.Cmd {
	text := yank.LastAssistantText(m.blocks)
	if strings.TrimSpace(text) == "" {
		m.AddBlock(Block{Kind: "notice", Text: "nothing to yank yet"})
		m.Refresh()
		return nil
	}
	m.YankText(text)
	return nil
}

// YankText copies arbitrary text to the clipboard with a toast popup. The
// transport (local system clipboard vs OSC 52 for Orca-hosted sessions) is
// chosen by components/clipboard and surfaced honestly in the toast.
func (m *Model) YankText(text string) {
	st := clipboard.Write(text)
	switch st.Channel {
	case clipboard.Atoto:
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("yanked %d chars to clipboard", st.Chars)})
	case clipboard.Osc52:
		m.AddBlock(Block{Kind: "notice", Text: fmt.Sprintf("yanked %d chars (osc52 — terminal-handled)", st.Chars)})
	default:
		msg := "copy unavailable — try /yank picker"
		if st.Err != nil {
			msg += " · " + st.Err.Error()
		}
		m.AddBlock(Block{Kind: "notice", Text: msg, Err: true})
	}
	m.Refresh()
}

// writeClipboard is the single clipboard write, overridable in tests. It
// is separated from YankText because a modal dialog already covers the
// toast overlay (View returns the dialog before overlayToasts runs), so a
// notice raised from inside one would surface as a stale popup long after
// the copy. Dialog copies report through the dialog footer instead.
var clipWrite = func(text string) error { return atottoclipboard.WriteAll(text) }

// OpenYank shows the message picker: ↑↓ pick any chat message, Enter
// copies its full text. The sidebar stays visible — copied text is
// always chat-only, like opencode's single-column copy.

func (m *Model) OpenYank() tea.Cmd {
	opts, descs, payload := yank.Entries(m.blocks)
	if len(opts) == 0 {
		m.AddBlock(Block{Kind: "notice", Text: "nothing to yank yet"})
		m.Refresh()
		return nil
	}
	d := &Dialog{Kind: "yank", Title: "Yank message to clipboard",
		Options: opts, Descs: descs, Payload: payload}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	m.Refresh()
	return nil
}
