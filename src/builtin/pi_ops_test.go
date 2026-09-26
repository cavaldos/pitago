package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/app"
	"pitago/src/pirpc"
)

// fakePiOps answers the RPC commands this file's builtins drive, and can be
// told to veto a session switch the way an extension does through
// session_before_switch (pi answers success:true + data{cancelled:true}).
const fakePiOps = `#!/usr/bin/env python3
import json, os, sys
log = open(os.environ["FAKE_PI_LOG"], "a", buffering=1)
veto = bool(os.environ.get("FAKE_PI_VETO"))
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    cmd = json.loads(line)
    log.write(json.dumps(cmd) + "\n")
    cid, typ = cmd.get("id", ""), cmd.get("type", "")
    resp = {"id": cid, "type": "response", "command": typ, "success": True}
    if typ == "get_fork_messages":
        resp["data"] = {"messages": [
            {"entryId": "e1", "text": "first question\nwith a second line"},
            {"entryId": "e2", "text": "second question"}]}
    elif typ == "fork":
        resp["data"] = {"text": "first question", "cancelled": veto}
    elif typ == "clone":
        resp["data"] = {"cancelled": veto}
    elif typ == "new_session":
        resp["data"] = {"cancelled": veto}
    elif typ == "compact":
        resp["data"] = {"summary": "s", "tokensBefore": 12000,
                        "estimatedTokensAfter": 1500}
    elif typ == "export_html":
        resp["data"] = {"path": cmd.get("outputPath") or "/tmp/pi-session.html"}
    elif typ == "get_state":
        resp["data"] = {"model": {"id": "m", "provider": "p"},
                        "thinkingLevel": "off",
                        "sessionName": os.environ.get("FAKE_PI_NAME", "")}
    elif typ == "set_session_name" and not str(cmd.get("name", "")).strip():
        resp["success"], resp["error"] = False, "Session name cannot be empty"
    print(json.dumps(resp), flush=True)
`

// opsModel spawns the fake pi and returns a model plus a command-log reader.
func opsModel(t *testing.T, env map[string]string) (*app.Model, func() []map[string]any) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-pi")
	logPath := filepath.Join(dir, "cmds.jsonl")
	if err := os.WriteFile(bin, []byte(fakePiOps), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_PI_LOG", logPath)
	for k, v := range env {
		t.Setenv(k, v)
	}
	pi, err := pirpc.Spawn(pirpc.Options{Bin: bin, Dir: dir})
	if err != nil {
		t.Fatalf("spawn fake pi: %v", err)
	}
	t.Cleanup(pi.Close)
	m := app.New(pi, dir)
	return &m, func() []map[string]any {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []map[string]any
		for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
			if l == "" {
				continue
			}
			var c map[string]any
			if err := json.Unmarshal([]byte(l), &c); err == nil {
				out = append(out, c)
			}
		}
		return out
	}
}

// runOp runs a builtin command and returns the PiOpMsg it reported.
func runOp(cmd tea.Cmd) app.PiOpMsg {
	msg := cmd()
	op, ok := msg.(app.PiOpMsg)
	if !ok {
		panic("expected a PiOpMsg, got " + reflect.TypeOf(msg).String())
	}
	return op
}

func builtinByName(t *testing.T, name string) app.Builtin {
	t.Helper()
	for _, b := range All() {
		if b.Name == name {
			return b
		}
	}
	t.Fatalf("/%s is not registered", name)
	return app.Builtin{}
}

// The five commands pi answers over RPC must not be reported as
// "pi TUI-only" any more, while the ones pi really keeps in its TUI must
// still report instead of leaking "/trust" into the chat.
func TestRPCBackedPiCommandsAreNotTUIOOnly(t *testing.T) {
	for _, name := range []string{"compact", "fork", "clone", "name", "export"} {
		b := builtinByName(t, name)
		if b.Origin != OriginPi {
			t.Errorf("/%s origin = %q, want %q", name, b.Origin, OriginPi)
		}
		if strings.Contains(b.Desc, "TUI-only") {
			t.Errorf("/%s still claims pi's TUI is required: %q", name, b.Desc)
		}
	}
	for _, name := range []string{"trust", "import", "share", "scoped-models"} {
		b := builtinByName(t, name)
		if !strings.Contains(b.Desc, "TUI-only") {
			t.Errorf("/%s has no RPC equivalent and must keep reporting: %q", name, b.Desc)
		}
	}
}

// /compact is an LLM call: it waits, then reports the token win and asks
// for the transcript to be re-read.
func TestCompactRunsAndReportsItsResult(t *testing.T) {
	m, log := opsModel(t, nil)
	cmd := compactSession(m, "keep the test plan")
	msg := cmd()
	op, ok := msg.(app.PiOpMsg)
	if !ok {
		t.Fatalf("compact must report a PiOpMsg, got %T", msg)
	}
	if op.Err != nil {
		t.Fatalf("compact: %v", op.Err)
	}
	if !strings.Contains(op.Notice, "12,000") || !strings.Contains(op.Notice, "1,500") {
		t.Errorf("notice = %q, want pi's before → after token counts", op.Notice)
	}
	if !op.Reload {
		t.Error("compaction rewrote the transcript, so the chat must be re-read")
	}
	cmds := log()
	if len(cmds) != 1 || cmds[0]["type"] != "compact" {
		t.Fatalf("commands = %v, want one compact", cmds)
	}
	if cmds[0]["customInstructions"] != "keep the test plan" {
		t.Errorf("customInstructions = %v, want the /compact argument passed through", cmds[0]["customInstructions"])
	}
}

