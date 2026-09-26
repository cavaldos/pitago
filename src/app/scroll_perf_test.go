package app

import (
	"fmt"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

// scrollModel is a ready model holding n chat lines of transcript, so a
// wheel event has real content to scroll through.
func scrollModel(t testing.TB, n int) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 120, 30
	m.baseVpH = 23
	m.hideSide = true
	m.vp = viewport.New(116, 23)
	m.sideVp = viewport.New(sideInnerW, 10)
	m.blocks = make([]Block, n)
	for i := range m.blocks {
		m.blocks[i] = Block{Kind: "assistant", Text: fmt.Sprintf("answer %d\ndetail line one\ndetail line two", i)}
	}
	m.Refresh() // first paint loads the transcript
	return m
}

// countPaints counts full repaints for the duration of fn.
func countPaints(t *testing.T, fn func()) int {
	t.Helper()
	n := 0
	paintHook = func() { n++ }
	t.Cleanup(func() { paintHook = nil })
	fn()
	return n
}

// A wheel report only moves an offset, so it must not re-render the
// transcript: that cost is O(transcript) per event, and paying it per event
// is what made a long chat scroll in jerks and left the view catching up
// after the wheel had already stopped.
func TestWheelScrollDoesNotRepaintTranscript(t *testing.T) {
	m := scrollModel(t, 400)
	paints := countPaints(t, func() {
		for i := 0; i < 20; i++ {
			tm, _ := m.Update(wheelAt(10, 10, false)) // wheel up
			m = tm.(Model)
		}
	})
	if paints != 0 {
		t.Fatalf("20 wheel events repainted the transcript %d time(s), want 0", paints)
	}
	if m.vp.YOffset == 0 {
		t.Fatal("wheel up over the chat did not scroll it")
	}
}

// The same holds for wheel reports that arrive as scrubbed KeyRunes after a
// split read (the trackpad path): the burst scrolls, but paints nothing.
func TestScrollLeakDoesNotRepaintTranscript(t *testing.T) {
	m := scrollModel(t, 400)
	// "[<64;10;5M" — SGR wheel-up press at column 10.
	burst := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("[<64;10;5M")}
	paints := countPaints(t, func() {
		for i := 0; i < 20; i++ {
			tm, _ := m.Update(burst)
			m = tm.(Model)
		}
	})
	if paints != 0 {
		t.Fatalf("20 leaked wheel events repainted the transcript %d time(s), want 0", paints)
	}
	if m.vp.YOffset == 0 {
		t.Fatal("leaked wheel up did not scroll the chat")
	}
}

// Scrolling the sidebar moves the sidebar and nothing else — neither the
// chat offset nor the rendered content of either viewport.
func TestWheelOverSidebarDoesNotRepaintEither(t *testing.T) {
	m := scrollModel(t, 400)
	m.hideSide = false // show the sidebar so overSide can route
	m.Refresh()
	chatOff := m.vp.YOffset
	paints := countPaints(t, func() {
		tm, _ := m.Update(wheelAt(m.mainW()+5, 10, true))
		m = tm.(Model)
	})
	if paints != 0 {
		t.Fatalf("sidebar wheel repainted %d time(s), want 0", paints)
	}
	if m.vp.YOffset != chatOff {
		t.Fatalf("sidebar wheel moved the chat offset: %d -> %d", chatOff, m.vp.YOffset)
	}
}

