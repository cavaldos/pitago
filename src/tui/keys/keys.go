// Package keys turns tcell input events into the app's event vocabulary
// (pitago/src/tui/ev), which is value-identical to Bubble Tea v1: the same
// KeyType numbers and the same String() output ("ctrl+x", "alt+enter",
// "shift+tab", "pgdown", "esc", runes, "[pasted]"). src/app keeps its
// dispatch tables — switches on Type, String() compares, "alt+x" shortcuts
// persisted in prefs.json — unchanged.
//
// The rule every mapping follows is physical-key parity: for a keypress,
// produce the KeyMsg Bubble Tea v1 produced for the same keypress on a
// legacy xterm. That matters because tcell is not a byte parser we can
// mirror. It hands us a normalized (Key, Rune, ModMask), and on xterm-like
// terminals it also turns on modifyOtherKeys(2), kitty CSI-u(1) and
// win32-input-mode, so the terminal now reports things tea never saw:
// ctrl+h apart from backspace, shift+enter, ctrl+i apart from tab. Those
// collapse here to the names tea used ("ctrl+h" stays reachable; shift+enter
// is "enter"). Code that wants the richer information can read
// ev.Modifiers() on the tcell event directly.
//
// Where tea delivered nothing for a key (it parsed the sequence as unknown:
// shift+pgup, ctrl+f5, shift+insert…), FromKey returns ok=false, so those
// keys stay dead exactly as before. keys_test.go replays a 250+ sequence
// corpus through both real parsers and lists every remaining difference in
// knownGaps.
package keys

import (
	"unicode"

	"github.com/gdamore/tcell/v2"

	"pitago/src/tui/ev"
)

// FromKey maps one tcell key event. ok=false means Bubble Tea v1 delivered
// no KeyMsg for this keypress; drop the event.
func FromKey(e *tcell.EventKey) (ev.KeyMsg, bool) {
	mod := e.Modifiers()
	alt := mod&tcell.ModAlt != 0
	ctrl := mod&tcell.ModCtrl != 0
	shift := mod&tcell.ModShift != 0
	msg := func(t ev.KeyType) (ev.KeyMsg, bool) { return ev.KeyMsg{Type: t, Alt: alt}, true }

	switch key := e.Key(); {
	case key == tcell.KeyRune:
		r := e.Rune()
		if ctrl {
			if t, ok := legacyCtrl(r); ok {
				return msg(t)
			}
			// ctrl+1, ctrl+, …: a legacy terminal sent the bare character.
		}
		if r == ' ' {
			return ev.KeyMsg{Type: ev.KeySpace, Runes: []rune{' '}, Alt: alt}, true
		}
		return ev.KeyMsg{Type: ev.KeyRunes, Runes: []rune{r}, Alt: alt}, true

	case key >= tcell.KeyCtrlA && key <= tcell.KeyCtrlZ: // 65…90 in tcell
		return msg(ev.KeyType(key-tcell.KeyCtrlA) + ev.KeyCtrlA)
	case key == tcell.KeyCtrlSpace:
		return msg(ev.KeyCtrlAt)
	case key == tcell.KeyCtrlLeftSq:
		return msg(ev.KeyEsc)
	case key == tcell.KeyCtrlBackslash:
		return msg(ev.KeyCtrlBackslash)
	case key == tcell.KeyCtrlRightSq:
		return msg(ev.KeyCtrlCloseBracket)
	case key == tcell.KeyCtrlCarat:
		return msg(ev.KeyCtrlCaret)
	case key == tcell.KeyCtrlUnderscore:
		return msg(ev.KeyCtrlUnderscore)

	// Keys whose legacy byte ignores shift/ctrl: same name either way.
	case key == tcell.KeyBackspace || key == tcell.KeyBackspace2:
		if ctrl { // legacy ctrl+backspace sends BS (0x08): tea's ctrl+h
			return msg(ev.KeyCtrlH)
		}
		return msg(ev.KeyBackspace)
	case key == tcell.KeyTab:
		if shift {
			return msg(ev.KeyShiftTab)
		}
		return msg(ev.KeyTab)
	case key == tcell.KeyBacktab:
		return msg(ev.KeyShiftTab)
	case key == tcell.KeyEnter:
		return msg(ev.KeyEnter)
	case key == tcell.KeyEsc:
		return msg(ev.KeyEsc)

	// Keys whose legacy encoding carries modifiers tea could not name.
	case key == tcell.KeyInsert:
		if alt || ctrl || shift {
			return ev.KeyMsg{}, false
		}
		return msg(ev.KeyInsert)
	case key == tcell.KeyDelete:
		if ctrl || shift {
			return ev.KeyMsg{}, false
		}
		return msg(ev.KeyDelete)
	case key == tcell.KeyPgUp || key == tcell.KeyPgDn:
		if shift {
			return ev.KeyMsg{}, false
		}
		up := key == tcell.KeyPgUp
		switch {
		case up && ctrl:
			return msg(ev.KeyCtrlPgUp)
		case up:
			return msg(ev.KeyPgUp)
		case ctrl:
			return msg(ev.KeyCtrlPgDown)
		}
		return msg(ev.KeyPgDown)
	}

	if t, ok := quad(e.Key(), ctrl, shift); ok {
		return msg(t)
	}
	if n := int(e.Key() - tcell.KeyF1); n >= 0 && n < 20 {
		// xterm sends shift+F1..F8 as F13..F20 and tea named them so;
		// ctrl+Fn and shift+F9..F12 were unknown sequences to tea.
		switch {
		case ctrl:
			return ev.KeyMsg{}, false
		case shift && n < 8:
			n += 12
		case shift:
			return ev.KeyMsg{}, false
		}
		return msg(ev.KeyF1 - ev.KeyType(n)) // named keys count down
	}
	return ev.KeyMsg{}, false // F21+, Print, Pause, Menu, …
}

