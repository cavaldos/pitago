package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

const dupEnd = `{"message":{"role":"assistant","content":[{"type":"text","text":"Hey there!"},{"type":"thinking","thinking":"user says hi"}]}}`

func countKinds(m *Model) (asst, think int) {
	for _, b := range m.blocks {
		switch b.Kind {
		case "assistant":
			asst++
		case "thinking":
			think++
		}
	}
	return asst, think
}

// Streaming deltas + message_end must not render the answer twice.
func TestNoDupOnMessageEnd(t *testing.T) {
	m := Model{curAsst: -1, curThink: -1}
	m.tools = make(map[string]int)
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_start"}}`))
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"text_delta","delta":"Hey there!"}}`))
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"thinking_start"}}`))
	m.applyDelta([]byte(`{"assistantMessageEvent":{"type":"thinking_delta","delta":"user says hi"}}`))
	m.applyMessageEnd([]byte(dupEnd))
	if asst, think := countKinds(&m); asst != 1 || think != 1 {
		t.Fatalf("streamed: want 1 asst + 1 think, got %d + %d", asst, think)
	}

	// Fallback: no deltas at all → message_end still renders once.
	m2 := Model{curAsst: -1, curThink: -1}
	m2.tools = make(map[string]int)
	m2.applyMessageEnd([]byte(dupEnd))
	if asst, think := countKinds(&m2); asst != 1 || think != 1 {
		t.Fatalf("fallback: want 1 asst + 1 think, got %d + %d", asst, think)
	}
}

// User echoes with vision blocks show a 📷 suffix (TextOf drops images).
func TestUserEchoWithImages(t *testing.T) {
	m := Model{curAsst: -1, curThink: -1}
	m.tools = make(map[string]int)
	m.applyMessageEnd([]byte(`{"message":{"role":"user","content":[{"type":"text","text":"look @shot.png"},{"type":"image"}]}}`))
	if len(m.blocks) != 1 || m.blocks[0].Kind != "user" {
		t.Fatalf("blocks = %+v", m.blocks)
	}
	if !strings.Contains(m.blocks[0].Text, "📷 1 image attached") {
		t.Fatalf("text = %q", m.blocks[0].Text)
	}
	if got := withImages("", 2); got != "📷 2 images attached" {
		t.Fatalf("image-only = %q", got)
	}
	if got := withImages("hi", 0); got != "hi" {
		t.Fatalf("no-image passthrough = %q", got)
	}
}
// Gutter: icon on the first line, continuations flush-left (no indent),
// separators untouched.
func TestGutter(t *testing.T) {
	got := gutter("●", "head\ncont\n\n")
	want := "● head\ncont\n\n"
	if got != want {
		t.Fatalf("gutter = %q, want %q", got, want)
	}
	got = gutter("○", "only\n\n")
	if !strings.HasPrefix(got, "○ only\n") {
		t.Fatalf("single line gutter: %q", got)
	}
}

// gutterBox: bordered blocks keep a blank 2-cell gutter so every row is
// as wide as the first and box borders stay vertically aligned.
func TestGutterBox(t *testing.T) {
	got := gutterBox("●", "head\ncont\n\n")
	want := "● head\n  cont\n\n"
	if got != want {
		t.Fatalf("gutterBox = %q, want %q", got, want)
	}
}

// frameCols returns the display-cell positions of every table frame glyph.
func frameCols(r string) []int {
	rs := []rune(r)
	var out []int
	for i := range rs {
		if strings.ContainsRune("│┼┬┴├┤┌┐└┘", rs[i]) {
			out = append(out, lipgloss.Width(string(rs[:i])))
		}
	}
	return out
}

func equalCols(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Regression (screenshot: table opening an assistant reply): gutter()
// prefixed "● " to the first row only, pushing it 2 cells past the body
// rows. Table-led replies keep the 2-cell gutter so every column aligns.
func TestAssistantTableRowsAligned(t *testing.T) {
	m := Model{blocks: []Block{{Kind: "assistant", Text: "| Cột 1 | Cột 2 |\n|---|---|\n| Ô 1 | Ô 2 |\n| Ô 3 | Ô 4 |"}}}
	m.vp = viewport.New(100, 20)
	rows := strings.Split(stripANSI(m.renderBlocks()), "\n")
	var want []int
	found := 0
	for _, r := range rows {
		set := frameCols(r)
		if len(set) == 0 {
			continue
		}
		found++
		if want == nil {
			want = set
		} else if !equalCols(set, want) {
			t.Fatalf("table col misaligned: %q vs %v\n%s", r, want, strings.Join(rows, "\n"))
		}
	}
	if found < 5 {
		t.Fatalf("table rendered %d framed rows, want >=5:\n%s", found, strings.Join(rows, "\n"))
	}
}
// Regression (screenshot: "hello" vs "continue"): a user box rendered
// through the flush-left gutter came out with the top border sticking 2
// cells past the sides. Every rendered row must share one visual width.
func TestUserBoxRowsAligned(t *testing.T) {
	m := Model{blocks: []Block{{Kind: "user", Text: "hello"}}}
	m.vp = viewport.New(100, 20)
	rows := strings.Split(stripANSI(m.renderBlocks()), "\n")
	var widths []int
	for _, r := range rows {
		if r == "" {
			continue
		}
		widths = append(widths, lipgloss.Width(r))
	}
	if len(widths) < 3 {
		t.Fatalf("user box rendered %d rows, want >=3: %q", len(widths), rows)
	}
	for _, w := range widths[1:] {
		if w != widths[0] {
			t.Fatalf("box rows misaligned: widths %v\n%q", widths, strings.Join(rows, "\n"))
		}
	}
	if !strings.HasPrefix(rows[1], "  ") {
		t.Fatalf("box continuation missing 2-cell gutter: %q", rows[1])
	}
}
