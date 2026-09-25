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
			if !strings.Contains(oneLine, "└ No files found matching pattern") {
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

	// details.diff from pi stays complete in either global expand state.
	var diff strings.Builder
	diff.WriteString("--- a/a.go\n+++ b/a.go\n")
	for i := 1; i <= 15; i++ {
		diff.WriteString(fmt.Sprintf("+line%d\n", i))
	}
	bl.ToolResult = diff.String()
	for _, expanded := range []bool{false, true} {
		got := stripANSI((Model{expandTools: expanded}).renderToolBody(bl))
		if !strings.Contains(got, "line1") || !strings.Contains(got, "line15") {
			t.Fatalf("edit expanded=%v should show full diff: %q", expanded, got)
		}
		if strings.Contains(got, "ctrl+g") {
			t.Fatalf("edit expanded=%v should not offer collapse: %q", expanded, got)
		}
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
