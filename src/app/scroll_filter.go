package app

import (
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// ScrollEventFilter drops the mouse messages that Bubble Tea can only answer
// with a frame it will throw away.
//
// Bubble Tea calls Model.View() once per message (tea.go:502) and the
// renderer keeps just the last frame per 60fps tick, last-write-wins
// (standard_renderer.go:441). A message that changes nothing therefore still
// costs a whole frame — ~730µs measured on a long chat, dominated by
// grapheme-aware width measurement — and then produces a byte-identical frame
// the renderer discards (standard_renderer.go:165).
//
// Two kinds of mouse traffic hit that path for nothing:
//
//   - Motion reports outside an active chat drag, which nothing else
//     reads: only Release carries meaning (the sidebar click on
//     update.go:1133). With mouse cell motion on, simply moving the
//     pointer across the window spends a frame per report.
//   - Wheel reports that arrive after the viewport is already pinned at that
//     edge. A macOS flick ends in a long momentum tail, so a fast scroll to
//     the top or the bottom ends with dozens of reports that cannot move
//     anything, each blocking the event loop for a frame. That drain is what
//     a scroll hitching as it reaches an edge actually is.
//
// Both drops are exact no-ops, not approximations: viewport.Update turns a
// wheel into ScrollUp/ScrollDown, which return early once the offset is at
// that edge, and the only other unconditional work in Update is the
// time-based pruneToasts, which the next real message redoes.
//
// Everything else passes through untouched. The conditions mirror the wheel
// branch in Update: a dialog captures the mouse before the viewports can see
// it, so nothing is dropped while one is open, and Shift+wheel is left alone
// because that is a horizontal scroll rather than a vertical one.
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
		if md.sel.Active {
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
	return msg
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
