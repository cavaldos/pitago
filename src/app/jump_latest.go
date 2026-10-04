package app

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Jump to latest — pi parity: once the user scrolls away from the newest
// message, a chip floats over the transcript offering the way back (End, or
// a click). Nothing about it is stored on the Model: at-bottom is always the
// viewport's own answer, so the chip cannot drift from where the transcript
// actually is. chatFrameGeometry re-pins the painted copy when panels shrink
// it, which makes the copy — not m.vp — the honest "is the newest line on
// screen" predicate, so every check here runs against it.

// jumpLatestRowFromBottom is how far above the chat frame's bottom edge the
// chip sits. Two rows keeps it clear of the input border instead of hugging
// it, the same floating look as the toast overlay.
const jumpLatestRowFromBottom = 2

// jumpLatestMinRows is the shortest chat frame the chip is drawn on: below
// this there is no room to float anything without it reading as part of the
// transcript.
const jumpLatestMinRows = 5

// jumpLatestChip is where the chip lives in the painted chat frame. row is
// 0-based inside the frame (the header is a separate row above it); start is
// the 0-based column where the chip begins.
type jumpLatestChip struct {
	row   int
	start int
	text  string
}

// jumpLatestChip reports the chip to draw over chatVp, and whether one is
// warranted at all. It is deliberately the single source of truth for both
// painting and hit-testing: render and click must agree on the exact cell, so
// they ask the same pure function rather than recomputing the geometry twice.
func (m Model) jumpLatestChip(chatVp viewport.Model) (jumpLatestChip, bool) {
	// AtBottom is the whole gate: on a transcript that fits (or a session
	// with nothing below the fold) there is nothing to jump back to, so the
	// affordance would be pure noise.
	if chatVp.Height < jumpLatestMinRows || chatVp.AtBottom() {
		return jumpLatestChip{}, false
	}
	// Advertise the key only when the key actually reaches the chat. A
	// multi-line input (a paste) keeps End for its own caret — see the
	// scroll-key switch in update.go — so the chip names the click instead
	// of promising a shortcut that would silently do nothing.
	label := "↓ Jump to latest message"
	if strings.Contains(m.ta.Value(), "\n") {
		label += " · click"
	} else {
		label += " · End"
	}
	text := cmdHiStyle.Render(label)
	width := m.mainW()
	tw := lipgloss.Width(text)
	// Too narrow to hold the chip: skip it rather than let it wrap onto a
	// transcript row and shift the frame.
	if tw >= width {
		return jumpLatestChip{}, false
	}
	return jumpLatestChip{
		row:   max(0, chatVp.Height-1-jumpLatestRowFromBottom),
		start: (width - tw) / 2,
		text:  text,
	}, true
}

// overlayJumpLatest floats the chip onto its own transcript row. The row's
// left cells keep their text (truncANSI) and the right is padded out, so the
// chip reads as transparent over the transcript instead of punching a hole
// in it, and the row count never changes — zero layout shift, the same trick
// overlayToasts uses for notifications.
func overlayJumpLatest(rows []string, chip jumpLatestChip, width int) []string {
	if chip.row < 0 || chip.row >= len(rows) {
		return rows
	}
	left := truncANSI(rows[chip.row], chip.start)
	pad := width - lipgloss.Width(left) - lipgloss.Width(chip.text)
	if pad < 0 {
		pad = 0
	}
	var b strings.Builder
	b.Grow(len(left) + len(chip.text) + pad)
	b.WriteString(left)
	b.WriteString(chip.text)
	b.WriteString(strings.Repeat(" ", pad))
	rows[chip.row] = b.String()
	return rows
}

// JumpToLatest pulls the transcript back to the newest message. Moving m.vp
// is enough on its own: chatFrameGeometry re-pins the painted copy whenever
// m.vp is at the bottom, which is the same invariant Refresh relies on.
func (m *Model) JumpToLatest() {
	m.vp.GotoBottom()
	// A drag selection is anchored to absolute chat-content points, so after
	// a jump the highlight would land on an unrelated block. Drop it.
	m.sel = Selection{}
	m.Refresh()
}

// jumpLatestAt reports whether a mouse event is a press on the jump chip.
// It reuses jumpLatestChip so a press resolves to exactly the cells that were
// painted; there is no second geometry to keep in sync.
func (m *Model) jumpLatestAt(msg tea.MouseMsg) bool {
	if !m.Mouse || m.overSide(msg.X) {
		return false
	}
	// Only a real click counts. Wheel events carry coordinates too, and
	// scrolling *through* the chip must keep scrolling.
	if msg.Button != tea.MouseButtonLeft && msg.Button != tea.MouseButtonNone {
		return false
	}
	if msg.Action != tea.MouseActionPress && msg.Action != tea.MouseActionRelease {
		return false
	}
	chip, ok := m.jumpLatestChip(m.chatViewport())
	if !ok {
		return false
	}
	// The header occupies screen row 0, so frame row r is screen row r+1.
	if msg.Y != chip.row+1 {
		return false
	}
	return msg.X >= chip.start && msg.X < chip.start+lipgloss.Width(chip.text)
}
