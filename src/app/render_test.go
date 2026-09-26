package app

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"pitago/src/pirpc"
)

func TestCodeLang(t *testing.T) {
	cases := []struct {
		in       string
		wantLang string
		wantOK   bool
	}{
		{"ok saved", "", false},
		{"file written ⏎ done", "", false},
		{"```go⏎func main() {}⏎```", "go", true},
		{"```⏎hello⏎```", "", true},
		{`{"a": 1}`, "json", true},
		{`[1, 2]`, "json", true},
		{"diff --git a/b ⏎ +++ c", "diff", true},
		{"--- a ⏎ +++ b", "diff", true},
	}
	for _, c := range cases {
		lang, ok := codeLang(c.in)
		if ok != c.wantOK || lang != c.wantLang {
			t.Errorf("codeLang(%q) = (%q, %v), want (%q, %v)",
				c.in, lang, ok, c.wantLang, c.wantOK)
		}
	}
}

func TestRenderBlocksSkipsBlank(t *testing.T) {
	// Streaming leaves content-less assistant/thinking blocks behind (bare
	// newlines around a tool call). They must not render as blank gaps.
	m := Model{blocks: []Block{
		{Kind: "user", Text: "hello"},
		{Kind: "assistant", Text: ""},
		{Kind: "assistant", Text: "\n\n"},
		{Kind: "thinking", Text: "   "},
		{Kind: "thinking", Text: "real thought"},
	}}
	clean := Model{blocks: []Block{
		{Kind: "user", Text: "hello"},
		{Kind: "thinking", Text: "real thought"},
	}}
	if got, want := m.renderBlocks(), clean.renderBlocks(); got != want {
		t.Fatalf("blank blocks leaked a gap:\n%q\nwant:\n%q", got, want)
	}
}

func TestRenderToolResult(t *testing.T) {
	// plain prose keeps the dim style, no ANSI highlight
	plain := renderToolResult("bash", "done", "ok saved")
	if !strings.Contains(plain, "ok saved") {
		t.Fatalf("plain lost text: %q", plain)
	}
	if strings.Contains(plain, "\x1b[38;2;") {
		t.Fatalf("plain got truecolor highlight: %q", plain)
	}
	// code gets Chroma highlight (in-process, always available)
	code := renderToolResult("bash", "done", "```go⏎func main() {}⏎```")
	if !strings.Contains(code, "func") {
		t.Fatalf("code lost text: %q", code)
	}
	if !strings.Contains(code, "\x1b[") {
		t.Fatalf("code not highlighted: %q", code)
	}
	// bash shows the last 5 lines, no ⏎ collapsing
	multi := renderToolResult("bash", "done", "l1\nl2\nl3\nl4\nl5\nl6\nl7")
	if strings.Contains(multi, "⏎") {
		t.Fatalf("multi-line got collapsed: %q", multi)
	}
	if !strings.Contains(multi, "earlier lines") {
		t.Fatalf("bash missing earlier-lines hint: %q", multi)
	}
	if strings.Contains(multi, "l1\n") || !strings.Contains(multi, "l7") {
		t.Fatalf("bash should show tail lines: %q", multi)
	}
	// read hides output on success, shows on error
	if got := renderToolResult("read", "done", "file content"); got != "" {
		t.Fatalf("read success should hide output, got %q", got)
	}
	if got := renderToolResult("read", "error", "no such file"); !strings.Contains(got, "no such file") {
		t.Fatalf("read error should show, got %q", got)
	}
}

func writeBlock(content string) Block {
	raw, _ := json.Marshal(map[string]string{"path": "game.js", "content": content})
	return Block{
		Kind: "tool", ToolName: "write", ToolStatus: "done",
		ToolArgs:    "game.js",
		ToolArgsRaw: string(raw),
		ToolResult:  "Successfully wrote to game.js",
	}
}

