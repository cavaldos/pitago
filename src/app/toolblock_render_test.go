package app

import (
	"fmt"
	"regexp"
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
		Kind: "tool", ToolName: "read", Expanded: true, ToolStatus: status,
		ToolArgs:    "src/app/view.go",
		ToolArgsRaw: `{"file_path":"src/app/view.go"}`,
		ToolResult:  "package app\n",
	}
}

func bashBlock(status, result string) Block {
	return Block{
		Kind: "tool", ToolName: "bash", Expanded: true, ToolStatus: status,
		ToolArgs:   "go build ./...",
		ToolResult: result,
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
		out, _ := (&Model{expandTools: true}).renderOneBlock(bl, 60)
		if strings.ContainsAny(stripANSI(out), "╭╰│") {
			t.Fatalf("%s block must not be framed: %q", bl.Kind, stripANSI(out))
		}
	}
}

// Reasoning text is italic: the block body carries SGR 3 next to the dim
// colour. Needs a real color profile — the default Ascii profile drops it.
func TestThinkingBlockIsItalic(t *testing.T) {
	prev := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.ANSI)
	defer lipgloss.SetColorProfile(prev)
	out, skip := (&Model{}).renderOneBlock(Block{Kind: "thinking", Text: "considering"}, 60)
	if skip {
		t.Fatal("thinking block skipped")
	}
	if !regexp.MustCompile(`\x1b\[(?:[0-9]+;)*3[;m]`).MatchString(out) {
		t.Fatalf("thinking not italic: %q", out)
	}
	if !strings.Contains(stripANSI(out), "considering") {
		t.Fatalf("thinking lost text: %q", out)
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
		out, _ := (&Model{expandTools: true}).renderOneBlock(bl, cw)
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

// Tool blocks have three levels, all unframed: one header line by
// default, a few lines when clicked, everything with ctrl+g.
func TestToolBlockThreeLevels(t *testing.T) {
	var out strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&out, "row%02d\n", i)
	}
	bl := Block{Kind: "tool", ToolName: "read", ToolStatus: "done",
		ToolArgs: "test.go:1-30", ToolResult: out.String()}

	collapsed := stripANSI((&Model{}).renderToolBlock(bl, 80))
	if collapsed != "● Read(test.go:1-30)" {
		t.Fatalf("default must be one header line, got %q", collapsed)
	}

	bl.Expanded = true
	opened := stripANSI((&Model{}).renderToolBlock(bl, 80))
	if !strings.Contains(opened, "row01") || strings.Contains(opened, "row30") ||
		!strings.Contains(opened, "+15 lines") {
		t.Fatalf("clicked must show the first lines only, got:\n%s", opened)
	}

	bl.Expanded = false
	full := stripANSI((&Model{expandTools: true}).renderToolBlock(bl, 80))
	if !strings.Contains(full, "row30") {
		t.Fatalf("ctrl+g must show everything, got:\n%s", full)
	}
	for _, s := range []string{opened, full} {
		if strings.ContainsAny(s, "╭╰│") {
			t.Fatalf("tool blocks must never be framed:\n%s", s)
		}
	}
}
