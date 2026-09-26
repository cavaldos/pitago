package live

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pitago/src/pirpc"
)

// tailPoll is the re-read interval every tail test runs at: small enough to
// keep assertions fast, large enough that "nothing happened yet" assertions
// are not racy.
const tailPoll = 5 * time.Millisecond

// tailRecorder collects the Message stream a Tail emits, exactly as the app's
// Bubble Tea loop would receive it.
type tailRecorder struct {
	tail *Tail
	msgs chan Message
	done chan struct{}
}

// startTail follows a synthetic session file and returns the recorder. The
// tail is stopped on cleanup, so no test leaks the follow goroutine.
func startTail(t *testing.T, path, sessionID, cwd string) *tailRecorder {
	t.Helper()
	r := &tailRecorder{msgs: make(chan Message, 256), done: make(chan struct{})}
	r.tail = NewTail(SessionFile{Path: path, SessionID: sessionID, CWD: cwd, ModTime: time.Now()})
	r.tail.Poll = tailPoll
	cmd := r.tail.Start(context.Background(), func(m Message) {
		select {
		case r.msgs <- m:
		default:
		}
	})
	go func() {
		cmd()
		close(r.done)
	}()
	t.Cleanup(r.tail.Stop)
	return r
}

// await waits for the first message matching pred, failing on a deadline.
func (r *tailRecorder) await(t *testing.T, what string, pred func(Message) bool) Message {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-r.msgs:
			if pred(m) {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// quiet asserts nothing arrives for d, which is how "must not be emitted yet"
// is checked without sleeping a fixed time and hoping.
func (r *tailRecorder) quiet(t *testing.T, what string, d time.Duration) {
	t.Helper()
	select {
	case m := <-r.msgs:
		t.Fatalf("%s arrived early: %+v", what, m)
	case <-time.After(d):
	}
}

// append writes raw bytes to the session file, newline included or not, the
// way pi appends one entry per line.
func appendTo(t *testing.T, path, raw string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(raw); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// liveSession writes a session file whose first three entries are already a
// short conversation, and returns its path.
func liveSession(t *testing.T, cwd string) string {
	t.Helper()
	dir := useSessionDir(t)
	path := filepath.Join(dir, "2026-01-02T10-00-00_s.jsonl")
	lines := []string{
		sessionHeader(cwd, "sess-file"),
		`{"type":"model_change","provider":"anthropic","modelId":"claude-x","timestamp":"2026-01-02T10:00:00.000Z"}`,
		`{"type":"thinking_level_change","thinkingLevel":"high","timestamp":"2026-01-02T10:00:00.000Z"}`,
		`{"type":"session_info","name":"lane b","timestamp":"2026-01-02T10:00:00.000Z"}`,
		`{"type":"message","id":"m0","parentId":null,"timestamp":"2026-01-02T10:00:01.000Z","message":{"role":"system","content":"preamble"}}`,
		`{"type":"message","id":"m1","parentId":"m0","timestamp":"2026-01-02T10:00:02.000Z","message":{"role":"user","content":"hi"}}`,
		`{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-01-02T10:00:03.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}`,
	}
	writeSessionFile(t, dir, filepath.Base(path), lines...)
	return path
}

// assistantEntry is one appended assistant message with parent m2.
func assistantEntry(id, text string) string {
	raw, _ := json.Marshal(map[string]any{
		"type":      "message",
		"id":        id,
		"parentId":  "m2",
		"timestamp": "2026-01-02T10:00:04.000Z",
		"message":   map[string]any{"role": "assistant", "content": []map[string]string{{"type": "text", "text": text}}},
	})
	return string(raw)
}

// messageText pulls the assistant text out of a rendered transcript row, the
// same way the app reads a message_end payload.
func messageText(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var msg pirpc.AgentMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("row is not an agent message: %s", raw)
	}
	return pirpc.TextOf(msg.Content)
}

// The attach handshake must be the bridge's: Connected, then a full snapshot
// in active-branch order with the system preamble dropped, so the app renders
// this session exactly as it renders a bridged one.
func TestTailEmitsConnectedThenFullSnapshot(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)

	conn := r.await(t, "Connected", func(m Message) bool { return m.Connected })
	if conn.Descriptor.SessionID != "sess-file" || !sameDir(conn.Descriptor.CWD, cwd) {
		t.Fatalf("connected descriptor wrong: %+v", conn.Descriptor)
	}
	snap := r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil }).Snapshot
	if snap.SessionID != "sess-file" || snap.SessionName != "lane b" || snap.Model != "claude-x" {
		t.Fatalf("snapshot header fields wrong: %+v", snap)
	}
	if snap.ThinkingLevel != "high" || snap.LeafID != "m2" {
		t.Fatalf("snapshot tail fields wrong: %+v", snap)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("expected the 2 branch messages, got %d: %s", len(snap.Messages), snap.Messages)
	}
	if got := messageText(t, snap.Messages[0]); got != "hi" {
		t.Fatalf("first branch row = %q, want the user message", got)
	}
	if got := messageText(t, snap.Messages[1]); got != "hello" {
		t.Fatalf("second branch row = %q, want the assistant message", got)
	}
}