func TestRenderToolBodyWriteAlwaysDetailed(t *testing.T) {
	var sb strings.Builder
	for i := 1; i <= 15; i++ {
		sb.WriteString(fmt.Sprintf("line%d\n", i))
	}
	bl := writeBlock(sb.String())
	for _, expanded := range []bool{false, true} {
		m := Model{expandTools: expanded}
		got := m.renderToolBody(bl)
		if !strings.Contains(got, "line1") || !strings.Contains(got, "line15") {
			t.Fatalf("write expanded=%v should show all content: %q", expanded, got)
		}
		if strings.Contains(got, "ctrl+g") {
			t.Fatalf("write expanded=%v should not offer collapse: %q", expanded, got)
		}
	}

	// full block header carries pi's "+N lines" suffix
	mFull := Model{blocks: []Block{bl}}
	mFull.vp = viewport.New(120, 20)
	full := stripANSI(mFull.renderBlocks())
	if !strings.Contains(full, "write game.js +15") {
		t.Fatalf("write header missing +N suffix: %q", full)
	}
}

func TestToolBg(t *testing.T) {
	// pi dark theme vars: pending #282832, success #283228, error #3c2828
	if got := string(toolBg("done")); got != "#283228" {
		t.Errorf("toolBg done = %q", got)
	}
	if got := string(toolBg("error")); got != "#3c2828" {
		t.Errorf("toolBg error = %q", got)
	}
	if got := string(toolBg("running")); got != "#282832" {
		t.Errorf("toolBg running = %q", got)
	}
	if got := string(toolBg("")); got != "#282832" {
		t.Errorf("toolBg default = %q", got)
	}
}

func TestRenderToolBodyReadCollapseExpand(t *testing.T) {
	m := Model{}
	bl := Block{Kind: "tool", ToolName: "read", ToolStatus: "done",
		ToolArgs: "game.js", ToolArgsRaw: `{"path":"game.js"}`,
		ToolResult: "const a = 1;\nconst b = 2;\n"}
	collapsed := m.renderToolBody(bl)
	if strings.Contains(collapsed, "const a") {
		t.Fatalf("collapsed read should hide content: %q", collapsed)
	}
	if !strings.Contains(collapsed, "… (2 lines, ctrl+g to expand)") {
		t.Fatalf("collapsed read missing compact hint: %q", collapsed)
	}
	m.expandTools = true
	expanded := m.renderToolBody(bl)
	// content is syntax-highlighted (ANSI), so compare stripped
	plain := stripANSI(expanded)
	if !strings.Contains(plain, "const a = 1;") {
		t.Fatalf("expanded read missing content: %q", expanded)
	}
	// errors always show, even collapsed
	m.expandTools = false
	bl.ToolStatus = "error"
	bl.ToolResult = "no such file"
	if got := m.renderToolBody(bl); !strings.Contains(got, "no such file") {
		t.Fatalf("read error should show: %q", got)
	}
}

func TestRenderToolBodyGenericCompact(t *testing.T) {
	for _, tool := range []string{"bash", "ffgrep", "custom_tool"} {
		t.Run(tool, func(t *testing.T) {
			bl := Block{Kind: "tool", ToolName: tool, ToolStatus: "done",
				ToolResult: "first\nsecond\nthird"}
			collapsed := (Model{}).renderToolBody(bl)
			for _, line := range []string{"first", "second", "third"} {
				if strings.Contains(collapsed, line) {
					t.Fatalf("collapsed %s leaked partial result %q: %q", tool, line, collapsed)
				}
			}
			if !strings.Contains(collapsed, "… (3 lines, ctrl+g to expand)") {
				t.Fatalf("collapsed %s missing compact hint: %q", tool, collapsed)
			}

			bl.ToolResult = "No files found matching pattern"
			oneLine := (Model{}).renderToolBody(bl)
			if !strings.Contains(oneLine, "└── No files found matching pattern") {
				t.Fatalf("one-line %s result should stay visible: %q", tool, oneLine)
			}
			if strings.Contains(oneLine, "ctrl+g") {
				t.Fatalf("one-line %s result should not show expand hint: %q", tool, oneLine)
			}

			bl.ToolResult = "first\nsecond\nthird"
			expanded := stripANSI((Model{expandTools: true}).renderToolBody(bl))
			if !strings.Contains(expanded, "first") || !strings.Contains(expanded, "third") {
				t.Fatalf("expanded %s missing full result: %q", tool, expanded)
			}
			if !strings.Contains(expanded, "ctrl+g to collapse") {
				t.Fatalf("expanded %s missing collapse hint: %q", tool, expanded)
			}

			bl.ToolStatus = "error"
			bl.ToolResult = "permission denied\nretry failed"
			failed := (Model{}).renderToolBody(bl)
			if !strings.Contains(failed, "permission denied") || !strings.Contains(failed, "retry failed") {
				t.Fatalf("%s error should remain visible: %q", tool, failed)
			}
			if strings.Contains(failed, "ctrl+g") {
				t.Fatalf("%s error should not advertise collapse: %q", tool, failed)
			}
		})
	}
}

