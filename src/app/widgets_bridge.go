package app

import (
	"pitago/src/components/textarea"

	tea "pitago/src/tui/ev"
	"pitago/src/tui/viewport"
)

// vpUpdate and taUpdate forward to the app-owned viewport and textarea
// (src/tui/viewport, src/components/textarea), which speak ev directly.

func vpUpdate(v viewport.Model, msg tea.Msg) (viewport.Model, tea.Cmd) {
	return v.Update(msg)
}

func taUpdate(t textarea.Model, msg tea.Msg) (textarea.Model, tea.Cmd) {
	return t.Update(msg)
}
