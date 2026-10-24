package app

import "sync/atomic"

// blinkPhase is the on/off phase of the "still running" dot. The pet tick
// (500ms while a turn is busy) flips it, so a running tool block's bullet
// pulses at about 1Hz; blockKey includes it so the block re-renders.
var blinkPhase atomic.Bool

func setBlink(on bool) { blinkPhase.Store(on) }

// blinkOn reports whether the running dot is lit; true by default so a
// block rendered before the first tick shows its dot.
func blinkOn() bool { return !blinkPhase.Load() }

// runningDot is the compact tool header's dot while the call runs: the
// normal glyph, alternating with a dim dot.
func runningDot() string {
	if blinkOn() {
		return toolStyle.Render("●")
	}
	return statusBarStyle.Render("·")
}

// runningBullet is the framed tool header's bullet while the call runs.
func runningBullet() string {
	if blinkOn() {
		return statusBarStyle.Render("○")
	}
	return statusBarStyle.Render("·")
}
