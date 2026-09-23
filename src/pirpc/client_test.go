package pirpc

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func needPi(t *testing.T) {
	t.Helper()
	if bin := os.Getenv("PI_BIN"); bin != "" {
		return
	}
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("pi not in PATH (set PI_BIN to run RPC tests)")
	}
}

func TestRPCNoLLM(t *testing.T) {
	needPi(t)
	c, err := Spawn(Options{NoSession: true})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	defer c.Close()

	state, err := c.GetState()
	if err != nil && strings.Contains(err.Error(), "timed out") {
		state, err = c.GetState() // pi cold start under load: one retry
	}
	if err != nil {
		t.Fatalf("get_state: %v", err)
	}
	t.Logf("model=%s provider=%s session=%s", state.Model.ID, state.Model.Provider, state.SessionID)

	resp, err := c.Send(Command{Type: "get_available_models"}, 30*time.Second)
	if err != nil {
		t.Fatalf("get_available_models: %v", err)
	}
	t.Logf("models payload bytes=%d", len(resp.Data))

	resp, err = c.Send(Command{Type: "get_commands"}, 30*time.Second)
	if err != nil {
		t.Fatalf("get_commands: %v", err)
	}
	t.Logf("commands payload bytes=%d", len(resp.Data))

	stats, err := c.GetStats()
	if err != nil {
		t.Fatalf("get_session_stats: %v", err)
	}
	t.Logf("stats session=%s tools=%d", stats.SessionID, stats.ToolCalls)
}

// Intentional Close must not emit pi_exited (reconnects would fake a
// "pi has exited" if the old pi's death notice lands after respawnMsg);
// an un-closed death still reports it.
func TestPiExitedSuppressedOnClose(t *testing.T) {
	fake := t.TempDir() + "/slowexit.sh"
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 0.5\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 4)
	// control: unexpected death reports pi_exited
	c, err := Spawn(Options{Bin: fake})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	c.OnEvent = func(e Event) { got <- e.Type }
	select {
	case typ := <-got:
		if typ != "pi_exited" {
			t.Fatalf("unexpected event %q", typ)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expected pi_exited for unexpected death")
	}
	// intentional close: no event
	c2, err := Spawn(Options{Bin: fake})
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	c2.OnEvent = func(e Event) { got <- e.Type }
	c2.Close()
	select {
	case typ := <-got:
		t.Fatalf("closed client must stay silent, got %q", typ)
	case <-time.After(2 * time.Second):
	}
}

// readLoop routes with one parse per line: responses resolve the pending
// Send, events reach OnEvent with their raw bytes intact.
func TestReadLoopRoutesResponseAndEvent(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	c := &Client{pending: make(map[string]chan Response), done: make(chan struct{})}
	respCh := make(chan Response, 1)
	c.pending["go-1"] = respCh
	events := make(chan Event, 4)
	c.OnEvent = func(e Event) { events <- e }
	go c.readLoop(r)

	respLine := `{"type":"response","id":"go-1","command":"get_state","success":true,"data":{"a":1}}`
	evLine := `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"hi"}}`
	if _, err := w.WriteString(respLine + "\n" + evLine + "\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	select {
	case resp := <-respCh:
		if resp.ID != "go-1" || !resp.Success {
			t.Fatalf("response misrouted: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("response never resolved pending Send")
	}
	select {
	case ev := <-events:
		if ev.Type != "message_update" {
			t.Fatalf("event type = %q", ev.Type)
		}
		if string(ev.Raw) != evLine {
			t.Fatalf("event raw mutated:\n%q\nwant:\n%q", ev.Raw, evLine)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("event never reached OnEvent")
	}
	w.Close()
}
