package app

import (
	"strings"
	"time"
)

// Input history (shell-like ↑↓ recall when the input is empty).
//
// Plain ↑↓ recalls only while mouse reporting is on: with mouse off the
// terminal turns wheel scrolls into plain ↑↓ (indistinguishable from keys),
// so plain ↑↓ must scroll instead of rewriting the input. Shift+↑↓ recalls
// in both modes (terminals never emit it for wheel); wheel (MouseMsg)
// always scrolls and never touches history.
//
// - hist holds sent messages, oldest→newest.
// - histIdx is -1 for live input, else the browsing index into hist.
// - Zero-value Model has histIdx==0 with empty hist: histBrowsing stays
//   false, so tests constructing Model{} directly are safe.

const histMax = 200

func (m *Model) histBrowsing() bool {
	return m.histIdx >= 0 && m.histIdx < len(m.hist)
}

// pushHist records a sent message (dedupe consecutive repeats).
// Slash commands are skipped: recalling one reopens the / popup and
// stutters navigation, and commands are re-discoverable via Tab anyway.
func (m *Model) pushHist(text string) {
	text = strings.TrimSpace(text)
	if text == "" || strings.HasPrefix(text, "/") {
		return
	}
	if n := len(m.hist); n > 0 && m.hist[n-1] == text {
		return
	}
	m.hist = append(m.hist, text)
	if len(m.hist) > histMax {
		m.hist = m.hist[len(m.hist)-histMax:]
	}
}

// setHist shows hist[histIdx] in the input (cursor jumps to end via SetValue).
func (m *Model) setHist() {
	m.ta.SetValue(m.hist[m.histIdx])
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
}

// clearHistInput empties the input back to live (Down past newest).
func (m *Model) clearHistInput() {
	m.histIdx = -1
	m.ta.Reset()
	m.refreshCmds()
	m.refreshAt()
	m.Refresh()
}

// clearInput wipes everything a send would have carried: the text, a
// history-recall browse, and the image tray (chips are attachments of the
// same draft, so leaving them behind would make "cleared" a lie). It also
// drops a live quit arm — a clear is a state change, and a stale arm from
// an earlier ^C would let the *next* ^C quit straight past a fresh draft.
//
// Reports whether there was anything to clear, so Ctrl+C can fall through
// to the quit arm on an empty editor (pi's app.clear: clear first, exit
// second).
func (m *Model) clearInput() bool {
	if m.ta.Value() == "" && len(m.imgAtts) == 0 && !m.histBrowsing() {
		return false
	}
	m.quitArm = time.Time{}
	m.quitGen++
	m.histIdx = -1
	m.ta.Reset()
	m.imgAtts = nil
	m.trayFocus = false
	m.refreshCmds()
	m.refreshAt()
	m.applyPopupH()
	m.Refresh()
	return true
}

// tryHistPrev handles ↑: older message. Returns true when consumed
// (including staying at the oldest, so chat doesn't scroll instead).
func (m *Model) tryHistPrev() bool {
	if len(m.hist) == 0 {
		return false
	}
	if m.histBrowsing() {
		if m.histIdx > 0 {
			m.histIdx--
			m.setHist()
		}
		return true
	}
	if strings.TrimSpace(m.ta.Value()) != "" || strings.Contains(m.ta.Value(), "\n") {
		return false
	}
	m.histIdx = len(m.hist) - 1
	m.setHist()
	return true
}

// tryHistNext handles ↓: newer message, past newest returns to empty.
func (m *Model) tryHistNext() bool {
	if len(m.hist) == 0 {
		return false
	}
	if m.histBrowsing() {
		if m.histIdx < len(m.hist)-1 {
			m.histIdx++
			m.setHist()
		} else {
			m.clearHistInput()
		}
		return true
	}
	if strings.TrimSpace(m.ta.Value()) != "" || strings.Contains(m.ta.Value(), "\n") {
		return false
	}
	m.histIdx = len(m.hist) - 1
	m.setHist()
	return true
}