func TestRenderToolBodyEditDiff(t *testing.T) {
	bl := Block{Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgs:    "a.go",
		ToolArgsRaw: `{"path":"a.go","edits":[{"oldText":"foo","newText":"bar"}]}`,
		ToolResult:  ""}
	for _, expanded := range []bool{false, true} {
		m := Model{expandTools: expanded}
		if got := m.renderToolBody(bl); !strings.Contains(got, "foo") || !strings.Contains(got, "bar") {
			t.Fatalf("edit expanded=%v fallback diff missing: %q", expanded, got)
		}
	}

	// pi's diff arrives in details.diff while ToolResult carries the receipt
	// (an args reconstruction would otherwise outrank a real 15-line diff).
	var diff strings.Builder
	diff.WriteString("--- a/a.go\n+++ b/a.go\n")
	for i := 1; i <= 15; i++ {
		diff.WriteString(fmt.Sprintf("+line%d\n", i))
	}
	bl.ToolDiff = diff.String()
	bl.ToolResult = "Successfully replaced 1 block(s) in a.go."
	for _, expanded := range []bool{false, true} {
		got := stripANSI((Model{expandTools: expanded}).renderToolBody(bl))
		if !strings.Contains(got, "line1") || !strings.Contains(got, "line15") {
			t.Fatalf("edit expanded=%v should show full diff: %q", expanded, got)
		}
		if strings.Contains(got, "ctrl+g") {
			t.Fatalf("edit expanded=%v should not offer collapse: %q", expanded, got)
		}
	}

	// A result that IS a real diff outranks the args reconstruction: the
	// 2-line guess must never mask authoritative diff text.
	guess := Block{Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgs:    "b.go",
		ToolArgsRaw: `{"path":"b.go","edits":[{"oldText":"xxx","newText":"yyy"}]}`,
		ToolResult:  "--- a/b.go\n+++ b/b.go\n@@ -1,2 +1,2 @@\n-oldline\n+newline\n"}
	got := stripANSI((Model{}).renderToolBody(guess))
	if !strings.Contains(got, "@@ -1,2 +1,2 @@") || !strings.Contains(got, "newline") {
		t.Fatalf("real diff in ToolResult must outrank the args guess: %q", got)
	}
	if strings.Contains(got, "xxx") || strings.Contains(got, "yyy") {
		t.Fatalf("args reconstruction must not mask the real diff: %q", got)
	}

	// A JSON result is not diff text, so it still falls through to the guess.
	jsonRes := Block{Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgsRaw: `{"path":"c.go","edits":[{"oldText":"foo","newText":"bar"}]}`,
		ToolResult:  `{"replaced":1}`}
	if got := stripANSI((Model{}).renderToolBody(jsonRes)); !strings.Contains(got, "foo") {
		t.Fatalf("non-diff result must fall through to the args guess: %q", got)
	}
}