// Dropping the repaint from the wheel path must not break follow-the-tail:
// a reader scrolled up in history keeps their position when new content
// arrives, and a reader at the bottom still sticks to it.
func TestScrollKeepsReadingPosition(t *testing.T) {
	m := scrollModel(t, 400)
	for i := 0; i < 5; i++ {
		tm, _ := m.Update(wheelAt(10, 10, false)) // scroll up, away from the tail
		m = tm.(Model)
	}
	off := m.vp.YOffset
	if off == 0 {
		t.Fatal("setup: wheel up did not scroll the chat")
	}
	m.blocks = append(m.blocks, Block{Kind: "assistant", Text: "new tail\nsecond line"})
	m.Refresh() // a content change, the way an incoming message would
	if m.vp.YOffset != off {
		t.Fatalf("reading position moved on new content: %d -> %d", off, m.vp.YOffset)
	}

	// At the bottom, new content still sticks.
	m.vp.GotoBottom()
	if !m.vp.AtBottom() {
		t.Fatal("setup: GotoBottom did not reach the bottom")
	}
	m.blocks = append(m.blocks, Block{Kind: "assistant", Text: "even newer"})
	m.Refresh()
	if !m.vp.AtBottom() {
		t.Fatal("new content did not keep the bottom-following reader at the bottom")
	}
}

// setChatContent must keep what it loaded in sync, so an unchanged
// transcript skips the re-measure while a real change still lands.
func TestSetChatContentTracksLoadedTranscript(t *testing.T) {
	m := scrollModel(t, 50)
	first := m.vp.TotalLineCount()
	if first == 0 {
		t.Fatal("first paint loaded no content")
	}
	m.Refresh() // unchanged transcript
	if m.vp.TotalLineCount() != first {
		t.Fatalf("re-measured an unchanged transcript: %d -> %d lines", first, m.vp.TotalLineCount())
	}
	m.blocks = append(m.blocks, Block{Kind: "assistant", Text: "fresh\nlines\nhere"})
	m.Refresh()
	if got := m.vp.TotalLineCount(); got <= first {
		t.Fatalf("content change not applied: %d lines, want > %d", got, first)
	}
	if m.chatContent == "" {
		t.Fatal("loaded transcript went untracked")
	}
}

// Per-wheel-event cost on a long chat. The "re_render_per_event" arm is the
// old behaviour (repaint on every wheel) kept here to document the delta:
// it is the O(transcript) work that used to queue up behind the input
// reader. The "offset_only" arm is the shipped path.
// A macOS flick to an edge: the offset moves for a few reports, then a long
// momentum tail keeps arriving after the viewport is already pinned. The
// "unfiltered" arm is what shipped before ScrollEventFilter, kept to document
// the delta: every report in the tail cost a full View() for a frame the
// renderer then discarded.
func BenchmarkFlickToEdge(b *testing.B) {
	for _, filtered := range []bool{false, true} {
		name := "unfiltered"
		if filtered {
			name = "filtered"
		}
		b.Run(name, func(b *testing.B) {
			m := scrollModel(b, 400)
			m.vp.GotoTop()
			// Close enough to the top that a 50-report burst pins after ~10
			// of them, leaving a ~40-report momentum tail to measure.
			m.vp.SetYOffset(30)
			const burst = 50
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.vp.SetYOffset(30)
				for j := 0; j < burst; j++ {
					ev := wheelAt(10, 10, false) // wheel up, toward the top
					if filtered && ScrollEventFilter(m, ev) == nil {
						continue
					}
					tm, _ := m.Update(ev)
					m = tm.(Model)
					_ = m.View() // Bubble Tea renders once per message
				}
			}
		})
	}
}

func BenchmarkWheelScrollLongChat(b *testing.B) {
	for _, blocks := range []int{200, 800} {
		for _, mode := range []string{"offset_only", "re_render_per_event"} {
			b.Run(fmt.Sprintf("blocks=%d/%s", blocks, mode), func(b *testing.B) {
				m := scrollModel(b, blocks)
				down := tea.MouseMsg{X: 10, Y: 10, Action: tea.MouseActionPress, Button: tea.MouseButtonWheelUp}
				up := down
				up.Button = tea.MouseButtonWheelDown
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					ev := down
					if i%2 == 1 {
						ev = up
					}
					if mode == "re_render_per_event" {
						var c tea.Cmd
						m.vp, c = m.vp.Update(ev)
						_ = c
						m.Refresh()
						continue
					}
					tm, _ := m.Update(ev)
					m = tm.(Model)
				}
			})
		}
	}
}