// KeyString is FromKey(e).String(), or "" when tea had no key for it.
func KeyString(e *tcell.EventKey) string {
	if k, ok := FromKey(e); ok {
		return k.String()
	}
	return ""
}

// legacyCtrl is what ctrl+<r> produced without key-reporting extensions: a
// C0 byte, which tea then named. It accepts both spellings tcell uses: the
// key itself (CSI-u: ctrl+'h') and the C0 byte + 0x60 that tcell reports
// for an ESC-prefixed control byte ('|' for alt+ctrl+\). ok=false for
// characters with no control form (those arrived bare).
func legacyCtrl(r rune) (ev.KeyType, bool) {
	r = unicode.ToLower(r)
	switch {
	case r >= 'a' && r <= 'z':
		return ev.KeyType(r-'a') + ev.KeyCtrlA, true
	case r == '@' || r == ' ' || r == '2' || r == '`':
		return ev.KeyCtrlAt, true
	case r == '[' || r == '3' || r == '{':
		return ev.KeyEsc, true
	case r == '\\' || r == '4' || r == '|':
		return ev.KeyCtrlBackslash, true
	case r == ']' || r == '5' || r == '}':
		return ev.KeyCtrlCloseBracket, true
	case r == '^' || r == '6' || r == '~':
		return ev.KeyCtrlCaret, true
	case r == '_' || r == '/' || r == '7' || r == '-':
		return ev.KeyCtrlUnderscore, true
	case r == '?' || r == '8':
		return ev.KeyBackspace, true
	}
	return 0, false
}

// quad maps arrows and home/end, which tea named in every ctrl/shift combo.
func quad(key tcell.Key, ctrl, shift bool) (ev.KeyType, bool) {
	var row [4]ev.KeyType // plain, ctrl, shift, ctrl+shift
	switch key {
	case tcell.KeyUp:
		row = [4]ev.KeyType{ev.KeyUp, ev.KeyCtrlUp, ev.KeyShiftUp, ev.KeyCtrlShiftUp}
	case tcell.KeyDown:
		row = [4]ev.KeyType{ev.KeyDown, ev.KeyCtrlDown, ev.KeyShiftDown, ev.KeyCtrlShiftDown}
	case tcell.KeyLeft:
		row = [4]ev.KeyType{ev.KeyLeft, ev.KeyCtrlLeft, ev.KeyShiftLeft, ev.KeyCtrlShiftLeft}
	case tcell.KeyRight:
		row = [4]ev.KeyType{ev.KeyRight, ev.KeyCtrlRight, ev.KeyShiftRight, ev.KeyCtrlShiftRight}
	case tcell.KeyHome:
		row = [4]ev.KeyType{ev.KeyHome, ev.KeyCtrlHome, ev.KeyShiftHome, ev.KeyCtrlShiftHome}
	case tcell.KeyEnd:
		row = [4]ev.KeyType{ev.KeyEnd, ev.KeyCtrlEnd, ev.KeyShiftEnd, ev.KeyCtrlShiftEnd}
	default:
		return 0, false
	}
	i := 0
	if ctrl {
		i |= 1
	}
	if shift {
		i |= 2
	}
	return row[i], true
}
