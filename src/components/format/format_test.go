package format

import (
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestPrettyArgs(t *testing.T) {
	cases := []struct {
		tool, raw, want string
	}{
		{"bash", `{"command":"ls -la"}`, "ls -la"},
		{"bash", `{"command":"ls","timeout":30}`, "ls (timeout 30s)"},
		{"read", `{"path":"README.md"}`, "README.md"},
		{"read", `{"path":"a.go","offset":10,"limit":5}`, "a.go:10-14"},
		{"edit", `{"path":"foo.go","edits":[]}`, "foo.go"},
		{"write", `{"path":"foo.go","content":"x"}`, "foo.go"},
		{"ls", `{}`, "."},
		{"find", `{"pattern":"*.go","path":"src"}`, "*.go in src"},
		{"grep", `{"pattern":"foo","path":"."}`, "/foo/ in ."},
		{"custom", `{"foo":"bar"}`, `{"foo":"bar"}`},
		{"bash", ``, ""},
	}
	for _, c := range cases {
		if got := PrettyArgs(c.tool, c.raw); got != c.want {
			t.Errorf("PrettyArgs(%q,%q)=%q want %q", c.tool, c.raw, got, c.want)
		}
	}
}

func TestToolResultPreview(t *testing.T) {
	// bash tails 5 lines
	p := ToolResultPreview("bash", "done", "l1\nl2\nl3\nl4\nl5\nl6\nl7")
	if p.Hidden || !p.Tail || p.Skipped != 2 || len(p.Lines) != 5 || p.Lines[0] != "l3" {
		t.Errorf("bash tail wrong: %+v", p)
	}
	// short bash shows all
	p = ToolResultPreview("bash", "done", "a\nb")
	if p.Hidden || p.Skipped != 0 || len(p.Lines) != 2 {
		t.Errorf("bash short wrong: %+v", p)
	}
	// read hidden on success, shown on error
	if p = ToolResultPreview("read", "done", "content"); !p.Hidden {
		t.Errorf("read success should hide: %+v", p)
	}
	if p = ToolResultPreview("read", "error", "boom"); p.Hidden || len(p.Lines) != 1 {
		t.Errorf("read error should show: %+v", p)
	}
	if p = ToolResultPreview("write", "done", "ok"); !p.Hidden {
		t.Errorf("write success should hide: %+v", p)
	}
	// grep heads 15 lines
	long := ""
	for i := 0; i < 20; i++ {
		long += "m\n"
	}
	p = ToolResultPreview("grep", "done", long)
	if p.Tail || p.Skipped != 5 || len(p.Lines) != 15 {
		t.Errorf("grep head wrong: skipped=%d lines=%d tail=%v", p.Skipped, len(p.Lines), p.Tail)
	}
}

func TestToolResultPreviewExpanded(t *testing.T) {
	// read success: hidden collapsed, full when expanded (pi parity)
	if p := ToolResultPreviewExpanded("read", "done", "a\nb", false); !p.Hidden {
		t.Errorf("read collapsed should hide: %+v", p)
	}
	if p := ToolResultPreviewExpanded("read", "done", "a\nb", true); p.Hidden || len(p.Lines) != 2 {
		t.Errorf("read expanded should show all: %+v", p)
	}
	// bash expanded: all 7 lines, no skip
	p := ToolResultPreviewExpanded("bash", "done", "l1\nl2\nl3\nl4\nl5\nl6\nl7", true)
	if p.Hidden || p.Skipped != 0 || len(p.Lines) != 7 || !p.Tail {
		t.Errorf("bash expanded wrong: %+v", p)
	}
	// write/edit success stay hidden (content renders from call args)
	if p := ToolResultPreviewExpanded("write", "done", "ok", true); !p.Hidden {
		t.Errorf("write expanded result should stay hidden: %+v", p)
	}
	if p := ToolResultPreviewExpanded("edit", "done", "ok", false); !p.Hidden {
		t.Errorf("edit success result should hide: %+v", p)
	}
	if p := ToolResultPreviewExpanded("edit", "error", "boom", false); p.Hidden || len(p.Lines) != 1 {
		t.Errorf("edit error should show: %+v", p)
	}
	// collapsed wrapper matches expanded=false
	if p := ToolResultPreview("read", "done", "x"); !p.Hidden {
		t.Errorf("wrapper should collapse: %+v", p)
	}
}

func TestCallPreview(t *testing.T) {
	// short content: all lines, no skip
	p := CallPreview("a\nb\n", false)
	if p.Hidden || p.Skipped != 0 || len(p.Lines) != 2 || p.Total != 2 {
		t.Errorf("short call preview wrong: %+v", p)
	}
	// 12 lines collapsed: 10 shown, 2 skipped, total kept for the hint
	long := ""
	for i := 0; i < 12; i++ {
		long += "line\n"
	}
	p = CallPreview(long, false)
	if p.Hidden || len(p.Lines) != 10 || p.Skipped != 2 || p.Total != 12 {
		t.Errorf("collapsed call preview wrong: %+v", p)
	}
	// expanded: all 12
	p = CallPreview(long, true)
	if p.Hidden || len(p.Lines) != 12 || p.Skipped != 0 || p.Total != 12 {
		t.Errorf("expanded call preview wrong: %+v", p)
	}
	if p := CallPreview("  \n\n", false); !p.Hidden {
		t.Errorf("blank content should hide: %+v", p)
	}
}

func TestWriteContentArgPathLang(t *testing.T) {
	raw := `{"path":"game.js","content":"const a = 1;\n"}`
	c, ok := WriteContent(raw)
	if !ok || c != "const a = 1;\n" {
		t.Errorf("WriteContent = %q, %v", c, ok)
	}
	if _, ok := WriteContent(`{"path":"a.js"}`); ok {
		t.Errorf("WriteContent without content should fail")
	}
	if p := ArgPath(raw); p != "game.js" {
		t.Errorf("ArgPath = %q", p)
	}
	if p := ArgPath(`{"file_path":"src/a.go","offset":10}`); p != "src/a.go" {
		t.Errorf("ArgPath file_path = %q", p)
	}
	if got := LangFromPath("game.js"); got != "javascript" {
		t.Errorf("LangFromPath js = %q", got)
	}
	if got := LangFromPath("main.go"); got != "go" {
		t.Errorf("LangFromPath go = %q", got)
	}
	if got := LangFromPath("noext"); got != "" {
		t.Errorf("LangFromPath noext = %q", got)
	}
	if got := LangFromPath("file.unknownext"); got != "" {
		t.Errorf("LangFromPath unknown = %q", got)
	}
}

func TestEditDiffFallback(t *testing.T) {
	raw := `{"path":"a.go","edits":[{"oldText":"foo","newText":"bar"}]}`
	got := EditDiffFallback(raw)
	if got != "- foo\n+ bar" {
		t.Errorf("EditDiffFallback = %q", got)
	}
	if got := EditDiffFallback(`{"path":"a.go"}`); got != "" {
		t.Errorf("EditDiffFallback without edits = %q", got)
	}
	if got := CollapsedBudget("bash"); got != 5 {
		t.Errorf("budget bash = %d", got)
	}
	if got := CollapsedBudget("write"); got != 10 {
		t.Errorf("budget write = %d", got)
	}
}

func TestWriteDiffFallback(t *testing.T) {
	raw := `{"path":"keys_test.go","content":"package main\n\nfunc main() {}\n"}`
	got := WriteDiffFallback(raw)
	if got != "+ package main\n+ func main() {}" {
		t.Errorf("WriteDiffFallback = %q", got)
	}
	if got := WriteDiffFallback(`{"path":"a.go"}`); got != "" {
		t.Errorf("WriteDiffFallback without content = %q", got)
	}
	if got := WriteDiffFallback(`{"content":"  \n"}`); got != "" {
		t.Errorf("WriteDiffFallback blank content = %q", got)
	}
}

func TestShortNeverExceedsWidth(t *testing.T) {
	cases := []struct {
		s string
		n int
	}{
		{"Xiaomi Token Plan (Singapore)", 20},
		{"ZAI Coding Plan (China)", 10},
		{"short", 20},
		{"exact-twenty-chars!!", 20},
		{"héllo wörld ünïcodé", 10}, // multi-byte: no split rune, no overflow
		{"abc", 1},
		{"abc", 0},
	}
	for _, c := range cases {
		got := Short(c.s, c.n)
		if w := lipgloss.Width(got); c.n >= 1 && w > c.n {
			t.Fatalf("Short(%q, %d) = %q (width %d)", c.s, c.n, got, w)
		}
		if c.n >= 1 && lipgloss.Width(c.s) > c.n && !containsEllipsis(got) {
			t.Fatalf("Short(%q, %d) = %q, want …", c.s, c.n, got)
		}
	}
}

func TestFitExactWidth(t *testing.T) {
	for _, w := range []int{5, 20, 40} {
		for _, s := range []string{"", "ab", "Xiaomi Token Plan (Singapore) no key", "héllo wörld"} {
			got := Fit(s, w)
			if gw := lipgloss.Width(got); gw != w {
				t.Fatalf("Fit(%q, %d) width = %d", s, w, gw)
			}
		}
	}
	if Fit("abc", 0) != "" {
		t.Fatal("Fit w=0 must be empty")
	}
}

func containsEllipsis(s string) bool {
	for _, r := range s {
		if r == '…' {
			return true
		}
	}
	return false
}
