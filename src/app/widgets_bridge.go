package app

import (
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"

	tea "pitago/src/tui/ev"
	"pitago/src/tui/teabridge"
)

// The chat/sidebar viewports and the input textarea are still Bubbles
// widgets; they take and return Bubble Tea types. These shims keep that
// conversion in one place until the tview ChatView/TextArea replace them.

func vpUpdate(v viewport.Model, msg tea.Msg) (viewport.Model, tea.Cmd) {
	nv, c := v.Update(teabridge.ToTea(msg))
	return nv, teabridge.FromTeaCmd(c)
}

func taUpdate(t textarea.Model, msg tea.Msg) (textarea.Model, tea.Cmd) {
	nt, c := t.Update(teabridge.ToTea(msg))
	return nt, teabridge.FromTeaCmd(c)
}
