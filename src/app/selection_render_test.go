package app

import (
	"strings"
	"testing"
)

// The selection highlight is rendered by overlaySelection from View(). A refactor
// that replaces the viewport render can silently orphan the overlay — nothing
// fails to compile, the highlight just stops appearing. These tests assert the
// highlight is actually present in the drawn chat, which is the only thing a user
// notices.
func TestOverlaySelectionHighlightsVisibleLines(t *testing.T) {
	view := "alpha line\nbravo line\ncharlie line"
	sel := Selection{
		Active: true,
		Anchor: Point{Line: 1, Col: 0},
		Focus:  Point{Line: 1, Col: 5},
	}
	got := overlaySelection(view, 1, sel, nil)
	if !strings.Contains(got, "\x1b[7m") || !strings.Contains(got, "\x1b[27m") {
		t.Fatalf("selection should be reverse-video highlighted:\n%q", got)
	}
	if !strings.Contains(got, "bravo") {
		t.Fatalf("highlighted line lost its text:\n%q", got)
	}
	// The offset maps a content line to a viewport row: with YOffset 1, content
	// line 1 is viewport row 0, and row 1 is content line 2 and must be untouched.
	rows := strings.Split(got, "\n")
	if !strings.Contains(rows[0], "\x1b[7m") {
		t.Fatalf("content line 1 should map to viewport row 0:\n%q", got)
	}
	if strings.Contains(rows[1], "\x1b[7m") {
		t.Fatalf("viewport row 1 is content line 2, outside the selection:\n%q", got)
	}
}

func TestOverlaySelectionIsNoOpWhenInactive(t *testing.T) {
	view := "alpha\nbravo"
	if got := overlaySelection(view, 0, Selection{}, nil); got != view {
		t.Fatalf("an inactive selection must not alter the view:\ngot  %q\nwant %q", got, view)
	}
}

func TestOverlaySelectionSpansMultipleLines(t *testing.T) {
	view := "one\ntwo\nthree"
	sel := Selection{
		Active: true,
		Anchor: Point{Line: 0, Col: 1},
		Focus:  Point{Line: 2, Col: 3},
	}
	lines := strings.Split(overlaySelection(view, 0, sel, nil), "\n")
	for i, l := range lines {
		if !strings.Contains(l, "\x1b[7m") {
			t.Fatalf("line %d should be highlighted: %q", i, l)
		}
	}
}

// The regression that prompted this: View() renders a local viewport copy with a
// reserved height, and the overlay has to run on that copy. Assert the drawn chat
// carries the highlight for a live selection, not merely that the helper works.
func TestViewRendersSelectionHighlight(t *testing.T) {
	m := New(nil, t.TempDir())
	// View() returns "starting…" until ready, and a test has no terminal, so the
	// viewport has no size until it is set. Either one hides the chat entirely.
	m.ready = true
	m.vp.Width, m.vp.Height = 60, 20
	m.vp.SetContent("first line of chat\nsecond line of chat\nthird line of chat")
	m.vp.GotoTop()
	m.sel = Selection{Active: true, Anchor: Point{Line: 0, Col: 0}, Focus: Point{Line: 0, Col: 4}}

	drawn := m.View()
	if !strings.Contains(drawn, "\x1b[7m") {
		t.Fatalf("the drawn chat lost the selection highlight; View() is not calling overlaySelection")
	}
}

func TestViewWithoutSelectionHasNoHighlight(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.vp.Width, m.vp.Height = 60, 20
	m.vp.SetContent("first line of chat\nsecond line of chat")
	m.vp.GotoTop()
	if strings.Contains(m.View(), "\x1b[7m") {
		t.Fatal("no selection should mean no reverse-video in the chat")
	}
}
