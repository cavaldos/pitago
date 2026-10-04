package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chipShown is "is the jump chip warranted right now", resolved the same way
// view() resolves it.
func chipShown(t testing.TB, m Model) bool {
	t.Helper()
	_, ok := m.jumpLatestChip(m.chatViewport())
	return ok
}

func chipIn(t testing.TB, m Model) jumpLatestChip {
	t.Helper()
	chip, ok := m.jumpLatestChip(m.chatViewport())
	if !ok {
		t.Fatal("expected a jump chip for a scrolled-up transcript, got none")
	}
	return chip
}

func TestJumpLatestChipOnlyWhenScrolledUp(t *testing.T) {
	m := scrollModel(t, 400)
	m.Refresh()
	if !m.vp.AtBottom() {
		t.Fatal("precondition: a freshly refreshed transcript sits at the bottom")
	}
	if chipShown(t, m) {
		t.Fatal("chip must stay hidden at the bottom — nothing to jump back to")
	}

	m.vp.LineUp(10)
	if m.vp.AtBottom() {
		t.Fatal("precondition: LineUp should have left the bottom")
	}
	if !chipShown(t, m) {
		t.Fatal("chip should appear once the newest line is off-frame")
	}
	if !strings.Contains(stripANSI(m.view()), "Jump to latest message") {
		t.Fatal("chip text missing from the rendered frame")
	}

	m.vp.GotoBottom()
	if chipShown(t, m) {
		t.Fatal("chip must disappear again after jumping back")
	}
}

// A transcript that fits entirely on screen has nothing below the fold, so
// scrolling never arms the chip even though content exists.
func TestJumpLatestChipStaysHiddenOnShortTranscript(t *testing.T) {
	m := scrollModel(t, 2)
	m.Refresh()
	if chipShown(t, m) {
		t.Fatal("a transcript shorter than the frame must not arm the chip")
	}
}

// The chip is floated onto an existing transcript row. If it appended a row,
// chatFrameRows' "the parts sum to exactly winH" invariant breaks and the
// editor stops sharing a bottom edge with the sidebar.
func TestJumpLatestChipDoesNotShiftTheFrame(t *testing.T) {
	m := scrollModel(t, 400)
	m.Refresh()
	atBottom := lipgloss.Height(m.view())

	m.vp.LineUp(10)
	scrolled := lipgloss.Height(m.view())
	if atBottom != scrolled {
		t.Fatalf("chip changed the frame height: %d rows at the bottom, %d scrolled up", atBottom, scrolled)
	}
	if want := m.winH; scrolled != want {
		t.Fatalf("frame height = %d, want %d (the terminal height)", scrolled, want)
	}
}

// The advertised key has to work, or the chip is lying to the user. End is
// the pi-parity binding and reaches the viewport whenever the input is a
// single line (update.go scroll-key switch).
func TestJumpLatestAdvertisedKeyJumps(t *testing.T) {
	m := scrollModel(t, 400)
	m.Refresh()
	m.vp.LineUp(20)
	if !strings.Contains(stripANSI(m.view()), "· End") {
		t.Fatal("single-line input should advertise the End key")
	}

	tm, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnd})
	got := tm.(Model)
	if !got.vp.AtBottom() {
		t.Fatalf("End did not jump to the latest message: YOffset=%d", got.vp.YOffset)
	}
	if chipShown(t, got) {
		t.Fatal("chip should be gone after the advertised key jumped")
	}
}

// A multi-line input keeps End for its own caret, so the chip must name the
// click instead of promising a shortcut that would silently do nothing.
func TestJumpLatestChipNamesClickForMultilineInput(t *testing.T) {
	m := scrollModel(t, 400)
	m.Refresh()
	m.vp.LineUp(10)
	m.ta.SetValue("first line\nsecond line")

	if strings.Contains(stripANSI(m.view()), "· End") {
		t.Fatal("multi-line input must not advertise End")
	}
	if !strings.Contains(stripANSI(m.view()), "· click") {
		t.Fatal("multi-line input should advertise the click")
	}
}