func TestExpandToggleKey(t *testing.T) {
	m := Model{}
	if m.expandTools {
		t.Fatal("expandTools should default off")
	}
	mdl, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	m2 := mdl.(Model)
	if !m2.expandTools {
		t.Fatal("Ctrl+G should enable expandTools")
	}
	mdl, _ = m2.Update(tea.KeyMsg{Type: tea.KeyCtrlG})
	if mdl.(Model).expandTools {
		t.Fatal("Ctrl+G again should disable expandTools")
	}
}

func TestRenderInputStatusTitle(t *testing.T) {
	newInputModel := func() Model {
		ta := textarea.New()
		ta.SetHeight(3)
		m := Model{ta: ta, winW: 100, hideSide: true, ModelLbl: "test"}
		m.ta.SetWidth(m.mainW() - 6)
		return m
	}
	m := newInputModel()
	m.thinking = true
	m.Status = "pi is running…"
	out := stripANSI(m.renderInput())
	top := strings.Split(out, "\n")[0]
	if !strings.HasPrefix(top, "╭─ ") || !strings.Contains(top, "pi is running…") || !strings.HasSuffix(top, "╮") {
		t.Fatalf("thinking input missing border status: %q", top)
	}
	// status lives in the border now, not duplicated in the footer
	if strings.Contains(out, "○ pi is running") {
		t.Fatalf("status duplicated in footer: %q", out)
	}
	// busy pet → live label (Working... + elapsed) with spinner, pi-style
	m.pet.status = petWorking
	m.pet.since = time.Now()
	out = stripANSI(m.renderInput())
	top = strings.Split(out, "\n")[0]
	if !strings.Contains(top, "Working...") {
		t.Fatalf("busy input should show live Working label: %q", top)
	}
	m.thinking = false
	out = stripANSI(m.renderInput())
	top = strings.Split(out, "\n")[0]
	if strings.Contains(top, "⋯") || strings.Contains(top, "running") {
		t.Fatalf("idle input should have a plain border: %q", top)
	}
	if !strings.HasPrefix(top, "╭") || !strings.HasSuffix(top, "╮") {
		t.Fatalf("idle input border broken: %q", top)
	}
}

// Regression (screenshot): a long model/stats line used to wrap the input
// footer to 2 rows, growing the left column past winH and leaving a gap
// under the top-aligned sidebar. The footer must stay one row so the
// frame stays exactly winH rows.
func TestInputFooterNeverWraps(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "cohere/north-mini-code: free"
	m.Stats = pirpc.Stats{ContextPct: 9, TokensTotal: 64800, Cost: 1.23}
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	rows := strings.Split(stripANSI(m.renderInput()), "\n")
	if len(rows) != 6 { // border 2 + textarea 3 + footer 1
		t.Fatalf("input box is %d rows, want 6:\n%q", len(rows), strings.Join(rows, "\n"))
	}
	if lines := strings.Split(m.View(), "\n"); len(lines) != 24 {
		t.Fatalf("frame is %d rows, want 24", len(lines))
	}
}

func TestRenderInputShowsAgent(t *testing.T) {
	m := New(nil, t.TempDir())
	m.Status = "ready"
	m.ModelLbl = "test"
	tm, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m = tm.(Model)
	m.CurAgent = "reviewer"
	// Idle: agent on the top border.
	top := strings.Split(stripANSI(m.renderInput()), "\n")[0]
	if !strings.Contains(top, "@reviewer") {
		t.Fatalf("idle input must show @agent on the border: %q", top)
	}
	// Footer stats carry it too.
	if !strings.Contains(stripANSI(m.renderInput()), "@reviewer") {
		t.Fatal("footer stats must show @agent")
	}
	// Running: agent next to the live status.
	m.thinking = true
	m.Status = "pi is running…"
	top = strings.Split(stripANSI(m.renderInput()), "\n")[0]
	if !strings.Contains(top, "pi is running") || !strings.Contains(top, "@reviewer") {
		t.Fatalf("running input must show status + agent: %q", top)
	}
}

