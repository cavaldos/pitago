package sessiontree

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"pitago/src/pirpc"
)

func sp(s string) *string { return &s }

func msg(id, parent, role, text string) pirpc.TreeNode {
	var p *string
	if parent != "" {
		p = sp(parent)
	}
	c, _ := json.Marshal([]map[string]string{{"type": "text", "text": text}})
	return pirpc.TreeNode{Entry: pirpc.TreeEntry{Type: "message", ID: id, ParentID: p,
		Message: pirpc.AgentMessage{Role: role, Content: c, StopReason: "stop"}}}
}

func with(n pirpc.TreeNode, kids ...pirpc.TreeNode) pirpc.TreeNode {
	n.Children = kids
	return n
}

// plain renders the list without styles.
func plain(l *List, width int) []string {
	var out []string
	for _, ln := range l.Render(width) {
		var b strings.Builder
		for _, s := range ln {
			b.WriteString(s.Text)
		}
		out = append(out, b.String())
	}
	return out
}

// branched: u1 → a1 → {u2 → a2 (old branch), u3 → a3 (active)}.
func branched() []pirpc.TreeNode {
	return []pirpc.TreeNode{with(msg("u1", "", "user", "hello"),
		with(msg("a1", "u1", "assistant", "hi"),
			with(msg("u2", "a1", "user", "old question"), msg("a2", "u2", "assistant", "old answer")),
			with(msg("u3", "a1", "user", "new question"), msg("a3", "u3", "assistant", "new answer")),
		))}
}

func TestRenderMatchesPiLayout(t *testing.T) {
	l := New(branched(), "a3", 10, "", "default")
	got := plain(l, 80)
	// Verbatim output of pi 1.4.0's TreeList for the same tree: active
	// branch first, ⊟ on foldable branch starts, selection on the leaf.
	want := []string{
		"  • user: hello",
		"  • assistant: hi",
		"  ├⊟ • user: new question",
		"› │     • assistant: new answer",
		"  └⊟ user: old question",
		"        assistant: old answer",
		"  (4/6)",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestFoldAndBranchJumps(t *testing.T) {
	l := New(branched(), "a3", 10, "u2", "default")
	if l.Selected().ID() != "u2" {
		t.Fatalf("initial selection = %s", l.Selected().ID())
	}
	l.FoldOrUp() // u2 starts a branch: fold it
	got := plain(l, 80)
	if !strings.Contains(strings.Join(got, "\n"), "└⊞ user: old question") || l.Len() != 5 {
		t.Fatalf("fold failed:\n%s", strings.Join(got, "\n"))
	}
	l.UnfoldOrDown()
	if l.Len() != 6 {
		t.Fatalf("unfold failed, %d rows", l.Len())
	}
	l.Select(0)
	l.UnfoldOrDown() // down to the next branch start
	if l.Selected().ID() != "u3" {
		t.Fatalf("branch down landed on %s", l.Selected().ID())
	}
	l.Select(3)  // a3
	l.FoldOrUp() // a3 is not foldable: up to its segment start
	if l.Selected().ID() != "u3" {
		t.Fatalf("branch up landed on %s", l.Selected().ID())
	}
}

func TestFiltersAndSearch(t *testing.T) {
	tool := msg("a0", "u1", "assistant", "")
	tool.Entry.Message.Content = json.RawMessage(`[{"type":"toolCall","id":"c1","name":"read","arguments":{"path":"/x/f.go","offset":3,"limit":2}}]`)
	tool.Entry.Message.StopReason = "toolUse"
	res := pirpc.TreeNode{Entry: pirpc.TreeEntry{Type: "message", ID: "r1", ParentID: sp("a0"),
		Message: pirpc.AgentMessage{Role: "toolResult", ToolCallID: "c1"}}}
	model := pirpc.TreeNode{Entry: pirpc.TreeEntry{Type: "model_change", ID: "m1", ParentID: sp("r1"), ModelID: "gpt"}}
	roots := []pirpc.TreeNode{with(msg("u1", "", "user", "find bug"), with(tool, with(res, with(model, msg("a1", "m1", "assistant", "done")))))}
	l := New(roots, "a1", 10, "", "default")
	l.Home = "/x"
	got := strings.Join(plain(l, 80), "\n")
	if strings.Contains(got, "a0") || !strings.Contains(got, "[read: ~/f.go:3-4]") || strings.Contains(got, "[model:") {
		t.Fatalf("default view wrong:\n%s", got)
	}
	l.ToggleMode("no-tools")
	if strings.Contains(strings.Join(plain(l, 80), "\n"), "[read") {
		t.Fatal("no-tools still shows tool results")
	}
	l.ToggleMode("all")
	if got := strings.Join(plain(l, 80), "\n"); !strings.Contains(got, "[model: gpt]") || !strings.Contains(got, "[all]") {
		t.Fatalf("all view wrong:\n%s", got)
	}
	l.CycleMode(1) // all → default
	if l.Mode() != "default" {
		t.Fatalf("cycle from all = %s", l.Mode())
	}
	l.Type("DONE")
	if l.Len() != 1 || l.Selected().ID() != "a1" {
		t.Fatalf("search kept %d rows", l.Len())
	}
	if !l.Cancel() || l.Query() != "" || l.Cancel() {
		t.Fatal("Esc must clear the search first, then report close")
	}
}

func TestLabelsAndCopy(t *testing.T) {
	l := New(branched(), "a3", 10, "", "default")
	l.Now = func() time.Time { return time.Date(2026, 10, 26, 9, 0, 0, 0, time.Local) }
	l.UpdateNodeLabel("u3", "fix", "")
	l.ToggleLabelTimestamps()
	got := strings.Join(plain(l, 80), "\n")
	if !strings.Contains(got, "• [fix] 09:00 user: new question") || !strings.Contains(got, "[+label time]") {
		t.Fatalf("label not drawn:\n%s", got)
	}
	l.SetMode("labeled-only")
	if l.Len() != 1 {
		t.Fatalf("labeled-only kept %d rows", l.Len())
	}
	if CopyText(l.Entry("u3")) != "new question" || EditorText(l.Entry("u3")) != "new question" || EditorText(l.Entry("a3")) != "" {
		t.Fatal("copy/editor text wrong")
	}
}

func TestPagingAndWrap(t *testing.T) {
	var kids []pirpc.TreeNode
	n := msg("u0", "", "user", "q0")
	cur := &n
	for i := 1; i < 20; i++ {
		c := msg("u"+string(rune('a'+i)), cur.Entry.ID, "user", "q")
		cur.Children = []pirpc.TreeNode{c}
		cur = &cur.Children[0]
	}
	_ = kids
	l := New([]pirpc.TreeNode{n}, "", 5, "", "default")
	if l.SelectedIndex() != 19 {
		t.Fatalf("no leaf selects the last row, got %d", l.SelectedIndex())
	}
	l.Down()
	if l.SelectedIndex() != 0 {
		t.Fatal("down from the last row wraps")
	}
	l.PageDown()
	if l.SelectedIndex() != 5 {
		t.Fatalf("page down = %d", l.SelectedIndex())
	}
	if s, e := l.VisibleRange(); s != 3 || e != 8 {
		t.Fatalf("window = %d..%d, want centred 3..8", s, e)
	}
}
