package app

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// isolatePiSettings points pi's settings at an empty agent dir, so a
// developer's own branchSummary/doubleEscape settings cannot steer a test.
func isolatePiSettings(t *testing.T, settings string) {
	t.Helper()
	dir := t.TempDir()
	if settings != "" {
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PI_CODING_AGENT_DIR", dir)
}

func treeMsgNode(id, parent, role, text string, kids ...pirpc.TreeNode) pirpc.TreeNode {
	var p *string
	if parent != "" {
		p = &parent
	}
	c, _ := json.Marshal(text)
	return pirpc.TreeNode{Entry: pirpc.TreeEntry{Type: "message", ID: id, ParentID: p,
		Message: pirpc.AgentMessage{Role: role, Content: c, StopReason: "stop"}}, Children: kids}
}

// u1 → a1 → {u2 → a2 (leaf), u3}
func testTree() TreeMsg {
	return TreeMsg{Leaf: "a2", Mode: "default", Nodes: []pirpc.TreeNode{
		treeMsgNode("u1", "", "user", "hello", treeMsgNode("a1", "u1", "assistant", "hi",
			treeMsgNode("u2", "a1", "user", "current", treeMsgNode("a2", "u2", "assistant", "answer")),
			treeMsgNode("u3", "a1", "user", "other branch"))),
	}}
}

// treeTestModel is a sized, painted model with no pi behind it.
func treeTestModel(t *testing.T) Model {
	t.Helper()
	um, _ := New(nil, t.TempDir()).Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	m := um.(Model)
	m.ready = true
	return m
}

func openTestTree(t *testing.T) Model {
	t.Helper()
	m := treeTestModel(t)
	um, _ := m.Update(testTree())
	m = um.(Model)
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != treeKind {
		t.Fatalf("TreeMsg did not open the tree: %+v", m.Dialogs)
	}
	return m
}

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }
func runes(s string) tea.KeyMsg    { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func press(m Model, keys ...tea.KeyMsg) (Model, tea.Cmd) {
	var cmd tea.Cmd
	for _, k := range keys {
		var um tea.Model
		um, cmd = m.Update(k)
		m = um.(Model)
	}
	return m, cmd
}

func TestTreeScreenIsPiSelectorAtTheBottom(t *testing.T) {
	m := openTestTree(t)
	got := strings.Split(stripANSI(m.View()), "\n")
	text := strings.Join(got, "\n")
	for _, want := range []string{"   Session Tree", "  ↑/↓ move · ←/→ page", "ctrl+x copy", "shift+l label",
		"filters ctrl+d/t/u/l/a", "  Type to search:", "├⊟ • user: current", "› │     • assistant: answer", "  (4/5)"} {
		if !strings.Contains(text, want) {
			t.Errorf("tree screen missing %q:\n%s", want, text)
		}
	}
	// pi's selector ends with a border on the last line it draws.
	last := ""
	for i := len(got) - 1; i >= 0; i-- {
		if strings.TrimSpace(got[i]) != "" {
			last = strings.TrimSpace(got[i])
			break
		}
	}
	if !strings.HasPrefix(last, "──") {
		t.Errorf("selector is not pinned to the bottom, last line %q", last)
	}
}

func TestTreeEnterOnLeafIsANoop(t *testing.T) {
	m := openTestTree(t)
	m, cmd := press(m, key(tea.KeyEnter))
	if len(m.Dialogs) != 0 || cmd != nil || lastNotice(m) != "Already at this point" {
		t.Fatalf("Enter on the leaf: dialogs=%d cmd=%v notice=%q", len(m.Dialogs), cmd != nil, lastNotice(m))
	}
}

func TestTreeEnterAsksSummarizeThenEscReopens(t *testing.T) {
	isolatePiSettings(t, "")
	m := openTestTree(t)
	m, _ = press(m, key(tea.KeyDown), key(tea.KeyEnter)) // u3
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != treeSummaryKind {
		t.Fatalf("want the summary selector, got %+v", m.Dialogs)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"Summarize branch?", "→ No summary", "Summarize with custom prompt", "↑↓ navigate  enter select  escape/ctrl+c cancel"} {
		if !strings.Contains(view, want) {
			t.Errorf("summary selector missing %q:\n%s", want, view)
		}
	}
	m, cmd := press(m, key(tea.KeyEsc))
	if len(m.Dialogs) != 0 || cmd == nil {
		t.Fatal("Esc must close the selector and reload the tree")
	}
}

func TestTreeSkipPromptNavigatesStraightAway(t *testing.T) {
	isolatePiSettings(t, `{"branchSummary":{"skipPrompt":true}}`)
	m := openTestTree(t)
	m, cmd := press(m, key(tea.KeyDown), key(tea.KeyEnter))
	if len(m.Dialogs) != 0 || cmd == nil || m.branchSummarizing {
		t.Fatalf("skipPrompt must navigate without a summary: dialogs=%d", len(m.Dialogs))
	}
}

func TestTreeCustomPromptFlow(t *testing.T) {
	isolatePiSettings(t, "")
	m := openTestTree(t)
	m, _ = press(m, key(tea.KeyDown), key(tea.KeyEnter), key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter))
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != treeCustomKind {
		t.Fatalf("want the instructions editor, got %+v", m.Dialogs)
	}
	m, _ = press(m, runes("keep"), key(tea.KeyCtrlJ), runes("it short"))
	if v := m.Dialogs[0].tree.edit.value(); v != "keep\nit short" {
		t.Fatalf("editor value %q", v)
	}
	// Esc goes back to a fresh selector, not to the tree.
	m, _ = press(m, key(tea.KeyEsc))
	if len(m.Dialogs) != 1 || m.Dialogs[0].Kind != treeSummaryKind || m.Dialogs[0].Cursor != 0 {
		t.Fatalf("Esc from the editor: %+v", m.Dialogs)
	}
	m, _ = press(m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter), runes("why"))
	m, cmd := press(m, key(tea.KeyEnter))
	if len(m.Dialogs) != 0 || cmd == nil || !m.branchSummarizing {
		t.Fatal("submitting the instructions must start a summarizing navigation")
	}
	if title := stripANSI(m.renderInput()); !strings.Contains(title, branchSummaryLabel) {
		t.Errorf("input border lacks the summary indicator:\n%s", title)
	}
	// Esc while summarizing aborts the summary.
	m, cmd = press(m, key(tea.KeyEsc))
	if !m.summaryAborted || cmd == nil {
		t.Fatal("Esc must abort the branch summary")
	}
	um, cmd := m.Update(treeNavMsg{nav: treeNav{target: "u3"}, summarize: true, res: pirpc.TreeNavResult{Cancelled: true}})
	m = um.(Model)
	if lastNotice(m) != "Branch summarization cancelled" || cmd == nil || m.branchSummarizing {
		t.Fatalf("aborted summary: notice %q", lastNotice(m))
	}
}

