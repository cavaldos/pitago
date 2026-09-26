package app

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"pitago/src/components/format"
	"pitago/src/components/theme"
)

// frameRows is one rendered block split into its rows, with the trailing
// blank separator dropped, so a test can look at the first and last row
// of the frame without counting padding. Rows keep their ANSI: the fill
// test needs it, and prefix checks strip it first.
func frameRows(t *testing.T, out string) []string {
	t.Helper()
	rows := strings.Split(out, "\n")
	for len(rows) > 0 && strings.TrimSpace(rows[len(rows)-1]) == "" {
		rows = rows[:len(rows)-1]
	}
	if len(rows) == 0 {
		t.Fatal("block rendered no rows")
	}
	return rows
}

func readBlock(status string) Block {
	return Block{
		Kind: "tool", ToolName: "read", ToolStatus: status,
		ToolArgs:    "src/app/view.go",
		ToolArgsRaw: `{"file_path":"src/app/view.go"}`,
		ToolResult:  "package app\n",
	}
}

func bashBlock(status, result string) Block {
	return Block{
		Kind: "tool", ToolName: "bash", ToolStatus: status,
		ToolArgs:   "go build ./...",
		ToolResult: result,
	}
}

// A tool call is visually bounded: the frame opens on the first row,
// closes on the last, and every framed row is exactly the render width.
func TestToolBlockIsFramed(t *testing.T) {
	for _, bl := range []Block{
		readBlock("done"),
		readBlock("running"),
		readBlock("error"),
		bashBlock("done", "ok  \tpitago\t0.5s\n"),
		{Kind: "tool", ToolName: "cd", ToolStatus: "done", ToolArgs: "src/components"},
		{Kind: "tool", ToolName: "fetch", ToolStatus: "done", ToolArgs: "https://example.test"},
		{Kind: "tool", ToolName: "subagent_launch", ToolStatus: "running", ToolArgs: "explorer"},
		{Kind: "bash", Text: "$ ls src"},
	} {
		const cw = 60
		out, skip := (&Model{}).renderOneBlock(bl, cw)
		if skip {
			t.Fatalf("%s/%s: block was skipped", bl.Kind, bl.ToolName)
		}
		rows := frameRows(t, out)
		first, last := stripANSI(rows[0]), stripANSI(rows[len(rows)-1])
		if !strings.HasPrefix(first, "╭─") {
			t.Fatalf("%s/%s: first row must open the frame: %q", bl.Kind, bl.ToolName, first)
		}
		if !strings.HasPrefix(last, "╰─") || !strings.HasSuffix(last, "╯") {
			t.Fatalf("%s/%s: last row must close the frame: %q", bl.Kind, bl.ToolName, last)
		}
		for i, r := range rows {
			if got := lipgloss.Width(r); got != cw {
				t.Fatalf("%s/%s: row %d is %d cells, want %d: %q",
					bl.Kind, bl.ToolName, i, got, cw, stripANSI(r))
			}
		}
		// A shell block leads with the prompt, not the tool name.
		if bl.Kind == "tool" && !isShell(bl.ToolName) && !strings.Contains(stripANSI(out), bl.ToolName) {
			t.Fatalf("%s: tool name must stay readable inside the frame", bl.ToolName)
		}
	}
}

// Assistant prose is not a tool: it stays unboxed, so the frame reads as
// "something was executed here" and the reply is free to use the full
// column. (A user turn keeps its own pre-existing prompt box — that is a
// separate, deliberate design element, not a tool block.)
func TestProseBlocksAreNotFramed(t *testing.T) {
	for _, bl := range []Block{
		{Kind: "assistant", Text: "Here is what changed and why it matters."},
		{Kind: "thinking", Text: "considering the block layout"},
		{Kind: "notice", Text: "model reloaded"},
	} {
		out, _ := (&Model{}).renderOneBlock(bl, 60)
		if strings.ContainsAny(stripANSI(out), "╭╰│") {
			t.Fatalf("%s block must not be framed: %q", bl.Kind, stripANSI(out))
		}
	}
}

// The frame is a hard width clamp: a row wider than the column (a long
// unbreakable token, a narrow terminal) must wrap inside the box, never
// push the right border out and never break a border mid-line.
func TestToolBlockNeverExceedsWidth(t *testing.T) {
	long := strings.Repeat("x", 400)
	bl := bashBlock("done", "short line\n"+long+"\nhttps://example.test/"+long)
	for _, cw := range []int{8, 12, 20, 24, 31, 60, 94, 200} {
		want := cw
		if want < blockMinW {
			want = blockMinW
		}
		out, _ := (&Model{}).renderOneBlock(bl, cw)
		for i, r := range frameRows(t, out) {
			if got := lipgloss.Width(r); got > want {
				t.Fatalf("cw=%d: row %d is %d cells, over the %d-cell column: %q",
					cw, i, got, want, stripANSI(r))
			}
		}
	}
}