// Each completed entry becomes the bridge's message_end event, so the app's
// existing handleEvent renders it with no new code, and revisions only ever
// move forward.
func TestTailEmitsMessageEndEventsForAppendedMessages(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)
	snap := r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil })
	last := snap.Event // nil: nothing incremental before the snapshot

	appendTo(t, path, assistantEntry("m3", "first")+"\n")
	appendTo(t, path, assistantEntry("m4", "second")+"\n")

	seen := 0
	for seen < 2 {
		m := r.await(t, "message_end event", func(m Message) bool { return m.Event != nil })
		var ev struct {
			Type    string          `json:"type"`
			Message json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(m.Event.Raw, &ev); err != nil {
			t.Fatalf("event is not JSON: %s", m.Event.Raw)
		}
		if ev.Type != "message_end" {
			t.Fatalf("event type = %q, want message_end", ev.Type)
		}
		want := []string{"first", "second"}[seen]
		if got := messageText(t, ev.Message); got != want {
			t.Fatalf("rendered text = %q, want %q", got, want)
		}
		if last != nil && m.Event.Revision <= last.Revision {
			t.Fatalf("revision %d did not increase past %d", m.Event.Revision, last.Revision)
		}
		last = m.Event
		seen++
	}
}

// pi writes a line in more than one write often enough that emitting half a
// record would show a truncated message: a trailing line without its newline
// must stay invisible until the newline lands.
func TestTailDoesNotEmitHalfWrittenLineUntilNewlineArrives(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)
	r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil })

	full := assistantEntry("m3", "complete")
	appendTo(t, path, full[:len(full)/2])
	r.quiet(t, "a half-written entry", 20*tailPoll)
	appendTo(t, path, full[len(full)/2:]+"\n")

	m := r.await(t, "the completed entry", func(m Message) bool { return m.Event != nil })
	var ev struct {
		Message json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(m.Event.Raw, &ev); err != nil {
		t.Fatal(err)
	}
	if got := messageText(t, ev.Message); got != "complete" {
		t.Fatalf("rendered text = %q, want the completed entry", got)
	}
}

// A file that shrank was truncated or rotated, so the transcript we already
// rendered no longer exists: the only honest repair is a fresh snapshot.
func TestTailResnapshotsWhenFileShrinks(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)
	r.await(t, "first snapshot", func(m Message) bool { return m.Snapshot != nil })

	writeSessionFile(t, filepath.Dir(path), filepath.Base(path),
		sessionHeader(cwd, "sess-file"),
		`{"type":"message","id":"n1","parentId":null,"message":{"role":"user","content":"only line"}}`)

	snap := r.await(t, "re-snapshot", func(m Message) bool { return m.Snapshot != nil }).Snapshot
	if len(snap.Messages) != 1 || messageText(t, snap.Messages[0]) != "only line" {
		t.Fatalf("re-snapshot did not replace the transcript: %s", snap.Messages)
	}
}

