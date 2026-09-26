package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// The screenshot: a trackpad swipe floods "[<Cb;Cx;CyM" reports that arrive
// as KeyRunes (ESC lost in a split read). They must never land in the input.
func TestMouseLeakSwallowed(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	// Tall chat so wheel events have somewhere to scroll.
	for i := 0; i < 30; i++ {
		m.AddBlock(Block{Kind: "user", Text: "line " + strings.Repeat("x", 40)})
	}
	m.RefreshFollow()
	top := m.vp.YOffset // at bottom after follow
	if top <= 0 {
		t.Fatal("need tall content for the scroll check")
	}

	// Wheel-up reports (Cb 64) must scroll the chat back up.
	up := strings.Repeat("[<64;50;31M", 10)
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(up)})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("mouse flood leaked into input: %q", got)
	}
	if m.vp.YOffset >= top {
		t.Fatal("wheel reports in the flood should still scroll the chat")
	}

	// The exact screenshot mix (up/down/horizontal) stays out of the input.
	flood := "[<65;50;31M[<67;50;31M[<65;50;31M[<65;50;31M[<65;50;31M" +
		"[<67;50;31M[<65;50;31M[<64;50;31M[<64;50;31M[<64;50;31M"
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(flood)})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("mouse flood leaked into input: %q", got)
	}
}

// Noise glued to real typing: strip the reports, keep the text.
func TestMouseLeakMixed(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	tm, _ = m.Update(tea.KeyMsg{
		Type: tea.KeyRunes, Runes: []rune("hi[<65;50;31M[<64;50;31M"),
	})
	m = tm.(Model)
	if got := m.ta.Value(); got != "hi" {
		t.Fatalf("mixed input = %q, want %q", got, "hi")
	}
}

// Ordinary typing (and a bare "[<" with no digits) must pass through.
func TestMouseLeakNormalTyping(t *testing.T) {
	for _, in := range []string{"hello world", "a[b", "[<"} {
		m := New(nil, t.TempDir())
		tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
		m = tm.(Model)
		tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(in)})
		m = tm.(Model)
		if got := m.ta.Value(); got != in {
			t.Fatalf("input %q became %q", in, got)
		}
	}
}

// Split-read shrapnel: the "[" arrives as a lone Alt+[ and the remainder
// "<Cb;Cx;CyM" without its bracket. Both must be swallowed, never typed.
func TestMouseLeakSplitBracket(t *testing.T) {
	m := New(nil, t.TempDir())
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)

	// Lone Alt+[ (split ESC[) is dropped, not inserted.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("["), Alt: true})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("orphan Alt+[ leaked into input: %q", got)
	}

	// Bracket-less remainder is a full report: stripped + scrolls.
	for i := 0; i < 30; i++ {
		m.AddBlock(Block{Kind: "user", Text: "line " + strings.Repeat("x", 40)})
	}
	m.RefreshFollow()
	top := m.vp.YOffset
	if top <= 0 {
		t.Fatal("need tall content for the scroll check")
	}
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<64;50;31M")})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("bracket-less report leaked into input: %q", got)
	}
	if m.vp.YOffset >= top {
		t.Fatal("bracket-less wheel report should still scroll the chat")
	}

	// The exact screenshot shape (far-right column, over the sidebar):
	// still swallowed, never typed.
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("<66;99;18M")})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("screenshot-shaped report leaked into input: %q", got)
	}

	// Head-less residue glued to a full report ("65;99;18M[<64;99;18M").
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("65;99;18M[<64;99;18M")})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("bare-coordinate residue leaked into input: %q", got)
	}

	// A lone continuation right after a burst (burst-armed path).
	tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(";50;31M")})
	m = tm.(Model)
	if got := m.ta.Value(); got != "" {
		t.Fatalf("burst continuation leaked into input: %q", got)
	}
}

// cleanMouseFrag only fires on fragment soup, never on real typing.
func TestCleanMouseFrag(t *testing.T) {
	for _, in := range []string{"65;99;18M", ";50;31M", "<65;99;18M", "65;99", ";31M", "<65"} {
		if _, ok := cleanMouseFrag(in); !ok {
			t.Fatalf("frag %q not swallowed", in)
		}
	}
	for _, in := range []string{"hello", "hi there", "x<65", "[<", "[", "<3", "<6", "50;", ";31", "call 65"} {
		if _, ok := cleanMouseFrag(in); ok {
			t.Fatalf("typing %q wrongly swallowed", in)
		}
	}
}

// Bracket-less typing that must keep working: hearts, comparisons, brackets.
func TestMouseLeakBracketlessTyping(t *testing.T) {
	for _, in := range []string{"<3", "x<65", "[", "[<", "a[b", "50;"} {
		m := New(nil, t.TempDir())
		tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
		m = tm.(Model)
		tm, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(in)})
		m = tm.(Model)
		if got := m.ta.Value(); got != in {
			t.Fatalf("input %q became %q", in, got)
		}
	}
}

// Cb bit 64 = wheel, low bits = up/down/left/right (bubbletea parity).
func TestCleanMouseLeakDecode(t *testing.T) {
	evs, _, ok := cleanMouseLeak([]rune("[<64;50;31M[<65;50;31M[<66;50;31M[<67;50;31M"))
	if !ok || len(evs) != 4 {
		t.Fatalf("want 4 wheel events, got %v, %v", len(evs), ok)
	}
	want := []tea.MouseButton{
		tea.MouseButtonWheelUp, tea.MouseButtonWheelDown,
		tea.MouseButtonWheelLeft, tea.MouseButtonWheelRight,
	}
	for i, w := range want {
		if evs[i].Button != w {
			t.Fatalf("event %d = %v, want %v", i, evs[i].Button, w)
		}
		if evs[i].X != 49 || evs[i].Y != 30 {
			t.Fatalf("event %d coords = %d,%d, want 49,30", i, evs[i].X, evs[i].Y)
		}
	}
	// Clicks are noise too (swallowed, no scroll event).
	if _, cleaned, ok := cleanMouseLeak([]rune("[<0;50;31M")); !ok || len(cleaned) != 0 {
		t.Fatalf("click report not swallowed: %q %v", cleaned, ok)
	}
	// Trailing partial from a split burst.
	evs, cleaned, ok := cleanMouseLeak([]rune("[<65;50;31M[<65"))
	if !ok || len(cleaned) != 0 || len(evs) != 2 || evs[1].Button != tea.MouseButtonWheelDown {
		t.Fatalf("partial tail not handled: evs=%v cleaned=%q ok=%v", evs, cleaned, ok)
	}
}