// Status drives the fill and the frame; the four classes must not all
// resolve to the same color, and the frame only reddens on failure.
func TestToolBlockPaletteByStatus(t *testing.T) {
	classes := []string{
		format.StatusRunning, format.StatusSuccess,
		format.StatusError, format.StatusNeutral,
	}
	fills := map[string]bool{}
	frames := map[string]bool{}
	for _, c := range classes {
		tb := blockThemeFor(c)
		if tb.fill == "" || tb.frame == "" {
			t.Fatalf("%s: frame/fill must both resolve from the theme: %+v", c, tb)
		}
		fills[string(tb.fill)] = true
		frames[string(tb.frame)] = true
	}
	if len(fills) != len(classes) {
		t.Fatalf("each status needs its own fill, got %d for %d classes", len(fills), len(classes))
	}
	if blockThemeFor(format.StatusError).frame != cToolFrameErr {
		t.Error("a failed block must take the error frame")
	}
	if blockThemeFor(format.StatusSuccess).frame != cToolFrame {
		t.Error("a successful block keeps the neutral frame; its fill carries the state")
	}
	// toolBorder is the status-keyed shorthand and must agree.
	if toolBorder("error") != blockThemeFor(format.StatusError).frame ||
		toolBorder("done") != blockThemeFor(format.StatusSuccess).frame {
		t.Error("toolBorder must resolve through the same palette as framedBlock")
	}
}

// The kind accent is picked from the tool name, so a read never reads
// like a write, and every kind maps to a distinct theme slot.
func TestToolKindAccents(t *testing.T) {
	tools := map[string]string{
		"read":  format.KindRead,
		"write": format.KindWrite,
		"edit":  format.KindEdit,
		"bash":  format.KindShell,
		"cd":    format.KindDir,
		"grep":  format.KindSearch,
		"fetch": format.KindOther,
	}
	seen := map[string]string{}
	for tool, kind := range tools {
		if got := format.ToolKind(tool); got != kind {
			t.Fatalf("ToolKind(%q) = %q, want %q", tool, got, kind)
		}
		acc := string(toolAccent(tool))
		if acc == "" {
			t.Fatalf("%s: accent must resolve from the theme", tool)
		}
		if prev, dup := seen[acc]; dup {
			t.Fatalf("%s and %s share accent %q: kinds must be distinguishable", tool, prev, acc)
		}
		seen[acc] = tool
	}
	if !toolNameStyleFor("read").GetBold() {
		t.Error("the tool name must stay bold, it is the block's title")
	}
}

// Switching themes at runtime recolors the blocks: no fill, frame or
// accent literal survives in the render path.
func TestToolBlockFollowsTheme(t *testing.T) {
	defer ApplyTheme(theme.Get("default"))
	before := blockThemeFor(format.StatusSuccess)
	beforeAccent := string(toolAccent("read"))
	beforeNeutral := string(cToolNeutral)

	one := theme.Get("one-dark")
	ApplyTheme(one)
	after := blockThemeFor(format.StatusSuccess)
	if string(after.fill) != one.ToolSuccess || string(after.frame) != one.Tool.Frame {
		t.Fatalf("fill/frame must come from the theme: %v vs %+v", after, one)
	}
	if string(toolAccent("read")) != one.Tool.Read {
		t.Fatalf("accent must come from the theme, got %q want %q", toolAccent("read"), one.Tool.Read)
	}
	if string(cToolNeutral) != one.ToolNeutral {
		t.Fatalf("neutral fill must come from the theme, got %q want %q", cToolNeutral, one.ToolNeutral)
	}
	if string(after.fill) == string(before.fill) || string(toolAccent("read")) == beforeAccent {
		t.Fatal("a theme switch must actually change the block colors")
	}
	// The neutral fill is optional per theme: an unset one falls back to
	// the pending panel rather than leaving the block unfilled.
	nord := theme.Get("nord")
	if nord.ToolNeutral == "" {
		t.Fatal("Resolve must always produce a neutral fill")
	}
	if string(beforeNeutral) == "" {
		t.Fatal("default palette must define the neutral fill")
	}
}

