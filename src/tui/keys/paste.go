package keys

import (
	"strings"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
)

// Bracketed paste with Bubble Tea v1 fidelity under tview.
//
// Bubble Tea delivered a paste as one KeyMsg holding the raw pasted runes:
// CR stays CR, LF stays LF, tabs and other C0 bytes survive. tcell instead
// parses the paste into key events (CR → KeyEnter, LF → KeyCtrlJ, 0x01 →
// KeyCtrlA, ESC x → Alt+x), and tview rebuilds the text from them keeping
// only runes, Enter (as '\n') and Tab — so an LF-only paste loses every
// newline and CR vs LF is gone.
//
// PasteScreen wraps the screen tview polls. Inside a paste it re-encodes
// every key event as a plain rune event that tview's paste buffer keeps
// verbatim: control keys become private-use runes (ctrlBase + byte). The
// paste still travels through tview's own event channel, so it stays in
// order with the keys typed before and after it. DecodePaste, called from
// the PasteHandler, turns the text back into the runes the terminal sent.
//
// Exactness: printable text, CR, LF, TAB, every C0 byte, DEL and ESC+char
// round-trip exactly. A real pasted rune inside the private block is
// escaped, so it round-trips too. The one loss: escape *sequences* inside a
// paste (arrow keys, CSI) have already been parsed by tcell into named keys
// and are rebuilt in their xterm form, which may differ from the bytes the
// terminal sent (see knownPasteGaps in the test).
// tcell's unexported paste marker keys (key.go: keyPasteStart = 16384).
// They only surface when an ESC precedes the marker; TestPasteParity's
// trailing-ESC case fails if a tcell upgrade renumbers them.
const (
	tcellPasteStart tcell.Key = 16384
	tcellPasteEnd   tcell.Key = 16385
)

const (
	ctrlBase   rune = 0xF0000 // + byte for C0 (0x00-0x1F) and DEL (0x7F)
	escLiteral rune = 0xF00FE // next rune is literal (a pasted rune in [ctrlBase, ctrlBase+0xFF])
	ctrlLimit  rune = ctrlBase + 0xFF
)

// PasteScreen is a tcell.Screen whose PollEvent rewrites paste contents.
// Give it to tview with app.SetScreen and keep app.EnablePaste(true).
type PasteScreen struct {
	tcell.Screen
	pasting bool
	pending []tcell.Event
}

// NewPasteScreen wraps s.
func NewPasteScreen(s tcell.Screen) *PasteScreen { return &PasteScreen{Screen: s} }

// PollEvent implements tcell.Screen. tview polls from a single goroutine.
func (p *PasteScreen) PollEvent() tcell.Event {
	if len(p.pending) > 0 {
		ev := p.pending[0]
		p.pending = p.pending[1:]
		return ev
	}
	ev := p.Screen.PollEvent()
	switch e := ev.(type) {
	case *tcell.EventPaste:
		p.pasting = e.Start()
	case *tcell.EventKey:
		// ESC immediately before a paste marker: tcell's parser folds the
		// ESC into an Alt modifier on its internal marker key and never
		// emits EventPaste, so the paste would never end and every later
		// key would be swallowed into it. Split it back apart.
		if k := e.Key(); k == tcellPasteStart || k == tcellPasteEnd {
			start := k == tcellPasteStart
			if p.pasting {
				p.pending = append(p.pending, tcell.NewEventPaste(start))
				p.pasting = start
				return runeEvent(ctrlBase + 0x1b)
			}
			p.pending = append(p.pending, tcell.NewEventPaste(start))
			p.pasting = start
			return tcell.NewEventKey(tcell.KeyEsc, 0, tcell.ModNone)
		}
		if p.pasting {
			out := encodePasteKey(e)
			if len(out) == 0 {
				return p.PollEvent()
			}
			p.pending = append(p.pending, out[1:]...)
			return out[0]
		}
	}
	return ev
}

// ChannelEvents implements tcell.Screen in terms of PollEvent, so the
// rewrite applies whichever way events are read.
func (p *PasteScreen) ChannelEvents(ch chan<- tcell.Event, quit <-chan struct{}) {
	defer close(ch)
	for {
		ev := p.PollEvent()
		if ev == nil {
			return
		}
		select {
		case ch <- ev:
		case <-quit:
			return
		}
	}
}

