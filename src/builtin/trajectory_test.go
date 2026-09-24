package builtin

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// /trajectory arg maps to a step tab; unknown args pre-filter the list.
func TestTrajScopeOf(t *testing.T) {
	for arg, want := range map[string]string{
		"": "all", "all": "all", "tools": "tools", "tool": "tools",
		"messages": "messages", "user": "messages",
	} {
		if scope, filter := trajScopeOf(arg); scope != want || filter != "" {
			t.Errorf("trajScopeOf(%q) = (%q,%q), want (%q,\"\")", arg, scope, filter, want)
		}
	}
	if scope, filter := trajScopeOf("read"); scope != "all" || filter != "read" {
		t.Errorf("trajScopeOf(read) = (%q,%q), want (all,read)", scope, filter)
	}
}

func trajNodes() []pirpc.TreeNode {
	asst := `[{"type":"text","text":"I'll read it"},{"type":"thinking","thinking":"user wants the file"},{"type":"toolCall","id":"tc1","name":"read","arguments":{"path":"a.go"}}]`
	tr := pirpc.TreeEntry{Type: "message", ID: "t1", ParentID: strptr("a1")}
	tr.Message.Role = "toolResult"
	tr.Message.ToolCallID = "tc1"
	tr.Message.ToolName = "read"
	tr.Message.Content = json.RawMessage(`[{"type":"text","text":"file content here"}]`)
	return []pirpc.TreeNode{{
		Entry: msgEntry("u1", "", "user", `"hello"+`),
		Children: []pirpc.TreeNode{
			{Entry: msgEntry("a1", "u1", "assistant", asst), Children: []pirpc.TreeNode{
				{Entry: tr},
			}},
			{Entry: pirpc.TreeEntry{Type: "usage", ID: "x1"}},
			{Entry: pirpc.TreeEntry{Type: "compaction", ID: "c1", TokensBefore: 12000}},
			{Entry: pirpc.TreeEntry{Type: "model_change", ID: "m1", ModelID: "x"}},
			{Entry: pirpc.TreeEntry{Type: "thinking_level_change", ID: "h1", ThinkingLevel: "high"}},
			{Entry: pirpc.TreeEntry{Type: "label", ID: "l1", Label: "wip"}},
			{Entry: pirpc.TreeEntry{Type: "session_info", ID: "s1", Name: "demo"}},
		},
	}}
}

// Steps render in run order, numbered, active path dotted. Usage,
// compaction, model/thinking changes, labels and titles are harness
// noise and never become steps.
func TestBuildTrajectoryAll(t *testing.T) {
	opts, descs, payload := buildTrajectory(trajNodes(), "t1", "all")
	if len(opts) != 3 {
		t.Fatalf("want 3 message steps (noise skipped), got %d: %v", len(opts), opts)
	}
	for i, o := range opts {
		if !strings.HasPrefix(o, fmt.Sprintf("#%02d ", i+1)) {
			t.Errorf("step %d missing number prefix: %q", i, o)
		}
		if !strings.Contains(o, "•") {
			t.Errorf("on-path step %d should carry •: %q", i, o)
		}
	}
	joined := strings.Join(opts, "\n")
	for _, nope := range []string{"compaction", "[model:", "[thinking:", "[label:", "[title:"} {
		if strings.Contains(joined, nope) {
			t.Errorf("noise %q must not render: %v", nope, opts)
		}
	}
	if !strings.Contains(opts[1], "assistant:") || !strings.Contains(opts[1], "read") {
		t.Errorf("assistant row should carry its tool call: %q", opts[1])
	}
	if !strings.Contains(opts[2], "[read:") {
		t.Errorf("toolResult should resolve via the toolCall map: %q", opts[2])
	}
	// desc = time + kind
	if !strings.Contains(descs[0], "user") || !strings.Contains(descs[2], "tool") {
		t.Errorf("descs = %v", descs)
	}
	// payload holds the FULL detail (thinking + args + result)
	if !strings.Contains(payload[1], "user wants the file") {
		t.Errorf("assistant detail should include thinking: %q", payload[1])
	}
	if !strings.Contains(payload[1], `"path":"a.go"`) {
		t.Errorf("assistant detail should include tool args: %q", payload[1])
	}
	if !strings.Contains(payload[2], "file content here") {
		t.Errorf("tool detail should include the result: %q", payload[2])
	}
}

// Tabs filter: tools = tool activity only, messages = chat only.
func TestBuildTrajectoryScopes(t *testing.T) {
	nodes := trajNodes()
	opts, _, _ := buildTrajectory(nodes, "t1", "tools")
	if len(opts) != 2 {
		t.Fatalf("tools tab want 2 steps (assistant+tool + result), got %v", opts)
	}
	opts, _, _ = buildTrajectory(nodes, "t1", "messages")
	if len(opts) != 2 {
		t.Fatalf("messages tab want 2 steps (user + assistant), got %v", opts)
	}
	if strings.Contains(strings.Join(opts, "\n"), "compaction") {
		t.Errorf("messages tab should hide compaction: %v", opts)
	}
}

// Enter closes the window (detail goes to chat as a tree-styled block);
// bad indices are a no-op. Blocks are private to app, so only the pop is
// asserted here — rendering is covered by TestBuildTrajectoryAll above.
func TestConfirmTrajectory(t *testing.T) {
	m := &app.Model{}
	d := &app.Dialog{Kind: "trajectory",
		Options: []string{"#01 • user: hi"}, Descs: []string{"meta"},
		Payload: []string{"#01 • user: hi\nmeta\n\nhi"}}
	d.Reindex()
	m.Dialogs = append(m.Dialogs, d)
	if _, ok := Confirmers()["trajectory"]; !ok {
		t.Fatal("trajectory missing from Confirmers")
	}
	if _, cmd := confirmTrajectory(m, m.Dialogs[0], 0); cmd != nil {
		t.Error("confirm should post synchronously, no cmd")
	}
	if len(m.Dialogs) != 0 {
		t.Fatalf("dialog should close, got %d", len(m.Dialogs))
	}
	m.Dialogs = append(m.Dialogs, d)
	if _, _ = confirmTrajectory(m, m.Dialogs[0], 9); len(m.Dialogs) != 1 {
		t.Error("bad index must leave the dialog open")
	}
}

// Registry: /trajectory exists as a pitago-origin command with its tabs.
func TestTrajectoryRegistered(t *testing.T) {
	found := false
	for _, b := range All() {
		if b.Name != "trajectory" {
			continue
		}
		found = true
		if b.Origin != OriginPitago {
			t.Error("/trajectory must be marked pitago origin")
		}
		if !strings.Contains(b.Usage, "tools") {
			t.Errorf("usage should advertise the tabs: %q", b.Usage)
		}
	}
	if !found {
		t.Fatal("/trajectory missing from All()")
	}
}