// A vanished file is the ordinary end of a session: report it once and stop
// rather than spinning on a missing path.
func TestTailDisconnectsWhenFileVanishes(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)
	r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil })
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m := r.await(t, "Disconnected", func(m Message) bool { return m.Disconnected })
	if m.Err != nil {
		t.Fatalf("a removed session file is not an error: %v", m.Err)
	}
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("tail kept polling a vanished file")
	}
	r.quiet(t, "another Disconnected", 20*tailPoll)
}

// Stop must end the follow: no further messages, and the follow goroutine is
// gone before Stop returns, so a detached window cannot keep writing into a
// newer attach.
func TestTailStopEndsEmission(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-file", cwd)
	r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil })

	r.tail.Stop()
	select {
	case <-r.done:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not end the follow goroutine")
	}
	appendTo(t, path, assistantEntry("m3", "after stop")+"\n")
	r.quiet(t, "an event after Stop", 20*tailPoll)
}

// The app needs a usable SessionFile to build a Tail from: the picker hands
// the very same value back, so the two must agree on the path.
func TestActiveSessionsRoundTripsIntoATail(t *testing.T) {
	dir := useSessionDir(t)
	cwd := t.TempDir()
	path := liveSessionIn(t, dir, cwd)
	files := ActiveSessions(cwd, time.Hour)
	if len(files) != 1 || files[0].Path != path {
		t.Fatalf("ActiveSessions did not report the live session: %#v", files)
	}
	r := startTail(t, files[0].Path, files[0].SessionID, files[0].CWD)
	snap := r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil }).Snapshot
	if snap.SessionID != "sess-file" {
		t.Fatalf("tail from a listed session file has no session id: %+v", snap)
	}
}

// liveSessionIn is liveSession with an explicit session dir, so two tests can
// each own their own.
func liveSessionIn(t *testing.T, dir, cwd string) string {
	t.Helper()
	name := filepath.Join(dir, "2026-01-02T10-00-00_s.jsonl")
	writeSessionFile(t, dir, filepath.Base(name),
		sessionHeader(cwd, "sess-file"),
		fmt.Sprintf(`{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"hi"}}`))
	return name
}

// A team-state record is not a transcript row, but it is the only durable
// trace that workers exist. The tail must announce it so the app can rebuild
// the roster from the file; a half-written line must not trigger it.
func TestTailEmitsTeamStateEventForAppendedWorkerRecord(t *testing.T) {
	cwd := t.TempDir()
	path := liveSession(t, cwd)
	r := startTail(t, path, "sess-team", cwd)
	r.await(t, "snapshot", func(m Message) bool { return m.Snapshot != nil })

	record := `{"type":"custom","customType":"pi-agent-team/state","id":"r1","parentId":null,` +
		`"timestamp":"2026-01-01T00:00:01.000Z","data":{"version":2,"kind":"worker_terminal",` +
		`"recordId":"terminal:1","worker":{"workerId":"w1","profileName":"reviewer","status":"exited",` +
		`"startedAt":1,"lastEventAt":2,"usage":{"turns":3}}}}`

	// Half a line first: the tail must stay quiet until the newline arrives.
	appendTo(t, path, record[:40])
	appendTo(t, path, record[40:]+"\n")

	m := r.await(t, "team_state event", func(m Message) bool {
		return m.Event != nil && strings.Contains(string(m.Event.Raw), "team_state")
	})
	if m.Event.Revision <= 0 {
		t.Fatalf("team_state event has no revision: %d", m.Event.Revision)
	}
}
