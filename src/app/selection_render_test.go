package app

import (
	"strings"
	"testing"
	"unicode/utf8"
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

const demoBody = "## Result\n\n" +
	"| Feature | Status | Notes |\n|---|---|---|\n" +
	"| Tables | ✓ working | Header + separator + rows render aligned |\n" +
	"| Code | ✓ working | Fenced blocks with language tags get syntax highlight |\n\n" +
	"Here's a paragraph of ordinary text to check wrapping and line breaks. It should flow at the " +
	"terminal width, respect hard wraps you insert, and keep the contrast legible against the theme.\n\n" +
	"Some extra bits to stress it:\n\n" +
	"- Lists (ordered and unordered) with nesting\n" +
	"- Links like [pi docs](https://pi.dev) if rendering is on\n" +
	"- Emoji / unicode: ✓ × · • — and a CJK line: 日本語のテキスト\n\n" +
	"1. First item\n2. Second item\n   - nested bullet\n\n" +
	"> Blockquoted text to verify left-rule and dimming.\n\n" +
	"```ts\nexport function greet(name: string): string {\n  if (!name) throw new Error(\"name required\");\n  return `Hello, ${name}!`;\n}\n```\n\n" +
	"Let me know which parts look off — alignment, colors, spacing, or wrapping.\n"

func outsideInverse(s string) []string {
	var out []string
	inv := false
	for i := 0; i < len(s); {
		if n, seq := escapeAt(s, i); n > 0 {
			switch {
			case seq == "\x1b[7m":
				inv = true
			case seq == "\x1b[27m":
				inv = false
			}
			i += n
			continue
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if r != utf8.RuneError && r >= 0x20 && r != 0x7f && r != '\t' {
			if !inv {
				out = append(out, string(r))
			}
		}
		i += size
	}
	return out
}

func TestViewSelectionHasNoGaps(t *testing.T) {
	m := New(nil, t.TempDir())
	m.ready = true
	m.winW, m.winH = 100, 30
	m.vp.Width, m.vp.Height = 100, 30
	m.blocks = []Block{{Kind: "user", Text: "Doing some testing. Give me a table, a code snippet, some text please."}, {Kind: "assistant", Text: demoBody}}
	m.renderBlocks()
	m.vp.SetContent(strings.Join(m.chatLines, "\n"))
	m.vp.GotoBottom()

	painted := m.chatViewport()
	m.sel = Selection{Active: true, HadDrag: true,
		Anchor: Point{Line: painted.YOffset, Col: 0},
		Focus:  Point{Line: painted.YOffset + painted.Height - 1, Col: 10_000}}

	// The exact expression View() uses to paint the chat: no header, no
	// input box, no sidebar — only the rendered transcript.
	out := overlaySelection(painted.View(), painted.YOffset, m.sel, m.gutterCols)
	bad := 0
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(stripSelectionANSI(line)) == "" {
			continue
		}
		o := outsideInverse(line)
		if len(o) > 2 { // the 2-cell gutter is chrome
			bad++
			if bad <= 3 {
				t.Errorf("row %d: %d un-highlighted %q in %q", i, len(o), strings.Join(o, ""), stripSelectionANSI(line))
			}
		}
	}
	if bad > 0 {
		t.Fatalf("%d rendered rows have un-highlighted text", bad)
	}
}