// pi's edit tool always returns a one-line "Successfully replaced ..."
// receipt, so the change only lives in details.diff. That payload is
// display-oriented (gutter + -/+ rows + "..." elision) and must be rendered
// as-is, not through the chroma `diff` lexer.
const sampleEditDiff = "edit ~/Code/Workspace/pitago/src/app/view.go\n" +
	"...\n" +
	"     800      // refresh the picker\n" +
	"    - 801      // first, then re-count models (same order as before).\n" +
	"    - 802      m.RefreshLoginKeys(msg.D)\n" +
	"    + 801      // first, then re-count models. D is nil when\n" +
	"    + 802      // the OAuth guide was closed over no dialog.\n" +
	"    + 803      if msg.D != nil {\n" +
	"     803      m.Status = \"reloading models…\"\n" +
	"..."

func TestRenderToolBodyEditPrefersToolDiff(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"path": "src/app/view.go"})
	bl := Block{
		Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgs:    "src/app/view.go",
		ToolArgsRaw: string(raw),
		ToolResult:  "Successfully replaced 1 block(s) in src/app/view.go.",
		ToolDiff:    sampleEditDiff,
	}

	got := stripANSI((Model{}).renderToolBody(bl))
	if strings.Contains(got, "Successfully replaced") {
		t.Fatalf("edit must not render the receipt line: %q", got)
	}
	// Both markers present, so the change is visible rather than collapsed.
	if !strings.Contains(got, "- 802      m.RefreshLoginKeys(msg.D)") {
		t.Fatalf("removed line missing: %q", got)
	}
	if !strings.Contains(got, "+ 803      if msg.D != nil {") {
		t.Fatalf("added line missing: %q", got)
	}
	// Gutter and elision markers are pi's deliberate signals; keep both.
	if !strings.Contains(got, "800      // refresh the picker") {
		t.Fatalf("context row with line number missing: %q", got)
	}
	if strings.Count(got, "...") < 2 {
		t.Fatalf("leading/trailing elision markers dropped: %q", got)
	}
	// Full diff, never collapsed, and no collapse affordance.
	if strings.Contains(got, expandHint) {
		t.Fatalf("edit diff must not offer collapse: %q", got)
	}
}

// A diff long enough to be truncated when collapsed must still render whole.
func TestRenderToolBodyEditNeverCollapses(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("edit src/app/view.go\n")
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&sb, "    + %d      added line\n", i)
	}
	bl := Block{
		Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolResult: "Successfully replaced 1 block(s) in src/app/view.go.",
		ToolDiff:   sb.String(),
	}
	got := stripANSI((Model{}).renderToolBody(bl))
	if !strings.Contains(got, "+ 40      added line") {
		t.Fatalf("long diff must not be truncated: %q", got)
	}
	if strings.Contains(got, "more lines") {
		t.Fatalf("long diff must not report skipped lines: %q", got)
	}
}

// With no details.diff the old args-based reconstruction still works, and it
// keeps the chroma `diff` lexer (bare -/+ pair, no gutter).
func TestRenderToolBodyEditFallsBackToArgs(t *testing.T) {
	bl := Block{
		Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolArgsRaw: `{"path":"src/app/view.go","edits":[{"oldText":"old line\n","newText":"new line\n"}]}`,
		ToolResult:  "Successfully replaced 1 block(s) in src/app/view.go.",
	}
	got := stripANSI((Model{}).renderToolBody(bl))
	if strings.Contains(got, "Successfully replaced") {
		t.Fatalf("edit must not render the receipt line: %q", got)
	}
	if !strings.Contains(got, "- old line") || !strings.Contains(got, "+ new line") {
		t.Fatalf("args fallback must show -/+ pair: %q", got)
	}
}