func TestTreeNavResults(t *testing.T) {
	m := openTestTree(t)
	m.Dialogs = nil
	m.ta.SetValue("")
	um, cmd := m.Update(treeNavMsg{nav: treeNav{target: "u3", editorText: "other branch"}})
	m = um.(Model)
	if m.ta.Value() != "other branch" || cmd == nil {
		t.Fatalf("success must reload and refill the editor, got %q", m.ta.Value())
	}
	m.ta.SetValue("draft")
	um, _ = m.Update(treeNavMsg{nav: treeNav{target: "u3", editorText: "other branch"}})
	if um.(Model).ta.Value() != "draft" {
		t.Fatal("a non-empty editor must be left alone")
	}
	um, _ = m.Update(treeNavMsg{res: pirpc.TreeNavResult{Cancelled: true}})
	if lastNotice(um.(Model)) != "Navigation cancelled" {
		t.Fatal("cancelled navigation not reported")
	}
	um, _ = m.Update(treeNavMsg{err: errors.New("boom")})
	if lastNotice(um.(Model)) != "boom" {
		t.Fatal("error not reported")
	}
}

func TestTreeLabelEditAndCopy(t *testing.T) {
	got := stubClip(t, nil)
	m := openTestTree(t)
	m, _ = press(m, runes("L"))
	if !strings.Contains(stripANSI(m.View()), "Label (empty to remove):") {
		t.Fatal("Shift+L must open the label input")
	}
	m, cmd := press(m, runes("done"), key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("saving a label must send it to pi")
	}
	if n := m.Dialogs[0].tree.list.Entry("a2"); n.Label != "done" || n.LabelTimestamp == "" {
		t.Fatalf("label not applied: %+v", n)
	}
	if !strings.Contains(stripANSI(m.View()), "[done] assistant: answer") {
		t.Errorf("label not drawn:\n%s", stripANSI(m.View()))
	}
	m, _ = press(m, key(tea.KeyCtrlX))
	if len(*got) != 1 || (*got)[0] != "answer" || lastNotice(m) != "Copied selected message to clipboard" {
		t.Fatalf("copy: clip=%q notice=%q", *got, lastNotice(m))
	}
}

