package app

// live_owned_test.go — the invariants that keep a follower from touching the
// session it is following *around*.
//
// The bridge is one-way: a follow-mode window renders a foreign session
// read-only while the OWNED pi keeps running underneath it. Every test here
// pins one way that could go wrong, and each one drives the real production
// entry point (applyLive) rather than a helper, so a future call site that
// reintroduces the write cannot pass.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"

	"pitago/src/live"
	"pitago/src/pirpc"
)

// stdinTap returns a pirpc.Client whose child appends every byte pitago writes
// to its stdin into a log file, plus a reader for that log.
//
// It is the recording pipe src/pirpc's own fakepi harness uses (tests/fakepi
// via FAKEPI_LOG), spelled as a shell one-liner so this package's tests need no
// built binary. Everything pitago sends to a child is a JSONL line on this
// pipe, so "did the follower talk to the owned pi" is answerable exactly.
// Hermetic: the tap and its log live in t.TempDir(), and the child is killed
// by the Client's own Close in t.Cleanup.
func stdinTap(t *testing.T) (*pirpc.Client, func() string) {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "stdin.jsonl")
	script := filepath.Join(dir, "stdin-tap")
	// No operands: cat reads stdin and appends it to the log. The argv pi
	// passes (--mode rpc) is deliberately ignored.
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexec cat >>\"$PITAGO_TEST_STDIN_TAP\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PITAGO_TEST_STDIN_TAP", logPath)
	c, err := pirpc.Spawn(pirpc.Options{Bin: script, Dir: dir})
	if err != nil {
		t.Fatalf("spawn recording child: %v", err)
	}
	t.Cleanup(c.Close)
	read := func() string {
		raw, err := os.ReadFile(logPath)
		if err != nil {
			return "" // not created yet: nothing reached the wire
		}
		return string(raw)
	}
	return c, read
}

// waitForBytes polls until the tap has seen at least n bytes, so a negative
// assertion below can never pass just because the child was slow.
func waitForBytes(t *testing.T, read func() string, n int) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if got := read(); len(got) >= n {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("recording child never saw %d bytes of stdin (got %q)", n, read())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runCmds executes a returned command graph so its effects actually happen.
// Bubble Tea only reaches a client's stdin when a command RUNS, so the
// no-RPC invariant below is only testable by running what applyLive returned.
// Nested batches are expanded; every other message is appended to out. A
// command that has not returned by the deadline is abandoned rather than
// failed: the assertion is about bytes on the wire, not about timers.
func runCmds(cmd tea.Cmd, out *[]tea.Msg, deadline time.Time) {
	if cmd == nil || time.Now().After(deadline) {
		return
	}
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-msgCh:
	case <-time.After(time.Until(deadline)):
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			runCmds(sub, out, deadline)
		}
		return
	}
	*out = append(*out, msg)
}

// forwardedEvents is every record the bridge forwards (pi.on handlers in
// pitago-live-bridge.ts, plus the extension_ui_request the UI tap re-emits).
// agent_settled is included even though applyLive drops its command: the point
// of the list is that NOTHING it returns may reach the owned client.
func forwardedEvents() []string {
	return []string{
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"remote answer"}]}}`,
		`{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a.go"}}`,
		`{"type":"tool_execution_update","toolCallId":"t1","status":"running"}`,
		`{"type":"tool_execution_end","toolCallId":"t1","result":{"content":"ok","details":""}}`,
		`{"type":"tool_call","toolCallId":"t1","toolName":"read"}`,
		`{"type":"tool_result","toolCallId":"t1","result":{"content":"ok"}}`,
		`{"type":"agent_settled"}`,
		`{"type":"agent_end"}`,
		`{"type":"turn_end"}`,
		`{"type":"session_info_changed","name":"remote-named"}`,
		`{"type":"extension_ui_request","id":"r1","method":"notify","message":"[subagent-async] done","notifyType":"info"}`,
		`{"type":"extension_ui_request","id":"r2","method":"setStatus","statusKey":"worker","statusText":"running"}`,
		`{"type":"extension_ui_request","id":"r3","method":"setWidget","widgetKey":"worker","widgetLines":["worker-1 · running"],"widgetPlacement":"aboveEditor"}`,
	}
}

// The core follow-mode invariant: a forwarded event is observational, so not
// one byte may reach the OWNED pi's stdin. Only a comment used to say so —
// the guard was an omission nobody could test. The control line first proves
// the tap is live, so a green run means "nothing was sent", not "nothing was
// recorded".
func TestFollowModeForwardsNoCommandToOwnedClient(t *testing.T) {
	pi, read := stdinTap(t)
	m := New(pi, t.TempDir())
	if err := pi.Fire(pirpc.Command{Type: "control-probe"}); err != nil {
		t.Fatalf("control write: %v", err)
	}
	waitForBytes(t, read, 1)
	baseline := len(read())

	m.applyLive(m.liveGeneration, live.Message{Connected: true, Descriptor: live.Descriptor{SessionID: "remote"}})
	for i, raw := range forwardedEvents() {
		var msgs []tea.Msg
		cmd := m.applyLive(m.liveGeneration, live.Message{
			Event: &live.Event{Revision: int64(i + 1), Raw: json.RawMessage(raw)},
		})
		runCmds(cmd, &msgs, time.Now().Add(2*time.Second))
		if got := read(); len(got) != baseline {
			t.Fatalf("forwarded %s wrote %d byte(s) to the OWNED pi: %q", raw, len(got)-baseline, got[baseline:])
		}
	}
	if !m.followRemote {
		t.Fatal("the forwarded events did not attach follow mode")
	}
}

