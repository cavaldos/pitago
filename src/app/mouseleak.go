package app

import (
	"regexp"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var (
	// One complete SGR mouse report with the ESC byte lost:
	// "[<Cb;Cx;CyM" (press) or "[<Cb;Cx;Cy m" (release). Terminals only
	// emit these while mouse reporting is on; when a trackpad swipe floods
	// dozens of reports into one input read, bubbletea can split the ESC
	// off as an Alt modifier and the remainder arrives as plain KeyRunes —
	// which the textarea would otherwise insert as literal garbage.
	//
	// The "[" itself often arrives separately (a lone Alt+[ message, or
	// eaten as the tail of the prior read), so the bracket is optional:
	// "<Cb;Cx;CyM" with coordinates is still a report, never typed text.
	sgrReport = regexp.MustCompile(`\[?<(\d+);(\d+);(\d+)([Mm])`)
	// A bare-coordinate report with the "[<" head lost in a prior split
	// read ("65;99;18M"). Burst context only (see below) — never matched
	// against ordinary typing on its own.
	sgrBare = regexp.MustCompile(`(\d{1,3});(\d{1,4});(\d{1,4})[Mm]`)
	// A report cut off mid-burst ("[<", "[<65", "[<65;50", …). Matched
	// without burst context too, but only with the bracket: "<65" alone
	// could be real typing ("x<65").
	sgrTail = regexp.MustCompile(`\[<\d{0,3};?\d{0,4};?\d{0,4}$`)
	// Same, bracket optional ("<65", "<65;50", …). Burst context only.
	sgrTailLoose = regexp.MustCompile(`\[?<\d{0,3};?\d{0,4};?\d{0,4}$`)
	// Pure fragment soup (digits/semicolons/split heads/trailing M) for the
	// burst-armed path below.
	fragSoup = regexp.MustCompile(`^[\d;\[<>mM]+$`)
	// A split head ("<65", "[<65"): bracket optional, 2-3 digits (a lone
	// "<3" is a heart, not shrapnel — single digits always pass).
	fragHead = regexp.MustCompile(`^\[?<\d{2,3}$`)
	// Head of a (possibly partial) report: Cb plus optional Cx/Cy.
	sgrHead = regexp.MustCompile(`^\[<(\d+)(?:;(\d*))?(?:;(\d*))?`)
)

// cleanMouseLeak strips SGR mouse-report remnants from KeyRunes. It returns
// the decoded wheel events (so a swipe still scrolls) and the remaining
// runes. ok=false means no leak — runes are untouched.
func cleanMouseLeak(runes []rune) (events []tea.MouseMsg, cleaned []rune, ok bool) {
	s := string(runes)
	full := sgrReport.FindAllStringSubmatch(s, -1)
	if len(full) == 0 {
		// No complete report: swallow only a digit-bearing tail
		// ("[<65", "[<65;50", …). A bare "[<" alone might be real typing.
		loc := sgrTail.FindStringIndex(s)
		if loc == nil || !sgrHead.MatchString(s[loc[0]:]) {
			return nil, runes, false
		}
		if ev, good := leakEvent(s[loc[0]:]); good {
			events = append(events, ev)
		}
		return events, []rune(s[:loc[0]]), true
	}
	for _, mt := range full {
		if ev, good := leakEventParts(mt[1], mt[2], mt[3]); good {
			events = append(events, ev)
		}
	}
	s = sgrReport.ReplaceAllString(s, "")
	// Burst context (a full report was just stripped): a head-less residue
	// ("65;99;18M" — its "[<" died as a tail in the prior read) is noise
	// too, as is a trailing split fragment ("[<65", "<65").
	for _, mt := range sgrBare.FindAllStringSubmatch(s, -1) {
		if ev, good := leakEventParts(mt[1], mt[2], mt[3]); good {
			events = append(events, ev)
		}
	}
	s = sgrBare.ReplaceAllString(s, "")
	// A burst split across reads can leave a tail fragment behind; in burst
	// context even a bracket-less "<65" is residue, not typing.
	if loc := sgrTailLoose.FindStringIndex(s); loc != nil {
		if ev, good := leakEvent(s[loc[0]:]); good {
			events = append(events, ev)
		}
		s = s[:loc[0]]
	}
	return events, []rune(s), true
}

// cleanMouseFrag swallows a lone report fragment that arrives as its own
// message right after a burst ("65;99;18M", ";50;31M" — the head was
// swallowed as a tail in the prior read). Call only while burst-armed (see
// mouseBurst): the remainder must be pure fragment soup containing ";" or a
// split head, so ordinary typing never matches.
func cleanMouseFrag(s string) (events []tea.MouseMsg, ok bool) {
	for _, mt := range sgrReport.FindAllStringSubmatch(s, -1) {
		if ev, good := leakEventParts(mt[1], mt[2], mt[3]); good {
			events = append(events, ev)
		}
	}
	rest := sgrReport.ReplaceAllString(s, "")
	for _, mt := range sgrBare.FindAllStringSubmatch(rest, -1) {
		if ev, good := leakEventParts(mt[1], mt[2], mt[3]); good {
			events = append(events, ev)
		}
	}
	rest = sgrBare.ReplaceAllString(rest, "")
	if rest == "" {
		return events, true
	}
	// Pure fragment soup with ";" (split coordinates) qualifies, unless it
	// is too short to tell from typing ("50;" passes — it needs an M
	// terminator or 3+ digits to count as residue). A split head ("<65")
	// qualifies on its own shape.
	digits := 0
	for i := 0; i < len(rest); i++ {
		if rest[i] >= '0' && rest[i] <= '9' {
			digits++
		}
	}
	if fragSoup.MatchString(rest) &&
		((strings.Contains(rest, ";") && (strings.ContainsAny(rest, "Mm") || digits >= 3)) ||
			fragHead.MatchString(rest)) {
		return events, true
	}
	return nil, false
}

// leakEvent decodes one (possibly partial) "[<Cb[;Cx[;Cy]]" fragment.
// Clicks/drags carry no scroll direction, so only wheel reports yield an
// event — everything else is still swallowed as noise.
func leakEvent(frag string) (tea.MouseMsg, bool) {
	mt := sgrHead.FindStringSubmatch(frag)
	if mt == nil {
		return tea.MouseMsg{}, false
	}
	return leakEventParts(mt[1], mt[2], mt[3])
}

func leakEventParts(cbS, cxS, cyS string) (tea.MouseMsg, bool) {
	cb, err := strconv.Atoi(cbS)
	if err != nil {
		return tea.MouseMsg{}, false
	}
	// Mirror bubbletea's parseMouseButton: bit 64 = wheel, low 2 bits pick
	// up/down/left/right (00 up, 01 down, 10 left, 11 right).
	if cb&64 == 0 {
		return tea.MouseMsg{}, false
	}
	var btn tea.MouseButton
	switch cb & 3 {
	case 0:
		btn = tea.MouseButtonWheelUp
	case 1:
		btn = tea.MouseButtonWheelDown
	case 2:
		btn = tea.MouseButtonWheelLeft
	default:
		btn = tea.MouseButtonWheelRight
	}
	cx, _ := strconv.Atoi(cxS)
	cy, _ := strconv.Atoi(cyS)
	return tea.MouseMsg{X: cx - 1, Y: cy - 1, Action: tea.MouseActionPress, Button: btn}, true
}

// applyWheelLeak replays scrubbed wheel reports into the open dialog
// (same routing as the normal MouseMsg path). Trackpad bursts often split
// across reads and arrive here as KeyRunes — without this the trajectory
// detail feels dead while the chat behind still scrolls. Other dialogs
// swallow (like MouseMsg).
func (m Model) applyWheelLeak(events []tea.MouseMsg) tea.Model {
	if len(m.Dialogs) == 0 || len(events) == 0 {
		return m
	}
	d := m.Dialogs[0]
	cur := m
	for _, ev := range events {
		if ev.Button != tea.MouseButtonWheelUp && ev.Button != tea.MouseButtonWheelDown {
			continue
		}
		switch {
		case d.Kind == "trajectory":
			nm, _ := cur.updateTrajWheel(d, ev)
			if mm, ok := nm.(Model); ok {
				cur = mm
			}
		case d.Kind == "pconfig" && len(d.Provs) > 0:
			nm, _ := cur.updatePconfigWheel(d, ev.Button == tea.MouseButtonWheelDown)
			if mm, ok := nm.(Model); ok {
				cur = mm
			}
		}
	}
	return cur
}

// scrollLeak replays scrubbed wheel reports into the viewports, mirroring
// the normal MouseMsg routing (wheel over the sidebar scrolls it).
func (m *Model) scrollLeak(events []tea.MouseMsg) tea.Cmd {
	if !m.ready || len(events) == 0 {
		return nil
	}
	var cmds []tea.Cmd
	for _, ev := range events {
		var c tea.Cmd
		if ev.Action == tea.MouseActionPress &&
			(ev.Button == tea.MouseButtonWheelUp || ev.Button == tea.MouseButtonWheelDown) &&
			m.overSide(ev.X) {
			m.sideVp, c = m.sideVp.Update(ev)
		} else {
			m.vp, c = m.vp.Update(ev)
		}
		if c != nil {
			cmds = append(cmds, c)
		}
	}
	m.Refresh()
	return tea.Batch(cmds...)
}
