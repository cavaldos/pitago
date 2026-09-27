package builtin

import (
	"encoding/json"
	"strings"
	"testing"

	"pitago/src/app"
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

func TestBuildTreeRowsForNativeTab(t *testing.T) {
	rows := buildTreeRows(trajNodes(), "t1", "all")
	if len(rows.opts) != 8 || len(rows.descs) != 8 || len(rows.payload) != 8 {
		t.Fatalf("tree rows must stay parallel: opts=%d descs=%d payload=%d", len(rows.opts), len(rows.descs), len(rows.payload))
	}
	if len(rows.ids) != 8 || len(rows.jump) != 8 || len(rows.role) != 8 {
		t.Fatalf("tree metadata must stay parallel: ids=%d jump=%d role=%d", len(rows.ids), len(rows.jump), len(rows.role))
	}
	if rows.current != 2 {
		t.Fatalf("active leaf index = %d, want 2", rows.current)
	}
	if rows.ids[rows.current] != "t1" {
		t.Errorf("row %d must be the leaf, got id %q", rows.current, rows.ids[rows.current])
	}
	want := []string{
		"• user: hello",
		"• assistant: I'll read it",
		"• [read: a.go]",
		"[compaction: 12k tokens]",
		"[model: x]",
		"[thinking: high]",
		"[label: wip]",
		"[title: demo]",
	}
	for i := range want {
		if rows.opts[i] != want[i] {
			t.Errorf("row %d:\n got %q\nwant %q", i, rows.opts[i], want[i])
		}
	}
	if !strings.Contains(rows.descs[0], "user") || !strings.Contains(rows.descs[2], "tool") {
		t.Errorf("descs should describe message kinds: %v", rows.descs)
	}
	if !strings.Contains(rows.payload[1], "user wants the file") || !strings.Contains(rows.payload[1], `"path":"a.go"`) {
		t.Errorf("assistant payload should include thinking and tool args: %q", rows.payload[1])
	}
	if !strings.Contains(rows.payload[2], "file content here") {
		t.Errorf("tool payload should include result: %q", rows.payload[2])
	}
	// The active branch here is u1 → a1 → t1: only the user and the
	// text-carrying assistant are transcript blocks; the toolResult and the
	// settings/noise entries are not jump targets and carry no role.
	wantIDs := []string{"u1", "a1", "t1", "c1", "m1", "h1", "l1", "s1"}
	wantJump := []int{0, 1, -1, -1, -1, -1, -1, -1}
	wantRole := []string{"user", "assistant", "", "", "", "", "", ""}
	for i := range wantIDs {
		if rows.ids[i] != wantIDs[i] || rows.jump[i] != wantJump[i] || rows.role[i] != wantRole[i] {
			t.Errorf("row %d: id=%q jump=%d role=%q, want id=%q jump=%d role=%q",
				i, rows.ids[i], rows.jump[i], rows.role[i], wantIDs[i], wantJump[i], wantRole[i])
		}
	}
}

// Jump ordinals follow the active branch and the transcript, not the flat
// list: an assistant turn that only issued a toolCall, its toolResult, a
// compaction and a whole abandoned branch all lack a block to scroll to.
func TestBuildTreeRowsJumpOrdinals(t *testing.T) {
	toolOnly := msgEntry("a2", "a1", "assistant", `[{"type":"toolCall","id":"tc9","name":"read","arguments":{}}]`)
	tr := pirpc.TreeEntry{Type: "message", ID: "tr1", ParentID: strptr("a2")}
	tr.Message.Role = "toolResult"
	tr.Message.ToolCallID = "tc9"
	tr.Message.ToolName = "read"
	nodes := []pirpc.TreeNode{{
		Entry: msgEntry("u1", "", "user", `"first question"`),
		Children: []pirpc.TreeNode{
			{Entry: msgEntry("a1", "u1", "assistant", `[{"type":"text","text":"on it"}]`), Children: []pirpc.TreeNode{
				{Entry: toolOnly, Children: []pirpc.TreeNode{{Entry: tr}}},
			}},
			{Entry: pirpc.TreeEntry{Type: "compaction", ID: "c1", TokensBefore: 900}},
			{Entry: msgEntry("u2", "u1", "user", `"abandoned"`), Children: []pirpc.TreeNode{
				{Entry: msgEntry("a9", "u2", "assistant", `[{"type":"text","text":"ghost"}]`)},
			}},
			{Entry: pirpc.TreeEntry{Type: "label", ID: "l1", Label: "wip"}},
		},
	}}
	rows := buildTreeRows(nodes, "tr1", "all")
	if rows.current != 3 || rows.ids[3] != "tr1" {
		t.Fatalf("current = %d (id %q), want 3 (tr1)", rows.current, rows.ids[3])
	}
	wantJump := []int{0, 1, -1, -1, -1, -1, -1, -1}
	wantRole := []string{"user", "assistant", "assistant", "", "", "user", "assistant", ""}
	if len(rows.jump) != len(wantJump) {
		t.Fatalf("jump rows = %v, want %d", rows.jump, len(wantJump))
	}
	for i := range wantJump {
		if rows.jump[i] != wantJump[i] || rows.role[i] != wantRole[i] {
			t.Errorf("row %d (%s): jump=%d role=%q, want jump=%d role=%q",
				i, rows.ids[i], rows.jump[i], rows.role[i], wantJump[i], wantRole[i])
		}
	}
}

// A user message carrying only images still becomes a transcript block
// (app.withImages renders "📷 1 image attached"), so it must keep an
// ordinal. Skipping it would shift every later jump one message early.
func TestBuildTreeRowsNumbersImageOnlyUserMessage(t *testing.T) {
	imgOnly := msgEntry("u1", "", "user", `[{"type":"image","data":"AAAA","mimeType":"image/png"}]`)
	nodes := []pirpc.TreeNode{{
		Entry: imgOnly,
		Children: []pirpc.TreeNode{
			{Entry: msgEntry("a1", "u1", "assistant", `[{"type":"text","text":"a cat"}]`)},
			{Entry: msgEntry("a2", "a1", "assistant", `[{"type":"text","text":"ok"}]`)},
		},
	}}
	rows := buildTreeRows(nodes, "a2", "all")
	want := []int{0, 1, 2}
	if len(rows.jump) != len(want) {
		t.Fatalf("jump rows = %v, want %v", rows.jump, want)
	}
	for i := range want {
		if rows.jump[i] != want[i] {
			t.Errorf("row %d (%s, role %q): jump = %d, want %d", i, rows.ids[i], rows.role[i], rows.jump[i], want[i])
		}
	}
}

// An assistant message with several text blocks becomes SEVERAL transcript
// blocks (restore adds one per block), and a plain-string message is one
// block with no content array at all. Both shapes have to consume exactly
// as many ordinals as blocks, or every later jump lands early.
func TestBuildTreeRowsNumbersEachTranscriptBlock(t *testing.T) {
	twoText := msgEntry("a1", "u1", "assistant", `[{"type":"text","text":"first"},{"type":"text","text":"second"}]`)
	plain := msgEntry("u2", "a1", "user", `"next question"`)
	plainAsst := msgEntry("a2", "u2", "assistant", `"plain answer"`)
	nodes := []pirpc.TreeNode{{
		Entry: msgEntry("u1", "", "user", `"hi"`),
		Children: []pirpc.TreeNode{
			{Entry: twoText},
			{Entry: plain, Children: []pirpc.TreeNode{{Entry: plainAsst}}},
		},
	}}
	rows := buildTreeRows(nodes, "a2", "all")
	// u1=0, a1=1 (its SECOND text block is ordinal 2), u2=3, a2=4.
	want := []int{0, 1, 3, 4}
	if len(rows.jump) != len(want) {
		t.Fatalf("jump rows = %v, want %v", rows.jump, want)
	}
	for i := range want {
		if rows.jump[i] != want[i] {
			t.Errorf("row %d (%s, role %q): jump = %d, want %d", i, rows.ids[i], rows.role[i], rows.jump[i], want[i])
		}
	}
}

// Enter on a tree row must open the local action menu — pi answers a picked
// row with a follow-up selector rather than printing the entry — and the row
// set must follow what pi can actually do: fork only from a user message
// (runtimeHost.fork rejects every other entry), jump only where a transcript
// block exists.
func TestConfirmTreeOpensActionMenu(t *testing.T) {
	cases := []struct {
		name              string
		row, desc, detail string
		id                string
		jump              int
		role              string
		want              []string
	}{
		{"user", "• user: hi", "user · 10:00", "user · id abc · 10:00\n\nhi", "e1", 0, "user",
			[]string{TreeActJump, TreeActCopy, TreeActFork, TreeActBack}},
		{"assistant", "• assistant: ok", "assistant · 10:00", "assistant · id ab2 · 10:00\n\nok", "e2", 1, "assistant",
			[]string{TreeActJump, TreeActCopy, TreeActBack}},
		{"compaction", "[compaction: 12k tokens]", "compaction · 10:00", "compaction · id ab3 · 10:00", "e3", -1, "",
			[]string{TreeActView, TreeActCopy, TreeActBack}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := &app.Model{}
			tree := &app.Dialog{Kind: "tree", Options: []string{c.row}, Descs: []string{c.desc},
				Payload: []string{c.detail}, Paths: []string{c.id},
				TreeJump: []int{c.jump}, TreeRole: []string{c.role}}
			tree.Reindex()
			m.Dialogs = append(m.Dialogs, tree)
			if _, ok := Confirmers()["tree"]; !ok {
				t.Fatal("tree missing from Confirmers")
			}
			if _, cmd := confirmTree(m, m.Dialogs[0], 0); cmd != nil {
				t.Error("confirm tree should be synchronous")
			}
			if len(m.Dialogs) != 2 {
				t.Fatalf("the tree must stay open under the menu, got %d dialogs", len(m.Dialogs))
			}
			// Dialogs[0] is the active dialog everywhere in the app (View,
			// updateDialog, dismissDialog), so the menu has to be in front —
			// appended at the end it would be invisible and leak one dialog
			// per Enter.
			act := m.Dialogs[0]
			if m.Dialogs[1].Kind != "tree" {
				t.Fatalf("the tree must sit behind the menu, got %q", m.Dialogs[1].Kind)
			}
			if act.Kind != "treeAction" || act.Title != "Tree action" || act.Message != c.row {
				t.Errorf("menu dialog = %+v", act)
			}
			if strings.Join(act.Options, "|") != strings.Join(c.want, "|") {
				t.Errorf("labels = %v, want %v", act.Options, c.want)
			}
			if len(act.Descs) != len(act.Options) {
				t.Errorf("every label needs a desc: %v vs %v", act.Descs, act.Options)
			}
			if act.Paths[0] != c.id || act.Payload[0] != c.detail || act.TreeJump[0] != c.jump {
				t.Errorf("menu must carry id/detail/ordinal: paths=%q payload=%q jump=%d",
					act.Paths[0], act.Payload[0], act.TreeJump[0])
			}
			if _, ok := Confirmers()["treeAction"]; !ok {
				t.Fatal("treeAction missing from Confirmers")
			}
		})
	}
}