// With no details.diff and no args the receipt is the last rung of the chain:
// better than an empty body, since it still reports what the tool did.
func TestRenderToolBodyEditReceiptOnly(t *testing.T) {
	bl := Block{
		Kind: "tool", ToolName: "edit", ToolStatus: "done",
		ToolResult: "Successfully replaced 1 block(s) in src/app/view.go.",
	}
	got := stripANSI((Model{}).renderToolBody(bl))
	if !strings.Contains(got, "Successfully replaced") {
		t.Fatalf("receipt-only edit should fall back to the receipt: %q", got)
	}
}

// oh-my-pi look: a tool block is never a full-bleed Background fill
// (terminals without truecolor drop Background and used to leave flat raw
// rows). A shell call is one flush-left framed box — command, divider,
// A tool block is a bordered box with a background fill, pi style: the
// frame replaces the old full-bleed Background() so a terminal that
// drops the fill still shows a clean outline. A shell call frames
// command + output in one box, flush left, with no status bullet: the
// border color carries the status instead. Every other tool keeps the
// bullet + bold name header (now inside the frame) over a tree body.
func TestToolBlockRestyle(t *testing.T) {
	bash := Block{Kind: "tool", ToolName: "bash", ToolStatus: "done",
		ToolArgs: "go test ./src/app/", ToolResult: "ok  \tpitago/src/app\t3.4s\nFAIL"}
	read := Block{Kind: "tool", ToolName: "read", ToolStatus: "done",
		ToolArgs: "game.js", ToolArgsRaw: `{"path":"game.js"}`,
		ToolResult: "const a = 1;\nconst b = 2;\nconst c = 3;"}
	// Both kinds render inside a closed frame, flush left, whatever the
	// tool — the frame is now the block's left edge, so no gutter bullet
	// may push the border past its column.
	for _, bl := range []Block{bash, read} {
		m := Model{blocks: []Block{bl}}
		m.vp = viewport.New(62, 20) // cw = 60
		rows := strings.Split(strings.TrimRight(stripANSI(m.renderBlocks()), "\n"), "\n")
		if !strings.HasPrefix(rows[0], "╭─") {
			t.Fatalf("%s block must open the frame: %q", bl.ToolName, rows[0])
		}
		if !strings.HasPrefix(rows[len(rows)-1], "╰─") {
			t.Fatalf("%s block must close the frame: %q", bl.ToolName, rows[len(rows)-1])
		}
	}

	// A shell call frames command + output in one box, flush left, with no
	// status bullet: the border color carries the status instead.
	bm := Model{blocks: []Block{bash}, vp: viewport.New(62, 20)}
	plain := stripANSI(bm.renderBlocks())
	if strings.HasPrefix(plain, "●") {
		t.Fatalf("shell box must be flush left with no bullet: %q", plain)
	}
	if !strings.HasPrefix(plain, "╭─") {
		t.Fatalf("shell block must open the frame immediately: %q", plain)
	}
	if !strings.Contains(plain, "│ $ go test ./src/app/ ") {
		t.Fatalf("command must sit inside the box: %q", plain)
	}
	if !strings.Contains(plain, "│ ─── Output ") {
		t.Fatalf("missing inline Output divider: %q", plain)
	}
	if !strings.HasSuffix(strings.TrimRight(plain, "\n"), strings.Repeat("╰", 1)+strings.Repeat("─", 58)+"╯") {
		t.Fatalf("shell box missing bottom border: %q", plain)
	}
	for _, row := range strings.Split(plain, "\n") {
		if strings.HasPrefix(row, "╭") || strings.HasPrefix(row, "│") || strings.HasPrefix(row, "╰") {
			if lipgloss.Width(row) != 60 {
				t.Fatalf("box row is %d cells, want 60: %q", lipgloss.Width(row), row)
			}
		}
	}
	if toolBorder("error") != cRed || toolBorder("done") != cBorder {
		t.Fatalf("border must carry the status the bullet gave up")
	}

	// Every other tool is framed too, with the status bullet + bold name
	// as the header row inside the box.
	rm0 := Model{blocks: []Block{read}, vp: viewport.New(62, 20)}
	plain = stripANSI(rm0.renderBlocks())
	rows := strings.Split(strings.TrimRight(plain, "\n"), "\n")
	if !strings.HasPrefix(rows[0], "╭─") {
		t.Fatalf("read block must open the frame: %q", plain)
	}
	if !strings.Contains(rows[1], "● read ") {
		t.Fatalf("read block header = %q, want \"● read …\" inside the box", rows[1])
	}
	// Styling is dropped when no color profile is set (the case this
	// restyle targets), so assert the style itself, not its escape.
	if !toolNameStyle.GetBold() {
		t.Fatalf("toolNameStyle must render the tool name bold")
	}

	// a multi-line generic result renders as a box-drawing tree
	rm := Model{expandTools: true, blocks: []Block{read}, vp: viewport.New(62, 20)}
	plain = stripANSI(rm.renderBlocks())
	if !strings.Contains(plain, "├── const a = 1;") {
		t.Fatalf("multi-line result missing ├─ tree row: %q", plain)
	}
	if !strings.Contains(plain, "└── const c = 3;") {
		t.Fatalf("last result row missing └─ prefix: %q", plain)
	}
}

