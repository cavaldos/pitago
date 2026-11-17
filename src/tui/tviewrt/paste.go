package tviewrt

import (
	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
	"pitago/src/tui/keys"
)

// Bracketed pastes reach the model byte-exact, as the one KeyMsg Bubble
// Tea v1 delivered (CR vs LF, tabs and C0 bytes kept), because the screen
// tview polls is wrapped in keys.PasteScreen. tview's own paste path,
// used only when the wrapper is missing, drops LF and every control byte.

// wrapPaste wraps scr and switches the PasteHandler to decoding.
func (r *Runtime) wrapPaste(scr tcell.Screen) tcell.Screen {
	r.exactPaste = true
	return keys.NewPasteScreen(scr)
}

// openTerminal creates the real-terminal screen here rather than letting
// tview's Run do it, which would be too late to wrap. It initializes the
// screen itself because tview's SetScreen discards Init's error; the
// wrapper's Init is idempotent, so SetScreen's own call is a no-op. Errors
// (no tty) surface from Run exactly as they did when tview made the screen.
func (r *Runtime) openTerminal() error {
	scr, err := tcell.NewScreen()
	if err != nil {
		return err
	}
	ps := keys.NewPasteScreen(scr)
	if err := ps.Init(); err != nil {
		return err
	}
	r.exactPaste = true
	r.app.SetScreen(ps)
	return nil
}

// pasteMsg converts the text tview hands the PasteHandler.
func (r *Runtime) pasteMsg(text string) ev.KeyMsg {
	if r.exactPaste {
		return keys.PasteMsg(text)
	}
	return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune(text), Paste: true}
}
