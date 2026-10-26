package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"pitago/src/pirpc"
)

// pi's real get_tree payload uses thinkingLevel (camelCase) — the old
// struct read `level` and rendered every row as "thinking → ?".
func TestRenderTreePiPayload(t *testing.T) {
	raw := `{"tree":[{"entry":{"type":"model_change","id":"d61f02ff","parentId":null,"timestamp":"2026-09-21T11:07:21.636Z","provider":"zai","modelId":"glm-5.3"},"children":[{"entry":{"type":"thinking_level_change","id":"57b85595","parentId":"d61f02ff","timestamp":"2026-09-21T11:07:21.636Z","thinkingLevel":"high"},"children":[]}]}],"leafId":"57b85595"}`
	var data struct {
		Tree   []pirpc.TreeNode `json:"tree"`
		LeafID string           `json:"leafId"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		t.Fatal(err)
	}
	got := renderTree(data.Tree, data.LeafID, "all")
	want := "└── • [model: glm-5.3]\n    └── • [thinking: high]"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func msgEntry(id, parent, role, content string) pirpc.TreeEntry {
	e := pirpc.TreeEntry{Type: "message", ID: id}
	if parent != "" {
		e.ParentID = &parent
	}
	e.Message.Role = role
	e.Message.Content = json.RawMessage(content)
	return e
}

// toolResult rows resolve through the assistant toolCall map (pi's
// TreeList keeps the same map); usage entries never render.
func TestRenderTreeToolMapAndUsage(t *testing.T) {
	asst := `[{"type":"text","text":"I'll read it"},{"type":"toolCall","id":"tc1","name":"read","arguments":{"path":"a.go","offset":10,"limit":5}}]`
	tr := pirpc.TreeEntry{Type: "message", ID: "t1", ParentID: strptr("a1")}
	tr.Message.Role = "toolResult"
	tr.Message.ToolCallID = "tc1"
	tr.Message.ToolName = "read"
	nodes := []pirpc.TreeNode{{
		Entry: msgEntry("u1", "", "user", `"hello"`),
		Children: []pirpc.TreeNode{
			{Entry: msgEntry("a1", "u1", "assistant", asst), Children: []pirpc.TreeNode{
				{Entry: tr},
			}},
			{Entry: pirpc.TreeEntry{Type: "usage", ID: "x1"}},
		},
	}}
	got := renderTree(nodes, "t1", "all")
	want := "└── • user: hello\n" +
		"    ├── • assistant: I'll read it\n" +
		"    │   └── • [read: a.go:10-14]"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if strings.Contains(got, "usage") {
		t.Error("usage entries must be skipped like pi")
	}
}

func strptr(s string) *string { return &s }

// Every non-message entry formats like pi's getEntryDisplayText.
func TestRenderTreeMiscEntries(t *testing.T) {
	cm := pirpc.TreeEntry{Type: "custom_message", ID: "m1", CustomType: "plan", Content: json.RawMessage(`"do things"`)}
	nodes := []pirpc.TreeNode{
		{Entry: pirpc.TreeEntry{Type: "compaction", ID: "c1", TokensBefore: 12000}},
		{Entry: pirpc.TreeEntry{Type: "branch_summary", ID: "b1", Summary: "tried X\nnext"}},
		{Entry: pirpc.TreeEntry{Type: "label", ID: "l1"}},
		{Entry: pirpc.TreeEntry{Type: "session_info", ID: "s1"}},
		{Entry: cm},
		{Entry: pirpc.TreeEntry{Type: "model_change", ID: "x1"}},
		{Entry: pirpc.TreeEntry{Type: "thinking_level_change", ID: "x2"}},
		{Entry: msgEntry("u9", "", "user", `"hi"`), Label: "wip"},
	}
	got := renderTree(nodes, "c1", "all")
	want := "├── • [compaction: 12k tokens]\n" +
		"├── [branch summary]: tried X next\n" +
		"├── [label: (cleared)]\n" +
		"├── [title: empty]\n" +
		"├── [plan]: do things\n" +
		"├── [model: ?]\n" +
		"├── [thinking: ?]\n" +
		"└── [wip] user: hi"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderTreeAssistantFallbacks(t *testing.T) {
	aborted := msgEntry("a1", "", "assistant", `""`)
	aborted.Message.StopReason = "aborted"
	errMsg := msgEntry("a2", "", "assistant", `""`)
	errMsg.Message.ErrorMessage = "boom"
	empty := msgEntry("a3", "", "assistant", `""`)
	orphan := pirpc.TreeEntry{Type: "message", ID: "t9"}
	orphan.Message.Role = "toolResult"
	orphan.Message.ToolName = "read"
	bash := msgEntry("b9", "", "bashExecution", `""`)
	bash.Message.Command = "ls -la"
	nodes := []pirpc.TreeNode{
		{Entry: aborted},
		{Entry: errMsg},
		{Entry: empty},
		{Entry: orphan},
		{Entry: bash},
	}
	got := renderTree(nodes, "", "all")
	want := "├── assistant: (aborted)\n" +
		"├── assistant: boom\n" +
		"├── assistant: (no content)\n" +
		"├── [read]\n" +
		"└── [bash]: ls -la"
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderTreeEmpty(t *testing.T) {
	if got := renderTree(nil, "", "all"); got != "No entries in session" {
		t.Errorf("got %q", got)
	}
}

// Stock pi filter semantics: default hides settings entries (model_change,
// thinking_level_change, ...), no-tools drops toolResults, user-only keeps
// user messages, labeled-only keeps bookmarked rows.
func TestRenderTreeFilters(t *testing.T) {
	nodes := []pirpc.TreeNode{
		{Entry: pirpc.TreeEntry{Type: "model_change", ID: "m1", ModelID: "x"}},
		{Entry: msgEntry("u1", "", "user", `"hi"`), Label: "wip"},
		{Entry: msgEntry("a1", "", "assistant", `"ok"`)},
	}
	tr := pirpc.TreeEntry{Type: "message", ID: "t1"}
	tr.Message.Role = "toolResult"
	tr.Message.ToolName = "read"
	nodes = append(nodes, pirpc.TreeNode{Entry: tr})

	if got := renderTree(nodes, "", "default"); strings.Contains(got, "[model:") {
		t.Errorf("default should hide settings entries:\n%s", got)
	}
	if got := renderTree(nodes, "", "no-tools"); strings.Contains(got, "[read]") {
		t.Errorf("no-tools should hide toolResults:\n%s", got)
	}
	got := renderTree(nodes, "", "user-only")
	if !strings.Contains(got, "user: hi") || strings.Contains(got, "assistant:") {
		t.Errorf("user-only should keep only user rows:\n%s", got)
	}
	got = renderTree(nodes, "", "labeled-only")
	if !strings.Contains(got, "[wip]") || strings.Contains(got, "assistant:") {
		t.Errorf("labeled-only should keep only bookmarked rows:\n%s", got)
	}
	if got := renderTree(nodes, "", "all"); !strings.Contains(got, "[model:") {
		t.Errorf("all should show everything:\n%s", got)
	}
}
