// Package pet is the sidebar-pet state core — a Go port of pi's
// sidebar-pet.ts extension: the status state machine below, the ASCII
// drawing set in art.go (the creature the user picks with /pet), and the
// classic kaomoji face set this file keeps for the pre-ASCII look.
//
// Tracks the turn live: idle | thinking (reasoning streaming)
// | writing (text streaming) | working (tools/steps)
// | success (turn finished) | error.
//
// Busy states are TIMED per cycle: the row shows elapsed seconds for the
// current status period ("Thinking... 7s"). Pure transition helpers live
// here; the Model wiring (timers, tea.Cmds) stays in src/app.
package pet

import (
	"strings"
	"time"

	"pitago/src/components/format"
)

// Style names for the two sidebar-pet looks. The ASCII set is the default;
// "classic" is the original kaomoji row, kept because some people just like
// it. Neither is an animal — both are /pet entries that pick a look.
const (
	StyleASCII   = "ascii"
	StyleClassic = "classic"
)

// Status is the pet's display state.
type Status string

const (
	Idle     Status = "idle"
	Thinking Status = "thinking"
	Writing  Status = "writing"
	Working  Status = "working"
	Success  Status = "success"
	Error    Status = "error"
)

// Busy reports streaming/working states (timed per cycle).
func (p Status) Busy() bool { return p == Thinking || p == Writing || p == Working }

// Flashing reports end-of-turn states (auto-decay via flash timer).
func (p Status) Flashing() bool { return p == Success || p == Error }

// classicFaces is the original kaomoji row, one set of frames per status.
// Kept verbatim (including the ≤8-cell widths, so the compact one-line
// classic style never widens the sidebar): /pet offers it as "classic" next
// to the ASCII animals, and the classic sidebar style animates these on the
// same 500ms tick the ASCII blinks on.
var classicFaces = map[Status][]string{
	Idle:     {"(◉‿◉)", "(˘‿˘)", "(◉‿◉)", "(-‿-)"},
	Thinking: {"(◔_◔)", "(◉_◔)", "(◔_◔)", "(¬_¬)", "(ᵕ_ᵕ)"},
	Writing:  {"(•‿•)✎", "(•o•)⋆", "(•‿•)✎", "(•o•)⋆", "(•ᴗ•)✎", "(•ᴗ•)⋆", "(ᵔᴗᵔ)✎"},
	Working:  {"(◉▿◉)⚙", "(◉▽◉)⋆", "(◉▿◉)⚙", "(●▿●)⋆", "(•ᴗ•)⚙", "(•_•)⋆", "(ᗒᴗᗕ)⚙", "(•̀ᴗ•́)⋆"},
	Success:  {"(ᵔᴥᵔ)", "(ᵔᴥᵔ)", "(ᵔᴥᵔ)", "(^‿^)", "(ᵔᴗᵔ)♡", "(•ᴗ•)✦", "(^ᴗ^)", "(ᵔ‿ᵔ)"},
	Error:    {"(ಠ_ಠ)", "()ಠ_ಠ)", "(T_T)", "(T_T)"},
}

// ClassicFaces returns the classic frames for a status ("" reads as idle).
func ClassicFaces(s Status) []string {
	if s == "" {
		s = Idle // zero value reads as idle
	}
	return classicFaces[s]
}

// ClassicFace picks the classic animation frame for tick.
func ClassicFace(s Status, tick int) string {
	frames := ClassicFaces(s)
	if len(frames) == 0 {
		return ""
	}
	return frames[tick%len(frames)]
}

// IsStyle reports whether a /pet entry selects a look rather than an animal.
func IsStyle(entry string) bool {
	switch strings.ToLower(strings.TrimSpace(entry)) {
	case StyleASCII, StyleClassic:
		return true
	}
	return false
}

// IsClassic reports whether an entry selects the classic kaomoji look.
func IsClassic(entry string) bool {
	return strings.ToLower(strings.TrimSpace(entry)) == StyleClassic
}

// Label renders the status line, appending elapsed time for busy states.
func Label(s Status, since time.Time) string {
	var out string
	switch s {
	case Thinking:
		out = "Thinking..."
	case Writing:
		out = "Writing..."
	case Working:
		out = "Working..."
	case Success:
		out = "Done!"
	case Error:
		out = "Error"
	default:
		out = "Ready"
	}
	if s.Busy() && !since.IsZero() {
		out += " " + format.FmtDur(time.Since(since))
	}
	return out
}
