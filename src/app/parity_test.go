package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/pirpc"
)

// fakePi writes a python stand-in for `pi --mode rpc` that logs every
// command it receives and answers from env switches: FAKE_PI_STEER_FAIL
// (a steer is refused), FAKE_PI_QUEUE (comma-separated steering/followUp
// returned by clear_queue), FAKE_PI_BUSY (pi is streaming, so a bare
// prompt is refused with pi's own wording), FAKE_PI_PROMPT_FAIL (any
// prompt is refused) and FAKE_PI_VETO (a session switch is cancelled by
// an extension). The prompt-path rules can then be asserted against real
// JSONL traffic.
const fakePi = `#!/usr/bin/env python3
import json, os, sys
log = open(os.environ["FAKE_PI_LOG"], "a", buffering=1)
queued = [q for q in os.environ.get("FAKE_PI_QUEUE", "").split(",") if q]
for line in sys.stdin:
    line = line.strip()
    if not line:
        continue
    cmd = json.loads(line)
    log.write(json.dumps(cmd) + "\n")
    cid, typ = cmd.get("id", ""), cmd.get("type", "")
    resp = {"id": cid, "type": "response", "command": typ, "success": True}
    if typ == "prompt" and cmd.get("streamingBehavior") == "steer" and os.environ.get("FAKE_PI_STEER_FAIL"):
        # pi never downgrades a steer to a plain prompt: it just fails.
        resp["success"], resp["error"] = False, "steer refused"
    if typ == "prompt" and not cmd.get("streamingBehavior") and os.environ.get("FAKE_PI_BUSY"):
        # pi's own refusal when a prompt arrives without streamingBehavior
        # while the agent is streaming (core/agent-session.js:1243-1246).
        resp["success"], resp["error"] = False, (
            "Agent is already processing. Specify streamingBehavior "
            "('steer' or 'followUp') to queue the message.")
    if typ == "prompt" and os.environ.get("FAKE_PI_PROMPT_FAIL"):
        resp["success"], resp["error"] = False, "prompt refused by pi"
    if typ == "clear_queue":
        resp["data"] = {"steering": queued[:1], "followUp": queued[1:]}
    if typ == "new_session" and os.environ.get("FAKE_PI_VETO"):
        resp["data"] = {"cancelled": True}
    if typ == "get_state":
        resp["data"] = {"model": {"id": "m", "provider": "p"},
                        "thinkingLevel": "off", "sessionName": "my session"}
    if typ == "bash":
        resp["data"] = {"output": "hello\n", "exitCode": 0}
    if typ == "abort_bash":
        resp["data"] = {}
    print(json.dumps(resp), flush=True)
`

// spawnFakePi starts the stand-in (env pre-set via t.Setenv) and returns the
// client plus a reader for the command log.
func spawnFakePi(t *testing.T, env map[string]string) (*pirpc.Client, func() []string) {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "fake-pi")
	logPath := filepath.Join(dir, "cmds.jsonl")
	if err := os.WriteFile(bin, []byte(fakePi), 0o755); err != nil {
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
	return pi, func() []string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		// The stand-in appends from another process, so a read can land
		// mid-line. Only whole lines are usable: a truncated tail would
		// otherwise reach the assertions as invalid JSON and fail a test
		// that is actually just racing the writer.
		s := string(raw)
		if s == "" {
			return nil
		}
		parts := strings.Split(strings.TrimSuffix(s, "\n"), "\n")
		if !strings.HasSuffix(s, "\n") {
			parts = parts[:len(parts)-1] // drop the partial last line
		}
		out := parts[:0]
		for _, p := range parts {
			if strings.TrimSpace(p) != "" {
				out = append(out, p)
			}
		}
		return out
	}
}