func TestJumpLatestClickJumpsToLatest(t *testing.T) {
	m := scrollModel(t, 400)
	m.Mouse = true // scrollModel leaves the mouse off; clicks need it on
	m.Refresh()
	m.vp.LineUp(15)
	chip := chipIn(t, m)
	// The header owns screen row 0, so frame row r lands on screen row r+1.
	x := chip.start + lipgloss.Width(chip.text)/2
	press := tea.MouseMsg{X: x, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}

	tm, cmd := m.Update(press)
	got := tm.(Model)
	if !got.vp.AtBottom() {
		t.Fatalf("clicking the chip did not jump: YOffset=%d", got.vp.YOffset)
	}
	if cmd != nil {
		t.Fatal("a chip press should not schedule commands")
	}
	if chipShown(t, got) {
		t.Fatal("chip should be gone after the click")
	}
}

// The chip floats over a transcript row, so its press must be tested before
// the drag selector claims the row — otherwise it starts a selection.
func TestJumpLatestClickBeatsDragSelection(t *testing.T) {
	m := scrollModel(t, 400)
	m.Mouse = true
	m.Refresh()
	m.vp.LineUp(15)
	chip := chipIn(t, m)
	x := chip.start + 1
	press := tea.MouseMsg{X: x, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}

	tm, _ := m.Update(press)
	got := tm.(Model)
	if !got.vp.AtBottom() {
		t.Fatal("chip press did not jump")
	}
	if got.sel.Active {
		t.Fatal("chip press started a drag selection instead of hitting the button")
	}
}

func TestJumpLatestClickMissesDoNothing(t *testing.T) {
	m := scrollModel(t, 400)
	m.Mouse = true
	m.Refresh()
	m.vp.LineUp(15)
	chip := chipIn(t, m)

	// Each event must fall through to the normal handlers without being
	// swallowed as a chip press. The invariant is "no jump": a miss may still
	// scroll (a wheel through the chip is exactly that) or open a copy menu.
	cases := []struct {
		name string
		msg  tea.MouseMsg
	}{
		{"left of the chip", tea.MouseMsg{X: 0, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}},
		{"right of the chip", tea.MouseMsg{X: m.mainW() - 1, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}},
		{"another transcript row", tea.MouseMsg{X: chip.start + 1, Y: 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}},
		{"wheel through the chip", tea.MouseMsg{X: chip.start + 1, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelDown}},
		{"right-click on the chip", tea.MouseMsg{X: chip.start + 1, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonRight}},
		{"motion over the chip", tea.MouseMsg{X: chip.start + 1, Y: chip.row + 1, Action: tea.MouseActionMotion}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tm, _ := m.Update(tc.msg)
			if tm.(Model).vp.AtBottom() {
				t.Fatalf("a non-chip event jumped to the latest message: %+v", tc.msg)
			}
		})
	}
}

// With mouse off there is no click target, so the chip must not pretend to be
// one — and the click guard has to stay inert too.
func TestJumpLatestChipInertWithoutMouse(t *testing.T) {
	m := scrollModel(t, 400)
	m.Mouse = false
	m.Refresh()
	m.vp.LineUp(10)
	chip := chipIn(t, m)
	press := tea.MouseMsg{X: chip.start + 1, Y: chip.row + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}

	tm, _ := m.Update(press)
	if tm.(Model).vp.AtBottom() {
		t.Fatal("a click must not jump while the mouse is off")
	}
	if got := tm.(Model).sel.Active; got {
		t.Fatal("the click was consumed by the selector while the mouse is off")
	}
}

// Selection points are absolute chat-content coordinates. After a jump the
// highlight would land on an unrelated block, so it has to be dropped.
func TestJumpToLatestDropsStaleSelection(t *testing.T) {
	m := scrollModel(t, 400)
	m.Refresh()
	m.vp.LineUp(15)
	m.sel = Selection{Active: true, Anchor: Point{Line: 0, Col: 0}, Focus: Point{Line: 2, Col: 4}, HadDrag: true}

	m.JumpToLatest()
	if m.sel.Active || m.sel.HadDrag {
		t.Fatalf("stale selection survived the jump: %+v", m.sel)
	}
	if !m.vp.AtBottom() {
		t.Fatal("JumpToLatest left the transcript off the bottom")
	}
}

// Team/external follow mode renders its own surface; the chat chip must not
// leak into it or fight handleFollowKey for End.
func TestJumpLatestChipRespectsFrameHeight(t *testing.T) {
	m := scrollModel(t, 400)
	m.vp.LineUp(10)
	// Squeeze the frame below the chip's minimum and the affordance drops
	// rather than drawing on a row that cannot hold it.
	m.vp.Height = jumpLatestMinRows - 1
	if chipShown(t, m) {
		t.Fatal("chip must not draw on a chat frame too short to float it")
	}
}