func runeEvent(r rune) tcell.Event { return tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone) }

// pasteBytes is the byte (or short sequence) the terminal sent for e, as
// runes, before tcell named it.
func pasteBytes(e *tcell.EventKey) []rune {
	var out []rune
	if e.Modifiers()&tcell.ModAlt != 0 {
		out = append(out, 0x1b) // tcell folds ESC+x into Alt+x
	}
	k := e.Key()
	switch {
	case k == tcell.KeyRune:
		r := e.Rune()
		// ESC + C0 arrives as Alt+Ctrl+<letter> (e.g. ESC CR = alt+ctrl+m).
		if e.Modifiers()&tcell.ModCtrl != 0 {
			if lr := r | 0x20; lr >= 'a' && lr <= 'z' {
				return append(out, lr&0x1f)
			}
			if r >= '@' && r <= '_' || r >= '`' && r <= 0x7f {
				return append(out, r&0x1f)
			}
		}
		return append(out, r)
	case k >= tcell.KeyCtrlA && k <= tcell.KeyCtrlZ:
		return append(out, rune(k-tcell.KeyCtrlA)+1)
	case k == tcell.KeyCtrlSpace:
		return append(out, 0)
	case k == tcell.KeyCtrlBackslash, k == tcell.KeyCtrlRightSq,
		k == tcell.KeyCtrlCarat, k == tcell.KeyCtrlUnderscore:
		return append(out, rune(k-tcell.KeyCtrlBackslash)+0x1c)
	case k == tcell.KeyEnter:
		return append(out, '\r')
	case k == tcell.KeyTab:
		return append(out, '\t')
	case k == tcell.KeyBacktab:
		return append(out, []rune("\x1b[Z")...)
	case k == tcell.KeyEsc:
		return append(out, 0x1b)
	case k == tcell.KeyBackspace2:
		return append(out, 0x7f)
	case k == tcell.KeyBackspace:
		// tcell folds BS and DEL together; DEL is what Backspace sends.
		return append(out, 0x7f)
	}
	if seq, ok := xtermSeq[k]; ok {
		return append(out, []rune(seq)...)
	}
	return out
}

// xtermSeq rebuilds named keys that can only have come from an escape
// sequence inside the paste.
var xtermSeq = map[tcell.Key]string{
	tcell.KeyUp: "\x1b[A", tcell.KeyDown: "\x1b[B", tcell.KeyRight: "\x1b[C", tcell.KeyLeft: "\x1b[D",
	tcell.KeyHome: "\x1b[H", tcell.KeyEnd: "\x1b[F", tcell.KeyInsert: "\x1b[2~", tcell.KeyDelete: "\x1b[3~",
	tcell.KeyPgUp: "\x1b[5~", tcell.KeyPgDn: "\x1b[6~",
}

func encodePasteKey(e *tcell.EventKey) []tcell.Event {
	var out []tcell.Event
	for _, r := range pasteBytes(e) {
		switch {
		case r < 0x20 || r == 0x7f:
			out = append(out, runeEvent(ctrlBase+r))
		case r >= ctrlBase && r <= ctrlLimit:
			out = append(out, runeEvent(escLiteral), runeEvent(r))
		default:
			out = append(out, runeEvent(r))
		}
	}
	return out
}

// DecodePaste turns the text tview hands a PasteHandler (from a
// PasteScreen) back into the exact pasted runes.
func DecodePaste(text string) []rune {
	in := []rune(text)
	out := make([]rune, 0, len(in))
	for i := 0; i < len(in); i++ {
		r := in[i]
		switch {
		case r == escLiteral && i+1 < len(in):
			i++
			out = append(out, in[i])
		case r >= ctrlBase && r <= ctrlLimit:
			out = append(out, r-ctrlBase)
		default:
			out = append(out, r)
		}
	}
	return out
}

// DecodePasteString is DecodePaste as a string.
func DecodePasteString(text string) string {
	var b strings.Builder
	for _, r := range DecodePaste(text) {
		b.WriteRune(r)
	}
	return b.String()
}

// PasteMsg is the KeyMsg Bubble Tea v1 delivered for a paste: all runes in
// one message, Paste set. text is what tview gave the PasteHandler.
func PasteMsg(text string) ev.KeyMsg {
	return ev.KeyMsg{Type: ev.KeyRunes, Runes: DecodePaste(text), Paste: true}
}