// Pi never falls back from steer to a plain prompt: a refused steer is
// reported to the user, not silently re-sent as a fresh turn.
func TestSteerFailureIsNotResentAsPrompt(t *testing.T) {
	pi, log := spawnFakePi(t, map[string]string{"FAKE_PI_STEER_FAIL": "1"})
	m := New(pi, t.TempDir())
	m.thinking = true

	msg, ok := m.sendCmd(true, "hold on", nil)().(sentAckMsg)
	if !ok {
		t.Fatal("sendCmd must report a sentAckMsg")
	}
	if msg.err == nil {
		t.Fatal("a refused steer must surface as an error")
	}
	prompts := 0
	for _, line := range log() {
		var c struct {
			Type              string `json:"type"`
			StreamingBehavior string `json:"streamingBehavior"`
		}
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatal(err)
		}
		if c.Type == "prompt" {
			prompts++
			if c.StreamingBehavior != "steer" {
				t.Errorf("prompt %q must stay a steer, got streamingBehavior %q",
					line, c.StreamingBehavior)
			}
		}
	}
	if prompts != 1 {
		t.Errorf("steer must be sent exactly once, got %d prompts:\n%s",
			prompts, strings.Join(log(), "\n"))
	}
}

// Esc while streaming must give the queued steer/follow-up text back to the
// editor (pi: restoreQueuedMessagesToEditor) instead of dropping it.
func TestEscAbortRestoresQueuedText(t *testing.T) {
	pi, _ := spawnFakePi(t, map[string]string{"FAKE_PI_QUEUE": "queued steer,queued follow-up"})
	m := New(pi, t.TempDir())
	m.thinking = true
	m.ta.SetValue("typed after the steer")

	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc}) // arms
	m = um.(Model)
	if !m.escArmed() {
		t.Fatal("first Esc must arm")
	}
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("second Esc must run the clear_queue + abort")
	}
	abort, ok := cmd().(escAbortMsg)
	if !ok {
		t.Fatal("Esc cancel must report escAbortMsg (queue + abort in one round trip)")
	}
	if abort.err != nil {
		t.Fatalf("abort: %v", abort.err)
	}
	um, _ = m.Update(abort)
	m = um.(Model)
	want := "queued steer\n\nqueued follow-up\n\ntyped after the steer"
	if got := m.ta.Value(); got != want {
		t.Errorf("editor after Esc = %q, want %q", got, want)
	}
}

// An Esc cancel with nothing queued leaves the editor untouched.
func TestEscAbortEmptyQueueKeepsEditor(t *testing.T) {
	pi, _ := spawnFakePi(t, nil)

	m := New(pi, t.TempDir())
	m.thinking = true
	m.ta.SetValue("keep me")
	um, _ := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	um, cmd := um.(Model).Update(tea.KeyMsg{Type: tea.KeyEsc})
	abort := cmd().(escAbortMsg)
	if len(abort.restored) != 0 {
		t.Fatalf("no queued messages expected, got %v", abort.restored)
	}
	um, _ = um.(Model).Update(abort)
	if got := um.(Model).ta.Value(); got != "keep me" {
		t.Errorf("editor = %q, want the typed text unchanged", got)
	}
}

// setModel adjusts the thinking level for the new model (pi parity), so the
// model-switch path re-reads get_state instead of keeping the old level.
func TestModelSwitchRefreshesThinkingLevel(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinkLvl = "high"
	um, cmd := m.Update(ModelCycleMsg{Label: "space-bunny-free", Provider: "opencode", ID: "space-bunny-free"})
	m = um.(Model)
	if cmd == nil {
		t.Fatal("a model switch must re-read pi state (thinking level follows the model)")
	}
	var st pirpc.State
	st.ThinkingLevel = "off"
	st.Model.ID = "x"
	um, _ = m.Update(stateRefreshMsg{state: st})
	if got := um.(Model).thinkLvl; got != "off" {
		t.Errorf("thinkLvl = %q, want the level pi reported after the switch", got)
	}
}

// Guard against a regression: the double-Esc window is still the pitago UX
// (one Esc never aborts).
func TestSingleEscDoesNotAbort(t *testing.T) {
	m := New(nil, t.TempDir())
	m.thinking = true
	um, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = um.(Model)
	if m.thinking != true {
		t.Error("a single Esc must not end the turn")
	}
	if cmd == nil {
		t.Fatal("a single Esc must return the disarm tick")
	}
	if m.escArmed() && time.Since(m.escArm) > quitArmWindow {
		t.Error("arm window must be the quit window")
	}
}
