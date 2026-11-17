// Code in this file is adapted from github.com/charmbracelet/bubbletea v1.3.10
// (MIT License, Copyright (c) 2020-2025 Charmbracelet, Inc). The key and mouse
// types are kept value-identical (same KeyType numbers, same String() output)
// so src/app's dispatch tables and tests survive the move off Bubble Tea.

package ev

import "strings"

type KeyMsg Key

// String returns a string representation for a key message. It's safe (and
// encouraged) for use in key comparison.
func (k KeyMsg) String() (str string) {
	return Key(k).String()
}

// Key contains information about a keypress.
type Key struct {
	Type  KeyType
	Runes []rune
	Alt   bool
	Paste bool
}

// String returns a friendly string representation for a key. It's safe (and
// encouraged) for use in key comparison.
//
//	k := Key{Type: KeyEnter}
//	fmt.Println(k)
//	// Output: enter
func (k Key) String() (str string) {
	var buf strings.Builder
	if k.Alt {
		buf.WriteString("alt+")
	}
	if k.Type == KeyRunes {
		if k.Paste {
			// Note: bubbles/keys bindings currently do string compares to
			// recognize shortcuts. Since pasted text should never activate
			// shortcuts, we need to ensure that the binding code doesn't
			// match Key events that result from pastes. We achieve this
			// here by enclosing pastes in '[...]' so that the string
			// comparison in Matches() fails in that case.
			buf.WriteByte('[')
		}
		buf.WriteString(string(k.Runes))
		if k.Paste {
			buf.WriteByte(']')
		}
		return buf.String()
	} else if s, ok := keyNames[k.Type]; ok {
		buf.WriteString(s)
		return buf.String()
	}
	return ""
}

// KeyType indicates the key pressed, such as KeyEnter or KeyBreak or KeyCtrlC.
// All other keys will be type KeyRunes. To get the rune value, check the Rune
// method on a Key struct, or use the Key.String() method:
//
//	k := Key{Type: KeyRunes, Runes: []rune{'a'}, Alt: true}
//	if k.Type == KeyRunes {
//
//	    fmt.Println(k.Runes)
//	    // Output: a
//
//	    fmt.Println(k.String())
//	    // Output: alt+a
//
//	}
type KeyType int

func (k KeyType) String() (str string) {
	if s, ok := keyNames[k]; ok {
		return s
	}
	return ""
}

// Control keys. We could do this with an iota, but the values are very
// specific, so we set the values explicitly to avoid any confusion.
//
// See also:
// https://en.wikipedia.org/wiki/C0_and_C1_control_codes
const (
	keyNUL KeyType = 0   // null, \0
	keySOH KeyType = 1   // start of heading
	keySTX KeyType = 2   // start of text
	keyETX KeyType = 3   // break, ctrl+c
	keyEOT KeyType = 4   // end of transmission
	keyENQ KeyType = 5   // enquiry
	keyACK KeyType = 6   // acknowledge
	keyBEL KeyType = 7   // bell, \a
	keyBS  KeyType = 8   // backspace
	keyHT  KeyType = 9   // horizontal tabulation, \t
	keyLF  KeyType = 10  // line feed, \n
	keyVT  KeyType = 11  // vertical tabulation \v
	keyFF  KeyType = 12  // form feed \f
	keyCR  KeyType = 13  // carriage return, \r
	keySO  KeyType = 14  // shift out
	keySI  KeyType = 15  // shift in
	keyDLE KeyType = 16  // data link escape
	keyDC1 KeyType = 17  // device control one
	keyDC2 KeyType = 18  // device control two
	keyDC3 KeyType = 19  // device control three
	keyDC4 KeyType = 20  // device control four
	keyNAK KeyType = 21  // negative acknowledge
	keySYN KeyType = 22  // synchronous idle
	keyETB KeyType = 23  // end of transmission block
	keyCAN KeyType = 24  // cancel
	keyEM  KeyType = 25  // end of medium
	keySUB KeyType = 26  // substitution
	keyESC KeyType = 27  // escape, \e
	keyFS  KeyType = 28  // file separator
	keyGS  KeyType = 29  // group separator
	keyRS  KeyType = 30  // record separator
	keyUS  KeyType = 31  // unit separator
	keyDEL KeyType = 127 // delete. on most systems this is mapped to backspace, I hear
)

