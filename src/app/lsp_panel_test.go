package app

import (
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

// The payload pi-lsp returns for lsp_diagnostics (runner.formatDiagnostics):
// a summary header, then one line per diagnostic, plus a "no diagnostics" line
// for a clean file.
const lspFixture = `gopls LSP diagnostics: 3 diagnostic(s) across 3 file(s).

src/app/view.go:12:5: error staticcheck: declared and not used: err
src/app/long_file_name_that_overflows_the_column.go:3:1: warning govet S1000: should use x += 1
src/app/view.go:40:2: info gopls: unused parameter
src/components/theme/theme.go:7:1: error compile: undefined: Colors
src/app/short.go:5:1: error govet: oops
src/app/clean.go: no diagnostics`

func lspModel(t *testing.T, result string) Model {
	t.Helper()
	m := New(nil, t.TempDir())
	m.Side = map[string]bool{SideLSP: true}
	if result != "" {
		m.blocks = append(m.blocks, Block{
			Kind: "tool", ToolName: lspDiagnosticsTool, ToolStatus: "done",
			ToolCallID: "call_1", ToolResult: result,
		})
	}
	return m
}

func TestParseLspDiagnostics(t *testing.T) {
	diags := parseLspDiagnostics(lspFixture)
	if len(diags) != 5 {
		t.Fatalf("want 5 diagnostics (the 'no diagnostics' line is not one), got %d: %+v", len(diags), diags)
	}
	d := diags[0]
	if d.File != "src/app/view.go" || d.Line != 12 || d.Col != 5 {
		t.Errorf("position parsed wrong: %+v", d)
	}
	if d.Severity != lspSevError || d.Source != "staticcheck" || d.Code != "" {
		t.Errorf("severity/source parsed wrong: %+v", d)
	}
	if d.Message != "declared and not used: err" {
		t.Errorf("message must keep its own colon, got %q", d.Message)
	}
	w := diags[1]
	if w.Severity != lspSevWarning || w.Source != "govet" || w.Code != "S1000" {
		t.Errorf("code must split off the source: %+v", w)
	}
	if diags[2].Severity != lspSevInfo || diags[2].Source != "gopls" {
		t.Errorf("info row parsed wrong: %+v", diags[2])
	}
}

func TestParseLspDiagnosticsIgnoresProse(t *testing.T) {
	// The route reason, the "---" joiner and the skipped-server note must
	// not turn into diagnostics.
	out := `ruff diagnostics

py/app.py:9:9: error ruff F841: local variable x is assigned but never used
---

Skipped unavailable default LSP server(s): gopls.`
	diags := parseLspDiagnostics(out)
	if len(diags) != 1 || diags[0].File != "py/app.py" ||
		diags[0].Source != "ruff" || diags[0].Code != "F841" {
		t.Fatalf("only the diagnostic line should parse, got %+v", diags)
	}
}

// Counts drive the badge: errors and warnings only, never info/hint.
func TestLspCounts(t *testing.T) {
	errs, warns := lspCounts(parseLspDiagnostics(lspFixture))
	if errs != 3 || warns != 1 {
		t.Fatalf("badge counts = %d errors / %d warnings, want 3/1", errs, warns)
	}
}

// Files with errors float up, rows inside a file run error -> warning -> info.
func TestGroupLspDiagnostics(t *testing.T) {
	files := groupLspDiagnostics(parseLspDiagnostics(lspFixture))
	if len(files) != 4 {
		t.Fatalf("want 4 files, got %d: %+v", len(files), files)
	}
	// Three files tie on one error each, so path breaks the tie; the
	// warning-only file sinks to the bottom.
	want := []string{
		"src/app/short.go",
		"src/app/view.go",
		"src/components/theme/theme.go",
		"src/app/long_file_name_that_overflows_the_column.go",
	}
	for i, w := range want {
		if files[i].Path != w {
			t.Errorf("file %d = %q, want %q", i, files[i].Path, w)
		}
	}
	var view []LspDiagnostic
	for _, f := range files {
		if f.Path == "src/app/view.go" {
			view = f.Diags
		}
	}
	if len(view) != 2 || view[0].Severity != lspSevError || view[1].Severity != lspSevInfo {
		t.Errorf("rows inside a file must run error -> info, got %+v", view)
	}
}

// The section renders from a fixture payload, grouped, inside the column.
func TestRenderLspSectionFromFixture(t *testing.T) {
	m := lspModel(t, lspFixture)
	out := lspPlain(m.renderLspSection(sideInnerW))
	// The title is the server that produced the data, like pi's "pi-lens"
	// header — not a hardcoded "LSP".
	if !strings.Contains(out, "gopls") {
		t.Fatalf("server name missing from the title:\n%s", out)
	}
	// pi's header badge: ●3E !1W — no spaces inside a group, one between.
	if !strings.Contains(out, "●3E !1W") {
		t.Errorf("header badge wrong:\n%s", out)
	}
	// One row per file, carrying that file's own counts.
	if !strings.Contains(out, "● src/app/short.go") {
		t.Errorf("file row missing:\n%s", out)
	}
	if !strings.Contains(out, "1E") {
		t.Errorf("per-file counts missing:\n%s", out)
	}
	// A row is "● L5 govet  oops": glyph, L<line>, tag, two spaces, message.
	if !strings.Contains(out, "● L5 govet  oops") {
		t.Errorf("row shape wrong:\n%s", out)
	}
	// A message too long for the column is ellipsized, never wrapped.
	if !strings.Contains(out, "declared …") {
		t.Errorf("long message must truncate, not wrap:\n%s", out)
	}
}

// lspPlain strips the SGR sequences so a test can assert on a row the way a
// reader sees it: glyph, L<line>, tag, message.
func lspPlain(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Rows carry L<line> and no column — pi's shape.
func TestLspRowDropsTheColumn(t *testing.T) {
	out := lspPlain(lspModel(t, lspFixture).renderLspSection(sideInnerW))
	if !strings.Contains(out, "L12") || !strings.Contains(out, "L40") {
		t.Errorf("rows must carry L<line>:\n%s", out)
	}
	// The fixture's first diagnostic is line 12 col 5: "12:5" must be gone.
	if strings.Contains(out, "12:5") || strings.Contains(out, "L12:5") {
		t.Errorf("column must not be drawn:\n%s", out)
	}
}

// source+code is rejoined with ':' the way pi spells it, and a diagnostic
// with no source leaves no double-space gap behind.
func TestLspTagSpelling(t *testing.T) {
	if got := lspTag(LspDiagnostic{Source: "typescript", Code: "6133"}); got != "typescript:6133" {
		t.Errorf("source+code must join with ':', got %q", got)
	}
	if got := lspTag(LspDiagnostic{Source: "govet", Code: "S1000"}); got != "govet:S1000" {
		t.Errorf("got %q", got)
	}
	if got := lspTag(LspDiagnostic{Source: "staticcheck"}); got != "staticcheck" {
		t.Errorf("a bare source must stand alone, got %q", got)
	}
	if got := lspTag(LspDiagnostic{}); got != "" {
		t.Errorf("no source means no tag, got %q", got)
	}

	// Rendered: the tag sits between the line and two spaces before the
	// message...
	row := lspPlain(lspDiagRow(LspDiagnostic{Line: 162, Source: "typescript", Code: "6133",
		Message: "'wc' is declared"}, 80))
	if !strings.Contains(row, "● L162 typescript:6133  'wc' is declared") {
		t.Errorf("tag not drawn in pi's spelling: %q", row)
	}
	// ...and without a source the two spaces go with it, so no gap is left.
	bare := lspPlain(lspDiagRow(LspDiagnostic{Line: 162, Message: "boom"}, 80))
	if !strings.Contains(bare, "● L162 boom") {
		t.Errorf("a sourceless row must not leave a gap: %q", bare)
	}
	if strings.Contains(bare, "L162  boom") {
		t.Errorf("double space survived a missing tag: %q", bare)
	}
}

// When the column is too narrow for the tag and a trimmed tag would still
// starve the message, the tag is dropped outright and the message takes every
// cell — a 14-cell tag is not worth a 10-cell message.
func TestLspTagYieldsToMessage(t *testing.T) {
	tag := "@typescript-eslint/no-unused-vars"

	// Wide enough for the whole tag: it stays (this is pi's shape).
	if got := lspFitTag(tag, 52); got != tag {
		t.Errorf("a fitting tag must be kept whole, got %q", got)
	}
	// Narrow: a trimmed tag would leave the message 10 cells, but a
	// tag-less message gets 26 — so the tag goes.
	if got := lspFitTag(tag, 26); got != "" {
		t.Errorf("a starving tag must be dropped, got %q", got)
	}
	// Far too narrow for a message either way: trimming is still better
	// than nothing, and the caps apply.
	if got := lspFitTag(tag, 12); got == tag {
		t.Errorf("a tag that cannot fit must not be drawn whole, got %q", got)
	}
	// The rule is about the tag starving the message, not about width alone:
	// a short tag stays exactly as long as it leaves the message a readable
	// run (11-wide tag + 2 + 10 message cells = 23), and goes below that.
	if got := lspFitTag("govet:S1000", 26); got != "govet:S1000" {
		t.Errorf("an affordable short tag must survive, got %q", got)
	}
	if got := lspFitTag("govet:S1000", 22); got != "" {
		t.Errorf("a short tag that still starves the message must go, got %q", got)
	}
}

// The count spelling: header "●13E !8W", per-file "13E8W", and neither group
// drawn when its count is 0.
func TestLspCountSpelling(t *testing.T) {
	if got := lspPlain(lspCountBadge(13, 8)); got != "●13E !8W" {
		t.Errorf("header badge = %q, want ●13E !8W", got)
	}
	if got := lspPlain(lspCountBadge(13, 0)); got != "●13E" {
		t.Errorf("zero warnings must drop their group, got %q", got)
	}
	if got := lspPlain(lspCountBadge(0, 8)); got != "!8W" {
		t.Errorf("zero errors must drop their group, got %q", got)
	}
	if got := lspPlain(lspCountBadge(0, 0)); got != "" {
		t.Errorf("a clean panel keeps the badge minimal, got %q", got)
	}
	if got := lspFileCounts(13, 8); got != "13E8W" {
		t.Errorf("per-file counts = %q, want 13E8W", got)
	}
	if got := lspFileCounts(0, 8); got != "8W" {
		t.Errorf("got %q", got)
	}
	if got := lspFileCounts(13, 0); got != "13E" {
		t.Errorf("got %q", got)
	}
	if got := lspFileCounts(0, 0); got != "" {
		t.Errorf("got %q", got)
	}

	// A file row carries its own counts, not the panel totals.
	out := "gopls LSP diagnostics: 3 diagnostic(s) across 1 file(s).\n\n" +
		"a.go:1:1: error gopls E1: one\n" +
		"a.go:2:1: error gopls E2: two\n" +
		"a.go:3:1: warning gopls W3: three\n"
	row := lspPlain(lspFileRow("a.go", 2, 1, sideInnerW))
	if !strings.Contains(row, "● a.go") || !strings.Contains(row, "2E1W") {
		t.Errorf("file row = %q", row)
	}
	if !strings.Contains(lspPlain(lspModel(t, out).renderLspSection(sideInnerW)), "2E1W") {
		t.Errorf("section must draw the per-file counts:\n%s", lspModel(t, out).renderLspSection(sideInnerW))
	}
}

// pi lets the message run long: with a column wide enough it must survive
// whole. This is the regression the old ~10-cell clip caused.
func TestLspMessageNotClippedWhenWide(t *testing.T) {
	msg := "'wc' is declared but its value is never read. Remove it or use it."
	d := LspDiagnostic{Line: 162, Source: "typescript", Code: "6133", Message: msg}
	want := " ● L162 typescript:6133  " + msg
	for _, inner := range []int{160, 120, 100, lipgloss.Width(want)} {
		row := strings.TrimRight(lspPlain(lspDiagRow(d, inner)), " \n")
		if row != want {
			t.Errorf("inner=%d: message was clipped.\n got: %q\nwant: %q", inner, row, want)
		}
	}
	// One cell short of fitting, it truncates instead of overflowing.
	narrow := strings.TrimRight(lspPlain(lspDiagRow(d, lipgloss.Width(want)-1)), " \n")
	if narrow == want {
		t.Error("a row that cannot fit must truncate")
	}
	if !strings.HasSuffix(narrow, "…") {
		t.Errorf("truncation must end with an ellipsis, got %q", narrow)
	}
}

// The column must never overflow: every drawn row fits the inner width.
func TestRenderLspSectionRespectsWidth(t *testing.T) {
	long := "src/a_very_long_directory_tree/with/many/segments/and/a_long_file_name.go:1234:56: error some-extremely-long-source-name: " +
		strings.Repeat("very long diagnostic message ", 10)
	m := lspModel(t, long)
	for _, line := range strings.Split(strings.TrimRight(m.renderLspSection(sideInnerW), "\n"), "\n") {
		if w := lipgloss.Width(line); w > sideInnerW {
			t.Errorf("row is %d cells wide, over the %d-cell column: %q", w, sideInnerW, line)
		}
	}
}

// A single row survives any column width, down to one that cannot even hold
// line:col: it truncates instead of overflowing.
func TestLspDiagRowRespectsWidth(t *testing.T) {
	d := LspDiagnostic{File: "a/b/c.go", Line: 1234, Col: 56, Severity: lspSevError,
		Source: "some-extremely-long-source-name", Code: "X1", Message: strings.Repeat("boom ", 40)}
	for _, inner := range []int{sideInnerW, 40, 30, 24, 20, 16, 12, 8, 4, 1, 0} {
		row := lspDiagRow(d, inner)
		if w := lipgloss.Width(strings.TrimRight(row, "\n")); w > inner && inner > 0 {
			t.Errorf("inner=%d: row is %d cells wide: %q", inner, w, row)
		}
	}
}

// Empty states are a line, never a blank block — and they say *why*: a tool
// that never ran, a clean run, and a missing language server are three
// different problems and must not look alike.
func TestRenderLspSectionEmptyStates(t *testing.T) {
	if out := lspModel(t, "").renderLspSection(sideInnerW); !strings.Contains(out, "run lsp_diagnostics") {
		t.Errorf("no tool result must say so:\n%s", out)
	}
	clean := "gopls LSP diagnostics: 0 diagnostic(s) across 1 file(s).\n\nsrc/app/clean.go: no diagnostics"
	out := lspPlain(lspModel(t, clean).renderLspSection(sideInnerW))
	if !strings.Contains(out, "gopls clean") {
		t.Errorf("a clean run must name the server, not shrug:\n%s", out)
	}
	if strings.Contains(out, "●0E") || strings.Contains(out, "!0W") {
		t.Errorf("clean run must not draw a zero badge:\n%s", out)
	}

	// The real-world trap: every server was missing, so there are zero
	// diagnostics — but the reason must surface, or the panel looks broken.
	skipped := "Skipped unavailable default LSP server(s): biome, ty, gopls, rubocop."
	out = lspPlain(lspModel(t, skipped).renderLspSection(sideInnerW))
	if !strings.Contains(out, "no LSP server available") {
		t.Errorf("missing servers must be reported:\n%s", out)
	}
	// The names are kept so the reason survives a wider column.
	if st := lspStatusDistilled(skipped); len(st.Skipped) != 4 {
		t.Errorf("skipped servers must be parsed, got %v", st.Skipped)
	}
	// A named missing command is more actionable than the skip list, and its
	// own wording is what the user needs — so it is kept whole when it fits.
	notFound := "gopls LSP command not found: gopls. Install gopls or update its command in pi-lsp.json."
	if st := lspStatusDistilled(notFound); !strings.Contains(st.Note, "command not found") {
		t.Errorf("a missing command must be captured, got %q", st.Note)
	}
	if s := lspStatusSummary(lspStatusDistilled(notFound), 120); !strings.Contains(s, "command not found") {
		t.Errorf("a missing command must be shown:\n%s", s)
	}
	// A tool-level error is not a clean file.
	errRun := "LSP server parameter must not be blank."
	if st := lspStatusDistilled(errRun); st.Clean {
		t.Errorf("a tool error must not count as clean: %+v", st)
	}
	if s := lspStatusSummary(lspStatusDistilled(errRun), 120); !strings.Contains(s, "must not be blank") {
		t.Errorf("a tool error must be shown:\n%s", s)
	}
}

// Folding keeps the header (so the section stays discoverable) and drops rows.
func TestRenderLspSectionCollapsed(t *testing.T) {
	m := lspModel(t, lspFixture)
	if LspCollapsed() {
		ToggleLspCollapsed()
	}
	out := lspPlain(m.renderLspSection(sideInnerW))
	if !strings.Contains(out, "▾") || !strings.Contains(out, "staticcheck") {
		t.Fatalf("open section must show the mark and rows:\n%s", out)
	}
	ToggleLspCollapsed()
	out = lspPlain(m.renderLspSection(sideInnerW))
	if !strings.Contains(out, "▸") || strings.Contains(out, "staticcheck") {
		t.Fatalf("folded section must show the mark and no rows:\n%s", out)
	}
	if !strings.Contains(out, "●3E !1W") {
		t.Errorf("badge survives folding:\n%s", out)
	}
	ToggleLspCollapsed()
}

// A long report stops at the row cap and says how many were left, like Todos.
func TestRenderLspSectionCapsRows(t *testing.T) {
	var b strings.Builder
	b.WriteString("gopls LSP diagnostics: 40 diagnostic(s) across 2 file(s).\n\n")
	for i := 1; i <= 40; i++ {
		n := strconv.Itoa(i)
		b.WriteString("src/app/x.go:" + n + ":1: error gopls: boom " + n + "\n")
	}
	out := lspPlain(lspModel(t, b.String()).renderLspSection(sideInnerW))
	if !strings.Contains(out, "+20 more") {
		t.Errorf("want a +N more line after %d rows:\n%s", lspRowsMax, out)
	}
	if got := strings.Count(out, " gopls  "); got != lspRowsMax {
		t.Errorf("drew %d rows, want the %d cap:\n%s", got, lspRowsMax, out)
	}
}

// The newest result wins, and a running call with no text yet does not blank
// the panel.
func TestLspDiagnosticsPicksNewestResult(t *testing.T) {
	m := lspModel(t, "a.go:1:1: error old: first run")
	m.blocks = append(m.blocks,
		Block{Kind: "tool", ToolName: lspDiagnosticsTool, ToolStatus: "running", ToolCallID: "call_2"},
		Block{Kind: "tool", ToolName: lspDiagnosticsTool, ToolStatus: "done", ToolCallID: "call_3",
			ToolResult: "b.go:9:2: warning gopls: second run"},
	)
	if d := m.lspDiags(); len(d) != 1 || d[0].Message != "second run" {
		t.Fatalf("newest result must win, got %+v", d)
	}
}

// Refresh is memoized on the payload, so redraws are cheap, and it starts no
// goroutine: the render loop must not leak one per refresh.
func TestLspDiagnosticsMemoNoGoroutineLeak(t *testing.T) {
	before := runtime.NumGoroutine()
	m := lspModel(t, lspFixture)
	for i := 0; i < 500; i++ {
		m.lspDiags()
		m.renderLspSection(sideInnerW)
	}
	if after := runtime.NumGoroutine(); after > before {
		t.Errorf("rendering leaked goroutines: %d -> %d", before, after)
	}
	// A changed payload must invalidate the memo, not serve the old rows.
	other := lspModel(t, "z.go:3:3: error newserver: changed")
	if d := other.lspDiags(); len(d) != 1 || d[0].Message != "changed" {
		t.Fatalf("stale memo served: %+v", d)
	}
}

// The section is wired like its siblings: hidden until toggled, on in the
// composed sidebar once visible. prefsPath and Side are both pinned so the
// test decides the outcome — New() otherwise reads (and ToggleSideSection
// writes) the real ~/.config/pitago/prefs.json.
func TestSideLSPVisibility(t *testing.T) {
	m := New(nil, t.TempDir())
	m.prefsPath = filepath.Join(t.TempDir(), "prefs.json")
	m.Side = map[string]bool{}
	if !m.SideVisible(SideLSP) {
		t.Error("LSP must show by default, like its visible siblings")
	}
	m.ToggleSideSection(SideLSP)
	if m.SideVisible(SideLSP) {
		t.Fatal("toggle should hide it")
	}
	m.ToggleSideSection(SideLSP)
	if !m.SideVisible(SideLSP) {
		t.Fatal("toggle should show it again")
	}
	if sideLabel(SideLSP) == "" || sideLabel(SideLSP) == SideLSP {
		t.Errorf("LSP needs a display label, got %q", sideLabel(SideLSP))
	}
	m.blocks = lspModel(t, lspFixture).blocks
	out := lspPlain(m.buildSidebarContent())
	if !strings.Contains(out, "gopls") || !strings.Contains(out, "L12 staticcheck") {
		t.Errorf("visible LSP section missing from the sidebar:\n%s", out)
	}
	m.ToggleSideSection(SideLSP)
	if out := lspPlain(m.buildSidebarContent()); strings.Contains(out, "L12 staticcheck") {
		t.Errorf("hiding the section must drop its rows:\n%s", out)
	}
}
