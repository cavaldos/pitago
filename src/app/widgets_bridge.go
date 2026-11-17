package app

import (
	"github.com/charmbracelet/bubbles/textarea"

	tea "pitago/src/tui/ev"
	"pitago/src/tui/teabridge"
	"pitago/src/tui/viewport"
)

// vpUpdate forwards to the app-owned viewport (src/tui/viewport). The input
// textarea is still the Bubbles widget, which takes and returns Bubble Tea
// types; taUpdate keeps that conversion in one place until it is replaced.

func vpUpdate(v viewport.Model, msg tea.Msg) (viewport.Model, tea.Cmd) {
	return v.Update(msg)
}

func taUpdate(t textarea.Model, msg tea.Msg) (textarea.Model, tea.Cmd) {
	nt, c := t.Update(teabridge.ToTea(msg))
	return nt, teabridge.FromTeaCmd(c)
}
