package keys

import (
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"pitago/src/tui/ev"
)

// MouseTracker converts tview mouse actions (as seen by
// Application.SetMouseCapture) into the MouseMsgs Bubble Tea v1 delivered
// for the same SGR reports: one per report, Press / Motion / Release with
// the button, wheel as Press of a wheel button, 0-based X/Y, Shift/Alt/Ctrl.
//
// tview fires several actions per tcell event (MouseMove before the
// Down/Up of the same report, then Click/DoubleClick after an Up), so most
// actions must be dropped to keep one message per report. The only state is
// the held button, needed to tell "motion while held" (Motion+Left) from
// the MouseMove that merely precedes a Down or an Up.
//
// The zero value is ready to use. Call from the event goroutine only.
type MouseTracker struct {
	held ev.MouseButton // MouseButtonNone when nothing is pressed
}

// FromMouse maps one (event, action) pair. It returns nil for actions that
// carry no report of their own.
func (t *MouseTracker) FromMouse(e *tcell.EventMouse, action tview.MouseAction) []ev.MouseMsg {
	if e == nil {
		return nil
	}
	x, y := e.Position()
	mod := e.Modifiers()
	m := ev.MouseEvent{
		X: x, Y: y,
		Shift: mod&tcell.ModShift != 0,
		Alt:   mod&tcell.ModAlt != 0,
		Ctrl:  mod&tcell.ModCtrl != 0,
	}
	buttons := e.Buttons()

	switch action {
	case tview.MouseMove:
		// A wheel report, or the report that a Down/Up action is about to
		// describe: those actions speak for it.
		if buttons&(tcell.WheelUp|tcell.WheelDown|tcell.WheelLeft|tcell.WheelRight) != 0 {
			return nil
		}
		b := buttonOf(buttons)
		if b != t.held {
			return nil
		}
		m.Action, m.Button = ev.MouseActionMotion, b

	case tview.MouseLeftDown, tview.MouseMiddleDown, tview.MouseRightDown:
		m.Action, m.Button = ev.MouseActionPress, downButton(action)
		t.held = m.Button

	case tview.MouseLeftUp, tview.MouseMiddleUp, tview.MouseRightUp:
		// tea reports the released button on an SGR release.
		m.Action, m.Button = ev.MouseActionRelease, upButton(action)
		t.held = ev.MouseButtonNone

	case tview.MouseScrollUp, tview.MouseScrollDown, tview.MouseScrollLeft, tview.MouseScrollRight:
		m.Action, m.Button = ev.MouseActionPress, wheelButton(action)

	default: // Click / DoubleClick: synthesized by tview after an Up.
		return nil
	}
	m.Type = legacyType(m)
	return []ev.MouseMsg{ev.MouseMsg(m)}
}

func buttonOf(b tcell.ButtonMask) ev.MouseButton {
	switch {
	case b&tcell.ButtonPrimary != 0:
		return ev.MouseButtonLeft
	case b&tcell.ButtonMiddle != 0:
		return ev.MouseButtonMiddle
	case b&tcell.ButtonSecondary != 0:
		return ev.MouseButtonRight
	}
	return ev.MouseButtonNone
}

func downButton(a tview.MouseAction) ev.MouseButton {
	switch a {
	case tview.MouseMiddleDown:
		return ev.MouseButtonMiddle
	case tview.MouseRightDown:
		return ev.MouseButtonRight
	}
	return ev.MouseButtonLeft
}

func upButton(a tview.MouseAction) ev.MouseButton {
	switch a {
	case tview.MouseMiddleUp:
		return ev.MouseButtonMiddle
	case tview.MouseRightUp:
		return ev.MouseButtonRight
	}
	return ev.MouseButtonLeft
}

func wheelButton(a tview.MouseAction) ev.MouseButton {
	switch a {
	case tview.MouseScrollDown:
		return ev.MouseButtonWheelDown
	case tview.MouseScrollLeft:
		return ev.MouseButtonWheelLeft
	case tview.MouseScrollRight:
		return ev.MouseButtonWheelRight
	}
	return ev.MouseButtonWheelUp
}

// legacyType fills the deprecated Type field the way tea's parser did.
func legacyType(m ev.MouseEvent) ev.MouseEventType {
	switch m.Action {
	case ev.MouseActionRelease:
		return ev.MouseRelease
	case ev.MouseActionMotion:
		switch m.Button {
		case ev.MouseButtonLeft:
			return ev.MouseLeft
		case ev.MouseButtonMiddle:
			return ev.MouseMiddle
		case ev.MouseButtonRight:
			return ev.MouseRight
		}
		return ev.MouseMotion
	}
	switch m.Button {
	case ev.MouseButtonLeft:
		return ev.MouseLeft
	case ev.MouseButtonMiddle:
		return ev.MouseMiddle
	case ev.MouseButtonRight:
		return ev.MouseRight
	case ev.MouseButtonWheelUp:
		return ev.MouseWheelUp
	case ev.MouseButtonWheelDown:
		return ev.MouseWheelDown
	case ev.MouseButtonWheelLeft:
		return ev.MouseWheelLeft
	case ev.MouseButtonWheelRight:
		return ev.MouseWheelRight
	}
	return ev.MouseUnknown
}