// /fork lists pi's own branch points and the ids it hands back are what
// Fork(entryID) takes.
func TestForkListsPiBranchPoints(t *testing.T) {
	m, _ := opsModel(t, nil)
	pm, ok := forkPick(m)().(app.PickerMsg)
	if !ok {
		t.Fatal("/fork must open a picker of pi's fork points")
	}
	if pm.Kind != "fork" {
		t.Errorf("picker kind = %q, want fork", pm.Kind)
	}
	if len(pm.Options) != 2 || len(pm.Paths) != 2 {
		t.Fatalf("picker rows = %v / ids %v, want 2 of each", pm.Options, pm.Paths)
	}
	if pm.Paths[0] != "e1" || pm.Paths[1] != "e2" {
		t.Errorf("ids = %v, want pi's entry ids", pm.Paths)
	}
	if strings.Contains(pm.Options[0], "\n") {
		t.Errorf("row %q must stay on one line", pm.Options[0])
	}
	if pm.Current != pm.Options[1] {
		t.Errorf("current = %q, want the newest message preselected (pi parity)", pm.Current)
	}
}

// Enter on a fork row forks there; pi hands the branch's text back so the
// user keeps going from it.
func TestConfirmForkForksAtThePickedEntry(t *testing.T) {
	m, log := opsModel(t, nil)
	d := &app.Dialog{Kind: "fork", Options: []string{"a", "b"}, Paths: []string{"e1", "e2"}}
	m.Dialogs = append(m.Dialogs, d)
	nm, cmd := Confirmers()["fork"](m, d, 1)
	m = nm.(*app.Model)
	if len(m.Dialogs) != 0 {
		t.Error("picking a fork point must close the picker")
	}
	op, ok := cmd().(app.PiOpMsg)
	if !ok {
		t.Fatal("fork must report a PiOpMsg")
	}
	if op.Err != nil {
		t.Fatalf("fork: %v", op.Err)
	}
	if op.Text != "first question" {
		t.Errorf("fork text = %q, want pi's selectedText for the editor", op.Text)
	}
	if !op.Reload {
		t.Error("a fork changes the session, so the transcript must be re-read")
	}
	cmds := log()
	if len(cmds) != 1 || cmds[0]["type"] != "fork" || cmds[0]["entryId"] != "e2" {
		t.Errorf("commands = %v, want one fork at e2", cmds)
	}
}

// An extension veto through session_before_switch is reported, never read
// as a completed switch.
func TestForkVetoIsReported(t *testing.T) {
	m, _ := opsModel(t, map[string]string{"FAKE_PI_VETO": "1"})
	d := &app.Dialog{Kind: "fork", Options: []string{"a"}, Paths: []string{"e1"}}
	m.Dialogs = append(m.Dialogs, d)
	_, cmd := Confirmers()["fork"](m, d, 0)
	op := cmd().(app.PiOpMsg)
	if !pirpc.IsVeto(op.Err) {
		t.Fatalf("a cancelled fork must surface as a veto, got %v", op.Err)
	}
	if op.Reload {
		t.Error("a vetoed fork did not switch sessions — nothing to re-read")
	}
}

func TestCloneVetoIsReported(t *testing.T) {
	m, _ := opsModel(t, map[string]string{"FAKE_PI_VETO": "1"})
	op := runOp(cloneSession(m))
	if !pirpc.IsVeto(op.Err) {
		t.Fatalf("a cancelled clone must surface as a veto, got %v", op.Err)
	}
	if op.Reload {
		t.Error("a vetoed clone did not switch sessions — nothing to re-read")
	}
}

func TestCloneSuccessReloadsTheTranscript(t *testing.T) {
	m, _ := opsModel(t, nil)
	op := runOp(cloneSession(m))
	if op.Err != nil || !op.Reload {
		t.Fatalf("clone = %+v, want a clean switch that re-reads the session", op)
	}
}

// /name with no argument shows the current name, like pi; with one it sets
// it over RPC.
func TestNameShowsAndSetsTheSessionName(t *testing.T) {
	m, log := opsModel(t, map[string]string{"FAKE_PI_NAME": "work log"})
	op := runOp(nameSession(m, ""))
	if op.Err != nil || !strings.Contains(op.Notice, "work log") {
		t.Fatalf("/name with no arg = %+v, want pi's current name", op)
	}
	op = runOp(nameSession(m, "  new name  "))
	if op.Err != nil {
		t.Fatalf("/name <name>: %v", op.Err)
	}
	cmds := log()
	if len(cmds) != 2 || cmds[1]["type"] != "set_session_name" || cmds[1]["name"] != "new name" {
		t.Errorf("commands = %v, want set_session_name \"new name\"", cmds)
	}
	if !strings.Contains(op.Notice, "new name") {
		t.Errorf("notice = %q, want the name that was set", op.Notice)
	}
}

// /export goes through pi's export_html; a .jsonl target is refused with
// the reason instead of quietly writing something else.
func TestExportUsesPiAndRefusesJSONL(t *testing.T) {
	m, log := opsModel(t, nil)
	op := runOp(exportSession(m, ""))
	if op.Err != nil {
		t.Fatalf("export: %v", op.Err)
	}
	if !strings.Contains(op.Notice, "/tmp/pi-session.html") {
		t.Errorf("notice = %q, want the path pi reported", op.Notice)
	}
	if cmds := log(); len(cmds) != 1 || cmds[0]["type"] != "export_html" {
		t.Errorf("commands = %v, want one export_html", cmds)
	}
	if cmd := exportSession(m, "out.jsonl"); cmd != nil {
		t.Error("a .jsonl export has no RPC equivalent and must not be attempted")
	}
	if cmds := log(); len(cmds) != 1 {
		t.Errorf("commands = %v, the refused export must not reach pi", cmds)
	}
}
