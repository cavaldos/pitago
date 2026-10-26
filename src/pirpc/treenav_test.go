package pirpc

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// treeFakePi answers like a pi that loaded tree_ext.ts: get_commands lists
// the two commands (unless FAKE_TREE_OFF), and a /pitago-tree prompt writes
// the report the extension would write — FAKE_TREE_REPORT verbatim, or
// nothing when it is "none". Every prompt is logged.
const treeFakePi = `#!/usr/bin/env python3
import json, os, sys
log = open(os.environ["FAKE_TREE_LOG"], "a", buffering=1)
for line in sys.stdin:
    cmd = json.loads(line)
    resp = {"id": cmd.get("id"), "type": "response", "command": cmd.get("type"), "success": True}
    if cmd.get("type") == "get_commands":
        names = [] if os.environ.get("FAKE_TREE_OFF") else ["pitago-tree", "pitago-tree-label"]
        resp["data"] = {"commands": [{"name": n, "source": "extension"} for n in names] + [{"name": "review", "source": "extension"}]}
    if cmd.get("type") == "prompt":
        log.write(cmd["message"] + "\n")
        name, _, args = cmd["message"].partition(" ")
        if name == "/pitago-tree":
            report = os.environ.get("FAKE_TREE_REPORT", '{"cancelled":false}')
            if report != "none":
                open(json.loads(args)["out"], "w").write(report)
        resp["data"] = {"disposition": "handled"}
    print(json.dumps(resp), flush=True)
`

func spawnTreeFake(t *testing.T, env map[string]string) (*Client, func() []string) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not found")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-pi")
	logPath := filepath.Join(dir, "prompts.log")
	if err := os.WriteFile(bin, []byte(treeFakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_TREE_LOG", logPath)
	for k, v := range env {
		t.Setenv(k, v)
	}
	c, err := Spawn(Options{Bin: bin, Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, func() []string {
		raw, _ := os.ReadFile(logPath)
		return strings.Fields(strings.ReplaceAll(string(raw), " ", "\x00"))
	}
}

func TestNavigateTreeSendsRequestAndReadsReport(t *testing.T) {
	c, prompts := spawnTreeFake(t, nil)
	custom := "focus on\nthe parser"
	res, err := c.NavigateTree("e1", TreeNavOptions{Summarize: true, CustomInstructions: &custom})
	if err != nil || res.Cancelled || res.Unknown {
		t.Fatalf("NavigateTree = %+v, %v", res, err)
	}
	got := prompts()
	if len(got) != 1 {
		t.Fatalf("prompts = %q", got)
	}
	name, args, _ := strings.Cut(strings.ReplaceAll(got[0], "\x00", " "), " ")
	var req map[string]any
	if err := json.Unmarshal([]byte(args), &req); err != nil || name != "/pitago-tree" {
		t.Fatalf("bad prompt %q: %v", got[0], err)
	}
	if req["targetId"] != "e1" || req["summarize"] != true || req["customInstructions"] != custom || req["out"] == "" {
		t.Fatalf("request = %v", req)
	}
	if _, err := os.Stat(req["out"].(string)); !os.IsNotExist(err) {
		t.Fatal("the report file must be removed afterwards")
	}
}

func TestNavigateTreeReports(t *testing.T) {
	for report, want := range map[string]TreeNavResult{
		`{"cancelled":true}`: {Cancelled: true},
		"none":               {Unknown: true},
	} {
		c, _ := spawnTreeFake(t, map[string]string{"FAKE_TREE_REPORT": report})
		if res, err := c.NavigateTree("e1", TreeNavOptions{}); err != nil || res != want {
			t.Errorf("report %s: got %+v, %v", report, res, err)
		}
	}
	c, _ := spawnTreeFake(t, map[string]string{"FAKE_TREE_REPORT": `{"error":"Entry e1 not found"}`})
	if _, err := c.NavigateTree("e1", TreeNavOptions{}); err == nil || err.Error() != "Entry e1 not found" {
		t.Errorf("error report: %v", err)
	}
}

func TestTreeCommandsNeedTheExtension(t *testing.T) {
	c, prompts := spawnTreeFake(t, map[string]string{"FAKE_TREE_OFF": "1"})
	if _, err := c.NavigateTree("e1", TreeNavOptions{}); !errors.Is(err, ErrTreeExtUnavailable) {
		t.Fatalf("NavigateTree without the extension: %v", err)
	}
	if err := c.SetTreeLabel("e1", "x"); !errors.Is(err, ErrTreeExtUnavailable) {
		t.Fatalf("SetTreeLabel without the extension: %v", err)
	}
	if got := prompts(); len(got) != 0 {
		t.Fatalf("a command pi does not know must never be sent as a prompt: %q", got)
	}
}

func TestSetTreeLabelAndHiddenCommands(t *testing.T) {
	c, prompts := spawnTreeFake(t, nil)
	if err := c.SetTreeLabel("e1", "checkpoint"); err != nil {
		t.Fatal(err)
	}
	if got := prompts(); len(got) != 1 || strings.ReplaceAll(got[0], "\x00", " ") != `/pitago-tree-label {"entryId":"e1","label":"checkpoint"}` {
		t.Fatalf("prompts = %q", got)
	}
	cmds, err := c.GetCommands()
	if err != nil || len(cmds) != 1 || cmds[0].Name != "review" {
		t.Fatalf("GetCommands must hide the tree commands: %+v, %v", cmds, err)
	}
}

func TestInstallTreeExtIn(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "pi-ext")
	path, err := InstallTreeExtIn(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != string(treeExtTS) || filepath.Base(path) != "pitago-tree.ts" {
		t.Fatalf("installed %s with wrong content", path)
	}
	if _, err := InstallTreeExtIn(dir); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("install left %d files", len(entries))
	}
}