func TestTreeEscClearsSearchThenCloses(t *testing.T) {
	m := openTestTree(t)
	m, _ = press(m, runes("oth"))
	if q := m.Dialogs[0].tree.list.Query(); q != "oth" || m.Dialogs[0].tree.list.Len() != 1 {
		t.Fatalf("search %q kept %d rows", q, m.Dialogs[0].tree.list.Len())
	}
	m, _ = press(m, key(tea.KeyEsc))
	if len(m.Dialogs) != 1 || m.Dialogs[0].tree.list.Query() != "" {
		t.Fatal("first Esc must only clear the search")
	}
	m, _ = press(m, key(tea.KeyEsc))
	if len(m.Dialogs) != 0 {
		t.Fatal("second Esc must close the tree")
	}
}

func TestTreeClickSelectsThenConfirms(t *testing.T) {
	m := openTestTree(t)
	d := m.Dialogs[0]
	lines, listTop := m.treeLines(d)
	_, top := m.renderBottomPanel(lines)
	nm, _, ok := m.clickTreeDialog(d, tea.MouseMsg{X: 4, Y: top + listTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if !ok || d.tree.list.SelectedIndex() != 0 {
		t.Fatalf("click did not select row 0: %d", d.tree.list.SelectedIndex())
	}
	m = nm.(Model)
	nm, _, _ = m.clickTreeDialog(d, tea.MouseMsg{X: 4, Y: top + listTop, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
	if len(nm.(Model).Dialogs) == 0 || nm.(Model).Dialogs[0].Kind != treeSummaryKind {
		t.Fatal("a click on the selected row must confirm it")
	}
}

func TestDoubleEscapeOpensTree(t *testing.T) {
	isolatePiSettings(t, "")
	ran := []string{}
	m := treeTestModel(t)
	m.builtins = []Builtin{{Name: "tree", Run: func(*Model, string) tea.Cmd { ran = append(ran, "tree"); return nil }}}
	m, _ = press(m, key(tea.KeyEsc))
	if len(ran) != 0 {
		t.Fatal("one Esc must not open the tree")
	}
	m, _ = press(m, key(tea.KeyEsc))
	if len(ran) != 1 {
		t.Fatal("a second Esc within 500ms must open the tree")
	}
	m.lastEsc = time.Now().Add(-time.Second)
	m, _ = press(m, key(tea.KeyEsc))
	if len(ran) != 1 {
		t.Fatal("a slow second Esc must not open the tree")
	}
	isolatePiSettings(t, `{"doubleEscapeAction":"none"}`)
	m, _ = press(m, key(tea.KeyEsc), key(tea.KeyEsc))
	if len(ran) != 1 {
		t.Fatal(`doubleEscapeAction "none" must disable it`)
	}
}
