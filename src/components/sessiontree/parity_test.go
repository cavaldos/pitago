package sessiontree

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"pitago/src/pirpc"
)

// TestParityWithPi renders every scenario with pi's own TreeList (through
// node) and with this port, and requires identical rows. It runs when pi's
// dist directory is found (PI_CODING_AGENT_DIST, or the managed install
// under ~/.pi/agent) and node is on PATH; otherwise it skips.
func TestParityWithPi(t *testing.T) {
	dist := piDist()
	node, err := exec.LookPath("node")
	if dist == "" || err != nil {
		t.Skip("pi's dist or node not found; set PI_CODING_AGENT_DIST to run")
	}
	scenarios := parityScenarios()
	in, _ := json.Marshal(scenarios)
	cmd := exec.Command(node, "testdata/pi_tree.mjs", dist)
	cmd.Stdin = bytes.NewReader(in)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("pi driver: %v\n%s", err, stderr.String())
	}
	var want [][]string
	if err := json.Unmarshal(out, &want); err != nil {
		t.Fatalf("pi driver output: %v\n%s", err, out)
	}
	for i, sc := range scenarios {
		got := runScenario(t, sc)
		if strings.Join(got, "\n") != strings.Join(want[i], "\n") {
			t.Errorf("scenario %q differs from pi\n--- pi\n%s\n--- pitago\n%s", sc.Name,
				strings.Join(want[i], "\n"), strings.Join(got, "\n"))
		}
	}
}