// fillRow re-arms the fill after every reset inside a row. Without it a
// syntax-highlighted body would punch transparent gaps between tokens.
func TestFillRowRearmsAfterEveryReset(t *testing.T) {
	const bg = "\x1b[48;2;40;40;50m"
	row := "\x1b[38;5;1mconst\x1b[0m \x1b[38;5;2ma\x1b[0m"
	want := bg + "\x1b[38;5;1mconst\x1b[0m" + bg + " \x1b[38;5;2ma\x1b[0m" + bg + "\x1b[0m"
	if got := fillRow(row, bg); got != want {
		t.Fatalf("fillRow = %q, want %q", got, want)
	}
	if got := fillRow(row, ""); got != row {
		t.Fatalf("no fill sequence must be a no-op, got %q", got)
	}
	if got := fillRow("", bg); got != "" {
		t.Fatalf("a blank row must stay blank, got %q", got)
	}
}

// With a color profile that has a background, the fill actually reaches
// every row of a highlighted body, and the frame is unaffected.
func TestToolBlockFillUnderColorProfile(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	defer lipgloss.SetColorProfile(prev)

	seq := fillSeq(cToolSuccess)
	if seq == "" {
		t.Fatal("a truecolor profile must produce a fill sequence")
	}
	if got := fillSeq(lipgloss.Color("")); got != "" {
		t.Fatalf("an unset fill must produce no sequence, got %q", got)
	}
	bl := readBlock("done")
	bl.ToolResult = "package app\n\nfunc main() { fmt.Println(\"hi\") }\n"
	m := Model{expandTools: true}
	out, _ := m.renderOneBlock(bl, 60)
	rows := frameRows(t, out)
	content := 0
	for i, r := range rows {
		if got := lipgloss.Width(r); got != 60 {
			t.Fatalf("row %d is %d cells with a fill, want 60", i, got)
		}
		// The fill must reach every content row; the top/bottom frame
		// glyphs are painted by lipgloss's border pass, which is out of
		// scope for this assertion.
		if !strings.Contains(stripANSI(r), "│") {
			continue
		}
		content++
		if !strings.Contains(r, seq) {
			t.Fatalf("content row %d lost the block fill: %q", i, stripANSI(r))
		}
	}
	if content == 0 {
		t.Fatal("expected at least one content row inside the frame")
	}
}

// A block under a color-less profile keeps its frame and text and simply
// drops the fill, instead of collapsing into flat raw rows.
func TestToolBlockDegradesWithoutColor(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.Ascii)
	defer lipgloss.SetColorProfile(prev)

	if seq := fillSeq(cToolSuccess); seq != "" {
		t.Fatalf("an ascii profile must produce no fill sequence, got %q", seq)
	}
	out, _ := (&Model{}).renderOneBlock(bashBlock("done", "ok\n"), 60)
	plain := stripANSI(out)
	if !strings.HasPrefix(plain, "╭─") || !strings.Contains(plain, "╰") {
		t.Fatalf("the frame must survive a color-less terminal: %q", plain)
	}
	if strings.Contains(out, "\x1b[48;") {
		t.Fatalf("a color-less terminal must not receive a background: %q", out)
	}
}

// Both the collapsed and the expanded (ctrl+g) path keep their hint
// inside the box, and both still close the frame.
func TestToolBlockCollapseHintsStayInside(t *testing.T) {
	var out strings.Builder
	for i := 1; i <= 30; i++ {
		out.WriteString("output line\n")
	}
	bl := bashBlock("done", out.String())
	for _, expanded := range []bool{false, true} {
		m := Model{expandTools: expanded}
		got, _ := m.renderOneBlock(bl, 60)
		plain := stripANSI(got)
		rows := frameRows(t, got)
		first, last := stripANSI(rows[0]), stripANSI(rows[len(rows)-1])
		if !strings.HasPrefix(first, "╭─") || !strings.HasPrefix(last, "╰─") {
			t.Fatalf("expanded=%v: frame must stay closed", expanded)
		}
		wantHint := expandHint
		if expanded {
			wantHint = collapseHint
		}
		idx := strings.Index(plain, wantHint)
		if idx < 0 {
			t.Fatalf("expanded=%v: missing %q hint", expanded, wantHint)
		}
		line := plain[strings.LastIndex(plain[:idx], "\n")+1:]
		if !strings.HasPrefix(line, "│") {
			t.Fatalf("expanded=%v: hint must render inside the box: %q", expanded, line)
		}
	}
}