// Control key aliases.
const (
	KeyNull      KeyType = keyNUL
	KeyBreak     KeyType = keyETX
	KeyEnter     KeyType = keyCR
	KeyBackspace KeyType = keyDEL
	KeyTab       KeyType = keyHT
	KeyEsc       KeyType = keyESC
	KeyEscape    KeyType = keyESC

	KeyCtrlAt           KeyType = keyNUL // ctrl+@
	KeyCtrlA            KeyType = keySOH
	KeyCtrlB            KeyType = keySTX
	KeyCtrlC            KeyType = keyETX
	KeyCtrlD            KeyType = keyEOT
	KeyCtrlE            KeyType = keyENQ
	KeyCtrlF            KeyType = keyACK
	KeyCtrlG            KeyType = keyBEL
	KeyCtrlH            KeyType = keyBS
	KeyCtrlI            KeyType = keyHT
	KeyCtrlJ            KeyType = keyLF
	KeyCtrlK            KeyType = keyVT
	KeyCtrlL            KeyType = keyFF
	KeyCtrlM            KeyType = keyCR
	KeyCtrlN            KeyType = keySO
	KeyCtrlO            KeyType = keySI
	KeyCtrlP            KeyType = keyDLE
	KeyCtrlQ            KeyType = keyDC1
	KeyCtrlR            KeyType = keyDC2
	KeyCtrlS            KeyType = keyDC3
	KeyCtrlT            KeyType = keyDC4
	KeyCtrlU            KeyType = keyNAK
	KeyCtrlV            KeyType = keySYN
	KeyCtrlW            KeyType = keyETB
	KeyCtrlX            KeyType = keyCAN
	KeyCtrlY            KeyType = keyEM
	KeyCtrlZ            KeyType = keySUB
	KeyCtrlOpenBracket  KeyType = keyESC // ctrl+[
	KeyCtrlBackslash    KeyType = keyFS  // ctrl+\
	KeyCtrlCloseBracket KeyType = keyGS  // ctrl+]
	KeyCtrlCaret        KeyType = keyRS  // ctrl+^
	KeyCtrlUnderscore   KeyType = keyUS  // ctrl+_
	KeyCtrlQuestionMark KeyType = keyDEL // ctrl+?
)

// Other keys.
const (
	KeyRunes KeyType = -(iota + 1)
	KeyUp
	KeyDown
	KeyRight
	KeyLeft
	KeyShiftTab
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDown
	KeyCtrlPgUp
	KeyCtrlPgDown
	KeyDelete
	KeyInsert
	KeySpace
	KeyCtrlUp
	KeyCtrlDown
	KeyCtrlRight
	KeyCtrlLeft
	KeyCtrlHome
	KeyCtrlEnd
	KeyShiftUp
	KeyShiftDown
	KeyShiftRight
	KeyShiftLeft
	KeyShiftHome
	KeyShiftEnd
	KeyCtrlShiftUp
	KeyCtrlShiftDown
	KeyCtrlShiftLeft
	KeyCtrlShiftRight
	KeyCtrlShiftHome
	KeyCtrlShiftEnd
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
	KeyF13
	KeyF14
	KeyF15
	KeyF16
	KeyF17
	KeyF18
	KeyF19
	KeyF20
)

// Mappings for control keys and other special keys to friendly consts.
var keyNames = map[KeyType]string{
	// Control keys.
	keyNUL: "ctrl+@", // also ctrl+` (that's ctrl+backtick)
	keySOH: "ctrl+a",
	keySTX: "ctrl+b",
	keyETX: "ctrl+c",
	keyEOT: "ctrl+d",
	keyENQ: "ctrl+e",
	keyACK: "ctrl+f",
	keyBEL: "ctrl+g",
	keyBS:  "ctrl+h",
	keyHT:  "tab", // also ctrl+i
	keyLF:  "ctrl+j",
	keyVT:  "ctrl+k",
	keyFF:  "ctrl+l",
	keyCR:  "enter",
	keySO:  "ctrl+n",
	keySI:  "ctrl+o",
	keyDLE: "ctrl+p",
	keyDC1: "ctrl+q",
	keyDC2: "ctrl+r",
	keyDC3: "ctrl+s",
	keyDC4: "ctrl+t",
	keyNAK: "ctrl+u",
	keySYN: "ctrl+v",
	keyETB: "ctrl+w",
	keyCAN: "ctrl+x",
	keyEM:  "ctrl+y",
	keySUB: "ctrl+z",
	keyESC: "esc",
	keyFS:  "ctrl+\\",
	keyGS:  "ctrl+]",
	keyRS:  "ctrl+^",
	keyUS:  "ctrl+_",
	keyDEL: "backspace",

	// Other keys.
	KeyRunes:          "runes",
	KeyUp:             "up",
	KeyDown:           "down",
	KeyRight:          "right",
	KeySpace:          " ", // for backwards compatibility
	KeyLeft:           "left",
	KeyShiftTab:       "shift+tab",
	KeyHome:           "home",
	KeyEnd:            "end",
	KeyCtrlHome:       "ctrl+home",
	KeyCtrlEnd:        "ctrl+end",
	KeyShiftHome:      "shift+home",
	KeyShiftEnd:       "shift+end",
	KeyCtrlShiftHome:  "ctrl+shift+home",
	KeyCtrlShiftEnd:   "ctrl+shift+end",
	KeyPgUp:           "pgup",
	KeyPgDown:         "pgdown",
	KeyCtrlPgUp:       "ctrl+pgup",
	KeyCtrlPgDown:     "ctrl+pgdown",
	KeyDelete:         "delete",
	KeyInsert:         "insert",
	KeyCtrlUp:         "ctrl+up",
	KeyCtrlDown:       "ctrl+down",
	KeyCtrlRight:      "ctrl+right",
	KeyCtrlLeft:       "ctrl+left",
	KeyShiftUp:        "shift+up",
	KeyShiftDown:      "shift+down",
	KeyShiftRight:     "shift+right",
	KeyShiftLeft:      "shift+left",
	KeyCtrlShiftUp:    "ctrl+shift+up",
	KeyCtrlShiftDown:  "ctrl+shift+down",
	KeyCtrlShiftLeft:  "ctrl+shift+left",
	KeyCtrlShiftRight: "ctrl+shift+right",
	KeyF1:             "f1",
	KeyF2:             "f2",
	KeyF3:             "f3",
	KeyF4:             "f4",
	KeyF5:             "f5",
	KeyF6:             "f6",
	KeyF7:             "f7",
	KeyF8:             "f8",
	KeyF9:             "f9",
	KeyF10:            "f10",
	KeyF11:            "f11",
	KeyF12:            "f12",
	KeyF13:            "f13",
	KeyF14:            "f14",
	KeyF15:            "f15",
	KeyF16:            "f16",
	KeyF17:            "f17",
	KeyF18:            "f18",
	KeyF19:            "f19",
	KeyF20:            "f20",
}