// Regression for the discarded petTickCmd (F1): tool_execution_start returns a
// pet command, and dropping it latched pet.ticking forever. The watchdog that
// used to paper over this was itself a bug (it could double-arm the loop), so
// the invariant is asserted the only way that survives a missing call site:
// the command applyLive returns must actually produce the tick.
func TestFollowModeDeliversPetTickCommand(t *testing.T) {
	pi, _ := stdinTap(t)
	m := New(pi, t.TempDir())
	m.applyLive(m.liveGeneration, live.Message{Connected: true, Descriptor: live.Descriptor{SessionID: "remote"}})
	cmd := m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(
		`{"type":"tool_execution_start","toolCallId":"t1","toolName":"read","args":{"path":"a.go"}}`,
	)}})
	var msgs []tea.Msg
	runCmds(cmd, &msgs, time.Now().Add(3*time.Second))
	for _, msg := range msgs {
		if _, ok := msg.(petTickMsg); ok {
			return
		}
	}
	t.Fatal("follow mode did not deliver the pet tick command: the tick loop latches forever")
}

// One forwarded event must cost one paint. handleEvent already Refresh()es
// (or coalesces through the stream frame); a second Refresh here gave every
// non-streaming remote event two uncached full repaints. Only agent_start and
// agent_settled may add one, because setRemoteRunning rewrites the status
// after handleEvent painted.
func TestForwardedEventPaintsOnce(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"streaming delta", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`},
		{"non-streaming message", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`},
		{"tool result", `{"type":"tool_execution_end","toolCallId":"t1","toolName":"read","result":"ok","isError":false}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New(nil, t.TempDir())
			m.ready = true
			m.winW, m.winH = 120, 40
			m.vp = viewport.New(100, 20)
			m.sideVp = viewport.New(sideInnerW, 10)
			paints := 0
			paintHook = func() { paints++ }
			t.Cleanup(func() { paintHook = nil })

			m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Revision: 1, Raw: json.RawMessage(tc.raw)}})
			if paints != 1 {
				t.Fatalf("forwarded %s painted %d times, want exactly 1", tc.name, paints)
			}
		})
	}
}

// Remote events write the owned turn-timing fields inline (turn_start stamps
// them; agent_settled arms pendSpeed while its queryStats is deliberately
// dropped in follow mode), and the tapped setStatus/setWidget traffic lands in
// the plugin registry. Detaching into the owned session with any of that left
// over hands the next owned turn a garbage speed baseline and shows plugin
// chrome for a session this window is no longer following.
func TestDetachLiveClearsRemoteTurnTimingAndPluginState(t *testing.T) {
	m := New(nil, t.TempDir())
	m.applyLive(m.liveGeneration, live.Message{Connected: true, Descriptor: live.Descriptor{SessionID: "remote"}})
	for _, raw := range []string{
		`{"type":"turn_start"}`,
		`{"type":"agent_settled"}`,
		`{"type":"extension_ui_request","id":"r1","method":"setStatus","statusKey":"worker","statusText":"running"}`,
		`{"type":"extension_ui_request","id":"r2","method":"setWidget","widgetKey":"worker","widgetLines":["worker-1 · running"]}`,
	} {
		m.applyLive(m.liveGeneration, live.Message{Event: &live.Event{Raw: json.RawMessage(raw)}})
	}
	if m.turnStart.IsZero() || !m.pendSpeed || m.extStatus["worker"] == "" || m.extWidget["worker"] == nil {
		t.Fatalf("precondition not reached: turnStart=%v pendSpeed=%v extStat=%q", !m.turnStart.IsZero(), m.pendSpeed, m.extStat)
	}

	m.detachLive()
	if !m.turnStart.IsZero() || m.turnOutBase != 0 || m.pendSpeed {
		t.Fatalf("detach kept remote turn timing: turnStart=%v turnOutBase=%d pendSpeed=%v",
			m.turnStart, m.turnOutBase, m.pendSpeed)
	}
	if len(m.extStatus) != 0 || len(m.extWidget) != 0 || m.extStat != "" {
		t.Fatalf("detach kept remote plugin registry: statuses=%v widgets=%v extStat=%q",
			m.extStatus, m.extWidget, m.extStat)
	}
	if m.followRemote {
		t.Fatal("detach did not leave follow mode")
	}
}