func piDist() string {
	if d := os.Getenv("PI_CODING_AGENT_DIST"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	matches, _ := filepath.Glob(filepath.Join(home, ".pi/agent/install/releases/*/node_modules/@earendil-works/pi-coding-agent/dist"))
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

type scenario struct {
	Name   string           `json:"name"`
	Tree   []pirpc.TreeNode `json:"tree"`
	Leaf   string           `json:"leaf,omitempty"`
	Select string           `json:"select,omitempty"`
	Mode   string           `json:"mode"`
	Rows   int              `json:"rows"`
	Width  int              `json:"width"`
	Ops    []string         `json:"ops"`
}

func runScenario(t *testing.T, sc scenario) []string {
	t.Helper()
	l := New(sc.Tree, sc.Leaf, max(5, sc.Rows/2), sc.Select, sc.Mode)
	for _, op := range sc.Ops {
		switch {
		case strings.HasPrefix(op, "type:"):
			l.Type(op[5:])
		case op == "up":
			l.Up()
		case op == "down":
			l.Down()
		case op == "left", op == "pageUp":
			l.PageUp()
		case op == "right", op == "pageDown":
			l.PageDown()
		case op == "fold":
			l.FoldOrUp()
		case op == "unfold":
			l.UnfoldOrDown()
		case op == "esc":
			l.Cancel()
		case op == "backspace":
			l.Backspace()
		case op == "ctrlD":
			l.SetMode("default")
		case op == "ctrlT":
			l.ToggleMode("no-tools")
		case op == "ctrlU":
			l.ToggleMode("user-only")
		case op == "ctrlL":
			l.ToggleMode("labeled-only")
		case op == "ctrlA":
			l.ToggleMode("all")
		case op == "ctrlO":
			l.CycleMode(1)
		case op == "labelTime":
			l.ToggleLabelTimestamps()
		default:
			t.Fatalf("unknown op %q", op)
		}
	}
	return plain(l, sc.Width)
}

// node builds one entry; raw fields are merged into it.
func node(entry string, kids ...pirpc.TreeNode) pirpc.TreeNode {
	var e pirpc.TreeEntry
	if err := json.Unmarshal([]byte(entry), &e); err != nil {
		panic(err)
	}
	return pirpc.TreeNode{Entry: e, Children: append([]pirpc.TreeNode{}, kids...)}
}

func parityScenarios() []scenario {
	u := func(id, parent, text string, kids ...pirpc.TreeNode) pirpc.TreeNode {
		p := "null"
		if parent != "" {
			p = `"` + parent + `"`
		}
		return node(`{"type":"message","id":"`+id+`","parentId":`+p+`,"timestamp":"","message":{"role":"user","content":[{"type":"text","text":"`+text+`"}]}}`, kids...)
	}
	a := func(id, parent, text string, kids ...pirpc.TreeNode) pirpc.TreeNode {
		return node(`{"type":"message","id":"`+id+`","parentId":"`+parent+`","timestamp":"","message":{"role":"assistant","content":[{"type":"text","text":"`+text+`"}],"stopReason":"stop"}}`, kids...)
	}
	call := func(id, parent string, kids ...pirpc.TreeNode) pirpc.TreeNode {
		return node(`{"type":"message","id":"`+id+`","parentId":"`+parent+`","timestamp":"","message":{"role":"assistant","content":[`+
			`{"type":"toolCall","id":"c-`+id+`","name":"bash","arguments":{"command":"go test ./... && echo a very long command line that keeps going and going"}},`+
			`{"type":"toolCall","id":"d-`+id+`","name":"mcp_thing","arguments":{"query":"x","limit":5,"deep":{"k":[1,2,3]}}}],"stopReason":"toolUse"}}`, kids...)
	}
	res := func(id, parent, callID string, kids ...pirpc.TreeNode) pirpc.TreeNode {
		return node(`{"type":"message","id":"`+id+`","parentId":"`+parent+`","timestamp":"","message":{"role":"toolResult","toolCallId":"`+callID+`","toolName":"bash","content":[]}}`, kids...)
	}
	misc := func(id, parent, raw string, kids ...pirpc.TreeNode) pirpc.TreeNode {
		return node(`{"id":"`+id+`","parentId":"`+parent+`","timestamp":"",`+raw+`}`, kids...)
	}

	branched := []pirpc.TreeNode{u("u1", "", "hello",
		a("a1", "u1", "hi",
			u("u2", "a1", "old question", a("a2", "u2", "old answer",
				u("u4", "a2", "deeper", a("a4", "u4", "deeper answer")),
				u("u5", "a2", "sibling", a("a5", "u5", "sibling answer")))),
			u("u3", "a1", "new question", a("a3", "u3", "new answer")),
		))}
	mixed := []pirpc.TreeNode{u("m1", "", "find the bug in\\tparser\\nplease",
		misc("mc", "m1", `"type":"model_change","provider":"p","modelId":"gpt-9"`,
			misc("tl", "mc", `"type":"thinking_level_change","thinkingLevel":"high"`,
				call("c1", "tl",
					res("r1", "c1", "c-c1",
						res("r2", "r1", "d-c1",
							misc("cm", "r2", `"type":"custom_message","customType":"note","content":"remember this","display":true`,
								misc("cp", "cm", `"type":"compaction","summary":"s","tokensBefore":15499,"firstKeptEntryId":"m1"`,
									misc("bs", "cp", `"type":"branch_summary","summary":"tried\\nthings","fromId":"m1"`,
										misc("si", "bs", `"type":"session_info","name":""`,
											misc("ce", "si", `"type":"context_edit","targetId":"r1","replacement":null`,
												misc("lb", "ce", `"type":"label","targetId":"m1","label":"x"`,
													node(`{"type":"message","id":"ab","parentId":"lb","timestamp":"","message":{"role":"assistant","content":[],"stopReason":"aborted"}}`,
														node(`{"type":"message","id":"er","parentId":"ab","timestamp":"","message":{"role":"assistant","content":[],"stopReason":"error","errorMessage":"rate limited\\nretry later"}}`,
															node(`{"type":"message","id":"bx","parentId":"er","timestamp":"","message":{"role":"bashExecution","command":"ls -la","output":"","exitCode":0}}`)))))))))))))))}
	roots := []pirpc.TreeNode{
		u("r1", "", "first root", a("ra", "r1", "first answer")),
		u("r2", "", "second root", a("rb", "r2", "second answer", u("rc", "rb", "follow"))),
	}
	var chain pirpc.TreeNode
	for i := 30; i >= 0; i-- {
		id, parent := "q"+strconv.Itoa(i), ""
		if i > 0 {
			parent = "q" + strconv.Itoa(i-1)
		}
		if i == 30 {
			chain = u(id, parent, "question number "+id)
		} else {
			chain = u(id, parent, "question number "+id, chain)
		}
	}
	// deep nests branch points so the indentation outgrows a narrow screen:
	// every level is a user message with two children, the next level and a
	// short side branch.
	var level func(i int, parent string) pirpc.TreeNode
	level = func(i int, parent string) pirpc.TreeNode {
		id := "d" + strconv.Itoa(i)
		if i == 7 {
			return u(id, parent, "the deepest question of them all", a("dz", id, "the deepest answer"))
		}
		return u(id, parent, "level "+strconv.Itoa(i), level(i+1, id), u("s"+strconv.Itoa(i), id, "side "+strconv.Itoa(i)))
	}
	deep := []pirpc.TreeNode{level(0, "")}

	return []scenario{
		{Name: "branched default", Tree: branched, Leaf: "a3", Mode: "default", Rows: 30, Width: 80},
		{Name: "fold + branch jumps", Tree: branched, Leaf: "a3", Select: "u2", Mode: "default", Rows: 30, Width: 80,
			Ops: []string{"fold", "down", "down", "unfold", "up", "up", "fold", "unfold", "unfold"}},
		{Name: "fold root", Tree: branched, Leaf: "a5", Select: "u1", Mode: "default", Rows: 30, Width: 80, Ops: []string{"fold"}},
		{Name: "segment start up", Tree: branched, Leaf: "a4", Mode: "default", Rows: 30, Width: 80, Ops: []string{"fold", "fold", "fold"}},
		{Name: "search", Tree: branched, Leaf: "a3", Mode: "default", Rows: 30, Width: 80, Ops: []string{"type:Answer old", "backspace", "backspace", "backspace", "backspace"}},
		{Name: "search esc", Tree: branched, Leaf: "a3", Mode: "default", Rows: 30, Width: 80, Ops: []string{"type:sibling", "esc"}},
		{Name: "user-only", Tree: branched, Leaf: "a3", Mode: "default", Rows: 30, Width: 80, Ops: []string{"ctrlU"}},
		{Name: "mixed default", Tree: mixed, Leaf: "bx", Mode: "default", Rows: 40, Width: 100},
		{Name: "mixed all", Tree: mixed, Leaf: "bx", Mode: "default", Rows: 40, Width: 100, Ops: []string{"ctrlA"}},
		{Name: "mixed no-tools", Tree: mixed, Leaf: "bx", Mode: "default", Rows: 40, Width: 100, Ops: []string{"ctrlT"}},
		{Name: "mixed labeled", Tree: mixed, Leaf: "bx", Mode: "default", Rows: 40, Width: 100, Ops: []string{"ctrlL"}},
		{Name: "mixed cycle", Tree: mixed, Leaf: "bx", Mode: "default", Rows: 40, Width: 100, Ops: []string{"ctrlO", "ctrlO", "ctrlO", "labelTime"}},
		{Name: "multiple roots", Tree: roots, Leaf: "rc", Mode: "default", Rows: 30, Width: 80},
		{Name: "multiple roots fold", Tree: roots, Leaf: "rc", Select: "r1", Mode: "default", Rows: 30, Width: 80, Ops: []string{"fold", "down"}},
		{Name: "no leaf", Tree: roots, Mode: "default", Rows: 30, Width: 80},
		{Name: "paging", Tree: []pirpc.TreeNode{chain}, Leaf: "q4", Mode: "default", Rows: 14, Width: 60,
			Ops: []string{"right", "right", "down", "left", "pageDown", "up", "up"}},
		{Name: "wrap", Tree: []pirpc.TreeNode{chain}, Leaf: "q0", Mode: "default", Rows: 14, Width: 60, Ops: []string{"up"}},
		{Name: "narrow + horizontal scroll", Tree: deep, Leaf: "dz", Mode: "default", Rows: 40, Width: 30},
		{Name: "narrow other row", Tree: deep, Leaf: "dz", Mode: "default", Rows: 40, Width: 30, Ops: []string{"up", "up", "up"}},
		{Name: "empty filter", Tree: branched, Leaf: "a3", Mode: "default", Rows: 30, Width: 80, Ops: []string{"type:zzz", "ctrlL"}},
		{Name: "initial mode", Tree: mixed, Leaf: "bx", Mode: "user-only", Rows: 30, Width: 80},
	}
}
