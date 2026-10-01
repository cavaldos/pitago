package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/components/chat"
	terminal_image "pitago/src/components/terminal_image"
)

const dupEnd = `{"message":{"role":"assistant","content":[{"type":"text","text":"Hey there!"},{"type":"thinking","thinking":"user says hi"}]}}`

func pngTestData(t *testing.T, width, height int) string {
	t.Helper()
	if width != 1 || height != 1 {
		t.Fatalf("test PNG helper only supports 1x1, got %dx%d", width, height)
	}
	return "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+XwWjJwAAAABJRU5ErkJggg=="
}

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

func TestLiveToolResultErrorUpdatesMatchingSidebarRow(t *testing.T) {
	m := New(nil, t.TempDir())
	i := m.ensureTool("call-err", "bash")
	if m.blocks[i].ToolStatus != "running" {
		t.Fatalf("initial tool status = %q, want running", m.blocks[i].ToolStatus)
	}
	m.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"call-err","toolName":"bash","isError":true,"content":[{"type":"text","text":"command failed"}]}}`))

	if got := m.blocks[i]; got.ToolStatus != "error" || got.ToolResult != "command failed" {
		t.Fatalf("matched tool result = %+v, want error status and preserved result", got)
	}
	if out := stripANSI(m.buildSidebarContent()); !strings.Contains(out, "× bash · error") {
		t.Fatalf("sidebar missing matched live tool error:\n%s", out)
	}
}

func TestLiveToolResultErrorCreatesFallbackErrorBlock(t *testing.T) {
	m := New(nil, t.TempDir())
	m.AddBlock(Block{Kind: "assistant", Text: "before"})
	m.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"missing-call","toolName":"read","isError":true,"content":[{"type":"text","text":"read failed"}]}}`))

	if len(m.blocks) != 2 {
		t.Fatalf("blocks = %d, want assistant + fallback tool", len(m.blocks))
	}
	got := m.blocks[1]
	if got.Kind != "tool" || got.ToolName != "read" || got.ToolStatus != "error" || got.ToolResult != "read failed" {
		t.Fatalf("fallback tool block = %+v, want named error block with preserved result", got)
	}
	if got.ToolCallID != "missing-call" || m.tools["missing-call"] != 1 {
		t.Fatalf("fallback call identity was not preserved: block=%+v tools=%v", got, m.tools)
	}
	if out := stripANSI(m.buildSidebarContent()); !strings.Contains(out, "× read · error") {
		t.Fatalf("sidebar missing fallback live tool error:\n%s", out)
	}
}

func TestLiveNamedToolResultFallbackWithoutContent(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"empty-call","toolName":"bash","isError":true,"content":[]}}`))

	if len(m.blocks) != 1 {
		t.Fatalf("blocks = %d, want named fallback tool", len(m.blocks))
	}
	got := m.blocks[0]
	if got.Kind != "tool" || got.ToolName != "bash" || got.ToolCallID != "empty-call" || got.ToolStatus != "error" || got.ToolResult != "" {
		t.Fatalf("empty fallback tool block = %+v, want named error block with empty result", got)
	}
	if m.tools["empty-call"] != 0 {
		t.Fatalf("fallback call mapping = %v, want empty-call at 0", m.tools)
	}
	if out := stripANSI(m.buildSidebarContent()); !strings.Contains(out, "× bash · error") {
		t.Fatalf("sidebar missing empty-content fallback tool error:\n%s", out)
	}

	// Empty successful content is still a valid named invocation.
	done := New(nil, t.TempDir())
	done.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"empty-ok","toolName":"read","content":[]}}`))
	if len(done.blocks) != 1 || done.blocks[0].ToolStatus != "done" {
		t.Fatalf("empty successful fallback blocks = %+v, want one done tool", done.blocks)
	}
	if out := stripANSI(done.buildSidebarContent()); !strings.Contains(out, "✓ read · done") {
		t.Fatalf("sidebar missing empty successful fallback tool:\n%s", out)
	}

	// A call ID alone is not enough to infer the tool name.
	m.applyMessageEnd([]byte(`{"message":{"role":"toolResult","toolCallId":"nameless-call","content":[]}}`))
	if len(m.blocks) != 1 {
		t.Fatalf("nameless result created a block: %+v", m.blocks)
	}
}