func TestRenderResultRows(t *testing.T) {
	// Tree rows carry no indent of their own: gutterBox is the single
	// source of the 2-cell block indent, so indenting here too would push
	// the tree two cells past the shell box and the header text.
	got := renderResultRows([]string{"a", "b", "c"}, lipgloss.NewStyle(), true)
	want := "├── a\n├── b\n└── c"
	if got != want {
		t.Fatalf("tree rows = %q, want %q", got, want)
	}
	if one := renderResultRows([]string{"solo"}, lipgloss.NewStyle(), true); one != "└── solo" {
		t.Fatalf("single-row tree = %q", one)
	}
	if blank := renderResultRows([]string{"a", "", "c"}, lipgloss.NewStyle(), true); blank != "├── a\n\n└── c" {
		t.Fatalf("blank row must stay blank: %q", blank)
	}
	// tree=false is the diff/error path: flat rows, no branch glyphs.
	if flat := renderResultRows([]string{"- old", "+ new"}, toolStyle, false); flat != "- old\n+ new" {
		t.Fatalf("flat rows = %q, want no glyphs", flat)
	}
}

// The labelled divider has to fill the framed content exactly, or the
// right border of the shell box comes out ragged. Narrow widths are the
// case that breaks, since the label alone can outgrow the inner column.
func TestShellBoxDividerFillsInnerWidth(t *testing.T) {
	for _, w := range []int{8, 20, 24, 60, 94, 200} {
		bl := Block{Kind: "tool", ToolName: "bash", ToolStatus: "done",
			ToolArgs: "ls src", ToolResult: "a\nb"}
		for _, row := range strings.Split((Model{}).renderShellBlock(bl, w), "\n") {
			// lipgloss.Width is ANSI-aware; StripANSI is not, and it eats
			// the padding that this measurement is about.
			if got := lipgloss.Width(row); got != max(w, 20) {
				t.Fatalf("w=%d: shell box row is %d cells: %q", w, got, row)
			}
		}
	}
	// A label wider than the inner column must not push the right border out.
	if d := dividerRow(4, "Output"); lipgloss.Width(d) != 11 {
		t.Fatalf("over-wide label must stay un-wrapped, got %d cells: %q",
			lipgloss.Width(d), stripANSI(d))
	}
}

func TestToolDetail(t *testing.T) {
	if got := toolDetail(Block{ToolStatus: "running"}); got != "running…" {
		t.Errorf("running detail = %q", got)
	}
	if got := toolDetail(Block{ToolStatus: "done", ToolResult: "ok"}); got != "" {
		t.Errorf("done-with-output detail = %q", got)
	}
	if got := toolDetail(Block{ToolStatus: "done"}); got != "no output" {
		t.Errorf("silent done detail = %q", got)
	}
	if got := toolDetail(Block{ToolStatus: "error", ToolResult: "  "}); got != "no output" {
		t.Errorf("blank result detail = %q", got)
	}
}
