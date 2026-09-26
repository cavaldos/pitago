package pirpc

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// pipeClient builds a Client with the fields readLoop needs, like Spawn
// minus the child process, so these tests need no real pi.
func pipeClient(t *testing.T) (*Client, *os.File, *os.File) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	t.Cleanup(func() { w.Close(); r.Close() })
	return &Client{pending: make(map[string]chan Response), done: make(chan struct{})}, r, w
}

// TestReadLoopNeverBlocksOnSlowUI is the regression test for the
// head-of-line stall: OnEvent stands in for tea.Program.Send on a busy
// UI, i.e. it blocks. The reader must keep draining pi's stdout anyway,
// so the writer never fills the pipe and a response sent *after* a flood
// of events is still routed to its pending Send.
func TestReadLoopNeverBlocksOnSlowUI(t *testing.T) {
	c, r, w := pipeClient(t)
	respCh := make(chan Response, 1)
	c.pending["go-1"] = respCh

	release := make(chan struct{})    // the UI thread becomes available
	entered := make(chan struct{}, 1) // OnEvent is blocked in here
	var mu sync.Mutex
	var delivered []Event
	c.SetOnEvent(func(e Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		mu.Lock()
		delivered = append(delivered, e)
		mu.Unlock()
	})
	go c.readLoop(r)

	const runs = 200
	var stream strings.Builder
	// Not mergeable, so the pump dispatches it at once and parks in the
	// blocking OnEvent while the reader fills the queue behind it.
	stream.WriteString(`{"type":"turn_start"}` + "\n")
	for i := 0; i < runs; i++ {
		for j := 0; j < 5; j++ {
			stream.WriteString(`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"ab"}}` + "\n")
		}
		// Also not mergeable: must survive verbatim and in order.
		fmt.Fprintf(&stream, `{"type":"tool_execution_update","toolName":"bash","step":%d}`+"\n", i)
	}
	// The response comes last: a reader stalled inside OnEvent never reads
	// it and Send times out. That is the bug under test.
	stream.WriteString(`{"type":"response","id":"go-1","command":"get_state","success":true,"data":{"ok":true}}` + "\n")

	written := make(chan error, 1)
	go func() {
		_, err := w.WriteString(stream.String())
		written <- err
	}()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("OnEvent never ran: the reader is not delivering queued events")
	}
	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("writer blocked: readLoop stalled on a slow UI, so pi's stdout pipe filled up")
	}
	select {
	case resp := <-respCh:
		if resp.ID != "go-1" || !resp.Success {
			t.Fatalf("response misrouted: %+v", resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("response never routed: readLoop was blocked inside OnEvent")
	}

	close(release)
	w.Close()
	waitPump(t, c)

	mu.Lock()
	got := append([]Event(nil), delivered...)
	mu.Unlock()

	// turn_start + 200 * (5 chunks merged into 1, plus the tool event).
	if len(got) != 1+2*runs {
		t.Fatalf("delivered %d events, want %d", len(got), 1+2*runs)
	}
	if got[0].Type != "turn_start" {
		t.Fatalf("first event type = %q, want turn_start", got[0].Type)
	}
	for i, ev := range got[1:] {
		if i%2 == 1 {
			if ev.Type != "tool_execution_update" {
				t.Fatalf("event %d: type = %q, want tool_execution_update", i, ev.Type)
			}
			want := fmt.Sprintf(`{"type":"tool_execution_update","toolName":"bash","step":%d}`, i/2)
			if string(ev.Raw) != want {
				t.Fatalf("event %d raw mutated:\n%s\nwant:\n%s", i, ev.Raw, want)
			}
			continue
		}
		d := deltaOf(t, ev)
		if d.Type != "text_delta" || d.ContentIndex != 0 {
			t.Fatalf("event %d: delta = %+v", i, d)
		}
		if want := strings.Repeat("ab", 5); d.Delta != want {
			t.Fatalf("event %d: merged delta = %q, want %q", i, d.Delta, want)
		}
	}
}

// TestEventPumpCoalescesOnlyStreamingDeltas pins the merge rule: only
// consecutive text/thinking chunks of the same content block collapse,
// and everything else is forwarded one-for-one in order.
func TestEventPumpCoalescesOnlyStreamingDeltas(t *testing.T) {
	c := &Client{}
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	got := make(chan Event, 32)
	c.SetOnEvent(func(e Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release // queue up the rest while the UI is busy
		got <- e
	})

	lines := []string{
		`{"type":"message_update","assistantMessageEvent":{"type":"text_start","contentIndex":0}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"he"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"ll"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"o"}}`,
		// Same delta type, other content block: must not bleed together.
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":1,"delta":"XX"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta","contentIndex":0,"delta":"hmm"}}`,
		// Structured deltas and other event types stay one-for-one.
		`{"type":"message_update","assistantMessageEvent":{"type":"toolcall_delta","contentIndex":0,"id":"t1","toolCall":{"id":"t1","name":"bash","arguments":"{\"a\":1}"}}}`,
		`{"type":"message_end","assistantMessageEvent":{"type":"done","reason":"stop"}}`,
	}
	for _, raw := range lines {
		var resp Response
		if err := json.Unmarshal([]byte(raw), &resp); err != nil {
			t.Fatalf("bad fixture %s: %v", raw, err)
		}
		c.enqueue(Event{Type: resp.Type, Raw: json.RawMessage(raw)})
	}
	<-entered
	close(release)
	c.finishReading() // flush the backlog, then let the pump exit
	waitPump(t, c)

	seq := drain(got)
	if len(seq) != 6 {
		t.Fatalf("delivered %d events, want 6: %s", len(seq), rawOf(seq))
	}
	wantTypes := []string{"message_update", "message_update", "message_update", "message_update", "message_update", "message_end"}
	wantDeltas := []string{"text_start", "text_delta", "text_delta", "thinking_delta", "toolcall_delta", "done"}
	wantText := []string{"", "hello", "XX", "hmm", "", ""}
	for i, ev := range seq {
		if ev.Type != wantTypes[i] {
			t.Fatalf("event %d type = %q, want %q", i, ev.Type, wantTypes[i])
		}
		d := deltaOf(t, ev)
		if d.Type != wantDeltas[i] {
			t.Fatalf("event %d delta type = %q, want %q", i, d.Type, wantDeltas[i])
		}
		if d.Delta != wantText[i] {
			t.Fatalf("event %d delta = %q, want %q", i, d.Delta, wantText[i])
		}
	}
	if d := deltaOf(t, seq[1]); d.ContentIndex != 0 {
		t.Fatalf("merged event lost contentIndex: %+v", d)
	}
	if d := deltaOf(t, seq[2]); d.ContentIndex != 1 {
		t.Fatalf("adjacent content blocks were merged: %+v", d)
	}
	if !strings.Contains(string(seq[5].Raw), `"reason":"stop"`) {
		t.Fatalf("message_end payload mutated: %s", seq[5].Raw)
	}
}

// TestCloseStopsEventPump: Close() must retire the pump, and late events
// must not be buffered for a UI that is already gone.
func TestCloseStopsEventPump(t *testing.T) {
	c := &Client{cmd: exec.Command("true")} // never started: Process is nil
	entered := make(chan struct{}, 1)
	c.SetOnEvent(func(Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
	})
	c.enqueue(Event{Type: "turn_start"})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pump never delivered the queued event")
	}

	c.Close()
	waitPump(t, c) // Close stops the pump

	c.enqueue(Event{Type: "turn_start"})
	c.evMu.Lock()
	backlog := len(c.evQueue)
	c.evMu.Unlock()
	if backlog != 0 {
		t.Fatalf("queue kept %d events after Close", backlog)
	}
}

// TestEventPumpKeepsMutexFree: the pump must never hold c.mu, otherwise a
// slow UI would also block Send/Fire.
func TestEventPumpKeepsMutexFree(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()

	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	c := &Client{stdin: w, pending: make(map[string]chan Response), done: make(chan struct{})}
	c.SetOnEvent(func(Event) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
	})
	c.enqueue(Event{Type: "turn_start"})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pump never reached OnEvent")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := c.Fire(Command{Type: "notify", Message: "still writable"}); err != nil {
			t.Errorf("Fire: %v", err)
		}
		c.nextID()
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("pump holds c.mu while OnEvent is blocked")
	}
	close(release)
	c.finishReading()
	waitPump(t, c)
}

// helpers

func deltaOf(t *testing.T, ev Event) Delta {
	t.Helper()
	var mu MessageUpdate
	if err := json.Unmarshal(ev.Raw, &mu); err != nil {
		t.Fatalf("parse %s: %v", ev.Raw, err)
	}
	return mu.Event
}

func drain(ch chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

func rawOf(events []Event) string {
	var sb strings.Builder
	for i, ev := range events {
		fmt.Fprintf(&sb, "\n  [%d] %s %s", i, ev.Type, ev.Raw)
	}
	return sb.String()
}

// waitPump blocks until the event pump goroutine has returned.
func waitPump(t *testing.T, c *Client) {
	t.Helper()
	select {
	case <-c.evExited:
	case <-time.After(5 * time.Second):
		t.Fatal("event pump did not exit")
	}
}