// User echoes retain image payloads for RPC/history and render kitty
// unicode placeholder rows inline.
func TestUserEchoWithImages(t *testing.T) {
	m := Model{curAsst: -1, curThink: -1, ShowImages: true, ImageWidthCells: 20,
		ImageProtocol: terminal_image.Kitty}
	m.tools = make(map[string]int)
	m.applyMessageEnd([]byte(`{"message":{"role":"user","content":[{"type":"text","text":"look @shot.png"},{"type":"image","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M/wHwAF/gL+XwWjJwAAAABJRU5ErkJggg==","mimeType":"image/png"}]}}`))
	if len(m.blocks) != 1 || m.blocks[0].Kind != "user" || len(m.blocks[0].Images) != 1 {
		t.Fatalf("blocks = %+v", m.blocks)
	}
	if !strings.Contains(m.blocks[0].Text, "📷 1 image attached") {
		t.Fatalf("text = %q", m.blocks[0].Text)
	}
	m.vp = viewport.New(50, 20)
	got := m.renderBlocks()
	// The 1x1 png at width 20 reserves 10 rows of 20 placeholder cells,
	// placed with a U=1 sequence after a one-time upload.
	if n := strings.Count(got, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("want 200 placeholder cells, got %d", n)
	}
	if !strings.Contains(got, "a=p,q=2,U=1") || !strings.Contains(got, "a=t,f=100") {
		t.Fatalf("transcript must place (and upload) the image")
	}
	if got := withImages("", 2); got != "📷 2 images attached" {
		t.Fatalf("image-only = %q", got)
	}
	if got := withImages("hi", 0); got != "hi" {
		t.Fatalf("no-image passthrough = %q", got)
	}
}

func TestRenderOneBlockImagePlaceholderOnKittyFallbackElsewhere(t *testing.T) {
	bl := Block{Kind: "user", Text: "look", Images: []chat.Image{
		chat.NewImage(pngTestData(t, 1, 1), "image/png"),
	}}
	// Kitty + shown: placeholder rows, flush-left below the user box.
	m := Model{ShowImages: true, ImageWidthCells: 20, ImageProtocol: terminal_image.Kitty}
	got, skip := m.renderOneBlock(bl, 48)
	if skip {
		t.Fatal("user image block was skipped")
	}
	if n := strings.Count(got, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("kitty want 200 placeholder cells, got %d", n)
	}
	if !strings.Contains(got, "a=p,q=2,U=1") || strings.Contains(got, "□") {
		t.Fatalf("kitty must place placeholders: %q", got[:200])
	}
	// iTerm2, hidden, or no protocol: the historical □ fallback, no escapes.
	for _, tc := range []struct {
		name     string
		show     bool
		protocol terminal_image.Protocol
	}{
		{"iterm2", true, terminal_image.ITerm2},
		{"hidden", false, terminal_image.Kitty},
		{"none", true, ""},
	} {
		m := Model{ShowImages: tc.show, ImageWidthCells: 20, ImageProtocol: tc.protocol}
		got, skip := m.renderOneBlock(bl, 48)
		if skip {
			t.Fatalf("%s: user image block was skipped", tc.name)
		}
		if !strings.HasSuffix(got, "□\n") {
			t.Fatalf("%s: fallback = %q", tc.name, got)
		}
		if strings.Contains(got, "\x1b_G") || strings.Contains(got, "\x1b]1337") || strings.Contains(got, terminal_image.PlaceholderChar) {
			t.Fatalf("%s: emitted image bytes: %q", tc.name, got)
		}
	}
}

func TestRenderBlocksImageStableAcrossScroll(t *testing.T) {
	img := chat.NewImage(pngTestData(t, 1, 1), "image/png")
	m := Model{blocks: []Block{{Kind: "user", Text: "look", Images: []chat.Image{img}}},
		ShowImages: true, ImageWidthCells: 20, ImageProtocol: terminal_image.Kitty}
	m.vp = viewport.New(50, 20)
	first := m.renderBlocks()
	if n := strings.Count(first, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("want 200 placeholder cells, got %d", n)
	}
	// Scroll repaint: renderBlocks is offset-independent, so the
	// placeholder grid is stable and the line stays byte-identical — the
	// renderer therefore writes nothing new. With U=1 the image lives in
	// the cells, so scrolling needs no delete pass.
	m.vp.YOffset = 3
	repaint := m.renderBlocks()
	if n := strings.Count(repaint, terminal_image.PlaceholderChar); n != 20*10 {
		t.Fatalf("scroll must keep 200 placeholder cells, got %d", n)
	}
	if !strings.Contains(repaint, "a=p,q=2,U=1") || !strings.Contains(repaint, "a=t,f=100") {
		t.Fatal("scroll must place and carry the upload, or a paint after a dropped render shows nothing")
	}
	if repaint != first {
		t.Fatal("scroll must not change the image bytes")
	}
	// Non-kitty protocol falls back to squares with no escapes.
	m.ImageProtocol = terminal_image.ITerm2
	m.ImageWidthCells = 120
	m.ShowImages = false
	if got := m.renderBlocks(); strings.Contains(got, "\x1b_G") || !strings.Contains(got, "□") {
		t.Fatalf("fallback transcript = %q", got)
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
	// jumpBlock: -1 = no "jumped here" mark; the zero value would mark block 0.
	m := Model{blocks: []Block{{Kind: "user", Text: "hello"}}, jumpBlock: -1}
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