type MouseMsg MouseEvent

// String returns a string representation of a mouse event.
func (m MouseMsg) String() string {
	return MouseEvent(m).String()
}

// MouseEvent represents a mouse event, which could be a click, a scroll wheel
// movement, a cursor movement, or a combination.
type MouseEvent struct {
	X      int
	Y      int
	Shift  bool
	Alt    bool
	Ctrl   bool
	Action MouseAction
	Button MouseButton

	// Deprecated: Use MouseAction & MouseButton instead.
	Type MouseEventType
}

// IsWheel returns true if the mouse event is a wheel event.
func (m MouseEvent) IsWheel() bool {
	return m.Button == MouseButtonWheelUp || m.Button == MouseButtonWheelDown ||
		m.Button == MouseButtonWheelLeft || m.Button == MouseButtonWheelRight
}

// String returns a string representation of a mouse event.
func (m MouseEvent) String() (s string) {
	if m.Ctrl {
		s += "ctrl+"
	}
	if m.Alt {
		s += "alt+"
	}
	if m.Shift {
		s += "shift+"
	}

	if m.Button == MouseButtonNone { //nolint:nestif
		if m.Action == MouseActionMotion || m.Action == MouseActionRelease {
			s += mouseActions[m.Action]
		} else {
			s += "unknown"
		}
	} else if m.IsWheel() {
		s += mouseButtons[m.Button]
	} else {
		btn := mouseButtons[m.Button]
		if btn != "" {
			s += btn
		}
		act := mouseActions[m.Action]
		if act != "" {
			s += " " + act
		}
	}

	return s
}

// MouseAction represents the action that occurred during a mouse event.
type MouseAction int

// Mouse event actions.
const (
	MouseActionPress MouseAction = iota
	MouseActionRelease
	MouseActionMotion
)

var mouseActions = map[MouseAction]string{
	MouseActionPress:   "press",
	MouseActionRelease: "release",
	MouseActionMotion:  "motion",
}

// MouseButton represents the button that was pressed during a mouse event.
type MouseButton int

// Mouse event buttons
//
// This is based on X11 mouse button codes.
//
//	1 = left button
//	2 = middle button (pressing the scroll wheel)
//	3 = right button
//	4 = turn scroll wheel up
//	5 = turn scroll wheel down
//	6 = push scroll wheel left
//	7 = push scroll wheel right
//	8 = 4th button (aka browser backward button)
//	9 = 5th button (aka browser forward button)
//	10
//	11
//
// Other buttons are not supported.
const (
	MouseButtonNone MouseButton = iota
	MouseButtonLeft
	MouseButtonMiddle
	MouseButtonRight
	MouseButtonWheelUp
	MouseButtonWheelDown
	MouseButtonWheelLeft
	MouseButtonWheelRight
	MouseButtonBackward
	MouseButtonForward
	MouseButton10
	MouseButton11
)

var mouseButtons = map[MouseButton]string{
	MouseButtonNone:       "none",
	MouseButtonLeft:       "left",
	MouseButtonMiddle:     "middle",
	MouseButtonRight:      "right",
	MouseButtonWheelUp:    "wheel up",
	MouseButtonWheelDown:  "wheel down",
	MouseButtonWheelLeft:  "wheel left",
	MouseButtonWheelRight: "wheel right",
	MouseButtonBackward:   "backward",
	MouseButtonForward:    "forward",
	MouseButton10:         "button 10",
	MouseButton11:         "button 11",
}

// MouseEventType indicates the type of mouse event occurring.
//
// Deprecated: Use MouseAction & MouseButton instead.
type MouseEventType int

// Mouse event types.
//
// Deprecated: Use MouseAction & MouseButton instead.
const (
	MouseUnknown MouseEventType = iota
	MouseLeft
	MouseRight
	MouseMiddle
	MouseRelease // mouse button release (X10 only)
	MouseWheelUp
	MouseWheelDown
	MouseWheelLeft
	MouseWheelRight
	MouseBackward
	MouseForward
	MouseMotion
)
