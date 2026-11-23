package app

import (
	"sync"
	"time"

	tea "pitago/src/tui/ev"
	"pitago/src/tui/viewport"
)

// ScrollEventFilter drops model updates for hover and wheel reports that
// would leave the app unchanged. Dialogs own their mouse traffic; Shift+wheel
// scrolls horizontally, so neither is filtered by the vertical bounds.
func ScrollEventFilter(m tea.Model, msg tea.Msg) tea.Msg {
	mm, ok := msg.(tea.MouseMsg)
	if !ok {
		return msg
	}
	md, ok := m.(Model)
	// An open dialog may own the pointer for a drag, so nothing is dropped
	// behind one even though this app binds no motion itself.
	if !ok || !md.ready || len(md.Dialogs) > 0 {
		return msg
	}
	if mm.Action == tea.MouseActionMotion {
		// A chat drag selection reads motion (press → motion* → release):
		// while one is active every report extends the focus. Otherwise
		// no binding reads motion; see above.
		if md.sel.Active || md.sideDrag {
			return msg
		}
		// Hover highlight reads motion, but only a change in which
		// clickable row is under the pointer repaints anything.
		if md.hoverChanged(mm.X, mm.Y) {
			return msg
		}
		return nil
	}
	if mm.Shift {
		return msg
	}
	if mm.Action != tea.MouseActionPress {
		return msg
	}
	up := mm.Button == tea.MouseButtonWheelUp
	if !up && mm.Button != tea.MouseButtonWheelDown {
		return msg // left/right wheel: let Update decide
	}
	if md.overSide(mm.X) {
		if pinnedAtEdge(&md.sideVp, up) {
			return nil
		}
		return msg
	}
	if pinnedAtEdge(&md.vp, up) {
		return nil
	}
	return wheelCoalesce(mm, up)
}

// wheelBurst is the minimum gap between two chat wheel reports that each get
// their own frame. A trackpad flick delivers reports far faster than the
// 60fps renderer paints, so the extras only queue Update/View work whose
// frames are discarded. Reports inside the gap are dropped and their
// distance is carried (wheelCarry) into the next one that goes through, so
// the total scroll is unchanged — it just lands in fewer, smoother frames.
const wheelBurst = 8 * time.Millisecond

var (
	wheelMu    sync.Mutex
	wheelLast  time.Time
	wheelCarry int // signed pending reports: + down, - up
)

func wheelCoalesce(mm tea.MouseMsg, up bool) tea.Msg {
	wheelMu.Lock()
	defer wheelMu.Unlock()
	d := 1
	if up {
		d = -1
	}
	now := time.Now()
	if now.Sub(wheelLast) < wheelBurst {
		wheelCarry += d
		return nil
	}
	wheelLast = now
	return mm
}

// takeWheelCarry returns and clears the distance dropped by wheelCoalesce.
func takeWheelCarry() int {
	wheelMu.Lock()
	defer wheelMu.Unlock()
	c := wheelCarry
	wheelCarry = 0
	return c
}

// pinnedAtEdge reports whether a vertical wheel report in direction up can no
// longer move this viewport. bubbles' ScrollUp/ScrollDown both bail out at
// their edge, so dropping such a report leaves the model bit-identical.
func pinnedAtEdge(vp *viewport.Model, up bool) bool {
	if up {
		return vp.AtTop()
	}
	return vp.AtBottom()
}
